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

## Post-review фиксы (2026-03-04, коммит ea6cdf1)

По результатам code review выявлены и устранены баги:

### Fix 1 — утечка состояния при ошибке продления
- ✅ `processModExtendConfirm`: при ошибке `ExtendUserSubscription` теперь очищаются `userStates` и `modExtendData` перед возвратом.
  - Без фикса: следующее сообщение модератора снова попадало в обработчик подтверждения.

### Fix 2 — N+1 запросов в «Мои подписчики»
- ✅ `handleModSubscribers`: заменены N поштучных `GetUser(uuid)` на один `GetAllUsers()` + `map[uuid]User` lookup.
  - Единый batch-запрос, как в `handleAdminModStats` и scheduler.

### Fix 3 — 404 при автокике логировался как Warn
- ✅ `handleAutoKick`: добавлена функция `isAutoKickNotFoundError`; при 404 от Remnawave (пользователь уже удалён администратором вручную) логируется на уровне `Debug` вместо `Warn`.

### Fix 4 — хрупкая проверка 403 через strings.Contains
- ✅ `logSchedulerSendError`: заменён `strings.Contains(err.Error(), "403")` на `errors.Is(tele.ErrBlockedByUser / ErrUserIsDeactivated / ErrNotStartedByUser)`.

### Fix 5 — автокик переоткрывал инвайт (критично для монетизации)
- ✅ Добавлена колонка `kicked_at TIMESTAMP` в таблицу `invites` (ALTER TABLE миграция).
- ✅ Добавлена функция `MarkInviteKickedByTelegramID` — проставляет `kicked_at`, не трогает `used_by`.
- ✅ `ClaimInvite` обновлён: отклоняет инвайты с `kicked_at IS NOT NULL`.
- ✅ `handleAutoKick` вызывает `MarkInviteKickedByTelegramID` вместо `ResetInviteUsageByTelegramID`.
- Эффект: кикнутый пользователь не может зайти по старой ссылке без нового инвайта от модератора.

### Fix 6 — состояние диалога продления не сбрасывалось в терминальных ветках
- ✅ В `processModExtendID` добавлен `b.userStates.Delete(moderatorID)` перед каждым `return` с `ModeratorMenuKeyboard`:
  - ошибка БД при проверке владения;
  - `dbUser == nil` (пользователь удалён);
  - 404 от Remnawave;
  - `CalculateExtendedExpireAt` вернул ошибку (слишком рано продлевать).

### Fix 7 — некорректный поиск инвайта после автокика и повторного захода

По результатам code review (chatgpt-codex-connector) выявлены и устранены два SQL-бага:

**P2 — `GetInviteByUsedBy` без фильтра `kicked_at` и без `ORDER BY`**
- Если пользователь был кикнут и вернулся по новому инвайту, в таблице два ряда с одним `used_by`. `LIMIT 1` без сортировки возвращал произвольный — мог вернуть старый инвайт бывшего куратора.
- Фикс: добавлен `AND kicked_at IS NULL ORDER BY used_at DESC` в запрос `GetInviteByUsedBy`.
- Добавлен тест `TestGetInviteByUsedBy_AfterKickAndRejoin`.

**P1 — `IsSubscriberOfModerator` не фильтровал кикнутые инвайты (утечка прав)**
- Старый модератор A мог продлить подписку пользователя, перешедшего к модератору B, потому что `EXISTS` находил его старый кикнутый инвайт.
- Фикс: добавлен `AND kicked_at IS NULL` в `IsSubscriberOfModerator`.
- Добавлен тест `TestIsSubscriberOfModerator_AfterKickAndRejoin`.

## Отклонения от плана

1. В шаге выбора подписчика для продления используется ввод `telegram_id` из списка, без отдельной нумерации строк. Это сохраняет целевой UX (продление по ID) и упрощает проверку владения подписчиком.

## Проверки

- `GOCACHE=/tmp/go-build go test ./...` — успешно.
- `make fmt` — выполнено.
- `make tests` — выполнено.
