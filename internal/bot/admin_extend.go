package bot

import (
	"time"

	"github.com/fus1ond/vpn_bot/internal/remnawave"
)

// nextMonthExpireAt считает новую дату окончания подписки при продлении на месяц.
// Если подписка активна и не истекла — плюсуем к текущему expireAt (не теряем остаток).
// Иначе (триал истёк, grace period, disabled) — считаем от now.
func nextMonthExpireAt(remUser *remnawave.User, now time.Time) time.Time {
	if remUser.ExpireAt.After(now) && remUser.Status == remnawave.StatusActive {
		return remUser.ExpireAt.AddDate(0, 1, 0)
	}
	return now.AddDate(0, 1, 0)
}
