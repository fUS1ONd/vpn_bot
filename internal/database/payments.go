package database

import (
	"database/sql"
	"time"
)

// Payment представляет запись платежа
type Payment struct {
	ID                   int64
	TelegramID           int64
	ModeratorID          *int64
	Amount               int
	PaymentMethod        string // "sbp", "card", "crypto"
	Status               string // "pending", "confirmed", "expired", "canceled", "chargebacked", "confirmed_not_activated"
	PlategaTransactionID *string
	RedirectURL          *string
	ExpiresAt            *time.Time
	CreatedAt            time.Time
	ConfirmedAt          *time.Time
}

// CreatePayment создаёт новый платёж
func (db *DB) CreatePayment(p *Payment) (int64, error) {
	res, err := db.conn.Exec(
		`INSERT INTO payments (telegram_id, moderator_id, amount, payment_method, status, platega_transaction_id, redirect_url, expires_at)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?)`,
		p.TelegramID, p.ModeratorID, p.Amount, p.PaymentMethod, p.Status, p.PlategaTransactionID, p.RedirectURL, p.ExpiresAt,
	)
	if err != nil {
		return 0, err
	}
	return res.LastInsertId()
}

// GetPaymentByID возвращает платёж по ID
func (db *DB) GetPaymentByID(id int64) (*Payment, error) {
	p := &Payment{}
	var modID sql.NullInt64
	var txID sql.NullString
	var redirectURL sql.NullString
	var expiresAt sql.NullTime
	var confirmedAt sql.NullTime

	err := db.conn.QueryRow(
		`SELECT id, telegram_id, moderator_id, amount, payment_method, status, platega_transaction_id, redirect_url, expires_at, created_at, confirmed_at
		 FROM payments WHERE id = ?`, id,
	).Scan(&p.ID, &p.TelegramID, &modID, &p.Amount, &p.PaymentMethod, &p.Status, &txID, &redirectURL, &expiresAt, &p.CreatedAt, &confirmedAt)

	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}

	if modID.Valid {
		p.ModeratorID = &modID.Int64
	}
	if txID.Valid {
		p.PlategaTransactionID = &txID.String
	}
	if redirectURL.Valid {
		p.RedirectURL = &redirectURL.String
	}
	if expiresAt.Valid {
		p.ExpiresAt = &expiresAt.Time
	}
	if confirmedAt.Valid {
		p.ConfirmedAt = &confirmedAt.Time
	}

	return p, nil
}

// GetPendingPayment возвращает активный PENDING платёж пользователя (не протухший)
func (db *DB) GetPendingPayment(telegramID int64) (*Payment, error) {
	p := &Payment{}
	var modID sql.NullInt64
	var txID sql.NullString
	var redirectURL sql.NullString
	var expiresAt sql.NullTime
	var confirmedAt sql.NullTime

	err := db.conn.QueryRow(
		`SELECT id, telegram_id, moderator_id, amount, payment_method, status, platega_transaction_id, redirect_url, expires_at, created_at, confirmed_at
		 FROM payments WHERE telegram_id = ? AND status = 'pending' AND (expires_at IS NULL OR expires_at > datetime('now'))
		 ORDER BY created_at DESC LIMIT 1`, telegramID,
	).Scan(&p.ID, &p.TelegramID, &modID, &p.Amount, &p.PaymentMethod, &p.Status, &txID, &redirectURL, &expiresAt, &p.CreatedAt, &confirmedAt)

	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}

	if modID.Valid {
		p.ModeratorID = &modID.Int64
	}
	if txID.Valid {
		p.PlategaTransactionID = &txID.String
	}
	if redirectURL.Valid {
		p.RedirectURL = &redirectURL.String
	}
	if expiresAt.Valid {
		p.ExpiresAt = &expiresAt.Time
	}
	if confirmedAt.Valid {
		p.ConfirmedAt = &confirmedAt.Time
	}

	return p, nil
}

// GetPaymentByPlategaTxID возвращает платёж по ID транзакции Platega
func (db *DB) GetPaymentByPlategaTxID(txID string) (*Payment, error) {
	p := &Payment{}
	var modID sql.NullInt64
	var txIDNull sql.NullString
	var redirectURL sql.NullString
	var expiresAt sql.NullTime
	var confirmedAt sql.NullTime

	err := db.conn.QueryRow(
		`SELECT id, telegram_id, moderator_id, amount, payment_method, status, platega_transaction_id, redirect_url, expires_at, created_at, confirmed_at
		 FROM payments WHERE platega_transaction_id = ?`, txID,
	).Scan(&p.ID, &p.TelegramID, &modID, &p.Amount, &p.PaymentMethod, &p.Status, &txIDNull, &redirectURL, &expiresAt, &p.CreatedAt, &confirmedAt)

	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}

	if modID.Valid {
		p.ModeratorID = &modID.Int64
	}
	if txIDNull.Valid {
		p.PlategaTransactionID = &txIDNull.String
	}
	if redirectURL.Valid {
		p.RedirectURL = &redirectURL.String
	}
	if expiresAt.Valid {
		p.ExpiresAt = &expiresAt.Time
	}
	if confirmedAt.Valid {
		p.ConfirmedAt = &confirmedAt.Time
	}

	return p, nil
}

// UpdatePaymentStatus обновляет статус платежа
func (db *DB) UpdatePaymentStatus(id int64, status string) error {
	_, err := db.conn.Exec(`UPDATE payments SET status = ? WHERE id = ?`, status, id)
	return err
}

// ConfirmPayment помечает платёж как confirmed с датой подтверждения
func (db *DB) ConfirmPayment(id int64) error {
	_, err := db.conn.Exec(
		`UPDATE payments SET status = 'confirmed', confirmed_at = datetime('now') WHERE id = ?`, id,
	)
	return err
}

// ExpireOldPendingPayments помечает протухшие PENDING как expired
func (db *DB) ExpireOldPendingPayments() (int64, error) {
	res, err := db.conn.Exec(
		`UPDATE payments SET status = 'expired' WHERE status = 'pending' AND expires_at <= datetime('now')`,
	)
	if err != nil {
		return 0, err
	}
	return res.RowsAffected()
}

// GetConfirmedNotActivated возвращает платежи со статусом confirmed_not_activated
func (db *DB) GetConfirmedNotActivated() ([]Payment, error) {
	rows, err := db.conn.Query(
		`SELECT id, telegram_id, moderator_id, amount, payment_method, status, platega_transaction_id, redirect_url, expires_at, created_at, confirmed_at
		 FROM payments WHERE status = 'confirmed_not_activated'`,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var payments []Payment
	for rows.Next() {
		var p Payment
		var modID sql.NullInt64
		var txID sql.NullString
		var redirectURL sql.NullString
		var expiresAt sql.NullTime
		var confirmedAt sql.NullTime

		if err := rows.Scan(&p.ID, &p.TelegramID, &modID, &p.Amount, &p.PaymentMethod, &p.Status, &txID, &redirectURL, &expiresAt, &p.CreatedAt, &confirmedAt); err != nil {
			return nil, err
		}

		if modID.Valid {
			p.ModeratorID = &modID.Int64
		}
		if txID.Valid {
			p.PlategaTransactionID = &txID.String
		}
		if redirectURL.Valid {
			p.RedirectURL = &redirectURL.String
		}
		if expiresAt.Valid {
			p.ExpiresAt = &expiresAt.Time
		}
		if confirmedAt.Valid {
			p.ConfirmedAt = &confirmedAt.Time
		}

		payments = append(payments, p)
	}
	return payments, rows.Err()
}

// HasConfirmedPayment проверяет, была ли у пользователя хотя бы одна подтверждённая оплата
func (db *DB) HasConfirmedPayment(telegramID int64) (bool, error) {
	var exists bool
	err := db.conn.QueryRow(
		`SELECT EXISTS(SELECT 1 FROM payments WHERE telegram_id = ? AND status = 'confirmed')`, telegramID,
	).Scan(&exists)
	return exists, err
}

// HasConfirmedPaymentSince проверяет, есть ли подтверждённый платёж после указанной даты.
// Используется scheduler для защиты от ложного кика/disable — если пользователь оплатил
// после expireAt, подписка уже активирована через callback.
func (db *DB) HasConfirmedPaymentSince(telegramID int64, since time.Time) (bool, error) {
	var exists bool
	err := db.conn.QueryRow(
		`SELECT EXISTS(SELECT 1 FROM payments WHERE telegram_id = ? AND status = 'confirmed' AND confirmed_at >= ?)`,
		telegramID, since,
	).Scan(&exists)
	return exists, err
}

// CountConfirmedPaymentsByMonth считает платежи за месяц (для статистики)
func (db *DB) CountConfirmedPaymentsByMonth(year int, month int) (int, error) {
	var count int
	start := time.Date(year, time.Month(month), 1, 0, 0, 0, 0, time.UTC)
	end := start.AddDate(0, 1, 0)
	err := db.conn.QueryRow(
		`SELECT COUNT(*) FROM payments WHERE status = 'confirmed' AND confirmed_at >= ? AND confirmed_at < ?`,
		start, end,
	).Scan(&count)
	return count, err
}

// SumConfirmedPaymentsByMonth возвращает сумму платежей за месяц
func (db *DB) SumConfirmedPaymentsByMonth(year int, month int) (int, error) {
	var sum int
	start := time.Date(year, time.Month(month), 1, 0, 0, 0, 0, time.UTC)
	end := start.AddDate(0, 1, 0)
	err := db.conn.QueryRow(
		`SELECT COALESCE(SUM(amount), 0) FROM payments WHERE status = 'confirmed' AND confirmed_at >= ? AND confirmed_at < ?`,
		start, end,
	).Scan(&sum)
	return sum, err
}

// CountTrialsByMonth считает триалы (активации модераторских инвайтов) за месяц
func (db *DB) CountTrialsByMonth(year int, month int) (int, error) {
	var count int
	start := time.Date(year, time.Month(month), 1, 0, 0, 0, 0, time.UTC)
	end := start.AddDate(0, 1, 0)
	err := db.conn.QueryRow(
		`SELECT COUNT(*) FROM invites WHERE used_at >= ? AND used_at < ? AND expire_days IS NOT NULL`,
		start, end,
	).Scan(&count)
	return count, err
}

// CountFirstPaymentsByMonth считает первые оплаты (конверсия триал→оплата) за месяц
func (db *DB) CountFirstPaymentsByMonth(year int, month int) (int, error) {
	var count int
	start := time.Date(year, time.Month(month), 1, 0, 0, 0, 0, time.UTC)
	end := start.AddDate(0, 1, 0)
	// Считаем пользователей, у которых первый confirmed платёж попал в этот месяц
	err := db.conn.QueryRow(
		`SELECT COUNT(*) FROM (
		    SELECT telegram_id, MIN(confirmed_at) as first_payment
		    FROM payments WHERE status = 'confirmed'
		    GROUP BY telegram_id
		    HAVING first_payment >= ? AND first_payment < ?
		)`, start, end,
	).Scan(&count)
	return count, err
}

// CountPayingSubscribersByModerator считает активных платящих подписчиков модератора
func (db *DB) CountPayingSubscribersByModerator(moderatorID int64) (int, error) {
	var count int
	err := db.conn.QueryRow(
		`SELECT COUNT(DISTINCT u.telegram_id) FROM users u
		 JOIN payments p ON p.telegram_id = u.telegram_id
		 WHERE u.moderator_id = ? AND p.status = 'confirmed'
		 AND p.confirmed_at >= datetime('now', '-60 days')`,
		moderatorID,
	).Scan(&count)
	return count, err
}
