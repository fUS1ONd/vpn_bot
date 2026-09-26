package bot

import (
	"errors"
	"fmt"
	"html"
	"log/slog"
	"strconv"
	"sync"
	"time"

	"github.com/fus1ond/vpn_bot/internal/database"
	"github.com/fus1ond/vpn_bot/internal/paymentprovider"
	tele "gopkg.in/telebot.v3"
)

// Платёжный экран — одно сообщение, которое проходит оба шага оплаты: выбор
// способа и ожидание оплаты. Вся навигация — inline-кнопками под ним, поэтому
// флоу не держит состояний в памяти и переживает перезапуск бота.

// paymentScreenTracker помнит, в каком сообщении показано ожидание оплаты по
// платежу. Нужен вебхуку: оплата, подтверждённая кассой, должна снять с экрана
// кнопки «Оплатить» и «Я оплатил», иначе под сообщением останутся действия по
// закрытому платежу.
//
// Хранилище in-memory намеренно, как у cardTracker: забытый после перезапуска
// экран лишь сохранит свои кнопки, а «Я оплатил» на нём честно ответит, что
// активных платежей нет.
type paymentScreenTracker struct {
	mu      sync.Mutex
	screens map[int64]paymentScreen
}

type paymentScreen struct {
	paymentID int64
	messageID int
}

func (t *paymentScreenTracker) set(telegramID, paymentID int64, messageID int) {
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.screens == nil {
		t.screens = make(map[int64]paymentScreen)
	}
	t.screens[telegramID] = paymentScreen{paymentID: paymentID, messageID: messageID}
}

// take забирает экран платежа. Экран другого платежа не трогается: отмена
// брошенного платежа не должна гасить экран того, что человек завёл взамен.
func (t *paymentScreenTracker) take(telegramID, paymentID int64) (int, bool) {
	t.mu.Lock()
	defer t.mu.Unlock()
	screen, ok := t.screens[telegramID]
	if !ok || screen.paymentID != paymentID {
		return 0, false
	}
	delete(t.screens, telegramID)
	return screen.messageID, true
}

// forgetMessage забывает экран, только если он показан именно в этом
// сообщении: нажатие на старом сообщении не должно отвязывать живой экран
// нового платежа — иначе вебхук не снимет с него кнопки.
func (t *paymentScreenTracker) forgetMessage(telegramID int64, messageID int) {
	t.mu.Lock()
	defer t.mu.Unlock()
	if screen, ok := t.screens[telegramID]; ok && screen.messageID == messageID {
		delete(t.screens, telegramID)
	}
}

// forgetScreenUnder забывает экран, на котором нажата кнопка.
func (b *Bot) forgetScreenUnder(c tele.Context) {
	if m := c.Message(); m != nil {
		b.paymentScreens.forgetMessage(c.Sender().ID, m.ID)
	}
}

// newPaymentScreenEditor собирает шов редактирования платёжного экрана вне
// контекста апдейта: вебхук приходит не от пользователя. Редактирование без
// reply_markup снимает inline-клавиатуру.
func newPaymentScreenEditor(api *tele.Bot) func(chatID int64, messageID int, text string) error {
	return func(chatID int64, messageID int, text string) error {
		_, err := api.Edit(&tele.StoredMessage{
			MessageID: strconv.Itoa(messageID),
			ChatID:    chatID,
		}, text, &tele.SendOptions{ParseMode: tele.ModeHTML})
		return err
	}
}

const (
	paymentScreenPaidText     = "✅ Оплата получена."
	paymentScreenCanceledText = "❌ Платёж отменён. Вы можете попробовать снова."
)

// retirePaymentScreen снимает кнопки с экрана ожидания оплаты по платежу,
// заменяя его текст итогом. Best-effort: не отредактировалось — сообщение об
// итоге всё равно придёт отдельно, если оно предусмотрено.
func (b *Bot) retirePaymentScreen(payment *database.Payment, text string) bool {
	messageID, ok := b.paymentScreens.take(payment.TelegramID, payment.ID)
	if !ok || b.editPaymentScreen == nil {
		return false
	}
	err := b.editPaymentScreen(payment.TelegramID, messageID, text)
	if err != nil && !errors.Is(err, tele.ErrSameMessageContent) {
		slog.Warn("Не удалось закрыть платёжный экран",
			"error", err, "telegram_id", payment.TelegramID, "payment_id", payment.ID)
		return false
	}
	return true
}

// moscowLocation — часовой пояс, в котором пользователю называется время.
func moscowLocation() *time.Location {
	location, err := time.LoadLocation("Europe/Moscow")
	if err != nil {
		return time.FixedZone("МСК", 3*60*60)
	}
	return location
}

// paymentLinkDeadline — срок жизни ссылки на оплату для текста экрана: «15:04 МСК»,
// а если срок не сегодня — с датой. Пустая строка значит «срок неизвестен или
// уже прошёл»: фразу о сроке тогда опускаем целиком, а не выдумываем значение.
func paymentLinkDeadline(expiresAt *time.Time, now time.Time) string {
	if expiresAt == nil || !expiresAt.After(now) {
		return ""
	}
	location := moscowLocation()
	deadline := expiresAt.In(location)
	if deadline.Format("2006-01-02") == now.In(location).Format("2006-01-02") {
		return deadline.Format("15:04 МСК")
	}
	return deadline.Format("02.01 15:04 МСК")
}

// paymentMethodScreenText — шаг выбора способа. Абзац согласия на автосписание
// перенесён сюда из reply-флоу дословно.
func (b *Bot) paymentMethodScreenText(price int) string {
	return fmt.Sprintf("💳 <b>Подписка на 1 месяц — %d руб.</b>\n\nВыберите способ оплаты:", price) +
		b.autorenewConsentNote()
}

// paymentWaitScreenText — шаг ожидания оплаты.
func (b *Bot) paymentWaitScreenText(payment *database.Payment, payURL string, now time.Time) string {
	msg := fmt.Sprintf("💳 <b>Подписка на 1 месяц — %d руб.</b>\n\n", payment.Amount)
	if isValidSubscriptionURL(payURL) {
		msg += "Нажмите «💳 Оплатить», чтобы перейти к оплате."
	} else {
		// URL-кнопки не будет — ссылка остаётся в тексте, иначе платить нечем.
		msg += "Перейдите по ссылке для оплаты:\n" + html.EscapeString(payURL)
	}
	if deadline := paymentLinkDeadline(payment.ExpiresAt, now); deadline != "" {
		msg += "\n\nСсылка на оплату действует до " + deadline + " — можно вернуться и доплатить."
	}
	msg += "\n\nПосле оплаты подписка включится автоматически, обычно в течение минуты."
	// Абзац согласия живёт до редиректа: касса сохраняет способ без вопросов, и
	// экран с кнопкой «Оплатить» — последнее место, где человек об этом узнаёт.
	if payment.Provider == paymentprovider.YooKassa {
		msg += b.autorenewConsentNote()
	}
	return msg
}

// showPaymentScreen показывает шаг платёжного экрана: с inline-кнопки
// редактирует сообщение под пальцем, иначе присылает новое. Возвращает id
// сообщения, если он известен.
func (b *Bot) showPaymentScreen(c tele.Context, msg string, markup *tele.ReplyMarkup) (int, bool) {
	opts := &tele.SendOptions{ParseMode: tele.ModeHTML, ReplyMarkup: markup, DisableWebPagePreview: true}
	if c.Callback() != nil && c.Message() != nil {
		err := c.Edit(msg, opts)
		if err == nil || errors.Is(err, tele.ErrSameMessageContent) {
			return c.Message().ID, true
		}
		slog.Warn("Не удалось отредактировать платёжный экран, отправляем новый",
			"error", err, "telegram_id", c.Sender().ID)
	}
	if err := c.Send(msg, opts); err != nil {
		slog.Error("Не удалось отправить платёжный экран", "error", err, "telegram_id", c.Sender().ID)
	}
	return 0, false
}

// closePaymentScreen заменяет текст экрана под пальцем, снимая клавиатуру.
// Вне inline-нажатия (старая reply-кнопка) экрана под пальцем нет — тогда
// текст просто отправляется.
func (b *Bot) closePaymentScreen(c tele.Context, msg string, markup *tele.ReplyMarkup) error {
	if c.Callback() != nil && c.Message() != nil {
		err := c.Edit(msg, &tele.SendOptions{ParseMode: tele.ModeHTML, ReplyMarkup: markup})
		if err == nil || errors.Is(err, tele.ErrSameMessageContent) {
			return nil
		}
		slog.Warn("Не удалось закрыть платёжный экран, отправляем итог отдельно",
			"error", err, "telegram_id", c.Sender().ID)
	}
	return c.Send(msg, &tele.SendOptions{ParseMode: tele.ModeHTML, ReplyMarkup: markup})
}

// handlePayMethodCallback — выбор способа оплаты inline-кнопкой.
func (b *Bot) handlePayMethodCallback(c tele.Context) error {
	provider := c.Data()
	if provider != paymentprovider.YooKassa && provider != paymentprovider.Platega {
		return c.RespondAlert("Некорректный запрос")
	}
	if err := c.Respond(); err != nil {
		slog.Warn("Failed to respond to payment method", "error", err, "telegram_id", c.Sender().ID)
	}
	return b.handlePaymentMethodSelected(c, provider)
}

// handlePayCheckCallback — «Я оплатил». Путь проверки тот же, что у старой
// reply-кнопки «Проверить оплату».
func (b *Bot) handlePayCheckCallback(c tele.Context) error {
	return b.handleCheckPayment(c)
}

// handlePayCancelCallback — «Отмена». Платёж у кассы при этом не отменяется:
// он живёт своей жизнью, и деньги по ссылке пройдут. Кнопка только убирает
// клавиатуру и честно говорит, что ссылка ещё действует.
func (b *Bot) handlePayCancelCallback(c tele.Context) error {
	telegramID := c.Sender().ID
	b.forgetScreenUnder(c)

	msg := "Оплата отменена."
	if raw := c.Data(); raw != "" {
		paymentID, err := strconv.ParseInt(raw, 10, 64)
		if err != nil {
			return c.RespondAlert("Некорректный запрос")
		}
		msg = b.paymentPostponedText(telegramID, paymentID)
	}

	if err := c.Edit(msg, &tele.SendOptions{ParseMode: tele.ModeHTML}); err != nil && !errors.Is(err, tele.ErrSameMessageContent) {
		slog.Warn("Не удалось закрыть платёжный экран", "error", err, "telegram_id", telegramID)
	}
	return c.Respond()
}

// paymentPostponedText — итог «Отмены» на шаге ожидания оплаты. Платёж
// перечитывается: кнопка лежит в чате сколько угодно, и к нажатию он мог уже
// закрыться.
func (b *Bot) paymentPostponedText(telegramID, paymentID int64) string {
	payment, err := b.db.GetPaymentByID(paymentID)
	if err != nil {
		slog.Warn("Не удалось прочитать платёж при отмене экрана", "error", err, "payment_id", paymentID)
	}
	if payment == nil || payment.TelegramID != telegramID || payment.Status != "pending" {
		return "Экран оплаты закрыт."
	}
	if deadline := paymentLinkDeadline(payment.ExpiresAt, time.Now().UTC()); deadline != "" {
		return "Оплата отложена.\n\nСсылка на оплату действует до " + deadline + " — можно вернуться и доплатить из меню."
	}
	return "Оплата отложена.\n\nПлатёж не отменён — можно вернуться и доплатить из меню."
}
