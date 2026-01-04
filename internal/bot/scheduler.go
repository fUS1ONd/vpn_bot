package bot

import (
	"context"
	"log/slog"
	"time"

	"github.com/fus1ond/vpn_bot/internal/database"
)

// StartScheduler starts background tasks for periodic checks
func (b *Bot) StartScheduler(ctx context.Context) {
	slog.Info("Starting background scheduler")

	// Run traffic reset check every hour
	ticker := time.NewTicker(1 * time.Hour)
	defer ticker.Stop()

	// Run immediately on start
	b.checkUnlimitedUsersTraffic()

	for {
		select {
		case <-ctx.Done():
			slog.Info("Scheduler stopped")
			return
		case <-ticker.C:
			b.checkUnlimitedUsersTraffic()
		}
	}
}

// checkUnlimitedUsersTraffic checks all unlimited users and resets traffic if needed
func (b *Bot) checkUnlimitedUsersTraffic() {
	slog.Info("Checking unlimited users for traffic reset")

	users, err := b.db.GetUnlimitedUsers()
	if err != nil {
		slog.Error("Failed to get unlimited users", "error", err)
		return
	}

	if len(users) == 0 {
		slog.Info("No unlimited users found")
		return
	}

	slog.Info("Found unlimited users", "count", len(users))

	for _, user := range users {
		if !b.db.NeedsTrafficReset(&user) {
			continue
		}

		slog.Info("Resetting traffic for unlimited user", "email", user.Email, "last_reset", user.TrafficResetAt)

		if err := b.resetUnlimitedUserTraffic(&user); err != nil {
			slog.Error("Failed to reset traffic", "email", user.Email, "error", err)
			continue
		}

		slog.Info("Traffic reset successful", "email", user.Email)
	}
}

// resetUnlimitedUserTraffic resets traffic for an unlimited user on Server A
func (b *Bot) resetUnlimitedUserTraffic(user *database.User) error {
	// Login to Server A
	if err := b.clientA.Login(); err != nil {
		return err
	}

	// Reset traffic counter on panel
	if err := b.clientA.ResetClientTraffic(b.config.ServerA.InboundID, user.Email); err != nil {
		return err
	}

	// Update reset timestamp in database
	now := time.Now()
	if err := b.db.UpdateTrafficResetAt(user.ID, now); err != nil {
		return err
	}

	// Reset extra traffic (if any was added)
	if err := b.db.ResetRuExtraTraffic(user.ID); err != nil {
		return err
	}

	return nil
}
