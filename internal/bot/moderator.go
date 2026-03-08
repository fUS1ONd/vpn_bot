package bot

import (
	"fmt"
	"log/slog"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/fus1ond/vpn_bot/internal/database"
	"github.com/fus1ond/vpn_bot/internal/remnawave"
	tele "gopkg.in/telebot.v3"
)

// Состояния модератора
const (
	StateWaitModDeleteInvite  = "wait_mod_delete_invite"  // Модератор ждёт код для удаления
	StateWaitModExtendID      = "wait_mod_extend_id"      // Ожидание telegram_id подписчика для продления
	StateWaitModExtendConfirm = "wait_mod_extend_confirm" // Ожидание подтверждения продления
)

type modExtendSession struct {
	SubscriberTelegramID int64
	SubscriberLabel      string
	UserUUID             string
	NewExpireAt          time.Time
}

// isModerator проверяет, является ли пользователь модератором
func (b *Bot) isModerator(telegramID int64) bool {
	ok, err := b.db.IsModerator(telegramID)
	if err != nil {
		slog.Error("Failed to check moderator status", "error", err, "telegram_id", telegramID)
		return false
	}
	return ok
}

// handleModeratorMenu показывает подменю модератора
func (b *Bot) handleModeratorMenu(c tele.Context) error {
	return c.Send("<b>🎟 Приглашения</b>\n\nВыберите действие:", &tele.SendOptions{
		ParseMode:   tele.ModeHTML,
		ReplyMarkup: ModeratorMenuKeyboard(),
	})
}

// handleModeratorCreateInvite создаёт инвайт от имени модератора
func (b *Bot) handleModeratorCreateInvite(c tele.Context) error {
	telegramID := c.Sender().ID

	expireDays := 30
	invite, err := b.db.CreateInviteWithExpiry(telegramID, &expireDays)
	if err != nil {
		slog.Error("Failed to create invite by moderator", "error", err, "moderator_id", telegramID)
		return c.Send("Ошибка создания приглашения", &tele.SendOptions{ReplyMarkup: ModeratorMenuKeyboard()})
	}

	msg := fmt.Sprintf(MsgInviteCreated, b.getBotUsername(), invite.Code)
	return c.Send(msg, &tele.SendOptions{
		ParseMode:   tele.ModeHTML,
		ReplyMarkup: ModeratorMenuKeyboard(),
	})
}

// handleModeratorViewInvites показывает список инвайтов модератора
func (b *Bot) handleModeratorViewInvites(c tele.Context) error {
	telegramID := c.Sender().ID

	invites, err := b.db.GetInvitesWithUsersByCreator(telegramID)
	if err != nil {
		slog.Error("Failed to get moderator invites", "error", err, "moderator_id", telegramID)
		return c.Send("Ошибка получения списка приглашений", &tele.SendOptions{ReplyMarkup: ModeratorMenuKeyboard()})
	}

	if len(invites) == 0 {
		return c.Send("📋 У вас пока нет приглашений", &tele.SendOptions{
			ParseMode:   tele.ModeHTML,
			ReplyMarkup: ModeratorMenuKeyboard(),
		})
	}

	var msg strings.Builder
	fmt.Fprintf(&msg, "<b>📋 Мои приглашения (%d)</b>\n\n", len(invites))

	for _, inv := range invites {
		if inv.UsedBy != nil {
			msg.WriteString("✅ Использован\n")
			fmt.Fprintf(&msg, "🔹 Код: <code>%s</code>\n", inv.Code)

			// Информация о пользователе
			fmt.Fprintf(&msg, "👤 %s\n", formatUserLabel(inv.UserFirstName, inv.UserUsername, *inv.UsedBy))

			if inv.UsedAt != nil {
				fmt.Fprintf(&msg, "📅 %s\n", inv.UsedAt.Format("02.01.06 15:04"))
			}
		} else {
			msg.WriteString("⭕ Не использован\n")
			fmt.Fprintf(&msg, "🔹 Код: <code>%s</code>\n", inv.Code)
			fmt.Fprintf(&msg, "📅 Создан: %s\n", inv.CreatedAt.Format("02.01.06 15:04"))
		}
		msg.WriteString("\n")
	}

	return c.Send(msg.String(), &tele.SendOptions{
		ParseMode:   tele.ModeHTML,
		ReplyMarkup: ModeratorMenuKeyboard(),
	})
}

// handleModSubscribers показывает подписчиков модератора и их статусы.
// Использует batch-запрос к Remnawave для получения всех пользователей сразу.
func (b *Bot) handleModSubscribers(c tele.Context) error {
	telegramID := c.Sender().ID

	subscribers, err := b.db.GetSubscribersByModerator(telegramID)
	if err != nil {
		slog.Error("Failed to get subscribers by moderator", "error", err, "moderator_id", telegramID)
		return c.Send("Ошибка получения списка подписчиков", &tele.SendOptions{ReplyMarkup: ModeratorMenuKeyboard()})
	}

	if len(subscribers) == 0 {
		return c.Send("👥 У вас пока нет подписчиков", &tele.SendOptions{
			ParseMode:   tele.ModeHTML,
			ReplyMarkup: ModeratorMenuKeyboard(),
		})
	}

	// Загружаем всех пользователей из Remnawave одним batch-запросом
	remUsers, err := b.remnawave.GetAllUsers()
	if err != nil {
		slog.Error("Failed to get all users from Remnawave for subscribers list", "error", err, "moderator_id", telegramID)
		return c.Send("Ошибка получения данных из системы", &tele.SendOptions{ReplyMarkup: ModeratorMenuKeyboard()})
	}

	// Строим lookup-таблицу uuid -> User
	remByUUID := make(map[string]remnawave.User, len(remUsers))
	for _, u := range remUsers {
		remByUUID[u.UUID] = u
	}

	sort.Slice(subscribers, func(i, j int) bool { return subscribers[i].TelegramID < subscribers[j].TelegramID })

	now := time.Now().UTC()
	activeCount := 0
	expiredCount := 0
	deletedCount := 0

	var msg strings.Builder
	fmt.Fprintf(&msg, "<b>👥 Мои подписчики (%d)</b>\n\n", len(subscribers))

	for _, sub := range subscribers {
		if sub.RemnawaveUUID == nil {
			deletedCount++
			fmt.Fprintf(&msg, "❌ ID: <code>%d</code> — удалён\n\n", sub.TelegramID)
			continue
		}

		remUser, exists := remByUUID[*sub.RemnawaveUUID]
		if !exists {
			// Пользователь есть в БД бота, но уже нет в Remnawave
			deletedCount++
			fmt.Fprintf(&msg, "❌ ID: <code>%d</code> — удалён\n\n", sub.TelegramID)
			continue
		}

		label := formatSubscriberLabel(sub)
		if remUser.Status == remnawave.StatusExpired || remUser.ExpireAt.Before(now) {
			expiredCount++
			daysToKick := int(remUser.ExpireAt.AddDate(0, 0, 3).Sub(now).Hours()/24) + 1
			if daysToKick < 0 {
				daysToKick = 0
			}
			fmt.Fprintf(&msg, "⏰ %s\n", label)
			fmt.Fprintf(&msg, "   истёк %s (кик через %d дн.)\n\n", remUser.ExpireAt.Format("02.01.06"), daysToKick)
			continue
		}

		activeCount++
		daysLeft := int(remUser.ExpireAt.Sub(now).Hours()/24) + 1
		if daysLeft < 0 {
			daysLeft = 0
		}
		fmt.Fprintf(&msg, "✅ %s\n", label)
		fmt.Fprintf(&msg, "   до %s (осталось %d дн.)\n\n", remUser.ExpireAt.Format("02.01.06"), daysLeft)
	}

	msg.WriteString("───\n")
	fmt.Fprintf(&msg, "✅ Активных: %d │ ⏰ Истекших: %d │ ❌ Удалённых: %d", activeCount, expiredCount, deletedCount)

	return c.Send(msg.String(), &tele.SendOptions{
		ParseMode:   tele.ModeHTML,
		ReplyMarkup: ModeratorMenuKeyboard(),
	})
}

// handleModExtend запускает диалог продления подписки.
func (b *Bot) handleModExtend(c tele.Context) error {
	telegramID := c.Sender().ID
	subscribers, err := b.db.GetSubscribersByModerator(telegramID)
	if err != nil {
		slog.Error("Failed to load moderator subscribers for extend", "error", err, "moderator_id", telegramID)
		return c.Send("Ошибка получения подписчиков", &tele.SendOptions{ReplyMarkup: ModeratorMenuKeyboard()})
	}
	if len(subscribers) == 0 {
		return c.Send("👥 У вас пока нет подписчиков для продления.", &tele.SendOptions{ReplyMarkup: ModeratorMenuKeyboard()})
	}

	sort.Slice(subscribers, func(i, j int) bool { return subscribers[i].TelegramID < subscribers[j].TelegramID })

	var msg strings.Builder
	msg.WriteString("<b>⏳ Продление подписки</b>\n\n")
	msg.WriteString("Подписчики:\n")
	for _, sub := range subscribers {
		if sub.RemnawaveUUID == nil {
			fmt.Fprintf(&msg, "❌ <code>%d</code> — удалён\n", sub.TelegramID)
			continue
		}
		fmt.Fprintf(&msg, "• %s\n", formatSubscriberLabel(sub))
	}
	msg.WriteString("\nВведите telegram_id подписчика:")

	b.userStates.Set(telegramID, StateWaitModExtendID)
	b.clearModExtendSession(telegramID)

	return c.Send(msg.String(), &tele.SendOptions{
		ParseMode:   tele.ModeHTML,
		ReplyMarkup: CancelKeyboard(),
	})
}

func (b *Bot) processModExtendID(c tele.Context, text string) error {
	moderatorID := c.Sender().ID
	targetID, err := strconv.ParseInt(strings.TrimSpace(text), 10, 64)
	if err != nil {
		return c.Send("❌ Неверный telegram_id. Введите число.", &tele.SendOptions{ReplyMarkup: CancelKeyboard()})
	}

	owned, err := b.db.IsSubscriberOfModerator(moderatorID, targetID)
	if err != nil {
		slog.Error("Failed to verify subscriber ownership", "error", err, "moderator_id", moderatorID, "target_id", targetID)
		b.userStates.Delete(moderatorID)
		return c.Send("Ошибка проверки подписчика", &tele.SendOptions{ReplyMarkup: ModeratorMenuKeyboard()})
	}
	if !owned {
		return c.Send("❌ Можно продлевать только своих подписчиков.", &tele.SendOptions{ReplyMarkup: CancelKeyboard()})
	}

	dbUser, err := b.db.GetUserByTelegramID(targetID)
	if err != nil {
		slog.Error("Failed to load subscriber from DB", "error", err, "target_id", targetID)
		return c.Send("Ошибка получения данных подписчика", &tele.SendOptions{ReplyMarkup: CancelKeyboard()})
	}
	if dbUser == nil {
		b.userStates.Delete(moderatorID)
		return c.Send("❌ Пользователь уже удалён из системы.", &tele.SendOptions{ReplyMarkup: ModeratorMenuKeyboard()})
	}

	remUser, err := b.remnawave.GetUser(dbUser.RemnawaveUUID)
	if err != nil {
		if strings.Contains(err.Error(), "API error 404") {
			b.userStates.Delete(moderatorID)
			return c.Send("❌ Пользователь уже удалён из системы.", &tele.SendOptions{ReplyMarkup: ModeratorMenuKeyboard()})
		}
		slog.Error("Failed to get user from Remnawave", "error", err, "target_id", targetID)
		return c.Send("Ошибка получения статуса пользователя", &tele.SendOptions{ReplyMarkup: CancelKeyboard()})
	}

	newExpireAt, err := remnawave.CalculateExtendedExpireAt(remUser.ExpireAt, time.Now().UTC(), 30)
	if err != nil {
		b.userStates.Delete(moderatorID)
		return c.Send(err.Error(), &tele.SendOptions{ReplyMarkup: ModeratorMenuKeyboard()})
	}

	label := dbUser.Username
	if label == "" {
		label = fmt.Sprintf("%d", targetID)
	} else {
		label = "@" + label
	}

	b.setModExtendSession(moderatorID, modExtendSession{
		SubscriberTelegramID: targetID,
		SubscriberLabel:      label,
		UserUUID:             dbUser.RemnawaveUUID,
		NewExpireAt:          newExpireAt,
	})
	b.userStates.Set(moderatorID, StateWaitModExtendConfirm)

	return c.Send(
		fmt.Sprintf(
			"Продлить подписку %s на 30 дней? (до %s).\nОтправьте 'да' для подтверждения или 'нет' для отмены.",
			label,
			newExpireAt.Format("02.01.06"),
		),
		&tele.SendOptions{ReplyMarkup: CancelKeyboard()},
	)
}

func (b *Bot) processModExtendConfirm(c tele.Context, text string) error {
	moderatorID := c.Sender().ID
	answer := strings.ToLower(strings.TrimSpace(text))

	switch answer {
	case "нет":
		b.userStates.Delete(moderatorID)
		b.clearModExtendSession(moderatorID)
		return c.Send("Отменено", &tele.SendOptions{ReplyMarkup: ModeratorMenuKeyboard()})
	case "да":
		// Продолжаем.
	default:
		return c.Send("Ответьте 'да' или 'нет'.", &tele.SendOptions{ReplyMarkup: CancelKeyboard()})
	}

	session, ok := b.getModExtendSession(moderatorID)
	if !ok {
		b.userStates.Delete(moderatorID)
		return c.Send("Сессия продления потеряна. Начните заново.", &tele.SendOptions{ReplyMarkup: ModeratorMenuKeyboard()})
	}

	if err := b.remnawave.ExtendUserSubscription(session.UserUUID, 30); err != nil {
		b.userStates.Delete(moderatorID)
		b.clearModExtendSession(moderatorID)
		return c.Send(err.Error(), &tele.SendOptions{ReplyMarkup: ModeratorMenuKeyboard()})
	}

	if err := b.db.ClearNotifications(session.SubscriberTelegramID); err != nil {
		slog.Error("Failed to clear notification markers after extension", "error", err, "telegram_id", session.SubscriberTelegramID)
	}

	b.userStates.Delete(moderatorID)
	b.clearModExtendSession(moderatorID)

	return c.Send(
		fmt.Sprintf("✅ Подписка %s продлена до %s.", session.SubscriberLabel, session.NewExpireAt.Format("02.01.06")),
		&tele.SendOptions{ReplyMarkup: ModeratorMenuKeyboard()},
	)
}

func formatSubscriberLabel(sub database.Subscriber) string {
	firstName := ""
	if sub.FirstName != nil {
		firstName = *sub.FirstName
	}
	username := ""
	if sub.Username != nil {
		username = *sub.Username
	}
	return formatUserLabel(firstName, username, sub.TelegramID)
}

func (b *Bot) setModExtendSession(moderatorID int64, session modExtendSession) {
	b.modExtendMu.Lock()
	defer b.modExtendMu.Unlock()
	if b.modExtendData == nil {
		b.modExtendData = make(map[int64]modExtendSession)
	}
	b.modExtendData[moderatorID] = session
}

func (b *Bot) getModExtendSession(moderatorID int64) (modExtendSession, bool) {
	b.modExtendMu.RLock()
	defer b.modExtendMu.RUnlock()
	session, ok := b.modExtendData[moderatorID]
	return session, ok
}

func (b *Bot) clearModExtendSession(moderatorID int64) {
	b.modExtendMu.Lock()
	defer b.modExtendMu.Unlock()
	delete(b.modExtendData, moderatorID)
}

// handleModeratorDeleteInviteRequest запрашивает код для удаления
func (b *Bot) handleModeratorDeleteInviteRequest(c tele.Context) error {
	b.userStates.Set(c.Sender().ID, StateWaitModDeleteInvite)
	return c.Send("<b>🗑 Удаление приглашения</b>\n\nВведите код для удаления:", &tele.SendOptions{
		ParseMode:   tele.ModeHTML,
		ReplyMarkup: CancelKeyboard(),
	})
}

// processModeratorDeleteInvite обрабатывает удаление инвайта модератором
func (b *Bot) processModeratorDeleteInvite(c tele.Context, code string) error {
	b.userStates.Delete(c.Sender().ID)
	code = strings.TrimSpace(code)

	err := b.db.DeleteUnusedInviteByOwner(code, c.Sender().ID)
	if err != nil {
		if strings.Contains(err.Error(), "not found") || strings.Contains(err.Error(), "not owned") {
			return c.Send("❌ Код не найден, уже использован или не ваш.\nМожно удалить только свои неиспользованные коды.", &tele.SendOptions{
				ReplyMarkup: ModeratorMenuKeyboard(),
			})
		}
		slog.Error("Failed to delete invite by moderator", "error", err)
		return c.Send("Ошибка удаления кода", &tele.SendOptions{ReplyMarkup: ModeratorMenuKeyboard()})
	}

	return c.Send(fmt.Sprintf("✅ Код <code>%s</code> удалён", code), &tele.SendOptions{
		ParseMode:   tele.ModeHTML,
		ReplyMarkup: ModeratorMenuKeyboard(),
	})
}

// handleModeratorBack возвращает модератора в пользовательское меню
func (b *Bot) handleModeratorBack(c tele.Context) error {
	b.userStates.Delete(c.Sender().ID)
	return c.Send(MsgWelcomeBack, &tele.SendOptions{
		ParseMode:   tele.ModeHTML,
		ReplyMarkup: UserMenuKeyboardModerator(),
	})
}

// cascadeDeleteModerator удаляет все неиспользованные инвайты модератора и снимает роль
func (b *Bot) cascadeDeleteModerator(telegramID int64) {
	// Удаляем неиспользованные инвайты
	count, err := b.db.DeleteUnusedInvitesByCreator(telegramID)
	if err != nil {
		slog.Error("Failed to delete moderator invites", "error", err, "telegram_id", telegramID)
	} else if count > 0 {
		slog.Info("Deleted unused invites of moderator", "count", count, "telegram_id", telegramID)
	}

	// Удаляем из таблицы модераторов
	if err := b.db.RemoveModerator(telegramID); err != nil {
		slog.Error("Failed to remove moderator", "error", err, "telegram_id", telegramID)
	}
}
