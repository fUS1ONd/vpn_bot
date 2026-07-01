# Ручное продление подписки админом на месяц — дизайн

**Дата:** 2026-07-01
**Ветка:** `feature/admin-extend-month`

## Цель

Дать админу возможность из карточки пользователя («🔍 Информация о пользователе»)
вручную продлить подписку на 1 месяц — «искусственно оплатить». Основной юз-кейс:
пригласить человека как подписчика модератора (цена 400₽/мес), у него активируется
триал на 72 часа, и админ руками продлевает ему подписку на месяц, снимая лимит трафика.

## Ключевое решение: техническое продление, БЕЗ финансового учёта

Продление **не создаёт** запись в `payments` и **не начисляет** earnings модератору.
Это осознанный выбор: иначе отчёт по выплатам разъедется с реальными деньгами Platega
(см. memory `moderator_earnings_quirks`). Продление трогает **только Remnawave**
(двигает `expireAt`, ставит `ACTIVE`, снимает лимит трафика). В БД бота
(`users`, `payments`) ничего не меняется — цена подписки и `moderator_id` остаются как есть.
Scheduler дальше работает с юзером штатно (увидит новый `expireAt`, за 3 дня напомнит).

## Поток (UX)

```
Админ → «Инфо о пользователе» → вводит TG ID
  → карточка юзера + inline-кнопки:
       [➕ Продлить на месяц]   (data = targetID)
       [⬅️ В меню]
    (кнопка продления СКРЫТА для безлимитных подписок expireAt.Year >= 2099)
        ↓ клик «Продлить»
  → c.Edit сообщения на подтверждение:
     «Продлить подписку {имя} до {новая_дата}?»
       [✅ Подтвердить]  (data = targetID)
       [❌ Отмена]       (data = targetID)
        ↓ клик «Подтвердить»
  → getPaymentMutex(targetID).Lock()
  → перечитываем свежего remUser из Remnawave
  → newExpireAt = nextMonthExpireAt(remUser, now)  // пересчёт от СВЕЖЕЙ даты
  → EnableUser(uuid, newExpireAt)   // ACTIVE + trafficLimitBytes=0
  → ClearNotifications(targetID)
  → юзеру: «✅ Ваша подписка продлена до {дата}»
  → админу: c.Edit на «✅ Продлено до {дата}» (кнопки исчезают)
```

## Механика продления

Логика расчёта даты выносится в чистую функцию (переиспользуется обычной оплатой):

```go
// nextMonthExpireAt считает новую дату окончания при продлении на месяц.
// Если подписка активна и не истекла — плюсуем к текущему expireAt (не теряем остаток).
// Иначе (триал истёк, grace, disabled) — считаем от now.
func nextMonthExpireAt(remUser *remnawave.User, now time.Time) time.Time {
    if remUser.ExpireAt.After(now) && remUser.Status == remnawave.StatusActive {
        return remUser.ExpireAt.AddDate(0, 1, 0)
    }
    return now.AddDate(0, 1, 0)
}
```

Существующий `activateSubscription` (payment.go) рефакторится на эту функцию —
устраняем дублирование, поведение эквивалентно текущему.

Продление выполняется через уже существующий `EnableUser(uuid, newExpireAt)`,
который ставит `Status=ACTIVE`, `ExpireAt`, `TrafficLimitBytes=0` (снимает лимит триала).

## Решения по граничным случаям

| Случай | Решение |
|---|---|
| База даты | Всегда к `expireAt` (ACTIVE→+1мес к expireAt, иначе now+1мес) |
| Триальные дни | Приплюсуются (месяц + остаток триала) — принято осознанно |
| Безлимит (2099) | Кнопка **скрыта** — продлевать вечную подписку бессмысленно |
| Забаненный | Отсекается раньше в `processAdminUserInfo` (проверка IsBanned) |
| Нет в БД/Remnawave | Отсекается раньше в `processAdminUserInfo` |
| Stale-дата | На confirm пересчитываем от свежего remUser (не доверяем дате из показа) |
| Дабл-клик confirm | `getPaymentMutex(targetID)` + `c.Edit` убирает кнопки |
| Ошибка EnableUser | Без retry/фонового состояния: показать ошибку админу, юзеру не слать |
| Авторизация | `b.isAdmin(c)` в начале каждого хендлера (инвариант) |

## Механика inline-кнопок (образец из devices.go)

```go
// 1. Unique-константы (keyboards.go)
cbAdminExtendMonth   = "adm_ext_month"
cbAdminExtendConfirm = "adm_ext_ok"
cbAdminExtendCancel  = "adm_ext_cancel"

// 2. Кнопка с параметром targetID — 3-й аргумент попадает в c.Args()
btn := menu.Data("➕ Продлить на месяц", cbAdminExtendMonth, fmt.Sprintf("%d", targetID))

// 3. Регистрация роутинга по Unique (handlers.go, рядом с devices/bug_report)
extMenu := &tele.ReplyMarkup{}
btnExtMonth := extMenu.Data("", cbAdminExtendMonth)
b.Handle(&btnExtMonth, bot.handleAdminExtendMonth)
// ... аналогично confirm/cancel

// 4. Чтение параметра в хендлере
args := c.Args()   // args[0] == targetID
```

## Затрагиваемые файлы

- **Создать** `internal/bot/admin_extend.go` — хендлеры + `nextMonthExpireAt` + текст юзеру
- **Создать** `internal/bot/admin_extend_test.go` — тесты
- **Изменить** `internal/bot/keyboards.go` — 3 cb-константы + `AdminUserInfoKeyboard(targetID, remUser)`
- **Изменить** `internal/bot/admin.go` — `processAdminUserInfo` шлёт inline-клавиатуру вместо reply
- **Изменить** `internal/bot/payment.go` — рефактор `activateSubscription` на `nextMonthExpireAt`
- **Изменить** `internal/bot/handlers.go` — регистрация 3 Unique-обработчиков
- **Удалить** из `internal/remnawave/client.go` мёртвый код `ExtendUserSubscription` +
  `CalculateExtendedExpireAt` и их тесты в `client_test.go`

## Тесты

- `nextMonthExpireAt` (unit, главная ценность):
  - ACTIVE + будущее → expireAt + 1мес
  - триал истёк (не ACTIVE) → now + 1мес
  - DISABLED (grace) → now + 1мес
  - граница месяца (31 янв → 28/29 фев — поведение AddDate)
- confirm-flow (httptest-мок Remnawave по образцу admin_test.go:224):
  - невалидный targetID в args → безопасная ошибка
  - не-админ → отбой (isAdmin-гейт)
  - успешный confirm → PATCH с нужным expireAt и trafficLimitBytes=0
