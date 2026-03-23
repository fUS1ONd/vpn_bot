package platega_test

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

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

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		receivedMerchantID = r.Header.Get("X-MerchantId")
		receivedSecret = r.Header.Get("X-Secret")
		// Возвращаем минимальный валидный ответ
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		json.NewEncoder(w).Encode(map[string]interface{}{
			"id":     "tx-123",
			"status": "PENDING",
		})
	}))
	defer server.Close()

	client := platega.NewClientWithBaseURL("merchant-id-test", "secret-test", server.URL)
	_, _ = client.GetTransactionStatus("tx-123")

	require.Equal(t, "merchant-id-test", receivedMerchantID)
	require.Equal(t, "secret-test", receivedSecret)
}

// TestCreatePayment проверяет создание платежа через мок-сервер
func TestCreatePayment(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		require.Equal(t, "POST", r.Method)
		require.Equal(t, "/transaction/process", r.URL.Path)
		require.Equal(t, "application/json", r.Header.Get("Content-Type"))

		var body map[string]interface{}
		require.NoError(t, json.NewDecoder(r.Body).Decode(&body))
		require.Equal(t, float64(2), body["paymentMethod"]) // СБП = 2

		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		json.NewEncoder(w).Encode(map[string]interface{}{
			"transactionId": "tx-abc",
			"redirect":      "https://pay.platega.io/tx-abc",
			"status":        "PENDING",
			"expiresIn":     900,
		})
	}))
	defer server.Close()

	client := platega.NewClientWithBaseURL("merchant", "secret", server.URL)
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
	require.Equal(t, 900, resp.ExpiresIn)
}

// TestCreatePaymentError проверяет обработку ошибки от API
func TestCreatePaymentError(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
		w.Write([]byte(`{"error": "unauthorized"}`))
	}))
	defer server.Close()

	client := platega.NewClientWithBaseURL("wrong", "wrong", server.URL)
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
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		require.Equal(t, "GET", r.Method)
		require.Equal(t, "/transaction/tx-xyz", r.URL.Path)

		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		json.NewEncoder(w).Encode(map[string]interface{}{
			"id":            "tx-xyz",
			"amount":        "500",
			"currency":      "RUB",
			"status":        "CONFIRMED",
			"paymentMethod": 2,
			"payload":       "789012",
		})
	}))
	defer server.Close()

	client := platega.NewClientWithBaseURL("merchant", "secret", server.URL)
	status, err := client.GetTransactionStatus("tx-xyz")

	require.NoError(t, err)
	require.Equal(t, "tx-xyz", status.ID)
	require.Equal(t, "500", status.Amount)
	require.Equal(t, "CONFIRMED", status.Status)
	require.Equal(t, 2, status.PaymentMethod)
	require.Equal(t, "789012", status.Payload)
}

// TestGetTransactionStatusNotFound проверяет обработку 404
func TestGetTransactionStatusNotFound(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNotFound)
		w.Write([]byte(`{"error": "not found"}`))
	}))
	defer server.Close()

	client := platega.NewClientWithBaseURL("merchant", "secret", server.URL)
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
