package database

import (
	"os"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestCreateEarning(t *testing.T) {
	dbFile := "test_earnings.db"
	db, err := New(dbFile)
	require.NoError(t, err)
	defer func() {
		db.Close()
		os.Remove(dbFile)
	}()

	// Сначала создаём платёж (внешний ключ)
	p := &Payment{
		TelegramID:    12345,
		Amount:        500,
		PaymentMethod: "sbp",
		Status:        "pending",
	}
	paymentID, err := db.CreatePayment(p)
	require.NoError(t, err)

	modID := int64(777)
	e := &ModeratorEarning{
		PaymentID:     paymentID,
		ModeratorID:   modID,
		GrossAmount:   500,
		PlategaFee:    15,
		WithdrawalFee: 10,
		NetAmount:     475,
		SharePercent:  70,
		ShareAmount:   332,
	}

	id, err := db.CreateEarning(e)
	require.NoError(t, err)
	assert.Greater(t, id, int64(0))
}

func TestGetModeratorEarningsByMonth(t *testing.T) {
	dbFile := "test_earnings_month.db"
	db, err := New(dbFile)
	require.NoError(t, err)
	defer func() {
		db.Close()
		os.Remove(dbFile)
	}()

	modID := int64(777)

	// Нет данных — возвращает нули
	me, err := db.GetModeratorEarningsByMonth(modID, 2026, 3)
	require.NoError(t, err)
	require.NotNil(t, me)
	assert.Equal(t, 0, me.TotalPayments)
	assert.Equal(t, 0, me.GrossAmount)

	// Создаём два платежа и начисления
	for i := 0; i < 2; i++ {
		p := &Payment{
			TelegramID:    int64(12345 + i),
			Amount:        500,
			PaymentMethod: "sbp",
			Status:        "confirmed",
		}
		paymentID, err := db.CreatePayment(p)
		require.NoError(t, err)
		confirmedAt := time.Date(2026, time.March, 10+i, 12, 0, 0, 0, time.UTC)
		_, err = db.Conn().Exec(`UPDATE payments SET status = 'confirmed', confirmed_at = ? WHERE id = ?`, confirmedAt, paymentID)
		require.NoError(t, err)

		e := &ModeratorEarning{
			PaymentID:     paymentID,
			ModeratorID:   modID,
			GrossAmount:   500,
			PlategaFee:    15,
			WithdrawalFee: 10,
			NetAmount:     475,
			SharePercent:  70,
			ShareAmount:   332,
		}
		_, err = db.CreateEarning(e)
		require.NoError(t, err)
	}

	// Текущий месяц — должны найти оба начисления
	me, err = db.GetModeratorEarningsByMonth(modID, 2026, 3)
	require.NoError(t, err)
	assert.Equal(t, 2, me.TotalPayments)
	assert.Equal(t, 1000, me.GrossAmount)
	assert.Equal(t, 664, me.TotalShareAmount)
	assert.Equal(t, 70, me.SharePercent)

	// Другой месяц — нули
	me, err = db.GetModeratorEarningsByMonth(modID, 2026, 2)
	require.NoError(t, err)
	assert.Equal(t, 0, me.TotalPayments)
}

func TestGetModeratorEarningsByMonth_UsesPaymentConfirmationMonth(t *testing.T) {
	dbFile := "test_earnings_month_boundary.db"
	db, err := New(dbFile)
	require.NoError(t, err)
	defer func() {
		db.Close()
		os.Remove(dbFile)
	}()

	modID := int64(778)

	paymentID, err := db.CreatePayment(&Payment{
		TelegramID:    9001,
		Amount:        500,
		PaymentMethod: "card",
		Status:        "pending",
	})
	require.NoError(t, err)
	require.NoError(t, db.ConfirmPayment(paymentID))

	confirmedAt := time.Date(2026, time.March, 31, 23, 59, 59, 0, time.UTC)
	_, err = db.Conn().Exec(`UPDATE payments SET confirmed_at = ? WHERE id = ?`, confirmedAt, paymentID)
	require.NoError(t, err)

	earningID, err := db.CreateEarning(&ModeratorEarning{
		PaymentID:     paymentID,
		ModeratorID:   modID,
		GrossAmount:   500,
		PlategaFee:    50,
		WithdrawalFee: 10,
		NetAmount:     440,
		SharePercent:  15,
		ShareAmount:   66,
	})
	require.NoError(t, err)

	createdAt := time.Date(2026, time.April, 1, 0, 0, 0, 0, time.UTC)
	_, err = db.Conn().Exec(`UPDATE moderator_earnings SET created_at = ? WHERE id = ?`, createdAt, earningID)
	require.NoError(t, err)

	me, err := db.GetModeratorEarningsByMonth(modID, 2026, 3)
	require.NoError(t, err)
	require.NotNil(t, me)
	assert.Equal(t, 1, me.TotalPayments)
	assert.Equal(t, 500, me.GrossAmount)
	assert.Equal(t, 66, me.TotalShareAmount)

	allMe, err := db.GetAllEarningsByMonth(2026, 3)
	require.NoError(t, err)
	require.NotNil(t, allMe)
	assert.Equal(t, 1, allMe.TotalPayments)
	assert.Equal(t, 500, allMe.GrossAmount)

	allMe, err = db.GetAllEarningsByMonth(2026, 4)
	require.NoError(t, err)
	require.NotNil(t, allMe)
	assert.Equal(t, 0, allMe.TotalPayments)
	assert.Equal(t, 0, allMe.GrossAmount)

	me, err = db.GetModeratorEarningsByMonth(modID, 2026, 4)
	require.NoError(t, err)
	require.NotNil(t, me)
	assert.Equal(t, 0, me.TotalPayments)
	assert.Equal(t, 0, me.GrossAmount)
}

func TestGetModeratorEarningsByMonth_IncludesConfirmedNotActivated(t *testing.T) {
	dbFile := "test_earnings_confirmed_not_activated.db"
	db, err := New(dbFile)
	require.NoError(t, err)
	defer func() {
		db.Close()
		os.Remove(dbFile)
	}()

	modID := int64(780)
	paymentID, err := db.CreatePayment(&Payment{
		TelegramID:    9201,
		Amount:        800,
		PaymentMethod: "sbp",
		Status:        "pending",
	})
	require.NoError(t, err)
	require.NoError(t, db.ConfirmPayment(paymentID))

	confirmedAt := time.Date(2026, time.March, 20, 12, 0, 0, 0, time.UTC)
	_, err = db.Conn().Exec(`UPDATE payments SET status = 'confirmed_not_activated', confirmed_at = ? WHERE id = ?`, confirmedAt, paymentID)
	require.NoError(t, err)

	_, err = db.CreateEarning(&ModeratorEarning{
		PaymentID:     paymentID,
		ModeratorID:   modID,
		GrossAmount:   800,
		PlategaFee:    80,
		WithdrawalFee: 14,
		NetAmount:     706,
		SharePercent:  15,
		ShareAmount:   105,
	})
	require.NoError(t, err)

	me, err := db.GetModeratorEarningsByMonth(modID, 2026, 3)
	require.NoError(t, err)
	require.NotNil(t, me)
	assert.Equal(t, 1, me.TotalPayments)
	assert.Equal(t, 800, me.GrossAmount)
	assert.Equal(t, 105, me.TotalShareAmount)
	assert.Equal(t, 15, me.SharePercent)
}

func TestGetModeratorEarningsByMonth_KeepsChargebackedConfirmedPaymentInHistory(t *testing.T) {
	dbFile := "test_earnings_chargebacked_history.db"
	db, err := New(dbFile)
	require.NoError(t, err)
	defer func() {
		db.Close()
		os.Remove(dbFile)
	}()

	modID := int64(781)
	paymentID, err := db.CreatePayment(&Payment{
		TelegramID:    9301,
		Amount:        600,
		PaymentMethod: "sbp",
		Status:        "pending",
	})
	require.NoError(t, err)
	require.NoError(t, db.ConfirmPayment(paymentID))

	confirmedAt := time.Date(2026, time.March, 21, 12, 0, 0, 0, time.UTC)
	_, err = db.Conn().Exec(`UPDATE payments SET status = 'chargebacked', confirmed_at = ? WHERE id = ?`, confirmedAt, paymentID)
	require.NoError(t, err)

	_, err = db.CreateEarning(&ModeratorEarning{
		PaymentID:     paymentID,
		ModeratorID:   modID,
		GrossAmount:   600,
		PlategaFee:    60,
		WithdrawalFee: 10,
		NetAmount:     530,
		SharePercent:  15,
		ShareAmount:   79,
	})
	require.NoError(t, err)

	me, err := db.GetModeratorEarningsByMonth(modID, 2026, 3)
	require.NoError(t, err)
	require.NotNil(t, me)
	assert.Equal(t, 1, me.TotalPayments)
	assert.Equal(t, 600, me.GrossAmount)
	assert.Equal(t, 79, me.TotalShareAmount)
}

func TestGetModeratorEarningsByMonth_UsesLatestPaymentConfirmationForSharePercent(t *testing.T) {
	dbFile := "test_earnings_share_percent.db"
	db, err := New(dbFile)
	require.NoError(t, err)
	defer func() {
		db.Close()
		os.Remove(dbFile)
	}()

	modID := int64(779)

	firstPaymentID, err := db.CreatePayment(&Payment{
		TelegramID:    9101,
		Amount:        500,
		PaymentMethod: "card",
		Status:        "confirmed",
	})
	require.NoError(t, err)
	_, err = db.Conn().Exec(`UPDATE payments SET confirmed_at = ? WHERE id = ?`, time.Date(2026, time.March, 5, 10, 0, 0, 0, time.UTC), firstPaymentID)
	require.NoError(t, err)

	secondPaymentID, err := db.CreatePayment(&Payment{
		TelegramID:    9102,
		Amount:        500,
		PaymentMethod: "card",
		Status:        "confirmed",
	})
	require.NoError(t, err)
	_, err = db.Conn().Exec(`UPDATE payments SET confirmed_at = ? WHERE id = ?`, time.Date(2026, time.March, 25, 10, 0, 0, 0, time.UTC), secondPaymentID)
	require.NoError(t, err)

	_, err = db.CreateEarning(&ModeratorEarning{
		PaymentID:     secondPaymentID,
		ModeratorID:   modID,
		GrossAmount:   500,
		PlategaFee:    15,
		WithdrawalFee: 10,
		NetAmount:     475,
		SharePercent:  20,
		ShareAmount:   95,
	})
	require.NoError(t, err)

	_, err = db.CreateEarning(&ModeratorEarning{
		PaymentID:     firstPaymentID,
		ModeratorID:   modID,
		GrossAmount:   500,
		PlategaFee:    15,
		WithdrawalFee: 10,
		NetAmount:     475,
		SharePercent:  10,
		ShareAmount:   47,
	})
	require.NoError(t, err)

	me, err := db.GetModeratorEarningsByMonth(modID, 2026, 3)
	require.NoError(t, err)
	require.NotNil(t, me)
	assert.Equal(t, 2, me.TotalPayments)
	assert.Equal(t, 20, me.SharePercent)
}

func TestGetModeratorTotalEarnings(t *testing.T) {
	dbFile := "test_earnings_total.db"
	db, err := New(dbFile)
	require.NoError(t, err)
	defer func() {
		db.Close()
		os.Remove(dbFile)
	}()

	modID := int64(777)

	// Нет данных — 0
	total, err := db.GetModeratorTotalEarnings(modID)
	require.NoError(t, err)
	assert.Equal(t, 0, total)

	// Добавляем начисления
	for i := 0; i < 3; i++ {
		p := &Payment{
			TelegramID:    int64(10000 + i),
			Amount:        500,
			PaymentMethod: "sbp",
			Status:        "confirmed",
		}
		paymentID, err := db.CreatePayment(p)
		require.NoError(t, err)

		e := &ModeratorEarning{
			PaymentID:     paymentID,
			ModeratorID:   modID,
			GrossAmount:   500,
			PlategaFee:    15,
			WithdrawalFee: 10,
			NetAmount:     475,
			SharePercent:  70,
			ShareAmount:   100, // Ровно для простоты
		}
		_, err = db.CreateEarning(e)
		require.NoError(t, err)
	}

	total, err = db.GetModeratorTotalEarnings(modID)
	require.NoError(t, err)
	assert.Equal(t, 300, total)
}
