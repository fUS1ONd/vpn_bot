package remnawave

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"sync"
	"time"
)

const (
	// TrafficStrategyMonth — стратегия сброса трафика раз в месяц
	TrafficStrategyMonth = "MONTH"

	// StatusActive — активный пользователь
	StatusActive = "ACTIVE"
	// StatusDisabled — заблокированный пользователь
	StatusDisabled = "DISABLED"
	// StatusLimited — превышен лимит трафика
	StatusLimited = "LIMITED"
	// StatusExpired — истёк срок действия
	StatusExpired = "EXPIRED"
)

var (
	// ErrUserNotFound возвращается, когда пользователь отсутствует в панели.
	ErrUserNotFound = errors.New("user not found")
)

// Client — HTTP-клиент для Remnawave API. Умеет работать и с 2.8.x, и с 3.x:
// версию определяет сам, вызывающий код про неё не знает.
type Client struct {
	baseURL    string
	apiToken   string
	squadUUIDs []string
	http       *http.Client

	versionMu  sync.RWMutex
	apiVersion APIVersion
	// detectMu схлопывает конкурентные попытки детекта в один запрос metadata.
	detectMu sync.Mutex
}

// NewClient создаёт новый клиент Remnawave API
func NewClient(baseURL, apiToken string, squadUUIDs []string) *Client {
	copiedSquadUUIDs := append([]string(nil), squadUUIDs...)

	return &Client{
		baseURL:    baseURL,
		apiToken:   apiToken,
		squadUUIDs: copiedSquadUUIDs,
		http: &http.Client{
			Timeout: 30 * time.Second,
		},
	}
}

// User — данные пользователя из Remnawave
type User struct {
	// ID есть в обеих версиях API; на 3.x это единственный идентификатор пользователя.
	ID int64 `json:"id"`
	// UUID заполнен только на 2.8.x — в 3.x колонка удалена из базы панели.
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
	UserTraffic       *Traffic  `json:"userTraffic"`
}

// Traffic — статистика трафика пользователя
type Traffic struct {
	UsedTrafficBytes         int64      `json:"usedTrafficBytes"`
	LifetimeUsedTrafficBytes int64      `json:"lifetimeUsedTrafficBytes"`
	OnlineAt                 *time.Time `json:"onlineAt"`
}

// Node — данные ноды из Remnawave
type Node struct {
	UUID        string   `json:"uuid"`
	Name        string   `json:"name"`
	Address     string   `json:"address"`
	Port        *int     `json:"port"`
	IsConnected bool     `json:"isConnected"`
	IsDisabled  bool     `json:"isDisabled"`
	CountryCode string   `json:"countryCode"`
	Tags        []string `json:"tags"`
	UsersOnline *int     `json:"usersOnline"`
}

// Host — данные хоста из Remnawave (прокси-конфиг, видимый пользователям)
type Host struct {
	UUID       string   `json:"uuid"`
	Remark     string   `json:"remark"`
	Nodes      []string `json:"nodes"`
	IsDisabled bool     `json:"isDisabled"` // хост отключён в панели
}

// CreateUserRequest — запрос на создание пользователя
type CreateUserRequest struct {
	Username             string   `json:"username"`
	TelegramID           int64    `json:"telegramId,omitempty"`
	TrafficLimitBytes    int64    `json:"trafficLimitBytes"`
	TrafficLimitStrategy string   `json:"trafficLimitStrategy"`
	ExpireAt             string   `json:"expireAt"`
	Description          string   `json:"description,omitempty"`
	ActiveInternalSquads []string `json:"activeInternalSquads,omitempty"`
}

// UpdateUserRequest — запрос на обновление пользователя. Идентификатор в тело
// подставляет сам клиент из UserRef: публичное поле привело бы к тому, что на 3.x
// вызывающая сторона продолжила бы класть туда UUID и получала 400.
type UpdateUserRequest struct {
	Username          *string `json:"username,omitempty"`
	Status            *string `json:"status,omitempty"`
	ExpireAt          *string `json:"expireAt,omitempty"`
	TrafficLimitBytes *int64  `json:"trafficLimitBytes,omitempty"`
}

// updateUserBody — тело PATCH /api/users с идентификатором нужного вида.
// На 2.8.x панель ждёт uuid, на 3.x — числовой id.
type updateUserBody struct {
	UUID              string  `json:"uuid,omitempty"`
	ID                int64   `json:"id,omitempty"`
	Username          *string `json:"username,omitempty"`
	Status            *string `json:"status,omitempty"`
	ExpireAt          *string `json:"expireAt,omitempty"`
	TrafficLimitBytes *int64  `json:"trafficLimitBytes,omitempty"`
}

// apiResponse — обёртка ответа API
type apiResponse struct {
	Response json.RawMessage `json:"response"`
}

// CreateUser создаёт нового пользователя в Remnawave.
// trafficLimitBytes=0 означает безлимит.
func (c *Client) CreateUser(telegramID int64, username string, expireAt time.Time, trafficLimitBytes int64) (*User, error) {
	req := CreateUserRequest{
		Username:             username,
		TelegramID:           telegramID,
		TrafficLimitBytes:    trafficLimitBytes,
		TrafficLimitStrategy: TrafficStrategyMonth,
		ExpireAt:             expireAt.UTC().Format(time.RFC3339),
	}

	// Передаём все default squads, если они заданы в конфиге.
	if len(c.squadUUIDs) > 0 {
		req.ActiveInternalSquads = append([]string(nil), c.squadUUIDs...)
	}

	body, err := json.Marshal(req)
	if err != nil {
		return nil, fmt.Errorf("failed to marshal request: %w", err)
	}

	resp, err := c.doRequest("POST", "/api/users", body)
	if err != nil {
		return nil, err
	}

	var result apiResponse
	if err := json.Unmarshal(resp, &result); err != nil {
		return nil, fmt.Errorf("failed to unmarshal response: %w", err)
	}

	var user User
	if err := json.Unmarshal(result.Response, &user); err != nil {
		return nil, fmt.Errorf("failed to unmarshal user: %w", err)
	}

	return &user, nil
}

// SetHTTPClient переопределяет HTTP-клиент (используется в тестах).
func (c *Client) SetHTTPClient(httpClient *http.Client) {
	if httpClient != nil {
		c.http = httpClient
	}
}

// GetUser получает данные пользователя по ссылке, независимой от версии API.
func (c *Client) GetUser(ref UserRef) (*User, error) {
	resp, err := c.doUserRequest(ref, userPathRequest("GET", "", nil))
	if err != nil {
		return nil, err
	}

	var result apiResponse
	if err := json.Unmarshal(resp, &result); err != nil {
		return nil, fmt.Errorf("failed to unmarshal response: %w", err)
	}

	var user User
	if err := json.Unmarshal(result.Response, &user); err != nil {
		return nil, fmt.Errorf("failed to unmarshal user: %w", err)
	}

	return &user, nil
}

// GetAllUsers получает список всех пользователей с пагинацией.
func (c *Client) GetAllUsers() ([]User, error) {
	const pageSize = 1000
	var allUsers []User

	for start := 0; ; start += pageSize {
		resp, err := c.doRequest("GET", fmt.Sprintf("/api/users?size=%d&start=%d", pageSize, start), nil)
		if err != nil {
			return nil, err
		}

		var result struct {
			Response struct {
				Users []User `json:"users"`
				Total int    `json:"total"`
			} `json:"response"`
		}
		if err := json.Unmarshal(resp, &result); err != nil {
			return nil, fmt.Errorf("failed to unmarshal response: %w", err)
		}

		allUsers = append(allUsers, result.Response.Users...)

		if len(allUsers) >= result.Response.Total || len(result.Response.Users) < pageSize {
			break
		}
	}

	return allUsers, nil
}

// GetUserHwidDevicesCount возвращает количество HWID-устройств пользователя.
func (c *Client) GetUserHwidDevicesCount(ref UserRef) (int, error) {
	resp, err := c.doUserRequest(ref, hwidDevicesRequest)
	if err != nil {
		return 0, err
	}

	var result struct {
		Response struct {
			Total int `json:"total"`
		} `json:"response"`
	}
	if err := json.Unmarshal(resp, &result); err != nil {
		return 0, fmt.Errorf("failed to unmarshal hwid devices response: %w", err)
	}

	return result.Response.Total, nil
}

// HwidDevice — устройство пользователя из Remnawave HWID API.
type HwidDevice struct {
	Hwid        string `json:"hwid"`
	Platform    string `json:"platform"`
	OsVersion   string `json:"osVersion"`
	DeviceModel string `json:"deviceModel"`
}

// GetUserHwidDevices возвращает список HWID-устройств пользователя.
func (c *Client) GetUserHwidDevices(ref UserRef) ([]HwidDevice, error) {
	resp, err := c.doUserRequest(ref, hwidDevicesRequest)
	if err != nil {
		return nil, err
	}

	var result struct {
		Response struct {
			Devices []HwidDevice `json:"devices"`
		} `json:"response"`
	}
	if err := json.Unmarshal(resp, &result); err != nil {
		return nil, fmt.Errorf("failed to unmarshal hwid devices response: %w", err)
	}

	return result.Response.Devices, nil
}

// DeleteUserHwidDevice удаляет одно HWID-устройство пользователя и
// возвращает обновлённый список оставшихся устройств.
func (c *Client) DeleteUserHwidDevice(ref UserRef, hwid string) ([]HwidDevice, error) {
	resp, err := c.doUserRequest(ref, func(version APIVersion, ref UserRef) (userRequest, error) {
		payload, err := hwidUserPayload(version, ref)
		if err != nil {
			return userRequest{}, err
		}
		payload["hwid"] = hwid

		body, err := json.Marshal(payload)
		if err != nil {
			return userRequest{}, fmt.Errorf("failed to marshal delete device request: %w", err)
		}
		return userRequest{method: "POST", path: "/api/hwid/devices/delete", body: body}, nil
	})
	if err != nil {
		return nil, err
	}

	var result struct {
		Response struct {
			Devices []HwidDevice `json:"devices"`
		} `json:"response"`
	}
	if err := json.Unmarshal(resp, &result); err != nil {
		return nil, fmt.Errorf("failed to unmarshal delete device response: %w", err)
	}

	return result.Response.Devices, nil
}

// DeleteAllUserHwidDevices сбрасывает все HWID-устройства пользователя одним запросом.
func (c *Client) DeleteAllUserHwidDevices(ref UserRef) error {
	_, err := c.doUserRequest(ref, func(version APIVersion, ref UserRef) (userRequest, error) {
		payload, err := hwidUserPayload(version, ref)
		if err != nil {
			return userRequest{}, err
		}

		body, err := json.Marshal(payload)
		if err != nil {
			return userRequest{}, fmt.Errorf("failed to marshal delete-all request: %w", err)
		}
		return userRequest{method: "POST", path: "/api/hwid/devices/delete-all", body: body}, nil
	})
	return err
}

// hwidDevicesRequest — чтение списка устройств: путь один и тот же, меняется только
// идентификатор пользователя в нём.
func hwidDevicesRequest(version APIVersion, ref UserRef) (userRequest, error) {
	segment, err := userPathSegment(version, ref)
	if err != nil {
		return userRequest{}, err
	}
	return userRequest{method: "GET", path: "/api/hwid/devices/" + segment}, nil
}

// hwidUserPayload собирает поле идентификатора пользователя для тел HWID-запросов:
// на 2.8.x это userUuid (строка), на 3.x — userId (число).
func hwidUserPayload(version APIVersion, ref UserRef) (map[string]any, error) {
	switch version {
	case APIVersionV3:
		if ref.ID == 0 {
			return nil, ErrUserRefMissingID
		}
		return map[string]any{"userId": ref.ID}, nil
	case APIVersionV2:
		if ref.UUID == "" {
			return nil, ErrUserRefMissingUUID
		}
		return map[string]any{"userUuid": ref.UUID}, nil
	default:
		return nil, ErrPanelVersionUnknown
	}
}

// RevokeUserSubscription перевыпускает подписку: панель генерирует новый
// shortUuid, старая ссылка немедленно перестаёт работать. Свой shortUuid не
// передаём — Remnawave настойчиво рекомендует генерировать его самостоятельно.
// Ответ содержит пользователя с уже обновлённым subscriptionUrl.
func (c *Client) RevokeUserSubscription(ref UserRef) (*User, error) {
	resp, err := c.doUserRequest(ref, userPathRequest("POST", "/actions/revoke", []byte("{}")))
	if err != nil {
		return nil, err
	}

	var result apiResponse
	if err := json.Unmarshal(resp, &result); err != nil {
		return nil, fmt.Errorf("failed to unmarshal response: %w", err)
	}

	var user User
	if err := json.Unmarshal(result.Response, &user); err != nil {
		return nil, fmt.Errorf("failed to unmarshal revoked user: %w", err)
	}

	return &user, nil
}

// UpdateUsername обновляет username пользователя в панели Remnawave
func (c *Client) UpdateUsername(ref UserRef, username string) error {
	return c.UpdateUser(ref, UpdateUserRequest{Username: &username})
}

// UpdateUser обновляет данные пользователя в панели Remnawave.
func (c *Client) UpdateUser(ref UserRef, req UpdateUserRequest) error {
	_, err := c.doUserRequest(ref, func(version APIVersion, ref UserRef) (userRequest, error) {
		payload := updateUserBody{
			Username:          req.Username,
			Status:            req.Status,
			ExpireAt:          req.ExpireAt,
			TrafficLimitBytes: req.TrafficLimitBytes,
		}

		switch version {
		case APIVersionV3:
			if ref.ID == 0 {
				return userRequest{}, ErrUserRefMissingID
			}
			payload.ID = ref.ID
		case APIVersionV2:
			if ref.UUID == "" {
				return userRequest{}, ErrUserRefMissingUUID
			}
			payload.UUID = ref.UUID
		default:
			return userRequest{}, ErrPanelVersionUnknown
		}

		body, err := json.Marshal(payload)
		if err != nil {
			return userRequest{}, fmt.Errorf("failed to marshal request: %w", err)
		}
		return userRequest{method: "PATCH", path: "/api/users", body: body}, nil
	})
	return err
}

// DeleteUser удаляет пользователя. На 3.x панель отвечает 204 без тела —
// для нас это тот же успех.
func (c *Client) DeleteUser(ref UserRef) error {
	_, err := c.doUserRequest(ref, userPathRequest("DELETE", "", nil))
	return err
}

// EnableUser реактивирует пользователя: ставит ACTIVE, обновляет ExpireAt, снимает лимит трафика.
// expireAt всегда уходит в UTC с суффиксом Z: 3.x валидирует формат регуляркой,
// и локальное время получило бы 400, а на 2.8.x прошло бы молча.
func (c *Client) EnableUser(ref UserRef, newExpireAt time.Time) error {
	expireStr := newExpireAt.UTC().Format(time.RFC3339)
	return c.UpdateUser(ref, UpdateUserRequest{
		Status:            strPtr(StatusActive),
		ExpireAt:          &expireStr,
		TrafficLimitBytes: int64Ptr(0), // Безлимит после оплаты
	})
}

// DisableUser деактивирует пользователя (grace period).
func (c *Client) DisableUser(ref UserRef) error {
	return c.UpdateUser(ref, UpdateUserRequest{
		Status: strPtr(StatusDisabled),
	})
}

// strPtr возвращает указатель на строку
func strPtr(s string) *string { return &s }

// int64Ptr возвращает указатель на int64
func int64Ptr(n int64) *int64 { return &n }

// GetAllNodes получает список всех нод
func (c *Client) GetAllNodes() ([]Node, error) {
	resp, err := c.doRequest("GET", "/api/nodes", nil)
	if err != nil {
		return nil, err
	}

	var result struct {
		Response []Node `json:"response"`
	}
	if err := json.Unmarshal(resp, &result); err != nil {
		return nil, fmt.Errorf("failed to unmarshal nodes response: %w", err)
	}

	return result.Response, nil
}

// GetAllHosts получает список всех хостов
func (c *Client) GetAllHosts() ([]Host, error) {
	resp, err := c.doRequest("GET", "/api/hosts", nil)
	if err != nil {
		return nil, err
	}

	var result struct {
		Response []Host `json:"response"`
	}
	if err := json.Unmarshal(resp, &result); err != nil {
		return nil, fmt.Errorf("failed to unmarshal hosts response: %w", err)
	}

	return result.Response, nil
}

// doRequest выполняет HTTP-запрос к API
func (c *Client) doRequest(method, path string, body []byte) ([]byte, error) {
	url := c.baseURL + path

	var bodyReader io.Reader
	if body != nil {
		bodyReader = bytes.NewReader(body)
	}

	req, err := http.NewRequest(method, url, bodyReader)
	if err != nil {
		return nil, fmt.Errorf("failed to create request: %w", err)
	}

	req.Header.Set("Authorization", "Bearer "+c.apiToken)
	req.Header.Set("Content-Type", "application/json")

	resp, err := c.http.Do(req)
	if err != nil {
		return nil, fmt.Errorf("request failed: %w", err)
	}
	defer resp.Body.Close()

	respBody, err := io.ReadAll(io.LimitReader(resp.Body, 10<<20)) // 10 MB max
	if err != nil {
		return nil, fmt.Errorf("failed to read response: %w", err)
	}

	if resp.StatusCode >= 400 {
		return nil, newAPIError(resp.StatusCode, respBody)
	}

	return respBody, nil
}
