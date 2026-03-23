# Этап 7: UI модератора с заработком

**Дата:** 2026-03-23
**План:** [2026-03-22-payment-implementation-plan.md](../plans/2026-03-22-payment-implementation-plan.md), строки 1804–1892
**Доп. контекст:** [2026-03-21-moderator-ui-redesign.md](../plans/2026-03-21-moderator-ui-redesign.md)
**Коммит:** `feat: этап 7 — UI модератора с заработком`

## Что сделано

### keyboards.go
- Удалена кнопка ручного продления `BtnModExtend`
- Добавлены новые кнопки `BtnModEarnings` и `BtnModChangePrice`
- `ModeratorMenuKeyboard()` обновлён под новый сценарий модератора
- Добавлен `ModeratorSubscribersKeyboard()` для списка подписчиков

### moderator.go
- Создание инвайта переведено на двухшаговый flow:
  - `handleModeratorCreateInvite()`
  - `StateWaitModInvitePrice`
  - `processModeratorInvitePrice()`
- Инвайт модератора теперь создаётся через `CreateInviteWithPrice()` с валидацией `MIN_SUBSCRIPTION_PRICE`
- `handleModSubscribers()` показывает:
  - тип подписки (`триал`, `оплачено`, `grace period`, `истёк`, `удалён`)
  - дату / остаток дней
  - цену подписки
  - агрегаты по типам внизу списка
- Добавлен `handleModeratorEarnings()` с live-статистикой за текущий месяц и за всё время
- Добавлен flow изменения цены триального подписчика:
  - `StateWaitModChangePriceID`
  - `StateWaitModChangePriceValue`
  - `handleModChangePriceRequest()`
  - `processModChangePriceID()`
  - `processModChangePriceValue()`
- Полностью удалён старый сценарий ручного продления подписки

### handlers.go
- Удалены состояния и роутинг старого moderator extend flow
- Добавлена обработка:
  - `StateWaitModInvitePrice`
  - `StateWaitModChangePriceID`
  - `StateWaitModChangePriceValue`
- Добавлены маршруты для `BtnModEarnings` и `BtnModChangePrice`
- Из `Bot` удалены pending-данные продления, вместо них добавлена сессия изменения цены

### invites.go
- `Subscriber` расширен полем `SubscriptionPrice`
- `GetSubscribersByModerator()` теперь возвращает цену подписки через `COALESCE(u.subscription_price, i.subscription_price)`
- Добавлен `UpdateInviteSubscriptionPrice()` для синхронизации цены в истории инвайта

### Тесты
- Обновлены тесты клавиатуры под новый модераторский UI
- Добавлены тесты на:
  - запуск flow создания инвайта с ценой
  - создание инвайта с `subscription_price`
  - валидацию минимальной цены
  - enriched-список подписчиков с типом и ценой
  - экран заработка модератора
  - смену цены триального подписчика
  - запрет смены цены уже оплатившему подписчику

## Проверка

- `make fmt`
- `make tests`
