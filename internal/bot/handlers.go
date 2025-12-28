package bot

import (
	"fmt"
	"log/slog"
	"strings"

	"github.com/fus1ond/vpn_bot/internal/config"
	"github.com/fus1ond/vpn_bot/internal/database"
	"github.com/fus1ond/vpn_bot/internal/threexui"
	"github.com/google/uuid"
	tele "gopkg.in/telebot.v3"
)

// Bot represents the Telegram bot
type Bot struct {
	bot     *tele.Bot
	db      *database.DB
	clientA *threexui.Client
	clientB *threexui.Client
	config  *config.Config
}

// New creates a new Telegram bot
func New(token string, db *database.DB, clientA, clientB *threexui.Client, cfg *config.Config) (*Bot, error) {
	pref := tele.Settings{
		Token:  token,
		Poller: &tele.LongPoller{},
	}

	b, err := tele.NewBot(pref)
	if err != nil {
		return nil, fmt.Errorf("failed to create bot: %w", err)
	}

	bot := &Bot{
		bot:     b,
		db:      db,
		clientA: clientA,
		clientB: clientB,
		config:  cfg,
	}

	// Register handlers
	b.Handle("/start", bot.handleStart)
	b.Handle("/create", bot.handleCreate)
	b.Handle("/list", bot.handleList)

	return bot, nil
}

// Run starts the bot
func (b *Bot) Run() {
	slog.Info("Bot started", "username", b.bot.Me.Username)
	b.bot.Start()
}

// handleStart handles the /start command
func (b *Bot) handleStart(c tele.Context) error {
	// Check if user is admin
	if c.Sender().ID != b.config.AdminID {
		return nil
	}

	helpMsg := "🚀 <b>Бот + Подписка активны</b>\n\n"
	helpMsg += "<b>Команды:</b>\n"
	helpMsg += "/create &lt;name&gt; - Создать нового клиента\n"
	helpMsg += "/list - Показать всех клиентов"

	return c.Send(helpMsg, &tele.SendOptions{
		ParseMode: tele.ModeHTML,
	})
}

// handleCreate handles the /create command
func (b *Bot) handleCreate(c tele.Context) error {
	// Check if user is admin
	if c.Sender().ID != b.config.AdminID {
		return nil
	}

	// Parse arguments
	args := strings.Fields(c.Text())
	if len(args) < 2 {
		return c.Send("Укажи имя!")
	}

	email := args[1]
	clientUUID := uuid.New().String()

	// Send status message
	status, err := c.Bot().Send(c.Sender(), fmt.Sprintf("⏳ Создаю <b>%s</b>...", email), &tele.SendOptions{
		ParseMode: tele.ModeHTML,
	})
	if err != nil {
		return err
	}

	// Login to both servers
	if err := b.clientA.Login(); err != nil {
		slog.Error("Failed to login to Server A", "error", err)
		_, editErr := c.Bot().Edit(status, fmt.Sprintf("❌ Ошибка подключения к Server A: %v", err))
		return editErr
	}

	if err := b.clientB.Login(); err != nil {
		slog.Error("Failed to login to Server B", "error", err)
		_, editErr := c.Bot().Edit(status, fmt.Sprintf("❌ Ошибка подключения к Server B: %v", err))
		return editErr
	}

	// Add client to Server A
	errA := b.clientA.AddClient(b.config.ServerA.InboundID, email, clientUUID, b.config.ServerA.LimitBytes)
	if errA != nil {
		slog.Error("Failed to add client to Server A", "error", errA)
	}

	// Add client to Server B
	errB := b.clientB.AddClient(b.config.ServerB.InboundID, email, clientUUID, b.config.ServerB.LimitBytes)
	if errB != nil {
		slog.Error("Failed to add client to Server B", "error", errB)
	}

	if errA != nil || errB != nil {
		errorMsg := fmt.Sprintf("❌ Ошибка:\nRU: %v\nEU: %v", errA, errB)
		_, editErr := c.Bot().Edit(status, errorMsg)
		return editErr
	}

	// Save to database
	if err := b.db.AddUser(email, clientUUID); err != nil {
		slog.Error("Failed to add user to database", "error", err)
		_, editErr := c.Bot().Edit(status, fmt.Sprintf("❌ Ошибка сохранения в БД: %v", err))
		return editErr
	}

	// Generate subscription link
	// Extract IP from Server A URL
	myIP := extractIP(b.config.ServerA.BaseURL)
	subLink := fmt.Sprintf("http://%s:%d/sub/%s", myIP, b.config.SubPort, clientUUID)

	successMsg := fmt.Sprintf(
		"✅ <b>Клиент %s создан!</b>\n\n"+
			"🔗 <b>Ссылка-подписка (Вставить в приложение):</b>\n<code>%s</code>\n\n"+
			"Теперь клиенту достаточно добавить эту ссылку 1 раз.\n"+
			"Если ты поменяешь настройки в боте — они обновятся у клиента.",
		email, subLink,
	)

	_, editErr := c.Bot().Edit(status, successMsg, &tele.SendOptions{
		ParseMode: tele.ModeHTML,
	})
	return editErr
}

// handleList handles the /list command
func (b *Bot) handleList(c tele.Context) error {
	// Check if user is admin
	if c.Sender().ID != b.config.AdminID {
		return nil
	}

	// First, sync clients from panel
	if err := b.syncClients(); err != nil {
		slog.Error("Failed to sync clients", "error", err)
		// Continue anyway to show what's in DB
	}

	// Get all users from database
	users, err := b.db.GetAllUsers()
	if err != nil {
		slog.Error("Failed to get users from database", "error", err)
		return c.Send("❌ Ошибка получения списка клиентов")
	}

	if len(users) == 0 {
		return c.Send("📋 Список клиентов пуст")
	}

	// Build message
	msg := fmt.Sprintf("📋 <b>Всего клиентов: %d</b>\n\n", len(users))
	for i, user := range users {
		// Generate subscription link
		myIP := extractIP(b.config.ServerA.BaseURL)
		subLink := fmt.Sprintf("http://%s:%d/sub/%s", myIP, b.config.SubPort, user.UUID)

		msg += fmt.Sprintf("<b>%d. %s</b>\n", i+1, user.Email)
		msg += fmt.Sprintf("└ UUID: <code>%s</code>\n", user.UUID)
		msg += fmt.Sprintf("└ Подписка: <code>%s</code>\n\n", subLink)
	}

	return c.Send(msg, &tele.SendOptions{
		ParseMode: tele.ModeHTML,
	})
}

// syncClients synchronizes clients from 3X-UI panel to database
func (b *Bot) syncClients() error {
	// Login to Server A
	if err := b.clientA.Login(); err != nil {
		return fmt.Errorf("failed to login to Server A: %w", err)
	}

	// Get clients from Server A
	clientsA, err := b.clientA.GetAllClients(b.config.ServerA.InboundID)
	if err != nil {
		return fmt.Errorf("failed to get clients from Server A: %w", err)
	}

	added := 0
	// Add clients to database (using Server A as source of truth)
	for _, client := range clientsA {
		// Check if user already exists
		existingUUID, err := b.db.GetUserUUID(client.Email)
		if err != nil {
			slog.Error("Error checking user", "email", client.Email, "error", err)
			continue
		}

		if existingUUID != "" {
			continue // Skip existing users
		}

		// Add user to database
		if err := b.db.AddUser(client.Email, client.ID); err != nil {
			slog.Error("Failed to add user to database", "email", client.Email, "error", err)
			continue
		}
		added++
	}

	if added > 0 {
		slog.Info("Synced clients from panel", "added", added, "total", len(clientsA))
	}

	return nil
}

// extractIP extracts IP address from base URL
func extractIP(baseURL string) string {
	// Remove https:// or http://
	ip := strings.TrimPrefix(baseURL, "https://")
	ip = strings.TrimPrefix(ip, "http://")

	// Remove port if present
	if idx := strings.Index(ip, ":"); idx != -1 {
		ip = ip[:idx]
	}

	return ip
}
