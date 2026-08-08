package bot

import (
	"fmt"
	"html"
	"log/slog"
	"sync"
	"time"

	"github.com/fus1ond/vpn_bot/internal/database"
	"github.com/fus1ond/vpn_bot/internal/moynalog"
	"github.com/fus1ond/vpn_bot/internal/paymentprovider"
)

const (
	// notificationReceiptsSummary — маркер суточной сводки по непробитым чекам.
	notificationReceiptsSummary = "receipts_summary"
	// notificationReceiptsDisabled — разовое предупреждение о выключенной интеграции.
	notificationReceiptsDisabled = "receipts_integration_disabled"
	// notificationReceiptStuckPrefix — префикс маркера сообщения о застрявшем чеке.
	notificationReceiptStuckPrefix = "receipt_stuck:"
	// notificationReceiptRejectedPrefix — префикс маркера сообщения об отвергнутом чеке.
	notificationReceiptRejectedPrefix = "receipt_rejected:"

	// stuckReceiptAge — после этого срока непробитый чек считается застрявшим.
	stuckReceiptAge = 24 * time.Hour
	// receiptsSummaryInterval — как часто напоминать о непробитых чеках.
	receiptsSummaryInterval = 24 * time.Hour
	// receiptsPassBudget — сколько времени шаг чеков вправе занять у прохода
	// планировщика. Проход повторяется каждые 30 минут, так что остаток очереди
	// ждёт недолго.
	receiptsPassBudget = 5 * time.Minute
)

// receiptMu — мьютексы по платежу. Кодовых путей два (оплата и плановый проход),
// и без сериализации они могут одновременно увидеть незавершённый чек и пробить
// его дважды. Берём мьютекс без ожидания: занят — значит по этому платежу уже
// идёт работа, и второй попытке делать нечего.
//
// Записи намеренно не удаляются. Удаление после терминального состояния сэкономило
// бы десятки байт на платёж (пара сотен платежей в месяц), но вернуло бы окно, в
// котором два пути берут каждый свой мьютекс на один платёж, — то есть ровно тот
// дубль чека, ради которого мьютекс и заведён.
var receiptMu sync.Map // payment_id -> *sync.Mutex

func getReceiptMutex(paymentID int64) *sync.Mutex {
	mu, _ := receiptMu.LoadOrStore(paymentID, &sync.Mutex{})
	return mu.(*sync.Mutex)
}

// paidPaymentStatuses — статусы, при которых деньги уже получены. Чек обязателен
// по каждому из них: судьба активации подписки к налоговой отношения не имеет.
var paidPaymentStatuses = map[string]bool{
	"confirmed":                            true,
	"confirmed_not_activated":              true,
	paymentStatusConfirmedActivationFailed: true,
}

// moynalogEnabled сообщает, включена ли интеграция с кабинетом «Мой налог».
func (b *Bot) moynalogEnabled() bool { return b.moynalog != nil }

// issueReceiptAsync пробивает чек по платежу вне ответа вебхуку и вне мьютекса
// платежа: недоступность ФНС не должна ни задерживать ответ провайдеру, ни
// превращаться в неоплаченную услугу у человека, который деньги уже отдал.
func (b *Bot) issueReceiptAsync(paymentID int64) {
	if !b.moynalogEnabled() {
		return
	}
	if !b.beginReceipt() {
		slog.Warn("Бот останавливается, чек добьём следующим проходом", "payment_id", paymentID)
		return
	}
	go func() {
		defer b.receiptsInFlight.Done()
		defer func() {
			if r := recover(); r != nil {
				slog.Error("Пробитие чека упало с паникой", "payment_id", paymentID, "recover", r)
			}
		}()
		b.processReceipt(paymentID)
	}()
}

// beginReceipt регистрирует начатое пробитие. false означает, что бот уже
// останавливается и новое пробитие начинать нельзя.
//
// Замок нужен именно здесь: без него вебхук, пришедший в момент остановки, успел бы
// сделать Add уже после того, как Wait досчитал до нуля, — учебная гонка sync.WaitGroup.
func (b *Bot) beginReceipt() bool {
	b.receiptsStopMu.RLock()
	defer b.receiptsStopMu.RUnlock()
	if b.receiptsStopped {
		return false
	}
	b.receiptsInFlight.Add(1)
	return true
}

// runReceipt пробивает чек синхронно, но под тем же счётчиком: плановый проход
// останавливается наравне с вебхуком, иначе Stop не дождался бы его пробитий.
func (b *Bot) runReceipt(paymentID int64) bool {
	if !b.beginReceipt() {
		return false
	}
	defer b.receiptsInFlight.Done()
	b.processReceipt(paymentID)
	return true
}

// stopReceipts закрывает приём новых пробитий. Вызывается перед waitReceipts.
func (b *Bot) stopReceipts() {
	b.receiptsStopMu.Lock()
	b.receiptsStopped = true
	b.receiptsStopMu.Unlock()
}

// waitReceipts дожидается завершения запущенных пробитий (нужно тестам и Stop).
func (b *Bot) waitReceipts() { b.receiptsInFlight.Wait() }

// processReceipt доводит чек по платежу до состояния created: застолбить право,
// создать чек, сохранить uuid. Одна попытка — повторы делает плановый проход.
func (b *Bot) processReceipt(paymentID int64) {
	if !b.moynalogEnabled() {
		return
	}

	mu := getReceiptMutex(paymentID)
	if !mu.TryLock() {
		return
	}
	defer mu.Unlock()

	payment, err := b.db.GetPaymentByID(paymentID)
	if err != nil {
		slog.Error("Не удалось загрузить платёж для пробития чека", "error", err, "payment_id", paymentID)
		return
	}
	if payment == nil {
		return
	}
	// Платежи Platega игнорируются молча: это осознанное решение владельца,
	// а не упущение, и шуметь про него в логе незачем.
	if payment.Provider != paymentprovider.YooKassa {
		return
	}
	if payment.ConfirmedAt == nil || !paidPaymentStatuses[payment.Status] {
		return
	}

	receipt, err := b.claimReceipt(payment)
	if err != nil {
		slog.Error("Не удалось застолбить чек", "error", err, "payment_id", paymentID)
		return
	}
	if receipt == nil {
		return
	}

	switch receipt.State {
	case database.ReceiptStateCreated, database.ReceiptStateCanceled:
		return
	case database.ReceiptStateUnknown:
		b.reconcileReceipt(payment, receipt)
	default:
		b.createReceipt(payment, receipt)
	}
}

// issuePendingReceipts добивает чеки, не пробитые на платёжном пути. Повторы
// бесконечные: чек обязателен, и «сдаться» — не вариант.
//
// Свои сбои шаг держит при себе: паника здесь не должна срывать уведомления,
// отключения и автокики остального прохода.
func (b *Bot) issuePendingReceipts() {
	defer func() {
		if r := recover(); r != nil {
			slog.Error("Шаг пробития чеков упал с паникой", "recover", r)
		}
	}()

	pending, err := b.db.PaymentsNeedingReceipt()
	if err != nil {
		slog.Error("Scheduler: не удалось получить платежи без чеков", "error", err)
		return
	}
	if len(pending) == 0 {
		return
	}

	if !b.moynalogEnabled() {
		b.warnReceiptsIntegrationDisabled(len(pending))
		return
	}

	slog.Info("Scheduler: добиваем чеки", "count", len(pending))
	// Проход идёт последовательно, а поход в ФНС стоит до минуты на платёж. Без
	// потолка накопившаяся очередь при недоступной ФНС растянула бы шаг на часы и
	// съела бы тики планировщика целиком. Что не успели — доберём следующим проходом:
	// чек никуда не денется, а вот пропущенные отключения и автокики заметны сразу.
	deadline := time.Now().Add(receiptsPassBudget)
	b.receiptAuthBlocked.Store(false)
	for i, item := range pending {
		if b.receiptAuthBlocked.Load() {
			slog.Error("Scheduler: проход по чекам остановлен — кабинет не принял вход",
				"processed", i, "left", len(pending)-i)
			break
		}
		if time.Now().After(deadline) {
			slog.Warn("Scheduler: бюджет времени на чеки исчерпан, остальные добьём следующим проходом",
				"processed", i, "left", len(pending)-i)
			break
		}
		if !b.runReceipt(item.PaymentID) {
			slog.Info("Scheduler: бот останавливается, проход по чекам прерван", "processed", i, "left", len(pending)-i)
			break
		}
	}

	// Что осталось после прохода — то и есть проблема владельца.
	remaining, err := b.db.PaymentsNeedingReceipt()
	if err != nil {
		slog.Error("Scheduler: не удалось пересчитать непробитые чеки", "error", err)
		return
	}
	now := time.Now().UTC()
	b.reportStuckReceipts(remaining, now)
	b.reportReceiptsSummary(remaining, now)
}

// warnReceiptsIntegrationDisabled предупреждает о выключенной интеграции при
// наличии платежей без чеков. Молчаливое отключение здесь опаснее, чем у оплаты:
// пропавшую кнопку видно сразу, а непробитые чеки не видно месяцами.
func (b *Bot) warnReceiptsIntegrationDisabled(count int) {
	slog.Warn("Интеграция с «Мой налог» выключена, а платежи без чеков есть", "count", count)

	b.alertOwnerOnce(notificationReceiptsDisabled, fmt.Sprintf(
		"⚠️ Интеграция с «Мой налог» выключена, а подтверждённых платежей без чеков — %d.\n\n"+
			"Задайте MOYNALOG_INN и MOYNALOG_PASSWORD, иначе чеки так и не появятся.", count,
	))
}

// reportStuckReceipts сообщает о чеках, застрявших дольше суток: разовый сбой
// добивается сам, а вот застрявший случай теряться не должен.
func (b *Bot) reportStuckReceipts(pending []database.PendingReceipt, now time.Time) {
	for _, item := range pending {
		if now.Sub(item.ConfirmedAt) < stuckReceiptAge {
			continue
		}
		lastError := "без пояснения"
		if item.Receipt != nil && item.Receipt.LastError != nil {
			lastError = *item.Receipt.LastError
		}
		// Маркер живёт в базе, а не в памяти: застрявший чек живёт неделями и
		// переживает перезапуски бота — сообщение о нём повторяться не должно.
		b.alertOwnerOnce(fmt.Sprintf("%s%d", notificationReceiptStuckPrefix, item.PaymentID), fmt.Sprintf(
			"⏳ Чек по платежу #%d на %d ₽ не пробит больше суток.\n\nПлатёж подтверждён %s, последняя ошибка: %s",
			item.PaymentID, item.Amount, moynalog.MoscowDate(item.ConfirmedAt), html.EscapeString(lastError),
		))
	}
}

// reportReceiptsSummary шлёт сводку раз в сутки, пока есть непробитые чеки.
// Когда непробитых нет, сообщений нет вообще: ежедневное «всё хорошо» перестаёт
// читаться через неделю.
func (b *Bot) reportReceiptsSummary(pending []database.PendingReceipt, now time.Time) {
	if len(pending) == 0 {
		return
	}

	sentAt, err := b.db.NotificationSentAt(b.config.AdminID, notificationReceiptsSummary)
	if err != nil {
		slog.Error("Не удалось прочитать время последней сводки по чекам", "error", err)
		return
	}
	if sentAt != nil && now.Sub(sentAt.UTC()) < receiptsSummaryInterval {
		return
	}

	oldest := pending[0].ConfirmedAt // выборка отсортирована по времени подтверждения
	for _, item := range pending {
		if item.ConfirmedAt.Before(oldest) {
			oldest = item.ConfirmedAt
		}
	}

	if err := b.sendSchedulerMessage(b.config.AdminID, fmt.Sprintf(
		"🧾 Не пробито чеков: %d. Самый старый платёж — от %s.",
		len(pending), moynalog.MoscowDate(oldest),
	)); err != nil {
		logSchedulerSendError(notificationReceiptsSummary, b.config.AdminID, err)
		return
	}
	if err := b.db.MarkNotificationSent(b.config.AdminID, notificationReceiptsSummary); err != nil {
		slog.Error("Не удалось сохранить маркер сводки по чекам", "error", err)
	}
}

// claimReceipt застолбляет право на пробитие и возвращает актуальное состояние
// чека. Проигранное застолбление не ошибка: значит, запись уже есть.
func (b *Bot) claimReceipt(payment *database.Payment) (*database.Receipt, error) {
	marker, err := database.NewReceiptMarker()
	if err != nil {
		return nil, err
	}
	if _, err := b.db.ClaimReceipt(payment.ID, marker, *payment.ConfirmedAt, payment.Amount); err != nil {
		return nil, err
	}
	return b.db.GetReceipt(payment.ID)
}

// createReceipt регистрирует чек в кабинете ФНС.
func (b *Bot) createReceipt(payment *database.Payment, receipt *database.Receipt) {
	uuid, err := b.moynalog.CreateIncome(moynalog.IncomeRequest{
		Name: receiptServiceName(b.config.MoynalogServiceName, receipt.Marker),
		// Сумма чека — полная сумма платежа: комиссия провайдера налоговую базу
		// при НПД не уменьшает.
		Amount:        float64(payment.Amount),
		OperationTime: receipt.OperationTime,
	})
	if err != nil {
		b.recordReceiptFailure(payment, receipt, err, database.ReceiptStatePending)
		return
	}

	if err := b.db.MarkReceiptCreated(payment.ID, uuid); err != nil {
		// Чек в кабинете есть, а в базе об этом ни следа. Оставить состояние pending —
		// значит позвать плановый проход пробить второй чек: ровно тот дубль, ради
		// защиты от которого вся таблица и заведена. Уводим в unknown: сверка по метке
		// найдёт наш чек и запишет его uuid.
		slog.Error("Чек пробит, но состояние не сохранилось", "error", err, "payment_id", payment.ID, "receipt_uuid", uuid)
		if saveErr := b.db.MarkReceiptFailed(payment.ID, database.ReceiptStateUnknown, "чек пробит, но uuid не сохранился: "+err.Error()); saveErr != nil {
			slog.Error("Не удалось увести чек в состояние unknown", "error", saveErr, "payment_id", payment.ID, "receipt_uuid", uuid)
		}
		return
	}
	slog.Info("Чек пробит в «Мой налог»",
		"payment_id", payment.ID,
		"receipt_uuid", uuid,
		"operation_date", moynalog.MoscowDate(receipt.OperationTime),
	)
}

// reconcileWindow — сутки вокруг времени операции: в этом окне ищем свой чек.
const reconcileWindow = 24 * time.Hour

// reconcileReceipt выясняет судьбу чека, ответ по которому потерялся. Искать
// приходится по метке, а не по uuid: uuid придумывает ФНС и присылает в ответе,
// а в единственном опасном сценарии этот ответ как раз и потерян.
func (b *Bot) reconcileReceipt(payment *database.Payment, receipt *database.Receipt) {
	if receipt.Marker == "" {
		// Ручные чеки пробиты по старому формату наименования — сверять нечем.
		slog.Warn("Чек без метки в состоянии unknown, сверка невозможна", "payment_id", payment.ID)
		return
	}

	incomes, err := b.moynalog.ListIncomes(
		receipt.OperationTime.Add(-reconcileWindow),
		receipt.OperationTime.Add(reconcileWindow),
	)
	if err != nil {
		// Состояние остаётся unknown: пробить вслепую — значит рискнуть дублем.
		b.recordReceiptFailure(payment, receipt, err, database.ReceiptStateUnknown)
		return
	}

	var matches []moynalog.Income
	for _, income := range incomes {
		if income.Matches(receipt.Marker) {
			matches = append(matches, income)
		}
	}

	switch len(matches) {
	case 1:
		b.saveReconciledReceipt(payment, matches[0])
	case 0:
		slog.Info("Сверка чек не нашла, пробиваем заново", "payment_id", payment.ID, "marker", receipt.Marker)
		b.createReceipt(payment, receipt)
	default:
		// Автоматика, угадывающая в неоднозначной ситуации, дороже разбора руками
		// раз в год: состояние не меняем.
		slog.Error("Сверка нашла несколько чеков по метке", "payment_id", payment.ID, "marker", receipt.Marker, "count", len(matches))
		b.alertReceiptOnce(fmt.Sprintf("ambiguous:%d", payment.ID), fmt.Sprintf(
			"⚠️ По платежу #%d в «Мой налог» нашлось %d чеков с меткой <code>%s</code>.\n\n"+
				"Бот ничего не менял — разберитесь в кабинете, какой чек лишний.",
			payment.ID, len(matches), html.EscapeString(receipt.Marker),
		))
	}
}

// saveReconciledReceipt фиксирует найденный сверкой чек.
func (b *Bot) saveReconciledReceipt(payment *database.Payment, income moynalog.Income) {
	if income.Canceled {
		// Чек нашёлся, но он аннулирован. Записываем как есть: второй чек по этому
		// платежу всё равно не наш случай, а состояние должно отражать кабинет.
		if err := b.db.MarkReceiptCanceled(payment.ID); err != nil {
			slog.Error("Не удалось сохранить результат сверки", "error", err, "payment_id", payment.ID)
		}
		slog.Warn("Сверка нашла аннулированный чек", "payment_id", payment.ID, "receipt_uuid", income.ApprovedReceiptUUID)
		return
	}
	if err := b.db.MarkReceiptCreated(payment.ID, income.ApprovedReceiptUUID); err != nil {
		slog.Error("Не удалось сохранить результат сверки", "error", err, "payment_id", payment.ID)
		return
	}
	slog.Info("Сверка нашла чек по метке", "payment_id", payment.ID, "receipt_uuid", income.ApprovedReceiptUUID)
}

// recordReceiptFailure сохраняет неудачу и решает, будить ли владельца.
// Чинится само (сеть, перегрузка ФНС) — молчим, повторит плановый проход;
// само не починится (пароль, неверный запрос) — сообщаем сразу.
//
// defaultState — состояние, в котором чек остаётся после обычной ошибки. Со сверки
// нельзя вернуться в pending: это означало бы «пробей заново» при неизвестной судьбе.
func (b *Bot) recordReceiptFailure(payment *database.Payment, receipt *database.Receipt, err error, defaultState string) {
	kind := moynalog.ErrorKind(err)
	message := moynalog.ErrorMessage(err)

	state := defaultState
	switch kind {
	case moynalog.KindUnknown:
		// Ответ потерян: неизвестно, создан чек или нет. Разрешит сверка по метке.
		state = database.ReceiptStateUnknown
	case moynalog.KindBadRequest:
		// Тот же запрос через полчаса отвергнут будет ровно так же. Состояние
		// терминальное: плановый проход его больше не берёт, разбор — руками.
		// Со сверки в rejected не уводим: там неизвестна судьба уже созданного чека,
		// и закрыть его как отвергнутый — потерять чек из виду.
		if defaultState == database.ReceiptStatePending {
			state = database.ReceiptStateRejected
		}
	}
	if saveErr := b.db.MarkReceiptFailed(payment.ID, state, message); saveErr != nil {
		slog.Error("Не удалось сохранить неудачную попытку пробития", "error", saveErr, "payment_id", payment.ID)
	}

	switch kind {
	case moynalog.KindAuth:
		slog.Error("Кабинет «Мой налог» не принял учётные данные", "error", err, "payment_id", payment.ID)
		// Пароль не платёжеспецифичен: продолжать проход — значит на каждый платёж
		// сделать ещё два неудачных входа подряд, а это прямая дорога к блокировке
		// кабинета. Останавливаем проход до следующего раза.
		b.receiptAuthBlocked.Store(true)
		b.alertReceiptOnce("auth", fmt.Sprintf(
			"🔐 «Мой налог» не принял вход: %s\n\nЧеки не пробиваются, пока не поправите учётные данные.",
			html.EscapeString(message),
		))
	case moynalog.KindBadRequest:
		slog.Error("ФНС отвергла запрос на пробитие чека", "error", err, "payment_id", payment.ID)
		// Маркер в базе, а не в памяти: отвергнутый чек больше не появится ни в
		// плановом проходе, ни в сводке, поэтому единственное сообщение о нём обязано
		// пережить перезапуск бота.
		b.alertOwnerOnce(fmt.Sprintf("%s%d", notificationReceiptRejectedPrefix, payment.ID), fmt.Sprintf(
			"⚠️ ФНС отвергла чек по платежу #%d на %d ₽: %s\n\nПовторять бесполезно, нужен разбор в кабинете.",
			payment.ID, payment.Amount, html.EscapeString(message),
		))
	default:
		// Недоступность ФНС владельца не будит: добьёт плановый проход.
		slog.Warn("Чек не пробит, повторим плановым проходом",
			"error", err, "payment_id", payment.ID, "attempts", receipt.Attempts+1)
	}
}

// alertReceiptOnce отправляет сообщение владельцу один раз на ключ: повторять
// одно и то же каждые полчаса — значит перестать быть прочитанным. Маркер живёт
// в памяти — для проблем, которые перезапуск бота и так проверяет заново.
func (b *Bot) alertReceiptOnce(key, message string) {
	if _, alerted := b.receiptAlerted.LoadOrStore(key, struct{}{}); alerted {
		return
	}
	b.sendAdminAlert(message)
}

// alertOwnerOnce делает то же, но с маркером в базе — сообщение не повторится и
// после перезапуска.
func (b *Bot) alertOwnerOnce(notificationType, message string) {
	sent, err := b.db.WasNotificationSent(b.config.AdminID, notificationType)
	if err != nil {
		slog.Error("Не удалось проверить маркер уведомления", "error", err, "type", notificationType)
		return
	}
	if sent {
		return
	}
	if err := b.sendSchedulerMessage(b.config.AdminID, message); err != nil {
		logSchedulerSendError(notificationType, b.config.AdminID, err)
		return
	}
	if err := b.db.MarkNotificationSent(b.config.AdminID, notificationType); err != nil {
		slog.Error("Не удалось сохранить маркер уведомления", "error", err, "type", notificationType)
	}
}

// receiptServiceName собирает наименование услуги: базовая часть из окружения плюс
// метка в скобках. У ручных чеков метки нет — им наименование не пересобирается.
func receiptServiceName(base, marker string) string {
	if marker == "" {
		return base
	}
	return fmt.Sprintf("%s (%s)", base, marker)
}
