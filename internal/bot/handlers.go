package bot

import (
	"errors"
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
	"github.com/fus1ond/vpn_bot/internal/moynalog"
	"github.com/fus1ond/vpn_bot/internal/platega"
	"github.com/fus1ond/vpn_bot/internal/remnawave"
	"github.com/fus1ond/vpn_bot/internal/render"
	"github.com/fus1ond/vpn_bot/internal/yookassa"
	tele "gopkg.in/telebot.v3"
)

// Состояния пользователя для диалогов
const (
	StateNone                = ""
	StateWaitInvite          = "wait_invite"           // Ожидание инвайт-кода
	StateWaitBroadcastActive = "wait_broadcast_active" // Ожидание сообщения для рассылки активным
	StateWaitBugComment      = "wait_bug_comment"      // Ожидание текста багрепорта
)

// Bot представляет Telegram бота
type Bot struct {
	bot                         *tele.Bot
	db                          *database.DB
	remnawave                   *remnawave.Client
	config                      *config.Config
	userStates                  *stateMap
	metricsClient               *monitoring.MetricsClient // клиент метрик VM
	dashboardMgr                *dashboardManager         // менеджер сессий дашборда
	sdConfigsPath               string                    // путь к sd_configs (для чтения targets)
	render                      *render.Client            // клиент render-сервиса (nil если не настроен)
	platega                     *platega.Client           // Platega API клиент (nil если не настроен)
	yookassa                    *yookassa.Client          // ЮKassa API клиент (nil если не настроен)
	moynalog                    *moynalog.Client          // Клиент кабинета «Мой налог» (nil если не настроен)
	maintenanceMode             atomic.Bool               // Режим обслуживания (сбрасывается при перезапуске)
	paymentRetryDelays          []time.Duration           // Тестовые override-задержки для короткого background retry активации
	paymentRetryInFlight        sync.Map                  // payment_id -> struct{}, чтобы не плодить дублирующие retry-воркеры
	shutdownCh                  chan struct{}             // Закрывается при Stop() для отмены фоновых горутин
	userLimiter                 *userRateLimiter          // per-user rate limiter для команд бота
	adminSwitchMu               sync.RWMutex
	adminSwitchData             map[int64]adminSwitchSession // pending-данные перевода тарифа для админа
	adminPriceMu                sync.RWMutex
	adminPriceData              map[int64]adminChangePriceSession // pending-данные изменения цены для админа
	bugReportMu                 sync.RWMutex
	bugReportData               map[int64]bugReportSession // pending-данные багрепорта
	bugReportCooldown           sync.Map                   // telegram_id -> time.Time последней отправки
	adminExtendCooldown         sync.Map                   // telegram_id -> time.Time последнего продления (защита от дабл-клика)
	unmatchedEventReported      sync.Map                   // object_id -> struct{}, чтобы повторные доставки не спамили владельца
	ignoredConfirmationReported sync.Map                   // payment_id -> struct{}, одна жалоба на непринятую оплату
	revivedPaymentReported      sync.Map                   // payment_id -> struct{}, одно сообщение о воскрешённом платеже
	receiptsInFlight            sync.WaitGroup             // Запущенные пробития чеков — чтобы дождаться их при остановке
	receiptsStopMu              sync.RWMutex               // Закрывает приём новых пробитий, чтобы Add не гонялся с Wait
	receiptsStopped             bool                       // true после Stop(): новые пробития не начинаем
	receiptAuthBlocked          atomic.Bool                // Кабинет не принял вход — проход по чекам прерывается до следующего раза
	receiptAlerted              sync.Map                   // ключ алерта по чекам -> struct{}, защита от повторов
	subRevokeCooldown           sync.Map                   // telegram_id -> time.Time последнего перевыпуска ссылки
	communityDeclineMu          sync.Mutex                 // Делает «проверить кулдаун и занять его» одной операцией
	communityDeclineCooldown    sync.Map                   // telegram_id -> time.Time последнего объяснения отказа по заявке в Канал
	communityMentionMu          sync.Mutex                 // Делает «прочитать кулдаун приписки и занять его» одной операцией
	communityPendingAlerted     sync.Map                   // telegram_id -> struct{}, защита от потока алертов о зависших заявках
	panelAuthAlerted            sync.Map                   // ключ алерта про токен панели -> struct{}, защита от повторов
}

// buildBotSettings собирает настройки telebot.
//
// LongPoller обязательно подписываем на полный список типов апдейтов
// (tele.AllowedUpdates включает callback_query). Без явного AllowedUpdates
// Telegram использует дефолтный набор getUpdates, который НЕ содержит
// callback_query, — тогда нажатия inline-кнопок боту не приходят вовсе.
func buildBotSettings(token string) tele.Settings {
	return tele.Settings{
		Token: token,
		Poller: &tele.LongPoller{
			AllowedUpdates: tele.AllowedUpdates,
		},
	}
}

// New создаёт нового Telegram бота
func New(cfg *config.Config, db *database.DB, remnawaveClient *remnawave.Client) (*Bot, error) {
	pref := buildBotSettings(cfg.BotToken)

	b, err := tele.NewBot(pref)
	if err != nil {
		return nil, fmt.Errorf("failed to create bot: %w", err)
	}

	bot := &Bot{
		bot:             b,
		db:              db,
		remnawave:       remnawaveClient,
		config:          cfg,
		userStates:      newStateMap(),
		metricsClient:   monitoring.NewMetricsClient(cfg.VictoriaMetricsURL),
		dashboardMgr:    newDashboardManager(),
		sdConfigsPath:   cfg.SDConfigsPath,
		shutdownCh:      make(chan struct{}),
		adminSwitchData: make(map[int64]adminSwitchSession),
		adminPriceData:  make(map[int64]adminChangePriceSession),
		bugReportData:   make(map[int64]bugReportSession),
	}
	bot.userLimiter = newUserRateLimiter(3, 5, bot.shutdownCh) // 3 req/s, burst 5

	// Rate limiting middleware — защита от спама командами
	b.Use(func(next tele.HandlerFunc) tele.HandlerFunc {
		return func(c tele.Context) error {
			if c.Sender() != nil && !bot.userLimiter.allow(c.Sender().ID) {
				slog.Warn("Rate limit exceeded", "telegram_id", c.Sender().ID)
				return nil // Молча игнорируем
			}
			return next(c)
		}
	})

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
	if cfg.YooKassaShopID != "" && cfg.YooKassaSecretKey != "" {
		bot.yookassa = yookassa.NewClient(cfg.YooKassaShopID, cfg.YooKassaSecretKey)
		slog.Info("YooKassa client initialized")
	}

	// Интеграция с «Мой налог» включается наличием ИНН и пароля.
	if cfg.MoynalogEnabled() {
		bot.moynalog = moynalog.NewClient(cfg.MoynalogINN, cfg.MoynalogPassword)
		slog.Info("Moynalog client initialized", "service_name", cfg.MoynalogServiceName)
	}

	// Канал подключается двумя переменными окружения. Без них обработчик заявок
	// не регистрируется вовсе — бот ведёт себя ровно как раньше.
	if cfg.CommunityEnabled() {
		b.Handle(tele.OnChatJoinRequest, bot.handleChatJoinRequest)
		slog.Info("Community channel enabled", "chat_id", cfg.CommunityChatID)
	}

	// Регистрация обработчиков
	b.Handle("/start", bot.handleStart)
	b.Handle(tele.OnText, bot.handleTextMessage)
	b.Handle(tele.OnPhoto, bot.handleMediaMessage)
	b.Handle(tele.OnVideo, bot.handleMediaMessage)
	b.Handle(tele.OnDocument, bot.handleMediaMessage)
	b.Handle(tele.OnVoice, bot.handleVoiceMessage)
	b.Handle(tele.OnVideoNote, bot.handleVideoNoteMessage)

	// Inline-кнопки управления устройствами (роутинг по Unique)
	devMenu := &tele.ReplyMarkup{}
	btnDevManage := devMenu.Data("", cbDevicesManage)
	btnDevDelete := devMenu.Data("", cbDeviceDelete)
	btnDevResetAll := devMenu.Data("", cbDevicesResetAll)
	btnDevResetAllOK := devMenu.Data("", cbDevicesResetAllConfirm)

	b.Handle(&btnDevManage, bot.handleDevicesManage)
	b.Handle(&btnDevDelete, bot.handleDeviceDelete)
	b.Handle(&btnDevResetAll, bot.handleDevicesResetAll)
	b.Handle(&btnDevResetAllOK, bot.handleDevicesResetAllConfirm)

	// Inline-кнопки карточки подписки (роутинг по Unique)
	subMenu := &tele.ReplyMarkup{}
	btnSubCard := subMenu.Data("", cbSubCard)
	btnSubRevoke := subMenu.Data("", cbSubRevoke)
	btnSubRevokeOK := subMenu.Data("", cbSubRevokeConfirm)
	btnSubRevokeCancel := subMenu.Data("", cbSubRevokeCancel)

	b.Handle(&btnSubCard, bot.handleSubscriptionCard)
	b.Handle(&btnSubRevoke, bot.handleSubRevoke)
	b.Handle(&btnSubRevokeOK, bot.handleSubRevokeConfirm)
	b.Handle(&btnSubRevokeCancel, bot.handleSubRevokeCancel)

	// Inline-кнопки багрепорта (роутинг по Unique)
	bugMenu := &tele.ReplyMarkup{}
	btnBugServer := bugMenu.Data("", cbBugServer)
	btnBugServerDone := bugMenu.Data("", cbBugServerDone)
	btnBugCategory := bugMenu.Data("", cbBugCategory)
	btnBugCancel := bugMenu.Data("", cbBugCancel)

	b.Handle(&btnBugServer, bot.handleBugServerToggle)
	b.Handle(&btnBugServerDone, bot.handleBugServersDone)
	b.Handle(&btnBugCategory, bot.handleBugCategorySelected)
	b.Handle(&btnBugCancel, bot.handleBugCancel)

	// Inline-кнопки ручного продления подписки админом (роутинг по Unique)
	extMenu := &tele.ReplyMarkup{}
	btnExtMonth := extMenu.Data("", cbAdminExtendMonth)
	btnExtConfirm := extMenu.Data("", cbAdminExtendConfirm)
	btnExtCancel := extMenu.Data("", cbAdminExtendCancel)

	b.Handle(&btnExtMonth, bot.handleAdminExtendMonth)
	b.Handle(&btnExtConfirm, bot.handleAdminExtendConfirm)
	b.Handle(&btnExtCancel, bot.handleAdminExtendCancel)

	refMenu := &tele.ReplyMarkup{}
	btnRefResend := refMenu.Data("", cbReferralResend)
	btnRefRevoke := refMenu.Data("", cbReferralRevoke)
	btnRefRevokeOK := refMenu.Data("", cbReferralRevokeOK)
	btnRefPage := refMenu.Data("", cbReferralPage)
	btnRefBack := refMenu.Data("", cbReferralBack)
	b.Handle(&btnRefResend, bot.handleReferralResend)
	b.Handle(&btnRefRevoke, bot.handleReferralRevoke)
	b.Handle(&btnRefRevokeOK, bot.handleReferralRevokeConfirm)
	b.Handle(&btnRefPage, bot.handleReferralPage)
	b.Handle(&btnRefBack, bot.handleReferralClose)

	adminRefMenu := &tele.ReplyMarkup{}
	btnAdminRefOverview := adminRefMenu.Data("", cbAdminReferralOverview)
	btnAdminRefLeaders := adminRefMenu.Data("", cbAdminReferralLeaders)
	btnAdminUserRefs := adminRefMenu.Data("", cbAdminUserReferrals)
	btnAdminRefRevoke := adminRefMenu.Data("", cbAdminReferralRevoke)
	btnAdminRefRevokeOK := adminRefMenu.Data("", cbAdminReferralRevokeOK)
	btnAdminRefBack := adminRefMenu.Data("", cbAdminReferralBack)
	b.Handle(&btnAdminRefOverview, bot.handleAdminReferralOverview)
	b.Handle(&btnAdminRefLeaders, bot.handleAdminReferralLeaderboard)
	b.Handle(&btnAdminUserRefs, bot.handleAdminUserReferrals)
	b.Handle(&btnAdminRefRevoke, bot.handleAdminReferralRevoke)
	b.Handle(&btnAdminRefRevokeOK, bot.handleAdminReferralRevokeConfirm)
	b.Handle(&btnAdminRefBack, bot.handleAdminReferralBack)

	return bot, nil
}

// Run запускает бота
func (b *Bot) Run() {
	slog.Info("Bot started", "username", b.bot.Me.Username)
	b.bot.Start()
}

// Stop останавливает бота (для graceful shutdown)
func (b *Bot) Stop() {
	close(b.shutdownCh)
	// Начатое пробитие чека доводим до конца: оборванная попытка оставила бы
	// в базе состояние, по которому непонятно, есть чек в кабинете или нет.
	// Сначала закрываем приём новых, потом ждём начатые.
	b.stopReceipts()
	b.waitReceipts()
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
			remaining := graceDeadline.Sub(time.Now().UTC())
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

	// В шаге ввода комментария багрепорта навигационная кнопка меню должна
	// сбросить флоу (иначе текст кнопки уйдёт в комментарий), кроме «Пропустить».
	if state == StateWaitBugComment && text != BtnBugSkip && isMenuNavigationButton(text) {
		b.clearBugReportSession(telegramID)
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

	case StateWaitBugComment:
		if text == BtnCancel {
			b.clearBugReportSession(telegramID)
			b.userStates.Delete(telegramID)
			return c.Send("Отменено.", &tele.SendOptions{ReplyMarkup: b.userKeyboard(telegramID)})
		}
		comment := text
		if text == BtnBugSkip {
			comment = ""
		}
		return b.finishBugReport(c, comment)

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

	case StateAdminUserInfoCard:
		if text == BtnCancel {
			b.userStates.Delete(telegramID)
			return c.Send("Отменено", &tele.SendOptions{ReplyMarkup: AdminManageKeyboard()})
		}
		// Любое другое действие выводит из карточки — обрабатываем его как обычно
		b.userStates.Delete(telegramID)

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

	case StateWaitPaymentMethod:
		if text == BtnCancel {
			b.userStates.Delete(telegramID)
			return c.Send("Отменено.", &tele.SendOptions{ReplyMarkup: b.userKeyboard(telegramID)})
		}
		if provider, ok := paymentProviderFromButton(text); ok {
			return b.handlePaymentMethodSelected(c, provider)
		}
		return c.Send("Выберите способ оплаты из меню:", &tele.SendOptions{ReplyMarkup: b.paymentMethodKeyboard()})

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
		case BtnAdminReferrals:
			return b.handleAdminReferralsMenu(c)
		case BtnAdminReferralOverview:
			return b.showAdminReferralOverview(c, "30", false)
		case BtnAdminReferralLeaders:
			return b.showAdminReferralLeaderboard(c, "30", 0, false)
		}
	}

	// Общая система приглашений доступна всем зарегистрированным пользователям.
	switch text {
	case BtnInvites:
		return b.handleInvitesMenu(c)
	case BtnInviteCreate:
		return b.handleCreateReferralInvite(c)
	case BtnInviteList:
		return b.handleReferralList(c)
	case BtnInviteBack:
		return b.handleBack(c)
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
	case BtnBugReport:
		return b.handleBugReportStart(c)
	case BtnServers:
		return b.handleDashboard(c)
	case BtnBack:
		return b.handleBack(c)
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
		switch {
		case errors.Is(err, database.ErrInviteExpired):
			return c.Send("❌ Срок действия приглашения истёк. Попросите новую ссылку:")
		case errors.Is(err, database.ErrInviteRevoked):
			return c.Send("❌ Приглашение было отозвано. Попросите новую ссылку:")
		case errors.Is(err, database.ErrInviteUsed):
			return c.Send("❌ Приглашение уже использовано. Попросите новую ссылку:")
		case errors.Is(err, database.ErrInviteNotFound):
			return c.Send("❌ Инвайт-код не найден. Проверьте код или попросите новую ссылку:")
		default:
			return c.Send("❌ Приглашение сейчас недоступно. Попробуйте другой код:")
		}
	}

	invite, err := b.db.GetInviteByCode(code)
	if err != nil || invite == nil {
		slog.Error("Failed to load invite after claim", "code", code, "error", err)
		_ = b.db.UnclaimInvite(code, telegramID)
		return c.Send("Ошибка обработки приглашения. Попробуйте позже.")
	}

	// Создаём пользователя в Remnawave
	username := c.Sender().Username
	if username == "" {
		username = fmt.Sprintf("tg_%d", telegramID)
	}

	expireAt := time.Date(2099, time.January, 1, 0, 0, 0, 0, time.UTC)
	if invite.Kind == database.InviteKindReferral || invite.IsTrial {
		expireAt = time.Now().UTC().Add(72 * time.Hour)
	}

	// Определяем лимит трафика: триал получает ограничение, админский инвайт — безлимит
	var trafficLimitBytes int64
	if invite.Kind == database.InviteKindReferral || invite.IsTrial {
		trafficLimitBytes = int64(b.config.TrialTrafficLimitGB) * 1024 * 1024 * 1024
	}

	remnawaveUser, err := b.remnawave.CreateUser(telegramID, username, expireAt, trafficLimitBytes)
	if err != nil {
		slog.Error("Failed to create user in Remnawave", "error", err)
		// Откатываем инвайт — пользователь не создан
		_ = b.db.UnclaimInvite(code, telegramID)
		return c.Send("Ошибка создания аккаунта. Попробуйте позже или обратитесь к администратору.")
	}

	// Цена берётся из snapshot инвайта, first-touch — из всей истории referral.
	var subscriptionPrice *int
	if invite.SubscriptionPrice != nil {
		subscriptionPrice = invite.SubscriptionPrice
	}
	var invitedBy *int64
	if invite.Kind == database.InviteKindReferral {
		invitedBy, err = b.db.GetFirstReferralInviter(telegramID)
		if err != nil {
			slog.Error("Failed to resolve first referral inviter", "error", err, "telegram_id", telegramID)
			b.rollbackCreatedRemnawaveUser(code, telegramID, remnawaveUser.Ref())
			return c.Send("Ошибка создания аккаунта. Попробуйте позже.")
		}
	}

	// Сохраняем обе половины связки: на 2.8.x значим UUID, на 3.x — числовой id.
	// UUID у пользователя, созданного на 3.x, пустой и уйдёт в колонку как NULL.
	_, err = b.db.CreateUserWithInviter(telegramID, username, c.Sender().FirstName,
		remnawaveUUIDPtr(remnawaveUser), &remnawaveUser.ID, subscriptionPrice, nil, invitedBy)
	if err != nil {
		slog.Error("Failed to create user in DB", "error", err)
		// Claim освобождается только после подтверждённого удаления из Remnawave.
		b.rollbackCreatedRemnawaveUser(code, telegramID, remnawaveUser.Ref())
		return c.Send("Ошибка создания аккаунта. Попробуйте позже.")
	}

	// Отправляем уведомление админу о новом пользователе (асинхронно)
	if b.bot != nil {
		go b.notifyAdminNewUser(telegramID, username, c.Sender().FirstName)
		go b.notifyReferralActivated(invite, telegramID, c.Sender().FirstName, c.Sender().Username)
	}

	// Очищаем состояние
	b.userStates.Delete(telegramID)

	// Приветствие несёт reply-клавиатуру главного меню, поэтому подключение
	// уходит отдельным сообщением с inline-кнопкой на страницу подписки.
	if err := c.Send(MsgAccountCreated, &tele.SendOptions{
		ParseMode:   tele.ModeHTML,
		ReplyMarkup: b.userKeyboard(telegramID),
	}); err != nil {
		return err
	}

	hint := fmt.Sprintf(MsgConnectHint, html.EscapeString(remnawaveUser.SubscriptionURL))
	return sendWithInlineFallback(c, hint, ConnectKeyboard(remnawaveUser.SubscriptionURL))
}

// rollbackCreatedRemnawaveUser компенсирует частично завершённую регистрацию.
// Если панель временно не смогла удалить пользователя, claim намеренно остаётся:
// startup-reconcile увидит его и безопасно завершит восстановление/откат.
func (b *Bot) rollbackCreatedRemnawaveUser(code string, telegramID int64, ref remnawave.UserRef) {
	err := b.remnawave.DeleteUser(ref)
	if err != nil && !isRemnawaveNotFound(err) {
		slog.Error("Partial registration rollback: keeping invite claimed for startup reconcile",
			"error", err, "telegram_id", telegramID, "invite_code", code,
			"remnawave_uuid", ref.UUID, "remnawave_id", ref.ID)
		return
	}
	if err := b.db.UnclaimInvite(code, telegramID); err != nil {
		slog.Error("Failed to release invite after Remnawave rollback", "error", err, "telegram_id", telegramID, "invite_code", code)
	}
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
	remnawaveUser, err := b.remnawaveUser(telegramID)
	if err != nil {
		slog.Error("Failed to get user from Remnawave", "error", err)
		return c.Send("Ошибка получения статуса. Попробуйте позже.")
	}

	// Карточка подписки самодостаточна: статус, ссылка и inline-кнопки
	// (страница подписки, устройства, перевыпуск). Reply-клавиатура главного
	// меню остаётся снизу нетронутой, отдельное подменю не нужно.
	msg, markup := b.buildSubscriptionCard(telegramID, remnawaveUser)
	return sendWithInlineFallback(c, msg, markup)
}

// handleInfo показывает помощь, контакты и ссылки на документы сервиса
func (b *Bot) handleInfo(c tele.Context) error {
	return c.Send(BuildInfoMessage(b.config), &tele.SendOptions{
		ParseMode:   tele.ModeHTML,
		ReplyMarkup: b.userKeyboard(c.Sender().ID),
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

// userKeyboard возвращает правильную клавиатуру для пользователя
// с динамической кнопкой оплаты и общим разделом приглашений
func (b *Bot) userKeyboard(telegramID int64) *tele.ReplyMarkup {
	// Определяем, показывать ли кнопку оплаты и какой текст
	user, err := b.db.GetUserByTelegramID(telegramID)
	if err != nil || user == nil {
		return UserMenuKeyboardDynamic("", false, false)
	}

	// Нет цены — кнопка оплаты скрыта. Для администратора тестовая цена из
	// окружения заменяет персональную цену и не меняет запись пользователя.
	if price, ok := b.paymentPrice(telegramID, user); !ok || price <= 0 {
		return UserMenuKeyboardDynamic("", false, true)
	}

	// Нет ни одного провайдера — кнопка оплаты скрыта
	if b.platega == nil && b.yookassa == nil {
		return UserMenuKeyboardDynamic("", false, true)
	}

	// В режиме обслуживания скрываем оплату для всех.
	if b.isMaintenanceMode() {
		return UserMenuKeyboardDynamic("", false, true)
	}

	// Проверяем тип подписки для определения текста кнопки
	remUser, err := b.remnawave.GetUserByTelegramID(telegramID)
	if err != nil || remUser == nil {
		return UserMenuKeyboardDynamic(BtnPay, true, true)
	}

	// Бесконечная подписка — кнопка скрыта (если не админ)
	if remUser.ExpireAt.Year() >= 2099 && telegramID != b.config.AdminID {
		return UserMenuKeyboardDynamic("", false, true)
	}

	// Триал или grace → "Оплатить", оплаченная → "Продлить"
	if b.isTrialUser(telegramID) || remUser.Status == remnawave.StatusDisabled {
		return UserMenuKeyboardDynamic(BtnPay, true, true)
	}

	return UserMenuKeyboardDynamic(BtnRenew, true, true)
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
		ref, err := b.userRef(telegramID)
		if err != nil {
			slog.Error("Failed to resolve user ref for username sync", "error", err, "telegram_id", telegramID)
			return
		}
		if err := b.remnawave.UpdateUsername(ref, usernameForRemnawave); err != nil {
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
		BtnBugReport,
		BtnServers,
		BtnBack,
		BtnInvites,
		BtnInviteCreate,
		BtnInviteList,
		BtnInviteBack,
		BtnAdminManage,
		BtnAdminBroadcast,
		BtnAdminStats,
		BtnAdminMaintenance,
		BtnAdminMaintenanceOff,
		BtnAdminUserMode,
		BtnAdminBack,
		BtnAdminCreateInvite,
		BtnAdminBanUser,
		BtnAdminUserInfo,
		BtnAdminSwitchSubscription,
		BtnAdminSwitchInfinite,
		BtnBroadcastActive,
		BtnAdminReferrals,
		BtnAdminReferralOverview,
		BtnAdminReferralLeaders:
		return true
	default:
		return false
	}
}
