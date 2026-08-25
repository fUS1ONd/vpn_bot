package bot

import (
	"fmt"
	"log/slog"
	"strings"
	"sync"
	"time"

	"github.com/fus1ond/vpn_bot/internal/database"
	"github.com/fus1ond/vpn_bot/internal/paymentprovider"
	"github.com/fus1ond/vpn_bot/internal/remnawave"
	"github.com/fus1ond/vpn_bot/internal/yookassa"
)

// autorenewChargeConcurrency — сколько списаний идут одновременно.
//
// Списания вынесены в отдельный шаг прохода не ради скорости: клиент кассы
// тратит до ~45 секунд на человека (3 попытки по 15 с), и тридцать человек в
// окне T−24ч, обработанные последовательно внутри основного цикла, встали бы на
// четверть часа и задержали уведомления и автокики остальным. Принцип тот же,
// по которому из основного потока вынесены чеки «Моего налога».
const autorenewChargeConcurrency = 5

// autorenewAttemptCount — сколько попыток даётся на один цикл подписки:
// T−24ч и T−0. В grace попыток нет.
const autorenewAttemptCount = 2

// autorenewPaymentTTL — срок жизни локальной записи платежа автосписания.
// Ответ кассы синхронный, так что запись либо закрывается в тот же миг, либо
// осталась в неизвестном исходе; час нужен, чтобы её успел подобрать вебхук.
const autorenewPaymentTTL = time.Hour

// autorenewChargeResult — исход одной попытки в проходе. Нужен агрегату:
// поголовный сбой транспорта отличается от обычных отказов карт.
type autorenewChargeResult struct {
	attempted bool
	// transportFailure — не дозвонились до кассы или получили 5xx. Отказ кассы
	// (`canceled`) сюда не входит: он означает, что касса работает.
	transportFailure bool
	// notify — что сказать пользователю после освобождения мьютекса.
	notify func()
}

// runAutorenewCharges — шаг прохода scheduler: списания по включённому
// Автопродлению.
func (b *Bot) runAutorenewCharges(now time.Time) {
	if !b.autorenewAvailable() {
		return
	}
	// В режиме обслуживания не списываем — по той же логике, по которой не
	// делаем disable: режим включают, когда с платежами или панелью что-то не
	// так, и списать деньги, не сумев продлить подписку, — худший исход.
	// Попытка при этом не расходуется: это наше решение не идти в кассу.
	if b.isMaintenanceMode() {
		slog.Info("Scheduler: maintenance mode, пропускаем автосписания")
		return
	}

	renewals, err := b.db.ListEnabledAutorenewals()
	if err != nil {
		slog.Error("Scheduler: не удалось получить список автопродлений", "error", err)
		return
	}
	if len(renewals) == 0 {
		return
	}

	var (
		mu        sync.Mutex
		attempted int
		transport int
		wg        sync.WaitGroup
	)
	sem := make(chan struct{}, autorenewChargeConcurrency)

	for _, renewal := range renewals {
		wg.Add(1)
		go func(r *database.Autorenewal) {
			defer wg.Done()
			defer func() {
				if rec := recover(); rec != nil {
					slog.Error("Автосписание упало с паникой", "telegram_id", r.TelegramID, "recover", rec)
				}
			}()
			sem <- struct{}{}
			defer func() { <-sem }()

			result := b.chargeAutorenewal(r, now)
			// Сообщение отправляется уже вне мьютекса платежей: под ним живут
			// только поход в кассу и подтверждение.
			if result.notify != nil {
				result.notify()
			}
			mu.Lock()
			defer mu.Unlock()
			if result.attempted {
				attempted++
			}
			if result.transportFailure {
				transport++
			}
		}(renewal)
	}
	wg.Wait()

	if attempted > 0 {
		slog.Info("Scheduler: автосписания завершены", "attempted", attempted, "transport_failures", transport)
	}
	b.reportAutorenewOutage(attempted, transport)
}

// chargeAutorenewal проводит одну попытку списания для одного пользователя.
//
// Всё тело — под мьютексом платежей этого пользователя: гонка «scheduler
// списывает, человек в ту же секунду платит руками» даёт две оплаты за месяц,
// самый дорогой баг этой фичи. Под мьютексом же перечитываются согласие,
// Способ и expireAt: если человек только что оплатил вручную, окно неактуально.
func (b *Bot) chargeAutorenewal(renewal *database.Autorenewal, now time.Time) autorenewChargeResult {
	telegramID := renewal.TelegramID

	mu := getPaymentMutex(telegramID)
	mu.Lock()
	defer mu.Unlock()

	fresh, err := b.db.GetAutorenewal(telegramID)
	if err != nil {
		slog.Error("Автосписание: не удалось перечитать автопродление", "error", err, "telegram_id", telegramID)
		return autorenewChargeResult{}
	}
	if !fresh.IsEnabled() || !fresh.HasMethod() {
		return autorenewChargeResult{}
	}

	dbUser, err := b.db.GetUserByTelegramID(telegramID)
	if err != nil || dbUser == nil {
		slog.Warn("Автосписание: пользователь не найден", "error", err, "telegram_id", telegramID)
		return autorenewChargeResult{}
	}
	// Legacy без цены и тестовые платежи в шаг не попадают: списывать нечего и
	// не по чему.
	if dbUser.SubscriptionPrice == nil || *dbUser.SubscriptionPrice <= 0 {
		return autorenewChargeResult{}
	}

	ref, ok := b.resolveUserRef(telegramID)
	if !ok {
		return autorenewChargeResult{}
	}
	remUser, err := b.remnawave.GetUser(ref)
	if err != nil {
		slog.Warn("Автосписание: не удалось перечитать пользователя панели", "error", err, "telegram_id", telegramID)
		return autorenewChargeResult{}
	}
	// Бессрочные подписки в шаг не попадают.
	if remUser.ExpireAt.Year() >= 2099 {
		return autorenewChargeResult{}
	}

	attemptNo, ok := b.autorenewAttemptFor(telegramID, remUser, now)
	if !ok {
		return autorenewChargeResult{}
	}

	already, err := b.db.HasAutorenewAttempt(telegramID, remUser.ExpireAt, attemptNo)
	if err != nil {
		slog.Error("Автосписание: не удалось проверить попытку", "error", err, "telegram_id", telegramID)
		return autorenewChargeResult{}
	}
	if already {
		return autorenewChargeResult{}
	}

	// Обычно от второго списания за месяц защищает сам сдвиг expireAt: списали —
	// подписка уехала, окна больше нет. Но если активация в панели упала, деньги
	// уже приняты (`confirmed_not_activated`), а expireAt остался на месте — и
	// попытка T−0 списала бы второй раз за тот же месяц. Поэтому спрашиваем не
	// «двигалась ли подписка», а «принимали ли мы деньги в этом цикле».
	paid, err := b.db.HasConfirmedPaymentSince(telegramID, remUser.ExpireAt.Add(-autorenewChargeLead))
	if err != nil {
		slog.Error("Автосписание: не удалось проверить оплату цикла", "error", err, "telegram_id", telegramID)
		return autorenewChargeResult{}
	}
	if paid {
		slog.Info("Автосписание: деньги за этот цикл уже приняты, пропускаем",
			"telegram_id", telegramID, "expire_at", remUser.ExpireAt)
		return autorenewChargeResult{}
	}

	// Живая ссылка на ручную оплату того же месяца. Мьютекс её не закрывает:
	// деньги по ней уходят в кассе, а не в нашем процессе, и списав сейчас, мы
	// оставили бы человеку действующую ссылку на второй платёж за тот же месяц.
	// Попытку не расходуем — ссылка протухнет, и следующий проход спишет.
	pending, err := b.db.GetPendingPayment(telegramID)
	if err != nil {
		slog.Error("Автосписание: не удалось проверить незакрытый платёж", "error", err, "telegram_id", telegramID)
		return autorenewChargeResult{}
	}
	if pending != nil && pending.RedirectURL != nil && *pending.RedirectURL != "" {
		slog.Info("Автосписание: у пользователя открыта ссылка на ручную оплату, пропускаем",
			"telegram_id", telegramID, "payment_id", pending.ID)
		return autorenewChargeResult{}
	}

	return b.performAutorenewCharge(telegramID, *dbUser.SubscriptionPrice, attemptNo, remUser)
}

// autorenewAttemptFor определяет, какая попытка положена пользователю прямо
// сейчас: первая за сутки до конца подписки, вторая — в момент окончания.
//
// Вторая попытка живёт ровно в том проходе, где `now` перевалило `expireAt`, и
// отрабатывает раньше ветки disable того же прохода. Границей grace служат две
// проверки сразу — статус в панели и наша пометка `expired`; почему обе, см. ниже.
func (b *Bot) autorenewAttemptFor(telegramID int64, remUser *remnawave.User, now time.Time) (int, bool) {
	if now.Before(remUser.ExpireAt) {
		// Окно T−24ч. Здесь пользователь обязан быть активен: неактивному в
		// этот момент списание не поможет, его отключили не за неоплату.
		if remUser.Status != remnawave.StatusActive || now.Before(remUser.ExpireAt.Add(-autorenewChargeLead)) {
			return 0, false
		}
		return 1, true
	}

	// Окно T−0. В grace не пробуем: человек уже отключён, увидел это и, если
	// хочет вернуться, продлит сам. Границ две, и обе обязательны.
	//
	// Статус в панели отсекает отключённых — в том числе тех, кого владелец
	// отключил руками: списать с такого деньги и вернуть ему доступ было бы
	// хуже любой потерянной попытки.
	if remUser.Status != remnawave.StatusActive {
		return 0, false
	}
	// Наша пометка `expired` отсекает тех, кто уже провёл в grace хотя бы один
	// проход. Без неё окно T−0 держалось бы все 72 часа: панель не меняет
	// статус сама, disable делаем мы — и на следующем проходе пользователь всё
	// ещё выглядел бы подходящим.
	notified, err := b.db.WasNotificationSent(telegramID, notificationExpired)
	if err != nil {
		slog.Warn("Автосписание: не удалось проверить пометку истечения", "error", err, "telegram_id", telegramID)
		return 0, false
	}
	if notified {
		return 0, false
	}
	return autorenewAttemptCount, true
}

// performAutorenewCharge создаёт платёж, столбит попытку и идёт в кассу.
//
// Попытка записывается до обращения к кассе — тем же приёмом, что и право на
// пробитие чека. Иначе падение между запросом и записью означало бы вторую
// попытку с новым ключом идемпотентности в следующем проходе: касса приняла бы
// её за отдельный платёж и списала бы дважды.
func (b *Bot) performAutorenewCharge(telegramID int64, price, attemptNo int, remUser *remnawave.User) autorenewChargeResult {
	cycle := remUser.ExpireAt

	payment, err := b.autorenewChargePayment(telegramID, price, cycle)
	if err != nil {
		slog.Error("Автосписание: не удалось подготовить платёж", "error", err, "telegram_id", telegramID)
		return autorenewChargeResult{}
	}
	paymentID := payment.ID
	key := *payment.ProviderRequestKey

	attempt := &database.AutorenewAttempt{
		TelegramID: telegramID, ExpireAt: cycle, AttemptNo: attemptNo,
		Outcome: database.AutorenewOutcomeUnknown, PaymentID: &paymentID,
	}
	if err := b.db.RecordAutorenewAttempt(attempt); err != nil {
		slog.Error("Автосписание: не удалось застолбить попытку", "error", err, "telegram_id", telegramID)
		_ = b.db.UpdatePaymentStatus(paymentID, "canceled")
		return autorenewChargeResult{}
	}

	renewal, err := b.db.GetAutorenewal(telegramID)
	if err != nil || !renewal.HasMethod() {
		slog.Error("Автосписание: Способ исчез перед обращением к кассе", "error", err, "telegram_id", telegramID)
		return autorenewChargeResult{attempted: true}
	}

	charged, err := b.yookassa.ChargeSavedMethod(paymentprovider.ChargeRequest{
		Amount:          price,
		Currency:        "RUB",
		Description:     "Продление подписки",
		LocalPaymentID:  paymentID,
		IdempotenceKey:  key,
		PaymentMethodID: *renewal.PaymentMethodID,
	})
	if err != nil {
		// Исход неизвестен: платёж живёт как обычный незавершённый, а следующая
		// попытка цикла пойдёт по тому же ключу идемпотентности и узнает его
		// судьбу. Пользователю не пишем — списать дважды страшнее, чем не списать.
		//
		// В счётчик аномалии идут только те ошибки, из которых складывается
		// «касса недоступна». Отказ по 400 у одного человека — это ответ кассы,
		// и алерт «ни одна попытка не дошла» подсказывал бы владельцу неверное.
		outage := isYooKassaOutage(err)
		slog.Warn("Автосписание: касса не ответила", "error", err,
			"telegram_id", telegramID, "payment_id", paymentID, "outage", outage)
		return autorenewChargeResult{attempted: true, transportFailure: outage}
	}

	// Идентификатор платежа у кассы сохраняется при любом исходе, а не только
	// при успехе: вебхук ищет платёж исключительно по нему. Без этой строки
	// отказ карты прилетал бы владельцу как «событие не сопоставлено», а
	// дозревший до succeeded pending не находился бы вовсе — деньги приняты,
	// подписка не выдана.
	if err := b.db.SetProviderPaymentDetails(paymentID, charged.ID, "", charged.ExpiresAt); err != nil {
		slog.Error("Автосписание: не удалось сохранить id платежа кассы", "error", err, "payment_id", paymentID)
	}
	payment.ProviderPaymentID = &charged.ID

	switch charged.Status {
	case paymentprovider.StatusSucceeded:
		// Сообщение уходит уже вне мьютекса: под ним делается только то, что
		// обязано быть атомарным относительно ручной оплаты, — поход в кассу и
		// подтверждение. Telegram и панель ретраятся секундами, и держать на
		// них платёжную блокировку пользователя незачем.
		if notify := b.finishSuccessfulAutorenew(payment, charged, attempt, price); notify != nil {
			return autorenewChargeResult{attempted: true, notify: notify}
		}
	case paymentprovider.StatusCanceled:
		b.finishDeclinedAutorenew(payment, charged, attempt, price)
	default:
		// pending: 54-ФЗ у нас не используется, но статус обработать обязаны.
		// Попытка израсходована, пользователю не пишем.
		slog.Info("Автосписание: касса ответила pending", "telegram_id", telegramID, "payment_id", paymentID)
	}
	return autorenewChargeResult{attempted: true}
}

// autorenewChargePayment готовит локальную запись платежа для попытки.
//
// Обычно это новая запись со свежим ключом идемпотентности. Но если прошлая
// попытка этого цикла закончилась неизвестным исходом и касса так и не назвала
// свой платёж, переиспользуется её запись и её ключ: обращение могло дойти и
// деньги могли уйти, а новый ключ означал бы второе списание за месяц. По
// прежнему ключу касса вернёт тот же платёж, а не создаст второй.
func (b *Bot) autorenewChargePayment(telegramID int64, price int, cycle time.Time) (*database.Payment, error) {
	unresolved, err := b.db.UnresolvedAutorenewAttempt(telegramID, cycle)
	if err != nil {
		return nil, err
	}
	if unresolved != nil && unresolved.PaymentID != nil {
		previous, err := b.db.GetPaymentByID(*unresolved.PaymentID)
		if err != nil {
			return nil, err
		}
		// Статус записи здесь ничего не решает. Между попытками цикла сутки, и
		// к моменту второй ExpireOldPendingPayments уже пометил запись
		// `expired` — если требовать `pending`, защита не сработает ровно
		// тогда, ради чего написана. Возвращаем запись к жизни: право на
		// повторное обращение даёт неразрешённая попытка, а не статус.
		//
		// Сумма обязана совпадать: касса откажет, если по тому же ключу придут
		// другие параметры. Разошлась цена — идём новой записью, зато честно.
		reusable := previous != nil && previous.ProviderRequestKey != nil &&
			previous.Amount == price &&
			(previous.Status == "pending" || previous.Status == "expired")
		if reusable {
			if previous.Status != "pending" {
				if err := b.db.UpdatePaymentStatus(previous.ID, "pending"); err != nil {
					return nil, err
				}
				previous.Status = "pending"
			}
			expiresAt := time.Now().UTC().Add(autorenewPaymentTTL)
			if err := b.db.SetPaymentExpiry(previous.ID, expiresAt); err != nil {
				return nil, err
			}
			previous.ExpiresAt = &expiresAt
			slog.Info("Автосписание: повторяем обращение по прежнему ключу идемпотентности",
				"telegram_id", telegramID, "payment_id", previous.ID)
			return previous, nil
		}
	}

	key, err := yookassa.NewIdempotenceKey()
	if err != nil {
		return nil, err
	}
	feeBasisPoints := b.getPaymentFeeBasisPoints(paymentprovider.YooKassa, paymentprovider.YooKassa)
	// expires_at ставится обязательно: без него запись не протухает никогда
	// (ExpireOldPendingPayments сравнивает даты и NULL пропускает), и висящий
	// pending остаётся у пользователя навсегда.
	expiresAt := time.Now().UTC().Add(autorenewPaymentTTL)
	payment := &database.Payment{
		TelegramID:             telegramID,
		Amount:                 price,
		PaymentMethod:          paymentprovider.YooKassa,
		Status:                 "pending",
		Provider:               paymentprovider.YooKassa,
		ProviderRequestKey:     &key,
		ProviderFeeBasisPoints: &feeBasisPoints,
		PeriodMonths:           1,
		ExpiresAt:              &expiresAt,
	}
	id, err := b.db.CreatePayment(payment)
	if err != nil {
		return nil, err
	}
	payment.ID = id
	return payment, nil
}

// isYooKassaOutage отличает «до кассы не достучались» от «касса ответила
// отказом». Клиент повторяет только транспортные сбои и 5xx, а 4xx отдаёт
// текстом «yookassa API error 4xx» — по нему и различаем.
func isYooKassaOutage(err error) bool {
	if err == nil {
		return false
	}
	msg := err.Error()
	for _, code := range []string{"error 400", "error 401", "error 403", "error 404", "error 409"} {
		if strings.Contains(msg, code) {
			return false
		}
	}
	return true
}

// finishSuccessfulAutorenew подтверждает платёж и сообщает пользователю.
// Подтверждение идёт молча (handleConfirmedSilently): штатное «Оплата прошла!»
// подразумевает действие пользователя, а он ничего не делал.
func (b *Bot) finishSuccessfulAutorenew(payment *database.Payment, charged *paymentprovider.Payment, attempt *database.AutorenewAttempt, price int) func() {
	telegramID := payment.TelegramID

	// Сверка ответа кассы с локальной записью — то же требование, что и на
	// вебхуке: выдаём подписку только по сверенному ответу API (Р10).
	if err := b.verifyYooKassaPayment(payment, charged); err != nil {
		slog.Error("Автосписание: ответ кассы не сошёлся с локальной записью", "error", err, "payment_id", payment.ID)
		b.sendAdminAlert(fmt.Sprintf(
			"⚠️ Автосписание #%d (%d ₽, пользователь %d): ответ ЮKassa не сошёлся с локальной записью. Разберите операцию вручную.",
			payment.ID, price, telegramID))
		return nil
	}

	// Сумму прошлого списания читаем до того, как эта попытка станет успешной:
	// иначе «прошлым» окажется текущее списание, и рост цены останется молчком.
	previous, hasPrevious := b.previousAutorenewCharge(telegramID)

	handler := &paymentCallbackHandler{bot: b}
	if err := handler.handleConfirmedSilently(payment); err != nil {
		slog.Error("Автосписание: не удалось подтвердить платёж", "error", err, "payment_id", payment.ID)
		return nil
	}

	attempt.Outcome = database.AutorenewOutcomeSuccess
	if err := b.db.RecordAutorenewAttempt(attempt); err != nil {
		slog.Error("Автосписание: не удалось записать исход попытки", "error", err, "payment_id", payment.ID)
	}

	final, err := b.db.GetPaymentByID(payment.ID)
	if err != nil || final == nil {
		slog.Error("Автосписание: не удалось перечитать платёж", "error", err, "payment_id", payment.ID)
		return nil
	}
	if final.Status != "confirmed" {
		// Деньги приняты, подписка не продлена. Про это уже кричит штатный
		// алерт активации; обещать пользователю продление нельзя.
		slog.Warn("Автосписание: платёж принят, но подписка не продлена",
			"payment_id", payment.ID, "status", final.Status, "telegram_id", telegramID)
		return nil
	}

	return func() { b.notifyAutorenewSuccess(telegramID, price, previous, hasPrevious) }
}

// finishDeclinedAutorenew обрабатывает отказ кассы.
func (b *Bot) finishDeclinedAutorenew(payment *database.Payment, charged *paymentprovider.Payment, attempt *database.AutorenewAttempt, price int) {
	telegramID := payment.TelegramID

	if err := b.db.UpdatePaymentStatus(payment.ID, "canceled"); err != nil {
		slog.Error("Автосписание: не удалось закрыть отклонённый платёж", "error", err, "payment_id", payment.ID)
	}

	attempt.Outcome = database.AutorenewOutcomeDeclined
	if charged.MethodGone {
		// Способа больше нет — гасим Способ, но не согласие: при следующей
		// ручной оплате картой автопродление оживает само (Р1).
		attempt.Outcome = database.AutorenewOutcomeMethodGone
		if err := b.db.ClearAutorenewMethod(telegramID); err != nil {
			slog.Error("Автосписание: не удалось погасить Способ", "error", err, "telegram_id", telegramID)
		}
	}
	if err := b.db.RecordAutorenewAttempt(attempt); err != nil {
		slog.Error("Автосписание: не удалось записать исход попытки", "error", err, "payment_id", payment.ID)
	}

	slog.Info("Автосписание отклонено кассой",
		"telegram_id", telegramID, "payment_id", payment.ID,
		"reason", charged.CancellationReason, "method_gone", charged.MethodGone)

	// После провала T−0 про автопродление молчим: человек вчера прочитал, что
	// списание не прошло и надо пополнить карту, а сегодня получит штатное
	// «подписка истекла». Повторять то же самое в момент отключения навязчиво.
	if attempt.AttemptNo == 1 {
		b.notifyAutorenewFailure(telegramID, price, attempt.ExpireAt, charged.MethodGone)
	}
}
