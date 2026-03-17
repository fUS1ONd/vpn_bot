# User Info Button Implementation Plan

> **For Claude:** REQUIRED SUB-SKILL: Use superpowers:executing-plans to implement this plan task-by-task.

**Goal:** Добавить в пользовательское меню новую кнопку `Информация`, которая отправляет сообщение `💡 Помощь и контакты` с контактом `@fus1ond` и двумя HTML-ссылками на политику конфиденциальности и пользовательское соглашение.

**Architecture:** Изменение локализовано в пользовательском UX-слое `internal/bot`: новая текстовая константа кнопки, новое HTML-сообщение, новая ветка в роутере текстовых сообщений и отдельный обработчик отправки ответа. Кнопка должна появиться и у обычного пользователя, и у модератора в пользовательском меню, потому что оба сценария используют общую схему `userKeyboard(...)`.

**Tech Stack:** Go, Telebot v3, testify, Makefile (`make fmt`, `make tests`)

---

### Task 1: Зафиксировать новое поведение тестами

**Files:**
- Modify: `internal/bot/keyboards_test.go`
- Modify: `internal/bot/handlers_test.go`
- Modify: `internal/bot/messages_test.go`
- Reference: `internal/bot/keyboards.go`
- Reference: `internal/bot/handlers.go`
- Reference: `internal/bot/messages.go`

**Step 1: Write the failing test**

Добавить в `internal/bot/keyboards_test.go` тест на обычную пользовательскую клавиатуру:

```go
func TestUserMenuKeyboardContainsInfoButton(t *testing.T) {
	keyboard := UserMenuKeyboard()

	var buttons []string
	for _, row := range keyboard.ReplyKeyboard {
		for _, btn := range row {
			buttons = append(buttons, btn.Text)
		}
	}

	assert.Contains(t, buttons, BtnInfo)
	assert.Contains(t, buttons, BtnDonate)
}
```

Добавить в `internal/bot/keyboards_test.go` тест на пользовательскую клавиатуру модератора:

```go
func TestUserMenuKeyboardModeratorContainsInfoButton(t *testing.T) {
	keyboard := UserMenuKeyboardModerator()

	var buttons []string
	for _, row := range keyboard.ReplyKeyboard {
		for _, btn := range row {
			buttons = append(buttons, btn.Text)
		}
	}

	assert.Contains(t, buttons, BtnInfo)
	assert.Contains(t, buttons, BtnModInvites)
}
```

Добавить в `internal/bot/messages_test.go` тест на текст сообщения:

```go
func TestMsgInfoContainsExpectedLinks(t *testing.T) {
	assert.Contains(t, MsgInfo, "💡 Помощь и контакты")
	assert.Contains(t, MsgInfo, "@fus1ond")
	assert.Contains(t, MsgInfo, "https://telegra.ph/Politika-konfidencialnosti-08-15-17")
	assert.Contains(t, MsgInfo, "https://telegra.ph/Polzovatelskoe-soglashenie-08-15-10")
	assert.Contains(t, MsgInfo, `>читать</a>`)
}
```

Добавить в `internal/bot/handlers_test.go` тест на обработчик:

```go
func TestHandleInfoSendsHelpMessage(t *testing.T) {
	b, _ := setupTestBot(t)
	ctx := &MockContext{
		sender:  &tele.User{ID: 777, Username: "reader"},
		message: &tele.Message{},
	}

	err := b.handleInfo(ctx)
	require.NoError(t, err)

	msg, ok := ctx.sentMsg.(string)
	require.True(t, ok)
	assert.Equal(t, MsgInfo, msg)
}
```

**Step 2: Run test to verify it fails**

Run:

```bash
go test ./internal/bot -run 'TestUserMenuKeyboardContainsInfoButton|TestUserMenuKeyboardModeratorContainsInfoButton|TestMsgInfoContainsExpectedLinks|TestHandleInfoSendsHelpMessage' -count=1
```

Expected: FAIL, потому что `BtnInfo`, `MsgInfo` и `handleInfo` ещё не реализованы.

**Step 3: Проверить, что тесты отражают целевое поведение**

- Кнопка `Информация` есть в обоих пользовательских меню
- Текст сообщения содержит точный заголовок и контакт
- Обе ссылки заданы как HTML-ссылки на слове `читать`
- Обработчик отправляет именно `MsgInfo`, без лишней бизнес-логики

**Step 4: Run test to verify it still fails for the right reason**

Run:

```bash
go test ./internal/bot -run 'TestUserMenuKeyboardContainsInfoButton|TestUserMenuKeyboardModeratorContainsInfoButton|TestMsgInfoContainsExpectedLinks|TestHandleInfoSendsHelpMessage' -count=1
```

Expected: FAIL только из-за отсутствующей реализации, а не из-за синтаксических ошибок в тестах.

**Step 5: Подготовить имя будущего коммита**

```bash
feat: добавить кнопку информации для пользователей
```

Примечание: на этом этапе коммит не выполнять.

### Task 2: Добавить кнопку, сообщение и обработчик

**Files:**
- Modify: `internal/bot/keyboards.go`
- Modify: `internal/bot/messages.go`
- Modify: `internal/bot/handlers.go`
- Reference: `internal/bot/keyboards_test.go`
- Reference: `internal/bot/handlers_test.go`
- Reference: `internal/bot/messages_test.go`

**Step 1: Write the minimal implementation**

В `internal/bot/keyboards.go`:

- добавить константу:

```go
BtnInfo = "ℹ️ Информация"
```

- обновить `UserMenuKeyboard()` так, чтобы нижний ряд содержал две кнопки:

```go
menu.Row(menu.Text(BtnDonate), menu.Text(BtnInfo))
```

- обновить `UserMenuKeyboardModerator()` аналогично, сохранив отдельный ряд с `BtnModInvites`:

```go
menu.Reply(
	menu.Row(menu.Text(BtnStatus), menu.Text(BtnConnect)),
	menu.Row(menu.Text(BtnServers), menu.Text(BtnInstructions)),
	menu.Row(menu.Text(BtnModInvites)),
	menu.Row(menu.Text(BtnDonate), menu.Text(BtnInfo)),
)
```

В `internal/bot/messages.go` добавить новую константу:

```go
MsgInfo = `<b>💡 Помощь и контакты</b>

Если есть вопросы — пишите @fus1ond

🔒 Политика конфиденциальности: <a href="https://telegra.ph/Politika-konfidencialnosti-08-15-17">читать</a>
📜 Пользовательское соглашение: <a href="https://telegra.ph/Polzovatelskoe-soglashenie-08-15-10">читать</a>`
```

В `internal/bot/handlers.go`:

- добавить в пользовательский роутер новую ветку:

```go
case BtnInfo:
	return b.handleInfo(c)
```

- добавить обработчик:

```go
func (b *Bot) handleInfo(c tele.Context) error {
	return c.Send(MsgInfo, &tele.SendOptions{
		ParseMode:   tele.ModeHTML,
		ReplyMarkup: b.userKeyboard(c.Sender().ID),
	})
}
```

**Step 2: Учесть существующую структуру меню**

- не менять поведение `BtnModInvites` и переходов в подменю модератора
- не трогать админские клавиатуры
- оставить `handleUserMode` без отдельной логики, потому что новая кнопка уже попадёт туда через `b.userKeyboard(...)`

**Step 3: Run targeted tests**

Run:

```bash
go test ./internal/bot -run 'TestUserMenuKeyboardContainsInfoButton|TestUserMenuKeyboardModeratorContainsInfoButton|TestMsgInfoContainsExpectedLinks|TestHandleInfoSendsHelpMessage' -count=1
```

Expected: PASS

**Step 4: Проверить, что новая кнопка подключена везде, где нужно**

Run:

```bash
rg -n "BtnInfo|MsgInfo|handleInfo" internal/bot
```

Expected: matches в `keyboards.go`, `messages.go`, `handlers.go` и новых тестах; отсутствуют пропущенные места пользовательского меню.

**Step 5: Подготовить имя будущего коммита**

```bash
feat: добавить кнопку информации для пользователей
```

Примечание: на этом этапе коммит не выполнять.

### Task 3: Синхронизировать документацию и выполнить финальную проверку

**Files:**
- Modify: `README.md`
- Reference: `internal/bot/keyboards.go`
- Reference: `internal/bot/messages.go`

**Step 1: Обновить README**

В `README.md` расширить описание пользовательских кнопок новой строкой:

```md
| `ℹ️ Информация` | Помощь, контакт для вопросов и ссылки на документы сервиса |
```

Если описание меню перечислено в текстовом виде рядом с таблицей, синхронизировать и его тоже.

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
git diff -- internal/bot/keyboards.go internal/bot/messages.go internal/bot/handlers.go internal/bot/keyboards_test.go internal/bot/handlers_test.go internal/bot/messages_test.go README.md
```

Expected: diff показывает только добавление новой пользовательской кнопки, сообщения, тестов и синхронизацию README.

**Step 5: Подготовить имя будущего коммита**

```bash
docs: синхронизировать описание кнопки информации
```

Примечание: на этом этапе коммит не выполнять.
