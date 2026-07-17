package database

import (
	"database/sql"
	"fmt"
)

// CreateUser создаёт нового пользователя
func (db *DB) CreateUser(telegramID int64, username, firstName, remnawaveUUID string, subscriptionPrice *int, moderatorID *int64) (*User, error) {
	return db.CreateUserWithInviter(telegramID, username, firstName, remnawaveUUID, subscriptionPrice, moderatorID, moderatorID)
}

// CreateUserWithInviter создаёт пользователя с раздельными архивным moderator_id
// и нейтральным first-touch invited_by.
func (db *DB) CreateUserWithInviter(telegramID int64, username, firstName, remnawaveUUID string, subscriptionPrice *int, moderatorID, invitedBy *int64) (*User, error) {
	_, err := db.conn.Exec(
		`INSERT INTO users (telegram_id, username, first_name, remnawave_uuid, subscription_price, moderator_id, invited_by, legacy_paid_migrated) VALUES (?, ?, ?, ?, ?, ?, ?, 0)`,
		telegramID, username, firstName, remnawaveUUID, subscriptionPrice, moderatorID, invitedBy,
	)
	if err != nil {
		return nil, fmt.Errorf("failed to create user: %w", err)
	}

	return db.GetUserByTelegramID(telegramID)
}

// GetUserByTelegramID получает пользователя по Telegram ID
func (db *DB) GetUserByTelegramID(telegramID int64) (*User, error) {
	var user User
	var firstName sql.NullString
	var subPrice sql.NullInt64
	var modID sql.NullInt64
	var invitedBy sql.NullInt64
	var legacyPaidMigrated sql.NullInt64
	err := db.conn.QueryRow(
		`SELECT telegram_id, username, first_name, remnawave_uuid, subscription_price, moderator_id, invited_by, legacy_paid_migrated, created_at FROM users WHERE telegram_id = ?`,
		telegramID,
	).Scan(&user.TelegramID, &user.Username, &firstName, &user.RemnawaveUUID, &subPrice, &modID, &invitedBy, &legacyPaidMigrated, &user.CreatedAt)

	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("failed to get user: %w", err)
	}

	if firstName.Valid {
		user.FirstName = firstName.String
	}
	if subPrice.Valid {
		v := int(subPrice.Int64)
		user.SubscriptionPrice = &v
	}
	if modID.Valid {
		user.ModeratorID = &modID.Int64
	}
	if invitedBy.Valid {
		user.InvitedBy = &invitedBy.Int64
	}
	user.LegacyPaidMigrated = legacyPaidMigrated.Valid && legacyPaidMigrated.Int64 != 0

	return &user, nil
}

// GetUserByRemnawaveUUID получает пользователя по Remnawave UUID
func (db *DB) GetUserByRemnawaveUUID(uuid string) (*User, error) {
	var user User
	var firstName sql.NullString
	var subPrice sql.NullInt64
	var modID sql.NullInt64
	var invitedBy sql.NullInt64
	var legacyPaidMigrated sql.NullInt64
	err := db.conn.QueryRow(
		`SELECT telegram_id, username, first_name, remnawave_uuid, subscription_price, moderator_id, invited_by, legacy_paid_migrated, created_at FROM users WHERE remnawave_uuid = ?`,
		uuid,
	).Scan(&user.TelegramID, &user.Username, &firstName, &user.RemnawaveUUID, &subPrice, &modID, &invitedBy, &legacyPaidMigrated, &user.CreatedAt)

	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("failed to get user: %w", err)
	}

	if firstName.Valid {
		user.FirstName = firstName.String
	}
	if subPrice.Valid {
		v := int(subPrice.Int64)
		user.SubscriptionPrice = &v
	}
	if modID.Valid {
		user.ModeratorID = &modID.Int64
	}
	if invitedBy.Valid {
		user.InvitedBy = &invitedBy.Int64
	}
	user.LegacyPaidMigrated = legacyPaidMigrated.Valid && legacyPaidMigrated.Int64 != 0

	return &user, nil
}

// GetAllUsers получает всех пользователей
func (db *DB) GetAllUsers() ([]User, error) {
	rows, err := db.conn.Query(
		`SELECT telegram_id, username, first_name, remnawave_uuid, subscription_price, moderator_id, invited_by, legacy_paid_migrated, created_at FROM users ORDER BY created_at`,
	)
	if err != nil {
		return nil, fmt.Errorf("failed to query users: %w", err)
	}
	defer rows.Close()

	var users []User
	for rows.Next() {
		var user User
		var firstName sql.NullString
		var subPrice sql.NullInt64
		var modID sql.NullInt64
		var invitedBy sql.NullInt64
		var legacyPaidMigrated sql.NullInt64
		if err := rows.Scan(&user.TelegramID, &user.Username, &firstName, &user.RemnawaveUUID, &subPrice, &modID, &invitedBy, &legacyPaidMigrated, &user.CreatedAt); err != nil {
			return nil, fmt.Errorf("failed to scan user: %w", err)
		}
		if firstName.Valid {
			user.FirstName = firstName.String
		}
		if subPrice.Valid {
			v := int(subPrice.Int64)
			user.SubscriptionPrice = &v
		}
		if modID.Valid {
			user.ModeratorID = &modID.Int64
		}
		if invitedBy.Valid {
			user.InvitedBy = &invitedBy.Int64
		}
		user.LegacyPaidMigrated = legacyPaidMigrated.Valid && legacyPaidMigrated.Int64 != 0
		users = append(users, user)
	}

	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("rows error: %w", err)
	}

	return users, nil
}

// UpdateSubscriptionPrice обновляет цену подписки пользователя
func (db *DB) UpdateSubscriptionPrice(telegramID int64, price int) error {
	_, err := db.conn.Exec(`UPDATE users SET subscription_price = ? WHERE telegram_id = ?`, price, telegramID)
	return err
}

// UpdateSubscriptionPriceAndLegacyPaidMigrated обновляет цену и флаг migration в одной транзакции.
func (db *DB) UpdateSubscriptionPriceAndLegacyPaidMigrated(telegramID int64, price int, legacyPaidMigrated *bool) error {
	tx, err := db.conn.Begin()
	if err != nil {
		return err
	}

	rollback := func(err error) error {
		if rbErr := tx.Rollback(); rbErr != nil {
			return fmt.Errorf("%w: rollback failed: %v", err, rbErr)
		}
		return err
	}

	if _, err := tx.Exec(`UPDATE users SET subscription_price = ? WHERE telegram_id = ?`, price, telegramID); err != nil {
		return rollback(err)
	}

	if legacyPaidMigrated != nil {
		intValue := 0
		if *legacyPaidMigrated {
			intValue = 1
		}
		if _, err := tx.Exec(`UPDATE users SET legacy_paid_migrated = ? WHERE telegram_id = ?`, intValue, telegramID); err != nil {
			return rollback(err)
		}
	}

	if err := tx.Commit(); err != nil {
		return err
	}

	return nil
}

// SetLegacyPaidMigrated помечает пользователя как переведённого со старой ручной оплаты.
func (db *DB) SetLegacyPaidMigrated(telegramID int64, value bool) error {
	var intValue int
	if value {
		intValue = 1
	}
	_, err := db.conn.Exec(`UPDATE users SET legacy_paid_migrated = ? WHERE telegram_id = ?`, intValue, telegramID)
	return err
}

// UpdateUsername обновляет username пользователя
func (db *DB) UpdateUsername(telegramID int64, username string) error {
	_, err := db.conn.Exec(
		`UPDATE users SET username = ? WHERE telegram_id = ?`,
		username, telegramID,
	)
	if err != nil {
		return fmt.Errorf("failed to update username: %w", err)
	}
	return nil
}

// UpdateUserInfo обновляет username и first_name пользователя (Upsert для актуализации данных)
func (db *DB) UpdateUserInfo(telegramID int64, username, firstName string) error {
	_, err := db.conn.Exec(
		`UPDATE users SET username = ?, first_name = ? WHERE telegram_id = ?`,
		username, firstName, telegramID,
	)
	if err != nil {
		return fmt.Errorf("failed to update user info: %w", err)
	}
	return nil
}

// DeleteUser удаляет пользователя
func (db *DB) DeleteUser(telegramID int64) error {
	_, err := db.conn.Exec(`DELETE FROM users WHERE telegram_id = ?`, telegramID)
	if err != nil {
		return fmt.Errorf("failed to delete user: %w", err)
	}
	return nil
}

// UserExists проверяет существует ли пользователь
func (db *DB) UserExists(telegramID int64) (bool, error) {
	var exists bool
	err := db.conn.QueryRow(
		`SELECT EXISTS(SELECT 1 FROM users WHERE telegram_id = ?)`,
		telegramID,
	).Scan(&exists)
	if err != nil {
		return false, fmt.Errorf("failed to check user existence: %w", err)
	}
	return exists, nil
}

// CountUsers возвращает количество пользователей
func (db *DB) CountUsers() (int, error) {
	var count int
	err := db.conn.QueryRow(`SELECT COUNT(*) FROM users`).Scan(&count)
	if err != nil {
		return 0, fmt.Errorf("failed to count users: %w", err)
	}
	return count, nil
}
