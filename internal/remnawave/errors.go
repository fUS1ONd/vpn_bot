package remnawave

import (
	"encoding/json"
	"errors"
	"fmt"
)

// APIError — ответ панели с кодом ≥ 400. Статус нужен вызывающему коду отдельно
// от текста: 404 означает «пользователя нет», 400 — «идентификатор не того типа»
// (сигнал к ре-детекту версии), 401/403 — проблема с токеном.
type APIError struct {
	StatusCode int
	// ErrorCode — код панели из тела ответа (например, A025). В 2.8.1 тело у 404
	// в спеке не описано вовсе, поэтому поле может быть пустым и служит только
	// уточнением текста, а не признаком.
	ErrorCode string
	Message   string
	Body      string
}

func (e *APIError) Error() string {
	if e.ErrorCode != "" {
		return fmt.Sprintf("API error %d (%s): %s", e.StatusCode, e.ErrorCode, e.Body)
	}
	return fmt.Sprintf("API error %d: %s", e.StatusCode, e.Body)
}

// Is связывает 404 с ErrUserNotFound, чтобы вызывающий код проверял отсутствие
// пользователя через errors.Is, а не сравнением текста ошибки.
func (e *APIError) Is(target error) bool {
	return target == ErrUserNotFound && e.StatusCode == 404
}

// newAPIError разбирает тело ошибки панели. Формат унифицирован в 3.x
// (`{timestamp, path, message, errorCode}`), в 2.8.1 описан не для всех кодов —
// поэтому нечитаемое тело не ошибка разбора, а просто пустые поля.
func newAPIError(statusCode int, body []byte) *APIError {
	apiErr := &APIError{StatusCode: statusCode, Body: string(body)}

	var parsed struct {
		Message   string `json:"message"`
		ErrorCode string `json:"errorCode"`
	}
	if err := json.Unmarshal(body, &parsed); err == nil {
		apiErr.Message = parsed.Message
		apiErr.ErrorCode = parsed.ErrorCode
	}

	return apiErr
}

// APIStatusCode возвращает HTTP-статус ошибки панели и признак, что ошибка вообще
// пришла от API, а не из транспорта.
func APIStatusCode(err error) (int, bool) {
	var apiErr *APIError
	if errors.As(err, &apiErr) {
		return apiErr.StatusCode, true
	}
	return 0, false
}

// IsAuthError сообщает, что панель отказала по токену: 401 — протухший или
// отозванный, 403 — не хватает scope. Обе ситуации требуют вмешательства
// владельца, а не молчаливой деградации.
func IsAuthError(err error) bool {
	status, ok := APIStatusCode(err)
	return ok && (status == 401 || status == 403)
}
