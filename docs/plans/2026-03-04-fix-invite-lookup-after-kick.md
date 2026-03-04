# Fix: корректный поиск инвайта после автокика

> **For Claude:** REQUIRED SUB-SKILL: Use superpowers:executing-plans to implement this plan task-by-task.

**Goal:** Исправить два бага в запросах к таблице `invites`, которые приводят к некорректным бизнес-решениям после того, как пользователь был кикнут и вернулся по новому инвайту.

**Architecture:**
- **Bug 1 (P2):** `GetInviteByUsedBy` использует `LIMIT 1` без `ORDER BY` и без фильтра `kicked_at IS NULL`. Если у пользователя два ряда (старый кикнутый + новый), запрос вернёт произвольный. Scheduler и валидация назначения модератора читают этот результат — могут ошибиться с куратором и типом подписки. Фикс: добавить `AND kicked_at IS NULL ORDER BY used_at DESC`.
- **Bug 2 (P1):** `IsSubscriberOfModerator` не фильтрует кикнутые инвайты. Старый модератор A приглашал пользователя → автокик → пользователь вернулся по инвайту модератора B. `EXISTS` находит старый инвайт от A и разрешает A продлить подписку чужого подписчика. Фикс: добавить `AND kicked_at IS NULL`.

**Tech Stack:** Go, SQLite

---

### Task 1: Исправить `GetInviteByUsedBy` — фильтровать кикнутые инвайты

**Files:**
- Modify: `internal/database/invites.go` (функция `GetInviteByUsedBy`)
- Modify: `internal/database/invites_ext_test.go` (новый тест)

---

**Step 1: Написать падающий тест**

В `internal/database/invites_ext_test.go` добавить тест после `TestGetInviteByUsedBy`:

```go
func TestGetInviteByUsedBy_AfterKickAndRejoin(t *testing.T) {
	db := setupTestDBInvites(t)

	// Модератор A приглашает пользователя 555
	days := 30
	inv1, err := db.CreateInviteWithExpiry(100, &days)
	require.NoError(t, err)
	require.NoError(t, db.ClaimInvite(inv1.Code, 555))

	// Автокик: помечаем старый инвайт кикнутым
	require.NoError(t, db.MarkInviteKickedByTelegramID(555))

	// Модератор B приглашает того же пользователя 555 снова
	inv2, err := db.CreateInviteWithExpiry(200, &days)
	require.NoError(t, err)
	require.NoError(t, db.ClaimInvite(inv2.Code, 555))

	// GetInviteByUsedBy должен вернуть НОВЫЙ инвайт от модератора B, не старый от A
	got, err := db.GetInviteByUsedBy(555)
	require.NoError(t, err)
	require.NotNil(t, got)
	assert.Equal(t, inv2.Code, got.Code, "должен вернуть актуальный (не кикнутый) инвайт")
	assert.Equal(t, int64(200), got.CreatedBy, "куратор должен быть модератор B (200), не A (100)")
}
```

**Step 2: Запустить тест — убедиться что падает**

```bash
GOCACHE=/tmp/go-build go test ./internal/database/ -run TestGetInviteByUsedBy_AfterKickAndRejoin -v
```

Ожидание: `FAIL` — тест вернёт инвайт от модератора A (100) вместо B (200).

---

**Step 3: Исправить запрос в `GetInviteByUsedBy`**

В `internal/database/invites.go` найти функцию `GetInviteByUsedBy` (строка ~438) и заменить SQL:

```go
// Было:
`SELECT code, created_by, used_by, used_at, expire_days, kicked_at, created_at
 FROM invites
 WHERE used_by = ?
 LIMIT 1`,

// Стало:
`SELECT code, created_by, used_by, used_at, expire_days, kicked_at, created_at
 FROM invites
 WHERE used_by = ? AND kicked_at IS NULL
 ORDER BY used_at DESC
 LIMIT 1`,
```

---

**Step 4: Запустить тест — убедиться что проходит**

```bash
GOCACHE=/tmp/go-build go test ./internal/database/ -run TestGetInviteByUsedBy_AfterKickAndRejoin -v
```

Ожидание: `PASS`

---

**Step 5: Запустить все тесты БД**

```bash
GOCACHE=/tmp/go-build go test ./internal/database/ -v
```

Ожидание: все зелёные.

---

### Task 2: Исправить `IsSubscriberOfModerator` — утечка прав на продление

**Files:**
- Modify: `internal/database/invites.go` (функция `IsSubscriberOfModerator`)
- Modify: `internal/database/invites_ext_test.go` (новый тест)

---

**Step 1: Написать падающий тест**

В `internal/database/invites_ext_test.go` добавить тест:

```go
func TestIsSubscriberOfModerator_AfterKickAndRejoin(t *testing.T) {
	db := setupTestDBInvites(t)

	// Модератор A (100) приглашает пользователя 555
	days := 30
	inv1, err := db.CreateInviteWithExpiry(100, &days)
	require.NoError(t, err)
	require.NoError(t, db.ClaimInvite(inv1.Code, 555))

	// Автокик
	require.NoError(t, db.MarkInviteKickedByTelegramID(555))

	// Модератор B (200) приглашает того же пользователя 555 снова
	inv2, err := db.CreateInviteWithExpiry(200, &days)
	require.NoError(t, err)
	require.NoError(t, db.ClaimInvite(inv2.Code, 555))

	// Модератор A НЕ должен считаться куратором пользователя 555
	isSubOfA, err := db.IsSubscriberOfModerator(100, 555)
	require.NoError(t, err)
	assert.False(t, isSubOfA, "старый модератор A не должен иметь прав на продление после перехода подписчика к B")

	// Модератор B ДОЛЖЕН считаться куратором
	isSubOfB, err := db.IsSubscriberOfModerator(200, 555)
	require.NoError(t, err)
	assert.True(t, isSubOfB, "новый модератор B должен иметь права на продление")
}
```

**Step 2: Запустить тест — убедиться что падает**

```bash
GOCACHE=/tmp/go-build go test ./internal/database/ -run TestIsSubscriberOfModerator_AfterKickAndRejoin -v
```

Ожидание: `FAIL` — `isSubOfA` вернёт `true` вместо `false`.

---

**Step 3: Исправить запрос в `IsSubscriberOfModerator`**

В `internal/database/invites.go` найти функцию `IsSubscriberOfModerator` (строка ~526) и заменить SQL:

```go
// Было:
`SELECT EXISTS(
    SELECT 1 FROM invites
    WHERE created_by = ? AND used_by = ?
)`,

// Стало:
`SELECT EXISTS(
    SELECT 1 FROM invites
    WHERE created_by = ? AND used_by = ? AND kicked_at IS NULL
)`,
```

---

**Step 4: Запустить тест — убедиться что проходит**

```bash
GOCACHE=/tmp/go-build go test ./internal/database/ -run TestIsSubscriberOfModerator_AfterKickAndRejoin -v
```

Ожидание: `PASS`

---

**Step 5: Финальная проверка**

```bash
make fmt
make tests
```

Ожидание: `fmt` без изменений, все тесты зелёные.

---

**Step 6: Коммит**

```bash
git add internal/database/invites.go internal/database/invites_ext_test.go
git commit -m "fix: фильтровать кикнутые инвайты в GetInviteByUsedBy и IsSubscriberOfModerator"
```

---

### Task 3: Обновить progress-файл

**Files:**
- Modify: `docs/progress/2026-03-04-moderator-monetization-progress.md`

**Step 1: Добавить раздел в конец файла**

Дополнить раздел «Post-review фиксы» записью о двух исправленных SQL-запросах.

**Step 2: Коммит**

```bash
git add docs/progress/2026-03-04-moderator-monetization-progress.md
git commit -m "docs: зафиксировать фикс SQL-запросов инвайтов после кика"
```
