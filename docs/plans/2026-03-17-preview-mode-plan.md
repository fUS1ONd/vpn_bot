# Preview Mode Implementation Plan

> **For Claude:** REQUIRED SUB-SKILL: Use superpowers:executing-plans to implement this plan task-by-task.

**Goal:** Добавить глобальный runtime preview-режим, который админ включает из админки, чтобы незарегистрированные пользователи могли просматривать пользовательский интерфейс без создания записей в БД и Remnawave до активации инвайта.

**Architecture:** Preview хранится только в памяти процесса как флаг внутри `internal/bot.Bot` и по умолчанию выключен при старте. Незарегистрированный пользователь в preview получает отдельный welcome-flow, гостевую клавиатуру с кнопкой активации кода и демо-ответы на чувствительные действия; зарегистрированные пользователи и обычный flow инвайтов продолжают работать как раньше.

**Tech Stack:** Go 1.25, Telebot v3, testify, SQLite, Makefile (`make fmt`, `make tests`)

---

### Task 1: Зафиксировать preview UX тестами

**Files:**
- Modify: `internal/bot/keyboards_test.go`
- Modify: `internal/bot/handlers_test.go`
- Modify: `internal/bot/messages_test.go`
- Modify: `internal/bot/admin_test.go`
- Reference: `internal/bot/keyboards.go`
- Reference: `internal/bot/handlers.go`
- Reference: `internal/bot/messages.go`
- Reference: `internal/bot/admin.go`

**Step 1: Write the failing keyboard tests**

Добавить тесты на:
- наличие кнопки активации у preview-клавиатуры;
- отсутствие кнопки активации у обычной пользовательской клавиатуры;
- наличие кнопки runtime preview-переключателя в админском меню.

**Step 2: Write the failing handler tests**

Добавить тесты на:
- `/start` для нового пользователя при выключенном preview возвращает `MsgWelcomeInvite`;
- `/start` для нового пользователя при включённом preview возвращает preview welcome и клавиатуру preview;
- `/start <code>` для нового пользователя при включённом preview сохраняет обычную активацию по deep link;
- `Мой статус` для preview-пользователя возвращает демо-сообщение без запроса к Remnawave;
- `Подключить` для preview-пользователя возвращает демо-сообщение без subscription URL;
- `Инструкции` и платформенные инструкции для preview-пользователя подставляют демо-текст вместо ссылки;
- кнопка активации переводит незарегистрированного пользователя в `StateWaitInvite`.

**Step 3: Write the failing admin tests**

Добавить тесты на:
- переключение preview из админки меняет runtime-флаг;
- сообщение/клавиатура админки обновляются после переключения;
- не-админ не может переключить preview.

**Step 4: Run targeted tests to verify RED**

Run:

```bash
go test ./internal/bot -run 'TestUserMenuKeyboard|TestHandleStart|TestHandlePreview|TestHandleAdminPreview' -count=1
```

Expected: FAIL из-за отсутствующих кнопок, сообщений и preview-логики.

**Step 5: Commit checkpoint**

```bash
git add internal/bot/keyboards_test.go internal/bot/handlers_test.go internal/bot/messages_test.go internal/bot/admin_test.go
git commit -m "test: добавить тесты preview режима"
```

### Task 2: Реализовать runtime preview-переключатель и гостевой UX

**Files:**
- Modify: `internal/bot/handlers.go`
- Modify: `internal/bot/admin.go`
- Modify: `internal/bot/keyboards.go`
- Modify: `internal/bot/messages.go`
- Reference: `internal/config/config.go`

**Step 1: Add minimal runtime state**

В `Bot` добавить потокобезопасный in-memory флаг preview и методы чтения/переключения состояния. Значение по умолчанию должно быть `false` при создании `Bot`.

**Step 2: Add admin toggle surface**

Добавить новую кнопку переключения preview в админское меню и обработчик, который:
- работает только для админа;
- переключает `on/off`;
- возвращает понятный статус (`Preview: ON/OFF`);
- не использует `.env` и БД;
- переживает только до рестарта процесса.

**Step 3: Add preview-aware keyboards**

Развести пользовательские клавиатуры:
- обычная клавиатура для зарегистрированного пользователя;
- preview-клавиатура для незарегистрированного пользователя с кнопкой активации кода;
- модераторское/админское пользовательское меню не ломать.

**Step 4: Make `/start` preview-aware**

Обновить `handleStart`:
- забаненный пользователь по-прежнему блокируется;
- существующий пользователь по-прежнему получает обычное меню;
- новый пользователь при preview=`off` продолжает видеть `MsgWelcomeInvite`;
- новый пользователь при preview=`on` получает preview welcome и preview-клавиатуру;
- deep link `/start <code>` работает по старой логике даже при preview=`on`.

**Step 5: Run targeted tests to verify GREEN**

Run:

```bash
go test ./internal/bot -run 'TestHandleStart|TestHandleAdminPreview|TestUserMenuKeyboard' -count=1
```

Expected: PASS

**Step 6: Commit checkpoint**

```bash
git add internal/bot/handlers.go internal/bot/admin.go internal/bot/keyboards.go internal/bot/messages.go
git commit -m "feat: добавить переключаемый preview режим"
```

### Task 3: Добавить демо-ответы для preview-пользователя

**Files:**
- Modify: `internal/bot/handlers.go`
- Modify: `internal/bot/messages.go`
- Modify: `internal/bot/handlers_test.go`
- Modify: `internal/bot/messages_test.go`

**Step 1: Implement preview guards**

Для незарегистрированного пользователя при preview=`on`:
- `handleStatus` возвращает отдельное preview-сообщение;
- `handleConnect` возвращает демо-сообщение без subscription URL;
- `getSubLinkForUser` или инструкции используют демо-плейсхолдер вместо реальной ссылки;
- кнопка активации кода ставит `StateWaitInvite` и показывает запрос на ввод кода.

**Step 2: Keep safe sections available**

Убедиться, что `Информация`, `Поддержать`, `Инструкции` и возвраты по меню работают для preview-пользователя без создания пользователя в БД и без запросов в Remnawave там, где они не нужны.

**Step 3: Run focused tests**

Run:

```bash
go test ./internal/bot -run 'TestHandlePreview|TestHandleStatus|TestHandleConnect|TestHandleInstruction' -count=1
```

Expected: PASS

**Step 4: Commit checkpoint**

```bash
git add internal/bot/handlers.go internal/bot/messages.go internal/bot/handlers_test.go internal/bot/messages_test.go
git commit -m "feat: добавить демо ответы для preview пользователей"
```

### Task 4: Синхронизировать документацию и завершить проверку

**Files:**
- Modify: `README.md`
- Create: `docs/progress/2026-03-17-preview-mode-progress.md`
- Reference: `docs/plans/2026-03-17-preview-mode-plan.md`

**Step 1: Update README**

Обновить README:
- описать кнопку админа для переключения preview;
- описать поведение preview для незарегистрированного пользователя;
- явно отметить, что состояние preview живёт только до рестарта бота.

**Step 2: Run full verification**

Run:

```bash
make fmt
make tests
```

Expected: обе команды завершаются успешно.

**Step 3: Write progress note**

Создать `docs/progress/2026-03-17-preview-mode-progress.md` с:
- ссылкой на план;
- кратким перечнем изменений;
- списком коммитов;
- указанием фактически выполненной проверки.

**Step 4: Final commits**

```bash
git add README.md docs/progress/2026-03-17-preview-mode-progress.md
git commit -m "docs: зафиксировать прогресс по preview режиму"
```

**Step 5: Optional future commit title**

```bash
feat: доработать preview режим для гостевого доступа
```
