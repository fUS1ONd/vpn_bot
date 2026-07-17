package bot

import (
	"errors"
	"fmt"
	"html"
	"log/slog"
	"strconv"
	"strings"
	"time"

	"github.com/fus1ond/vpn_bot/internal/database"
	"github.com/fus1ond/vpn_bot/internal/remnawave"
	tele "gopkg.in/telebot.v3"
)

const referralHistoryPageSize = 10

func moscowTime(value time.Time) string {
	location, err := time.LoadLocation("Europe/Moscow")
	if err != nil {
		location = time.FixedZone("МСК", 3*60*60)
	}
	return value.In(location).Format("02.01.2006 15:04 МСК")
}

func (b *Bot) referralInviteMessage(invite *database.Invite) string {
	price := b.config.DefaultSubscriptionPrice
	if invite != nil && invite.SubscriptionPrice != nil {
		price = *invite.SubscriptionPrice
	}
	expires := "не указан"
	if invite != nil && invite.ExpiresAt != nil {
		expires = moscowTime(*invite.ExpiresAt)
	}
	code := ""
	if invite != nil {
		code = invite.Code
	}
	return fmt.Sprintf(
		"🔒 Приглашение в VPN\n\n"+
			"Пробный доступ: 72 часа, %d ГБ\n"+
			"После пробного периода: %d ₽/мес\n\n"+
			"Если подписка не будет оплачена, VPN-доступ автоматически отключится, "+
			"а пользователь будет исключён из системы.\n\n"+
			"Приглашение действительно до: %s\n\n"+
			"👉 https://t.me/%s?start=%s",
		b.config.TrialTrafficLimitGB,
		price,
		expires,
		b.getBotUsername(),
		code,
	)
}

func (b *Bot) canCreateReferralInvite(telegramID int64) (bool, error) {
	accessible, err := b.canAccessReferralSection(telegramID)
	if err != nil || !accessible {
		return false, err
	}
	user, err := b.db.GetUserByTelegramID(telegramID)
	if err != nil || user == nil {
		return false, err
	}
	remUser, err := b.remnawave.GetUser(user.RemnawaveUUID)
	if err != nil || remUser == nil {
		return false, err
	}
	now := time.Now().UTC()
	accessActive := remUser.ExpireAt.Year() >= 2099 ||
		((remUser.Status == remnawave.StatusActive || remUser.Status == remnawave.StatusLimited) && remUser.ExpireAt.After(now))
	if !accessActive {
		return false, nil
	}
	if remUser.ExpireAt.Year() >= 2099 || user.LegacyPaidMigrated {
		return true, nil
	}
	return b.db.HasConfirmedPayment(telegramID)
}

func (b *Bot) canAccessReferralSection(telegramID int64) (bool, error) {
	banned, err := b.db.IsBanned(telegramID)
	if err != nil {
		return false, err
	}
	if banned {
		return false, nil
	}
	user, err := b.db.GetUserByTelegramID(telegramID)
	if err != nil {
		return false, err
	}
	return user != nil, nil
}

func (b *Bot) handleInvitesMenu(c tele.Context) error {
	accessible, err := b.canAccessReferralSection(c.Sender().ID)
	if err != nil {
		return c.Send("Ошибка проверки регистрации. Попробуйте позже.")
	}
	if !accessible {
		return c.Send("Раздел приглашений доступен только зарегистрированным пользователям. Отправьте /start.")
	}
	return c.Send("<b>🎟 Приглашения</b>\n\nПриглашайте друзей на пробный доступ.", &tele.SendOptions{
		ParseMode:   tele.ModeHTML,
		ReplyMarkup: InvitesMenuKeyboard(),
	})
}

func (b *Bot) handleCreateReferralInvite(c tele.Context) error {
	allowed, err := b.canCreateReferralInvite(c.Sender().ID)
	if err != nil {
		slog.Error("Failed to verify referral invite eligibility", "error", err, "telegram_id", c.Sender().ID)
		return c.Send("Ошибка проверки подписки. Попробуйте позже.", &tele.SendOptions{ReplyMarkup: InvitesMenuKeyboard()})
	}
	if !allowed {
		return c.Send(
			"Создавать приглашения могут пользователи с действующей оплаченной или бессрочной подпиской.",
			&tele.SendOptions{ReplyMarkup: InvitesMenuKeyboard()},
		)
	}

	invite, err := b.db.CreateReferralInvite(c.Sender().ID, b.config.DefaultSubscriptionPrice, time.Now().UTC())
	if err != nil {
		switch {
		case errors.Is(err, database.ErrActiveInviteLimit):
			return c.Send("У вас уже есть 3 активных приглашения. Используйте или отзовите одно из них.", &tele.SendOptions{ReplyMarkup: InvitesMenuKeyboard()})
		case errors.Is(err, database.ErrDailyInviteLimit):
			return c.Send("Достигнут лимит: 15 созданных приглашений за последние 24 часа.", &tele.SendOptions{ReplyMarkup: InvitesMenuKeyboard()})
		default:
			slog.Error("Failed to create referral invite", "error", err, "telegram_id", c.Sender().ID)
			return c.Send("Ошибка создания приглашения.", &tele.SendOptions{ReplyMarkup: InvitesMenuKeyboard()})
		}
	}
	return c.Send(b.referralInviteMessage(invite), &tele.SendOptions{ReplyMarkup: InvitesMenuKeyboard()})
}

func (b *Bot) buildReferralList(telegramID int64, page int) (string, []database.Invite, bool, error) {
	if page < 0 {
		page = 0
	}
	now := time.Now().UTC()
	active, err := b.db.GetActiveReferralInvites(telegramID, now)
	if err != nil {
		return "", nil, false, err
	}
	used, err := b.db.GetUsedReferralInvitesByCreator(telegramID, referralHistoryPageSize+1, page*referralHistoryPageSize)
	if err != nil {
		return "", nil, false, err
	}
	hasNext := len(used) > referralHistoryPageSize
	if hasNext {
		used = used[:referralHistoryPageSize]
	}

	var msg strings.Builder
	msg.WriteString("<b>📋 Мои приглашения</b>\n\n")
	if len(active) == 0 {
		msg.WriteString("Активных ссылок нет.\n")
	} else {
		fmt.Fprintf(&msg, "<b>Активные ссылки: %d из %d</b>\n", len(active), database.MaxActiveReferralInvites)
		for _, invite := range active {
			fmt.Fprintf(&msg, "• <code>%s</code> — до %s\n", invite.Code, moscowTime(*invite.ExpiresAt))
		}
	}

	msg.WriteString("\n<b>Использованные приглашения</b>\n")
	if len(used) == 0 {
		msg.WriteString("Пока нет.")
	} else {
		for _, invite := range used {
			label := "пользователь"
			if invite.UsedBy != nil {
				label = formatUserLabel(invite.UserFirstName, invite.UserUsername, *invite.UsedBy)
			}
			date := ""
			if invite.UsedAt != nil {
				date = " · " + invite.UsedAt.Format("02.01.2006")
			}
			fmt.Fprintf(&msg, "• %s%s\n", label, date)
		}
	}
	return msg.String(), active, hasNext, nil
}

func (b *Bot) showReferralList(c tele.Context, page int, edit bool) error {
	accessible, accessErr := b.canAccessReferralSection(c.Sender().ID)
	if accessErr != nil || !accessible {
		if edit {
			return c.RespondAlert("Раздел доступен только зарегистрированным пользователям")
		}
		return c.Send("Раздел приглашений доступен только зарегистрированным пользователям. Отправьте /start.")
	}
	msg, active, hasNext, err := b.buildReferralList(c.Sender().ID, page)
	if err != nil {
		slog.Error("Failed to build referral list", "error", err, "telegram_id", c.Sender().ID)
		return c.Send("Ошибка получения приглашений.", &tele.SendOptions{ReplyMarkup: InvitesMenuKeyboard()})
	}
	opts := &tele.SendOptions{ParseMode: tele.ModeHTML, ReplyMarkup: ReferralInvitesKeyboard(active, page, hasNext)}
	if edit {
		if err := c.Edit(msg, opts); err == nil {
			return c.Respond()
		}
	}
	return c.Send(msg, opts)
}

func (b *Bot) handleReferralList(c tele.Context) error {
	return b.showReferralList(c, 0, false)
}

func callbackArg(c tele.Context) (string, bool) {
	args := c.Args()
	if len(args) == 0 || strings.TrimSpace(args[0]) == "" {
		return "", false
	}
	return strings.TrimSpace(args[0]), true
}

func (b *Bot) handleReferralResend(c tele.Context) error {
	accessible, err := b.canAccessReferralSection(c.Sender().ID)
	if err != nil || !accessible {
		return c.RespondAlert("Раздел доступен только зарегистрированным пользователям")
	}
	code, ok := callbackArg(c)
	if !ok {
		return c.RespondAlert("Некорректный запрос")
	}
	invite, err := b.db.GetInviteByCode(code)
	if err != nil || invite == nil || invite.CreatedBy != c.Sender().ID || invite.Kind != database.InviteKindReferral ||
		invite.UsedBy != nil || invite.RevokedAt != nil || invite.ExpiresAt == nil || !invite.ExpiresAt.After(time.Now().UTC()) {
		return c.RespondAlert("Приглашение больше не активно")
	}
	if err := c.Send(b.referralInviteMessage(invite)); err != nil {
		return err
	}
	return c.Respond(&tele.CallbackResponse{Text: "Сообщение отправлено"})
}

func (b *Bot) handleReferralRevoke(c tele.Context) error {
	accessible, err := b.canAccessReferralSection(c.Sender().ID)
	if err != nil || !accessible {
		return c.RespondAlert("Раздел доступен только зарегистрированным пользователям")
	}
	code, ok := callbackArg(c)
	if !ok {
		return c.RespondAlert("Некорректный запрос")
	}
	invite, err := b.db.GetInviteByCode(code)
	if err != nil || invite == nil || invite.CreatedBy != c.Sender().ID {
		return c.RespondAlert("Приглашение не найдено")
	}
	return c.Edit(
		fmt.Sprintf("Отозвать приглашение <code>%s</code>? Ссылка сразу перестанет работать.", html.EscapeString(code)),
		&tele.SendOptions{ParseMode: tele.ModeHTML, ReplyMarkup: ReferralRevokeConfirmKeyboard(code)},
	)
}

func (b *Bot) handleReferralRevokeConfirm(c tele.Context) error {
	accessible, err := b.canAccessReferralSection(c.Sender().ID)
	if err != nil || !accessible {
		return c.RespondAlert("Раздел доступен только зарегистрированным пользователям")
	}
	code, ok := callbackArg(c)
	if !ok {
		return c.RespondAlert("Некорректный запрос")
	}
	if err := b.db.RevokeReferralInvite(code, c.Sender().ID, false, time.Now().UTC()); err != nil {
		if errors.Is(err, database.ErrInviteNotOwned) {
			return c.RespondAlert("Это не ваше приглашение")
		}
		return c.RespondAlert("Приглашение уже не активно")
	}
	_ = c.Respond(&tele.CallbackResponse{Text: "Приглашение отозвано"})
	return b.showReferralList(c, 0, true)
}

func (b *Bot) handleReferralPage(c tele.Context) error {
	raw, ok := callbackArg(c)
	if !ok {
		return c.RespondAlert("Некорректный запрос")
	}
	page, err := strconv.Atoi(raw)
	if err != nil || page < 0 {
		return c.RespondAlert("Некорректная страница")
	}
	return b.showReferralList(c, page, true)
}

func (b *Bot) handleReferralClose(c tele.Context) error {
	_ = c.Edit("Раздел приглашений закрыт.")
	return c.Respond()
}

func (b *Bot) notifyReferralActivated(invite *database.Invite, telegramID int64, firstName, username string) {
	if b.bot == nil || invite == nil || invite.Kind != database.InviteKindReferral {
		return
	}
	active, err := b.db.CountActiveReferralInvites(invite.CreatedBy, time.Now().UTC())
	if err != nil {
		slog.Error("Failed to count free referral slots", "error", err, "creator_id", invite.CreatedBy)
		return
	}
	msg := fmt.Sprintf(
		"✅ Ваше приглашение активировано\nПользователь: %s\nСвободных приглашений: %d из %d",
		formatUserLabel(firstName, username, telegramID),
		database.MaxActiveReferralInvites-active,
		database.MaxActiveReferralInvites,
	)
	if _, err := b.bot.Send(&tele.User{ID: invite.CreatedBy}, msg, &tele.SendOptions{ParseMode: tele.ModeHTML}); err != nil {
		slog.Debug("Failed to notify referral creator", "error", err, "creator_id", invite.CreatedBy)
	}
}
