# Unify PC Instructions Implementation Plan

> **For Claude:** REQUIRED SUB-SKILL: Use superpowers:executing-plans to implement this plan task-by-task.

**Goal:** Объединить desktop-инструкции в одну кнопку `ПК` и один текст сообщения `Настройка на ПК`, сохранив iOS и Android без изменений.

**Architecture:** Изменение локализовано в UX-слое бота: константы текста, кнопки клавиатуры и обработчики пользовательских сообщений в `internal/bot`. Для снижения риска регрессии сначала добавляются точечные тесты на клавиатуру и unified desktop-инструкцию, затем упрощается код за счёт удаления отдельной ветки для `Windows/Linux` и `macOS`.

**Tech Stack:** Go, Telebot v3, testify, Makefile (`make fmt`, `make tests`)

---

### Task 1: Зафиксировать новое поведение тестами

**Files:**
- Modify: `internal/bot/keyboards_test.go`
- Modify: `internal/bot/handlers_test.go`
- Reference: `internal/bot/keyboards.go`
- Reference: `internal/bot/handlers.go`
- Reference: `internal/bot/messages.go`

**Step 1: Write the failing test**

Добавить в `internal/bot/keyboards_test.go` тест вида:

```go
func TestInstructionsKeyboardContainsUnifiedDesktopButton(t *testing.T) {
	keyboard := InstructionsKeyboard()

	var buttons []string
	for _, row := range keyboard.ReplyKeyboard {
		for _, btn := range row {
			buttons = append(buttons, btn.Text)
		}
	}

	assert.Contains(t, buttons, BtnInstIOS)
	assert.Contains(t, buttons, BtnInstAndroid)
	assert.Contains(t, buttons, BtnInstDesktop)
	assert.NotContains(t, buttons, "💻 Windows/Linux")
	assert.NotContains(t, buttons, "🍏 macOS")
}
```

Добавить в `internal/bot/handlers_test.go` тест вида:

```go
func TestHandleInstructionDesktopUsesUnifiedPCMessage(t *testing.T) {
	b, _ := setupTestBot(t)
	ctx := &MockContext{
		sender:  &tele.User{ID: 777, Username: "desktop"},
		message: &tele.Message{},
	}

	err := b.handleInstructionDesktop(ctx)
	require.NoError(t, err)

	msg, ok := ctx.sentMsg.(string)
	require.True(t, ok)
	assert.Contains(t, msg, "<b>Настройка на ПК</b>")
	assert.Contains(t, msg, "https://www.happ.su/main/ru")
	assert.Contains(t, msg, "\"URL подписки\"")
	assert.Contains(t, msg, "\"TUN\"")
	assert.Contains(t, msg, "Сначала активируйте подписку")
}
```

**Step 2: Run test to verify it fails**

Run:

```bash
go test ./internal/bot -run 'TestInstructionsKeyboardContainsUnifiedDesktopButton|TestHandleInstructionDesktopUsesUnifiedPCMessage' -count=1
```

Expected: FAIL, потому что в коде ещё нет `BtnInstDesktop`, `handleInstructionDesktop` и старые desktop-кнопки/сообщения ещё не удалены.

**Step 3: Убедиться, что тесты проверяют именно целевое поведение**

- Кнопка инструкций для десктопа одна и называется `ПК`
- Заголовок unified сообщения: `Настройка на ПК`
- Сообщение использует `HAPP`
- Старые desktop-кнопки не считаются валидными

**Step 4: Run test to verify it still fails for the right reason**

Run:

```bash
go test ./internal/bot -run 'TestInstructionsKeyboardContainsUnifiedDesktopButton|TestHandleInstructionDesktopUsesUnifiedPCMessage' -count=1
```

Expected: FAIL только из-за отсутствующей новой реализации, а не из-за синтаксических ошибок в тестах.

**Step 5: Подготовить имя будущего коммита**

```bash
feat: объединить desktop-инструкцию в кнопку ПК
```

Примечание: на этом этапе коммит не выполнять.

### Task 2: Объединить desktop-кнопки, текст и обработчики

**Files:**
- Modify: `internal/bot/keyboards.go`
- Modify: `internal/bot/handlers.go`
- Modify: `internal/bot/messages.go`
- Reference: `internal/bot/keyboards_test.go`
- Reference: `internal/bot/handlers_test.go`

**Step 1: Write the minimal implementation**

В `internal/bot/keyboards.go`:

- заменить `BtnInstWindows` и `BtnInstMac` на одну константу `BtnInstDesktop = "ПК"`
- обновить `InstructionsKeyboard()`, чтобы ряды были:

```go
menu.Reply(
	menu.Row(menu.Text(BtnInstIOS), menu.Text(BtnInstAndroid)),
	menu.Row(menu.Text(BtnInstDesktop)),
	menu.Row(menu.Text(BtnBack)),
)
```

В `internal/bot/messages.go`:

- заменить `MsgInstructionWindows` и `MsgInstructionMac` на одну константу `MsgInstructionDesktop`
- задать такой текст:

```go
MsgInstructionDesktop = `<b>Настройка на ПК</b>

1. Скачайте <b>HAPP</b>:
   https://www.happ.su/main/ru

2. Установите и откройте клиент

3. Нажмите "Добавить"(визуально плюсик)

4. Вставьте в поле "URL подписки" вашу ссылку (внизу данного поста)

5. Переключите тумблер на "TUN" и подключитесь

<b>Ваша ссылка подписки:</b>
<code>%s</code>`
```

В `internal/bot/handlers.go`:

- заменить ветки `case BtnInstWindows` и `case BtnInstMac` на один `case BtnInstDesktop`
- удалить `handleInstructionWindows` и `handleInstructionMac`
- добавить `handleInstructionDesktop`, который использует `MsgInstructionDesktop`

```go
func (b *Bot) handleInstructionDesktop(c tele.Context) error {
	subLink := b.getSubLinkForUser(c.Sender().ID)
	return c.Send(fmt.Sprintf(MsgInstructionDesktop, subLink), &tele.SendOptions{
		ParseMode:   tele.ModeHTML,
		ReplyMarkup: InstructionsKeyboard(),
	})
}
```

**Step 2: Учесть текущее состояние рабочей директории**

- перед редактированием внимательно перечитать `internal/bot/messages.go`
- не перетирать уже существующий незакоммиченный текст в этом файле вслепую
- вносить только целевые изменения через `apply_patch`

**Step 3: Run targeted tests**

Run:

```bash
go test ./internal/bot -run 'TestInstructionsKeyboardContainsUnifiedDesktopButton|TestHandleInstructionDesktopUsesUnifiedPCMessage' -count=1
```

Expected: PASS

**Step 4: Проверить, что старые desktop-символы больше не используются**

Run:

```bash
rg -n "BtnInstWindows|BtnInstMac|MsgInstructionWindows|MsgInstructionMac|handleInstructionWindows|handleInstructionMac" internal
```

Expected: no matches

**Step 5: Подготовить имя будущего коммита**

```bash
feat: объединить desktop-инструкцию в кнопку ПК
```

Примечание: на этом этапе коммит не выполнять.

### Task 3: Синхронизировать документацию и финально проверить проект

**Files:**
- Modify: `README.md`
- Reference: `internal/bot/keyboards.go`
- Reference: `internal/bot/messages.go`

**Step 1: Обновить описание в README**

В `README.md` заменить описание пользовательской кнопки `📚 Инструкции` на актуальное, например:

```md
| `📚 Инструкции` | Инструкции по настройке клиентов: iOS, Android, ПК |
```

**Step 2: Run formatting**

Run:

```bash
make fmt
```

Expected: exit code 0

**Step 3: Run test suite**

Run:

```bash
make tests
```

Expected: exit code 0

**Step 4: Проверить итоговый diff**

Run:

```bash
git diff -- internal/bot/messages.go internal/bot/keyboards.go internal/bot/handlers.go internal/bot/keyboards_test.go internal/bot/handlers_test.go README.md
```

Expected: diff показывает только целевые изменения по unified PC UX.

**Step 5: Подготовить имя будущего коммита**

```bash
docs: синхронизировать инструкцию для кнопки ПК
```

Примечание: на этом этапе коммит не выполнять.
