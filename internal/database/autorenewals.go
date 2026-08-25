package database

import (
	"database/sql"
	"fmt"
	"time"
)

// Автопродление живёт в двух таблицах, потому что описывает два независимых
// факта. `autorenewals` — согласие пользователя (Автопродление) и сохранённый у
// кассы инструмент (Способ автосписания). Они разъезжаются постоянно: карта
// отвалилась, а согласие осталось; способ сохранён у того, кто автопродление не
// включал. Сводить их к одному полю нельзя — «выключено» стало бы сразу тремя
// разными состояниями.
//
// `autorenew_attempts` — попытки списания, привязанные к конкретному значению
// expireAt. Не в notifications_sent: ClearNotifications зовётся при каждой
// успешной активации платежа и стёрла бы историю попыток ровно тогда, когда она
// нужнее всего.

// Исходы попытки автосписания.
const (
	// AutorenewOutcomeSuccess — касса списала, подписка продлена.
	AutorenewOutcomeSuccess = "success"
	// AutorenewOutcomeDeclined — касса отказала (нет денег, отклонено банком).
	// Способ при этом жив.
	AutorenewOutcomeDeclined = "declined"
	// AutorenewOutcomeMethodGone — способа у кассы больше нет. Гасит Способ,
	// но не согласие.
	AutorenewOutcomeMethodGone = "method_gone"
	// AutorenewOutcomeUnknown — исход неизвестен: pending или сетевой сбой без
	// ответа. Попытка израсходована, пользователю не пишем.
	AutorenewOutcomeUnknown = "unknown"
)

// Autorenewal — строка согласия и Способа. Одна на пользователя.
type Autorenewal struct {
	TelegramID      int64
	Enabled         bool
	PaymentMethodID *string
	MethodTitle     *string
	// PeriodMonths сегодня всегда 1: покупка более одного месяца за раз в объём
	// не входит. Поле заведено заранее, но ветвлений под другие значения в коде
	// нет и быть не должно.
	PeriodMonths int
	CreatedAt    time.Time
	UpdatedAt    *time.Time
}

// HasMethod сообщает, есть ли у пользователя Способ автосписания.
func (a *Autorenewal) HasMethod() bool {
	return a != nil && a.PaymentMethodID != nil && *a.PaymentMethodID != ""
}

// IsEnabled сообщает, дал ли пользователь согласие на автосписание.
func (a *Autorenewal) IsEnabled() bool {
	return a != nil && a.Enabled
}

// AutorenewAttempt — одно обращение к кассе в рамках одного цикла подписки.
type AutorenewAttempt struct {
	TelegramID int64
	// ExpireAt — цикл подписки, к которому привязана попытка. Сдвинулся
	// expireAt — новый цикл, попытки свежие.
	ExpireAt  time.Time
	AttemptNo int // 1 (T−24ч) или 2 (T−0)
	Outcome   string
	PaymentID *int64
	CreatedAt time.Time
}

// autorenewCycleKey приводит момент окончания подписки к стабильному виду:
// попытки ищутся по точному равенству, и лишние наносекунды из ответа панели
// развалили бы поиск.
func autorenewCycleKey(expireAt time.Time) time.Time {
	return expireAt.UTC().Truncate(time.Second)
}

// GetAutorenewal возвращает строку автопродления или nil, если её нет.
func (db *DB) GetAutorenewal(telegramID int64) (*Autorenewal, error) {
	a := &Autorenewal{}
	var methodID, methodTitle sql.NullString
	var updatedAt sql.NullTime

	err := db.conn.QueryRow(
		`SELECT telegram_id, enabled, payment_method_id, method_title, period_months, created_at, updated_at
		 FROM autorenewals WHERE telegram_id = ?`, telegramID,
	).Scan(&a.TelegramID, &a.Enabled, &methodID, &methodTitle, &a.PeriodMonths, &a.CreatedAt, &updatedAt)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("failed to read autorenewal: %w", err)
	}

	if methodID.Valid {
		a.PaymentMethodID = &methodID.String
	}
	if methodTitle.Valid {
		a.MethodTitle = &methodTitle.String
	}
	if updatedAt.Valid {
		a.UpdatedAt = &updatedAt.Time
	}
	return a, nil
}

// SaveAutorenewMethod записывает Способ автосписания. Согласие при этом не
// трогается: сохранение способа при обычной оплате не включает Автопродление.
func (db *DB) SaveAutorenewMethod(telegramID int64, paymentMethodID, methodTitle string) error {
	_, err := db.conn.Exec(
		`INSERT INTO autorenewals (telegram_id, enabled, payment_method_id, method_title, period_months, updated_at)
		 VALUES (?, 0, ?, ?, 1, CURRENT_TIMESTAMP)
		 ON CONFLICT(telegram_id) DO UPDATE SET
		   payment_method_id = excluded.payment_method_id,
		   method_title = excluded.method_title,
		   updated_at = CURRENT_TIMESTAMP`,
		telegramID, paymentMethodID, methodTitle,
	)
	if err != nil {
		return fmt.Errorf("failed to save autorenew method: %w", err)
	}
	return nil
}

// ClearAutorenewMethod гасит Способ, оставляя согласие как было: при следующей
// ручной оплате картой автопродление оживает само, без повторного вопроса.
func (db *DB) ClearAutorenewMethod(telegramID int64) error {
	_, err := db.conn.Exec(
		`UPDATE autorenewals
		 SET payment_method_id = NULL, method_title = NULL, updated_at = CURRENT_TIMESTAMP
		 WHERE telegram_id = ?`,
		telegramID,
	)
	if err != nil {
		return fmt.Errorf("failed to clear autorenew method: %w", err)
	}
	return nil
}

// SetAutorenewEnabled записывает согласие пользователя. Способ не трогается.
func (db *DB) SetAutorenewEnabled(telegramID int64, enabled bool) error {
	_, err := db.conn.Exec(
		`INSERT INTO autorenewals (telegram_id, enabled, period_months, updated_at)
		 VALUES (?, ?, 1, CURRENT_TIMESTAMP)
		 ON CONFLICT(telegram_id) DO UPDATE SET
		   enabled = excluded.enabled,
		   updated_at = CURRENT_TIMESTAMP`,
		telegramID, enabled,
	)
	if err != nil {
		return fmt.Errorf("failed to set autorenew flag: %w", err)
	}
	return nil
}

// ListEnabledAutorenewals возвращает всех, кто дал согласие и у кого есть
// Способ. Согласие без Способа — нормальное состояние, но списывать по нему
// нечем.
func (db *DB) ListEnabledAutorenewals() ([]*Autorenewal, error) {
	rows, err := db.conn.Query(
		`SELECT telegram_id, enabled, payment_method_id, method_title, period_months, created_at, updated_at
		 FROM autorenewals
		 WHERE enabled = 1 AND payment_method_id IS NOT NULL AND payment_method_id != ''
		 ORDER BY telegram_id`,
	)
	if err != nil {
		return nil, fmt.Errorf("failed to list autorenewals: %w", err)
	}
	defer rows.Close()

	var result []*Autorenewal
	for rows.Next() {
		a := &Autorenewal{}
		var methodID, methodTitle sql.NullString
		var updatedAt sql.NullTime
		if err := rows.Scan(&a.TelegramID, &a.Enabled, &methodID, &methodTitle, &a.PeriodMonths, &a.CreatedAt, &updatedAt); err != nil {
			return nil, fmt.Errorf("failed to scan autorenewal: %w", err)
		}
		if methodID.Valid {
			a.PaymentMethodID = &methodID.String
		}
		if methodTitle.Valid {
			a.MethodTitle = &methodTitle.String
		}
		if updatedAt.Valid {
			a.UpdatedAt = &updatedAt.Time
		}
		result = append(result, a)
	}
	return result, rows.Err()
}

// DeleteAutorenewal убирает согласие, Способ и историю попыток. Зовётся при
// удалении пользователя: хранить платёжный токен человека, которого мы сами
// отключили, оснований нет.
func (db *DB) DeleteAutorenewal(telegramID int64) error {
	if _, err := db.conn.Exec(`DELETE FROM autorenewals WHERE telegram_id = ?`, telegramID); err != nil {
		return fmt.Errorf("failed to delete autorenewal: %w", err)
	}
	if _, err := db.conn.Exec(`DELETE FROM autorenew_attempts WHERE telegram_id = ?`, telegramID); err != nil {
		return fmt.Errorf("failed to delete autorenew attempts: %w", err)
	}
	return nil
}

// RecordAutorenewAttempt фиксирует израсходованную попытку. Первичный ключ
// (telegram_id, expire_at, attempt_no) — физический барьер против второй записи
// той же попытки; повторный вызов дописывает исход, а не заводит строку.
func (db *DB) RecordAutorenewAttempt(a *AutorenewAttempt) error {
	_, err := db.conn.Exec(
		`INSERT INTO autorenew_attempts (telegram_id, expire_at, attempt_no, outcome, payment_id)
		 VALUES (?, ?, ?, ?, ?)
		 ON CONFLICT(telegram_id, expire_at, attempt_no) DO UPDATE SET
		   outcome = excluded.outcome,
		   payment_id = COALESCE(excluded.payment_id, autorenew_attempts.payment_id)`,
		a.TelegramID, autorenewCycleKey(a.ExpireAt), a.AttemptNo, a.Outcome, a.PaymentID,
	)
	if err != nil {
		return fmt.Errorf("failed to record autorenew attempt: %w", err)
	}
	return nil
}

// HasAutorenewAttempt сообщает, была ли уже попытка с этим номером в этом цикле.
func (db *DB) HasAutorenewAttempt(telegramID int64, expireAt time.Time, attemptNo int) (bool, error) {
	var exists bool
	err := db.conn.QueryRow(
		`SELECT EXISTS(SELECT 1 FROM autorenew_attempts WHERE telegram_id = ? AND expire_at = ? AND attempt_no = ?)`,
		telegramID, autorenewCycleKey(expireAt), attemptNo,
	).Scan(&exists)
	if err != nil {
		return false, fmt.Errorf("failed to check autorenew attempt: %w", err)
	}
	return exists, nil
}

// IsAutorenewPayment сообщает, был ли платёж создан шагом автосписаний.
//
// Нужна ручному flow оплаты: висящий pending без ссылки он переиспользует
// вместе с ключом идемпотентности, а ключ автосписания выписан под запрос с
// `payment_method_id` и без `confirmation` — касса отвергнет по нему обычный
// платёж, и человек не сможет оплатить вовсе.
func (db *DB) IsAutorenewPayment(paymentID int64) (bool, error) {
	var exists bool
	err := db.conn.QueryRow(
		`SELECT EXISTS(SELECT 1 FROM autorenew_attempts WHERE payment_id = ?)`, paymentID,
	).Scan(&exists)
	if err != nil {
		return false, fmt.Errorf("failed to check autorenew payment: %w", err)
	}
	return exists, nil
}

// UnresolvedAutorenewAttempt возвращает попытку цикла с неизвестным исходом, по
// которой касса так и не назвала свой платёж.
//
// Такая попытка — единственный случай, когда следующая обязана пойти в кассу с
// **тем же** ключом идемпотентности: если прошлое обращение всё-таки дошло и
// деньги списаны, новый ключ означал бы второе списание за месяц.
func (db *DB) UnresolvedAutorenewAttempt(telegramID int64, expireAt time.Time) (*AutorenewAttempt, error) {
	a := &AutorenewAttempt{}
	var paymentID sql.NullInt64
	err := db.conn.QueryRow(
		`SELECT a.telegram_id, a.expire_at, a.attempt_no, a.outcome, a.payment_id, a.created_at
		 FROM autorenew_attempts a
		 JOIN payments p ON p.id = a.payment_id
		 WHERE a.telegram_id = ? AND a.expire_at = ? AND a.outcome = ?
		   AND (p.provider_payment_id IS NULL OR p.provider_payment_id = '')
		 ORDER BY a.attempt_no DESC LIMIT 1`,
		telegramID, autorenewCycleKey(expireAt), AutorenewOutcomeUnknown,
	).Scan(&a.TelegramID, &a.ExpireAt, &a.AttemptNo, &a.Outcome, &paymentID, &a.CreatedAt)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("failed to read unresolved autorenew attempt: %w", err)
	}
	if paymentID.Valid {
		a.PaymentID = &paymentID.Int64
	}
	return a, nil
}

// HasFailedAutorenewAttempt сообщает, была ли в этом цикле попытка, не
// закончившаяся успехом. Нужна уведомлениям: подавлять предупреждение о конце
// подписки можно, только пока автопродление ещё обещает сработать.
func (db *DB) HasFailedAutorenewAttempt(telegramID int64, expireAt time.Time) (bool, error) {
	var exists bool
	err := db.conn.QueryRow(
		`SELECT EXISTS(SELECT 1 FROM autorenew_attempts
		 WHERE telegram_id = ? AND expire_at = ? AND outcome != ?)`,
		telegramID, autorenewCycleKey(expireAt), AutorenewOutcomeSuccess,
	).Scan(&exists)
	if err != nil {
		return false, fmt.Errorf("failed to check failed autorenew attempt: %w", err)
	}
	return exists, nil
}

// LastAutorenewChargeAmount возвращает сумму прошлого успешного автосписания.
// Нужна, чтобы заметить рост цены: молча списать больше, чем в прошлый раз, —
// кратчайший путь к chargeback.
func (db *DB) LastAutorenewChargeAmount(telegramID int64) (int, bool, error) {
	var amount int
	err := db.conn.QueryRow(
		`SELECT p.amount
		 FROM autorenew_attempts a
		 JOIN payments p ON p.id = a.payment_id
		 WHERE a.telegram_id = ? AND a.outcome = ?
		 ORDER BY a.created_at DESC, a.expire_at DESC LIMIT 1`,
		telegramID, AutorenewOutcomeSuccess,
	).Scan(&amount)
	if err == sql.ErrNoRows {
		return 0, false, nil
	}
	if err != nil {
		return 0, false, fmt.Errorf("failed to read last autorenew charge: %w", err)
	}
	return amount, true, nil
}

// ListAutorenewAttempts возвращает все попытки цикла в порядке номеров.
func (db *DB) ListAutorenewAttempts(telegramID int64, expireAt time.Time) ([]*AutorenewAttempt, error) {
	rows, err := db.conn.Query(
		`SELECT telegram_id, expire_at, attempt_no, outcome, payment_id, created_at
		 FROM autorenew_attempts WHERE telegram_id = ? AND expire_at = ?
		 ORDER BY attempt_no`,
		telegramID, autorenewCycleKey(expireAt),
	)
	if err != nil {
		return nil, fmt.Errorf("failed to list autorenew attempts: %w", err)
	}
	defer rows.Close()

	var result []*AutorenewAttempt
	for rows.Next() {
		a := &AutorenewAttempt{}
		var paymentID sql.NullInt64
		if err := rows.Scan(&a.TelegramID, &a.ExpireAt, &a.AttemptNo, &a.Outcome, &paymentID, &a.CreatedAt); err != nil {
			return nil, fmt.Errorf("failed to scan autorenew attempt: %w", err)
		}
		if paymentID.Valid {
			a.PaymentID = &paymentID.Int64
		}
		result = append(result, a)
	}
	return result, rows.Err()
}
