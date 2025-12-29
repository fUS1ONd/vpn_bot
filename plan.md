# План разработки VPN-бота с автоматизацией подписок

## Обзор проекта

**Цель:** Превратить админский бот в публичный сервис с автоматической оплатой, управлением подписками и защитой от шаринга ключей.

**Текущее состояние:**
- Telegram бот на Go (только для админа)
- 2 сервера 3X-UI: RU (30GB) + EU (безлимит)
- Простая БД: `users (email, uuid)`
- HTTP сервер подписок на :8000

**Требования:**
- Оплата: Robokassa
- Тарифы: 200₽/мес + 100₽/10GB доп. трафик
- Триал: 3 дня + 1GB бесплатно
- Антишаринг: лимит 2-3 соединения
- При исчерпании RU лимита → блокируется только RU, EU работает до конца подписки
- Промокоды для скидок/бонусов

**Ключевое упрощение:**
- 3X-UI сам блокирует клиента при превышении лимита трафика (totalGB)
- 3X-UI сам блокирует клиента по истечении времени (expiryTime)
- Наша задача: синхронизировать статусы с БД + уведомлять пользователей

---

## Этап 1: Рефакторинг базы данных

### Файлы для изменения:
- `internal/database/db.go` — новая схема
- `internal/database/users.go` — (новый) CRUD пользователей
- `internal/database/payments.go` — (новый) CRUD платежей

### Новая схема БД:

```sql
-- Расширенная таблица пользователей
CREATE TABLE users (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    telegram_id INTEGER UNIQUE NOT NULL,
    email TEXT UNIQUE NOT NULL,
    uuid TEXT UNIQUE NOT NULL,
    created_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP,

    -- Статус подписки (синхронизируется с 3X-UI)
    subscription_status TEXT DEFAULT 'none', -- none, trial, active, expired
    subscription_end_at TIMESTAMP,           -- дублируем expiryTime из панели

    trial_used BOOLEAN DEFAULT FALSE,

    -- Дополнительный трафик RU (докупленный)
    ru_extra_traffic INTEGER DEFAULT 0       -- в байтах
);

-- Платежи
CREATE TABLE payments (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    user_id INTEGER NOT NULL,
    payment_id TEXT UNIQUE NOT NULL,
    provider TEXT NOT NULL,        -- robokassa
    amount INTEGER NOT NULL,       -- рубли
    status TEXT DEFAULT 'pending', -- pending, succeeded, failed
    type TEXT NOT NULL,            -- monthly, traffic_10gb, promo
    promo_code TEXT,               -- использованный промокод (если есть)
    created_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP,
    confirmed_at TIMESTAMP,
    FOREIGN KEY (user_id) REFERENCES users(id)
);

-- Промокоды
CREATE TABLE promo_codes (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    code TEXT UNIQUE NOT NULL,
    type TEXT NOT NULL,            -- discount, free_days, extra_traffic
    value INTEGER NOT NULL,        -- скидка %, дни, или байты
    max_uses INTEGER DEFAULT 1,    -- сколько раз можно использовать
    used_count INTEGER DEFAULT 0,
    valid_until TIMESTAMP,         -- срок действия
    created_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP
);

-- Использование промокодов (кто какой использовал)
CREATE TABLE promo_uses (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    user_id INTEGER NOT NULL,
    promo_id INTEGER NOT NULL,
    used_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP,
    FOREIGN KEY (user_id) REFERENCES users(id),
    FOREIGN KEY (promo_id) REFERENCES promo_codes(id),
    UNIQUE(user_id, promo_id)      -- один юзер = один промокод один раз
);
```

**Логика лимитов RU:**
- Базовый лимит 30GB задается в панели 3X-UI (totalGB)
- При докупке трафика: обновляем totalGB в панели на (30GB + ru_extra_traffic)
- Панель сама блокирует клиента при превышении

### Задачи:
- [ ] Создать миграцию для новой схемы
- [ ] Реализовать методы CRUD для users
- [ ] Реализовать методы CRUD для payments
- [ ] Реализовать методы CRUD для promo_codes
- [ ] Добавить индексы для быстрого поиска

---

## Этап 2: Публичный интерфейс бота

### Файлы для изменения:
- `internal/bot/handlers.go` — переработать под публичный доступ
- `internal/bot/keyboards.go` — (новый) inline-кнопки
- `internal/bot/messages.go` — (новый) тексты сообщений
- `internal/bot/admin.go` — (новый) вынести админ-команды

### UX для пользователя:

**Главное меню (`/start`):**
```
🚀 VPN-бот

[ Подключить VPN ]  — триал или оплата
[ Мой статус ]      — подписка, трафик
[ Инструкции ]      — iOS/Android/Windows
[ Докупить трафик ] — +10GB за 100₽ (RU сервер)
[ Промокод ]        — ввести промокод
[ Поддержка ]       — связь с админом
```

**Сценарий нового пользователя:**
1. `/start` → приветствие
2. "Подключить VPN" → проверка trial_used
3. Если триал не использован → активация (3 дня, 1GB на RU)
4. Показать ссылку подписки + инструкции

**Сценарий оплаты:**
1. "Оплатить подписку" → генерация ссылки Robokassa
2. Пользователь оплачивает
3. Webhook → активация на месяц
4. Уведомление в бот

**Сценарий промокода:**
1. "Промокод" → бот просит ввести код
2. Пользователь вводит код
3. Проверка: существует, не истек, не использован этим юзером, есть лимит
4. Применение: скидка/доп.дни/доп.трафик

### Задачи:
- [ ] Создать inline-клавиатуры (keyboards.go)
- [ ] Написать тексты сообщений (messages.go)
- [ ] Обработчик /start для всех пользователей
- [ ] Обработчик callback-кнопок
- [ ] Функция активации триала
- [ ] Функция показа статуса
- [ ] Инструкции по настройке (iOS, Android, Windows)
- [ ] Обработчик промокодов
- [ ] Вынести админ-команды в отдельный файл
- [ ] Админ-команды для управления промокодами

---

## Этап 3: Интеграция платежей (Robokassa)

### Файлы для создания:
- `internal/payment/robokassa.go` — клиент Robokassa API
- `internal/payment/service.go` — общий сервис оплат

### Файлы для изменения:
- `internal/subscription/server.go` — добавить webhook endpoints
- `internal/config/config.go` — добавить конфиг платежей

### API endpoints:
```
POST /webhook/robokassa — Result URL (уведомление об оплате)
GET  /payment/success   — Success URL (редирект после оплаты)
GET  /payment/fail      — Fail URL (редирект при отмене)
```

### Robokassa API:
**Генерация ссылки оплаты:**
```
https://auth.robokassa.ru/Merchant/Index.aspx?
  MerchantLogin=xxx&
  OutSum=200.00&
  InvId=123&
  Description=VPN подписка&
  SignatureValue=MD5(MerchantLogin:OutSum:InvId:Password1)&
  IsTest=1
```

**Result URL (webhook):**
Robokassa шлет POST с параметрами: `OutSum`, `InvId`, `SignatureValue`
Проверка: `MD5(OutSum:InvId:Password2)` должен совпадать с SignatureValue

### Логика оплаты:
1. Пользователь нажимает "Оплатить" в боте
2. Создаем запись в payments (status=pending)
3. Генерируем ссылку Robokassa с InvId=payment.id
4. Отправляем ссылку пользователю
5. Robokassa шлет Result URL → проверяем подпись
6. Активируем подписку / добавляем трафик
7. Отвечаем "OK{InvId}" (обязательно для Robokassa)

### Задачи:
- [ ] Реализовать генерацию ссылки оплаты Robokassa
- [ ] Реализовать Result URL handler (webhook)
- [ ] Реализовать Success/Fail URL handlers
- [ ] Проверка подписи MD5
- [ ] Логика активации после оплаты
- [ ] Добавить конфиг переменных

### Настройка Webhook URL:

**Вариант 1: Публичный IP сервера**
```
Result URL: http://YOUR_SERVER_IP:8000/webhook/robokassa
Success URL: http://YOUR_SERVER_IP:8000/payment/success
Fail URL: http://YOUR_SERVER_IP:8000/payment/fail
```

**Вариант 2: Cloudflare Tunnel (бесплатно)**
```bash
# Установка
curl -L https://github.com/cloudflare/cloudflared/releases/latest/download/cloudflared-linux-amd64 -o cloudflared
chmod +x cloudflared

# Запуск туннеля
./cloudflared tunnel --url http://localhost:8000
# Получишь URL типа https://xxx-yyy-zzz.trycloudflare.com
```

**Вариант 3: Nginx reverse proxy + Let's Encrypt**
Настроить домен с SSL на сервере.

---

## Этап 4: Расширение 3X-UI клиента

### Файлы для изменения:
- `internal/threexui/client.go` — новые методы API

### Ключевой момент:
**3X-UI сам управляет блокировками!** Мы только задаем параметры:
- `totalGB` — лимит трафика (панель сама заблокирует при превышении)
- `expiryTime` — время окончания (панель сама заблокирует после истечения)
- `limitIp` — лимит соединений (антишаринг)

### Новые методы:
```go
// Обновление клиента (лимиты, время, статус)
UpdateClient(inboundID int, email string, settings ClientSettings) error

// Получение статуса клиента (enable, трафик, expiryTime)
GetClientStatus(inboundID int, email string) (*ClientStatus, error)

// Сброс трафика клиента (для ежемесячного сброса или докупки)
ResetClientTraffic(inboundID int, email string) error
```

### При создании клиента:
```go
clientSettings := map[string]interface{}{
    "clients": []map[string]interface{}{
        {
            "id":         uuid,
            "email":      email,
            "limitIp":    2,                    // Антишаринг
            "totalGB":    30 * 1024 * 1024 * 1024, // 30GB для RU
            "expiryTime": subscriptionEndTime,  // Unix timestamp окончания
            "enable":     true,
        },
    },
}
```

### Логика для двух серверов:
| Сервер | totalGB | expiryTime | limitIp |
|--------|---------|------------|---------|
| RU     | 30GB (+ докупленный) | Конец подписки | 2 |
| EU     | 0 (безлимит) | Конец подписки | 2 |

**При исчерпании RU:** EU продолжает работать (разные totalGB)

### Задачи:
- [ ] Добавить метод UpdateClient
- [ ] Добавить метод GetClientStatus
- [ ] Добавить метод ResetClientTraffic
- [ ] Обновить AddClient — передавать limitIp, expiryTime
- [ ] Метод для докупки трафика (увеличить totalGB на RU)

---

## Этап 5: Фоновые задачи (Scheduler)

### Файлы для создания:
- `internal/scheduler/scheduler.go` — планировщик задач

### Cron-задачи (упрощено):
| Задача | Интервал | Описание |
|--------|----------|----------|
| SyncStatuses | 15 мин | Синхронизация статусов из 3X-UI в БД |
| SendNotifications | 1 час | Уведомления об истечении / лимите |
| MonthlyReset | 1-е число | Сброс трафика RU + продление expiryTime |

**Важно:** Блокировка происходит в панели автоматически! Scheduler только:
- Синхронизирует статусы в БД для отображения в боте
- Отправляет уведомления пользователям
- Сбрасывает лимиты в начале месяца

### SyncStatuses (каждые 15 мин):
```go
func (s *Scheduler) SyncStatuses() {
    users := s.db.GetActiveUsers()
    for _, user := range users {
        // Получаем статус из панели
        statusRU := s.clientA.GetClientStatus(user.Email)
        statusEU := s.clientB.GetClientStatus(user.Email)

        // Обновляем БД
        s.db.UpdateUserTraffic(user.ID, statusRU.UsedTraffic)

        // Если RU заблокирован (превышен лимит) - уведомляем
        if !statusRU.Enable && statusEU.Enable {
            s.notifyRuBlocked(user)
        }
    }
}
```

### Задачи:
- [ ] Создать структуру Scheduler
- [ ] Реализовать SyncStatuses
- [ ] Реализовать уведомления (истечение, лимит RU)
- [ ] Реализовать MonthlyReset
- [ ] Интегрировать в main.go

---

## Этап 6: Конфигурация и финализация

### Файлы для изменения:
- `internal/config/config.go` — новые переменные
- `.env.example` — документация переменных
- `cmd/bot/main.go` — интеграция всех компонентов

### Новые переменные окружения:
```env
# Robokassa
ROBOKASSA_MERCHANT_LOGIN=your_login
ROBOKASSA_PASSWORD1=xxx        # Для генерации подписи
ROBOKASSA_PASSWORD2=yyy        # Для проверки Result URL
ROBOKASSA_TEST_MODE=true       # true для тестов, false для прода

# Тарифы (рубли)
PRICE_MONTHLY=200
PRICE_TRAFFIC_10GB=100

# Лимиты
TRIAL_DAYS=3
TRIAL_TRAFFIC_BYTES=1073741824
MAX_IP_CONNECTIONS=2
RU_MONTHLY_LIMIT_BYTES=32212254720

# Webhook (публичный URL для Robokassa)
WEBHOOK_BASE_URL=https://your-domain.com
```

### Задачи:
- [ ] Обновить config.go
- [ ] Обновить .env.example
- [ ] Интегрировать scheduler в main.go
- [ ] Интегрировать payment webhooks
- [ ] Тестирование полного flow
- [ ] Обновить README.md

---

## Итоговая структура проекта

```
vpn_bot/
├── cmd/bot/main.go
├── internal/
│   ├── bot/
│   │   ├── handlers.go      (обновить)
│   │   ├── handlers_test.go (новый)
│   │   ├── admin.go         (новый)
│   │   ├── keyboards.go     (новый)
│   │   └── messages.go      (новый)
│   ├── config/config.go     (обновить)
│   ├── database/
│   │   ├── db.go            (обновить)
│   │   ├── db_test.go       (новый)
│   │   ├── users.go         (новый)
│   │   ├── payments.go      (новый)
│   │   └── promo.go         (новый)
│   ├── payment/
│   │   ├── robokassa.go     (новый)
│   │   ├── robokassa_test.go (новый)
│   │   └── service.go       (новый)
│   ├── scheduler/
│   │   └── scheduler.go     (новый)
│   ├── subscription/server.go (обновить)
│   ├── threexui/
│   │   ├── client.go        (обновить)
│   │   ├── client_test.go   (новый)
│   │   └── mock.go          (новый) — мок для тестов
│   └── vless/generator.go
├── tests/
│   └── integration_test.go  (новый)
├── .env.example             (обновить)
├── .github/
│   └── workflows/
│       └── test.yml         (новый) — CI
├── Makefile                 (обновить)
├── docker-compose.yml
└── docker-compose.test.yml  (новый)
```

---

## Зависимости для добавления

```go
// go.mod
github.com/robfig/cron/v3 v3.0.1       // Планировщик задач
github.com/stretchr/testify v1.9.0     // Тестирование (assert, require, mock)
```

**Примечание:** Robokassa не требует SDK — простой REST API с MD5 подписью.

---

## Этап 7: Тестирование

### Файлы для создания:
- `internal/database/db_test.go` — тесты БД
- `internal/payment/robokassa_test.go` — тесты платежей
- `internal/threexui/client_test.go` — тесты 3X-UI клиента
- `internal/bot/handlers_test.go` — тесты хендлеров
- `tests/integration_test.go` — интеграционные тесты

### Стратегия тестирования:

**1. Unit-тесты (быстрые, изолированные):**
```go
// database/db_test.go
func TestCreateUser(t *testing.T) {
    db := setupTestDB(t)  // in-memory SQLite
    defer db.Close()

    user, err := db.CreateUser(123456, "test@email.com", "uuid-123")
    assert.NoError(t, err)
    assert.Equal(t, "none", user.SubscriptionStatus)
}

// payment/robokassa_test.go
func TestGeneratePaymentURL(t *testing.T) {
    r := robokassa.New(testConfig)
    url := r.GenerateURL(200, 1, "VPN подписка")
    assert.Contains(t, url, "MerchantLogin=")
    assert.Contains(t, url, "SignatureValue=")
}

func TestVerifySignature(t *testing.T) {
    r := robokassa.New(testConfig)
    valid := r.VerifySignature("200.00", "1", "abc123")
    assert.True(t, valid)
}
```

**2. Моки для внешних сервисов:**
```go
// Мок 3X-UI клиента
type MockThreeXUI struct {
    clients map[string]*ClientStatus
}

func (m *MockThreeXUI) AddClient(...) error { ... }
func (m *MockThreeXUI) GetClientStatus(...) (*ClientStatus, error) { ... }

// Мок Telegram бота
type MockBot struct {
    sentMessages []Message
}

func (m *MockBot) Send(chatID int64, msg string) error {
    m.sentMessages = append(m.sentMessages, Message{chatID, msg})
    return nil
}
```

**3. Интеграционные тесты (с реальной БД):**
```go
// tests/integration_test.go
func TestFullSubscriptionFlow(t *testing.T) {
    // 1. Создаем юзера
    user := createTestUser(t)

    // 2. Активируем триал
    err := service.ActivateTrial(user.ID)
    assert.NoError(t, err)

    // 3. Проверяем статус
    user = getUser(t, user.ID)
    assert.Equal(t, "trial", user.SubscriptionStatus)
    assert.False(t, user.TrialUsed)  // Должен быть true после

    // 4. Симулируем webhook оплаты
    err = service.ProcessPayment(user.ID, "monthly", 200)
    assert.NoError(t, err)

    // 5. Проверяем что подписка активна
    user = getUser(t, user.ID)
    assert.Equal(t, "active", user.SubscriptionStatus)
}
```

**4. E2E тесты (опционально, с тестовым ботом):**
```go
// Запуск тестового бота + симуляция сообщений
func TestBotE2E(t *testing.T) {
    bot := setupTestBot(t)

    // Симулируем /start
    response := bot.HandleMessage("/start", testUserID)
    assert.Contains(t, response, "Подключить VPN")

    // Симулируем нажатие кнопки
    response = bot.HandleCallback("connect_vpn", testUserID)
    assert.Contains(t, response, "триал")
}
```

### Тестовое окружение:

**docker-compose.test.yml:**
```yaml
services:
  test:
    build:
      context: .
      dockerfile: Dockerfile.test
    environment:
      - DB_PATH=:memory:
      - ROBOKASSA_TEST_MODE=true
    command: go test -v ./...
```

**Makefile:**
```makefile
test:
	go test -v ./...

test-coverage:
	go test -coverprofile=coverage.out ./...
	go tool cover -html=coverage.out

test-integration:
	go test -v -tags=integration ./tests/...
```

### Тестирование Robokassa (sandbox):
1. Регистрация тестового магазина на robokassa.ru
2. Включить `ROBOKASSA_TEST_MODE=true`
3. Использовать тестовые карты: `4111 1111 1111 1111`

### Задачи:
- [ ] Написать unit-тесты для database
- [ ] Написать unit-тесты для payment (Robokassa)
- [ ] Создать моки для 3X-UI и Telegram
- [ ] Написать интеграционные тесты
- [ ] Настроить CI (GitHub Actions)
- [ ] Добавить Makefile команды для тестов
