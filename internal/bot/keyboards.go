package bot

import (
	"fmt"

	"github.com/fus1ond/vpn_bot/internal/database"
	tele "gopkg.in/telebot.v3"
)

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
	BtnSeller       = "ℹ️ Реквизиты продавца"
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
	BtnAdminClients     = "👥 Клиенты"
	BtnAdminPromos      = "🎫 Промокоды"
	BtnAdminBroadcast   = "📢 Рассылка"
	BtnAdminUserMode    = "👤 Режим пользователя"
	BtnAdminBack        = "🔙 В меню админа"

	BtnAdminClientsList   = "📋 Список клиентов"
	BtnAdminClientsCreate = "➕ Создать клиента"
	BtnAdminClientsDelete = "➖ Удалить клиента"

	BtnAdminPromosList   = "📋 Список промо"
	BtnAdminPromosCreate = "➕ Создать промо"
	BtnAdminPromosDelete = "➖ Удалить промо"

	BtnBroadcastAll    = "📢 Всем пользователям"
	BtnBroadcastActive = "📢 Только активным"
)

// Status icons for dynamic buttons
const StatusActiveIcon = "🟢"
const StatusInactiveIcon = "🔵"

// inlineBtn creates an inline button with callback data
func inlineBtn(text, data string) tele.InlineButton {
	return tele.InlineButton{
		Text: text,
		Data: data,
	}
}

// MenuKeyboard returns the main menu reply keyboard
func MenuKeyboard(user *database.User) *tele.ReplyMarkup {
	menu := &tele.ReplyMarkup{ResizeKeyboard: true}

	infoText := fmt.Sprintf("%s Неактивна %s", StatusInactiveIcon, StatusInactiveIcon)
	if user != nil && (user.SubscriptionStatus == database.StatusActive || user.SubscriptionStatus == database.StatusTrial) {
		if user.SubscriptionEndAt != nil {
			infoText = fmt.Sprintf("%s до %s %s", StatusActiveIcon, user.SubscriptionEndAt.Format("02.01 15:04"), StatusActiveIcon)
		}
	}
	btnInfo := menu.Text(infoText)

	menu.Reply(
		menu.Row(btnInfo),
		menu.Row(menu.Text(BtnPayment), menu.Text(BtnConnect)),
		menu.Row(menu.Text(BtnStatus), menu.Text(BtnPromo)),
		menu.Row(menu.Text(BtnSupport), menu.Text(BtnInstructions)),
		menu.Row(menu.Text(BtnSeller)),
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
		menu.Row(menu.Text(BtnAdminClients), menu.Text(BtnAdminPromos)),
		menu.Row(menu.Text(BtnAdminBroadcast)),
		menu.Row(menu.Text(BtnAdminUserMode)),
	)
	return menu
}

// AdminClientsKeyboard returns keyboard for admin clients menu
func AdminClientsKeyboard() *tele.ReplyMarkup {
	menu := &tele.ReplyMarkup{ResizeKeyboard: true}
	menu.Reply(
		menu.Row(menu.Text(BtnAdminClientsList), menu.Text(BtnAdminClientsCreate), menu.Text(BtnAdminClientsDelete)),
		menu.Row(menu.Text(BtnAdminBack)),
	)
	return menu
}

// AdminPromosKeyboard returns keyboard for admin promos menu
func AdminPromosKeyboard() *tele.ReplyMarkup {
	menu := &tele.ReplyMarkup{ResizeKeyboard: true}
	menu.Reply(
		menu.Row(menu.Text(BtnAdminPromosList), menu.Text(BtnAdminPromosCreate), menu.Text(BtnAdminPromosDelete)),
		menu.Row(menu.Text(BtnAdminBack)),
	)
	return menu
}

// AdminBroadcastKeyboard returns keyboard for admin broadcast menu
func AdminBroadcastKeyboard() *tele.ReplyMarkup {
	menu := &tele.ReplyMarkup{ResizeKeyboard: true}
	menu.Reply(
		menu.Row(menu.Text(BtnBroadcastAll)),
		menu.Row(menu.Text(BtnBroadcastActive)),
		menu.Row(menu.Text(BtnAdminBack)),
	)
	return menu
}
