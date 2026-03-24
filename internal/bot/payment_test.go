package bot

import (
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/fus1ond/vpn_bot/internal/config"
	"github.com/fus1ond/vpn_bot/internal/database"
	"github.com/fus1ond/vpn_bot/internal/platega"
	"github.com/fus1ond/vpn_bot/internal/remnawave"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	tele "gopkg.in/telebot.v3"
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

func TestHandleConfirmedCreatesModeratorEarningBeforeActivationRetry(t *testing.T) {
	dbFile := "test_payment_confirm_retry_earning.db"
	db, err := database.New(dbFile)
	require.NoError(t, err)
	t.Cleanup(func() {
		db.Close()
		os.Remove(dbFile)
	})

	adminID := int64(999)
	modID := int64(550)
	userID := int64(551)
	price := 400

	_, err = db.CreateUser(modID, "moderator", "Moderator", "uuid-550", nil, nil)
	require.NoError(t, err)
	require.NoError(t, db.AddModerator(modID, adminID))
	_, err = db.CreateUser(userID, "payer", "Payer", "uuid-551", &price, &modID)
	require.NoError(t, err)

	txID := "tx-retry-earning"
	payment := &database.Payment{
		TelegramID:           userID,
		ModeratorID:          &modID,
		Amount:               price,
		PaymentMethod:        "sbp",
		Status:               "pending",
		PlategaTransactionID: &txID,
	}
	id, err := db.CreatePayment(payment)
	require.NoError(t, err)
	payment.ID = id

	cfg := &config.Config{AdminID: adminID, PlategaFeeSBP: 11, PlategaFeeWithdrawal: 2}
	client := remnawave.NewClient("https://panel.example.com", "test-token", nil)
	client.SetHTTPClient(&http.Client{
		Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
			if r.Method == http.MethodGet && r.URL.Path == "/api/users/uuid-551" {
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

	err = handler.handleConfirmed(payment)
	require.NoError(t, err)

	stored, err := db.GetPaymentByID(id)
	require.NoError(t, err)
	require.NotNil(t, stored)
	assert.Equal(t, "confirmed_not_activated", stored.Status)

	var count int
	err = db.Conn().QueryRow(`SELECT COUNT(*) FROM moderator_earnings WHERE payment_id = ?`, id).Scan(&count)
	require.NoError(t, err)
	assert.Equal(t, 1, count)

	var shareAmount int
	err = db.Conn().QueryRow(`SELECT share_amount FROM moderator_earnings WHERE payment_id = ?`, id).Scan(&shareAmount)
	require.NoError(t, err)
	assert.Equal(t, 52, shareAmount)
}

func TestRetryConfirmedPaymentActivationDoesNotDuplicateEarning(t *testing.T) {
	dbFile := "test_payment_retry_duplicate_earning.db"
	db, err := database.New(dbFile)
	require.NoError(t, err)
	t.Cleanup(func() {
		db.Close()
		os.Remove(dbFile)
	})

	adminID := int64(999)
	modID := int64(560)
	userID := int64(561)
	price := 400

	_, err = db.CreateUser(modID, "moderator", "Moderator", "uuid-560", nil, nil)
	require.NoError(t, err)
	require.NoError(t, db.AddModerator(modID, adminID))
	_, err = db.CreateUser(userID, "payer", "Payer", "uuid-561", &price, &modID)
	require.NoError(t, err)

	txID := "tx-retry-no-duplicate"
	payment := &database.Payment{
		TelegramID:           userID,
		ModeratorID:          &modID,
		Amount:               price,
		PaymentMethod:        "sbp",
		Status:               "pending",
		PlategaTransactionID: &txID,
	}
	id, err := db.CreatePayment(payment)
	require.NoError(t, err)
	payment.ID = id

	var getUserAttempts atomic.Int32
	client := remnawave.NewClient("https://panel.example.com", "test-token", nil)
	client.SetHTTPClient(&http.Client{
		Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
			switch {
			case r.Method == http.MethodGet && r.URL.Path == "/api/users/uuid-561":
				if getUserAttempts.Add(1) == 1 {
					return &http.Response{
						StatusCode: http.StatusInternalServerError,
						Body:       io.NopCloser(strings.NewReader(`{"error":"boom"}`)),
						Header:     make(http.Header),
					}, nil
				}
				return &http.Response{
					StatusCode: http.StatusOK,
					Body:       io.NopCloser(strings.NewReader(`{"response":{"uuid":"uuid-561","status":"EXPIRED","expireAt":"2026-03-01T00:00:00Z"}}`)),
					Header:     make(http.Header),
				}, nil
			case r.Method == http.MethodPatch && r.URL.Path == "/api/users":
				return &http.Response{
					StatusCode: http.StatusOK,
					Body:       io.NopCloser(strings.NewReader(`{"response":{}}`)),
					Header:     make(http.Header),
				}, nil
			case r.Method == http.MethodGet && r.URL.Path == "/api/users/by-telegram-id/561":
				return &http.Response{
					StatusCode: http.StatusOK,
					Body:       io.NopCloser(strings.NewReader(`{"response":{"uuid":"uuid-561","status":"ACTIVE","expireAt":"2026-04-20T00:00:00Z"}}`)),
					Header:     make(http.Header),
				}, nil
			default:
				return nil, assert.AnError
			}
		}),
	})

	cfg := &config.Config{AdminID: adminID, PlategaFeeSBP: 11, PlategaFeeWithdrawal: 2}
	b := &Bot{db: db, config: cfg, userStates: newStateMap(), remnawave: client}
	handler := &paymentCallbackHandler{bot: b}

	err = handler.handleConfirmed(payment)
	require.NoError(t, err)

	ok := b.retryConfirmedPaymentActivation(id, "test")
	require.True(t, ok)

	var count int
	err = db.Conn().QueryRow(`SELECT COUNT(*) FROM moderator_earnings WHERE payment_id = ?`, id).Scan(&count)
	require.NoError(t, err)
	assert.Equal(t, 1, count)
}

func TestHandleConfirmedDoesNotReapplyActivationFromStaleSnapshot(t *testing.T) {
	dbFile := "test_payment_stale_snapshot.db"
	db, err := database.New(dbFile)
	require.NoError(t, err)
	t.Cleanup(func() {
		db.Close()
		os.Remove(dbFile)
	})

	userID := int64(580)
	_, err = db.CreateUser(userID, "payer", "Payer", "uuid-580", nil, nil)
	require.NoError(t, err)

	txID := "tx-stale-snapshot"
	payment := &database.Payment{
		TelegramID:           userID,
		Amount:               400,
		PaymentMethod:        "sbp",
		Status:               "pending",
		PlategaTransactionID: &txID,
	}
	id, err := db.CreatePayment(payment)
	require.NoError(t, err)

	firstSnapshot, err := db.GetPaymentByID(id)
	require.NoError(t, err)
	secondSnapshot, err := db.GetPaymentByID(id)
	require.NoError(t, err)

	var getUserCalls atomic.Int32
	var patchCalls atomic.Int32

	client := remnawave.NewClient("https://panel.example.com", "test-token", nil)
	client.SetHTTPClient(&http.Client{
		Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
			switch {
			case r.Method == http.MethodGet && r.URL.Path == "/api/users/uuid-580":
				if getUserCalls.Add(1) == 1 {
					return &http.Response{
						StatusCode: http.StatusOK,
						Body:       io.NopCloser(strings.NewReader(`{"response":{"uuid":"uuid-580","status":"EXPIRED","expireAt":"2026-03-01T00:00:00Z"}}`)),
						Header:     make(http.Header),
					}, nil
				}
				return &http.Response{
					StatusCode: http.StatusOK,
					Body:       io.NopCloser(strings.NewReader(`{"response":{"uuid":"uuid-580","status":"ACTIVE","expireAt":"2026-04-20T00:00:00Z"}}`)),
					Header:     make(http.Header),
				}, nil
			case r.Method == http.MethodPatch && r.URL.Path == "/api/users":
				patchCalls.Add(1)
				return &http.Response{
					StatusCode: http.StatusOK,
					Body:       io.NopCloser(strings.NewReader(`{"response":{}}`)),
					Header:     make(http.Header),
				}, nil
			case r.Method == http.MethodGet && r.URL.Path == "/api/users/by-telegram-id/580":
				expireAt := "2026-04-20T00:00:00Z"
				if patchCalls.Load() > 1 {
					expireAt = "2026-05-20T00:00:00Z"
				}
				payload := fmt.Sprintf(`{"response":{"uuid":"uuid-580","username":"payer","status":"ACTIVE","expireAt":"%s"}}`, expireAt)
				return &http.Response{
					StatusCode: http.StatusOK,
					Body:       io.NopCloser(strings.NewReader(payload)),
					Header:     make(http.Header),
				}, nil
			default:
				return nil, assert.AnError
			}
		}),
	})

	cfg := &config.Config{AdminID: 999}
	b := &Bot{db: db, config: cfg, userStates: newStateMap(), remnawave: client}
	handler := &paymentCallbackHandler{bot: b}

	err = handler.handleConfirmed(firstSnapshot)
	require.NoError(t, err)

	err = handler.handleConfirmed(secondSnapshot)
	require.NoError(t, err)

	assert.Equal(t, int32(1), patchCalls.Load(), "повторная обработка stale snapshot не должна повторно активировать подписку")

	stored, err := db.GetPaymentByID(id)
	require.NoError(t, err)
	require.NotNil(t, stored)
	assert.Equal(t, "confirmed", stored.Status)
}

func TestCreatePaymentForUser_RejectsZeroOrNilPrice(t *testing.T) {
	dbFile := "test_payment_zero_price.db"
	db, err := database.New(dbFile)
	require.NoError(t, err)
	t.Cleanup(func() {
		db.Close()
		os.Remove(dbFile)
	})

	// Пользователь без цены подписки (NULL)
	_, err = db.CreateUser(700, "user_no_price", "No Price", "uuid-700", nil, nil)
	require.NoError(t, err)

	// Пользователь с нулевой ценой подписки
	zeroPrice := 0
	_, err = db.CreateUser(701, "user_zero_price", "Zero Price", "uuid-701", &zeroPrice, nil)
	require.NoError(t, err)

	cfg := &config.Config{AdminID: 999}
	b := &Bot{
		db:         db,
		config:     cfg,
		userStates: newStateMap(),
		remnawave:  remnawave.NewClient("https://panel.example.com", "test-token", nil),
	}

	t.Run("SubscriptionPrice is nil — ошибка", func(t *testing.T) {
		_, _, createErr := b.createPaymentForUser(700, 1)
		require.Error(t, createErr)
		assert.Contains(t, createErr.Error(), "subscription price")
	})

	t.Run("SubscriptionPrice равен 0 — ошибка валидации", func(t *testing.T) {
		_, _, createErr := b.createPaymentForUser(701, 1)
		require.Error(t, createErr)
		assert.Contains(t, createErr.Error(), "некорректная сумма платежа")
	})
}

func TestCreatePaymentForUserSerializesConcurrentRequests(t *testing.T) {
	dbFile := "test_payment_concurrent_create.db"
	db, err := database.New(dbFile)
	require.NoError(t, err)
	t.Cleanup(func() {
		db.Close()
		os.Remove(dbFile)
	})

	userID := int64(702)
	price := 500
	_, err = db.CreateUser(userID, "payer", "Payer", "uuid-702", &price, nil)
	require.NoError(t, err)

	plategaClient := platega.NewClientWithBaseURL("merchant", "secret", "https://platega.test")
	var paymentRequests atomic.Int32
	plategaClient.SetHTTPClient(&http.Client{
		Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
			require.Equal(t, http.MethodPost, r.Method)
			require.Equal(t, "/transaction/process", r.URL.Path)

			requestNo := paymentRequests.Add(1)
			time.Sleep(100 * time.Millisecond)

			resp := fmt.Sprintf(
				`{"transactionId":"tx-%d","redirect":"https://pay.example/tx-%d","status":"PENDING","expiresIn":"00:15:00"}`,
				requestNo,
				requestNo,
			)
			return &http.Response{
				StatusCode: http.StatusOK,
				Body:       io.NopCloser(strings.NewReader(resp)),
				Header:     make(http.Header),
			}, nil
		}),
	})

	remClient := remnawave.NewClient("https://panel.example.com", "test-token", nil)
	remClient.SetHTTPClient(&http.Client{
		Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
			return nil, assert.AnError
		}),
	})

	b := &Bot{
		db:         db,
		config:     &config.Config{AdminID: 999, PlategaCallbackURL: "https://bot.example/callback"},
		userStates: newStateMap(),
		remnawave:  remClient,
		platega:    plategaClient,
		bot:        &tele.Bot{Me: &tele.User{Username: "testbot"}},
	}

	start := make(chan struct{})
	errCh := make(chan error, 2)
	var wg sync.WaitGroup

	for _, method := range []int{platega.PaymentMethodSBP, platega.PaymentMethodCard} {
		wg.Add(1)
		go func(paymentMethod int) {
			defer wg.Done()
			<-start
			_, _, createErr := b.createPaymentForUser(userID, paymentMethod)
			errCh <- createErr
		}(method)
	}

	close(start)
	wg.Wait()
	close(errCh)

	for createErr := range errCh {
		require.NoError(t, createErr)
	}

	var pendingCount int
	err = db.Conn().QueryRow(`SELECT COUNT(*) FROM payments WHERE telegram_id = ? AND status = 'pending'`, userID).Scan(&pendingCount)
	require.NoError(t, err)
	assert.Equal(t, 1, pendingCount, "должен остаться только один живой pending платёж")
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
		shutdownCh:         make(chan struct{}),
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

func TestRetryConfirmedPaymentActivationMarksTerminalFailureWhenUserMissingInRemnawave(t *testing.T) {
	dbFile := "test_payment_terminal_retry.db"
	db, err := database.New(dbFile)
	require.NoError(t, err)
	t.Cleanup(func() {
		db.Close()
		os.Remove(dbFile)
	})

	userID := int64(590)
	_, err = db.CreateUser(userID, "payer", "Payer", "uuid-590", nil, nil)
	require.NoError(t, err)

	payment := &database.Payment{
		TelegramID:    userID,
		Amount:        400,
		PaymentMethod: "sbp",
		Status:        "pending",
	}
	id, err := db.CreatePayment(payment)
	require.NoError(t, err)
	require.NoError(t, db.ConfirmPayment(id))
	require.NoError(t, db.UpdatePaymentStatus(id, "confirmed_not_activated"))

	client := remnawave.NewClient("https://panel.example.com", "test-token", nil)
	client.SetHTTPClient(&http.Client{
		Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
			if r.Method == http.MethodGet && r.URL.Path == "/api/users/uuid-590" {
				return &http.Response{
					StatusCode: http.StatusNotFound,
					Body:       io.NopCloser(strings.NewReader(`{"error":"not found"}`)),
					Header:     make(http.Header),
				}, nil
			}
			return nil, assert.AnError
		}),
	})

	b := &Bot{
		db:         db,
		config:     &config.Config{AdminID: 999},
		userStates: newStateMap(),
		remnawave:  client,
	}

	ok := b.retryConfirmedPaymentActivation(id, "test")
	require.True(t, ok, "terminal ошибка должна останавливать дальнейшие retry")

	stored, err := db.GetPaymentByID(id)
	require.NoError(t, err)
	require.NotNil(t, stored)
	assert.Equal(t, "confirmed_activation_failed", stored.Status)
}
