# Fix: 4 проблемы из code review (security-and-bugs-audit)

> **For Claude:** REQUIRED SUB-SKILL: Use superpowers:executing-plans to implement this plan task-by-task.

**Goal:** Исправить 4 проблемы категории Important, выявленные code reviewer в ветке fix/security-and-bugs-audit.

**Architecture:** Точечные исправления в 4 файлах без рефакторинга. Каждое исправление минимально и изолировано. Строгий TDD: тест пишется первым, проверяется что он падает, затем пишется минимальная реализация.

**Tech Stack:** Go, SQLite, telebot/v3

---

## Контекст

Code review ветки выявил 4 проблемы категории Important:

1. **Chargeback race condition** — проверка статуса и обновление неатомарны, два параллельных callback могут оба выполнить BanUser/DeleteUser
2. **Callback-server без runWithRestart** — при панике в горутине платёжного сервера он умирает навсегда, тогда как scheduler/alerter/sync-loop защищены
3. **runWithRestart не проверяет ctx.Err()** — комментарий предполагает что штатный выход = ctx отменён, но это неявное допущение
4. **Зависшие состояния при снятии модератора** — при `isModerator = false` state не очищается, пользователь застревает навсегда

---

## Task 1: Атомарный chargeback через условное обновление

**Файлы:**

- Modify: `internal/database/payments.go` — добавить `UpdatePaymentStatusIfNot`
- Modify: `internal/bot/payment.go` — использовать атомарную проверку
- Test: `internal/database/payments_test.go` — тест `UpdatePaymentStatusIfNot`
- Test: `internal/bot/payment_test.go` — тест idempotency chargeback

**Шаг 1: Написать падающий тест для UpdatePaymentStatusIfNot**

В `internal/database/payments_test.go` добавить:

```go
func TestUpdatePaymentStatusIfNot(t *testing.T) {
    db := setupTestDB(t)

    // Создать тестовый платёж со статусом "confirmed"
    paymentID := createTestPayment(t, db, 123, "confirmed")

    // Первый вызов: статус не "chargebacked" → должен обновить
    updated, err := db.UpdatePaymentStatusIfNot(paymentID, "chargebacked", "chargebacked")
    require.NoError(t, err)
    assert.True(t, updated, "должен обновить, т.к. статус был не chargebacked")

    // Второй вызов: статус уже "chargebacked" → не должен обновлять
    updated, err = db.UpdatePaymentStatusIfNot(paymentID, "chargebacked", "chargebacked")
    require.NoError(t, err)
    assert.False(t, updated, "не должен обновлять повторно")
}
```

**Шаг 2: Убедиться что тест падает**

```bash
make tests 2>&1 | grep -A5 "TestUpdatePaymentStatusIfNot"
```

Ожидаем: ошибку компиляции или FAIL (метод не существует).

**Шаг 3: Добавить функцию UpdatePaymentStatusIfNot в payments.go**

После существующей `UpdatePaymentStatus` (строка 167) добавить:

```go
// UpdatePaymentStatusIfNot обновляет статус платежа только если текущий статус не равен excludedStatus.
// Возвращает true если обновление произошло (строк изменено > 0), false если статус уже excludedStatus.
// Используется для атомарной idempotency при обработке chargeback.
func (db *DB) UpdatePaymentStatusIfNot(id int64, newStatus, excludedStatus string) (bool, error) {
	res, err := db.conn.Exec(
		`UPDATE payments SET status = ? WHERE id = ? AND status != ?`,
		newStatus, id, excludedStatus,
	)
	if err != nil {
		return false, err
	}
	affected, err := res.RowsAffected()
	if err != nil {
		return false, err
	}
	return affected > 0, nil
}
```

**Шаг 4: Убедиться что тест проходит**

```bash
make tests 2>&1 | grep -A5 "TestUpdatePaymentStatusIfNot"
```

Ожидаем: PASS.

**Шаг 5: Написать падающий тест idempotency в payment_test.go**

В `internal/bot/payment_test.go` добавить тест, который отправляет два параллельных chargeback-callback на один платёж и проверяет что BanUser вызван ровно один раз. Найти существующий тест `TestCheckPaymentStatusSyncsCanceledAndChargebacked` для примера структуры.

**Шаг 6: Обновить handleChargeback в payment.go**

Заменить блок idempotency check (строки 419-428):

```go
// Старый код (удалить):
// Idempotency: если уже обработан — не повторяем
if payment.Status == "chargebacked" {
    return nil
}
if err := h.bot.db.UpdatePaymentStatus(payment.ID, "chargebacked"); err != nil {
    return fmt.Errorf("update status to chargebacked: %w", err)
}
```

```go
// Новый код:
// Атомарная idempotency: обновляем статус только если ещё не chargebacked.
// Защита от race condition при параллельных retry от Platega.
updated, err := h.bot.db.UpdatePaymentStatusIfNot(payment.ID, "chargebacked", "chargebacked")
if err != nil {
    return fmt.Errorf("update status to chargebacked: %w", err)
}
if !updated {
    // Уже обработан другим параллельным запросом
    return nil
}
```

**Шаг 7: Запустить все тесты**

```bash
make tests
```

Ожидаем: все PASS.

**Шаг 8: Коммит**

```bash
git add internal/database/payments.go internal/bot/payment.go internal/database/payments_test.go internal/bot/payment_test.go
git commit -m "fix: атомарный chargeback через conditional UPDATE"
```

---

## Task 2: runWithRestart с явной проверкой ctx.Err()

**Файлы:**

- Modify: `cmd/bot/main.go` — строки 38-43

> Примечание: `runWithRestart` — простая функция в main.go, для неё нет unit-тестов. TDD здесь применяется через интеграционную проверку (тесты всего пакета).

**Шаг 1: Прочитать текущий код runWithRestart**

Проверить строки 20-55 в `cmd/bot/main.go`.

**Шаг 2: Обновить логику runWithRestart**

Заменить блок:

```go
if !panicked {
    // fn вернулась штатно (ctx отменён внутри)
    return
}
```

На:

```go
if !panicked {
    // fn вернулась штатно — проверяем, был ли это штатный shutdown
    if ctx.Err() != nil {
        return
    }
    // fn завершилась без паники и без отмены ctx — неожиданный выход, перезапускаем
    slog.Warn("goroutine exited unexpectedly, will restart", "goroutine", name, "backoff", backoff)
}
```

**Шаг 3: Запустить тесты**

```bash
make tests
```

Ожидаем: PASS.

**Шаг 4: Проверить форматирование**

```bash
make fmt
```

**Шаг 5: Коммит**

```bash
git add cmd/bot/main.go
git commit -m "fix: runWithRestart явно проверяет ctx.Err() при штатном выходе"
```

---

## Task 3: Callback-server через runWithRestart

**Файлы:**

- Modify: `cmd/bot/main.go` — строки 118-143

> Примечание: изменение в main.go, unit-тесты не применимы. Верификация — `make tests` + визуальная проверка кода.

**Шаг 1: Прочитать текущий код запуска callback-server**

Проверить строки 118-143 в `cmd/bot/main.go`.

**Шаг 2: Обернуть callback-server в runWithRestart**

Заменить:

```go
go func() {
    defer func() {
        if r := recover(); r != nil {
            slog.Error("goroutine panicked", "goroutine", "callback-server", "recover", r)
        }
    }()
    if err := callbackServer.Start(); err != nil && err != http.ErrServerClosed {
        slog.Error("Callback server error", "error", err)
    }
}()
```

На:

```go
go runWithRestart(ctx, "callback-server", func() {
    if err := callbackServer.Start(); err != nil && err != http.ErrServerClosed {
        slog.Error("Callback server error", "error", err)
    }
})
```

Горутина graceful shutdown (`<-ctx.Done()` → `callbackServer.Shutdown()`) остаётся без изменений.

**Шаг 3: Запустить тесты**

```bash
make tests
```

Ожидаем: PASS.

**Шаг 4: Коммит**

```bash
git add cmd/bot/main.go
git commit -m "fix: callback-server обёрнут в runWithRestart для защиты от паник"
```

---

## Task 4: Очистка состояния при isModerator=false

**Файлы:**

- Modify: `internal/bot/handlers.go` — строки 360-396 (4 case StateWaitMod\*)
- Test: найти существующий тест для handleTextMessage или создать новый

**Шаг 1: Написать падающий тест**

Найти существующие тесты для `handleTextMessage` в `internal/bot/`. Добавить тест:

```go
func TestHandleTextMessage_ModeratorStateCleared_WhenRightsRevoked(t *testing.T) {
    // Создать бота с пользователем у которого есть состояние StateWaitModDeleteInvite,
    // но он НЕ является модератором (права отозваны)
    // Отправить произвольный текст
    // Проверить что состояние очищено (userStates.Get(telegramID) == StateNone)
}
```

**Шаг 2: Убедиться что тест падает**

```bash
make tests 2>&1 | grep -A5 "TestHandleTextMessage_ModeratorState"
```

Ожидаем: FAIL (состояние не очищается).

**Шаг 3: Добавить очистку состояния в handlers.go**

Для каждого из 4 case добавить `b.userStates.Delete(telegramID)` когда `isModerator = false`:

```go
case StateWaitModDeleteInvite:
    if text == BtnCancel {
        b.userStates.Delete(telegramID)
        return c.Send("Отменено", &tele.SendOptions{ReplyMarkup: ModeratorMenuKeyboard()})
    }
    if b.isModerator(telegramID) {
        return b.processModeratorDeleteInvite(c, text)
    }
    b.userStates.Delete(telegramID) // права модератора отозваны — сбрасываем состояние

case StateWaitModInvitePrice:
    if text == BtnCancel {
        b.userStates.Delete(telegramID)
        return c.Send("Отменено", &tele.SendOptions{ReplyMarkup: ModeratorMenuKeyboard()})
    }
    if b.isModerator(telegramID) {
        return b.processModeratorInvitePrice(c, text)
    }
    b.userStates.Delete(telegramID) // права модератора отозваны — сбрасываем состояние

case StateWaitModChangePriceID:
    if text == BtnCancel {
        b.userStates.Delete(telegramID)
        b.clearModChangePriceSession(telegramID)
        return c.Send("Отменено", &tele.SendOptions{ReplyMarkup: ModeratorSubscribersKeyboard()})
    }
    if b.isModerator(telegramID) {
        return b.processModChangePriceID(c, text)
    }
    b.userStates.Delete(telegramID)             // права модератора отозваны — сбрасываем состояние
    b.clearModChangePriceSession(telegramID)    // очищаем сессионные данные смены цены

case StateWaitModChangePriceValue:
    if text == BtnCancel {
        b.userStates.Delete(telegramID)
        b.clearModChangePriceSession(telegramID)
        return c.Send("Отменено", &tele.SendOptions{ReplyMarkup: ModeratorSubscribersKeyboard()})
    }
    if b.isModerator(telegramID) {
        return b.processModChangePriceValue(c, text)
    }
    b.userStates.Delete(telegramID)             // права модератора отозваны — сбрасываем состояние
    b.clearModChangePriceSession(telegramID)    // очищаем сессионные данные смены цены
```

**Шаг 4: Убедиться что тест проходит**

```bash
make tests
```

Ожидаем: PASS.

**Шаг 5: Проверить форматирование**

```bash
make fmt
```

**Шаг 6: Коммит**

```bash
git add internal/bot/handlers.go
git commit -m "fix: очищать состояние пользователя при потере прав модератора"
```

---

## Финальная верификация

```bash
make fmt    # должно пройти без ошибок
make tests  # все тесты должны пройти
```
