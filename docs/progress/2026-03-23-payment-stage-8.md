# Этап 8: UI админа

**Дата:** 2026-03-23
**План:** [2026-03-22-payment-implementation-plan.md](../plans/2026-03-22-payment-implementation-plan.md), строки 1895–2003
**Доп. контекст:** [2026-03-21-admin-ui-redesign.md](../plans/2026-03-21-admin-ui-redesign.md)
**Коммит:** `feat: этап 8 — доработать UI админа и режим обслуживания`

## Что сделано

### `internal/bot/keyboards.go`
- Добавлены кнопки:
  - `BtnAdminStats`
  - `BtnAdminMaintenance`
  - `BtnAdminMaintenanceOff`
  - `BtnAdminUserInfo`
  - `BtnAdminSwitchInfinite`
  - `BtnAdminChangePrice`
- `AdminKeyboard` переведён на сигнатуру `AdminKeyboard(maintenanceMode bool)` и теперь показывает:
  - общую статистику
  - toggle режима обслуживания
- `AdminManageKeyboard` дополнен кнопкой `🔍 Инфо о пользователе`
- Добавлено подменю `AdminSwitchSubmenu()`

### `internal/bot/admin.go`
- Добавлена `handleAdminStats`:
  - финансы за текущий месяц
  - выплаты модераторам
  - доход владельца
  - операционные счётчики пользователей
  - конверсия trial → first payment
- Добавлен flow `Инфо о пользователе`:
  - `StateWaitAdminUserInfo`
  - карточка с куратором, ценой, сроком, трафиком, устройствами, типом и статусом
- Добавлен toggle режима обслуживания:
  - `maintenanceMode` переключается из админ-меню
  - возвращается разная клавиатура под состояние
- `Сменить тариф` переработан в подменю:
  - `♾️ Перевести на бессрочную` использует существующую логику
  - `✏️ Изменить цену` реализован через
    - `StateWaitAdminChangePriceID`
    - `StateWaitAdminChangePriceValue`
- Добавлено уведомление пользователю при смене цены (если Telegram-бот инициализирован)
- Статистика модераторов переписана:
  - каждый модератор отправляется отдельным сообщением
  - расчёт идёт за прошлый завершённый месяц
  - финансовые данные берутся из `moderator_earnings`

### `internal/bot/handlers.go`
- Обновлена маршрутизация новых admin-кнопок и состояний
- Добавлены pending-сессии админской смены цены
- `userKeyboard` теперь скрывает оплату при `maintenanceMode=true`
- Обновлён `isMenuNavigationButton` под новые кнопки

### `internal/remnawave/client.go`
- В `User` добавлен `HwidDeviceLimit`
- Добавлен `GetUserHwidDevicesCount(uuid string)` для карточки пользователя

## Тесты

### Обновлены / добавлены тесты
- `internal/bot/keyboards_test.go`
  - новые кнопки главного админ-меню
  - toggle режима обслуживания
  - подменю смены тарифа
- `internal/bot/format_test.go`
  - верхнеуровневое админ-меню обновлено под новый layout
- `internal/bot/admin_test.go`
  - общая статистика
  - карточка пользователя
  - admin flow смены цены
  - статистика модераторов отдельными сообщениями
- `internal/bot/handlers_test.go`
  - скрытие оплаты в maintenance mode
- `internal/remnawave/client_test.go`
  - получение количества HWID-устройств

## Проверка

- `GOCACHE=/tmp/go-build go test ./internal/bot ./internal/remnawave -count=1`
- Далее обязательный прогон:
  - `make fmt`
  - `make tests`
