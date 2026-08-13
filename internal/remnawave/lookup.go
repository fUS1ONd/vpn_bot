package remnawave

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strconv"
)

// ErrMultipleUsersForTelegramID — по одному Telegram ID панель знает нескольких
// пользователей. Панель дубликаты не запрещает, а выбрать «первого» — значит с
// некоторой вероятностью привязать чужой аккаунт к платежу.
var ErrMultipleUsersForTelegramID = errors.New("multiple panel users share the same telegram id")

// streamLookupSize — сколько пользователей просим у stream при поиске одного.
// Второй элемент нужен только чтобы заметить дубликат telegramId.
const streamLookupSize = 2

// GetUserByTelegramID ищет пользователя панели по Telegram ID.
//
// Возвращает (nil, nil), если пользователя нет: отсутствие — штатная ситуация
// (пользователь удалён вручную, ещё не создан), а не ошибка. На 2.8.x ответ
// by-telegram-id — массив; на 3.x этот маршрут удалён, замена — stream с фильтром.
// В обеих версиях результат дофильтровывается точным сравнением telegramId:
// в OpenAPI строгость фильтра не выражена, а /api/users с filters ищет подстрокой.
func (c *Client) GetUserByTelegramID(telegramID int64) (*User, error) {
	version, err := c.DetectAPIVersion()
	if err != nil {
		return nil, err
	}

	var users []User
	switch version {
	case APIVersionV3:
		users, err = c.streamUsersByTelegramID(telegramID)
	default:
		users, err = c.usersByTelegramIDV2(telegramID)
	}
	if err != nil {
		if errors.Is(err, ErrUserNotFound) {
			return nil, nil
		}
		return nil, err
	}

	return exactTelegramIDMatch(users, telegramID)
}

// usersByTelegramIDV2 читает /api/users/by-telegram-id/{id} панели 2.8.x.
func (c *Client) usersByTelegramIDV2(telegramID int64) ([]User, error) {
	resp, err := c.doRequest("GET", fmt.Sprintf("/api/users/by-telegram-id/%d", telegramID), nil)
	if err != nil {
		return nil, err
	}

	var result struct {
		Response []User `json:"response"`
	}
	if err := json.Unmarshal(resp, &result); err != nil {
		return nil, fmt.Errorf("failed to unmarshal users by telegram id: %w", err)
	}

	return result.Response, nil
}

// streamUsersByTelegramID читает первую страницу /api/users/stream панели 3.x.
// Параметр telegramId объявлен строкой, поэтому передаём десятичное представление.
func (c *Client) streamUsersByTelegramID(telegramID int64) ([]User, error) {
	query := url.Values{}
	query.Set("telegramId", strconv.FormatInt(telegramID, 10))
	query.Set("size", strconv.Itoa(streamLookupSize))

	resp, err := c.doRequest("GET", "/api/users/stream?"+query.Encode(), nil)
	if err != nil {
		return nil, err
	}

	var result struct {
		Response struct {
			Users   []User `json:"users"`
			HasMore bool   `json:"hasMore"`
		} `json:"response"`
	}
	if err := json.Unmarshal(resp, &result); err != nil {
		return nil, fmt.Errorf("failed to unmarshal users stream: %w", err)
	}

	return result.Response.Users, nil
}

// exactTelegramIDMatch оставляет только точные совпадения по Telegram ID.
func exactTelegramIDMatch(users []User, telegramID int64) (*User, error) {
	var matches []User
	for _, user := range users {
		if user.TelegramID != nil && *user.TelegramID == telegramID {
			matches = append(matches, user)
		}
	}

	switch len(matches) {
	case 0:
		return nil, nil
	case 1:
		return &matches[0], nil
	default:
		return nil, fmt.Errorf("%w: telegram_id=%d, count=%d", ErrMultipleUsersForTelegramID, telegramID, len(matches))
	}
}

// ResolvedUser — ответ /api/users/resolve. На 2.8.x содержит и uuid, и id,
// поэтому годится для точечной доливки связки; на 3.x поля uuid нет.
type ResolvedUser struct {
	UUID      string `json:"uuid"`
	ID        int64  `json:"id"`
	ShortUUID string `json:"shortUuid"`
	Username  string `json:"username"`
}

// ResolveUserByUUID добирает числовой id по UUID. Работает только на 2.8.x:
// в 3.x UUID удалён из базы панели, и резолвить по нему нечего.
func (c *Client) ResolveUserByUUID(uuid string) (*ResolvedUser, error) {
	version, err := c.DetectAPIVersion()
	if err != nil {
		return nil, err
	}
	if version != APIVersionV2 {
		return nil, fmt.Errorf("resolve by uuid is unavailable on panel %s", version)
	}

	body, err := json.Marshal(map[string]string{"uuid": uuid})
	if err != nil {
		return nil, fmt.Errorf("failed to marshal resolve request: %w", err)
	}

	resp, err := c.doRequest("POST", "/api/users/resolve", body)
	if err != nil {
		if status, ok := APIStatusCode(err); ok && status == http.StatusNotFound {
			return nil, nil
		}
		return nil, err
	}

	var result struct {
		Response ResolvedUser `json:"response"`
	}
	if err := json.Unmarshal(resp, &result); err != nil {
		return nil, fmt.Errorf("failed to unmarshal resolve response: %w", err)
	}

	return &result.Response, nil
}
