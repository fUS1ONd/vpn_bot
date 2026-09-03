// Command mockpanel — заглушка Remnawave API для ручной проверки бота без
// настоящей панели.
//
// Нужна там, где панель поднимать незачем или нельзя: прогнать пользовательский
// flow, снять скриншоты интерфейса, проверить платёжный путь на тестовом
// магазине кассы. Состояние держится в памяти и умирает вместе с процессом —
// это стенд, а не сервис.
//
// Два семейства маршрутов, и путать их нельзя:
//
//   - /api/... — контракт Remnawave. Заглушка обязана отвечать в нём ровно то,
//     что ожидает боевой клиент, включая различия 2.8.x и 3.x.
//   - /mock/... — пульт стенда: рычаги, которых у настоящей панели нет. Срок,
//     статус, трафик, устройства, отказы и задержки.
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

// isV3 — эмулируем ли контракт 3.x. Различие не косметическое: в 3.x у
// пользователя нет колонки uuid вовсе, а параметр пути объявлен числом, поэтому
// панель отвечает 400 на нечисловой сегмент. Именно на этот 400 у бота завязан
// ре-детект версии (`doUserRequest` в internal/remnawave/userref.go) —
// единственная страховка на случай апгрейда панели без перезапуска бота. Отдавая
// на 3.x uuid и 200, заглушка делала бы эту ветку недостижимой.
func isV3() bool { return strings.HasPrefix(panelVersion, "3.") }

type user struct {
	ID int64 `json:"id"`
	// omitempty плюс очистка в userView: на 3.x поля быть не должно, иначе бот
	// на стенде никогда не пройдёт веткой `remnawave_uuid IS NULL`.
	UUID              string    `json:"uuid,omitempty"`
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

	// devices не уходит в JSON пользователя: у настоящей панели устройства
	// живут за отдельным HWID-маршрутом, и заглушка обязана повторять это,
	// иначе бот на стенде пошёл бы не тем путём, что в проде.
	devices []device
}

type traffic struct {
	UsedTrafficBytes         int64 `json:"usedTrafficBytes"`
	LifetimeUsedTrafficBytes int64 `json:"lifetimeUsedTrafficBytes"`
}

// device повторяет remnawave.HwidDevice — то, что бот читает из HWID API.
type device struct {
	Hwid        string `json:"hwid"`
	Platform    string `json:"platform"`
	OsVersion   string `json:"osVersion"`
	DeviceModel string `json:"deviceModel"`
}

type store struct {
	mu     sync.Mutex
	nextID int64
	users  map[string]*user // ключ — UUID
}

func newStore() *store { return &store{nextID: 1, users: map[string]*user{}} }

// userView готовит пользователя к выдаче наружу. Внутри UUID есть всегда — он
// служит ключом хранилища, — но на 3.x панель его не отдаёт, и заглушка обязана
// вести себя так же.
func userView(u *user) user {
	view := *u
	if isV3() {
		view.UUID = ""
	}
	return view
}

func userViews(users []user) []user {
	views := make([]user, 0, len(users))
	for i := range users {
		views = append(views, userView(&users[i]))
	}
	return views
}

// numericSegment сообщает, годится ли сегмент пути как идентификатор под текущий
// контракт. На 3.x путь объявлен числом, и нечисловой сегмент панель отвергает
// четырёхсоткой — тем самым сигналом, по которому бот перечитывает версию.
func numericSegment(segment string) bool {
	_, err := strconv.ParseInt(segment, 10, 64)
	return err == nil
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

// firstByTelegramID возвращает указатель на запись, а не копию: пульт меняет
// пользователя на месте.
func (s *store) firstByTelegramID(telegramID int64) *user {
	found := s.byTelegramID(telegramID)
	if len(found) == 0 {
		return nil
	}
	return s.byUUID(found[0].UUID)
}

func (s *store) list() []user {
	all := make([]user, 0, len(s.users))
	for _, u := range s.users {
		all = append(all, *u)
	}
	return all
}

// chaos — рычаги отказов и задержек. Нужны, чтобы проверять то, что иначе на
// стенде не воспроизвести: как выглядит интерфейс, когда панель отвалилась или
// отвечает медленно.
//
// Собственный мьютекс, а не мьютекс store: задержка выдерживается до входа в
// обработчик, и держать на ней блокировку данных нельзя.
type chaos struct {
	mu        sync.Mutex
	failCount int           // сколько ближайших запросов к /api/ провалить
	failCode  int           // каким статусом
	latency   time.Duration // задержка перед ответом
}

// nextFailure возвращает статус, которым надо провалить текущий запрос, и
// уменьшает счётчик. Ноль означает «пропустить как обычно».
func (c *chaos) nextFailure() int {
	c.mu.Lock()
	defer c.mu.Unlock()

	if c.failCount <= 0 {
		return 0
	}
	c.failCount--
	return c.failCode
}

func (c *chaos) delay() time.Duration {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.latency
}

func (c *chaos) setFailure(count, code int) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.failCount, c.failCode = count, code
}

func (c *chaos) setLatency(d time.Duration) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.latency = d
}

// newServer собирает маршруты стенда. Отдельная функция, а не тело main, чтобы
// тесты поднимали её через httptest без сети и переменных окружения.
func newServer() http.Handler {
	s := newStore()
	c := &chaos{}

	api := http.NewServeMux()
	registerPanelRoutes(api, s)

	root := http.NewServeMux()
	// Контракт панели — под рычагами отказов и задержек.
	root.Handle("/api/", withChaos(c, api))
	// Пульт — вне рычагов: иначе включённый отказ невозможно было бы выключить.
	registerControlRoutes(root, s, c)

	return logRequests(root)
}

// registerPanelRoutes — маршруты контракта Remnawave.
func registerPanelRoutes(mux *http.ServeMux, s *store) {
	mux.HandleFunc("/api/system/metadata", func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, map[string]any{"response": map[string]string{"version": panelVersion}})
	})

	mux.HandleFunc("/api/users", func(w http.ResponseWriter, r *http.Request) {
		// Тело разбирается до взятия блокировки: клиент, открывший POST и не
		// дославший тело, иначе держал бы мьютекс данных, и весь стенд вставал
		// бы за ним молча, без единой строки в логе.
		switch r.Method {
		case http.MethodGet:
			s.mu.Lock()
			defer s.mu.Unlock()

			all := userViews(s.list())
			writeJSON(w, map[string]any{"response": map[string]any{"users": all, "total": len(all)}})
		case http.MethodPost:
			var req createRequest
			_ = json.NewDecoder(r.Body).Decode(&req)

			s.mu.Lock()
			defer s.mu.Unlock()

			writeJSON(w, map[string]any{"response": userView(s.create(req))})
		case http.MethodPatch:
			var req patchRequest
			_ = json.NewDecoder(r.Body).Decode(&req)

			s.mu.Lock()
			defer s.mu.Unlock()

			u := s.patch(req)
			if u == nil {
				writeError(w, http.StatusNotFound, "user not found")
				return
			}
			writeJSON(w, map[string]any{"response": userView(u)})
		default:
			writeError(w, http.StatusMethodNotAllowed, "method not allowed")
		}
	})

	mux.HandleFunc("/api/users/", func(w http.ResponseWriter, r *http.Request) {
		rest := strings.TrimPrefix(r.URL.Path, "/api/users/")

		// Резолв UUID → числовой id. Разбирается раньше остальных: иначе
		// «resolve» уйдёт в поиск как идентификатор пользователя и обернётся
		// 404, а бот трактует 404 здесь как «связку добрать нечем» и молча
		// признаёт backfill несостоявшимся.
		if rest == "resolve" {
			var req struct {
				UUID string `json:"uuid"`
			}
			_ = json.NewDecoder(r.Body).Decode(&req)

			s.mu.Lock()
			defer s.mu.Unlock()

			s.resolve(w, req.UUID)
			return
		}

		s.mu.Lock()
		defer s.mu.Unlock()

		if id, ok := strings.CutPrefix(rest, "by-telegram-id/"); ok {
			telegramID, _ := strconv.ParseInt(id, 10, 64)
			writeJSON(w, map[string]any{"response": userViews(s.byTelegramID(telegramID))})
			return
		}
		if rest == "stream" {
			telegramID, _ := strconv.ParseInt(r.URL.Query().Get("telegramId"), 10, 64)
			writeJSON(w, map[string]any{"response": map[string]any{
				"users":   userViews(s.byTelegramID(telegramID)),
				"hasMore": false,
			}})
			return
		}

		segment, action, _ := strings.Cut(rest, "/")
		if isV3() && !numericSegment(segment) {
			writeError(w, http.StatusBadRequest, "user id must be a number")
			return
		}

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
		writeJSON(w, map[string]any{"response": userView(u)})
	})

	// HWID-маршруты. Три разных операции живут под одним префиксом, поэтому
	// «delete» и «delete-all» разбираются до того, как остаток пути будет
	// принят за идентификатор пользователя.
	mux.HandleFunc("/api/hwid/devices/", func(w http.ResponseWriter, r *http.Request) {
		rest := strings.TrimPrefix(r.URL.Path, "/api/hwid/devices/")

		switch rest {
		case "delete", "delete-all":
			// Тело разбирается до блокировки — см. комментарий в /api/users.
			var body hwidRequest
			_ = json.NewDecoder(r.Body).Decode(&body)

			s.mu.Lock()
			defer s.mu.Unlock()

			u := s.userFromHwidBody(body)
			if u == nil {
				writeError(w, http.StatusNotFound, "user not found")
				return
			}
			if rest == "delete" {
				u.removeDevice(body.Hwid)
			} else {
				u.devices = nil
			}
			writeDevices(w, u)
		default:
			s.mu.Lock()
			defer s.mu.Unlock()

			if isV3() && !numericSegment(rest) {
				writeError(w, http.StatusBadRequest, "user id must be a number")
				return
			}
			u := s.find(rest)
			if u == nil {
				writeError(w, http.StatusNotFound, "user not found")
				return
			}
			writeDevices(w, u)
		}
	})

	mux.HandleFunc("/api/nodes", func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, map[string]any{"response": []any{}})
	})
	mux.HandleFunc("/api/hosts", func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, map[string]any{"response": []any{}})
	})
}

// hwidRequest — тело HWID-запросов бота. На 2.8.x приходит userUuid строкой, на
// 3.x — userId числом.
type hwidRequest struct {
	UserUUID string `json:"userUuid"`
	UserID   int64  `json:"userId"`
	Hwid     string `json:"hwid"`
}

// userFromHwidBody принимает обе формы адресации: какую прислать, решает клиент
// по детекту версии, и заглушке нечего на этом настаивать.
func (s *store) userFromHwidBody(req hwidRequest) *user {
	if req.UserUUID != "" {
		return s.byUUID(req.UserUUID)
	}
	if req.UserID != 0 {
		return s.byID(req.UserID)
	}
	return nil
}

// removeDevice убирает одно устройство. Ответ на удаление — оставшийся список:
// бот перерисовывает экран прямо из него, не перезапрашивая.
func (u *user) removeDevice(hwid string) {
	kept := make([]device, 0, len(u.devices))
	for _, d := range u.devices {
		if d.Hwid != hwid {
			kept = append(kept, d)
		}
	}
	u.devices = kept
}

// resolve отвечает на POST /api/users/resolve — точечный добор числового id по
// UUID при заполнении связки (`BackfillRemnawaveIDs`).
//
// На 3.x резолвить по UUID нечего: колонки уже нет. Клиент туда и не ходит
// (`ResolveUserByUUID` отказывает по версии до запроса), но заглушка отвечает
// честной четырёхсоткой, а не выдумывает ответ, которого панель дать не может.
func (s *store) resolve(w http.ResponseWriter, uuid string) {
	if isV3() {
		writeError(w, http.StatusBadRequest, "resolve by uuid is unavailable on 3.x")
		return
	}

	u := s.byUUID(uuid)
	if u == nil {
		writeError(w, http.StatusNotFound, "user not found")
		return
	}

	writeJSON(w, map[string]any{"response": map[string]any{
		"uuid":      u.UUID,
		"id":        u.ID,
		"shortUuid": u.ShortUUID,
		"username":  u.Username,
	}})
}

func writeDevices(w http.ResponseWriter, u *user) {
	devices := u.devices
	if devices == nil {
		devices = []device{}
	}
	writeJSON(w, map[string]any{"response": map[string]any{"total": len(devices), "devices": devices}})
}

// registerControlRoutes — пульт стенда. Префикс /mock/ намеренно не похож на
// /api/: это не часть контракта Remnawave.
func registerControlRoutes(mux *http.ServeMux, s *store, c *chaos) {
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
		devices := u.devices
		if devices == nil {
			devices = []device{}
		}
		writeJSON(w, map[string]any{"response": userView(u), "devices": devices})
	})

	mux.HandleFunc("/mock/fail", func(w http.ResponseWriter, r *http.Request) {
		var req struct {
			Count  int `json:"count"`
			Status int `json:"status"`
		}
		if err := decodePost(r, &req); err != nil {
			writeError(w, http.StatusBadRequest, err.Error())
			return
		}

		if req.Status == 0 {
			req.Status = http.StatusInternalServerError
		}
		if req.Count < 0 {
			req.Count = 0
		}
		c.setFailure(req.Count, req.Status)
		writeJSON(w, map[string]any{"failCount": req.Count, "failStatus": req.Status})
	})

	mux.HandleFunc("/mock/latency", func(w http.ResponseWriter, r *http.Request) {
		var req struct {
			Ms int `json:"ms"`
		}
		if err := decodePost(r, &req); err != nil {
			writeError(w, http.StatusBadRequest, err.Error())
			return
		}

		if req.Ms < 0 {
			req.Ms = 0
		}
		c.setLatency(time.Duration(req.Ms) * time.Millisecond)
		writeJSON(w, map[string]any{"latencyMs": req.Ms})
	})
}

// withChaos выдерживает задержку и проваливает запросы, пока счётчик не
// исчерпан.
//
// chaosExempt — маршруты, которые отказы не трогают.
//
// Метаданные: по ним бот определяет версию панели, и провалившийся детект увёл
// бы стенд в состояние, которое проверять никто не собирался, вместо экрана
// ошибки конкретного действия.
//
// Ноды и хосты: бот скрейпит их сам, при старте и каждые 60 секунд
// (`StartSyncLoop` в internal/monitoring). Считай их отказы обычными — и
// поставленный «провалить один запрос» съест фоновый тик, а не то нажатие,
// ради которого отказ включали. Тестировщик увидит штатно отработавшую кнопку
// и сделает ровно неверный вывод.
func chaosExempt(path string) bool {
	switch path {
	case "/api/system/metadata", "/api/nodes", "/api/hosts":
		return true
	default:
		return false
	}
}

// Часть маршрутов выведена из-под отказов намеренно, см. chaosExempt.
func withChaos(c *chaos, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if chaosExempt(r.URL.Path) {
			next.ServeHTTP(w, r)
			return
		}

		if d := c.delay(); d > 0 {
			time.Sleep(d)
		}
		if code := c.nextFailure(); code != 0 {
			slog.Info("mock panel injected failure", "path", r.URL.Path, "status", code)
			writeError(w, code, "injected failure")
			return
		}

		next.ServeHTTP(w, r)
	})
}

// control — рычаги стенда над одним пользователем: подкрутить срок, статус,
// потраченный трафик и набор устройств, чтобы увидеть ветку интерфейса или шаг
// планировщика, не дожидаясь реального времени.
//
// Срок задаётся **относительно «сейчас»**, а не абсолютной датой, потому что
// проверяются окна планировщика, и все они считаются от текущего момента:
// уведомление за 3 дня ждёт expireAt через 48–72 часа, за 1 день — через 0–24,
// истечение — прошедший expireAt, grace-кик — прошедший больше чем на 72 часа.
// С абсолютной датой это пришлось бы каждый раз считать руками.
func (s *store) control(r *http.Request) (*user, error) {
	var req struct {
		TelegramID    int64    `json:"telegramId"`
		ExpireInHours *float64 `json:"expireInHours"`
		Status        *string  `json:"status"`
		UsedTrafficGB *float64 `json:"usedTrafficGB"`
		Devices       *int     `json:"devices"`
	}
	if err := decodePost(r, &req); err != nil {
		return nil, err
	}
	if req.TelegramID == 0 {
		return nil, fmt.Errorf("нужен telegramId")
	}

	u := s.firstByTelegramID(req.TelegramID)
	if u == nil {
		return nil, nil
	}

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
	if req.Devices != nil {
		u.devices = makeDevices(*req.Devices)
	}
	return u, nil
}

// deviceTemplates — правдоподобные устройства для экрана «Мои устройства».
// Разные платформы и модели нужны не для красоты: подписи кнопок обрезаются по
// длине, и на одинаковых значениях этого не увидеть.
var deviceTemplates = []device{
	{Platform: "iOS", OsVersion: "17.5.1", DeviceModel: "iPhone 15 Pro"},
	{Platform: "Android", OsVersion: "14", DeviceModel: "Pixel 8"},
	{Platform: "Windows", OsVersion: "11", DeviceModel: "ThinkPad X1 Carbon Gen 11"},
	{Platform: "macOS", OsVersion: "14.5", DeviceModel: "MacBook Pro 16"},
	{Platform: "Linux", OsVersion: "6.8", DeviceModel: ""},
}

func makeDevices(count int) []device {
	if count <= 0 {
		return nil
	}

	devices := make([]device, 0, count)
	for i := range count {
		d := deviceTemplates[i%len(deviceTemplates)]
		d.Hwid = fmt.Sprintf("hwid-%d", i+1)
		devices = append(devices, d)
	}
	return devices
}

type createRequest struct {
	Username          string `json:"username"`
	TelegramID        *int64 `json:"telegramId"`
	TrafficLimitBytes int64  `json:"trafficLimitBytes"`
	ExpireAt          string `json:"expireAt"`
}

// create заводит пользователя. Значения полей берутся из запроса бота, чтобы
// стенд отвечал тем же, что у него попросили.
func (s *store) create(req createRequest) *user {
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

type patchRequest struct {
	UUID              string  `json:"uuid"`
	ID                int64   `json:"id"`
	Status            *string `json:"status"`
	ExpireAt          *string `json:"expireAt"`
	TrafficLimitBytes *int64  `json:"trafficLimitBytes"`
	Username          *string `json:"username"`
}

// patch применяет изменения статуса, срока и лимита трафика.
func (s *store) patch(req patchRequest) *user {
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

// decodePost — общий разбор тела пультовых запросов. Метод проверяется здесь же:
// GET по пульту это почти всегда попытка «посмотреть», и внятный отказ лучше
// молчаливого применения пустого тела.
func decodePost(r *http.Request, dst any) error {
	if r.Method != http.MethodPost {
		return fmt.Errorf("нужен POST")
	}
	if err := json.NewDecoder(r.Body).Decode(dst); err != nil {
		return fmt.Errorf("некорректный JSON: %w", err)
	}
	return nil
}

func writeJSON(w http.ResponseWriter, payload any) {
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(payload)
}

// writeError отвечает в формате ошибки панели: бот разбирает тело как
// {message, errorCode}, и заглушка обязана давать ему то же самое — иначе на
// стенде не проверить, что видит пользователь при отказе.
func writeError(w http.ResponseWriter, code int, message string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	_ = json.NewEncoder(w).Encode(map[string]string{"message": message, "errorCode": "MOCK"})
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

func main() {
	port := envOr("MOCK_PANEL_PORT", "8081")
	slog.Info("Mock Remnawave panel started", "port", port, "version", panelVersion)

	// ReadTimeout обязателен: клиент, открывший запрос и не дославший тело,
	// иначе занимал бы обработчик бесконечно. Стенд молча вставал бы, и
	// разбираться в этом пришлось бы посреди ручной проверки.
	srv := &http.Server{
		Addr:              ":" + port,
		Handler:           newServer(),
		ReadHeaderTimeout: 5 * time.Second,
		ReadTimeout:       15 * time.Second,
		WriteTimeout:      60 * time.Second, // больше максимальной /mock/latency
	}
	if err := srv.ListenAndServe(); err != nil {
		slog.Error("mock panel stopped", "error", err)
		os.Exit(1)
	}
}
