package bot

import (
	"os"
	"testing"

	"github.com/fus1ond/vpn_bot/internal/config"
	"github.com/fus1ond/vpn_bot/internal/database"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestCalculateSharePercent(t *testing.T) {
	tests := []struct {
		name        string
		payingCount int
		wantPercent int
	}{
		{"Менее 15 — 15%", 0, 15},
		{"Ровно 14 — 15%", 14, 15},
		{"Ровно 15 — 20%", 15, 20},
		{"Между 15 и 25 — 20%", 20, 20},
		{"Ровно 25 — 25%", 25, 25},
		{"Более 25 — 25%", 50, 25},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := calculateSharePercent(tt.payingCount)
			assert.Equal(t, tt.wantPercent, got)
		})
	}
}

func TestGetPlategaFeePercent(t *testing.T) {
	b := &Bot{
		config: &config.Config{
			PlategaFeeSBP:    11,
			PlategaFeeCard:   12,
			PlategaFeeCrypto: 5,
		},
	}

	assert.Equal(t, 11, b.getPlategaFeePercent("sbp"))
	assert.Equal(t, 12, b.getPlategaFeePercent("card"))
	assert.Equal(t, 5, b.getPlategaFeePercent("crypto"))
	assert.Equal(t, 11, b.getPlategaFeePercent("unknown")) // Fallback на SBP
}

func TestHandleConfirmedIdempotency(t *testing.T) {
	dbFile := "test_payment_idempotency.db"
	db, err := database.New(dbFile)
	require.NoError(t, err)
	t.Cleanup(func() {
		db.Close()
		os.Remove(dbFile)
	})

	// Создаём пользователя
	_, err = db.CreateUser(500, "payer", "Payer", "uuid-500", nil, nil)
	require.NoError(t, err)

	// Создаём платёж и сразу подтверждаем
	txID := "tx-idempotent"
	payment := &database.Payment{
		TelegramID:           500,
		Amount:               400,
		PaymentMethod:        "sbp",
		Status:               "pending",
		PlategaTransactionID: &txID,
	}
	id, err := db.CreatePayment(payment)
	require.NoError(t, err)

	// Подтверждаем
	err = db.ConfirmPayment(id)
	require.NoError(t, err)

	// Перечитываем — статус confirmed
	confirmed, err := db.GetPaymentByID(id)
	require.NoError(t, err)
	require.Equal(t, "confirmed", confirmed.Status)

	// Повторный вызов handleConfirmed на уже подтверждённом платеже — должен быть noop
	cfg := &config.Config{AdminID: 999}
	b := &Bot{db: db, config: cfg, userStates: newStateMap()}
	handler := &paymentCallbackHandler{bot: b}

	err = handler.handleConfirmed(confirmed)
	assert.NoError(t, err) // Идемпотентность — нет ошибки

	// Статус не изменился
	after, err := db.GetPaymentByID(id)
	require.NoError(t, err)
	assert.Equal(t, "confirmed", after.Status)
}
