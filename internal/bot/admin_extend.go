package bot

import (
	"fmt"
	"log/slog"
	"strconv"
	"strings"
	"time"

	tele "gopkg.in/telebot.v3"

	"github.com/fus1ond/vpn_bot/internal/remnawave"
)

// nextMonthExpireAt считает новую дату окончания подписки при продлении на месяц.
// Если подписка активна и не истекла — плюсуем к текущему expireAt (не теряем остаток).
// Иначе (триал истёк, grace period, disabled) — считаем от now.
func nextMonthExpireAt(remUser *remnawave.User, now time.Time) time.Time {
	if remUser.ExpireAt.After(now) && remUser.Status == remnawave.StatusActive {
		return remUser.ExpireAt.AddDate(0, 1, 0)
	}
	return now.AddDate(0, 1, 0)
}

// parseAdminExtendTargetID парсит targetID из callback-данных.
func parseAdminExtendTargetID(c tele.Context) (int64, bool) {
	args := c.Args()
	if len(args) == 0 {
		return 0, false
	}
	id, err := strconv.ParseInt(strings.TrimSpace(args[0]), 10, 64)
	if err != nil {
		return 0, false
	}
	return id, true
}

// handleAdminExtendMonth показывает экран подтверждения продления.
func (b *Bot) handleAdminExtendMonth(c tele.Context) error {
	if !b.isAdmin(c) {
		return c.Respond()
	}

	targetID, ok := parseAdminExtendTargetID(c)
	if !ok {
		return c.RespondAlert("Некорректный запрос")
	}

	dbUser, err := b.db.GetUserByTelegramID(targetID)
	if err != nil || dbUser == nil {
		return c.RespondAlert("Пользователь не найден")
	}

	remUser, err := b.remnawave.GetUser(dbUser.RemnawaveUUID)
	if err != nil {
		slog.Error("Failed to load Remnawave user for extend", "error", err, "telegram_id", targetID)
		return c.RespondAlert("Ошибка получения данных подписки")
	}

	newExpireAt := nextMonthExpireAt(remUser, time.Now().UTC())
	text := fmt.Sprintf(
		"Продлить подписку %s до <b>%s</b>?",
		formatUserLabel(dbUser.FirstName, dbUser.Username, dbUser.TelegramID),
		newExpireAt.Format("02.01.2006"),
	)

	return c.Edit(text, &tele.SendOptions{
		ParseMode:   tele.ModeHTML,
		ReplyMarkup: AdminExtendConfirmKeyboard(targetID),
	})
}
