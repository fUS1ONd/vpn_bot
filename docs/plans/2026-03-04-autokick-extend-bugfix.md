# Bugfix: автокик и диалог продления — план исправлений

> **For Claude:** REQUIRED SUB-SKILL: Use superpowers:executing-plans to implement this plan task-by-task.

**Goal:** Исправить два бага: (1) автокик делает старый инвайт переиспользуемым, ломая монетизацию; (2) в части веток processModExtendID состояние диалога не сбрасывается.

**Architecture:**

- **Bug 1:** `ResetInviteUsageByTelegramID` обнуляет `used_by/used_at`, делая код снова активным. Правильное поведение при автокике — **пометить инвайт как истёкший** через новое поле `kicked_at`, но **не обнулять** `used_by`. Тогда код нельзя использовать повторно, но история сохраняется. Требуется DB-миграция и замена вызова.
- **Bug 2:** Ветки `dbUser == nil`, 404 от Remnawave и ранее-продлённая подписка в `processModExtendID` возвращают `ModeratorMenuKeyboard`, но оставляют `StateWaitModExtendID`. Следующее сообщение модератора снова попадёт в обработчик. Исправление: добавить `b.userStates.Delete` перед возвратом в каждой терминальной ветке.

**Tech Stack:** Go, SQLite

---

### Task 1: Запретить переиспользование инвайта после автокика через поле `kicked_at`

**Суть:** сейчас `ResetInviteUsageByTelegramID` обнуляет `used_by`, делая код снова валидным. Нужно вместо этого проставлять `kicked_at = NOW()` — инвайт остаётся «использованным» (used_by не NULL), но ProcessInviteCode отклоняет его.

**Files:**
- Modify: `internal/database/db.go` (миграция)
- Modify: `internal/database/invites.go` (новая функция + проверка в ClaimInvite)
- Modify: `internal/bot/scheduler.go` (замена вызова)

---

**Step 1: Написать падающий тест**

В файле `internal/database/invites_ext_test.go` добавить тест:

```go
func TestMarkInviteKicked_PreventsReuse(t *testing.T) {
    db := setupTestDB(t)

    inv, err := db.CreateInvite(1)
    require.NoError(t, err)
    require.NoError(t, db.ClaimInvite(inv.Code, 2))

    // После автокика помечаем инвайт как кикнутый
    require.NoError(t, db.MarkInviteKickedByTelegramID(2))

    // Инвайт должен существовать с used_by != NULL (история сохранена)
    found, err := db.GetInviteByCode(inv.Code)
    require.NoError(t, err)
    require.NotNil(t, found)
    assert.NotNil(t, found.UsedBy, "used_by должен остаться")

    // ClaimInvite должен отклонить кикнутый инвайт
    err = db.ClaimInvite(inv.Code, 3)
    assert.Error(t, err, "повторное использование кикнутого инвайта должно вернуть ошибку")
}
```

**Step 2: Запустить тест — убедиться что падает**

```bash
GOCACHE=/tmp/go-build go test ./internal/database/ -run TestMarkInviteKicked_PreventsReuse -v
```

Ожидание: `FAIL — undefined: MarkInviteKickedByTelegramID`

---

**Step 3: Добавить миграцию в `db.go`**

В срез `alterMigrations` добавить:

```go
`ALTER TABLE invites ADD COLUMN kicked_at TIMESTAMP`,
```

---

**Step 4: Добавить функцию `MarkInviteKickedByTelegramID` в `invites.go`**

```go
// MarkInviteKickedByTelegramID проставляет kicked_at для инвайта пользователя.
// Инвайт остаётся «использованным» (used_by не обнуляется), но недоступен для повторного использования.
func (db *DB) MarkInviteKickedByTelegramID(telegramID int64) error {
    _, err := db.conn.Exec(
        `UPDATE invites SET kicked_at = CURRENT_TIMESTAMP WHERE used_by = ?`,
        telegramID,
    )
    if err != nil {
        return fmt.Errorf("failed to mark invite as kicked: %w", err)
    }
    return nil
}
```

---

**Step 5: Обновить структуру `Invite` в `invites.go`**

Добавить поле:

```go
KickedAt  *time.Time
```

---

**Step 6: Обновить `ClaimInvite` — отклонять кикнутые инвайты**

Найти функцию `ClaimInvite` в `invites.go`. После проверки `used_by IS NOT NULL` добавить проверку `kicked_at IS NOT NULL`:

```go
// Текущая проверка (уже есть):
if inv.UsedBy != nil {
    return fmt.Errorf("invite already used")
}

// Добавить после:
if inv.KickedAt != nil {
    return fmt.Errorf("invite already used")
}
```

Также обновить SQL в функции, которая читает инвайт, чтобы читалось поле `kicked_at`.

Найти все SELECT из таблицы `invites` в файле и добавить `kicked_at` в список колонок. Функции: `GetInviteByCode`, `GetInviteByUsedBy`, `getInviteRow` (если есть общий хелпер).

---

**Step 7: Запустить тест — убедиться что проходит**

```bash
GOCACHE=/tmp/go-build go test ./internal/database/ -run TestMarkInviteKicked_PreventsReuse -v
```

Ожидание: `PASS`

---

**Step 8: Заменить вызов в `scheduler.go`**

В `handleAutoKick` заменить:

```go
// Было:
if err := b.db.ResetInviteUsageByTelegramID(telegramID); err != nil {
    slog.Warn("Scheduler failed to reset invite usage during auto-kick", ...)
}

// Стало:
if err := b.db.MarkInviteKickedByTelegramID(telegramID); err != nil {
    slog.Warn("Scheduler failed to mark invite as kicked during auto-kick", ...)
}
```

---

**Step 9: Проверить компиляцию и все тесты**

```bash
GOCACHE=/tmp/go-build go build ./...
GOCACHE=/tmp/go-build go test ./...
```

Ожидание: все зелёные.

---

**Step 10: Коммит**

```bash
git add internal/database/db.go internal/database/invites.go internal/bot/scheduler.go internal/database/invites_ext_test.go
git commit -m "fix: автокик помечает инвайт как kicked_at вместо сброса used_by"
```

---

### Task 2: Очистить состояние диалога в терминальных ветках `processModExtendID`

**Суть:** ветки, которые возвращают пользователя в главное меню (`ModeratorMenuKeyboard`), оставляют `StateWaitModExtendID`. Следующий текст попадает обратно в обработчик.

**Files:**
- Modify: `internal/bot/moderator.go`
- Modify: `internal/bot/moderator_test.go`

---

**Step 1: Написать падающий тест**

В `moderator_test.go` добавить:

```go
func TestProcessModExtendID_ClearsStateOnTerminalErrors(t *testing.T) {
    b, db, _, modID := setupModeratorTestBot(t)

    t.Run("dbUser == nil очищает состояние", func(t *testing.T) {
        // Создаём инвайт с используемым пользователем, которого нет в users
        inv, err := db.CreateInviteWithExpiry(modID, intPtrBot(30))
        require.NoError(t, err)
        require.NoError(t, db.ClaimInvite(inv.Code, 9001))
        // Не создаём пользователя 9001 в users — он будет nil

        b.userStates.Set(modID, StateWaitModExtendID)

        ctx := &MockContext{
            sender:  &tele.User{ID: modID},
            message: &tele.Message{Text: "9001"},
        }
        err = b.processModExtendID(ctx, "9001")
        require.NoError(t, err)

        assert.Empty(t, b.userStates.Get(modID), "состояние должно быть очищено когда пользователь удалён")
    })

    t.Run("ранее продлённая подписка очищает состояние", func(t *testing.T) {
        // Создаём пользователя с подпиской далеко в будущем (>30 дней)
        _, err := db.CreateUser(9002, "future", "Future", "uuid-9002")
        require.NoError(t, err)
        inv, err := db.CreateInviteWithExpiry(modID, intPtrBot(30))
        require.NoError(t, err)
        require.NoError(t, db.ClaimInvite(inv.Code, 9002))

        // Настраиваем Remnawave — подписка в далёком будущем (>30 дней)
        client := remnawave.NewClient("https://panel.example.com", "test-token", "")
        client.SetHTTPClient(&http.Client{
            Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
                if strings.Contains(r.URL.Path, "uuid-9002") {
                    payload, _ := json.Marshal(map[string]any{
                        "response": map[string]any{
                            "uuid":      "uuid-9002",
                            "username":  "future",
                            "status":    remnawave.StatusActive,
                            "expireAt":  time.Now().UTC().AddDate(0, 0, 60).Format(time.RFC3339),
                            "createdAt": time.Now().UTC().Format(time.RFC3339),
                        },
                    })
                    return &http.Response{
                        StatusCode: http.StatusOK,
                        Body:       io.NopCloser(strings.NewReader(string(payload))),
                        Header:     make(http.Header),
                    }, nil
                }
                return nil, fmt.Errorf("unexpected: %s", r.URL.Path)
            }),
        })
        b.remnawave = client

        b.userStates.Set(modID, StateWaitModExtendID)

        ctx := &MockContext{
            sender:  &tele.User{ID: modID},
            message: &tele.Message{Text: "9002"},
        }
        err = b.processModExtendID(ctx, "9002")
        require.NoError(t, err)

        assert.Empty(t, b.userStates.Get(modID), "состояние должно быть очищено когда подписка ещё не может быть продлена")
    })
}
```

**Step 2: Запустить тест — убедиться что падает**

```bash
GOCACHE=/tmp/go-build go test ./internal/bot/ -run TestProcessModExtendID_ClearsStateOnTerminalErrors -v
```

Ожидание: `FAIL — state not cleared`

---

**Step 3: Исправить `processModExtendID` в `moderator.go`**

Добавить `b.userStates.Delete(moderatorID)` перед каждым `return` в терминальных ветках, которые возвращают `ModeratorMenuKeyboard`.

Три места:

```go
// 1. Ошибка БД при проверке владения
return c.Send("Ошибка проверки подписчика", &tele.SendOptions{ReplyMarkup: ModeratorMenuKeyboard()})
// → добавить перед return:
b.userStates.Delete(moderatorID)

// 2. dbUser == nil
return c.Send("❌ Пользователь уже удалён из системы.", &tele.SendOptions{ReplyMarkup: ModeratorMenuKeyboard()})
// → добавить перед return:
b.userStates.Delete(moderatorID)

// 3. 404 от Remnawave
return c.Send("❌ Пользователь уже удалён из системы.", &tele.SendOptions{ReplyMarkup: ModeratorMenuKeyboard()})
// → добавить перед return:
b.userStates.Delete(moderatorID)

// 4. CalculateExtendedExpireAt вернул ошибку (слишком рано)
return c.Send(err.Error(), &tele.SendOptions{ReplyMarkup: ModeratorMenuKeyboard()})
// → добавить перед return:
b.userStates.Delete(moderatorID)
```

> Ветки с `CancelKeyboard()` оставить без изменений — они продолжают диалог.

---

**Step 4: Запустить тест — убедиться что проходит**

```bash
GOCACHE=/tmp/go-build go test ./internal/bot/ -run TestProcessModExtendID_ClearsStateOnTerminalErrors -v
```

Ожидание: `PASS`

---

**Step 5: Запустить все тесты**

```bash
make fmt
make tests
```

Ожидание: все зелёные, fmt без изменений.

---

**Step 6: Коммит**

```bash
git add internal/bot/moderator.go internal/bot/moderator_test.go
git commit -m "fix: очищать состояние диалога продления в терминальных ветках processModExtendID"
```

---

### Task 3: Обновить progress-файл

**Files:**
- Modify: `docs/progress/2026-03-04-moderator-monetization-progress.md`

**Step 1: Добавить раздел в progress-файл**

Дополнить существующий раздел «Post-review фиксы» записями о Task 1 и Task 2 из этого плана.

**Step 2: Коммит**

```bash
git add docs/progress/2026-03-04-moderator-monetization-progress.md
git commit -m "docs: обновить прогресс-файл — фикс автокика и диалога продления"
```
