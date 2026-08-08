package database

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func setupReceiptsDB(t *testing.T) *DB {
	t.Helper()
	db, err := New(t.TempDir() + "/receipts.db")
	require.NoError(t, err)
	t.Cleanup(func() { db.Close() })
	return db
}

// createConfirmedPayment создаёт подтверждённый платёж с заданным id и моментом подтверждения.
func createConfirmedPayment(t *testing.T, db *DB, id int64, provider string, amount int, confirmedAt string) {
	t.Helper()
	_, err := db.conn.Exec(
		`INSERT INTO payments (id, telegram_id, amount, payment_method, status, provider, confirmed_at)
		 VALUES (?, ?, ?, ?, 'confirmed', ?, ?)`,
		id, 1000+id, amount, provider, provider, confirmedAt,
	)
	require.NoError(t, err)
}

func TestClaimReceiptIsIdempotentPerPayment(t *testing.T) {
	db := setupReceiptsDB(t)
	opTime := time.Date(2026, 8, 7, 21, 43, 31, 0, time.UTC)

	claimed, err := db.ClaimReceipt(96, "k7m2xq", opTime, 400)
	require.NoError(t, err)
	assert.True(t, claimed, "первое застолбление принадлежит нам")

	claimed, err = db.ClaimReceipt(96, "other1", opTime, 400)
	require.NoError(t, err)
	assert.False(t, claimed, "повторное застолбление не создаёт вторую запись")

	receipt, err := db.GetReceipt(96)
	require.NoError(t, err)
	require.NotNil(t, receipt)
	assert.Equal(t, "k7m2xq", receipt.Marker, "метка первого владельца сохраняется")
	assert.Equal(t, ReceiptStatePending, receipt.State)
	assert.Equal(t, 400, receipt.Amount)
	assert.Equal(t, opTime.Unix(), receipt.OperationTime.UTC().Unix())
	assert.Zero(t, receipt.Attempts)
}

func TestMarkReceiptCreatedAndFailedRecordHistory(t *testing.T) {
	db := setupReceiptsDB(t)
	_, err := db.ClaimReceipt(96, "k7m2xq", time.Now().UTC(), 400)
	require.NoError(t, err)

	require.NoError(t, db.MarkReceiptFailed(96, ReceiptStatePending, "ФНС недоступна"))
	receipt, err := db.GetReceipt(96)
	require.NoError(t, err)
	assert.Equal(t, 1, receipt.Attempts)
	require.NotNil(t, receipt.LastError)
	assert.Equal(t, "ФНС недоступна", *receipt.LastError)
	assert.Equal(t, ReceiptStatePending, receipt.State)

	require.NoError(t, db.MarkReceiptCreated(96, "202zzz"))
	receipt, err = db.GetReceipt(96)
	require.NoError(t, err)
	assert.Equal(t, ReceiptStateCreated, receipt.State)
	require.NotNil(t, receipt.ReceiptUUID)
	assert.Equal(t, "202zzz", *receipt.ReceiptUUID)
	assert.Nil(t, receipt.LastError, "успех стирает текст прошлой ошибки")
	assert.Equal(t, 2, receipt.Attempts)
}

func TestPaymentsNeedingReceiptCoversMissingPendingAndUnknown(t *testing.T) {
	db := setupReceiptsDB(t)
	createConfirmedPayment(t, db, 96, "yookassa", 400, "2026-08-07 21:43:31")
	createConfirmedPayment(t, db, 97, "yookassa", 450, "2026-08-08 10:00:00")
	createConfirmedPayment(t, db, 98, "yookassa", 500, "2026-08-08 11:00:00")
	createConfirmedPayment(t, db, 99, "yookassa", 400, "2026-08-08 12:00:00")
	// Platega игнорируется: чеки по ней не пробиваются — решение владельца.
	createConfirmedPayment(t, db, 100, "platega", 400, "2026-08-08 13:00:00")

	_, err := db.ClaimReceipt(97, "aaa111", time.Now().UTC(), 450)
	require.NoError(t, err)
	_, err = db.ClaimReceipt(98, "bbb222", time.Now().UTC(), 500)
	require.NoError(t, err)
	require.NoError(t, db.MarkReceiptFailed(98, ReceiptStateUnknown, "ответ потерян"))
	_, err = db.ClaimReceipt(99, "ccc333", time.Now().UTC(), 400)
	require.NoError(t, err)
	require.NoError(t, db.MarkReceiptCreated(99, "202done"))

	pending, err := db.PaymentsNeedingReceipt()
	require.NoError(t, err)

	var ids []int64
	for _, p := range pending {
		ids = append(ids, p.PaymentID)
	}
	assert.Equal(t, []int64{96, 97, 98}, ids)
	assert.Nil(t, pending[0].Receipt, "по платежу без записи чека состояния ещё нет")
	require.NotNil(t, pending[1].Receipt)
	assert.Equal(t, ReceiptStatePending, pending[1].Receipt.State)
	require.NotNil(t, pending[2].Receipt)
	assert.Equal(t, ReceiptStateUnknown, pending[2].Receipt.State)
	assert.Equal(t, "bbb222", pending[2].Receipt.Marker)
}

func TestPaymentsNeedingReceiptIncludesUnactivatedButPaid(t *testing.T) {
	db := setupReceiptsDB(t)
	createConfirmedPayment(t, db, 96, "yookassa", 400, "2026-08-07 21:43:31")
	_, err := db.conn.Exec(`UPDATE payments SET status = 'confirmed_not_activated' WHERE id = 96`)
	require.NoError(t, err)

	pending, err := db.PaymentsNeedingReceipt()
	require.NoError(t, err)
	require.Len(t, pending, 1, "деньги получены — чек обязателен независимо от судьбы активации")
	assert.Equal(t, int64(96), pending[0].PaymentID)
}

func TestSeedManualReceiptsStopsThirteenFromBeingIssuedAgain(t *testing.T) {
	db := setupReceiptsDB(t)
	for _, r := range manualReceipts {
		createConfirmedPayment(t, db, r.PaymentID, "yookassa", r.Amount, r.ConfirmedAt)
	}
	// Отменённый платёж 94 в засев не входит — денег по нему не было.
	_, err := db.conn.Exec(
		`INSERT INTO payments (id, telegram_id, amount, payment_method, status, provider)
		 VALUES (94, 194, 400, 'yookassa', 'canceled', 'yookassa')`,
	)
	require.NoError(t, err)
	// Платёж 96 подтверждён и чека не имеет — его должен пробить первый же проход.
	createConfirmedPayment(t, db, 96, "yookassa", 400, "2026-08-07 21:43:31")

	require.NoError(t, db.SeedManualReceipts())

	pending, err := db.PaymentsNeedingReceipt()
	require.NoError(t, err)
	require.Len(t, pending, 1, "после засева пробивать нужно только платёж 96")
	assert.Equal(t, int64(96), pending[0].PaymentID)

	seeded, err := db.GetReceipt(95)
	require.NoError(t, err)
	require.NotNil(t, seeded)
	assert.Equal(t, ReceiptStateCreated, seeded.State)
	require.NotNil(t, seeded.ReceiptUUID)
	assert.Equal(t, "202o7gt6yo", *seeded.ReceiptUUID)
	assert.Empty(t, seeded.Marker, "ручные чеки пробиты по старому формату наименования")

	// Повторный засев ничего не ломает.
	require.NoError(t, db.SeedManualReceipts())
}

func TestSeedManualReceiptsSkipsPaymentsThatDoNotMatchTheCabinet(t *testing.T) {
	db := setupReceiptsDB(t)
	// Чужая база: платёж с тем же номером, но другой суммой и датой.
	createConfirmedPayment(t, db, 79, "yookassa", 1000, "2026-09-01 10:00:00")

	require.NoError(t, db.SeedManualReceipts())

	receipt, err := db.GetReceipt(79)
	require.NoError(t, err)
	assert.Nil(t, receipt, "чужой платёж не должен получить чужой чек")
}

func TestNewReceiptMarkerIsSixLowercaseAlnum(t *testing.T) {
	seen := make(map[string]bool)
	for i := 0; i < 50; i++ {
		marker, err := NewReceiptMarker()
		require.NoError(t, err)
		require.Len(t, marker, markerLength)
		for _, r := range marker {
			assert.Contains(t, markerAlphabet, string(r))
		}
		seen[marker] = true
	}
	assert.Greater(t, len(seen), 40, "метка должна быть случайной")
}
