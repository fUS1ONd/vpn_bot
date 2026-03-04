package remnawave

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
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

// Client — HTTP-клиент для Remnawave API
type Client struct {
	baseURL   string
	apiToken  string
	squadUUID string
	http      *http.Client
}

// NewClient создаёт новый клиент Remnawave API
func NewClient(baseURL, apiToken, squadUUID string) *Client {
	return &Client{
		baseURL:   baseURL,
		apiToken:  apiToken,
		squadUUID: squadUUID,
		http: &http.Client{
			Timeout: 30 * time.Second,
		},
	}
}

// User — данные пользователя из Remnawave
type User struct {
	UUID              string    `json:"uuid"`
	ShortUUID         string    `json:"shortUuid"`
	Username          string    `json:"username"`
	Status            string    `json:"status"`
	TelegramID        *int64    `json:"telegramId"`
	TrafficLimitBytes int64     `json:"trafficLimitBytes"`
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
	UUID   string   `json:"uuid"`
	Remark string   `json:"remark"`
	Nodes  []string `json:"nodes"`
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

// UpdateUserRequest — запрос на обновление пользователя
type UpdateUserRequest struct {
	UUID     string  `json:"uuid"`
	Username *string `json:"username,omitempty"`
	Status   string  `json:"status,omitempty"`
	ExpireAt *string `json:"expireAt,omitempty"`
}

// apiResponse — обёртка ответа API
type apiResponse struct {
	Response json.RawMessage `json:"response"`
}

// CreateUser создаёт нового пользователя в Remnawave.
func (c *Client) CreateUser(telegramID int64, username string, expireAt time.Time) (*User, error) {
	req := CreateUserRequest{
		Username:             username,
		TelegramID:           telegramID,
		TrafficLimitBytes:    0,
		TrafficLimitStrategy: TrafficStrategyMonth,
		ExpireAt:             expireAt.UTC().Format(time.RFC3339),
	}

	// Добавляем сквад если указан
	if c.squadUUID != "" {
		req.ActiveInternalSquads = []string{c.squadUUID}
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

// GetUser получает данные пользователя по UUID
func (c *Client) GetUser(uuid string) (*User, error) {
	resp, err := c.doRequest("GET", "/api/users/"+uuid, nil)
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

// GetUserByTelegramID получает пользователя по Telegram ID
func (c *Client) GetUserByTelegramID(telegramID int64) (*User, error) {
	resp, err := c.doRequest("GET", fmt.Sprintf("/api/users/by-telegram-id/%d", telegramID), nil)
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

// GetAllUsers получает список всех пользователей
func (c *Client) GetAllUsers() ([]User, error) {
	// Получаем с максимальным лимитом (API ограничивает до 1000)
	resp, err := c.doRequest("GET", "/api/users?size=1000", nil)
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

	return result.Response.Users, nil
}

// UpdateUsername обновляет username пользователя в панели Remnawave
func (c *Client) UpdateUsername(uuid string, username string) error {
	req := UpdateUserRequest{
		UUID:     uuid,
		Username: &username,
	}

	body, err := json.Marshal(req)
	if err != nil {
		return fmt.Errorf("failed to marshal request: %w", err)
	}

	_, err = c.doRequest("PATCH", "/api/users", body)
	return err
}

// ResetUserTraffic сбрасывает использованный трафик пользователя
func (c *Client) ResetUserTraffic(uuid string) error {
	_, err := c.doRequest("POST", "/api/users/"+uuid+"/actions/reset-traffic", nil)
	return err
}

// DeleteUser удаляет пользователя
func (c *Client) DeleteUser(uuid string) error {
	_, err := c.doRequest("DELETE", "/api/users/"+uuid, nil)
	return err
}

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

	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("failed to read response: %w", err)
	}

	if resp.StatusCode >= 400 {
		return nil, fmt.Errorf("API error %d: %s", resp.StatusCode, string(respBody))
	}

	return respBody, nil
}
