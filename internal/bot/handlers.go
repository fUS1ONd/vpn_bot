package bot

import (
	"fmt"
	"html"
	"log/slog"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/fus1ond/vpn_bot/internal/config"
	"github.com/fus1ond/vpn_bot/internal/database"
	"github.com/fus1ond/vpn_bot/internal/monitoring"
	"github.com/fus1ond/vpn_bot/internal/platega"
	"github.com/fus1ond/vpn_bot/internal/remnawave"
	"github.com/fus1ond/vpn_bot/internal/render"
	tele "gopkg.in/telebot.v3"
)

// Состояния пользователя для диалогов
const (
	StateNone                = ""
	StateWaitInvite          = "wait_invite"           // Ожидание инвайт-кода
	StateWaitBroadcastActive = "wait_broadcast_active" // Ожидание сообщения для рассылки активным
)

// Bot представляет Telegram бота
type Bot struct {
	bot                  *tele.Bot
	db                   *database.DB
	remnawave            *remnawave.Client
	config               *config.Config
	userStates           *stateMap
	metricsClient        *monitoring.MetricsClient // клиент метрик VM
	dashboardMgr         *dashboardManager         // менеджер сессий дашборда
	sdConfigsPath        string                    // путь к sd_configs (для чтения targets)
	render               *render.Client            // клиент render-сервиса (nil если не настроен)
	platega              *platega.Client           // Platega API клиент (nil если не настроен)
	maintenanceMode      atomic.Bool               // Режим обслуживания (сбрасывается при перезапуске)
	paymentRetryDelays   []time.Duration           // Тестовые override-задержки для короткого background retry активации
	paymentRetryInFlight sync.Map                  // payment_id -> struct{}, чтобы не плодить дублирующие retry-воркеры
	modChangePriceMu     sync.RWMutex
	modChangePriceData   map[int64]modChangePriceSession // pending-данные изменения цены для модератора
	adminSwitchMu        sync.RWMutex
	adminSwitchData      map[int64]adminSwitchSession // pending-данные перевода тарифа для админа
	adminPriceMu         sync.RWMutex
	adminPriceData       map[int64]adminChangePriceSession // pending-данные изменения цены для админа
}

// New создаёт нового Telegram бота
func New(cfg *config.Config, db *database.DB, remnawaveClient *remnawave.Client) (*Bot, error) {
	pref := tele.Settings{
		Token:  cfg.BotToken,
		Poller: &tele.LongPoller{},
	}

	b, err := tele.NewBot(pref)
	if err != nil {
		return nil, fmt.Errorf("failed to create bot: %w", err)
	}

	bot := &Bot{
		bot:                b,
		db:                 db,
		remnawave:          remnawaveClient,
		config:             cfg,
		userStates:         newStateMap(),
		metricsClient:      monitoring.NewMetricsClient(cfg.VictoriaMetricsURL),
		dashboardMgr:       newDashboardManager(),
		sdConfigsPath:      cfg.SDConfigsPath,
		modChangePriceData: make(map[int64]modChangePriceSession),
		adminSwitchData:    make(map[int64]adminSwitchSession),
		adminPriceData:     make(map[int64]adminChangePriceSession),
	}

	// Middleware для логирования
	b.Use(func(next tele.HandlerFunc) tele.HandlerFunc {
		return func(c tele.Context) error {
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

	// Инициализация render-клиента (опционально)
	if cfg.RenderURL != "" {
		bot.render = render.NewClient(cfg.RenderURL, cfg.RenderAPIKey)
		slog.Info("Render service enabled", "url", cfg.RenderURL)
	}

	// Инициализация Platega-клиента (опционально)
	if cfg.PlategaMerchantID != "" && cfg.PlategaSecret != "" {
		bot.platega = platega.NewClient(cfg.PlategaMerchantID, cfg.PlategaSecret)
		slog.Info("Platega client initialized")
	}

	// Регистрация обработчиков
	b.Handle("/start", bot.handleStart)
	b.Handle(tele.OnText, bot.handleTextMessage)
	b.Handle(tele.OnPhoto, bot.handleMediaMessage)
	b.Handle(tele.OnVideo, bot.handleMediaMessage)
	b.Handle(tele.OnDocument, bot.handleMediaMessage)
	b.Handle(tele.OnVoice, bot.handleVoiceMessage)
	b.Handle(tele.OnVideoNote, bot.handleVideoNoteMessage)

	return bot, nil
}

// Run запускает бота
func (b *Bot) Run() {
	slog.Info("Bot started", "username", b.bot.Me.Username)
	b.bot.Start()
}

// Stop останавливает бота (для graceful shutdown)
func (b *Bot) Stop() {
	b.bot.Stop()
}

// handleMediaMessage обрабатывает медиа-сообщения (для рассылки)
func (b *Bot) handleMediaMessage(c tele.Context) error {
	telegramID := c.Sender().ID
	state := b.userStates.Get(telegramID)

	if state == StateWaitBroadcastActive && b.isAdmin(c) {
		return b.processBroadcastMessage(c)
	}

	return nil
}

// handleStart обрабатывает команду /start
func (b *Bot) handleStart(c tele.Context) error {
	telegramID := c.Sender().ID

	// Проверка на админа
	if telegramID == b.config.AdminID {
		return b.handleAdminStart(c)
	}

	// Блокированные пользователи не допускаются к боту.
	if banned, err := b.db.IsBanned(telegramID); err == nil && banned {
		return c.Send("🚫 Ваш аккаунт заблокирован. Доступ запрещён.")
	}

	// Проверяем, зарегистрирован ли пользователь
	user, err := b.db.GetUserByTelegramID(telegramID)
	if err != nil {
		slog.Error("Failed to get user from DB", "error", err)
		return c.Send("Произошла ошибка. Попробуйте позже.")
	}

	// Новый пользователь — требуется инвайт
	if user == nil {
		// Проверяем deep link payload (из ссылки /start <code>)
		payload := ""
		if msg := c.Message(); msg != nil {
			payload = strings.TrimSpace(msg.Payload)
		}

		if payload != "" {
			// Пытаемся автоматически активировать код из deep link
			err := b.processInviteCode(c, payload)
			if err != nil {
				return err
			}
			// Если processInviteCode показал ошибку (код не найден/использован),
			// ставим StateWaitInvite чтобы юзер мог ввести код вручную
			if b.userStates.Get(telegramID) == "" {
				// Код невалиден — processInviteCode отправил ошибку, ставим ожидание
				existsNow, _ := b.db.UserExists(telegramID)
				if !existsNow {
					b.userStates.Set(telegramID, StateWaitInvite)
				}
			}
			return nil
		}

		b.userStates.Set(telegramID, StateWaitInvite)
		return c.Send(MsgWelcomeInvite, &tele.SendOptions{
			ParseMode: tele.ModeHTML,
		})
	}

	// Существующий пользователь — синхронизируем данные
	// Очищаем состояние ожидания инвайта, если оно было (чтобы не блокировать доступ)
	b.userStates.Delete(telegramID)

	// Актуализируем username и first_name в БД и Remnawave
	b.syncUserInfo(c)

	// Проверяем grace period — показываем тревожный экран
	remUser, err := b.remnawave.GetUserByTelegramID(telegramID)
	if err == nil && remUser != nil {
		subType := determineSubscriptionType(remUser, b.isTrialUser(telegramID))
		if subType == subTypeGrace {
			graceDeadline := remUser.ExpireAt.Add(72 * time.Hour)
			remaining := time.Until(graceDeadline)
			var remainStr string
			days := int(remaining.Hours() / 24)
			if days > 0 {
				remainStr = fmt.Sprintf("%d дн.", days)
			} else {
				hours := int(remaining.Hours())
				if hours > 0 {
					remainStr = fmt.Sprintf("%d ч.", hours)
				} else {
					remainStr = "менее часа"
				}
			}
			msg := fmt.Sprintf(MsgGraceWarning, remainStr, graceDeadline.Format("02.01.2006"))
			return c.Send(msg, &tele.SendOptions{
				ParseMode:   tele.ModeHTML,
				ReplyMarkup: b.userKeyboard(telegramID),
			})
		}
	}

	return c.Send(MsgWelcomeBack, &tele.SendOptions{
		ParseMode:   tele.ModeHTML,
		ReplyMarkup: b.userKeyboard(telegramID),
	})
}

// handleTextMessage роутер текстовых сообщений
func (b *Bot) handleTextMessage(c tele.Context) error {
	telegramID := c.Sender().ID
	state := b.userStates.Get(telegramID)
	text := c.Text()

	if isPaymentFlowState(state) && isMenuNavigationButton(text) {
		b.userStates.Delete(telegramID)
		state = StateNone
	}

	// Обработка состояний
	switch state {
	case StateWaitInvite:
		if text == BtnCancel {
			b.userStates.Delete(telegramID)
			return c.Send("Отменено. Для начала отправьте /start")
		}
		return b.processInviteCode(c, text)

	case StateWaitBroadcastActive:
		if text == BtnCancel {
			b.userStates.Delete(telegramID)
			return c.Send("Отменено", &tele.SendOptions{ReplyMarkup: AdminKeyboard(b.isMaintenanceMode())})
		}
		if b.isAdmin(c) {
			return b.processBroadcastMessage(c)
		}

	case StateWaitBanUser:
		if text == BtnCancel {
			b.userStates.Delete(telegramID)
			return c.Send("Отменено", &tele.SendOptions{ReplyMarkup: AdminKeyboard(b.isMaintenanceMode())})
		}
		if b.isAdmin(c) {
			return b.processBanUser(c, text)
		}

	case StateWaitDeleteInvite:
		if text == BtnCancel {
			b.userStates.Delete(telegramID)
			return c.Send("Отменено", &tele.SendOptions{ReplyMarkup: AdminKeyboard(b.isMaintenanceMode())})
		}
		if b.isAdmin(c) {
			return b.processDeleteInvite(c, text)
		}

	case StateWaitSwitchSubscriptionID:
		if text == BtnCancel {
			b.userStates.Delete(telegramID)
			b.clearAdminSwitchSession(telegramID)
			return c.Send("Отменено", &tele.SendOptions{ReplyMarkup: AdminManageKeyboard()})
		}
		if b.isAdmin(c) {
			return b.processSwitchSubscriptionID(c, text)
		}

	case StateWaitSwitchSubscriptionConfirm:
		if text == BtnCancel {
			b.userStates.Delete(telegramID)
			b.clearAdminSwitchSession(telegramID)
			return c.Send("Отменено", &tele.SendOptions{ReplyMarkup: AdminManageKeyboard()})
		}
		if b.isAdmin(c) {
			return b.processSwitchSubscriptionConfirm(c, text)
		}

	case StateWaitAdminUserInfo:
		if text == BtnCancel {
			b.userStates.Delete(telegramID)
			return c.Send("Отменено", &tele.SendOptions{ReplyMarkup: AdminManageKeyboard()})
		}
		if b.isAdmin(c) {
			return b.processAdminUserInfo(c, text)
		}

	case StateWaitAdminChangePriceID:
		if text == BtnCancel {
			b.userStates.Delete(telegramID)
			b.clearAdminChangePriceSession(telegramID)
			return c.Send("Отменено", &tele.SendOptions{ReplyMarkup: AdminManageKeyboard()})
		}
		if b.isAdmin(c) {
			return b.processAdminChangePriceID(c, text)
		}

	case StateWaitAdminChangePriceValue:
		if text == BtnCancel {
			b.userStates.Delete(telegramID)
			b.clearAdminChangePriceSession(telegramID)
			return c.Send("Отменено", &tele.SendOptions{ReplyMarkup: AdminManageKeyboard()})
		}
		if b.isAdmin(c) {
			return b.processAdminChangePriceValue(c, text)
		}

	case StateWaitAdminChangePriceMigrationConfirm:
		if text == BtnCancel {
			b.userStates.Delete(telegramID)
			b.clearAdminChangePriceSession(telegramID)
			return c.Send("Отменено", &tele.SendOptions{ReplyMarkup: AdminManageKeyboard()})
		}
		if b.isAdmin(c) {
			return b.processAdminChangePriceMigrationConfirm(c, text)
		}

	case StateWaitModDeleteInvite:
		if text == BtnCancel {
			b.userStates.Delete(telegramID)
			return c.Send("Отменено", &tele.SendOptions{ReplyMarkup: ModeratorMenuKeyboard()})
		}
		if b.isModerator(telegramID) {
			return b.processModeratorDeleteInvite(c, text)
		}

	case StateWaitModInvitePrice:
		if text == BtnCancel {
			b.userStates.Delete(telegramID)
			return c.Send("Отменено", &tele.SendOptions{ReplyMarkup: ModeratorMenuKeyboard()})
		}
		if b.isModerator(telegramID) {
			return b.processModeratorInvitePrice(c, text)
		}

	case StateWaitModChangePriceID:
		if text == BtnCancel {
			b.userStates.Delete(telegramID)
			b.clearModChangePriceSession(telegramID)
			return c.Send("Отменено", &tele.SendOptions{ReplyMarkup: ModeratorSubscribersKeyboard()})
		}
		if b.isModerator(telegramID) {
			return b.processModChangePriceID(c, text)
		}

	case StateWaitModChangePriceValue:
		if text == BtnCancel {
			b.userStates.Delete(telegramID)
			b.clearModChangePriceSession(telegramID)
			return c.Send("Отменено", &tele.SendOptions{ReplyMarkup: ModeratorSubscribersKeyboard()})
		}
		if b.isModerator(telegramID) {
			return b.processModChangePriceValue(c, text)
		}

	case StateWaitAddModerator:
		if text == BtnCancel {
			b.userStates.Delete(telegramID)
			return c.Send("Отменено", &tele.SendOptions{ReplyMarkup: AdminKeyboard(b.isMaintenanceMode())})
		}
		if b.isAdmin(c) {
			return b.processAddModerator(c, text)
		}

	case StateWaitRemoveModerator:
		if text == BtnCancel {
			b.userStates.Delete(telegramID)
			return c.Send("Отменено", &tele.SendOptions{ReplyMarkup: AdminKeyboard(b.isMaintenanceMode())})
		}
		if b.isAdmin(c) {
			return b.processRemoveModerator(c, text)
		}

	case StateWaitPaymentMethod:
		if text == BtnCancel {
			b.userStates.Delete(telegramID)
			return c.Send("Отменено.", &tele.SendOptions{ReplyMarkup: b.userKeyboard(telegramID)})
		}
		if method, ok := paymentMethodFromButton(text); ok {
			return b.handlePaymentMethodSelected(c, method)
		}
		return c.Send("Выберите способ оплаты из меню:", &tele.SendOptions{ReplyMarkup: PaymentMethodKeyboard()})

	case StateWaitPaymentResult:
		if text == BtnCancel {
			b.userStates.Delete(telegramID)
			return c.Send("Возврат в меню. Платёж не отменён — он протухнет автоматически.", &tele.SendOptions{
				ReplyMarkup: b.userKeyboard(telegramID),
			})
		}
		if text == BtnCheckPayment {
			return b.handleCheckPayment(c)
		}
		return c.Send("Нажмите \"🔄 Проверить оплату\" или \"🚫 Отмена\".", &tele.SendOptions{
			ReplyMarkup: PaymentWaitKeyboard(),
		})
	}

	// Админ-кнопки
	if b.isAdmin(c) {
		switch text {
		case BtnAdminManage:
			return b.handleAdminManageMenu(c)
		case BtnAdminBroadcast:
			return b.handleAdminBroadcastMenu(c)
		case BtnAdminStats:
			return b.handleAdminStats(c)
		case BtnAdminMaintenance, BtnAdminMaintenanceOff:
			return b.handleAdminMaintenanceToggle(c)
		case BtnAdminUserMode:
			return b.handleUserMode(c)
		case BtnAdminBack:
			return b.handleAdminStart(c)
		case BtnAdminCreateInvite:
			return b.handleCreateInvite(c)
		case BtnAdminBanUser:
			return b.handleBanUserRequest(c)
		case BtnAdminViewInvites:
			return b.handleViewInvites(c)
		case BtnAdminDeleteInvite:
			return b.handleDeleteInviteRequest(c)
		case BtnAdminSwitchSubscription:
			return b.handleSwitchSubscription(c)
		case BtnAdminSwitchInfinite:
			return b.handleAdminSwitchInfiniteRequest(c)
		case BtnAdminChangePrice:
			return b.handleAdminChangePriceRequest(c)
		case BtnAdminUserInfo:
			return b.handleAdminUserInfoRequest(c)
		case BtnBroadcastActive:
			return b.handleBroadcastActiveRequest(c)
		case BtnAdminModerators:
			return b.handleAdminModeratorMenu(c)
		case BtnAdminAddModerator:
			return b.handleAdminAddModeratorRequest(c)
		case BtnAdminListMods:
			return b.handleAdminListModerators(c)
		case BtnAdminRemoveMod:
			return b.handleAdminRemoveModeratorRequest(c)
		case BtnAdminModStats:
			return b.handleAdminModStats(c)
		}
	}

	// Кнопки модератора
	if b.isModerator(telegramID) {
		switch text {
		case BtnModInvites:
			return b.handleModeratorMenu(c)
		case BtnModCreate:
			return b.handleModeratorCreateInvite(c)
		case BtnModView:
			return b.handleModeratorViewInvites(c)
		case BtnModDelete:
			return b.handleModeratorDeleteInviteRequest(c)
		case BtnModSubscribers:
			return b.handleModSubscribers(c)
		case BtnModEarnings:
			return b.handleModeratorEarnings(c)
		case BtnModChangePrice:
			return b.handleModChangePriceRequest(c)
		case BtnBack:
			if b.userStates.Get(c.Sender().ID) == StateModSubscribers {
				return b.handleModeratorMenu(c)
			}
		case BtnModBack:
			return b.handleModeratorBack(c)
		}
	}

	// Кнопки пользователя
	switch text {
	case BtnStatus:
		return b.handleStatus(c)
	case BtnPay, BtnRenew:
		return b.handlePayButton(c)
	case BtnCheckPayment:
		return b.handleCheckPayment(c)
	case BtnInfo:
		return b.handleInfo(c)
	case BtnServers:
		return b.handleDashboard(c)
	case BtnInstructions:
		return b.handleInstructionsMenu(c)
	case BtnBack:
		return b.handleBack(c)
	case BtnInstIOS:
		return b.handleInstructionIOS(c)
	case BtnInstAndroid:
		return b.handleInstructionAndroid(c)
	case BtnInstDesktop:
		return b.handleInstructionDesktop(c)
	}

	// Неизвестное сообщение — показываем меню
	return b.handleStart(c)
}

// processInviteCode обрабатывает ввод инвайт-кода
func (b *Bot) processInviteCode(c tele.Context, code string) error {
	telegramID := c.Sender().ID
	code = strings.TrimSpace(code)

	// Атомарно забираем инвайт (защита от race condition — два пользователя с одним кодом)
	err := b.db.ClaimInvite(code, telegramID)
	if err != nil {
		slog.Warn("Failed to claim invite", "code", code, "error", err)
		return c.Send("❌ Инвайт-код не найден или уже использован. Попробуйте другой:")
	}

	invite, err := b.db.GetInviteByCode(code)
	if err != nil || invite == nil {
		slog.Error("Failed to load invite after claim", "code", code, "error", err)
		_ = b.db.UnclaimInvite(code)
		return c.Send("Ошибка обработки приглашения. Попробуйте позже.")
	}

	// Создаём пользователя в Remnawave
	username := c.Sender().Username
	if username == "" {
		username = fmt.Sprintf("tg_%d", telegramID)
	}

	expireAt := time.Date(2099, time.January, 1, 0, 0, 0, 0, time.UTC)
	if invite.ExpireDays != nil {
		expireAt = time.Now().UTC().Add(72 * time.Hour)
	}

	// Определяем лимит трафика: триал получает ограничение, админский инвайт — безлимит
	var trafficLimitBytes int64
	if invite.ExpireDays != nil {
		trafficLimitBytes = int64(b.config.TrialTrafficLimitGB) * 1024 * 1024 * 1024
	}

	remnawaveUser, err := b.remnawave.CreateUser(telegramID, username, expireAt, trafficLimitBytes)
	if err != nil {
		slog.Error("Failed to create user in Remnawave", "error", err)
		// Откатываем инвайт — пользователь не создан
		_ = b.db.UnclaimInvite(code)
		return c.Send("Ошибка создания аккаунта. Попробуйте позже или обратитесь к администратору.")
	}

	// Определяем subscription_price и moderator_id из инвайта
	var subscriptionPrice *int
	var moderatorID *int64
	if invite.SubscriptionPrice != nil {
		subscriptionPrice = invite.SubscriptionPrice
	}
	if invite.ExpireDays != nil && b.isModerator(invite.CreatedBy) {
		// Модераторский инвайт — ставим created_by как moderator_id
		moderatorID = &invite.CreatedBy
	}

	// Сохраняем связку в БД
	_, err = b.db.CreateUser(telegramID, username, c.Sender().FirstName, remnawaveUser.UUID, subscriptionPrice, moderatorID)
	if err != nil {
		slog.Error("Failed to create user in DB", "error", err)
		// Откатываем: удаляем из Remnawave и освобождаем инвайт
		_ = b.remnawave.DeleteUser(remnawaveUser.UUID)
		_ = b.db.UnclaimInvite(code)
		return c.Send("Ошибка создания аккаунта. Попробуйте позже.")
	}

	// Отправляем уведомление админу о новом пользователе (асинхронно)
	if b.bot != nil {
		go b.notifyAdminNewUser(telegramID, username, c.Sender().FirstName)
	}

	// Очищаем состояние
	b.userStates.Delete(telegramID)

	// Отправляем приветствие
	msg := fmt.Sprintf(MsgAccountCreated, remnawaveUser.SubscriptionURL)
	return c.Send(msg, &tele.SendOptions{
		ParseMode:   tele.ModeHTML,
		ReplyMarkup: b.userKeyboard(telegramID),
	})
}

// notifyAdminNewUser отправляет админу уведомление о новом пользователе
func (b *Bot) notifyAdminNewUser(telegramID int64, username, firstName string) {
	var msg strings.Builder
	msg.WriteString("🆕 <b>Новый пользователь</b>\n\n")

	// Дата и время
	now := time.Now()
	fmt.Fprintf(&msg, "📅 %s\n", now.Format("02.01.06 15:04"))

	// Telegram ID с кликабельной ссылкой
	userLink := fmt.Sprintf("<a href=\"tg://user?id=%d\">%d</a>", telegramID, telegramID)
	if username != "" {
		fmt.Fprintf(&msg, "🆔 %s (@%s)\n", userLink, username)
	} else {
		fmt.Fprintf(&msg, "🆔 %s\n", userLink)
	}

	// First name (если есть)
	if firstName != "" {
		fmt.Fprintf(&msg, "👤 %s\n", html.EscapeString(firstName))
	}

	admin := &tele.User{ID: b.config.AdminID}
	_, err := b.bot.Send(admin, msg.String(), &tele.SendOptions{
		ParseMode: tele.ModeHTML,
	})
	if err != nil {
		slog.Error("Failed to notify admin about new user", "error", err)
	}
}

// handleStatus показывает статус пользователя
func (b *Bot) handleStatus(c tele.Context) error {
	telegramID := c.Sender().ID

	user, err := b.db.GetUserByTelegramID(telegramID)
	if err != nil || user == nil {
		return c.Send(MsgNotRegistered, &tele.SendOptions{ParseMode: tele.ModeHTML})
	}

	// Получаем данные из Remnawave
	remnawaveUser, err := b.remnawave.GetUser(user.RemnawaveUUID)
	if err != nil {
		slog.Error("Failed to get user from Remnawave", "error", err)
		return c.Send("Ошибка получения статуса. Попробуйте позже.")
	}

	var devicesCount *int
	count, err := b.remnawave.GetUserHwidDevicesCount(user.RemnawaveUUID)
	if err != nil {
		slog.Warn("Failed to get user HWID devices for status", "error", err, "telegram_id", telegramID)
	} else {
		devicesCount = &count
	}

	msg := FormatUserStatus(remnawaveUser, user, b.isTrialUser(telegramID), devicesCount)
	return c.Send(msg, &tele.SendOptions{
		ParseMode:   tele.ModeHTML,
		ReplyMarkup: b.userKeyboard(telegramID),
	})
}

// handleInfo показывает помощь, контакты и ссылки на документы сервиса
func (b *Bot) handleInfo(c tele.Context) error {
	return c.Send(MsgInfo, &tele.SendOptions{
		ParseMode:   tele.ModeHTML,
		ReplyMarkup: b.userKeyboard(c.Sender().ID),
	})
}

// handleInstructionsMenu показывает меню инструкций
func (b *Bot) handleInstructionsMenu(c tele.Context) error {
	return c.Send(MsgInstructions, &tele.SendOptions{
		ParseMode:   tele.ModeHTML,
		ReplyMarkup: InstructionsKeyboard(),
	})
}

// handleBack возвращает в главное меню
func (b *Bot) handleBack(c tele.Context) error {
	b.userStates.Delete(c.Sender().ID)

	// Проверяем, зарегистрирован ли пользователь
	user, _ := b.db.GetUserByTelegramID(c.Sender().ID)
	if user == nil {
		return b.handleStart(c)
	}

	return c.Send(MsgWelcomeBack, &tele.SendOptions{
		ParseMode:   tele.ModeHTML,
		ReplyMarkup: b.userKeyboard(c.Sender().ID),
	})
}

// handleUserMode переключает админа в режим пользователя
func (b *Bot) handleUserMode(c tele.Context) error {
	return c.Send(MsgWelcomeBack, &tele.SendOptions{
		ParseMode:   tele.ModeHTML,
		ReplyMarkup: b.userKeyboard(c.Sender().ID),
	})
}

// getSubLinkForUser возвращает ссылку подписки для пользователя
func (b *Bot) getSubLinkForUser(telegramID int64) string {
	user, err := b.db.GetUserByTelegramID(telegramID)
	if err != nil || user == nil {
		return "Сначала активируйте подписку"
	}

	remnawaveUser, err := b.remnawave.GetUser(user.RemnawaveUUID)
	if err != nil {
		return "Ошибка получения ссылки"
	}

	return remnawaveUser.SubscriptionURL
}

// handleInstructionIOS - инструкция для iOS
func (b *Bot) handleInstructionIOS(c tele.Context) error {
	subLink := b.getSubLinkForUser(c.Sender().ID)
	return c.Send(fmt.Sprintf(MsgInstructionIOS, subLink), &tele.SendOptions{
		ParseMode:   tele.ModeHTML,
		ReplyMarkup: InstructionsKeyboard(),
	})
}

// handleInstructionAndroid - инструкция для Android
func (b *Bot) handleInstructionAndroid(c tele.Context) error {
	subLink := b.getSubLinkForUser(c.Sender().ID)
	return c.Send(fmt.Sprintf(MsgInstructionAndroid, subLink), &tele.SendOptions{
		ParseMode:   tele.ModeHTML,
		ReplyMarkup: InstructionsKeyboard(),
	})
}

// handleInstructionDesktop - инструкция для ПК
func (b *Bot) handleInstructionDesktop(c tele.Context) error {
	subLink := b.getSubLinkForUser(c.Sender().ID)
	return c.Send(fmt.Sprintf(MsgInstructionDesktop, subLink), &tele.SendOptions{
		ParseMode:   tele.ModeHTML,
		ReplyMarkup: InstructionsKeyboard(),
	})
}

// userKeyboard возвращает правильную клавиатуру для пользователя
// с динамической кнопкой оплаты и учётом роли модератора
func (b *Bot) userKeyboard(telegramID int64) *tele.ReplyMarkup {
	isMod := b.isModerator(telegramID)

	// Определяем, показывать ли кнопку оплаты и какой текст
	user, err := b.db.GetUserByTelegramID(telegramID)
	if err != nil || user == nil {
		return UserMenuKeyboardDynamic("", false, isMod)
	}

	// Нет цены — кнопка оплаты скрыта
	if user.SubscriptionPrice == nil {
		return UserMenuKeyboardDynamic("", false, isMod)
	}

	// Нет Platega — кнопка оплаты скрыта
	if b.platega == nil {
		return UserMenuKeyboardDynamic("", false, isMod)
	}

	// В режиме обслуживания скрываем оплату для всех.
	if b.isMaintenanceMode() {
		return UserMenuKeyboardDynamic("", false, isMod)
	}

	// Проверяем тип подписки для определения текста кнопки
	remUser, err := b.remnawave.GetUserByTelegramID(telegramID)
	if err != nil || remUser == nil {
		return UserMenuKeyboardDynamic(BtnPay, true, isMod)
	}

	// Бесконечная подписка — кнопка скрыта (если не админ)
	if remUser.ExpireAt.Year() >= 2099 && telegramID != b.config.AdminID {
		return UserMenuKeyboardDynamic("", false, isMod)
	}

	// Триал или grace → "Оплатить", оплаченная → "Продлить"
	if b.isTrialUser(telegramID) || remUser.Status == remnawave.StatusDisabled {
		return UserMenuKeyboardDynamic(BtnPay, true, isMod)
	}

	return UserMenuKeyboardDynamic(BtnRenew, true, isMod)
}

// getBotUsername возвращает username бота для формирования deep link
func (b *Bot) getBotUsername() string {
	if b.bot != nil && b.bot.Me != nil {
		return b.bot.Me.Username
	}
	return "bot"
}

// syncUserInfo синхронизирует username и first_name пользователя с БД и Remnawave
// Вызывается при каждом взаимодействии пользователя с ботом для актуализации данных
func (b *Bot) syncUserInfo(c tele.Context) {
	telegramID := c.Sender().ID
	currentUsername := c.Sender().Username
	currentFirstName := c.Sender().FirstName

	// Получаем данные из БД
	user, err := b.db.GetUserByTelegramID(telegramID)
	if err != nil || user == nil {
		return // Пользователь не зарегистрирован - ничего не делаем
	}

	// Проверяем изменения
	usernameChanged := user.Username != currentUsername
	firstNameChanged := user.FirstName != currentFirstName

	if !usernameChanged && !firstNameChanged {
		return // Нет изменений
	}

	// Обновляем в БД
	if err := b.db.UpdateUserInfo(telegramID, currentUsername, currentFirstName); err != nil {
		slog.Error("Failed to update user info in DB", "error", err, "telegram_id", telegramID)
	}

	// Синхронизируем username с Remnawave (только если изменился)
	if usernameChanged {
		// Если username пустой, используем tg_id
		usernameForRemnawave := currentUsername
		if usernameForRemnawave == "" {
			usernameForRemnawave = fmt.Sprintf("tg_%d", telegramID)
		}
		if err := b.remnawave.UpdateUsername(user.RemnawaveUUID, usernameForRemnawave); err != nil {
			slog.Error("Failed to sync username to Remnawave", "error", err, "telegram_id", telegramID)
		}
	}
}

func isPaymentFlowState(state string) bool {
	return state == StateWaitPaymentMethod || state == StateWaitPaymentResult
}

func isMenuNavigationButton(text string) bool {
	switch text {
	case BtnStatus,
		BtnPay,
		BtnRenew,
		BtnInfo,
		BtnServers,
		BtnInstructions,
		BtnBack,
		BtnInstIOS,
		BtnInstAndroid,
		BtnInstDesktop,
		BtnModInvites,
		BtnModCreate,
		BtnModView,
		BtnModSubscribers,
		BtnModEarnings,
		BtnModChangePrice,
		BtnModDelete,
		BtnModBack,
		BtnAdminManage,
		BtnAdminBroadcast,
		BtnAdminStats,
		BtnAdminMaintenance,
		BtnAdminMaintenanceOff,
		BtnAdminUserMode,
		BtnAdminBack,
		BtnAdminCreateInvite,
		BtnAdminViewInvites,
		BtnAdminDeleteInvite,
		BtnAdminBanUser,
		BtnAdminUserInfo,
		BtnAdminSwitchSubscription,
		BtnAdminSwitchInfinite,
		BtnBroadcastActive,
		BtnAdminModerators,
		BtnAdminAddModerator,
		BtnAdminListMods,
		BtnAdminModStats,
		BtnAdminRemoveMod:
		return true
	default:
		return false
	}
}
