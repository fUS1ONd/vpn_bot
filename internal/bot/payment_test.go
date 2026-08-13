package bot

import (
	"encoding/json"
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
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	tele "gopkg.in/telebot.v3"
)

func TestAdminTestPaymentUsesConfiguredPriceAndIsMarked(t *testing.T) {
	db, err := database.New(t.TempDir() + "/payments.db")
	require.NoError(t, err)
	t.Cleanup(func() { db.Close() })

	const adminID int64 = 99
	_, err = db.CreateUser(adminID, "admin", "Admin", strPtrTest("uuid-admin"), nil, nil, nil)
	require.NoError(t, err)

	plategaClient := platega.NewClientWithBaseURL("merchant", "secret", "https://platega.test")
	plategaClient.SetHTTPClient(&http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
		require.Equal(t, http.MethodPost, r.Method)
		require.Equal(t, "/transaction/process", r.URL.Path)
		var body struct {
			PaymentDetails struct {
				Amount int `json:"amount"`
			} `json:"paymentDetails"`
		}
		require.NoError(t, json.NewDecoder(r.Body).Decode(&body))
		assert.Equal(t, 10, body.PaymentDetails.Amount)
		return &http.Response{
			StatusCode: http.StatusOK,
			Body:       io.NopCloser(strings.NewReader(`{"transactionId":"test-10","redirect":"https://pay.example/test-10","status":"PENDING","expiresIn":"00:15:00"}`)),
			Header:     make(http.Header),
		}, nil
	})})

	b := &Bot{
		db:         db,
		config:     &config.Config{AdminID: adminID, AdminTestPaymentPrice: 10, PlategaCallbackURL: "https://bot.example/callback", YooKassaReturnURL: "https://t.me/testbot"},
		userStates: newStateMap(),
		remnawave:  newTestPanelClient(),
		platega:    plategaClient,
	}
	b.remnawave.SetHTTPClient(&http.Client{Transport: roundTripFunc(func(*http.Request) (*http.Response, error) {
		return nil, assert.AnError
	})})

	payment, _, err := b.createPaymentForProvider(adminID, "platega")
	require.NoError(t, err)
	assert.Equal(t, 10, payment.Amount)
	assert.True(t, payment.IsTest)
	assert.Nil(t, payment.ModeratorID)

	stored, err := db.GetPaymentByID(payment.ID)
	require.NoError(t, err)
	require.NotNil(t, stored)
	assert.Equal(t, 10, stored.Amount)
	assert.True(t, stored.IsTest)
}

func TestAdminTestPaymentCallbackPreservesAccountOnConfirmationAndChargeback(t *testing.T) {
	db, err := database.New(t.TempDir() + "/payments.db")
	require.NoError(t, err)
	t.Cleanup(func() { db.Close() })

	const adminID int64 = 99
	_, err = db.CreateUser(adminID, "admin", "Admin", strPtrTest("uuid-admin"), nil, nil, nil)
	require.NoError(t, err)
	payment := &database.Payment{TelegramID: adminID, Amount: 10, PaymentMethod: "crypto", Status: "pending", IsTest: true}
	payment.ID, err = db.CreatePayment(payment)
	require.NoError(t, err)

	b := &Bot{db: db, config: &config.Config{AdminID: adminID}, userStates: newStateMap()}
	handler := &paymentCallbackHandler{bot: b}
	require.NoError(t, handler.handleConfirmedSilently(payment))

	stored, err := db.GetPaymentByID(payment.ID)
	require.NoError(t, err)
	require.NotNil(t, stored)
	assert.Equal(t, "confirmed", stored.Status)
	user, err := db.GetUserByTelegramID(adminID)
	require.NoError(t, err)
	assert.NotNil(t, user)

	require.NoError(t, handler.handleChargeback(stored))
	stored, err = db.GetPaymentByID(payment.ID)
	require.NoError(t, err)
	assert.Equal(t, "chargebacked", stored.Status)
	user, err = db.GetUserByTelegramID(adminID)
	require.NoError(t, err)
	assert.NotNil(t, user)
}

func TestTestPaymentConfirmationMessageDoesNotMentionSubscription(t *testing.T) {
	message := (&Bot{}).paymentConfirmationMessage(&database.Payment{IsTest: true})
	assert.Equal(t, "✅ Платёжная система работает.", message)
	assert.NotContains(t, message, "подписк")
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

func TestGetPaymentFeeBasisPointsDoesNotApplyPlategaTariffToYooKassa(t *testing.T) {
	b := &Bot{config: &config.Config{PlategaFeeCard: 12, YooKassaFeeBasisPoints: 350}}
	assert.Equal(t, 350, b.getPaymentFeeBasisPoints("yookassa", "card"))
	assert.Equal(t, 1200, b.getPaymentFeeBasisPoints("platega", "card"))
}

func TestHandleConfirmedIgnoresExpiredAlternativePayment(t *testing.T) {
	db, err := database.New(t.TempDir() + "/bot.db")
	require.NoError(t, err)
	t.Cleanup(func() { db.Close() })
	_, err = db.CreateUser(77, "payer", "Payer", strPtrTest("uuid-77"), nil, nil, nil)
	require.NoError(t, err)
	id, err := db.CreatePayment(&database.Payment{TelegramID: 77, Amount: 500, PaymentMethod: "yookassa", Status: "expired", Provider: "yookassa"})
	require.NoError(t, err)
	p, err := db.GetPaymentByID(id)
	require.NoError(t, err)
	h := &paymentCallbackHandler{bot: &Bot{db: db, config: &config.Config{}, userStates: newStateMap()}}
	require.NoError(t, h.handleConfirmed(p))
	stored, err := db.GetPaymentByID(id)
	require.NoError(t, err)
	assert.Equal(t, "expired", stored.Status)
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
	_, err = db.CreateUser(500, "payer", "Payer", strPtrTest("uuid-500"), nil, nil, nil)
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

	_, err = db.CreateUser(501, "payer", "Payer", strPtrTest("uuid-501"), nil, nil, nil)
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
	client := newTestPanelClient()
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

func TestHandleConfirmedDoesNotCreateModeratorEarning(t *testing.T) {
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

	_, err = db.CreateUser(modID, "moderator", "Moderator", strPtrTest("uuid-550"), nil, nil, nil)
	require.NoError(t, err)
	require.NoError(t, db.AddModerator(modID, adminID))
	_, err = db.CreateUser(userID, "payer", "Payer", strPtrTest("uuid-551"), nil, &price, &modID)
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
	client := newTestPanelClient()
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
	assert.Equal(t, 0, count)
}

func TestRetryConfirmedPaymentActivationDoesNotCreateEarning(t *testing.T) {
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

	_, err = db.CreateUser(modID, "moderator", "Moderator", strPtrTest("uuid-560"), nil, nil, nil)
	require.NoError(t, err)
	require.NoError(t, db.AddModerator(modID, adminID))
	_, err = db.CreateUser(userID, "payer", "Payer", strPtrTest("uuid-561"), nil, &price, &modID)
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
	client := newTestPanelClient()
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
					Body:       io.NopCloser(strings.NewReader(`{"response":[{"uuid":"uuid-561","telegramId":561,"status":"ACTIVE","expireAt":"2026-04-20T00:00:00Z"}]}`)),
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
	assert.Equal(t, 0, count)
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
	_, err = db.CreateUser(userID, "payer", "Payer", strPtrTest("uuid-580"), nil, nil, nil)
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

	client := newTestPanelClient()
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
				payload := fmt.Sprintf(`{"response":[{"uuid":"uuid-580","telegramId":580,"username":"payer","status":"ACTIVE","expireAt":"%s"}]}`, expireAt)
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
	_, err = db.CreateUser(700, "user_no_price", "No Price", strPtrTest("uuid-700"), nil, nil, nil)
	require.NoError(t, err)

	// Пользователь с нулевой ценой подписки
	zeroPrice := 0
	_, err = db.CreateUser(701, "user_zero_price", "Zero Price", strPtrTest("uuid-701"), nil, &zeroPrice, nil)
	require.NoError(t, err)

	cfg := &config.Config{AdminID: 999}
	b := &Bot{
		db:         db,
		config:     cfg,
		userStates: newStateMap(),
		remnawave:  newTestPanelClient(),
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
	_, err = db.CreateUser(userID, "payer", "Payer", strPtrTest("uuid-702"), nil, &price, nil)
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

	remClient := newTestPanelClient()
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

	_, err = db.CreateUser(502, "payer", "Payer", strPtrTest("uuid-502"), nil, nil, nil)
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
	client := newTestPanelClient()
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
					Body:       io.NopCloser(strings.NewReader(`{"response":[{"uuid":"uuid-502","telegramId":502,"status":"ACTIVE","expireAt":"2026-04-20T00:00:00Z"}]}`)),
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
	_, err = db.CreateUser(userID, "payer", "Payer", strPtrTest("uuid-590"), nil, nil, nil)
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

	client := newTestPanelClient()
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
