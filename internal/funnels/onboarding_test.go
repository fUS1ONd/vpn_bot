package funnels

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// fakePanel — фейковый порт панели: заранее заданные моменты первого
// подключения или ошибка, как у недоступной панели.
type fakePanel struct {
	connected map[int64]time.Time
	err       error
}

func (p fakePanel) FirstConnections(context.Context) (map[int64]time.Time, error) {
	return p.connected, p.err
}

// user кладёт пользователя бота; created_at пишется так же, как у бота, —
// CURRENT_TIMESTAMP целыми секундами.
func (f fixture) user(t *testing.T, telegramID int64, createdAt time.Time) {
	t.Helper()
	f.exec(t, `INSERT INTO users (telegram_id, username, first_name, legacy_paid_migrated, created_at) VALUES (?, '', '', 0, ?)`,
		telegramID, sqliteNow(createdAt))
}

func (f fixture) openWithPanel(t *testing.T, panel FirstConnections) *Funnels {
	t.Helper()
	funnels, err := New(f.eventsPath, f.mainPath, []int64{ownerID}, panel)
	require.NoError(t, err)
	t.Cleanup(func() { funnels.Close() })
	return funnels
}

// Вход — регистрация в боте за период; «подключил устройство» — первое
// подключение по данным панели не раньше регистрации и в пределах 14 дней;
// «оплатил» — ручная оплата после подключения в том же окне.
func TestOnboardingFunnel_RegisteredConnectedPaid(t *testing.T) {
	now := time.Date(2026, 9, 28, 12, 0, 0, 0, time.UTC)
	registered := now.Add(-20 * 24 * time.Hour)
	from := now.Add(-30 * 24 * time.Hour)

	f := newFixture(t)
	for _, id := range []int64{1, 2, 3, 4, 5, ownerID} {
		f.user(t, id, registered)
	}
	// зарегистрировался до периода — не в когорте
	f.user(t, 6, from.Add(-time.Hour))

	panel := fakePanel{connected: map[int64]time.Time{
		1:       registered.Add(time.Hour),
		2:       registered.Add(2 * time.Hour),
		3:       registered.Add(15 * 24 * time.Hour), // за окном 14 дней
		5:       registered.Add(-time.Hour),          // раньше регистрации
		6:       from.Add(time.Hour),
		ownerID: registered.Add(time.Hour),
		// 4 не подключался вовсе
	}}
	f.payment(t, 1, "confirmed", false, registered.Add(3*time.Hour))
	f.payment(t, 2, "confirmed", true, registered.Add(3*time.Hour)) // тест владельца
	f.payment(t, ownerID, "confirmed", false, registered.Add(3*time.Hour))

	report, err := f.openWithPanel(t, panel).Report(context.Background(), FunnelOnboarding, from, now)
	require.NoError(t, err)

	require.Len(t, report.Steps, 3)
	assert.Equal(t, StepReport{ID: StepRegistered, People: 5, FromPrevious: 1, FromFirst: 1}, report.Steps[0])
	assert.Equal(t, StepReport{ID: StepDeviceConnected, People: 2, FromPrevious: 0.4, FromFirst: 0.4}, report.Steps[1])
	assert.Equal(t, StepReport{ID: StepPaymentConfirmed, People: 1, FromPrevious: 0.5, FromFirst: 0.2}, report.Steps[2])
	assert.False(t, report.WindowOpen)
}

// Панель недоступна — «нет данных» только у Шага устройства; «оплатил»
// считается от регистрации: сбой панели не прячет всю Воронку.
func TestOnboardingFunnel_PanelDownIsNoDataOnlyOnDeviceStep(t *testing.T) {
	now := time.Date(2026, 9, 28, 12, 0, 0, 0, time.UTC)
	registered := now.Add(-20 * 24 * time.Hour)

	f := newFixture(t)
	f.user(t, 1, registered)
	f.user(t, 2, registered)
	f.payment(t, 1, "confirmed", false, registered.Add(time.Hour))
	// оплатил раньше регистрации (время у записей разошлось) — не шаг когорты
	f.payment(t, 2, "confirmed", false, registered.Add(-time.Hour))

	panel := fakePanel{err: errors.New("panel is down")}
	report, err := f.openWithPanel(t, panel).Report(context.Background(), FunnelOnboarding, now.Add(-30*24*time.Hour), now)
	require.NoError(t, err)

	require.Len(t, report.Steps, 3)
	assert.Equal(t, 2, report.Steps[0].People)
	assert.Equal(t, StepReport{ID: StepDeviceConnected, NoData: true}, report.Steps[1])
	assert.False(t, report.Steps[2].NoData)
	assert.Equal(t, 1, report.Steps[2].People)
	assert.InDelta(t, 0.5, report.Steps[2].FromFirst, 1e-9)
	assert.Zero(t, report.Steps[2].FromPrevious, "процент от неизвестного не показываем")
}

// Панель не подключена к модулю вовсе — то же «нет данных» у Шага устройства.
func TestOnboardingFunnel_NoPanelIsNoDataOnDeviceStep(t *testing.T) {
	now := time.Date(2026, 9, 28, 12, 0, 0, 0, time.UTC)
	f := newFixture(t)
	f.user(t, 1, now.Add(-time.Hour))

	report, err := f.openWithPanel(t, nil).Report(context.Background(), FunnelOnboarding, now.Add(-7*24*time.Hour), now)
	require.NoError(t, err)

	assert.Equal(t, 1, report.Steps[0].People)
	assert.True(t, report.Steps[1].NoData)
	assert.False(t, report.Steps[2].NoData)
}
