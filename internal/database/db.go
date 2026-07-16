package database

import (
	"database/sql"
	"fmt"
	"os"
	"path/filepath"
	"time"

	_ "github.com/mattn/go-sqlite3"
)

// DB оборачивает операции с базой данных
type DB struct {
	conn *sql.DB
}

// User представляет запись пользователя
type User struct {
	TelegramID         int64
	Username           string
	FirstName          string // Имя пользователя из Telegram
	RemnawaveUUID      string
	SubscriptionPrice  *int   // Цена подписки руб/мес (NULL = не установлена)
	ModeratorID        *int64 // Telegram ID куратора (NULL = админский/снят)
	LegacyPaidMigrated bool   // Старый пользователь с ручной оплатой, переведённый на новую модель
	CreatedAt          time.Time
}

// Invite представляет запись инвайта
type Invite struct {
	Code              string
	CreatedBy         int64
	UsedBy            *int64
	UsedAt            *time.Time // Время активации кода
	ExpireDays        *int       // NULL = бессрочный инвайт
	SubscriptionPrice *int       // Цена подписки при создании инвайта
	KickedAt          *time.Time // Время автокика — инвайт нельзя переиспользовать
	CreatedAt         time.Time
}

// New создаёт новое подключение к БД и инициализирует схему
func New(dbPath string) (*DB, error) {
	// Создаём директорию если не существует
	dir := filepath.Dir(dbPath)
	if dir != "" && dir != "." && dir != ":memory:" {
		if err := os.MkdirAll(dir, 0755); err != nil {
			return nil, fmt.Errorf("failed to create database directory: %w", err)
		}
	}

	// Открываем подключение к БД
	conn, err := sql.Open("sqlite3", dbPath)
	if err != nil {
		return nil, fmt.Errorf("failed to open database: %w", err)
	}

	// Включаем WAL mode для корректной работы при concurrent writes
	// (callback-сервер + scheduler + Telegram handler)
	if _, err := conn.Exec("PRAGMA journal_mode = WAL"); err != nil {
		conn.Close()
		return nil, fmt.Errorf("failed to enable WAL mode: %w", err)
	}

	// Включаем foreign keys
	if _, err := conn.Exec("PRAGMA foreign_keys = ON"); err != nil {
		conn.Close()
		return nil, fmt.Errorf("failed to enable foreign keys: %w", err)
	}

	// Запускаем миграции
	if err := migrate(conn); err != nil {
		conn.Close()
		return nil, fmt.Errorf("failed to run migrations: %w", err)
	}

	return &DB{conn: conn}, nil
}

// migrate выполняет миграции БД
func migrate(conn *sql.DB) error {
	migrations := []string{
		// Таблица пользователей — связка Telegram <-> Remnawave
		`CREATE TABLE IF NOT EXISTS users (
			telegram_id INTEGER PRIMARY KEY,
			username TEXT,
			remnawave_uuid TEXT UNIQUE NOT NULL,
			created_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP
		)`,

		// Таблица инвайтов
		`CREATE TABLE IF NOT EXISTS invites (
				code TEXT PRIMARY KEY,
				created_by INTEGER NOT NULL,
				used_by INTEGER,
				used_at TIMESTAMP,
				expire_days INTEGER,
				is_trial INTEGER NOT NULL DEFAULT 0,
				created_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP
			)`,

		// Таблица модераторов
		`CREATE TABLE IF NOT EXISTS moderators (
			telegram_id INTEGER PRIMARY KEY,
			added_by INTEGER NOT NULL,
			created_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP
		)`,

		// Таблица банов (перманентные блокировки)
		`CREATE TABLE IF NOT EXISTS banned_users (
			telegram_id INTEGER PRIMARY KEY,
			banned_by INTEGER NOT NULL,
			reason TEXT,
			banned_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP
		)`,

		// Таблица отправленных уведомлений по подписке
		`CREATE TABLE IF NOT EXISTS notifications_sent (
			telegram_id INTEGER NOT NULL,
			type TEXT NOT NULL,
			sent_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP,
			PRIMARY KEY (telegram_id, type)
		)`,

		// Таблица платежей
		`CREATE TABLE IF NOT EXISTS payments (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			telegram_id INTEGER NOT NULL,
			moderator_id INTEGER,
			amount INTEGER NOT NULL,
			payment_method TEXT NOT NULL,
			status TEXT NOT NULL DEFAULT 'pending',
			platega_transaction_id TEXT UNIQUE,
			provider TEXT NOT NULL DEFAULT 'platega',
			provider_payment_id TEXT,
			provider_request_key TEXT,
			provider_fee_percent INTEGER,
			is_test INTEGER NOT NULL DEFAULT 0,
			redirect_url TEXT,
			expires_at TIMESTAMP,
			created_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP,
			confirmed_at TIMESTAMP
		)`,

		// Таблица начислений модераторов
		`CREATE TABLE IF NOT EXISTS moderator_earnings (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			payment_id INTEGER NOT NULL REFERENCES payments(id),
			moderator_id INTEGER NOT NULL,
			gross_amount INTEGER NOT NULL,
			platega_fee INTEGER NOT NULL,
			withdrawal_fee INTEGER NOT NULL,
			net_amount INTEGER NOT NULL,
			share_percent INTEGER NOT NULL,
			share_amount INTEGER NOT NULL,
			created_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP
		)`,

		// Индексы
		`CREATE INDEX IF NOT EXISTS idx_users_remnawave_uuid ON users(remnawave_uuid)`,
		`CREATE INDEX IF NOT EXISTS idx_invites_used_by ON invites(used_by)`,
		`CREATE INDEX IF NOT EXISTS idx_payments_telegram_id ON payments(telegram_id)`,
		`CREATE INDEX IF NOT EXISTS idx_payments_status ON payments(status)`,
		`CREATE INDEX IF NOT EXISTS idx_payments_platega_tx ON payments(platega_transaction_id)`,
		`CREATE INDEX IF NOT EXISTS idx_earnings_moderator ON moderator_earnings(moderator_id)`,
		`CREATE INDEX IF NOT EXISTS idx_earnings_payment ON moderator_earnings(payment_id)`,
	}

	for _, m := range migrations {
		if _, err := conn.Exec(m); err != nil {
			return fmt.Errorf("migration failed: %w\nSQL: %s", err, m)
		}
	}

	// Безопасные миграции ALTER TABLE (игнорируем ошибку "duplicate column")
	alterMigrations := []string{
		// Миграция: добавление поля first_name в таблицу users
		`ALTER TABLE users ADD COLUMN first_name TEXT`,
		// Миграция: добавление поля used_at в таблицу invites
		`ALTER TABLE invites ADD COLUMN used_at TIMESTAMP`,
		// Миграция: добавление срока действия инвайта в днях (NULL = бессрочно)
		`ALTER TABLE invites ADD COLUMN expire_days INTEGER`,
		// Миграция: метка автокика — инвайт нельзя использовать повторно, но история сохраняется
		`ALTER TABLE invites ADD COLUMN kicked_at TIMESTAMP`,
		// Миграция: цена подписки пользователя (руб/мес, NULL = не установлена)
		`ALTER TABLE users ADD COLUMN subscription_price INTEGER`,
		// Миграция: telegram_id модератора-куратора (NULL = админский или снят модератор)
		`ALTER TABLE users ADD COLUMN moderator_id INTEGER`,
		// Миграция: флаг старой ручной оплаты для перевода legacy-пользователей на новую модель
		`ALTER TABLE users ADD COLUMN legacy_paid_migrated INTEGER NOT NULL DEFAULT 0`,
		// Миграция: цена подписки при создании инвайта
		`ALTER TABLE invites ADD COLUMN subscription_price INTEGER`,
		// Миграция: неизменяемый исторический флаг trial-инвайта
		`ALTER TABLE invites ADD COLUMN is_trial INTEGER NOT NULL DEFAULT 0`,
		// Нейтральные поля провайдера; старый platega_transaction_id остаётся для rollback-совместимости.
		`ALTER TABLE payments ADD COLUMN provider TEXT NOT NULL DEFAULT 'platega'`,
		`ALTER TABLE payments ADD COLUMN provider_payment_id TEXT`,
		`ALTER TABLE payments ADD COLUMN provider_request_key TEXT`,
		`ALTER TABLE payments ADD COLUMN provider_fee_percent INTEGER`,
		// Тестовые платежи администратора не влияют на доступ и финансовые отчёты.
		`ALTER TABLE payments ADD COLUMN is_test INTEGER NOT NULL DEFAULT 0`,
	}
	for _, m := range alterMigrations {
		// Игнорируем ошибки ALTER TABLE - колонка может уже существовать
		conn.Exec(m)
	}
	if _, err := conn.Exec(`UPDATE payments SET provider = 'platega' WHERE provider IS NULL OR provider = ''`); err != nil {
		return fmt.Errorf("failed to backfill payments.provider: %w", err)
	}
	if _, err := conn.Exec(`UPDATE payments SET provider_payment_id = platega_transaction_id WHERE provider_payment_id IS NULL AND platega_transaction_id IS NOT NULL`); err != nil {
		return fmt.Errorf("failed to backfill payments.provider_payment_id: %w", err)
	}
	if _, err := conn.Exec(`CREATE UNIQUE INDEX IF NOT EXISTS idx_payments_provider_payment_id ON payments(provider, provider_payment_id) WHERE provider_payment_id IS NOT NULL AND provider_payment_id != ''`); err != nil {
		return fmt.Errorf("failed to create provider payment index: %w", err)
	}
	// До поддержки дробных процентов snapshot хранился в целых процентах.
	// Конвертируем только старые значения <100; уже новые basis points не затрагиваем.
	if _, err := conn.Exec(`UPDATE payments SET provider_fee_percent = provider_fee_percent * 100 WHERE provider_fee_percent IS NOT NULL AND provider_fee_percent > 0 AND provider_fee_percent < 100`); err != nil {
		return fmt.Errorf("failed to migrate provider fee snapshots: %w", err)
	}

	// Бэкофилл для старых записей: всё, что изначально было trial (expire_days IS NOT NULL),
	// должно остаться trial в исторической статистике даже после последующих изменений expire_days.
	if _, err := conn.Exec(`UPDATE invites SET is_trial = 1 WHERE is_trial = 0 AND expire_days IS NOT NULL`); err != nil {
		return fmt.Errorf("failed to backfill invites.is_trial: %w", err)
	}

	return nil
}

// Close закрывает соединение с БД
func (db *DB) Close() error {
	return db.conn.Close()
}

// Conn возвращает базовое соединение sql.DB (для тестов)
func (db *DB) Conn() *sql.DB {
	return db.conn
}
