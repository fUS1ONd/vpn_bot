package database

import (
	"database/sql"
	"time"
)

// ModeratorEarning представляет запись начисления модератору
type ModeratorEarning struct {
	ID            int64
	PaymentID     int64
	ModeratorID   int64
	GrossAmount   int // Сумма платежа
	PlategaFee    int // Комиссия Platega
	WithdrawalFee int // Комиссия вывода (2%)
	NetAmount     int // Чистый доход после всех комиссий
	SharePercent  int // Процент доли модератора
	ShareAmount   int // Сумма доли модератора
	CreatedAt     time.Time
}

// CreateEarning создаёт запись начисления модератору
func (db *DB) CreateEarning(e *ModeratorEarning) (int64, error) {
	res, err := db.conn.Exec(
		`INSERT INTO moderator_earnings (payment_id, moderator_id, gross_amount, platega_fee, withdrawal_fee, net_amount, share_percent, share_amount)
		 SELECT ?, ?, ?, ?, ?, ?, ?, ?
		 WHERE NOT EXISTS (SELECT 1 FROM moderator_earnings WHERE payment_id = ?)`,
		e.PaymentID, e.ModeratorID, e.GrossAmount, e.PlategaFee, e.WithdrawalFee, e.NetAmount, e.SharePercent, e.ShareAmount, e.PaymentID,
	)
	if err != nil {
		return 0, err
	}
	return res.LastInsertId()
}

// MonthlyEarnings содержит агрегированные данные о доходах за месяц
type MonthlyEarnings struct {
	TotalPayments    int // Количество платежей
	GrossAmount      int // Сумма платежей
	TotalPlategaFee  int // Суммарная комиссия Platega
	TotalWithdrawal  int // Суммарная комиссия вывода
	TotalNetAmount   int // Суммарный чистый доход
	TotalShareAmount int // Суммарная доля модератора
	SharePercent     int // Последний актуальный процент
}

// GetModeratorEarningsByMonth возвращает агрегированные данные за месяц для модератора
func (db *DB) GetModeratorEarningsByMonth(moderatorID int64, year int, month int) (*MonthlyEarnings, error) {
	start := time.Date(year, time.Month(month), 1, 0, 0, 0, 0, time.UTC)
	end := start.AddDate(0, 1, 0)

	me := &MonthlyEarnings{}
	err := db.conn.QueryRow(
		`SELECT COUNT(*), COALESCE(SUM(me.gross_amount), 0), COALESCE(SUM(me.platega_fee), 0),
		        COALESCE(SUM(me.withdrawal_fee), 0), COALESCE(SUM(me.net_amount), 0), COALESCE(SUM(me.share_amount), 0)
		 FROM moderator_earnings me
		 JOIN payments p ON p.id = me.payment_id
		 WHERE me.moderator_id = ? AND p.confirmed_at >= ? AND p.confirmed_at < ?`,
		moderatorID, start, end,
	).Scan(&me.TotalPayments, &me.GrossAmount, &me.TotalPlategaFee, &me.TotalWithdrawal, &me.TotalNetAmount, &me.TotalShareAmount)
	if err != nil {
		return nil, err
	}

	// Получаем актуальный процент (из последнего начисления модератора)
	var pct sql.NullInt64
	db.conn.QueryRow(
		`SELECT me.share_percent
		 FROM moderator_earnings me
		 JOIN payments p ON p.id = me.payment_id
		 WHERE me.moderator_id = ? AND p.confirmed_at IS NOT NULL
		 ORDER BY p.confirmed_at DESC, me.id DESC
		 LIMIT 1`,
		moderatorID,
	).Scan(&pct)
	if pct.Valid {
		me.SharePercent = int(pct.Int64)
	}

	return me, nil
}

// GetModeratorTotalEarnings возвращает суммарную долю модератора за всё время
func (db *DB) GetModeratorTotalEarnings(moderatorID int64) (int, error) {
	var sum sql.NullInt64
	err := db.conn.QueryRow(
		`SELECT COALESCE(SUM(share_amount), 0) FROM moderator_earnings WHERE moderator_id = ?`,
		moderatorID,
	).Scan(&sum)
	if sum.Valid {
		return int(sum.Int64), err
	}
	return 0, err
}

// GetAllEarningsByMonth возвращает общую статистику начислений за месяц по дате подтверждения платежа
func (db *DB) GetAllEarningsByMonth(year int, month int) (*MonthlyEarnings, error) {
	start := time.Date(year, time.Month(month), 1, 0, 0, 0, 0, time.UTC)
	end := start.AddDate(0, 1, 0)

	me := &MonthlyEarnings{}
	err := db.conn.QueryRow(
		`SELECT COUNT(*), COALESCE(SUM(me.gross_amount), 0), COALESCE(SUM(me.platega_fee), 0),
		        COALESCE(SUM(me.withdrawal_fee), 0), COALESCE(SUM(me.net_amount), 0), COALESCE(SUM(me.share_amount), 0)
		 FROM moderator_earnings me
		 JOIN payments p ON p.id = me.payment_id
		 WHERE p.confirmed_at >= ? AND p.confirmed_at < ?`,
		start, end,
	).Scan(&me.TotalPayments, &me.GrossAmount, &me.TotalPlategaFee, &me.TotalWithdrawal, &me.TotalNetAmount, &me.TotalShareAmount)
	return me, err
}
