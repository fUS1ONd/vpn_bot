# VPN Bot - Архитектура и проектирование

## Общая архитектура

VPN Bot состоит из нескольких независимых компонентов, которые взаимодействуют через единую БД и API панелей 3X-UI:

```
┌─────────────────────────────────────────────────────────────┐
│                     Telegram Users                           │
└────────────┬────────────────────────────────┬────────────────┘
             │                                │
             ▼                                ▼
      ┌──────────────────┐          ┌─────────────────┐
      │  Telegram Bot    │          │  HTTP Sub       │
      │  (polling)       │          │  Server :8000   │
      │                  │          │                 │
      │ - handlers       │          │ - /sub/{uuid}   │
      │ - keyboards      │          │ - base64 config │
      │ - admin panel    │          └────────┬────────┘
      │ - scheduler      │                   │
      └────────┬─────────┘                   │
               │                             │
               └─────────────┬───────────────┘
                             │
                    ┌────────▼─────────┐
                    │  SQLite Database │
                    │                  │
                    │ - users          │
                    │ - payments       │
                    │ - promo_codes    │
                    │ - promo_uses     │
                    └────────┬─────────┘
                             │
             ┌───────────────┼───────────────┐
             │               │               │
             ▼               ▼               ▼
        ┌─────────┐    ┌──────────┐    ┌──────────┐
        │ Server  │    │  Server  │    │ Server   │
        │   A     │    │    B     │    │    C     │
        │         │    │          │    │          │
        │3X-UI    │    │ 3X-UI    │    │ 3X-UI    │
        │(Россия) │    │(Европа)  │    │(Европа2) │
        │Каскад   │    │Прямой    │    │Прямой    │
        │Limit:  │    │ Limit:   │    │ Limit:   │
        │ 30GB    │    │Безлимит  │    │Безлимит  │
        └─────────┘    └──────────┘    └──────────┘
```

## Компоненты системы

### 1. Telegram Bot (`internal/bot/`)

**Назначение:** Основной интерфейс для пользователей

**Файлы:**
- `handlers.go` - основные обработчики команд и сообщений
- `keyboards.go` - определение кнопок и меню
- `messages.go` - текстовые сообщения и форматирование
- `admin.go` - функции администратора
- `scheduler.go` - периодические задачи (проверка активности, ренью подписок)

**Функциональность:**
- Обработка команды `/start`
- Главное меню с 7 основными функциями
- Статус подписки с отображением трафика
- Управление промокодами
- Админ-функции (создание/удаление клиентов)
- Рассылка сообщений

**State Machine:**
Используется in-memory словарь `userStates` для отслеживания диалога:
```go
type UserState int

const (
    StateNone UserState = iota
    StateWaitPromo
    StateWaitClient
    StateWaitClientDelete
    StateWaitPromoAdd
    StateWaitPromoDel
    StateWaitBroadcast
)
```

### 2. База данных (`internal/database/`)

**Назначение:** Хранение всех данных о пользователях, подписках и платежах

**Файлы:**
- `db.go` - инициализация БД, миграции
- `users.go` - операции с пользователями
- `payments.go` - история платежей
- `promo.go` - управление промокодами

**Таблицы:**

#### users
```sql
CREATE TABLE users (
    id INTEGER PRIMARY KEY,
    uuid TEXT UNIQUE,
    telegram_id INTEGER UNIQUE,  -- может быть NULL
    expiry_time INTEGER,          -- Unix timestamp в миллисекундах
    trial_used INTEGER DEFAULT 0,
    ru_extra_traffic INTEGER DEFAULT 0,  -- бонусный трафик для Server A
    created_at TEXT,
    updated_at TEXT
)
```

**Ключевые моменты:**
- `uuid` - уникальный идентификатор пользователя, используется для всех трёх серверов
- `telegram_id` - UNIQUE но может быть NULL (админ может создавать клиентов без Telegram)
- `trial_used` - флаг, предотвращающий повторную активацию пробного периода
- `ru_extra_traffic` - бонусный трафик из промокодов (складывается с SERVER_A_LIMIT_BYTES)

#### payments
```sql
CREATE TABLE payments (
    id INTEGER PRIMARY KEY,
    user_id INTEGER,
    amount REAL,
    payment_method TEXT,
    description TEXT,
    created_at TEXT
)
```

#### promo_codes
```sql
CREATE TABLE promo_codes (
    id INTEGER PRIMARY KEY,
    code TEXT UNIQUE,
    type TEXT,  -- free_days, extra_traffic, discount, unlimited
    value TEXT,  -- значение в зависимости от типа
    max_uses INTEGER DEFAULT 0,  -- 0 = неограниченно
    used_count INTEGER DEFAULT 0,
    created_at TEXT,
    expires_at TEXT
)
```

#### promo_uses
```sql
CREATE TABLE promo_uses (
    id INTEGER PRIMARY KEY,
    user_id INTEGER,
    promo_id INTEGER,
    used_at TEXT
)
```

**Ограничения:**
- Каждый пользователь может использовать один промокод только один раз
- `promo_uses` предотвращает повторное использование

### 3. 3X-UI клиент (`internal/threexui/`)

**Назначение:** HTTP клиент для управления VPN клиентами на 3X-UI панелях

**Файлы:**
- `client.go` - основная логика клиента
- `interface.go` - интерфейсы для mock'ирования (для тестов)
- `mock.go` - mock реализация для тестирования
- `mock_test.go` - тесты mock'а

**Функциональность:**
- Cookie-based авторизация (сессии 3X-UI)
- Создание/обновление/удаление VPN клиентов
- Получение статуса и трафика
- Кэширование сессий

**API операции:**

```go
// Авторизация
func (c *Client) Login() error

// Создание клиента
func (c *Client) AddClientWithSettings(
    uuid string,
    maxIPs int,
    expiryTime int64,
    totalGB int64,
) error

// Обновление клиента
func (c *Client) UpdateClient(
    clientID string,
    expiryTime int64,
    totalGB int64,
) error

// Получение статуса
func (c *Client) GetClientStatus(clientID string) (*ClientStatus, error)

// Удаление клиента
func (c *Client) DeleteClient(clientID string) error
```

**Важные детали:**
- **InsecureSkipVerify:** пропускает проверку TLS сертификатов (самоподписанные на панелях)
- **Cookie Jar:** используется для хранения сессий
- **ExpiryTime:** в 3X-UI хранится в миллисекундах Unix timestamp
- **TotalGB:** в байтах, 0 = безлимит
- **MaxIPs:** максимальное количество одновременных подключений (обычно 2-3)

### 4. HTTP сервер подписок (`internal/subscription/`)

**Назначение:** Отдавать конфигурации VPN клиентам по HTTP

**Файлы:**
- `server.go` - основная логика сервера

**API:**
```
GET /sub/{uuid}
```

**Ответ:**
- Base64 кодированная конфигурация с двумя VLESS ссылками
- Заголовок `Subscription-Userinfo` с информацией о трафике

**Пример заголовка:**
```
Subscription-Userinfo: upload=1073741824;download=2147483648;total=3221225472;expire=1735689600
```

Значения в байтах.

### 5. VLESS генератор (`internal/vless/`)

**Назначение:** Генерация VLESS конфигурационных строк

**Файлы:**
- `generator.go` - логика генерации

**Формат VLESS:**
```
vless://uuid@server:port?type=http&encryption=none&flow=xtls-rprx-vision&sni=sni-value&sid=short-id#Name
```

## Поток данных

### Создание нового пользователя

```
User /start
   ↓
Bot handlers.go
   ↓
Проверка: есть ли в БД?
   ├─ Нет → Создать в БД с trial флагом
   └─ Да → Вернуть существующие данные
   ↓
Создание клиентов на трёх серверах
   ├─ threexui.AddClientWithSettings(Server A)
   ├─ threexui.AddClientWithSettings(Server B)
   └─ threexui.AddClientWithSettings(Server C)
   ↓
Сохранение в БД
   ↓
Отправка ссылки подписки пользователю
   ↓
VPN клиент получает конфиг со всех трёх серверов
```

### Получение подписки

```
VPN Client
   ↓
GET /sub/{uuid}
   ↓
subscription/server.go
   ↓
Получение данных пользователя из БД
   ↓
Получение статуса на Server A (трафик, лимит)
   ↓
Получение статуса на Server B
   ↓
Получение статуса на Server C
   ↓
Генерация двух VLESS ссылок
   ↓
Base64 кодирование конфига
   ↓
Добавление заголовка Subscription-Userinfo
   ↓
Отправка клиенту
```

### Использование промокода

```
User: /promo
   ↓
Bot запрашивает код
   ↓
User отправляет код
   ↓
handlers.go applyPromoCode()
   ↓
Проверка в БД:
   ├─ Существует ли?
   ├─ Не истек ли?
   ├─ Не превышен ли лимит использований?
   └─ Не использовал ли уже этот пользователь?
   ↓
Применение в зависимости от типа:
   ├─ free_days → обновить expiry_time
   ├─ extra_traffic → добавить к ru_extra_traffic
   ├─ discount → сохранить для использования при платеже
   └─ unlimited → установить безлимитный доступ на месяц
   ↓
Сохранить использование в promo_uses
   ↓
Обновить клиентов на серверах (если нужно)
```

## Жизненный цикл подписки

### Пробный период (Trial)

```
Новый пользователь
   ↓
Автоматическая активация:
   ├─ Expiry: now + 3 дня
   ├─ Server A трафик: 1 GB
   └─ Server B, C: безлимит
   ↓
trial_used = 1 (предотвращает повтор)
```

### Обновление подписки

```
User оплачивает
   ↓
Обновление в БД:
   ├─ expiry_time += дни
   └─ Сброс трафика (в зависимости от типа подписки)
   ↓
Обновление на всех серверах:
   ├─ threexui.UpdateClient(Server A, new_expiry, new_traffic)
   ├─ threexui.UpdateClient(Server B, new_expiry, 0)
   └─ threexui.UpdateClient(Server C, new_expiry, 0)
   ↓
Отправка подтверждения пользователю
```

### Истечение подписки

```
Scheduler (каждый час)
   ↓
Проверка всех пользователей:
   ├─ expiry_time < now?
   │  ├─ Да → удалить клиентов со всех серверов
   │  └─ Нет → пропустить
   ↓
Удаление с серверов:
   ├─ threexui.DeleteClient(Server A)
   ├─ threexui.DeleteClient(Server B)
   └─ threexui.DeleteClient(Server C)
   ↓
Уведомление пользователю (опционально)
```

## Типы промокодов

### free_days
- Добавляет дни к существующей подписке
- Пример: промокод `DAYS7` добавит 7 дней
- Требует активную подписку

### extra_traffic
- Добавляет трафик к Server A
- Хранится в `ru_extra_traffic`
- Пример: промокод `GB10` добавит 10GB

### discount
- Процент скидки на оплату
- Пример: промокод `SALE10` даст 10% скидку

### unlimited
- Предоставляет безлимитный доступ на определённый период
- Сбрасывает трафик ежемесячно
- Пример: промокод `UNLIMITED_3` даст 3 месяца безлимита

## Три сервера

**Зачем три сервера?**

1. **Резервирование:** Если один сервер недоступен, пользователь может использовать другой
2. **Распределение нагрузки:** Трафик распределяется между серверами
3. **Гибкость:** Разные лимиты и конфигурации для разных целей

**Server A (Каскад/Россия):**
- Основной сервер
- Обычно с лимитом трафика (например, 30GB)
- Используется для основного подключения

**Server B (Прямой/Европа):**
- Резервный сервер
- Обычно безлимитный
- Используется как fallback

**Server C (Дополнительный):**
- Третий сервер
- Конфигурация по усмотрению администратора
- Может быть безлимитным или с лимитом

**Создание пользователя:**
```
1 UUID → 3 клиента
   ├─ Server A (UUID как client ID)
   ├─ Server B (UUID как client ID)
   └─ Server C (UUID как client ID)
```

Все три клиента используют один и тот же UUID для синхронизации и идентификации.

## Безопасность

### Данные в движении
- Все запросы к 3X-UI через HTTPS (с пропуском проверки сертификата)
- HTTP сервер подписок может быть защищён nginx с SSL

### Данные в хранилище
- SQLite БД хранится в Docker volume
- .env файл не коммитится в git
- Чувствительные данные хранятся в GitHub Secrets

### Аутентификация
- Telegram ID для идентификации пользователя
- Cookie-based сессии для 3X-UI
- Админ-функции защищены проверкой ADMIN_ID

### Ограничения
- MaxIPs = 2-3 (предотвращает шаринг аккаунта)
- Трафик отслеживается на уровне 3X-UI
- Просроченные подписки удаляются автоматически

## Возможные улучшения

1. **Масштабируемость:**
   - Переход на PostgreSQL для большого количества пользователей
   - Кэширование статусов с Redis

2. **Функциональность:**
   - Поддержка других протоколов (Shadowsocks, trojan)
   - Веб-панель для администраторов
   - Мобильное приложение

3. **Надежность:**
   - Retry logic для операций с 3X-UI
   - Health checks серверов
   - Автоматическое восстановление после сбоев

4. **Мониторинг:**
   - Метрики использования (Prometheus)
   - Логирование в ELK stack
   - Оповещения при сбоях

## Тестирование

Проект содержит unit тесты:
- `internal/database/db_test.go` - тесты БД (используют :memory: SQLite)
- `internal/threexui/mock_test.go` - тесты mock клиента

**Запуск тестов:**
```bash
go test ./...
go test -v ./internal/database
go test -cover ./...
```

## Развертывание

Проект использует Docker с multi-stage build для минимизации размера образа:

```dockerfile
Stage 1: Builder (golang:1.25-alpine)
   ├─ Установка зависимостей (gcc, musl-dev)
   ├─ Загрузка Go модулей
   └─ Компиляция статического бинарника

Stage 2: Runtime (alpine:latest)
   ├─ Копирование только бинарника
   ├─ Установка ca-certificates
   └─ Образ размером ~30MB
```

**Результат:** Один контейнер ~30MB, вместо Python версии ~300MB.
