# Прогресс: перевод подписки с месячной на бессрочную

**План:** [docs/plans/2026-03-08-switch-subscription-type-design.md](../plans/2026-03-08-switch-subscription-type-design.md)

**Дата выполнения:** 2026-03-08

**Статус:** ✅ Выполнено

---

## Что было сделано

### 1. Обновлён DB-слой инвайтов

**Файлы:** `internal/database/invites.go`, `internal/database/invites_ext_test.go`

- Добавлен метод `UpdateInviteExpireDays(usedBy int64, expireDays *int)`.
- Перевод на бессрочный тариф теперь обновляет `invites.expire_days` по `used_by`.
- Добавлены TDD-тесты на успешный перевод в `NULL` и на ошибку при отсутствии инвайта.

### 2. Добавлен flow «Сменить тариф» в админке

**Файлы:** `internal/bot/admin.go`, `internal/bot/handlers.go`, `internal/bot/keyboards.go`, `internal/bot/admin_test.go`, `internal/bot/keyboards_test.go`

- В `AdminManageKeyboard()` добавлена кнопка `♾️ Сменить тариф`.
- Добавлены состояния:
  - `StateWaitSwitchSubscriptionID`
  - `StateWaitSwitchSubscriptionConfirm`
- Реализован диалог:
  - ввод `telegram_id`;
  - проверка граничных случаев;
  - показ карточки подтверждения;
  - подтверждение кнопкой `Да` или отмена через `🚫 Отмена`.
- Добавлена in-memory сессия подтверждения для админа, аналогично moderator-flow продления.

### 3. Реализована бизнес-логика перевода

**Файл:** `internal/bot/admin.go`

- Проверяются кейсы:
  - пользователь уже бессрочный;
  - пользователь не найден;
  - пользователь забанен;
  - инвайт не найден;
  - ошибки Remnawave API.
- Карточка подтверждения показывает:
  - имя пользователя;
  - куратора;
  - текущий срок подписки из Remnawave.
- При подтверждении выполняется порядок из плана:
  1. `EnableUser()` для `EXPIRED` / `DISABLED`;
  2. PATCH `expireAt = 2099-01-01T00:00:00Z`;
  3. `UpdateInviteExpireDays(..., nil)`;
  4. `ClearNotifications()`.

### 4. Расширен клиент Remnawave

**Файл:** `internal/remnawave/client.go`

- Добавлен общий метод `UpdateUser(uuid string, req UpdateUserRequest)`.
- `UpdateUsername()` переведён на переиспользование `UpdateUser()`.
- `ExtendUserSubscription()` также использует новый PATCH-хелпер.

### 5. Обновлена документация проекта

**Файл:** `README.md`

- В раздел возможностей администратора добавлена кнопка `♾️ Сменить тариф`.
- В описании логики подписок добавлен сценарий перевода месячной подписки в бессрочную.

---

## Тесты

- Добавлены TDD-тесты:
  - `TestUpdateInviteExpireDays`
  - `TestProcessSwitchSubscriptionID_ValidationErrors`
  - `TestProcessSwitchSubscription_ConfirmFlow`
- Обновлён тест клавиатуры админки на наличие новой кнопки.

---

## Коммиты

- `feat: добавить перевод подписки в бессрочную`
