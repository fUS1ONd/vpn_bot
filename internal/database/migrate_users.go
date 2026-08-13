package database

import (
	"context"
	"database/sql"
	"fmt"
	"log/slog"
)

// usersColumns — полный список колонок users, которые переносятся при перестройке.
// Перечислять поимённо обязательно: физический порядок колонок в чужой базе
// зависит от того, в какой момент отработали прежние ALTER-миграции, и
// `INSERT INTO … SELECT *` там развалился бы.
const usersColumns = `telegram_id, username, first_name, remnawave_uuid, remnawave_id,
	subscription_price, moderator_id, invited_by, legacy_paid_migrated, created_at`

// usersTableNeedsRebuild сообщает, стоит ли у remnawave_uuid ещё NOT NULL.
// SQLite не умеет снимать NOT NULL через ALTER, поэтому единственный способ —
// одноразовая перестройка таблицы.
func usersTableNeedsRebuild(conn *sql.DB) bool {
	rows, err := conn.Query(`SELECT name, "notnull" FROM pragma_table_info('users')`)
	if err != nil {
		slog.Error("Failed to read users table info", "error", err)
		return false
	}
	defer rows.Close()

	for rows.Next() {
		var name string
		var notNull int
		if err := rows.Scan(&name, &notNull); err != nil {
			slog.Error("Failed to scan users table info", "error", err)
			return false
		}
		if name == "remnawave_uuid" {
			return notNull == 1
		}
	}

	return false
}

// rebuildUsersTable снимает NOT NULL с remnawave_uuid, пересобирая таблицу.
//
// Всё внутри одной транзакции: падение процесса между DROP TABLE users и RENAME
// оставило бы базу без таблицы users, а следующий старт создал бы её пустой через
// CREATE TABLE IF NOT EXISTS — тихая полная потеря пользователей.
// PRAGMA foreign_keys выключается вне транзакции: SQLite требует именно так.
func rebuildUsersTable(conn *sql.DB) error {
	if !usersTableNeedsRebuild(conn) {
		return nil
	}

	slog.Warn("Rebuilding users table to allow NULL remnawave_uuid")

	var before int
	if err := conn.QueryRow(`SELECT COUNT(*) FROM users`).Scan(&before); err != nil {
		return fmt.Errorf("count users before rebuild: %w", err)
	}

	// PRAGMA действует на соединение, а не на базу, поэтому и pragma, и транзакция
	// обязаны идти по одному и тому же соединению: выполненный на пуле
	// `PRAGMA foreign_keys = OFF` мог бы не попасть на то соединение, где потом
	// открылась транзакция.
	ctx := context.Background()
	dbConn, err := conn.Conn(ctx)
	if err != nil {
		return fmt.Errorf("acquire connection for users rebuild: %w", err)
	}
	defer dbConn.Close()

	// SQLite требует переключать foreign_keys вне транзакции.
	if _, err := dbConn.ExecContext(ctx, `PRAGMA foreign_keys = OFF`); err != nil {
		return fmt.Errorf("disable foreign keys before rebuild: %w", err)
	}
	defer func() {
		if _, err := dbConn.ExecContext(ctx, `PRAGMA foreign_keys = ON`); err != nil {
			slog.Error("Failed to re-enable foreign keys after users rebuild", "error", err)
		}
	}()

	tx, err := dbConn.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin users rebuild: %w", err)
	}

	rollback := func(cause error) error {
		if rbErr := tx.Rollback(); rbErr != nil {
			return fmt.Errorf("%w: rollback failed: %v", cause, rbErr)
		}
		return cause
	}

	steps := []string{
		// Остаток прерванной попытки, если процесс упал в прошлый раз.
		`DROP TABLE IF EXISTS users_new`,
		`CREATE TABLE users_new (
			telegram_id          INTEGER PRIMARY KEY,
			username             TEXT,
			first_name           TEXT,
			remnawave_uuid       TEXT UNIQUE,
			remnawave_id         INTEGER,
			subscription_price   INTEGER,
			moderator_id         INTEGER,
			invited_by           INTEGER,
			legacy_paid_migrated INTEGER NOT NULL DEFAULT 0,
			created_at           TIMESTAMP DEFAULT CURRENT_TIMESTAMP
		)`,
		`INSERT INTO users_new (` + usersColumns + `) SELECT ` + usersColumns + ` FROM users`,
		`DROP TABLE users`,
		`ALTER TABLE users_new RENAME TO users`,
	}

	for _, step := range steps {
		if _, err := tx.Exec(step); err != nil {
			return rollback(fmt.Errorf("users rebuild step failed: %w\nSQL: %s", err, step))
		}
	}

	var after int
	if err := tx.QueryRow(`SELECT COUNT(*) FROM users`).Scan(&after); err != nil {
		return rollback(fmt.Errorf("count users after rebuild: %w", err))
	}
	if after != before {
		return rollback(fmt.Errorf("users rebuild lost rows: before=%d after=%d", before, after))
	}

	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit users rebuild: %w", err)
	}

	slog.Info("Users table rebuilt", "rows", after)
	return nil
}
