package database

import (
	"os"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestCreatePayment(t *testing.T) {
	dbFile := "test_payments.db"
	db, err := New(dbFile)
	require.NoError(t, err)
	defer func() {
		db.Close()
		os.Remove(dbFile)
	}()

	p := &Payment{
		TelegramID:    12345,
		Amount:        500,
		PaymentMethod: "sbp",
		Status:        "pending",
	}

	id, err := db.CreatePayment(p)
	require.NoError(t, err)
	assert.Greater(t, id, int64(0))

	// Получаем созданный платёж
	got, err := db.GetPaymentByID(id)
	require.NoError(t, err)
	require.NotNil(t, got)

	assert.Equal(t, int64(12345), got.TelegramID)
	assert.Equal(t, 500, got.Amount)
	assert.Equal(t, "sbp", got.PaymentMethod)
	assert.Equal(t, "pending", got.Status)
	assert.Nil(t, got.ModeratorID)
	assert.Nil(t, got.PlategaTransactionID)
	assert.Nil(t, got.ConfirmedAt)
}

func TestGetPendingPayment(t *testing.T) {
	dbFile := "test_payments_pending.db"
	db, err := New(dbFile)
	require.NoError(t, err)
	defer func() {
		db.Close()
		os.Remove(dbFile)
	}()

	// Нет платежей — возвращает nil
	got, err := db.GetPendingPayment(12345)
	require.NoError(t, err)
	assert.Nil(t, got)

	// Создаём активный PENDING платёж
	future := time.Now().UTC().Add(30 * time.Minute)
	p := &Payment{
		TelegramID:    12345,
		Amount:        500,
		PaymentMethod: "card",
		Status:        "pending",
		ExpiresAt:     &future,
	}
	id, err := db.CreatePayment(p)
	require.NoError(t, err)

	got, err = db.GetPendingPayment(12345)
	require.NoError(t, err)
	require.NotNil(t, got)
	assert.Equal(t, id, got.ID)

	// Протухший PENDING — не возвращается
	past := time.Now().UTC().Add(-1 * time.Minute)
	expired := &Payment{
		TelegramID:    99999,
		Amount:        300,
		PaymentMethod: "sbp",
		Status:        "pending",
		ExpiresAt:     &past,
	}
	_, err = db.CreatePayment(expired)
	require.NoError(t, err)

	got, err = db.GetPendingPayment(99999)
	require.NoError(t, err)
	assert.Nil(t, got)
}

func TestGetPaymentByPlategaTxID(t *testing.T) {
	dbFile := "test_payments_tx.db"
	db, err := New(dbFile)
	require.NoError(t, err)
	defer func() {
		db.Close()
		os.Remove(dbFile)
	}()

	txID := "platega-tx-abc123"
	p := &Payment{
		TelegramID:           12345,
		Amount:               500,
		PaymentMethod:        "sbp",
		Status:               "pending",
		PlategaTransactionID: &txID,
	}
	id, err := db.CreatePayment(p)
	require.NoError(t, err)

	// Находим по transaction ID
	got, err := db.GetPaymentByPlategaTxID(txID)
	require.NoError(t, err)
	require.NotNil(t, got)
	assert.Equal(t, id, got.ID)
	assert.Equal(t, txID, *got.PlategaTransactionID)

	// Несуществующий — nil
	got, err = db.GetPaymentByPlategaTxID("nonexistent")
	require.NoError(t, err)
	assert.Nil(t, got)
}

func TestConfirmPayment(t *testing.T) {
	dbFile := "test_payments_confirm.db"
	db, err := New(dbFile)
	require.NoError(t, err)
	defer func() {
		db.Close()
		os.Remove(dbFile)
	}()

	p := &Payment{
		TelegramID:    12345,
		Amount:        500,
		PaymentMethod: "sbp",
		Status:        "pending",
	}
	id, err := db.CreatePayment(p)
	require.NoError(t, err)

	// Подтверждаем платёж
	err = db.ConfirmPayment(id)
	require.NoError(t, err)

	got, err := db.GetPaymentByID(id)
	require.NoError(t, err)
	require.NotNil(t, got)
	assert.Equal(t, "confirmed", got.Status)
	assert.NotNil(t, got.ConfirmedAt)
}

func TestConfirmPaymentPreservesExistingConfirmedAt(t *testing.T) {
	dbFile := "test_payments_confirm_preserve.db"
	db, err := New(dbFile)
	require.NoError(t, err)
	defer func() {
		db.Close()
		os.Remove(dbFile)
	}()

	p := &Payment{
		TelegramID:    12345,
		Amount:        500,
		PaymentMethod: "sbp",
		Status:        "pending",
	}
	id, err := db.CreatePayment(p)
	require.NoError(t, err)

	require.NoError(t, db.ConfirmPayment(id))

	original := time.Date(2026, time.March, 1, 12, 0, 0, 0, time.UTC)
	_, err = db.Conn().Exec(`UPDATE payments SET confirmed_at = ? WHERE id = ?`, original, id)
	require.NoError(t, err)

	require.NoError(t, db.UpdatePaymentStatus(id, "confirmed_not_activated"))
	require.NoError(t, db.ConfirmPayment(id))

	got, err := db.GetPaymentByID(id)
	require.NoError(t, err)
	require.NotNil(t, got)
	require.NotNil(t, got.ConfirmedAt)
	assert.Equal(t, "confirmed", got.Status)
	assert.True(t, got.ConfirmedAt.Equal(original), "confirmed_at не должен перезаписываться при retry")
}

func TestExpireOldPendingPayments(t *testing.T) {
	dbFile := "test_payments_expire.db"
	db, err := New(dbFile)
	require.NoError(t, err)
	defer func() {
		db.Close()
		os.Remove(dbFile)
	}()

	// Создаём протухший платёж напрямую
	past := time.Now().UTC().Add(-2 * time.Minute)
	p := &Payment{
		TelegramID:    12345,
		Amount:        500,
		PaymentMethod: "sbp",
		Status:        "pending",
		ExpiresAt:     &past,
	}
	id, err := db.CreatePayment(p)
	require.NoError(t, err)

	n, err := db.ExpireOldPendingPayments()
	require.NoError(t, err)
	assert.Equal(t, int64(1), n)

	got, err := db.GetPaymentByID(id)
	require.NoError(t, err)
	assert.Equal(t, "expired", got.Status)
}

func TestHasConfirmedPayment(t *testing.T) {
	dbFile := "test_payments_has.db"
	db, err := New(dbFile)
	require.NoError(t, err)
	defer func() {
		db.Close()
		os.Remove(dbFile)
	}()

	// Без платежей — false
	has, err := db.HasConfirmedPayment(12345)
	require.NoError(t, err)
	assert.False(t, has)

	// Создаём PENDING — всё ещё false
	p := &Payment{
		TelegramID:    12345,
		Amount:        500,
		PaymentMethod: "sbp",
		Status:        "pending",
	}
	id, err := db.CreatePayment(p)
	require.NoError(t, err)

	has, err = db.HasConfirmedPayment(12345)
	require.NoError(t, err)
	assert.False(t, has)

	// Подтверждаем — true
	require.NoError(t, db.ConfirmPayment(id))
	has, err = db.HasConfirmedPayment(12345)
	require.NoError(t, err)
	assert.True(t, has)
}
