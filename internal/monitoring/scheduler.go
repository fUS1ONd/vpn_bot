package monitoring

import (
	"context"
	"log/slog"
	"time"

	"github.com/fus1ond/vpn_bot/internal/remnawave"
)

const syncInterval = 60 * time.Second

// StartSyncLoop запускает фоновый цикл синхронизации targets.json
func StartSyncLoop(ctx context.Context, client *remnawave.Client, sdConfigsPath string) {
	slog.Info("Запуск фоновой синхронизации targets", "interval", syncInterval)

	// Синхронизация при старте
	if _, err := SyncNodes(client, sdConfigsPath); err != nil {
		slog.Error("Ошибка первичной синхронизации targets", "error", err)
	}

	ticker := time.NewTicker(syncInterval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			slog.Info("Синхронизация targets остановлена")
			return
		case <-ticker.C:
			if _, err := SyncNodes(client, sdConfigsPath); err != nil {
				slog.Error("Ошибка синхронизации targets", "error", err)
			}
		}
	}
}
