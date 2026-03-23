# План исправления замечаний по валидации платёжной системы

> **For Claude:** REQUIRED SUB-SKILL: Use superpowers:executing-plans to implement this plan task-by-task.

**Goal:** Закрыть критичные баги и расхождения, найденные при параллельной валидации платёжной системы, без лишних рефакторингов и с упором на корректность платежей, scheduler и UI.

**Architecture:** Сначала исправляем P0-проблемы с гонками, идемпотентностью и потерей консистентности между ботом и Remnawave. Затем выравниваем пользовательский, модераторский и админский UI с утверждёнными планами. Для исторической статистики отдельно фиксируем decision point: часть замечаний нельзя исправить честно без новых snapshot-данных, поэтому в плане есть отдельный этап с выбором стратегии.

**Tech Stack:** Go 1.25, SQLite, telebot.v3, testify

**Связанные документы:**
- `docs/plans/2026-03-21-user-ui-redesign.md`
- `docs/plans/2026-03-21-moderator-ui-redesign.md`
- `docs/plans/2026-03-21-admin-ui-redesign.md`
- `docs/plans/2026-03-22-payment-implementation-plan.md`
- `docs/plans/2026-03-23-payment-bugs-fix-plan.md`
- `docs/plans/2026-03-23-confirmed-not-activated-scheduler-guard-plan.md`
- `docs/plans/2026-03-23-admin-financial-stats-fix-plan.md`

---

## Приоритеты

### P0
- Повторный concurrent callback может повторно продлить подписку
- Быстрое двойное создание платежа может создать два `pending`
- Grace kick удаляет пользователя локально даже при ошибке Remnawave
- `confirmed_not_activated` может застревать бесконечно

### P1
- Ручная проверка оплаты даёт лишнее/урезанное success-сообщение
- В grace-статусе пользователя нет строки про удаление аккаунта
- В UI модератора broken back-flow и слишком широкое право на смену цены
- В карточке пользователя админа статус всегда с `✅`

### P2
- Историческая статистика модераторов и админа смешивает snapshot-данные с текущим live-состоянием

---

### Task 1: Зафиксировать идемпотентность callback и сериализовать создание платежа

**Files:**
- Modify: `internal/bot/payment.go`
- Modify: `internal/bot/payment_test.go`
- Modify: `internal/bot/payment_handler_test.go`
- Modify: `internal/database/payments.go`

**Step 1: Добавить регрессионный тест на повторный concurrent callback**

В `internal/bot/payment_test.go` добавить сценарий:
- один `pending` платёж;
- два параллельных вызова `HandlePaymentCallback(CONFIRMED)`;
- ожидание: `moderator_earnings` остаётся один, `ExpireAt` продлевается ровно на один месяц, а не на два.

Run:

```bash
GOCACHE=/tmp/go-build go test ./internal/bot/ -run 'TestPaymentCallback.*Concurrent.*' -v
```

Expected: `FAIL` на текущей реализации.

**Step 2: Добавить регрессионный тест на двойное быстрое создание платежа**

В `internal/bot/payment_test.go` добавить сценарий:
- один пользователь;
- два параллельных вызова `createPaymentForUser` с разными методами;
- ожидание: в БД не больше одного живого `pending`, второй запрос либо переиспользует ссылку, либо делает схему `старый expired -> новый pending`.

Run:

```bash
GOCACHE=/tmp/go-build go test ./internal/bot/ -run 'TestCreatePaymentForUser.*Concurrent.*' -v
```

Expected: `FAIL` или flaky-поведение на текущей реализации.

**Step 3: Исправить callback-path**

В `internal/bot/payment.go`:
- после входа в mutex перечитывать платёж из БД по `payment.ID`;
- принимать решение по актуальному `status`, а не по stale-объекту, прочитанному до lock;
- пропускать повторную активацию, если под mutex уже виден `confirmed`.

**Step 4: Исправить create-flow**

В `internal/bot/payment.go`:
- взять тот же mutex по `telegram_id` внутри `createPaymentForUser`;
- проверять `pending` уже под lock;
- обновление `expired` и создание нового платежа выполнять в одной критической секции;
- комментарии в коде оставить на русском.

**Step 5: Обновить helper-комментарии**

В `internal/database/payments.go` и `internal/bot/payment.go` уточнить, какие статусы являются terminal, а какие допускают retry.

**Step 6: Перезапустить таргетные тесты**

```bash
GOCACHE=/tmp/go-build go test ./internal/bot/ -run 'TestPaymentCallback|TestCreatePaymentForUser' -v
```

Expected: `PASS`.

**Suggested commit name:** `fix: сериализовать платежи и закрыть гонку callback`

---

### Task 2: Остановить потерю консистентности при grace kick и завершать безнадёжные retry

**Files:**
- Modify: `internal/bot/scheduler.go`
- Modify: `internal/bot/payment.go`
- Modify: `internal/database/payments.go`
- Modify: `internal/bot/scheduler_test.go`
- Modify: `internal/bot/payment_test.go`

**Step 1: Добавить тест на недоступный Remnawave во время grace kick**

В `internal/bot/scheduler_test.go` добавить сценарий:
- grace period закончился;
- `GetUser`/`DeleteUser` в Remnawave возвращают ошибку;
- ожидание: бот НЕ удаляет пользователя из локальной БД, НЕ помечает инвайт `kicked`, а только логирует/алертит проблему.

Run:

```bash
GOCACHE=/tmp/go-build go test ./internal/bot/ -run 'TestScheduler.*GraceKick.*Remnawave.*' -v
```

Expected: `FAIL`.

**Step 2: Добавить тест на unrecoverable `confirmed_not_activated`**

В `internal/bot/payment_test.go` добавить сценарии:
- локальный пользователь удалён из БД;
- пользователь удалён из Remnawave;
- ожидание: retry не крутится бесконечно, статус переводится в terminal error-state, админу уходит алерт.

**Step 3: Ввести terminal status для безнадёжной активации**

В `internal/database/payments.go` и `internal/bot/payment.go`:
- добавить новый статус, например `confirmed_activation_failed`;
- использовать его только для случаев, когда retry уже не имеет смысла (`user not found` в локальной БД или в панели);
- не трогать финансовую часть: деньги уже подтверждены, меняется только технический статус активации.

**Step 4: Исправить scheduler kick-path**

В `internal/bot/scheduler.go`:
- если `GetUser`/`DeleteUser` падает, не удалять локальные данные;
- `handleAutoKick` должен быть атомарным по смыслу: сначала успешное действие в панели, только потом локальный cleanup;
- при ошибке отправлять алерт админу.

**Step 5: Подчистить retry-path**

В `internal/bot/payment.go`:
- различать временные ошибки Remnawave и terminal `not found`;
- для terminal-case переводить платёж в `confirmed_activation_failed` и не планировать новые retry;
- для временных ошибок сохранять текущую retry-логику `[30s, 1m, 5m] + scheduler`.

**Step 6: Перезапустить таргетные тесты**

```bash
GOCACHE=/tmp/go-build go test ./internal/bot/ -run 'TestScheduler|TestRetryConfirmedPaymentActivation' -v
```

Expected: `PASS`.

**Suggested commit name:** `fix: защитить kick и завершать безнадёжные payment retry`

---

### Task 3: Выровнять пользовательский payment UX с планом

**Files:**
- Modify: `internal/bot/payment_handler.go`
- Modify: `internal/bot/messages.go`
- Modify: `internal/bot/payment_handler_test.go`
- Modify: `internal/bot/messages_test.go`

**Step 1: Добавить тест на полный grace-экран в `Мой статус`**

В `internal/bot/messages_test.go` добавить проверку, что grace-экран содержит:
- дедлайн оплаты;
- строку про удаление аккаунта после дедлайна.

**Step 2: Добавить тест на ручную проверку оплаты**

В `internal/bot/payment_handler_test.go` зафиксировать поведение:
- `checkPaymentStatus()` при `confirmed` не должен приводить к второму урезанному success-сообщению;
- пользователь должен получить только одно финальное подтверждение с датой окончания подписки.

**Step 3: Исправить `handleCheckPayment`**

В `internal/bot/payment_handler.go` выбрать одну из двух реализаций и оставить её единственной:
- либо `checkPaymentStatus()` только подтверждает платёж, а `handleCheckPayment` сам формирует финальный подробный ответ;
- либо `handleConfirmed()` уже отправляет финальное сообщение, а `handleCheckPayment` после `confirmed` не шлёт второе success-сообщение.

Рекомендация: выбрать второй вариант, чтобы не дублировать шаблон финального сообщения.

**Step 4: Исправить grace-текст**

В `internal/bot/messages.go` добавить строку:

```text
Если не оплатить до {дата}, аккаунт будет удалён.
```

**Step 5: Перезапустить таргетные тесты**

```bash
GOCACHE=/tmp/go-build go test ./internal/bot/ -run 'TestFormatUserStatusGrace|TestHandleCheckPayment' -v
```

Expected: `PASS`.

**Suggested commit name:** `fix: выровнять пользовательский payment ux`

---

### Task 4: Исправить moderator UI flow и ужесточить смену цены до true-trial

**Files:**
- Modify: `internal/bot/keyboards.go`
- Modify: `internal/bot/moderator.go`
- Modify: `internal/bot/moderator_test.go`
- Modify: `internal/bot/handlers.go`

**Step 1: Добавить тест на back-flow из списка подписчиков**

В `internal/bot/moderator_test.go` добавить сценарий:
- открыть `Мои подписчики`;
- нажать back;
- ожидание: возврат в модераторское меню/предыдущий экран, а не в пользовательское меню.

**Step 2: Добавить тест на запрет смены цены для non-trial**

Зафиксировать отдельно:
- paid пользователь;
- grace пользователь;
- expired пользователь;
- ожидание: смена цены запрещена во всех случаях, кроме настоящего `trial`.

**Step 3: Исправить клавиатуру**

В `internal/bot/keyboards.go`:
- для экрана подписчиков использовать `BtnBack`, а не `BtnModBack`;
- не ломать основной `В меню` для корневого раздела модератора.

**Step 4: Исправить eligibility на смену цены**

В `internal/bot/moderator.go`:
- уйти от правила “не было confirmed-оплаты”;
- проверять именно текущий тип подписки пользователя как `trial`;
- не разрешать смену цены для `grace` и `expired`, даже если исторически оплат ещё не было.

**Step 5: Перезапустить таргетные тесты**

```bash
GOCACHE=/tmp/go-build go test ./internal/bot/ -run 'TestModerator.*ChangePrice|TestModerator.*Subscribers.*' -v
```

Expected: `PASS`.

**Suggested commit name:** `fix: починить flow модератора и ограничить смену цены триалом`

---

### Task 5: Исправить явные ошибки admin UI

**Files:**
- Modify: `internal/bot/admin.go`
- Modify: `internal/bot/admin_test.go`

**Step 1: Добавить тест на статус в карточке пользователя**

В `internal/bot/admin_test.go` добавить сценарии:
- активный paid;
- grace;
- disabled/expired;
- ожидание: префикс и текст статуса соответствуют реальному состоянию, а не всегда `✅`.

**Step 2: Исправить рендер карточки**

В `internal/bot/admin.go`:
- перестать хардкодить `✅ Статус:`;
- вернуть из helper-а готовую пару `emoji + текст` или готовую строку статуса;
- сохранить остальные поля карточки без изменений.

**Step 3: Перезапустить таргетные тесты**

```bash
GOCACHE=/tmp/go-build go test ./internal/bot/ -run 'TestAdmin.*UserInfo.*' -v
```

Expected: `PASS`.

**Suggested commit name:** `fix: корректно отображать статус в карточке пользователя`

---

### Task 6: Принять решение по исторической статистике и реализовать одну честную стратегию

**Files:**
- Modify: `internal/bot/admin.go`
- Modify: `internal/bot/moderator.go`
- Modify: `internal/database/payments.go`
- Modify: `internal/database/earnings.go`
- Modify: `internal/bot/admin_test.go`
- Modify: `internal/bot/moderator_test.go`
- Optional migration: `internal/database/db.go`

**Проблема:** Замечания по `Мой заработок`, `Общей статистике` и `Статистике модераторов за прошлый месяц` частично упираются в отсутствие исторических snapshot-данных. Текущая схема не умеет честно отвечать на вопросы вида:
- сколько было `paid / trial / grace` у модератора именно в прошлом месяце;
- сколько было платящих “на 01.MM”;
- какие комиссии применялись к админским платежам, если fee-конфиг потом менялся.

**Step 1: Зафиксировать решение по одной из стратегий**

Нужно выбрать один путь до реализации:

**Вариант A, рекомендованный:** добавить snapshot-данные.
- хранить fee snapshot для каждого платежа;
- хранить месячный snapshot счётчиков модератора (`paid / trial / grace`) на конец или начало месяца;
- после этого приводить UI в точное соответствие планам.

**Вариант B, минимальный:** честно переименовать/упростить UI.
- `Мой заработок` и статистику модераторов явно пометить как “текущее состояние + финансы за месяц”;
- убрать формулировки, которые обещают исторический snapshot, если данных для него нет.

**Step 2: Если выбран вариант A, подготовить миграцию и тесты**

Новые сущности зависят от точного решения, но минимум:
- snapshot комиссий на платеже;
- snapshot модераторских счётчиков на месяц.

Сначала написать падающие тесты, потом миграцию, потом выборки.

**Step 3: Если выбран вариант B, зафиксировать copy и тесты**

В `internal/bot/moderator_test.go` и `internal/bot/admin_test.go`:
- ожидать только те метрики, которые код реально может доказать;
- убрать misleading-тексты про `на 01.MM` и “статистика прошлого месяца”, если там живые текущие состояния.

**Step 4: Реализовать выбранную стратегию**

Команды проверки зависят от варианта, но минимум:

```bash
GOCACHE=/tmp/go-build go test ./internal/bot/ -run 'TestHandleAdminStats|TestHandleAdminModStats|TestHandleModeratorEarnings' -v
GOCACHE=/tmp/go-build go test ./internal/database/ -run 'Test.*Earnings|Test.*Payments' -v
```

**Suggested commit name:** `refactor: привести историческую статистику к честной модели`

---

### Task 7: Сквозная верификация и документация выполнения

**Files:**
- Modify: `docs/progress/2026-03-23-payment-validation-remediation-progress.md`
- Optional modify: `README.md`

**Step 1: Прогнать таргетные тесты по изменённым зонам**

```bash
GOCACHE=/tmp/go-build go test ./internal/bot/... -count=1
GOCACHE=/tmp/go-build go test ./internal/database/... -count=1
```

**Step 2: Прогнать обязательные проектные проверки**

```bash
make fmt
make tests
```

Ожидание: всё `PASS`. Нельзя завершать работу с красными проверками.

**Step 3: Записать прогресс**

Создать `docs/progress/2026-03-23-payment-validation-remediation-progress.md` и указать:
- ссылку на этот план;
- что именно реализовано;
- какие команды проверки запускались;
- какие пункты сознательно отложены и почему.

**Step 4: Обновить README только если меняется публично наблюдаемое поведение**

Обновлять README только при существенных внешних изменениях:
- новый terminal status;
- новая трактовка статистики;
- новые ограничения поведения scheduler.

**Suggested commit name:** `docs: задокументировать исправления по валидации платежей`
