package funnels

import (
	"context"
	"database/sql"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/fus1ond/vpn_bot/internal/database"
	"github.com/fus1ond/vpn_bot/internal/journal"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// Модель расчёта общая для всех Воронок, а Воронки пока из одного Шага, поэтому
// цепочку Шагов проверяем на Воронке-заготовке из тех же источников.

func failingSource(context.Context, *sql.Conn, time.Time, time.Time) ([]occurrence, error) {
	return nil, errors.New("source is down")
}

func withFunnel(t *testing.T, f fixture, fn funnel) *Funnels {
	t.Helper()
	funnels := f.open(t)
	funnels.funnels = []funnel{fn}
	return funnels
}

// Каждый следующий Шаг — подмножество предыдущего, не раньше него и в пределах
// окна от входа; конверсии — к предыдущему и к первому Шагу.
func TestCompute_ChainKeepsOrderAndWindow(t *testing.T) {
	now := time.Date(2026, 9, 28, 12, 0, 0, 0, time.UTC)
	from := now.Add(-7 * 24 * time.Hour)
	entry := now.Add(-5 * 24 * time.Hour)

	f := newFixture(t)
	f.record(t,
		// 1, 2, 3, 4 вошли
		journal.Event{At: entry, TelegramID: 1, Action: "a"},
		journal.Event{At: entry, TelegramID: 2, Action: "a"},
		journal.Event{At: entry, TelegramID: 3, Action: "a"},
		journal.Event{At: entry, TelegramID: 4, Action: "a"},
		// 1 сделал второй Шаг в окне, потом третий
		journal.Event{At: entry.Add(time.Hour), TelegramID: 1, Action: "b"},
		journal.Event{At: entry.Add(2 * time.Hour), TelegramID: 1, Action: "c"},
		// 2 сделал второй Шаг раньше входа — не считается
		journal.Event{At: entry.Add(-time.Hour), TelegramID: 2, Action: "b"},
		// 3 сделал второй Шаг за пределами окна
		journal.Event{At: entry.Add(3 * 24 * time.Hour), TelegramID: 3, Action: "b"},
		// 4 сделал второй Шаг в окне, а третий — раньше второго
		journal.Event{At: entry.Add(2 * time.Hour), TelegramID: 4, Action: "b"},
		journal.Event{At: entry.Add(time.Hour), TelegramID: 4, Action: "c"},
		// 5 сделал второй Шаг, не войдя
		journal.Event{At: entry, TelegramID: 5, Action: "b"},
	)

	funnels := withFunnel(t, f, funnel{id: "test", window: 2 * 24 * time.Hour, steps: []step{
		{id: "a", source: journalAction("a")},
		{id: "b", source: journalAction("b")},
		{id: "c", source: journalAction("c")},
	}})

	report, err := funnels.Report(context.Background(), "test", from, now)
	require.NoError(t, err)

	require.Len(t, report.Steps, 3)
	assert.Equal(t, StepReport{ID: "a", People: 4, FromPrevious: 1, FromFirst: 1}, report.Steps[0])
	assert.Equal(t, StepReport{ID: "b", People: 2, FromPrevious: 0.5, FromFirst: 0.5}, report.Steps[1])
	assert.Equal(t, StepReport{ID: "c", People: 1, FromPrevious: 0.5, FromFirst: 0.25}, report.Steps[2])
}

// Недоступный источник даёт «нет данных» на своём Шаге и на всех следующих,
// а не ноль; Шаги до него посчитаны.
func TestCompute_FailedSourceIsNoData(t *testing.T) {
	now := time.Date(2026, 9, 28, 12, 0, 0, 0, time.UTC)
	f := newFixture(t)
	f.record(t, journal.Event{At: now.Add(-time.Hour), TelegramID: 1, Action: "a"})

	funnels := withFunnel(t, f, funnel{id: "test", window: time.Hour, steps: []step{
		{id: "a", source: journalAction("a")},
		{id: "b", source: failingSource},
		{id: "c", source: journalAction("c")},
	}})

	report, err := funnels.Report(context.Background(), "test", now.Add(-24*time.Hour), now)
	require.NoError(t, err)

	assert.Equal(t, 1, report.Steps[0].People)
	assert.False(t, report.Steps[0].NoData)
	assert.True(t, report.Steps[1].NoData)
	assert.True(t, report.Steps[2].NoData)
}

// Первичная причина «нет данных» — только у Шага, чей источник упал; Шаги
// после него помечены каскадом: подсказку о причине показывать у них нельзя.
func TestCompute_NoDataAfterFailedSourceIsCascaded(t *testing.T) {
	now := time.Date(2026, 9, 28, 12, 0, 0, 0, time.UTC)
	f := newFixture(t)
	f.record(t, journal.Event{At: now.Add(-time.Hour), TelegramID: 1, Action: "a"})

	funnels := withFunnel(t, f, funnel{id: "test", window: time.Hour, steps: []step{
		{id: "a", source: journalAction("a")},
		{id: "b", source: failingSource},
		{id: "c", source: journalAction("c")},
	}})

	report, err := funnels.Report(context.Background(), "test", now.Add(-24*time.Hour), now)
	require.NoError(t, err)

	assert.Equal(t, StepReport{ID: "b", NoData: true}, report.Steps[1])
	assert.Equal(t, StepReport{ID: "c", NoData: true, Cascaded: true}, report.Steps[2])
}

// Основная база подключена только на чтение: воронки не могут её изменить.
func TestAttachMain_IsReadOnly(t *testing.T) {
	f := newFixture(t)
	f.record(t)
	funnels := f.open(t)

	conn, err := funnels.events.Conn(context.Background())
	require.NoError(t, err)
	defer conn.Close()

	detach := funnels.attachMain(context.Background(), conn)
	defer detach()

	var users int
	require.NoError(t, conn.QueryRowContext(context.Background(),
		"SELECT COUNT(*) FROM "+mainSchema+".users").Scan(&users))

	_, err = conn.ExecContext(context.Background(),
		"INSERT INTO "+mainSchema+".users (telegram_id) VALUES (1)")
	assert.Error(t, err, "основная база должна быть подключена только на чтение")
}

// Неизвестная Воронка — ошибка, а не пустой отчёт.
func TestReport_UnknownFunnel(t *testing.T) {
	f := newFixture(t)
	_, err := f.open(t).Report(context.Background(), "nope", time.Now(), time.Now())
	assert.ErrorIs(t, err, ErrUnknownFunnel)
}

// Путь основной базы со служебными для URI символами подключает именно её.
func TestAttachMain_PathWithURISymbols(t *testing.T) {
	// «?» и «%» основная база не переносит сама (драйвер режет DSN по «?»),
	// поэтому проверяем то, что до ATTACH вообще доходит.
	dir := filepath.Join(t.TempDir(), "data #1")
	require.NoError(t, os.MkdirAll(dir, 0o755))
	f := fixture{eventsPath: filepath.Join(dir, journal.FileName), mainPath: filepath.Join(dir, "bot.db")}
	db, err := database.New(f.mainPath)
	require.NoError(t, err)
	require.NoError(t, db.Close())
	f.record(t)
	funnels := f.open(t)

	conn, err := funnels.events.Conn(context.Background())
	require.NoError(t, err)
	defer conn.Close()
	defer funnels.attachMain(context.Background(), conn)()

	var users int
	require.NoError(t, conn.QueryRowContext(context.Background(),
		"SELECT COUNT(*) FROM "+mainSchema+".users").Scan(&users))
}
