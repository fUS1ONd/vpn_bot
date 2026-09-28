package funnels

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	"github.com/fus1ond/vpn_bot/internal/database"
	"github.com/fus1ond/vpn_bot/internal/journal"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const ownerID int64 = 999

// fixture — настоящие SQLite-файлы во временной папке: журнал и основная база.
type fixture struct {
	eventsPath string
	mainPath   string
}

func newFixture(t *testing.T) fixture {
	t.Helper()
	dir := t.TempDir()
	f := fixture{
		eventsPath: filepath.Join(dir, journal.FileName),
		mainPath:   filepath.Join(dir, "bot.db"),
	}
	db, err := database.New(f.mainPath)
	require.NoError(t, err)
	require.NoError(t, db.Close())
	return f
}

// record пишет События через настоящий журнал: фикстура лежит в файле ровно так,
// как её положил бы бот.
func (f fixture) record(t *testing.T, events ...journal.Event) {
	t.Helper()
	j, err := journal.Open(f.eventsPath, journal.Options{})
	require.NoError(t, err)
	for _, event := range events {
		if event.Source == "" {
			event.Source = journal.SourceUser
		}
		j.Record(event)
	}
	j.Close()
}

func (f fixture) open(t *testing.T) *Funnels {
	t.Helper()
	funnels, err := New(f.eventsPath, f.mainPath, []int64{ownerID})
	require.NoError(t, err)
	t.Cleanup(func() { funnels.Close() })
	return funnels
}

// Воронка «Приглашение» считает людей, открывших раздел приглашений за период:
// повторные открытия — один человек, открытия вне периода не считаются,
// владелец не считается никогда.
func TestInviteFunnel_CountsPeopleWhoOpenedSection(t *testing.T) {
	now := time.Date(2026, 9, 28, 12, 0, 0, 0, time.UTC)
	from := now.Add(-7 * 24 * time.Hour)

	f := newFixture(t)
	f.record(t,
		journal.Event{At: now.Add(-time.Hour), TelegramID: 1, Action: ActionInvitesOpen},
		journal.Event{At: now.Add(-2 * time.Hour), TelegramID: 1, Action: ActionInvitesOpen},
		journal.Event{At: now.Add(-3 * 24 * time.Hour), TelegramID: 2, Action: ActionInvitesOpen},
		journal.Event{At: from.Add(-time.Minute), TelegramID: 3, Action: ActionInvitesOpen},
		journal.Event{At: now.Add(-time.Hour), TelegramID: 4, Action: "pay_open"},
		journal.Event{At: now.Add(-time.Hour), TelegramID: ownerID, Action: ActionInvitesOpen},
	)

	report, err := f.open(t).Report(context.Background(), FunnelInvite, from, now)
	require.NoError(t, err)

	require.Len(t, report.Steps, 1)
	step := report.Steps[0]
	assert.Equal(t, StepInvitesOpened, step.ID)
	assert.Equal(t, 2, step.People)
	assert.False(t, step.NoData)
}
