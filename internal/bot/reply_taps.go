package bot

import (
	"log/slog"
	"strconv"

	"github.com/fus1ond/vpn_bot/internal/funnels"
	tele "gopkg.in/telebot.v3"
)

// replyButtonCaptions — подписи reply-кнопок, нажатие которых считается
// навигацией и убирается из истории чата.
//
// Набор перечислен явно, а не собран по всем Btn*-константам: список решает,
// что мы стираем у пользователя, и такое решение должно быть видно глазами.
//
// «Да» (BtnConfirmYes) сюда намеренно не входит — единственная подпись,
// неотличимая от обычного слова: набранное руками «Да» это реплика, а не тап.
var replyButtonCaptions = map[string]struct{}{
	BtnStatus:                  {},
	BtnInfo:                    {},
	BtnBack:                    {},
	BtnCancel:                  {},
	BtnBugReport:               {},
	BtnBugSkip:                 {},
	BtnBugNoServer:             {},
	BtnPay:                     {},
	BtnRenew:                   {},
	BtnPayYooKassa:             {},
	BtnPayCrypto:               {},
	BtnCheckPayment:            {},
	BtnServers:                 {},
	BtnInvites:                 {},
	BtnInviteCreate:            {},
	BtnInviteList:              {},
	BtnInviteBack:              {},
	BtnAdminManage:             {},
	BtnAdminBroadcast:          {},
	BtnAdminStats:              {},
	BtnAdminMaintenance:        {},
	BtnAdminMaintenanceOff:     {},
	BtnAdminUserMode:           {},
	BtnAdminBack:               {},
	BtnAdminCreateInvite:       {},
	BtnAdminBanUser:            {},
	BtnAdminUserInfo:           {},
	BtnAdminSwitchSubscription: {},
	BtnAdminSwitchInfinite:     {},
	BtnAdminChangePrice:        {},
	BtnAdminMigrationPaidYes:   {},
	BtnAdminMigrationPaidNo:    {},
	BtnBroadcastActive:         {},
	BtnAdminReferrals:          {},
	BtnAdminReferralOverview:   {},
	BtnAdminReferralLeaders:    {},
	BtnAdminFunnels:            {},
}

// replyButtonActions — id Действий reply-кнопок для журнала Событий. Id
// стабилен и от подписи не зависит: подпись можно переписать, история
// нажатий в журнале от этого не порвётся. Id, на которые опираются Шаги
// воронок, берутся из пакета funnels, чтобы запись и расчёт не разошлись.
//
// Карта шире replyButtonCaptions: «Да» здесь есть. Для уборки из чата
// ошибиться дорого — стёрли бы реплику человека, — а для журнала набранное
// руками «Да» и нажатие кнопки означают одно и то же: согласие.
//
// Тест сверяет карту со всеми Btn*-константами: новая reply-кнопка без id
// роняет тест, а не выпадает из журнала молча.
var replyButtonActions = map[string]string{
	// Пользователь: главное меню и общие кнопки флоу
	BtnStatus:       "subscription_open",
	BtnInfo:         "info_open",
	BtnServers:      "servers_open",
	BtnBugReport:    "bug_report_open",
	BtnPay:          "pay_menu",
	BtnRenew:        "renew_menu",
	BtnInvites:      funnels.ActionInvitesOpen,
	BtnBack:         "back",
	BtnCancel:       "cancel",
	BtnConfirmYes:   "confirm_yes",
	BtnBugSkip:      "bug_skip",
	BtnBugNoServer:  "bug_no_server",
	BtnPayYooKassa:  "pay_yookassa_legacy",
	BtnPayCrypto:    "pay_crypto_legacy",
	BtnCheckPayment: "pay_check_legacy",

	// Раздел приглашений
	BtnInviteCreate: "invite_create",
	BtnInviteList:   "invite_list",
	BtnInviteBack:   "invites_back",

	// Админка
	BtnAdminManage:             "admin_manage",
	BtnAdminBroadcast:          "admin_broadcast",
	BtnBroadcastActive:         "admin_broadcast_active",
	BtnAdminStats:              "admin_stats",
	BtnAdminMaintenance:        "admin_maintenance_on",
	BtnAdminMaintenanceOff:     "admin_maintenance_off",
	BtnAdminUserMode:           "admin_user_mode",
	BtnAdminBack:               "admin_back",
	BtnAdminCreateInvite:       "admin_invite_create",
	BtnAdminBanUser:            "admin_ban",
	BtnAdminUserInfo:           "admin_user_info",
	BtnAdminSwitchSubscription: "admin_switch_subscription",
	BtnAdminSwitchInfinite:     "admin_switch_infinite",
	BtnAdminChangePrice:        "admin_change_price",
	BtnAdminMigrationPaidYes:   "admin_migration_paid_yes",
	BtnAdminMigrationPaidNo:    "admin_migration_paid_no",
	BtnAdminReferrals:          "admin_referrals",
	BtnAdminReferralOverview:   "admin_referral_overview",
	BtnAdminReferralLeaders:    "admin_referral_leaders",
	BtnAdminFunnels:            "admin_funnels",
}

// isReplyButtonTap отвечает, является ли текст нажатием reply-кнопки, а не
// содержимым переписки. Сравнение точное: «👤 Моя подписка не работает» — это
// написанное человеком, и трогать его нельзя.
func isReplyButtonTap(text string) bool {
	_, ok := replyButtonCaptions[text]
	return ok
}

// newUserMessageDeleter собирает шов удаления сообщения пользователя поверх
// Bot API: в приватных чатах боту разрешено удалять входящие сообщения.
func newUserMessageDeleter(api *tele.Bot) func(tele.Context, int) error {
	return func(c tele.Context, messageID int) error {
		return api.Delete(&tele.StoredMessage{
			MessageID: strconv.Itoa(messageID),
			ChatID:    c.Chat().ID,
		})
	}
}

// dropReplyTapMiddleware убирает из чата сообщение-нажатие reply-кнопки.
// Нажатие — это навигация, и в истории переписки ему делать нечего: без уборки
// десять открытий карточки оставляют десять одинаковых строк от пользователя.
//
// Удаляем после обработчика: сообщение-триггер не должно исчезать раньше, чем
// сделана работа, ради которой его прислали.
func (b *Bot) dropReplyTapMiddleware(next tele.HandlerFunc) tele.HandlerFunc {
	return func(c tele.Context) error {
		err := next(c)

		msg := c.Message()
		if msg == nil || c.Callback() != nil || !isReplyButtonTap(msg.Text) {
			return err
		}
		b.dropUserMessage(c, msg.ID)
		return err
	}
}

// dropUserMessage удаляет сообщение пользователя. Best-effort: сообщение не
// наше, чтобы ронять из-за него флоу, — не удалилось, значит осталось в чате.
func (b *Bot) dropUserMessage(c tele.Context, messageID int) {
	if b.deleteUserMessage == nil {
		return
	}
	if err := b.deleteUserMessage(c, messageID); err != nil {
		slog.Warn("Failed to delete reply button tap",
			"error", err, "telegram_id", c.Sender().ID, "message_id", messageID)
	}
}
