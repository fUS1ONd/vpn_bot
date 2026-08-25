package bot

import (
	"fmt"
	"html"
	"log/slog"

	tele "gopkg.in/telebot.v3"
)

// Экран сохранённого Способа автосписания и его отвязка.
//
// Живёт отдельно от экрана автопродления намеренно: ЮKassa согласовывает
// рекурренты только при наличии интерфейса, где покупатель сам отвязывает
// способ оплаты, не обращаясь в поддержку. Такой интерфейс должен называться
// своим именем и находиться без знания о том, что такое автосписание.

// handlePaymentMethod показывает сохранённый способ оплаты.
func (b *Bot) handlePaymentMethod(c tele.Context) error {
	telegramID := c.Sender().ID
	view, ok := b.loadAutorenewView(telegramID)
	if !ok {
		return c.RespondAlert("Сначала активируйте подписку")
	}

	hasMethod := view.methodTitle != ""
	if err := editWithInlineFallback(c, b.paymentMethodScreen(view), SavedMethodKeyboard(hasMethod)); err != nil {
		slog.Error("Не удалось показать экран способа оплаты", "error", err, "telegram_id", telegramID)
	}
	return c.Respond()
}

func (b *Bot) paymentMethodScreen(v autorenewView) string {
	if v.methodTitle == "" {
		return "<b>💳 Способ оплаты</b>\n\n" +
			"Сохранённого способа оплаты нет.\n\n" +
			"Он появится после оплаты картой или через СБП — тогда подписку можно будет продлевать автоматически."
	}

	msg := fmt.Sprintf("<b>💳 Способ оплаты</b>\n\nСохранён: <b>%s</b>\n", html.EscapeString(v.methodTitle))
	if v.consent {
		msg += fmt.Sprintf("\nПо нему включено автопродление: спишем %d ₽ за сутки до окончания подписки.\n", v.price)
	} else {
		msg += "\nАвтопродление по нему выключено — списаний не будет.\n"
	}
	msg += "\nВы можете отвязать способ оплаты в любой момент: мы перестанем его хранить, и списывать по нему будет нечем."
	return msg
}

// handlePaymentMethodUnlink показывает подтверждение отвязки: какой именно
// способ отвязывается и что при этом произойдёт.
func (b *Bot) handlePaymentMethodUnlink(c tele.Context) error {
	telegramID := c.Sender().ID
	view, ok := b.loadAutorenewView(telegramID)
	if !ok {
		return c.RespondAlert("Сначала активируйте подписку")
	}
	if view.methodTitle == "" {
		return c.RespondAlert("Сохранённого способа оплаты нет")
	}

	msg := fmt.Sprintf("<b>💳 Отвязать способ оплаты</b>\n\n"+
		"Отвязать <b>%s</b>?\n\n"+
		"После отвязки мы перестанем хранить этот способ, автопродление выключится, "+
		"и списывать по нему будет нечем.\n",
		html.EscapeString(view.methodTitle))
	if !view.expireAt.IsZero() {
		msg += fmt.Sprintf("\nДоступ сохранится до окончания оплаченного периода — <b>%s</b>. "+
			"Дальше подписку нужно будет продлевать вручную.", view.expireAt.Format("02.01.2006"))
	}

	if err := editWithInlineFallback(c, msg, SavedMethodUnlinkKeyboard()); err != nil {
		slog.Error("Не удалось показать подтверждение отвязки", "error", err, "telegram_id", telegramID)
	}
	return c.Respond()
}

// handlePaymentMethodUnlinkConfirm отвязывает способ оплаты.
//
// Гасится и Способ, и согласие: оставить согласие живым при отсутствующем
// Способе значит обещать списания, которых не будет. Заново способ сохранится
// только при следующей ручной оплате — вместе со свежим согласием, которое
// показывается на экране оплаты.
func (b *Bot) handlePaymentMethodUnlinkConfirm(c tele.Context) error {
	telegramID := c.Sender().ID

	if err := b.db.SetAutorenewEnabled(telegramID, false); err != nil {
		slog.Error("Не удалось выключить автопродление при отвязке", "error", err, "telegram_id", telegramID)
		return c.RespondAlert("Не удалось отвязать способ оплаты. Попробуйте позже.")
	}
	if err := b.db.ClearAutorenewMethod(telegramID); err != nil {
		slog.Error("Не удалось отвязать способ оплаты", "error", err, "telegram_id", telegramID)
		return c.RespondAlert("Не удалось отвязать способ оплаты. Попробуйте позже.")
	}
	slog.Info("Пользователь отвязал способ оплаты", "telegram_id", telegramID)

	msg := "<b>💳 Способ оплаты отвязан</b>\n\n" +
		"Мы больше не храним его, автопродление выключено. " +
		"Продлевать подписку теперь нужно вручную."
	if err := editWithInlineFallback(c, msg, SavedMethodKeyboard(false)); err != nil {
		slog.Error("Не удалось перерисовать экран после отвязки", "error", err, "telegram_id", telegramID)
	}
	return c.Respond(&tele.CallbackResponse{Text: "Способ оплаты отвязан"})
}
