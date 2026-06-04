# Управление устройствами (сброс HWID) — Implementation Plan

> **For Claude:** REQUIRED SUB-SKILL: Use superpowers:executing-plans to implement this plan task-by-task.

**Goal:** Дать пользователю возможность из Telegram-бота посмотреть список своих подключённых
устройств (HWID) и удалить одно конкретное устройство или сбросить все сразу.

**Architecture:** Добавляем три метода в HTTP-клиент Remnawave (`internal/remnawave/client.go`).
Точка входа — inline-кнопка «📱 Управлять устройствами», которую бот присылает под сообщением
статуса (reply-кнопка «👤 Моя подписка»). Само управление устройствами реализовано на
inline-кнопках с callback-роутингом telebot (новый для проекта паттерн — раньше бот работал
только на reply-клавиатуре + FSM). Удаление одного устройства происходит сразу, сброс всех —
через экран подтверждения. После каждого удаления список перерисовывается из ответа API
(эндпоинты delete/delete-all возвращают обновлённый список — повторный GET не нужен).

**Tech Stack:** Go 1.x, `gopkg.in/telebot.v3` v3.3.8, `github.com/stretchr/testify/require`,
Remnawave HWID API. Запуск/тесты — через `make fmt`, `make tests` (см. CLAUDE.md).

---

## Контекст для исполнителя (прочитай перед началом)

### Как устроен роутинг в этом боте
- Бот **не** использует inline-кнопки нигде, кроме того, что мы добавим. Обычные кнопки —
  это reply-клавиатура (текстовые кнопки внизу), роутинг по тексту в
  `internal/bot/handlers.go` → `handleTextMessage` (большой `switch text`).
- Inline-callback'и сейчас только логируются в middleware (`handlers.go:102-108`), но не
  обрабатываются. Мы добавим первые `b.Handle(&btn, handler)` для inline-кнопок.

### Паттерн telebot для inline-кнопок (важно — следуй точно)
```go
// Объявление кнопки-конструктора (var уровня пакета или внутри функции):
var devicesMenu = &tele.ReplyMarkup{}
btnManage := devicesMenu.Data("📱 Управлять устройствами", "dev_manage") // Unique = "dev_manage"
btnDelete := devicesMenu.Data("🔄 iPhone", "dev_del", "3")               // Data="3", доступно через c.Args()[0]

// Регистрация хендлера — telebot роутит по Unique:
b.Handle(&btnManage, bot.handleDevicesManage)

// Внутри хендлера для callback:
idx := c.Args()[0]                 // строковый параметр из Data (split по "|")
c.Edit("новый текст", markup)      // редактирует текущее сообщение
c.RespondAlert("Готово")           // всплывающий alert-тост
c.Respond(&tele.CallbackResponse{Text: "Готово"}) // обычный тост
```
- `r.Data(text, unique, data...)` — конструктор. `data` склеивается через `|`, читается `c.Args()`.
- `b.Handle(&btn, h)` использует `btn.CallbackUnique()` = `"\f"+Unique`. **Регистрировать нужно
  кнопки с теми же `Unique`, что и при построении клавиатуры.** Параметр `Data` на регистрацию
  не влияет — важен только `Unique`.
- Лимит `callback_data` в Telegram — 64 байта. Поэтому в `Data` кладём **индекс** устройства
  (короткий), а не hwid. По индексу заново берём актуальный список и достаём hwid.

### Контракт Remnawave HWID API (проверен по docs/api-remnawave2.6.4.json)
- `GET /api/hwid/devices/{userUuid}` → `{"response":{"total":N,"devices":[{hwid,platform,osVersion,deviceModel,userAgent,createdAt,updatedAt}, ...]}}`. Поля `platform/osVersion/deviceModel/userAgent` могут быть null.
- `POST /api/hwid/devices/delete` body `{"userUuid":"...","hwid":"..."}` → возвращает обновлённый `{"response":{"total","devices"}}`.
- `POST /api/hwid/devices/delete-all` body `{"userUuid":"..."}` → возвращает обновлённый `{"response":{"total","devices"}}`.

### Существующий код для переиспользования
- `internal/remnawave/client.go:240` — `GetUserHwidDevicesCount` (образец GET + парсинга).
- `internal/remnawave/client.go:402` — `c.doRequest(method, path, body)` — единая точка HTTP.
- `internal/remnawave/client_test.go:255` — `TestGetUserHwidDevicesCount` (образец теста с `roundTripFunc`).
- `internal/bot/handlers.go:660` — `handleStatus` (сюда добавляем inline-кнопку).
- `internal/bot/keyboards.go:10` — `BtnStatus` (переименование).
- `internal/bot/render_handler.go` — образец файла с хендлерами-методами на `*Bot` + slog.

### Стиль
- Комментарии в коде — на русском (CLAUDE.md глобальный).
- Коммиты: `<type>: <русское описание>`, без точки, без авторства Claude. Тип в нижнем регистре.
- Коммитить можно часто. После каждой задачи — `make fmt` и `make tests` должны быть зелёными.

---

## Task 1: Метод GetUserHwidDevices в клиенте Remnawave

**Files:**
- Modify: `internal/remnawave/client.go` (рядом с `GetUserHwidDevicesCount`, ~строка 256)
- Test: `internal/remnawave/client_test.go` (после `TestGetUserHwidDevicesCount`, ~строка 274)

**Step 1: Написать падающий тест**

Добавь в `internal/remnawave/client_test.go`:
```go
func TestGetUserHwidDevices(t *testing.T) {
	client := NewClient("https://panel.example.com", "test-token", nil)
	client.http = &http.Client{
		Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
			require.Equal(t, http.MethodGet, r.Method)
			require.Equal(t, "/api/hwid/devices/uuid-1", r.URL.Path)

			payload := `{"response":{"total":2,"devices":[` +
				`{"hwid":"hw-a","platform":"iOS","deviceModel":"iPhone 14","createdAt":"2026-01-01T00:00:00Z"},` +
				`{"hwid":"hw-b","platform":"Android","deviceModel":"Pixel 7","createdAt":"2026-01-02T00:00:00Z"}]}}`
			return &http.Response{
				StatusCode: http.StatusOK,
				Body:       io.NopCloser(strings.NewReader(payload)),
				Header:     make(http.Header),
			}, nil
		}),
	}

	devices, err := client.GetUserHwidDevices("uuid-1")
	require.NoError(t, err)
	require.Len(t, devices, 2)
	require.Equal(t, "hw-a", devices[0].Hwid)
	require.Equal(t, "iOS", devices[0].Platform)
	require.Equal(t, "iPhone 14", devices[0].DeviceModel)
	require.Equal(t, "hw-b", devices[1].Hwid)
}
```

**Step 2: Запустить тест — убедиться, что не компилируется/падает**

Run: `go test ./internal/remnawave/ -run TestGetUserHwidDevices -v`
Expected: FAIL (undefined: HwidDevice / GetUserHwidDevices)

**Step 3: Реализовать минимально**

Добавь в `internal/remnawave/client.go` после `GetUserHwidDevicesCount` (после строки 256):
```go
// HwidDevice — устройство пользователя из Remnawave HWID API.
type HwidDevice struct {
	Hwid        string `json:"hwid"`
	Platform    string `json:"platform"`
	OsVersion   string `json:"osVersion"`
	DeviceModel string `json:"deviceModel"`
}

// GetUserHwidDevices возвращает список HWID-устройств пользователя.
func (c *Client) GetUserHwidDevices(uuid string) ([]HwidDevice, error) {
	resp, err := c.doRequest("GET", "/api/hwid/devices/"+uuid, nil)
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
```

**Step 4: Запустить тест — убедиться, что прошёл**

Run: `go test ./internal/remnawave/ -run TestGetUserHwidDevices -v`
Expected: PASS

**Step 5: Коммит**

```bash
git add internal/remnawave/client.go internal/remnawave/client_test.go
git commit -m "feat: добавить GetUserHwidDevices в клиент Remnawave"
```

---

## Task 2: Методы DeleteUserHwidDevice и DeleteAllUserHwidDevices

**Files:**
- Modify: `internal/remnawave/client.go` (после `GetUserHwidDevices`)
- Test: `internal/remnawave/client_test.go`

**Step 1: Написать падающие тесты**

Добавь в `internal/remnawave/client_test.go`:
```go
func TestDeleteUserHwidDevice(t *testing.T) {
	client := NewClient("https://panel.example.com", "test-token", nil)
	client.http = &http.Client{
		Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
			require.Equal(t, http.MethodPost, r.Method)
			require.Equal(t, "/api/hwid/devices/delete", r.URL.Path)

			body, _ := io.ReadAll(r.Body)
			require.JSONEq(t, `{"userUuid":"uuid-1","hwid":"hw-a"}`, string(body))

			// API возвращает обновлённый список (осталось одно устройство)
			payload := `{"response":{"total":1,"devices":[{"hwid":"hw-b","platform":"Android","deviceModel":"Pixel 7"}]}}`
			return &http.Response{
				StatusCode: http.StatusOK,
				Body:       io.NopCloser(strings.NewReader(payload)),
				Header:     make(http.Header),
			}, nil
		}),
	}

	devices, err := client.DeleteUserHwidDevice("uuid-1", "hw-a")
	require.NoError(t, err)
	require.Len(t, devices, 1)
	require.Equal(t, "hw-b", devices[0].Hwid)
}

func TestDeleteAllUserHwidDevices(t *testing.T) {
	client := NewClient("https://panel.example.com", "test-token", nil)
	client.http = &http.Client{
		Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
			require.Equal(t, http.MethodPost, r.Method)
			require.Equal(t, "/api/hwid/devices/delete-all", r.URL.Path)

			body, _ := io.ReadAll(r.Body)
			require.JSONEq(t, `{"userUuid":"uuid-1"}`, string(body))

			payload := `{"response":{"total":0,"devices":[]}}`
			return &http.Response{
				StatusCode: http.StatusOK,
				Body:       io.NopCloser(strings.NewReader(payload)),
				Header:     make(http.Header),
			}, nil
		}),
	}

	err := client.DeleteAllUserHwidDevices("uuid-1")
	require.NoError(t, err)
}
```

**Step 2: Запустить — убедиться, что падает**

Run: `go test ./internal/remnawave/ -run 'TestDelete.*HwidDevice' -v`
Expected: FAIL (undefined methods)

**Step 3: Реализовать**

Добавь в `internal/remnawave/client.go` после `GetUserHwidDevices`:
```go
// DeleteUserHwidDevice удаляет одно HWID-устройство пользователя и
// возвращает обновлённый список оставшихся устройств.
func (c *Client) DeleteUserHwidDevice(uuid, hwid string) ([]HwidDevice, error) {
	body, err := json.Marshal(map[string]string{
		"userUuid": uuid,
		"hwid":     hwid,
	})
	if err != nil {
		return nil, fmt.Errorf("failed to marshal delete device request: %w", err)
	}

	resp, err := c.doRequest("POST", "/api/hwid/devices/delete", body)
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
func (c *Client) DeleteAllUserHwidDevices(uuid string) error {
	body, err := json.Marshal(map[string]string{"userUuid": uuid})
	if err != nil {
		return fmt.Errorf("failed to marshal delete-all request: %w", err)
	}

	_, err = c.doRequest("POST", "/api/hwid/devices/delete-all", body)
	return err
}
```

**Step 4: Запустить — убедиться, что прошло**

Run: `go test ./internal/remnawave/ -run 'TestDelete.*HwidDevice' -v`
Expected: PASS

**Step 5: Коммит**

```bash
git add internal/remnawave/client.go internal/remnawave/client_test.go
git commit -m "feat: добавить удаление HWID-устройств в клиент Remnawave"
```

---

## Task 3: Константы кнопок и клавиатуры устройств

**Files:**
- Modify: `internal/bot/keyboards.go` (константы кнопок ~строка 10; новые функции в конце файла)
- Test: `internal/bot/keyboards_test.go`

**Step 1: Переименовать reply-кнопку**

В `internal/bot/keyboards.go:10` заменить:
```go
	BtnStatus       = "👤 Мой статус"
```
на:
```go
	BtnStatus       = "👤 Моя подписка"
```
Роутинг идёт по константе `BtnStatus` (в `handleTextMessage`), менять `case` не нужно.
Проверь grep по строковому литералу: `grep -rn "Мой статус" internal/` — если он есть в тестах,
поправь там ожидание на «Моя подписка».

**Step 2: Написать падающий тест клавиатур**

Добавь в `internal/bot/keyboards_test.go`:
```go
func TestDevicesStatusInlineKeyboard(t *testing.T) {
	kb := DevicesStatusInlineKeyboard()
	require.NotNil(t, kb)
	require.Len(t, kb.InlineKeyboard, 1)
	require.Len(t, kb.InlineKeyboard[0], 1)
	require.Equal(t, "dev_manage", kb.InlineKeyboard[0][0].Unique)
}

func TestDevicesManagementKeyboard(t *testing.T) {
	devices := []remnawave.HwidDevice{
		{Hwid: "hw-a", Platform: "iOS", DeviceModel: "iPhone 14"},
		{Hwid: "hw-b", Platform: "Android", DeviceModel: "Pixel 7"},
	}
	kb := DevicesManagementKeyboard(devices)
	require.NotNil(t, kb)
	// 2 устройства + строка "сбросить все" + строка "закрыть" = 4 ряда
	require.Len(t, kb.InlineKeyboard, 4)
	require.Equal(t, "dev_del", kb.InlineKeyboard[0][0].Unique)
	require.Equal(t, "0", kb.InlineKeyboard[0][0].Data)
	require.Equal(t, "dev_del", kb.InlineKeyboard[1][0].Unique)
	require.Equal(t, "1", kb.InlineKeyboard[1][0].Data)
	require.Equal(t, "dev_reset_all", kb.InlineKeyboard[2][0].Unique)
	require.Equal(t, "dev_close", kb.InlineKeyboard[3][0].Unique)
}

func TestDevicesManagementKeyboardEmpty(t *testing.T) {
	kb := DevicesManagementKeyboard(nil)
	// нет устройств -> только кнопка "закрыть", без "сбросить все"
	require.Len(t, kb.InlineKeyboard, 1)
	require.Equal(t, "dev_close", kb.InlineKeyboard[0][0].Unique)
}
```
Убедись, что в `keyboards_test.go` импортирован пакет
`"github.com/<...>/vpn_bot/internal/remnawave"` (узнай точный module path из `go.mod`,
строка `module ...`). Если импорт ещё не используется в этом тестовом файле — добавь его.

**Step 3: Запустить — убедиться, что падает**

Run: `go test ./internal/bot/ -run 'TestDevices.*Keyboard' -v`
Expected: FAIL (undefined functions)

**Step 4: Реализовать константы и клавиатуры**

В `internal/bot/keyboards.go` добавить константы Unique (рядом с прочими константами кнопок):
```go
// Unique-идентификаторы inline-кнопок управления устройствами
const (
	cbDevicesManage   = "dev_manage"
	cbDeviceDelete    = "dev_del"
	cbDevicesResetAll = "dev_reset_all"
	cbDevicesResetAllConfirm = "dev_reset_all_ok"
	cbDevicesClose    = "dev_close"
)
```

Добавить функции построения клавиатур (в конце файла). Проверь, что `remnawave`
импортирован в `keyboards.go` (если нет — добавь импорт):
```go
// DevicesStatusInlineKeyboard — inline-кнопка под сообщением статуса, открывающая
// управление устройствами.
func DevicesStatusInlineKeyboard() *tele.ReplyMarkup {
	menu := &tele.ReplyMarkup{}
	btn := menu.Data("📱 Управлять устройствами", cbDevicesManage)
	menu.Inline(menu.Row(btn))
	return menu
}

// truncateDeviceLabel обрезает подпись устройства до разумной длины для кнопки.
func truncateDeviceLabel(s string) string {
	const max = 25
	r := []rune(s)
	if len(r) <= max {
		return s
	}
	return string(r[:max-1]) + "…"
}

// deviceLabel формирует читаемую подпись устройства для кнопки.
func deviceLabel(d remnawave.HwidDevice) string {
	platform := d.Platform
	if platform == "" {
		platform = "Устройство"
	}
	label := platform
	if d.DeviceModel != "" {
		label = platform + " · " + d.DeviceModel
	}
	return truncateDeviceLabel(label)
}

// DevicesManagementKeyboard — список устройств как inline-кнопки (нажатие = удаление),
// плюс «Сбросить все» (если есть устройства) и «Закрыть».
func DevicesManagementKeyboard(devices []remnawave.HwidDevice) *tele.ReplyMarkup {
	menu := &tele.ReplyMarkup{}
	var rows []tele.Row

	for i, d := range devices {
		btn := menu.Data("🔄 "+deviceLabel(d), cbDeviceDelete, fmt.Sprintf("%d", i))
		rows = append(rows, menu.Row(btn))
	}

	if len(devices) > 0 {
		resetAll := menu.Data("🗑 Сбросить все устройства", cbDevicesResetAll)
		rows = append(rows, menu.Row(resetAll))
	}

	closeBtn := menu.Data("🔙 Закрыть", cbDevicesClose)
	rows = append(rows, menu.Row(closeBtn))

	menu.Inline(rows...)
	return menu
}

// DevicesResetAllConfirmKeyboard — подтверждение сброса всех устройств.
func DevicesResetAllConfirmKeyboard() *tele.ReplyMarkup {
	menu := &tele.ReplyMarkup{}
	yes := menu.Data("✅ Да, сбросить все", cbDevicesResetAllConfirm)
	no := menu.Data("🔙 Отмена", cbDevicesManage)
	menu.Inline(menu.Row(yes), menu.Row(no))
	return menu
}
```
Если `fmt` ещё не импортирован в `keyboards.go` — добавь.

**Step 5: Запустить — убедиться, что прошло**

Run: `go test ./internal/bot/ -run 'TestDevices.*Keyboard' -v`
Expected: PASS

**Step 6: Коммит**

```bash
git add internal/bot/keyboards.go internal/bot/keyboards_test.go
git commit -m "feat: клавиатуры и кнопки управления устройствами"
```

---

## Task 4: Хендлеры управления устройствами

**Files:**
- Create: `internal/bot/devices.go`
- Test: `internal/bot/devices_test.go` (см. примечание о тестируемости ниже)

**Примечание о тестах:** хендлеры завязаны на `tele.Context` и сетевой клиент Remnawave,
их сложно покрыть юнит-тестом без рефакторинга. Вынеси чистую логику в тестируемые функции
и покрой их; сами хендлеры проверим вручную в Task 6. Конкретно вынеси формирование текста
сообщения и выбор hwid по индексу.

**Step 1: Написать падающий тест чистой логики**

Создай `internal/bot/devices_test.go`:
```go
package bot

import (
	"testing"

	"github.com/stretchr/testify/require"
	// поправь путь модуля при необходимости:
	"github.com/fus1ond/vpn_bot/internal/remnawave"
)

func TestBuildDevicesMessage(t *testing.T) {
	// нет устройств
	require.Contains(t, buildDevicesMessage(nil), "нет подключённых устройств")

	// есть устройства
	devices := []remnawave.HwidDevice{
		{Hwid: "hw-a", Platform: "iOS", DeviceModel: "iPhone 14"},
	}
	msg := buildDevicesMessage(devices)
	require.Contains(t, msg, "Подключено устройств: 1")
}

func TestDeviceByIndex(t *testing.T) {
	devices := []remnawave.HwidDevice{
		{Hwid: "hw-a"}, {Hwid: "hw-b"},
	}
	d, ok := deviceByIndex(devices, "1")
	require.True(t, ok)
	require.Equal(t, "hw-b", d.Hwid)

	_, ok = deviceByIndex(devices, "5")
	require.False(t, ok)

	_, ok = deviceByIndex(devices, "abc")
	require.False(t, ok)
}
```
Замени `github.com/fus1ond/vpn_bot` на реальный module path из `go.mod`.

**Step 2: Запустить — убедиться, что падает**

Run: `go test ./internal/bot/ -run 'TestBuildDevicesMessage|TestDeviceByIndex' -v`
Expected: FAIL (undefined)

**Step 3: Реализовать devices.go**

Создай `internal/bot/devices.go`:
```go
package bot

import (
	"fmt"
	"log/slog"
	"strconv"

	tele "gopkg.in/telebot.v3"

	// поправь путь модуля при необходимости:
	"github.com/fus1ond/vpn_bot/internal/remnawave"
)

// buildDevicesMessage формирует текст экрана управления устройствами.
func buildDevicesMessage(devices []remnawave.HwidDevice) string {
	if len(devices) == 0 {
		return "<b>📱 Управление устройствами</b>\n\nУ вас нет подключённых устройств."
	}
	msg := "<b>📱 Управление устройствами</b>\n\n"
	msg += fmt.Sprintf("Подключено устройств: %d\n\n", len(devices))
	msg += "Нажмите на устройство, чтобы удалить его, либо сбросьте все сразу."
	return msg
}

// deviceByIndex возвращает устройство по строковому индексу из callback-данных.
func deviceByIndex(devices []remnawave.HwidDevice, idxStr string) (remnawave.HwidDevice, bool) {
	idx, err := strconv.Atoi(idxStr)
	if err != nil || idx < 0 || idx >= len(devices) {
		return remnawave.HwidDevice{}, false
	}
	return devices[idx], true
}

// resolveUserUUID возвращает remnawave UUID для отправителя или ошибку-сообщение.
func (b *Bot) resolveUserUUID(telegramID int64) (string, bool) {
	user, err := b.db.GetUserByTelegramID(telegramID)
	if err != nil || user == nil || user.RemnawaveUUID == "" {
		return "", false
	}
	return user.RemnawaveUUID, true
}

// handleDevicesManage показывает экран управления устройствами (inline-список).
func (b *Bot) handleDevicesManage(c tele.Context) error {
	uuid, ok := b.resolveUserUUID(c.Sender().ID)
	if !ok {
		return c.RespondAlert("Сначала активируйте подписку")
	}

	devices, err := b.remnawave.GetUserHwidDevices(uuid)
	if err != nil {
		slog.Error("Failed to get HWID devices", "error", err, "telegram_id", c.Sender().ID)
		return c.RespondAlert("Ошибка получения списка устройств")
	}

	if err := c.Edit(buildDevicesMessage(devices), &tele.SendOptions{
		ParseMode:   tele.ModeHTML,
		ReplyMarkup: DevicesManagementKeyboard(devices),
	}); err != nil {
		// Если редактировать нечего (например, вызвано не из callback) — шлём новое сообщение.
		return c.Send(buildDevicesMessage(devices), &tele.SendOptions{
			ParseMode:   tele.ModeHTML,
			ReplyMarkup: DevicesManagementKeyboard(devices),
		})
	}
	return c.Respond()
}

// handleDeviceDelete удаляет одно устройство по индексу и перерисовывает список.
func (b *Bot) handleDeviceDelete(c tele.Context) error {
	uuid, ok := b.resolveUserUUID(c.Sender().ID)
	if !ok {
		return c.RespondAlert("Сначала активируйте подписку")
	}

	args := c.Args()
	if len(args) == 0 {
		return c.RespondAlert("Некорректный запрос")
	}

	// Берём актуальный список и сопоставляем по индексу (индекс мог устареть).
	devices, err := b.remnawave.GetUserHwidDevices(uuid)
	if err != nil {
		slog.Error("Failed to get HWID devices before delete", "error", err, "telegram_id", c.Sender().ID)
		return c.RespondAlert("Ошибка получения списка устройств")
	}

	device, found := deviceByIndex(devices, args[0])
	if !found {
		// Список устарел — перерисуем актуальный.
		_ = c.Edit(buildDevicesMessage(devices), &tele.SendOptions{
			ParseMode:   tele.ModeHTML,
			ReplyMarkup: DevicesManagementKeyboard(devices),
		})
		return c.RespondAlert("Список обновлён, попробуйте снова")
	}

	updated, err := b.remnawave.DeleteUserHwidDevice(uuid, device.Hwid)
	if err != nil {
		slog.Error("Failed to delete HWID device", "error", err, "telegram_id", c.Sender().ID)
		return c.RespondAlert("Ошибка удаления устройства")
	}

	_ = c.Edit(buildDevicesMessage(updated), &tele.SendOptions{
		ParseMode:   tele.ModeHTML,
		ReplyMarkup: DevicesManagementKeyboard(updated),
	})
	return c.Respond(&tele.CallbackResponse{Text: "Устройство удалено"})
}

// handleDevicesResetAll показывает экран подтверждения сброса всех устройств.
func (b *Bot) handleDevicesResetAll(c tele.Context) error {
	msg := "<b>🗑 Сбросить все устройства?</b>\n\n" +
		"Все подключённые устройства будут отключены. " +
		"Их нужно будет подключить заново по ссылке из подписки."
	if err := c.Edit(msg, &tele.SendOptions{
		ParseMode:   tele.ModeHTML,
		ReplyMarkup: DevicesResetAllConfirmKeyboard(),
	}); err != nil {
		return c.RespondAlert("Ошибка")
	}
	return c.Respond()
}

// handleDevicesResetAllConfirm сбрасывает все устройства пользователя.
func (b *Bot) handleDevicesResetAllConfirm(c tele.Context) error {
	uuid, ok := b.resolveUserUUID(c.Sender().ID)
	if !ok {
		return c.RespondAlert("Сначала активируйте подписку")
	}

	if err := b.remnawave.DeleteAllUserHwidDevices(uuid); err != nil {
		slog.Error("Failed to reset all HWID devices", "error", err, "telegram_id", c.Sender().ID)
		return c.RespondAlert("Ошибка сброса устройств")
	}

	_ = c.Edit(buildDevicesMessage(nil), &tele.SendOptions{
		ParseMode:   tele.ModeHTML,
		ReplyMarkup: DevicesManagementKeyboard(nil),
	})
	return c.Respond(&tele.CallbackResponse{Text: "Все устройства сброшены"})
}

// handleDevicesClose закрывает экран управления устройствами.
func (b *Bot) handleDevicesClose(c tele.Context) error {
	if err := c.Delete(); err != nil {
		// Если удалить не получилось — просто убираем inline-клавиатуру.
		_ = c.Edit("Готово.", &tele.SendOptions{ParseMode: tele.ModeHTML})
	}
	return c.Respond()
}
```
Замени `github.com/fus1ond/vpn_bot` на реальный module path. Проверь, что `b.db.GetUserByTelegramID`
и поле `RemnawaveUUID` называются именно так (см. `internal/database/users.go` и существующее
использование в `handleStatus`).

**Step 4: Запустить — убедиться, что прошло**

Run: `go test ./internal/bot/ -run 'TestBuildDevicesMessage|TestDeviceByIndex' -v`
Expected: PASS

**Step 5: Коммит**

```bash
git add internal/bot/devices.go internal/bot/devices_test.go
git commit -m "feat: хендлеры управления устройствами"
```

---

## Task 5: Регистрация callback-роутинга и кнопка в статусе

**Files:**
- Modify: `internal/bot/handlers.go` (регистрация ~строки 126-132; `handleStatus` ~660-688)

**Step 1: Зарегистрировать inline-хендлеры**

В `internal/bot/handlers.go`, рядом с остальными `b.Handle(...)` (после строки 132), добавь.
Кнопки для регистрации строим через тот же `ReplyMarkup`, важен только `Unique`:
```go
	// Inline-кнопки управления устройствами (роутинг по Unique)
	devMenu := &tele.ReplyMarkup{}
	btnDevManage := devMenu.Data("", cbDevicesManage)
	btnDevDelete := devMenu.Data("", cbDeviceDelete)
	btnDevResetAll := devMenu.Data("", cbDevicesResetAll)
	btnDevResetAllOK := devMenu.Data("", cbDevicesResetAllConfirm)
	btnDevClose := devMenu.Data("", cbDevicesClose)

	b.Handle(&btnDevManage, bot.handleDevicesManage)
	b.Handle(&btnDevDelete, bot.handleDeviceDelete)
	b.Handle(&btnDevResetAll, bot.handleDevicesResetAll)
	b.Handle(&btnDevResetAllOK, bot.handleDevicesResetAllConfirm)
	b.Handle(&btnDevClose, bot.handleDevicesClose)
```
(Примечание: `Data("", unique)` достаточно для регистрации — `Handle` использует только `Unique`.)

**Step 2: Добавить inline-кнопку под статусом**

В `handleStatus` (после формирования `msg`, строки ~683-687). Сейчас статус уходит с reply-меню.
Telegram запрещает reply- и inline-разметку в одном сообщении, поэтому:
- основное сообщение статуса отправляем с inline-кнопкой устройств;
- reply-меню оставляем активным отдельным лёгким сообщением **только если** его нужно
  обновить. Проще: отправить статус с inline-клавиатурой, а reply-меню уже «прилипло» снизу
  с предыдущих экранов. Реализация:
```go
	msg := FormatUserStatus(remnawaveUser, user, b.isTrialUser(telegramID), devicesCount)

	// Сначала гарантируем актуальное reply-меню (на случай, если его не было).
	// Затем отправляем статус с inline-кнопкой управления устройствами.
	return c.Send(msg, &tele.SendOptions{
		ParseMode:   tele.ModeHTML,
		ReplyMarkup: DevicesStatusInlineKeyboard(),
	})
```
⚠️ Если при ручной проверке (Task 6) окажется, что reply-меню пропадает — добавь перед этим
отправку короткого сообщения с reply-меню:
```go
	_ = c.Send("…", &tele.SendOptions{ReplyMarkup: b.userKeyboard(telegramID)})
```
или объедини в два сообщения. Финальный выбор подтверди вживую — это единственное место,
где поведение Telegram надо проверить руками.

**Step 3: Сборка и тесты**

Run: `make fmt && make tests`
Expected: всё зелёное, проект компилируется.

**Step 4: Коммит**

```bash
git add internal/bot/handlers.go
git commit -m "feat: подключить управление устройствами к статусу подписки"
```

---

## Task 6: Ручная проверка end-to-end

**Шаги:**
```bash
make up     # пересобрать и поднять бота
make logs   # смотреть логи
```

Проверить в Telegram под реальным/тестовым пользователем с активной подпиской:
1. Нажать «👤 Моя подписка» → приходит статус, под ним inline-кнопка «📱 Управлять устройствами».
   Убедиться, что reply-меню снизу не пропало (если пропало — доработать Task 5 Step 2).
2. Нажать «📱 Управлять устройствами» → сообщение редактируется в список устройств.
3. Нажать на устройство → оно удаляется, список перерисовывается, всплывает тост «Устройство удалено».
   Сверить в панели Remnawave, что устройство реально удалено.
4. Нажать «🗑 Сбросить все устройства» → экран подтверждения → «✅ Да, сбросить все» →
   сообщение «нет подключённых устройств», тост «Все устройства сброшены». Сверить в панели.
5. «🔙 Отмена» на экране подтверждения → возврат к списку.
6. «🔙 Закрыть» → сообщение закрывается/убирается клавиатура.
7. Кейс: пользователь без подписки (нет remnawave_uuid) — нажатие кнопки даёт alert
   «Сначала активируйте подписку» (можно сэмулировать или проверить логи).
8. Кейс: 0 устройств — список показывает «нет подключённых устройств», кнопки «сбросить все» нет.

Если всё работает — переходим к документации.

---

## Task 7: Документация

**Files:**
- Modify: `CLAUDE.md` (раздел «Важные заметки»)
- Create: `docs/progress/2026-06-05-delete-devices-progress.md`

**Step 1: Обновить CLAUDE.md**

В раздел «Важные заметки» добавить пункт:
```markdown
15. **Управление устройствами** — пользователь с активной подпиской может из бота
    («👤 Моя подписка» → «📱 Управлять устройствами») посмотреть подключённые HWID-устройства,
    удалить одно или сбросить все. Реализовано на inline-кнопках (см. `internal/bot/devices.go`),
    API — `GetUserHwidDevices`/`DeleteUserHwidDevice`/`DeleteAllUserHwidDevices`.
```
Документацию обновляй через агента **docs-updater** (правило глобального CLAUDE.md) — запусти
его на изменения этой ветки, чтобы он синхронизировал README/доки, если нужно.

**Step 2: Создать progress-файл**

Создай `docs/progress/2026-06-05-delete-devices-progress.md` со ссылкой на этот план
(`docs/plans/2026-06-05-delete-devices-plan.md`), кратким описанием выполненного и результатами
`make tests`.

**Step 3: Финальная проверка и коммит**

```bash
make fmt && make tests
git add CLAUDE.md docs/progress/2026-06-05-delete-devices-progress.md
git commit -m "docs: описать фичу управления устройствами"
```

---

## Итоговый чек-лист завершения
- [ ] `make fmt` — без ошибок
- [ ] `make tests` — все зелёные (включая новые тесты клиента и клавиатур)
- [ ] Ручная проверка end-to-end пройдена (Task 6)
- [ ] Устройства реально удаляются в панели Remnawave
- [ ] reply-меню не ломается при показе статуса с inline-кнопкой
- [ ] Документация и progress-файл обновлены
- [ ] Коммиты атомарные, в правильном формате, без авторства Claude
