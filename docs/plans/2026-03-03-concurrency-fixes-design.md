# Фиксы конкурентности и безопасности — План реализации

**Goal:** Исправить race condition в `userStates`, TOCTOU на инвайт-код, мёртвый параметр рассылки, защиту от само-бана, переполнение Telegram-сообщения при большом числе инвайтов.

**Источник:** Code Review в `docs/plans/2026-03-02-remove-traffic-limits-design.md`

**Подход:** TDD — сначала тест, потом реализация.

---

### Task 1: Race condition в `userStates` — заменить `map` на `sync.Map`

**Проблема:** `userStates map[int64]string` — обычная map без защиты. Telebot обрабатывает сообщения конкурентно → data race → паника.

**Files:**
- Modify: `internal/bot/handlers.go` — заменить `map[int64]string` на `sync.Map`
- Modify: все использования `b.userStates[id]`, `delete(b.userStates, id)`, `b.userStates[id] = ...`

**Решение:** Заменить `userStates map[int64]string` на `sync.Map`. Обновить все точки доступа:
- Запись: `b.userStates.Store(id, state)`
- Чтение: `val, ok := b.userStates.Load(id); state = val.(string)`
- Удаление: `b.userStates.Delete(id)`

**Тест:** Конкурентный тест — запуск N горутин, одновременно пишущих/читающих из userStates. Не должно быть паники.

---

### Task 2: TOCTOU на инвайт-код — атомарная активация

**Проблема:** Между проверкой инвайта и его пометкой как использованного — окно для race condition.

**Files:**
- Modify: `internal/database/invites.go` — новый метод `ClaimInvite(code, usedBy)` (атомарный SELECT+UPDATE в одной транзакции)
- Modify: `internal/bot/handlers.go` — `processInviteCode` использует `ClaimInvite` вместо `GetInviteByCode` + `UseInvite`

**Решение:** Создать метод `ClaimInvite(code string, usedBy int64) error` — один SQL `UPDATE invites SET used_by=?, used_at=CURRENT_TIMESTAMP WHERE code=? AND used_by IS NULL` с проверкой `RowsAffected`. Если 0 — код не найден или уже использован.

В `processInviteCode`:
1. Вызвать `ClaimInvite` — если ошибка, сообщить пользователю
2. Только после успеха — создать пользователя в Remnawave
3. Если создание Remnawave не удалось — откатить инвайт (`UnclamInvite`)

**Тест:** Вызвать `ClaimInvite` дважды с одним кодом — второй должен вернуть ошибку.

---

### Task 3: Удалить мёртвый параметр `activeOnly` в `processBroadcastMessage`

**Проблема:** Параметр `_ bool` не используется. `StateWaitBroadcastAll` обрабатывается, но результат идентичен `StateWaitBroadcastActive`.

**Files:**
- Modify: `internal/bot/admin.go` — убрать параметр `_ bool`, оставить `processBroadcastMessage(c tele.Context) error`
- Modify: `internal/bot/handlers.go` — убрать `StateWaitBroadcastAll` и его обработку, обновить вызовы

**Тест:** Убедиться что компиляция проходит и `StateWaitBroadcastAll` не используется.

---

### Task 4: Защита от само-бана администратора

**Проблема:** Админ может ввести свой `ADMIN_ID` в бан и удалить себя из Remnawave.

**Files:**
- Modify: `internal/bot/admin.go` — добавить проверку `telegramID == b.config.AdminID` в `processBanUser`

**Тест:** Вызов `processBanUser` с AdminID должен возвращать сообщение об ошибке, а не удалять.

---

### Task 5: Разбиение длинного списка инвайтов

**Проблема:** При большом числе инвайтов Telegram вернёт ошибку (лимит 4096 символов).

**Files:**
- Modify: `internal/bot/admin.go` — `handleViewInvites` разбивает на части по 4000 символов

**Тест:** Создать список инвайтов > 4096 символов, проверить что `formatInvitesList` возвращает чанки.

---

### Task 6: Финальная верификация

- `make fmt`
- `make tests`
- Обновить `CLAUDE.md` если нужно

---

## Порядок выполнения

Task 1 → Task 2 → Task 3 → Task 4 → Task 5 → Task 6

Task 1 — самый критичный (паника в проде). Task 2 — следующий по важности.
