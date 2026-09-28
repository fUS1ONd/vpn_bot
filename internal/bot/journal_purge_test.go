package bot

import (
	"database/sql"
	"errors"
	"path/filepath"
	"testing"
	"time"

	"github.com/fus1ond/vpn_bot/internal/database"
	"github.com/fus1ond/vpn_bot/internal/journal"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// eventActions читает Действия журнала отдельным соединением.
func eventActions(t *testing.T, path string) []string {
	t.Helper()
	conn, err := sql.Open("sqlite3", database.DSN(path))
	require.NoError(t, err)
	defer conn.Close()

	rows, err := conn.Query(`SELECT action FROM events ORDER BY id`)
	require.NoError(t, err)
	defer rows.Close()

	var actions []string
	for rows.Next() {
		var action string
		require.NoError(t, rows.Scan(&action))
		actions = append(actions, action)
	}
	require.NoError(t, rows.Err())
	return actions
}

// Проход планировщика чистит журнал: старше срока хранения — удалено, свежее
// осталось.
func TestSchedulerPassPurgesOldJournalEvents(t *testing.T) {
	b, _ := setupTestBot(t)
	path := filepath.Join(t.TempDir(), journal.FileName)
	events, err := journal.Open(path, journal.Options{BatchSize: 2, FlushInterval: time.Hour})
	require.NoError(t, err)
	defer events.Close()
	b.AttachAnalytics(events, nil)

	now := time.Now()
	events.Record(journal.Event{At: now.Add(-journal.Retention - time.Hour), TelegramID: 1, Action: "old", Source: journal.SourceUser})
	events.Record(journal.Event{At: now.Add(-time.Hour), TelegramID: 2, Action: "fresh", Source: journal.SourceUser})
	require.Eventually(t, func() bool { return len(eventActions(t, path)) == 2 }, 5*time.Second, 10*time.Millisecond)

	b.runSubscriptionSchedulerPass()

	assert.Equal(t, []string{"fresh"}, eventActions(t, path))
}

// panickingPurger — журнал, чистка которого падает с паникой.
type panickingPurger struct{}

func (panickingPurger) Purge(time.Time) (int64, error) { panic("journal is broken") }

// failingPurger — журнал, чистка которого возвращает ошибку базы.
type failingPurger struct{}

func (failingPurger) Purge(time.Time) (int64, error) { return 0, errors.New("database is locked") }

// Сломанная чистка журнала не роняет проход: аналитика не имеет права
// остановить уведомления, отключения и чеки.
func TestJournalPurgeStepIsIsolated(t *testing.T) {
	for name, purger := range map[string]eventPurger{"паника": panickingPurger{}, "ошибка": failingPurger{}} {
		t.Run(name, func(t *testing.T) {
			b, _ := setupTestBot(t)
			b.eventsPurger = purger

			assert.NotPanics(t, func() { b.runSubscriptionSchedulerPass() })
		})
	}
}

// countingPurger — журнал, считающий вызовы чистки.
type countingPurger struct{ calls int }

func (p *countingPurger) Purge(time.Time) (int64, error) {
	p.calls++
	return 0, nil
}

// Бот останавливается — журнал закрывается следом, чистку не начинаем.
func TestJournalPurgeStepSkippedOnShutdown(t *testing.T) {
	b, _ := setupTestBot(t)
	purger := &countingPurger{}
	b.eventsPurger = purger

	b.purgeEventsJournal(time.Now())
	require.Equal(t, 1, purger.calls)

	close(b.shutdownCh)
	b.purgeEventsJournal(time.Now())
	assert.Equal(t, 1, purger.calls)
}

// Без журнала шаг чистки ничего не делает.
func TestJournalPurgeStepWithoutJournal(t *testing.T) {
	b, _ := setupTestBot(t)
	assert.NotPanics(t, func() { b.purgeEventsJournal(time.Now()) })
}
