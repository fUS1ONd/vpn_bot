package bot

import (
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/fus1ond/vpn_bot/internal/database"
	"github.com/fus1ond/vpn_bot/internal/platega"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	tele "gopkg.in/telebot.v3"
)

func TestCheckPaymentStatusSyncsCanceledAndChargebacked(t *testing.T) {
	tests := []struct {
		name         string
		remoteStatus string
		wantStatus   string
	}{
		{name: "canceled", remoteStatus: platega.StatusCanceled, wantStatus: "canceled"},
		{name: "chargebacked", remoteStatus: platega.StatusChargebacked, wantStatus: "chargebacked"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			b, db := setupTestBot(t)

			userID := int64(810)
			price := 500
			_, err := db.CreateUser(userID, "payer", "Payer", "uuid-810", &price, nil)
			require.NoError(t, err)

			txID := "tx-810"
			redirect := "https://pay.example/tx-810"
			expiresAt := time.Now().UTC().Add(15 * time.Minute)
			payment := &database.Payment{
				TelegramID:           userID,
				Amount:               price,
				PaymentMethod:        "sbp",
				Status:               "pending",
				PlategaTransactionID: &txID,
				RedirectURL:          &redirect,
				ExpiresAt:            &expiresAt,
			}
			paymentID, err := db.CreatePayment(payment)
			require.NoError(t, err)

			b.platega = platega.NewClientWithBaseURL("merchant", "secret", "https://platega.test")
			b.platega.SetHTTPClient(&http.Client{
				Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
					require.Equal(t, http.MethodGet, r.Method)
					require.Equal(t, "/transaction/"+txID, r.URL.Path)

					respBody, err := json.Marshal(map[string]any{
						"id": txID,
						"paymentDetails": map[string]any{
							"amount":   price,
							"currency": "RUB",
						},
						"status":        tt.remoteStatus,
						"paymentMethod": "SBPQR",
						"expiresIn":     "00:15:00",
						"payload":       "810",
					})
					require.NoError(t, err)

					return &http.Response{
						StatusCode: http.StatusOK,
						Body:       io.NopCloser(strings.NewReader(string(respBody))),
						Header:     make(http.Header),
					}, nil
				}),
			})
			b.remnawave.SetHTTPClient(&http.Client{
				Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
					if tt.remoteStatus == platega.StatusChargebacked && (r.Method == http.MethodPatch || r.Method == http.MethodDelete) {
						return &http.Response{
							StatusCode: http.StatusOK,
							Body:       io.NopCloser(strings.NewReader(`{"response":{}}`)),
							Header:     make(http.Header),
						}, nil
					}
					return nil, assert.AnError
				}),
			})

			status, err := b.checkPaymentStatus(userID)
			require.NoError(t, err)
			assert.Equal(t, tt.remoteStatus, status)

			stored, err := db.GetPaymentByID(paymentID)
			require.NoError(t, err)
			require.NotNil(t, stored)
			assert.Equal(t, tt.wantStatus, stored.Status)
		})
	}
}

func TestHandleCheckPaymentDoesNotClaimActivationWhenEnableFails(t *testing.T) {
	b, db := setupTestBot(t)

	userID := int64(811)
	price := 500
	_, err := db.CreateUser(userID, "payer", "Payer", "uuid-811", &price, nil)
	require.NoError(t, err)

	txID := "tx-811"
	redirect := "https://pay.example/tx-811"
	expiresAt := time.Now().UTC().Add(15 * time.Minute)
	payment := &database.Payment{
		TelegramID:           userID,
		Amount:               price,
		PaymentMethod:        "sbp",
		Status:               "pending",
		PlategaTransactionID: &txID,
		RedirectURL:          &redirect,
		ExpiresAt:            &expiresAt,
	}
	paymentID, err := db.CreatePayment(payment)
	require.NoError(t, err)

	b.platega = platega.NewClientWithBaseURL("merchant", "secret", "https://platega.test")
	b.platega.SetHTTPClient(&http.Client{
		Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
			require.Equal(t, http.MethodGet, r.Method)
			require.Equal(t, "/transaction/"+txID, r.URL.Path)

			respBody, err := json.Marshal(map[string]any{
				"id": txID,
				"paymentDetails": map[string]any{
					"amount":   price,
					"currency": "RUB",
				},
				"status":        platega.StatusConfirmed,
				"paymentMethod": "SBPQR",
				"expiresIn":     "00:15:00",
				"payload":       "811",
			})
			require.NoError(t, err)

			return &http.Response{
				StatusCode: http.StatusOK,
				Body:       io.NopCloser(strings.NewReader(string(respBody))),
				Header:     make(http.Header),
			}, nil
		}),
	})
	b.remnawave.SetHTTPClient(&http.Client{
		Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
			if r.Method == http.MethodGet && r.URL.Path == "/api/users/uuid-811" {
				payload := `{"response":{"uuid":"uuid-811","username":"payer","status":"EXPIRED","expireAt":"2026-03-01T00:00:00Z"}}`
				return &http.Response{
					StatusCode: http.StatusOK,
					Body:       io.NopCloser(strings.NewReader(payload)),
					Header:     make(http.Header),
				}, nil
			}
			return nil, assert.AnError
		}),
	})

	ctx := &MockContext{
		sender:  &tele.User{ID: userID},
		message: &tele.Message{},
	}

	err = b.handleCheckPayment(ctx)
	require.NoError(t, err)

	msg, ok := ctx.sentMsg.(string)
	require.True(t, ok)
	assert.NotContains(t, msg, "Подписка активирована")
	assert.Contains(t, msg, "Оплата подтверждена")
	assert.Contains(t, msg, "активация")

	stored, err := db.GetPaymentByID(paymentID)
	require.NoError(t, err)
	require.NotNil(t, stored)
	assert.Equal(t, "confirmed_not_activated", stored.Status)
	require.NotNil(t, stored.ConfirmedAt)
}

func TestCheckPaymentStatusTreatsManualConfirmedAsConfirmed(t *testing.T) {
	b, db := setupTestBot(t)

	userID := int64(812)
	price := 500
	_, err := db.CreateUser(userID, "payer", "Payer", "uuid-812", &price, nil)
	require.NoError(t, err)

	txID := "tx-812"
	redirect := "https://pay.example/tx-812"
	expiresAt := time.Now().UTC().Add(15 * time.Minute)
	payment := &database.Payment{
		TelegramID:           userID,
		Amount:               price,
		PaymentMethod:        "sbp",
		Status:               "pending",
		PlategaTransactionID: &txID,
		RedirectURL:          &redirect,
		ExpiresAt:            &expiresAt,
	}
	paymentID, err := db.CreatePayment(payment)
	require.NoError(t, err)

	b.platega = platega.NewClientWithBaseURL("merchant", "secret", "https://platega.test")
	b.platega.SetHTTPClient(&http.Client{
		Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
			require.Equal(t, http.MethodGet, r.Method)
			require.Equal(t, "/transaction/"+txID, r.URL.Path)

			respBody, err := json.Marshal(map[string]any{
				"id": txID,
				"paymentDetails": map[string]any{
					"amount":   price,
					"currency": "RUB",
				},
				"status":        "MANUAL_CONFIRMED",
				"paymentMethod": "SBPQR",
				"expiresIn":     "00:15:00",
				"payload":       "812",
			})
			require.NoError(t, err)

			return &http.Response{
				StatusCode: http.StatusOK,
				Body:       io.NopCloser(strings.NewReader(string(respBody))),
				Header:     make(http.Header),
			}, nil
		}),
	})
	b.remnawave.SetHTTPClient(&http.Client{
		Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
			switch {
			case r.Method == http.MethodGet && r.URL.Path == "/api/users/uuid-812":
				payload := `{"response":{"uuid":"uuid-812","username":"payer","status":"EXPIRED","expireAt":"2026-03-01T00:00:00Z"}}`
				return &http.Response{
					StatusCode: http.StatusOK,
					Body:       io.NopCloser(strings.NewReader(payload)),
					Header:     make(http.Header),
				}, nil
			case r.Method == http.MethodGet && r.URL.Path == "/api/users/by-telegram-id/812":
				payload := `{"response":{"uuid":"uuid-812","username":"payer","status":"ACTIVE","expireAt":"2026-04-20T00:00:00Z"}}`
				return &http.Response{
					StatusCode: http.StatusOK,
					Body:       io.NopCloser(strings.NewReader(payload)),
					Header:     make(http.Header),
				}, nil
			case r.Method == http.MethodPatch && r.URL.Path == "/api/users":
				return &http.Response{
					StatusCode: http.StatusOK,
					Body:       io.NopCloser(strings.NewReader(`{"response":{}}`)),
					Header:     make(http.Header),
				}, nil
			default:
				return nil, assert.AnError
			}
		}),
	})

	status, err := b.checkPaymentStatus(userID)
	require.NoError(t, err)
	assert.Equal(t, "confirmed", status)

	stored, err := db.GetPaymentByID(paymentID)
	require.NoError(t, err)
	require.NotNil(t, stored)
	assert.Equal(t, "confirmed", stored.Status)
	require.NotNil(t, stored.ConfirmedAt)
}

func TestHandleCheckPaymentReturnsDetailedSuccessMessage(t *testing.T) {
	b, db := setupTestBot(t)

	userID := int64(815)
	price := 500
	_, err := db.CreateUser(userID, "payer", "Payer", "uuid-815", &price, nil)
	require.NoError(t, err)

	txID := "tx-815"
	redirect := "https://pay.example/tx-815"
	expiresAt := time.Now().UTC().Add(15 * time.Minute)
	payment := &database.Payment{
		TelegramID:           userID,
		Amount:               price,
		PaymentMethod:        "sbp",
		Status:               "pending",
		PlategaTransactionID: &txID,
		RedirectURL:          &redirect,
		ExpiresAt:            &expiresAt,
	}
	paymentID, err := db.CreatePayment(payment)
	require.NoError(t, err)

	b.platega = platega.NewClientWithBaseURL("merchant", "secret", "https://platega.test")
	b.platega.SetHTTPClient(&http.Client{
		Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
			require.Equal(t, http.MethodGet, r.Method)
			require.Equal(t, "/transaction/"+txID, r.URL.Path)

			respBody, err := json.Marshal(map[string]any{
				"id": txID,
				"paymentDetails": map[string]any{
					"amount":   price,
					"currency": "RUB",
				},
				"status":        platega.StatusConfirmed,
				"paymentMethod": "SBPQR",
				"expiresIn":     "00:15:00",
				"payload":       "815",
			})
			require.NoError(t, err)

			return &http.Response{
				StatusCode: http.StatusOK,
				Body:       io.NopCloser(strings.NewReader(string(respBody))),
				Header:     make(http.Header),
			}, nil
		}),
	})
	b.remnawave.SetHTTPClient(&http.Client{
		Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
			switch {
			case r.Method == http.MethodGet && r.URL.Path == "/api/users/uuid-815":
				payload := `{"response":{"uuid":"uuid-815","username":"payer","status":"EXPIRED","expireAt":"2026-03-01T00:00:00Z"}}`
				return &http.Response{
					StatusCode: http.StatusOK,
					Body:       io.NopCloser(strings.NewReader(payload)),
					Header:     make(http.Header),
				}, nil
			case r.Method == http.MethodGet && r.URL.Path == "/api/users/by-telegram-id/815":
				payload := `{"response":{"uuid":"uuid-815","username":"payer","status":"ACTIVE","expireAt":"2026-04-20T00:00:00Z"}}`
				return &http.Response{
					StatusCode: http.StatusOK,
					Body:       io.NopCloser(strings.NewReader(payload)),
					Header:     make(http.Header),
				}, nil
			case r.Method == http.MethodPatch && r.URL.Path == "/api/users":
				return &http.Response{
					StatusCode: http.StatusOK,
					Body:       io.NopCloser(strings.NewReader(`{"response":{}}`)),
					Header:     make(http.Header),
				}, nil
			default:
				return nil, assert.AnError
			}
		}),
	})

	ctx := &MockContext{
		sender:  &tele.User{ID: userID},
		message: &tele.Message{},
	}

	err = b.handleCheckPayment(ctx)
	require.NoError(t, err)

	msg, ok := ctx.sentMsg.(string)
	require.True(t, ok)
	assert.Contains(t, msg, "Ваша подписка активна до")
	assert.Contains(t, msg, "20.04.2026")
	assert.Contains(t, msg, "Лимит трафика снят")
	assert.NotContains(t, msg, "Подписка активирована.")
	assert.Len(t, ctx.sentMsgs, 1)

	stored, err := db.GetPaymentByID(paymentID)
	require.NoError(t, err)
	require.NotNil(t, stored)
	assert.Equal(t, "confirmed", stored.Status)
}

func TestHandlePaymentCallbackDoesNotCreateLegacyModeratorEarning(t *testing.T) {
	b, db := setupTestBot(t)

	adminID := int64(999999)
	oldModeratorID := int64(813)
	userID := int64(814)

	_, err := db.CreateUser(oldModeratorID, "oldmod", "Old Mod", "uuid-oldmod", nil, nil)
	require.NoError(t, err)
	require.NoError(t, db.AddModerator(oldModeratorID, adminID))

	_, err = db.CreateUser(userID, "payer", "Payer", "uuid-payer", nil, &oldModeratorID)
	require.NoError(t, err)

	txID := "tx-814"
	payment := &database.Payment{
		TelegramID:           userID,
		ModeratorID:          &oldModeratorID,
		Amount:               500,
		PaymentMethod:        "card",
		Status:               "pending",
		PlategaTransactionID: &txID,
	}
	paymentID, err := db.CreatePayment(payment)
	require.NoError(t, err)

	require.NoError(t, db.RemoveModerator(oldModeratorID))

	b.remnawave.SetHTTPClient(&http.Client{
		Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
			switch {
			case r.Method == http.MethodGet && r.URL.Path == "/api/users/uuid-payer":
				payload := `{"response":{"uuid":"uuid-payer","username":"payer","status":"EXPIRED","expireAt":"2026-03-01T00:00:00Z"}}`
				return &http.Response{
					StatusCode: http.StatusOK,
					Body:       io.NopCloser(strings.NewReader(payload)),
					Header:     make(http.Header),
				}, nil
			case r.Method == http.MethodPatch && r.URL.Path == "/api/users":
				return &http.Response{
					StatusCode: http.StatusOK,
					Body:       io.NopCloser(strings.NewReader(`{"response":{}}`)),
					Header:     make(http.Header),
				}, nil
			case r.Method == http.MethodGet && r.URL.Path == "/api/users/by-telegram-id/814":
				payload := `{"response":{"uuid":"uuid-payer","username":"payer","status":"ACTIVE","expireAt":"2026-04-20T00:00:00Z"}}`
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

	err = b.PaymentCallbackHandler().HandlePaymentCallback(platega.CallbackPayload{
		ID:            txID,
		Amount:        500,
		Currency:      "RUB",
		Status:        platega.StatusConfirmed,
		PaymentMethod: platega.PaymentMethodCard,
		Payload:       "814",
	})
	require.NoError(t, err)

	var earningCount int
	err = db.Conn().QueryRow(
		`SELECT COUNT(*) FROM moderator_earnings WHERE payment_id = ?`,
		paymentID,
	).Scan(&earningCount)
	require.NoError(t, err)
	assert.Zero(t, earningCount)
	stored, err := db.GetPaymentByID(paymentID)
	require.NoError(t, err)
	require.NotNil(t, stored.ModeratorID, "архивный snapshot старого платежа сохраняется")
	assert.Equal(t, oldModeratorID, *stored.ModeratorID)
}
