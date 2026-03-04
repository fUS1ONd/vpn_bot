# Прогресс: монетизация через модераторов

План: [2026-03-03-moderator-monetization-design.md](../plans/2026-03-03-moderator-monetization-design.md)

Дата выполнения: 2026-03-04

## Статус по задачам

### Task 1 — `invites.expire_days`
- ✅ Добавлена миграция `expire_days`.
- ✅ Добавлена таблица `banned_users`.
- ✅ Добавлена таблица `notifications_sent`.
- ✅ Расширен DB-слой: `CreateInviteWithExpiry`, `GetInviteByUsedBy`, `GetSubscribersByModerator`, `IsSubscriberOfModerator`, `ResetInviteUsageByTelegramID`.

### Task 2 — срок подписки по типу инвайта
- ✅ `CreateUser` в Remnawave принимает `expireAt`.
- ✅ В `processInviteCode` срок берётся из `invite.expire_days`:
  - `NULL` -> `2099-01-01`.
  - `30` -> `now + 30d`.
- ✅ Админские инвайты создаются бессрочными (`nil`), модераторские — `30`.
- ✅ В `Мои приглашения` добавлен Telegram ID активировавшего пользователя.

### Task 3 — кнопка «Мои подписчики»
- ✅ Добавлена кнопка `👥 Мои подписчики`.
- ✅ Реализован `handleModSubscribers` с LEFT JOIN-логикой (включая удалённых).
- ✅ Для активных/истёкших пользователей статусы берутся из Remnawave.
- ✅ Добавлена маршрутизация в `handleTextMessage`.

### Task 4 — кнопка «Продлить подписку»
- ✅ Добавлена кнопка `⏳ Продлить подписку`.
- ✅ Добавлен диалог продления со state-машиной:
  - `StateWaitModExtendID`
  - `StateWaitModExtendConfirm`
- ✅ Реализована проверка «только своих подписчиков».
- ✅ Добавлена логика расчёта `newExpireAt` и защита от раннего продления (>30 дней до конца).
- ✅ При успешном продлении очищаются метки `notifications_sent` пользователя.

### Task 4b — разделение бана и автокика
- ✅ Добавлен DB-слой банов (`BanUser`, `IsBanned`).
- ✅ `processBanUser` теперь:
  - пишет запись в `banned_users`;
  - удаляет пользователя из Remnawave и `users`;
  - очищает `notifications_sent`;
  - сохраняет историю использованного инвайта (`used_by` не сбрасывается).
- ✅ `/start` проверяет `IsBanned` и блокирует доступ.

### Task 5 — scheduler уведомлений и автокика
- ✅ Создан `internal/bot/scheduler.go`.
- ✅ Запуск scheduler добавлен в `cmd/bot/main.go`.
- ✅ Первый запуск рассчитывается на ближайшие 12:00 MSK, далее каждые 24 часа.
- ✅ Batch-загрузка пользователей Remnawave (`GetAllUsers`).
- ✅ Реализованы уведомления `expire_3d` и `expire_today` с anti-duplicate через `notifications_sent`.
- ✅ Автокик через 3 дня после истечения:
  - удаление из Remnawave;
  - удаление из `users`;
  - сброс `used_by/used_at` в инвайте;
  - очистка `notifications_sent`;
  - без записи в `banned_users`.

### Task 6 — статистика модераторов для админа
- ✅ Добавлена кнопка `📊 Статистика` в меню модераторов админа.
- ✅ Реализован `handleAdminModStats`:
  - batch Remnawave users;
  - `Всего приглашено` по invites (включая удалённых из users);
  - `Активных/Истекших` по данным Remnawave.

### Task 7 — валидация назначения модератора
- ✅ В `processAddModerator` добавлена проверка инвайта пользователя:
  - месячный инвайт (`expire_days != NULL`) -> отказ;
  - бессрочный или старый пользователь без записи инвайта -> разрешено.

### Task 8 — финальная верификация и документация
- ✅ Обновлены `README.md` и `CLAUDE.md`.
- ✅ Добавлен этот progress-файл.

## Отклонения от плана

1. В шаге выбора подписчика для продления используется ввод `telegram_id` из списка, без отдельной нумерации строк. Это сохраняет целевой UX (продление по ID) и упрощает проверку владения подписчиком.

## Проверки

- `GOCACHE=/tmp/go-build go test ./...` — успешно.
- `make fmt` — выполнено.
- `make tests` — выполнено.
