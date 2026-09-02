// Command mockpanel — заглушка Remnawave API для ручной проверки бота без
// настоящей панели.
//
// Нужна там, где панель поднимать незачем или нельзя: прогнать пользовательский
// flow, снять скриншоты интерфейса, проверить платёжный путь на тестовом
// магазине кассы. Состояние держится в памяти и умирает вместе с процессом —
// это стенд, а не сервис.
//
// Запуск: go run ./cmd/mockpanel  (порт из MOCK_PANEL_PORT, по умолчанию 8081)
package main

import (
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"strconv"
	"strings"
	"sync"
	"time"
)

// panelVersion определяет, какой контракт увидит бот: 2.8.x адресует
// пользователя UUID, 3.x — числовым id. Меняется через MOCK_PANEL_VERSION,
// чтобы одним стендом проверять обе ветки.
var panelVersion = envOr("MOCK_PANEL_VERSION", "2.8.1")

type user struct {
	ID                int64     `json:"id"`
	UUID              string    `json:"uuid"`
	ShortUUID         string    `json:"shortUuid"`
	Username          string    `json:"username"`
	Status            string    `json:"status"`
	TelegramID        *int64    `json:"telegramId"`
	TrafficLimitBytes int64     `json:"trafficLimitBytes"`
	HwidDeviceLimit   int       `json:"hwidDeviceLimit"`
	SubscriptionURL   string    `json:"subscriptionUrl"`
	CreatedAt         time.Time `json:"createdAt"`
	ExpireAt          time.Time `json:"expireAt"`
	UserTraffic       *traffic  `json:"userTraffic"`
}

type traffic struct {
	UsedTrafficBytes         int64 `json:"usedTrafficBytes"`
	LifetimeUsedTrafficBytes int64 `json:"lifetimeUsedTrafficBytes"`
}

type store struct {
	mu     sync.Mutex
	nextID int64
	users  map[string]*user // ключ — UUID
}

func (s *store) byUUID(uuid string) *user { return s.users[uuid] }

func (s *store) byID(id int64) *user {
	for _, u := range s.users {
		if u.ID == id {
			return u
		}
	}
	return nil
}

// find принимает и UUID, и числовой id: бот адресует пользователя по-разному в
// зависимости от версии панели, а стенд обязан отвечать в обеих.
func (s *store) find(segment string) *user {
	if u := s.byUUID(segment); u != nil {
		return u
	}
	if id, err := strconv.ParseInt(segment, 10, 64); err == nil {
		return s.byID(id)
	}
	return nil
}

func (s *store) byTelegramID(telegramID int64) []user {
	var found []user
	for _, u := range s.users {
		if u.TelegramID != nil && *u.TelegramID == telegramID {
			found = append(found, *u)
		}
	}
	return found
}

func (s *store) list() []user {
	all := make([]user, 0, len(s.users))
	for _, u := range s.users {
		all = append(all, *u)
	}
	return all
}

func main() {
	s := &store{nextID: 1, users: map[string]*user{}}
	mux := http.NewServeMux()

	mux.HandleFunc("/api/system/metadata", func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, map[string]any{"response": map[string]string{"version": panelVersion}})
	})

	mux.HandleFunc("/api/users", func(w http.ResponseWriter, r *http.Request) {
		s.mu.Lock()
		defer s.mu.Unlock()

		switch r.Method {
		case http.MethodGet:
			all := s.list()
			writeJSON(w, map[string]any{"response": map[string]any{"users": all, "total": len(all)}})
		case http.MethodPost:
			writeJSON(w, map[string]any{"response": s.create(r)})
		case http.MethodPatch:
			u := s.patch(r)
			if u == nil {
				writeError(w, http.StatusNotFound, "user not found")
				return
			}
			writeJSON(w, map[string]any{"response": u})
		default:
			writeError(w, http.StatusMethodNotAllowed, "method not allowed")
		}
	})

	mux.HandleFunc("/api/users/", func(w http.ResponseWriter, r *http.Request) {
		s.mu.Lock()
		defer s.mu.Unlock()

		rest := strings.TrimPrefix(r.URL.Path, "/api/users/")

		if id, ok := strings.CutPrefix(rest, "by-telegram-id/"); ok {
			telegramID, _ := strconv.ParseInt(id, 10, 64)
			writeJSON(w, map[string]any{"response": s.byTelegramID(telegramID)})
			return
		}
		if rest == "stream" {
			telegramID, _ := strconv.ParseInt(r.URL.Query().Get("telegramId"), 10, 64)
			writeJSON(w, map[string]any{"response": map[string]any{"users": s.byTelegramID(telegramID)}})
			return
		}

		segment, action, _ := strings.Cut(rest, "/")
		u := s.find(segment)
		if u == nil {
			writeError(w, http.StatusNotFound, "user not found")
			return
		}
		// Перевыпуск ссылки: короткий идентификатор меняется, остальное нет.
		if strings.HasPrefix(action, "actions/revoke") {
			u.ShortUUID = fmt.Sprintf("short-%d", time.Now().UnixNano())
			u.SubscriptionURL = "https://sub.example.com/" + u.ShortUUID
		}
		if r.Method == http.MethodDelete {
			delete(s.users, u.UUID)
			w.WriteHeader(http.StatusNoContent)
			return
		}
		writeJSON(w, map[string]any{"response": u})
	})

	// Пульт стенда. Префикс /mock/ намеренно не похож на /api/: это не часть
	// контракта Remnawave, а рычаги, которых у настоящей панели нет.
	mux.HandleFunc("/mock/user", func(w http.ResponseWriter, r *http.Request) {
		s.mu.Lock()
		defer s.mu.Unlock()

		u, err := s.control(r)
		if err != nil {
			writeError(w, http.StatusBadRequest, err.Error())
			return
		}
		if u == nil {
			writeError(w, http.StatusNotFound, "user not found")
			return
		}
		writeJSON(w, map[string]any{"response": u})
	})

	// Устройств у стенда нет: HWID-лимиты к платёжному flow отношения не имеют.
	mux.HandleFunc("/api/hwid/devices/", func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, map[string]any{"response": map[string]any{"total": 0, "devices": []any{}}})
	})
	mux.HandleFunc("/api/nodes", func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, map[string]any{"response": []any{}})
	})
	mux.HandleFunc("/api/hosts", func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, map[string]any{"response": []any{}})
	})

	port := envOr("MOCK_PANEL_PORT", "8081")
	slog.Info("Mock Remnawave panel started", "port", port, "version", panelVersion)
	if err := http.ListenAndServe(":"+port, logRequests(mux)); err != nil {
		slog.Error("mock panel stopped", "error", err)
		os.Exit(1)
	}
}

// create заводит пользователя. Значения полей берутся из запроса бота, чтобы
// стенд отвечал тем же, что у него попросили.
func (s *store) create(r *http.Request) *user {
	var req struct {
		Username          string `json:"username"`
		TelegramID        *int64 `json:"telegramId"`
		TrafficLimitBytes int64  `json:"trafficLimitBytes"`
		ExpireAt          string `json:"expireAt"`
	}
	_ = json.NewDecoder(r.Body).Decode(&req)

	expireAt, err := time.Parse(time.RFC3339, req.ExpireAt)
	if err != nil {
		expireAt = time.Now().UTC().AddDate(0, 1, 0)
	}

	id := s.nextID
	s.nextID++
	u := &user{
		ID:                id,
		UUID:              fmt.Sprintf("mock-uuid-%d", id),
		ShortUUID:         fmt.Sprintf("short-%d", id),
		Username:          req.Username,
		Status:            "ACTIVE",
		TelegramID:        req.TelegramID,
		TrafficLimitBytes: req.TrafficLimitBytes,
		HwidDeviceLimit:   3,
		SubscriptionURL:   fmt.Sprintf("https://sub.example.com/short-%d", id),
		CreatedAt:         time.Now().UTC(),
		ExpireAt:          expireAt,
		UserTraffic:       &traffic{},
	}
	s.users[u.UUID] = u
	return u
}

// patch применяет изменения статуса, срока и лимита трафика.
func (s *store) patch(r *http.Request) *user {
	var req struct {
		UUID              string  `json:"uuid"`
		ID                int64   `json:"id"`
		Status            *string `json:"status"`
		ExpireAt          *string `json:"expireAt"`
		TrafficLimitBytes *int64  `json:"trafficLimitBytes"`
		Username          *string `json:"username"`
	}
	_ = json.NewDecoder(r.Body).Decode(&req)

	u := s.byUUID(req.UUID)
	if u == nil && req.ID != 0 {
		u = s.byID(req.ID)
	}
	if u == nil {
		return nil
	}

	if req.Status != nil {
		u.Status = *req.Status
	}
	if req.ExpireAt != nil {
		if t, err := time.Parse(time.RFC3339, *req.ExpireAt); err == nil {
			u.ExpireAt = t
		}
	}
	if req.TrafficLimitBytes != nil {
		u.TrafficLimitBytes = *req.TrafficLimitBytes
	}
	if req.Username != nil {
		u.Username = *req.Username
	}
	return u
}

// control — рычаги стенда, которых у настоящей панели нет: подкрутить
// пользователю срок, статус и потраченный трафик, чтобы увидеть ветку интерфейса
// или шаг планировщика, не дожидаясь реального времени.
//
// Срок задаётся **относительно «сейчас»**, а не абсолютной датой, потому что
// проверяются окна планировщика, и все они считаются от текущего момента:
// уведомление за 3 дня ждёт expireAt через 48–72 часа, за 1 день — через 0–24,
// истечение — прошедший expireAt, grace-кик — прошедший больше чем на 72 часа.
// С абсолютной датой это пришлось бы каждый раз считать руками.
func (s *store) control(r *http.Request) (*user, error) {
	if r.Method != http.MethodPost {
		return nil, fmt.Errorf("нужен POST")
	}

	var req struct {
		TelegramID    int64    `json:"telegramId"`
		ExpireInHours *float64 `json:"expireInHours"`
		Status        *string  `json:"status"`
		UsedTrafficGB *float64 `json:"usedTrafficGB"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		return nil, fmt.Errorf("некорректный JSON: %w", err)
	}
	if req.TelegramID == 0 {
		return nil, fmt.Errorf("нужен telegramId")
	}

	found := s.byTelegramID(req.TelegramID)
	if len(found) == 0 {
		return nil, nil
	}
	u := s.byUUID(found[0].UUID)

	if req.ExpireInHours != nil {
		u.ExpireAt = time.Now().UTC().Add(time.Duration(*req.ExpireInHours * float64(time.Hour)))
	}
	if req.Status != nil {
		u.Status = *req.Status
	}
	if req.UsedTrafficGB != nil {
		if u.UserTraffic == nil {
			u.UserTraffic = &traffic{}
		}
		u.UserTraffic.UsedTrafficBytes = int64(*req.UsedTrafficGB * 1024 * 1024 * 1024)
	}
	return u, nil
}

func writeJSON(w http.ResponseWriter, payload any) {
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(payload)
}

func writeError(w http.ResponseWriter, code int, message string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	_ = json.NewEncoder(w).Encode(map[string]string{"message": message})
}

func logRequests(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		slog.Info("mock panel request", "method", r.Method, "path", r.URL.Path)
		next.ServeHTTP(w, r)
	})
}

func envOr(key, fallback string) string {
	if v := strings.TrimSpace(os.Getenv(key)); v != "" {
		return v
	}
	return fallback
}
