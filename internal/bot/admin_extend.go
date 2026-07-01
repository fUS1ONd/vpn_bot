package bot

import (
	"errors"
	"fmt"
	"log/slog"
	"strconv"
	"strings"
	"time"

	tele "gopkg.in/telebot.v3"

	"github.com/fus1ond/vpn_bot/internal/remnawave"
)

// nextMonthExpireAt считает новую дату окончания подписки при продлении на месяц.
// Если срок ещё не истёк и пользователь не заблокирован явно (ACTIVE или LIMITED —
// LIMITED означает лишь исчерпанный трафик триала, а не истёкшую подписку) —
// плюсуем к текущему expireAt, не теряя остаток. Иначе (expired, grace period,
// disabled) — считаем от now.
func nextMonthExpireAt(remUser *remnawave.User, now time.Time) time.Time {
	statusOK := remUser.Status == remnawave.StatusActive || remUser.Status == remnawave.StatusLimited
	if remUser.ExpireAt.After(now) && statusOK {
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
// newExpireAt приходит от вызывающей стороны (уже применена через EnableUser),
// повторный запрос к Remnawave не нужен.
func extendedSubscriptionMessage(newExpireAt time.Time) string {
	return fmt.Sprintf(
		"✅ Ваша подписка продлена до <b>%s</b>.\n\nЛимит трафика снят — пользуйтесь без ограничений.",
		newExpireAt.Format("02.01.2006"),
	)
}

// adminExtendCooldownWindow — минимальный интервал между двумя продлениями одного
// пользователя. Защита от дабл-клика/повторного callback при плохой сети:
// без неё второй вызов, дождавшись мьютекса, продлил бы уже продлённую подписку ещё раз.
const adminExtendCooldownWindow = 10 * time.Second

// handleAdminExtendConfirm выполняет продление на месяц.
func (b *Bot) handleAdminExtendConfirm(c tele.Context) error {
	if !b.isAdmin(c) {
		return c.Respond()
	}

	targetID, ok := parseAdminExtendTargetID(c)
	if !ok {
		return c.RespondAlert("Некорректный запрос")
	}

	newExpireAt, err := b.applyAdminExtend(targetID)
	if err != nil {
		return c.RespondAlert(adminExtendErrorAlert(err))
	}

	// Уведомляем пользователя и отвечаем админу уже вне мьютекса —
	// это чисто нотификационные шаги, не требующие атомарности с EnableUser.
	_ = b.sendSchedulerMessageWithKeyboard(targetID, extendedSubscriptionMessage(newExpireAt), b.userKeyboard(targetID))

	_ = c.Edit(fmt.Sprintf("✅ Подписка продлена до <b>%s</b>.", newExpireAt.Format("02.01.2006")), &tele.SendOptions{
		ParseMode: tele.ModeHTML,
	})
	return c.Respond()
}

// Ошибки applyAdminExtend, различаемые для показа нужного текста алерта админу.
var (
	errAdminExtendCooldown     = errors.New("продление уже выполнено недавно")
	errAdminExtendUserNotFound = errors.New("пользователь не найден")
	errAdminExtendLoadFailed   = errors.New("ошибка получения данных подписки")
	errAdminExtendEnableFailed = errors.New("не удалось продлить подписку")
)

// adminExtendErrorAlert возвращает текст алерта админу для ошибки applyAdminExtend.
func adminExtendErrorAlert(err error) string {
	switch {
	case errors.Is(err, errAdminExtendCooldown):
		return "Подписка уже продлена, повторное нажатие проигнорировано"
	case errors.Is(err, errAdminExtendUserNotFound):
		return "Пользователь не найден"
	case errors.Is(err, errAdminExtendLoadFailed):
		return "Ошибка получения данных подписки"
	default:
		return "❌ Не удалось продлить. Попробуйте ещё раз."
	}
}

// applyAdminExtend продлевает подписку в Remnawave и возвращает применённую дату.
// Критическая секция мьютекса ограничена только операциями, которым нужна атомарность
// относительно платёжного flow (чтение/запись Remnawave-состояния); отправка уведомлений
// в вызывающей handleAdminExtendConfirm выполняется уже после разблокировки.
func (b *Bot) applyAdminExtend(targetID int64) (time.Time, error) {
	// Сериализуем с платёжными операциями по этому юзеру (callback от Platega и т.п.).
	mu := getPaymentMutex(targetID)
	mu.Lock()
	defer mu.Unlock()

	if last, ok := b.adminExtendCooldown.Load(targetID); ok {
		if time.Since(last.(time.Time)) < adminExtendCooldownWindow {
			return time.Time{}, errAdminExtendCooldown
		}
	}

	dbUser, err := b.db.GetUserByTelegramID(targetID)
	if err != nil || dbUser == nil {
		return time.Time{}, errAdminExtendUserNotFound
	}

	// Перечитываем свежего remUser: дата могла измениться (юзер мог сам оплатить).
	remUser, err := b.remnawave.GetUser(dbUser.RemnawaveUUID)
	if err != nil {
		slog.Error("Failed to reload Remnawave user before extend", "error", err, "telegram_id", targetID)
		return time.Time{}, errAdminExtendLoadFailed
	}

	newExpireAt := nextMonthExpireAt(remUser, time.Now().UTC())

	if err := b.remnawave.EnableUser(dbUser.RemnawaveUUID, newExpireAt); err != nil {
		slog.Error("Failed to extend subscription", "error", err, "telegram_id", targetID)
		return time.Time{}, errAdminExtendEnableFailed
	}

	b.adminExtendCooldown.Store(targetID, time.Now())

	// Очищаем маркеры уведомлений (юзер мог быть в grace period).
	if err := b.db.ClearNotifications(targetID); err != nil {
		slog.Error("Failed to clear notifications after manual extend", "error", err, "telegram_id", targetID)
	}

	return newExpireAt, nil
}

// handleAdminExtendCancel отменяет продление.
func (b *Bot) handleAdminExtendCancel(c tele.Context) error {
	if !b.isAdmin(c) {
		return c.Respond()
	}
	_ = c.Edit("Продление отменено.")
	return c.Respond()
}
