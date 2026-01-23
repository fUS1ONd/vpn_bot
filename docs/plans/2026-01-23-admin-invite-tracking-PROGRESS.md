# Прогресс: Отслеживание промокодов и уведомления админа

**План:** [2026-01-23-admin-invite-tracking-design.md](./2026-01-23-admin-invite-tracking-design.md)
**Дата начала:** 2026-01-23
**Статус:** Завершён

## Шаги выполнения

### Шаг 1: Обновление БД
- [x] Добавить миграцию для `first_name` в таблице `users`
- [x] Добавить миграцию для `used_at` в таблице `invites`
- [x] Обновить структуру `User` в `database/db.go`
- [x] Обновить структуру `Invite` в `database/db.go`

### Шаг 2: Remnawave API
- [x] Расширить `UpdateUserRequest` полем `Username`
- [x] Добавить метод `UpdateUsername()` в `remnawave/client.go`

### Шаг 3: Механизм Upsert
- [x] Добавить `UpdateUserInfo()` в `database/users.go`
- [x] Обновить `GetUserByTelegramID()` для чтения `first_name`
- [x] Добавить `syncUserInfo()` в `bot/handlers.go`
- [x] Вызывать синхронизацию при каждом /start

### Шаг 4: Управление кодами
- [x] Добавить `InviteWithUser` структуру в `database/invites.go`
- [x] Добавить `GetAllInvitesWithUsers()` в `database/invites.go`
- [x] Добавить `DeleteUnusedInvite()` в `database/invites.go`
- [x] Добавить константы кнопок `BtnAdminViewInvites`, `BtnAdminDeleteInvite` в `bot/keyboards.go`
- [x] Обновить `AdminManageKeyboard()` в `bot/keyboards.go`
- [x] Добавить `handleViewInvites()` в `bot/admin.go`
- [x] Добавить `formatInvitesList()` в `bot/admin.go`
- [x] Добавить `handleDeleteInviteRequest()` в `bot/admin.go`
- [x] Добавить `processDeleteInvite()` в `bot/admin.go`
- [x] Добавить состояние `StateWaitDeleteInvite` в `bot/admin.go`
- [x] Добавить обработку `StateWaitDeleteInvite` в `bot/handlers.go`
- [x] Добавить роутинг кнопок `BtnAdminViewInvites`, `BtnAdminDeleteInvite` в `handleTextMessage()`

### Шаг 5: Уведомления
- [x] Обновить `UseInvite()` для записи `used_at`
- [x] Добавить `notifyAdminNewUser()` в `bot/handlers.go`
- [x] Вызывать уведомление в `processInviteCode()` (асинхронно через goroutine)

### Шаг 6: Верификация
- [x] Запустить `go build ./...` — успешно
- [x] Запустить `go vet ./...` — успешно
- [x] Запустить тесты `go test ./...` — успешно

### Шаг 7: Документация
- [x] Обновить `README.md` с новыми возможностями
- [x] Обновить `CLAUDE.md` с новыми возможностями

## Лог изменений

### 2026-01-23
- Начата реализация плана
- Реализованы все шаги плана
- Обновлена документация (README.md, CLAUDE.md)
- План завершён

## Изменённые файлы

- `internal/database/db.go` — добавлены поля `FirstName` в User, `UsedAt` в Invite, миграции
- `internal/database/users.go` — обновлены методы чтения для first_name, добавлен `UpdateUserInfo()`
- `internal/database/invites.go` — добавлены `InviteWithUser`, `GetAllInvitesWithUsers()`, `DeleteUnusedInvite()`, обновлён `UseInvite()`
- `internal/remnawave/client.go` — добавлено поле `Username` в UpdateUserRequest, добавлен метод `UpdateUsername()`
- `internal/bot/keyboards.go` — добавлены кнопки `BtnAdminViewInvites`, `BtnAdminDeleteInvite`, обновлён `AdminManageKeyboard()`
- `internal/bot/handlers.go` — добавлены `syncUserInfo()`, `notifyAdminNewUser()`, обработка новых кнопок и состояний
- `internal/bot/admin.go` — добавлены `handleViewInvites()`, `formatInvitesList()`, `handleDeleteInviteRequest()`, `processDeleteInvite()`, состояние `StateWaitDeleteInvite`
- `internal/bot/handlers_test.go` — обновлён вызов CreateUser с новым параметром first_name
- `cmd/migrator/main.go` — обновлён вызов CreateUser с пустым first_name
- `README.md` — обновлена документация
- `CLAUDE.md` — обновлена документация
