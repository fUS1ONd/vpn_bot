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
	BtnInstWindows = "💻 Windows/Linux"
	BtnInstMac     = "🍏 macOS"

	// Кнопка серверов (мониторинг)
	BtnServers = "📡 Серверы"

	// Админ-кнопки
	BtnAdminManage       = "📋 Управление"
	BtnAdminBroadcast    = "📢 Рассылка"
	BtnAdminUserMode     = "👤 Режим пользователя"
	BtnAdminBack         = "🔙 В меню админа"
	BtnAdminCreateInvite = "🎟 Создать инвайт"
	BtnAdminViewInvites  = "📋 Коды"
	BtnAdminDeleteInvite = "🗑 Удалить код"
	BtnAdminBanUser      = "🚫 Забанить"

	// Кнопки рассылки — только активным (у кого есть доступ)
	BtnBroadcastActive = "📢 Рассылка активным"

	// Кнопки модератора
	BtnModInvites     = "🎟 Приглашения"
	BtnModCreate      = "📨 Создать приглашение"
	BtnModView        = "📋 Мои приглашения"
	BtnModSubscribers = "👥 Мои подписчики"
	BtnModExtend      = "⏳ Продлить подписку"
	BtnModDelete      = "🗑 Удалить приглашение"
	BtnModBack        = "🔙 В меню"

	// Админ-кнопки управления модераторами
	BtnAdminModerators   = "👥 Модераторы"
	BtnAdminAddModerator = "➕ Назначить модератора"
	BtnAdminListMods     = "📋 Список модераторов"
	BtnAdminModStats     = "📊 Статистика"
	BtnAdminRemoveMod    = "➖ Снять модератора"
)

// UserMenuKeyboard возвращает главное меню пользователя
func UserMenuKeyboard() *tele.ReplyMarkup {
	menu := &tele.ReplyMarkup{ResizeKeyboard: true}
	menu.Reply(
		menu.Row(menu.Text(BtnStatus), menu.Text(BtnConnect)),
		menu.Row(menu.Text(BtnServers), menu.Text(BtnInstructions)),
		menu.Row(menu.Text(BtnDonate)),
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
		menu.Row(menu.Text(BtnAdminBanUser), menu.Text(BtnAdminDeleteInvite)),
		menu.Row(menu.Text(BtnAdminModerators)),
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

// UserMenuKeyboardModerator возвращает меню пользователя с кнопкой приглашений (для модераторов)
func UserMenuKeyboardModerator() *tele.ReplyMarkup {
	menu := &tele.ReplyMarkup{ResizeKeyboard: true}
	menu.Reply(
		menu.Row(menu.Text(BtnStatus), menu.Text(BtnConnect)),
		menu.Row(menu.Text(BtnServers), menu.Text(BtnInstructions)),
		menu.Row(menu.Text(BtnModInvites)),
		menu.Row(menu.Text(BtnDonate)),
	)
	return menu
}

// ModeratorMenuKeyboard возвращает подменю модератора
func ModeratorMenuKeyboard() *tele.ReplyMarkup {
	menu := &tele.ReplyMarkup{ResizeKeyboard: true}
	menu.Reply(
		menu.Row(menu.Text(BtnModCreate)),
		menu.Row(menu.Text(BtnModView), menu.Text(BtnModSubscribers)),
		menu.Row(menu.Text(BtnModExtend)),
		menu.Row(menu.Text(BtnModDelete)),
		menu.Row(menu.Text(BtnModBack)),
	)
	return menu
}

// AdminModeratorKeyboard возвращает подменю управления модераторами
func AdminModeratorKeyboard() *tele.ReplyMarkup {
	menu := &tele.ReplyMarkup{ResizeKeyboard: true}
	menu.Reply(
		menu.Row(menu.Text(BtnAdminAddModerator)),
		menu.Row(menu.Text(BtnAdminListMods), menu.Text(BtnAdminModStats)),
		menu.Row(menu.Text(BtnAdminRemoveMod)),
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
