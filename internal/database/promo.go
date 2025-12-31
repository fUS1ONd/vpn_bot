package database

import (
	"database/sql"
	"fmt"
	"time"
)

// Promo code types
const (
	PromoTypeDiscount     = "discount"      // value = discount percentage
	PromoTypeFreeDays     = "free_days"     // value = number of free days
	PromoTypeExtraTraffic = "extra_traffic" // value = bytes of extra traffic
	PromoTypeUnlimited    = "unlimited"     // value = ignored, grants unlimited subscription with monthly traffic reset
)

// CreatePromoCode creates a new promo code
func (db *DB) CreatePromoCode(code, promoType string, value, maxUses int, validUntil *time.Time) (*PromoCode, error) {
	result, err := db.conn.Exec(
		`INSERT INTO promo_codes (code, type, value, max_uses, valid_until)
		 VALUES (?, ?, ?, ?, ?)`,
		code, promoType, value, maxUses, validUntil,
	)
	if err != nil {
		return nil, fmt.Errorf("failed to create promo code: %w", err)
	}

	id, err := result.LastInsertId()
	if err != nil {
		return nil, fmt.Errorf("failed to get last insert id: %w", err)
	}

	return db.GetPromoCodeByID(id)
}

// GetPromoCodeByID retrieves a promo code by ID
func (db *DB) GetPromoCodeByID(id int64) (*PromoCode, error) {
	return db.scanPromoCode(db.conn.QueryRow(
		`SELECT id, code, type, value, max_uses, used_count, valid_until, created_at
		 FROM promo_codes WHERE id = ?`, id,
	))
}

// GetPromoCodeByCode retrieves a promo code by code string
func (db *DB) GetPromoCodeByCode(code string) (*PromoCode, error) {
	return db.scanPromoCode(db.conn.QueryRow(
		`SELECT id, code, type, value, max_uses, used_count, valid_until, created_at
		 FROM promo_codes WHERE code = ?`, code,
	))
}

// scanPromoCode scans a single promo code row
func (db *DB) scanPromoCode(row *sql.Row) (*PromoCode, error) {
	var promo PromoCode
	var validUntil sql.NullTime

	err := row.Scan(
		&promo.ID, &promo.Code, &promo.Type, &promo.Value,
		&promo.MaxUses, &promo.UsedCount, &validUntil, &promo.CreatedAt,
	)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("failed to scan promo code: %w", err)
	}

	if validUntil.Valid {
		promo.ValidUntil = &validUntil.Time
	}

	return &promo, nil
}

// GetAllPromoCodes retrieves all promo codes
func (db *DB) GetAllPromoCodes() ([]PromoCode, error) {
	rows, err := db.conn.Query(
		`SELECT id, code, type, value, max_uses, used_count, valid_until, created_at
		 FROM promo_codes ORDER BY created_at DESC`,
	)
	if err != nil {
		return nil, fmt.Errorf("failed to query promo codes: %w", err)
	}
	defer rows.Close()

	return db.scanPromoCodes(rows)
}

// GetActivePromoCodes retrieves valid and available promo codes
func (db *DB) GetActivePromoCodes() ([]PromoCode, error) {
	rows, err := db.conn.Query(
		`SELECT id, code, type, value, max_uses, used_count, valid_until, created_at
		 FROM promo_codes
		 WHERE used_count < max_uses
		   AND (valid_until IS NULL OR valid_until > ?)
		 ORDER BY created_at DESC`,
		time.Now(),
	)
	if err != nil {
		return nil, fmt.Errorf("failed to query active promo codes: %w", err)
	}
	defer rows.Close()

	return db.scanPromoCodes(rows)
}

// scanPromoCodes scans multiple promo code rows
func (db *DB) scanPromoCodes(rows *sql.Rows) ([]PromoCode, error) {
	var promos []PromoCode
	for rows.Next() {
		var promo PromoCode
		var validUntil sql.NullTime

		if err := rows.Scan(
			&promo.ID, &promo.Code, &promo.Type, &promo.Value,
			&promo.MaxUses, &promo.UsedCount, &validUntil, &promo.CreatedAt,
		); err != nil {
			return nil, fmt.Errorf("failed to scan promo code: %w", err)
		}

		if validUntil.Valid {
			promo.ValidUntil = &validUntil.Time
		}

		promos = append(promos, promo)
	}

	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("rows error: %w", err)
	}

	return promos, nil
}

// ValidatePromoCode checks if a promo code can be used by a user
func (db *DB) ValidatePromoCode(code string, userID int64) (*PromoCode, error) {
	promo, err := db.GetPromoCodeByCode(code)
	if err != nil {
		return nil, err
	}
	if promo == nil {
		return nil, fmt.Errorf("promo code not found")
	}

	// Check if expired
	if promo.ValidUntil != nil && promo.ValidUntil.Before(time.Now()) {
		return nil, fmt.Errorf("promo code expired")
	}

	// Check if max uses reached
	if promo.UsedCount >= promo.MaxUses {
		return nil, fmt.Errorf("promo code usage limit reached")
	}

	// Check if user already used this promo
	used, err := db.HasUserUsedPromo(userID, promo.ID)
	if err != nil {
		return nil, err
	}
	if used {
		return nil, fmt.Errorf("promo code already used")
	}

	return promo, nil
}

// UsePromoCode records promo code usage and increments counter
func (db *DB) UsePromoCode(userID, promoID int64) error {
	tx, err := db.conn.Begin()
	if err != nil {
		return fmt.Errorf("failed to begin transaction: %w", err)
	}
	defer tx.Rollback()

	// Record usage
	_, err = tx.Exec(
		`INSERT INTO promo_uses (user_id, promo_id) VALUES (?, ?)`,
		userID, promoID,
	)
	if err != nil {
		return fmt.Errorf("failed to record promo use: %w", err)
	}

	// Increment counter
	_, err = tx.Exec(
		`UPDATE promo_codes SET used_count = used_count + 1 WHERE id = ?`,
		promoID,
	)
	if err != nil {
		return fmt.Errorf("failed to increment promo counter: %w", err)
	}

	if err := tx.Commit(); err != nil {
		return fmt.Errorf("failed to commit transaction: %w", err)
	}

	return nil
}

// HasUserUsedPromo checks if user has already used a promo code
func (db *DB) HasUserUsedPromo(userID, promoID int64) (bool, error) {
	var exists bool
	err := db.conn.QueryRow(
		`SELECT EXISTS(SELECT 1 FROM promo_uses WHERE user_id = ? AND promo_id = ?)`,
		userID, promoID,
	).Scan(&exists)
	if err != nil {
		return false, fmt.Errorf("failed to check promo usage: %w", err)
	}
	return exists, nil
}

// GetUserPromoUses retrieves all promo codes used by a user
func (db *DB) GetUserPromoUses(userID int64) ([]PromoCode, error) {
	rows, err := db.conn.Query(
		`SELECT p.id, p.code, p.type, p.value, p.max_uses, p.used_count, p.valid_until, p.created_at
		 FROM promo_codes p
		 JOIN promo_uses pu ON p.id = pu.promo_id
		 WHERE pu.user_id = ?
		 ORDER BY pu.used_at DESC`,
		userID,
	)
	if err != nil {
		return nil, fmt.Errorf("failed to query user promo uses: %w", err)
	}
	defer rows.Close()

	return db.scanPromoCodes(rows)
}

// DeletePromoCode deletes a promo code
func (db *DB) DeletePromoCode(id int64) error {
	tx, err := db.conn.Begin()
	if err != nil {
		return fmt.Errorf("failed to begin transaction: %w", err)
	}
	defer tx.Rollback()

	// Delete uses first
	_, err = tx.Exec(`DELETE FROM promo_uses WHERE promo_id = ?`, id)
	if err != nil {
		return fmt.Errorf("failed to delete promo uses: %w", err)
	}

	// Delete promo code
	_, err = tx.Exec(`DELETE FROM promo_codes WHERE id = ?`, id)
	if err != nil {
		return fmt.Errorf("failed to delete promo code: %w", err)
	}

	if err := tx.Commit(); err != nil {
		return fmt.Errorf("failed to commit transaction: %w", err)
	}

	return nil
}
