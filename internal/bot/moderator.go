package bot

import (
	"fmt"
	"log/slog"
	"strings"

	tele "gopkg.in/telebot.v3"
)

// Состояния модератора
const (
	StateWaitModDeleteInvite = "wait_mod_delete_invite" // Модератор ждёт код для удаления
)

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
			if inv.UserUsername != "" {
				fmt.Fprintf(&msg, "👤 @%s", inv.UserUsername)
			} else {
				msg.WriteString("👤 пользователь")
			}
			if inv.UserFirstName != "" {
				fmt.Fprintf(&msg, " • %s", inv.UserFirstName)
			}
			fmt.Fprintf(&msg, " • ID: <code>%d</code>", *inv.UsedBy)
			msg.WriteString("\n")

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
