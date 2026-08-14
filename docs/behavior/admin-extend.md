# Ручное продление подписки админом

Как админ продлевает подписку пользователя на месяц из админ-панели.

## Вход

В карточке пользователя (админ-панель, «🔍 Инфо о пользователе») доступна
inline-кнопка «➕ Продлить на месяц».

Кнопка скрыта для безлимитных подписок (`expireAt.Year >= 2099`).

## Что делает продление

Продление чисто техническое: **не создаёт** запись в `payments`. Меняется только
Remnawave — `EnableUser`:

- двигает `expireAt` (+1 месяц к текущей дате, если подписка `ACTIVE` или `LIMITED`
  и не истекла, иначе от текущего момента);
- ставит `Status=ACTIVE`;
- снимает лимит трафика.

Статус `LIMITED` (исчерпан лимит трафика триала, но срок ещё не истёк) учитывается
наравне с `ACTIVE` — иначе триальный пользователь с исчерпанным трафиком терял бы
остаток дней при ручном продлении.

Логика расчёта даты вынесена в чистую функцию `nextMonthExpireAt`, переиспользуемую
обычной оплатой в `activateSubscription`.

## Защита от дабл-клика — два уровня

1. `getPaymentMutex` сериализует конкурентные вызовы по telegram_id (мьютекс общий
   с платёжным callback).
2. Поле `adminExtendCooldown sync.Map` в `Bot` (telegram_id → время последнего
   успешного продления) дедуплицирует повторные подтверждения: если confirm-callback
   приходит повторно в течение `adminExtendCooldownWindow` (10 секунд) после
   успешного продления, `EnableUser` повторно не вызывается, админ получает алерт
   «Подписка уже продлена, повторное нажатие проигнорировано».

## Границы критической секции

Само продление вынесено в `applyAdminExtend(targetID int64) (time.Time, error)` —
критическая секция `getPaymentMutex` держит внутри себя только чтение/запись
Remnawave-состояния и кулдаун-проверку. Отправка уведомления пользователю и ответ
админу (`c.Edit` / `c.Respond`) выполняются уже после разблокировки мьютекса в
`handleAdminExtendConfirm`, чтобы не задерживать параллельный платёжный callback на
время похода в Telegram Bot API.

## Ошибки и тексты

Типизированные ошибки (`errAdminExtendCooldown`, `errAdminExtendUserNotFound`,
`errAdminExtendLoadFailed`, `errAdminExtendEnableFailed`) маппятся в текст алерта
функцией `adminExtendErrorAlert`.

Текст уведомления пользователю строит чистая функция
`extendedSubscriptionMessage(newExpireAt time.Time) string` — без побочных сетевых
вызовов, принимает уже посчитанную дату параметром.

## Код

- `internal/bot/admin_extend.go` — хендлеры `handleAdminExtendMonth` /
  `handleAdminExtendConfirm` / `handleAdminExtendCancel`.
- `internal/bot/keyboards.go` — клавиатуры `AdminUserInfoKeyboard`,
  `AdminExtendConfirmKeyboard`.
