# Этап 6: UI пользователя с оплатой

**Дата:** 2026-03-23
**План:** [2026-03-22-payment-implementation-plan.md](../plans/2026-03-22-payment-implementation-plan.md), строки 1633–1801
**Доп. контекст:** [2026-03-21-user-ui-redesign.md](../plans/2026-03-21-user-ui-redesign.md)
**Коммит:** `feat: этап 6 — UI пользователя с оплатой`

## Что сделано

### keyboards.go
- Удалены старые пользовательские кнопки `BtnConnect` и `BtnDonate`
- Добавлены кнопки оплаты: `BtnPay`, `BtnRenew`, `BtnPaySBP`, `BtnPayCard`, `BtnPayCrypto`, `BtnCheckPayment`
- `BtnInfo` переименована в `ℹ️ Информация`
- Добавлена единая динамическая клавиатура `UserMenuKeyboardDynamic(payButtonText, showPayButton, isModerator)`
- Добавлены `PaymentMethodKeyboard()` и `PaymentWaitKeyboard()`

### messages.go
- Добавлен `subscriptionType` для UI: `trial`, `paid`, `grace`, `infinite`
- `FormatUserStatus` переработан под четыре типа подписки
- Добавлен отдельный `MsgGraceWarning` для тревожного экрана при `/start`

### payment_handler.go
- Создан обработчик пользовательского flow оплаты
- Добавлены состояния `StateWaitPaymentMethod` и `StateWaitPaymentResult`
- Реализованы:
  - `handlePayButton`
  - `handlePaymentMethodSelected`
  - `handleCheckPayment`

### handlers.go
- `handleTextMessage` обрабатывает:
  - `BtnPay` / `BtnRenew`
  - выбор метода оплаты
  - `BtnCheckPayment`
- При нажатии кнопок главного меню во время payment flow состояние оплаты сбрасывается
- `handleStart` показывает тревожный экран для пользователей в grace period
- `userKeyboard` строит динамическое меню по цене, типу подписки, Platega и роли модератора
- `processInviteCode` теперь:
  - создаёт trial на фиксированные `72h`
  - задаёт `trafficLimitBytes` из `TRIAL_TRAFFIC_LIMIT_GB`
  - копирует `subscription_price` из инвайта
  - заполняет `moderator_id` только для модераторских инвайтов

### Тесты
- Обновлены тесты клавиатур под новый UI
- Обновлены тесты `FormatUserStatus` под новую сигнатуру и grace period
- Добавлены тесты:
  - trial создаётся на 72 часа
  - `moderator_id` копируется только у модераторских инвайтов
  - payment flow сбрасывается при возврате в главное меню

## Проверка

- `GOCACHE=/tmp/go-build go test ./internal/bot/... -count=1`
- Далее полный обязательный прогон: `make fmt && make tests`
