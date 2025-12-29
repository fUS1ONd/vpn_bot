package bot

import tele "gopkg.in/telebot.v3"

// Callback data constants
const (
	// Admin
	CallbackAdminList   = "admin_list"
	CallbackAdminCreate = "admin_create"
	CallbackAdminPromo  = "admin_promo"
)

// Text Constants for Reply Keyboards
const (
	BtnConnect      = "🌐 Подключить VPN"
	BtnStatus       = "👤 Мой статус"
	BtnInstructions = "📚 Инструкции"
	BtnPayment      = "💳 Оплата доступа"
	BtnPromo        = "🎁 Промокод"
	BtnSupport      = "🆘 Написать админу"
	BtnBack         = "🔙 Назад"
	BtnCancel       = "🚫 Отмена"

	// Instructions sub-menu
	BtnInstIOS     = "🍎 iOS"
	BtnInstAndroid = "🤖 Android"
	BtnInstWindows = "💻 Windows"
	BtnInstMac     = "🍏 macOS"

	// Payment sub-menu
	BtnPaySub     = "💎 Подписка (200р)"
	BtnBuyTraffic = "⚡ Доп. трафик (100р)"

	// Admin buttons
	BtnAdminList   = "👥 Клиенты"
	BtnAdminCreate = "➕ Создать"
	BtnAdminPromos = "🎫 Промокоды"
)

// inlineBtn creates an inline button with callback data
func inlineBtn(text, data string) tele.InlineButton {
	return tele.InlineButton{
		Text: text,
		Data: data,
	}
}

// MenuKeyboard returns the main menu reply keyboard
func MenuKeyboard() *tele.ReplyMarkup {
	menu := &tele.ReplyMarkup{ResizeKeyboard: true}
	menu.Reply(
		menu.Row(menu.Text(BtnPayment), menu.Text(BtnConnect)),
		menu.Row(menu.Text(BtnStatus), menu.Text(BtnPromo)),
		menu.Row(menu.Text(BtnSupport), menu.Text(BtnInstructions)),
	)
	return menu
}

// InstructionsReplyKeyboard returns keyboard with instruction options
func InstructionsReplyKeyboard() *tele.ReplyMarkup {
	menu := &tele.ReplyMarkup{ResizeKeyboard: true}
	menu.Reply(
		menu.Row(menu.Text(BtnInstIOS), menu.Text(BtnInstAndroid)),
		menu.Row(menu.Text(BtnInstWindows), menu.Text(BtnInstMac)),
		menu.Row(menu.Text(BtnBack)),
	)
	return menu
}

// PaymentReplyKeyboard returns keyboard with payment options
func PaymentReplyKeyboard() *tele.ReplyMarkup {
	menu := &tele.ReplyMarkup{ResizeKeyboard: true}
	menu.Reply(
		menu.Row(menu.Text(BtnPaySub)),
		menu.Row(menu.Text(BtnBuyTraffic)),
		menu.Row(menu.Text(BtnBack)),
	)
	return menu
}

// CancelReplyKeyboard returns keyboard with only Cancel button (for states)
func CancelReplyKeyboard() *tele.ReplyMarkup {
	menu := &tele.ReplyMarkup{ResizeKeyboard: true}
	menu.Reply(
		menu.Row(menu.Text(BtnCancel)),
	)
	return menu
}

// BackKeyboard returns keyboard with only back button
// Kept for Admin interactions if needed
func BackKeyboard() *tele.ReplyMarkup {
	return &tele.ReplyMarkup{
		InlineKeyboard: [][]tele.InlineButton{
			{inlineBtn("Назад", "back")},
		},
	}
}

// AdminKeyboard returns keyboard for admin menu
func AdminKeyboard() *tele.ReplyMarkup {
	menu := &tele.ReplyMarkup{ResizeKeyboard: true}
	menu.Reply(
		menu.Row(menu.Text(BtnAdminList), menu.Text(BtnAdminPromos)),
		menu.Row(menu.Text(BtnAdminCreate), menu.Text(BtnBack)),
	)
	return menu
}
