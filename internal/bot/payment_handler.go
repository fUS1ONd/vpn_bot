package bot

import (
	"fmt"
	"log/slog"
	"time"

	"github.com/fus1ond/vpn_bot/internal/platega"
	tele "gopkg.in/telebot.v3"
)

// Состояния оплаты
const (
	StateWaitPaymentMethod = "wait_payment_method" // Ожидание выбора способа оплаты
	StateWaitPaymentResult = "wait_payment_result" // Ожидание оплаты (показана ссылка)
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

	// Показываем экран выбора способа оплаты
	b.userStates.Set(telegramID, StateWaitPaymentMethod)
	msg := fmt.Sprintf("💳 <b>Подписка на 1 месяц — %d руб.</b>\n\nВыберите способ оплаты:", price)
	msg += b.autorenewConsentNote()
	return c.Send(msg, &tele.SendOptions{
		ParseMode:             tele.ModeHTML,
		ReplyMarkup:           b.paymentMethodKeyboard(),
		DisableWebPagePreview: true,
	})
}

// handlePaymentMethodSelected обрабатывает выбор способа оплаты
func (b *Bot) handlePaymentMethodSelected(c tele.Context, provider string) error {
	telegramID := c.Sender().ID

	payment, redirectURL, err := b.createPaymentForProvider(telegramID, provider)
	if err != nil {
		slog.Error("Ошибка создания платежа", "error", err, "telegram_id", telegramID)

		// Обработка специфических ошибок
		if err.Error() == "subscription price not set" {
			return c.Send("❌ Цена подписки не установлена.", &tele.SendOptions{
				ReplyMarkup: b.userKeyboard(telegramID),
			})
		}

		b.userStates.Delete(telegramID)
		return c.Send("❌ Не удалось создать платёж. Попробуйте позже.", &tele.SendOptions{
			ReplyMarkup: b.userKeyboard(telegramID),
		})
	}

	b.userStates.Set(telegramID, StateWaitPaymentResult)

	msg := fmt.Sprintf("✅ <b>Платёж создан!</b>\n\n"+
		"Перейдите по ссылке для оплаты:\n%s\n\n"+
		"Сумма: <b>%d руб.</b>\n\n"+
		"После оплаты подписка будет активирована автоматически.\n"+
		"Обычно это занимает до 1 минуты.",
		redirectURL, payment.Amount)

	return c.Send(msg, &tele.SendOptions{
		ParseMode:   tele.ModeHTML,
		ReplyMarkup: PaymentWaitKeyboard(),
	})
}

// handleCheckPayment обрабатывает кнопку "Проверить оплату"
func (b *Bot) handleCheckPayment(c tele.Context) error {
	telegramID := c.Sender().ID

	status, err := b.checkPaymentStatus(telegramID)
	if err != nil {
		slog.Error("Ошибка проверки статуса платежа", "error", err, "telegram_id", telegramID)
		return c.Send("❌ Ошибка проверки. Попробуйте позже.", &tele.SendOptions{
			ReplyMarkup: PaymentWaitKeyboard(),
		})
	}

	switch status {
	case "confirmed":
		b.userStates.Delete(telegramID)
		// Разметка выбирается тем же хелпером, что и на пути вебхука: путей к
		// сообщению об успешной оплате два, и расходиться они не должны — в
		// частности, тестовый платёж админа автопродление не предлагает.
		return c.Send(b.paymentActivatedMessage(telegramID), &tele.SendOptions{
			ParseMode:   tele.ModeHTML,
			ReplyMarkup: b.paymentSuccessMarkupFor(telegramID, b.isTestPaymentUser(telegramID)),
		})
	case "confirmed_not_activated":
		b.userStates.Delete(telegramID)
		return c.Send("✅ Оплата подтверждена, но активация подписки ещё не завершена.\n\nМы повторим попытку автоматически и отдельно сообщим о результате.", &tele.SendOptions{
			ParseMode:   tele.ModeHTML,
			ReplyMarkup: b.userKeyboard(telegramID),
		})
	case "not_found":
		b.userStates.Delete(telegramID)
		return c.Send("Активных платежей не найдено.", &tele.SendOptions{
			ReplyMarkup: b.userKeyboard(telegramID),
		})
	case "canceled", platega.StatusCanceled:
		b.userStates.Delete(telegramID)
		return c.Send("❌ Платёж отменён. Вы можете попробовать снова.", &tele.SendOptions{
			ReplyMarkup: b.userKeyboard(telegramID),
		})
	case "chargebacked", platega.StatusChargebacked:
		b.userStates.Delete(telegramID)
		return c.Send("⚠️ По платежу выполнен возврат средств. Доступ будет отключён или уже отключён. Если это ошибка, обратитесь к администратору.", &tele.SendOptions{
			ReplyMarkup: b.userKeyboard(telegramID),
		})
	default:
		// pending или другой промежуточный статус
		return c.Send("⏳ Оплата пока не поступила. Подождите немного и проверьте снова.", &tele.SendOptions{
			ReplyMarkup: PaymentWaitKeyboard(),
		})
	}
}

// paymentMethodFromButton определяет метод оплаты по тексту кнопки
func paymentProviderFromButton(text string) (string, bool) {
	switch text {
	case BtnPayYooKassa:
		return "yookassa", true
	case BtnPayCrypto:
		return "platega", true
	default:
		return "", false
	}
}

func (b *Bot) paymentMethodKeyboard() *tele.ReplyMarkup {
	return PaymentMethodKeyboard(b.yookassa != nil, b.platega != nil)
}
