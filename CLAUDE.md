# CLAUDE.md

This file provides guidance to Claude Code (claude.ai/code) when working with code in this repository.

## Проект

VPN Bot - Telegram бот для управления VPN подписками через 3X-UI панели. Написан на Go 1.25 для максимальной производительности. Поддерживает два сервера: Server A (Россия, с лимитом трафика) и Server B (Европа, безлимитный).

## Команды разработки

### Основные команды (через Makefile)

```bash
make up              # Запустить бота в Docker
make down            # Остановить бота
make restart         # Перезапустить бота
make logs            # Показать логи
make rebuild         # Пересобрать и перезапустить
make status          # Статус контейнеров
make shell           # Войти в контейнер бота
```

### База данных

```bash
make db-ui           # Запустить SQLite Web UI на http://localhost:8080
make db-ui-stop      # Остановить SQLite Web UI
make backup          # Создать бэкап БД в backups/
make volume-info     # Информация о Docker volume
make clean           # ОСТОРОЖНО: удаляет все данные и контейнеры
```

### Локальная разработка (без Docker)

```bash
go mod download      # Установить зависимости
go run cmd/bot/main.go  # Запустить бота локально
go build -o vpn-bot cmd/bot/main.go  # Собрать бинарник
go test ./...        # Запустить все тесты
```

### Docker команды

```bash
docker compose up -d --build           # Собрать и запустить
docker compose logs -f vpn-bot         # Логи бота
docker compose --profile tools up -d   # Запустить с SQLite Web UI
```

## Архитектура

### Основная структура

```
cmd/bot/main.go         - Точка входа, инициализация компонентов
internal/
  bot/                  - Telegram бот (handlers, keyboards, messages, admin)
  config/               - Конфигурация из .env файлов
  database/             - SQLite ORM (users, payments, promo codes)
  threexui/             - HTTP клиент для 3X-UI API
  vless/                - Генерация VLESS ссылок
  subscription/         - HTTP сервер для подписок
```

### Поток данных

1. **Telegram Bot** (`internal/bot`) - принимает команды пользователей
2. **Database** (`internal/database`) - хранит пользователей, подписки, промокоды
3. **3X-UI Client** (`internal/threexui`) - управляет VPN клиентами на панелях
4. **Subscription Server** (`internal/subscription`) - отдает VLESS конфиги по HTTP

### Два сервера

- **Server A** (Россия/Каскад): лимитированный трафик, VLESS+XTLS-Vision
- **Server B** (Европа/Прямой): безлимитный трафик, VLESS+XTLS-Vision

Оба сервера создаются одновременно для каждого пользователя с одним UUID.

### Состояние пользователя

Бот использует in-memory карту `userStates` для отслеживания состояния разговора:
- `StateWaitPromo` - ожидает ввод промокода
- `StateWaitClient` - админ создает клиента
- `StateWaitClientDelete` - админ удаляет клиента
- `StateWaitPromoAdd` - админ добавляет промокод
- `StateWaitPromoDel` - админ удаляет промокод

### База данных (SQLite)

**Таблицы:**
- `users` - пользователи (telegram_id может быть NULL для админских клиентов)
- `payments` - история платежей
- `promo_codes` - промокоды (типы: free_days, extra_traffic, discount)
- `promo_uses` - использование промокодов

**Важно:** `telegram_id` - UNIQUE, но может быть NULL. Это позволяет админу создавать клиентов без привязки к Telegram.

### 3X-UI API интеграция

Клиент использует cookie-based авторизацию:
1. `Login()` - авторизация и сохранение cookie
2. `AddClientWithSettings()` - создание клиента
3. `UpdateClient()` - обновление настроек
4. `GetClientStatus()` - получение статуса и трафика
5. `DeleteClient()` - удаление клиента

**ВАЖНО:** Сессии истекают, поэтому перед каждой операцией нужно вызывать `Login()`.

### HTTP Subscription Server

- Запускается на порту 8000 (SUB_PORT)
- Endpoint: `http://{SUBSCRIPTION_HOST}:8000/sub/{UUID}`
- Отдает base64 конфиг с двумя VLESS ссылками
- Добавляет заголовок `Subscription-Userinfo` с трафиком и лимитами

### Обработка сообщений

Роутинг в `handleTextMessage`:
1. Проверка состояния пользователя (state machine)
2. Проверка динамических кнопок (статус с иконками)
3. Админские кнопки (если `isAdmin()`)
4. Обычные кнопки меню
5. Fallback: показать главное меню

### Промокоды

Три типа промокодов:
- `free_days` - добавляет дни к подписке
- `extra_traffic` - добавляет трафик к Server A
- `discount` - скидка (применяется при оплате)

Промокод может быть использован несколько раз (`max_uses`), каждый пользователь может использовать промокод только один раз.

## Конфигурация (.env)

### Обязательные переменные

```
BOT_TOKEN                - Токен Telegram бота
ADMIN_ID                 - Telegram ID администратора
SUBSCRIPTION_HOST        - Домен/IP для ссылок подписок
SERVER_A_BASE_URL        - URL 3X-UI панели (Server A)
SERVER_A_WEB_PATH        - Web path для панели
SERVER_A_USERNAME        - Логин
SERVER_A_PASSWORD        - Пароль
SERVER_A_INBOUND_ID      - ID inbound'а
SERVER_A_LIMIT_BYTES     - Лимит трафика в байтах
SERVER_A_PUBLIC_KEY      - Public key для VLESS
SERVER_A_SNI             - SNI для TLS
SERVER_A_SID             - Short ID
```

То же самое для `SERVER_B_*`.

### Опциональные

```
SUB_PORT=8000           - Порт сервера подписок
DB_PATH=/app/data/users.db  - Путь к БД
```

## Паттерны разработки

### Логирование

Используется `log/slog`:
```go
slog.Info("message", "key", value)
slog.Error("error occurred", "error", err)
slog.Warn("warning", "data", data)
```

### Обработка ошибок

Всегда логировать ошибки и показывать пользовательские сообщения:
```go
if err != nil {
    slog.Error("Failed to do something", "error", err)
    return c.Send("Произошла ошибка. Попробуйте позже.")
}
```

### Работа с 3X-UI

Всегда логиниться перед операциями:
```go
if err := b.clientA.Login(); err != nil {
    slog.Error("Failed to login", "error", err)
    return c.Send("Ошибка подключения к серверу")
}
```

### Транзакции создания пользователя

При создании пользователя:
1. Создать на Server A
2. Создать на Server B (при ошибке - откатить Server A)
3. Сохранить в БД
4. Обновить статус подписки

### Тестирование

- Используется `testify` для ассертов
- Моки для 3X-UI клиента: `internal/threexui/mock.go`
- Тесты БД используют `:memory:` SQLite

```go
// Пример теста
func TestSomething(t *testing.T) {
    assert := assert.New(t)
    // ...
    assert.NoError(err)
    assert.Equal(expected, actual)
}
```

## Особенности реализации

### Trial система

- 3 дня бесплатно
- 1 ГБ трафика на Server A
- Безлимит на Server B
- Флаг `trial_used` предотвращает повторную активацию

### Дополнительный трафик

Поле `ru_extra_traffic` в БД хранит бонусный трафик (из промокодов). При отображении статуса складывается с `SERVER_A_LIMIT_BYTES`.

### UUID как ключ

Один UUID используется для:
- Идентификации пользователя в БД
- Клиента на Server A
- Клиента на Server B
- URL подписки

### Graceful Shutdown

Используется context с сигналами:
```go
ctx, cancel := context.WithCancel(context.Background())
signal.Notify(sigChan, os.Interrupt, syscall.SIGTERM)
```

## Deployment

### Продакшн

1. Настроить `.env` файл
2. Открыть порт 8000 в firewall
3. `make up` или `docker compose up -d --build`
4. Проверить логи: `make logs`

### Обновление

```bash
git pull
make rebuild
```

Данные сохраняются в volume `vpn_bot_vpn_data`.

## Важные нюансы

- **InsecureSkipVerify**: 3X-UI клиент пропускает проверку TLS сертификатов (самоподписанные)
- **Cookie Jar**: HTTP клиент использует cookie jar для сессий 3X-UI
- **VLESS Flow**: используется `xtls-rprx-vision`
- **ExpiryTime**: в 3X-UI хранится в миллисекундах Unix timestamp
- **TotalGB**: в байтах, 0 = безлимит
- **Telegram ID**: может быть 0/NULL для админских клиентов

## Частые задачи

### Добавить новую команду бота

1. Добавить константу кнопки в `internal/bot/keyboards.go`
2. Добавить обработчик в `internal/bot/handlers.go`
3. Добавить маршрут в `handleTextMessage()`

### Добавить новый тип промокода

1. Добавить константу в `internal/database/promo.go`
2. Обновить `applyPromoCode()` в `internal/bot/handlers.go`
3. Обновить `FormatPromoResult()` в `internal/bot/messages.go`

### Изменить лимиты trial

Константы находятся в `activateTrialNewUser()` и `activateTrial()`:
```go
expiryTime := time.Now().AddDate(0, 0, 3)  // 3 дня
trialTrafficBytes := int64(1 * 1024 * 1024 * 1024)  // 1 ГБ
```

### Добавить миграцию БД

Добавить SQL в массив `migrations` в `internal/database/db.go`:
```go
migrations := []string{
    // existing migrations
    `ALTER TABLE users ADD COLUMN new_field TEXT`,
}
```
