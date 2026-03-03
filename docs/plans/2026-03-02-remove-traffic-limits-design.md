# Удаление лимитов трафика — План реализации

> **For Claude:** REQUIRED SUB-SKILL: Use superpowers:executing-plans to implement this plan task-by-task.

**Goal:** Убрать систему лимитов трафика из бота. Лимит на сервере больше не тарифицируется — ограничения не нужны. Сохранить отображение использованного трафика за месяц (без лимита).

**Architecture:** Удаляем всё, связанное с лимитами: константу 30GB, scheduler сброса лимитов (не нужен — Remnawave сам сбрасывает `usedTrafficBytes` по стратегии `MONTH`), кнопку «Добавить трафик» в админке, состояние `StateWaitAddTraffic`. При создании пользователя ставим `trafficLimitBytes=0` (unlimited) + `trafficLimitStrategy="MONTH"` (Remnawave будет сбрасывать счётчик 1-го числа). В статусе показываем только использованный трафик за месяц — без лимита и процентов.

**Tech Stack:** Go, Remnawave API

**Результат ресёрча Remnawave API:**
- `trafficLimitBytes: 0` = unlimited (без ограничений)
- `trafficLimitStrategy: "MONTH"` = Remnawave автоматически сбрасывает `usedTrafficBytes` 1-го числа
- Эти два параметра совместимы: можно иметь безлимит + ежемесячный сброс счётчика
- Наш scheduler.go выполнял сброс **лимитов** (а не счётчика) — он больше не нужен

**Решения пользователя:**
- Отображение трафика: показывать только использованный за месяц
- Кнопка «Добавить трафик»: удалить
- Remnawave API: trafficLimitBytes=0, trafficLimitStrategy="MONTH"

---

### Task 1: Убрать константы и лимит при создании пользователя

**Files:**
- Modify: `internal/remnawave/client.go:12-27` (константы)
- Modify: `internal/remnawave/client.go:114-150` (CreateUser)
- Modify: `internal/remnawave/client.go:101-107` (UpdateUserRequest)
- Delete method: `internal/remnawave/client.go:213-227` (UpdateUserTraffic)

**Step 1: Удалить константу TrafficLimit30GB, оставить TrafficStrategyMonth**

В файле `internal/remnawave/client.go`:

```go
// Было:
const (
	// TrafficLimit30GB — лимит трафика 30 ГБ в байтах
	TrafficLimit30GB = 32212254720

	// TrafficStrategyMonth — стратегия сброса трафика раз в месяц
	TrafficStrategyMonth = "MONTH"

	// StatusActive — активный пользователь
	StatusActive = "ACTIVE"
	...
)

// Стало:
const (
	// TrafficStrategyMonth — стратегия сброса трафика раз в месяц (Remnawave сбрасывает счётчик 1-го числа)
	TrafficStrategyMonth = "MONTH"

	// StatusActive — активный пользователь
	StatusActive = "ACTIVE"
	...
)
```

**Step 2: Обновить CreateUser — поставить trafficLimitBytes=0 (unlimited)**

```go
// Было:
req := CreateUserRequest{
	Username:             username,
	TelegramID:           telegramID,
	TrafficLimitBytes:    TrafficLimit30GB,
	TrafficLimitStrategy: TrafficStrategyMonth,
	ExpireAt:             "2099-01-01T00:00:00Z",
}

// Стало:
req := CreateUserRequest{
	Username:             username,
	TelegramID:           telegramID,
	TrafficLimitBytes:    0, // Без ограничений
	TrafficLimitStrategy: TrafficStrategyMonth,
	ExpireAt:             "2099-01-01T00:00:00Z",
}
```

**Step 3: Удалить функцию UpdateUserTraffic (строки 213-227)**

Использовалась только для изменения лимита (в admin и scheduler). Без лимитов не нужна.

**Step 4: Удалить поле TrafficLimitBytes из UpdateUserRequest**

```go
// Было:
type UpdateUserRequest struct {
	UUID              string  `json:"uuid"`
	Username          *string `json:"username,omitempty"`
	TrafficLimitBytes *int64  `json:"trafficLimitBytes,omitempty"`
	Status            string  `json:"status,omitempty"`
}

// Стало:
type UpdateUserRequest struct {
	UUID     string  `json:"uuid"`
	Username *string `json:"username,omitempty"`
	Status   string  `json:"status,omitempty"`
}
```

**Step 5: Проверить компиляцию**

Run: `cd /home/krivonosov/projects/vpn_bot && go build ./...`
Expected: ошибки компиляции в scheduler.go и admin.go (ссылки на удалённые сущности). Это ожидаемо — исправим в следующих задачах.

---

### Task 2: Удалить scheduler (сброс лимитов 1-го числа)

**Причина удаления:** scheduler сбрасывал **увеличенные лимиты** обратно к 30 GB. Теперь лимитов нет. Сброс **счётчика** `usedTrafficBytes` делает сам Remnawave по стратегии `MONTH`.

**Files:**
- Delete: `internal/bot/scheduler.go` (весь файл)
- Modify: файл где вызывается `StartScheduler` (найти через grep)

**Step 1: Найти и удалить вызов StartScheduler**

Найти строку `go bot.StartScheduler(` или `b.StartScheduler(` и удалить вызов.

**Step 2: Удалить файл scheduler.go**

```bash
rm internal/bot/scheduler.go
```

**Step 3: Проверить компиляцию**

Run: `cd /home/krivonosov/projects/vpn_bot && go build ./...`
Expected: ошибки из admin.go (ссылки на удалённые функции трафика).

---

### Task 3: Удалить кнопку «Добавить трафик» и всю логику

**Files:**
- Modify: `internal/bot/admin.go:67-127` (удалить handleAddTrafficRequest и processAddTraffic)
- Modify: `internal/bot/handlers.go:23` (удалить StateWaitAddTraffic)
- Modify: `internal/bot/handlers.go:220-227` (удалить case StateWaitAddTraffic)
- Modify: `internal/bot/handlers.go:287-288` (удалить case BtnAdminAddTraffic)
- Modify: `internal/bot/keyboards.go:34` (удалить BtnAdminAddTraffic)
- Modify: `internal/bot/keyboards.go:86-96` (убрать кнопку из меню)
- Modify: `internal/bot/messages.go:145-150` (удалить MsgEnterAddTraffic)

**Step 1: Удалить handleAddTrafficRequest и processAddTraffic из admin.go**

Удалить строки 67-127 (оба метода целиком).

**Step 2: Удалить StateWaitAddTraffic из handlers.go**

```go
// Удалить строку:
StateWaitAddTraffic = "wait_add_traffic" // Ожидание данных для добавления трафика
```

**Step 3: Удалить обработку StateWaitAddTraffic в handleTextMessage**

```go
// Удалить блок (строки 220-227):
case StateWaitAddTraffic:
	if text == BtnCancel {
		b.userStates.Delete(telegramID)
		return c.Send("Отменено", &tele.SendOptions{ReplyMarkup: AdminKeyboard()})
	}
	if b.isAdmin(c) {
		return b.processAddTraffic(c, text)
	}
```

**Step 4: Удалить маршрутизацию BtnAdminAddTraffic в handleTextMessage**

```go
// Удалить строки 287-288:
case BtnAdminAddTraffic:
	return b.handleAddTrafficRequest(c)
```

**Step 5: Удалить BtnAdminAddTraffic из keyboards.go**

```go
// Удалить строку 34:
BtnAdminAddTraffic = "📊 Добавить трафик"
```

**Step 6: Убрать кнопку из AdminManageKeyboard**

```go
// Было:
menu.Reply(
	menu.Row(menu.Text(BtnAdminCreateInvite), menu.Text(BtnAdminViewInvites)),
	menu.Row(menu.Text(BtnAdminAddTraffic), menu.Text(BtnAdminBanUser)),
	menu.Row(menu.Text(BtnAdminDeleteInvite), menu.Text(BtnAdminModerators)),
	menu.Row(menu.Text(BtnAdminBack)),
)

// Стало:
menu.Reply(
	menu.Row(menu.Text(BtnAdminCreateInvite), menu.Text(BtnAdminViewInvites)),
	menu.Row(menu.Text(BtnAdminBanUser), menu.Text(BtnAdminDeleteInvite)),
	menu.Row(menu.Text(BtnAdminModerators)),
	menu.Row(menu.Text(BtnAdminBack)),
)
```

**Step 7: Удалить MsgEnterAddTraffic из messages.go**

```go
// Удалить:
MsgEnterAddTraffic = `<b>📊 Добавить трафик</b>

Введите данные через пробел:
<code>telegram_id GB</code>

Пример: <code>123456789 10</code>`
```

**Step 8: Проверить компиляцию**

Run: `cd /home/krivonosov/projects/vpn_bot && go build ./...`
Expected: PASS

---

### Task 4: Обновить отображение трафика — только использованный за месяц

**Files:**
- Modify: `internal/bot/messages.go:21-32` (MsgAccountCreated)
- Modify: `internal/bot/messages.go:175-215` (FormatUserStatus)

**Step 1: Убрать упоминание лимита из MsgAccountCreated**

```go
// Было:
MsgAccountCreated = `<b>✅ Аккаунт создан!</b>

Добро пожаловать! Ваш VPN-доступ активирован.

<b>Лимит трафика:</b> 30 GB / месяц
<b>Сброс трафика:</b> 1-го числа каждого месяца

<b>Ссылка для подключения:</b>
<code>%s</code>

Скопируйте ссылку и вставьте в приложение.
Нажмите "📚 Инструкции" для настройки.`

// Стало:
MsgAccountCreated = `<b>✅ Аккаунт создан!</b>

Добро пожаловать! Ваш VPN-доступ активирован.

<b>Ссылка для подключения:</b>
<code>%s</code>

Скопируйте ссылку и вставьте в приложение.
Нажмите "📚 Инструкции" для настройки.`
```

**Step 2: Переписать отображение трафика в FormatUserStatus — только использованный**

```go
// Было:
msg += fmt.Sprintf("<b>Статус:</b> %s %s\n", statusEmoji, statusText)

// Трафик
if user.UserTraffic != nil {
	usedGB := float64(user.UserTraffic.UsedTrafficBytes) / (1024 * 1024 * 1024)
	limitGB := float64(user.TrafficLimitBytes) / (1024 * 1024 * 1024)
	percent := float64(user.UserTraffic.UsedTrafficBytes) / float64(user.TrafficLimitBytes) * 100

	msg += fmt.Sprintf("\n<b>Трафик:</b>\n")
	msg += fmt.Sprintf("%.2f GB / %.0f GB (%.0f%%)\n", usedGB, limitGB, percent)
}

msg += "\n<b>Сброс трафика:</b> 1-го числа месяца\n"

// Стало:
msg += fmt.Sprintf("<b>Статус:</b> %s %s\n", statusEmoji, statusText)

// Использованный трафик за текущий месяц
if user.UserTraffic != nil {
	usedGB := float64(user.UserTraffic.UsedTrafficBytes) / (1024 * 1024 * 1024)
	msg += fmt.Sprintf("\n<b>Трафик за месяц:</b> %.2f GB\n", usedGB)
}
```

**Step 3: Проверить компиляцию**

Run: `cd /home/krivonosov/projects/vpn_bot && go build ./...`
Expected: PASS

---

### Task 5: Проверить чистоту и финальная верификация

**Step 1: Проверить неиспользуемые импорты**

После удаления `processAddTraffic` из admin.go, проверить что все импорты используются (Go компилятор сам подскажет). Удалить неиспользуемые.

**Step 2: Запустить `make fmt`**

Run: `make fmt`
Expected: PASS

**Step 3: Добавить тест на FormatUserStatus с TrafficLimitBytes=0**

Edge case: при `TrafficLimitBytes=0` (unlimited) старый код делал деление на ноль в вычислении процентов. Добавить тест, который вызывает `FormatUserStatus` с `TrafficLimitBytes=0` и проверяет, что паники нет и трафик отображается корректно ("Трафик за месяц: X.XX GB" без процентов и лимита).

**Step 4: Запустить `make tests`**

Run: `make tests`
Expected: PASS

**Step 5: Обновить CLAUDE.md**

Убрать из CLAUDE.md упоминания:
- `internal/bot/scheduler.go` из списка компонентов
- «Scheduler проверяет 1-го числа месяца — сбрасывает увеличенные лимиты к базовым 30 GB»
- «Трафик — базовый лимит 30 GB/месяц, сбрасывается автоматически Remnawave»
- «Добавление трафика — увеличивает текущий лимит (например, 30 → 40 GB), не затрагивает использованный трафик»

Обновить:
- Добавить: «Трафик — без лимита (trafficLimitBytes=0), Remnawave автоматически сбрасывает счётчик 1-го числа (стратегия MONTH)»

---

## Итого: что удаляется

| Что | Где | Действие |
|-----|-----|----------|
| `TrafficLimit30GB` | `client.go:14` | Удалить |
| `TrafficLimitBytes` поле | `UpdateUserRequest` | Удалить |
| `UpdateUserTraffic()` | `client.go:213-227` | Удалить метод |
| `scheduler.go` | весь файл | Удалить |
| `StartScheduler()` вызов | `main.go` | Удалить строку |
| `handleAddTrafficRequest()` | `admin.go:67-78` | Удалить |
| `processAddTraffic()` | `admin.go:80-127` | Удалить |
| `StateWaitAddTraffic` | `handlers.go:23` | Удалить |
| case `StateWaitAddTraffic` | `handlers.go:220-227` | Удалить |
| case `BtnAdminAddTraffic` | `handlers.go:287-288` | Удалить |
| `BtnAdminAddTraffic` | `keyboards.go:34` | Удалить |
| Кнопка в `AdminManageKeyboard` | `keyboards.go:91` | Перестроить |
| `MsgEnterAddTraffic` | `messages.go:145-150` | Удалить |
| Лимит в `MsgAccountCreated` | `messages.go:25-26` | Удалить строки |
| Лимит+% в `FormatUserStatus` | `messages.go:198-208` | Заменить на «Трафик за месяц: X.XX GB» |

## Что остаётся

- `TrafficStrategyMonth` — используется при создании пользователя (Remnawave сбрасывает счётчик)
- `TrafficLimitBytes` в структуре `User` — приходит от API
- `TrafficLimitBytes` в `CreateUserRequest` — отправляем 0 (unlimited)
- `Traffic` структура и `UserTraffic` — для отображения использованного трафика
- `StatusLimited` — статус от Remnawave, отображается корректно
- `ResetUserTraffic()` — может понадобиться для админских задач

---

## Обнаруженные проблемы (Code Review 2026-03-03)

При ревью реализации обнаружены проблемы, не связанные напрямую с удалением лимитов, но выявленные в ходе аудита затронутых файлов.

### 🔴 Критичные

**1. Race condition в `userStates`** (`handlers.go:31`)
- `userStates` — обычная `map[int64]string` без защиты от конкурентного доступа
- Telebot обрабатывает сообщения конкурентно (каждое в горутине)
- При одновременных сообщениях от разных пользователей — data race → паника

**2. TOCTOU на инвайт-код** (`handlers.go:273-309`)
- Между `GetInviteByCode` и `UseInvite` — окно для race condition
- Два пользователя с одним кодом → оба пройдут проверку → «осиротевший» аккаунт в Remnawave

### 🟡 Средние

**3. `GetAllUsers` ограничен 1000** (`client.go:191`) — нет пагинации, нет предупреждения при превышении
**4. Мёртвый параметр `activeOnly`** (`admin.go:169`) — `processBroadcastMessage` игнорирует параметр
**5. Нет защиты от само-бана** (`admin.go:82`) — админ может забанить самого себя
**6. Переполнение Telegram-сообщения** (`admin.go:251`) — нет разбиения при большом числе инвайтов

### 🟢 Незначительные

**7. HTTP-запросы без `context.Context`** (`client.go:280`)
**8. Утечка памяти в `userStates`** — заброшенные сессии хранятся вечно

> **План фиксов:** `docs/plans/2026-03-03-concurrency-fixes-design.md`
