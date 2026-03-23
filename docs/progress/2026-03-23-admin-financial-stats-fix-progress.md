# Исправление финансовой отчётности админа и модераторов

**Дата:** 2026-03-23
**План:** [2026-03-23-admin-financial-stats-fix-plan.md](../plans/2026-03-23-admin-financial-stats-fix-plan.md)
**Коммиты:**
- `869c4f8` — fix: добавить валидацию суммы платежа > 0
- `65a9236` — fix: использовать earnings table в статистике админа и исправить формулу конверсии
- `3ce6c59` — fix: исправить подсчёт grace period в статистике админа
- `b9cfc8e` — fix: считать финансовую статистику по всем confirmed-платежам включая admin

## Что сделано

### `internal/database/earnings.go`
- Месячные выборки начислений модераторов переведены с `moderator_earnings.created_at` на `payments.confirmed_at`
- Актуальный `share_percent` теперь выбирается по дате подтверждения платежа
- `GetAllEarningsByMonth` считает начисления по историческому факту подтверждения, не переписывая прошлые месяцы из-за последующего `chargebacked`
- `CreateEarning` стал идемпотентным на уровне SQL-вставки по `payment_id`

### `internal/database/payments.go`
- Добавлен `MonthlyConfirmedPayment`
- Добавлен `GetConfirmedPaymentsByMonth(year, month)`:
  - берёт финансово подтверждённые платежи со статусами `confirmed` и `confirmed_not_activated`
  - режет период по `payments.confirmed_at`
  - не теряет админские платежи без `moderator_earnings`
  - возвращает `share_amount = 0`, если выплаты модератору нет
- `CountFirstPaymentsByMonth` переведён на первую финансово подтверждённую оплату по `confirmed_at`, чтобы конверсия не расходилась с финансовым блоком

### `internal/bot/admin.go`
- Финансовый блок `handleAdminStats` больше не строится из `moderator_earnings` как единственного источника
- Общая сводка теперь считает:
  - количество платежей
  - грязную сумму
  - комиссии Platega
  - комиссию вывода
  - чистый доход
  - выплаты модераторам
  - доход владельца
  по confirmed-платежам месяца
- Формула комиссий переиспользует ту же конфигурацию и ту же бизнес-логику, что и callback платежей

### `internal/bot/payment.go`
- Убрана зависимость создания `moderator_earnings` от текущего статуса роли модератора
- Начисление модератору теперь фиксируется в момент подтверждения денег, даже если активация подписки уходит в retry
- Финансовая история теперь опирается на snapshot `payments.moderator_id`, зафиксированный при создании платежа

## Какие сценарии закрыты

- Админские платежи теперь попадают в общую месячную финансовую статистику
- Платежи пользователей бывшего модератора не теряют historical snapshot начисления
- Платёж, подтверждённый в конце месяца и доактивированный позже, относится к месяцу `payments.confirmed_at`, а не к месяцу фактической вставки earnings
- Платёж со статусом `confirmed_not_activated` больше не выпадает из финансовой отчётности, если деньги уже подтверждены
- Поздний `chargebacked` не стирает платёж из уже закрытого отчётного месяца

## Тесты

### Добавлены / обновлены

- `internal/bot/admin_test.go`
  - регрессия на админские платежи и выплаты модераторам
  - обновлены ожидания финансового блока
  - исправлен setup статистики модераторов под новую семантику месяца
- `internal/bot/payment_handler_test.go`
  - регрессия на сохранение snapshot модератора после снятия роли
- `internal/bot/payment_test.go`
  - начисление модератору создаётся до успешной активации
  - retry активации не создаёт дубликат earnings
- `internal/database/earnings_test.go`
  - регрессия на месяц по `confirmed_at`
  - проверка выбора актуального `share_percent` по дате подтверждения
  - историческое сохранение `chargebacked` в месяце подтверждения
- `internal/database/payments_test.go`
  - выборка финансово подтверждённых платежей месяца с учётом admin/moderator/previous-month/confirmed_not_activated/chargebacked сценариев

## Проверка

- `GOCACHE=/tmp/go-build go test ./internal/database/... -count=1`
- `GOCACHE=/tmp/go-build go test ./internal/bot/... -count=1`
- `make fmt`
- `make tests`
