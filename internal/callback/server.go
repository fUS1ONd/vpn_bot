package callback

import (
	"context"
	"crypto/subtle"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"time"

	"github.com/fus1ond/vpn_bot/internal/platega"
)

// PaymentHandler — интерфейс обработки подтверждённых платежей.
// Использует platega.CallbackPayload (единственное определение, без дублирования).
type PaymentHandler interface {
	HandlePaymentCallback(payload platega.CallbackPayload) error
}

// Server — HTTP-сервер для приёма callback от Platega
type Server struct {
	merchantID string
	secret     string
	handler    PaymentHandler
	httpServer *http.Server
	mux        *http.ServeMux
	limiter    *ipRateLimiter
}

// NewServer создаёт callback-сервер. port=0 означает автовыбор ОС (для тестов).
func NewServer(port int, merchantID, secret string, handler PaymentHandler) *Server {
	s := &Server{
		merchantID: merchantID,
		secret:     secret,
		handler:    handler,
		limiter:    newIPRateLimiter(10, 20), // 10 req/s, burst 20
	}

	mux := http.NewServeMux()
	mux.HandleFunc("/platega/callback", s.rateLimitMiddleware(s.handleCallback))
	mux.HandleFunc("/health", s.handleHealth)
	s.mux = mux

	s.httpServer = &http.Server{
		Addr:         fmt.Sprintf(":%d", port),
		Handler:      mux,
		ReadTimeout:  10 * time.Second,
		WriteTimeout: 65 * time.Second,
		IdleTimeout:  120 * time.Second,
	}

	return s
}

// Handler возвращает http.Handler для использования в тестах
func (s *Server) Handler() http.Handler {
	return s.mux
}

// Start запускает сервер (блокирующий вызов)
func (s *Server) Start() error {
	slog.Info("Callback server starting", "addr", s.httpServer.Addr)
	return s.httpServer.ListenAndServe()
}

// Shutdown останавливает сервер
func (s *Server) Shutdown(ctx context.Context) error {
	return s.httpServer.Shutdown(ctx)
}

// handleCallback обрабатывает callback от Platega
func (s *Server) handleCallback(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	// Верификация заголовков
	merchantID := r.Header.Get("X-MerchantId")
	secret := r.Header.Get("X-Secret")

	if subtle.ConstantTimeCompare([]byte(merchantID), []byte(s.merchantID)) != 1 ||
		subtle.ConstantTimeCompare([]byte(secret), []byte(s.secret)) != 1 {
		slog.Warn("Callback rejected: invalid credentials",
			"merchant_id", merchantID,
			"remote_addr", r.RemoteAddr,
		)
		http.Error(w, "Unauthorized", http.StatusUnauthorized)
		return
	}

	// Чтение и парсинг тела (лимит 1 МБ)
	body, err := io.ReadAll(io.LimitReader(r.Body, 1<<20))
	if err != nil {
		slog.Error("Callback: не удалось прочитать тело", "error", err)
		http.Error(w, "Bad request", http.StatusBadRequest)
		return
	}

	var payload platega.CallbackPayload
	if err := json.Unmarshal(body, &payload); err != nil {
		slog.Error("Callback: ошибка разбора JSON", "error", err, "body", string(body))
		http.Error(w, "Bad request", http.StatusBadRequest)
		return
	}

	slog.Info("Callback получен",
		"transaction_id", payload.ID,
		"status", payload.Status,
		"amount", payload.Amount,
		"payload", payload.Payload,
	)

	// Обработка через handler
	if err := s.handler.HandlePaymentCallback(payload); err != nil {
		slog.Error("Callback: ошибка handler",
			"error", err,
			"transaction_id", payload.ID,
		)
		// Возвращаем 500, чтобы Platega сделала retry
		http.Error(w, "Internal error", http.StatusInternalServerError)
		return
	}

	w.WriteHeader(http.StatusOK)
	_, _ = w.Write([]byte("OK"))
}

// handleHealth — эндпоинт для проверки работоспособности
func (s *Server) handleHealth(w http.ResponseWriter, r *http.Request) {
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write([]byte("OK"))
}
