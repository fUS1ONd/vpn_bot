package database

import (
	"database/sql"
	"fmt"
	"os"
	"path/filepath"

	_ "github.com/mattn/go-sqlite3"
)

// DB wraps database operations
type DB struct {
	conn *sql.DB
}

// New creates a new database connection and initializes the schema
func New(dbPath string) (*DB, error) {
	// Create directory if it doesn't exist
	dir := filepath.Dir(dbPath)
	if err := os.MkdirAll(dir, 0755); err != nil {
		return nil, fmt.Errorf("failed to create database directory: %w", err)
	}

	// Open database connection
	conn, err := sql.Open("sqlite3", dbPath)
	if err != nil {
		return nil, fmt.Errorf("failed to open database: %w", err)
	}

	// Create table if not exists
	_, err = conn.Exec(`CREATE TABLE IF NOT EXISTS users (email text, uuid text)`)
	if err != nil {
		conn.Close()
		return nil, fmt.Errorf("failed to create table: %w", err)
	}

	return &DB{conn: conn}, nil
}

// AddUser adds a new user to the database
func (db *DB) AddUser(email, uuid string) error {
	_, err := db.conn.Exec("INSERT INTO users VALUES (?, ?)", email, uuid)
	if err != nil {
		return fmt.Errorf("failed to add user: %w", err)
	}
	return nil
}

// GetUserUUID retrieves UUID by email
func (db *DB) GetUserUUID(email string) (string, error) {
	var uuid string
	err := db.conn.QueryRow("SELECT uuid FROM users WHERE email=?", email).Scan(&uuid)
	if err == sql.ErrNoRows {
		return "", nil
	}
	if err != nil {
		return "", fmt.Errorf("failed to get user UUID: %w", err)
	}
	return uuid, nil
}

// GetUserEmail retrieves email by UUID
func (db *DB) GetUserEmail(uuid string) (string, error) {
	var email string
	err := db.conn.QueryRow("SELECT email FROM users WHERE uuid=?", uuid).Scan(&email)
	if err == sql.ErrNoRows {
		return "User", nil // Default value if not found
	}
	if err != nil {
		return "", fmt.Errorf("failed to get user email: %w", err)
	}
	return email, nil
}

// Close closes the database connection
func (db *DB) Close() error {
	return db.conn.Close()
}
