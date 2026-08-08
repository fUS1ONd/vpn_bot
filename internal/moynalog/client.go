// Package moynalog — HTTP-клиент закрытого API кабинета самозанятого «Мой налог»
// (https://lknpd.nalog.ru/api/v1). Официального API у ФНС нет, протокол сверен по
// двум независимым реализациям и живыми запросами; подробности — в
// docs/adr/0001-own-moynalog-client.md.
package moynalog

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"time"
)

const (
	defaultBaseURL = "https://lknpd.nalog.ru/api/v1"

	// Москва не переходит на летнее время с 2014 года, поэтому смещение — константа.
	// Прибавляем его явно и явно пишем суффикс, чтобы дата чека не зависела ни от TZ
	// контейнера, ни от системной локали процесса.
	moscowOffset = 3 * time.Hour
	moscowSuffix = "+03:00"

	// Формат дат в запросе списка доходов — ISO 8601 со смещением. Проверено живым
	// запросом: дата без времени и Z-форма дают 500.
	moscowLayout     = "2006-01-02T15:04:05"
	moscowMsecLayout = "2006-01-02T15:04:05.000"

	// deviceIDLength — длина sourceDeviceId в кабинете ФНС.
	deviceIDLength = 21

	defaultUserAgent = "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/120.0.0.0 Safari/537.36"

	// incomesPageLimit — сверка смотрит сутки вокруг времени операции, столько доходов
	// там быть не может; страничного обхода намеренно не заводим. Если предположение
	// всё-таки нарушится, полная страница считается обрезанной выборкой и сверка
	// честно признаёт судьбу чека неизвестной, а не пробивает дубль.
	incomesPageLimit = 100
)

// Kind — класс ошибки ФНС. Перечня кодов ошибок не существует ни в документации,
// ни в чужих реализациях, поэтому разбор идёт по HTTP-статусу.
type Kind int

const (
	// KindTransport — сеть не ответила. Ретраить молча.
	KindTransport Kind = iota
	// KindServer — 500. Ретраить по расписанию: ФНС отвечает 500 и на неверный
	// запрос с валидным токеном, различить нельзя.
	KindServer
	// KindAuth — не подошёл пароль либо второй 401 подряд. Само не починится.
	KindAuth
	// KindBadRequest — 400/403/404. Повторять бесполезно.
	KindBadRequest
	// KindUnknown — ответ нечитаем: неизвестно, создан чек или нет. Нужна сверка.
	KindUnknown
)

// stageLogin помечает ошибки, случившиеся на входе в кабинет. Отличать их важно
// ровно в одном месте: оборванная связь при логине означает, что до /income дело
// не дошло и чек точно не создан, — в отличие от обрыва на самом /income.
const stageLogin = "login"

// Error — ошибка обращения к кабинету ФНС с сохранённым текстом для показа владельцу.
type Error struct {
	Kind       Kind
	StatusCode int
	Code       string
	Message    string
	Stage      string // stageLogin, если запрос не дошёл дальше входа
	Err        error
}

func (e *Error) Error() string {
	switch {
	case e.Message != "":
		return fmt.Sprintf("moynalog API error %d: %s", e.StatusCode, e.Message)
	case e.Err != nil:
		return fmt.Sprintf("moynalog request failed: %v", e.Err)
	default:
		return fmt.Sprintf("moynalog API error %d", e.StatusCode)
	}
}

func (e *Error) Unwrap() error { return e.Err }

// ErrorKind классифицирует ошибку. Незнакомые ошибки считаем транспортными:
// молча ретраить безопаснее, чем разбудить владельца или бросить чек.
func ErrorKind(err error) Kind {
	var e *Error
	if errors.As(err, &e) {
		return e.Kind
	}
	return KindTransport
}

// markLoginStage помечает ошибку, случившуюся на входе в кабинет.
func markLoginStage(err error) error {
	var e *Error
	if errors.As(err, &e) {
		e.Stage = stageLogin
	}
	return err
}

// errorStage возвращает стадию, на которой ошибка возникла (пусто — не входа).
func errorStage(err error) string {
	var e *Error
	if errors.As(err, &e) {
		return e.Stage
	}
	return ""
}

// ErrorMessage возвращает текст ФНС, если он есть, иначе текст самой ошибки.
func ErrorMessage(err error) string {
	if err == nil {
		return ""
	}
	var e *Error
	if errors.As(err, &e) && e.Message != "" {
		return e.Message
	}
	return err.Error()
}

// Client — клиент кабинета «Мой налог». Токен живёт только в памяти: на диск
// ничего не пишется, а протухание чинит сам себя на первом же использовании.
type Client struct {
	inn      string
	password string
	baseURL  string
	http     *http.Client

	mu    sync.Mutex // сериализует логин, чтобы параллельные вызовы не логинились наперегонки
	token string
}

func NewClient(inn, password string) *Client {
	return NewClientWithBaseURL(inn, password, defaultBaseURL)
}

func NewClientWithBaseURL(inn, password, baseURL string) *Client {
	return &Client{
		inn:      inn,
		password: password,
		baseURL:  strings.TrimRight(baseURL, "/"),
		http:     &http.Client{Timeout: 30 * time.Second},
	}
}

func (c *Client) SetHTTPClient(client *http.Client) {
	if client != nil {
		c.http = client
	}
}

// deviceID вычисляется детерминированно из ИНН и нигде не хранится: случайный
// идентификатор при каждом входе плодил бы в кабинете ФНС по устройству на запуск.
func (c *Client) deviceID() string {
	sum := sha256.Sum256([]byte("moynalog-device:" + c.inn))
	return hex.EncodeToString(sum[:])[:deviceIDLength]
}

// IncomeRequest — параметры регистрируемого чека.
type IncomeRequest struct {
	Name          string    // наименование услуги вместе с меткой
	Amount        float64   // полная сумма платежа: комиссия провайдера базу при НПД не уменьшает
	OperationTime time.Time // момент подтверждения платежа в UTC
}

// Income — доход из кабинета ФНС (в терминах бота — чек).
type Income struct {
	ApprovedReceiptUUID string
	Name                string
	Services            []string
	TotalAmount         float64
	OperationTime       time.Time
	Canceled            bool
}

// Matches сообщает, встречается ли метка в наименовании услуги.
func (i Income) Matches(marker string) bool {
	if marker == "" {
		return false
	}
	if strings.Contains(i.Name, marker) {
		return true
	}
	for _, service := range i.Services {
		if strings.Contains(service, marker) {
			return true
		}
	}
	return false
}

// CreateIncome регистрирует чек и возвращает его uuid.
func (c *Client) CreateIncome(req IncomeRequest) (string, error) {
	now := time.Now().UTC()
	body := map[string]any{
		"paymentType":                     "CASH",
		"ignoreMaxTotalIncomeRestriction": false,
		"client": map[string]any{
			"contactPhone": nil,
			"displayName":  nil,
			"incomeType":   "FROM_INDIVIDUAL",
			"inn":          nil,
		},
		"requestTime":   FormatMoscow(now),
		"operationTime": FormatMoscow(req.OperationTime),
		"services": []map[string]any{
			{"name": req.Name, "amount": req.Amount, "quantity": 1},
		},
		"totalAmount": formatAmount(req.Amount),
	}

	var out struct {
		ApprovedReceiptUUID string `json:"approvedReceiptUuid"`
	}
	if err := c.do(http.MethodPost, "/income", body, &out); err != nil {
		// Обрыв на входе в кабинет неоднозначным не делает ничего: /income не
		// уходил, чека нет, — обычный ретрай. Ambiguity начинается только с обрыва
		// на самом /income.
		if ErrorKind(err) == KindTransport && errorStage(err) != stageLogin {
			// Связь оборвалась на единственном неидемпотентном запросе: чек мог
			// успеть создаться. Молча повторить — значит рискнуть дублем, поэтому
			// судьба считается неизвестной и разрешается сверкой по метке.
			return "", &Error{Kind: KindUnknown, Err: err, Message: "связь с ФНС оборвалась, судьба чека неизвестна"}
		}
		return "", err
	}
	if out.ApprovedReceiptUUID == "" {
		// Ответ прочитан, но чека в нём нет: неизвестно, создан он или нет.
		return "", &Error{Kind: KindUnknown, Message: "ответ ФНС без approvedReceiptUuid"}
	}
	return out.ApprovedReceiptUUID, nil
}

// CancelIncome аннулирует чек целиком — частичного аннулирования у ФНС нет.
func (c *Client) CancelIncome(receiptUUID, comment string, operationTime time.Time) error {
	body := map[string]any{
		"operationTime": FormatMoscow(operationTime),
		"requestTime":   FormatMoscow(time.Now().UTC()),
		"comment":       comment,
		"receiptUuid":   receiptUUID,
		"partnerCode":   nil,
	}
	return c.do(http.MethodPost, "/cancel", body, nil)
}

// ListIncomes возвращает доходы за интервал. Границы передаются в московском времени.
func (c *Client) ListIncomes(from, to time.Time) ([]Income, error) {
	query := url.Values{}
	query.Set("from", FormatMoscowSeconds(from))
	query.Set("to", FormatMoscowSeconds(to))
	query.Set("offset", "0")
	query.Set("limit", strconv.Itoa(incomesPageLimit))
	query.Set("sortBy", "operation_time:desc")

	var out struct {
		Content []struct {
			ApprovedReceiptUUID string `json:"approvedReceiptUuid"`
			Name                string `json:"name"`
			TotalAmount         any    `json:"totalAmount"`
			OperationTime       string `json:"operationTime"`
			CancellationInfo    any    `json:"cancellationInfo"`
			Services            []struct {
				Name string `json:"name"`
			} `json:"services"`
		} `json:"content"`
	}
	if err := c.do(http.MethodGet, "/incomes?"+query.Encode(), nil, &out); err != nil {
		return nil, err
	}
	if len(out.Content) >= incomesPageLimit {
		// Страница забита под завязку — значит выборка могла обрезаться, и «метка не
		// нашлась» больше не означает «чека нет». Тихо вернуть неполный список хуже
		// всего: сверка решила бы пробить чек заново и создала дубль.
		return nil, &Error{
			Kind:    KindUnknown,
			Message: fmt.Sprintf("доходов за интервал не меньше %d, список мог обрезаться", incomesPageLimit),
		}
	}

	incomes := make([]Income, 0, len(out.Content))
	for _, item := range out.Content {
		income := Income{
			ApprovedReceiptUUID: item.ApprovedReceiptUUID,
			Name:                item.Name,
			TotalAmount:         parseAmount(item.TotalAmount),
			OperationTime:       parseMoscow(item.OperationTime),
			Canceled:            item.CancellationInfo != nil,
		}
		for _, service := range item.Services {
			income.Services = append(income.Services, service.Name)
		}
		incomes = append(incomes, income)
	}
	return incomes, nil
}

// do выполняет запрос с логином по требованию: токена нет — логинимся, есть — идём
// с ним, ответ 401 — перелогин и ровно один повтор.
func (c *Client) do(method, path string, body any, out any) error {
	payload, err := marshalBody(body)
	if err != nil {
		return err
	}

	token, err := c.currentToken()
	if err != nil {
		return err
	}

	status, data, err := c.send(method, path, payload, token)
	if err != nil {
		return err
	}

	if status == http.StatusUnauthorized {
		token, err = c.relogin(token)
		if err != nil {
			return err
		}
		status, data, err = c.send(method, path, payload, token)
		if err != nil {
			return err
		}
		if status == http.StatusUnauthorized {
			// Второй 401 подряд со свежим токеном — дело не в протухании.
			return apiError(status, data)
		}
	}

	if status < 200 || status >= 300 {
		return apiError(status, data)
	}
	if out == nil {
		return nil
	}
	if err := json.Unmarshal(data, out); err != nil {
		return &Error{Kind: KindUnknown, StatusCode: status, Err: err, Message: "нечитаемый ответ ФНС"}
	}
	return nil
}

func (c *Client) currentToken() (string, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.token != "" {
		return c.token, nil
	}
	return c.login()
}

// relogin логинится заново, если токен ещё не обновил кто-то параллельный.
func (c *Client) relogin(usedToken string) (string, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.token != "" && c.token != usedToken {
		return c.token, nil
	}
	c.token = ""
	return c.login()
}

// login выполняет вход по ИНН и паролю. Вызывается только под c.mu.
func (c *Client) login() (string, error) {
	body := map[string]any{
		"username": c.inn,
		"password": c.password,
		"deviceInfo": map[string]any{
			"sourceDeviceId": c.deviceID(),
			"sourceType":     "WEB",
			"appVersion":     "1.0.0",
			"metaDetails":    map[string]any{"userAgent": defaultUserAgent},
		},
	}
	payload, err := marshalBody(body)
	if err != nil {
		return "", err
	}

	status, data, err := c.send(http.MethodPost, "/auth/lkfl", payload, "")
	if err != nil {
		return "", markLoginStage(err)
	}
	if status < 200 || status >= 300 {
		return "", markLoginStage(apiError(status, data))
	}

	var out struct {
		Token string `json:"token"`
	}
	if err := json.Unmarshal(data, &out); err != nil || out.Token == "" {
		return "", &Error{Kind: KindAuth, StatusCode: status, Stage: stageLogin, Message: "ФНС не вернула токен при входе", Err: err}
	}

	c.token = out.Token
	return c.token, nil
}

func (c *Client) send(method, path string, body []byte, token string) (int, []byte, error) {
	var reader io.Reader
	if body != nil {
		reader = bytes.NewReader(body)
	}
	req, err := http.NewRequest(method, c.baseURL+path, reader)
	if err != nil {
		// Сбой на нашей стороне: ФНС в нём не участвовала, поэтому и текста от неё нет.
		return 0, nil, fmt.Errorf("собрать запрос к ФНС: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json, text/plain, */*")
	req.Header.Set("User-Agent", defaultUserAgent)
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}

	resp, err := c.http.Do(req)
	if err != nil {
		return 0, nil, &Error{Kind: KindTransport, Err: err}
	}
	defer resp.Body.Close()

	data, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		// Тело не дочитано: по нему нельзя понять, что случилось с чеком.
		return resp.StatusCode, nil, &Error{Kind: KindUnknown, StatusCode: resp.StatusCode, Err: err, Message: "нечитаемый ответ ФНС"}
	}
	return resp.StatusCode, data, nil
}

func marshalBody(body any) ([]byte, error) {
	if body == nil {
		return nil, nil
	}
	data, err := json.Marshal(body)
	if err != nil {
		return nil, fmt.Errorf("собрать тело запроса к ФНС: %w", err)
	}
	return data, nil
}

// apiError разбирает единообразное тело ошибки ФНС и классифицирует её по статусу.
func apiError(status int, data []byte) error {
	e := &Error{StatusCode: status, Kind: kindByStatus(status)}
	var parsed struct {
		Code    string `json:"code"`
		Message string `json:"message"`
	}
	if err := json.Unmarshal(data, &parsed); err == nil {
		e.Code = parsed.Code
		e.Message = parsed.Message
	}
	if e.Message == "" {
		e.Message = strings.TrimSpace(string(data))
	}
	if e.Message == "" {
		e.Message = fmt.Sprintf("ФНС ответила %d без пояснения", status)
	}
	return e
}

func kindByStatus(status int) Kind {
	switch {
	case status == http.StatusUnauthorized:
		return KindAuth
	case status == http.StatusBadRequest, status == http.StatusForbidden, status == http.StatusNotFound:
		return KindBadRequest
	default:
		// Всё остальное, включая 500, ретраим: живая проба показала, что ФНС
		// отвечает 500 и на неверный запрос с валидным токеном — различить нельзя.
		return KindServer
	}
}

// FormatMoscow переводит момент из UTC в московское время явным прибавлением трёх
// часов и явным суффиксом смещения — без обращения к time.Local и переменной TZ.
func FormatMoscow(t time.Time) string {
	return t.UTC().Add(moscowOffset).Format(moscowMsecLayout) + moscowSuffix
}

// FormatMoscowSeconds — то же без миллисекунд, для границ выборки доходов.
func FormatMoscowSeconds(t time.Time) string {
	return t.UTC().Add(moscowOffset).Format(moscowLayout) + moscowSuffix
}

// MoscowDate возвращает календарную дату операции в Москве.
func MoscowDate(t time.Time) string {
	return t.UTC().Add(moscowOffset).Format("02.01.2006")
}

func formatAmount(amount float64) string {
	return strconv.FormatFloat(amount, 'f', -1, 64)
}

func parseAmount(raw any) float64 {
	switch v := raw.(type) {
	case float64:
		return v
	case string:
		value, err := strconv.ParseFloat(v, 64)
		if err != nil {
			return 0
		}
		return value
	default:
		return 0
	}
}

func parseMoscow(raw string) time.Time {
	if raw == "" {
		return time.Time{}
	}
	for _, layout := range []string{time.RFC3339, moscowMsecLayout + moscowSuffix, moscowLayout + moscowSuffix} {
		if parsed, err := time.Parse(layout, raw); err == nil {
			return parsed.UTC()
		}
	}
	return time.Time{}
}
