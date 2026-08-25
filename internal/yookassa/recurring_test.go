package yookassa

import (
	"encoding/json"
	"errors"
	"net/http"
	"testing"

	"github.com/fus1ond/vpn_bot/internal/paymentprovider"
	"github.com/stretchr/testify/require"
)

func TestCreatePaymentOmitsSaveFlagByDefault(t *testing.T) {
	c := NewClientWithBaseURL("shop", "secret", "https://yookassa.test")
	c.SetHTTPClient(&http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
		var body map[string]any
		require.NoError(t, json.NewDecoder(r.Body).Decode(&body))
		_, present := body["save_payment_method"]
		require.False(t, present, "без явной просьбы поле в запрос не попадает")
		return response(`{"id":"yo","status":"pending","amount":{"value":"400.00","currency":"RUB"}}`), nil
	})})
	_, err := c.CreatePayment(paymentprovider.CreateRequest{Amount: 400, Currency: "RUB", LocalPaymentID: 1})
	require.NoError(t, err)
}

func TestCreatePaymentSendsSaveFlagAndParsesSavedMethod(t *testing.T) {
	c := NewClientWithBaseURL("shop", "secret", "https://yookassa.test")
	c.SetHTTPClient(&http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
		var body map[string]any
		require.NoError(t, json.NewDecoder(r.Body).Decode(&body))
		require.Equal(t, true, body["save_payment_method"])
		return response(`{"id":"yo","status":"succeeded","amount":{"value":"400.00","currency":"RUB"},
			"payment_method":{"type":"bank_card","id":"pm-1","saved":true,"card":{"last4":"4242"}}}`), nil
	})})
	p, err := c.CreatePayment(paymentprovider.CreateRequest{Amount: 400, Currency: "RUB", LocalPaymentID: 1, SavePaymentMethod: true})
	require.NoError(t, err)
	require.Equal(t, "pm-1", p.SavedMethodID)
	require.Equal(t, "•••• 4242", p.SavedMethodTitle)
}

// saved: false — способ не сохранён, а не «сохранён неизвестно какой».
func TestCreatePaymentIgnoresUnsavedMethod(t *testing.T) {
	c := NewClientWithBaseURL("shop", "secret", "https://yookassa.test")
	c.SetHTTPClient(&http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
		return response(`{"id":"yo","status":"succeeded","amount":{"value":"400.00","currency":"RUB"},
			"payment_method":{"type":"bank_card","id":"pm-1","saved":false,"card":{"last4":"4242"}}}`), nil
	})})
	p, err := c.CreatePayment(paymentprovider.CreateRequest{Amount: 400, Currency: "RUB", LocalPaymentID: 1, SavePaymentMethod: true})
	require.NoError(t, err)
	require.Empty(t, p.SavedMethodID)
	require.Empty(t, p.SavedMethodTitle)
}

func TestSavedMethodTitles(t *testing.T) {
	for _, tc := range []struct{ raw, want string }{
		{`{"type":"sbp","id":"pm","saved":true}`, "СБП"},
		{`{"type":"yoo_money","id":"pm","saved":true}`, "ЮMoney"},
		{`{"type":"sber_pay","id":"pm","saved":true}`, "SberPay"},
		{`{"type":"mir_pay","id":"pm","saved":true}`, "Mir Pay"},
		{`{"type":"bank_card","id":"pm","saved":true}`, "Карта"},
		{`{"type":"exotic","id":"pm","saved":true,"title":"Экзотика"}`, "Экзотика"},
	} {
		p, err := parsePayment([]byte(`{"id":"yo","status":"succeeded","amount":{"value":"400.00","currency":"RUB"},"payment_method":` + tc.raw + `}`))
		require.NoError(t, err)
		require.Equal(t, tc.want, p.SavedMethodTitle)
	}
}

func TestChargeSavedMethodSendsRecurringRequest(t *testing.T) {
	c := NewClientWithBaseURL("shop", "secret", "https://yookassa.test")
	c.SetHTTPClient(&http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
		require.Equal(t, http.MethodPost, r.Method)
		require.Equal(t, "/v3/payments", r.URL.Path)
		require.Equal(t, "charge-key", r.Header.Get("Idempotence-Key"))
		var body map[string]any
		require.NoError(t, json.NewDecoder(r.Body).Decode(&body))
		require.Equal(t, "pm-1", body["payment_method_id"])
		require.Equal(t, true, body["capture"])
		require.Equal(t, "400.00", body["amount"].(map[string]any)["value"])
		_, hasConfirmation := body["confirmation"]
		require.False(t, hasConfirmation, "подтверждения от пользователя такой платёж не требует")
		return response(`{"id":"yo-charge","status":"succeeded","amount":{"value":"400.00","currency":"RUB"},
			"payment_method":{"type":"bank_card","id":"pm-1","saved":true,"card":{"last4":"4242"}}}`), nil
	})})
	p, err := c.ChargeSavedMethod(paymentprovider.ChargeRequest{
		Amount: 400, Currency: "RUB", LocalPaymentID: 7, PaymentMethodID: "pm-1", IdempotenceKey: "charge-key",
	})
	require.NoError(t, err)
	require.Equal(t, paymentprovider.StatusSucceeded, p.Status)
	require.Equal(t, "yo-charge", p.ID)
}

func TestChargeSavedMethodRefusesEmptyMethod(t *testing.T) {
	c := NewClientWithBaseURL("shop", "secret", "https://yookassa.test")
	_, err := c.ChargeSavedMethod(paymentprovider.ChargeRequest{Amount: 400, Currency: "RUB"})
	require.Error(t, err)
}

// Обычный отказ карты Способ не гасит: деньги появятся — списание пройдёт.
func TestChargeCanceledWithInsufficientFundsKeepsMethod(t *testing.T) {
	c := NewClientWithBaseURL("shop", "secret", "https://yookassa.test")
	c.SetHTTPClient(&http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
		return response(`{"id":"yo","status":"canceled","amount":{"value":"400.00","currency":"RUB"},
			"cancellation_details":{"party":"payment_network","reason":"insufficient_funds"}}`), nil
	})})
	p, err := c.ChargeSavedMethod(paymentprovider.ChargeRequest{Amount: 400, Currency: "RUB", PaymentMethodID: "pm-1"})
	require.NoError(t, err)
	require.Equal(t, paymentprovider.StatusCanceled, p.Status)
	require.Equal(t, "insufficient_funds", p.CancellationReason)
	require.False(t, p.MethodGone)
}

func TestChargeCanceledWithGoneMethod(t *testing.T) {
	for _, reason := range []string{"permission_revoked", "card_expired", "invalid_card_number", "payment_method_restricted"} {
		t.Run(reason, func(t *testing.T) {
			c := NewClientWithBaseURL("shop", "secret", "https://yookassa.test")
			c.SetHTTPClient(&http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
				return response(`{"id":"yo","status":"canceled","amount":{"value":"400.00","currency":"RUB"},
					"cancellation_details":{"party":"yoo_money","reason":"` + reason + `"}}`), nil
			})})
			p, err := c.ChargeSavedMethod(paymentprovider.ChargeRequest{Amount: 400, Currency: "RUB", PaymentMethodID: "pm-1"})
			require.NoError(t, err)
			require.True(t, p.MethodGone)
		})
	}
}

// pending — это «исход неизвестен», а не ошибка: платёж живёт как обычный
// незавершённый и добирается штатным механизмом.
func TestChargePendingIsNotAnError(t *testing.T) {
	c := NewClientWithBaseURL("shop", "secret", "https://yookassa.test")
	c.SetHTTPClient(&http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
		return response(`{"id":"yo","status":"pending","amount":{"value":"400.00","currency":"RUB"}}`), nil
	})})
	p, err := c.ChargeSavedMethod(paymentprovider.ChargeRequest{Amount: 400, Currency: "RUB", PaymentMethodID: "pm-1"})
	require.NoError(t, err)
	require.Equal(t, paymentprovider.StatusPending, p.Status)
}

// Транспортный сбой повторяется общим механизмом и в итоге отдаётся ошибкой.
func TestChargeRetriesTransportFailure(t *testing.T) {
	var attempts int
	c := NewClientWithBaseURL("shop", "secret", "https://yookassa.test")
	c.SetRetryBackoff(0)
	c.SetHTTPClient(&http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
		attempts++
		return nil, errors.New("TLS handshake timeout")
	})})
	_, err := c.ChargeSavedMethod(paymentprovider.ChargeRequest{Amount: 400, Currency: "RUB", PaymentMethodID: "pm-1", IdempotenceKey: "k"})
	require.Error(t, err)
	require.Equal(t, maxAttempts, attempts)
}

// Клиент обязан удовлетворять контракту рекуррентного провайдера.
func TestClientImplementsRecurringProvider(t *testing.T) {
	var _ paymentprovider.RecurringProvider = NewClient("shop", "secret")
}
