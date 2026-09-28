package journal

import (
	"database/sql"
	"path/filepath"
	"testing"
	"time"

	"github.com/fus1ond/vpn_bot/internal/database"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// storedEvent — строка журнала глазами читателя файла events.db.
type storedEvent struct {
	At         time.Time
	TelegramID int64
	Action     string
	Source     string
	Param      string
}

// readEvents читает журнал отдельным соединением, как его прочтёт модуль воронок.
func readEvents(t *testing.T, path string) []storedEvent {
	t.Helper()
	conn, err := sql.Open("sqlite3", database.DSN(path))
	require.NoError(t, err)
	defer conn.Close()

	rows, err := conn.Query(`SELECT ts, telegram_id, action, source, COALESCE(param, '') FROM events ORDER BY id`)
	require.NoError(t, err)
	defer rows.Close()

	var events []storedEvent
	for rows.Next() {
		var e storedEvent
		require.NoError(t, rows.Scan(&e.At, &e.TelegramID, &e.Action, &e.Source, &e.Param))
		events = append(events, e)
	}
	require.NoError(t, rows.Err())
	return events
}

func openTestJournal(t *testing.T, opts Options) (*Journal, string) {
	t.Helper()
	path := filepath.Join(t.TempDir(), "events.db")
	j, err := Open(path, opts)
	require.NoError(t, err)
	return j, path
}

// Записанные События попадают в файл при остановке: рестарт при выкате не
// теряет то, что лежало в буфере.
func TestJournal_CloseFlushesBufferedEvents(t *testing.T) {
	j, path := openTestJournal(t, Options{FlushInterval: time.Hour})

	at := time.Date(2026, 9, 28, 10, 0, 0, 0, time.UTC)
	j.Record(Event{At: at, TelegramID: 42, Action: "invites_open", Source: SourceUser})
	j.Record(Event{At: at.Add(time.Second), TelegramID: 43, Action: "pay_method", Source: SourceUser, Param: "yookassa"})
	j.Close()

	events := readEvents(t, path)
	require.Len(t, events, 2)
	assert.Equal(t, storedEvent{At: at, TelegramID: 42, Action: "invites_open", Source: SourceUser}, events[0])
	assert.Equal(t, "yookassa", events[1].Param)
	assert.Equal(t, int64(43), events[1].TelegramID)
}

// Писатель сбрасывает накопленное по таймеру, не дожидаясь остановки.
func TestJournal_FlushesByTimer(t *testing.T) {
	j, path := openTestJournal(t, Options{FlushInterval: 20 * time.Millisecond})
	t.Cleanup(j.Close)

	j.Record(Event{TelegramID: 42, Action: "invites_open", Source: SourceUser})

	assert.Eventually(t, func() bool { return len(readEvents(t, path)) == 1 },
		2*time.Second, 20*time.Millisecond)
}

// Пачка набирается по размеру: сброс не ждёт таймера.
func TestJournal_FlushesByBatchSize(t *testing.T) {
	j, path := openTestJournal(t, Options{FlushInterval: time.Hour, BatchSize: 3})
	t.Cleanup(j.Close)

	for i := 0; i < 3; i++ {
		j.Record(Event{TelegramID: int64(i), Action: "invites_open", Source: SourceUser})
	}

	assert.Eventually(t, func() bool { return len(readEvents(t, path)) == 3 },
		2*time.Second, 20*time.Millisecond)
}

// Время без явного значения — момент записи, в UTC.
func TestJournal_DefaultsTimeToNowUTC(t *testing.T) {
	j, path := openTestJournal(t, Options{})

	before := time.Now().UTC().Add(-time.Second)
	j.Record(Event{TelegramID: 42, Action: "invites_open", Source: SourceUser})
	j.Close()

	events := readEvents(t, path)
	require.Len(t, events, 1)
	assert.True(t, events[0].At.After(before), "время События — момент записи")
	assert.Equal(t, time.UTC, events[0].At.Location())
}

// Всплеск нажатий не вешает обработку: переполненный буфер отбрасывает
// События, а вызывающий возвращается сразу.
func TestJournal_OverflowDoesNotBlockCaller(t *testing.T) {
	j, _ := openTestJournal(t, Options{BufferSize: 1, BatchSize: 1})
	t.Cleanup(j.Close)

	done := make(chan struct{})
	go func() {
		for i := 0; i < 100_000; i++ {
			j.Record(Event{TelegramID: 42, Action: "invites_open", Source: SourceUser})
		}
		close(done)
	}()

	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("запись в переполненный журнал заблокировала вызывающего")
	}
}

// Сбой базы журнала не роняет бота: События теряются, паники нет.
func TestJournal_WriteFailureDoesNotPanic(t *testing.T) {
	j, path := openTestJournal(t, Options{FlushInterval: time.Hour})

	conn, err := sql.Open("sqlite3", database.DSN(path))
	require.NoError(t, err)
	_, err = conn.Exec(`DROP TABLE events`)
	require.NoError(t, err)
	require.NoError(t, conn.Close())

	assert.NotPanics(t, func() {
		j.Record(Event{TelegramID: 42, Action: "invites_open", Source: SourceUser})
		j.Close()
	})
}

// Нажатие, пришедшее после остановки, не роняет процесс.
func TestJournal_RecordAfterCloseIsIgnored(t *testing.T) {
	j, path := openTestJournal(t, Options{})
	j.Close()

	assert.NotPanics(t, func() {
		j.Record(Event{TelegramID: 42, Action: "invites_open", Source: SourceUser})
		j.Close()
	})
	assert.Empty(t, readEvents(t, path))
}

// Журнал лежит рядом с основной базой: тот же volume, тот же бэкап.
func TestPathNextTo(t *testing.T) {
	assert.Equal(t, filepath.Join("/app/data", "events.db"), PathNextTo("/app/data/bot.db"))
}

// Чистка удаляет События старше срока хранения и оставляет свежие: журнал не
// копится вечно, как обещает политика конфиденциальности.
func TestJournal_PurgeDeletesEventsOlderThanRetention(t *testing.T) {
	j, path := openTestJournal(t, Options{BatchSize: 3, FlushInterval: time.Hour})
	defer j.Close()

	now := time.Date(2026, 9, 28, 12, 0, 0, 0, time.UTC)
	day := 24 * time.Hour
	j.Record(Event{At: now.Add(-181 * day), TelegramID: 1, Action: "old", Source: SourceUser})
	j.Record(Event{At: now.Add(-180 * day), TelegramID: 2, Action: "boundary", Source: SourceUser})
	j.Record(Event{At: now.Add(-179 * day), TelegramID: 3, Action: "fresh", Source: SourceBot})
	require.Eventually(t, func() bool { return len(readEvents(t, path)) == 3 }, 5*time.Second, 10*time.Millisecond)

	deleted, err := j.Purge(now)
	require.NoError(t, err)
	assert.Equal(t, int64(1), deleted)

	events := readEvents(t, path)
	require.Len(t, events, 2)
	assert.Equal(t, "boundary", events[0].Action)
	assert.Equal(t, "fresh", events[1].Action)
}
