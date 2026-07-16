package callback_test

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/fus1ond/vpn_bot/internal/callback"
	"github.com/fus1ond/vpn_bot/internal/platega"
)

// mockHandler — тестовая реализация PaymentHandler
type mockHandler struct {
	called  bool
	payload platega.CallbackPayload
	err     error
}

type mockYooKassaHandler struct {
	id  string
	err error
}

func (m *mockYooKassaHandler) HandleYooKassaWebhook(id string) error { m.id = id; return m.err }

func (m *mockHandler) HandlePaymentCallback(p platega.CallbackPayload) error {
	m.called = true
	m.payload = p
	return m.err
}

const (
	testMerchantID = "merchant-123"
	testSecret     = "secret-abc"
)

func newTestServer(handler callback.PaymentHandler) *callback.Server {
	return callback.NewServer(0, testMerchantID, testSecret, handler)
}

// TestCallbackVerification — отклонение запросов с неверными заголовками (401)
func TestCallbackVerification(t *testing.T) {
	srv := newTestServer(&mockHandler{})

	tests := []struct {
		name       string
		merchantID string
		secret     string
	}{
		{"пустые заголовки", "", ""},
		{"неверный merchantID", "wrong-merchant", testSecret},
		{"неверный secret", testMerchantID, "wrong-secret"},
		{"оба неверны", "wrong-merchant", "wrong-secret"},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			body := makeCallbackBody(t, platega.CallbackPayload{ID: "tx-1", Status: platega.StatusConfirmed})
			req := httptest.NewRequest(http.MethodPost, "/platega/callback", bytes.NewReader(body))
			req.Header.Set("X-MerchantId", tc.merchantID)
			req.Header.Set("X-Secret", tc.secret)
			req.Header.Set("Content-Type", "application/json")

			w := httptest.NewRecorder()
			srv.Handler().ServeHTTP(w, req)

			if w.Code != http.StatusUnauthorized {
				t.Errorf("ожидали 401, получили %d", w.Code)
			}
		})
	}
}

// TestCallbackValidRequest — приём запроса с корректными заголовками (200)
func TestCallbackValidRequest(t *testing.T) {
	handler := &mockHandler{}
	srv := newTestServer(handler)

	payload := platega.CallbackPayload{
		ID:     "tx-42",
		Status: platega.StatusConfirmed,
		Amount: 500,
	}
	body := makeCallbackBody(t, payload)

	req := httptest.NewRequest(http.MethodPost, "/platega/callback", bytes.NewReader(body))
	req.Header.Set("X-MerchantId", testMerchantID)
	req.Header.Set("X-Secret", testSecret)
	req.Header.Set("Content-Type", "application/json")

	w := httptest.NewRecorder()
	srv.Handler().ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("ожидали 200, получили %d", w.Code)
	}
	if !handler.called {
		t.Error("HandlePaymentCallback не был вызван")
	}
	if handler.payload.ID != "tx-42" {
		t.Errorf("ожидали ID=tx-42, получили %s", handler.payload.ID)
	}
	if handler.payload.Amount != 500 {
		t.Errorf("ожидали amount=500, получили %v", handler.payload.Amount)
	}
}

// TestCallbackHealth — проверка /health (200)
func TestCallbackHealth(t *testing.T) {
	srv := newTestServer(&mockHandler{})

	req := httptest.NewRequest(http.MethodGet, "/health", nil)
	w := httptest.NewRecorder()
	srv.Handler().ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("ожидали 200, получили %d", w.Code)
	}
}

func TestYooKassaWebhookPassesOnlyExternalPaymentID(t *testing.T) {
	srv := newTestServer(&mockHandler{})
	h := &mockYooKassaHandler{}
	srv.SetYooKassaHandler(h)
	req := httptest.NewRequest(http.MethodPost, "/yookassa/webhook", bytes.NewBufferString(`{"event":"payment.succeeded","object":{"id":"yo-42","status":"succeeded"}}`))
	w := httptest.NewRecorder()
	srv.Handler().ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("ожидали 200, получили %d", w.Code)
	}
	if h.id != "yo-42" {
		t.Fatalf("ожидали ID yo-42, получили %q", h.id)
	}
}

// TestCallbackInvalidJSON — некорректный JSON (400)
func TestCallbackInvalidJSON(t *testing.T) {
	srv := newTestServer(&mockHandler{})

	req := httptest.NewRequest(http.MethodPost, "/platega/callback", bytes.NewReader([]byte("not-json")))
	req.Header.Set("X-MerchantId", testMerchantID)
	req.Header.Set("X-Secret", testSecret)
	req.Header.Set("Content-Type", "application/json")

	w := httptest.NewRecorder()
	srv.Handler().ServeHTTP(w, req)

	if w.Code != http.StatusBadRequest {
		t.Errorf("ожидали 400, получили %d", w.Code)
	}
}

func makeCallbackBody(t *testing.T, p platega.CallbackPayload) []byte {
	t.Helper()
	b, err := json.Marshal(p)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	return b
}
