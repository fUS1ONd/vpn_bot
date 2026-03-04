package database

import "fmt"

// BanUser добавляет пользователя в перманентный бан.
func (db *DB) BanUser(telegramID, bannedBy int64) error {
	_, err := db.conn.Exec(
		`INSERT INTO banned_users (telegram_id, banned_by)
		 VALUES (?, ?)
		 ON CONFLICT(telegram_id) DO UPDATE SET
		   banned_by = excluded.banned_by,
		   banned_at = CURRENT_TIMESTAMP`,
		telegramID, bannedBy,
	)
	if err != nil {
		return fmt.Errorf("failed to ban user: %w", err)
	}
	return nil
}

// IsBanned проверяет, находится ли пользователь в перманентном бане.
func (db *DB) IsBanned(telegramID int64) (bool, error) {
	var exists bool
	err := db.conn.QueryRow(
		`SELECT EXISTS(SELECT 1 FROM banned_users WHERE telegram_id = ?)`,
		telegramID,
	).Scan(&exists)
	if err != nil {
		return false, fmt.Errorf("failed to check ban status: %w", err)
	}
	return exists, nil
}
