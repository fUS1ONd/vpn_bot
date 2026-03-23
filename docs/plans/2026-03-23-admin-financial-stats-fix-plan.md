# Исправление финансовой отчётности админа и модераторов — план реализации

> **For Claude:** REQUIRED SUB-SKILL: Use superpowers:executing-plans to implement this plan task-by-task.

**Goal:** Сделать месячную финансовую отчётность корректной: админская сводка должна учитывать все подтверждённые платежи месяца, а модераторские начисления и месячные периоды должны определяться по дате подтверждения платежа, а не по времени поздней доактивации.

**Architecture:** Источником истины для общей финансовой статистики становится таблица `payments` с фильтром по `confirmed_at` и статусу `confirmed`. Таблица `moderator_earnings` остаётся снимком доли модератора на момент платежа, но месячные выборки для неё тоже должны фильтроваться через связанный платёж. Начисление модератору должно опираться на snapshot `payments.moderator_id`, а не на текущий статус роли модератора.

**Tech Stack:** Go 1.25, SQLite, telebot.v3, testify

---

### Task 1: Зафиксировать регрессии в тестах до изменения логики

**Files:**
- Modify: `internal/bot/admin_test.go`
- Modify: `internal/database/earnings_test.go`
- Modify: `internal/bot/payment_handler_test.go`

**Step 1: Добавить падающий тест на админскую сводку с админским платежом**

В `internal/bot/admin_test.go` добавить сценарий:

- есть один платёж клиента модератора;
- есть один платёж клиента админа (`moderator_id = NULL`);
- оба платежа подтверждены в текущем месяце;
- `handleAdminStats` должен показать:
  - `Платежей за месяц: 2`
  - суммарную грязную выручку по двум платежам;
  - выплаты модераторам только по платежу модератора;
  - доход владельца как `чистый доход - выплаты модераторам`.

Тест должен падать на текущей реализации, потому что админский платёж не попадает в финансовый блок.

**Step 2: Добавить падающий тест на “поздно доактивированный” платёж**

В `internal/database/earnings_test.go` добавить тест, который моделирует:

- платёж подтверждён 31 марта (`payments.confirmed_at = 2026-03-31 ...`);
- запись в `moderator_earnings` создана 1 апреля (`moderator_earnings.created_at = 2026-04-01 ...`);
- запрос `GetModeratorEarningsByMonth(..., 2026, 3)` должен вернуть этот платёж в март;
- запрос `GetModeratorEarningsByMonth(..., 2026, 4)` не должен считать его апрельским.

Тест должен падать на текущей реализации, потому что сейчас период режется по `moderator_earnings.created_at`.

**Step 3: Добавить падающий тест на снятого модератора**

В `internal/bot/payment_handler_test.go` или рядом с уже существующими тестами callback-потока добавить сценарий:

- у пользователя уже создан `pending` платёж с `payment.moderator_id = oldModeratorID`;
- до подтверждения модератора снимают;
- callback подтверждает платёж и активация проходит успешно;
- в `moderator_earnings` должна появиться запись с `moderator_id = oldModeratorID`.

Ожидание: тест падает на текущей реализации из-за проверки текущей роли модератора.

**Step 4: Запустить только новые регрессионные тесты**

Run:

```bash
GOCACHE=/tmp/go-build go test ./internal/bot/ -run 'TestHandleAdminStats.*|TestPaymentCallback.*RemovedModerator.*' -v
GOCACHE=/tmp/go-build go test ./internal/database/ -run 'TestGetModeratorEarningsByMonth.*ConfirmedAt.*' -v
```

Expected: `FAIL` на текущей логике.

**Suggested commit name:** `fix: зафиксировать регрессии финансовой отчётности`

---

### Task 2: Перевести месячные выборки начислений на период подтверждения платежа

**Files:**
- Modify: `internal/database/earnings.go`
- Modify: `internal/database/earnings_test.go`

**Step 1: Обновить `GetModeratorEarningsByMonth`**

Изменить SQL так, чтобы метод агрегировал `moderator_earnings` через `JOIN payments ON payments.id = moderator_earnings.payment_id` и фильтровал месяц по:

```sql
payments.status = 'confirmed'
AND payments.confirmed_at >= ?
AND payments.confirmed_at < ?
```

Суммы по-прежнему брать из `moderator_earnings`, но календарный период определять только по `payments.confirmed_at`.

**Step 2: Обновить получение актуального `share_percent`**

Если текущий helper всё ещё нужен, выбирать последний процент через связанный платёж, чтобы порядок был привязан к дате подтверждения, а не к времени позднего retry-вставления earnings.

**Step 3: Удалить или переопределить `GetAllEarningsByMonth`**

Если метод остаётся в кодовой базе, он должен либо:

- стать private/internal helper только для выплат модераторам с фильтром по `payments.confirmed_at`, либо
- быть заменён на более честное имя, например `GetModeratorPayoutsByMonth`.

Цель: убрать ложное ощущение, что это “общая” финансовая статистика бизнеса.

**Step 4: Перезапустить только DB-тесты earnings**

Run:

```bash
GOCACHE=/tmp/go-build go test ./internal/database/ -run 'TestGetModeratorEarningsByMonth' -v
```

Expected: `PASS`, март/апрель определяются по `confirmed_at`.

**Suggested commit name:** `fix: считать месячные начисления по confirmed_at`

---

### Task 3: Вынести общую финансовую статистику бизнеса на confirmed-платежи

**Files:**
- Modify: `internal/database/payments.go`
- Modify: `internal/database/payments_test.go`
- Modify: `internal/bot/admin.go`
- Modify: `internal/bot/admin_test.go`

**Step 1: Добавить helper для списка или агрегата подтверждённых платежей месяца**

В `internal/database/payments.go` добавить новый метод с прозрачной семантикой, например:

- `ListConfirmedPaymentsByMonth(year, month int) ([]Payment, error)`, если расчёт комиссий удобнее делать в Go;
- или `ListConfirmedPaymentsWithModeratorShareByMonth(...)`, если нужен `LEFT JOIN moderator_earnings` по `payment_id`.

Требования к helper:

- фильтр только по `payments.status = 'confirmed'`;
- период только по `payments.confirmed_at`;
- платежи без `moderator_earnings` не теряются;
- `share_amount` для таких платежей трактуется как `0`.

**Step 2: Написать unit-тест на helper**

В `internal/database/payments_test.go` добавить тест, который создаёт:

- подтверждённый админский платёж без earnings;
- подтверждённый модераторский платёж с earnings;
- подтверждённый платёж прошлого месяца;
- `confirmed_not_activated` платёж этого месяца.

Ожидание:

- в результат попадают только два `confirmed`-платежа целевого месяца;
- share по админскому платежу равен нулю;
- `confirmed_not_activated` не включается в финансовую сводку.

**Step 3: Переписать `handleAdminStats`**

В `internal/bot/admin.go` заменить использование “общих earnings” на расчёт по подтверждённым платежам месяца:

- количество платежей = число `payments.status = 'confirmed'` за месяц;
- грязная сумма = сумма `payments.amount`;
- комиссия Platega и комиссия вывода считаются по каждому платежу через текущие конфиги и `payment_method`;
- чистый доход = сумма по всем платежам после комиссий;
- выплаты модераторам = сумма `share_amount` из earnings, привязанных к этим платежам;
- доход владельца = `чистый доход - выплаты модераторам`.

Текущие helper-методы `getPlategaFeePercent` и конфиг `PlategaFeeWithdrawal` должны использоваться как единый источник формулы, чтобы не появилось расхождения между callback и отчётом.

**Step 4: Перезапустить только тесты admin stats**

Run:

```bash
GOCACHE=/tmp/go-build go test ./internal/bot/ -run 'TestHandleAdminStats' -v
```

Expected: `PASS`, включая сценарий с админским клиентом.

**Suggested commit name:** `fix: считать общую финансовую статистику по payments`

---

### Task 4: Сохранить snapshot модератора при подтверждении платежа

**Files:**
- Modify: `internal/bot/payment.go`
- Modify: `internal/bot/payment_handler_test.go`
- Check: `docs/plans/2026-03-21-payment-business-model-redesign.md`

**Step 1: Упростить правило создания earnings**

В `createEarningRecord` оставить только бизнес-условие:

- если `payment.ModeratorID == nil`, earnings не создаётся;
- если `payment.ModeratorID != nil`, earnings создаётся всегда.

Проверку “модератор ещё активен” удалить, потому что она ломает snapshot-историю уже созданного платежа.

**Step 2: Зафиксировать допущение в комментарии**

Рядом с логикой создания earnings добавить короткий комментарий на русском:

- `payments.moderator_id` — это snapshot куратора на момент создания платежа;
- финансовая история не должна зависеть от последующего снятия роли.

Комментарий должен объяснять, почему бывший модератор всё ещё может фигурировать в historical earnings конкретного платежа.

**Step 3: Перезапустить callback/payment-тесты**

Run:

```bash
GOCACHE=/tmp/go-build go test ./internal/bot/ -run 'TestPaymentCallback|TestCheckPaymentStatus' -v
```

Expected: `PASS`, earnings для снятого модератора создаются, админский платёж по-прежнему не получает moderator share.

**Suggested commit name:** `fix: сохранять snapshot модератора в earnings`

---

### Task 5: Полная верификация и документация выполнения

**Files:**
- Create: `docs/progress/2026-03-23-admin-financial-stats-fix-progress.md`
- Modify: `docs/progress/PROGRESS.md` (если в проекте принято индексировать выполненные работы)
- Check: `README.md` только если в процессе меняются пользовательские правила отчётности

**Step 1: Описать факт выполнения плана**

В новом progress-файле указать:

- ссылку на этот план;
- какие сценарии были закрыты:
  - админские платежи;
  - платежи бывших модераторов;
  - корректный месяц по `confirmed_at`;
- какие тесты добавлены;
- какие команды верификации были выполнены.

**Step 2: Прогнать точечные тесты пакетов**

Run:

```bash
GOCACHE=/tmp/go-build go test ./internal/database/... -count=1
GOCACHE=/tmp/go-build go test ./internal/bot/... -count=1
```

Expected: `PASS`.

**Step 3: Выполнить обязательную проектную верификацию**

Run:

```bash
make fmt
make tests
```

Expected: обе команды завершаются успешно. Нельзя считать задачу закрытой, если хотя бы одна из них падает.

**Step 4: Подготовить итоговое описание**

В финальном отчёте явно указать:

- что теперь общая финансовая статистика считает все подтверждённые платежи месяца;
- что отчётный период определяется по `payments.confirmed_at`;
- что `moderator_earnings` больше не теряет snapshot платежа после снятия модератора.

**Suggested commit name:** `docs: задокументировать исправление финансовой отчётности`
