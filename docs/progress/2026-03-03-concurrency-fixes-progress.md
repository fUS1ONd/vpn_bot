# Прогресс: фиксы конкурентности и безопасности

**План:** [docs/plans/2026-03-03-concurrency-fixes-design.md](../plans/2026-03-03-concurrency-fixes-design.md)

**Дата:** 2026-03-03

**Статус:** ✅ Выполнено (+ дополнительный фикс 2026-03-04)

---

## Выполнено

### Task 1: Race condition в `userStates`

- ✅ При ребейзе на main скипнут — main уже содержал `internal/bot/state_map.go` с `sync.RWMutex`
- ✅ Структура `Bot` использует `*stateMap` (из main), все точки доступа через `.Get()`, `.Set()`, `.Delete()`
- ✅ Тесты обновлены под API `stateMap` (`newStateMap()` вместо `NewUserStates()`)

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
| `TestStateMap*` (из main, `state_map_test.go`) | Конкурентный доступ к stateMap без паники |
| `TestClaimInviteAtomicity` | Двойной claim одного инвайта невозможен |
| `TestClaimInviteNonExistent` | Claim несуществующего кода |
| `TestProcessBanUserRejectsSelfBan` | Админ не может забанить себя |
| `TestFormatInvitesListChunking` | Длинный список разбивается на части |

Команды:
- `make fmt` ✅
- `make tests` ✅

---

## Дополнительный фикс: Reconcile orphaned invites (2026-03-04)

**Источник:** Code review от chatgpt-codex-connector (P2)

**Проблема:** После `ClaimInvite` бот мог упасть до `CreateUser`. При перезапуске инвайт оставался навсегда помеченным как использованный, хотя пользователь не был создан. Affected signups блокировались до выдачи нового кода вручную.

**Решение:** При старте бота вызывается `db.ReconcileOrphanedInvites()` — откатывает все инвайты с `used_by IS NOT NULL`, у которых нет соответствующей записи в `users`.

**Изменения:**
- `internal/database/invites.go` — новый метод `ReconcileOrphanedInvites() (int, error)`
- `cmd/bot/main.go` — вызов reconcile сразу после инициализации БД, логирование если были откаты

**Тесты (TDD):**
- `TestReconcileOrphanedInvites` — "зависший" инвайт (нет пользователя, claimed < 1 часа) откатывается
- `TestReconcileOrphanedInvites_SkipsValidClaims` — инвайт активного пользователя не трогается
- `TestReconcileOrphanedInvites_SkipsBannedUserInvites` — инвайт забаненного (claimed > 1 часа назад) не трогается

**Исправление P1 (code review chatgpt-codex-connector):** первоначальная реализация откатывала ВСЕ инвайты без пользователя в `users`, включая инвайты забаненных. Исправлено добавлением условия `AND used_at >= datetime('now', '-1 hour')` — откатываются только свежие claims (< 1 часа), которые могут быть следствием краша при регистрации.

- `make fmt` ✅
- `make tests` ✅
