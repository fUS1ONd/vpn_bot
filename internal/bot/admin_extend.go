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

// extendedSubscriptionMessage — текст пользователю о ручном продлении.
func (b *Bot) extendedSubscriptionMessage(telegramID int64) string {
	remUser, _ := b.remnawave.GetUserByTelegramID(telegramID)
	if remUser != nil {
		return fmt.Sprintf(
			"✅ Ваша подписка продлена до <b>%s</b>.\n\nЛимит трафика снят — пользуйтесь без ограничений.",
			remUser.ExpireAt.Format("02.01.2006"),
		)
	}
	return "✅ Ваша подписка продлена."
}

// handleAdminExtendConfirm выполняет продление на месяц.
func (b *Bot) handleAdminExtendConfirm(c tele.Context) error {
	if !b.isAdmin(c) {
		return c.Respond()
	}

	targetID, ok := parseAdminExtendTargetID(c)
	if !ok {
		return c.RespondAlert("Некорректный запрос")
	}

	// Сериализуем с платёжными операциями по этому юзеру (callback от Platega и т.п.).
	mu := getPaymentMutex(targetID)
	mu.Lock()
	defer mu.Unlock()

	dbUser, err := b.db.GetUserByTelegramID(targetID)
	if err != nil || dbUser == nil {
		return c.RespondAlert("Пользователь не найден")
	}

	// Перечитываем свежего remUser: дата могла измениться (юзер мог сам оплатить).
	remUser, err := b.remnawave.GetUser(dbUser.RemnawaveUUID)
	if err != nil {
		slog.Error("Failed to reload Remnawave user before extend", "error", err, "telegram_id", targetID)
		return c.RespondAlert("Ошибка получения данных подписки")
	}

	newExpireAt := nextMonthExpireAt(remUser, time.Now().UTC())

	if err := b.remnawave.EnableUser(dbUser.RemnawaveUUID, newExpireAt); err != nil {
		slog.Error("Failed to extend subscription", "error", err, "telegram_id", targetID)
		return c.RespondAlert("❌ Не удалось продлить. Попробуйте ещё раз.")
	}

	// Очищаем маркеры уведомлений (юзер мог быть в grace period).
	if err := b.db.ClearNotifications(targetID); err != nil {
		slog.Error("Failed to clear notifications after manual extend", "error", err, "telegram_id", targetID)
	}

	// Уведомляем пользователя.
	_ = b.sendSchedulerMessageWithKeyboard(targetID, b.extendedSubscriptionMessage(targetID), b.userKeyboard(targetID))

	// Убираем кнопки, показываем результат админу.
	_ = c.Edit(fmt.Sprintf("✅ Подписка продлена до <b>%s</b>.", newExpireAt.Format("02.01.2006")), &tele.SendOptions{
		ParseMode: tele.ModeHTML,
	})
	return c.Respond()
}

// handleAdminExtendCancel отменяет продление.
func (b *Bot) handleAdminExtendCancel(c tele.Context) error {
	if !b.isAdmin(c) {
		return c.Respond()
	}
	_ = c.Edit("Продление отменено.")
	return c.Respond()
}
