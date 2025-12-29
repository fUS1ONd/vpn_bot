package database

import (
	"database/sql"
	"fmt"
	"time"
)

// Payment statuses
const (
	PaymentStatusPending   = "pending"
	PaymentStatusSucceeded = "succeeded"
	PaymentStatusFailed    = "failed"
)

// Payment types
const (
	PaymentTypeMonthly    = "monthly"
	PaymentTypeTraffic10G = "traffic_10gb"
	PaymentTypePromo      = "promo"
)

// CreatePayment creates a new payment record
func (db *DB) CreatePayment(userID int64, paymentID, provider string, amount int, paymentType string, promoCode *string) (*Payment, error) {
	result, err := db.conn.Exec(
		`INSERT INTO payments (user_id, payment_id, provider, amount, type, promo_code)
		 VALUES (?, ?, ?, ?, ?, ?)`,
		userID, paymentID, provider, amount, paymentType, promoCode,
	)
	if err != nil {
		return nil, fmt.Errorf("failed to create payment: %w", err)
	}

	id, err := result.LastInsertId()
	if err != nil {
		return nil, fmt.Errorf("failed to get last insert id: %w", err)
	}

	return db.GetPaymentByID(id)
}

// GetPaymentByID retrieves a payment by ID
func (db *DB) GetPaymentByID(id int64) (*Payment, error) {
	return db.scanPayment(db.conn.QueryRow(
		`SELECT id, user_id, payment_id, provider, amount, status, type, promo_code, created_at, confirmed_at
		 FROM payments WHERE id = ?`, id,
	))
}

// GetPaymentByPaymentID retrieves a payment by external payment ID
func (db *DB) GetPaymentByPaymentID(paymentID string) (*Payment, error) {
	return db.scanPayment(db.conn.QueryRow(
		`SELECT id, user_id, payment_id, provider, amount, status, type, promo_code, created_at, confirmed_at
		 FROM payments WHERE payment_id = ?`, paymentID,
	))
}

// scanPayment scans a single payment row
func (db *DB) scanPayment(row *sql.Row) (*Payment, error) {
	var payment Payment
	var promoCode sql.NullString
	var confirmedAt sql.NullTime

	err := row.Scan(
		&payment.ID, &payment.UserID, &payment.PaymentID, &payment.Provider,
		&payment.Amount, &payment.Status, &payment.Type, &promoCode,
		&payment.CreatedAt, &confirmedAt,
	)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("failed to scan payment: %w", err)
	}

	if promoCode.Valid {
		payment.PromoCode = &promoCode.String
	}
	if confirmedAt.Valid {
		payment.ConfirmedAt = &confirmedAt.Time
	}

	return &payment, nil
}

// GetPaymentsByUserID retrieves all payments for a user
func (db *DB) GetPaymentsByUserID(userID int64) ([]Payment, error) {
	rows, err := db.conn.Query(
		`SELECT id, user_id, payment_id, provider, amount, status, type, promo_code, created_at, confirmed_at
		 FROM payments WHERE user_id = ? ORDER BY created_at DESC`, userID,
	)
	if err != nil {
		return nil, fmt.Errorf("failed to query payments: %w", err)
	}
	defer rows.Close()

	return db.scanPayments(rows)
}

// GetPendingPayments retrieves all pending payments
func (db *DB) GetPendingPayments() ([]Payment, error) {
	rows, err := db.conn.Query(
		`SELECT id, user_id, payment_id, provider, amount, status, type, promo_code, created_at, confirmed_at
		 FROM payments WHERE status = ? ORDER BY created_at`, PaymentStatusPending,
	)
	if err != nil {
		return nil, fmt.Errorf("failed to query pending payments: %w", err)
	}
	defer rows.Close()

	return db.scanPayments(rows)
}

// scanPayments scans multiple payment rows
func (db *DB) scanPayments(rows *sql.Rows) ([]Payment, error) {
	var payments []Payment
	for rows.Next() {
		var payment Payment
		var promoCode sql.NullString
		var confirmedAt sql.NullTime

		if err := rows.Scan(
			&payment.ID, &payment.UserID, &payment.PaymentID, &payment.Provider,
			&payment.Amount, &payment.Status, &payment.Type, &promoCode,
			&payment.CreatedAt, &confirmedAt,
		); err != nil {
			return nil, fmt.Errorf("failed to scan payment: %w", err)
		}

		if promoCode.Valid {
			payment.PromoCode = &promoCode.String
		}
		if confirmedAt.Valid {
			payment.ConfirmedAt = &confirmedAt.Time
		}

		payments = append(payments, payment)
	}

	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("rows error: %w", err)
	}

	return payments, nil
}

// ConfirmPayment marks a payment as succeeded
func (db *DB) ConfirmPayment(paymentID string) error {
	result, err := db.conn.Exec(
		`UPDATE payments SET status = ?, confirmed_at = ? WHERE payment_id = ?`,
		PaymentStatusSucceeded, time.Now(), paymentID,
	)
	if err != nil {
		return fmt.Errorf("failed to confirm payment: %w", err)
	}

	rows, err := result.RowsAffected()
	if err != nil {
		return fmt.Errorf("failed to get rows affected: %w", err)
	}
	if rows == 0 {
		return fmt.Errorf("payment not found: %s", paymentID)
	}

	return nil
}

// FailPayment marks a payment as failed
func (db *DB) FailPayment(paymentID string) error {
	result, err := db.conn.Exec(
		`UPDATE payments SET status = ? WHERE payment_id = ?`,
		PaymentStatusFailed, paymentID,
	)
	if err != nil {
		return fmt.Errorf("failed to fail payment: %w", err)
	}

	rows, err := result.RowsAffected()
	if err != nil {
		return fmt.Errorf("failed to get rows affected: %w", err)
	}
	if rows == 0 {
		return fmt.Errorf("payment not found: %s", paymentID)
	}

	return nil
}

// GetUserTotalPaid returns the total amount paid by a user
func (db *DB) GetUserTotalPaid(userID int64) (int, error) {
	var total sql.NullInt64
	err := db.conn.QueryRow(
		`SELECT SUM(amount) FROM payments WHERE user_id = ? AND status = ?`,
		userID, PaymentStatusSucceeded,
	).Scan(&total)
	if err != nil {
		return 0, fmt.Errorf("failed to get total paid: %w", err)
	}
	if !total.Valid {
		return 0, nil
	}
	return int(total.Int64), nil
}
