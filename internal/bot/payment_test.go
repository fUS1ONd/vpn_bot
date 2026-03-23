package bot

import (
	"io"
	"net/http"
	"os"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/fus1ond/vpn_bot/internal/config"
	"github.com/fus1ond/vpn_bot/internal/database"
	"github.com/fus1ond/vpn_bot/internal/remnawave"
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

func TestHandleConfirmedReturnsQuicklyWhenActivationFails(t *testing.T) {
	dbFile := "test_payment_confirm_retry.db"
	db, err := database.New(dbFile)
	require.NoError(t, err)
	t.Cleanup(func() {
		db.Close()
		os.Remove(dbFile)
	})

	_, err = db.CreateUser(501, "payer", "Payer", "uuid-501", nil, nil)
	require.NoError(t, err)

	txID := "tx-quick-fail"
	payment := &database.Payment{
		TelegramID:           501,
		Amount:               400,
		PaymentMethod:        "sbp",
		Status:               "pending",
		PlategaTransactionID: &txID,
	}
	id, err := db.CreatePayment(payment)
	require.NoError(t, err)
	payment.ID = id

	cfg := &config.Config{AdminID: 999}
	client := remnawave.NewClient("https://panel.example.com", "test-token", nil)
	client.SetHTTPClient(&http.Client{
		Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
			if r.Method == http.MethodGet && r.URL.Path == "/api/users/uuid-501" {
				return &http.Response{
					StatusCode: http.StatusInternalServerError,
					Body:       io.NopCloser(strings.NewReader(`{"error":"boom"}`)),
					Header:     make(http.Header),
				}, nil
			}
			return nil, assert.AnError
		}),
	})

	b := &Bot{db: db, config: cfg, userStates: newStateMap(), remnawave: client}
	handler := &paymentCallbackHandler{bot: b}

	start := time.Now()
	err = handler.handleConfirmed(payment)
	duration := time.Since(start)

	assert.NoError(t, err)
	assert.Less(t, duration, 2*time.Second, "handleConfirmed не должен держать request path на retry/sleep")

	stored, err := db.GetPaymentByID(id)
	require.NoError(t, err)
	require.NotNil(t, stored)
	assert.Equal(t, "confirmed_not_activated", stored.Status)
	require.NotNil(t, stored.ConfirmedAt)
}

func TestHandleConfirmedRetriesActivationInBackground(t *testing.T) {
	dbFile := "test_payment_background_retry.db"
	db, err := database.New(dbFile)
	require.NoError(t, err)
	t.Cleanup(func() {
		db.Close()
		os.Remove(dbFile)
	})

	_, err = db.CreateUser(502, "payer", "Payer", "uuid-502", nil, nil)
	require.NoError(t, err)

	txID := "tx-background-retry"
	payment := &database.Payment{
		TelegramID:           502,
		Amount:               400,
		PaymentMethod:        "sbp",
		Status:               "pending",
		PlategaTransactionID: &txID,
	}
	id, err := db.CreatePayment(payment)
	require.NoError(t, err)
	payment.ID = id

	var getUserAttempts atomic.Int32
	enabledCh := make(chan struct{}, 1)

	cfg := &config.Config{AdminID: 999}
	client := remnawave.NewClient("https://panel.example.com", "test-token", nil)
	client.SetHTTPClient(&http.Client{
		Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
			switch {
			case r.Method == http.MethodGet && r.URL.Path == "/api/users/uuid-502":
				attempt := getUserAttempts.Add(1)
				if attempt == 1 {
					return &http.Response{
						StatusCode: http.StatusInternalServerError,
						Body:       io.NopCloser(strings.NewReader(`{"error":"boom"}`)),
						Header:     make(http.Header),
					}, nil
				}
				return &http.Response{
					StatusCode: http.StatusOK,
					Body:       io.NopCloser(strings.NewReader(`{"response":{"uuid":"uuid-502","status":"EXPIRED","expireAt":"2026-03-01T00:00:00Z"}}`)),
					Header:     make(http.Header),
				}, nil
			case r.Method == http.MethodPatch && r.URL.Path == "/api/users":
				select {
				case enabledCh <- struct{}{}:
				default:
				}
				return &http.Response{
					StatusCode: http.StatusOK,
					Body:       io.NopCloser(strings.NewReader(`{"response":{}}`)),
					Header:     make(http.Header),
				}, nil
			case r.Method == http.MethodGet && r.URL.Path == "/api/users/by-telegram-id/502":
				return &http.Response{
					StatusCode: http.StatusOK,
					Body:       io.NopCloser(strings.NewReader(`{"response":{"uuid":"uuid-502","status":"ACTIVE","expireAt":"2026-04-20T00:00:00Z"}}`)),
					Header:     make(http.Header),
				}, nil
			default:
				return nil, assert.AnError
			}
		}),
	})

	b := &Bot{
		db:                 db,
		config:             cfg,
		userStates:         newStateMap(),
		remnawave:          client,
		paymentRetryDelays: []time.Duration{10 * time.Millisecond},
	}
	handler := &paymentCallbackHandler{bot: b}

	err = handler.handleConfirmed(payment)
	require.NoError(t, err)

	select {
	case <-enabledCh:
	case <-time.After(time.Second):
		t.Fatal("ожидали background retry активации")
	}

	require.Eventually(t, func() bool {
		stored, getErr := db.GetPaymentByID(id)
		require.NoError(t, getErr)
		return stored != nil && stored.Status == "confirmed"
	}, time.Second, 20*time.Millisecond)
}
