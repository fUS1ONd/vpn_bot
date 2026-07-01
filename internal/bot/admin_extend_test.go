package bot

import (
	"testing"
	"time"

	"github.com/fus1ond/vpn_bot/internal/remnawave"
)

func TestNextMonthExpireAt(t *testing.T) {
	now := time.Date(2026, 3, 10, 12, 0, 0, 0, time.UTC)

	tests := []struct {
		name   string
		status string
		expire time.Time
		want   time.Time
	}{
		{
			name:   "активная подписка в будущем — плюсуем к expireAt",
			status: remnawave.StatusActive,
			expire: time.Date(2026, 3, 20, 12, 0, 0, 0, time.UTC),
			want:   time.Date(2026, 4, 20, 12, 0, 0, 0, time.UTC),
		},
		{
			name:   "истёкшая (не ACTIVE) — считаем от now",
			status: remnawave.StatusExpired,
			expire: time.Date(2026, 3, 1, 12, 0, 0, 0, time.UTC),
			want:   time.Date(2026, 4, 10, 12, 0, 0, 0, time.UTC),
		},
		{
			name:   "disabled grace — считаем от now",
			status: remnawave.StatusDisabled,
			expire: time.Date(2026, 3, 5, 12, 0, 0, 0, time.UTC),
			want:   time.Date(2026, 4, 10, 12, 0, 0, 0, time.UTC),
		},
		{
			name:   "ACTIVE но дата в прошлом — считаем от now",
			status: remnawave.StatusActive,
			expire: time.Date(2026, 3, 1, 12, 0, 0, 0, time.UTC),
			want:   time.Date(2026, 4, 10, 12, 0, 0, 0, time.UTC),
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			remUser := &remnawave.User{Status: tt.status, ExpireAt: tt.expire}
			got := nextMonthExpireAt(remUser, now)
			if !got.Equal(tt.want) {
				t.Errorf("nextMonthExpireAt() = %v, want %v", got, tt.want)
			}
		})
	}
}
