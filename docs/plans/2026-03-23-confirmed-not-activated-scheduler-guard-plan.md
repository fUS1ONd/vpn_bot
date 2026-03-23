# Защита confirmed_not_activated от scheduler — план реализации

> **For Claude:** REQUIRED SUB-SKILL: Use superpowers:executing-plans to implement this plan task-by-task.

**Goal:** Не допускать `disable` и `kick` для пользователей с уже подтверждённой оплатой, если платёж временно находится в статусе `confirmed_not_activated`.

**Architecture:** Исправление должно быть минимальным по поверхности: scheduler и классификация trial/paid должны считать `confirmed_not_activated` защитным платёжным состоянием наравне с `confirmed`. При этом логика retry активации в Remnawave остаётся без изменений, а временный статус не должен менять финансовую статистику и прочие выборки, где нужен именно финальный `confirmed`.

**Tech Stack:** Go 1.25, SQLite, telebot.v3, testify

---

### Task 1: Зафиксировать ожидаемую семантику статуса в тестах БД

**Files:**
- Modify: `internal/database/payments_test.go`
- Modify: `internal/database/payments.go`

**Step 1: Написать падающий тест для `HasConfirmedPayment`**

В `internal/database/payments_test.go` добавить тест:

```go
func TestHasConfirmedPaymentTreatsConfirmedNotActivatedAsPaid(t *testing.T) {
	dbFile := "test_payments_confirmed_not_activated_paid.db"
	db, err := New(dbFile)
	require.NoError(t, err)
	defer func() {
		db.Close()
		os.Remove(dbFile)
	}()

	p := &Payment{
		TelegramID:    12345,
		Amount:        500,
		PaymentMethod: "sbp",
		Status:        "pending",
	}
	id, err := db.CreatePayment(p)
	require.NoError(t, err)
	require.NoError(t, db.ConfirmPayment(id))
	require.NoError(t, db.UpdatePaymentStatus(id, "confirmed_not_activated"))

	has, err := db.HasConfirmedPayment(12345)
	require.NoError(t, err)
	assert.True(t, has, "confirmed_not_activated должен считаться подтверждённой оплатой для защитных проверок")
}
```

**Step 2: Написать падающий тест для `HasConfirmedPaymentSince`**

В тот же файл добавить тест:

```go
func TestHasConfirmedPaymentSinceTreatsConfirmedNotActivatedAsPaid(t *testing.T) {
	dbFile := "test_payments_confirmed_not_activated_since.db"
	db, err := New(dbFile)
	require.NoError(t, err)
	defer func() {
		db.Close()
		os.Remove(dbFile)
	}()

	p := &Payment{
		TelegramID:    12345,
		Amount:        500,
		PaymentMethod: "sbp",
		Status:        "pending",
	}
	id, err := db.CreatePayment(p)
	require.NoError(t, err)
	require.NoError(t, db.ConfirmPayment(id))
	require.NoError(t, db.UpdatePaymentStatus(id, "confirmed_not_activated"))

	since := time.Now().UTC().Add(-1 * time.Hour)
	has, err := db.HasConfirmedPaymentSince(12345, since)
	require.NoError(t, err)
	assert.True(t, has, "confirmed_not_activated должен защищать пользователя в проверках scheduler по времени")
}
```

**Step 3: Запустить только новые DB-тесты и убедиться, что они падают**

Run:

```bash
GOCACHE=/tmp/go-build go test ./internal/database/ -run 'TestHasConfirmedPaymentTreatsConfirmedNotActivatedAsPaid|TestHasConfirmedPaymentSinceTreatsConfirmedNotActivatedAsPaid' -v
```

Expected: `FAIL`, потому что текущие запросы смотрят только `status = 'confirmed'`.

**Step 4: Исправить защитные выборки в `payments.go`**

В `internal/database/payments.go` обновить только защитные helper-методы:

- `HasConfirmedPayment`
- `HasConfirmedPaymentSince`

SQL должен использовать:

```sql
status IN ('confirmed', 'confirmed_not_activated')
```

Комментарии к методам тоже обновить: явно указать, что для scheduler-защиты оба статуса означают «оплата подтверждена, пользователя нельзя считать неоплатившим».

**Step 5: Повторно запустить DB-тесты**

Run:

```bash
GOCACHE=/tmp/go-build go test ./internal/database/ -run 'TestHasConfirmedPaymentTreatsConfirmedNotActivatedAsPaid|TestHasConfirmedPaymentSinceTreatsConfirmedNotActivatedAsPaid' -v
```

Expected: `PASS`.

**Step 6: Коммит**

```bash
git add internal/database/payments.go internal/database/payments_test.go
git commit -m "fix: защитить confirmed_not_activated в проверках оплаты"
```

---

### Task 2: Закрыть регрессию тестами scheduler для trial, disable и grace

**Files:**
- Modify: `internal/bot/scheduler_test.go`
- Modify: `internal/bot/scheduler.go`

**Step 1: Написать падающий тест для trial-пользователя**

В `internal/bot/scheduler_test.go` добавить тест, который создаёт:

- trial-пользователя с уже истёкшим `expireAt`
- платёж со статусом `confirmed_not_activated`

Проверка:

```go
func TestSchedulerTrialNotKickedIfPaymentConfirmedNotActivated(t *testing.T) {
	// setup trial user
	// create payment -> ConfirmPayment -> UpdatePaymentStatus(..., "confirmed_not_activated")
	// вызвать processTrialUser(...)
	// убедиться, что пользователь не удалён из БД
}
```

Ожидание: до фикса тест падает, потому что scheduler считает пользователя неоплатившим.

**Step 2: Написать падающий тест для paid disable-path**

Добавить тест для пользователя с платной подпиской:

```go
func TestSchedulerPaidDisableSkippedIfPaymentConfirmedNotActivated(t *testing.T) {
	// setup paid user
	// платёж confirmed_not_activated после expireAt
	// мок Remnawave с флагом disableCalled
	// вызвать processPaidUser(...)
	// assert.False(t, disableCalled)
}
```

Ожидание: до фикса `disableCalled == true`.

**Step 3: Написать падающий тест для grace kick-path**

Добавить отдельный тест:

```go
func TestSchedulerGraceKickSkippedIfPaymentConfirmedNotActivated(t *testing.T) {
	// setup paid user
	// expireAt = now - 96h
	// платёж confirmed_not_activated после expireAt
	// мок Remnawave: свежая проверка пользователя не должна приводить к kick
	// assert, что пользователь не удалён
}
```

Это нужен отдельный регресс-тест, потому что reported bug затрагивает не только `disable`, но и `kick` после grace period.

**Step 4: Добавить интеграционный тест на полный scheduler-pass с неуспешным retry**

Сделать сценарий ближе к реальному:

```go
func TestSchedulerPassDoesNotPunishConfirmedNotActivatedWhenRetryStillFails(t *testing.T) {
	// 1. Пользователь уже просрочен
	// 2. Платёж = confirmed_not_activated
	// 3. retryConfirmedNotActivated вызывается в начале pass, но Remnawave на активации снова падает
	// 4. scheduler продолжает обход пользователей
	// 5. Проверяем, что ни disable, ни kick не случились
}
```

Мок клиента должен различать:

- запросы списка пользователей для основного scheduler-pass
- запрос активации/получения пользователя для retry
- попытки `PATCH`/`DELETE`, которые должны остаться не вызванными

Этот тест обязателен, потому что баг проявляется именно в одном scheduler-pass: retry может не сработать, но это не даёт права считать пользователя неоплатившим.

**Step 5: Запустить только scheduler-регрессионные тесты**

Run:

```bash
GOCACHE=/tmp/go-build go test ./internal/bot/ -run 'TestSchedulerTrialNotKickedIfPaymentConfirmedNotActivated|TestSchedulerPaidDisableSkippedIfPaymentConfirmedNotActivated|TestSchedulerGraceKickSkippedIfPaymentConfirmedNotActivated|TestSchedulerPassDoesNotPunishConfirmedNotActivatedWhenRetryStillFails' -v
```

Expected: `FAIL` до реализации.

**Step 6: Уточнить комментарии в `scheduler.go`**

После фикса обновить комментарии рядом с:

- `processTrialUser`
- `processPaidUser`
- `isTrialUser`

Нужно явно зафиксировать в коде, что:

- `confirmed` и `confirmed_not_activated` оба означают подтверждённую оплату для защитных решений scheduler
- `confirmed_not_activated` не означает успешную активацию в панели, но уже запрещает считать пользователя должником

Комментарии писать по-русски.

**Step 7: Повторно запустить scheduler-тесты**

Run:

```bash
GOCACHE=/tmp/go-build go test ./internal/bot/ -run 'TestSchedulerTrialNotKickedIfPaymentConfirmedNotActivated|TestSchedulerPaidDisableSkippedIfPaymentConfirmedNotActivated|TestSchedulerGraceKickSkippedIfPaymentConfirmedNotActivated|TestSchedulerPassDoesNotPunishConfirmedNotActivatedWhenRetryStillFails' -v
```

Expected: `PASS`.

**Step 8: Коммит**

```bash
git add internal/bot/scheduler.go internal/bot/scheduler_test.go
git commit -m "fix: запретить scheduler наказывать confirmed_not_activated"
```

---

### Task 3: Проверить, что побочные выборки не изменили смысл

**Files:**
- Review only: `internal/database/payments.go`
- Review only: `internal/bot/payment.go`

**Step 1: Проверить, что не были изменены агрегаты и статистика**

Убедиться, что без необходимости не затронуты:

- `CountConfirmedPaymentsByMonth`
- `SumConfirmedPaymentsByMonth`
- `CountFirstPaymentsByMonth`
- `CountPayingSubscribersByModerator`

Для этого бага достаточно исправить только защитные helper-методы scheduler. Если в ходе реализации появилось желание расширить остальные выборки на `confirmed_not_activated`, этого делать не нужно без отдельного требования.

**Step 2: Проверить, что retry-механизм не сломан**

Убедиться, что всё ещё сохраняется поведение:

- callback фиксирует платёж
- при ошибке активации ставится `confirmed_not_activated`
- scheduler продолжает retry
- после успешной активации статус возвращается в `confirmed`

**Step 3: При необходимости добавить короткий комментарий в код**

Если при ревью видно, что причина различия между «защитными» и «финансовыми» запросами неочевидна, добавить краткий комментарий в `payments.go` рядом с helper-методами. Комментарий должен объяснять, почему scheduler-защита шире, чем финансовая статистика.

**Step 4: Коммит**

```bash
git add internal/database/payments.go internal/bot/payment.go
git commit -m "refactor: задокументировать семантику статусов оплаты"
```

---

### Task 4: Полная проверка и документация выполнения

**Files:**
- Modify: `docs/progress/2026-03-23-confirmed-not-activated-scheduler-guard.md`
- Modify: `README.md` (только если по итогу появится пользовательское или операционное изменение)

**Step 1: Прогнать форматирование**

Run:

```bash
make fmt
```

Expected: успешное завершение без ошибок.

**Step 2: Прогнать тесты**

Run:

```bash
make tests
```

Expected: все тесты зелёные.

**Step 3: Создать progress-документ**

Создать `docs/progress/2026-03-23-confirmed-not-activated-scheduler-guard.md` и зафиксировать:

- ссылку на этот план
- какие файлы изменены
- какие регресс-тесты добавлены
- результаты `make fmt`
- результаты `make tests`

**Step 4: Проверить необходимость обновления `README.md`**

Если поведение системы поменялось только внутренне и пользовательский/операционный контракт не изменился, `README.md` не трогать. Если по ходу реализации появится новая важная оговорка по `confirmed_not_activated` для эксплуатации, добавить короткое пояснение.

**Step 5: Финальный коммит**

```bash
git add docs/progress/2026-03-23-confirmed-not-activated-scheduler-guard.md README.md
git commit -m "docs: описать защиту confirmed_not_activated от scheduler"
```
