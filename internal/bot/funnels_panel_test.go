package bot

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
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
	calls *atomic.Int32 // сколько раз панель опрошена; nil — не считаем
}

func (p fakePanelUsers) GetAllUsers() ([]remnawave.User, error) {
	if p.calls != nil {
		p.calls.Add(1)
	}
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

	connected, err := port.FirstConnectedAt(context.Background())
	require.NoError(t, err)
	assert.Equal(t, map[int64]time.Time{1: early}, connected)
}

// Ошибка панели доходит до модуля воронок: там она станет «нет данных».
func TestPanelFirstConnections_PanelError(t *testing.T) {
	port := NewPanelFirstConnections(fakePanelUsers{err: errors.New("panel is down")})

	_, err := port.FirstConnectedAt(context.Background())
	assert.Error(t, err)
}

// Зависшая панель не держит расчёт дольше своего потолка адаптера.
func TestPanelFirstConnections_Timeout(t *testing.T) {
	port := &panelFirstConnections{panel: fakePanelUsers{delay: time.Second}, timeout: 20 * time.Millisecond}

	started := time.Now()
	_, err := port.FirstConnectedAt(context.Background())
	assert.Error(t, err)
	assert.Less(t, time.Since(started), 500*time.Millisecond)
}

// Расчёты, пришедшие, пока опрос панели идёт, ждут его же, а не запускают
// свой: иначе при зависшей панели каждое нажатие оставляло бы в фоне ещё
// один полный опрос.
func TestPanelFirstConnections_SharesInflightRequest(t *testing.T) {
	var calls atomic.Int32
	one, at := int64(1), time.Date(2026, 9, 1, 10, 0, 0, 0, time.UTC)
	port := &panelFirstConnections{
		panel:   fakePanelUsers{users: []remnawave.User{panelUser(&one, &at)}, delay: 100 * time.Millisecond, calls: &calls},
		timeout: time.Second,
	}

	var wg sync.WaitGroup
	for range 3 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			connected, err := port.FirstConnectedAt(context.Background())
			assert.NoError(t, err)
			assert.Equal(t, map[int64]time.Time{1: at}, connected)
		}()
	}
	wg.Wait()
	assert.EqualValues(t, 1, calls.Load())

	// Опрос закончился — следующий расчёт спрашивает панель заново.
	_, err := port.FirstConnectedAt(context.Background())
	require.NoError(t, err)
	assert.EqualValues(t, 2, calls.Load())
}
