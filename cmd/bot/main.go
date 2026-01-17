package main

import (
	"context"
	"log/slog"
	"os"
	"os/signal"
	"syscall"

	"github.com/fus1ond/vpn_bot/internal/bot"
	"github.com/fus1ond/vpn_bot/internal/config"
	"github.com/fus1ond/vpn_bot/internal/database"
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

	// Создание клиента Remnawave API
	remnawaveClient := remnawave.NewClient(
		cfg.RemnawaveURL,
		cfg.RemnawaveAPIToken,
		cfg.RemnawaveSquadUUID,
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

	// Запуск фонового scheduler для сброса лимитов (1-го числа месяца)
	go telegramBot.StartScheduler(ctx)

	// Запуск бота (блокирующий вызов)
	telegramBot.Run()

	<-ctx.Done()
	slog.Info("Bot stopped")
}