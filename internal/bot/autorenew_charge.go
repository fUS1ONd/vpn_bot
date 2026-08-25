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

// Списания вынесены отдельным шагом с пулом: клиент кассы тратит до ~45 секунд
// на человека, и последовательная обработка задержала бы уведомления и
// автокики остальным.
const autorenewChargeConcurrency = 5

// Попыток на цикл: T−24ч и T−0. В grace попыток нет.
const autorenewAttemptCount = 2

// Срок жизни записи платежа: час, чтобы её успел подобрать вебхук.
const autorenewPaymentTTL = time.Hour

// autorenewChargeResult — исход одной попытки в проходе.
type autorenewChargeResult struct {
	attempted bool
	// transportFailure — до кассы не достучались или 5xx. Отказ кассы сюда не
	// входит: он означает, что касса работает.
	transportFailure bool
	notify           func() // что сказать пользователю вне мьютекса
}

// runAutorenewCharges — шаг прохода scheduler: списания по включённому
// Автопродлению.
func (b *Bot) runAutorenewCharges(now time.Time) {
	if !b.autorenewAvailable() {
		return
	}
	// В maintenance не списываем: списать деньги, не сумев продлить подписку, —
	// худший исход. Попытка не расходуется — это наше решение не идти в кассу.
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
			// Сообщение уходит вне мьютекса: под ним только касса и подтверждение.
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

// chargeAutorenewal — одна попытка списания. Тело под мьютексом платежей:
// гонка «scheduler списывает, человек платит руками» даёт две оплаты за месяц.
// Под ним же перечитываются согласие, Способ и expireAt.
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
	// Legacy без цены: списывать нечего.
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

	// Обычно от второго списания защищает сдвиг expireAt, но при упавшей
	// активации деньги уже приняты, а expireAt на месте. Поэтому спрашиваем не
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

	// Живая ссылка на ручную оплату: мьютекс её не закрывает, деньги по ней
	// уходят в кассе. Попытку не расходуем — ссылка протухнет, спишем позже.
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

// autorenewAttemptFor — какая попытка положена сейчас: первая за сутки до
// конца подписки, вторая в момент окончания, раньше ветки disable того же прохода.
func (b *Bot) autorenewAttemptFor(telegramID int64, remUser *remnawave.User, now time.Time) (int, bool) {
	if now.Before(remUser.ExpireAt) {
		// Окно T−24ч. Неактивному списание не поможет: его отключили не за неоплату.
		if remUser.Status != remnawave.StatusActive || now.Before(remUser.ExpireAt.Add(-autorenewChargeLead)) {
			return 0, false
		}
		return 1, true
	}

	// Окно T−0. В grace не пробуем, и границы две. Статус отсекает отключённых,
	// в том числе вручную владельцем: списать с такого и вернуть ему доступ
	// хуже любой потерянной попытки. Пометка `expired` отсекает тех, кто уже
	// провёл в grace проход, — иначе окно держалось бы все 72 часа.
	if remUser.Status != remnawave.StatusActive {
		return 0, false
	}
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
// Попытка записывается ДО обращения к кассе: падение между запросом и записью
// означало бы новый ключ идемпотентности и второе списание.
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
		// Исход неизвестен: платёж живёт незавершённым, следующая попытка пойдёт
		// по тому же ключу и узнает его судьбу. Пользователю не пишем. В счётчик
		// аномалии идёт только недоступность кассы, 4xx — это её ответ.
		outage := isYooKassaOutage(err)
		slog.Warn("Автосписание: касса не ответила", "error", err,
			"telegram_id", telegramID, "payment_id", paymentID, "outage", outage)
		return autorenewChargeResult{attempted: true, transportFailure: outage}
	}

	// Id кассы сохраняется при любом исходе: вебхук ищет платёж только по нему.
	if err := b.db.SetProviderPaymentDetails(paymentID, charged.ID, "", charged.ExpiresAt); err != nil {
		slog.Error("Автосписание: не удалось сохранить id платежа кассы", "error", err, "payment_id", paymentID)
	}
	payment.ProviderPaymentID = &charged.ID

	switch charged.Status {
	case paymentprovider.StatusSucceeded:
		if notify := b.finishSuccessfulAutorenew(payment, charged, attempt, price); notify != nil {
			return autorenewChargeResult{attempted: true, notify: notify}
		}
	case paymentprovider.StatusCanceled:
		b.finishDeclinedAutorenew(payment, charged, attempt, price)
	default:
		// pending: попытка израсходована, пользователю не пишем.
		slog.Info("Автосписание: касса ответила pending", "telegram_id", telegramID, "payment_id", paymentID)
	}
	return autorenewChargeResult{attempted: true}
}

// autorenewChargePayment готовит запись платежа. Обычно новую, но если прошлая
// попытка цикла осталась с неизвестным исходом — переиспользует её вместе с
// ключом: деньги могли уйти, и новый ключ означал бы второе списание.
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
		// `expired` тоже подходит: между попытками сутки, и запись успевает
		// протухнуть — право на повтор даёт неразрешённая попытка, а не статус.
		// Сумма обязана совпасть: по тому же ключу с другими параметрами касса откажет.
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
	// Без expires_at запись не протухнет никогда и повиснет у пользователя навсегда.
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

// isYooKassaOutage отличает «до кассы не достучались» от её отказа: 4xx клиент
// отдаёт текстом «yookassa API error 4xx».
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

// finishSuccessfulAutorenew подтверждает платёж и возвращает уведомление для
// отправки вне мьютекса. Подтверждение молчаливое: штатное «Оплата прошла!»
// подразумевает действие пользователя, а он ничего не делал.
func (b *Bot) finishSuccessfulAutorenew(payment *database.Payment, charged *paymentprovider.Payment, attempt *database.AutorenewAttempt, price int) func() {
	telegramID := payment.TelegramID

	// Подписку выдаём только по сверенному ответу API — как и на вебхуке.
	if err := b.verifyYooKassaPayment(payment, charged); err != nil {
		slog.Error("Автосписание: ответ кассы не сошёлся с локальной записью", "error", err, "payment_id", payment.ID)
		b.sendAdminAlert(fmt.Sprintf(
			"⚠️ Автосписание #%d (%d ₽, пользователь %d): ответ ЮKassa не сошёлся с локальной записью. Разберите операцию вручную.",
			payment.ID, price, telegramID))
		return nil
	}

	// До записи успеха: иначе «прошлым» окажется текущее списание.
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
		// Деньги приняты, подписка не продлена: обещать продление нельзя.
		// Про это уже кричит штатный алерт активации.
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
		// Гасим Способ, но не согласие: при следующей оплате картой оживёт само.
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

	// После провала T−0 молчим: человек вчера всё прочитал, а сегодня получит
	// штатное «подписка истекла».
	if attempt.AttemptNo == 1 {
		b.notifyAutorenewFailure(telegramID, price, attempt.ExpireAt, charged.MethodGone)
	}
}
