package bot

import (
	"fmt"
	"log/slog"
	"strconv"
	"strings"
	"time"

	"github.com/fus1ond/vpn_bot/internal/remnawave"
	tele "gopkg.in/telebot.v3"
)

// Состояния админа
const (
	StateWaitBanUser = "wait_ban_user" // Ожидание telegram_id для бана
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
	invite, err := b.db.CreateInvite(c.Sender().ID)
	if err != nil {
		slog.Error("Failed to create invite", "error", err)
		return c.Send("Ошибка создания инвайта", &tele.SendOptions{ReplyMarkup: AdminManageKeyboard()})
	}

	msg := fmt.Sprintf(MsgInviteCreated, invite.Code)
	return c.Send(msg, &tele.SendOptions{
		ParseMode:   tele.ModeHTML,
		ReplyMarkup: AdminManageKeyboard(),
	})
}

// handleAddTrafficRequest запрашивает данные для добавления трафика
func (b *Bot) handleAddTrafficRequest(c tele.Context) error {
	if !b.isAdmin(c) {
		return nil
	}

	b.userStates[c.Sender().ID] = StateWaitAddTraffic
	return c.Send(MsgEnterAddTraffic, &tele.SendOptions{
		ParseMode:   tele.ModeHTML,
		ReplyMarkup: CancelKeyboard(),
	})
}

// processAddTraffic обрабатывает добавление трафика
func (b *Bot) processAddTraffic(c tele.Context, text string) error {
	delete(b.userStates, c.Sender().ID)

	// Формат: telegram_id GB
	parts := strings.Fields(text)
	if len(parts) != 2 {
		return c.Send("Неверный формат. Используйте: telegram_id GB", &tele.SendOptions{ReplyMarkup: AdminManageKeyboard()})
	}

	telegramID, err := strconv.ParseInt(parts[0], 10, 64)
	if err != nil {
		return c.Send("Неверный telegram_id", &tele.SendOptions{ReplyMarkup: AdminManageKeyboard()})
	}

	gb, err := strconv.ParseInt(parts[1], 10, 64)
	if err != nil || gb <= 0 {
		return c.Send("Неверное количество GB", &tele.SendOptions{ReplyMarkup: AdminManageKeyboard()})
	}

	// Находим пользователя
	user, err := b.db.GetUserByTelegramID(telegramID)
	if err != nil || user == nil {
		return c.Send("Пользователь не найден", &tele.SendOptions{ReplyMarkup: AdminManageKeyboard()})
	}

	// Получаем текущий лимит из Remnawave
	remnawaveUser, err := b.remnawave.GetUser(user.RemnawaveUUID)
	if err != nil {
		slog.Error("Failed to get user from Remnawave", "error", err)
		return c.Send("Ошибка получения данных пользователя", &tele.SendOptions{ReplyMarkup: AdminManageKeyboard()})
	}

	// Добавляем трафик к текущему лимиту
	newLimit := remnawaveUser.TrafficLimitBytes + (gb * 1024 * 1024 * 1024)
	err = b.remnawave.UpdateUserTraffic(user.RemnawaveUUID, newLimit)
	if err != nil {
		slog.Error("Failed to update traffic", "error", err)
		return c.Send("Ошибка обновления лимита трафика", &tele.SendOptions{ReplyMarkup: AdminManageKeyboard()})
	}

	msg := fmt.Sprintf("✅ Добавлено %d GB пользователю %d\nНовый лимит: %.1f GB",
		gb, telegramID, float64(newLimit)/(1024*1024*1024))
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

	b.userStates[c.Sender().ID] = StateWaitBanUser
	return c.Send(MsgEnterBanUser, &tele.SendOptions{
		ParseMode:   tele.ModeHTML,
		ReplyMarkup: CancelKeyboard(),
	})
}

// processBanUser обрабатывает бан пользователя
func (b *Bot) processBanUser(c tele.Context, text string) error {
	delete(b.userStates, c.Sender().ID)

	telegramID, err := strconv.ParseInt(strings.TrimSpace(text), 10, 64)
	if err != nil {
		return c.Send("Неверный telegram_id", &tele.SendOptions{ReplyMarkup: AdminManageKeyboard()})
	}

	// Находим пользователя в БД
	user, err := b.db.GetUserByTelegramID(telegramID)
	if err != nil || user == nil {
		return c.Send("Пользователь не найден", &tele.SendOptions{ReplyMarkup: AdminManageKeyboard()})
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

	b.userStates[c.Sender().ID] = StateWaitBroadcastActive
	return c.Send(fmt.Sprintf(MsgAdminEnterBroadcast, activeCount), &tele.SendOptions{
		ParseMode:   tele.ModeHTML,
		ReplyMarkup: CancelKeyboard(),
	})
}

// processBroadcastMessage отправляет рассылку только активным пользователям
func (b *Bot) processBroadcastMessage(c tele.Context, _ bool) error {
	delete(b.userStates, c.Sender().ID)

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

