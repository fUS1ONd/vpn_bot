package database

import (
	"context"
	"database/sql"
	"path/filepath"
	"sync"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// Настройки соединения должны действовать на каждое соединение пула, а не только
// на то, через которое прошёл первый запрос после открытия.
func TestEveryPoolConnectionHasForeignKeysAndWAL(t *testing.T) {
	db, err := New(filepath.Join(t.TempDir(), "pool.db"))
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, db.Close()) })

	const poolSize = 4
	db.Conn().SetMaxOpenConns(poolSize)
	ctx := context.Background()

	// Держим все соединения одновременно, чтобы пул не отдал одно и то же дважды.
	conns := make([]*sql.Conn, 0, poolSize)
	for index := 0; index < poolSize; index++ {
		conn, connErr := db.Conn().Conn(ctx)
		require.NoError(t, connErr)
		conns = append(conns, conn)
	}
	t.Cleanup(func() {
		for _, conn := range conns {
			_ = conn.Close()
		}
	})

	for index, conn := range conns {
		var foreignKeys int
		require.NoError(t, conn.QueryRowContext(ctx, "PRAGMA foreign_keys").Scan(&foreignKeys))
		assert.Equal(t, 1, foreignKeys, "соединение %d без foreign_keys", index)

		var busyTimeout int
		require.NoError(t, conn.QueryRowContext(ctx, "PRAGMA busy_timeout").Scan(&busyTimeout))
		assert.Equal(t, 5000, busyTimeout, "соединение %d с другим busy_timeout", index)

		var journalMode string
		require.NoError(t, conn.QueryRowContext(ctx, "PRAGMA journal_mode").Scan(&journalMode))
		assert.Equal(t, "wal", journalMode, "соединение %d не в WAL", index)
	}
}

// Конкурентные писатели ждут друг друга, а не падают с "database is locked" —
// в том числе транзакции, которые сначала читают, а потом пишут.
func TestConcurrentWritersDoNotFailWithLocked(t *testing.T) {
	db, err := New(filepath.Join(t.TempDir(), "writers.db"))
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, db.Close()) })

	const writers = 16
	db.Conn().SetMaxOpenConns(writers)

	start := make(chan struct{})
	errs := make(chan error, writers)
	var group sync.WaitGroup
	for index := 0; index < writers; index++ {
		group.Add(1)
		go func(telegramID int64) {
			defer group.Done()
			<-start
			errs <- readThenWrite(db.Conn(), telegramID)
		}(int64(index + 1))
	}
	close(start)
	group.Wait()
	close(errs)

	for writeErr := range errs {
		assert.NoError(t, writeErr)
	}
	var count int
	require.NoError(t, db.Conn().QueryRow("SELECT COUNT(*) FROM moderators").Scan(&count))
	assert.Equal(t, writers, count)
}

// readThenWrite повторяет форму денежных транзакций: проверка состояния, затем запись.
func readThenWrite(conn *sql.DB, telegramID int64) error {
	tx, err := conn.Begin()
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()

	var existing int
	if err := tx.QueryRow("SELECT COUNT(*) FROM moderators").Scan(&existing); err != nil {
		return err
	}
	if _, err := tx.Exec("INSERT INTO moderators (telegram_id, added_by) VALUES (?, ?)", telegramID, existing); err != nil {
		return err
	}
	return tx.Commit()
}
