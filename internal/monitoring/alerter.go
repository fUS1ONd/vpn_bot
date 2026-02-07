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

	// Отправляем «recovered» для нод, которые вернулись в норму
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
