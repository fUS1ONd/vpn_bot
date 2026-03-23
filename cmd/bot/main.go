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
	"github.com/fus1ond/vpn_bot/internal/platega"
	"github.com/fus1ond/vpn_bot/internal/remnawave"
)

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

	// Откат инвайтов, зависших после краша (claimed но пользователь не создан)
	if count, err := db.ReconcileOrphanedInvites(); err != nil {
		slog.Error("Failed to reconcile orphaned invites", "error", err)
	} else if count > 0 {
		slog.Warn("Reconciled orphaned invites on startup", "count", count)
	}

	// Создание клиента Remnawave API
	remnawaveClient := remnawave.NewClient(
		cfg.RemnawaveURL,
		cfg.RemnawaveAPIToken,
		cfg.RemnawaveSquadUUIDs,
	)

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
	}()

	// Запуск callback-сервера (если Platega настроена)
	if cfg.PlategaMerchantID != "" && cfg.PlategaSecret != "" {
		// TODO(этап 4): заменить stubHandler на telegramBot.PaymentCallbackHandler()
		// когда метод будет реализован в боте
		stubHandler := &noopPaymentHandler{}
		callbackServer := callback.NewServer(cfg.CallbackPort, cfg.PlategaMerchantID, cfg.PlategaSecret, stubHandler)

		go func() {
			if err := callbackServer.Start(); err != nil && err != http.ErrServerClosed {
				slog.Error("Callback server error", "error", err)
			}
		}()

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
	go monitoring.StartSyncLoop(ctx, remnawaveClient, cfg.SDConfigsPath)

	// Запуск алертера (проверка состояния нод раз в минуту)
	alertSender := bot.NewBotAlertSender(telegramBot)
	go monitoring.StartAlerter(ctx, telegramBot.MetricsClient(), cfg.SDConfigsPath, alertSender)

	// Запуск ежедневного scheduler подписок (уведомления и автокик).
	go telegramBot.StartScheduler(ctx)

	// Запуск бота (блокирующий вызов)
	telegramBot.Run()

	<-ctx.Done()
	slog.Info("Bot stopped")
}

// noopPaymentHandler — заглушка обработчика платежей до реализации в этапе 4
type noopPaymentHandler struct{}

func (n *noopPaymentHandler) HandlePaymentCallback(payload platega.CallbackPayload) error {
	slog.Info("Callback получен (заглушка, этап 4 не реализован)", "transaction_id", payload.ID)
	return nil
}
