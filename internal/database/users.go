package database

import (
	"database/sql"
	"fmt"
	"time"
)

// CreateUser creates a new user
func (db *DB) CreateUser(telegramID int64, email, uuid string) (*User, error) {
	result, err := db.conn.Exec(
		`INSERT INTO users (telegram_id, email, uuid) VALUES (?, ?, ?)`,
		telegramID, email, uuid,
	)
	if err != nil {
		return nil, fmt.Errorf("failed to create user: %w", err)
	}

	id, err := result.LastInsertId()
	if err != nil {
		return nil, fmt.Errorf("failed to get last insert id: %w", err)
	}

	return db.GetUserByID(id)
}

// GetUserByID retrieves a user by ID
func (db *DB) GetUserByID(id int64) (*User, error) {
	return db.scanUser(db.conn.QueryRow(
		`SELECT id, telegram_id, email, uuid, created_at, subscription_status,
		        subscription_end_at, trial_used, ru_extra_traffic
		 FROM users WHERE id = ?`, id,
	))
}

// GetUserByTelegramID retrieves a user by Telegram ID
func (db *DB) GetUserByTelegramID(telegramID int64) (*User, error) {
	return db.scanUser(db.conn.QueryRow(
		`SELECT id, telegram_id, email, uuid, created_at, subscription_status,
		        subscription_end_at, trial_used, ru_extra_traffic
		 FROM users WHERE telegram_id = ?`, telegramID,
	))
}

// GetUserByEmail retrieves a user by email
func (db *DB) GetUserByEmail(email string) (*User, error) {
	return db.scanUser(db.conn.QueryRow(
		`SELECT id, telegram_id, email, uuid, created_at, subscription_status,
		        subscription_end_at, trial_used, ru_extra_traffic
		 FROM users WHERE email = ?`, email,
	))
}

// GetUserByUUID retrieves a user by UUID
func (db *DB) GetUserByUUID(uuid string) (*User, error) {
	return db.scanUser(db.conn.QueryRow(
		`SELECT id, telegram_id, email, uuid, created_at, subscription_status,
		        subscription_end_at, trial_used, ru_extra_traffic
		 FROM users WHERE uuid = ?`, uuid,
	))
}

// scanUser scans a single user row
func (db *DB) scanUser(row *sql.Row) (*User, error) {
	var user User
	var subscriptionEndAt sql.NullTime

	err := row.Scan(
		&user.ID, &user.TelegramID, &user.Email, &user.UUID, &user.CreatedAt,
		&user.SubscriptionStatus, &subscriptionEndAt, &user.TrialUsed, &user.RuExtraTraffic,
	)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("failed to scan user: %w", err)
	}

	if subscriptionEndAt.Valid {
		user.SubscriptionEndAt = &subscriptionEndAt.Time
	}

	return &user, nil
}

// GetAllUsers retrieves all users
func (db *DB) GetAllUsers() ([]User, error) {
	rows, err := db.conn.Query(
		`SELECT id, telegram_id, email, uuid, created_at, subscription_status,
		        subscription_end_at, trial_used, ru_extra_traffic
		 FROM users ORDER BY id`,
	)
	if err != nil {
		return nil, fmt.Errorf("failed to query users: %w", err)
	}
	defer rows.Close()

	return db.scanUsers(rows)
}

// GetActiveUsers retrieves users with active or trial subscriptions
func (db *DB) GetActiveUsers() ([]User, error) {
	rows, err := db.conn.Query(
		`SELECT id, telegram_id, email, uuid, created_at, subscription_status,
		        subscription_end_at, trial_used, ru_extra_traffic
		 FROM users WHERE subscription_status IN (?, ?) ORDER BY id`,
		StatusActive, StatusTrial,
	)
	if err != nil {
		return nil, fmt.Errorf("failed to query active users: %w", err)
	}
	defer rows.Close()

	return db.scanUsers(rows)
}

// GetExpiredUsers retrieves users whose subscription has expired
func (db *DB) GetExpiredUsers() ([]User, error) {
	rows, err := db.conn.Query(
		`SELECT id, telegram_id, email, uuid, created_at, subscription_status,
		        subscription_end_at, trial_used, ru_extra_traffic
		 FROM users
		 WHERE subscription_status IN (?, ?)
		   AND subscription_end_at IS NOT NULL
		   AND subscription_end_at < ?
		 ORDER BY id`,
		StatusActive, StatusTrial, time.Now(),
	)
	if err != nil {
		return nil, fmt.Errorf("failed to query expired users: %w", err)
	}
	defer rows.Close()

	return db.scanUsers(rows)
}

// scanUsers scans multiple user rows
func (db *DB) scanUsers(rows *sql.Rows) ([]User, error) {
	var users []User
	for rows.Next() {
		var user User
		var subscriptionEndAt sql.NullTime

		if err := rows.Scan(
			&user.ID, &user.TelegramID, &user.Email, &user.UUID, &user.CreatedAt,
			&user.SubscriptionStatus, &subscriptionEndAt, &user.TrialUsed, &user.RuExtraTraffic,
		); err != nil {
			return nil, fmt.Errorf("failed to scan user: %w", err)
		}

		if subscriptionEndAt.Valid {
			user.SubscriptionEndAt = &subscriptionEndAt.Time
		}

		users = append(users, user)
	}

	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("rows error: %w", err)
	}

	return users, nil
}

// UpdateUserSubscription updates user's subscription status and end time
func (db *DB) UpdateUserSubscription(userID int64, status string, endAt *time.Time) error {
	_, err := db.conn.Exec(
		`UPDATE users SET subscription_status = ?, subscription_end_at = ? WHERE id = ?`,
		status, endAt, userID,
	)
	if err != nil {
		return fmt.Errorf("failed to update subscription: %w", err)
	}
	return nil
}

// MarkTrialUsed marks user's trial as used
func (db *DB) MarkTrialUsed(userID int64) error {
	_, err := db.conn.Exec(`UPDATE users SET trial_used = TRUE WHERE id = ?`, userID)
	if err != nil {
		return fmt.Errorf("failed to mark trial used: %w", err)
	}
	return nil
}

// AddRuExtraTraffic adds extra traffic quota for RU server
func (db *DB) AddRuExtraTraffic(userID int64, bytes int64) error {
	_, err := db.conn.Exec(
		`UPDATE users SET ru_extra_traffic = ru_extra_traffic + ? WHERE id = ?`,
		bytes, userID,
	)
	if err != nil {
		return fmt.Errorf("failed to add extra traffic: %w", err)
	}
	return nil
}

// ResetRuExtraTraffic resets extra traffic quota
func (db *DB) ResetRuExtraTraffic(userID int64) error {
	_, err := db.conn.Exec(`UPDATE users SET ru_extra_traffic = 0 WHERE id = ?`, userID)
	if err != nil {
		return fmt.Errorf("failed to reset extra traffic: %w", err)
	}
	return nil
}

// DeleteUser deletes a user by ID
func (db *DB) DeleteUser(userID int64) error {
	_, err := db.conn.Exec(`DELETE FROM users WHERE id = ?`, userID)
	if err != nil {
		return fmt.Errorf("failed to delete user: %w", err)
	}
	return nil
}

// UserExists checks if user exists by telegram ID
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
