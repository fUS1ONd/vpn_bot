package bot

import (
	"errors"
	"fmt"
	"log/slog"
	"strconv"
	"strings"
	"time"

	"github.com/fus1ond/vpn_bot/internal/database"
	"github.com/fus1ond/vpn_bot/internal/remnawave"
	tele "gopkg.in/telebot.v3"
)

func (b *Bot) handleAdminReferralsMenu(c tele.Context) error {
	if !b.isAdmin(c) {
		return nil
	}
	return c.Send("<b>🤝 Приглашения</b>\n\nВыберите раздел:", &tele.SendOptions{
		ParseMode:   tele.ModeHTML,
		ReplyMarkup: AdminReferralsKeyboard(),
	})
}

func referralPeriod(value string, now time.Time) (*time.Time, *time.Time, string) {
	end := now.UTC()
	switch value {
	case "7":
		start := end.Add(-7 * 24 * time.Hour)
		return &start, &end, "последние 7 дней"
	case "all":
		return nil, nil, "всё время"
	default:
		start := end.Add(-30 * 24 * time.Hour)
		return &start, &end, "последние 30 дней"
	}
}

func (b *Bot) renderAdminReferralOverview(period string) (string, error) {
	start, end, label := referralPeriod(period, time.Now().UTC())
	stats, err := b.db.GetReferralOverview(start, end)
	if err != nil {
		return "", err
	}
	conversion := 0
	if stats.UniqueInvited > 0 {
		conversion = stats.FirstPaid * 100 / stats.UniqueInvited
	}
	return fmt.Sprintf(
		"<b>📊 Приглашения — %s</b>\n\n"+
			"├ Создано: %d\n"+
			"├ Активировано: %d\n"+
			"├ Истекло без использования: %d\n"+
			"├ Отозвано: %d\n"+
			"├ Уникальных новых пользователей: %d\n"+
			"├ Впервые оплатили: %d\n"+
			"├ Конверсия в первую оплату: %d%%\n"+
			"└ Служебных admin-активаций: %d",
		label, stats.Created, stats.Activated, stats.Expired, stats.Revoked,
		stats.UniqueInvited, stats.FirstPaid, conversion, stats.AdminActivations,
	), nil
}

func (b *Bot) showAdminReferralOverview(c tele.Context, period string, edit bool) error {
	if !b.isAdmin(c) {
		return c.Respond()
	}
	if period != "7" && period != "30" && period != "all" {
		period = "30"
	}
	msg, err := b.renderAdminReferralOverview(period)
	if err != nil {
		slog.Error("Failed to render referral overview", "error", err)
		return c.Send("Ошибка получения статистики", &tele.SendOptions{ReplyMarkup: AdminReferralsKeyboard()})
	}
	opts := &tele.SendOptions{ParseMode: tele.ModeHTML, ReplyMarkup: AdminReferralOverviewKeyboard(period)}
	if edit {
		if err := c.Edit(msg, opts); err == nil {
			return c.Respond()
		}
	}
	return c.Send(msg, opts)
}

func (b *Bot) handleAdminReferralOverview(c tele.Context) error {
	period := "30"
	if arg, ok := callbackArg(c); ok {
		period = arg
	}
	return b.showAdminReferralOverview(c, period, c.Callback() != nil)
}

func parseLeaderboardArgs(c tele.Context) (string, int) {
	period := "30"
	page := 0
	args := c.Args()
	if len(args) > 0 && args[0] == "all" {
		period = "all"
	}
	if len(args) > 1 {
		if value, err := strconv.Atoi(args[1]); err == nil && value >= 0 {
			page = value
		}
	}
	return period, page
}

func (b *Bot) countActiveReferralInvitees(creatorID int64, start *time.Time, remUsers map[int64]remnawave.User, now time.Time) int {
	ids, err := b.db.GetFirstTouchInvitees(creatorID, start)
	if err != nil {
		return 0
	}
	count := 0
	for _, id := range ids {
		user, ok := remUsers[id]
		if !ok {
			continue
		}
		if (user.Status == remnawave.StatusActive || user.Status == remnawave.StatusLimited) && user.ExpireAt.After(now) {
			count++
		}
	}
	return count
}

func (b *Bot) showAdminReferralLeaderboard(c tele.Context, period string, page int, edit bool) error {
	if !b.isAdmin(c) {
		return c.Respond()
	}
	var start *time.Time
	if period != "all" {
		period = "30"
		value := time.Now().UTC().Add(-30 * 24 * time.Hour)
		start = &value
	}
	rows, err := b.db.GetReferralLeaderboard(start, 11, page*10)
	if err != nil {
		return c.Send("Ошибка получения рейтинга", &tele.SendOptions{ReplyMarkup: AdminReferralsKeyboard()})
	}
	hasNext := len(rows) > 10
	if hasNext {
		rows = rows[:10]
	}
	allRemUsers, err := b.remnawave.GetAllUsers()
	if err != nil {
		return c.Send("Ошибка получения статусов пользователей", &tele.SendOptions{ReplyMarkup: AdminReferralsKeyboard()})
	}
	remByID := make(map[int64]remnawave.User, len(allRemUsers))
	for _, user := range allRemUsers {
		if user.TelegramID != nil {
			remByID[*user.TelegramID] = user
		}
	}
	var msg strings.Builder
	label := "30 дней"
	if period == "all" {
		label = "всё время"
	}
	fmt.Fprintf(&msg, "<b>🏆 Кто приглашает — %s</b>\n\n", label)
	if len(rows) == 0 {
		msg.WriteString("Данных пока нет.")
	}
	now := time.Now().UTC()
	for i, row := range rows {
		active := b.countActiveReferralInvitees(row.TelegramID, start, remByID, now)
		adminMark := ""
		if row.TelegramID == b.config.AdminID {
			adminMark = " · администратор"
		}
		fmt.Fprintf(&msg, "%d. %s%s\n   Пригласил: %d · Оплатили: %d · Активны: %d\n",
			page*10+i+1, formatUserLabel(row.FirstName, row.Username, row.TelegramID), adminMark,
			row.Invited, row.Paid, active)
	}
	opts := &tele.SendOptions{ParseMode: tele.ModeHTML, ReplyMarkup: AdminReferralLeadersKeyboard(period, page, hasNext)}
	if edit {
		if err := c.Edit(msg.String(), opts); err == nil {
			return c.Respond()
		}
	}
	return c.Send(msg.String(), opts)
}

func (b *Bot) handleAdminReferralLeaderboard(c tele.Context) error {
	period, page := parseLeaderboardArgs(c)
	return b.showAdminReferralLeaderboard(c, period, page, c.Callback() != nil)
}

func parseAdminReferralTarget(c tele.Context) (int64, int, bool) {
	args := c.Args()
	if len(args) == 0 {
		return 0, 0, false
	}
	targetID, err := strconv.ParseInt(args[0], 10, 64)
	if err != nil {
		return 0, 0, false
	}
	page := 0
	if len(args) > 1 {
		page, _ = strconv.Atoi(args[1])
	}
	if page < 0 {
		page = 0
	}
	return targetID, page, true
}

func (b *Bot) buildAdminUserReferrals(targetID int64, page int) (string, []database.Invite, bool, error) {
	user, err := b.db.GetUserByTelegramID(targetID)
	if err != nil || user == nil {
		return "", nil, false, errors.New("user not found")
	}
	now := time.Now().UTC()
	active, err := b.db.GetActiveReferralInvites(targetID, now)
	if err != nil {
		return "", nil, false, err
	}
	summary, err := b.db.GetReferralCreatorSummary(targetID, now)
	if err != nil {
		return "", nil, false, err
	}
	used, err := b.db.GetUsedReferralInvitesByCreator(targetID, 11, page*10)
	if err != nil {
		return "", nil, false, err
	}
	hasNext := len(used) > 10
	if hasNext {
		used = used[:10]
	}
	var msg strings.Builder
	fmt.Fprintf(&msg, "<b>🎟 Приглашения пользователя</b>\n%s\n\n", formatUserLabel(user.FirstName, user.Username, user.TelegramID))
	fmt.Fprintf(&msg, "Активных: %d/3 · Использовано: %d · Истекло: %d · Отозвано: %d\n\n", summary.Active, summary.Used, summary.Expired, summary.Revoked)
	if len(active) > 0 {
		msg.WriteString("<b>Активные ссылки</b>\n")
		for _, invite := range active {
			fmt.Fprintf(&msg, "• <code>%s</code> — до %s\n", invite.Code, moscowTime(*invite.ExpiresAt))
		}
		msg.WriteString("\n")
	}
	msg.WriteString("<b>Приглашённые</b>\n")
	if len(used) == 0 {
		msg.WriteString("Пока нет.")
	} else {
		for _, invite := range used {
			if invite.UsedBy != nil {
				fmt.Fprintf(&msg, "• %s\n", formatUserLabel(invite.UserFirstName, invite.UserUsername, *invite.UsedBy))
			}
		}
	}
	return msg.String(), active, hasNext, nil
}

func (b *Bot) showAdminUserReferrals(c tele.Context, targetID int64, page int) error {
	msg, active, hasNext, err := b.buildAdminUserReferrals(targetID, page)
	if err != nil {
		return c.RespondAlert("Ошибка получения приглашений")
	}
	if err := c.Edit(msg, &tele.SendOptions{ParseMode: tele.ModeHTML, ReplyMarkup: AdminUserReferralsKeyboard(targetID, active, page, hasNext)}); err != nil {
		return err
	}
	return c.Respond()
}

func (b *Bot) handleAdminUserReferrals(c tele.Context) error {
	if !b.isAdmin(c) {
		return c.Respond()
	}
	targetID, page, ok := parseAdminReferralTarget(c)
	if !ok {
		return c.RespondAlert("Некорректный запрос")
	}
	return b.showAdminUserReferrals(c, targetID, page)
}

func (b *Bot) handleAdminReferralRevoke(c tele.Context) error {
	if !b.isAdmin(c) {
		return c.Respond()
	}
	args := c.Args()
	if len(args) < 2 {
		return c.RespondAlert("Некорректный запрос")
	}
	targetID, err := strconv.ParseInt(args[0], 10, 64)
	if err != nil {
		return c.RespondAlert("Некорректный пользователь")
	}
	code := args[1]
	return c.Edit(fmt.Sprintf("Отозвать приглашение <code>%s</code>?", code), &tele.SendOptions{
		ParseMode:   tele.ModeHTML,
		ReplyMarkup: AdminReferralRevokeConfirmKeyboard(targetID, code),
	})
}

func (b *Bot) handleAdminReferralRevokeConfirm(c tele.Context) error {
	if !b.isAdmin(c) {
		return c.Respond()
	}
	args := c.Args()
	if len(args) < 2 {
		return c.RespondAlert("Некорректный запрос")
	}
	targetID, err := strconv.ParseInt(args[0], 10, 64)
	if err != nil {
		return c.RespondAlert("Некорректный пользователь")
	}
	if err := b.db.RevokeReferralInvite(args[1], c.Sender().ID, true, time.Now().UTC()); err != nil {
		return c.RespondAlert("Приглашение уже не активно")
	}
	_ = c.Respond(&tele.CallbackResponse{Text: "Приглашение отозвано"})
	return b.showAdminUserReferrals(c, targetID, 0)
}

func (b *Bot) handleAdminReferralBack(c tele.Context) error {
	if !b.isAdmin(c) {
		return c.Respond()
	}
	targetID, _, ok := parseAdminReferralTarget(c)
	if !ok {
		return c.RespondAlert("Некорректный запрос")
	}
	return b.editAdminUserInfo(c, targetID)
}
