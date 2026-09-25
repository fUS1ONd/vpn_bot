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

// Срок записи платежа: через час она пропадает из незакрытых платежей
// пользователя. Закрывает её сверка зависших — по ответу кассы или через сутки.
const autorenewPaymentTTL = time.Hour

// autorenewChargeResult — исход одной попытки в проходе.
type autorenewChargeResult struct {
	attempted bool
	// transportFailure — до кассы не достучались или 5xx. Отказ кассы сюда не
	// входит: он означает, что касса работает.
	transportFailure bool
	// resend — повтор недошедшего обращения тем же ключом. В счётчики прохода не
	// идёт: о недоступности кассы владелец узнал на первом обращении, и повторы
	// каждые полчаса иначе слали бы тот же алерт до самого истечения ключа.
	resend bool
	notify func() // что сказать пользователю вне мьютекса
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
			if result.resend {
				return
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
	resend := false
	if already {
		// Попытка этого окна уже была. Повторяем её только если касса так и не
		// назвала свой платёж, а ключ ещё жив: до следующего окна почти сутки, и
		// ключ к нему гарантированно протухнет.
		if resend, err = b.autorenewResendable(telegramID, remUser.ExpireAt, attemptNo, now); err != nil || !resend {
			return autorenewChargeResult{}
		}
	}

	// Обычно от второго списания защищает сдвиг expireAt, но при упавшей
	// активации деньги уже приняты, а expireAt на месте. Поэтому спрашиваем не
	// «двигалась ли подписка», а «принимали ли мы деньги в этом цикле». Платёж с
	// упавшей активацией считается при любой дате приёма: ручная оплата за два дня
	// до конца тоже висит на месте expireAt, и retry её ещё доведёт.
	paid, err := b.db.HasConfirmedPaymentSince(telegramID, remUser.ExpireAt.Add(-autorenewChargeLead))
	if err == nil && !paid {
		paid, err = b.db.HasUnactivatedPayment(telegramID)
	}
	if err != nil {
		slog.Error("Автосписание: не удалось проверить оплату цикла", "error", err, "telegram_id", telegramID)
		return autorenewChargeResult{}
	}
	if paid {
		slog.Info("Автосписание: деньги за этот цикл уже приняты, пропускаем",
			"telegram_id", telegramID, "expire_at", remUser.ExpireAt)
		return autorenewChargeResult{}
	}

	if b.autorenewCycleUnsettled(telegramID, remUser.ExpireAt, now) {
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

	result := b.performAutorenewCharge(telegramID, *dbUser.SubscriptionPrice, attemptNo, remUser)
	result.resend = resend
	return result
}

// autorenewKeyLifetime — сколько после первого обращения прежний ключ
// идемпотентности ещё безопасно переиспользовать. ЮKassa держит ключ сутки:
// запас в час, иначе повтор «по тому же ключу» уйдёт в кассу уже новым платежом.
// Попытки T−24ч и T−0 разнесены почти ровно на сутки, поэтому повтор по ключу
// делается в окне своей же попытки — каждым проходом scheduler, пока ключ жив.
const autorenewKeyLifetime = 23 * time.Hour

// autorenewResendable — можно ли повторить уже сделанную попытку attemptNo тем
// же ключом: касса не ответила и своего платежа не назвала, а ключ ещё жив.
// Повтор безопасен — по тому же ключу касса вернёт прежний платёж, если деньги
// уже ушли, и не создаст второй.
func (b *Bot) autorenewResendable(telegramID int64, cycle time.Time, attemptNo int, now time.Time) (bool, error) {
	unresolved, err := b.db.UnresolvedAutorenewAttempt(telegramID, cycle)
	if err != nil {
		slog.Error("Автосписание: не удалось проверить попытку без ответа кассы", "error", err, "telegram_id", telegramID)
		return false, err
	}
	if unresolved == nil || unresolved.AttemptNo != attemptNo {
		return false, nil
	}
	// Протухший ключ разбирает autorenewCycleUnsettled: алерт владельцу и без
	// новых обращений.
	return now.Sub(unresolved.CreatedAt) < autorenewKeyLifetime, nil
}

// autorenewCycleUnsettled — висит ли в цикле попытка, судьбу которой мы не
// знаем. Новый ключ идемпотентности в цикле допустим только после определённого
// отказа кассы: при любом другом исходе деньги могли уйти, и новая попытка
// списала бы второй раз за месяц.
//
//   - Касса назвала свой платёж, а он не отменён (pending, не сошёлся со сверкой,
//     не подтвердился у нас) — его судьбу решают вебхук и сверка зависших.
//   - Касса платежа не назвала — повтор возможен только по прежнему ключу и
//     только пока тот жив (autorenewKeyLifetime).
func (b *Bot) autorenewCycleUnsettled(telegramID int64, cycle, now time.Time) bool {
	attempts, err := b.db.ListAutorenewAttempts(telegramID, cycle)
	if err != nil {
		slog.Error("Автосписание: не удалось прочитать попытки цикла", "error", err, "telegram_id", telegramID)
		return true
	}
	for _, a := range attempts {
		if a.Outcome != database.AutorenewOutcomeUnknown || a.PaymentID == nil {
			continue
		}
		payment, err := b.db.GetPaymentByID(*a.PaymentID)
		if err != nil || payment == nil {
			slog.Error("Автосписание: не удалось прочитать платёж прошлой попытки", "error", err, "payment_id", *a.PaymentID)
			return true
		}
		if payment.ProviderPaymentID != nil && *payment.ProviderPaymentID != "" {
			if payment.Status == "canceled" {
				continue
			}
			slog.Warn("Автосписание: исход прошлой попытки цикла не выяснен, новую не делаем",
				"telegram_id", telegramID, "payment_id", payment.ID, "status", payment.Status)
			return true
		}
		if now.Sub(a.CreatedAt) >= autorenewKeyLifetime {
			slog.Warn("Автосписание: ключ прошлой попытки истёк, а исход неизвестен — новую не делаем",
				"telegram_id", telegramID, "payment_id", payment.ID, "attempt_at", a.CreatedAt)
			b.sendAdminAlert(fmt.Sprintf(
				"⚠️ Автосписание #%d (пользователь %d): касса не ответила на попытку %s, и повторить её по тому же ключу уже нельзя. "+
					"Вторую попытку не делаем, чтобы не списать дважды. Проверьте платёж в кабинете ЮKassa.",
				payment.ID, telegramID, a.CreatedAt.Format("02.01.2006 15:04")))
			return true
		}
	}
	return false
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
		// Исход неизвестен: платёж живёт незавершённым, следующий проход повторит
		// обращение по тому же ключу (autorenewResendable) и узнает его судьбу.
		// Пользователю не пишем. В счётчик
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
		if notify := b.finishDeclinedAutorenew(payment, charged, attempt, price); notify != nil {
			return autorenewChargeResult{attempted: true, notify: notify}
		}
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
	// Перечитываем, чтобы у записи был created_at из БД: по нему решается,
	// можно ли сохранять Способ из ответа кассы.
	created, err := b.db.GetPaymentByID(id)
	if err != nil {
		return nil, err
	}
	if created == nil {
		return nil, fmt.Errorf("autorenew payment %d vanished after insert", id)
	}
	return created, nil
}

// isYooKassaOutage отличает поломку на стороне кассы или магазина от ответа по
// конкретному платежу: 4xx клиент отдаёт текстом «yookassa API error 4xx».
// 401 (сломанный ключ) и 403 (отозвано разрешение на автоплатежи) — поломка:
// они бьют по всем списаниям сразу, и ради них алерт и существует.
func isYooKassaOutage(err error) bool {
	if err == nil {
		return false
	}
	msg := err.Error()
	for _, code := range []string{"error 400", "error 404", "error 409"} {
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

// finishDeclinedAutorenew обрабатывает отказ кассы и возвращает уведомление для
// отправки вне мьютекса: медленный Telegram не должен держать платежи человека.
func (b *Bot) finishDeclinedAutorenew(payment *database.Payment, charged *paymentprovider.Payment, attempt *database.AutorenewAttempt, price int) func() {
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
	if attempt.AttemptNo != 1 {
		return nil
	}
	expireAt, methodGone := attempt.ExpireAt, charged.MethodGone
	return func() { b.notifyAutorenewFailure(telegramID, price, expireAt, methodGone) }
}
