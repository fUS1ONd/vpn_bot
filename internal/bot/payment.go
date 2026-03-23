package bot

import (
	"fmt"
	"log/slog"
	"strconv"
	"sync"
	"time"

	"github.com/fus1ond/vpn_bot/internal/callback"
	"github.com/fus1ond/vpn_bot/internal/database"
	"github.com/fus1ond/vpn_bot/internal/platega"
)

// paymentMu — мьютексы по telegram_id для защиты от race condition при обработке callback.
// TODO: sync.Map не чистится — за годы работы накопятся тысячи мьютексов.
// Не критично (мьютекс маленький), но при необходимости можно добавить периодическую чистку.
var paymentMu sync.Map // map[int64]*sync.Mutex

var defaultPaymentRetryDelays = []time.Duration{
	30 * time.Second,
	1 * time.Minute,
	5 * time.Minute,
}

func getPaymentMutex(telegramID int64) *sync.Mutex {
	mu, _ := paymentMu.LoadOrStore(telegramID, &sync.Mutex{})
	return mu.(*sync.Mutex)
}

// paymentCallbackHandler реализует callback.PaymentHandler
type paymentCallbackHandler struct {
	bot *Bot
}

// PaymentCallbackHandler возвращает обработчик callback от Platega
func (b *Bot) PaymentCallbackHandler() callback.PaymentHandler {
	return &paymentCallbackHandler{bot: b}
}

// HandlePaymentCallback обрабатывает callback от Platega
func (h *paymentCallbackHandler) HandlePaymentCallback(payload platega.CallbackPayload) error {
	// Находим платёж по platega_transaction_id
	payment, err := h.bot.db.GetPaymentByPlategaTxID(payload.ID)
	if err != nil {
		return fmt.Errorf("get payment by tx: %w", err)
	}
	if payment == nil {
		slog.Warn("Callback для неизвестной транзакции", "transaction_id", payload.ID)
		return nil // Не возвращаем ошибку, чтобы Platega не retry-ила
	}

	// Блокируем обработку по telegram_id
	mu := getPaymentMutex(payment.TelegramID)
	mu.Lock()
	defer mu.Unlock()

	switch payload.Status {
	case platega.StatusConfirmed, platega.StatusManualConfirmed:
		return h.handleConfirmed(payment)
	case platega.StatusCanceled:
		return h.handleCanceled(payment)
	case platega.StatusChargebacked:
		return h.handleChargeback(payment)
	default:
		slog.Warn("Callback с неожиданным статусом", "status", payload.Status, "transaction_id", payload.ID)
		return nil
	}
}

// handleConfirmed обрабатывает успешный платёж
func (h *paymentCallbackHandler) handleConfirmed(payment *database.Payment) error {
	// Идемпотентность: если платёж уже confirmed — пропускаем
	if payment.Status == "confirmed" {
		slog.Info("Платёж уже подтверждён, пропускаем", "payment_id", payment.ID)
		return nil
	}

	alreadyMarkedForRetry := payment.Status == "confirmed_not_activated"

	// Подтверждаем платёж в БД
	if err := h.bot.db.ConfirmPayment(payment.ID); err != nil {
		return fmt.Errorf("confirm payment: %w", err)
	}

	// Пытаемся активировать подписку один раз.
	// Долгие retry выполняет scheduler, чтобы не держать callback/manual-check path открытым.
	if err := h.activateSubscription(payment); err != nil {
		slog.Error("Не удалось активировать подписку после подтверждения, помечаем для scheduler",
			"error", err, "payment_id", payment.ID)
		if updateErr := h.bot.db.UpdatePaymentStatus(payment.ID, "confirmed_not_activated"); updateErr != nil {
			return fmt.Errorf("update status to confirmed_not_activated: %w", updateErr)
		}
		h.bot.schedulePaymentActivationRetry(payment.ID)

		// Уведомляем админа
		if !alreadyMarkedForRetry {
			h.bot.sendAdminAlert(fmt.Sprintf(
				"⚠️ Платёж #%d подтверждён, но не удалось активировать подписку для %d. Платёж помечен как confirmed_not_activated и будет повторно обработан scheduler.",
				payment.ID, payment.TelegramID,
			))
		}
		return nil // Не возвращаем ошибку — платёж уже сохранён
	}

	h.finalizeActivatedPayment(payment)
	return nil
}

func (h *paymentCallbackHandler) finalizeActivatedPayment(payment *database.Payment) {
	// Создаём запись в moderator_earnings (если есть модератор)
	h.createEarningRecord(payment)

	// Уведомляем пользователя
	remUser, _ := h.bot.remnawave.GetUserByTelegramID(payment.TelegramID)

	var msg string
	if remUser != nil {
		expireDate := remUser.ExpireAt.Format("02.01.2006")
		msg = fmt.Sprintf("✅ Оплата прошла! Ваша подписка активна до <b>%s</b>.\n\nЛимит трафика снят — пользуйтесь без ограничений.\n\nБлиже к концу подписки мы напомним о продлении.", expireDate)
	} else {
		msg = "✅ Оплата прошла! Подписка активирована."
	}

	_ = h.bot.sendSchedulerMessage(payment.TelegramID, msg)

	// Очищаем уведомления (пользователь мог быть в grace period)
	h.bot.db.ClearNotifications(payment.TelegramID)
}

func (b *Bot) paymentActivationRetryDelays() []time.Duration {
	if len(b.paymentRetryDelays) > 0 {
		delays := make([]time.Duration, len(b.paymentRetryDelays))
		copy(delays, b.paymentRetryDelays)
		return delays
	}

	delays := make([]time.Duration, len(defaultPaymentRetryDelays))
	copy(delays, defaultPaymentRetryDelays)
	return delays
}

func (b *Bot) schedulePaymentActivationRetry(paymentID int64) {
	if _, loaded := b.paymentRetryInFlight.LoadOrStore(paymentID, struct{}{}); loaded {
		return
	}

	delays := b.paymentActivationRetryDelays()
	go func() {
		defer b.paymentRetryInFlight.Delete(paymentID)

		for attempt, delay := range delays {
			time.Sleep(delay)

			if b.retryConfirmedPaymentActivation(paymentID, "background_retry") {
				return
			}

			slog.Warn("Background retry активации не удался",
				"payment_id", paymentID,
				"attempt", attempt+1,
				"next_retry_in", nextRetryDelay(delays, attempt),
			)
		}
	}()
}

func nextRetryDelay(delays []time.Duration, attempt int) string {
	if attempt+1 >= len(delays) {
		return "scheduler"
	}
	return delays[attempt+1].String()
}

func (b *Bot) retryConfirmedPaymentActivation(paymentID int64, source string) bool {
	payment, err := b.db.GetPaymentByID(paymentID)
	if err != nil {
		slog.Error("Не удалось загрузить платёж для retry активации",
			"error", err, "payment_id", paymentID, "source", source)
		return false
	}
	if payment == nil || payment.Status != "confirmed_not_activated" {
		return true
	}

	mu := getPaymentMutex(payment.TelegramID)
	mu.Lock()
	defer mu.Unlock()

	payment, err = b.db.GetPaymentByID(paymentID)
	if err != nil {
		slog.Error("Не удалось перечитать платёж под mutex для retry активации",
			"error", err, "payment_id", paymentID, "source", source)
		return false
	}
	if payment == nil || payment.Status != "confirmed_not_activated" {
		return true
	}

	handler := &paymentCallbackHandler{bot: b}
	if err := handler.activateSubscription(payment); err != nil {
		slog.Warn("Не удалось активировать подписку при retry",
			"error", err,
			"payment_id", paymentID,
			"telegram_id", payment.TelegramID,
			"source", source,
		)
		return false
	}

	if err := b.db.ConfirmPayment(payment.ID); err != nil {
		slog.Error("Не удалось обновить статус после retry активации",
			"error", err, "payment_id", paymentID, "source", source)
		return false
	}

	handler.finalizeActivatedPayment(payment)
	slog.Info("Retry активации успешен",
		"payment_id", paymentID,
		"telegram_id", payment.TelegramID,
		"source", source,
	)

	return true
}

// activateSubscription продлевает подписку в Remnawave
func (h *paymentCallbackHandler) activateSubscription(payment *database.Payment) error {
	user, err := h.bot.db.GetUserByTelegramID(payment.TelegramID)
	if err != nil || user == nil {
		return fmt.Errorf("user not found: telegram_id=%d", payment.TelegramID)
	}

	remUser, err := h.bot.remnawave.GetUser(user.RemnawaveUUID)
	if err != nil {
		return fmt.Errorf("get remnawave user: %w", err)
	}

	now := time.Now().UTC()
	var newExpireAt time.Time

	// Если подписка ещё активна (досрочное продление) — плюсуем к текущему expireAt
	if remUser.ExpireAt.After(now) && remUser.Status == "ACTIVE" {
		newExpireAt = remUser.ExpireAt.AddDate(0, 1, 0)
	} else {
		// Триал, grace period или истёк — считаем от момента оплаты
		newExpireAt = now.AddDate(0, 1, 0)
	}

	// Реактивируем пользователя: ставит Status=ACTIVE, ExpireAt=newExpireAt, TrafficLimitBytes=0.
	return h.bot.remnawave.EnableUser(user.RemnawaveUUID, newExpireAt)
}

// createEarningRecord создаёт запись начисления модератору
func (h *paymentCallbackHandler) createEarningRecord(payment *database.Payment) {
	if payment.ModeratorID == nil {
		return // Админский пользователь — без начислений
	}

	moderatorID := *payment.ModeratorID

	// Проверяем, что модератор ещё активен
	if !h.bot.isModerator(moderatorID) {
		return
	}

	// Считаем количество платящих клиентов для определения доли
	payingCount, err := h.bot.db.CountPayingSubscribersByModerator(moderatorID)
	if err != nil {
		slog.Error("Ошибка подсчёта платящих подписчиков", "error", err, "moderator_id", moderatorID)
		return
	}

	sharePercent := calculateSharePercent(payingCount)

	// Определяем комиссию Platega по методу оплаты
	feePercent := h.bot.getPlategaFeePercent(payment.PaymentMethod)
	withdrawalPercent := h.bot.config.PlategaFeeWithdrawal

	grossAmount := payment.Amount
	plategaFee := grossAmount * feePercent / 100
	afterPlatega := grossAmount - plategaFee
	withdrawalFee := afterPlatega * withdrawalPercent / 100
	netAmount := afterPlatega - withdrawalFee
	shareAmount := netAmount * sharePercent / 100

	earning := &database.ModeratorEarning{
		PaymentID:     payment.ID,
		ModeratorID:   moderatorID,
		GrossAmount:   grossAmount,
		PlategaFee:    plategaFee,
		WithdrawalFee: withdrawalFee,
		NetAmount:     netAmount,
		SharePercent:  sharePercent,
		ShareAmount:   shareAmount,
	}

	if _, err := h.bot.db.CreateEarning(earning); err != nil {
		slog.Error("Ошибка создания записи начисления", "error", err, "payment_id", payment.ID)
	}
}

// calculateSharePercent определяет долю модератора по количеству платящих клиентов
func calculateSharePercent(payingCount int) int {
	switch {
	case payingCount >= 25:
		return 25
	case payingCount >= 15:
		return 20
	default:
		return 15
	}
}

// getPlategaFeePercent возвращает процент комиссии Platega для метода оплаты
func (b *Bot) getPlategaFeePercent(paymentMethod string) int {
	switch paymentMethod {
	case "sbp":
		return b.config.PlategaFeeSBP
	case "card":
		return b.config.PlategaFeeCard
	case "crypto":
		return b.config.PlategaFeeCrypto
	default:
		return b.config.PlategaFeeSBP // Fallback
	}
}

// handleCanceled обрабатывает отменённый платёж
func (h *paymentCallbackHandler) handleCanceled(payment *database.Payment) error {
	if payment.Status != "pending" {
		return nil
	}
	if err := h.bot.db.UpdatePaymentStatus(payment.ID, "canceled"); err != nil {
		return fmt.Errorf("update status to canceled: %w", err)
	}
	_ = h.bot.sendSchedulerMessage(payment.TelegramID, "❌ Платёж отменён. Вы можете попробовать снова.")
	return nil
}

// handleChargeback обрабатывает chargeback
func (h *paymentCallbackHandler) handleChargeback(payment *database.Payment) error {
	if err := h.bot.db.UpdatePaymentStatus(payment.ID, "chargebacked"); err != nil {
		return fmt.Errorf("update status to chargebacked: %w", err)
	}

	// Деактивируем пользователя
	user, err := h.bot.db.GetUserByTelegramID(payment.TelegramID)
	if err == nil && user != nil {
		_ = h.bot.remnawave.DisableUser(user.RemnawaveUUID)
	}

	// Уведомляем админа
	h.bot.sendAdminAlert(fmt.Sprintf(
		"⚠️ Chargeback от %d, сумма: %d руб. Пользователь деактивирован.",
		payment.TelegramID, payment.Amount,
	))

	return nil
}

// sendAdminAlert отправляет сообщение админу
func (b *Bot) sendAdminAlert(msg string) {
	_ = b.sendSchedulerMessage(b.config.AdminID, msg)
}

// createPaymentForUser создаёт платёж для пользователя
func (b *Bot) createPaymentForUser(telegramID int64, paymentMethodInt int) (*database.Payment, string, error) {
	user, err := b.db.GetUserByTelegramID(telegramID)
	if err != nil || user == nil {
		return nil, "", fmt.Errorf("user not found")
	}

	if user.SubscriptionPrice == nil {
		return nil, "", fmt.Errorf("subscription price not set")
	}

	// Проверка лимита 90 дней: нельзя оплатить, если до конца подписки >= 90 дней
	remUser, err := b.remnawave.GetUserByTelegramID(telegramID)
	if err == nil && remUser != nil && remUser.Status == "ACTIVE" && remUser.ExpireAt.Year() < 2099 {
		daysLeft := int(time.Until(remUser.ExpireAt).Hours() / 24)
		if daysLeft >= 90 {
			return nil, "", fmt.Errorf("subscription_too_far: %d days left", daysLeft)
		}
	}

	paymentMethodStr := platega.PaymentMethodString(paymentMethodInt)

	// Проверяем наличие активного PENDING платежа
	pending, err := b.db.GetPendingPayment(telegramID)
	if err != nil {
		return nil, "", fmt.Errorf("check pending: %w", err)
	}

	if pending != nil {
		if pending.PaymentMethod == paymentMethodStr {
			// Тот же способ — возвращаем ту же ссылку
			url := ""
			if pending.RedirectURL != nil {
				url = *pending.RedirectURL
			}
			return pending, url, nil
		}
		// Другой способ — помечаем старый как expired
		b.db.UpdatePaymentStatus(pending.ID, "expired")
	}

	// Создаём платёж в Platega
	callbackURL := b.config.PlategaCallbackURL
	telegramIDStr := strconv.FormatInt(telegramID, 10)

	resp, err := b.platega.CreatePayment(platega.CreateTransactionRequest{
		PaymentMethod: paymentMethodInt,
		Amount:        *user.SubscriptionPrice,
		Currency:      "RUB",
		Description:   "VPN подписка на 1 месяц",
		ReturnURL:     fmt.Sprintf("https://t.me/%s", b.bot.Me.Username),
		FailedURL:     fmt.Sprintf("https://t.me/%s", b.bot.Me.Username),
		CallbackURL:   callbackURL,
		Payload:       telegramIDStr,
	})
	if err != nil {
		return nil, "", fmt.Errorf("platega create payment: %w", err)
	}

	// Вычисляем время жизни
	var expiresAt *time.Time
	if resp.ExpiresIn > 0 {
		t := time.Now().Add(resp.ExpiresIn)
		expiresAt = &t
	}

	// Сохраняем в БД
	payment := &database.Payment{
		TelegramID:           telegramID,
		ModeratorID:          user.ModeratorID,
		Amount:               *user.SubscriptionPrice,
		PaymentMethod:        paymentMethodStr,
		Status:               "pending",
		PlategaTransactionID: &resp.TransactionID,
		RedirectURL:          &resp.Redirect,
		ExpiresAt:            expiresAt,
	}

	id, err := b.db.CreatePayment(payment)
	if err != nil {
		return nil, "", fmt.Errorf("save payment: %w", err)
	}
	payment.ID = id

	return payment, resp.Redirect, nil
}

// checkPaymentStatus ручная проверка статуса платежа через Platega API.
// Защищён мьютексом по telegram_id для предотвращения race condition
// с параллельным callback от Platega.
func (b *Bot) checkPaymentStatus(telegramID int64) (string, error) {
	// Берём мьютекс ДО чтения из БД — та же блокировка, что и в callback
	mu := getPaymentMutex(telegramID)
	mu.Lock()
	defer mu.Unlock()

	// Попутно помечаем протухшие PENDING как expired (не ждём scheduler)
	b.db.ExpireOldPendingPayments()

	pending, err := b.db.GetPendingPayment(telegramID)
	if err != nil {
		return "", fmt.Errorf("get pending: %w", err)
	}
	if pending == nil {
		return "not_found", nil
	}
	if pending.PlategaTransactionID == nil {
		return "pending", nil
	}

	status, err := b.platega.GetTransactionStatus(*pending.PlategaTransactionID)
	if err != nil {
		return "", fmt.Errorf("check status: %w", err)
	}

	if status.Status == platega.StatusConfirmed {
		// Платёж подтверждён — обрабатываем как callback (мьютекс уже взят)
		handler := &paymentCallbackHandler{bot: b}
		if err := handler.handleConfirmed(pending); err != nil {
			return "", err
		}

		updated, err := b.db.GetPaymentByID(pending.ID)
		if err != nil {
			return "", fmt.Errorf("reload payment after confirm: %w", err)
		}
		if updated == nil {
			return "confirmed", nil
		}

		return updated.Status, nil
	}

	if status.Status == platega.StatusManualConfirmed {
		handler := &paymentCallbackHandler{bot: b}
		if err := handler.handleConfirmed(pending); err != nil {
			return "", err
		}

		updated, err := b.db.GetPaymentByID(pending.ID)
		if err != nil {
			return "", fmt.Errorf("reload payment after manual confirm: %w", err)
		}
		if updated == nil {
			return "confirmed", nil
		}

		return updated.Status, nil
	}

	if status.Status == platega.StatusCanceled {
		handler := &paymentCallbackHandler{bot: b}
		if err := handler.handleCanceled(pending); err != nil {
			return "", err
		}
		return platega.StatusCanceled, nil
	}

	if status.Status == platega.StatusChargebacked {
		handler := &paymentCallbackHandler{bot: b}
		if err := handler.handleChargeback(pending); err != nil {
			return "", err
		}
		return platega.StatusChargebacked, nil
	}

	return status.Status, nil
}
