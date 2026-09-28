package bot

import (
	"context"
	"fmt"
	"time"

	"github.com/fus1ond/vpn_bot/internal/funnels"
	"github.com/fus1ond/vpn_bot/internal/remnawave"
)

// panelFirstConnectionsTimeout — потолок одного опроса панели для воронок.
// Свой, короче потолка отчёта в админке: панель не ответила — Шаг устройства
// «нет данных», а остальные Шаги успевают посчитаться и показаться.
const panelFirstConnectionsTimeout = 15 * time.Second

// allPanelUsers — получение всех пользователей панели, над которым построен
// адаптер (*remnawave.Client в проде).
type allPanelUsers interface {
	GetAllUsers() ([]remnawave.User, error)
}

// panelFirstConnections — адаптер порта воронок над списком всех
// пользователей панели.
type panelFirstConnections struct {
	panel   allPanelUsers
	timeout time.Duration
}

// NewPanelFirstConnections — порт «момент первого подключения по Telegram ID»
// для модуля воронок поверх клиента панели.
func NewPanelFirstConnections(panel allPanelUsers) funnels.FirstConnections {
	return &panelFirstConnections{panel: panel, timeout: panelFirstConnectionsTimeout}
}

type panelUsersResult struct {
	users []remnawave.User
	err   error
}

// FirstConnections отдаёт самый ранний момент первого подключения на Telegram
// ID: у человека может быть несколько пользователей панели. Не подключавшиеся
// и пользователи панели без Telegram ID в ответ не попадают.
func (p *panelFirstConnections) FirstConnections(ctx context.Context) (map[int64]time.Time, error) {
	// Клиент панели не принимает context, поэтому потолок держится снаружи:
	// запрос дорабатывает в фоне до таймаута HTTP-клиента, а расчёт его не ждёт.
	// Канал буферизован, чтобы горутина завершилась и без читателя.
	done := make(chan panelUsersResult, 1)
	go func() {
		users, err := p.panel.GetAllUsers()
		done <- panelUsersResult{users: users, err: err}
	}()

	timer := time.NewTimer(p.timeout)
	defer timer.Stop()

	var result panelUsersResult
	select {
	case result = <-done:
	case <-timer.C:
		return nil, fmt.Errorf("panel users request timed out after %s", p.timeout)
	case <-ctx.Done():
		return nil, ctx.Err()
	}
	if result.err != nil {
		return nil, fmt.Errorf("failed to get panel users: %w", result.err)
	}

	connected := make(map[int64]time.Time)
	for _, u := range result.users {
		if u.TelegramID == nil || u.UserTraffic == nil || u.UserTraffic.FirstConnectedAt == nil {
			continue
		}
		at := u.UserTraffic.FirstConnectedAt.UTC()
		if earliest, ok := connected[*u.TelegramID]; !ok || at.Before(earliest) {
			connected[*u.TelegramID] = at
		}
	}
	return connected, nil
}
