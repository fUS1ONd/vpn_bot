package bot

import (
	"context"
	"fmt"
	"log/slog"
	"sync"
	"time"

	"github.com/fus1ond/vpn_bot/internal/monitoring"
	tele "gopkg.in/telebot.v3"
)

const (
	dashboardUpdateInterval = 5 * time.Second
	dashboardTTL            = 60 * time.Second
)

// Inline-кнопки дашборда (глобальные для регистрации обработчиков)
var (
	btnDashRefresh = tele.InlineButton{Unique: "dash_refresh", Text: "🔄 Обновить"}
	btnDashStop    = tele.InlineButton{Unique: "dash_stop", Text: "⏹ Стоп"}
	btnDashStart   = tele.InlineButton{Unique: "dash_start", Text: "▶️ Запустить"}
)

// dashboardSession — активная сессия мониторинга для одного чата
type dashboardSession struct {
	cancel    context.CancelFunc
	chatID    int64
	msgID     int
	startedAt time.Time
}

// dashboardManager — менеджер активных сессий дашборда
type dashboardManager struct {
	mu       sync.Mutex
	sessions map[int64]*dashboardSession // chatID -> session
}

func newDashboardManager() *dashboardManager {
	return &dashboardManager{
		sessions: make(map[int64]*dashboardSession),
	}
}

// stop останавливает активную сессию для чата, если есть
func (dm *dashboardManager) stop(chatID int64) {
	dm.mu.Lock()
	defer dm.mu.Unlock()
	if s, ok := dm.sessions[chatID]; ok {
		s.cancel()
		delete(dm.sessions, chatID)
	}
}

// set сохраняет новую сессию
func (dm *dashboardManager) set(chatID int64, session *dashboardSession) {
	dm.mu.Lock()
	defer dm.mu.Unlock()
	dm.sessions[chatID] = session
}

// registerDashboardHandlers регистрирует обработчики inline-кнопок дашборда
func (b *Bot) registerDashboardHandlers() {
	b.bot.Handle(&btnDashRefresh, b.handleDashCallbackRefresh)
	b.bot.Handle(&btnDashStop, b.handleDashCallbackStop)
	b.bot.Handle(&btnDashStart, b.handleDashCallbackStart)
}

// handleDashboard запускает live-дашборд для пользователя
func (b *Bot) handleDashboard(c tele.Context) error {
	chatID := c.Chat().ID

	// Останавливаем предыдущую сессию если есть
	b.dashboardMgr.stop(chatID)

	// Отправляем начальное сообщение
	msg, err := b.bot.Send(c.Chat(), "⏳ <i>Загрузка данных...</i>", &tele.SendOptions{
		ParseMode: tele.ModeHTML,
	})
	if err != nil {
		return fmt.Errorf("ошибка отправки начального сообщения: %w", err)
	}

	// Создаём контекст с отменой
	ctx, cancel := context.WithCancel(context.Background())

	session := &dashboardSession{
		cancel:    cancel,
		chatID:    chatID,
		msgID:     msg.ID,
		startedAt: time.Now(),
	}
	b.dashboardMgr.set(chatID, session)

	// Запускаем горутину обновлений
	go b.runDashboardLoop(ctx, chatID, msg.ID, session.startedAt)

	return nil
}

// runDashboardLoop — цикл обновления дашборда
func (b *Bot) runDashboardLoop(ctx context.Context, chatID int64, msgID int, startedAt time.Time) {
	ticker := time.NewTicker(dashboardUpdateInterval)
	defer ticker.Stop()

	deadline := time.After(dashboardTTL)
	var lastText string
	var lastStats []monitoring.NodeStats

	// Первое обновление сразу
	remaining := dashboardTTL - time.Since(startedAt)
	lastStats, lastText = b.updateDashboardMessage(chatID, msgID, lastText, remaining)

	for {
		select {
		case <-ctx.Done():
			// Остановлен пользователем или менеджером
			return

		case <-deadline:
			// TTL истёк — засыпаем
			b.dashboardMgr.stop(chatID)
			b.sendDashboardStopped(chatID, msgID, lastStats)
			return

		case <-ticker.C:
			remaining = dashboardTTL - time.Since(startedAt)
			if remaining < 0 {
				remaining = 0
			}
			lastStats, lastText = b.updateDashboardMessage(chatID, msgID, lastText, remaining)
		}
	}
}

// updateDashboardMessage получает метрики и обновляет сообщение
func (b *Bot) updateDashboardMessage(chatID int64, msgID int, prevText string, remaining time.Duration) ([]monitoring.NodeStats, string) {
	// Читаем текущие targets
	targets, err := monitoring.ReadTargets(b.sdConfigsPath)
	if err != nil {
		slog.Error("Ошибка чтения targets", "error", err)
		return nil, prevText
	}

	// Получаем метрики из VictoriaMetrics
	stats, err := b.metricsClient.GetAllNodeStats(targets)
	if err != nil {
		slog.Error("Ошибка получения метрик", "error", err)
		return nil, prevText
	}

	// Рендерим сообщение
	text := renderDashboard(stats, remaining)

	// Не дёргаем API если текст не изменился (дедупликация)
	if text == prevText {
		return stats, prevText
	}

	// Inline-кнопки
	markup := &tele.ReplyMarkup{}
	markup.Inline(
		markup.Row(tele.Btn{Unique: btnDashRefresh.Unique, Text: btnDashRefresh.Text},
			tele.Btn{Unique: btnDashStop.Unique, Text: btnDashStop.Text}),
	)

	// Редактируем сообщение
	_, err = b.bot.Edit(&tele.Message{
		ID:   msgID,
		Chat: &tele.Chat{ID: chatID},
	}, text, &tele.SendOptions{
		ParseMode: tele.ModeHTML,
	}, markup)
	if err != nil {
		slog.Error("Ошибка редактирования дашборда", "error", err, "chat_id", chatID)
	}

	return stats, text
}

// sendDashboardStopped показывает «приостановлен» с кнопкой «Запустить»
func (b *Bot) sendDashboardStopped(chatID int64, msgID int, lastStats []monitoring.NodeStats) {
	text := renderDashboardStopped(lastStats)

	markup := &tele.ReplyMarkup{}
	markup.Inline(
		markup.Row(tele.Btn{Unique: btnDashStart.Unique, Text: btnDashStart.Text}),
	)

	_, err := b.bot.Edit(&tele.Message{
		ID:   msgID,
		Chat: &tele.Chat{ID: chatID},
	}, text, &tele.SendOptions{
		ParseMode: tele.ModeHTML,
	}, markup)
	if err != nil {
		slog.Error("Ошибка отправки stopped-дашборда", "error", err)
	}
}

// handleDashCallbackRefresh — кнопка «Обновить» (сбрасывает TTL)
func (b *Bot) handleDashCallbackRefresh(c tele.Context) error {
	chatID := c.Chat().ID

	// Останавливаем текущую сессию и запускаем новую с тем же сообщением
	b.dashboardMgr.stop(chatID)

	msgID := c.Message().ID
	ctx, cancel := context.WithCancel(context.Background())
	now := time.Now()

	session := &dashboardSession{
		cancel:    cancel,
		chatID:    chatID,
		msgID:     msgID,
		startedAt: now,
	}
	b.dashboardMgr.set(chatID, session)

	go b.runDashboardLoop(ctx, chatID, msgID, now)

	return c.Respond(&tele.CallbackResponse{Text: "🔄 Обновлено"})
}

// handleDashCallbackStop — кнопка «Стоп»
func (b *Bot) handleDashCallbackStop(c tele.Context) error {
	chatID := c.Chat().ID
	b.dashboardMgr.stop(chatID)

	// Получаем последние данные для отображения
	targets, _ := monitoring.ReadTargets(b.sdConfigsPath)
	stats, _ := b.metricsClient.GetAllNodeStats(targets)

	b.sendDashboardStopped(chatID, c.Message().ID, stats)

	return c.Respond(&tele.CallbackResponse{Text: "⏹ Остановлено"})
}

// handleDashCallbackStart — кнопка «Запустить снова»
func (b *Bot) handleDashCallbackStart(c tele.Context) error {
	chatID := c.Chat().ID
	b.dashboardMgr.stop(chatID)

	msgID := c.Message().ID
	ctx, cancel := context.WithCancel(context.Background())
	now := time.Now()

	session := &dashboardSession{
		cancel:    cancel,
		chatID:    chatID,
		msgID:     msgID,
		startedAt: now,
	}
	b.dashboardMgr.set(chatID, session)

	go b.runDashboardLoop(ctx, chatID, msgID, now)

	return c.Respond(&tele.CallbackResponse{Text: "▶️ Запущено"})
}

// BotAlertSender — реализация AlertSender через Telegram бота
type BotAlertSender struct {
	bot     *tele.Bot
	adminID int64
}

// NewBotAlertSender создаёт AlertSender для отправки алертов админу
func NewBotAlertSender(b *Bot) *BotAlertSender {
	return &BotAlertSender{
		bot:     b.bot,
		adminID: b.config.AdminID,
	}
}

// SendAlert отправляет алерт админу
func (s *BotAlertSender) SendAlert(text string) error {
	admin := &tele.User{ID: s.adminID}
	_, err := s.bot.Send(admin, text, &tele.SendOptions{
		ParseMode: tele.ModeHTML,
	})
	return err
}

// MetricsClient возвращает клиент метрик бота
func (b *Bot) MetricsClient() *monitoring.MetricsClient {
	return b.metricsClient
}
