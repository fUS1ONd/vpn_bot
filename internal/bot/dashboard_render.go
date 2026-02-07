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
	if stats.PktLoss < 0.01 {
		fmt.Fprintf(&b, "└ <b>LOSS:</b> 0%%")
	} else {
		fmt.Fprintf(&b, "└ <b>LOSS:</b> %.1f seg/s", stats.PktLoss)
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
	if remaining <= 0 {
		// TTL истёк — показываем время последнего обновления
		fmt.Fprintf(&b, "\n<i>⏱ Обновлено в %s</i>", now.Format("15:04"))
	} else {
		secs := int(remaining.Seconds())
		fmt.Fprintf(&b, "\n<i>⏱ %s (Live • %ds)</i>", now.Format("15:04:05"), secs)
	}

	return b.String()
}
