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
	"github.com/fus1ond/vpn_bot/internal/subscription"
	"github.com/fus1ond/vpn_bot/internal/threexui"
)

func main() {
	// Setup logging
	logger := slog.New(slog.NewTextHandler(os.Stdout, &slog.HandlerOptions{
		Level: slog.LevelInfo,
	}))
	slog.SetDefault(logger)

	slog.Info("Starting VPN Bot...")

	// Load configuration
	cfg, err := config.Load()
	if err != nil {
		slog.Error("Failed to load config", "error", err)
		os.Exit(1)
	}

	// Initialize database
	db, err := database.New(cfg.DBPath)
	if err != nil {
		slog.Error("Failed to initialize database", "error", err)
		os.Exit(1)
	}
	defer db.Close()

	// Create 3X-UI clients
	clientA, err := threexui.New(cfg.ServerA)
	if err != nil {
		slog.Error("Failed to create Server A client", "error", err)
		os.Exit(1)
	}

	clientB, err := threexui.New(cfg.ServerB)
	if err != nil {
		slog.Error("Failed to create Server B client", "error", err)
		os.Exit(1)
	}

	clientC, err := threexui.New(cfg.ServerC)
	if err != nil {
		slog.Error("Failed to create Server C client", "error", err)
		os.Exit(1)
	}

	// Login to all servers
	slog.Info("Logging in to Server A...")
	if err := clientA.Login(); err != nil {
		slog.Error("Failed to login to Server A", "error", err)
		os.Exit(1)
	}

	slog.Info("Logging in to Server B...")
	if err := clientB.Login(); err != nil {
		slog.Error("Failed to login to Server B", "error", err)
		os.Exit(1)
	}

	slog.Info("Logging in to Server C...")
	if err := clientC.Login(); err != nil {
		slog.Error("Failed to login to Server C", "error", err)
		os.Exit(1)
	}

	// Start subscription server in a goroutine
	subServer := subscription.New(db, clientA, clientC, cfg)
	go func() {
		if err := subServer.Start(cfg.SubPort); err != nil {
			slog.Error("Subscription server failed", "error", err)
		}
	}()

	// Create and start Telegram bot
	telegramBot, err := bot.New(cfg.BotToken, db, clientA, clientB, clientC, cfg)
	if err != nil {
		slog.Error("Failed to create Telegram bot", "error", err)
		os.Exit(1)
	}

	// Setup graceful shutdown
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	go func() {
		sigChan := make(chan os.Signal, 1)
		signal.Notify(sigChan, os.Interrupt, syscall.SIGTERM)
		<-sigChan
		slog.Info("Shutdown signal received, stopping bot...")
		cancel()
	}()

	// Start bot (blocks until stopped)
	telegramBot.Run()

	<-ctx.Done()
	slog.Info("Bot stopped")
}
