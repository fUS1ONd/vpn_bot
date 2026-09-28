package bot

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/fus1ond/vpn_bot/internal/remnawave"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// fakePanelUsers — список пользователей панели с задержкой или ошибкой.
type fakePanelUsers struct {
	users []remnawave.User
	err   error
	delay time.Duration
}

func (p fakePanelUsers) GetAllUsers() ([]remnawave.User, error) {
	time.Sleep(p.delay)
	return p.users, p.err
}

func panelUser(telegramID *int64, firstConnected *time.Time) remnawave.User {
	return remnawave.User{TelegramID: telegramID, UserTraffic: &remnawave.Traffic{FirstConnectedAt: firstConnected}}
}

// Адаптер отдаёт самое раннее первое подключение на Telegram ID и пропускает
// не подключавшихся и пользователей панели без Telegram ID.
func TestPanelFirstConnections_EarliestPerTelegramID(t *testing.T) {
	early := time.Date(2026, 9, 1, 10, 0, 0, 0, time.UTC)
	late := early.Add(48 * time.Hour)
	one, two, three := int64(1), int64(2), int64(3)

	port := NewPanelFirstConnections(fakePanelUsers{users: []remnawave.User{
		panelUser(&one, &late),
		panelUser(&one, &early),
		panelUser(&two, nil),
		panelUser(nil, &early),
		{TelegramID: &three},
	}})

	connected, err := port.FirstConnections(context.Background())
	require.NoError(t, err)
	assert.Equal(t, map[int64]time.Time{1: early}, connected)
}

// Ошибка панели доходит до модуля воронок: там она станет «нет данных».
func TestPanelFirstConnections_PanelError(t *testing.T) {
	port := NewPanelFirstConnections(fakePanelUsers{err: errors.New("panel is down")})

	_, err := port.FirstConnections(context.Background())
	assert.Error(t, err)
}

// Зависшая панель не держит расчёт дольше своего потолка адаптера.
func TestPanelFirstConnections_Timeout(t *testing.T) {
	port := &panelFirstConnections{panel: fakePanelUsers{delay: time.Second}, timeout: 20 * time.Millisecond}

	started := time.Now()
	_, err := port.FirstConnections(context.Background())
	assert.Error(t, err)
	assert.Less(t, time.Since(started), 500*time.Millisecond)
}
