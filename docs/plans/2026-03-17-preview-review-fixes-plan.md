# Preview Review Fixes Implementation Plan

> **For Claude:** REQUIRED SUB-SKILL: Use superpowers:executing-plans to implement this plan task-by-task.

**Goal:** Исправить замечания code review по preview-режиму: вернуть жёсткую блокировку забаненных пользователей на раннем роутере и довести тестовое покрытие preview-инструкций до требований плана.

**Architecture:** Исправление должно остаться минимально инвазивным: ранняя проверка бана добавляется в общий роутер текстовых сообщений, чтобы ни одна preview-ветка не была достижима для забаненного пользователя. Тесты дополняются только недостающими сценариями preview для `BtnInstructions`, iOS и Android, без изменения пользовательского UX.

**Tech Stack:** Go 1.25, Telebot v3, testify, Makefile (`make fmt`, `make tests`)

---

### Task 1: Зафиксировать замечания ревью тестами

**Files:**
- Modify: `internal/bot/handlers_test.go`
- Reference: `internal/bot/handlers.go`
- Reference: `docs/plans/2026-03-17-preview-mode-plan.md`

**Step 1: Write the failing ban regression tests**

Добавить тесты, которые подтверждают:
- забаненный пользователь при включённом preview не может открыть `👤 Мой статус` и `🌐 Подключить` через `handleTextMessage`;
- забаненный пользователь не может перейти в `StateWaitInvite` через `🎟 Активировать код`.

**Step 2: Write the failing instruction coverage tests**

Добавить тесты, которые подтверждают:
- `BtnInstructions` роутится в `MsgInstructions` и клавиатуру инструкций для preview-гостя;
- iOS и Android инструкции для preview-гостя подставляют `PreviewSubscriptionPlaceholder`.

**Step 3: Run targeted tests to verify RED**

Run:

```bash
go test ./internal/bot -run 'TestHandleTextMessage_BannedUser|TestHandleTextMessage_InstructionsButtonRoutesToInstructionsMenu|TestHandleInstruction(IOS|Android)UsesPreviewPlaceholderForGuest' -count=1
```

Expected: FAIL, потому что ранняя блокировка бана и часть preview-тестов ещё отсутствуют.

**Step 4: Commit checkpoint**

```bash
git add internal/bot/handlers_test.go
git commit -m "test: добавить регрессии для preview после ревью"
```

### Task 2: Починить раннюю блокировку banned-пользователей

**Files:**
- Modify: `internal/bot/handlers.go`
- Reference: `internal/database/bans.go`

**Step 1: Add shared banned-user guard**

Добавить минимальный helper для проверки бана по `telegram_id` и использовать его в общем текстовом роутере до любых preview-веток.

**Step 2: Tighten preview guest detection**

Сделать так, чтобы `isPreviewGuest` не трактовал забаненного пользователя как гостя preview-режима.

**Step 3: Run focused regression tests**

Run:

```bash
go test ./internal/bot -run 'TestHandleTextMessage_BannedUser' -count=1
```

Expected: PASS

**Step 4: Commit checkpoint**

```bash
git add internal/bot/handlers.go internal/bot/handlers_test.go
git commit -m "fix: закрыть preview для забаненных пользователей"
```

### Task 3: Довести покрытие preview-инструкций и завершить работу

**Files:**
- Modify: `internal/bot/handlers_test.go`
- Create: `docs/progress/2026-03-17-preview-review-fixes-progress.md`
- Reference: `docs/plans/2026-03-17-preview-review-fixes-plan.md`

**Step 1: Verify instruction coverage**

Убедиться, что тестами покрыты:
- вход в меню инструкций через `BtnInstructions`;
- iOS, Android и Desktop preview-плейсхолдеры.

**Step 2: Run full verification**

Run:

```bash
make fmt
make tests
```

Expected: PASS

**Step 3: Write progress note**

Создать `docs/progress/2026-03-17-preview-review-fixes-progress.md` с:
- ссылкой на новый план;
- перечислением исправленных review-пунктов;
- списком коммитов;
- указанием выполненных проверок.

**Step 4: Final commit**

```bash
git add docs/progress/2026-03-17-preview-review-fixes-progress.md
git commit -m "docs: зафиксировать прогресс по правкам preview ревью"
```
