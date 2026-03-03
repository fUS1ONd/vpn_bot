# Прогресс: фиксы конкурентности и безопасности

**План:** [docs/plans/2026-03-03-concurrency-fixes-design.md](../plans/2026-03-03-concurrency-fixes-design.md)

**Дата:** 2026-03-03

**Статус:** ✅ Выполнено

---

## Выполнено

### Task 1: Race condition в `userStates`

- ✅ Создан `internal/bot/user_states.go` — обёртка над `sync.Map`
- ✅ Заменён `map[int64]string` на `*UserStates` в структуре `Bot`
- ✅ Обновлены все точки доступа в `handlers.go` и `admin.go`
- ✅ Обновлены существующие тесты под новый API
- ✅ Добавлен тест `TestUserStatesConcurrentAccess` с `-race`

### Task 2: TOCTOU на инвайт-код

- ✅ Добавлен `ClaimInvite` — атомарный claim инвайта
- ✅ Добавлен `UnclaimInvite` — откат при ошибке создания пользователя
- ✅ `processInviteCode` переписан: сначала claim, потом Remnawave, с откатом при ошибке
- ✅ Тесты `TestClaimInviteAtomicity` и `TestClaimInviteNonExistent`

### Task 3: Мёртвый параметр `activeOnly`

- ✅ Удалён параметр `_ bool` из `processBroadcastMessage`
- ✅ Удалена константа `StateWaitBroadcastAll` и её обработка
- ✅ Упрощён роутинг рассылки в `handlers.go`

### Task 4: Защита от само-бана

- ✅ Добавлена проверка `telegramID == b.config.AdminID` в `processBanUser`
- ✅ Тест `TestProcessBanUserRejectsSelfBan`

### Task 5: Разбиение длинного списка инвайтов

- ✅ Добавлена `FormatInvitesListChunked` с разбиением по лимиту 4000 символов
- ✅ `handleViewInvites` отправляет несколько сообщений при превышении лимита
- ✅ Удалена устаревшая `formatInvitesList`
- ✅ Тест `TestFormatInvitesListChunking`

### Task 6: Финальная верификация

- ✅ `make fmt` — PASS
- ✅ `make tests` — PASS

---

## TDD-верификация

Все фиксы разрабатывались по TDD (RED → GREEN):

| Тест | Что проверяет |
|------|--------------|
| `TestUserStatesConcurrentAccess` | Конкурентный доступ к userStates без паники |
| `TestClaimInviteAtomicity` | Двойной claim одного инвайта невозможен |
| `TestClaimInviteNonExistent` | Claim несуществующего кода |
| `TestProcessBanUserRejectsSelfBan` | Админ не может забанить себя |
| `TestFormatInvitesListChunking` | Длинный список разбивается на части |

Команды:
- `make fmt` ✅
- `make tests` ✅
