package database

import (
	"os"
	"testing"

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
