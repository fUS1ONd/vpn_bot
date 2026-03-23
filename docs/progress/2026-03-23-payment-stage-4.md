# Этап 4: Платёжный флоу — прогресс

**План:** [docs/plans/2026-03-22-payment-implementation-plan.md](../plans/2026-03-22-payment-implementation-plan.md), строки 1018–1499

## Что сделано

### Шаг 1: `internal/remnawave/client.go`
- Добавлен параметр `trafficLimitBytes int64` в `CreateUser` — триал получает лимит трафика, админские инвайты — безлимит
- Переработан `EnableUser(uuid, newExpireAt)` — теперь одним PATCH-запросом ставит Status=ACTIVE, обновляет ExpireAt и снимает лимит трафика (TrafficLimitBytes=0)
- Добавлен `DisableUser(uuid)` — деактивация пользователя через PATCH (Status=DISABLED)
- Обновлён `UpdateUserRequest`: `Status` и `TrafficLimitBytes` теперь указатели для корректного omitempty
- Добавлены хелперы `strPtr`, `int64Ptr`
- `ExtendUserSubscription` обновлён — для EXPIRED/DISABLED теперь вызывает `EnableUser` вместо отдельных enable+patch

### Шаг 2: `internal/bot/payment.go` (создан)
- `paymentMu sync.Map` + `getPaymentMutex` — мьютексы по telegram_id
- `paymentCallbackHandler` + `PaymentCallbackHandler()` — реализует `callback.PaymentHandler`
- `HandlePaymentCallback` — диспетчер по статусу (CONFIRMED/CANCELED/CHARGEBACKED)
- `handleConfirmed` — идемпотентная обработка с retry/backoff (30с, 1м, 5м), fallback на `confirmed_not_activated`
- `activateSubscription` — продление через `EnableUser` (досрочное = +1 месяц к текущему, иначе = от now)
- `createEarningRecord` — начисление модератору с расчётом комиссий
- `calculateSharePercent` — шкала 15%/20%/25%
- `getPlategaFeePercent` — комиссии SBP/Card/Crypto из конфига
- `handleCanceled`, `handleChargeback` — отмена и chargeback с уведомлениями
- `sendAdminAlert` — уведомление админа
- `createPaymentForUser` — создание платежа в Platega + сохранение в БД, защита от дублей
- `checkPaymentStatus` — ручная проверка через API (с мьютексом)

### Шаг 3: `internal/bot/handlers.go`
- Добавлены поля `platega *platega.Client` и `maintenanceMode bool` в структуру `Bot`
- В `New()` — условная инициализация Platega-клиента
- В `processInviteCode` — триал получает лимит трафика `TrialTrafficLimitGB * 1 GiB`

### Шаг 4: `cmd/bot/main.go`
- Заменён `noopPaymentHandler` на `telegramBot.PaymentCallbackHandler()`
- Удалена заглушка `noopPaymentHandler`

### Шаг 5: `internal/bot/payment_test.go` (создан)
- `TestCalculateSharePercent` — шкала 15/20/25% (6 кейсов)
- `TestGetPlategaFeePercent` — комиссии SBP/Card/Crypto + fallback
- `TestHandleConfirmedIdempotency` — повторный callback не дублирует обработку

## Изменённые файлы

| Файл | Действие |
|------|----------|
| `internal/remnawave/client.go` | Изменён (CreateUser, EnableUser, DisableUser, UpdateUserRequest) |
| `internal/remnawave/client_test.go` | Изменён (обновлены под новые сигнатуры) |
| `internal/bot/payment.go` | Создан |
| `internal/bot/payment_test.go` | Создан |
| `internal/bot/handlers.go` | Изменён (platega, maintenanceMode, триал трафик) |
| `internal/bot/admin.go` | Изменён (EnableUser с новой сигнатурой) |
| `internal/bot/admin_test.go` | Изменён (обновлён тест switch subscription) |
| `internal/bot/handlers_test.go` | Изменён (TrialTrafficLimitGB, проверка trafficLimitBytes) |
| `cmd/bot/main.go` | Изменён (убрана заглушка, подключён PaymentCallbackHandler) |
| `cmd/migrator/main.go` | Изменён (trafficLimitBytes=0) |

## Отклонения от плана

1. **`EnableUser` переработан глубже чем в плане:** план предлагал метод, обёртывающий `UpdateUser`, но в коде уже был `EnableUser` вызывающий `POST /actions/enable`. Переработал его на PATCH с Status+ExpireAt+TrafficLimitBytes, как указано в плане. Это потребовало обновления тестов `ExtendUserSubscription` и `SwitchSubscription`.
2. **`UpdateUserRequest.Status` стал `*string`:** необходимо для корректного omitempty — пустая строка `""` не должна отправляться в JSON.

## Статус критериев приёмки

- [x] Полный цикл: createPayment → Platega API → callback → confirm → activateSubscription
- [x] Защита от двойных платежей (PENDING с тем же/другим способом)
- [x] Chargeback деактивирует пользователя + алерт админу
- [x] confirmed_not_activated при недоступности Remnawave + алерт
- [x] Race condition защищён мьютексом по telegram_id
- [x] Все тесты проходят (`make tests` + `make fmt`)
