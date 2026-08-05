package bot

import (
	"fmt"
	"net/url"

	tele "gopkg.in/telebot.v3"

	"github.com/fus1ond/vpn_bot/internal/database"
	"github.com/fus1ond/vpn_bot/internal/remnawave"
)

// Unique-идентификаторы inline-кнопок управления устройствами
const (
	cbDevicesManage          = "dev_manage"
	cbDeviceDelete           = "dev_del"
	cbDevicesResetAll        = "dev_reset_all"
	cbDevicesResetAllConfirm = "dev_reset_all_ok"
)

// Unique-идентификаторы inline-кнопок карточки подписки
const (
	cbSubRevoke        = "sub_revoke"        // запрос перевыпуска ссылки
	cbSubRevokeConfirm = "sub_revoke_ok"     // подтверждение перевыпуска
	cbSubRevokeCancel  = "sub_revoke_cancel" // отмена перевыпуска, возврат карточки
	cbSubCard          = "sub_card"          // возврат к карточке подписки
)

// Unique-идентификаторы inline-кнопок багрепорта
const (
	cbBugServer     = "bug_server"      // переключение выбора сервера (Data = индекс хоста)
	cbBugServerDone = "bug_server_done" // завершить выбор серверов (Data="" — готово, "none" — все/не знаю)
	cbBugCategory   = "bug_category"    // выбор категории (Data = код категории)
	cbBugCancel     = "bug_cancel"
)

// Unique-идентификаторы inline-кнопок ручного продления подписки админом
const (
	cbAdminExtendMonth      = "adm_ext_month"  // запрос продления (Data = targetID)
	cbAdminExtendConfirm    = "adm_ext_ok"     // подтверждение (Data = targetID)
	cbAdminExtendCancel     = "adm_ext_cancel" // отмена (Data = targetID)
	cbReferralResend        = "ref_resend"
	cbReferralRevoke        = "ref_revoke"
	cbReferralRevokeOK      = "ref_revoke_ok"
	cbReferralPage          = "ref_page"
	cbReferralBack          = "ref_back"
	cbAdminReferralOverview = "adm_ref_overview"
	cbAdminReferralLeaders  = "adm_ref_leaders"
	cbAdminUserReferrals    = "adm_user_refs"
	cbAdminReferralRevoke   = "adm_ref_revoke"
	cbAdminReferralRevokeOK = "adm_ref_revoke_ok"
	cbAdminReferralBack     = "adm_ref_back"
)

// Текстовые константы кнопок
const (
	// Кнопки пользователя
	BtnStatus = "👤 Моя подписка"
	BtnInfo   = "ℹ️ Информация"
	BtnBack   = "🔙 Назад"
	BtnCancel = "🚫 Отмена"

	// Кнопки багрепорта
	BtnBugReport   = "🛠 Сообщить о проблеме"
	BtnBugSkip     = "⏭ Пропустить"
	BtnBugNoServer = "🤷 Не знаю / все сразу"

	// Кнопки оплаты
	BtnPay          = "💳 Оплатить подписку"
	BtnRenew        = "💳 Продлить подписку"
	BtnPayYooKassa  = "⚡ Карта / СБП / SberPay"
	BtnPayCrypto    = "🪙 Крипта"
	BtnCheckPayment = "🔄 Проверить оплату"

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

	// Общая система приглашений
	BtnInvites      = "🎟 Приглашения"
	BtnInviteCreate = "📨 Создать приглашение"
	BtnInviteList   = "📋 Мои приглашения"
	BtnInviteBack   = "🔙 В меню"

	// Админ-кнопки статистики приглашений
	BtnAdminReferrals        = "🤝 Приглашения"
	BtnAdminReferralOverview = "📊 Обзор"
	BtnAdminReferralLeaders  = "🏆 Кто приглашает"
)

// UserMenuKeyboardDynamic строит главное меню с динамической кнопкой оплаты.
// payButtonText — текст кнопки ("Оплатить" / "Продлить"), showPayButton — показывать ли,
// showInvites — добавляет кнопку "Приглашения".
func UserMenuKeyboardDynamic(payButtonText string, showPayButton bool, showInvites bool) *tele.ReplyMarkup {
	menu := &tele.ReplyMarkup{ResizeKeyboard: true}
	rows := []tele.Row{
		menu.Row(menu.Text(BtnStatus)),
	}
	if showPayButton && payButtonText != "" {
		rows = append(rows, menu.Row(menu.Text(payButtonText), menu.Text(BtnServers)))
	} else {
		rows = append(rows, menu.Row(menu.Text(BtnServers)))
	}
	rows = append(rows, menu.Row(menu.Text(BtnInfo)))
	rows = append(rows, menu.Row(menu.Text(BtnBugReport)))
	if showInvites {
		rows = append(rows, menu.Row(menu.Text(BtnInvites)))
	}
	menu.Reply(rows...)
	return menu
}

// InvitesMenuKeyboard — общее меню приглашений без moderator-функций.
func InvitesMenuKeyboard() *tele.ReplyMarkup {
	menu := &tele.ReplyMarkup{ResizeKeyboard: true}
	menu.Reply(
		menu.Row(menu.Text(BtnInviteCreate)),
		menu.Row(menu.Text(BtnInviteList)),
		menu.Row(menu.Text(BtnInviteBack)),
	)
	return menu
}

// ReferralInvitesKeyboard строит действия для активных ссылок и пагинацию истории.
func ReferralInvitesKeyboard(invites []database.Invite, page int, hasNext bool) *tele.ReplyMarkup {
	menu := &tele.ReplyMarkup{}
	var rows []tele.Row
	for _, invite := range invites {
		resend := menu.Data("📨 "+invite.Code, cbReferralResend, invite.Code)
		revoke := menu.Data("🗑 Отозвать", cbReferralRevoke, invite.Code)
		rows = append(rows, menu.Row(resend, revoke))
	}
	var nav tele.Row
	if page > 0 {
		nav = append(nav, menu.Data("◀️", cbReferralPage, fmt.Sprintf("%d", page-1)))
	}
	if hasNext {
		nav = append(nav, menu.Data("▶️", cbReferralPage, fmt.Sprintf("%d", page+1)))
	}
	if len(nav) > 0 {
		rows = append(rows, nav)
	}
	rows = append(rows, menu.Row(menu.Data("🔙 Закрыть", cbReferralBack)))
	menu.Inline(rows...)
	return menu
}

func ReferralRevokeConfirmKeyboard(code string) *tele.ReplyMarkup {
	menu := &tele.ReplyMarkup{}
	yes := menu.Data("✅ Отозвать", cbReferralRevokeOK, code)
	back := menu.Data("🔙 Назад", cbReferralPage, "0")
	menu.Inline(menu.Row(yes), menu.Row(back))
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
		menu.Row(menu.Text(BtnAdminManage), menu.Text(BtnAdminReferrals)),
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
		menu.Row(menu.Text(BtnAdminCreateInvite)),
		menu.Row(menu.Text(BtnAdminBanUser)),
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

func AdminReferralsKeyboard() *tele.ReplyMarkup {
	menu := &tele.ReplyMarkup{ResizeKeyboard: true}
	menu.Reply(
		menu.Row(menu.Text(BtnAdminReferralOverview), menu.Text(BtnAdminReferralLeaders)),
		menu.Row(menu.Text(BtnAdminBack)),
	)
	return menu
}

func AdminReferralOverviewKeyboard(selected string) *tele.ReplyMarkup {
	menu := &tele.ReplyMarkup{}
	labels := []struct{ value, label string }{{"7", "7 дней"}, {"30", "30 дней"}, {"all", "Всё время"}}
	var row tele.Row
	for _, item := range labels {
		label := item.label
		if item.value == selected {
			label = "✅ " + label
		}
		row = append(row, menu.Data(label, cbAdminReferralOverview, item.value))
	}
	menu.Inline(row)
	return menu
}

func AdminReferralLeadersKeyboard(period string, page int, hasNext bool) *tele.ReplyMarkup {
	menu := &tele.ReplyMarkup{}
	periodRow := menu.Row(
		menu.Data(map[bool]string{true: "✅ 30 дней", false: "30 дней"}[period == "30"], cbAdminReferralLeaders, "30", "0"),
		menu.Data(map[bool]string{true: "✅ Всё время", false: "Всё время"}[period == "all"], cbAdminReferralLeaders, "all", "0"),
	)
	rows := []tele.Row{periodRow}
	var nav tele.Row
	if page > 0 {
		nav = append(nav, menu.Data("◀️", cbAdminReferralLeaders, period, fmt.Sprintf("%d", page-1)))
	}
	if hasNext {
		nav = append(nav, menu.Data("▶️", cbAdminReferralLeaders, period, fmt.Sprintf("%d", page+1)))
	}
	if len(nav) > 0 {
		rows = append(rows, nav)
	}
	menu.Inline(rows...)
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
func PaymentMethodKeyboard(hasYooKassa, hasPlatega bool) *tele.ReplyMarkup {
	menu := &tele.ReplyMarkup{ResizeKeyboard: true}
	var rows []tele.Row
	if hasYooKassa {
		rows = append(rows, menu.Row(menu.Text(BtnPayYooKassa)))
	}
	if hasPlatega {
		rows = append(rows, menu.Row(menu.Text(BtnPayCrypto)))
	}
	rows = append(rows, menu.Row(menu.Text(BtnCancel)))
	menu.Reply(rows...)
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

// isValidSubscriptionURL проверяет, что ссылку подписки можно отдать Telegram
// как URL inline-кнопки. Битый URL Telegram отвергает вместе со всем сообщением,
// поэтому при сомнении кнопку лучше не показывать вовсе.
func isValidSubscriptionURL(subURL string) bool {
	if subURL == "" {
		return false
	}
	u, err := url.Parse(subURL)
	if err != nil {
		return false
	}
	if u.Scheme != "http" && u.Scheme != "https" {
		return false
	}
	return u.Host != ""
}

// SubscriptionCardKeyboard — inline-клавиатура карточки «Моя подписка».
// showConnect включает кнопки перехода на страницу подписки и перевыпуска ссылки:
// они показываются там же, где показывается сама ссылка (активный доступ).
// «Мои устройства» доступны всегда, пока пользователь зарегистрирован.
func SubscriptionCardKeyboard(subURL string, showConnect bool) *tele.ReplyMarkup {
	menu := &tele.ReplyMarkup{}
	var rows []tele.Row

	if showConnect && isValidSubscriptionURL(subURL) {
		rows = append(rows, menu.Row(menu.URL("🔗 Подключить устройство", subURL)))
	}
	rows = append(rows, menu.Row(menu.Data("📱 Мои устройства", cbDevicesManage)))
	if showConnect {
		rows = append(rows, menu.Row(menu.Data("🔄 Перевыпустить ссылку", cbSubRevoke)))
	}

	menu.Inline(rows...)
	return menu
}

// ConnectKeyboard — одиночная кнопка перехода на страницу подписки.
// Возвращает nil, если ссылка непригодна для URL-кнопки: тогда сообщение
// отправляется без разметки, со ссылкой в тексте.
func ConnectKeyboard(subURL string) *tele.ReplyMarkup {
	if !isValidSubscriptionURL(subURL) {
		return nil
	}
	menu := &tele.ReplyMarkup{}
	menu.Inline(menu.Row(menu.URL("🔗 Подключить устройство", subURL)))
	return menu
}

// SubscriptionRevokeConfirmKeyboard — подтверждение перевыпуска ссылки.
func SubscriptionRevokeConfirmKeyboard() *tele.ReplyMarkup {
	menu := &tele.ReplyMarkup{}
	menu.Inline(
		menu.Row(menu.Data("✅ Да, перевыпустить", cbSubRevokeConfirm)),
		menu.Row(menu.Data("🔙 Отмена", cbSubRevokeCancel)),
	)
	return menu
}

// truncateDeviceLabel обрезает подпись устройства до разумной длины для кнопки.
func truncateDeviceLabel(s string) string {
	const max = 25
	r := []rune(s)
	if len(r) <= max {
		return s
	}
	return string(r[:max-1]) + "…"
}

// deviceLabel формирует читаемую подпись устройства для кнопки.
func deviceLabel(d remnawave.HwidDevice) string {
	platform := d.Platform
	if platform == "" {
		platform = "Устройство"
	}
	label := platform
	if d.DeviceModel != "" {
		label = platform + " · " + d.DeviceModel
	}
	return truncateDeviceLabel(label)
}

// DevicesManagementKeyboard — список устройств как inline-кнопки (нажатие = удаление),
// плюс «Сбросить все» (если есть устройства) и «Закрыть».
func DevicesManagementKeyboard(devices []remnawave.HwidDevice) *tele.ReplyMarkup {
	menu := &tele.ReplyMarkup{}
	var rows []tele.Row

	for i, d := range devices {
		btn := menu.Data("🔄 "+deviceLabel(d), cbDeviceDelete, fmt.Sprintf("%d", i))
		rows = append(rows, menu.Row(btn))
	}

	if len(devices) > 0 {
		resetAll := menu.Data("🗑 Сбросить все устройства", cbDevicesResetAll)
		rows = append(rows, menu.Row(resetAll))
	}

	// Экран устройств редактирует карточку подписки, поэтому выход возвращает
	// карточку на место, а не удаляет сообщение.
	backBtn := menu.Data("🔙 Назад", cbSubCard)
	rows = append(rows, menu.Row(backBtn))

	menu.Inline(rows...)
	return menu
}

// bugCategories — фиксированный список категорий проблем (код callback → подпись).
var bugCategories = []struct{ Code, Label string }{
	{"conn", "🔌 Не подключается"},
	{"slow", "🐢 Медленно работает"},
	{"site", "🌍 Не грузит сайт/сервис"},
	{"other", "✍️ Другое"},
}

// bugCategoryLabel возвращает подпись категории по коду.
func bugCategoryLabel(code string) string {
	for _, c := range bugCategories {
		if c.Code == code {
			return c.Label
		}
	}
	return "Другое"
}

// BugServersKeyboard — inline-список серверов с мультивыбором (тогглы с галочками).
// selected — множество выбранных Remark'ов (может быть nil). Выбранные помечаются «✅».
func BugServersKeyboard(hosts []remnawave.Host, selected map[string]bool) *tele.ReplyMarkup {
	menu := &tele.ReplyMarkup{}
	var rows []tele.Row
	for i, h := range hosts {
		label := h.Remark
		if selected[h.Remark] {
			label = "✅ " + label
		}
		btn := menu.Data(label, cbBugServer, fmt.Sprintf("%d", i))
		rows = append(rows, menu.Row(btn))
	}
	// «Готово» показываем, только если что-то выбрано.
	if len(selected) > 0 {
		rows = append(rows, menu.Row(menu.Data("✅ Готово", cbBugServerDone)))
	}
	rows = append(rows, menu.Row(menu.Data(BtnBugNoServer, cbBugServerDone, "none")))
	rows = append(rows, menu.Row(menu.Data("🚫 Отмена", cbBugCancel)))
	menu.Inline(rows...)
	return menu
}

// BugCategoriesKeyboard — inline-список категорий проблемы.
func BugCategoriesKeyboard() *tele.ReplyMarkup {
	menu := &tele.ReplyMarkup{}
	var rows []tele.Row
	for _, c := range bugCategories {
		rows = append(rows, menu.Row(menu.Data(c.Label, cbBugCategory, c.Code)))
	}
	rows = append(rows, menu.Row(menu.Data("🚫 Отмена", cbBugCancel)))
	menu.Inline(rows...)
	return menu
}

// BugCommentKeyboard — reply-клавиатура шага ввода комментария.
func BugCommentKeyboard() *tele.ReplyMarkup {
	menu := &tele.ReplyMarkup{ResizeKeyboard: true}
	menu.Reply(menu.Row(menu.Text(BtnBugSkip), menu.Text(BtnCancel)))
	return menu
}

// AdminUserInfoKeyboard — inline-клавиатура карточки пользователя.
// Кнопка «Продлить на месяц» скрыта для безлимитных подписок (expireAt год >= 2099).
func AdminUserInfoKeyboard(targetID int64, remUser *remnawave.User) *tele.ReplyMarkup {
	return AdminUserInfoKeyboardWithReferrals(targetID, remUser, 0)
}

func AdminUserInfoKeyboardWithReferrals(targetID int64, remUser *remnawave.User, activeInvites int) *tele.ReplyMarkup {
	menu := &tele.ReplyMarkup{}
	var rows []tele.Row
	var row tele.Row

	if remUser != nil && remUser.ExpireAt.Year() < 2099 {
		extend := menu.Data("➕ Продлить на месяц", cbAdminExtendMonth, fmt.Sprintf("%d", targetID))
		row = append(row, extend)
	}
	refs := menu.Data(fmt.Sprintf("🎟 Приглашения %d/3", activeInvites), cbAdminUserReferrals, fmt.Sprintf("%d", targetID), "0")
	row = append(row, refs)
	rows = append(rows, row)

	menu.Inline(rows...)
	return menu
}

func AdminUserReferralsKeyboard(targetID int64, active []database.Invite, page int, hasNext bool) *tele.ReplyMarkup {
	menu := &tele.ReplyMarkup{}
	var rows []tele.Row
	for _, invite := range active {
		rows = append(rows, menu.Row(menu.Data("🗑 "+invite.Code, cbAdminReferralRevoke, fmt.Sprintf("%d", targetID), invite.Code)))
	}
	var nav tele.Row
	if page > 0 {
		nav = append(nav, menu.Data("◀️", cbAdminUserReferrals, fmt.Sprintf("%d", targetID), fmt.Sprintf("%d", page-1)))
	}
	if hasNext {
		nav = append(nav, menu.Data("▶️", cbAdminUserReferrals, fmt.Sprintf("%d", targetID), fmt.Sprintf("%d", page+1)))
	}
	if len(nav) > 0 {
		rows = append(rows, nav)
	}
	rows = append(rows, menu.Row(menu.Data("🔙 Назад", cbAdminReferralBack, fmt.Sprintf("%d", targetID))))
	menu.Inline(rows...)
	return menu
}

func AdminReferralRevokeConfirmKeyboard(targetID int64, code string) *tele.ReplyMarkup {
	menu := &tele.ReplyMarkup{}
	yes := menu.Data("✅ Отозвать", cbAdminReferralRevokeOK, fmt.Sprintf("%d", targetID), code)
	back := menu.Data("🔙 Назад", cbAdminUserReferrals, fmt.Sprintf("%d", targetID), "0")
	menu.Inline(menu.Row(yes), menu.Row(back))
	return menu
}

// AdminExtendConfirmKeyboard — кнопки подтверждения/отмены продления.
func AdminExtendConfirmKeyboard(targetID int64) *tele.ReplyMarkup {
	menu := &tele.ReplyMarkup{}
	idStr := fmt.Sprintf("%d", targetID)
	ok := menu.Data("✅ Подтвердить", cbAdminExtendConfirm, idStr)
	cancel := menu.Data("❌ Отмена", cbAdminExtendCancel, idStr)
	menu.Inline(menu.Row(ok, cancel))
	return menu
}

// DevicesResetAllConfirmKeyboard — подтверждение сброса всех устройств.
func DevicesResetAllConfirmKeyboard() *tele.ReplyMarkup {
	menu := &tele.ReplyMarkup{}
	yes := menu.Data("✅ Да, сбросить все", cbDevicesResetAllConfirm)
	no := menu.Data("🔙 Отмена", cbDevicesManage)
	menu.Inline(menu.Row(yes), menu.Row(no))
	return menu
}
