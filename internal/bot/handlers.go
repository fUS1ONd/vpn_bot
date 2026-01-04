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
	StateNone              = ""
	StateWaitPromo         = "wait_promo"
	StateWaitClient        = "wait_client"
	StateWaitClientDelete  = "wait_client_delete"
	StateWaitPromoAdd      = "wait_promo_add"
	StateWaitPromoDel      = "wait_promo_del"
	StateWaitBroadcastAll  = "wait_broadcast_all"
	StateWaitBroadcastActive = "wait_broadcast_active"
)

// Bot represents the Telegram bot
type Bot struct {
	bot        *tele.Bot
	db         *database.DB
	clientA    *threexui.Client
	clientB    *threexui.Client
	clientC    *threexui.Client
	config     *config.Config
	userStates map[int64]string
}

// New creates a new Telegram bot
func New(token string, db *database.DB, clientA, clientB, clientC *threexui.Client, cfg *config.Config) (*Bot, error) {
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
		clientC:    clientC,
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
	// Admin commands

	// Register callback handler (single handler for all callbacks)
	// Kept primarily for Admin Inline Keyboards
	b.Handle(tele.OnCallback, bot.handleCallback)

	// Handle text messages (Main interaction router)
	b.Handle(tele.OnText, bot.handleTextMessage)

	// Handle media messages for broadcast
	b.Handle(tele.OnPhoto, bot.handleMediaMessage)
	b.Handle(tele.OnVideo, bot.handleMediaMessage)
	b.Handle(tele.OnDocument, bot.handleMediaMessage)

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
	default:
		slog.Warn("Unknown callback", "data", data)
		return c.Respond()
	}
}

// handleMediaMessage handles photo/video/document messages (for broadcast)
func (b *Bot) handleMediaMessage(c tele.Context) error {
	telegramID := c.Sender().ID
	state := b.userStates[telegramID]

	// Only handle media in broadcast states
	switch state {
	case StateWaitBroadcastAll:
		if b.isAdmin(c) {
			return b.processBroadcastMessage(c, false)
		}
	case StateWaitBroadcastActive:
		if b.isAdmin(c) {
			return b.processBroadcastMessage(c, true)
		}
	}

	// Ignore media in other states
	return nil
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

	// New user - show warnings and welcome with trial offer
	if user == nil {
		// Send torrent warning first
		if err := c.Send(MsgTorrentWarning, &tele.SendOptions{
			ParseMode: tele.ModeHTML,
		}); err != nil {
			slog.Error("Failed to send torrent warning", "error", err)
		}

		// Send refund policy
		if err := c.Send(MsgRefundPolicy, &tele.SendOptions{
			ParseMode: tele.ModeHTML,
		}); err != nil {
			slog.Error("Failed to send refund policy", "error", err)
		}

		// Then send welcome message
		return c.Send(MsgWelcome, &tele.SendOptions{
			ParseMode:   tele.ModeHTML,
			ReplyMarkup: MenuKeyboard(nil),
		})
	}

	// Existing user - show welcome back
	// Update username if changed
	currentUsername := c.Sender().Username
	if user.Username.String != currentUsername {
		if err := b.db.UpdateUserUsername(user.ID, currentUsername); err != nil {
			slog.Error("Failed to update username", "error", err)
		}
	}

	return c.Send(MsgWelcomeBack, &tele.SendOptions{
		ParseMode:   tele.ModeHTML,
		ReplyMarkup: MenuKeyboard(user),
	})
}

// handleConnect handles the connect button request
func (b *Bot) handleConnect(c tele.Context) error {
	telegramID := c.Sender().ID
	user, err := b.db.GetUserByTelegramID(telegramID)
	if err != nil {
		slog.Error("Failed to get user", "error", err)
		return c.Send("Произошла ошибка. Попробуйте позже.")
	}

	// User already has active subscription
	if user != nil && (user.SubscriptionStatus == database.StatusActive || user.SubscriptionStatus == database.StatusTrial) {
		subLink := b.generateSubLink(user.UUID)
		return c.Send(fmt.Sprintf(MsgTrialActivated, user.SubscriptionEndAt.Format("02.01.2006 15:04"), subLink), &tele.SendOptions{
			ParseMode:   tele.ModeHTML,
			ReplyMarkup: MenuKeyboard(user),
		})
	}

	// User exists but subscription expired or none
	if user != nil {
		if user.TrialUsed {
			return c.Send(MsgTrialUsed, &tele.SendOptions{
				ParseMode:   tele.ModeHTML,
				ReplyMarkup: PaymentReplyKeyboard(),
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

	// Login to all servers
	if err := b.clientA.Login(); err != nil {
		slog.Error("Failed to login to Server A", "error", err)
		return c.Send("Ошибка подключения к серверу. Попробуйте позже.")
	}
	if err := b.clientB.Login(); err != nil {
		slog.Error("Failed to login to Server B", "error", err)
		return c.Send("Ошибка подключения к серверу. Попробуйте позже.")
	}
	if err := b.clientC.Login(); err != nil {
		slog.Error("Failed to login to Server C", "error", err)
		return c.Send("Ошибка подключения к серверу. Попробуйте позже.")
	}

	// Create settings for all servers
	settingsRU := threexui.ClientSettings{
		UUID:       clientUUID,
		Email:      email,
		LimitIP:    3,
		TotalGB:    trialTrafficBytes,
		ExpiryTime: expiryTimeMs,
		Enable:     true,
	}

	settingsEU := threexui.ClientSettings{
		UUID:       clientUUID,
		Email:      email,
		LimitIP:    3,
		TotalGB:    0, // Unlimited
		ExpiryTime: expiryTimeMs,
		Enable:     true,
	}

	// Add to servers
	if err := b.clientA.AddClientWithSettings(b.config.ServerA.InboundID, settingsRU); err != nil {
		slog.Error("Failed to add client to Server A", "error", err)
		return c.Send("Ошибка создания аккаунта. Попробуйте позже.")
	}

	if err := b.clientB.AddClientWithSettings(b.config.ServerB.InboundID, settingsEU); err != nil {
		slog.Error("Failed to add client to Server B", "error", err)
		_ = b.clientA.DeleteClient(b.config.ServerA.InboundID, clientUUID)
		return c.Send("Ошибка создания аккаунта. Попробуйте позже.")
	}

	if err := b.clientC.AddClientWithSettings(b.config.ServerC.InboundID, settingsEU); err != nil {
		slog.Error("Failed to add client to Server C", "error", err)
		_ = b.clientA.DeleteClient(b.config.ServerA.InboundID, clientUUID)
		_ = b.clientB.DeleteClient(b.config.ServerB.InboundID, clientUUID)
		return c.Send("Ошибка создания аккаунта. Попробуйте позже.")
	}

	// Save to database
	user, err := b.db.CreateUser(telegramID, email, clientUUID, c.Sender().Username)
	if err != nil {
		slog.Error("Failed to create user in DB", "error", err)
		return c.Send("Ошибка создания аккаунта. Попробуйте позже.")
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

	return c.Send(fmt.Sprintf(MsgTrialActivated, expiryTime.Format("02.01.2006 15:04"), subLink), &tele.SendOptions{
		ParseMode:   tele.ModeHTML,
		ReplyMarkup: InstructionsReplyKeyboard(),
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
		return c.Send("Ошибка подключения к серверу. Попробуйте позже.")
	}
	if err := b.clientB.Login(); err != nil {
		slog.Error("Failed to login to Server B", "error", err)
		return c.Send("Ошибка подключения к серверу. Попробуйте позже.")
	}
	if err := b.clientC.Login(); err != nil {
		slog.Error("Failed to login to Server C", "error", err)
		return c.Send("Ошибка подключения к серверу. Попробуйте позже.")
	}

	// Update client settings
	settingsRU := threexui.ClientSettings{
		UUID:       user.UUID,
		Email:      user.Email,
		LimitIP:    3,
		TotalGB:    trialTrafficBytes,
		ExpiryTime: expiryTimeMs,
		Enable:     true,
	}

	settingsEU := threexui.ClientSettings{
		UUID:       user.UUID,
		Email:      user.Email,
		LimitIP:    3,
		TotalGB:    0,
		ExpiryTime: expiryTimeMs,
		Enable:     true,
	}

	if err := b.clientA.UpdateClient(b.config.ServerA.InboundID, user.UUID, settingsRU); err != nil {
		slog.Error("Failed to update client on Server A", "error", err)
		return c.Send("Ошибка активации. Попробуйте позже.")
	}

	if err := b.clientB.UpdateClient(b.config.ServerB.InboundID, user.UUID, settingsEU); err != nil {
		slog.Error("Failed to update client on Server B", "error", err)
		return c.Send("Ошибка активации. Попробуйте позже.")
	}

	if err := b.clientC.UpdateClient(b.config.ServerC.InboundID, user.UUID, settingsEU); err != nil {
		slog.Error("Failed to update client on Server C", "error", err)
		return c.Send("Ошибка активации. Попробуйте позже.")
	}

	// Update database
	if err := b.db.UpdateUserSubscription(user.ID, database.StatusTrial, &expiryTime); err != nil {
		slog.Error("Failed to update subscription", "error", err)
	}

	if err := b.db.MarkTrialUsed(user.ID); err != nil {
		slog.Error("Failed to mark trial used", "error", err)
	}

	subLink := b.generateSubLink(user.UUID)

	return c.Send(fmt.Sprintf(MsgTrialActivated, expiryTime.Format("02.01.2006 15:04"), subLink), &tele.SendOptions{
		ParseMode:   tele.ModeHTML,
		ReplyMarkup: InstructionsReplyKeyboard(),
	})
}

// handleStatus handles the status button
func (b *Bot) handleStatus(c tele.Context) error {
	user, err := b.db.GetUserByTelegramID(c.Sender().ID)
	if err != nil {
		slog.Error("Failed to get user", "error", err)
		return c.Send("Произошла ошибка. Попробуйте позже.")
	}

	if user == nil {
		return c.Send(MsgNoSubscription, &tele.SendOptions{
			ParseMode:   tele.ModeHTML,
			ReplyMarkup: MenuKeyboard(nil),
		})
	}

	// Check and reset traffic for unlimited subscriptions if needed
	if err := b.checkAndResetTrafficIfNeeded(user); err != nil {
		slog.Error("Failed to check/reset traffic", "error", err)
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

	return c.Send(msg, &tele.SendOptions{
		ParseMode:   tele.ModeHTML,
		ReplyMarkup: MenuKeyboard(user),
	})
}

// handleInstructionsMenu handles the instructions menu button
func (b *Bot) handleInstructionsMenu(c tele.Context) error {
	return c.Send(MsgInstructions, &tele.SendOptions{
		ParseMode:   tele.ModeHTML,
		ReplyMarkup: InstructionsReplyKeyboard(),
	})
}

// handleInstructionIOS handles iOS instruction button
func (b *Bot) handleInstructionIOS(c tele.Context) error {
	subLink := b.getSubLinkForUser(c.Sender().ID)
	return c.Send(fmt.Sprintf(MsgInstructionIOS, subLink), &tele.SendOptions{
		ParseMode:   tele.ModeHTML,
		ReplyMarkup: InstructionsReplyKeyboard(),
	})
}

// handleInstructionAndroid handles Android instruction button
func (b *Bot) handleInstructionAndroid(c tele.Context) error {
	subLink := b.getSubLinkForUser(c.Sender().ID)
	return c.Send(fmt.Sprintf(MsgInstructionAndroid, subLink), &tele.SendOptions{
		ParseMode:   tele.ModeHTML,
		ReplyMarkup: InstructionsReplyKeyboard(),
	})
}

// handleInstructionWindows handles Windows instruction button
func (b *Bot) handleInstructionWindows(c tele.Context) error {
	subLink := b.getSubLinkForUser(c.Sender().ID)
	return c.Send(fmt.Sprintf(MsgInstructionWindows, subLink), &tele.SendOptions{
		ParseMode:   tele.ModeHTML,
		ReplyMarkup: InstructionsReplyKeyboard(),
	})
}

// handleInstructionMac handles Mac instruction button
func (b *Bot) handleInstructionMac(c tele.Context) error {
	subLink := b.getSubLinkForUser(c.Sender().ID)
	return c.Send(fmt.Sprintf(MsgInstructionMac, subLink), &tele.SendOptions{
		ParseMode:   tele.ModeHTML,
		ReplyMarkup: InstructionsReplyKeyboard(),
	})
}

// handlePaymentMenu opens the payment menu
func (b *Bot) handlePaymentMenu(c tele.Context) error {
	return c.Send("Выберите опцию:", &tele.SendOptions{
		ReplyMarkup: PaymentReplyKeyboard(),
	})
}

// handleBuyTraffic handles buy traffic button
func (b *Bot) handleBuyTraffic(c tele.Context) error {
	return c.Send(MsgBuyTraffic, &tele.SendOptions{
		ParseMode:   tele.ModeHTML,
		ReplyMarkup: PaymentReplyKeyboard(),
	})
}

// handlePromo handles promo button
func (b *Bot) handlePromo(c tele.Context) error {
	b.userStates[c.Sender().ID] = StateWaitPromo
	return c.Send(MsgEnterPromo, &tele.SendOptions{
		ParseMode:   tele.ModeHTML,
		ReplyMarkup: CancelReplyKeyboard(),
	})
}

// handleSupport handles support button
func (b *Bot) handleSupport(c tele.Context) error {
	user, _ := b.db.GetUserByTelegramID(c.Sender().ID)
	return c.Send(MsgSupport, &tele.SendOptions{
		ParseMode:   tele.ModeHTML,
		ReplyMarkup: MenuKeyboard(user),
	})
}

// handleSeller handles seller info button
func (b *Bot) handleSeller(c tele.Context) error {
	user, _ := b.db.GetUserByTelegramID(c.Sender().ID)
	return c.Send(MsgSeller, &tele.SendOptions{
		ParseMode:   tele.ModeHTML,
		ReplyMarkup: MenuKeyboard(user),
	})
}

// handleBack handles back button to return to main menu
func (b *Bot) handleBack(c tele.Context) error {
	// Clear user state
	delete(b.userStates, c.Sender().ID)
	user, _ := b.db.GetUserByTelegramID(c.Sender().ID)
	return c.Send(MsgWelcomeBack, &tele.SendOptions{
		ParseMode:   tele.ModeHTML,
		ReplyMarkup: MenuKeyboard(user),
	})
}

// handlePay handles pay button
func (b *Bot) handlePay(c tele.Context) error {
	// TODO: Integrate with Robokassa
	return c.Send("Оплата будет доступна в ближайшее время.\n\nОбратитесь в поддержку для ручной активации.", &tele.SendOptions{
		ParseMode:   tele.ModeHTML,
		ReplyMarkup: PaymentReplyKeyboard(),
	})
}

// handleTextMessage handles text messages (router)
func (b *Bot) handleTextMessage(c tele.Context) error {
	telegramID := c.Sender().ID
	state := b.userStates[telegramID]
	text := c.Text()

	// Handle dynamic status button
	if strings.HasPrefix(text, StatusActiveIcon) || strings.HasPrefix(text, StatusInactiveIcon) {
		return b.handleStatus(c)
	}

	// 1. Handle States
	switch state {
	case StateWaitPromo:
		if text == BtnCancel {
			return b.handleBack(c)
		}
		return b.processPromoCode(c, c.Text())
	case StateWaitClient:
		if text == BtnCancel {
			delete(b.userStates, c.Sender().ID)
			return c.Send("Отменено", &tele.SendOptions{ReplyMarkup: AdminClientsKeyboard()})
		}
		if b.isAdmin(c) {
			return b.processCreateClient(c, c.Text())
		}
	case StateWaitClientDelete:
		if text == BtnCancel {
			delete(b.userStates, c.Sender().ID)
			return c.Send("Отменено", &tele.SendOptions{ReplyMarkup: AdminClientsKeyboard()})
		}
		if b.isAdmin(c) {
			return b.processAdminDeleteClient(c, c.Text())
		}
	case StateWaitPromoAdd:
		if text == BtnCancel {
			delete(b.userStates, c.Sender().ID)
			return c.Send("Отменено", &tele.SendOptions{ReplyMarkup: AdminPromosKeyboard()})
		}
		if b.isAdmin(c) {
			return b.processPromoAdd(c, c.Text())
		}
	case StateWaitPromoDel:
		if text == BtnCancel {
			delete(b.userStates, c.Sender().ID)
			return c.Send("Отменено", &tele.SendOptions{ReplyMarkup: AdminPromosKeyboard()})
		}
		if b.isAdmin(c) {
			return b.processPromoDel(c, c.Text())
		}
	case StateWaitBroadcastAll:
		if text == BtnCancel {
			delete(b.userStates, c.Sender().ID)
			return c.Send("Отменено", &tele.SendOptions{ReplyMarkup: AdminBroadcastKeyboard()})
		}
		if b.isAdmin(c) {
			return b.processBroadcastMessage(c, false)
		}
	case StateWaitBroadcastActive:
		if text == BtnCancel {
			delete(b.userStates, c.Sender().ID)
			return c.Send("Отменено", &tele.SendOptions{ReplyMarkup: AdminBroadcastKeyboard()})
		}
		if b.isAdmin(c) {
			return b.processBroadcastMessage(c, true)
		}
	}

	// Admin specific buttons routing
	if b.isAdmin(c) {
		switch text {
		case BtnAdminClients:
			return b.handleAdminClientsMenu(c)
		case BtnAdminPromos:
			return b.handleAdminPromosMenu(c)
		case BtnAdminBroadcast:
			return b.handleAdminBroadcastMenu(c)
		case BtnAdminUserMode:
			return b.handleBack(c) // Go to User Menu
		case BtnAdminBack:
			return b.handleAdminStart(c) // Go to Admin Main
		// Client Submenu
		case BtnAdminClientsList:
			return b.handleAdminList(c)
		case BtnAdminClientsCreate:
			return b.handleAdminCreateRequest(c)
		case BtnAdminClientsDelete:
			return b.handleAdminDeleteRequest(c)
		// Promo Submenu
		case BtnAdminPromosList:
			return b.handlePromoList(c)
		case BtnAdminPromosCreate:
			return b.handlePromoAddRequest(c)
		case BtnAdminPromosDelete:
			return b.handlePromoDelRequest(c)
		// Broadcast Submenu
		case BtnBroadcastAll:
			return b.handleBroadcastAllRequest(c)
		case BtnBroadcastActive:
			return b.handleBroadcastActiveRequest(c)
		}
	}

	// 2. Handle Menu Buttons
	switch text {
	case BtnConnect:
		return b.handleConnect(c)
	case BtnStatus:
		return b.handleStatus(c)
	case BtnInstructions:
		return b.handleInstructionsMenu(c)
	case BtnPayment:
		return b.handlePaymentMenu(c)
	case BtnPromo:
		return b.handlePromo(c)
	case BtnSupport:
		return b.handleSupport(c)
	case BtnSeller:
		return b.handleSeller(c)
	case BtnBack:
		return b.handleBack(c)
	// Sub-menu handlers
	case BtnInstIOS: return b.handleInstructionIOS(c)
	case BtnInstAndroid: return b.handleInstructionAndroid(c)
	case BtnInstWindows: return b.handleInstructionWindows(c)
	case BtnInstMac: return b.handleInstructionMac(c)
	case BtnPaySub: return b.handlePay(c)
	case BtnBuyTraffic: return b.handleBuyTraffic(c)
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

	// Login to all servers
	if err := b.clientA.Login(); err != nil {
		slog.Error("Failed to login to Server A", "error", err)
		return c.Send(fmt.Sprintf("Ошибка подключения к Server A: %v", err))
	}

	if err := b.clientB.Login(); err != nil {
		slog.Error("Failed to login to Server B", "error", err)
		return c.Send(fmt.Sprintf("Ошибка подключения к Server B: %v", err))
	}

	if err := b.clientC.Login(); err != nil {
		slog.Error("Failed to login to Server C", "error", err)
		return c.Send(fmt.Sprintf("Ошибка подключения к Server C: %v", err))
	}

	// Create settings for all servers
	settingsRU := threexui.ClientSettings{
		UUID:       clientUUID,
		Email:      email,
		LimitIP:    3,
		TotalGB:    b.config.ServerA.LimitBytes,
		ExpiryTime: expiryTimeMs,
		Enable:     true,
	}

	settingsEU := threexui.ClientSettings{
		UUID:       clientUUID,
		Email:      email,
		LimitIP:    3,
		TotalGB:    0, // Unlimited
		ExpiryTime: expiryTimeMs,
		Enable:     true,
	}

	// Add client to Server A (RU)
	errA := b.clientA.AddClientWithSettings(b.config.ServerA.InboundID, settingsRU)
	if errA != nil {
		slog.Error("Failed to add client to Server A", "error", errA)
	}

	// Add client to Server B (DE)
	errB := b.clientB.AddClientWithSettings(b.config.ServerB.InboundID, settingsEU)
	if errB != nil {
		slog.Error("Failed to add client to Server B", "error", errB)
	}

	// Add client to Server C (NL)
	errC := b.clientC.AddClientWithSettings(b.config.ServerC.InboundID, settingsEU)
	if errC != nil {
		slog.Error("Failed to add client to Server C", "error", errC)
	}

	if errA != nil || errB != nil || errC != nil {
		return c.Send(fmt.Sprintf("Ошибка:\nRU: %v\nDE: %v\nNL: %v", errA, errB, errC))
	}

	// Save to database with telegram_id = 0 (NULL, admin-created user)
	user, dbErr := b.db.CreateUser(0, email, clientUUID, "")
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
		ReplyMarkup: AdminClientsKeyboard(),
	})
}

// processPromoCode processes promo code input
func (b *Bot) processPromoCode(c tele.Context, code string) error {
	telegramID := c.Sender().ID
	delete(b.userStates, telegramID)

	user, err := b.db.GetUserByTelegramID(telegramID)
	if err != nil {
		slog.Error("Failed to get user", "error", err)
		return c.Send("Произошла ошибка.", &tele.SendOptions{
			ParseMode:   tele.ModeHTML,
			ReplyMarkup: MenuKeyboard(nil),
		})
	}

	if user == nil {
		return c.Send("Сначала активируйте триал или подписку", &tele.SendOptions{
			ParseMode:   tele.ModeHTML,
			ReplyMarkup: MenuKeyboard(nil),
		})
	}

	// Validate promo code
	promo, err := b.db.ValidatePromoCode(strings.TrimSpace(code), user.ID)
	if err != nil {
		return c.Send(fmt.Sprintf(MsgPromoError, err.Error()), &tele.SendOptions{
			ParseMode:   tele.ModeHTML,
			ReplyMarkup: MenuKeyboard(user),
		})
	}

	// Apply promo code
	if err := b.applyPromoCode(user, promo); err != nil {
		slog.Error("Failed to apply promo code", "error", err)
		return c.Send("Ошибка применения промокода", &tele.SendOptions{
			ParseMode:   tele.ModeHTML,
			ReplyMarkup: MenuKeyboard(user),
		})
	}

	// Record promo usage
	if err := b.db.UsePromoCode(user.ID, promo.ID); err != nil {
		slog.Error("Failed to record promo usage", "error", err)
	}

	result := FormatPromoResult(promo)
	return c.Send(fmt.Sprintf(MsgPromoSuccess, result), &tele.SendOptions{
		ParseMode:   tele.ModeHTML,
		ReplyMarkup: MenuKeyboard(user),
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
		// Check if subscription is active
		if user.SubscriptionStatus != database.StatusActive && user.SubscriptionStatus != database.StatusTrial {
			return fmt.Errorf("промокод на трафик можно применить только при активной подписке")
		}

		// Add extra traffic
		if err := b.db.AddRuExtraTraffic(user.ID, int64(promo.Value)); err != nil {
			return err
		}

		// Update panel limit
		return b.updatePanelTrafficLimit(user, int64(promo.Value))

	case database.PromoTypeDiscount:
		// Discount is applied during payment - nothing to do here
		return nil

	case database.PromoTypeUnlimited:
		// Set unlimited subscription
		if err := b.db.SetUnlimitedSubscription(user.ID); err != nil {
			return err
		}

		// Reset traffic in panel and set no expiry
		return b.resetPanelForUnlimited(user)
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
	if err := b.clientC.Login(); err != nil {
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

	if err := b.clientB.UpdateClient(b.config.ServerB.InboundID, user.UUID, settingsB); err != nil {
		return err
	}

	statusC, err := b.clientC.GetClientStatus(b.config.ServerC.InboundID, user.Email)
	if err != nil {
		return err
	}

	settingsC := threexui.ClientSettings{
		UUID:       user.UUID,
		Email:      user.Email,
		LimitIP:    statusC.LimitIP,
		TotalGB:    statusC.TotalGB,
		ExpiryTime: expiryTimeMs,
		Enable:     true,
	}

	return b.clientC.UpdateClient(b.config.ServerC.InboundID, user.UUID, settingsC)
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

// resetPanelForUnlimited resets traffic and removes expiry for unlimited subscription
func (b *Bot) resetPanelForUnlimited(user *database.User) error {
	if err := b.clientA.Login(); err != nil {
		return err
	}
	if err := b.clientB.Login(); err != nil {
		return err
	}
	if err := b.clientC.Login(); err != nil {
		return err
	}

	// Reset Server A with base limit and no expiry
	settingsA := threexui.ClientSettings{
		UUID:       user.UUID,
		Email:      user.Email,
		LimitIP:    3,
		TotalGB:    b.config.ServerA.LimitBytes, // 30GB
		ExpiryTime: 0,                           // No expiry
		Enable:     true,
	}
	if err := b.clientA.ResetClientTraffic(b.config.ServerA.InboundID, user.Email); err != nil {
		slog.Error("Failed to reset traffic on Server A", "error", err)
	}
	if err := b.clientA.UpdateClient(b.config.ServerA.InboundID, user.UUID, settingsA); err != nil {
		return err
	}

	// Reset Server B with no limit and no expiry
	settingsEU := threexui.ClientSettings{
		UUID:       user.UUID,
		Email:      user.Email,
		LimitIP:    3,
		TotalGB:    0, // Unlimited
		ExpiryTime: 0, // No expiry
		Enable:     true,
	}
	if err := b.clientB.UpdateClient(b.config.ServerB.InboundID, user.UUID, settingsEU); err != nil {
		return err
	}

	// Reset Server C with no limit and no expiry
	if err := b.clientC.UpdateClient(b.config.ServerC.InboundID, user.UUID, settingsEU); err != nil {
		return err
	}

	return nil
}

// checkAndResetTrafficIfNeeded checks if user needs monthly traffic reset and performs it
func (b *Bot) checkAndResetTrafficIfNeeded(user *database.User) error {
	// Only for unlimited subscriptions
	if !b.db.IsUnlimitedSubscription(user) {
		return nil
	}

	// Check if 30 days passed since last reset
	if !b.db.NeedsTrafficReset(user) {
		return nil
	}

	slog.Info("Resetting monthly traffic for unlimited user", "email", user.Email)

	// Reset traffic in panel
	if err := b.clientA.Login(); err != nil {
		return err
	}
	if err := b.clientA.ResetClientTraffic(b.config.ServerA.InboundID, user.Email); err != nil {
		return err
	}

	// Update reset timestamp
	now := time.Now()
	if err := b.db.UpdateTrafficResetAt(user.ID, now); err != nil {
		return err
	}

	// Reset extra traffic in DB
	if err := b.db.ResetRuExtraTraffic(user.ID); err != nil {
		return err
	}

	return nil
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
			if _, err := b.db.CreateUser(0, client.Email, client.ID, ""); err != nil {
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
		slog.Info("Synced user status from panel", "email", user.Email, "new_status", newStatus, "expiry", expiryTime)
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
