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
	TelegramID    int64
	Username      string
	RemnawaveUUID string
	CreatedAt     time.Time
}

// Invite представляет запись инвайта
type Invite struct {
	Code      string
	CreatedBy int64
	UsedBy    *int64
	CreatedAt time.Time
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
			created_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP
		)`,

		// Индексы
		`CREATE INDEX IF NOT EXISTS idx_users_remnawave_uuid ON users(remnawave_uuid)`,
		`CREATE INDEX IF NOT EXISTS idx_invites_used_by ON invites(used_by)`,
	}

	for _, m := range migrations {
		if _, err := conn.Exec(m); err != nil {
			return fmt.Errorf("migration failed: %w\nSQL: %s", err, m)
		}
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
