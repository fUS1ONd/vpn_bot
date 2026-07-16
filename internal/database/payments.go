package database

import (
	"database/sql"
	"time"
)

// Payment представляет запись платежа
type Payment struct {
	ID                     int64
	TelegramID             int64
	ModeratorID            *int64
	Amount                 int
	PaymentMethod          string // "sbp", "card", "crypto"
	Status                 string // "pending", "confirmed", "expired", "canceled", "chargebacked", "confirmed_not_activated"
	PlategaTransactionID   *string
	Provider               string
	ProviderPaymentID      *string
	ProviderRequestKey     *string
	ProviderFeeBasisPoints *int // сотые доли процента, например 350 = 3.5%
	RedirectURL            *string
	ExpiresAt              *time.Time
	CreatedAt              time.Time
	ConfirmedAt            *time.Time
}

// MonthlyConfirmedPayment хранит подтверждённый платёж месяца и долю модератора.
type MonthlyConfirmedPayment struct {
	Payment
	ShareAmount int
}

// CreatePayment создаёт новый платёж
func (db *DB) CreatePayment(p *Payment) (int64, error) {
	if p.Provider == "" {
		p.Provider = "platega"
	}
	if p.ProviderPaymentID == nil && p.PlategaTransactionID != nil {
		p.ProviderPaymentID = p.PlategaTransactionID
	}
	res, err := db.conn.Exec(
		`INSERT INTO payments (telegram_id, moderator_id, amount, payment_method, status, platega_transaction_id, provider, provider_payment_id, provider_request_key, provider_fee_percent, redirect_url, expires_at)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		p.TelegramID, p.ModeratorID, p.Amount, p.PaymentMethod, p.Status, p.PlategaTransactionID, p.Provider, p.ProviderPaymentID, p.ProviderRequestKey, p.ProviderFeeBasisPoints, p.RedirectURL, p.ExpiresAt,
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
	var redirectURL, providerPaymentID, providerRequestKey sql.NullString
	var providerFeePercent sql.NullInt64
	var expiresAt sql.NullTime
	var confirmedAt sql.NullTime

	err := db.conn.QueryRow(
		`SELECT id, telegram_id, moderator_id, amount, payment_method, status, platega_transaction_id, provider, provider_payment_id, provider_request_key, provider_fee_percent, redirect_url, expires_at, created_at, confirmed_at
		 FROM payments WHERE id = ?`, id,
	).Scan(&p.ID, &p.TelegramID, &modID, &p.Amount, &p.PaymentMethod, &p.Status, &txID, &p.Provider, &providerPaymentID, &providerRequestKey, &providerFeePercent, &redirectURL, &expiresAt, &p.CreatedAt, &confirmedAt)

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
	if providerPaymentID.Valid {
		p.ProviderPaymentID = &providerPaymentID.String
	}
	if providerRequestKey.Valid {
		p.ProviderRequestKey = &providerRequestKey.String
	}
	if providerFeePercent.Valid {
		v := int(providerFeePercent.Int64)
		p.ProviderFeeBasisPoints = &v
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
	var redirectURL, providerPaymentID, providerRequestKey sql.NullString
	var providerFeePercent sql.NullInt64
	var expiresAt sql.NullTime
	var confirmedAt sql.NullTime

	err := db.conn.QueryRow(
		`SELECT id, telegram_id, moderator_id, amount, payment_method, status, platega_transaction_id, provider, provider_payment_id, provider_request_key, provider_fee_percent, redirect_url, expires_at, created_at, confirmed_at
		 FROM payments WHERE telegram_id = ? AND status = 'pending' AND (expires_at IS NULL OR datetime(expires_at) > datetime('now'))
		 ORDER BY created_at DESC LIMIT 1`, telegramID,
	).Scan(&p.ID, &p.TelegramID, &modID, &p.Amount, &p.PaymentMethod, &p.Status, &txID, &p.Provider, &providerPaymentID, &providerRequestKey, &providerFeePercent, &redirectURL, &expiresAt, &p.CreatedAt, &confirmedAt)

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
	if providerPaymentID.Valid {
		p.ProviderPaymentID = &providerPaymentID.String
	}
	if providerRequestKey.Valid {
		p.ProviderRequestKey = &providerRequestKey.String
	}
	if providerFeePercent.Valid {
		v := int(providerFeePercent.Int64)
		p.ProviderFeeBasisPoints = &v
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
	return db.GetPaymentByProviderPaymentID("platega", txID)
}

// GetPaymentByProviderPaymentID retrieves a payment by its provider-owned immutable ID.
func (db *DB) GetPaymentByProviderPaymentID(provider, providerPaymentID string) (*Payment, error) {
	var id int64
	err := db.conn.QueryRow(`SELECT id FROM payments WHERE provider = ? AND provider_payment_id = ?`, provider, providerPaymentID).Scan(&id)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return db.GetPaymentByID(id)
}

// SetProviderPaymentDetails stores the external ID and redirect details after provider creation.
func (db *DB) SetProviderPaymentDetails(id int64, providerPaymentID, redirectURL string, expiresAt *time.Time) error {
	_, err := db.conn.Exec(`UPDATE payments SET provider_payment_id = ?, redirect_url = ?, expires_at = ? WHERE id = ?`, providerPaymentID, redirectURL, expiresAt, id)
	return err
}

func (db *DB) UpdatePaymentMethod(id int64, paymentMethod string) error {
	_, err := db.conn.Exec(`UPDATE payments SET payment_method = ? WHERE id = ?`, paymentMethod, id)
	return err
}

// UpdatePaymentStatus обновляет статус платежа
func (db *DB) UpdatePaymentStatus(id int64, status string) error {
	_, err := db.conn.Exec(`UPDATE payments SET status = ? WHERE id = ?`, status, id)
	return err
}

// UpdatePaymentStatusIfNot обновляет статус платежа только если текущий статус не равен excludedStatus.
// Возвращает true если обновление произошло (строк изменено > 0), false если статус уже excludedStatus.
// Используется для атомарной idempotency при обработке chargeback.
func (db *DB) UpdatePaymentStatusIfNot(id int64, newStatus, excludedStatus string) (bool, error) {
	res, err := db.conn.Exec(
		`UPDATE payments SET status = ? WHERE id = ? AND status != ?`,
		newStatus, id, excludedStatus,
	)
	if err != nil {
		return false, err
	}
	affected, err := res.RowsAffected()
	if err != nil {
		return false, err
	}
	return affected > 0, nil
}

// ConfirmPayment помечает платёж как confirmed с датой подтверждения
func (db *DB) ConfirmPayment(id int64) error {
	_, err := db.conn.Exec(
		`UPDATE payments
		 SET status = 'confirmed',
		     confirmed_at = COALESCE(confirmed_at, datetime('now'))
		 WHERE id = ?`, id,
	)
	return err
}

// ExpireOldPendingPayments помечает протухшие PENDING как expired
func (db *DB) ExpireOldPendingPayments() (int64, error) {
	res, err := db.conn.Exec(
		`UPDATE payments SET status = 'expired' WHERE status = 'pending' AND datetime(expires_at) <= datetime('now')`,
	)
	if err != nil {
		return 0, err
	}
	return res.RowsAffected()
}

// GetConfirmedNotActivated возвращает платежи со статусом confirmed_not_activated
func (db *DB) GetConfirmedNotActivated() ([]Payment, error) {
	rows, err := db.conn.Query(
		`SELECT id, telegram_id, moderator_id, amount, payment_method, status, platega_transaction_id, provider, provider_payment_id, redirect_url, expires_at, created_at, confirmed_at
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
		var redirectURL, providerPaymentID sql.NullString
		var expiresAt sql.NullTime
		var confirmedAt sql.NullTime

		if err := rows.Scan(&p.ID, &p.TelegramID, &modID, &p.Amount, &p.PaymentMethod, &p.Status, &txID, &p.Provider, &providerPaymentID, &redirectURL, &expiresAt, &p.CreatedAt, &confirmedAt); err != nil {
			return nil, err
		}

		if modID.Valid {
			p.ModeratorID = &modID.Int64
		}
		if txID.Valid {
			p.PlategaTransactionID = &txID.String
		}
		if providerPaymentID.Valid {
			p.ProviderPaymentID = &providerPaymentID.String
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

// HasConfirmedPayment проверяет, была ли у пользователя хотя бы одна подтверждённая оплата.
// Для защитных проверок scheduler считаем оплатой и confirmed_not_activated:
// деньги уже подтверждены, пользователя нельзя считать неоплатившим.
func (db *DB) HasConfirmedPayment(telegramID int64) (bool, error) {
	var exists bool
	err := db.conn.QueryRow(
		`SELECT EXISTS(SELECT 1 FROM payments WHERE telegram_id = ? AND status IN ('confirmed', 'confirmed_not_activated'))`, telegramID,
	).Scan(&exists)
	return exists, err
}

// HasConfirmedPaymentSince проверяет, есть ли подтверждённый платёж после указанной даты.
// Используется scheduler для защиты от ложного кика/disable. Для этой проверки
// confirmed_not_activated тоже считается оплатой: callback уже подтвердил деньги,
// даже если активация в Remnawave ещё retry-ится.
func (db *DB) HasConfirmedPaymentSince(telegramID int64, since time.Time) (bool, error) {
	var exists bool
	err := db.conn.QueryRow(
		`SELECT EXISTS(SELECT 1 FROM payments WHERE telegram_id = ? AND status IN ('confirmed', 'confirmed_not_activated') AND confirmed_at >= ?)`,
		telegramID, since,
	).Scan(&exists)
	return exists, err
}

// GetConfirmedPaymentsByMonth возвращает финансово подтверждённые платежи за месяц.
// Платежи без moderator_earnings остаются в выборке, а доля модератора для них равна нулю.
func (db *DB) GetConfirmedPaymentsByMonth(year int, month int) ([]MonthlyConfirmedPayment, error) {
	start := time.Date(year, time.Month(month), 1, 0, 0, 0, 0, time.UTC)
	end := start.AddDate(0, 1, 0)

	rows, err := db.conn.Query(
		`SELECT p.id, p.telegram_id, p.moderator_id, p.amount, p.payment_method, p.status,
		        p.platega_transaction_id, p.provider, p.provider_payment_id, p.redirect_url, p.expires_at, p.created_at, p.confirmed_at,
		        COALESCE(me.share_amount, 0)
		 FROM payments p
		 LEFT JOIN (
		     SELECT payment_id, COALESCE(SUM(share_amount), 0) AS share_amount
		     FROM moderator_earnings
		     GROUP BY payment_id
		 ) me ON me.payment_id = p.id
		 WHERE p.confirmed_at >= ? AND p.confirmed_at < ?
		   AND p.status NOT IN ('chargebacked')
		 ORDER BY p.confirmed_at ASC, p.id ASC`,
		start, end,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var payments []MonthlyConfirmedPayment
	for rows.Next() {
		var p MonthlyConfirmedPayment
		var modID sql.NullInt64
		var txID sql.NullString
		var redirectURL, providerPaymentID sql.NullString
		var expiresAt sql.NullTime
		var confirmedAt sql.NullTime

		if err := rows.Scan(&p.ID, &p.TelegramID, &modID, &p.Amount, &p.PaymentMethod, &p.Status, &txID, &p.Provider, &providerPaymentID, &redirectURL, &expiresAt, &p.CreatedAt, &p.ConfirmedAt, &p.ShareAmount); err != nil {
			return nil, err
		}

		if modID.Valid {
			p.ModeratorID = &modID.Int64
		}
		if txID.Valid {
			p.PlategaTransactionID = &txID.String
		}
		if providerPaymentID.Valid {
			p.ProviderPaymentID = &providerPaymentID.String
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

// CountConfirmedPaymentsByMonth считает финансово подтверждённые платежи за месяц.
func (db *DB) CountConfirmedPaymentsByMonth(year int, month int) (int, error) {
	var count int
	start := time.Date(year, time.Month(month), 1, 0, 0, 0, 0, time.UTC)
	end := start.AddDate(0, 1, 0)
	err := db.conn.QueryRow(
		`SELECT COUNT(*) FROM payments WHERE confirmed_at >= ? AND confirmed_at < ? AND status NOT IN ('chargebacked')`,
		start, end,
	).Scan(&count)
	return count, err
}

// SumConfirmedPaymentsByMonth возвращает сумму финансово подтверждённых платежей за месяц.
func (db *DB) SumConfirmedPaymentsByMonth(year int, month int) (int, error) {
	var sum int
	start := time.Date(year, time.Month(month), 1, 0, 0, 0, 0, time.UTC)
	end := start.AddDate(0, 1, 0)
	err := db.conn.QueryRow(
		`SELECT COALESCE(SUM(amount), 0) FROM payments WHERE confirmed_at >= ? AND confirmed_at < ? AND status NOT IN ('chargebacked')`,
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
		`SELECT COUNT(*) FROM invites WHERE used_at >= ? AND used_at < ? AND is_trial = 1`,
		start, end,
	).Scan(&count)
	return count, err
}

// CountFirstPaymentsByMonth считает первые оплаты (конверсия триал→оплата) за месяц
func (db *DB) CountFirstPaymentsByMonth(year int, month int) (int, error) {
	var count int
	start := time.Date(year, time.Month(month), 1, 0, 0, 0, 0, time.UTC)
	end := start.AddDate(0, 1, 0)
	// Считаем пользователей, у которых первая финансово подтверждённая оплата попала в этот месяц.
	err := db.conn.QueryRow(
		`SELECT COUNT(*) FROM (
		    SELECT telegram_id, MIN(confirmed_at) as first_payment
		    FROM payments
		    WHERE confirmed_at IS NOT NULL AND status NOT IN ('chargebacked')
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
