# Управление устройствами (сброс HWID) — Progress

**План:** [docs/plans/2026-06-05-delete-devices-plan.md](../plans/2026-06-05-delete-devices-plan.md)

**Дата:** 2026-06-05
**Ветка:** `feature/delete-devices`

## Что сделано

Реализована возможность пользователю с активной подпиской посмотреть свои подключённые
HWID-устройства и удалить одно конкретное или сбросить все сразу прямо из Telegram-бота.

### Клиент Remnawave (`internal/remnawave/client.go`)
- `HwidDevice` — тип устройства (`hwid`, `platform`, `osVersion`, `deviceModel`).
- `GetUserHwidDevices(uuid)` — GET `/api/hwid/devices/{uuid}`.
- `DeleteUserHwidDevice(uuid, hwid)` — POST `/api/hwid/devices/delete`, возвращает обновлённый список.
- `DeleteAllUserHwidDevices(uuid)` — POST `/api/hwid/devices/delete-all`.

### Бот (`internal/bot/`)
- `keyboards.go`:
  - `BtnStatus` переименована «👤 Мой статус» → «👤 Моя подписка».
  - Новая reply-кнопка `BtnDevices` «📱 Управление устройствами».
  - `SubscriptionMenuKeyboard()` — reply-подменю подписки (устройства + «Назад»).
  - `DevicesManagementKeyboard()` — inline-список устройств + «Сбросить все» + «Закрыть».
  - `DevicesResetAllConfirmKeyboard()` — подтверждение сброса всех.
- `devices.go` — хендлеры: показ списка, удаление одного устройства, подтверждение и сброс всех,
  закрытие экрана; чистые функции `buildDevicesMessage` и `deviceByIndex` покрыты тестами.
- `handlers.go` — регистрация inline-callback-роутинга по `Unique`; `case BtnDevices` в
  `handleTextMessage`; `handleStatus` теперь отправляет статус с `SubscriptionMenuKeyboard()`.

### Отступление от плана (согласовано с заказчиком)
В плане предлагалось вешать inline-кнопку «Управлять устройствами» прямо под сообщением статуса.
Telegram не допускает reply- и inline-разметку в одном сообщении, поэтому по решению заказчика
выбран другой UX: «Моя подписка» переводит пользователя в **reply-подменю** с кнопками
«📱 Управление устройствами» и «🔙 Назад». Список устройств приходит отдельным сообщением
с inline-кнопками. Функция `DevicesStatusInlineKeyboard` из плана не понадобилась и не добавлялась.

### Попутный фикс
`TestHandleModeratorEarnings` падал из-за хардкод-даты `expireAt: 2026-04-20` (уже истекла на
2026-06-05) — заменено на `time.Now().UTC().AddDate(0, 0, 10)`. Падение было предсуществующим и
не связано с фичей (тот же класс бага, что чинили в коммите 67764b0).

## Результаты проверки

```
make fmt   — без ошибок (go vet ./..., go fmt ./...)
make tests — все пакеты OK:
  ok  internal/bot
  ok  internal/callback
  ok  internal/config
  ok  internal/database
  ok  internal/monitoring
  ok  internal/platega
  ok  internal/remnawave
```

Новые тесты: `TestGetUserHwidDevices`, `TestDeleteUserHwidDevice`, `TestDeleteAllUserHwidDevices`,
`TestSubscriptionMenuKeyboard`, `TestDevicesManagementKeyboard`, `TestDevicesManagementKeyboardEmpty`,
`TestBuildDevicesMessage`, `TestDeviceByIndex`.

## Коммиты

- `feat: добавить GetUserHwidDevices в клиент Remnawave`
- `feat: добавить удаление HWID-устройств в клиент Remnawave`
- `feat: клавиатуры и кнопки управления устройствами`
- `feat: хендлеры управления устройствами`
- `test: убрать хардкод-дату expireAt из TestHandleModeratorEarnings`
- `feat: подключить управление устройствами к подписке через reply-подменю`

## Осталось

- **Task 6 — ручная проверка end-to-end** в Telegram (требует поднятого бота: `make up` / `make logs`):
  проверить полный сценарий — подписка → подменю → список устройств → удаление одного → сброс всех
  → отмена → назад, а также кейсы «нет подписки» и «0 устройств». Сверить удаление в панели Remnawave.
