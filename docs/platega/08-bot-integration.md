# План интеграции Platega с VPN-ботом

## Общая идея

Пользователь нажимает кнопку "Оплатить подписку" в боте -> бот создает платеж в Platega -> пользователь переходит по ссылке и оплачивает -> Platega отправляет callback -> бот активирует/продлевает подписку через Remnawave API.

## Что нужно реализовать

### 1. HTTP-клиент Platega (`internal/platega/client.go`)

По аналогии с `internal/remnawave/client.go`:

```go
type Client struct {
    baseURL    string
    merchantID string
    secret     string
    httpClient *http.Client
}

// Создание платежа
func (c *Client) CreatePayment(ctx context.Context, req CreatePaymentRequest) (*CreatePaymentResponse, error)

// Проверка статуса
func (c *Client) GetTransactionStatus(ctx context.Context, transactionID string) (*TransactionStatusResponse, error)
```

### 2. Callback-сервер

Бот должен принимать входящие POST-запросы от Platega. Варианты:

**Вариант A: Встроенный HTTP-сервер**
- Поднять `net/http` сервер на отдельном порту (напр. 8443)
- Роут: `POST /platega/callback`
- Верифицировать X-MerchantId и X-Secret из заголовков
- Требует публичный HTTPS-адрес (можно через reverse proxy / Caddy)

**Вариант B: Polling (без callback)**
- После создания платежа периодически проверять статус через GET /transaction/{id}
- Проще в реализации, не требует публичного endpoint
- Минус: задержка до обнаружения оплаты

**Рекомендация:** Вариант A (callback) — мгновенное подтверждение, стандартная практика.

### 3. База данных — таблица `payments`

```sql
CREATE TABLE payments (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    telegram_id INTEGER NOT NULL,
    transaction_id TEXT NOT NULL UNIQUE,  -- UUID из Platega
    amount REAL NOT NULL,
    currency TEXT NOT NULL DEFAULT 'RUB',
    status TEXT NOT NULL DEFAULT 'PENDING',  -- PENDING/CONFIRMED/CANCELED/CHARGEBACKED
    payment_method INTEGER NOT NULL,
    payload TEXT,
    created_at DATETIME DEFAULT CURRENT_TIMESTAMP,
    confirmed_at DATETIME,
    FOREIGN KEY (telegram_id) REFERENCES users(telegram_id)
);
```

### 4. Обработчик в боте (`internal/bot/payment_handler.go`)

Флоу:
1. Пользователь нажимает "Оплатить" -> выбирает тариф
2. Бот вызывает `platega.CreatePayment()` с `payload = telegram_id`
3. Сохраняет `transaction_id` в таблицу `payments`
4. Отправляет InlineKeyboard с кнопкой-ссылкой `redirect`
5. При получении callback с `status=CONFIRMED`:
   - Обновляет запись в `payments`
   - Продлевает подписку через Remnawave API (`POST /api/users/{uuid}/extend`)
   - Уведомляет пользователя в Telegram

### 5. Переменные окружения

```env
# Platega
PLATEGA_MERCHANT_ID=uuid
PLATEGA_SECRET=ключ
PLATEGA_CALLBACK_URL=https://your-domain.com/platega/callback  # опционально, для настройки
```

### 6. Тарифы

Определить в конфиге или env:

```env
PLATEGA_PRICE_1M=200    # цена за 1 месяц в RUB
PLATEGA_PRICE_3M=500    # цена за 3 месяца
PLATEGA_PRICE_6M=900    # цена за 6 месяцев
```

## Флоу взаимодействия

```
Пользователь          Бот                  Platega           Remnawave
    |                  |                      |                  |
    |-- /pay --------->|                      |                  |
    |<- Выбери тариф --|                      |                  |
    |-- 1 месяц ------>|                      |                  |
    |                  |-- POST /transaction/process -->|        |
    |                  |<-- {redirect, transactionId} --|        |
    |<- [Оплатить] ----|                      |                  |
    |-- (переходит) ---|--------------------->|                  |
    |                  |                      |                  |
    |              (оплата проходит)          |                  |
    |                  |                      |                  |
    |                  |<-- POST callback ----|                  |
    |                  |    status=CONFIRMED  |                  |
    |                  |                      |                  |
    |                  |-- extend subscription ----------------->|
    |                  |<---------------------------------------------|
    |<- Подписка       |                      |                  |
    |   активирована!  |                      |                  |
```

## Безопасность

1. **Верификация callback** — сравнивать X-MerchantId и X-Secret из заголовков с нашими значениями
2. **Идемпотентность** — проверять, что платеж не был обработан повторно (по transaction_id)
3. **payload** — передавать telegram_id для идентификации пользователя при callback
4. **HTTPS** — callback endpoint только по HTTPS с валидным сертификатом
