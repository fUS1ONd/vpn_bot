package monitoring

import "math"

// CalculateLoadIndex вычисляет индекс нагрузки ноды.
// Формула: 0.3×CPU% + 0.7×NET% + мягкий штраф за потери пакетов.
func CalculateLoadIndex(stats NodeStats) NodeStats {
	// Процент загрузки сети (исходящий трафик / лимит канала)
	netLoadPercent := 0.0
	if stats.BandwidthMb > 0 {
		netLoadPercent = (stats.NetOutMbps / float64(stats.BandwidthMb)) * 100
	}

	// Взвешенная нагрузка: сеть важнее CPU для VPN-ноды
	baseLoad := 0.3*stats.CpuPercent + 0.7*netLoadPercent

	// Мягкий штраф за потери пакетов (TCP retransmissions/sec)
	penalty := 0.0
	if stats.PktLoss > 0.5 {
		penalty += 5
	}
	if stats.PktLoss > 2.0 {
		penalty += 10
	}

	stats.LoadIndex = math.Min(baseLoad+penalty, 100)

	// Определяем эмодзи статуса
	switch {
	case !stats.IsUp:
		stats.StatusEmoji = "⚫"
	case stats.LoadIndex >= 80:
		stats.StatusEmoji = "🔴"
	case stats.LoadIndex >= 50:
		stats.StatusEmoji = "🟡"
	default:
		stats.StatusEmoji = "🟢"
	}

	return stats
}
