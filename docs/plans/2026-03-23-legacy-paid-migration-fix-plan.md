# Исправление migration-gap для старых оплаченных пользователей модераторов Implementation Plan

> **For Claude:** REQUIRED SUB-SKILL: Use superpowers:executing-plans to implement this plan task-by-task.

**Goal:** Убрать migration-gap, при котором старый пользователь модератора с уже оплаченным вручную активным периодом после установки `subscription_price` ошибочно считается `trial` вместо `paid`.

**Architecture:** Источник истины о финансах и первой оплате остаётся в таблице `payments`; фейковые backfill-платежи не создаём. Для миграции добавляем в `users` отдельный boolean-флаг, который означает: текущий период уже был оплачен вне нового payment-flow и должен обрабатываться как `paid` до первой реальной оплаты через Platega. В админском flow смены цены показываем дополнительный вопрос только для migration-case: модераторский пользователь, активный finite `expireAt`, нет ни одного подтверждённого платежа.

**Tech Stack:** Go 1.25, SQLite, telebot.v3, testify

**Связанные документы:**
- `docs/plans/2026-03-21-payment-business-model-redesign.md`
- `docs/plans/2026-03-22-deployment-checklist.md`
- `docs/plans/2026-03-22-payment-implementation-plan.md`
- `docs/plans/2026-03-23-payment-validation-remediation-plan.md`

---

## Решение

- Добавить в `users` флаг `legacy_paid_migrated` (`INTEGER`, трактуется как bool, default `0`).
- При `✏️ Изменить цену` админу задавать дополнительный вопрос только если одновременно выполняются условия:
  - у пользователя есть модераторский инвайт (`invite.ExpireDays != nil`);
  - у пользователя нет подтверждённых платежей в `payments`;
  - в Remnawave пользователь активен сейчас и `expireAt > now`;
  - подписка не бессрочная (`expireAt.Year() < 2099`).
- Если админ отвечает `Да, считать оплаченной`, сохранять новую цену и `legacy_paid_migrated = true`.
- Если админ отвечает `Нет, оставить trial`, сохранять только цену и `legacy_paid_migrated = false`.
- Для новых пользователей после деплоя никакого дополнительного вопроса быть не должно: они остаются обычным `trial` до первой оплаты через Platega.

---

### Task 1: Зафиксировать регрессии тестами до изменения логики

**Files:**
- Create: `internal/database/users_test.go`
- Modify: `internal/bot/admin_test.go`
- Modify: `internal/bot/scheduler_test.go`
- Modify: `internal/bot/handlers_test.go`

**Step 1: Добавить DB-тест на новый migration-флаг**

В `internal/database/users_test.go` добавить сценарий:
- создать пользователя;
- убедиться, что `legacy_paid_migrated` по умолчанию `false`;
- установить флаг в `true`;
- перечитать пользователя и проверить, что флаг сохранился.

Run:

```bash
GOCACHE=/tmp/go-build go test ./internal/database/ -run 'TestUserLegacyPaidMigrated' -v
```

Expected: `FAIL`, потому что поля и accessor-ов ещё нет.

**Step 2: Добавить тест на admin change price flow для migration-case**

В `internal/bot/admin_test.go` добавить сценарий:
- пользователь пришёл по модераторскому инвайту;
- в Remnawave у него активный finite `expireAt` в будущем;
- подтверждённых платежей нет;
- админ запускает `✏️ Изменить цену`, вводит `telegram_id`, затем новую цену;
- ожидание: бот НЕ завершает flow сразу, а переводит админа в новый state подтверждения migration-case и показывает вопрос:
  - "Текущий период уже оплачен вручную?"
  - в сообщении есть дата `expireAt`.

Run:

```bash
GOCACHE=/tmp/go-build go test ./internal/bot/ -run 'TestAdminChangePrice.*Migration.*Prompt' -v
```

Expected: `FAIL`.

**Step 3: Добавить тест, что новый пользователь после деплоя не видит migration-вопрос**

В `internal/bot/admin_test.go` добавить сценарий:
- новый пользователь пришёл по модераторскому инвайту;
- у него обычный триал без старой ручной оплаты;
- админ меняет цену;
- ожидание: бот сохраняет цену без дополнительного вопроса и не переходит в migration-state.

Run:

```bash
GOCACHE=/tmp/go-build go test ./internal/bot/ -run 'TestAdminChangePrice.*NoMigrationPromptForFreshTrial' -v
```

Expected: `FAIL`.

**Step 4: Добавить тест на статус migrated-user**

В `internal/bot/handlers_test.go` и/или `internal/bot/scheduler_test.go` добавить сценарий:
- модераторский пользователь без записей в `payments`;
- `legacy_paid_migrated = true`;
- ожидание:
  - `isTrialUser()` возвращает `false`;
  - в `userKeyboard()` показывается `💳 Продлить подписку`, а не `💳 Оплатить подписку`.

Run:

```bash
GOCACHE=/tmp/go-build go test ./internal/bot/ -run 'TestIsTrialUser.*LegacyPaidMigrated|TestUserKeyboard.*LegacyPaidMigrated' -v
```

Expected: `FAIL`.

**Step 5: Добавить тест на scheduler paid-ветку для migrated-user**

В `internal/bot/scheduler_test.go` добавить сценарий:
- модераторский пользователь без `payments`, но с `legacy_paid_migrated = true`;
- до конца подписки остаётся 3 дня;
- ожидание: отправляется `paid`-уведомление за 3 дня, а не trial-уведомление за 24 часа.

Run:

```bash
GOCACHE=/tmp/go-build go test ./internal/bot/ -run 'TestScheduler.*LegacyPaidMigrated.*PaidBranch' -v
```

Expected: `FAIL`.

**Suggested commit name:** `test: зафиксировать migration gap старых оплат`

---

### Task 2: Добавить в БД migration-флаг и helper-методы

**Files:**
- Modify: `internal/database/db.go`
- Modify: `internal/database/users.go`
- Create: `internal/database/users_test.go`

**Step 1: Расширить модель `User`**

В `internal/database/db.go`:
- добавить в `type User struct` поле:

```go
LegacyPaidMigrated bool
```

**Step 2: Добавить миграцию схемы**

В `internal/database/db.go`:
- добавить `ALTER TABLE users ADD COLUMN legacy_paid_migrated INTEGER NOT NULL DEFAULT 0`.

Проверить, что:
- существующие БД поднимутся без ручных миграций;
- default значение безопасно для всех текущих пользователей.

**Step 3: Обновить чтение пользователей**

В `internal/database/users.go`:
- расширить `SELECT`/`Scan` в `GetUserByTelegramID`, `GetUserByRemnawaveUUID`, `GetAllUsers`;
- читать `legacy_paid_migrated` как `INTEGER` и маппить в `bool`.

**Step 4: Добавить setter**

В `internal/database/users.go` добавить метод:

```go
func (db *DB) SetLegacyPaidMigrated(telegramID int64, value bool) error
```

Реализация:
- писать `1` или `0` в `users.legacy_paid_migrated`.

**Step 5: Запустить таргетные DB-тесты**

```bash
GOCACHE=/tmp/go-build go test ./internal/database/ -run 'TestUserLegacyPaidMigrated' -v
```

Expected: `PASS`.

**Suggested commit name:** `feat: добавить migration-флаг старой оплаты`

---

### Task 3: Добавить условный migration-подтверждающий шаг в admin price flow

**Files:**
- Modify: `internal/bot/admin.go`
- Modify: `internal/bot/handlers.go`
- Modify: `internal/bot/keyboards.go`
- Modify: `internal/bot/keyboards_test.go`
- Modify: `internal/bot/admin_test.go`

**Step 1: Ввести новые state и session-поля**

В `internal/bot/admin.go`:
- добавить новый state, например:

```go
StateWaitAdminChangePriceMigrationConfirm = "wait_admin_change_price_migration_confirm"
```

- расширить `adminChangePriceSession` полями:
  - `PendingPrice int`
  - `HasPendingPrice bool`
  - `ShouldAskMigrationConfirm bool`
  - `CurrentExpireAt *time.Time`

**Step 2: Добавить отдельную клавиатуру для migration-вопроса**

В `internal/bot/keyboards.go`:
- добавить новые кнопки:
  - `BtnAdminMigrationPaidYes = "✅ Да, считать оплаченной"`
  - `BtnAdminMigrationPaidNo = "❌ Нет, оставить trial"`
- добавить `AdminMigrationPaidKeyboard()`.

В `internal/bot/keyboards_test.go`:
- зафиксировать наличие этих кнопок.

**Step 3: Вычислять eligibility заранее**

В `processAdminChangePriceID()`:
- кроме существующих проверок, загрузить пользователя из Remnawave;
- вычислить `ShouldAskMigrationConfirm` только если:
  - инвайт модераторский;
  - `HasConfirmedPayment(targetID) == false`;
  - `remUser.Status == ACTIVE`;
  - `remUser.ExpireAt.After(time.Now().UTC())`;
  - `remUser.ExpireAt.Year() < 2099`.

Если хоть одно условие не выполнено:
- flow работает по старой схеме без дополнительного вопроса.

**Step 4: Разделить применение цены и финальное подтверждение**

В `processAdminChangePriceValue()`:
- если `ShouldAskMigrationConfirm == false`, применять цену сразу, как сейчас;
- если `true`, сохранять `PendingPrice`, переводить админа в новый state и отправлять вопрос:

```text
Срок в панели: до DD.MM.YYYY.
Текущий период у пользователя уже оплачен вручную?
```

**Step 5: Реализовать новый обработчик ответа**

Добавить метод вроде:

```go
func (b *Bot) processAdminChangePriceMigrationConfirm(c tele.Context, text string) error
```

Ветви:
- `BtnAdminMigrationPaidYes`:
  - `UpdateSubscriptionPrice`
  - `UpdateInviteSubscriptionPrice`
  - `SetLegacyPaidMigrated(..., true)`
  - финальное сообщение: цена изменена, пользователь помечен как `paid`.
- `BtnAdminMigrationPaidNo`:
  - те же update цены;
  - `SetLegacyPaidMigrated(..., false)`
  - финальное сообщение: цена изменена, пользователь остаётся `trial`.
- `BtnCancel`:
  - чисто завершить flow без side effect.

Вынести применение цены в helper, чтобы не дублировать код.

**Step 6: Подключить новый state в роутинг**

В `internal/bot/handlers.go`:
- добавить обработку `StateWaitAdminChangePriceMigrationConfirm`.

**Step 7: Запустить таргетные UI-тесты**

```bash
GOCACHE=/tmp/go-build go test ./internal/bot/ -run 'TestAdminChangePrice|TestAdminMigrationPaidKeyboard' -v
```

Expected: `PASS`.

**Suggested commit name:** `feat: добавить migration-вопрос в смену цены админа`

---

### Task 4: Перевести migrated-user в paid-ветку без подделки платежей

**Files:**
- Modify: `internal/bot/scheduler.go`
- Modify: `internal/bot/handlers.go`
- Modify: `internal/bot/handlers_test.go`
- Modify: `internal/bot/scheduler_test.go`

**Step 1: Исправить `isTrialUser()`**

В `internal/bot/scheduler.go`:
- до проверки `HasConfirmedPayment()` добавить ранний выход:

```go
if dbUser.LegacyPaidMigrated {
    return false
}
```

Не загружать legacy-флаг из побочных источников; использовать только поле в `users`.

**Step 2: Сохранить поведение для новых пользователей**

Убедиться тестами, что:
- обычный новый модераторский пользователь без оплат по-прежнему `trial`;
- админский бессрочный пользователь не меняет поведение;
- `legacy_paid_migrated = false` ничего не ломает.

**Step 3: Проверить пользовательский UI**

В `internal/bot/handlers.go` логика уже опирается на `isTrialUser()`, поэтому дополнительных веток не добавлять.
Нужно только тестами зафиксировать, что migrated-user:
- видит `💳 Продлить подписку`;
- попадает в paid-ветку scheduler с напоминаниями за 3 дня и 1 день;
- получает grace period, а не мгновенный kick при `expireAt`.

**Step 4: Запустить таргетные scheduler/UI-тесты**

```bash
GOCACHE=/tmp/go-build go test ./internal/bot/ -run 'TestIsTrialUser|TestUserKeyboard|TestScheduler.*LegacyPaidMigrated.*' -v
```

Expected: `PASS`.

**Suggested commit name:** `fix: считать мигрированных пользователей paid`

---

### Task 5: Обновить документацию и прогнать полную верификацию

**Files:**
- Modify: `README.md`
- Create: `docs/progress/2026-03-23-legacy-paid-migration-fix-progress.md`

**Step 1: Обновить README**

В `README.md` уточнить:
- что старые пользователи с установленной ценой могут быть переведены на новую модель как `paid` через админский migration-вопрос;
- что этот вопрос показывается только для старых активных пользователей модераторов без платежей в `payments`;
- что новые пользователи после деплоя не затрагиваются и остаются обычным `trial`.

**Step 2: Создать progress-файл после выполнения плана**

В `docs/progress/2026-03-23-legacy-paid-migration-fix-progress.md` зафиксировать:
- ссылку на этот план;
- какие тесты добавлены;
- какие команды верификации выполнены;
- итоговое поведение migration-case и fresh-user-case.

**Step 3: Прогнать обязательную верификацию**

```bash
make fmt
make tests
```

Expected: оба прогона успешны.

**Step 4: Финальный коммит**

```bash
git add README.md docs/plans/2026-03-23-legacy-paid-migration-fix-plan.md docs/progress/2026-03-23-legacy-paid-migration-fix-progress.md internal/database/db.go internal/database/users.go internal/database/users_test.go internal/bot/admin.go internal/bot/handlers.go internal/bot/keyboards.go internal/bot/keyboards_test.go internal/bot/admin_test.go internal/bot/handlers_test.go internal/bot/scheduler.go internal/bot/scheduler_test.go
git commit -m "fix: закрыть migration gap старых оплат"
```

**Suggested commit name:** `docs: задокументировать миграцию старых оплат`
