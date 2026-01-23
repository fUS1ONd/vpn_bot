package bot

import (
	tele "gopkg.in/telebot.v3"
)

// Текстовые константы кнопок
const (
	// Кнопки пользователя
	BtnStatus       = "👤 Мой статус"
	BtnConnect      = "🌐 Подключить"
	BtnDonate       = "💸 Поддержать"
	BtnInstructions = "📚 Инструкции"
	BtnBack         = "🔙 Назад"
	BtnCancel       = "🚫 Отмена"

	// Кнопки инструкций
	BtnInstIOS     = "🍎 iOS"
	BtnInstAndroid = "🤖 Android"
	BtnInstWindows = "💻 Windows"
	BtnInstMac     = "🍏 macOS"

	// Админ-кнопки
	BtnAdminManage       = "📋 Управление"
	BtnAdminBroadcast    = "📢 Рассылка"
	BtnAdminUserMode     = "👤 Режим пользователя"
	BtnAdminBack         = "🔙 В меню админа"
	BtnAdminCreateInvite = "🎟 Создать инвайт"
	BtnAdminViewInvites  = "📋 Коды"
	BtnAdminDeleteInvite = "🗑 Удалить код"
	BtnAdminAddTraffic   = "📊 Добавить трафик"
	BtnAdminBanUser      = "🚫 Забанить"

	// Кнопки рассылки — только активным (у кого есть доступ)
	BtnBroadcastActive = "📢 Рассылка активным"
)

// UserMenuKeyboard возвращает главное меню пользователя
func UserMenuKeyboard() *tele.ReplyMarkup {
	menu := &tele.ReplyMarkup{ResizeKeyboard: true}
	menu.Reply(
		menu.Row(menu.Text(BtnStatus), menu.Text(BtnConnect)),
		menu.Row(menu.Text(BtnDonate), menu.Text(BtnInstructions)),
	)
	return menu
}

// InstructionsKeyboard возвращает меню инструкций
func InstructionsKeyboard() *tele.ReplyMarkup {
	menu := &tele.ReplyMarkup{ResizeKeyboard: true}
	menu.Reply(
		menu.Row(menu.Text(BtnInstIOS), menu.Text(BtnInstAndroid)),
		menu.Row(menu.Text(BtnInstWindows), menu.Text(BtnInstMac)),
		menu.Row(menu.Text(BtnBack)),
	)
	return menu
}

// AdminKeyboard возвращает главное меню админа
func AdminKeyboard() *tele.ReplyMarkup {
	menu := &tele.ReplyMarkup{ResizeKeyboard: true}
	menu.Reply(
		menu.Row(menu.Text(BtnAdminManage), menu.Text(BtnAdminBroadcast)),
		menu.Row(menu.Text(BtnAdminUserMode)),
	)
	return menu
}

// AdminManageKeyboard возвращает меню управления
func AdminManageKeyboard() *tele.ReplyMarkup {
	menu := &tele.ReplyMarkup{ResizeKeyboard: true}
	menu.Reply(
		menu.Row(menu.Text(BtnAdminCreateInvite), menu.Text(BtnAdminViewInvites)),
		menu.Row(menu.Text(BtnAdminAddTraffic), menu.Text(BtnAdminBanUser)),
		menu.Row(menu.Text(BtnAdminDeleteInvite)),
		menu.Row(menu.Text(BtnAdminBack)),
	)
	return menu
}

// AdminBroadcastKeyboard возвращает меню рассылки
func AdminBroadcastKeyboard() *tele.ReplyMarkup {
	menu := &tele.ReplyMarkup{ResizeKeyboard: true}
	menu.Reply(
		menu.Row(menu.Text(BtnBroadcastActive)),
		menu.Row(menu.Text(BtnAdminBack)),
	)
	return menu
}

// CancelKeyboard возвращает клавиатуру с кнопкой отмены
func CancelKeyboard() *tele.ReplyMarkup {
	menu := &tele.ReplyMarkup{ResizeKeyboard: true}
	menu.Reply(
		menu.Row(menu.Text(BtnCancel)),
	)
	return menu
}
