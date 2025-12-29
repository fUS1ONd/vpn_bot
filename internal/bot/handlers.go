package bot

import (
	"fmt"
	"log/slog"
	"strings"
	"time"

	"github.com/fus1ond/vpn_bot/internal/config"
	"github.com/fus1ond/vpn_bot/internal/database"
	"github.com/fus1ond/vpn_bot/internal/threexui"
	"github.com/google/uuid"
	tele "gopkg.in/telebot.v3"
)

// User states for conversation
const (
	StateNone       = ""
	StateWaitPromo  = "wait_promo"
	StateWaitClient = "wait_client"
)

// Bot represents the Telegram bot
type Bot struct {
	bot        *tele.Bot
	db         *database.DB
	clientA    *threexui.Client
	clientB    *threexui.Client
	config     *config.Config
	userStates map[int64]string
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
		bot:        b,
		db:         db,
		clientA:    clientA,
		clientB:    clientB,
		config:     cfg,
		userStates: make(map[int64]string),
	}

	// Add logging middleware
	b.Use(func(next tele.HandlerFunc) tele.HandlerFunc {
		return func(c tele.Context) error {
			// Log incoming update
			if c.Message() != nil {
				slog.Info("Incoming message",
					"from", c.Sender().ID,
					"username", c.Sender().Username,
					"text", c.Message().Text,
				)
			}
			if c.Callback() != nil {
				slog.Info("Incoming callback",
					"from", c.Sender().ID,
					"username", c.Sender().Username,
					"data", c.Callback().Data,
				)
			}
			return next(c)
		}
	})

	// Register command handlers
	b.Handle("/start", bot.handleStart)
	b.Handle("/create", bot.handleAdminCreate)
	b.Handle("/delete", bot.handleAdminDelete)
	b.Handle("/list", bot.handleAdminList)
	b.Handle("/promo_add", bot.handlePromoAdd)
	b.Handle("/promo_del", bot.handlePromoDel)
	b.Handle("/promos", bot.handlePromoList)

	// Register callback handler (single handler for all callbacks)
	b.Handle(tele.OnCallback, bot.handleCallback)

	// Handle text messages for promo codes
	b.Handle(tele.OnText, bot.handleTextMessage)

	return bot, nil
}

// Run starts the bot
func (b *Bot) Run() {
	slog.Info("Bot started", "username", b.bot.Me.Username)
	b.bot.Start()
}

// handleCallback routes all callback queries
func (b *Bot) handleCallback(c tele.Context) error {
	callback := c.Callback()
	if callback == nil {
		return nil
	}

	data := callback.Data
	slog.Info("Callback routing", "data", data, "from", c.Sender().ID)

	switch data {
	case CallbackConnect:
		return b.handleConnectCallback(c)
	case CallbackStatus:
		return b.handleStatusCallback(c)
	case CallbackInstructions:
		return b.handleInstructionsCallback(c)
	case CallbackBuyTraffic:
		return b.handleBuyTrafficCallback(c)
	case CallbackPromo:
		return b.handlePromoCallback(c)
	case CallbackSupport:
		return b.handleSupportCallback(c)
	case CallbackBack:
		return b.handleBackCallback(c)
	case CallbackPay:
		return b.handlePayCallback(c)
	case CallbackInstructionIOS:
		return b.handleInstructionIOSCallback(c)
	case CallbackInstructionAndroid:
		return b.handleInstructionAndroidCallback(c)
	case CallbackInstructionWindows:
		return b.handleInstructionWindowsCallback(c)
	case CallbackInstructionMac:
		return b.handleInstructionMacCallback(c)
	// Admin callbacks
	case CallbackAdminList:
		return b.handleAdminList(c)
	case CallbackAdminCreate:
		return b.handleAdminCreateCallback(c)
	case CallbackAdminPromo:
		return b.handlePromoList(c)
	default:
		slog.Warn("Unknown callback", "data", data)
		return c.Respond()
	}
}

// handleStart handles the /start command
func (b *Bot) handleStart(c tele.Context) error {
	telegramID := c.Sender().ID

	// Check if admin
	if telegramID == b.config.AdminID {
		return b.handleAdminStart(c)
	}

	// Check if user exists
	user, err := b.db.GetUserByTelegramID(telegramID)
	if err != nil {
		slog.Error("Failed to get user", "error", err)
		return c.Send("Произошла ошибка. Попробуйте позже.")
	}

	// New user - show welcome with trial offer
	if user == nil {
		return c.Send(MsgWelcome, &tele.SendOptions{
			ParseMode:   tele.ModeHTML,
			ReplyMarkup: MainMenuKeyboard(),
		})
	}

	// Existing user - show welcome back
	keyboard := MainMenuKeyboard()
	if user.SubscriptionStatus == database.StatusActive || user.SubscriptionStatus == database.StatusTrial {
		keyboard = ActiveUserKeyboard()
	}

	return c.Send(MsgWelcomeBack, &tele.SendOptions{
		ParseMode:   tele.ModeHTML,
		ReplyMarkup: keyboard,
	})
}

// handleConnectCallback handles the connect button
func (b *Bot) handleConnectCallback(c tele.Context) error {
	if err := c.Respond(); err != nil {
		slog.Error("Failed to respond to callback", "error", err)
	}

	telegramID := c.Sender().ID
	user, err := b.db.GetUserByTelegramID(telegramID)
	if err != nil {
		slog.Error("Failed to get user", "error", err)
		return c.Send("Произошла ошибка. Попробуйте позже.")
	}

	// User already has active subscription
	if user != nil && (user.SubscriptionStatus == database.StatusActive || user.SubscriptionStatus == database.StatusTrial) {
		subLink := b.generateSubLink(user.UUID)
		return c.Edit(fmt.Sprintf(MsgTrialActivated, user.SubscriptionEndAt.Format("02.01.2006 15:04"), subLink), &tele.SendOptions{
			ParseMode:   tele.ModeHTML,
			ReplyMarkup: BackKeyboard(),
		})
	}

	// User exists but subscription expired or none
	if user != nil {
		if user.TrialUsed {
			return c.Edit(MsgTrialUsed, &tele.SendOptions{
				ParseMode:   tele.ModeHTML,
				ReplyMarkup: PaymentKeyboard(),
			})
		}
		// Activate trial for existing user
		return b.activateTrial(c, user)
	}

	// New user - activate trial
	return b.activateTrialNewUser(c)
}

// activateTrialNewUser activates trial for a new user
func (b *Bot) activateTrialNewUser(c tele.Context) error {
	telegramID := c.Sender().ID
	email := fmt.Sprintf("user_%d", telegramID)
	clientUUID := uuid.New().String()

	// Calculate trial expiry (3 days)
	expiryTime := time.Now().AddDate(0, 0, 3)
	expiryTimeMs := expiryTime.UnixMilli()

	// Trial traffic limit: 1GB
	trialTrafficBytes := int64(1 * 1024 * 1024 * 1024)

	// Login to both servers
	if err := b.clientA.Login(); err != nil {
		slog.Error("Failed to login to Server A", "error", err)
		return c.Edit("Ошибка подключения к серверу. Попробуйте позже.")
	}
	if err := b.clientB.Login(); err != nil {
		slog.Error("Failed to login to Server B", "error", err)
		return c.Edit("Ошибка подключения к серверу. Попробуйте позже.")
	}

	// Create settings for both servers
	settingsRU := threexui.ClientSettings{
		UUID:       clientUUID,
		Email:      email,
		LimitIP:    2,
		TotalGB:    trialTrafficBytes,
		ExpiryTime: expiryTimeMs,
		Enable:     true,
	}

	settingsEU := threexui.ClientSettings{
		UUID:       clientUUID,
		Email:      email,
		LimitIP:    2,
		TotalGB:    0, // Unlimited
		ExpiryTime: expiryTimeMs,
		Enable:     true,
	}

	// Add to servers
	if err := b.clientA.AddClientWithSettings(b.config.ServerA.InboundID, settingsRU); err != nil {
		slog.Error("Failed to add client to Server A", "error", err)
		return c.Edit("Ошибка создания аккаунта. Попробуйте позже.")
	}

	if err := b.clientB.AddClientWithSettings(b.config.ServerB.InboundID, settingsEU); err != nil {
		slog.Error("Failed to add client to Server B", "error", err)
		// Rollback Server A
		_ = b.clientA.DeleteClient(b.config.ServerA.InboundID, clientUUID)
		return c.Edit("Ошибка создания аккаунта. Попробуйте позже.")
	}

	// Save to database
	user, err := b.db.CreateUser(telegramID, email, clientUUID)
	if err != nil {
		slog.Error("Failed to create user in DB", "error", err)
		return c.Edit("Ошибка создания аккаунта. Попробуйте позже.")
	}

	// Update subscription status
	if err := b.db.UpdateUserSubscription(user.ID, database.StatusTrial, &expiryTime); err != nil {
		slog.Error("Failed to update subscription", "error", err)
	}

	// Mark trial as used
	if err := b.db.MarkTrialUsed(user.ID); err != nil {
		slog.Error("Failed to mark trial used", "error", err)
	}

	subLink := b.generateSubLink(clientUUID)

	return c.Edit(fmt.Sprintf(MsgTrialActivated, expiryTime.Format("02.01.2006 15:04"), subLink), &tele.SendOptions{
		ParseMode:   tele.ModeHTML,
		ReplyMarkup: InstructionsKeyboard(),
	})
}

// activateTrial activates trial for existing user
func (b *Bot) activateTrial(c tele.Context, user *database.User) error {
	expiryTime := time.Now().AddDate(0, 0, 3)
	expiryTimeMs := expiryTime.UnixMilli()
	trialTrafficBytes := int64(1 * 1024 * 1024 * 1024)

	// Login to servers
	if err := b.clientA.Login(); err != nil {
		slog.Error("Failed to login to Server A", "error", err)
		return c.Edit("Ошибка подключения к серверу. Попробуйте позже.")
	}
	if err := b.clientB.Login(); err != nil {
		slog.Error("Failed to login to Server B", "error", err)
		return c.Edit("Ошибка подключения к серверу. Попробуйте позже.")
	}

	// Update client settings
	settingsRU := threexui.ClientSettings{
		UUID:       user.UUID,
		Email:      user.Email,
		LimitIP:    2,
		TotalGB:    trialTrafficBytes,
		ExpiryTime: expiryTimeMs,
		Enable:     true,
	}

	settingsEU := threexui.ClientSettings{
		UUID:       user.UUID,
		Email:      user.Email,
		LimitIP:    2,
		TotalGB:    0,
		ExpiryTime: expiryTimeMs,
		Enable:     true,
	}

	if err := b.clientA.UpdateClient(b.config.ServerA.InboundID, user.UUID, settingsRU); err != nil {
		slog.Error("Failed to update client on Server A", "error", err)
		return c.Edit("Ошибка активации. Попробуйте позже.")
	}

	if err := b.clientB.UpdateClient(b.config.ServerB.InboundID, user.UUID, settingsEU); err != nil {
		slog.Error("Failed to update client on Server B", "error", err)
		return c.Edit("Ошибка активации. Попробуйте позже.")
	}

	// Update database
	if err := b.db.UpdateUserSubscription(user.ID, database.StatusTrial, &expiryTime); err != nil {
		slog.Error("Failed to update subscription", "error", err)
	}

	if err := b.db.MarkTrialUsed(user.ID); err != nil {
		slog.Error("Failed to mark trial used", "error", err)
	}

	subLink := b.generateSubLink(user.UUID)

	return c.Edit(fmt.Sprintf(MsgTrialActivated, expiryTime.Format("02.01.2006 15:04"), subLink), &tele.SendOptions{
		ParseMode:   tele.ModeHTML,
		ReplyMarkup: InstructionsKeyboard(),
	})
}

// handleStatusCallback handles the status button
func (b *Bot) handleStatusCallback(c tele.Context) error {
	if err := c.Respond(); err != nil {
		slog.Error("Failed to respond to callback", "error", err)
	}

	user, err := b.db.GetUserByTelegramID(c.Sender().ID)
	if err != nil {
		slog.Error("Failed to get user", "error", err)
		return c.Edit("Произошла ошибка. Попробуйте позже.")
	}

	if user == nil {
		return c.Edit(MsgNoSubscription, &tele.SendOptions{
			ParseMode:   tele.ModeHTML,
			ReplyMarkup: MainMenuKeyboard(),
		})
	}

	// Get traffic from panel
	var trafficUsed int64
	var trafficLimit int64 = b.config.ServerA.LimitBytes + user.RuExtraTraffic

	if err := b.clientA.Login(); err == nil {
		status, err := b.clientA.GetClientStatus(b.config.ServerA.InboundID, user.Email)
		if err == nil && status != nil {
			trafficUsed = status.UsedTraffic
			if status.TotalGB > 0 {
				trafficLimit = status.TotalGB
			}
		}
	}

	subLink := b.generateSubLink(user.UUID)
	msg := FormatStatus(user, subLink, trafficUsed, trafficLimit)

	return c.Edit(msg, &tele.SendOptions{
		ParseMode:   tele.ModeHTML,
		ReplyMarkup: BackKeyboard(),
	})
}

// handleInstructionsCallback handles the instructions button
func (b *Bot) handleInstructionsCallback(c tele.Context) error {
	if err := c.Respond(); err != nil {
		slog.Error("Failed to respond to callback", "error", err)
	}

	return c.Edit(MsgInstructions, &tele.SendOptions{
		ParseMode:   tele.ModeHTML,
		ReplyMarkup: InstructionsKeyboard(),
	})
}

// handleInstructionIOSCallback handles iOS instruction button
func (b *Bot) handleInstructionIOSCallback(c tele.Context) error {
	if err := c.Respond(); err != nil {
		slog.Error("Failed to respond to callback", "error", err)
	}

	subLink := b.getSubLinkForUser(c.Sender().ID)
	return c.Edit(fmt.Sprintf(MsgInstructionIOS, subLink), &tele.SendOptions{
		ParseMode:   tele.ModeHTML,
		ReplyMarkup: BackKeyboard(),
	})
}

// handleInstructionAndroidCallback handles Android instruction button
func (b *Bot) handleInstructionAndroidCallback(c tele.Context) error {
	if err := c.Respond(); err != nil {
		slog.Error("Failed to respond to callback", "error", err)
	}

	subLink := b.getSubLinkForUser(c.Sender().ID)
	return c.Edit(fmt.Sprintf(MsgInstructionAndroid, subLink), &tele.SendOptions{
		ParseMode:   tele.ModeHTML,
		ReplyMarkup: BackKeyboard(),
	})
}

// handleInstructionWindowsCallback handles Windows instruction button
func (b *Bot) handleInstructionWindowsCallback(c tele.Context) error {
	if err := c.Respond(); err != nil {
		slog.Error("Failed to respond to callback", "error", err)
	}

	subLink := b.getSubLinkForUser(c.Sender().ID)
	return c.Edit(fmt.Sprintf(MsgInstructionWindows, subLink), &tele.SendOptions{
		ParseMode:   tele.ModeHTML,
		ReplyMarkup: BackKeyboard(),
	})
}

// handleInstructionMacCallback handles Mac instruction button
func (b *Bot) handleInstructionMacCallback(c tele.Context) error {
	if err := c.Respond(); err != nil {
		slog.Error("Failed to respond to callback", "error", err)
	}

	subLink := b.getSubLinkForUser(c.Sender().ID)
	return c.Edit(fmt.Sprintf(MsgInstructionMac, subLink), &tele.SendOptions{
		ParseMode:   tele.ModeHTML,
		ReplyMarkup: BackKeyboard(),
	})
}

// handleBuyTrafficCallback handles buy traffic button
func (b *Bot) handleBuyTrafficCallback(c tele.Context) error {
	if err := c.Respond(); err != nil {
		slog.Error("Failed to respond to callback", "error", err)
	}

	return c.Edit(MsgBuyTraffic, &tele.SendOptions{
		ParseMode:   tele.ModeHTML,
		ReplyMarkup: BackKeyboard(), // TODO: Add payment button when Robokassa is integrated
	})
}

// handlePromoCallback handles promo button
func (b *Bot) handlePromoCallback(c tele.Context) error {
	if err := c.Respond(); err != nil {
		slog.Error("Failed to respond to callback", "error", err)
	}

	b.userStates[c.Sender().ID] = StateWaitPromo

	return c.Edit(MsgEnterPromo, &tele.SendOptions{
		ParseMode:   tele.ModeHTML,
		ReplyMarkup: PromoInputKeyboard(),
	})
}

// handleSupportCallback handles support button
func (b *Bot) handleSupportCallback(c tele.Context) error {
	if err := c.Respond(); err != nil {
		slog.Error("Failed to respond to callback", "error", err)
	}

	return c.Edit(MsgSupport, &tele.SendOptions{
		ParseMode:   tele.ModeHTML,
		ReplyMarkup: BackKeyboard(),
	})
}

// handleBackCallback handles back button
func (b *Bot) handleBackCallback(c tele.Context) error {
	if err := c.Respond(); err != nil {
		slog.Error("Failed to respond to callback", "error", err)
	}

	// Clear user state
	delete(b.userStates, c.Sender().ID)

	user, _ := b.db.GetUserByTelegramID(c.Sender().ID)
	keyboard := MainMenuKeyboard()
	if user != nil && (user.SubscriptionStatus == database.StatusActive || user.SubscriptionStatus == database.StatusTrial) {
		keyboard = ActiveUserKeyboard()
	}

	return c.Edit(MsgWelcomeBack, &tele.SendOptions{
		ParseMode:   tele.ModeHTML,
		ReplyMarkup: keyboard,
	})
}

// handlePayCallback handles pay button
func (b *Bot) handlePayCallback(c tele.Context) error {
	if err := c.Respond(); err != nil {
		slog.Error("Failed to respond to callback", "error", err)
	}

	// TODO: Integrate with Robokassa
	return c.Edit("Оплата будет доступна в ближайшее время.\n\nОбратитесь в поддержку для ручной активации.", &tele.SendOptions{
		ParseMode:   tele.ModeHTML,
		ReplyMarkup: BackKeyboard(),
	})
}

// handleTextMessage handles text messages (for promo codes and admin input)
func (b *Bot) handleTextMessage(c tele.Context) error {
	telegramID := c.Sender().ID
	state := b.userStates[telegramID]

	switch state {
	case StateWaitPromo:
		return b.processPromoCode(c, c.Text())
	case StateWaitClient:
		if b.isAdmin(c) {
			return b.processCreateClient(c, c.Text())
		}
	}

	// Unknown message - show main menu
	return b.handleStart(c)
}

// processCreateClient creates a new client (admin only)
func (b *Bot) processCreateClient(c tele.Context, email string) error {
	delete(b.userStates, c.Sender().ID)

	clientUUID := uuid.New().String()

	// Calculate expiry time (1 month from now)
	expiryTime := time.Now().AddDate(0, 1, 0)
	expiryTimeMs := expiryTime.UnixMilli()

	// Login to both servers
	if err := b.clientA.Login(); err != nil {
		slog.Error("Failed to login to Server A", "error", err)
		return c.Send(fmt.Sprintf("Ошибка подключения к Server A: %v", err))
	}

	if err := b.clientB.Login(); err != nil {
		slog.Error("Failed to login to Server B", "error", err)
		return c.Send(fmt.Sprintf("Ошибка подключения к Server B: %v", err))
	}

	// Create settings for both servers
	settingsRU := threexui.ClientSettings{
		UUID:       clientUUID,
		Email:      email,
		LimitIP:    2,
		TotalGB:    b.config.ServerA.LimitBytes,
		ExpiryTime: expiryTimeMs,
		Enable:     true,
	}

	settingsEU := threexui.ClientSettings{
		UUID:       clientUUID,
		Email:      email,
		LimitIP:    2,
		TotalGB:    0, // Unlimited
		ExpiryTime: expiryTimeMs,
		Enable:     true,
	}

	// Add client to Server A (RU)
	errA := b.clientA.AddClientWithSettings(b.config.ServerA.InboundID, settingsRU)
	if errA != nil {
		slog.Error("Failed to add client to Server A", "error", errA)
	}

	// Add client to Server B (EU)
	errB := b.clientB.AddClientWithSettings(b.config.ServerB.InboundID, settingsEU)
	if errB != nil {
		slog.Error("Failed to add client to Server B", "error", errB)
	}

	if errA != nil || errB != nil {
		return c.Send(fmt.Sprintf("Ошибка:\nRU: %v\nEU: %v", errA, errB))
	}

	// Save to database with telegram_id = 0 (NULL, admin-created user)
	user, dbErr := b.db.CreateUser(0, email, clientUUID)
	if dbErr != nil {
		slog.Error("Failed to add user to database", "error", dbErr)
		return c.Send(fmt.Sprintf("Ошибка сохранения в БД: %v", dbErr))
	}

	// Update subscription status
	if err := b.db.UpdateUserSubscription(user.ID, database.StatusActive, &expiryTime); err != nil {
		slog.Error("Failed to update subscription", "error", err)
	}

	// Generate subscription link
	subLink := b.generateSubLink(clientUUID)

	return c.Send(fmt.Sprintf(MsgAdminClientCreated, email, clientUUID, subLink), &tele.SendOptions{
		ParseMode:   tele.ModeHTML,
		ReplyMarkup: AdminKeyboard(),
	})
}

// processPromoCode processes promo code input
func (b *Bot) processPromoCode(c tele.Context, code string) error {
	telegramID := c.Sender().ID
	delete(b.userStates, telegramID)

	user, err := b.db.GetUserByTelegramID(telegramID)
	if err != nil {
		slog.Error("Failed to get user", "error", err)
		return c.Send(fmt.Sprintf(MsgPromoError, "Произошла ошибка"), &tele.SendOptions{
			ParseMode:   tele.ModeHTML,
			ReplyMarkup: BackKeyboard(),
		})
	}

	if user == nil {
		return c.Send(fmt.Sprintf(MsgPromoError, "Сначала активируйте триал или подписку"), &tele.SendOptions{
			ParseMode:   tele.ModeHTML,
			ReplyMarkup: MainMenuKeyboard(),
		})
	}

	// Validate promo code
	promo, err := b.db.ValidatePromoCode(strings.TrimSpace(code), user.ID)
	if err != nil {
		return c.Send(fmt.Sprintf(MsgPromoError, err.Error()), &tele.SendOptions{
			ParseMode:   tele.ModeHTML,
			ReplyMarkup: BackKeyboard(),
		})
	}

	// Apply promo code
	if err := b.applyPromoCode(user, promo); err != nil {
		slog.Error("Failed to apply promo code", "error", err)
		return c.Send(fmt.Sprintf(MsgPromoError, "Ошибка применения промокода"), &tele.SendOptions{
			ParseMode:   tele.ModeHTML,
			ReplyMarkup: BackKeyboard(),
		})
	}

	// Record promo usage
	if err := b.db.UsePromoCode(user.ID, promo.ID); err != nil {
		slog.Error("Failed to record promo usage", "error", err)
	}

	result := FormatPromoResult(promo)
	return c.Send(fmt.Sprintf(MsgPromoSuccess, result), &tele.SendOptions{
		ParseMode:   tele.ModeHTML,
		ReplyMarkup: BackKeyboard(),
	})
}

// applyPromoCode applies promo code effects
func (b *Bot) applyPromoCode(user *database.User, promo *database.PromoCode) error {
	switch promo.Type {
	case database.PromoTypeFreeDays:
		// Add days to subscription
		var newEnd time.Time
		if user.SubscriptionEndAt != nil && user.SubscriptionEndAt.After(time.Now()) {
			newEnd = user.SubscriptionEndAt.AddDate(0, 0, promo.Value)
		} else {
			newEnd = time.Now().AddDate(0, 0, promo.Value)
		}

		status := user.SubscriptionStatus
		if status == database.StatusNone || status == database.StatusExpired {
			status = database.StatusActive
		}

		if err := b.db.UpdateUserSubscription(user.ID, status, &newEnd); err != nil {
			return err
		}

		// Update panel expiry
		return b.updatePanelExpiry(user, newEnd.UnixMilli())

	case database.PromoTypeExtraTraffic:
		// Add extra traffic
		if err := b.db.AddRuExtraTraffic(user.ID, int64(promo.Value)); err != nil {
			return err
		}

		// Update panel limit
		return b.updatePanelTrafficLimit(user, int64(promo.Value))

	case database.PromoTypeDiscount:
		// Discount is applied during payment - nothing to do here
		return nil
	}

	return nil
}

// updatePanelExpiry updates expiry time in the panel
func (b *Bot) updatePanelExpiry(user *database.User, expiryTimeMs int64) error {
	if err := b.clientA.Login(); err != nil {
		return err
	}
	if err := b.clientB.Login(); err != nil {
		return err
	}

	// Get current status to preserve other settings
	statusA, err := b.clientA.GetClientStatus(b.config.ServerA.InboundID, user.Email)
	if err != nil {
		return err
	}

	settingsA := threexui.ClientSettings{
		UUID:       user.UUID,
		Email:      user.Email,
		LimitIP:    statusA.LimitIP,
		TotalGB:    statusA.TotalGB,
		ExpiryTime: expiryTimeMs,
		Enable:     true,
	}

	if err := b.clientA.UpdateClient(b.config.ServerA.InboundID, user.UUID, settingsA); err != nil {
		return err
	}

	statusB, err := b.clientB.GetClientStatus(b.config.ServerB.InboundID, user.Email)
	if err != nil {
		return err
	}

	settingsB := threexui.ClientSettings{
		UUID:       user.UUID,
		Email:      user.Email,
		LimitIP:    statusB.LimitIP,
		TotalGB:    statusB.TotalGB,
		ExpiryTime: expiryTimeMs,
		Enable:     true,
	}

	return b.clientB.UpdateClient(b.config.ServerB.InboundID, user.UUID, settingsB)
}

// updatePanelTrafficLimit adds extra traffic to the panel limit
func (b *Bot) updatePanelTrafficLimit(user *database.User, extraBytes int64) error {
	if err := b.clientA.Login(); err != nil {
		return err
	}

	status, err := b.clientA.GetClientStatus(b.config.ServerA.InboundID, user.Email)
	if err != nil {
		return err
	}

	newLimit := status.TotalGB + extraBytes

	settings := threexui.ClientSettings{
		UUID:       user.UUID,
		Email:      user.Email,
		LimitIP:    status.LimitIP,
		TotalGB:    newLimit,
		ExpiryTime: status.ExpiryTime,
		Enable:     true,
	}

	return b.clientA.UpdateClient(b.config.ServerA.InboundID, user.UUID, settings)
}

// getSubLinkForUser returns subscription link for user
func (b *Bot) getSubLinkForUser(telegramID int64) string {
	user, err := b.db.GetUserByTelegramID(telegramID)
	if err != nil || user == nil {
		return "Сначала активируйте подписку"
	}
	return b.generateSubLink(user.UUID)
}

// syncClients synchronizes clients from 3X-UI panel to database
func (b *Bot) syncClients() error {
	if err := b.clientA.Login(); err != nil {
		return fmt.Errorf("failed to login to Server A: %w", err)
	}

	clientsA, err := b.clientA.GetAllClients(b.config.ServerA.InboundID)
	if err != nil {
		return fmt.Errorf("failed to get clients from Server A: %w", err)
	}

	added := 0
	updated := 0

	for _, client := range clientsA {
		existingUser, err := b.db.GetUserByEmail(client.Email)
		if err != nil {
			slog.Error("Error checking user", "email", client.Email, "error", err)
			continue
		}

		if existingUser == nil {
			// New client from panel - add to database with telegram_id = 0 (NULL)
			if _, err := b.db.CreateUser(0, client.Email, client.ID); err != nil {
				slog.Error("Failed to add user to database", "email", client.Email, "error", err)
				continue
			}
			added++
			continue
		}

		// Existing user - update status from panel
		if err := b.syncUserStatus(existingUser, &client); err != nil {
			slog.Error("Failed to sync user status", "email", client.Email, "error", err)
			continue
		}
		updated++
	}

	if added > 0 || updated > 0 {
		slog.Info("Synced clients from panel", "added", added, "updated", updated, "total", len(clientsA))
	}

	return nil
}

// syncUserStatus updates user status in DB based on panel data
func (b *Bot) syncUserStatus(user *database.User, client *threexui.ClientInfo) error {
	var newStatus string
	var expiryTime *time.Time

	// Determine status based on panel data
	if !client.Enable {
		newStatus = database.StatusExpired
	} else if client.ExpiryTime > 0 {
		expiry := time.UnixMilli(client.ExpiryTime)
		expiryTime = &expiry

		if expiry.Before(time.Now()) {
			newStatus = database.StatusExpired
		} else if user.TrialUsed && user.SubscriptionStatus == database.StatusTrial {
			newStatus = database.StatusTrial
		} else {
			newStatus = database.StatusActive
		}
	} else {
		// No expiry set - consider active
		newStatus = database.StatusActive
	}

	// Update only if status changed
	if user.SubscriptionStatus != newStatus || !timeEqual(user.SubscriptionEndAt, expiryTime) {
		if err := b.db.UpdateUserSubscription(user.ID, newStatus, expiryTime); err != nil {
			return err
		}
	}

	return nil
}

// timeEqual compares two time pointers
func timeEqual(a, b *time.Time) bool {
	if a == nil && b == nil {
		return true
	}
	if a == nil || b == nil {
		return false
	}
	return a.Unix() == b.Unix()
}

// extractIP extracts IP address from base URL
func extractIP(baseURL string) string {
	ip := strings.TrimPrefix(baseURL, "https://")
	ip = strings.TrimPrefix(ip, "http://")

	if idx := strings.Index(ip, ":"); idx != -1 {
		ip = ip[:idx]
	}

	return ip
}
