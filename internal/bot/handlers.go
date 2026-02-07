package bot

import (
	"fmt"
	"log/slog"
	"strings"
	"time"

	"github.com/fus1ond/vpn_bot/internal/config"
	"github.com/fus1ond/vpn_bot/internal/database"
	"github.com/fus1ond/vpn_bot/internal/monitoring"
	"github.com/fus1ond/vpn_bot/internal/remnawave"
	tele "gopkg.in/telebot.v3"
)

// Состояния пользователя для диалогов
const (
	StateNone               = ""
	StateWaitInvite         = "wait_invite"          // Ожидание инвайт-кода
	StateWaitBroadcastAll   = "wait_broadcast_all"   // Ожидание сообщения для рассылки всем
	StateWaitBroadcastActive = "wait_broadcast_active" // Ожидание сообщения для рассылки активным
	StateWaitAddTraffic     = "wait_add_traffic"     // Ожидание данных для добавления трафика
)

// Bot представляет Telegram бота
type Bot struct {
	bot           *tele.Bot
	db            *database.DB
	remnawave     *remnawave.Client
	config        *config.Config
	userStates    map[int64]string
	metricsClient *monitoring.MetricsClient // клиент метрик VM
	dashboardMgr  *dashboardManager         // менеджер сессий дашборда
	sdConfigsPath string                    // путь к sd_configs (для чтения targets)
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
		bot:           b,
		db:            db,
		remnawave:     remnawaveClient,
		config:        cfg,
		userStates:    make(map[int64]string),
		metricsClient: monitoring.NewMetricsClient(cfg.VictoriaMetricsURL),
		dashboardMgr:  newDashboardManager(),
		sdConfigsPath: cfg.SDConfigsPath,
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

	// Регистрация обработчиков
	b.Handle("/start", bot.handleStart)
	b.Handle(tele.OnCallback, bot.handleCallback)
	b.Handle(tele.OnText, bot.handleTextMessage)
	b.Handle(tele.OnPhoto, bot.handleMediaMessage)
	b.Handle(tele.OnVideo, bot.handleMediaMessage)
	b.Handle(tele.OnDocument, bot.handleMediaMessage)

	// Обработчики inline-кнопок дашборда
	bot.registerDashboardHandlers()

	return bot, nil
}

// Run запускает бота
func (b *Bot) Run() {
	slog.Info("Bot started", "username", b.bot.Me.Username)
	b.bot.Start()
}

// handleCallback обрабатывает callback-запросы (fallback для незарегистрированных кнопок)
func (b *Bot) handleCallback(c tele.Context) error {
	callback := c.Callback()
	if callback == nil {
		return nil
	}

	slog.Info("Unhandled callback", "data", callback.Data, "from", c.Sender().ID)
	return c.Respond()
}

// handleMediaMessage обрабатывает медиа-сообщения (для рассылки)
func (b *Bot) handleMediaMessage(c tele.Context) error {
	telegramID := c.Sender().ID
	state := b.userStates[telegramID]

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

	return nil
}

// handleStart обрабатывает команду /start
func (b *Bot) handleStart(c tele.Context) error {
	telegramID := c.Sender().ID

	// Проверка на админа
	if telegramID == b.config.AdminID {
		return b.handleAdminStart(c)
	}

	// Проверяем, зарегистрирован ли пользователь
	user, err := b.db.GetUserByTelegramID(telegramID)
	if err != nil {
		slog.Error("Failed to get user from DB", "error", err)
		return c.Send("Произошла ошибка. Попробуйте позже.")
	}

	// Новый пользователь — требуется инвайт
	if user == nil {
		b.userStates[telegramID] = StateWaitInvite
		return c.Send(MsgWelcomeInvite, &tele.SendOptions{
			ParseMode: tele.ModeHTML,
		})
	}

	// Существующий пользователь — синхронизируем данные
	// Очищаем состояние ожидания инвайта, если оно было (чтобы не блокировать доступ)
	delete(b.userStates, telegramID)

	// Актуализируем username и first_name в БД и Remnawave
	b.syncUserInfo(c)

	return c.Send(MsgWelcomeBack, &tele.SendOptions{
		ParseMode:   tele.ModeHTML,
		ReplyMarkup: UserMenuKeyboard(),
	})
}

// handleTextMessage роутер текстовых сообщений
func (b *Bot) handleTextMessage(c tele.Context) error {
	telegramID := c.Sender().ID
	state := b.userStates[telegramID]
	text := c.Text()

	// Обработка состояний
	switch state {
	case StateWaitInvite:
		if text == BtnCancel {
			delete(b.userStates, telegramID)
			return c.Send("Отменено. Для начала отправьте /start")
		}
		return b.processInviteCode(c, text)

	case StateWaitBroadcastAll:
		if text == BtnCancel {
			delete(b.userStates, telegramID)
			return c.Send("Отменено", &tele.SendOptions{ReplyMarkup: AdminKeyboard()})
		}
		if b.isAdmin(c) {
			return b.processBroadcastMessage(c, false)
		}

	case StateWaitBroadcastActive:
		if text == BtnCancel {
			delete(b.userStates, telegramID)
			return c.Send("Отменено", &tele.SendOptions{ReplyMarkup: AdminKeyboard()})
		}
		if b.isAdmin(c) {
			return b.processBroadcastMessage(c, true)
		}

	case StateWaitAddTraffic:
		if text == BtnCancel {
			delete(b.userStates, telegramID)
			return c.Send("Отменено", &tele.SendOptions{ReplyMarkup: AdminKeyboard()})
		}
		if b.isAdmin(c) {
			return b.processAddTraffic(c, text)
		}

	case StateWaitBanUser:
		if text == BtnCancel {
			delete(b.userStates, telegramID)
			return c.Send("Отменено", &tele.SendOptions{ReplyMarkup: AdminKeyboard()})
		}
		if b.isAdmin(c) {
			return b.processBanUser(c, text)
		}

	case StateWaitDeleteInvite:
		if text == BtnCancel {
			delete(b.userStates, telegramID)
			return c.Send("Отменено", &tele.SendOptions{ReplyMarkup: AdminKeyboard()})
		}
		if b.isAdmin(c) {
			return b.processDeleteInvite(c, text)
		}
	}

	// Админ-кнопки
	if b.isAdmin(c) {
		switch text {
		case BtnAdminManage:
			return b.handleAdminManageMenu(c)
		case BtnAdminBroadcast:
			return b.handleAdminBroadcastMenu(c)
		case BtnAdminUserMode:
			return b.handleUserMode(c)
		case BtnAdminBack:
			return b.handleAdminStart(c)
		case BtnAdminCreateInvite:
			return b.handleCreateInvite(c)
		case BtnAdminAddTraffic:
			return b.handleAddTrafficRequest(c)
		case BtnAdminBanUser:
			return b.handleBanUserRequest(c)
		case BtnAdminViewInvites:
			return b.handleViewInvites(c)
		case BtnAdminDeleteInvite:
			return b.handleDeleteInviteRequest(c)
		case BtnBroadcastActive:
			return b.handleBroadcastActiveRequest(c)
		}
	}

	// Кнопки пользователя
	switch text {
	case BtnStatus:
		return b.handleStatus(c)
	case BtnConnect:
		return b.handleConnect(c)
	case BtnDonate:
		return b.handleDonate(c)
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
	case BtnInstWindows:
		return b.handleInstructionWindows(c)
	case BtnInstMac:
		return b.handleInstructionMac(c)
	}

	// Неизвестное сообщение — показываем меню
	return b.handleStart(c)
}

// processInviteCode обрабатывает ввод инвайт-кода
func (b *Bot) processInviteCode(c tele.Context, code string) error {
	telegramID := c.Sender().ID
	code = strings.TrimSpace(code)

	// Проверяем инвайт
	invite, err := b.db.GetInviteByCode(code)
	if err != nil {
		slog.Error("Failed to get invite", "error", err)
		return c.Send("Произошла ошибка. Попробуйте позже.")
	}

	if invite == nil {
		return c.Send("❌ Инвайт-код не найден. Попробуйте ещё раз:")
	}

	if invite.UsedBy != nil {
		return c.Send("❌ Этот инвайт-код уже использован. Попробуйте другой:")
	}

	// Создаём пользователя в Remnawave
	username := c.Sender().Username
	if username == "" {
		username = fmt.Sprintf("tg_%d", telegramID)
	}

	remnawaveUser, err := b.remnawave.CreateUser(telegramID, username)
	if err != nil {
		slog.Error("Failed to create user in Remnawave", "error", err)
		return c.Send("Ошибка создания аккаунта. Попробуйте позже или обратитесь к администратору.")
	}

	// Сохраняем связку в БД
	_, err = b.db.CreateUser(telegramID, username, c.Sender().FirstName, remnawaveUser.UUID)
	if err != nil {
		slog.Error("Failed to create user in DB", "error", err)
		// Пытаемся удалить из Remnawave если не смогли сохранить в БД
		_ = b.remnawave.DeleteUser(remnawaveUser.UUID)
		return c.Send("Ошибка создания аккаунта. Попробуйте позже.")
	}

	// Помечаем инвайт как использованный
	if err := b.db.UseInvite(code, telegramID); err != nil {
		slog.Error("Failed to mark invite as used", "error", err)
	}

	// Отправляем уведомление админу о новом пользователе (асинхронно)
	go b.notifyAdminNewUser(telegramID, username, c.Sender().FirstName)

	// Очищаем состояние
	delete(b.userStates, telegramID)

	// Отправляем приветствие
	msg := fmt.Sprintf(MsgAccountCreated, remnawaveUser.SubscriptionURL)
	return c.Send(msg, &tele.SendOptions{
		ParseMode:   tele.ModeHTML,
		ReplyMarkup: UserMenuKeyboard(),
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
		fmt.Fprintf(&msg, "👤 %s\n", firstName)
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

	msg := FormatUserStatus(remnawaveUser)
	return c.Send(msg, &tele.SendOptions{
		ParseMode:   tele.ModeHTML,
		ReplyMarkup: UserMenuKeyboard(),
	})
}

// handleConnect показывает ссылку подключения
func (b *Bot) handleConnect(c tele.Context) error {
	telegramID := c.Sender().ID

	user, err := b.db.GetUserByTelegramID(telegramID)
	if err != nil || user == nil {
		return c.Send(MsgNotRegistered, &tele.SendOptions{ParseMode: tele.ModeHTML})
	}

	// Получаем данные из Remnawave
	remnawaveUser, err := b.remnawave.GetUser(user.RemnawaveUUID)
	if err != nil {
		slog.Error("Failed to get user from Remnawave", "error", err)
		return c.Send("Ошибка получения ссылки. Попробуйте позже.")
	}

	msg := fmt.Sprintf(MsgSubscriptionLink, remnawaveUser.SubscriptionURL)
	return c.Send(msg, &tele.SendOptions{
		ParseMode:   tele.ModeHTML,
		ReplyMarkup: UserMenuKeyboard(),
	})
}

// handleDonate показывает информацию о донате
func (b *Bot) handleDonate(c tele.Context) error {
	msg := fmt.Sprintf(MsgDonate, b.config.DonateText)
	return c.Send(msg, &tele.SendOptions{
		ParseMode:   tele.ModeHTML,
		ReplyMarkup: UserMenuKeyboard(),
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
	delete(b.userStates, c.Sender().ID)

	// Проверяем, зарегистрирован ли пользователь
	user, _ := b.db.GetUserByTelegramID(c.Sender().ID)
	if user == nil {
		return b.handleStart(c)
	}

	return c.Send(MsgWelcomeBack, &tele.SendOptions{
		ParseMode:   tele.ModeHTML,
		ReplyMarkup: UserMenuKeyboard(),
	})
}

// handleUserMode переключает админа в режим пользователя
func (b *Bot) handleUserMode(c tele.Context) error {
	return c.Send(MsgWelcomeBack, &tele.SendOptions{
		ParseMode:   tele.ModeHTML,
		ReplyMarkup: UserMenuKeyboard(),
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

// handleInstructionWindows - инструкция для Windows
func (b *Bot) handleInstructionWindows(c tele.Context) error {
	subLink := b.getSubLinkForUser(c.Sender().ID)
	return c.Send(fmt.Sprintf(MsgInstructionWindows, subLink), &tele.SendOptions{
		ParseMode:   tele.ModeHTML,
		ReplyMarkup: InstructionsKeyboard(),
	})
}

// handleInstructionMac - инструкция для macOS
func (b *Bot) handleInstructionMac(c tele.Context) error {
	subLink := b.getSubLinkForUser(c.Sender().ID)
	return c.Send(fmt.Sprintf(MsgInstructionMac, subLink), &tele.SendOptions{
		ParseMode:   tele.ModeHTML,
		ReplyMarkup: InstructionsKeyboard(),
	})
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