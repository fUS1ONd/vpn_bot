package database

import (
	"database/sql"
	"fmt"
	"os"
	"path/filepath"
	"time"

	_ "github.com/mattn/go-sqlite3"
)

// Subscription statuses
const (
	StatusNone    = "none"
	StatusTrial   = "trial"
	StatusActive  = "active"
	StatusExpired = "expired"
)

// DB wraps database operations
type DB struct {
	conn *sql.DB
}

// New creates a new database connection and initializes the schema
func New(dbPath string) (*DB, error) {
	// Create directory if it doesn't exist
	dir := filepath.Dir(dbPath)
	if dir != "" && dir != "." && dir != ":memory:" {
		if err := os.MkdirAll(dir, 0755); err != nil {
			return nil, fmt.Errorf("failed to create database directory: %w", err)
		}
	}

	// Open database connection
	conn, err := sql.Open("sqlite3", dbPath)
	if err != nil {
		return nil, fmt.Errorf("failed to open database: %w", err)
	}

	// Enable foreign keys
	if _, err := conn.Exec("PRAGMA foreign_keys = ON"); err != nil {
		conn.Close()
		return nil, fmt.Errorf("failed to enable foreign keys: %w", err)
	}

	// Run migrations
	if err := migrate(conn); err != nil {
		conn.Close()
		return nil, fmt.Errorf("failed to run migrations: %w", err)
	}

	return &DB{conn: conn}, nil
}

// migrate runs all database migrations
func migrate(conn *sql.DB) error {
	// First, check if we have old schema and need to migrate
	if err := migrateOldSchema(conn); err != nil {
		return fmt.Errorf("failed to migrate old schema: %w", err)
	}

	migrations := []string{
		// Users table (new schema)
		// telegram_id can be NULL for admin-created users
		`CREATE TABLE IF NOT EXISTS users (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			telegram_id INTEGER UNIQUE,
			email TEXT UNIQUE NOT NULL,
			uuid TEXT UNIQUE NOT NULL,
			created_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP,
			subscription_status TEXT DEFAULT 'none',
			subscription_end_at TIMESTAMP,
			trial_used BOOLEAN DEFAULT FALSE,
			ru_extra_traffic INTEGER DEFAULT 0
		)`,

		// Payments table
		`CREATE TABLE IF NOT EXISTS payments (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			user_id INTEGER NOT NULL,
			payment_id TEXT UNIQUE NOT NULL,
			provider TEXT NOT NULL,
			amount INTEGER NOT NULL,
			status TEXT DEFAULT 'pending',
			type TEXT NOT NULL,
			promo_code TEXT,
			created_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP,
			confirmed_at TIMESTAMP,
			FOREIGN KEY (user_id) REFERENCES users(id)
		)`,

		// Promo codes table
		`CREATE TABLE IF NOT EXISTS promo_codes (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			code TEXT UNIQUE NOT NULL,
			type TEXT NOT NULL,
			value INTEGER NOT NULL,
			max_uses INTEGER DEFAULT 1,
			used_count INTEGER DEFAULT 0,
			valid_until TIMESTAMP,
			created_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP
		)`,

		// Promo uses table
		`CREATE TABLE IF NOT EXISTS promo_uses (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			user_id INTEGER NOT NULL,
			promo_id INTEGER NOT NULL,
			used_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP,
			FOREIGN KEY (user_id) REFERENCES users(id),
			FOREIGN KEY (promo_id) REFERENCES promo_codes(id),
			UNIQUE(user_id, promo_id)
		)`,

		// Indexes
		`CREATE INDEX IF NOT EXISTS idx_users_telegram_id ON users(telegram_id)`,
		`CREATE INDEX IF NOT EXISTS idx_users_email ON users(email)`,
		`CREATE INDEX IF NOT EXISTS idx_users_uuid ON users(uuid)`,
		`CREATE INDEX IF NOT EXISTS idx_users_subscription_status ON users(subscription_status)`,
		`CREATE INDEX IF NOT EXISTS idx_payments_user_id ON payments(user_id)`,
		`CREATE INDEX IF NOT EXISTS idx_payments_status ON payments(status)`,
		`CREATE INDEX IF NOT EXISTS idx_promo_codes_code ON promo_codes(code)`,
	}

	for _, m := range migrations {
		if _, err := conn.Exec(m); err != nil {
			return fmt.Errorf("migration failed: %w\nSQL: %s", err, m)
		}
	}

	return nil
}

// migrateOldSchema handles migration from old schema (email, uuid only) to new schema
func migrateOldSchema(conn *sql.DB) error {
	// Check if users table exists
	var tableName string
	err := conn.QueryRow("SELECT name FROM sqlite_master WHERE type='table' AND name='users'").Scan(&tableName)
	if err == sql.ErrNoRows {
		// No users table yet, nothing to migrate
		return nil
	}
	if err != nil {
		return err
	}

	// Check if telegram_id column exists
	rows, err := conn.Query("PRAGMA table_info(users)")
	if err != nil {
		return err
	}
	defer rows.Close()

	columns := make(map[string]bool)
	for rows.Next() {
		var cid int
		var name, ctype string
		var notnull, pk int
		var dfltValue interface{}
		if err := rows.Scan(&cid, &name, &ctype, &notnull, &dfltValue, &pk); err != nil {
			return err
		}
		columns[name] = true
	}

	// If telegram_id doesn't exist, we need to migrate
	if !columns["telegram_id"] {
		// SQLite doesn't support ADD COLUMN with constraints well,
		// so we recreate the table with new schema
		migrationSQL := []string{
			// Rename old table
			`ALTER TABLE users RENAME TO users_old`,

			// Create new table with full schema
			// telegram_id is NULL for migrated/admin-created users
			`CREATE TABLE users (
				id INTEGER PRIMARY KEY AUTOINCREMENT,
				telegram_id INTEGER UNIQUE,
				email TEXT UNIQUE NOT NULL,
				uuid TEXT UNIQUE NOT NULL,
				created_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP,
				subscription_status TEXT DEFAULT 'none',
				subscription_end_at TIMESTAMP,
				trial_used BOOLEAN DEFAULT FALSE,
				ru_extra_traffic INTEGER DEFAULT 0
			)`,

			// Copy data from old table (telegram_id = NULL for old users)
			`INSERT INTO users (id, email, uuid, telegram_id)
			 SELECT id, email, uuid, NULL FROM users_old`,

			// Drop old table
			`DROP TABLE users_old`,
		}

		for _, sql := range migrationSQL {
			if _, err := conn.Exec(sql); err != nil {
				return fmt.Errorf("old schema migration failed: %w\nSQL: %s", err, sql)
			}
		}
	}

	return nil
}

// User represents a user record
type User struct {
	ID                 int64
	TelegramID         int64
	Email              string
	UUID               string
	CreatedAt          time.Time
	SubscriptionStatus string
	SubscriptionEndAt  *time.Time
	TrialUsed          bool
	RuExtraTraffic     int64
}

// Payment represents a payment record
type Payment struct {
	ID          int64
	UserID      int64
	PaymentID   string
	Provider    string
	Amount      int
	Status      string
	Type        string
	PromoCode   *string
	CreatedAt   time.Time
	ConfirmedAt *time.Time
}

// PromoCode represents a promo code record
type PromoCode struct {
	ID         int64
	Code       string
	Type       string
	Value      int
	MaxUses    int
	UsedCount  int
	ValidUntil *time.Time
	CreatedAt  time.Time
}

// Close closes the database connection
func (db *DB) Close() error {
	return db.conn.Close()
}

// Conn returns the underlying sql.DB connection (for testing)
func (db *DB) Conn() *sql.DB {
	return db.conn
}
