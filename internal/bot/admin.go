package bot

import (
	"fmt"
	"log/slog"
	"strconv"
	"strings"
	"time"

	"github.com/fus1ond/vpn_bot/internal/database"
	"github.com/fus1ond/vpn_bot/internal/remnawave"
	tele "gopkg.in/telebot.v3"
)

// Состояния админа
const (
	StateWaitBanUser         = "wait_ban_user"         // Ожидание telegram_id для бана
	StateWaitDeleteInvite    = "wait_delete_invite"    // Ожидание кода для удаления
	StateWaitAddModerator    = "wait_add_moderator"    // Ожидание telegram_id для назначения модератора
	StateWaitRemoveModerator = "wait_remove_moderator" // Ожидание telegram_id для снятия модератора
)

// isAdmin проверяет, является ли пользователь админом
func (b *Bot) isAdmin(c tele.Context) bool {
	return c.Sender().ID == b.config.AdminID
}

// handleAdminStart показывает главное меню админа
func (b *Bot) handleAdminStart(c tele.Context) error {
	return c.Send(MsgAdminWelcome, &tele.SendOptions{
		ParseMode:   tele.ModeHTML,
		ReplyMarkup: AdminKeyboard(),
	})
}

// handleAdminManageMenu показывает меню управления
func (b *Bot) handleAdminManageMenu(c tele.Context) error {
	if !b.isAdmin(c) {
		return nil
	}
	return c.Send("<b>Управление</b>\n\nВыберите действие:", &tele.SendOptions{
		ParseMode:   tele.ModeHTML,
		ReplyMarkup: AdminManageKeyboard(),
	})
}

// handleCreateInvite создаёт новый инвайт-код
func (b *Bot) handleCreateInvite(c tele.Context) error {
	if !b.isAdmin(c) {
		return nil
	}

	// Создаём инвайт (код генерируется автоматически в БД)
	invite, err := b.db.CreateInviteWithExpiry(c.Sender().ID, nil)
	if err != nil {
		slog.Error("Failed to create invite", "error", err)
		return c.Send("Ошибка создания инвайта", &tele.SendOptions{ReplyMarkup: AdminManageKeyboard()})
	}

	msg := fmt.Sprintf(MsgInviteCreated, b.getBotUsername(), invite.Code)
	return c.Send(msg, &tele.SendOptions{
		ParseMode:   tele.ModeHTML,
		ReplyMarkup: AdminManageKeyboard(),
	})
}

// handleBanUserRequest запрашивает telegram_id для бана
func (b *Bot) handleBanUserRequest(c tele.Context) error {
	if !b.isAdmin(c) {
		return nil
	}

	b.userStates.Set(c.Sender().ID, StateWaitBanUser)
	return c.Send(MsgEnterBanUser, &tele.SendOptions{
		ParseMode:   tele.ModeHTML,
		ReplyMarkup: CancelKeyboard(),
	})
}

// processBanUser обрабатывает бан пользователя
func (b *Bot) processBanUser(c tele.Context, text string) error {
	b.userStates.Delete(c.Sender().ID)

	telegramID, err := strconv.ParseInt(strings.TrimSpace(text), 10, 64)
	if err != nil {
		return c.Send("Неверный telegram_id", &tele.SendOptions{ReplyMarkup: AdminManageKeyboard()})
	}

	// Защита от само-бана
	if telegramID == b.config.AdminID {
		return c.Send("❌ Нельзя забанить себя", &tele.SendOptions{ReplyMarkup: AdminManageKeyboard()})
	}

	// Находим пользователя в БД
	user, err := b.db.GetUserByTelegramID(telegramID)
	if err != nil || user == nil {
		return c.Send("Пользователь не найден", &tele.SendOptions{ReplyMarkup: AdminManageKeyboard()})
	}

	// Фиксируем перманентный бан.
	if err := b.db.BanUser(telegramID, c.Sender().ID); err != nil {
		slog.Error("Failed to persist user ban", "error", err, "telegram_id", telegramID)
		return c.Send("Ошибка сохранения бана", &tele.SendOptions{ReplyMarkup: AdminManageKeyboard()})
	}

	// Каскадное удаление: если пользователь — модератор
	if b.isModerator(telegramID) {
		b.cascadeDeleteModerator(telegramID)
	}

	// Удаляем из Remnawave (отключаем доступ к серверам)
	err = b.remnawave.DeleteUser(user.RemnawaveUUID)
	if err != nil {
		slog.Error("Failed to delete user from Remnawave", "error", err)
		// Продолжаем удаление из БД даже если не удалось удалить из Remnawave
	}

	// Удаляем из БД бота (отключаем доступ к боту)
	err = b.db.DeleteUser(telegramID)
	if err != nil {
		slog.Error("Failed to delete user from DB", "error", err)
		return c.Send("Ошибка удаления пользователя из БД", &tele.SendOptions{ReplyMarkup: AdminManageKeyboard()})
	}

	// Очищаем маркеры отправленных уведомлений.
	if err := b.db.ClearNotifications(telegramID); err != nil {
		slog.Error("Failed to clear notifications for banned user", "error", err, "telegram_id", telegramID)
	}

	msg := fmt.Sprintf("🚫 Пользователь %d забанен\n• Удалён из БД бота\n• Удалён из Remnawave", telegramID)
	return c.Send(msg, &tele.SendOptions{
		ParseMode:   tele.ModeHTML,
		ReplyMarkup: AdminManageKeyboard(),
	})
}

// handleAdminBroadcastMenu показывает меню рассылки
func (b *Bot) handleAdminBroadcastMenu(c tele.Context) error {
	if !b.isAdmin(c) {
		return nil
	}

	// Получаем количество активных пользователей из Remnawave
	users, err := b.remnawave.GetAllUsers()
	activeCount := 0
	if err != nil {
		slog.Error("Failed to get users from Remnawave for broadcast menu", "error", err)
	} else {
		slog.Info("Got users from Remnawave", "total", len(users))
		for _, u := range users {
			// Считаем только активных пользователей с привязанным Telegram ID
			if u.Status == remnawave.StatusActive && u.TelegramID != nil && *u.TelegramID != 0 {
				activeCount++
			}
		}
		slog.Info("Active users with TelegramID", "count", activeCount)
	}

	msg := fmt.Sprintf(MsgAdminBroadcastMenu, activeCount)
	return c.Send(msg, &tele.SendOptions{
		ParseMode:   tele.ModeHTML,
		ReplyMarkup: AdminBroadcastKeyboard(),
	})
}

// handleBroadcastActiveRequest запрашивает сообщение для рассылки активным
func (b *Bot) handleBroadcastActiveRequest(c tele.Context) error {
	if !b.isAdmin(c) {
		return nil
	}

	// Получаем количество активных с Telegram ID
	users, err := b.remnawave.GetAllUsers()
	activeCount := 0
	if err == nil {
		for _, u := range users {
			// Считаем только активных пользователей с привязанным Telegram ID
			if u.Status == remnawave.StatusActive && u.TelegramID != nil && *u.TelegramID != 0 {
				activeCount++
			}
		}
	}

	b.userStates.Set(c.Sender().ID, StateWaitBroadcastActive)
	return c.Send(fmt.Sprintf(MsgAdminEnterBroadcast, activeCount), &tele.SendOptions{
		ParseMode:   tele.ModeHTML,
		ReplyMarkup: CancelKeyboard(),
	})
}

// processBroadcastMessage отправляет рассылку активным пользователям
func (b *Bot) processBroadcastMessage(c tele.Context) error {
	b.userStates.Delete(c.Sender().ID)

	// Получаем всех активных пользователей из Remnawave
	remnawaveUsers, err := b.remnawave.GetAllUsers()
	if err != nil {
		slog.Error("Failed to get users from Remnawave", "error", err)
		return c.Send("Ошибка получения списка пользователей", &tele.SendOptions{ReplyMarkup: AdminBroadcastKeyboard()})
	}

	// Фильтруем только активных с TelegramID
	var activeUsers []remnawave.User
	for _, u := range remnawaveUsers {
		if u.Status == remnawave.StatusActive && u.TelegramID != nil && *u.TelegramID != 0 {
			activeUsers = append(activeUsers, u)
		}
	}

	if len(activeUsers) == 0 {
		return c.Send("Нет активных пользователей для рассылки", &tele.SendOptions{ReplyMarkup: AdminBroadcastKeyboard()})
	}

	// Отправляем статус
	statusMsg, err := c.Bot().Send(c.Sender(), fmt.Sprintf("Начинаю рассылку %d активным пользователям...", len(activeUsers)))
	if err != nil {
		return err
	}

	successCount := 0
	failCount := 0
	msg := c.Message()

	for _, user := range activeUsers {
		recipient := &tele.User{ID: *user.TelegramID}

		// Копируем сообщение (работает для текста, фото, видео и т.д.)
		_, err := c.Bot().Copy(recipient, msg, &tele.SendOptions{
			ParseMode: tele.ModeHTML,
		})

		if err != nil {
			slog.Error("Failed to send broadcast", "telegram_id", *user.TelegramID, "error", err)
			failCount++
		} else {
			successCount++
		}

		// Задержка для избежания rate limiting
		time.Sleep(50 * time.Millisecond)
	}

	resultMsg := fmt.Sprintf(MsgAdminBroadcastResult, successCount, failCount)
	_, _ = c.Bot().Edit(statusMsg, resultMsg, &tele.SendOptions{
		ParseMode:   tele.ModeHTML,
		ReplyMarkup: AdminBroadcastKeyboard(),
	})

	return nil
}

// handleViewInvites показывает список всех инвайт-кодов
func (b *Bot) handleViewInvites(c tele.Context) error {
	if !b.isAdmin(c) {
		return nil
	}

	invites, err := b.db.GetAllInvitesWithUsers()
	if err != nil {
		slog.Error("Failed to get invites", "error", err)
		return c.Send("Ошибка получения списка кодов", &tele.SendOptions{
			ReplyMarkup: AdminManageKeyboard(),
		})
	}

	if len(invites) == 0 {
		return c.Send("📋 Инвайт-кодов пока нет", &tele.SendOptions{
			ParseMode:   tele.ModeHTML,
			ReplyMarkup: AdminManageKeyboard(),
		})
	}

	chunks := FormatInvitesListChunked(invites, 4000)
	for i, chunk := range chunks {
		opts := &tele.SendOptions{ParseMode: tele.ModeHTML}
		// Клавиатуру показываем только в последнем сообщении
		if i == len(chunks)-1 {
			opts.ReplyMarkup = AdminManageKeyboard()
		}
		if err := c.Send(chunk, opts); err != nil {
			return err
		}
	}
	return nil
}

// FormatInvitesListChunked разбивает список инвайтов на части, не превышающие maxLen символов
func FormatInvitesListChunked(invites []database.InviteWithUser, maxLen int) []string {
	if len(invites) == 0 {
		return nil
	}

	var chunks []string
	var current strings.Builder
	current.WriteString("<b>📋 Список инвайт-кодов</b>\n\n")

	for _, inv := range invites {
		entry := formatInviteEntry(inv)

		// Если добавление записи превысит лимит — сохраняем текущий чанк и начинаем новый
		if current.Len()+len(entry) > maxLen && current.Len() > 0 {
			chunks = append(chunks, current.String())
			current.Reset()
			current.WriteString("<b>📋 Список инвайт-кодов (продолжение)</b>\n\n")
		}

		current.WriteString(entry)
	}

	if current.Len() > 0 {
		chunks = append(chunks, current.String())
	}

	return chunks
}

// formatInviteEntry форматирует один инвайт для списка
func formatInviteEntry(inv database.InviteWithUser) string {
	var msg strings.Builder
	if inv.UsedBy != nil {
		msg.WriteString("✅ <b>Использован</b>\n")
		msg.WriteString(fmt.Sprintf("🔹 Код: <code>%s</code>\n", inv.Code))

		userLink := fmt.Sprintf("<a href=\"tg://user?id=%d\">%d</a>", *inv.UsedBy, *inv.UsedBy)
		if inv.UserUsername != "" {
			msg.WriteString(fmt.Sprintf("👤 @%s (%s)", inv.UserUsername, userLink))
		} else {
			msg.WriteString(fmt.Sprintf("👤 %s", userLink))
		}

		if inv.UserFirstName != "" {
			msg.WriteString(fmt.Sprintf(" • %s", inv.UserFirstName))
		}
		msg.WriteString("\n")

		if inv.UsedAt != nil {
			msg.WriteString(fmt.Sprintf("📅 %s\n", inv.UsedAt.Format("02.01.06 15:04")))
		}
	} else {
		msg.WriteString("⭕ <b>Не использован</b>\n")
		msg.WriteString(fmt.Sprintf("🔹 Код: <code>%s</code>\n", inv.Code))
		msg.WriteString(fmt.Sprintf("📅 Создан: %s\n", inv.CreatedAt.Format("02.01.06 15:04")))
	}

	// Автор кода (модератор или админ)
	if inv.CreatorUsername != "" {
		fmt.Fprintf(&msg, "✍️ @%s\n", inv.CreatorUsername)
	} else if inv.CreatorFirstName != "" {
		fmt.Fprintf(&msg, "✍️ %s\n", inv.CreatorFirstName)
	}

	msg.WriteString("\n")
	return msg.String()
}

// handleDeleteInviteRequest запрашивает код для удаления
func (b *Bot) handleDeleteInviteRequest(c tele.Context) error {
	if !b.isAdmin(c) {
		return nil
	}

	b.userStates.Set(c.Sender().ID, StateWaitDeleteInvite)
	return c.Send("<b>🗑 Удаление инвайт-кода</b>\n\nВведите код для удаления:", &tele.SendOptions{
		ParseMode:   tele.ModeHTML,
		ReplyMarkup: CancelKeyboard(),
	})
}

// processDeleteInvite обрабатывает удаление инвайта
func (b *Bot) processDeleteInvite(c tele.Context, code string) error {
	b.userStates.Delete(c.Sender().ID)

	code = strings.TrimSpace(code)

	err := b.db.DeleteUnusedInvite(code)
	if err != nil {
		if strings.Contains(err.Error(), "not found or already used") {
			return c.Send("❌ Код не найден или уже использован.\nМожно удалить только неиспользованные коды.", &tele.SendOptions{
				ReplyMarkup: AdminManageKeyboard(),
			})
		}
		slog.Error("Failed to delete invite", "error", err)
		return c.Send("Ошибка удаления кода", &tele.SendOptions{
			ReplyMarkup: AdminManageKeyboard(),
		})
	}

	return c.Send(fmt.Sprintf("✅ Код <code>%s</code> удалён", code), &tele.SendOptions{
		ParseMode:   tele.ModeHTML,
		ReplyMarkup: AdminManageKeyboard(),
	})
}

// --- Управление модераторами ---

// handleAdminModeratorMenu показывает меню управления модераторами
func (b *Bot) handleAdminModeratorMenu(c tele.Context) error {
	if !b.isAdmin(c) {
		return nil
	}
	return c.Send("<b>👥 Модераторы</b>\n\nВыберите действие:", &tele.SendOptions{
		ParseMode:   tele.ModeHTML,
		ReplyMarkup: AdminModeratorKeyboard(),
	})
}

// handleAdminAddModeratorRequest запрашивает telegram_id для назначения модератора
func (b *Bot) handleAdminAddModeratorRequest(c tele.Context) error {
	if !b.isAdmin(c) {
		return nil
	}

	b.userStates.Set(c.Sender().ID, StateWaitAddModerator)
	return c.Send("<b>➕ Назначить модератора</b>\n\nВведите telegram_id зарегистрированного пользователя:", &tele.SendOptions{
		ParseMode:   tele.ModeHTML,
		ReplyMarkup: CancelKeyboard(),
	})
}

// processAddModerator обрабатывает назначение модератора
func (b *Bot) processAddModerator(c tele.Context, text string) error {
	b.userStates.Delete(c.Sender().ID)

	telegramID, err := strconv.ParseInt(strings.TrimSpace(text), 10, 64)
	if err != nil {
		return c.Send("Неверный telegram_id", &tele.SendOptions{ReplyMarkup: AdminModeratorKeyboard()})
	}

	// Проверяем что пользователь существует
	user, err := b.db.GetUserByTelegramID(telegramID)
	if err != nil || user == nil {
		return c.Send("❌ Пользователь не найден в БД бота", &tele.SendOptions{ReplyMarkup: AdminModeratorKeyboard()})
	}

	// Назначать модератором можно только бессрочных пользователей (админский инвайт).
	invite, err := b.db.GetInviteByUsedBy(telegramID)
	if err != nil {
		slog.Error("Failed to get invite for moderator validation", "error", err, "telegram_id", telegramID)
		return c.Send("Ошибка проверки приглашения пользователя", &tele.SendOptions{ReplyMarkup: AdminModeratorKeyboard()})
	}
	if invite != nil && invite.ExpireDays != nil {
		return c.Send("❌ Этот пользователь приглашён по месячному инвайту. Назначить модератором можно только пользователя с бессрочным (админским) приглашением.", &tele.SendOptions{
			ReplyMarkup: AdminModeratorKeyboard(),
		})
	}

	err = b.db.AddModerator(telegramID, c.Sender().ID)
	if err != nil {
		if strings.Contains(err.Error(), "UNIQUE") {
			return c.Send("❌ Этот пользователь уже является модератором", &tele.SendOptions{ReplyMarkup: AdminModeratorKeyboard()})
		}
		slog.Error("Failed to add moderator", "error", err)
		return c.Send("Ошибка назначения модератора", &tele.SendOptions{ReplyMarkup: AdminModeratorKeyboard()})
	}

	msg := fmt.Sprintf("✅ Пользователь <code>%d</code> (@%s) назначен модератором", telegramID, user.Username)
	return c.Send(msg, &tele.SendOptions{
		ParseMode:   tele.ModeHTML,
		ReplyMarkup: AdminModeratorKeyboard(),
	})
}

// handleAdminListModerators показывает список модераторов
func (b *Bot) handleAdminListModerators(c tele.Context) error {
	if !b.isAdmin(c) {
		return nil
	}

	mods, err := b.db.GetAllModerators()
	if err != nil {
		slog.Error("Failed to get moderators", "error", err)
		return c.Send("Ошибка получения списка модераторов", &tele.SendOptions{ReplyMarkup: AdminModeratorKeyboard()})
	}

	if len(mods) == 0 {
		return c.Send("📋 Модераторов пока нет", &tele.SendOptions{
			ParseMode:   tele.ModeHTML,
			ReplyMarkup: AdminModeratorKeyboard(),
		})
	}

	var msg strings.Builder
	fmt.Fprintf(&msg, "<b>📋 Модераторы (%d)</b>\n\n", len(mods))

	for _, mod := range mods {
		if mod.Username != "" {
			fmt.Fprintf(&msg, "👤 @%s", mod.Username)
		} else {
			msg.WriteString("👤 без username")
		}
		if mod.FirstName != "" {
			fmt.Fprintf(&msg, " • %s", mod.FirstName)
		}
		msg.WriteString("\n")
		fmt.Fprintf(&msg, "🆔 <code>%d</code>\n", mod.TelegramID)
		fmt.Fprintf(&msg, "📨 Приглашено: %d\n\n", mod.InvitesCount)
	}

	return c.Send(msg.String(), &tele.SendOptions{
		ParseMode:   tele.ModeHTML,
		ReplyMarkup: AdminModeratorKeyboard(),
	})
}

// handleAdminModStats показывает сводную статистику модераторов.
func (b *Bot) handleAdminModStats(c tele.Context) error {
	if !b.isAdmin(c) {
		return nil
	}

	mods, err := b.db.GetAllModerators()
	if err != nil {
		slog.Error("Failed to load moderators for stats", "error", err)
		return c.Send("Ошибка получения списка модераторов", &tele.SendOptions{ReplyMarkup: AdminModeratorKeyboard()})
	}
	if len(mods) == 0 {
		return c.Send("📊 Модераторов пока нет", &tele.SendOptions{ReplyMarkup: AdminModeratorKeyboard()})
	}

	remUsers, err := b.remnawave.GetAllUsers()
	if err != nil {
		slog.Error("Failed to load users from Remnawave for moderator stats", "error", err)
		return c.Send("Ошибка получения статистики из панели", &tele.SendOptions{ReplyMarkup: AdminModeratorKeyboard()})
	}

	byTelegramID := make(map[int64]remnawave.User, len(remUsers))
	for _, user := range remUsers {
		if user.TelegramID == nil || *user.TelegramID == 0 {
			continue
		}
		byTelegramID[*user.TelegramID] = user
	}

	now := time.Now().UTC()
	totalActive := 0
	totalExpired := 0

	var msg strings.Builder
	msg.WriteString("<b>📊 Статистика модераторов</b>\n\n")

	for _, mod := range mods {
		subs, err := b.db.GetSubscribersByModerator(mod.TelegramID)
		if err != nil {
			slog.Error("Failed to load subscribers for moderator stats", "error", err, "moderator_id", mod.TelegramID)
			continue
		}

		active := 0
		expired := 0
		for _, sub := range subs {
			remUser, ok := byTelegramID[sub.TelegramID]
			if !ok {
				continue
			}
			if remUser.Status == remnawave.StatusExpired || remUser.ExpireAt.Before(now) {
				expired++
			} else {
				active++
			}
		}

		totalActive += active
		totalExpired += expired

		name := fmt.Sprintf("<code>%d</code>", mod.TelegramID)
		if mod.Username != "" {
			name = "@" + mod.Username
		}

		fmt.Fprintf(&msg, "👤 %s", name)
		if mod.FirstName != "" {
			fmt.Fprintf(&msg, " • %s", mod.FirstName)
		}
		msg.WriteString("\n")
		fmt.Fprintf(&msg, "   ✅ Активных: %d\n", active)
		fmt.Fprintf(&msg, "   ⏰ Истекших: %d\n", expired)
		fmt.Fprintf(&msg, "   👥 Всего приглашено: %d\n\n", len(subs))
	}

	msg.WriteString("───\n")
	fmt.Fprintf(&msg, "Итого: ✅ %d активных │ ⏰ %d истекших", totalActive, totalExpired)

	return c.Send(msg.String(), &tele.SendOptions{
		ParseMode:   tele.ModeHTML,
		ReplyMarkup: AdminModeratorKeyboard(),
	})
}

// handleAdminRemoveModeratorRequest запрашивает telegram_id для снятия модератора
func (b *Bot) handleAdminRemoveModeratorRequest(c tele.Context) error {
	if !b.isAdmin(c) {
		return nil
	}

	b.userStates.Set(c.Sender().ID, StateWaitRemoveModerator)
	return c.Send("<b>➖ Снять модератора</b>\n\nВведите telegram_id модератора:", &tele.SendOptions{
		ParseMode:   tele.ModeHTML,
		ReplyMarkup: CancelKeyboard(),
	})
}

// processRemoveModerator обрабатывает снятие модератора
func (b *Bot) processRemoveModerator(c tele.Context, text string) error {
	b.userStates.Delete(c.Sender().ID)

	telegramID, err := strconv.ParseInt(strings.TrimSpace(text), 10, 64)
	if err != nil {
		return c.Send("Неверный telegram_id", &tele.SendOptions{ReplyMarkup: AdminModeratorKeyboard()})
	}

	if !b.isModerator(telegramID) {
		return c.Send("❌ Этот пользователь не является модератором", &tele.SendOptions{ReplyMarkup: AdminModeratorKeyboard()})
	}

	// Каскадное удаление инвайтов и снятие роли
	b.cascadeDeleteModerator(telegramID)

	msg := fmt.Sprintf("✅ Модератор <code>%d</code> снят. Неиспользованные инвайты удалены.", telegramID)
	return c.Send(msg, &tele.SendOptions{
		ParseMode:   tele.ModeHTML,
		ReplyMarkup: AdminModeratorKeyboard(),
	})
}
