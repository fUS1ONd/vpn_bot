package platega_test

import (
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/fus1ond/vpn_bot/internal/platega"
	"github.com/stretchr/testify/require"
)

// TestPaymentMethodConversion проверяет конвертацию между int и строковым идентификатором
func TestPaymentMethodConversion(t *testing.T) {
	tests := []struct {
		method int
		name   string
		strID  string
	}{
		{platega.PaymentMethodSBP, "СБП", "sbp"},
		{platega.PaymentMethodCard, "Карта", "card"},
		{platega.PaymentMethodCrypto, "Крипта", "crypto"},
	}

	for _, tt := range tests {
		t.Run(tt.strID, func(t *testing.T) {
			require.Equal(t, tt.name, platega.PaymentMethodName(tt.method))
			require.Equal(t, tt.strID, platega.PaymentMethodString(tt.method))
			require.Equal(t, tt.method, platega.PaymentMethodFromString(tt.strID))
		})
	}
}

// TestPaymentMethodConversionUnknown проверяет обработку неизвестных способов оплаты
func TestPaymentMethodConversionUnknown(t *testing.T) {
	require.Equal(t, "Неизвестно", platega.PaymentMethodName(999))
	require.Equal(t, "unknown", platega.PaymentMethodString(999))
	require.Equal(t, 0, platega.PaymentMethodFromString("unknown_method"))
}

// TestClientHeaders проверяет, что клиент устанавливает правильные заголовки авторизации
func TestClientHeaders(t *testing.T) {
	var receivedMerchantID, receivedSecret string

	client := platega.NewClientWithBaseURL("merchant-id-test", "secret-test", "https://platega.test")
	client.SetHTTPClient(&http.Client{
		Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
			receivedMerchantID = r.Header.Get("X-MerchantId")
			receivedSecret = r.Header.Get("X-Secret")

			body := `{"id":"tx-123","paymentDetails":{"amount":500,"currency":"RUB"},"paymentMethod":"SBPQR","status":"PENDING"}`
			return &http.Response{
				StatusCode: http.StatusOK,
				Body:       io.NopCloser(strings.NewReader(body)),
				Header:     make(http.Header),
			}, nil
		}),
	})
	_, _ = client.GetTransactionStatus("tx-123")

	require.Equal(t, "merchant-id-test", receivedMerchantID)
	require.Equal(t, "secret-test", receivedSecret)
}

// TestCreatePayment проверяет создание платежа через мок-сервер
func TestCreatePayment(t *testing.T) {
	client := platega.NewClientWithBaseURL("merchant", "secret", "https://platega.test")
	client.SetHTTPClient(&http.Client{
		Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
			require.Equal(t, "POST", r.Method)
			require.Equal(t, "/transaction/process", r.URL.Path)
			require.Equal(t, "application/json", r.Header.Get("Content-Type"))

			var body map[string]interface{}
			require.NoError(t, json.NewDecoder(r.Body).Decode(&body))
			require.Equal(t, float64(2), body["paymentMethod"]) // СБП = 2
			paymentDetails, ok := body["paymentDetails"].(map[string]interface{})
			require.True(t, ok)
			require.Equal(t, float64(500), paymentDetails["amount"])
			require.Equal(t, "RUB", paymentDetails["currency"])

			resp := `{"transactionId":"tx-abc","redirect":"https://pay.platega.io/tx-abc","status":"PENDING","expiresIn":"00:15:00"}`
			return &http.Response{
				StatusCode: http.StatusOK,
				Body:       io.NopCloser(strings.NewReader(resp)),
				Header:     make(http.Header),
			}, nil
		}),
	})
	resp, err := client.CreatePayment(platega.CreateTransactionRequest{
		PaymentMethod: platega.PaymentMethodSBP,
		Amount:        500,
		Currency:      "RUB",
		Description:   "VPN подписка",
		ReturnURL:     "https://t.me/bot",
		FailedURL:     "https://t.me/bot",
		CallbackURL:   "https://example.com/callback",
		Payload:       "123456",
	})

	require.NoError(t, err)
	require.Equal(t, "tx-abc", resp.TransactionID)
	require.Equal(t, "https://pay.platega.io/tx-abc", resp.Redirect)
	require.Equal(t, "PENDING", resp.Status)
	require.Equal(t, 15*time.Minute, resp.ExpiresIn)
}

// TestCreatePaymentError проверяет обработку ошибки от API
func TestCreatePaymentError(t *testing.T) {
	client := platega.NewClientWithBaseURL("wrong", "wrong", "https://platega.test")
	client.SetHTTPClient(&http.Client{
		Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
			return &http.Response{
				StatusCode: http.StatusUnauthorized,
				Body:       io.NopCloser(strings.NewReader(`{"error":"unauthorized"}`)),
				Header:     make(http.Header),
			}, nil
		}),
	})
	_, err := client.CreatePayment(platega.CreateTransactionRequest{
		PaymentMethod: platega.PaymentMethodSBP,
		Amount:        500,
		Currency:      "RUB",
	})

	require.Error(t, err)
	require.Contains(t, err.Error(), "401")
}

// TestGetTransactionStatus проверяет получение статуса транзакции
func TestGetTransactionStatus(t *testing.T) {
	client := platega.NewClientWithBaseURL("merchant", "secret", "https://platega.test")
	client.SetHTTPClient(&http.Client{
		Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
			require.Equal(t, "GET", r.Method)
			require.Equal(t, "/transaction/tx-xyz", r.URL.Path)

			resp := `{"id":"tx-xyz","paymentDetails":{"amount":500,"currency":"RUB"},"status":"CONFIRMED","paymentMethod":"SBPQR","expiresIn":"00:15:00","payload":"789012"}`
			return &http.Response{
				StatusCode: http.StatusOK,
				Body:       io.NopCloser(strings.NewReader(resp)),
				Header:     make(http.Header),
			}, nil
		}),
	})
	status, err := client.GetTransactionStatus("tx-xyz")

	require.NoError(t, err)
	require.Equal(t, "tx-xyz", status.ID)
	require.Equal(t, 500.0, status.PaymentDetails.Amount)
	require.Equal(t, "RUB", status.PaymentDetails.Currency)
	require.Equal(t, "CONFIRMED", status.Status)
	require.Equal(t, "SBPQR", status.PaymentMethod)
	require.Equal(t, 15*time.Minute, status.ExpiresIn)
	require.Equal(t, "789012", status.Payload)
}

// TestGetTransactionStatusNotFound проверяет обработку 404
func TestGetTransactionStatusNotFound(t *testing.T) {
	client := platega.NewClientWithBaseURL("merchant", "secret", "https://platega.test")
	client.SetHTTPClient(&http.Client{
		Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
			return &http.Response{
				StatusCode: http.StatusNotFound,
				Body:       io.NopCloser(strings.NewReader(`{"error":"not found"}`)),
				Header:     make(http.Header),
			}, nil
		}),
	})
	_, err := client.GetTransactionStatus("nonexistent")

	require.Error(t, err)
	require.Contains(t, err.Error(), "404")
}

// TestClientMerchantAndSecretAccessors проверяет геттеры merchant_id и secret
func TestClientMerchantAndSecretAccessors(t *testing.T) {
	client := platega.NewClient("my-merchant", "my-secret")

	require.Equal(t, "my-merchant", client.MerchantID())
	require.Equal(t, "my-secret", client.Secret())
}

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(r *http.Request) (*http.Response, error) {
	return f(r)
}
