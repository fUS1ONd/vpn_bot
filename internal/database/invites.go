package database

import (
	"crypto/rand"
	"database/sql"
	"encoding/hex"
	"fmt"
)

// CreateInvite создаёт новый инвайт
func (db *DB) CreateInvite(createdBy int64) (*Invite, error) {
	code, err := generateInviteCode()
	if err != nil {
		return nil, fmt.Errorf("failed to generate invite code: %w", err)
	}

	_, err = db.conn.Exec(
		`INSERT INTO invites (code, created_by) VALUES (?, ?)`,
		code, createdBy,
	)
	if err != nil {
		return nil, fmt.Errorf("failed to create invite: %w", err)
	}

	return db.GetInviteByCode(code)
}

// GetInviteByCode получает инвайт по коду
func (db *DB) GetInviteByCode(code string) (*Invite, error) {
	var invite Invite
	var usedBy sql.NullInt64

	err := db.conn.QueryRow(
		`SELECT code, created_by, used_by, created_at FROM invites WHERE code = ?`,
		code,
	).Scan(&invite.Code, &invite.CreatedBy, &usedBy, &invite.CreatedAt)

	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("failed to get invite: %w", err)
	}

	if usedBy.Valid {
		invite.UsedBy = &usedBy.Int64
	}

	return &invite, nil
}

// UseInvite помечает инвайт как использованный
func (db *DB) UseInvite(code string, usedBy int64) error {
	result, err := db.conn.Exec(
		`UPDATE invites SET used_by = ? WHERE code = ? AND used_by IS NULL`,
		usedBy, code,
	)
	if err != nil {
		return fmt.Errorf("failed to use invite: %w", err)
	}

	rows, err := result.RowsAffected()
	if err != nil {
		return fmt.Errorf("failed to get affected rows: %w", err)
	}
	if rows == 0 {
		return fmt.Errorf("invite not found or already used")
	}

	return nil
}

// IsInviteValid проверяет валиден ли инвайт (существует и не использован)
func (db *DB) IsInviteValid(code string) (bool, error) {
	var exists bool
	err := db.conn.QueryRow(
		`SELECT EXISTS(SELECT 1 FROM invites WHERE code = ? AND used_by IS NULL)`,
		code,
	).Scan(&exists)
	if err != nil {
		return false, fmt.Errorf("failed to check invite: %w", err)
	}
	return exists, nil
}

// GetAllInvites получает все инвайты
func (db *DB) GetAllInvites() ([]Invite, error) {
	rows, err := db.conn.Query(
		`SELECT code, created_by, used_by, created_at FROM invites ORDER BY created_at DESC`,
	)
	if err != nil {
		return nil, fmt.Errorf("failed to query invites: %w", err)
	}
	defer rows.Close()

	var invites []Invite
	for rows.Next() {
		var invite Invite
		var usedBy sql.NullInt64

		if err := rows.Scan(&invite.Code, &invite.CreatedBy, &usedBy, &invite.CreatedAt); err != nil {
			return nil, fmt.Errorf("failed to scan invite: %w", err)
		}

		if usedBy.Valid {
			invite.UsedBy = &usedBy.Int64
		}

		invites = append(invites, invite)
	}

	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("rows error: %w", err)
	}

	return invites, nil
}

// GetUnusedInvites получает неиспользованные инвайты
func (db *DB) GetUnusedInvites() ([]Invite, error) {
	rows, err := db.conn.Query(
		`SELECT code, created_by, used_by, created_at FROM invites WHERE used_by IS NULL ORDER BY created_at DESC`,
	)
	if err != nil {
		return nil, fmt.Errorf("failed to query invites: %w", err)
	}
	defer rows.Close()

	var invites []Invite
	for rows.Next() {
		var invite Invite
		var usedBy sql.NullInt64

		if err := rows.Scan(&invite.Code, &invite.CreatedBy, &usedBy, &invite.CreatedAt); err != nil {
			return nil, fmt.Errorf("failed to scan invite: %w", err)
		}

		invites = append(invites, invite)
	}

	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("rows error: %w", err)
	}

	return invites, nil
}

// DeleteInvite удаляет инвайт
func (db *DB) DeleteInvite(code string) error {
	_, err := db.conn.Exec(`DELETE FROM invites WHERE code = ?`, code)
	if err != nil {
		return fmt.Errorf("failed to delete invite: %w", err)
	}
	return nil
}

// CountUnusedInvites возвращает количество неиспользованных инвайтов
func (db *DB) CountUnusedInvites() (int, error) {
	var count int
	err := db.conn.QueryRow(`SELECT COUNT(*) FROM invites WHERE used_by IS NULL`).Scan(&count)
	if err != nil {
		return 0, fmt.Errorf("failed to count invites: %w", err)
	}
	return count, nil
}

// generateInviteCode генерирует случайный 8-символьный код
func generateInviteCode() (string, error) {
	bytes := make([]byte, 4)
	if _, err := rand.Read(bytes); err != nil {
		return "", err
	}
	return hex.EncodeToString(bytes), nil
}
