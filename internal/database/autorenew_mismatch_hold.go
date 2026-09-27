package database

import (
	"database/sql"
	"fmt"
	"time"
)

// Удержание цикла: касса сказала «оплачено» по платежу автосписания, но ответ не
// сошёлся с записью. Деньги у человека списаны, подписка не продлена, и пока
// владелец не разобрал платёж, бот не отключает человека и не пугает его
// напоминаниями «продлите». Признак живёт в payments, а не в памяти процесса:
// перезапуск бота удержание не снимает. Цикл берётся из autorenew_attempts —
// так удержание само уходит, когда expireAt сдвинулся (подтверждение платежа,
// ручное продление владельцем).

// AutorenewMismatchHold — состояние удержания цикла.
type AutorenewMismatchHold struct {
	Active bool
	// ReleasedAt — когда владелец разобрал последний несовпавший платёж цикла;
	// nil, если не разбирал. От него, а не от expireAt, отсчитывается grace:
	// иначе разбор после трёх суток означал бы мгновенный кик.
	ReleasedAt *time.Time
}

// MarkPaidMismatch помечает платёж: касса сказала «оплачено», ответ не сошёлся.
// Повторная пометка момент первой не сдвигает.
func (db *DB) MarkPaidMismatch(paymentID int64) error {
	_, err := db.conn.Exec(
		`UPDATE payments SET paid_mismatch_at = COALESCE(paid_mismatch_at, ?) WHERE id = ?`,
		time.Now().UTC(), paymentID,
	)
	if err != nil {
		return fmt.Errorf("failed to mark paid mismatch: %w", err)
	}
	return nil
}

// AutorenewMismatchHold возвращает удержание цикла expireAt. Держит несовпавшее
// «оплачено» по платежу автосписания этого цикла, которое владелец не разобрал и
// которое не подтвердилось позже.
func (db *DB) AutorenewMismatchHold(telegramID int64, expireAt time.Time) (AutorenewMismatchHold, error) {
	var hold AutorenewMismatchHold
	cycle := autorenewCycleKey(expireAt)

	err := db.conn.QueryRow(
		`SELECT EXISTS(SELECT 1 FROM autorenew_attempts a JOIN payments p ON p.id = a.payment_id
		 WHERE a.telegram_id = ? AND a.expire_at = ?
		   AND p.paid_mismatch_at IS NOT NULL AND p.mismatch_resolved_at IS NULL
		   AND p.status NOT IN ('confirmed', 'confirmed_not_activated'))`,
		telegramID, cycle,
	).Scan(&hold.Active)
	if err != nil {
		return hold, fmt.Errorf("failed to check autorenew mismatch hold: %w", err)
	}

	var released sql.NullTime
	err = db.conn.QueryRow(
		`SELECT p.mismatch_resolved_at FROM autorenew_attempts a JOIN payments p ON p.id = a.payment_id
		 WHERE a.telegram_id = ? AND a.expire_at = ? AND p.mismatch_resolved_at IS NOT NULL
		 ORDER BY p.mismatch_resolved_at DESC LIMIT 1`,
		telegramID, cycle,
	).Scan(&released)
	if err != nil && err != sql.ErrNoRows {
		return hold, fmt.Errorf("failed to read autorenew mismatch release: %w", err)
	}
	if released.Valid {
		at := released.Time.UTC()
		hold.ReleasedAt = &at
	}
	return hold, nil
}

// ResolveAutorenewMismatches — владелец разобрал несовпавшие автосписания
// человека: удержание снимается со всех его циклов. Возвращает, сколько
// платежей разобрано.
func (db *DB) ResolveAutorenewMismatches(telegramID int64) (int64, error) {
	res, err := db.conn.Exec(
		`UPDATE payments SET mismatch_resolved_at = ?
		 WHERE telegram_id = ? AND paid_mismatch_at IS NOT NULL AND mismatch_resolved_at IS NULL
		   AND id IN (SELECT payment_id FROM autorenew_attempts WHERE payment_id IS NOT NULL)`,
		time.Now().UTC(), telegramID,
	)
	if err != nil {
		return 0, fmt.Errorf("failed to resolve autorenew mismatches: %w", err)
	}
	return res.RowsAffected()
}
