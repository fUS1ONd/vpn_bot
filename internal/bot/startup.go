package bot

import (
	"fmt"
	"strings"

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
			if isRemnawaveNotFound(err) {
				if err := db.UnclaimInvite(invite.Code, *invite.UsedBy); err != nil {
					return stats, fmt.Errorf("unclaim invite %s: %w", invite.Code, err)
				}
				stats.ReleasedInvites++
				continue
			}
			return stats, fmt.Errorf("get remote user by telegram_id=%d: %w", telegramID, err)
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
			remoteUser.UUID,
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

func isRemnawaveNotFound(err error) bool {
	if err == nil {
		return false
	}

	if strings.Contains(err.Error(), "API error 404") {
		return true
	}

	return strings.Contains(err.Error(), remnawave.ErrUserNotFound.Error())
}
