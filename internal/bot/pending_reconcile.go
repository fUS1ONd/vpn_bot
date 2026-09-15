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
	// закрывается. Совпадает с окном, в котором ЮKassa повторяет уведомления:
	// позже ни уведомление, ни оплата уже не придут.
	pendingMaxAge = 24 * time.Hour
)

func (b *Bot) firstPendingCheckDelay() time.Duration {
	if b.pendingCheckDelay > 0 {
		return b.pendingCheckDelay
	}
	return pendingFirstCheckDelay
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

// reconcilePendingPayments — шаг планировщика: сверяет все PENDING-платежи старше
// первой задержки.
func (b *Bot) reconcilePendingPayments(now time.Time) {
	ids, err := b.db.PendingPaymentIDsCreatedBefore(now.Add(-pendingFirstCheckDelay))
	if err != nil {
		slog.Error("Scheduler: не удалось получить зависшие платежи", "error", err)
		return
	}
	for _, id := range ids {
		b.reconcilePendingPayment(id, now, "scheduler")
	}
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
	if payment == nil || payment.Status != "pending" {
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
	if payment == nil || payment.Status != "pending" {
		return
	}

	deadlinePassed := !now.Before(pendingDeadline(payment))

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
			"age", now.Sub(payment.CreatedAt).Round(time.Second).String(), "source", source)
		if err := h.handleConfirmedFromProviderState(payment); err != nil {
			slog.Error("Сверка платежа: не удалось принять оплату", "error", err, "payment_id", payment.ID, "source", source)
		}
	case paymentprovider.StatusCanceled:
		slog.Info("Платёж отменён по сверке: уведомление об отмене не дошло",
			"payment_id", payment.ID, "provider", payment.Provider, "source", source)
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

// pendingDeadline — момент, после которого неоплаченный платёж закрывается:
// сутки с создания или срок жизни от провайдера, если он раньше.
func pendingDeadline(payment *database.Payment) time.Time {
	deadline := payment.CreatedAt.Add(pendingMaxAge)
	if payment.ExpiresAt != nil && payment.ExpiresAt.Before(deadline) {
		return *payment.ExpiresAt
	}
	return deadline
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
	if payment.Provider == paymentprovider.YooKassa {
		if err := b.verifyYooKassaPayment(payment, verified); err != nil {
			return nil, err
		}
	}
	return verified, nil
}

func (b *Bot) expirePendingPayment(payment *database.Payment) {
	if err := b.db.ExpirePendingPayment(payment.ID); err != nil {
		slog.Error("Сверка платежа: не удалось закрыть платёж", "error", err, "payment_id", payment.ID)
	}
}
