package bot

import (
	"fmt"
	"html"
	"log/slog"
	"strings"
	"sync"
	"time"

	"github.com/fus1ond/vpn_bot/internal/callback"
	"github.com/fus1ond/vpn_bot/internal/database"
	"github.com/fus1ond/vpn_bot/internal/paymentprovider"
	"github.com/fus1ond/vpn_bot/internal/platega"
	"github.com/fus1ond/vpn_bot/internal/yookassa"
	tele "gopkg.in/telebot.v3"
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

const paymentStatusConfirmedActivationFailed = "confirmed_activation_failed"

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

// HandleYooKassaWebhook реализует callback.YooKassaHandler. Тело вебхука не
// авторитетно: ответ API провайдера сверяется с неизменной локальной записью.
func (b *Bot) HandleYooKassaWebhook(event, providerPaymentID string) error {
	payment, err := b.db.GetPaymentByProviderPaymentID(paymentprovider.YooKassa, providerPaymentID)
	if err != nil {
		return fmt.Errorf("get YooKassa payment: %w", err)
	}
	if payment == nil {
		// Событие не сопоставилось с платежом. Так теряются возвраты: в теле
		// refund.succeeded лежит идентификатор возврата, а не платежа. Тихо
		// отвечать «ок» нельзя — молчание про деньги не видно месяцами.
		b.reportUnmatchedYooKassaEvent(event, providerPaymentID)
		return nil
	}
	mu := getPaymentMutex(payment.TelegramID)
	mu.Lock()
	defer mu.Unlock()
	verified, err := b.yookassa.GetPayment(providerPaymentID)
	if err != nil {
		return fmt.Errorf("load YooKassa payment: %w", err)
	}
	if err := b.verifyYooKassaPayment(payment, verified); err != nil {
		return err
	}
	h := &paymentCallbackHandler{bot: b}
	switch verified.Status {
	case paymentprovider.StatusSucceeded:
		return h.handleConfirmedFromProviderState(payment)
	case paymentprovider.StatusCanceled:
		return h.handleCanceled(payment)
	default:
		return nil
	}
}

// reportUnmatchedYooKassaEvent логирует несопоставленное событие и доносит его до
// владельца. Ответ вебхуку остаётся успешным: повторные доставки от ЮKassa ничего не
// изменят — событие не станет сопоставимым, — а вот поток одинаковых сообщений создадут,
// поэтому про каждое событие сообщаем один раз.
func (b *Bot) reportUnmatchedYooKassaEvent(event, objectID string) {
	slog.Warn("Событие ЮKassa не сопоставлено с платежом",
		"event", event, "object_id", objectID)

	if _, alreadyReported := b.unmatchedEventReported.LoadOrStore(objectID, struct{}{}); alreadyReported {
		return
	}

	eventText := event
	if eventText == "" {
		eventText = "без типа"
	}
	b.sendAdminAlert(fmt.Sprintf(
		"⚠️ Событие ЮKassa <b>%s</b> не сопоставлено с платежом (объект <code>%s</code>).\n\n"+
			"Так приходят возвраты: в теле лежит идентификатор возврата, а не платежа. Проверьте операцию в кабинете ЮKassa.",
		html.EscapeString(eventText), html.EscapeString(objectID),
	))
}

// reportIgnoredPaymentConfirmation доносит до владельца оплату, которую бот
// принять не смог: локальный статус платежа не допускает подтверждения.
// Провайдеру мы отвечаем успехом (повтор доставки ничего не изменит), поэтому
// без этого сообщения деньги остались бы принятыми втихую.
func (b *Bot) reportIgnoredPaymentConfirmation(payment *database.Payment) {
	if _, alreadyReported := b.ignoredConfirmationReported.LoadOrStore(payment.ID, struct{}{}); alreadyReported {
		return
	}
	b.sendAdminAlert(fmt.Sprintf(
		"⚠️ Платёж #%d (%d ₽, пользователь %d) подтверждён провайдером, но локальный статус <b>%s</b> не допускает подтверждения.\n\n"+
			"Подписка не выдана и чек не пробит — разберите операцию вручную.",
		payment.ID, payment.Amount, payment.TelegramID, html.EscapeString(payment.Status),
	))
}

// reportRevivedPayment сообщает владельцу о принятой оплате по локально закрытому
// платежу. Клиент своё получает автоматически, но событие аномальное: закрытый
// платёж, по которому прошли деньги, — повод посмотреть, почему он закрылся.
func (b *Bot) reportRevivedPayment(payment *database.Payment) {
	if _, alreadyReported := b.revivedPaymentReported.LoadOrStore(payment.ID, struct{}{}); alreadyReported {
		return
	}
	b.sendAdminAlert(fmt.Sprintf(
		"ℹ️ Платёж #%d (%d ₽, пользователь %d) был локально закрыт со статусом <b>%s</b>, но провайдер подтвердил оплату.\n\n"+
			"Платёж принят по ответу провайдера: подписка продлевается, чек пробивается.",
		payment.ID, payment.Amount, payment.TelegramID, html.EscapeString(payment.Status),
	))
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

// handleConfirmed обрабатывает успешный платёж и уведомляет пользователя.
// Источник — тело callback провайдера, а не его ответ по API, поэтому
// воскрешать неактуальный локальный статус этот путь не имеет права.
func (h *paymentCallbackHandler) handleConfirmed(payment *database.Payment) error {
	return h.handleConfirmedWithNotification(payment, true, false)
}

// handleConfirmedFromProviderState обрабатывает платёж, оплату которого
// подтвердил сам провайдер своим ответом по API (и этот ответ уже сверен с
// локальной записью). Такому источнику доверия хватает, чтобы принять оплату
// даже по платежу, локально закрытому раньше времени.
func (h *paymentCallbackHandler) handleConfirmedFromProviderState(payment *database.Payment) error {
	return h.handleConfirmedWithNotification(payment, true, true)
}

// handleConfirmedSilently обрабатывает успешный платёж без отдельного push-уведомления.
// Используется для ручной проверки оплаты, чтобы не дублировать финальное сообщение.
// Статус там тоже получен ответом API провайдера и сверен с локальной записью.
func (h *paymentCallbackHandler) handleConfirmedSilently(payment *database.Payment) error {
	return h.handleConfirmedWithNotification(payment, false, true)
}

// revivablePaymentStatuses — локальные статусы, из которых платёж возвращается к
// жизни, если провайдер своим ответом API подтвердил оплату. Сюда намеренно не
// входят chargebacked (деньги уже вернули) и confirmed_activation_failed
// (активация признана невозможной — разбор ручной).
var revivablePaymentStatuses = map[string]bool{
	"expired":  true,
	"canceled": true,
}

func (h *paymentCallbackHandler) handleConfirmedWithNotification(payment *database.Payment, notifyUser, providerVerified bool) error {
	freshPayment, err := h.bot.db.GetPaymentByID(payment.ID)
	if err != nil {
		return fmt.Errorf("reload payment before confirm: %w", err)
	}
	if freshPayment == nil {
		return fmt.Errorf("payment not found: id=%d", payment.ID)
	}
	payment = freshPayment

	// Идемпотентность: если платёж уже confirmed — пропускаем
	if payment.Status == "confirmed" {
		slog.Info("Платёж уже подтверждён, пропускаем", "payment_id", payment.ID)
		return nil
	}
	if payment.Status != "pending" && payment.Status != "confirmed_not_activated" {
		// Локально платёж закрыт, а провайдер говорит «оплачен». Молчать здесь
		// нельзя ни в одном из исходов: деньги приняты, и если услугу не оказать,
		// про это не узнает никто, кроме клиента.
		if !providerVerified || !revivablePaymentStatuses[payment.Status] {
			slog.Warn("Подтверждение неактуального платежа проигнорировано", "payment_id", payment.ID, "status", payment.Status)
			h.bot.reportIgnoredPaymentConfirmation(payment)
			return nil
		}

		slog.Warn("Платёж оплачен, но локально был закрыт — подтверждаем по ответу провайдера",
			"payment_id", payment.ID, "status", payment.Status, "telegram_id", payment.TelegramID)
		h.bot.reportRevivedPayment(payment)
	}

	alreadyMarkedForRetry := payment.Status == "confirmed_not_activated"

	// Подтверждаем платёж в БД
	if err := h.bot.db.ConfirmPayment(payment.ID); err != nil {
		return fmt.Errorf("confirm payment: %w", err)
	}

	// Пытаемся активировать подписку один раз.
	// Долгие retry выполняет scheduler, чтобы не держать callback/manual-check path открытым.
	if err := h.activateSubscription(payment); err != nil {
		if isTerminalActivationError(err) {
			slog.Error("Активация подписки невозможна, переводим платёж в terminal-статус",
				"error", err, "payment_id", payment.ID, "telegram_id", payment.TelegramID)
			if updateErr := h.bot.db.UpdatePaymentStatus(payment.ID, paymentStatusConfirmedActivationFailed); updateErr != nil {
				return fmt.Errorf("update status to %s: %w", paymentStatusConfirmedActivationFailed, updateErr)
			}
			h.bot.sendAdminAlert(fmt.Sprintf(
				"⚠️ Платёж #%d подтверждён, но активация подписки невозможна для %d: %v",
				payment.ID, payment.TelegramID, err,
			))
			return nil
		}

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

	h.finalizeActivatedPayment(payment, notifyUser)
	return nil
}

// paymentActivatedMessage — сообщение об успешной оплате. Момент оплаты лучшая
// точка для приписки про Канал: предикат «Платящий» пользователь проходит
// именно сейчас. Приписка живёт здесь, а не у вызывающих, потому что путей к
// этому сообщению два — вебхук провайдера и кнопка «Проверить оплату», — и
// расходиться они не должны.
func (b *Bot) paymentActivatedMessage(telegramID int64) string {
	remUser, _ := b.remnawave.GetUserByTelegramID(telegramID)

	msg := "✅ Оплата прошла! Подписка активирована."
	if remUser != nil {
		expireDate := remUser.ExpireAt.Format("02.01.2006")
		msg = fmt.Sprintf("✅ Оплата прошла! Ваша подписка активна до <b>%s</b>.\n\nЛимит трафика снят — пользуйтесь без ограничений.\n\nБлиже к концу подписки мы напомним о продлении.", expireDate)
	}
	if mention := b.claimCommunityMention(telegramID); mention != "" {
		msg += "\n\n" + mention
	}
	return msg
}

func (h *paymentCallbackHandler) finalizeActivatedPayment(payment *database.Payment, notifyUser bool) {
	// Сбрасываем состояние только если пользователь всё ещё в платёжном flow
	h.bot.userStates.DeleteIfOneOf(payment.TelegramID, StateWaitPaymentMethod, StateWaitPaymentResult)

	if notifyUser {
		_ = h.bot.sendSchedulerMessageWithKeyboard(payment.TelegramID, h.bot.paymentConfirmationMessage(payment), h.bot.paymentSuccessMarkup(payment))
	}

	// Очищаем уведомления (пользователь мог быть в grace period)
	h.bot.db.ClearNotifications(payment.TelegramID)

	// Чек пробивается после активации подписки и параллельно ответу вебхуку:
	// налоговая не блокирует клиента.
	h.bot.issueReceiptAsync(payment.ID)
}

// paymentSuccessMarkup выбирает разметку сообщения об успешной оплате: обычно
// reply-клавиатуру меню, но при наличии предложения — inline-кнопку вместо неё.
// Обе Telegram не позволяет, а второе сообщение на ту же оплату было бы шумом.
func (b *Bot) paymentSuccessMarkup(payment *database.Payment) *tele.ReplyMarkup {
	return b.paymentSuccessMarkupFor(payment.TelegramID, payment.IsTest)
}

// paymentSuccessMarkupFor — то же для пути ручной проверки оплаты.
func (b *Bot) paymentSuccessMarkupFor(telegramID int64, isTest bool) *tele.ReplyMarkup {
	if !isTest {
		if offer := b.autorenewOfferMarkup(telegramID); offer != nil {
			return offer
		}
	}
	return b.userKeyboard(telegramID)
}

// isTestPaymentUser — то же условие, по которому платёж помечается тестовым при
// создании. Один предикат на оба места, иначе они разойдутся.
func (b *Bot) isTestPaymentUser(telegramID int64) bool {
	return b.config != nil && telegramID == b.config.AdminID && b.config.AdminTestPaymentPrice > 0
}

func (b *Bot) paymentConfirmationMessage(payment *database.Payment) string {
	if payment.IsTest {
		return "✅ Платёжная система работает."
	}
	// Платёж автосписания доходит сюда через retry активации, и текст обязан
	// остаться текстом автосписания: «я не платил» на сообщение о списанных
	// деньгах — прямая дорога к chargeback.
	if fromAutorenew, err := b.db.IsAutorenewPayment(payment.ID); err != nil {
		slog.Warn("Не удалось определить происхождение платежа", "error", err, "payment_id", payment.ID)
	} else if fromAutorenew {
		return b.autorenewActivatedMessage(payment)
	}
	return b.paymentActivatedMessage(payment.TelegramID)
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
		defer func() {
			if r := recover(); r != nil {
				slog.Error("payment retry goroutine panicked", "payment_id", paymentID, "recover", r)
			}
		}()

		for attempt, delay := range delays {
			select {
			case <-b.shutdownCh:
				slog.Info("Payment retry cancelled by shutdown", "payment_id", paymentID, "attempt", attempt+1)
				return
			case <-time.After(delay):
			}

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

func isTerminalActivationError(err error) bool {
	if err == nil {
		return false
	}

	msg := err.Error()
	return strings.Contains(msg, "user not found: telegram_id=") ||
		strings.Contains(msg, "API error 404")
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
		if isTerminalActivationError(err) {
			slog.Error("Retry активации упёрся в terminal-ошибку, останавливаем повторные попытки",
				"error", err,
				"payment_id", paymentID,
				"telegram_id", payment.TelegramID,
				"source", source,
			)
			if updateErr := b.db.UpdatePaymentStatus(payment.ID, paymentStatusConfirmedActivationFailed); updateErr != nil {
				slog.Error("Не удалось обновить статус после terminal-ошибки активации",
					"error", updateErr, "payment_id", paymentID, "source", source)
				return false
			}
			b.sendAdminAlert(fmt.Sprintf(
				"⚠️ Retry активации остановлен: платёж #%d подтверждён, но подписку невозможно активировать для %d: %v",
				payment.ID, payment.TelegramID, err,
			))
			return true
		}

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

	handler.finalizeActivatedPayment(payment, true)
	slog.Info("Retry активации успешен",
		"payment_id", paymentID,
		"telegram_id", payment.TelegramID,
		"source", source,
	)

	return true
}

// activateSubscription продлевает подписку в Remnawave
func (h *paymentCallbackHandler) activateSubscription(payment *database.Payment) error {
	// Тестовый платёж проверяет кассу и callback, но не должен менять доступ
	// администратора: в частности, заменять бессрочный expireAt на месячный.
	if payment.IsTest {
		return nil
	}

	user, err := h.bot.db.GetUserByTelegramID(payment.TelegramID)
	if err != nil || user == nil {
		return fmt.Errorf("user not found: telegram_id=%d", payment.TelegramID)
	}

	ref, err := h.bot.userRef(payment.TelegramID)
	if err != nil {
		return fmt.Errorf("resolve user ref: %w", err)
	}

	remUser, err := h.bot.remnawave.GetUser(ref)
	if err != nil {
		return fmt.Errorf("get remnawave user: %w", err)
	}

	newExpireAt := nextMonthExpireAt(remUser, time.Now().UTC())

	// Реактивируем пользователя: ставит Status=ACTIVE, ExpireAt=newExpireAt, TrafficLimitBytes=0.
	return h.bot.remnawave.EnableUser(ref, newExpireAt)
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

func (b *Bot) getPaymentFeeBasisPoints(provider, paymentMethod string) int {
	if provider == paymentprovider.YooKassa {
		return b.config.YooKassaFeeBasisPoints
	}
	return b.getPlategaFeePercent(paymentMethod) * 100
}

// handleCanceled обрабатывает отменённый платёж
func (h *paymentCallbackHandler) handleCanceled(payment *database.Payment) error {
	if payment.Status != "pending" {
		return nil
	}
	if err := h.bot.db.UpdatePaymentStatus(payment.ID, "canceled"); err != nil {
		return fmt.Errorf("update status to canceled: %w", err)
	}
	h.bot.userStates.DeleteIfOneOf(payment.TelegramID, StateWaitPaymentMethod, StateWaitPaymentResult)
	_ = h.bot.sendSchedulerMessageWithKeyboard(payment.TelegramID, "❌ Платёж отменён. Вы можете попробовать снова.", h.bot.userKeyboard(payment.TelegramID))
	return nil
}

// handleChargeback обрабатывает chargeback.
// Полностью зеркалит admin-ban flow (processBanUser): BanUser + DeleteUser из Remnawave + DeleteUser из БД.
func (h *paymentCallbackHandler) handleChargeback(payment *database.Payment) error {
	// Атомарная idempotency: обновляем статус только если ещё не chargebacked.
	// Защита от race condition при параллельных retry от Platega.
	updated, err := h.bot.db.UpdatePaymentStatusIfNot(payment.ID, "chargebacked", "chargebacked")
	if err != nil {
		return fmt.Errorf("update status to chargebacked: %w", err)
	}
	if !updated {
		// Уже обработан другим параллельным запросом
		return nil
	}
	if payment.IsTest {
		h.bot.sendAdminAlert(fmt.Sprintf(
			"⚠️ Chargeback тестового платежа #%d на %d руб. Учётная запись и подписка не изменены.",
			payment.ID, payment.Amount,
		))
		return nil
	}

	// Банём пользователя — chargeback = мошенничество, повторная регистрация запрещена.
	// Если BanUser не сработает — возвращаем ошибку, чтобы Platega retry-ла callback.
	if err := h.bot.db.BanUser(payment.TelegramID, 0); err != nil {
		return fmt.Errorf("chargeback ban user: %w", err)
	}

	// Кик из Канала — часть перманентного бана, а не админского действия:
	// забаненный за chargeback не должен остаться в сообществе.
	go h.bot.kickFromCommunity(payment.TelegramID)

	// Удаляем из Remnawave (полное удаление, не просто disable)
	user, err := h.bot.db.GetUserByTelegramID(payment.TelegramID)
	if err == nil && user != nil {
		if delErr := h.bot.deleteRemnawaveUser(payment.TelegramID); delErr != nil {
			slog.Error("Chargeback: не удалось удалить из Remnawave", "error", delErr, "telegram_id", payment.TelegramID)
		}
	}

	// Удаляем из БД бота
	if delErr := h.bot.db.DeleteUser(payment.TelegramID); delErr != nil {
		slog.Error("Chargeback: не удалось удалить из БД", "error", delErr, "telegram_id", payment.TelegramID)
	}

	// Очищаем маркеры отправленных уведомлений
	if clearErr := h.bot.db.ClearNotifications(payment.TelegramID); clearErr != nil {
		slog.Error("Chargeback: не удалось очистить уведомления", "error", clearErr, "telegram_id", payment.TelegramID)
	}

	// Уведомляем админа
	h.bot.sendAdminAlert(fmt.Sprintf(
		"⚠️ Chargeback от %d, сумма: %d руб. Пользователь удалён из Remnawave и забанен.",
		payment.TelegramID, payment.Amount,
	))

	return nil
}

// sendAdminAlert отправляет сообщение админу
func (b *Bot) sendAdminAlert(msg string) {
	_ = b.sendSchedulerMessage(b.config.AdminID, msg)
}

// createPaymentForUser создаёт платёж для пользователя
func (b *Bot) createPaymentForUser(telegramID int64, _ int) (*database.Payment, string, error) {
	return b.createPaymentForProvider(telegramID, paymentprovider.Platega)
}

func (b *Bot) createPaymentForProvider(telegramID int64, providerName string) (*database.Payment, string, error) {
	user, err := b.db.GetUserByTelegramID(telegramID)
	if err != nil || user == nil {
		return nil, "", fmt.Errorf("user not found")
	}

	price, ok := b.paymentPrice(telegramID, user)
	if !ok {
		return nil, "", fmt.Errorf("subscription price not set")
	}
	if price <= 0 {
		return nil, "", fmt.Errorf("некорректная сумма платежа: %d", price)
	}

	// Проверка лимита 90 дней: нельзя оплатить, если до конца подписки >= 90 дней
	remUser, err := b.remnawave.GetUserByTelegramID(telegramID)
	if err == nil && remUser != nil && remUser.Status == "ACTIVE" && remUser.ExpireAt.Year() < 2099 {
		daysLeft := int(remUser.ExpireAt.Sub(time.Now().UTC()).Hours() / 24)
		if daysLeft >= 90 {
			return nil, "", fmt.Errorf("subscription_too_far: %d days left", daysLeft)
		}
	}

	provider, err := b.paymentProvider(providerName)
	if err != nil {
		return nil, "", err
	}
	paymentMethodStr := "crypto"
	if providerName == paymentprovider.YooKassa {
		paymentMethodStr = paymentprovider.YooKassa
	}

	// Сериализуем весь create-flow по telegram_id.
	// Иначе два быстрых нажатия могут одновременно пройти проверку pending
	// и создать две живые ссылки на оплату.
	mu := getPaymentMutex(telegramID)
	mu.Lock()
	defer mu.Unlock()

	// Проверяем наличие активного PENDING платежа
	pending, err := b.db.GetPendingPayment(telegramID)
	if err != nil {
		return nil, "", fmt.Errorf("check pending: %w", err)
	}

	var payment *database.Payment
	if pending != nil {
		if pending.Provider == providerName {
			// Тот же способ — возвращаем ту же ссылку
			url := ""
			if pending.RedirectURL != nil {
				url = *pending.RedirectURL
			}
			if url != "" {
				return pending, url, nil
			}
			// Ссылки ещё нет: прошлый вызов провайдера сорвался, не дойдя до
			// сохранения confirmation_url. Переиспользуем ту же запись и тот же
			// ключ идемпотентности — провайдер вернёт по нему тот же платёж.
			// Помечать её expired здесь нельзя: ссылку по ней мы всё равно
			// выдадим, а оплату по expired-платежу подтверждение отвергнет как
			// неактуальную — деньги приняты, услуга не оказана.
			//
			// Кроме записи автосписания: по её ключу касса откажет обычному
			// платежу. Оставляем её жить своей жизнью и заводим новую.
			fromAutorenew, autorenewErr := b.db.IsAutorenewPayment(pending.ID)
			if autorenewErr != nil {
				return nil, "", fmt.Errorf("check autorenew payment: %w", autorenewErr)
			}
			if !fromAutorenew {
				payment = pending
			}
		} else if err := b.db.UpdatePaymentStatus(pending.ID, "expired"); err != nil {
			// Другой способ оплаты — старый pending помечаем expired
			return nil, "", fmt.Errorf("expire previous pending: %w", err)
		}
	}

	if payment == nil {
		// Сначала создаём локальную запись: её ID передаётся в metadata ЮKassa.
		isTest := b.isTestPaymentUser(telegramID)
		payment = &database.Payment{TelegramID: telegramID, Amount: price, PaymentMethod: paymentMethodStr, Status: "pending", Provider: providerName, IsTest: isTest}
		feeBasisPoints := b.getPaymentFeeBasisPoints(providerName, paymentMethodStr)
		payment.ProviderFeeBasisPoints = &feeBasisPoints
		if providerName == paymentprovider.YooKassa {
			key, keyErr := yookassa.NewIdempotenceKey()
			if keyErr != nil {
				return nil, "", keyErr
			}
			payment.ProviderRequestKey = &key
		}
		id, err := b.db.CreatePayment(payment)
		if err != nil {
			return nil, "", fmt.Errorf("save payment: %w", err)
		}
		payment.ID = id
	}
	returnURL := b.config.YooKassaReturnURL
	if returnURL == "" {
		returnURL = fmt.Sprintf("https://t.me/%s", b.bot.Me.Username)
	}
	callbackURL := b.config.PlategaCallbackURL
	requestKey := ""
	if payment.ProviderRequestKey != nil {
		requestKey = *payment.ProviderRequestKey
	}
	resp, err := provider.CreatePayment(paymentprovider.CreateRequest{Amount: price, Currency: "RUB", Description: "Пополнение баланса", ReturnURL: returnURL, CallbackURL: callbackURL, LocalPaymentID: payment.ID, IdempotenceKey: requestKey, SavePaymentMethod: b.shouldSavePaymentMethod(providerName, payment.IsTest)})
	if err != nil {
		if providerName != paymentprovider.YooKassa {
			_ = b.db.UpdatePaymentStatus(payment.ID, "expired")
		}
		return nil, "", fmt.Errorf("%s create payment: %w", providerName, err)
	}
	if err := b.db.SetProviderPaymentDetails(payment.ID, resp.ID, resp.ConfirmationURL, resp.ExpiresAt); err != nil {
		return nil, "", fmt.Errorf("save provider payment details: %w", err)
	}
	payment.ProviderPaymentID, payment.RedirectURL, payment.ExpiresAt = &resp.ID, &resp.ConfirmationURL, resp.ExpiresAt
	if providerName == paymentprovider.Platega {
		payment.PlategaTransactionID = &resp.ID
	}
	return payment, resp.ConfirmationURL, nil
}

// paymentPrice возвращает сумму для нового платежа. Тестовая цена администратора
// намеренно имеет приоритет над ценой пользователя, чтобы тест не создавал платёж
// по клиентскому тарифу. Ноль в конфигурации означает, что тестовая оплата
// отключена; пользовательская нулевая цена возвращается для её валидации.
func (b *Bot) paymentPrice(telegramID int64, user *database.User) (int, bool) {
	if b.config != nil && telegramID == b.config.AdminID && b.config.AdminTestPaymentPrice > 0 {
		return b.config.AdminTestPaymentPrice, true
	}
	if user == nil || user.SubscriptionPrice == nil {
		return 0, false
	}
	return *user.SubscriptionPrice, true
}

// checkPaymentStatus ручная проверка статуса платежа через Platega API.
// Защищён мьютексом по telegram_id для предотвращения race condition
// с параллельным callback от Platega.
func (b *Bot) checkPaymentStatus(telegramID int64) (string, error) {
	// Глобальная операция: помечаем протухшие PENDING как expired (не ждём scheduler).
	// Вызывается ДО захвата per-user mutex, т.к. операция не привязана к конкретному пользователю.
	b.db.ExpireOldPendingPayments()

	// Берём мьютекс ДО чтения из БД — та же блокировка, что и в callback
	mu := getPaymentMutex(telegramID)
	mu.Lock()
	defer mu.Unlock()

	pending, err := b.db.GetPendingPayment(telegramID)
	if err != nil {
		return "", fmt.Errorf("get pending: %w", err)
	}
	if pending == nil {
		return "not_found", nil
	}
	if pending.ProviderPaymentID == nil {
		return "pending", nil
	}
	provider, err := b.paymentProvider(pending.Provider)
	if err != nil {
		return "", err
	}
	status, err := provider.GetPayment(*pending.ProviderPaymentID)
	if err != nil {
		return "", fmt.Errorf("check status: %w", err)
	}
	if pending.Provider == paymentprovider.YooKassa {
		if err := b.verifyYooKassaPayment(pending, status); err != nil {
			return "", err
		}
	}

	if status.Status == paymentprovider.StatusSucceeded {
		// Платёж подтверждён — синхронизируем его без отдельного push-уведомления.
		handler := &paymentCallbackHandler{bot: b}
		if err := handler.handleConfirmedSilently(pending); err != nil {
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

	if status.Status == paymentprovider.StatusCanceled {
		handler := &paymentCallbackHandler{bot: b}
		if err := handler.handleCanceled(pending); err != nil {
			return "", err
		}
		if pending.Provider == paymentprovider.Platega {
			return platega.StatusCanceled, nil
		}
		return "canceled", nil
	}

	if status.Status == paymentprovider.StatusChargebacked {
		handler := &paymentCallbackHandler{bot: b}
		if err := handler.handleChargeback(pending); err != nil {
			return "", err
		}
		if pending.Provider == paymentprovider.Platega {
			return platega.StatusChargebacked, nil
		}
		return "chargebacked", nil
	}

	return status.Status, nil
}

func (b *Bot) verifyYooKassaPayment(payment *database.Payment, verified *paymentprovider.Payment) error {
	if payment.ProviderPaymentID == nil || verified.ID != *payment.ProviderPaymentID || verified.Amount != payment.Amount || verified.Currency != "RUB" || verified.RecipientID != b.config.YooKassaShopID {
		return fmt.Errorf("YooKassa payment verification mismatch: local_payment_id=%d", payment.ID)
	}
	if verified.PaymentMethod != "" {
		if err := b.db.UpdatePaymentMethod(payment.ID, verified.PaymentMethod); err != nil {
			return fmt.Errorf("update payment method: %w", err)
		}
	}
	// Единственная воронка, где сверенный ответ кассы встречается с локальным
	// платежом: и вебхук, и ручная проверка проходят здесь. Способ записывается
	// именно тут, чтобы два пути не разъехались.
	b.rememberAutorenewMethod(payment, verified)
	return nil
}

func (b *Bot) paymentProvider(name string) (paymentprovider.Provider, error) {
	switch name {
	case paymentprovider.Platega:
		if b.platega != nil {
			return platega.NewProvider(b.platega), nil
		}
	case paymentprovider.YooKassa:
		if b.yookassa != nil {
			return b.yookassa, nil
		}
	}
	return nil, fmt.Errorf("payment provider %q is not configured", name)
}
