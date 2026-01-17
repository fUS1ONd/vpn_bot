# Прогресс миграции на Remnawave

**Дата начала:** 2026-01-17
**Ветка:** v2
**План:** `docs/plans/2026-01-17-remnawave-migration-design.md`

---

## Выполненные задачи

### Batch 1 (Фундамент) — ЗАВЕРШЁН

| # | Задача | Коммит | Статус |
|---|--------|--------|--------|
| 1 | Обновить конфигурацию | `0bc4056` | ✅ |
| 2 | Создать клиент Remnawave API | `907a218` | ✅ |
| 3 | Создать новую схему БД | `35d04a9` | ✅ |

---

## Следующие задачи

| # | Задача | Статус |
|---|--------|--------|
| 4 | Удалить internal/threexui | ⏳ |
| 5 | Удалить internal/vless | ⏳ |
| 6 | Удалить internal/subscription | ⏳ |
| 7 | Создать мигратор (cmd/migrator) | ⏳ |
| 8 | Обновить main.go | ⏳ |
| 9 | Обновить хендлеры — система инвайтов | ⏳ |
| 10 | Обновить хендлеры — кнопки пользователя | ⏳ |
| 11 | Обновить админ-хендлеры | ⏳ |
| 12 | Добавить scheduler для сброса лимитов | ⏳ |

---

## Важная информация для продолжения

### Новая структура конфига (`internal/config/config.go`)

```go
type Config struct {
    BotToken           string
    AdminID            int64
    RemnawaveURL       string
    RemnawaveAPIToken  string
    RemnawaveSquadUUID string // Опционально
    DBPath             string
    DonateText         string
}
```

**Переменные .env:**
- `BOT_TOKEN`, `ADMIN_ID` — как раньше
- `REMNAWAVE_URL` — URL панели Remnawave
- `REMNAWAVE_API_TOKEN` — JWT токен
- `REMNAWAVE_DEFAULT_SQUAD_UUID` — опционально, UUID сквада
- `DB_PATH` — путь к БД (по умолчанию `/app/data/bot.db`)
- `DONATE_TEXT` — текст для кнопки доната

### Новая структура БД (`bot.db`)

**Таблица `users`:**
- `telegram_id` INTEGER PRIMARY KEY
- `username` TEXT
- `remnawave_uuid` TEXT UNIQUE NOT NULL
- `created_at` TIMESTAMP

**Таблица `invites`:**
- `code` TEXT PRIMARY KEY
- `created_by` INTEGER NOT NULL
- `used_by` INTEGER (nullable)
- `created_at` TIMESTAMP

### Клиент Remnawave (`internal/remnawave/client.go`)

**Константы:**
- `TrafficLimit30GB = 32212254720` (30 ГБ в байтах)
- `TrafficStrategyMonth = "MONTH"`
- Статусы: `StatusActive`, `StatusDisabled`, `StatusLimited`, `StatusExpired`

**Методы:**
- `CreateUser(telegramID, username)` → `*User, error`
- `GetUser(uuid)` → `*User, error`
- `GetUserByTelegramID(telegramID)` → `*User, error`
- `GetAllUsers()` → `[]User, error`
- `UpdateUserTraffic(uuid, bytes)` → `error`
- `ResetUserTraffic(uuid)` → `error`
- `DeleteUser(uuid)` → `error`

### Файлы для удаления (Batch 2)

- `internal/threexui/` — весь каталог
- `internal/vless/` — весь каталог
- `internal/subscription/` — весь каталог
- `internal/database/payments.go` — удалить
- `internal/database/promo.go` — удалить
- `internal/database/db_test.go` — переписать или удалить

### Ошибки компиляции

Текущие ошибки — ожидаемы, старый код ссылается на удалённые модули:
- `handlers.go` — ссылки на `ServerA/B/C`, `StatusActive`, `StatusTrial`
- `admin.go` — ссылки на `CreateUser` с 4 аргументами, `UpdateUserSubscription`
- `scheduler.go` — ссылки на `ServerA`
- `main.go` — ссылки на `ServerA/B/C`, `SubPort`
- `server.go` — ссылки на `ServerA/B/C`
- `generator.go` — ссылки на `config.ServerConfig`
- `client.go` (threexui) — ссылки на `config.ServerConfig`

Все эти ошибки исправятся при удалении старых модулей и обновлении хендлеров.

### Особенности реализации

1. **Scheduler для сброса лимитов** — нужен только для возврата увеличенных лимитов к базовым 30 GB. Remnawave сам сбрасывает `usedTrafficBytes` при стратегии `MONTH`.

2. **Сквады** — опциональны. Перед миграцией нужно протестировать создание пользователя без сквада. Если не работает — создать сквад в панели и добавить UUID в конфиг.

3. **Рассылка** — фильтровать по `status=ACTIVE` из Remnawave API (`GetAllUsers()`).

4. **UUID** — генерируются Remnawave (новые), старые не переиспользуются.

---

## Коммиты

```
35d04a9 refactor: новая схема БД (users, invites)
907a218 feat: добавить клиент Remnawave API
0bc4056 refactor: заменить конфиг 3X-UI на Remnawave
```
