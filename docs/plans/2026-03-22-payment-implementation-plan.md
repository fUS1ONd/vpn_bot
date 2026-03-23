# Реализация платёжной системы Platega — План реализации

> **For Claude:** REQUIRED SUB-SKILL: Use superpowers:executing-plans to implement this plan task-by-task.

**Goal:** Интеграция платёжной системы Platega в Telegram-бота для автоматической оплаты VPN-подписок.

**Architecture:** Бот получает встроенный HTTP-сервер для приёма callback от Platega (проксируется через nginx). Platega HTTP-клиент создаёт платежи и проверяет статусы. Scheduler переработан на event-driven модель (каждые 30 минут). Новые таблицы `payments` и `moderator_earnings` хранят финансовую историю. Поле `subscription_price` добавляется в `users` и `invites`.

**Tech Stack:** Go 1.25, SQLite, telebot.v3, net/http (callback-сервер), Platega REST API

**Связанные документы:**
- [Бизнес-план](./2026-03-21-payment-business-model-redesign.md)
- [UI пользователя](./2026-03-21-user-ui-redesign.md)
- [UI модератора](./2026-03-21-moderator-ui-redesign.md)
- [UI админа](./2026-03-21-admin-ui-redesign.md)
- [Platega API](../platega/README.md)

---

## Этап 1: Миграция БД (новые таблицы и поля)

**Цель:** Добавить таблицы `payments`, `moderator_earnings` и новые поля в существующие таблицы. После этого этапа бот работает как раньше — новые таблицы просто существуют.

**Файлы:**
- Изменить: `internal/database/db.go` — добавить миграции
- Изменить: `internal/database/users.go` — добавить поля `subscription_price`, `moderator_id` в структуру `User`
- Изменить: `internal/database/invites.go` — добавить поле `subscription_price` в структуру `Invite`
- Создать: `internal/database/payments.go` — CRUD для таблицы `payments`
- Создать: `internal/database/earnings.go` — CRUD для таблицы `moderator_earnings`
- Создать: `internal/database/payments_test.go`
- Создать: `internal/database/earnings_test.go`

### Шаг 1: Добавить миграции в `db.go`

В массив `alterMigrations` в функции `migrate()` добавить:

```go
// Миграция: цена подписки пользователя (руб/мес, NULL = не установлена)
`ALTER TABLE users ADD COLUMN subscription_price INTEGER`,
// Миграция: telegram_id модератора-куратора (NULL = админский или снят модератор)
`ALTER TABLE users ADD COLUMN moderator_id INTEGER`,
// Миграция: цена подписки при создании инвайта
`ALTER TABLE invites ADD COLUMN subscription_price INTEGER`,
```

В массив `migrations` (CREATE TABLE) добавить:

```sql
CREATE TABLE IF NOT EXISTS payments (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    telegram_id INTEGER NOT NULL,
    moderator_id INTEGER,
    amount INTEGER NOT NULL,
    payment_method TEXT NOT NULL,
    status TEXT NOT NULL DEFAULT 'pending',
    platega_transaction_id TEXT UNIQUE,
    redirect_url TEXT,
    expires_at TIMESTAMP,
    created_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP,
    confirmed_at TIMESTAMP
)
```

```sql
CREATE TABLE IF NOT EXISTS moderator_earnings (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    payment_id INTEGER NOT NULL REFERENCES payments(id),
    moderator_id INTEGER NOT NULL,
    gross_amount INTEGER NOT NULL,
    platega_fee INTEGER NOT NULL,
    withdrawal_fee INTEGER NOT NULL,
    net_amount INTEGER NOT NULL,
    share_percent INTEGER NOT NULL,
    share_amount INTEGER NOT NULL,
    created_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP
)
```

Индексы:

```sql
CREATE INDEX IF NOT EXISTS idx_payments_telegram_id ON payments(telegram_id)
CREATE INDEX IF NOT EXISTS idx_payments_status ON payments(status)
CREATE INDEX IF NOT EXISTS idx_payments_platega_tx ON payments(platega_transaction_id)
CREATE INDEX IF NOT EXISTS idx_earnings_moderator ON moderator_earnings(moderator_id)
CREATE INDEX IF NOT EXISTS idx_earnings_payment ON moderator_earnings(payment_id)
```

### Шаг 2: Обновить структуру `User` в `db.go`

```go
type User struct {
    TelegramID        int64
    Username          string
    FirstName         string
    RemnawaveUUID     string
    SubscriptionPrice *int   // Цена подписки руб/мес (NULL = не установлена)
    ModeratorID       *int64 // Telegram ID куратора (NULL = админский/снят)
    CreatedAt         time.Time
}
```

### Шаг 3: Обновить все SELECT-запросы в `users.go`

Все функции, читающие из `users`, должны добавить `subscription_price, moderator_id` в SELECT и Scan.

Обновить: `GetUserByTelegramID`, `GetAllUsers`, `GetUserByRemnawaveUUID`.

Обновить `CreateUser` — добавить параметры `subscriptionPrice *int, moderatorID *int64`:

```go
func (db *DB) CreateUser(telegramID int64, username, firstName string, remnawaveUUID string, subscriptionPrice *int, moderatorID *int64) error {
    _, err := db.conn.Exec(
        `INSERT INTO users (telegram_id, username, first_name, remnawave_uuid, subscription_price, moderator_id) VALUES (?, ?, ?, ?, ?, ?)`,
        telegramID, username, firstName, remnawaveUUID, subscriptionPrice, moderatorID,
    )
    return err
}
```

Добавить `UpdateSubscriptionPrice`:

```go
func (db *DB) UpdateSubscriptionPrice(telegramID int64, price int) error {
    _, err := db.conn.Exec(`UPDATE users SET subscription_price = ? WHERE telegram_id = ?`, price, telegramID)
    return err
}
```

### Шаг 4: Обновить структуру `Invite` в `db.go`

```go
type Invite struct {
    Code              string
    CreatedBy         int64
    UsedBy            *int64
    UsedAt            *time.Time
    ExpireDays        *int
    SubscriptionPrice *int   // Цена подписки при создании инвайта
    KickedAt          *time.Time
    CreatedAt         time.Time
}
```

Добавить `CreateInviteWithPrice` (для модератора):

```go
func (db *DB) CreateInviteWithPrice(createdBy int64, expireDays int, price int) (string, error) {
    code := generateCode()
    _, err := db.conn.Exec(
        `INSERT INTO invites (code, created_by, expire_days, subscription_price) VALUES (?, ?, ?, ?)`,
        code, createdBy, expireDays, price,
    )
    return code, err
}
```

**Важно:** Существующий `CreateInvite` (админский, бессрочный) остаётся без изменений — он создаёт инвайт с `expire_days=NULL, subscription_price=NULL`. Админские инвайты не участвуют в платёжной модели.

Обновить все SELECT-запросы для invites — добавить `subscription_price` в SELECT и Scan.

### Шаг 5: Создать `internal/database/payments.go`

```go
package database

import (
    "database/sql"
    "time"
)

// Payment представляет запись платежа
type Payment struct {
    ID                    int64
    TelegramID            int64
    ModeratorID           *int64
    Amount                int
    PaymentMethod         string   // "sbp", "card", "crypto"
    Status                string   // "pending", "confirmed", "expired", "canceled", "chargebacked", "confirmed_not_activated"
    PlategaTransactionID  *string
    RedirectURL           *string
    ExpiresAt             *time.Time
    CreatedAt             time.Time
    ConfirmedAt           *time.Time
}

// CreatePayment создаёт новый платёж
func (db *DB) CreatePayment(p *Payment) (int64, error) {
    res, err := db.conn.Exec(
        `INSERT INTO payments (telegram_id, moderator_id, amount, payment_method, status, platega_transaction_id, redirect_url, expires_at)
         VALUES (?, ?, ?, ?, ?, ?, ?, ?)`,
        p.TelegramID, p.ModeratorID, p.Amount, p.PaymentMethod, p.Status, p.PlategaTransactionID, p.RedirectURL, p.ExpiresAt,
    )
    if err != nil {
        return 0, err
    }
    return res.LastInsertId()
}

// GetPaymentByID возвращает платёж по ID
func (db *DB) GetPaymentByID(id int64) (*Payment, error) {
    p := &Payment{}
    err := db.conn.QueryRow(
        `SELECT id, telegram_id, moderator_id, amount, payment_method, status, platega_transaction_id, redirect_url, expires_at, created_at, confirmed_at
         FROM payments WHERE id = ?`, id,
    ).Scan(&p.ID, &p.TelegramID, &p.ModeratorID, &p.Amount, &p.PaymentMethod, &p.Status, &p.PlategaTransactionID, &p.RedirectURL, &p.ExpiresAt, &p.CreatedAt, &p.ConfirmedAt)
    if err == sql.ErrNoRows {
        return nil, nil
    }
    return p, err
}

// GetPendingPayment возвращает активный PENDING платёж пользователя (не протухший)
func (db *DB) GetPendingPayment(telegramID int64) (*Payment, error) {
    p := &Payment{}
    err := db.conn.QueryRow(
        `SELECT id, telegram_id, moderator_id, amount, payment_method, status, platega_transaction_id, redirect_url, expires_at, created_at, confirmed_at
         FROM payments WHERE telegram_id = ? AND status = 'pending' AND (expires_at IS NULL OR expires_at > datetime('now'))
         ORDER BY created_at DESC LIMIT 1`, telegramID,
    ).Scan(&p.ID, &p.TelegramID, &p.ModeratorID, &p.Amount, &p.PaymentMethod, &p.Status, &p.PlategaTransactionID, &p.RedirectURL, &p.ExpiresAt, &p.CreatedAt, &p.ConfirmedAt)
    if err == sql.ErrNoRows {
        return nil, nil
    }
    return p, err
}

// GetPaymentByPlategaTxID возвращает платёж по ID транзакции Platega
func (db *DB) GetPaymentByPlategaTxID(txID string) (*Payment, error) {
    p := &Payment{}
    err := db.conn.QueryRow(
        `SELECT id, telegram_id, moderator_id, amount, payment_method, status, platega_transaction_id, redirect_url, expires_at, created_at, confirmed_at
         FROM payments WHERE platega_transaction_id = ?`, txID,
    ).Scan(&p.ID, &p.TelegramID, &p.ModeratorID, &p.Amount, &p.PaymentMethod, &p.Status, &p.PlategaTransactionID, &p.RedirectURL, &p.ExpiresAt, &p.CreatedAt, &p.ConfirmedAt)
    if err == sql.ErrNoRows {
        return nil, nil
    }
    return p, err
}

// UpdatePaymentStatus обновляет статус платежа
func (db *DB) UpdatePaymentStatus(id int64, status string) error {
    _, err := db.conn.Exec(`UPDATE payments SET status = ? WHERE id = ?`, status, id)
    return err
}

// ConfirmPayment помечает платёж как confirmed с датой
func (db *DB) ConfirmPayment(id int64) error {
    _, err := db.conn.Exec(
        `UPDATE payments SET status = 'confirmed', confirmed_at = datetime('now') WHERE id = ?`, id,
    )
    return err
}

// ExpireOldPendingPayments помечает протухшие PENDING как expired
func (db *DB) ExpireOldPendingPayments() (int64, error) {
    res, err := db.conn.Exec(
        `UPDATE payments SET status = 'expired' WHERE status = 'pending' AND expires_at <= datetime('now')`,
    )
    if err != nil {
        return 0, err
    }
    return res.RowsAffected()
}

// GetConfirmedNotActivated возвращает платежи со статусом confirmed_not_activated
func (db *DB) GetConfirmedNotActivated() ([]Payment, error) {
    rows, err := db.conn.Query(
        `SELECT id, telegram_id, moderator_id, amount, payment_method, status, platega_transaction_id, redirect_url, expires_at, created_at, confirmed_at
         FROM payments WHERE status = 'confirmed_not_activated'`,
    )
    if err != nil {
        return nil, err
    }
    defer rows.Close()

    var payments []Payment
    for rows.Next() {
        var p Payment
        if err := rows.Scan(&p.ID, &p.TelegramID, &p.ModeratorID, &p.Amount, &p.PaymentMethod, &p.Status, &p.PlategaTransactionID, &p.RedirectURL, &p.ExpiresAt, &p.CreatedAt, &p.ConfirmedAt); err != nil {
            return nil, err
        }
        payments = append(payments, p)
    }
    return payments, rows.Err()
}

// HasConfirmedPayment проверяет, была ли у пользователя хотя бы одна подтверждённая оплата
func (db *DB) HasConfirmedPayment(telegramID int64) (bool, error) {
    var exists bool
    err := db.conn.QueryRow(
        `SELECT EXISTS(SELECT 1 FROM payments WHERE telegram_id = ? AND status = 'confirmed')`, telegramID,
    ).Scan(&exists)
    return exists, err
}

// CountConfirmedPaymentsByMonth считает платежи за месяц (для статистики)
func (db *DB) CountConfirmedPaymentsByMonth(year int, month int) (int, error) {
    var count int
    start := time.Date(year, time.Month(month), 1, 0, 0, 0, 0, time.UTC)
    end := start.AddDate(0, 1, 0)
    err := db.conn.QueryRow(
        `SELECT COUNT(*) FROM payments WHERE status = 'confirmed' AND confirmed_at >= ? AND confirmed_at < ?`,
        start, end,
    ).Scan(&count)
    return count, err
}

// SumConfirmedPaymentsByMonth возвращает сумму платежей за месяц
func (db *DB) SumConfirmedPaymentsByMonth(year int, month int) (int, error) {
    var sum int
    start := time.Date(year, time.Month(month), 1, 0, 0, 0, 0, time.UTC)
    end := start.AddDate(0, 1, 0)
    err := db.conn.QueryRow(
        `SELECT COALESCE(SUM(amount), 0) FROM payments WHERE status = 'confirmed' AND confirmed_at >= ? AND confirmed_at < ?`,
        start, end,
    ).Scan(&sum)
    return sum, err
}

// CountTrialsByMonth считает триалы (активации инвайтов) за месяц
func (db *DB) CountTrialsByMonth(year int, month int) (int, error) {
    var count int
    start := time.Date(year, time.Month(month), 1, 0, 0, 0, 0, time.UTC)
    end := start.AddDate(0, 1, 0)
    err := db.conn.QueryRow(
        `SELECT COUNT(*) FROM invites WHERE used_at >= ? AND used_at < ? AND expire_days IS NOT NULL`,
        start, end,
    ).Scan(&count)
    return count, err
}

// CountFirstPaymentsByMonth считает первые оплаты (конверсия триал→оплата) за месяц
func (db *DB) CountFirstPaymentsByMonth(year int, month int) (int, error) {
    var count int
    start := time.Date(year, time.Month(month), 1, 0, 0, 0, 0, time.UTC)
    end := start.AddDate(0, 1, 0)
    // Считаем пользователей, у которых первый confirmed платёж попал в этот месяц
    err := db.conn.QueryRow(
        `SELECT COUNT(*) FROM (
            SELECT telegram_id, MIN(confirmed_at) as first_payment
            FROM payments WHERE status = 'confirmed'
            GROUP BY telegram_id
            HAVING first_payment >= ? AND first_payment < ?
        )`, start, end,
    ).Scan(&count)
    return count, err
}

// CountPayingSubscribersByModerator считает активных платящих подписчиков модератора
// Считаются только пользователи, которые: 1) существуют в БД (не удалены), 2) имеют confirmed платёж,
// 3) имеют последний платёж не старше 60 дней (активный платящий клиент)
func (db *DB) CountPayingSubscribersByModerator(moderatorID int64) (int, error) {
    var count int
    err := db.conn.QueryRow(
        `SELECT COUNT(DISTINCT u.telegram_id) FROM users u
         JOIN payments p ON p.telegram_id = u.telegram_id
         WHERE u.moderator_id = ? AND p.status = 'confirmed'
         AND p.confirmed_at >= datetime('now', '-60 days')`,
        moderatorID,
    ).Scan(&count)
    return count, err
}
```

### Шаг 6: Создать `internal/database/earnings.go`

```go
package database

import (
    "database/sql"
    "time"
)

// ModeratorEarning представляет запись начисления модератору
type ModeratorEarning struct {
    ID             int64
    PaymentID      int64
    ModeratorID    int64
    GrossAmount    int // Сумма платежа
    PlategaFee     int // Комиссия Platega
    WithdrawalFee  int // Комиссия вывода (2%)
    NetAmount      int // Чистый доход после всех комиссий
    SharePercent   int // Процент доли модератора
    ShareAmount    int // Сумма доли модератора
    CreatedAt      time.Time
}

// CreateEarning создаёт запись начисления модератору
func (db *DB) CreateEarning(e *ModeratorEarning) (int64, error) {
    res, err := db.conn.Exec(
        `INSERT INTO moderator_earnings (payment_id, moderator_id, gross_amount, platega_fee, withdrawal_fee, net_amount, share_percent, share_amount)
         VALUES (?, ?, ?, ?, ?, ?, ?, ?)`,
        e.PaymentID, e.ModeratorID, e.GrossAmount, e.PlategaFee, e.WithdrawalFee, e.NetAmount, e.SharePercent, e.ShareAmount,
    )
    if err != nil {
        return 0, err
    }
    return res.LastInsertId()
}

// GetModeratorEarningsByMonth возвращает агрегированные данные за месяц
type MonthlyEarnings struct {
    TotalPayments    int // Количество платежей
    GrossAmount      int // Сумма платежей
    TotalPlategaFee  int // Суммарная комиссия Platega
    TotalWithdrawal  int // Суммарная комиссия вывода
    TotalNetAmount   int // Суммарный чистый доход
    TotalShareAmount int // Суммарная доля модератора
    SharePercent     int // Последний актуальный процент
}

func (db *DB) GetModeratorEarningsByMonth(moderatorID int64, year int, month int) (*MonthlyEarnings, error) {
    start := time.Date(year, time.Month(month), 1, 0, 0, 0, 0, time.UTC)
    end := start.AddDate(0, 1, 0)

    me := &MonthlyEarnings{}
    err := db.conn.QueryRow(
        `SELECT COUNT(*), COALESCE(SUM(gross_amount), 0), COALESCE(SUM(platega_fee), 0),
                COALESCE(SUM(withdrawal_fee), 0), COALESCE(SUM(net_amount), 0), COALESCE(SUM(share_amount), 0)
         FROM moderator_earnings WHERE moderator_id = ? AND created_at >= ? AND created_at < ?`,
        moderatorID, start, end,
    ).Scan(&me.TotalPayments, &me.GrossAmount, &me.TotalPlategaFee, &me.TotalWithdrawal, &me.TotalNetAmount, &me.TotalShareAmount)
    if err != nil {
        return nil, err
    }

    // Получаем актуальный процент (из последнего начисления)
    var pct sql.NullInt64
    db.conn.QueryRow(
        `SELECT share_percent FROM moderator_earnings WHERE moderator_id = ? ORDER BY created_at DESC LIMIT 1`,
        moderatorID,
    ).Scan(&pct)
    if pct.Valid {
        me.SharePercent = int(pct.Int64)
    }

    return me, nil
}

// GetModeratorTotalEarnings возвращает суммарную долю модератора за всё время
func (db *DB) GetModeratorTotalEarnings(moderatorID int64) (int, error) {
    var sum sql.NullInt64
    err := db.conn.QueryRow(
        `SELECT COALESCE(SUM(share_amount), 0) FROM moderator_earnings WHERE moderator_id = ?`,
        moderatorID,
    ).Scan(&sum)
    if sum.Valid {
        return int(sum.Int64), err
    }
    return 0, err
}

// GetAllEarningsByMonth возвращает общую статистику за месяц (для админа)
func (db *DB) GetAllEarningsByMonth(year int, month int) (*MonthlyEarnings, error) {
    start := time.Date(year, time.Month(month), 1, 0, 0, 0, 0, time.UTC)
    end := start.AddDate(0, 1, 0)

    me := &MonthlyEarnings{}
    err := db.conn.QueryRow(
        `SELECT COUNT(*), COALESCE(SUM(gross_amount), 0), COALESCE(SUM(platega_fee), 0),
                COALESCE(SUM(withdrawal_fee), 0), COALESCE(SUM(net_amount), 0), COALESCE(SUM(share_amount), 0)
         FROM moderator_earnings WHERE created_at >= ? AND created_at < ?`,
        start, end,
    ).Scan(&me.TotalPayments, &me.GrossAmount, &me.TotalPlategaFee, &me.TotalWithdrawal, &me.TotalNetAmount, &me.TotalShareAmount)
    return me, err
}
```

### Шаг 7: Написать тесты

Файл `internal/database/payments_test.go` — тесты на CRUD payments:
- `TestCreatePayment` — создание и получение по ID
- `TestGetPendingPayment` — поиск активного PENDING платежа
- `TestGetPaymentByPlategaTxID` — поиск по transaction_id
- `TestConfirmPayment` — подтверждение и проверка confirmed_at
- `TestExpireOldPendingPayments` — протухание старых платежей
- `TestHasConfirmedPayment` — проверка наличия оплаты

Файл `internal/database/earnings_test.go` — тесты на earnings:
- `TestCreateEarning` — создание записи
- `TestGetModeratorEarningsByMonth` — агрегация за месяц
- `TestGetModeratorTotalEarnings` — суммарная доля за всё время

### Шаг 8: Запустить тесты и коммит

```bash
make tests
make fmt
```

**Критерии приёмки этапа 1:**
- Бот запускается без ошибок (таблицы создаются при старте)
- Новые поля `subscription_price` и `moderator_id` в `users` доступны (NULL для существующих)
- Таблицы `payments` и `moderator_earnings` созданы
- Все тесты проходят
- Существующая функциональность не сломана

---

## Этап 2: Platega HTTP-клиент

**Цель:** Создать HTTP-клиент для работы с Platega API — создание платежей, проверка статуса. После этого этапа клиент готов к использованию, но нигде не вызывается.

**Файлы:**
- Создать: `internal/platega/client.go`
- Создать: `internal/platega/client_test.go`

### Шаг 1: Добавить конфигурацию Platega в `config.go`

В структуру `Config` добавить:

```go
// Platega
PlategaMerchantID      string
PlategaSecret          string
PlategaCallbackURL     string // Полный URL для callback (https://domain.com/platega/callback)
MinSubscriptionPrice   int    // Минимальная цена подписки (руб), по умолчанию 400
TrialTrafficLimitGB    int    // Лимит трафика триала (ГБ), по умолчанию 1
PlategaFeeSBP          int    // Комиссия Platega СБП (%), по умолчанию 11
PlategaFeeCard         int    // Комиссия Platega карты (%), по умолчанию 12
PlategaFeeCrypto       int    // Комиссия Platega крипта (%), по умолчанию 5
PlategaFeeWithdrawal   int    // Комиссия вывода (%), по умолчанию 2
```

В функции `Load()`:

```go
cfg.PlategaMerchantID = os.Getenv("PLATEGA_MERCHANT_ID")
cfg.PlategaSecret = os.Getenv("PLATEGA_SECRET")
cfg.PlategaCallbackURL = os.Getenv("PLATEGA_CALLBACK_URL")
cfg.MinSubscriptionPrice = getEnvOrDefaultInt("MIN_SUBSCRIPTION_PRICE", 400)
cfg.TrialTrafficLimitGB = getEnvOrDefaultInt("TRIAL_TRAFFIC_LIMIT_GB", 1)
cfg.PlategaFeeSBP = getEnvOrDefaultInt("PLATEGA_FEE_SBP", 11)
cfg.PlategaFeeCard = getEnvOrDefaultInt("PLATEGA_FEE_CARD", 12)
cfg.PlategaFeeCrypto = getEnvOrDefaultInt("PLATEGA_FEE_CRYPTO", 5)
cfg.PlategaFeeWithdrawal = getEnvOrDefaultInt("PLATEGA_FEE_WITHDRAWAL", 2)
```

Добавить хелпер:

```go
func getEnvOrDefaultInt(key string, defaultValue int) int {
    if v := os.Getenv(key); v != "" {
        if n, err := strconv.Atoi(v); err == nil {
            return n
        }
    }
    return defaultValue
}
```

**Важно:** Platega-переменные НЕ обязательные — если не заданы, платёжный функционал просто отключён (как render-сервис).

### Шаг 2: Создать `internal/platega/client.go`

```go
package platega

import (
    "bytes"
    "encoding/json"
    "fmt"
    "io"
    "net/http"
    "time"
)

const baseURL = "https://app.platega.io"

// Способы оплаты (Platega paymentMethod int)
const (
    PaymentMethodSBP    = 2
    PaymentMethodCard   = 11
    PaymentMethodCrypto = 13
)

// Статусы платежа
const (
    StatusPending     = "PENDING"
    StatusConfirmed   = "CONFIRMED"
    StatusCanceled    = "CANCELED"
    StatusChargebacked = "CHARGEBACKED"
)

// Client — HTTP-клиент Platega API
type Client struct {
    merchantID string
    secret     string
    http       *http.Client
}

// NewClient создаёт клиент Platega
func NewClient(merchantID, secret string) *Client {
    return &Client{
        merchantID: merchantID,
        secret:     secret,
        http:       &http.Client{Timeout: 30 * time.Second},
    }
}

// MerchantID возвращает merchant_id (для верификации callback)
func (c *Client) MerchantID() string {
    return c.merchantID
}

// Secret возвращает secret (для верификации callback)
func (c *Client) Secret() string {
    return c.secret
}

// CreateTransactionRequest — запрос на создание платежа
type CreateTransactionRequest struct {
    PaymentMethod int    `json:"paymentMethod"`
    Amount        int    `json:"amount"`        // В рублях (целое число)
    Currency      string `json:"currency"`      // "RUB"
    Description   string `json:"description"`
    ReturnURL     string `json:"return"`        // URL возврата после оплаты (бот Telegram)
    FailedURL     string `json:"failedUrl"`     // URL при ошибке
    CallbackURL   string `json:"callbackUrl"`   // URL для callback
    Payload       string `json:"payload"`       // Произвольные данные (telegram_id)
}

// CreateTransactionResponse — ответ на создание платежа
type CreateTransactionResponse struct {
    TransactionID string    `json:"transactionId"`
    Redirect      string    `json:"redirect"`     // Ссылка для перенаправления пользователя
    Status        string    `json:"status"`
    ExpiresIn     int       `json:"expiresIn"`    // Время жизни в секундах
}

// TransactionStatus — полный статус транзакции
type TransactionStatus struct {
    ID            string `json:"id"`
    Amount        string `json:"amount"`
    Currency      string `json:"currency"`
    Status        string `json:"status"`
    PaymentMethod int    `json:"paymentMethod"`
    Payload       string `json:"payload"`
}

// CallbackPayload — тело callback-запроса от Platega.
// Единственное определение — используется и в platega-клиенте, и в callback-сервере (импортируется оттуда).
type CallbackPayload struct {
    ID            string `json:"id"`
    Amount        string `json:"amount"`
    Currency      string `json:"currency"`
    Status        string `json:"status"`
    PaymentMethod int    `json:"paymentMethod"`
    Payload       string `json:"payload"`
}

// CreatePayment создаёт платёж в Platega
func (c *Client) CreatePayment(req CreateTransactionRequest) (*CreateTransactionResponse, error) {
    // Формируем тело запроса согласно API
    body := map[string]interface{}{
        "paymentMethod": req.PaymentMethod,
        "paymentDetails": map[string]interface{}{
            "amount":   req.Amount,
            "currency": req.Currency,
        },
        "description": req.Description,
        "return":      req.ReturnURL,
        "failedUrl":   req.FailedURL,
        "callbackUrl": req.CallbackURL,
        "payload":     req.Payload,
    }

    data, err := json.Marshal(body)
    if err != nil {
        return nil, fmt.Errorf("marshal request: %w", err)
    }

    httpReq, err := http.NewRequest("POST", baseURL+"/transaction/process", bytes.NewReader(data))
    if err != nil {
        return nil, fmt.Errorf("create request: %w", err)
    }

    c.setHeaders(httpReq)
    httpReq.Header.Set("Content-Type", "application/json")

    resp, err := c.http.Do(httpReq)
    if err != nil {
        return nil, fmt.Errorf("send request: %w", err)
    }
    defer resp.Body.Close()

    respBody, err := io.ReadAll(resp.Body)
    if err != nil {
        return nil, fmt.Errorf("read response: %w", err)
    }

    if resp.StatusCode != http.StatusOK && resp.StatusCode != http.StatusCreated {
        return nil, fmt.Errorf("platega API error %d: %s", resp.StatusCode, string(respBody))
    }

    var result CreateTransactionResponse
    if err := json.Unmarshal(respBody, &result); err != nil {
        return nil, fmt.Errorf("unmarshal response: %w", err)
    }

    return &result, nil
}

// GetTransactionStatus проверяет статус транзакции
func (c *Client) GetTransactionStatus(transactionID string) (*TransactionStatus, error) {
    httpReq, err := http.NewRequest("GET", baseURL+"/transaction/"+transactionID, nil)
    if err != nil {
        return nil, fmt.Errorf("create request: %w", err)
    }

    c.setHeaders(httpReq)

    resp, err := c.http.Do(httpReq)
    if err != nil {
        return nil, fmt.Errorf("send request: %w", err)
    }
    defer resp.Body.Close()

    respBody, err := io.ReadAll(resp.Body)
    if err != nil {
        return nil, fmt.Errorf("read response: %w", err)
    }

    if resp.StatusCode != http.StatusOK {
        return nil, fmt.Errorf("platega API error %d: %s", resp.StatusCode, string(respBody))
    }

    var result TransactionStatus
    if err := json.Unmarshal(respBody, &result); err != nil {
        return nil, fmt.Errorf("unmarshal response: %w", err)
    }

    return &result, nil
}

// setHeaders устанавливает авторизационные заголовки
func (c *Client) setHeaders(req *http.Request) {
    req.Header.Set("X-MerchantId", c.merchantID)
    req.Header.Set("X-Secret", c.secret)
}

// PaymentMethodName возвращает человекочитаемое название способа оплаты
func PaymentMethodName(method int) string {
    switch method {
    case PaymentMethodSBP:
        return "СБП"
    case PaymentMethodCard:
        return "Карта"
    case PaymentMethodCrypto:
        return "Крипта"
    default:
        return "Неизвестно"
    }
}

// PaymentMethodString возвращает строковый идентификатор для БД
func PaymentMethodString(method int) string {
    switch method {
    case PaymentMethodSBP:
        return "sbp"
    case PaymentMethodCard:
        return "card"
    case PaymentMethodCrypto:
        return "crypto"
    default:
        return "unknown"
    }
}

// PaymentMethodFromString возвращает int из строкового идентификатора
func PaymentMethodFromString(s string) int {
    switch s {
    case "sbp":
        return PaymentMethodSBP
    case "card":
        return PaymentMethodCard
    case "crypto":
        return PaymentMethodCrypto
    default:
        return 0
    }
}
```

### Шаг 3: Написать тесты

Файл `internal/platega/client_test.go`:
- `TestPaymentMethodConversion` — конвертация method int ↔ string
- `TestSetHeaders` — проверка установки заголовков
- Интеграционные тесты с httptest.Server (мок Platega API) для `CreatePayment` и `GetTransactionStatus`

### Шаг 4: Запустить тесты и коммит

```bash
make tests
make fmt
```

**Критерии приёмки этапа 2:**
- Platega-клиент компилируется и тесты проходят
- Конфигурация расширена новыми переменными (все опциональные)
- Бот запускается без PLATEGA_* переменных (клиент не создаётся)

---

## Этап 3: Callback HTTP-сервер + верификация

**Цель:** Добавить встроенный HTTP-сервер в процесс бота для приёма callback от Platega. После этого этапа сервер стартует, принимает запросы, верифицирует заголовки, но ещё не обрабатывает платежи (только логирует).

**Файлы:**
- Создать: `internal/callback/server.go`
- Создать: `internal/callback/server_test.go`
- Изменить: `cmd/bot/main.go` — запуск callback-сервера
- Изменить: `internal/config/config.go` — порт callback-сервера

### Шаг 1: Добавить конфигурацию порта

В `Config` добавить:

```go
CallbackPort int // Порт для callback-сервера (по умолчанию 8080)
```

В `Load()`:

```go
cfg.CallbackPort = getEnvOrDefaultInt("CALLBACK_PORT", 8080)
```

### Шаг 2: Создать `internal/callback/server.go`

```go
package callback

import (
    "context"
    "encoding/json"
    "fmt"
    "io"
    "log/slog"
    "net/http"
    "time"

    "github.com/fus1ond/vpn_bot/internal/platega"
)

// PaymentHandler — интерфейс обработки подтверждённых платежей.
// Использует platega.CallbackPayload (единственное определение, без дублирования).
type PaymentHandler interface {
    HandlePaymentCallback(payload platega.CallbackPayload) error
}

// Server — HTTP-сервер для приёма callback от Platega
type Server struct {
    merchantID string
    secret     string
    handler    PaymentHandler
    httpServer *http.Server
}

// NewServer создаёт callback-сервер
func NewServer(port int, merchantID, secret string, handler PaymentHandler) *Server {
    s := &Server{
        merchantID: merchantID,
        secret:     secret,
        handler:    handler,
    }

    mux := http.NewServeMux()
    mux.HandleFunc("/platega/callback", s.handleCallback)
    mux.HandleFunc("/health", s.handleHealth)

    s.httpServer = &http.Server{
        Addr:         fmt.Sprintf(":%d", port),
        Handler:      mux,
        ReadTimeout:  10 * time.Second,
        WriteTimeout: 10 * time.Second,
    }

    return s
}

// Start запускает сервер (блокирующий вызов)
func (s *Server) Start() error {
    slog.Info("Callback server starting", "addr", s.httpServer.Addr)
    return s.httpServer.ListenAndServe()
}

// Shutdown останавливает сервер
func (s *Server) Shutdown(ctx context.Context) error {
    return s.httpServer.Shutdown(ctx)
}

// handleCallback обрабатывает callback от Platega
func (s *Server) handleCallback(w http.ResponseWriter, r *http.Request) {
    if r.Method != http.MethodPost {
        http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
        return
    }

    // Верификация заголовков
    merchantID := r.Header.Get("X-MerchantId")
    secret := r.Header.Get("X-Secret")

    if merchantID != s.merchantID || secret != s.secret {
        slog.Warn("Callback rejected: invalid credentials",
            "merchant_id", merchantID,
            "remote_addr", r.RemoteAddr,
        )
        http.Error(w, "Unauthorized", http.StatusUnauthorized)
        return
    }

    // Чтение и парсинг тела
    body, err := io.ReadAll(io.LimitReader(r.Body, 1<<20)) // 1MB лимит
    if err != nil {
        slog.Error("Callback: failed to read body", "error", err)
        http.Error(w, "Bad request", http.StatusBadRequest)
        return
    }

    var payload platega.CallbackPayload
    if err := json.Unmarshal(body, &payload); err != nil {
        slog.Error("Callback: failed to parse JSON", "error", err, "body", string(body))
        http.Error(w, "Bad request", http.StatusBadRequest)
        return
    }

    slog.Info("Callback received",
        "transaction_id", payload.ID,
        "status", payload.Status,
        "amount", payload.Amount,
        "payload", payload.Payload,
    )

    // Обработка через handler
    if err := s.handler.HandlePaymentCallback(payload); err != nil {
        slog.Error("Callback: handler error",
            "error", err,
            "transaction_id", payload.ID,
        )
        // Возвращаем 500, чтобы Platega сделала retry
        http.Error(w, "Internal error", http.StatusInternalServerError)
        return
    }

    w.WriteHeader(http.StatusOK)
    w.Write([]byte("OK"))
}

// handleHealth — эндпоинт для проверки работоспособности
func (s *Server) handleHealth(w http.ResponseWriter, r *http.Request) {
    w.WriteHeader(http.StatusOK)
    w.Write([]byte("OK"))
}
```

### Шаг 3: Запуск сервера в `cmd/bot/main.go`

После создания бота, перед `telegramBot.Run()`:

```go
// Запуск callback-сервера (если Platega настроена)
if cfg.PlategaMerchantID != "" && cfg.PlategaSecret != "" {
    callbackHandler := telegramBot.PaymentCallbackHandler()
    callbackServer := callback.NewServer(cfg.CallbackPort, cfg.PlategaMerchantID, cfg.PlategaSecret, callbackHandler)

    go func() {
        if err := callbackServer.Start(); err != nil && err != http.ErrServerClosed {
            slog.Error("Callback server error", "error", err)
        }
    }()

    go func() {
        <-ctx.Done()
        shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
        defer cancel()
        callbackServer.Shutdown(shutdownCtx)
    }()

    slog.Info("Platega callback server started", "port", cfg.CallbackPort)
}
```

### Шаг 4: Добавить порт в docker-compose.yml

```yaml
vpn-bot:
  ports:
    - "127.0.0.1:8080:8080"
```

### Шаг 5: Написать тесты

Файл `internal/callback/server_test.go`:
- `TestCallbackVerification` — отклонение запросов с неверными заголовками (401)
- `TestCallbackValidRequest` — приём запроса с корректными заголовками (200)
- `TestCallbackHealth` — проверка /health (200)
- `TestCallbackInvalidJSON` — некорректный JSON (400)

### Шаг 6: Запустить тесты и коммит

```bash
make tests
make fmt
```

**Критерии приёмки этапа 3:**
- Callback-сервер стартует на порту 8080 при наличии PLATEGA_* переменных
- `/health` возвращает 200
- `/platega/callback` отклоняет запросы без корректных X-MerchantId/X-Secret
- Корректные callback-запросы логируются
- Без PLATEGA_* переменных бот работает как раньше

---

## Этап 4: Платёжный флоу (создание платежа, callback, активация подписки)

**Цель:** Реализовать полный цикл оплаты: создание платежа → пользователь платит → callback → продление подписки. Это ядро платёжной логики.

**Файлы:**
- Создать: `internal/bot/payment.go` — бизнес-логика платежей
- Создать: `internal/bot/payment_test.go`
- Изменить: `internal/bot/handlers.go` — добавить поля Platega-клиента и метод `PaymentCallbackHandler()`
- Изменить: `internal/remnawave/client.go` — добавить `EnableUser` и обновить `CreateUser` для лимита трафика

### Шаг 1: Обновить `remnawave/client.go`

Добавить параметр `trafficLimitBytes` в `CreateUser`:

```go
func (c *Client) CreateUser(telegramID int64, username string, expireAt time.Time, trafficLimitBytes int64) (*User, error)
```

Добавить метод `EnableUser` (реактивация после grace period):

```go
func (c *Client) EnableUser(uuid string, newExpireAt time.Time) error {
    return c.UpdateUser(uuid, UpdateUserRequest{
        Status:            strPtr(StatusActive),
        ExpireAt:          &newExpireAt,
        TrafficLimitBytes: int64Ptr(0), // Безлимит после оплаты
    })
}
```

Добавить `DisableUser` (деактивация при начале grace period):

```go
func (c *Client) DisableUser(uuid string) error {
    return c.UpdateUser(uuid, UpdateUserRequest{
        Status: strPtr(StatusDisabled),
    })
}
```

### Шаг 2: Создать `internal/bot/payment.go`

```go
package bot

import (
    "fmt"
    "log/slog"
    "strconv"
    "sync"
    "time"

    "github.com/fus1ond/vpn_bot/internal/callback"
    "github.com/fus1ond/vpn_bot/internal/database"
    "github.com/fus1ond/vpn_bot/internal/platega"
)

// paymentMu — мьютексы по telegram_id для защиты от race condition при обработке callback.
// TODO: sync.Map не чистится — за годы работы накопятся тысячи мьютексов.
// Не критично (мьютекс маленький), но при необходимости можно добавить периодическую чистку.
var paymentMu sync.Map // map[int64]*sync.Mutex

func getPaymentMutex(telegramID int64) *sync.Mutex {
    mu, _ := paymentMu.LoadOrStore(telegramID, &sync.Mutex{})
    return mu.(*sync.Mutex)
}

// paymentCallbackHandler реализует callback.PaymentHandler
type paymentCallbackHandler struct {
    bot *Bot
}

// PaymentCallbackHandler возвращает обработчик callback от Platega
func (b *Bot) PaymentCallbackHandler() callback.PaymentHandler {
    return &paymentCallbackHandler{bot: b}
}

// HandlePaymentCallback обрабатывает callback от Platega
func (h *paymentCallbackHandler) HandlePaymentCallback(payload callback.CallbackPayload) error {
    // Находим платёж по platega_transaction_id
    payment, err := h.bot.db.GetPaymentByPlategaTxID(payload.ID)
    if err != nil {
        return fmt.Errorf("get payment by tx: %w", err)
    }
    if payment == nil {
        slog.Warn("Callback for unknown transaction", "transaction_id", payload.ID)
        return nil // Не возвращаем ошибку, чтобы Platega не retry-ила
    }

    // Блокируем обработку по telegram_id
    mu := getPaymentMutex(payment.TelegramID)
    mu.Lock()
    defer mu.Unlock()

    switch payload.Status {
    case platega.StatusConfirmed:
        return h.handleConfirmed(payment)
    case platega.StatusCanceled:
        return h.handleCanceled(payment)
    case platega.StatusChargebacked:
        return h.handleChargeback(payment)
    default:
        slog.Warn("Callback with unexpected status", "status", payload.Status, "transaction_id", payload.ID)
        return nil
    }
}

// handleConfirmed обрабатывает успешный платёж
func (h *paymentCallbackHandler) handleConfirmed(payment *database.Payment) error {
    // Идемпотентность: если платёж уже confirmed — пропускаем
    if payment.Status == "confirmed" {
        slog.Info("Payment already confirmed, skipping", "payment_id", payment.ID)
        return nil
    }

    // Подтверждаем платёж в БД
    if err := h.bot.db.ConfirmPayment(payment.ID); err != nil {
        return fmt.Errorf("confirm payment: %w", err)
    }

    // Активируем подписку в Remnawave с retry и backoff (3 попытки: 30с, 1м, 5м)
    retryDelays := []time.Duration{30 * time.Second, 1 * time.Minute, 5 * time.Minute}
    var activateErr error
    for attempt, delay := range retryDelays {
        activateErr = h.activateSubscription(payment)
        if activateErr == nil {
            break
        }
        slog.Warn("Failed to activate subscription, retrying",
            "error", activateErr, "payment_id", payment.ID,
            "attempt", attempt+1, "next_retry_in", delay)
        time.Sleep(delay)
    }

    if activateErr != nil {
        // Все попытки провалились — помечаем для retry через scheduler
        slog.Error("All retry attempts failed, marking for scheduler retry",
            "error", activateErr, "payment_id", payment.ID)
        h.bot.db.UpdatePaymentStatus(payment.ID, "confirmed_not_activated")

        // Уведомляем админа
        h.bot.sendAdminAlert(fmt.Sprintf(
            "⚠️ Платёж #%d подтверждён, но не удалось активировать подписку для %d после 3 попыток. Требуется ручная проверка.",
            payment.ID, payment.TelegramID,
        ))
        return nil // Не возвращаем ошибку — платёж уже сохранён
    }

    // Создаём запись в moderator_earnings (если есть модератор)
    h.createEarningRecord(payment)

    // Уведомляем пользователя
    user, _ := h.bot.db.GetUserByTelegramID(payment.TelegramID)
    remUser, _ := h.bot.remnawave.GetUserByTelegramID(payment.TelegramID)

    var msg string
    if remUser != nil {
        expireDate := remUser.ExpireAt.Format("02.01.2006")
        msg = fmt.Sprintf("✅ Оплата прошла! Ваша подписка активна до <b>%s</b>.\n\nЛимит трафика снят — пользуйтесь без ограничений.\n\nБлиже к концу подписки мы напомним о продлении.", expireDate)
    } else {
        msg = "✅ Оплата прошла! Подписка активирована."
    }

    _ = h.bot.sendSchedulerMessage(payment.TelegramID, msg)

    // Очищаем уведомления (пользователь мог быть в grace period)
    h.bot.db.ClearNotifications(payment.TelegramID)

    _ = user // подавление unused warning

    return nil
}

// activateSubscription продлевает подписку в Remnawave
func (h *paymentCallbackHandler) activateSubscription(payment *database.Payment) error {
    user, err := h.bot.db.GetUserByTelegramID(payment.TelegramID)
    if err != nil || user == nil {
        return fmt.Errorf("user not found: telegram_id=%d", payment.TelegramID)
    }

    remUser, err := h.bot.remnawave.GetUser(user.RemnawaveUUID)
    if err != nil {
        return fmt.Errorf("get remnawave user: %w", err)
    }

    now := time.Now().UTC()
    var newExpireAt time.Time

    // Если подписка ещё активна (досрочное продление) — плюсуем к текущему expireAt
    if remUser.ExpireAt.After(now) && remUser.Status == "ACTIVE" {
        newExpireAt = remUser.ExpireAt.AddDate(0, 1, 0)
    } else {
        // Триал, grace period или истёк — считаем от момента оплаты
        newExpireAt = now.AddDate(0, 1, 0)
    }

    // Реактивируем пользователя: ставит Status=ACTIVE, ExpireAt=newExpireAt, TrafficLimitBytes=0.
    // Работает одинаково для всех случаев: первая оплата из триала (снимает лимит трафика),
    // досрочное продление (просто продлевает), восстановление после grace period (реактивирует).
    return h.bot.remnawave.EnableUser(user.RemnawaveUUID, newExpireAt)
}

// createEarningRecord создаёт запись начисления модератору
func (h *paymentCallbackHandler) createEarningRecord(payment *database.Payment) {
    if payment.ModeratorID == nil {
        return // Админский пользователь — без начислений
    }

    moderatorID := *payment.ModeratorID

    // Проверяем, что модератор ещё активен
    if !h.bot.isModerator(moderatorID) {
        return
    }

    // Считаем количество платящих клиентов для определения доли
    payingCount, err := h.bot.db.CountPayingSubscribersByModerator(moderatorID)
    if err != nil {
        slog.Error("Failed to count paying subscribers", "error", err, "moderator_id", moderatorID)
        return
    }

    sharePercent := calculateSharePercent(payingCount)

    // Определяем комиссию Platega по методу оплаты
    feePercent := h.bot.getPlategaFeePercent(payment.PaymentMethod)
    withdrawalPercent := h.bot.config.PlategaFeeWithdrawal

    grossAmount := payment.Amount
    plategaFee := grossAmount * feePercent / 100
    afterPlatega := grossAmount - plategaFee
    withdrawalFee := afterPlatega * withdrawalPercent / 100
    netAmount := afterPlatega - withdrawalFee
    shareAmount := netAmount * sharePercent / 100

    earning := &database.ModeratorEarning{
        PaymentID:     payment.ID,
        ModeratorID:   moderatorID,
        GrossAmount:   grossAmount,
        PlategaFee:    plategaFee,
        WithdrawalFee: withdrawalFee,
        NetAmount:     netAmount,
        SharePercent:  sharePercent,
        ShareAmount:   shareAmount,
    }

    if _, err := h.bot.db.CreateEarning(earning); err != nil {
        slog.Error("Failed to create earning record", "error", err, "payment_id", payment.ID)
    }
}

// calculateSharePercent определяет долю модератора по количеству платящих клиентов
func calculateSharePercent(payingCount int) int {
    switch {
    case payingCount >= 25:
        return 25
    case payingCount >= 15:
        return 20
    default:
        return 15
    }
}

// getPlategaFeePercent возвращает процент комиссии Platega для метода оплаты
func (b *Bot) getPlategaFeePercent(paymentMethod string) int {
    switch paymentMethod {
    case "sbp":
        return b.config.PlategaFeeSBP
    case "card":
        return b.config.PlategaFeeCard
    case "crypto":
        return b.config.PlategaFeeCrypto
    default:
        return b.config.PlategaFeeSBP // Fallback
    }
}

// handleCanceled обрабатывает отменённый платёж
func (h *paymentCallbackHandler) handleCanceled(payment *database.Payment) error {
    if payment.Status != "pending" {
        return nil
    }
    if err := h.bot.db.UpdatePaymentStatus(payment.ID, "canceled"); err != nil {
        return fmt.Errorf("update status to canceled: %w", err)
    }
    _ = h.bot.sendSchedulerMessage(payment.TelegramID, "❌ Платёж отменён. Вы можете попробовать снова.")
    return nil
}

// handleChargeback обрабатывает chargeback
func (h *paymentCallbackHandler) handleChargeback(payment *database.Payment) error {
    if err := h.bot.db.UpdatePaymentStatus(payment.ID, "chargebacked"); err != nil {
        return fmt.Errorf("update status to chargebacked: %w", err)
    }

    // Деактивируем пользователя
    user, err := h.bot.db.GetUserByTelegramID(payment.TelegramID)
    if err == nil && user != nil {
        _ = h.bot.remnawave.DisableUser(user.RemnawaveUUID)
    }

    // Уведомляем админа
    h.bot.sendAdminAlert(fmt.Sprintf(
        "⚠️ Chargeback от %d, сумма: %d руб. Пользователь деактивирован.",
        payment.TelegramID, payment.Amount,
    ))

    return nil
}

// sendAdminAlert отправляет сообщение админу
func (b *Bot) sendAdminAlert(msg string) {
    _ = b.sendSchedulerMessage(b.config.AdminID, msg)
}

// createPaymentForUser создаёт платёж для пользователя
func (b *Bot) createPaymentForUser(telegramID int64, paymentMethodInt int) (*database.Payment, string, error) {
    user, err := b.db.GetUserByTelegramID(telegramID)
    if err != nil || user == nil {
        return nil, "", fmt.Errorf("user not found")
    }

    if user.SubscriptionPrice == nil {
        return nil, "", fmt.Errorf("subscription price not set")
    }

    // Проверка лимита 90 дней: нельзя оплатить, если до конца подписки >= 90 дней
    remUser, err := b.remnawave.GetUserByTelegramID(telegramID)
    if err == nil && remUser != nil && remUser.Status == "ACTIVE" && remUser.ExpireAt.Year() < 2099 {
        daysLeft := int(time.Until(remUser.ExpireAt).Hours() / 24)
        if daysLeft >= 90 {
            return nil, "", fmt.Errorf("subscription_too_far: %d days left", daysLeft)
        }
    }

    paymentMethodStr := platega.PaymentMethodString(paymentMethodInt)

    // Проверяем наличие активного PENDING платежа
    pending, err := b.db.GetPendingPayment(telegramID)
    if err != nil {
        return nil, "", fmt.Errorf("check pending: %w", err)
    }

    if pending != nil {
        if pending.PaymentMethod == paymentMethodStr {
            // Тот же способ — возвращаем ту же ссылку
            url := ""
            if pending.RedirectURL != nil {
                url = *pending.RedirectURL
            }
            return pending, url, nil
        }
        // Другой способ — помечаем старый как expired
        b.db.UpdatePaymentStatus(pending.ID, "expired")
    }

    // Создаём платёж в Platega
    callbackURL := b.config.PlategaCallbackURL
    telegramIDStr := strconv.FormatInt(telegramID, 10)

    resp, err := b.platega.CreatePayment(platega.CreateTransactionRequest{
        PaymentMethod: paymentMethodInt,
        Amount:        *user.SubscriptionPrice,
        Currency:      "RUB",
        Description:   "VPN подписка на 1 месяц",
        ReturnURL:     fmt.Sprintf("https://t.me/%s", b.bot.Me.Username),
        FailedURL:     fmt.Sprintf("https://t.me/%s", b.bot.Me.Username),
        CallbackURL:   callbackURL,
        Payload:       telegramIDStr,
    })
    if err != nil {
        return nil, "", fmt.Errorf("platega create payment: %w", err)
    }

    // Вычисляем время жизни
    var expiresAt *time.Time
    if resp.ExpiresIn > 0 {
        t := time.Now().Add(time.Duration(resp.ExpiresIn) * time.Second)
        expiresAt = &t
    }

    // Сохраняем в БД
    payment := &database.Payment{
        TelegramID:           telegramID,
        ModeratorID:          user.ModeratorID,
        Amount:               *user.SubscriptionPrice,
        PaymentMethod:        paymentMethodStr,
        Status:               "pending",
        PlategaTransactionID: &resp.TransactionID,
        RedirectURL:          &resp.Redirect,
        ExpiresAt:            expiresAt,
    }

    id, err := b.db.CreatePayment(payment)
    if err != nil {
        return nil, "", fmt.Errorf("save payment: %w", err)
    }
    payment.ID = id

    return payment, resp.Redirect, nil
}

// checkPaymentStatus ручная проверка статуса платежа через Platega API.
// Защищён мьютексом по telegram_id для предотвращения race condition
// с параллельным callback от Platega.
func (b *Bot) checkPaymentStatus(telegramID int64) (string, error) {
    // Берём мьютекс ДО чтения из БД — та же блокировка, что и в callback
    mu := getPaymentMutex(telegramID)
    mu.Lock()
    defer mu.Unlock()

    // Попутно помечаем протухшие PENDING как expired (не ждём scheduler)
    b.db.ExpireOldPendingPayments()

    pending, err := b.db.GetPendingPayment(telegramID)
    if err != nil {
        return "", fmt.Errorf("get pending: %w", err)
    }
    if pending == nil {
        return "not_found", nil
    }
    if pending.PlategaTransactionID == nil {
        return "pending", nil
    }

    status, err := b.platega.GetTransactionStatus(*pending.PlategaTransactionID)
    if err != nil {
        return "", fmt.Errorf("check status: %w", err)
    }

    if status.Status == platega.StatusConfirmed {
        // Платёж подтверждён — обрабатываем как callback (мьютекс уже взят)
        handler := &paymentCallbackHandler{bot: b}
        handler.handleConfirmed(pending)
        return "confirmed", nil
    }

    return status.Status, nil
}
```

### Шаг 3: Обновить структуру `Bot` в `handlers.go`

Добавить поля:

```go
platega          *platega.Client          // Platega API клиент (nil если не настроен)
maintenanceMode  bool                     // Режим обслуживания (не персистится — сбрасывается при перезапуске, это ОК: включается осознанно перед обновлением)
```

В функции `New()` — инициализация Platega-клиента:

```go
if cfg.PlategaMerchantID != "" && cfg.PlategaSecret != "" {
    bot.platega = platega.NewClient(cfg.PlategaMerchantID, cfg.PlategaSecret)
    slog.Info("Platega client initialized")
}
```

### Шаг 4: Написать тесты

Файл `internal/bot/payment_test.go`:
- `TestCalculateSharePercent` — проверка шкалы долей (15%, 20%, 25%)
- `TestGetPlategaFeePercent` — проверка комиссий по методам оплаты
- `TestHandleConfirmedIdempotency` — повторный callback не дублирует обработку

### Шаг 5: Запустить тесты и коммит

```bash
make tests
make fmt
```

**Критерии приёмки этапа 4:**
- Полный цикл: createPayment → Platega API → callback → confirm → activateSubscription
- Защита от двойных платежей (PENDING с тем же/другим способом)
- Chargeback деактивирует пользователя + алерт админу
- confirmed_not_activated при недоступности Remnawave + алерт
- Race condition защищён мьютексом по telegram_id
- Все тесты проходят

---

## Этап 5: Переработка scheduler (event-driven)

**Цель:** Перевести scheduler с модели "раз в день в 12:00" на "каждые 30 минут с полным проходом при старте". Добавить логику триала, grace period, disable, retry confirmed_not_activated. Режим обслуживания.

**Файлы:**
- Изменить: `internal/bot/scheduler.go` — полная переработка
- Изменить: `internal/bot/scheduler_test.go` (если есть, или создать)
- Изменить: `internal/database/notifications.go` — добавить новые типы уведомлений

### Шаг 1: Обновить константы уведомлений

```go
const (
    // Триал
    notificationTrialExpire1d = "trial_expire_1d"    // За 1 день до конца триала
    notificationTrialExpired  = "trial_expired"       // Триал истёк

    // Оплаченная подписка
    notificationExpire3d      = "expire_3d"           // За 3 дня до конца
    notificationExpire1d      = "expire_1d"           // За 1 день до конца
    notificationExpired       = "expired"             // Подписка истекла (начало grace)

    // Grace period
    notificationGraceKick     = "grace_kick"          // Кик после grace period
)
```

### Шаг 2: Переработать `StartScheduler`

```go
func (b *Bot) StartScheduler(ctx context.Context) {
    // Первый проход при старте — не ждём 30 минут
    slog.Info("Scheduler: running initial pass on startup")
    b.runSubscriptionSchedulerPass()

    ticker := time.NewTicker(30 * time.Minute)
    defer ticker.Stop()

    slog.Info("Subscription scheduler started", "interval", "30m")

    for {
        select {
        case <-ctx.Done():
            slog.Info("Subscription scheduler stopped")
            return
        case <-ticker.C:
            b.runSubscriptionSchedulerPass()
        }
    }
}
```

### Шаг 3: Переработать `runSubscriptionSchedulerPass`

Новая логика:

1. Протухание старых PENDING платежей: `b.db.ExpireOldPendingPayments()`
2. Retry confirmed_not_activated платежей: `b.retryConfirmedNotActivated()`
3. Для каждого пользователя:
   - Бесконечная подписка (expireAt >= 2099) → пропуск
   - Определить тип подписки через `b.isTrialUser(telegramID)`
   - **Триал:**
     - За 1 день до expireAt → уведомление `notificationTrialExpire1d`
     - При expireAt (now >= expireAt) → если НЕ режим обслуживания → кик (удаление из Remnawave + БД, без grace period)
   - **Оплаченная подписка:**
     - За 3 дня до expireAt → уведомление `notificationExpire3d`
     - За 1 день до expireAt → уведомление `notificationExpire1d`
     - При expireAt (now >= expireAt) → если НЕ режим обслуживания → disable в Remnawave (не удалять!), уведомление `notificationExpired` о начале grace period
     - **Grace period кик (expireAt + 3 дня):** если `now >= expireAt + 72h` → если НЕ режим обслуживания → проверить нет ли confirmed payment с даты expireAt (пользователь мог оплатить) → если нет → кик (удаление из Remnawave + БД)
   - **Защита от ложного кика:** перед любым киком/disable проверяем `HasConfirmedPayment` с даты истечения — если есть confirmed платёж новее expireAt, пропускаем (callback уже обработал)

```go
// Ключевая проверка grace period кика:
graceDeadline := remUser.ExpireAt.Add(72 * time.Hour)
if time.Now().UTC().After(graceDeadline) && !b.maintenanceMode {
    // Проверяем, не оплатил ли пользователь во время grace period
    // (callback мог прийти, но scheduler ещё не видел обновлённый expireAt)
    freshUser, err := b.remnawave.GetUser(dbUser.RemnawaveUUID)
    if err == nil && freshUser.Status == "ACTIVE" && freshUser.ExpireAt.After(time.Now().UTC()) {
        continue // Пользователь оплатил — пропускаем
    }
    b.handleAutoKick(telegramID, dbUser.RemnawaveUUID)
}
```

### Шаг 4: Определение типа подписки

```go
// isTrialUser проверяет, находится ли пользователь на триале.
// Триальный = приглашён модераторским инвайтом (expire_days != NULL) И ни разу не платил.
// Пользователи, созданные админским инвайтом (expire_days = NULL), НЕ считаются триальными
// — у них бесконечная подписка, другая логика.
func (b *Bot) isTrialUser(telegramID int64) bool {
    // Проверяем инвайт — должен быть модераторский (expire_days != NULL)
    invite, err := b.db.GetInviteByUsedBy(telegramID)
    if err != nil || invite == nil || invite.ExpireDays == nil {
        return false // Админский инвайт или нет инвайта — не триал
    }

    // Проверяем, была ли оплата
    hasPaid, err := b.db.HasConfirmedPayment(telegramID)
    if err != nil {
        return false
    }
    return !hasPaid
}
```

### Шаг 5: Написать тесты

- `TestSchedulerTrialExpire` — триал: уведомление за 1 день → кик при expireAt
- `TestSchedulerPaidGracePeriod` — оплаченная: уведомления → disable → кик через 3 дня
- `TestSchedulerMaintenanceMode` — в режиме обслуживания не кикает и не disable-ит
- `TestSchedulerRetryConfirmedNotActivated` — retry подтверждённых но не активированных

### Шаг 6: Запустить тесты и коммит

```bash
make tests
make fmt
```

**Критерии приёмки этапа 5:**
- Scheduler запускается каждые 30 минут + при старте бота
- Триал: уведомление за 1 день → кик при expireAt (без grace period)
- Оплаченная: уведомления за 3д/1д → disable при expireAt → кик через 3 дня
- Режим обслуживания блокирует кик и disable
- confirmed_not_activated ретраятся
- Существующие пользователи не ломаются

---

## Этап 6: UI пользователя (статус, оплата, динамические кнопки)

**Цель:** Переработать пользовательский интерфейс: динамические кнопки (оплата/продление), обновлённый "Мой статус", флоу оплаты (выбор способа → создание платежа → ожидание → результат).

**Файлы:**
- Изменить: `internal/bot/keyboards.go` — новые кнопки и раскладки
- Изменить: `internal/bot/messages.go` — новые UX-тексты, переработка `FormatUserStatus`
- Изменить: `internal/bot/handlers.go` — новые состояния, обработчики оплаты
- Создать: `internal/bot/payment_handler.go` — обработчики UI оплаты

### Шаг 1: Обновить кнопки в `keyboards.go`

Удалить: `BtnConnect`, `BtnDonate`.

Добавить:

```go
// Кнопки оплаты
BtnPay           = "💳 Оплатить подписку"
BtnRenew         = "💳 Продлить подписку"
BtnPaySBP        = "🏦 СБП"
BtnPayCard       = "💳 Карта"
BtnPayCrypto     = "🪙 Крипта"
BtnCheckPayment  = "🔄 Проверить оплату"

// Кнопка информации (переименование)
BtnInfo          = "ℹ️ Информация"
```

Новая функция `UserMenuKeyboardDynamic`:

```go
// UserMenuKeyboardDynamic строит главное меню с динамической кнопкой оплаты.
// isModerator — добавляет кнопку "🎟 Приглашения" для модераторов.
func UserMenuKeyboardDynamic(payButtonText string, showPayButton bool, isModerator bool) *tele.ReplyMarkup {
    menu := &tele.ReplyMarkup{ResizeKeyboard: true}
    rows := []tele.Row{
        menu.Row(menu.Text(BtnStatus)),
    }
    if showPayButton && payButtonText != "" {
        rows = append(rows, menu.Row(menu.Text(payButtonText), menu.Text(BtnServers)))
    } else {
        rows = append(rows, menu.Row(menu.Text(BtnServers)))
    }
    rows = append(rows, menu.Row(menu.Text(BtnInstructions), menu.Text(BtnInfo)))
    if isModerator {
        rows = append(rows, menu.Row(menu.Text(BtnModInvites)))
    }
    menu.Reply(rows...)
    return menu
}
```

Это заменяет как `UserMenuKeyboard()`, так и `UserMenuKeyboardModerator()` — одна функция вместо трёх.

`PaymentMethodKeyboard`:

```go
func PaymentMethodKeyboard() *tele.ReplyMarkup {
    menu := &tele.ReplyMarkup{ResizeKeyboard: true}
    menu.Reply(
        menu.Row(menu.Text(BtnPaySBP), menu.Text(BtnPayCard)),
        menu.Row(menu.Text(BtnPayCrypto), menu.Text(BtnCancel)),
    )
    return menu
}
```

`PaymentWaitKeyboard`:

```go
func PaymentWaitKeyboard() *tele.ReplyMarkup {
    menu := &tele.ReplyMarkup{ResizeKeyboard: true}
    menu.Reply(
        menu.Row(menu.Text(BtnCheckPayment), menu.Text(BtnCancel)),
    )
    return menu
}
```

### Шаг 2: Обновить `FormatUserStatus` в `messages.go`

Полная переработка с учётом типов подписки (триал, оплаченная, grace period, бесконечная).

Входные данные: remnawave.User + database.User (для цены, типа).

Добавить вспомогательную функцию `determineSubscriptionType`:

```go
type subscriptionType int

const (
    subTypeTrial    subscriptionType = iota // Триал
    subTypePaid                              // Оплаченная подписка
    subTypeGrace                             // Grace period (disabled + не кикнут)
    subTypeInfinite                          // Бесконечная (expireAt >= 2099)
)
```

### Шаг 3: Добавить состояния и обработчики оплаты

Новые состояния:

```go
StateWaitPaymentMethod = "wait_payment_method"  // Ожидание выбора способа оплаты
StateWaitPaymentResult = "wait_payment_result"  // Ожидание оплаты (показана ссылка)
```

В `handleTextMessage` добавить обработку:
- `BtnPay` / `BtnRenew` → показать экран выбора способа оплаты
- `BtnPaySBP` / `BtnPayCard` / `BtnPayCrypto` → создать платёж
- `BtnCheckPayment` → проверить статус через API
- `BtnStatus` → переработанный статус

### Шаг 4: Создать `internal/bot/payment_handler.go`

```go
package bot

// handlePayButton обрабатывает нажатие "Оплатить/Продлить"
func (b *Bot) handlePayButton(c tele.Context) error {
    // Проверка режима обслуживания
    // Проверка лимита 90 дней
    // Проверка наличия цены
    // Показ экрана выбора способа оплаты
}

// handlePaymentMethodSelected обрабатывает выбор способа оплаты
func (b *Bot) handlePaymentMethodSelected(c tele.Context, methodInt int) error {
    // Создание платежа через createPaymentForUser
    // Отправка ссылки
    // Установка состояния StateWaitPaymentResult
}

// handleCheckPayment обрабатывает кнопку "Проверить оплату"
func (b *Bot) handleCheckPayment(c tele.Context) error {
    // Вызов checkPaymentStatus
    // Если confirmed — показ подтверждения
    // Если pending — "Оплата пока не поступила"
}
```

### Шаг 5: Обновить `handleStart`

Для зарегистрированного пользователя в grace period — показать тревожный экран. Динамическая кнопка оплаты на основании типа подписки.

### Шаг 6: Обновить обработку активации инвайта

При `processInviteCode`:
- Создавать пользователя с `trafficLimitBytes = TrialTrafficLimitGB * 1024^3`
- Устанавливать `expireAt = now + 72h`
- Копировать `subscription_price` из инвайта в users
- Устанавливать `moderator_id` из инвайта `created_by` (если модератор)

### Шаг 7: Запустить тесты и коммит

```bash
make tests
make fmt
```

**Критерии приёмки этапа 6:**
- Главное меню показывает динамическую кнопку оплаты
- "Мой статус" показывает тип подписки, трафик, цену, устройства
- Флоу оплаты работает: выбор способа → ссылка → проверка
- Кнопка оплаты скрыта для бесконечных подписок и пользователей без цены
- Grace period показывает тревожный экран при /start
- Кнопки "Подключить" и "Поддержать" удалены

---

## Этап 7: UI модератора (создание инвайта с ценой, подписчики, заработок)

**Цель:** Переработать интерфейс модератора: при создании инвайта запрашивать цену, обогатить список подписчиков, добавить "Мой заработок" и "Изменить цену".

**Файлы:**
- Изменить: `internal/bot/moderator.go` — переработка хендлеров
- Изменить: `internal/bot/keyboards.go` — новые кнопки модератора

### Шаг 1: Обновить кнопки модератора

Удалить: `BtnModExtend` (модератор больше не продлевает вручную).

Добавить:

```go
BtnModEarnings    = "💰 Мой заработок"
BtnModChangePrice = "✏️ Изменить цену"
```

Обновить `ModeratorMenuKeyboard`:

```go
func ModeratorMenuKeyboard() *tele.ReplyMarkup {
    menu := &tele.ReplyMarkup{ResizeKeyboard: true}
    menu.Reply(
        menu.Row(menu.Text(BtnModCreate)),
        menu.Row(menu.Text(BtnModView), menu.Text(BtnModSubscribers)),
        menu.Row(menu.Text(BtnModEarnings), menu.Text(BtnModDelete)),
        menu.Row(menu.Text(BtnModBack)),
    )
    return menu
}
```

### Шаг 2: Переработать создание инвайта

Новый флоу:
1. Модератор нажимает "Создать приглашение"
2. Бот: "Введите цену подписки (руб/мес). Минимум: {MIN_SUBSCRIPTION_PRICE} руб."
3. Модератор вводит число → валидация → создание инвайта с ценой

Новые состояния:

```go
StateWaitModInvitePrice = "wait_mod_invite_price"
```

### Шаг 3: Обогатить список подписчиков

Показывать тип подписки (триал/оплачено/grace/истёк), дату, цену. Добавить кнопку "Изменить цену".

### Шаг 4: Добавить "Мой заработок"

```go
func (b *Bot) handleModeratorEarnings(c tele.Context) error {
    // Получить данные из moderator_earnings за текущий месяц и за всё время
    // Показать: платящих клиентов, долю, суммы комиссий, чистый доход, долю модератора
}
```

### Шаг 5: Добавить "Изменить цену"

Модератор может менять цену только триальным клиентам (до первой оплаты). Валидация: свой подписчик, на триале, цена >= MIN_SUBSCRIPTION_PRICE.

Новые состояния:

```go
StateWaitModChangePriceID    = "wait_mod_change_price_id"
StateWaitModChangePriceValue = "wait_mod_change_price_value"
```

### Шаг 6: Удалить логику продления

Удалить: `handleModExtend`, `processModExtendID`, `processModExtendConfirm`, `modExtendSessionTimeout`. Удалить поля `modExtendMu`, `modExtendData` из структуры `Bot`.

### Шаг 7: Запустить тесты и коммит

```bash
make tests
make fmt
```

**Критерии приёмки этапа 7:**
- Модератор при создании инвайта указывает цену
- Список подписчиков показывает тип, дату, цену
- "Мой заработок" показывает финансовую статистику
- Модератор может изменить цену триальным клиентам
- Кнопка "Продлить подписку" убрана из модераторского меню

---

## Этап 8: UI админа (статистика, инфо, обслуживание, цена)

**Цель:** Добавить "Общую статистику", "Инфо о пользователе", "Режим обслуживания", подменю "Сменить тариф" с опцией "Изменить цену".

**Файлы:**
- Изменить: `internal/bot/admin.go` — новые хендлеры
- Изменить: `internal/bot/keyboards.go` — новые кнопки админа

### Шаг 1: Обновить кнопки админа

Добавить:

```go
BtnAdminStats            = "📊 Общая статистика"
BtnAdminMaintenance      = "🔧 Режим обслуживания"
BtnAdminMaintenanceOff   = "▶️ Штатный режим"
BtnAdminUserInfo         = "🔍 Инфо о пользователе"
BtnAdminSwitchInfinite   = "♾️ Перевести на бессрочную"
BtnAdminChangePrice      = "✏️ Изменить цену"
```

Обновить `AdminKeyboard`:

```go
func AdminKeyboard(maintenanceMode bool) *tele.ReplyMarkup {
    menu := &tele.ReplyMarkup{ResizeKeyboard: true}
    maintenanceBtn := BtnAdminMaintenance
    if maintenanceMode {
        maintenanceBtn = BtnAdminMaintenanceOff
    }
    menu.Reply(
        menu.Row(menu.Text(BtnAdminManage), menu.Text(BtnAdminModerators)),
        menu.Row(menu.Text(BtnAdminBroadcast), menu.Text(BtnAdminStats)),
        menu.Row(menu.Text(maintenanceBtn)),
        menu.Row(menu.Text(BtnAdminUserMode)),
    )
    return menu
}
```

Обновить `AdminManageKeyboard` — добавить `BtnAdminUserInfo`.

Добавить `AdminSwitchSubmenu`:

```go
func AdminSwitchSubmenu() *tele.ReplyMarkup {
    menu := &tele.ReplyMarkup{ResizeKeyboard: true}
    menu.Reply(
        menu.Row(menu.Text(BtnAdminSwitchInfinite)),
        menu.Row(menu.Text(BtnAdminChangePrice)),
        menu.Row(menu.Text(BtnAdminBack)),
    )
    return menu
}
```

### Шаг 2: Добавить "Общую статистику"

```go
func (b *Bot) handleAdminStats(c tele.Context) error {
    // Финансы: платежей, сумма, комиссии, чистый доход, выплаты модераторам, доход владельца
    // Пользователи: всего, платящих, триал, grace, бессрочных
    // Конверсия: первые оплаты / триалы за месяц
}
```

### Шаг 3: Добавить "Статистику модераторов" (обновить существующую)

Для каждого модератора — финансовая сводка за прошлый завершённый месяц. Данные из `moderator_earnings`.

### Шаг 4: Добавить "Инфо о пользователе"

```go
StateWaitAdminUserInfo = "wait_admin_user_info"
```

Показывает: имя, куратор, цена, подписка до, трафик, устройства, тип, статус.

### Шаг 5: Добавить "Режим обслуживания"

Переключатель `maintenanceMode` в структуре `Bot`. При включении: скрыть кнопку оплаты, scheduler не кикает/не disable-ит.

### Шаг 6: Переработать "Сменить тариф"

Текущий "Сменить тариф" → подменю с двумя опциями:
- "Перевести на бессрочную" — существующая логика
- "Изменить цену" — админ вводит telegram_id → новую цену → уведомление пользователю

Новые состояния:

```go
StateWaitAdminChangePriceID    = "wait_admin_change_price_id"
StateWaitAdminChangePriceValue = "wait_admin_change_price_value"
```

### Шаг 7: Запустить тесты и коммит

```bash
make tests
make fmt
```

**Критерии приёмки этапа 8:**
- "Общая статистика" показывает финансы и операционку
- "Инфо о пользователе" показывает полную карточку
- "Режим обслуживания" скрывает оплату и останавливает кики
- "Сменить тариф" имеет подменю с бессрочной и изменением цены
- Статистика модераторов показывает финансы за прошлый месяц

---

## Этап 9: Финализация и интеграция

**Цель:** Связать все компоненты, обновить docker-compose, добавить переменные окружения, протестировать e2e.

**Файлы:**
- Изменить: `docker-compose.yml` — проброс порта callback
- Изменить: `Dockerfile` — без изменений (binary тот же)
- Изменить: `cmd/bot/main.go` — финальная интеграция
- Обновить: `CLAUDE.md` — описание новой архитектуры

### Шаг 1: Обновить `docker-compose.yml`

```yaml
vpn-bot:
  ports:
    - "127.0.0.1:8080:8080"  # Callback-сервер для Platega
  environment:
    # ... существующие ...
    - PLATEGA_MERCHANT_ID=${PLATEGA_MERCHANT_ID}
    - PLATEGA_SECRET=${PLATEGA_SECRET}
    - PLATEGA_CALLBACK_URL=${PLATEGA_CALLBACK_URL}
    - CALLBACK_PORT=${CALLBACK_PORT:-8080}
    - MIN_SUBSCRIPTION_PRICE=${MIN_SUBSCRIPTION_PRICE:-400}
    - TRIAL_TRAFFIC_LIMIT_GB=${TRIAL_TRAFFIC_LIMIT_GB:-1}
    - PLATEGA_FEE_SBP=${PLATEGA_FEE_SBP:-11}
    - PLATEGA_FEE_CARD=${PLATEGA_FEE_CARD:-12}
    - PLATEGA_FEE_CRYPTO=${PLATEGA_FEE_CRYPTO:-5}
    - PLATEGA_FEE_WITHDRAWAL=${PLATEGA_FEE_WITHDRAWAL:-2}
```

### Шаг 2: Проверить обратную совместимость

- Бот без PLATEGA_* переменных работает как раньше
- Существующие пользователи с NULL subscription_price не видят кнопку оплаты
- Scheduler для триальных пользователей без инвайта (старые) — пропускает

### Шаг 3: Полное тестирование

```bash
make tests
make fmt
```

### Шаг 4: Обновить CLAUDE.md

Добавить новые компоненты в описание архитектуры, переменные окружения, таблицы.

**Критерии приёмки этапа 9:**
- `make tests` — все тесты проходят
- `make fmt` — без ошибок
- Бот запускается и работает с Platega-переменными
- Бот запускается и работает без Platega-переменных (обратная совместимость)
- Docker compose поддерживает проброс callback-порта

---

## Порядок реализации и зависимости

```
Этап 1 (БД)
    ↓
Этап 2 (Platega-клиент)    ← параллельно с Этапом 1
    ↓
Этап 3 (Callback-сервер)   ← зависит от Этапа 2
    ↓
Этап 4 (Платёжный флоу)    ← зависит от Этапов 1, 2, 3
    ↓
Этап 5 (Scheduler)         ← зависит от Этапа 4
    ↓
Этап 6 (UI пользователя)   ← зависит от Этапов 4, 5
    ↓
Этап 7 (UI модератора)     ← зависит от Этапа 6
    ↓
Этап 8 (UI админа)         ← зависит от Этапов 6, 7
    ↓
Этап 9 (Финализация)       ← зависит от всех этапов
```

Этапы 1 и 2 можно реализовывать **параллельно**.
