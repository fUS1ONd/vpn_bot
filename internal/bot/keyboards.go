package bot

import (
	tele "gopkg.in/telebot.v3"
)

// Текстовые константы кнопок
const (
	// Кнопки пользователя
	BtnStatus       = "👤 Мой статус"
	BtnInfo         = "ℹ️ Информация"
	BtnInstructions = "📚 Инструкции"
	BtnBack         = "🔙 Назад"
	BtnCancel       = "🚫 Отмена"

	// Кнопки оплаты
	BtnPay          = "💳 Оплатить подписку"
	BtnRenew        = "💳 Продлить подписку"
	BtnPaySBP       = "🏦 СБП"
	BtnPayCard      = "💳 Карта"
	BtnPayCrypto    = "🪙 Крипта"
	BtnCheckPayment = "🔄 Проверить оплату"

	// Кнопки инструкций
	BtnInstIOS     = "🍎 iOS"
	BtnInstAndroid = "🤖 Android"
	BtnInstDesktop = "💻ПК"

	// Кнопка серверов (мониторинг)
	BtnServers = "📡 Серверы"

	// Админ-кнопки
	BtnAdminManage             = "📋 Управление"
	BtnAdminBroadcast          = "📢 Рассылка"
	BtnAdminStats              = "📊 Общая статистика"
	BtnAdminMaintenance        = "🔧 Режим обслуживания"
	BtnAdminMaintenanceOff     = "▶️ Штатный режим"
	BtnAdminUserMode           = "👤 Режим пользователя"
	BtnAdminBack               = "🔙 В меню админа"
	BtnAdminCreateInvite       = "🎟 Создать инвайт"
	BtnAdminViewInvites        = "📋 Коды"
	BtnAdminDeleteInvite       = "🗑 Удалить код"
	BtnAdminBanUser            = "🚫 Забанить"
	BtnAdminUserInfo           = "🔍 Инфо о пользователе"
	BtnAdminSwitchSubscription = "♾️ Сменить тариф"
	BtnAdminSwitchInfinite     = "♾️ Перевести на бессрочную"
	BtnAdminChangePrice        = "✏️ Изменить цену"
	BtnAdminMigrationPaidYes   = "✅ Да, считать оплаченной"
	BtnAdminMigrationPaidNo    = "❌ Нет, оставить trial"

	// Кнопки подтверждения
	BtnConfirmYes = "Да"

	// Кнопки рассылки — только активным (у кого есть доступ)
	BtnBroadcastActive = "📢 Рассылка активным"

	// Кнопки модератора
	BtnModInvites     = "🎟 Приглашения"
	BtnModCreate      = "📨 Создать приглашение"
	BtnModView        = "📋 Мои приглашения"
	BtnModSubscribers = "👥 Мои подписчики"
	BtnModEarnings    = "💰 Мой заработок"
	BtnModChangePrice = "✏️ Изменить цену"
	BtnModDelete      = "🗑 Удалить приглашение"
	BtnModBack        = "🔙 В меню"

	// Админ-кнопки управления модераторами
	BtnAdminModerators   = "👥 Модераторы"
	BtnAdminAddModerator = "➕ Назначить модератора"
	BtnAdminListMods     = "📋 Список модераторов"
	BtnAdminModStats     = "📊 Статистика"
	BtnAdminRemoveMod    = "➖ Снять модератора"
)

// UserMenuKeyboardDynamic строит главное меню с динамической кнопкой оплаты.
// payButtonText — текст кнопки ("Оплатить" / "Продлить"), showPayButton — показывать ли,
// isModerator — добавляет кнопку "Приглашения".
func UserMenuKeyboardDynamic(payButtonText string, showPayButton bool, isModerator bool) *tele.ReplyMarkup {
	menu := &tele.ReplyMarkup{ResizeKeyboard: true}
	rows := []tele.Row{
		menu.Row(menu.Text(BtnStatus)),
	}
	if showPayButton && payButtonText != "" {
		rows = append(rows, menu.Row(menu.Text(payButtonText), menu.Text(BtnServers)))
	} else {
		rows = append(rows, menu.Row(menu.Text(BtnServers)))
	}
	rows = append(rows, menu.Row(menu.Text(BtnInstructions), menu.Text(BtnInfo)))
	if isModerator {
		rows = append(rows, menu.Row(menu.Text(BtnModInvites)))
	}
	menu.Reply(rows...)
	return menu
}

// InstructionsKeyboard возвращает меню инструкций
func InstructionsKeyboard() *tele.ReplyMarkup {
	menu := &tele.ReplyMarkup{ResizeKeyboard: true}
	menu.Reply(
		menu.Row(menu.Text(BtnInstIOS), menu.Text(BtnInstAndroid)),
		menu.Row(menu.Text(BtnInstDesktop)),
		menu.Row(menu.Text(BtnBack)),
	)
	return menu
}

// AdminKeyboard возвращает главное меню админа
func AdminKeyboard(maintenanceMode bool) *tele.ReplyMarkup {
	menu := &tele.ReplyMarkup{ResizeKeyboard: true}
	maintenanceBtn := BtnAdminMaintenance
	if maintenanceMode {
		maintenanceBtn = BtnAdminMaintenanceOff
	}
	menu.Reply(
		menu.Row(menu.Text(BtnAdminManage), menu.Text(BtnAdminModerators)),
		menu.Row(menu.Text(BtnAdminBroadcast), menu.Text(BtnAdminStats)),
		menu.Row(menu.Text(maintenanceBtn)),
		menu.Row(menu.Text(BtnAdminUserMode)),
	)
	return menu
}

// AdminManageKeyboard возвращает меню управления (инвайты + действия с пользователями)
func AdminManageKeyboard() *tele.ReplyMarkup {
	menu := &tele.ReplyMarkup{ResizeKeyboard: true}
	menu.Reply(
		menu.Row(menu.Text(BtnAdminCreateInvite), menu.Text(BtnAdminViewInvites)),
		menu.Row(menu.Text(BtnAdminBanUser), menu.Text(BtnAdminDeleteInvite)),
		menu.Row(menu.Text(BtnAdminSwitchSubscription)),
		menu.Row(menu.Text(BtnAdminUserInfo)),
		menu.Row(menu.Text(BtnAdminBack)),
	)
	return menu
}

// AdminSwitchSubmenu возвращает подменю смены тарифа.
func AdminSwitchSubmenu() *tele.ReplyMarkup {
	menu := &tele.ReplyMarkup{ResizeKeyboard: true}
	menu.Reply(
		menu.Row(menu.Text(BtnAdminSwitchInfinite)),
		menu.Row(menu.Text(BtnAdminChangePrice)),
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

// ModeratorMenuKeyboard возвращает подменю модератора
func ModeratorMenuKeyboard() *tele.ReplyMarkup {
	menu := &tele.ReplyMarkup{ResizeKeyboard: true}
	menu.Reply(
		menu.Row(menu.Text(BtnModCreate)),
		menu.Row(menu.Text(BtnModView), menu.Text(BtnModSubscribers)),
		menu.Row(menu.Text(BtnModEarnings), menu.Text(BtnModDelete)),
		menu.Row(menu.Text(BtnModBack)),
	)
	return menu
}

// ModeratorSubscribersKeyboard возвращает клавиатуру для списка подписчиков модератора.
func ModeratorSubscribersKeyboard() *tele.ReplyMarkup {
	menu := &tele.ReplyMarkup{ResizeKeyboard: true}
	menu.Reply(
		menu.Row(menu.Text(BtnModChangePrice), menu.Text(BtnBack)),
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

// AdminChangePriceMigrationKeyboard возвращает меню подтверждения migration-case.
func AdminChangePriceMigrationKeyboard() *tele.ReplyMarkup {
	menu := &tele.ReplyMarkup{ResizeKeyboard: true}
	menu.Reply(
		menu.Row(menu.Text(BtnAdminMigrationPaidYes), menu.Text(BtnAdminMigrationPaidNo)),
		menu.Row(menu.Text(BtnCancel)),
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

// ConfirmKeyboard возвращает клавиатуру подтверждения.
func ConfirmKeyboard() *tele.ReplyMarkup {
	menu := &tele.ReplyMarkup{ResizeKeyboard: true}
	menu.Reply(
		menu.Row(menu.Text(BtnConfirmYes), menu.Text(BtnCancel)),
	)
	return menu
}

// PaymentMethodKeyboard возвращает меню выбора способа оплаты
func PaymentMethodKeyboard() *tele.ReplyMarkup {
	menu := &tele.ReplyMarkup{ResizeKeyboard: true}
	menu.Reply(
		menu.Row(menu.Text(BtnPaySBP), menu.Text(BtnPayCard)),
		menu.Row(menu.Text(BtnPayCrypto), menu.Text(BtnCancel)),
	)
	return menu
}

// PaymentWaitKeyboard возвращает меню ожидания оплаты
func PaymentWaitKeyboard() *tele.ReplyMarkup {
	menu := &tele.ReplyMarkup{ResizeKeyboard: true}
	menu.Reply(
		menu.Row(menu.Text(BtnCheckPayment), menu.Text(BtnCancel)),
	)
	return menu
}
