package bot

import (
	"log/slog"
	"strconv"

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
