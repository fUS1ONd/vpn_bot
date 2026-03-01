# Прогресс: Deep Link инвайты + система модераторов

**План:** [docs/plans/2026-03-02-invites-deeplink-moderators-design.md](../plans/2026-03-02-invites-deeplink-moderators-design.md)

**Дата выполнения:** 2026-03-02

**Статус:** ✅ Выполнено

---

## Что было сделано

### 1. Deep Link инвайты

**Файлы:** `internal/bot/handlers.go`, `internal/bot/messages.go`

- `handleStart` обрабатывает `c.Message().Payload` для автоматической активации кода
- Новый пользователь с валидным payload → автоматическая регистрация
- Новый пользователь с невалидным payload → ошибка + `StateWaitInvite` для ручного ввода
- Существующий пользователь с payload → код игнорируется, показывается меню
- `MsgInviteCreated` обновлён на формат deep link: `https://t.me/<bot>?start=<code>`

### 2. Система модераторов

#### БД (`internal/database/`)

- **`db.go`** — миграция таблицы `moderators`
- **`moderators.go`** (новый) — `AddModerator`, `IsModerator`, `GetModerator`, `GetAllModerators`, `RemoveModerator`
- **`invites.go`** — `GetInvitesWithUsersByCreator`, `DeleteUnusedInviteByOwner`, `DeleteUnusedInvitesByCreator`, автор в `GetAllInvitesWithUsers`

#### Обработчики модератора (`internal/bot/moderator.go`, новый)

- `handleModeratorMenu` — подменю модератора
- `handleModeratorCreateInvite` — создание инвайта с deep link
- `handleModeratorViewInvites` — список своих приглашений
- `handleModeratorDeleteInviteRequest` / `processModeratorDeleteInvite` — удаление своих кодов
- `handleModeratorBack` — возврат в пользовательское меню
- `cascadeDeleteModerator` — каскадное удаление инвайтов и роли

#### Админ-панель (`internal/bot/admin.go`)

- `handleAdminModeratorMenu` — подменю управления модераторами
- `handleAdminAddModeratorRequest` / `processAddModerator` — назначение модератора
- `handleAdminListModerators` — список модераторов с количеством приглашённых
- `handleAdminRemoveModeratorRequest` / `processRemoveModerator` — снятие модератора
- Каскадное удаление при бане: `processBanUser` проверяет модератора

#### Клавиатуры (`internal/bot/keyboards.go`)

- `UserMenuKeyboardModerator()` — меню пользователя с кнопкой «Приглашения»
- `ModeratorMenuKeyboard()` — подменю модератора
- `AdminModeratorKeyboard()` — подменю управления модераторами
- `AdminManageKeyboard()` — обновлено с кнопкой «Модераторы»

#### Роутинг (`internal/bot/handlers.go`)

- Новые состояния: `StateWaitModDeleteInvite`, `StateWaitAddModerator`, `StateWaitRemoveModerator`
- Кнопки модератора и админские кнопки добавлены в `handleTextMessage`
- `handleStart` показывает `UserMenuKeyboardModerator` для модераторов

### 3. Потокобезопасность userStates

**Файл:** `internal/bot/state_map.go` (новый)

- `stateMap` — обёртка с `sync.RWMutex`
- Методы: `Get`, `Set`, `Delete`
- Заменён `map[int64]string` во всех файлах

### 4. Автор в списке инвайтов

**Файлы:** `internal/database/invites.go`, `internal/bot/admin.go`

- `InviteWithUser` дополнен полями `CreatorUsername`, `CreatorFirstName`
- `GetAllInvitesWithUsers` делает JOIN с авторами
- `formatInvitesList` отображает автора каждого кода

---

## Тесты

### Новые тестовые файлы

- `internal/database/moderators_test.go` — CRUD модераторов (8 тестов)
- `internal/database/invites_ext_test.go` — новые методы инвайтов (8 тестов)
- `internal/bot/state_map_test.go` — потокобезопасность (5 тестов + конкурентный)
- `internal/bot/moderator_test.go` — обработчики модераторов (14 тестов)

### Обновлённые тесты

- `internal/bot/handlers_test.go` — deep link сценарии (5 тестов)

---

## Подход

Разработка выполнена по TDD: сначала писались падающие тесты (RED), затем минимальный код для прохождения (GREEN). Каждый шаг верифицирован через `make tests` и `make fmt`.

Оба замечания GPT были реальными:

1. Потеря клавиатуры модератора — handleStatus, handleConnect, handleDonate, handleBack, handleUserMode возвращали  
   UserMenuKeyboard() без проверки роли. Теперь все используют хелпер b.userKeyboard(telegramID), который проверяет  
   isModerator.
2. Маскирование ошибок БД — strings.Contains(err.Error(), "add moderator") всегда срабатывало, потому что обёртка  
   AddModerator добавляет эту строку ко всем ошибкам. Убрал, оставил только "UNIQUE" для обнаружения дубликатов.
