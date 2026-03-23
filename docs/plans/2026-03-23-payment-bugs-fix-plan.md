# Payment Bugs Fix Plan

> **For Claude:** REQUIRED SUB-SKILL: Use superpowers:executing-plans to implement this plan task-by-task.

**Goal:** Устранить 4 найденных проблемы в реализации платёжной системы.

**Architecture:** Все правки изолированы — каждый таск независим. Таск 1 меняет `admin.go` (финансовая статистика). Таск 2 меняет `admin.go` (формула конверсии). Таск 3 и 4 — мелкие защитные правки в `payment.go` и `payment_handler.go`.

**Tech Stack:** Go, SQLite, telebot.v3, testify

---

## Проблемы для устранения

| # | Проблема | Критичность |
|---|----------|-------------|
| 1 | `handleAdminStats` пересчитывает финансы из payments вместо earnings | Средняя |
| 2 | Формула конверсии `(firstPayments*100 + trialsThisMonth/2) / trialsThisMonth` — неверная | Средняя |
| 3 | Нет валидации `amount > 0` перед созданием платежа | Низкая |
| 4 | graceCount считает `!remUser.ExpireAt.After(now)` — это считает истёкших, а не grace | Низкая |

---

## Task 1: Заменить пересчёт финансов в handleAdminStats на GetAllEarningsByMonth

**Проблема:** `handleAdminStats` вручную суммирует комиссии по каждому payment, применяя **текущие** ставки комиссий. Но при изменении конфига `PLATEGA_FEE_*` статистика за прошлые месяцы будет врать. Правильный источник — таблица `moderator_earnings`, где всё уже посчитано в момент транзакции.

**Что менять:** `internal/bot/admin.go`, функция `handleAdminStats`.

**Текущий код (строки 645–660):**
```go
confirmedPayments, err := b.db.GetConfirmedPaymentsByMonth(year, month)
// ...
monthEarnings := &database.MonthlyEarnings{}
for _, payment := range confirmedPayments {
    plategaFee, withdrawalFee, netAmount := b.calculateMonthlyPaymentFinance(payment)
    monthEarnings.TotalPayments++
    monthEarnings.GrossAmount += payment.Amount
    monthEarnings.TotalPlategaFee += plategaFee
    monthEarnings.TotalWithdrawal += withdrawalFee
    monthEarnings.TotalNetAmount += netAmount
    monthEarnings.TotalShareAmount += payment.ShareAmount
}
```

**Правильный код:**
```go
monthEarnings, err := b.db.GetAllEarningsByMonth(year, month)
if err != nil {
    slog.Error("Failed to load monthly earnings for admin stats", "error", err)
    return c.Send("Ошибка получения статистики", &tele.SendOptions{ReplyMarkup: AdminKeyboard(b.maintenanceMode)})
}
```

**Замечание:** После этой замены переменная `confirmedPayments` больше не нужна. Также функция `calculateMonthlyPaymentFinance` становится неиспользуемой — удалить её вместе с вызовом.

**Тесты:** В `internal/bot/admin_test.go` найти или добавить тест `TestHandleAdminStats`. Проверить что:
1. Вызывается `GetAllEarningsByMonth`, а не `GetConfirmedPaymentsByMonth` (через mock или проверку результата)
2. Итог отображает правильные суммы из earnings

**Файлы:**
- Modify: `internal/bot/admin.go:636-660`
- Test: `internal/bot/admin_test.go`

### Шаги

**Шаг 1: Написать тест (TDD)**

В `internal/bot/admin_test.go` добавить тест, который создаёт платёж + earning и проверяет, что статистика берётся из earnings:

```go
func TestHandleAdminStatsUsesEarningsTable(t *testing.T) {
    b, db := newTestBot(t)
    // создаём пользователя
    telegramID := int64(1001)
    _ = db.CreateUser(&database.User{TelegramID: telegramID, RemnawaveUUID: "uuid-1"})

    // создаём payment
    paymentID, _ := db.CreatePayment(&database.Payment{
        TelegramID:    telegramID,
        Amount:        1000,
        PaymentMethod: "sbp",
        Status:        "confirmed",
    })
    now := time.Now().UTC()
    db.ConfirmPayment(paymentID, "tx-1")

    // создаём earning с заранее известными суммами
    _, _ = db.CreateEarning(&database.ModeratorEarning{
        PaymentID:     paymentID,
        ModeratorID:   999,
        GrossAmount:   1000,
        PlategaFee:    110,
        WithdrawalFee: 18,
        NetAmount:     872,
        SharePercent:  15,
        ShareAmount:   130,
    })

    // вызываем handleAdminStats
    resp := callAdminStats(b, t) // хелпер из testutil

    // проверяем что в ответе фигурируют правильные суммы из earnings
    assert.Contains(t, resp, "1000") // GrossAmount
    assert.Contains(t, resp, "110")  // PlategaFee
    assert.Contains(t, resp, "872")  // NetAmount
    assert.Contains(t, resp, "130")  // ShareAmount
    _ = now
}
```

Запустить: `make tests` — тест должен **падать** (если функциональность ещё не исправлена).

**Шаг 2: Заменить реализацию**

В `internal/bot/admin.go` строки 645–660 заменить пересчёт на:
```go
monthEarnings, err := b.db.GetAllEarningsByMonth(year, month)
if err != nil {
    slog.Error("Failed to load monthly earnings for admin stats", "error", err)
    return c.Send("Ошибка получения статистики", &tele.SendOptions{ReplyMarkup: AdminKeyboard(b.maintenanceMode)})
}
```

Удалить больше неиспользуемые:
- Переменную `confirmedPayments` и весь цикл (строки 645–660)
- Импорт, который перестал использоваться (если есть)
- Функцию `calculateMonthlyPaymentFinance` (строки 621–633) — она больше нигде не нужна

**Шаг 3: Проверить что компилируется и тесты проходят**

```bash
make fmt
make tests
```

**Шаг 4: Коммит**

```bash
git add internal/bot/admin.go internal/bot/admin_test.go
git commit -m "fix: использовать earnings table в статистике админа вместо пересчёта"
```

---

## Task 2: Исправить формулу конверсии

**Проблема:** Текущая формула:
```go
conversion = (firstPayments*100 + trialsThisMonth/2) / trialsThisMonth
```
Это «округление через добавление половины знаменателя» — но по сути дела неправильная метрика. По плану конверсия = `firstPayments * 100 / trialsThisMonth`.

**Что менять:** `internal/bot/admin.go`, строка 720.

**Правильный код:**
```go
conversion = firstPayments * 100 / trialsThisMonth
```

**Файлы:**
- Modify: `internal/bot/admin.go:718-721`
- Test: `internal/bot/admin_test.go`

### Шаги

**Шаг 1: Написать тест**

В `internal/bot/admin_test.go`:
```go
func TestConversionCalculation(t *testing.T) {
    // 3 триала, 1 первая оплата → конверсия 33%
    trials := 3
    first := 1
    result := first * 100 / trials
    assert.Equal(t, 33, result)

    // 10 триалов, 5 оплат → 50%
    assert.Equal(t, 50, 5*100/10)

    // 0 триалов → деление не происходит (защита в коде)
}
```

Если в admin.go логика конверсии не выделена в функцию, достаточно просто убедиться что правило верное и тест проходит после правки.

**Шаг 2: Исправить формулу**

В `internal/bot/admin.go` строка 720:
```go
// Было:
conversion = (firstPayments*100 + trialsThisMonth/2) / trialsThisMonth
// Стало:
conversion = firstPayments * 100 / trialsThisMonth
```

**Шаг 3: Проверить**

```bash
make fmt
make tests
```

**Шаг 4: Коммит**

```bash
git add internal/bot/admin.go internal/bot/admin_test.go
git commit -m "fix: исправить формулу конверсии триал → оплата"
```

---

## Task 3: Добавить валидацию amount > 0 при создании платежа

**Проблема:** `createPaymentForUser` не проверяет что `amount > 0` перед отправкой в Platega API.

**Где это:** `internal/bot/payment.go`, функция `createPaymentForUser`.

**Найти функцию:**
```bash
grep -n "createPaymentForUser\|func.*createPayment" internal/bot/payment.go
```

**Добавить валидацию в начало функции:**
```go
if price <= 0 {
    return fmt.Errorf("некорректная сумма платежа: %d", price)
}
```

**Файлы:**
- Modify: `internal/bot/payment.go`
- Test: `internal/bot/payment_test.go`

### Шаги

**Шаг 1: Найти точку вставки**

```bash
grep -n "createPaymentForUser" internal/bot/payment.go
```

Прочитать функцию и определить где берётся `price`/`amount`.

**Шаг 2: Написать тест**

В `internal/bot/payment_test.go`:
```go
func TestCreatePaymentForUserRejectsZeroAmount(t *testing.T) {
    b, db := newTestBot(t)
    telegramID := int64(2001)
    _ = db.CreateUser(&database.User{
        TelegramID:        telegramID,
        RemnawaveUUID:     "uuid-zero",
        SubscriptionPrice: nil, // нет цены → должна вернуть ошибку
    })
    err := b.createPaymentForUser(telegramID, 2 /*SBP*/)
    assert.Error(t, err)
}
```

Запустить: `make tests` — тест должен **падать** (если функция не проверяет).

**Шаг 3: Добавить валидацию**

В функцию `createPaymentForUser` добавить в начало после получения `price`:
```go
if price <= 0 {
    return fmt.Errorf("некорректная сумма платежа: %d", price)
}
```

**Шаг 4: Проверить**

```bash
make fmt
make tests
```

**Шаг 5: Коммит**

```bash
git add internal/bot/payment.go internal/bot/payment_test.go
git commit -m "fix: добавить валидацию суммы платежа > 0"
```

---

## Task 4: Исправить логику graceCount в handleAdminStats

**Проблема:** Текущее условие:
```go
case remUser.Status == remnawave.StatusDisabled && !remUser.ExpireAt.After(now):
    graceCount++
```
`!remUser.ExpireAt.After(now)` = `ExpireAt <= now` — это означает что подписка **уже истекла**. Но grace period — это пользователи у которых подписка истекла, но срок кика ещё не пришёл (kicked_at < now). При текущей логике в graceCount попадают пользователи, которых scheduler уже должен был выкинуть.

Правильное условие для grace: `Status == DISABLED` && `ExpireAt <= now` && `ExpireAt > now - 72h` (не более 3 дней назад).

**Что менять:** `internal/bot/admin.go`, строка 708.

**Правильный код:**
```go
graceDeadline := now.Add(-72 * time.Hour)
// ...
case remUser.Status == remnawave.StatusDisabled &&
    !remUser.ExpireAt.After(now) &&
    remUser.ExpireAt.After(graceDeadline):
    graceCount++
```

**Файлы:**
- Modify: `internal/bot/admin.go:694-715`
- Test: `internal/bot/admin_test.go`

### Шаги

**Шаг 1: Написать тест**

В `internal/bot/admin_test.go` написать тест, где есть пользователь с истёкшей подпиской (> 72ч назад) и пользователь в grace (< 72ч). Убедиться что счётчики правильные.

**Шаг 2: Исправить условие**

В `handleAdminStats` перед циклом добавить:
```go
graceDeadline := now.Add(-72 * time.Hour)
```

Обновить `case` для graceCount:
```go
case remUser.Status == remnawave.StatusDisabled &&
    !remUser.ExpireAt.After(now) &&
    remUser.ExpireAt.After(graceDeadline):
    graceCount++
```

**Шаг 3: Проверить**

```bash
make fmt
make tests
```

**Шаг 4: Коммит**

```bash
git add internal/bot/admin.go internal/bot/admin_test.go
git commit -m "fix: исправить подсчёт grace period в статистике админа"
```

---

## Финальная проверка

После всех тасков:

```bash
make fmt
make tests
```

Все тесты должны проходить, форматирование без ошибок.
