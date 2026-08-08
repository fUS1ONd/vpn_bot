package database

import (
	"crypto/rand"
	"database/sql"
	"fmt"
	"math/big"
	"time"
)

// Состояния чека. Написание canceled с одной «l» — как в остальном коде.
const (
	ReceiptStatePending  = "pending"  // право застолблено, результат неизвестен
	ReceiptStateCreated  = "created"  // чек зарегистрирован в кабинете
	ReceiptStateCanceled = "canceled" // чек аннулирован
	ReceiptStateUnknown  = "unknown"  // ответ ФНС потерян, нужна сверка по метке
	ReceiptStateRejected = "rejected" // ФНС отвергла запрос, повторы бесполезны
)

// markerAlphabet — метка из шести символов a-z0-9. Случайная, а не номер платежа:
// порядковый номер сообщал бы налоговой, сколько всего расчётов прошло через бота.
const markerAlphabet = "abcdefghijklmnopqrstuvwxyz0123456789"

const markerLength = 6

// Receipt — состояние чека по платежу. Связь с платежом строго 1:1.
type Receipt struct {
	PaymentID     int64
	Marker        string
	ReceiptUUID   *string
	State         string
	OperationTime time.Time // момент подтверждения платежа, в UTC как и всё в базе
	Amount        int
	Attempts      int
	LastError     *string
	CreatedAt     time.Time
	UpdatedAt     *time.Time
}

// PendingReceipt — платёж, по которому чека ещё нет, вместе с состоянием попыток.
type PendingReceipt struct {
	PaymentID   int64
	Amount      int
	ConfirmedAt time.Time
	Provider    string
	Receipt     *Receipt // nil, если запись ещё не заведена
}

// NewReceiptMarker генерирует метку чека.
func NewReceiptMarker() (string, error) {
	out := make([]byte, markerLength)
	for i := range out {
		n, err := rand.Int(rand.Reader, big.NewInt(int64(len(markerAlphabet))))
		if err != nil {
			return "", fmt.Errorf("failed to generate receipt marker: %w", err)
		}
		out[i] = markerAlphabet[n.Int64()]
	}
	return string(out), nil
}

// ClaimReceipt застолбляет право на пробитие чека до обращения к ФНС.
// Возвращает true, если запись создали мы; false означает, что чек уже ведёт
// другой кодовый путь — это и есть физический барьер против второго чека.
func (db *DB) ClaimReceipt(paymentID int64, marker string, operationTime time.Time, amount int) (bool, error) {
	res, err := db.conn.Exec(
		`INSERT INTO receipts (payment_id, marker, state, operation_time, amount, attempts, created_at)
		 VALUES (?, ?, ?, ?, ?, 0, CURRENT_TIMESTAMP)
		 ON CONFLICT(payment_id) DO NOTHING`,
		paymentID, marker, ReceiptStatePending, operationTime.UTC(), amount,
	)
	if err != nil {
		return false, fmt.Errorf("failed to claim receipt: %w", err)
	}
	affected, err := res.RowsAffected()
	if err != nil {
		return false, fmt.Errorf("failed to claim receipt: %w", err)
	}
	return affected > 0, nil
}

// GetReceipt возвращает запись чека по платежу или nil, если её нет.
func (db *DB) GetReceipt(paymentID int64) (*Receipt, error) {
	row := db.conn.QueryRow(
		`SELECT payment_id, marker, receipt_uuid, state, operation_time, amount, attempts, last_error, created_at, updated_at
		 FROM receipts WHERE payment_id = ?`, paymentID,
	)
	receipt, err := scanReceipt(row)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("failed to get receipt: %w", err)
	}
	return receipt, nil
}

// MarkReceiptCreated фиксирует успешное пробитие: uuid чека и состояние created.
func (db *DB) MarkReceiptCreated(paymentID int64, receiptUUID string) error {
	_, err := db.conn.Exec(
		`UPDATE receipts
		 SET receipt_uuid = ?, state = ?, last_error = NULL, attempts = attempts + 1, updated_at = CURRENT_TIMESTAMP
		 WHERE payment_id = ?`,
		receiptUUID, ReceiptStateCreated, paymentID,
	)
	if err != nil {
		return fmt.Errorf("failed to mark receipt created: %w", err)
	}
	return nil
}

// MarkReceiptFailed сохраняет неудачную попытку: состояние, счётчик и текст ошибки,
// чтобы инцидент разбирался по базе, а не по логам.
func (db *DB) MarkReceiptFailed(paymentID int64, state, lastError string) error {
	_, err := db.conn.Exec(
		`UPDATE receipts
		 SET state = ?, last_error = ?, attempts = attempts + 1, updated_at = CURRENT_TIMESTAMP
		 WHERE payment_id = ?`,
		state, lastError, paymentID,
	)
	if err != nil {
		return fmt.Errorf("failed to mark receipt failed: %w", err)
	}
	return nil
}

// MarkReceiptCanceled фиксирует аннулирование чека.
func (db *DB) MarkReceiptCanceled(paymentID int64) error {
	_, err := db.conn.Exec(
		`UPDATE receipts SET state = ?, updated_at = CURRENT_TIMESTAMP WHERE payment_id = ?`,
		ReceiptStateCanceled, paymentID,
	)
	if err != nil {
		return fmt.Errorf("failed to mark receipt canceled: %w", err)
	}
	return nil
}

// PaymentsNeedingReceipt возвращает платежи ЮKassa, по которым чека ещё нет:
// без записи вовсе либо в незавершённых состояниях pending / unknown.
// Статусы confirmed_not_activated и confirmed_activation_failed включены намеренно:
// деньги уже получены, значит чек обязателен независимо от судьбы активации.
//
// Состояние rejected сюда не попадает: ФНС отвергла запрос, повторять его
// бесполезно, и бесконечный цикл попыток только копил бы attempts и стучался в
// кабинет каждые полчаса. Такой чек разбирается руками — владелец получает
// отдельное сообщение в момент отказа.
func (db *DB) PaymentsNeedingReceipt() ([]PendingReceipt, error) {
	rows, err := db.conn.Query(
		`SELECT p.id, p.amount, p.confirmed_at, p.provider,
		        r.payment_id, r.marker, r.receipt_uuid, r.state, r.operation_time, r.amount, r.attempts, r.last_error, r.created_at, r.updated_at
		 FROM payments p
		 LEFT JOIN receipts r ON r.payment_id = p.id
		 WHERE p.provider = 'yookassa'
		   AND p.status IN ('confirmed', 'confirmed_not_activated', 'confirmed_activation_failed')
		   AND p.confirmed_at IS NOT NULL
		   AND (r.payment_id IS NULL OR r.state IN (?, ?))
		 ORDER BY p.confirmed_at ASC, p.id ASC`,
		ReceiptStatePending, ReceiptStateUnknown,
	)
	if err != nil {
		return nil, fmt.Errorf("failed to select payments needing receipt: %w", err)
	}
	defer rows.Close()

	var pending []PendingReceipt
	for rows.Next() {
		var item PendingReceipt
		var receiptPaymentID sql.NullInt64
		var marker, state sql.NullString
		var receiptUUID, lastError sql.NullString
		var operationTime, createdAt sql.NullTime
		var updatedAt sql.NullTime
		var receiptAmount, attempts sql.NullInt64

		if err := rows.Scan(
			&item.PaymentID, &item.Amount, &item.ConfirmedAt, &item.Provider,
			&receiptPaymentID, &marker, &receiptUUID, &state, &operationTime, &receiptAmount, &attempts, &lastError, &createdAt, &updatedAt,
		); err != nil {
			return nil, fmt.Errorf("failed to scan payment needing receipt: %w", err)
		}

		if receiptPaymentID.Valid {
			receipt := &Receipt{
				PaymentID:     receiptPaymentID.Int64,
				Marker:        marker.String,
				State:         state.String,
				OperationTime: operationTime.Time,
				Amount:        int(receiptAmount.Int64),
				Attempts:      int(attempts.Int64),
				CreatedAt:     createdAt.Time,
			}
			if receiptUUID.Valid {
				receipt.ReceiptUUID = &receiptUUID.String
			}
			if lastError.Valid {
				receipt.LastError = &lastError.String
			}
			if updatedAt.Valid {
				receipt.UpdatedAt = &updatedAt.Time
			}
			item.Receipt = receipt
		}

		pending = append(pending, item)
	}
	return pending, rows.Err()
}

func scanReceipt(row *sql.Row) (*Receipt, error) {
	var r Receipt
	var receiptUUID, lastError sql.NullString
	var updatedAt sql.NullTime
	if err := row.Scan(&r.PaymentID, &r.Marker, &receiptUUID, &r.State, &r.OperationTime, &r.Amount, &r.Attempts, &lastError, &r.CreatedAt, &updatedAt); err != nil {
		return nil, err
	}
	if receiptUUID.Valid {
		r.ReceiptUUID = &receiptUUID.String
	}
	if lastError.Valid {
		r.LastError = &lastError.String
	}
	if updatedAt.Valid {
		r.UpdatedAt = &updatedAt.Time
	}
	return &r, nil
}

// ManualReceiptSeed — чек, пробитый владельцем вручную 6 августа 2026. Пары сверены
// с кабинетом ФНС; время операции у всех совпадает с подтверждением платежа
// посекундно. Тип экспортирован, чтобы состав засева читался снаружи, а не
// выяснялся по коду миграции.
type ManualReceiptSeed struct {
	PaymentID   int64
	ConfirmedAt string // UTC, как в базе
	Amount      int
	ReceiptUUID string
}

// manualReceipts — 13 чеков за июль–август, пробитых вручную. Без этой связи первый
// же проход увидел бы 13 платежей без записи и пробил дубли на 5 211 ₽.
// Платёж 94 отсутствует намеренно: он отменён, денег по нему не было.
var manualReceipts = []ManualReceiptSeed{
	{79, "2026-07-16 13:24:33", 10, "202c0louba"},
	{83, "2026-07-18 19:32:48", 500, "202dh0yzwi"},
	{84, "2026-07-23 11:26:57", 400, "202ezawh8a"},
	{85, "2026-07-23 13:00:51", 400, "202fp44ipu"},
	{86, "2026-07-25 07:50:34", 400, "202gaxlnp9"},
	{87, "2026-07-26 11:18:48", 450, "202htjh773"},
	{88, "2026-07-26 12:37:12", 450, "202ihglpki"},
	{89, "2026-07-31 11:23:15", 451, "202j3pqymm"},
	{90, "2026-08-02 07:42:17", 400, "202kj9so71"},
	{91, "2026-08-02 08:48:10", 450, "202lr2n5ua"},
	{92, "2026-08-02 20:24:31", 400, "202mkv0zs7"},
	{93, "2026-08-03 07:35:23", 500, "202nt0tnqk"},
	{95, "2026-08-03 23:33:01", 400, "202o7gt6yo"},
}

// ManualReceiptSeeds возвращает засеваемые пары «платёж → чек».
func ManualReceiptSeeds() []ManualReceiptSeed { return manualReceipts }

// SeedManualReceipts засевает связь ручных чеков с платежами. Метка у них пустая:
// они пробиты по старому формату наименования, сверка по метке к ним неприменима.
//
// Засев идёт только по точному совпадению платежа с кабинетом (id, сумма и момент
// подтверждения), поэтому на любой другой базе это полный no-op: чужой платёж с тем
// же номером не получит чужой чек и не останется без своего.
func (db *DB) SeedManualReceipts() error {
	return seedManualReceipts(db.conn)
}

func seedManualReceipts(conn *sql.DB) error {
	for _, r := range manualReceipts {
		_, err := conn.Exec(
			`INSERT INTO receipts (payment_id, marker, receipt_uuid, state, operation_time, amount, attempts, created_at)
			 SELECT p.id, '', ?, ?, p.confirmed_at, p.amount, 0, CURRENT_TIMESTAMP
			 FROM payments p
			 WHERE p.id = ? AND p.amount = ? AND p.provider = 'yookassa'
			   AND datetime(p.confirmed_at) = datetime(?)
			 ON CONFLICT(payment_id) DO NOTHING`,
			r.ReceiptUUID, ReceiptStateCreated, r.PaymentID, r.Amount, r.ConfirmedAt,
		)
		if err != nil {
			return fmt.Errorf("failed to seed manual receipt for payment %d: %w", r.PaymentID, err)
		}
	}
	return nil
}
