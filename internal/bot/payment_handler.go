package bot

import (
	"fmt"
	"log/slog"
	"strings"
	"time"

	"github.com/fus1ond/vpn_bot/internal/platega"
	tele "gopkg.in/telebot.v3"
)

// handlePayButton обрабатывает нажатие "Оплатить подписку" / "Продлить подписку"
func (b *Bot) handlePayButton(c tele.Context) error {
	telegramID := c.Sender().ID

	// Проверка режима обслуживания
	if b.isMaintenanceMode() {
		return c.Send("⚙️ Платёжная система временно на обслуживании. Попробуйте позже.", &tele.SendOptions{
			ReplyMarkup: b.userKeyboard(telegramID),
		})
	}

	if b.platega == nil && b.yookassa == nil {
		return c.Send("❌ Платёжная система не настроена.", &tele.SendOptions{
			ReplyMarkup: b.userKeyboard(telegramID),
		})
	}

	// Получаем пользователя
	user, err := b.db.GetUserByTelegramID(telegramID)
	if err != nil || user == nil {
		return c.Send(MsgNotRegistered, &tele.SendOptions{ParseMode: tele.ModeHTML})
	}

	price, ok := b.paymentPrice(telegramID, user)
	if !ok || price <= 0 {
		return c.Send("❌ Цена подписки не установлена. Обратитесь к администратору.", &tele.SendOptions{
			ReplyMarkup: b.userKeyboard(telegramID),
		})
	}

	// Проверка лимита 90 дней
	remUser, err := b.remnawave.GetUserByTelegramID(telegramID)
	if err == nil && remUser != nil && remUser.Status == "ACTIVE" && remUser.ExpireAt.Year() < 2099 {
		daysLeft := int(remUser.ExpireAt.Sub(time.Now().UTC()).Hours() / 24)
		if daysLeft >= 90 {
			msg := fmt.Sprintf("ℹ️ Подписка уже оплачена до <b>%s</b>.\nПродлить можно не раньше чем за 90 дней до окончания.",
				remUser.ExpireAt.Format("02.01.2006"))
			return c.Send(msg, &tele.SendOptions{
				ParseMode:   tele.ModeHTML,
				ReplyMarkup: b.userKeyboard(telegramID),
			})
		}
	}

	// Экран выбора способа всегда новым сообщением: с inline-кнопки сюда
	// приходят из сообщения об автосписании, и затирать его нельзя.
	return c.Send(b.paymentMethodScreenText(price), &tele.SendOptions{
		ParseMode:             tele.ModeHTML,
		ReplyMarkup:           b.paymentMethodKeyboard(),
		DisableWebPagePreview: true,
	})
}

// handlePaymentMethodSelected создаёт платёж выбранным способом и переводит
// платёжный экран в ожидание оплаты. Логика createPaymentForProvider не
// трогается: здесь меняется только то, как показан результат.
func (b *Bot) handlePaymentMethodSelected(c tele.Context, provider string) error {
	telegramID := c.Sender().ID

	// Кнопка способа живёт в чате сколько угодно: режим обслуживания мог
	// включиться уже после показа экрана.
	if b.isMaintenanceMode() {
		return b.closePaymentScreen(c, "⚙️ Платёжная система временно на обслуживании. Попробуйте позже.", nil)
	}

	payment, redirectURL, err := b.createPaymentForProvider(telegramID, provider)
	if err != nil {
		slog.Error("Ошибка создания платежа", "error", err, "telegram_id", telegramID)

		// Обработка специфических ошибок
		switch {
		case err.Error() == "subscription price not set":
			return b.closePaymentScreen(c, "❌ Цена подписки не установлена.", nil)
		case strings.HasPrefix(err.Error(), "subscription_too_far"):
			return b.closePaymentScreen(c, "ℹ️ Подписка уже оплачена надолго вперёд.\nПродлить можно не раньше чем за 90 дней до окончания.", nil)
		}

		return b.closePaymentScreen(c, b.errorExitText("❌ Не удалось создать платёж\n\nДеньги не списаны."),
			b.errorExitKeyboard(retryAction{unique: cbRetryPayment, data: provider}))
	}

	msg := b.paymentWaitScreenText(payment, redirectURL, time.Now().UTC())
	markup := PaymentWaitKeyboard(redirectURL, payment.Amount, payment.ID)
	if messageID, ok := b.showPaymentScreen(c, msg, markup); ok {
		b.paymentScreens.set(telegramID, payment.ID, messageID)
	}
	return nil
}

// handleCheckPayment — ручная проверка оплаты: «🔄 Я оплатил» на платёжном
// экране и reply-кнопка «Проверить оплату» из старых сообщений в истории.
func (b *Bot) handleCheckPayment(c tele.Context) error {
	telegramID := c.Sender().ID
	fromScreen := c.Callback() != nil

	status, err := b.checkPaymentStatus(telegramID)
	if err != nil {
		slog.Error("Ошибка проверки статуса платежа", "error", err, "telegram_id", telegramID)
		if fromScreen {
			// Экран с его кнопками остаётся как был — он по-прежнему верен.
			respondCallback(c)
		}
		return b.sendErrorExit(c, "❌ Не удалось проверить оплату\n\nЕсли вы уже оплатили, подписка включится сама в течение минуты.",
			retryAction{unique: cbRetryPaymentCheck})
	}

	switch status {
	case "confirmed", "confirmed_not_activated":
		msg := "✅ Оплата подтверждена, но активация подписки ещё не завершена.\n\nМы повторим попытку автоматически и отдельно сообщим о результате."
		// Разметка выбирается тем же хелпером, что и на пути вебхука: путей к
		// сообщению об успешной оплате два, и расходиться они не должны — в
		// частности, тестовый платёж админа автопродление не предлагает.
		markup := b.userKeyboard(telegramID)
		if status == "confirmed" {
			msg = b.paymentActivatedMessage(telegramID)
			markup = b.paymentSuccessMarkupFor(telegramID, b.isTestPaymentUser(telegramID))
		}
		if fromScreen {
			// Экран убираем, а итог шлём новым сообщением: с ним приходит
			// обновлённая reply-клавиатура («Оплатить» становится «Продлить»).
			b.dropScreenUnder(c)
			respondCallback(c)
		}
		return c.Send(msg, &tele.SendOptions{
			ParseMode:   tele.ModeHTML,
			ReplyMarkup: markup,
		})
	case "not_found":
		return b.finishCheck(c, "Активных платежей не найдено.")
	case "canceled", platega.StatusCanceled:
		return b.finishCheck(c, paymentScreenCanceledText)
	case "chargebacked", platega.StatusChargebacked:
		return b.finishCheck(c, "⚠️ По платежу выполнен возврат средств. Доступ будет отключён или уже отключён. Если это ошибка, обратитесь к администратору.")
	default:
		// pending или другой промежуточный статус: экран остаётся с кнопками.
		const notYet = "⏳ Оплата пока не поступила. Подождите немного и проверьте снова."
		if fromScreen {
			return c.Respond(&tele.CallbackResponse{Text: notYet, ShowAlert: true})
		}
		return c.Send(notYet, &tele.SendOptions{ReplyMarkup: b.userKeyboard(telegramID)})
	}
}

// finishCheck завершает проверку итогом, после которого платить нечего: на
// экране итог заменяет его, без экрана — приходит сообщением.
func (b *Bot) finishCheck(c tele.Context, msg string) error {
	telegramID := c.Sender().ID
	if c.Callback() == nil {
		return c.Send(msg, &tele.SendOptions{ReplyMarkup: b.userKeyboard(telegramID)})
	}
	b.forgetScreenUnder(c)
	err := b.closePaymentScreen(c, msg, nil)
	respondCallback(c)
	return err
}

// respondCallback гасит «часики» на нажатой кнопке. Ошибка ответа флоу не
// ломает: действие уже выполнено.
func respondCallback(c tele.Context) {
	if err := c.Respond(); err != nil {
		slog.Warn("Failed to respond to callback", "error", err, "telegram_id", c.Sender().ID)
	}
}

func (b *Bot) paymentMethodKeyboard() *tele.ReplyMarkup {
	return PaymentMethodKeyboard(b.yookassa != nil, b.platega != nil)
}

// handleRetryPayment повторяет создание платежа тем же способом, что выбрал
// пользователь: способ приходит в данных кнопки, а не берётся из состояния —
// сообщение об ошибке может пролежать дольше, чем живёт состояние в памяти.
func (b *Bot) handleRetryPayment(c tele.Context) error {
	args := c.Args()
	if len(args) == 0 || args[0] == "" {
		return c.RespondAlert("Некорректный запрос")
	}
	if err := c.Respond(); err != nil {
		slog.Warn("Failed to respond to payment retry", "error", err, "telegram_id", c.Sender().ID)
	}
	return b.handlePaymentMethodSelected(c, args[0])
}

// handleRetryPaymentCheck повторяет проверку оплаты. Нажатие приходит из
// сообщения об ошибке, и итог проверки заменяет его так же, как платёжный экран.
func (b *Bot) handleRetryPaymentCheck(c tele.Context) error {
	return b.handleCheckPayment(c)
}
