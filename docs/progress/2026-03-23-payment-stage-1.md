# Этап 1: Миграция БД — таблицы payments и moderator_earnings
**План:** [2026-03-22-payment-implementation-plan.md](../plans/2026-03-22-payment-implementation-plan.md)
**Статус:** Выполнен

## Что сделано

### Изменённые файлы

- **`internal/database/db.go`**
  - Структура `User` расширена: добавлены `SubscriptionPrice *int` и `ModeratorID *int64`
  - Структура `Invite` расширена: добавлено `SubscriptionPrice *int`
  - В `migrations`: добавлены таблицы `payments` и `moderator_earnings` с индексами
  - В `alterMigrations`: добавлены ALTER TABLE для `users.subscription_price`, `users.moderator_id`, `invites.subscription_price`

- **`internal/database/users.go`**
  - `CreateUser` — сигнатура расширена параметрами `subscriptionPrice *int, moderatorID *int64`
  - `GetUserByTelegramID`, `GetUserByRemnawaveUUID`, `GetAllUsers` — SELECT и Scan обновлены под новые поля
  - Добавлена `UpdateSubscriptionPrice`

- **`internal/database/invites.go`**
  - `GetInviteByCode`, `GetAllInvites`, `GetUnusedInvites`, `GetInviteByUsedBy` — SELECT и Scan обновлены под `subscription_price`
  - Добавлена `CreateInviteWithPrice(createdBy int64, expireDays int, price int) (string, error)`

- **`internal/bot/handlers.go`** — вызов `CreateUser` дополнен `nil, nil`
- **`cmd/migrator/main.go`** — вызов `CreateUser` дополнен `nil, nil`
- Все тестовые файлы в `internal/database/` и `internal/bot/` — вызовы `CreateUser` обновлены

### Новые файлы

- **`internal/database/payments.go`** — CRUD для таблицы `payments`: `CreatePayment`, `GetPaymentByID`, `GetPendingPayment`, `GetPaymentByPlategaTxID`, `UpdatePaymentStatus`, `ConfirmPayment`, `ExpireOldPendingPayments`, `GetConfirmedNotActivated`, `HasConfirmedPayment`, статистика за месяц
- **`internal/database/earnings.go`** — CRUD для `moderator_earnings`: `CreateEarning`, `GetModeratorEarningsByMonth`, `GetModeratorTotalEarnings`, `GetAllEarningsByMonth`
- **`internal/database/payments_test.go`** — 6 тестов (TDD: сначала написаны тесты)
- **`internal/database/earnings_test.go`** — 3 теста (TDD: сначала написаны тесты)

## Отклонения от плана

- Нет: все шаги выполнены строго по плану
- Тесты написаны в стиле TDD: сначала RED (тесты без реализации), затем GREEN (реализация)
- Исправлена проблема с временными зонами: `time.Now()` → `time.Now().UTC()` в тестах протухших платежей
