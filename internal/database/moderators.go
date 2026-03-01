package database

import (
	"database/sql"
	"fmt"
	"time"
)

// Moderator представляет запись модератора
type Moderator struct {
	TelegramID int64
	AddedBy    int64
	CreatedAt  time.Time
}

// ModeratorWithUser содержит данные модератора вместе с данными пользователя
type ModeratorWithUser struct {
	TelegramID   int64
	Username     string
	FirstName    string
	AddedBy      int64
	CreatedAt    time.Time
	InvitesCount int // количество приглашённых пользователей
}

// AddModerator назначает пользователя модератором (пользователь должен существовать в users)
func (db *DB) AddModerator(telegramID, addedBy int64) error {
	// Проверяем что пользователь зарегистрирован
	exists, err := db.UserExists(telegramID)
	if err != nil {
		return fmt.Errorf("failed to check user existence: %w", err)
	}
	if !exists {
		return fmt.Errorf("user %d is not registered", telegramID)
	}

	_, err = db.conn.Exec(
		`INSERT INTO moderators (telegram_id, added_by) VALUES (?, ?)`,
		telegramID, addedBy,
	)
	if err != nil {
		return fmt.Errorf("failed to add moderator: %w", err)
	}

	return nil
}

// IsModerator проверяет, является ли пользователь модератором
func (db *DB) IsModerator(telegramID int64) (bool, error) {
	var exists bool
	err := db.conn.QueryRow(
		`SELECT EXISTS(SELECT 1 FROM moderators WHERE telegram_id = ?)`,
		telegramID,
	).Scan(&exists)
	if err != nil {
		return false, fmt.Errorf("failed to check moderator: %w", err)
	}
	return exists, nil
}

// GetModerator получает модератора по telegram_id
func (db *DB) GetModerator(telegramID int64) (*Moderator, error) {
	var mod Moderator
	err := db.conn.QueryRow(
		`SELECT telegram_id, added_by, created_at FROM moderators WHERE telegram_id = ?`,
		telegramID,
	).Scan(&mod.TelegramID, &mod.AddedBy, &mod.CreatedAt)

	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("failed to get moderator: %w", err)
	}

	return &mod, nil
}

// GetAllModerators получает всех модераторов с данными пользователей
func (db *DB) GetAllModerators() ([]ModeratorWithUser, error) {
	query := `
		SELECT m.telegram_id, u.username, u.first_name, m.added_by, m.created_at,
			(SELECT COUNT(*) FROM invites WHERE created_by = m.telegram_id AND used_by IS NOT NULL) as invites_count
		FROM moderators m
		JOIN users u ON m.telegram_id = u.telegram_id
		ORDER BY m.created_at
	`

	rows, err := db.conn.Query(query)
	if err != nil {
		return nil, fmt.Errorf("failed to query moderators: %w", err)
	}
	defer rows.Close()

	var mods []ModeratorWithUser
	for rows.Next() {
		var mod ModeratorWithUser
		var firstName sql.NullString
		err := rows.Scan(&mod.TelegramID, &mod.Username, &firstName, &mod.AddedBy, &mod.CreatedAt, &mod.InvitesCount)
		if err != nil {
			return nil, fmt.Errorf("failed to scan moderator: %w", err)
		}
		if firstName.Valid {
			mod.FirstName = firstName.String
		}
		mods = append(mods, mod)
	}

	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("rows error: %w", err)
	}

	return mods, nil
}

// RemoveModerator снимает права модератора
func (db *DB) RemoveModerator(telegramID int64) error {
	result, err := db.conn.Exec(
		`DELETE FROM moderators WHERE telegram_id = ?`,
		telegramID,
	)
	if err != nil {
		return fmt.Errorf("failed to remove moderator: %w", err)
	}

	rows, err := result.RowsAffected()
	if err != nil {
		return fmt.Errorf("failed to get affected rows: %w", err)
	}
	if rows == 0 {
		return fmt.Errorf("moderator not found")
	}

	return nil
}
