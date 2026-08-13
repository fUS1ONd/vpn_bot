package bot

import (
	"errors"
	"fmt"

	"github.com/fus1ond/vpn_bot/internal/database"
	"github.com/fus1ond/vpn_bot/internal/remnawave"
)

// StartupReconcileStats содержит итоги восстановления регистрации после перезапуска.
type StartupReconcileStats struct {
	RestoredUsers   int
	ReleasedInvites int
}

// ReconcileOrphanedRegistrations чинит застрявшие регистрации после падения процесса.
// Если пользователь уже успел создаться в Remnawave, восстанавливает локальную запись users.
// Если пользователя в панели нет, освобождает инвайт как и раньше.
func ReconcileOrphanedRegistrations(db *database.DB, client *remnawave.Client) (StartupReconcileStats, error) {
	var stats StartupReconcileStats

	invites, err := db.GetOrphanedInvites()
	if err != nil {
		return stats, fmt.Errorf("load orphaned invites: %w", err)
	}

	for _, invite := range invites {
		if invite.UsedBy == nil {
			continue
		}

		telegramID := *invite.UsedBy
		remoteUser, err := client.GetUserByTelegramID(telegramID)
		if err != nil {
			return stats, fmt.Errorf("get remote user by telegram_id=%d: %w", telegramID, err)
		}

		// Отсутствие пользователя — это (nil, nil), а не ошибка. Прежний код ждал
		// здесь именно ошибку, и на nil разыменовал бы remoteUser ниже — паника
		// при старте, до запуска бота.
		if remoteUser == nil {
			if err := db.UnclaimInvite(invite.Code, telegramID); err != nil {
				return stats, fmt.Errorf("unclaim invite %s: %w", invite.Code, err)
			}
			stats.ReleasedInvites++
			continue
		}

		username := remoteUser.Username
		if username == "" {
			username = fmt.Sprintf("tg_%d", telegramID)
		}

		invitedBy, err := db.GetFirstReferralInviter(telegramID)
		if err != nil {
			return stats, fmt.Errorf("load first inviter for telegram_id=%d: %w", telegramID, err)
		}

		if _, err := db.CreateUserWithInviter(
			telegramID,
			username,
			"",
			remnawaveUUIDPtr(remoteUser),
			&remoteUser.ID,
			invite.SubscriptionPrice,
			nil,
			invitedBy,
		); err != nil {
			return stats, fmt.Errorf("restore local user for telegram_id=%d: %w", telegramID, err)
		}

		stats.RestoredUsers++
	}

	return stats, nil
}

// isRemnawaveNotFound проверяет, что панель ответила «пользователя нет».
// Признак — HTTP-статус 404, а не подстрока в тексте ошибки.
func isRemnawaveNotFound(err error) bool {
	return err != nil && errors.Is(err, remnawave.ErrUserNotFound)
}
