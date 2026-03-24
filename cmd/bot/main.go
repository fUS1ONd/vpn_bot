package main

import (
	"context"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/fus1ond/vpn_bot/internal/bot"
	"github.com/fus1ond/vpn_bot/internal/callback"
	"github.com/fus1ond/vpn_bot/internal/config"
	"github.com/fus1ond/vpn_bot/internal/database"
	"github.com/fus1ond/vpn_bot/internal/monitoring"
	"github.com/fus1ond/vpn_bot/internal/remnawave"
)

// runWithRestart запускает fn в цикле: при панике логирует, ждёт backoff и перезапускает.
// Если ctx отменён — выходит без retry.
func runWithRestart(ctx context.Context, name string, fn func()) {
	const maxBackoff = 5 * time.Minute
	backoff := 5 * time.Second

	for {
		panicked := func() (didPanic bool) {
			defer func() {
				if r := recover(); r != nil {
					slog.Error("goroutine panicked, will restart", "goroutine", name, "recover", r, "backoff", backoff)
					didPanic = true
				}
			}()
			fn()
			return false
		}()

		if !panicked {
			// fn вернулась штатно — проверяем, был ли это штатный shutdown
			if ctx.Err() != nil {
				return
			}
			// fn завершилась без паники и без отмены ctx — неожиданный выход, перезапускаем
			slog.Warn("goroutine exited unexpectedly, will restart", "goroutine", name, "backoff", backoff)
		}

		select {
		case <-ctx.Done():
			return
		case <-time.After(backoff):
		}

		// Экспоненциальный backoff до maxBackoff
		backoff *= 2
		if backoff > maxBackoff {
			backoff = maxBackoff
		}
	}
}

func main() {
	// Настройка логирования
	logger := slog.New(slog.NewTextHandler(os.Stdout, &slog.HandlerOptions{
		Level: slog.LevelInfo,
	}))
	slog.SetDefault(logger)

	slog.Info("Starting VPN Bot v2 (Remnawave)...")

	// Загрузка конфигурации
	cfg, err := config.Load()
	if err != nil {
		slog.Error("Failed to load config", "error", err)
		os.Exit(1)
	}

	// Инициализация базы данных
	db, err := database.New(cfg.DBPath)
	if err != nil {
		slog.Error("Failed to initialize database", "error", err)
		os.Exit(1)
	}
	defer db.Close()

	// Создание клиента Remnawave API
	remnawaveClient := remnawave.NewClient(
		cfg.RemnawaveURL,
		cfg.RemnawaveAPIToken,
		cfg.RemnawaveSquadUUIDs,
	)

	// Восстановление регистраций, застрявших после краша между Remnawave и локальной БД.
	if stats, err := bot.ReconcileOrphanedRegistrations(db, remnawaveClient); err != nil {
		slog.Error("Failed to reconcile orphaned registrations", "error", err)
	} else if stats.RestoredUsers > 0 || stats.ReleasedInvites > 0 {
		slog.Warn("Reconciled orphaned registrations on startup",
			"restored_users", stats.RestoredUsers,
			"released_invites", stats.ReleasedInvites,
		)
	}

	// Создание и запуск Telegram бота
	telegramBot, err := bot.New(cfg, db, remnawaveClient)
	if err != nil {
		slog.Error("Failed to create Telegram bot", "error", err)
		os.Exit(1)
	}

	// Настройка graceful shutdown
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	go func() {
		sigChan := make(chan os.Signal, 1)
		signal.Notify(sigChan, os.Interrupt, syscall.SIGTERM)
		<-sigChan
		slog.Info("Shutdown signal received, stopping bot...")
		cancel()
		telegramBot.Stop()
	}()

	// Запуск callback-сервера (если Platega настроена)
	if cfg.PlategaMerchantID != "" && cfg.PlategaSecret != "" {
		callbackServer := callback.NewServer(cfg.CallbackPort, cfg.PlategaMerchantID, cfg.PlategaSecret, telegramBot.PaymentCallbackHandler())

		go runWithRestart(ctx, "callback-server", func() {
			if err := callbackServer.Start(); err != nil && err != http.ErrServerClosed {
				slog.Error("Callback server error", "error", err)
			}
		})

		go func() {
			<-ctx.Done()
			shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			if err := callbackServer.Shutdown(shutdownCtx); err != nil {
				slog.Error("Callback server shutdown error", "error", err)
			}
		}()

		slog.Info("Platega callback server started", "port", cfg.CallbackPort)
	}

	// Запуск фоновой синхронизации targets.json для мониторинга нод
	go runWithRestart(ctx, "sync-loop", func() {
		monitoring.StartSyncLoop(ctx, remnawaveClient, cfg.SDConfigsPath)
	})

	// Запуск алертера (проверка состояния нод раз в минуту)
	alertSender := bot.NewBotAlertSender(telegramBot)
	go runWithRestart(ctx, "alerter", func() {
		monitoring.StartAlerter(ctx, telegramBot.MetricsClient(), cfg.SDConfigsPath, alertSender)
	})

	// Запуск ежедневного scheduler подписок (уведомления и автокик).
	go runWithRestart(ctx, "scheduler", func() {
		telegramBot.StartScheduler(ctx)
	})

	// Запуск бота (блокирующий вызов)
	telegramBot.Run()

	<-ctx.Done()
	slog.Info("Bot stopped")
}
