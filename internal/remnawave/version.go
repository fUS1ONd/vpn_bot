package remnawave

import (
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"strconv"
	"strings"
)

// APIVersion — контракт API панели, по которому клиент строит запросы.
type APIVersion int

const (
	// APIVersionUnknown — версия панели ещё не определена.
	APIVersionUnknown APIVersion = iota
	// APIVersionV2 — 2.8.x: пользователь адресуется UUID.
	APIVersionV2
	// APIVersionV3 — 3.x: пользователь адресуется числовым id.
	APIVersionV3
)

// String даёт читаемое имя контракта для логов.
func (v APIVersion) String() string {
	switch v {
	case APIVersionV2:
		return "v2"
	case APIVersionV3:
		return "v3"
	default:
		return "unknown"
	}
}

// ErrPanelVersionUnknown — версию панели определить не удалось, и слепой запрос
// отправлять нельзя: на 2.8.x и 3.x путь пользователя различается типом.
var ErrPanelVersionUnknown = errors.New("panel API version is unknown")

// CachedAPIVersion возвращает уже известную версию без обращения к панели.
func (c *Client) CachedAPIVersion() APIVersion {
	c.versionMu.RLock()
	defer c.versionMu.RUnlock()
	return c.apiVersion
}

// SetAPIVersion задаёт контракт принудительно, минуя детект. Нужен тестам, которые
// проверяют поведение под конкретной версией панели; в проде версию определяет
// сам клиент.
func (c *Client) SetAPIVersion(version APIVersion) {
	c.versionMu.Lock()
	defer c.versionMu.Unlock()
	c.apiVersion = version
}

// DetectAPIVersion определяет версию API панели и кэширует результат. Если версия
// уже известна, повторного запроса не делает — принудительное обновление живёт в
// RefreshAPIVersion.
func (c *Client) DetectAPIVersion() (APIVersion, error) {
	if v := c.CachedAPIVersion(); v != APIVersionUnknown {
		return v, nil
	}

	c.detectMu.Lock()
	defer c.detectMu.Unlock()

	// Повторная проверка под detectMu: пока мы ждали замок, версию мог определить
	// параллельный вызов. Без неё десяток одновременных запросов на старте
	// устроил бы десяток походов в metadata.
	if v := c.CachedAPIVersion(); v != APIVersionUnknown {
		return v, nil
	}

	return c.refreshLocked()
}

// RefreshAPIVersion перечитывает версию панели, даже если она уже была известна.
// Конкурентные вызовы схлопываются в один запрос: detectMu держится на всё время
// обращения к панели, иначе после апгрейда десяток параллельных вызовов устроил бы
// шторм из metadata.
func (c *Client) RefreshAPIVersion() (APIVersion, error) {
	c.detectMu.Lock()
	defer c.detectMu.Unlock()

	return c.refreshLocked()
}

// refreshLocked выполняет детект и запись кэша; вызывается с захваченным detectMu.
func (c *Client) refreshLocked() (APIVersion, error) {
	version, err := c.detectAPIVersion()
	if err != nil {
		return APIVersionUnknown, err
	}

	c.versionMu.Lock()
	previous := c.apiVersion
	c.apiVersion = version
	c.versionMu.Unlock()

	if previous != APIVersionUnknown && previous != version {
		slog.Warn("Remnawave panel API contract changed", "from", previous.String(), "to", version.String())
	}

	return version, nil
}

// detectAPIVersion спрашивает версию у панели: сначала metadata, при её недоступности
// — проба поведения user-маршрута.
func (c *Client) detectAPIVersion() (APIVersion, error) {
	version, metaErr := c.panelVersion()
	if metaErr == nil {
		contract, parseErr := contractForPanelVersion(version)
		if parseErr == nil {
			slog.Info("Detected Remnawave panel version", "version", version, "contract", contract.String())
			return contract, nil
		}
		metaErr = parseErr
	}

	slog.Warn("Failed to detect panel version from metadata, probing user route", "error", metaErr)

	contract, probeErr := c.probeAPIVersion()
	if probeErr != nil {
		// Причины оборачиваются через errors.Join, а не форматируются в текст:
		// иначе APIError внутри теряется, и вызывающий код не отличит протухший
		// токен (401/403) от недоступной панели — то есть отказ по токену прошёл бы
		// молча, вместо сообщения владельцу.
		return APIVersionUnknown, errors.Join(ErrPanelVersionUnknown, metaErr, probeErr)
	}

	slog.Info("Detected Remnawave API contract by probe", "contract", contract.String())
	return contract, nil
}

// panelVersion читает версию панели из /api/system/metadata.
func (c *Client) panelVersion() (string, error) {
	resp, err := c.doRequest("GET", "/api/system/metadata", nil)
	if err != nil {
		return "", err
	}

	var result struct {
		Response struct {
			Version string `json:"version"`
		} `json:"response"`
	}
	if err := json.Unmarshal(resp, &result); err != nil {
		return "", fmt.Errorf("failed to unmarshal metadata response: %w", err)
	}
	if result.Response.Version == "" {
		return "", errors.New("metadata response has empty version")
	}

	return result.Response.Version, nil
}

// contractForPanelVersion переводит версию панели в контракт API.
// Мажор ≥ 3 трактуем как 3.x: молча ломаться при выходе 4.0 бот не должен.
func contractForPanelVersion(version string) (APIVersion, error) {
	major, _, _ := strings.Cut(strings.TrimPrefix(strings.TrimSpace(version), "v"), ".")
	n, err := strconv.Atoi(major)
	if err != nil {
		return APIVersionUnknown, fmt.Errorf("unrecognized panel version %q", version)
	}

	switch {
	case n >= 4:
		slog.Warn("Remnawave panel is newer than known to the bot, using 3.x contract", "version", version)
		return APIVersionV3, nil
	case n == 3:
		return APIVersionV3, nil
	case n == 2:
		return APIVersionV2, nil
	default:
		return APIVersionUnknown, fmt.Errorf("unsupported panel version %q", version)
	}
}

// probeUserUUID — валидный UUID, которого в панели заведомо нет. На 2.8.x запрос
// по нему отвечает 404, на 3.x — 400 (в пути ожидается число).
const probeUserUUID = "00000000-0000-0000-0000-000000000000"

// probeAPIVersion определяет контракт по поведению user-маршрута. Нужен, когда
// metadata недоступна: эндпоинт закрыт scope-ом system, и токен с урезанными
// правами получит на нём 403.
//
// Обе пробы состояние панели не меняют и работают на пустой базе. Вторая нужна
// только тогда, когда первая ответила неожиданным кодом.
func (c *Client) probeAPIVersion() (APIVersion, error) {
	version, err := c.probeByPath("/api/users/"+probeUserUUID, APIVersionV2, APIVersionV3)
	if err == nil {
		return version, nil
	}

	// Первая проба ответила чем-то неожиданным — спрашиваем с другой стороны:
	// числовой путь на 2.8.x не проходит валидацию (там ждут uuid), на 3.x он
	// нормален и отвечает 404 или 200.
	version, secondErr := c.probeByPath("/api/users/1", APIVersionV3, APIVersionV2)
	if secondErr != nil {
		return APIVersionUnknown, errors.Join(err, secondErr)
	}

	return version, nil
}

// probeByPath трактует ответ на пробный запрос: 200 или 404 значат «путь принят»
// (onAccepted), 400 — «идентификатор не того типа» (onRejected).
func (c *Client) probeByPath(path string, onAccepted, onRejected APIVersion) (APIVersion, error) {
	_, err := c.doRequest("GET", path, nil)
	if err == nil {
		return onAccepted, nil
	}

	var apiErr *APIError
	if !errors.As(err, &apiErr) {
		return APIVersionUnknown, err
	}

	switch apiErr.StatusCode {
	case http.StatusNotFound:
		return onAccepted, nil
	case http.StatusBadRequest:
		return onRejected, nil
	default:
		return APIVersionUnknown, err
	}
}
