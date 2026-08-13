package database

import (
	"database/sql"
	"os"
	"testing"

	_ "github.com/mattn/go-sqlite3"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// legacyUsersDDL — таблица users в том виде, в каком её создавали прежние версии
// бота: remnawave_uuid объявлен NOT NULL, колонки remnawave_id нет.
const legacyUsersDDL = `CREATE TABLE users (
	telegram_id INTEGER PRIMARY KEY,
	username TEXT,
	remnawave_uuid TEXT UNIQUE NOT NULL,
	invited_by INTEGER,
	created_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP
)`

// columnNotNull сообщает, стоит ли NOT NULL у колонки.
func columnNotNull(t *testing.T, conn *sql.DB, table, column string) bool {
	t.Helper()
	rows, err := conn.Query(`SELECT name, "notnull" FROM pragma_table_info(?)`, table)
	require.NoError(t, err)
	defer rows.Close()

	for rows.Next() {
		var name string
		var notNull int
		require.NoError(t, rows.Scan(&name, &notNull))
		if name == column {
			return notNull == 1
		}
	}
	t.Fatalf("колонка %s.%s не найдена", table, column)
	return false
}

func indexExists(t *testing.T, conn *sql.DB, name string) bool {
	t.Helper()
	var count int
	require.NoError(t, conn.QueryRow(`SELECT COUNT(*) FROM sqlite_master WHERE type = 'index' AND name = ?`, name).Scan(&count))
	return count == 1
}

// Перестройка таблицы users на старой базе: NOT NULL снимается, все девять
// колонок и все строки сохраняются, индексы пересоздаются.
func TestMigrateRebuildsLegacyUsersTable(t *testing.T) {
	dbFile := "test_migrate_legacy_users.db"
	os.Remove(dbFile)
	defer os.Remove(dbFile)

	// Готовим базу «как раньше»: старый DDL плюс ALTER-колонки прежних миграций.
	conn, err := sql.Open("sqlite3", dbFile)
	require.NoError(t, err)
	_, err = conn.Exec(legacyUsersDDL)
	require.NoError(t, err)
	for _, alter := range []string{
		`ALTER TABLE users ADD COLUMN first_name TEXT`,
		`ALTER TABLE users ADD COLUMN subscription_price INTEGER`,
		`ALTER TABLE users ADD COLUMN moderator_id INTEGER`,
		`ALTER TABLE users ADD COLUMN legacy_paid_migrated INTEGER NOT NULL DEFAULT 0`,
	} {
		_, err = conn.Exec(alter)
		require.NoError(t, err)
	}
	_, err = conn.Exec(`INSERT INTO users
		(telegram_id, username, first_name, remnawave_uuid, subscription_price, moderator_id, invited_by, legacy_paid_migrated, created_at)
		VALUES
		(4001, 'alice', 'Alice', 'uuid-4001', 400, 5001, 5001, 1, '2026-01-01 00:00:00'),
		(4002, 'bob', 'Bob', 'uuid-4002', NULL, NULL, NULL, 0, '2026-02-02 00:00:00')`)
	require.NoError(t, err)
	require.NoError(t, conn.Close())

	db, err := New(dbFile)
	require.NoError(t, err)
	defer db.Close()

	assert.False(t, columnNotNull(t, db.Conn(), "users", "remnawave_uuid"),
		"после перестройки NOT NULL с remnawave_uuid должен быть снят")
	assert.True(t, indexExists(t, db.Conn(), "idx_users_remnawave_uuid"))
	assert.True(t, indexExists(t, db.Conn(), "idx_users_remnawave_id"))

	var count int
	require.NoError(t, db.Conn().QueryRow(`SELECT COUNT(*) FROM users`).Scan(&count))
	assert.Equal(t, 2, count, "перестройка не должна терять строки")

	alice, err := db.GetUserByTelegramID(4001)
	require.NoError(t, err)
	require.NotNil(t, alice)
	assert.Equal(t, "alice", alice.Username)
	assert.Equal(t, "Alice", alice.FirstName)
	require.NotNil(t, alice.RemnawaveUUID)
	assert.Equal(t, "uuid-4001", *alice.RemnawaveUUID)
	require.NotNil(t, alice.SubscriptionPrice)
	assert.Equal(t, 400, *alice.SubscriptionPrice)
	require.NotNil(t, alice.ModeratorID)
	assert.Equal(t, int64(5001), *alice.ModeratorID)
	require.NotNil(t, alice.InvitedBy)
	assert.Equal(t, int64(5001), *alice.InvitedBy)
	assert.True(t, alice.LegacyPaidMigrated)
	assert.Equal(t, 2026, alice.CreatedAt.Year())
	assert.Nil(t, alice.RemnawaveID)

	bob, err := db.GetUserByTelegramID(4002)
	require.NoError(t, err)
	require.NotNil(t, bob)
	assert.Nil(t, bob.SubscriptionPrice)
	assert.False(t, bob.LegacyPaidMigrated)

	// Повторный старт на уже перестроенной базе — no-op, ничего не теряется.
	require.NoError(t, db.Close())
	db2, err := New(dbFile)
	require.NoError(t, err)
	defer db2.Close()

	require.NoError(t, db2.Conn().QueryRow(`SELECT COUNT(*) FROM users`).Scan(&count))
	assert.Equal(t, 2, count)
	assert.False(t, columnNotNull(t, db2.Conn(), "users", "remnawave_uuid"))
}

// Свежая база создаётся сразу правильной: перестройка не нужна и не запускается.
func TestFreshDatabaseNeedsNoRebuild(t *testing.T) {
	dbFile := "test_migrate_fresh_users.db"
	os.Remove(dbFile)
	defer os.Remove(dbFile)

	db, err := New(dbFile)
	require.NoError(t, err)
	defer db.Close()

	assert.False(t, columnNotNull(t, db.Conn(), "users", "remnawave_uuid"))
	assert.False(t, usersTableNeedsRebuild(db.Conn()))
	assert.True(t, indexExists(t, db.Conn(), "idx_users_remnawave_id"))
}
