# Прогресс: закрытие migration gap старых оплат

**Дата:** 2026-03-23
**План:** [2026-03-23-legacy-paid-migration-fix-plan.md](../plans/2026-03-23-legacy-paid-migration-fix-plan.md)

## Что сделано

### DB и модель пользователя
- В `users` добавлен флаг `legacy_paid_migrated`.
- Добавлена безопасная миграция `ALTER TABLE users ADD COLUMN legacy_paid_migrated INTEGER NOT NULL DEFAULT 0`.
- `GetUserByTelegramID`, `GetUserByRemnawaveUUID` и `GetAllUsers` читают новый флаг.
- Добавлены helper-методы:
  - `SetLegacyPaidMigrated`
  - `UpdateSubscriptionPriceAndLegacyPaidMigrated`

### Admin flow смены цены
- Введён отдельный state `StateWaitAdminChangePriceMigrationConfirm`.
- После ввода новой цены бот показывает migration-вопрос только для legacy-case:
  - модераторский инвайт;
  - `subscription_price == nil`;
  - нет confirmed-платежей;
  - в Remnawave пользователь `ACTIVE`;
  - `expireAt` конечный и в будущем.
- До ответа на migration-вопрос цена не пишется в БД.
- Варианты ответа:
  - `✅ Да, считать оплаченной` -> цена сохраняется, `legacy_paid_migrated = true`
  - `❌ Нет, оставить trial` -> цена сохраняется, `legacy_paid_migrated = false`
  - `🚫 Отмена` -> flow завершается без side effect
- При ошибке загрузки пользователя из Remnawave для legacy-candidate flow теперь fail-closed и не продолжает смену цены молча.

### Scheduler и пользовательский UI
- `isTrialUser()` теперь сначала проверяет `LegacyPaidMigrated`.
- Мигрированный legacy-paid пользователь:
  - не считается `trial`, даже без confirmed-платежа;
  - видит `💳 Продлить подписку`, а не `💳 Оплатить подписку`;
  - идёт по paid-ветке scheduler;
  - получает напоминания за 3 дня и 1 день;
  - получает grace period вместо мгновенного кика.

### Тесты
- Добавлены и зафиксированы регрессионные тесты на:
  - persistence `legacy_paid_migrated` во всех scan-path DB;
  - migration-prompt в admin change-price flow;
  - ветки `Yes / No / Cancel`;
  - fail-closed при ошибке lookup в Remnawave;
  - `isTrialUser()` для migrated-user;
  - `BtnRenew` для migrated-user;
  - paid reminder scheduler для migrated-user.

## Верификация

Таргетные проверки:

```bash
GOCACHE=/tmp/go-build go test ./internal/database -run 'TestUserLegacyPaidMigratedPersists|TestUpdateSubscriptionPriceAndLegacyPaidMigrated' -v
GOCACHE=/tmp/go-build go test ./internal/bot -run 'TestAdminChangePriceFlow|TestAdminChangePriceMigrationKeyboardContainsExpectedButtons|TestAdminChangePriceFlow_FailsClosedWhenMigrationLookupFails' -v
GOCACHE=/tmp/go-build go test ./internal/bot -run 'TestIsTrialUserTreatsLegacyPaidMigratedUserAsPaid|TestUserKeyboardShowsRenewForLegacyPaidMigratedUser|TestSchedulerPaidReminderForLegacyPaidMigratedUser' -v
```

Финальная обязательная верификация:
- `make fmt`
- `make tests`
