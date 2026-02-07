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

	// Первое обновление сразу
	remaining := dashboardTTL - time.Since(startedAt)
	_, lastText = b.updateDashboardMessage(chatID, msgID, lastText, remaining)

	for {
		select {
		case <-ctx.Done():
			// Остановлен менеджером (повторное нажатие «Серверы»)
			return

		case <-deadline:
			// TTL истёк — финальное обновление с пометкой времени
			b.dashboardMgr.stop(chatID)
			b.sendDashboardFinished(chatID, msgID)
			return

		case <-ticker.C:
			remaining = dashboardTTL - time.Since(startedAt)
			if remaining < 0 {
				remaining = 0
			}
			_, lastText = b.updateDashboardMessage(chatID, msgID, lastText, remaining)
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

	// Редактируем сообщение (без inline-кнопок)
	_, err = b.bot.Edit(&tele.Message{
		ID:   msgID,
		Chat: &tele.Chat{ID: chatID},
	}, text, &tele.SendOptions{
		ParseMode: tele.ModeHTML,
	})
	if err != nil {
		slog.Error("Ошибка редактирования дашборда", "error", err, "chat_id", chatID)
	}

	return stats, text
}

// sendDashboardFinished — финальное обновление после истечения TTL с пометкой времени
func (b *Bot) sendDashboardFinished(chatID int64, msgID int) {
	// Читаем последние данные
	targets, err := monitoring.ReadTargets(b.sdConfigsPath)
	if err != nil {
		slog.Error("Ошибка чтения targets при завершении дашборда", "error", err)
		return
	}

	stats, err := b.metricsClient.GetAllNodeStats(targets)
	if err != nil {
		slog.Error("Ошибка получения метрик при завершении дашборда", "error", err)
		return
	}

	// Рендерим с нулевым remaining — покажет «Обновлено в HH:MM»
	text := renderDashboard(stats, 0)

	_, err = b.bot.Edit(&tele.Message{
		ID:   msgID,
		Chat: &tele.Chat{ID: chatID},
	}, text, &tele.SendOptions{
		ParseMode: tele.ModeHTML,
	})
	if err != nil {
		slog.Error("Ошибка финального обновления дашборда", "error", err)
	}
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
