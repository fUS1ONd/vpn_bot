package bot

import (
	"context"
	"fmt"
	"sync"
	"time"

	"github.com/fus1ond/vpn_bot/internal/funnels"
	"github.com/fus1ond/vpn_bot/internal/remnawave"
)

// panelFirstConnectionsTimeout — потолок одного опроса панели для воронок.
// Свой, короче потолка отчёта в админке: панель не ответила — Шаг устройства
// «нет данных», а остальные Шаги успевают посчитаться и показаться.
const panelFirstConnectionsTimeout = 15 * time.Second

// panelUsersLister — получение всех пользователей панели, над которым построен
// адаптер (*remnawave.Client в проде).
type panelUsersLister interface {
	GetAllUsers() ([]remnawave.User, error)
}

// panelFirstConnections — адаптер порта воронок над списком всех
// пользователей панели.
type panelFirstConnections struct {
	panel   panelUsersLister
	timeout time.Duration

	mu       sync.Mutex
	inflight *panelUsersCall // опрос панели, который ещё идёт
}

// panelUsersCall — один опрос панели, результат которого ждут все расчёты,
// пришедшие, пока он идёт. done закрывается, когда users и err заполнены.
type panelUsersCall struct {
	done  chan struct{}
	users []remnawave.User
	err   error
}

// NewPanelFirstConnections — порт «момент первого подключения по Telegram ID»
// для модуля воронок поверх клиента панели.
func NewPanelFirstConnections(panel panelUsersLister) funnels.FirstConnections {
	return &panelFirstConnections{panel: panel, timeout: panelFirstConnectionsTimeout}
}

// FirstConnectedAt отдаёт самый ранний момент первого подключения на Telegram
// ID: у человека может быть несколько пользователей панели. Не подключавшиеся
// и пользователи панели без Telegram ID в ответ не попадают.
func (p *panelFirstConnections) FirstConnectedAt(ctx context.Context) (map[int64]time.Time, error) {
	call := p.startCall()

	timer := time.NewTimer(p.timeout)
	defer timer.Stop()

	select {
	case <-call.done:
	case <-timer.C:
		return nil, fmt.Errorf("panel users request timed out after %s", p.timeout)
	case <-ctx.Done():
		return nil, ctx.Err()
	}
	if call.err != nil {
		return nil, fmt.Errorf("failed to get panel users: %w", call.err)
	}

	connected := make(map[int64]time.Time)
	for _, u := range call.users {
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

// startCall присоединяется к идущему опросу панели или начинает новый. Клиент
// панели не принимает context, поэтому потолок держится снаружи, а опрос
// дорабатывает в фоне до таймаута HTTP-клиента. Без присоединения каждое
// нажатие периода при зависшей панели оставляло бы в фоне ещё один полный
// постраничный опрос.
func (p *panelFirstConnections) startCall() *panelUsersCall {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.inflight != nil {
		return p.inflight
	}

	call := &panelUsersCall{done: make(chan struct{})}
	p.inflight = call
	go func() {
		call.users, call.err = p.panel.GetAllUsers()
		p.mu.Lock()
		p.inflight = nil
		p.mu.Unlock()
		close(call.done)
	}()
	return call
}
