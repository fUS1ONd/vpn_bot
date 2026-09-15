package bot

import (
	"fmt"
	"log/slog"
	"time"

	"github.com/fus1ond/vpn_bot/internal/database"
	"github.com/fus1ond/vpn_bot/internal/paymentprovider"
)

// Сверка зависших платежей. Об оплате бот узнаёт по уведомлению провайдера или по
// кнопке «Проверить оплату». Если уведомление потерялось, а человек кнопку не
// нажал, деньги списаны, а подписка не выдана (платёж №135). Сверка спрашивает
// провайдера сама.
const (
	// pendingFirstCheckDelay — сколько платёж ждёт уведомления, прежде чем бот
	// спросит провайдера сам. Раньше спрашивать незачем: оплата с переходом в
	// банковское приложение занимает минуты, а брошенные платежи лишь нагружали бы API.
	pendingFirstCheckDelay = 15 * time.Minute

	// pendingMaxAge — возраст, после которого платёж без конечного статуса
	// закрывается, а закрытый перестаёт сверяться. Совпадает с окном, в котором
	// ЮKassa повторяет уведомления: позже ни уведомление, ни оплата уже не придут.
	// Срок жизни ссылки пределом не служит: у Platega он около 15 минут, а крипта
	// подтверждается позже — закрытый по ссылке платёж callback уже не оживит.
	pendingMaxAge = 24 * time.Hour

	// defaultReconcilePassBudget — потолок времени на шаг сверки в проходе
	// планировщика. Недоступный провайдер держит каждый платёж до минуты, а за
	// сверкой в проходе стоят уведомления, отключения и автокики: им ждать нельзя.
	// Что не успели — досверим следующим проходом.
	defaultReconcilePassBudget = 5 * time.Minute
)

func (b *Bot) firstPendingCheckDelay() time.Duration {
	if b.pendingCheckDelay > 0 {
		return b.pendingCheckDelay
	}
	return pendingFirstCheckDelay
}

func (b *Bot) reconcileBudget() time.Duration {
	if b.reconcilePassBudget > 0 {
		return b.reconcilePassBudget
	}
	return defaultReconcilePassBudget
}

// schedulePendingPaymentCheck ставит первую сверку нового платежа. Таймер живёт в
// памяти и при перезапуске теряется — такой платёж подхватит планировщик.
func (b *Bot) schedulePendingPaymentCheck(paymentID int64) {
	if _, loaded := b.pendingCheckScheduled.LoadOrStore(paymentID, struct{}{}); loaded {
		return
	}
	delay := b.firstPendingCheckDelay()
	go func() {
		defer b.pendingCheckScheduled.Delete(paymentID)
		defer func() {
			if r := recover(); r != nil {
				slog.Error("pending payment check goroutine panicked", "payment_id", paymentID, "recover", r)
			}
		}()
		select {
		case <-b.shutdownCh:
			return
		case <-time.After(delay):
		}
		b.reconcilePendingPayment(paymentID, time.Now().UTC(), "timer")
	}()
}

// reconcilePendingPayments — шаг планировщика: сверяет платежи, судьба которых
// решается ответом провайдера.
//
// Свои сбои шаг держит при себе: паника здесь не должна срывать уведомления,
// отключения и автокики остального прохода.
func (b *Bot) reconcilePendingPayments(now time.Time) {
	defer func() {
		if r := recover(); r != nil {
			slog.Error("Шаг сверки платежей упал с паникой", "recover", r)
		}
	}()

	ids, err := b.reconcilablePaymentIDs(now)
	if err != nil {
		slog.Error("Scheduler: не удалось получить платежи для сверки", "error", err)
		return
	}

	deadline := time.Now().Add(b.reconcileBudget())
	for i, id := range ids {
		select {
		case <-b.shutdownCh:
			slog.Info("Scheduler: бот останавливается, сверка платежей прервана", "processed", i, "left", len(ids)-i)
			return
		default:
		}
		if time.Now().After(deadline) {
			slog.Warn("Scheduler: бюджет времени на сверку платежей исчерпан, остальные досверим следующим проходом",
				"processed", i, "left", len(ids)-i)
			return
		}
		b.reconcilePendingPayment(id, now, "scheduler")
	}
}

// reconcilablePaymentIDs собирает платежи, которые ещё имеет смысл сверять:
// зависшие PENDING старше первой задержки и локально закрытые не старше суток.
// Закрытые нужны потому, что бот закрывает платёж сам (смена способа оплаты,
// сорвавшееся создание), а деньги по нему могли всё же пройти.
func (b *Bot) reconcilablePaymentIDs(now time.Time) ([]int64, error) {
	pending, err := b.db.PendingPaymentIDsCreatedBefore(now.Add(-pendingFirstCheckDelay))
	if err != nil {
		return nil, err
	}
	closed, err := b.db.ClosedPaymentIDsCreatedAfter(now.Add(-pendingMaxAge))
	if err != nil {
		return nil, err
	}
	return append(pending, closed...), nil
}

// reconcilePendingPayment переносит на платёж статус, который отдаёт провайдер.
// Платёж, так и не получивший конечного статуса к сроку, закрывается — но только
// после вопроса провайдеру, чтобы не закрыть оплаченный.
func (b *Bot) reconcilePendingPayment(paymentID int64, now time.Time, source string) {
	payment, err := b.db.GetPaymentByID(paymentID)
	if err != nil {
		slog.Error("Сверка платежа: не удалось загрузить платёж", "error", err, "payment_id", paymentID, "source", source)
		return
	}
	if !reconcilable(payment, now) {
		return
	}

	mu := getPaymentMutex(payment.TelegramID)
	mu.Lock()
	defer mu.Unlock()

	// Под мьютексом перечитываем: пока ждали, мог прийти вебхук или нажата кнопка.
	payment, err = b.db.GetPaymentByID(paymentID)
	if err != nil {
		slog.Error("Сверка платежа: не удалось перечитать платёж", "error", err, "payment_id", paymentID, "source", source)
		return
	}
	if !reconcilable(payment, now) {
		return
	}

	// Закрывается только платёж, который всё ещё ждёт оплаты: закрытый закрывать нечего.
	deadlinePassed := payment.Status == "pending" && !now.Before(payment.CreatedAt.Add(pendingMaxAge))

	verified, err := b.verifiedProviderState(payment)
	if err != nil {
		if deadlinePassed {
			slog.Warn("Сверка платежа: провайдер не дал ответа к сроку, платёж закрыт",
				"error", err, "payment_id", payment.ID, "provider", payment.Provider, "source", source)
			b.expirePendingPayment(payment)
			return
		}
		slog.Warn("Сверка платежа: провайдер не дал ответа, спросим позже",
			"error", err, "payment_id", payment.ID, "provider", payment.Provider, "source", source)
		return
	}

	h := &paymentCallbackHandler{bot: b}
	switch verified.Status {
	case paymentprovider.StatusSucceeded:
		slog.Warn("Платёж подтверждён сверкой: уведомление провайдера не дошло",
			"payment_id", payment.ID, "provider", payment.Provider, "telegram_id", payment.TelegramID,
			"local_status", payment.Status, "age", now.Sub(payment.CreatedAt).Round(time.Second).String(), "source", source)
		if err := h.handleConfirmedFromProviderState(payment); err != nil {
			slog.Error("Сверка платежа: не удалось принять оплату", "error", err, "payment_id", payment.ID, "source", source)
		}
	case paymentprovider.StatusCanceled:
		if payment.Status == "pending" {
			slog.Info("Платёж отменён по сверке: уведомление об отмене не дошло",
				"payment_id", payment.ID, "provider", payment.Provider, "source", source)
		}
		if err := h.handleCanceled(payment); err != nil {
			slog.Error("Сверка платежа: не удалось отменить платёж", "error", err, "payment_id", payment.ID, "source", source)
		}
	case paymentprovider.StatusChargebacked:
		if err := h.handleChargeback(payment); err != nil {
			slog.Error("Сверка платежа: не удалось обработать chargeback", "error", err, "payment_id", payment.ID, "source", source)
		}
	default:
		if deadlinePassed {
			slog.Info("Сверка платежа: платёж не оплачен к сроку, закрыт",
				"payment_id", payment.ID, "provider", payment.Provider, "provider_status", verified.Status, "source", source)
			b.expirePendingPayment(payment)
		}
	}
}

// reconcilable сообщает, решается ли судьба платежа ответом провайдера: платёж
// ждёт оплаты либо закрыт локально не больше суток назад.
func reconcilable(payment *database.Payment, now time.Time) bool {
	if payment == nil {
		return false
	}
	if payment.Status == "pending" {
		return true
	}
	if !revivablePaymentStatuses[payment.Status] {
		return false
	}
	return now.Before(payment.CreatedAt.Add(pendingMaxAge))
}

// reconcileUserPaymentsBeforeKick спрашивает провайдера о живых платежах человека
// перед отключением или удалением. Обычный шаг сверки ждёт 15 минут, а удаление
// учётной записи необратимо: оплата, сделанная минуту назад, должна успеть стать
// подпиской.
func (b *Bot) reconcileUserPaymentsBeforeKick(telegramID int64, now time.Time) {
	ids, err := b.db.PendingPaymentIDsOfUser(telegramID)
	if err != nil {
		slog.Error("Не удалось получить платежи пользователя перед киком", "error", err, "telegram_id", telegramID)
		return
	}
	for _, id := range ids {
		b.reconcilePendingPayment(id, now, "pre-kick")
	}
}

// verifiedProviderState запрашивает у провайдера состояние платежа и сверяет его
// с локальной записью.
func (b *Bot) verifiedProviderState(payment *database.Payment) (*paymentprovider.Payment, error) {
	if payment.ProviderPaymentID == nil {
		return nil, fmt.Errorf("платёж не дошёл до провайдера")
	}
	provider, err := b.paymentProvider(payment.Provider)
	if err != nil {
		return nil, err
	}
	verified, err := provider.GetPayment(*payment.ProviderPaymentID)
	if err != nil {
		return nil, err
	}
	if err := b.verifyProviderPayment(payment, verified); err != nil {
		return nil, err
	}
	return verified, nil
}

func (b *Bot) expirePendingPayment(payment *database.Payment) {
	if err := b.db.ExpirePendingPayment(payment.ID); err != nil {
		slog.Error("Сверка платежа: не удалось закрыть платёж", "error", err, "payment_id", payment.ID)
	}
}
