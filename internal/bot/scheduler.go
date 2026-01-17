package bot

import (
	"context"
	"log/slog"
	"time"

	"github.com/fus1ond/vpn_bot/internal/remnawave"
)

// StartScheduler запускает фоновые задачи
func (b *Bot) StartScheduler(ctx context.Context) {
	slog.Info("Starting background scheduler")

	// Проверяем каждый день в 00:05
	ticker := time.NewTicker(24 * time.Hour)
	defer ticker.Stop()

	// Проверяем сразу при запуске
	b.checkAndResetTrafficLimits()

	for {
		select {
		case <-ctx.Done():
			slog.Info("Scheduler stopped")
			return
		case <-ticker.C:
			b.checkAndResetTrafficLimits()
		}
	}
}

// checkAndResetTrafficLimits проверяет и сбрасывает увеличенные лимиты 1-го числа
func (b *Bot) checkAndResetTrafficLimits() {
	now := time.Now()

	// Сброс только 1-го числа месяца
	if now.Day() != 1 {
		slog.Info("Not the 1st day of month, skipping traffic reset", "day", now.Day())
		return
	}

	slog.Info("1st day of month — checking traffic limits")

	// Получаем всех пользователей из Remnawave
	users, err := b.remnawave.GetAllUsers()
	if err != nil {
		slog.Error("Failed to get users from Remnawave", "error", err)
		return
	}

	resetCount := 0
	for _, user := range users {
		// Сбрасываем только если лимит больше базовых 30 GB
		if user.TrafficLimitBytes > remnawave.TrafficLimit30GB {
			slog.Info("Resetting traffic limit to 30 GB",
				"username", user.Username,
				"old_limit_gb", float64(user.TrafficLimitBytes)/(1024*1024*1024),
			)

			err := b.remnawave.UpdateUserTraffic(user.UUID, remnawave.TrafficLimit30GB)
			if err != nil {
				slog.Error("Failed to reset traffic limit", "uuid", user.UUID, "error", err)
				continue
			}
			resetCount++
		}
	}

	if resetCount > 0 {
		slog.Info("Traffic limits reset completed", "reset_count", resetCount)
	} else {
		slog.Info("No users with increased traffic limits found")
	}
}
