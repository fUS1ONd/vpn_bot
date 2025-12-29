package bot

import tele "gopkg.in/telebot.v3"

// Callback data constants
const (
	// Main menu
	CallbackConnect      = "connect"
	CallbackStatus       = "status"
	CallbackInstructions = "instructions"
	CallbackBuyTraffic   = "buy_traffic"
	CallbackPromo        = "promo"
	CallbackSupport      = "support"
	CallbackPay          = "pay"
	CallbackBack         = "back"

	// Instructions
	CallbackInstructionIOS     = "instruction_ios"
	CallbackInstructionAndroid = "instruction_android"
	CallbackInstructionWindows = "instruction_windows"
	CallbackInstructionMac     = "instruction_mac"

	// Admin
	CallbackAdminList   = "admin_list"
	CallbackAdminCreate = "admin_create"
	CallbackAdminPromo  = "admin_promo"
)

// inlineBtn creates an inline button with callback data
func inlineBtn(text, data string) tele.InlineButton {
	return tele.InlineButton{
		Text: text,
		Data: data,
	}
}

// MainMenuKeyboard returns the main menu inline keyboard
func MainMenuKeyboard() *tele.ReplyMarkup {
	return &tele.ReplyMarkup{
		InlineKeyboard: [][]tele.InlineButton{
			{inlineBtn("Подключить VPN", CallbackConnect)},
			{inlineBtn("Мой статус", CallbackStatus)},
			{inlineBtn("Инструкции", CallbackInstructions)},
			{inlineBtn("Докупить трафик", CallbackBuyTraffic), inlineBtn("Промокод", CallbackPromo)},
			{inlineBtn("Поддержка", CallbackSupport)},
		},
	}
}

// ActiveUserKeyboard returns keyboard for users with active subscription
func ActiveUserKeyboard() *tele.ReplyMarkup {
	return &tele.ReplyMarkup{
		InlineKeyboard: [][]tele.InlineButton{
			{inlineBtn("Мой статус", CallbackStatus)},
			{inlineBtn("Инструкции", CallbackInstructions)},
			{inlineBtn("Докупить трафик", CallbackBuyTraffic), inlineBtn("Промокод", CallbackPromo)},
			{inlineBtn("Поддержка", CallbackSupport)},
		},
	}
}

// PaymentKeyboard returns keyboard with payment options
func PaymentKeyboard() *tele.ReplyMarkup {
	return &tele.ReplyMarkup{
		InlineKeyboard: [][]tele.InlineButton{
			{inlineBtn("Оплатить 200 руб/мес", CallbackPay)},
			{inlineBtn("Назад", CallbackBack)},
		},
	}
}

// TrialKeyboard returns keyboard for trial activation
func TrialKeyboard() *tele.ReplyMarkup {
	return &tele.ReplyMarkup{
		InlineKeyboard: [][]tele.InlineButton{
			{inlineBtn("Активировать триал (3 дня)", CallbackConnect)},
			{inlineBtn("Назад", CallbackBack)},
		},
	}
}

// InstructionsKeyboard returns keyboard with instruction options
func InstructionsKeyboard() *tele.ReplyMarkup {
	return &tele.ReplyMarkup{
		InlineKeyboard: [][]tele.InlineButton{
			{inlineBtn("iOS (iPhone/iPad)", CallbackInstructionIOS)},
			{inlineBtn("Android", CallbackInstructionAndroid)},
			{inlineBtn("Windows", CallbackInstructionWindows)},
			{inlineBtn("macOS", CallbackInstructionMac)},
			{inlineBtn("Назад", CallbackBack)},
		},
	}
}

// BackKeyboard returns keyboard with only back button
func BackKeyboard() *tele.ReplyMarkup {
	return &tele.ReplyMarkup{
		InlineKeyboard: [][]tele.InlineButton{
			{inlineBtn("Назад", CallbackBack)},
		},
	}
}

// BuyTrafficKeyboard returns keyboard for buying extra traffic
func BuyTrafficKeyboard() *tele.ReplyMarkup {
	return &tele.ReplyMarkup{
		InlineKeyboard: [][]tele.InlineButton{
			{inlineBtn("Купить +10GB за 100 руб", CallbackBuyTraffic)},
			{inlineBtn("Назад", CallbackBack)},
		},
	}
}

// AdminKeyboard returns keyboard for admin menu
func AdminKeyboard() *tele.ReplyMarkup {
	return &tele.ReplyMarkup{
		InlineKeyboard: [][]tele.InlineButton{
			{inlineBtn("Список клиентов", CallbackAdminList)},
			{inlineBtn("Создать клиента", CallbackAdminCreate)},
			{inlineBtn("Управление промокодами", CallbackAdminPromo)},
		},
	}
}

// PromoInputKeyboard returns keyboard when waiting for promo input
func PromoInputKeyboard() *tele.ReplyMarkup {
	return &tele.ReplyMarkup{
		InlineKeyboard: [][]tele.InlineButton{
			{inlineBtn("Отмена", CallbackBack)},
		},
	}
}
