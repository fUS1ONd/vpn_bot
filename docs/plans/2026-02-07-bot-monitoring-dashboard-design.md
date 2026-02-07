# Live-дашборд мониторинга нод в Telegram боте

> **For Claude:** REQUIRED SUB-SKILL: Use superpowers:executing-plans to implement this plan task-by-task.

**Goal:** Добавить в бота интерактивный Live-дашборд состояния нод с авто-обновлением, прогресс-барами и алертами админу.

**Architecture:** Пользователь нажимает кнопку "📡 Серверы" — бот отправляет сообщение с метриками и inline-кнопками. Горутина обновляет сообщение каждые 5 секунд через `EditMessageText`. Через 60 секунд мониторинг засыпает. Session Manager гарантирует одну активную сессию на пользователя. Фоновый alerter проверяет алерты раз в минуту.

**Tech Stack:** Go, telebot.v3 (inline keyboards + EditMessage), VictoriaMetrics PromQL API, sync.Map для сессий

**Зависимость:** Требует выполненного плана `2026-02-07-monitoring-infrastructure-design.md` (пакет `internal/monitoring`)

---

## Пример сообщения дашборда

**Для обычных пользователей** (без IP):

```
📡 MONITORING DASHBOARD

🇩🇪 DE-Frankfurt-1
Load: [▓▓░░░░░░░░] 18% 🟢
├ CPU:  5%
├ NET:  15% (150 Mbps)
└ LOSS: 0%

🇺🇸 US-NewYork-2
Load: [▓▓▓▓▓▓░░░░] 62% 🟡
├ CPU:  45%
├ NET:  62% (620 Mbps)
└ LOSS: 0.1 seg/s

🇳🇱 NL-Amsterdam-Main ⚫ OFFLINE

⏱ 14:45:12 (Live • 55s)
```

**Inline-кнопки:** `[🔄 Обновить]` `[⏹ Стоп]`

**Когда мониторинг засыпает:**

```
📡 MONITORING DASHBOARD

... (последние данные) ...

💤 Мониторинг приостановлен
```

**Inline-кнопка:** `[▶️ Запустить]`

---

## Task 1: Session Manager — управление активными мониторингами

**Files:**
- Create: `internal/bot/dashboard.go`

**Step 1: Реализовать Session Manager и запуск дашборда**

```go
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

	// Callback data для inline-кнопок
	cbDashRefresh = "dash_refresh"
	cbDashStop    = "dash_stop"
	cbDashStart   = "dash_start"
)

// dashboardSession — активная сессия мониторинга для одного чата
type dashboardSession struct {
	cancel context.CancelFunc
	chatID int64
	msgID  int
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

// get возвращает сессию по chatID
func (dm *dashboardManager) get(chatID int64) (*dashboardSession, bool) {
	dm.mu.Lock()
	defer dm.mu.Unlock()
	s, ok := dm.sessions[chatID]
	return s, ok
}
```

**Step 2: Добавить поля в структуру Bot**

В `internal/bot/handlers.go`, в структуру `Bot` добавить:

```go
type Bot struct {
	bot            *tele.Bot
	db             *database.DB
	remnawave      *remnawave.Client
	config         *config.Config
	userStates     map[int64]string
	metricsClient  *monitoring.MetricsClient  // клиент метрик VM
	dashboardMgr   *dashboardManager          // менеджер сессий дашборда
	sdConfigsPath  string                     // путь к sd_configs (для чтения targets)
}
```

В конструкторе `New()` инициализировать:

```go
metricsClient:  monitoring.NewMetricsClient(cfg.VictoriaMetricsURL),
dashboardMgr:   newDashboardManager(),
sdConfigsPath:  cfg.SDConfigsPath,
```

**Step 3: Commit**

```
feat: Session Manager для управления дашбордами
```

---

## Task 2: Визуализатор — рендеринг дашборда

**Files:**
- Create: `internal/bot/dashboard_render.go`

**Step 1: Реализовать рендер**

```go
package bot

import (
	"fmt"
	"math"
	"strings"
	"time"

	"github.com/fus1ond/vpn_bot/internal/monitoring"
)

// Флаги стран по ISO-кодам (наиболее частые)
var countryFlags = map[string]string{
	"DE": "🇩🇪", "US": "🇺🇸", "NL": "🇳🇱", "FI": "🇫🇮",
	"FR": "🇫🇷", "GB": "🇬🇧", "JP": "🇯🇵", "SG": "🇸🇬",
	"CA": "🇨🇦", "AU": "🇦🇺", "SE": "🇸🇪", "CH": "🇨🇭",
	"KR": "🇰🇷", "HK": "🇭🇰", "PL": "🇵🇱", "RU": "🇷🇺",
	"TR": "🇹🇷", "AE": "🇦🇪", "IN": "🇮🇳", "BR": "🇧🇷",
}

// renderLoadBar генерирует шкалу загрузки [▓▓▓░░░░░░░]
func renderLoadBar(percent float64) string {
	const barLen = 10
	filled := int(math.Round(percent / 100 * float64(barLen)))
	if filled > barLen {
		filled = barLen
	}
	if filled < 0 {
		filled = 0
	}
	return "[" + strings.Repeat("▓", filled) + strings.Repeat("░", barLen-filled) + "]"
}

// renderNodeBlock генерирует текстовый блок одной ноды
func renderNodeBlock(stats monitoring.NodeStats) string {
	flag := countryFlags[stats.Country]
	if flag == "" {
		flag = "🌐"
	}

	// Нода offline
	if !stats.IsUp {
		return fmt.Sprintf("%s <b>%s</b> ⚫ OFFLINE", flag, stats.Hostname)
	}

	var b strings.Builder

	// Заголовок: 🇩🇪 DE-Frankfurt-1
	fmt.Fprintf(&b, "%s <b>%s</b>\n", flag, stats.Hostname)

	// Load bar: [▓▓░░░░░░░░] 18% 🟢
	bar := renderLoadBar(stats.LoadIndex)
	fmt.Fprintf(&b, "<b>Load:</b> %s <b>%.0f%%</b> %s\n", bar, stats.LoadIndex, stats.StatusEmoji)

	// CPU
	cpuExtra := ""
	if stats.CpuPercent > 90 {
		cpuExtra = " 🔥"
	}
	fmt.Fprintf(&b, "├ <b>CPU:</b>  %.0f%%%s\n", stats.CpuPercent, cpuExtra)

	// NET (% от канала + значение в Mbps)
	netPercent := 0.0
	if stats.BandwidthMb > 0 {
		netPercent = (stats.NetOutMbps / float64(stats.BandwidthMb)) * 100
	}
	netExtra := ""
	if netPercent > 80 {
		netExtra = " ⚠️"
	}
	fmt.Fprintf(&b, "├ <b>NET:</b>  %.0f%% (%.0f Mbps)%s\n", netPercent, stats.NetOutMbps, netExtra)

	// LOSS (TCP retransmissions/sec)
	lossExtra := ""
	if stats.PktLoss > 0.5 {
		lossExtra = " 💀"
	}
	if stats.PktLoss < 0.01 {
		fmt.Fprintf(&b, "└ <b>LOSS:</b> 0%%%s", lossExtra)
	} else {
		fmt.Fprintf(&b, "└ <b>LOSS:</b> %.1f seg/s%s", stats.PktLoss, lossExtra)
	}

	return b.String()
}

// renderDashboard собирает полное сообщение дашборда
func renderDashboard(allStats []monitoring.NodeStats, remaining time.Duration) string {
	var b strings.Builder

	b.WriteString("📡 <b>MONITORING DASHBOARD</b>\n")

	for _, stats := range allStats {
		b.WriteString("\n")
		b.WriteString(renderNodeBlock(stats))
		b.WriteString("\n")
	}

	// Футер с временем
	now := time.Now()
	secs := int(remaining.Seconds())
	fmt.Fprintf(&b, "\n<i>⏱ %s (Live • %ds)</i>", now.Format("15:04:05"), secs)

	return b.String()
}

// renderDashboardStopped — сообщение при остановке мониторинга
func renderDashboardStopped(allStats []monitoring.NodeStats) string {
	var b strings.Builder

	b.WriteString("📡 <b>MONITORING DASHBOARD</b>\n")

	for _, stats := range allStats {
		b.WriteString("\n")
		b.WriteString(renderNodeBlock(stats))
		b.WriteString("\n")
	}

	b.WriteString("\n<i>💤 Мониторинг приостановлен</i>")

	return b.String()
}
```

**Step 2: Commit**

```
feat: визуализатор дашборда (прогресс-бары, флаги, метрики)
```

---

## Task 3: Движок обновлений — горутина live-дашборда

**Files:**
- Modify: `internal/bot/dashboard.go`

**Step 1: Реализовать движок обновлений**

Добавить в `dashboard.go`:

```go
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

	// Создаём контекст с таймаутом
	ctx, cancel := context.WithCancel(context.Background())

	session := &dashboardSession{
		cancel: cancel,
		chatID: chatID,
		msgID:  msg.ID,
	}
	b.dashboardMgr.set(chatID, session)

	// Запускаем горутину обновлений
	go b.runDashboardLoop(ctx, chatID, msg.ID)

	return nil
}

// runDashboardLoop — цикл обновления дашборда
func (b *Bot) runDashboardLoop(ctx context.Context, chatID int64, msgID int) {
	ticker := time.NewTicker(dashboardUpdateInterval)
	defer ticker.Stop()

	deadline := time.After(dashboardTTL)
	var lastText string
	var lastStats []monitoring.NodeStats

	// Первое обновление сразу
	lastStats, lastText = b.updateDashboardMessage(chatID, msgID, lastText, dashboardTTL)

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
			// Вычисляем оставшееся время (приблизительно)
			lastStats, lastText = b.updateDashboardMessage(chatID, msgID, lastText, 0)
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
	btnRefresh := markup.Data("🔄 Обновить", cbDashRefresh)
	btnStop := markup.Data("⏹ Стоп", cbDashStop)
	markup.Inline(markup.Row(btnRefresh, btnStop))

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

// sendDashboardStopped показывает "приостановлен" с кнопкой "Запустить"
func (b *Bot) sendDashboardStopped(chatID int64, msgID int, lastStats []monitoring.NodeStats) {
	text := renderDashboardStopped(lastStats)

	markup := &tele.ReplyMarkup{}
	btnStart := markup.Data("▶️ Запустить", cbDashStart)
	markup.Inline(markup.Row(btnStart))

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
```

**Step 2: Commit**

```
feat: движок live-обновлений дашборда (5сек / 60сек TTL)
```

---

## Task 4: Утилита чтения targets.json

**Files:**
- Modify: `internal/monitoring/sync.go`

**Step 1: Добавить ReadTargets в sync.go**

```go
// ReadTargets читает текущий targets.json
func ReadTargets(sdConfigsPath string) ([]Target, error) {
	targetFile := filepath.Join(sdConfigsPath, "targets.json")
	data, err := os.ReadFile(targetFile)
	if err != nil {
		return nil, fmt.Errorf("не удалось прочитать %s: %w", targetFile, err)
	}

	var targets []Target
	if err := json.Unmarshal(data, &targets); err != nil {
		return nil, fmt.Errorf("не удалось распарсить targets: %w", err)
	}

	return targets, nil
}
```

**Step 2: Commit**

```
feat: ReadTargets для чтения текущего targets.json
```

---

## Task 5: Обработчики кнопок и callback-ов дашборда

**Files:**
- Modify: `internal/bot/keyboards.go` — добавить кнопку "📡 Серверы"
- Modify: `internal/bot/handlers.go` — подключить роутинг

**Step 1: Добавить кнопку в пользовательское меню**

В `keyboards.go` добавить константу:

```go
BtnServers = "📡 Серверы"
```

Изменить `UserMenuKeyboard()`:

```go
func UserMenuKeyboard() *tele.ReplyMarkup {
	menu := &tele.ReplyMarkup{ResizeKeyboard: true}
	menu.Reply(
		menu.Row(menu.Text(BtnStatus), menu.Text(BtnConnect)),
		menu.Row(menu.Text(BtnServers), menu.Text(BtnInstructions)),
		menu.Row(menu.Text(BtnDonate)),
	)
	return menu
}
```

**Step 2: Добавить роутинг в handleTextMessage**

В `handlers.go`, в секции "Кнопки пользователя" добавить:

```go
case BtnServers:
	return b.handleDashboard(c)
```

**Step 3: Добавить обработку callback-ов дашборда**

В `handleCallback` в `handlers.go`:

```go
func (b *Bot) handleCallback(c tele.Context) error {
	callback := c.Callback()
	if callback == nil {
		return nil
	}

	data := callback.Data
	slog.Info("Callback routing", "data", data, "from", c.Sender().ID)

	switch data {
	case "\f" + cbDashRefresh:
		return b.handleDashCallbackRefresh(c)
	case "\f" + cbDashStop:
		return b.handleDashCallbackStop(c)
	case "\f" + cbDashStart:
		return b.handleDashCallbackStart(c)
	}

	return c.Respond()
}
```

**Step 4: Реализовать callback-обработчики дашборда**

Добавить в `dashboard.go`:

```go
// handleDashCallbackRefresh — кнопка "Обновить" (сбрасывает TTL)
func (b *Bot) handleDashCallbackRefresh(c tele.Context) error {
	chatID := c.Chat().ID

	// Останавливаем текущую сессию и запускаем новую с тем же сообщением
	b.dashboardMgr.stop(chatID)

	msgID := c.Message().ID
	ctx, cancel := context.WithCancel(context.Background())

	session := &dashboardSession{
		cancel: cancel,
		chatID: chatID,
		msgID:  msgID,
	}
	b.dashboardMgr.set(chatID, session)

	go b.runDashboardLoop(ctx, chatID, msgID)

	return c.Respond(&tele.CallbackResponse{Text: "🔄 Обновлено"})
}

// handleDashCallbackStop — кнопка "Стоп"
func (b *Bot) handleDashCallbackStop(c tele.Context) error {
	chatID := c.Chat().ID
	b.dashboardMgr.stop(chatID)

	// Получаем последние данные для отображения
	targets, _ := monitoring.ReadTargets(b.sdConfigsPath)
	stats, _ := b.metricsClient.GetAllNodeStats(targets)

	b.sendDashboardStopped(chatID, c.Message().ID, stats)

	return c.Respond(&tele.CallbackResponse{Text: "⏹ Остановлено"})
}

// handleDashCallbackStart — кнопка "Запустить снова"
func (b *Bot) handleDashCallbackStart(c tele.Context) error {
	chatID := c.Chat().ID
	b.dashboardMgr.stop(chatID)

	msgID := c.Message().ID
	ctx, cancel := context.WithCancel(context.Background())

	session := &dashboardSession{
		cancel: cancel,
		chatID: chatID,
		msgID:  msgID,
	}
	b.dashboardMgr.set(chatID, session)

	go b.runDashboardLoop(ctx, chatID, msgID)

	return c.Respond(&tele.CallbackResponse{Text: "▶️ Запущено"})
}
```

**Step 5: Commit**

```
feat: кнопка "Серверы" + inline-кнопки дашборда (обновить/стоп/запустить)
```

---

## Task 6: Алерты админу — нода упала или перегружена

**Files:**
- Create: `internal/monitoring/alerter.go`

**Step 1: Реализовать фоновый alerter**

```go
package monitoring

import (
	"context"
	"fmt"
	"log/slog"
	"strings"
	"time"
)

const alertCheckInterval = 60 * time.Second

// AlertSender — интерфейс отправки сообщений (реализуется ботом)
type AlertSender interface {
	SendAlert(text string) error
}

// AlertState — состояние алертов (чтобы не спамить)
type AlertState struct {
	// Ноды, по которым уже был отправлен алерт (hostname -> тип алерта)
	fired map[string]string
}

// StartAlerter запускает фоновую проверку алертов
func StartAlerter(ctx context.Context, mc *MetricsClient, sdConfigsPath string, sender AlertSender) {
	slog.Info("Запуск фонового alerter", "interval", alertCheckInterval)

	state := &AlertState{fired: make(map[string]string)}

	ticker := time.NewTicker(alertCheckInterval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			slog.Info("Alerter остановлен")
			return
		case <-ticker.C:
			checkAlerts(mc, sdConfigsPath, sender, state)
		}
	}
}

func checkAlerts(mc *MetricsClient, sdConfigsPath string, sender AlertSender, state *AlertState) {
	targets, err := ReadTargets(sdConfigsPath)
	if err != nil {
		slog.Error("Alerter: ошибка чтения targets", "error", err)
		return
	}

	if len(targets) == 0 {
		return
	}

	stats, err := mc.GetAllNodeStats(targets)
	if err != nil {
		slog.Error("Alerter: ошибка получения метрик", "error", err)
		return
	}

	// Проверяем каждую ноду
	activeAlerts := make(map[string]string)
	for _, node := range stats {
		alertType := ""

		if !node.IsUp {
			alertType = "down"
		} else if node.LoadIndex >= 80 {
			alertType = "overload"
		}

		if alertType == "" {
			continue
		}

		activeAlerts[node.Hostname] = alertType

		// Если алерт уже был отправлен с тем же типом — пропускаем
		if prev, ok := state.fired[node.Hostname]; ok && prev == alertType {
			continue
		}

		// Отправляем новый алерт
		msg := formatAlert(node, alertType)
		if err := sender.SendAlert(msg); err != nil {
			slog.Error("Alerter: ошибка отправки алерта", "error", err, "hostname", node.Hostname)
		}
	}

	// Отправляем "recovered" для нод, которые вернулись в норму
	for hostname, prevType := range state.fired {
		if _, stillActive := activeAlerts[hostname]; !stillActive {
			msg := fmt.Sprintf("✅ <b>%s</b> — восстановлена (было: %s)", hostname, alertTypeText(prevType))
			if err := sender.SendAlert(msg); err != nil {
				slog.Error("Alerter: ошибка отправки recovery", "error", err)
			}
		}
	}

	// Обновляем состояние
	state.fired = activeAlerts
}

func formatAlert(node NodeStats, alertType string) string {
	var b strings.Builder

	switch alertType {
	case "down":
		fmt.Fprintf(&b, "🚨 <b>ALERT: %s — OFFLINE</b>\n\n", node.Hostname)
		fmt.Fprintf(&b, "Node Exporter не отвечает.\n")
		fmt.Fprintf(&b, "Проверьте доступность сервера.")
	case "overload":
		fmt.Fprintf(&b, "⚠️ <b>ALERT: %s — перегрузка</b>\n\n", node.Hostname)
		fmt.Fprintf(&b, "<b>Load Index:</b> %.0f%%\n", node.LoadIndex)
		fmt.Fprintf(&b, "<b>CPU:</b> %.0f%%\n", node.CpuPercent)
		fmt.Fprintf(&b, "<b>NET:</b> %.0f Mbps\n", node.NetOutMbps)
	}

	fmt.Fprintf(&b, "\n<i>%s</i>", time.Now().Format("02.01.06 15:04:05"))
	return b.String()
}

func alertTypeText(t string) string {
	switch t {
	case "down":
		return "OFFLINE"
	case "overload":
		return "перегрузка"
	default:
		return t
	}
}
```

**Step 2: Реализовать AlertSender в боте**

В `internal/bot/dashboard.go` добавить:

```go
// BotAlertSender — реализация AlertSender через Telegram бота
type BotAlertSender struct {
	bot     *tele.Bot
	adminID int64
}

// SendAlert отправляет алерт админу
func (s *BotAlertSender) SendAlert(text string) error {
	admin := &tele.User{ID: s.adminID}
	_, err := s.bot.Send(admin, text, &tele.SendOptions{
		ParseMode: tele.ModeHTML,
	})
	return err
}
```

**Step 3: Запустить alerter в main.go**

В `cmd/bot/main.go` добавить после запуска SyncLoop:

```go
// Запуск алертера
alertSender := bot.NewBotAlertSender(telegramBot)
go monitoring.StartAlerter(ctx, metricsClient, cfg.SDConfigsPath, alertSender)
```

Для этого нужно экспортировать из бота нужные поля. Добавить в `internal/bot/handlers.go`:

```go
// NewBotAlertSender создаёт AlertSender для отправки алертов админу
func NewBotAlertSender(b *Bot) *BotAlertSender {
	return &BotAlertSender{
		bot:     b.bot,
		adminID: b.config.AdminID,
	}
}

// MetricsClient возвращает клиент метрик бота
func (b *Bot) MetricsClient() *monitoring.MetricsClient {
	return b.metricsClient
}
```

**Step 4: Commit**

```
feat: алерты админу при недоступности ноды или Load > 80%
```

---

## Task 7: Обновить .env.example и конфиг-документацию

**Files:**
- Modify: `CLAUDE.md` — добавить документацию по мониторингу

**Step 1: Добавить в CLAUDE.md секцию мониторинга**

Дополнить раздел "Переменные окружения":

```env
# Мониторинг (опционально, включается автоматически если VM доступна)
SD_CONFIGS_PATH=/app/sd_configs
VICTORIA_METRICS_URL=http://victoriametrics:8428
```

Добавить новый раздел:

```markdown
## Мониторинг нод

### Архитектура
- **VictoriaMetrics** — база метрик (порт 8428)
- **vmagent** — скрейпит Node Exporter на нодах
- **Бот** — генерирует `targets.json`, читает метрики через PromQL

### Конвенция тегов
На нодах в Remnawave задаётся тег `bw:<число>` для указания bandwidth в Mbps.
Пример: `bw:1000` = 1 Gbit. Дефолт: 1000 Mbps.

### Алерты
Бот отправляет админу алерты при:
- Нода OFFLINE (Node Exporter не отвечает)
- Load Index > 80% (перегрузка)

### Установка Node Exporter на ноду
```bash
bash scripts/install-node-exporter.sh <IP_СЕРВЕРА_БОТА>
```
```

**Step 2: Commit**

```
docs: документация мониторинга нод
```

---

## Итог

После выполнения всех задач из обоих планов:

1. **Инфраструктура** — VictoriaMetrics + vmagent в docker-compose, Node Exporter на нодах
2. **Авто-обнаружение** — бот генерирует targets.json из Remnawave API каждые 60 сек
3. **Live-дашборд** — кнопка "📡 Серверы" для всех пользователей, обновление каждые 5 сек
4. **Визуализация** — прогресс-бары, флаги стран, Load Index, метрики CPU/NET/LOSS
5. **Интерактивность** — inline-кнопки (обновить/стоп/запустить), авто-стоп через 60 сек
6. **Алерты** — проактивные уведомления админу при проблемах
7. **Безопасность** — IP нод скрыты от пользователей, показываются только имена

### Порядок реализации

1. Сначала выполнить план `2026-02-07-monitoring-infrastructure-design.md` (Tasks 1-8)
2. Затем этот план (Tasks 1-7)
