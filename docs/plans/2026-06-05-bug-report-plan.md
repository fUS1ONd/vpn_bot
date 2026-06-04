# Bug Report Feature Implementation Plan

> **For Claude:** REQUIRED SUB-SKILL: Use superpowers:executing-plans to implement this plan task-by-task.

**Goal:** Дать пользователю кнопку «🛠 Сообщить о проблеме», которая собирает сервер + категорию + комментарий и шлёт структурированный багрепорт администратору в личку.

**Architecture:** Новый файл `internal/bot/bug_report.go` с handlers и чистыми функциями. Выбор сервера и категории — через inline-callback (как `devices.go`), ввод комментария — через reply-состояние `StateWaitBugComment` (как payment-flow). Сессия и кулдаун хранятся in-memory в `Bot`. Доставка — переиспользование паттерна `notifyAdminNewUser`.

**Tech Stack:** Go, telebot.v3, testify. Команды: `make tests` (= `go test ./...`), `make fmt`.

Дизайн: `docs/plans/2026-06-05-bug-report-design.md`

---

### Task 1: Чистая функция формирования сообщения админу

**Files:**
- Create: `internal/bot/bug_report.go`
- Test: `internal/bot/bug_report_test.go`

**Step 1: Написать падающий тест**

```go
package bot

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestBuildBugReportMessage(t *testing.T) {
	r := bugReport{
		telegramID:   12345,
		username:     "ivan",
		firstName:    "Иван",
		server:       "🇳🇱 Нидерланды",
		category:     "🔌 Не подключается",
		comment:      "второй день не коннектится",
		subscription: "оплачена до 12.06.26",
	}
	msg := buildBugReportMessage(r)
	require.Contains(t, msg, "Багрепорт")
	require.Contains(t, msg, "Иван")
	require.Contains(t, msg, "@ivan")
	require.Contains(t, msg, "tg://user?id=12345")
	require.Contains(t, msg, "🇳🇱 Нидерланды")
	require.Contains(t, msg, "Не подключается")
	require.Contains(t, msg, "второй день не коннектится")
	require.Contains(t, msg, "оплачена до 12.06.26")
}

func TestBuildBugReportMessage_NoServerNoComment(t *testing.T) {
	r := bugReport{telegramID: 1, category: "🐢 Медленно"}
	msg := buildBugReportMessage(r)
	require.Contains(t, msg, "не указан")  // сервер
	require.NotContains(t, msg, "💬")       // блок комментария отсутствует
}
```

**Step 2: Запустить — убедиться, что падает**

Run: `go test ./internal/bot/ -run TestBuildBugReportMessage -v`
Expected: FAIL (undefined: bugReport, buildBugReportMessage)

**Step 3: Минимальная реализация**

В `internal/bot/bug_report.go`:

```go
package bot

import (
	"fmt"
	"html"
	"strings"
	"time"
)

// bugReport — собранные данные одного багрепорта для отправки админу.
type bugReport struct {
	telegramID   int64
	username     string
	firstName    string
	server       string // Remark хоста или "" если не указан
	category     string
	comment      string // "" если пропущен
	subscription string // человекочитаемый статус подписки
}

// buildBugReportMessage формирует HTML-сообщение багрепорта для админа.
func buildBugReportMessage(r bugReport) string {
	var b strings.Builder
	b.WriteString("🛠 <b>Багрепорт</b>\n\n")
	fmt.Fprintf(&b, "📅 %s\n", time.Now().Format("02.01.06 15:04"))

	userLink := fmt.Sprintf("<a href=\"tg://user?id=%d\">%d</a>", r.telegramID, r.telegramID)
	name := html.EscapeString(r.firstName)
	if r.username != "" {
		fmt.Fprintf(&b, "👤 %s (@%s) · %s\n", name, html.EscapeString(r.username), userLink)
	} else {
		fmt.Fprintf(&b, "👤 %s · %s\n", name, userLink)
	}

	server := r.server
	if server == "" {
		server = "не указан"
	}
	fmt.Fprintf(&b, "📡 Сервер: %s\n", html.EscapeString(server))
	fmt.Fprintf(&b, "❗ Проблема: %s\n", html.EscapeString(r.category))

	if r.comment != "" {
		fmt.Fprintf(&b, "💬 «%s»\n", html.EscapeString(r.comment))
	}
	if r.subscription != "" {
		fmt.Fprintf(&b, "\nПодписка: %s", html.EscapeString(r.subscription))
	}
	return b.String()
}
```

**Step 4: Запустить — убедиться, что проходит**

Run: `go test ./internal/bot/ -run TestBuildBugReportMessage -v`
Expected: PASS

**Step 5: Commit**

```bash
git add internal/bot/bug_report.go internal/bot/bug_report_test.go
git commit -m "feat: формирование сообщения багрепорта для админа"
```

---

### Task 2: Обрезка длинного комментария

**Files:**
- Modify: `internal/bot/bug_report.go`
- Test: `internal/bot/bug_report_test.go`

**Step 1: Падающий тест**

```go
func TestTruncateComment(t *testing.T) {
	require.Equal(t, "abc", truncateComment("abc"))
	long := strings.Repeat("я", 2000)
	got := truncateComment(long)
	require.LessOrEqual(t, len([]rune(got)), 1001) // 1000 + символ «…»
	require.True(t, strings.HasSuffix(got, "…"))
}
```
(добавить `"strings"` в импорты теста)

**Step 2:** Run `go test ./internal/bot/ -run TestTruncateComment -v` → FAIL

**Step 3: Реализация** в `bug_report.go`:

```go
// truncateComment обрезает комментарий пользователя до разумной длины.
func truncateComment(s string) string {
	const max = 1000
	r := []rune(s)
	if len(r) <= max {
		return s
	}
	return string(r[:max]) + "…"
}
```

Вызвать в начале формирования (в хендлере при сборе `comment`), но в функции пока достаточно объявления.

**Step 4:** Run test → PASS

**Step 5: Commit**

```bash
git add internal/bot/bug_report.go internal/bot/bug_report_test.go
git commit -m "feat: обрезка длинного комментария багрепорта"
```

---

### Task 3: Кулдаун на пользователя

**Files:**
- Modify: `internal/bot/handlers.go` (поля в struct Bot + init в New)
- Modify: `internal/bot/bug_report.go` (методы кулдауна)
- Test: `internal/bot/bug_report_test.go`

**Step 1: Падающий тест**

```go
func TestBugReportCooldown(t *testing.T) {
	b := &Bot{}
	require.False(t, b.bugReportOnCooldown(42)) // первый раз — нет кулдауна
	b.markBugReportSent(42)
	require.True(t, b.bugReportOnCooldown(42))   // сразу после — кулдаун активен
	require.False(t, b.bugReportOnCooldown(99))  // другой юзер — свободен
}
```

**Step 2:** Run `go test ./internal/bot/ -run TestBugReportCooldown -v` → FAIL

**Step 3: Реализация**

В `bug_report.go`:

```go
// bugReportCooldownDur — интервал между багрепортами одного пользователя.
const bugReportCooldownDur = 10 * time.Minute

// bugReportOnCooldown сообщает, отправлял ли пользователь репорт недавно.
func (b *Bot) bugReportOnCooldown(telegramID int64) bool {
	v, ok := b.bugReportCooldown.Load(telegramID)
	if !ok {
		return false
	}
	last, ok := v.(time.Time)
	if !ok {
		return false
	}
	return time.Since(last) < bugReportCooldownDur
}

// markBugReportSent фиксирует время отправки для кулдауна.
func (b *Bot) markBugReportSent(telegramID int64) {
	b.bugReportCooldown.Store(telegramID, time.Now())
}
```

В `handlers.go` в struct `Bot` добавить поле:

```go
	bugReportMu       sync.RWMutex
	bugReportData     map[int64]bugReportSession // pending-данные багрепорта
	bugReportCooldown sync.Map                   // telegram_id -> time.Time последней отправки
```

В `New()` в инициализацию `bot := &Bot{...}` добавить:

```go
		bugReportData: make(map[int64]bugReportSession),
```

И тип сессии в `bug_report.go`:

```go
// bugReportSession — pending-выбор пользователя в процессе багрепорта.
type bugReportSession struct {
	server   string // Remark выбранного хоста или "" = не указан
	category string
}
```

**Step 4:** Run test → PASS, затем `go build ./...` чтобы убедиться, что struct компилируется.

**Step 5: Commit**

```bash
git add internal/bot/handlers.go internal/bot/bug_report.go internal/bot/bug_report_test.go
git commit -m "feat: кулдаун и сессия багрепорта"
```

---

### Task 4: Методы работы с сессией багрепорта

**Files:**
- Modify: `internal/bot/bug_report.go`
- Test: `internal/bot/bug_report_test.go`

**Step 1: Падающий тест**

```go
func TestBugReportSession(t *testing.T) {
	b := &Bot{bugReportData: make(map[int64]bugReportSession)}

	b.setBugReportServer(7, "🇩🇪 Германия")
	s, ok := b.getBugReportSession(7)
	require.True(t, ok)
	require.Equal(t, "🇩🇪 Германия", s.server)

	b.setBugReportCategory(7, "🐢 Медленно")
	s, _ = b.getBugReportSession(7)
	require.Equal(t, "🐢 Медленно", s.category)
	require.Equal(t, "🇩🇪 Германия", s.server) // сервер сохранился

	b.clearBugReportSession(7)
	_, ok = b.getBugReportSession(7)
	require.False(t, ok)
}
```

**Step 2:** Run → FAIL

**Step 3: Реализация** в `bug_report.go`:

```go
func (b *Bot) setBugReportServer(telegramID int64, server string) {
	b.bugReportMu.Lock()
	defer b.bugReportMu.Unlock()
	s := b.bugReportData[telegramID]
	s.server = server
	b.bugReportData[telegramID] = s
}

func (b *Bot) setBugReportCategory(telegramID int64, category string) {
	b.bugReportMu.Lock()
	defer b.bugReportMu.Unlock()
	s := b.bugReportData[telegramID]
	s.category = category
	b.bugReportData[telegramID] = s
}

func (b *Bot) getBugReportSession(telegramID int64) (bugReportSession, bool) {
	b.bugReportMu.RLock()
	defer b.bugReportMu.RUnlock()
	s, ok := b.bugReportData[telegramID]
	return s, ok
}

func (b *Bot) clearBugReportSession(telegramID int64) {
	b.bugReportMu.Lock()
	defer b.bugReportMu.Unlock()
	delete(b.bugReportData, telegramID)
}
```

**Step 4:** Run test → PASS

**Step 5: Commit**

```bash
git add internal/bot/bug_report.go internal/bot/bug_report_test.go
git commit -m "feat: методы сессии багрепорта"
```

---

### Task 5: Кнопки и клавиатуры

**Files:**
- Modify: `internal/bot/keyboards.go`
- Test: `internal/bot/keyboards_test.go` (или bug_report_test.go)

**Step 1: Падающий тест**

```go
func TestBugServersKeyboard(t *testing.T) {
	hosts := []remnawave.Host{
		{Remark: "🇳🇱 Нидерланды"}, {Remark: "🇩🇪 Германия"},
	}
	kb := BugServersKeyboard(hosts)
	require.NotNil(t, kb.InlineKeyboard)
	// сервера + "не знаю" + "отмена"
	var labels []string
	for _, row := range kb.InlineKeyboard {
		for _, btn := range row {
			labels = append(labels, btn.Text)
		}
	}
	require.Contains(t, labels, "🇳🇱 Нидерланды")
	require.Contains(t, labels, "🇩🇪 Германия")
}

func TestBugCategoriesKeyboard(t *testing.T) {
	kb := BugCategoriesKeyboard()
	require.NotNil(t, kb.InlineKeyboard)
}
```

**Step 2:** Run → FAIL

**Step 3: Реализация.** В `keyboards.go` добавить:

Константы Unique для callback:
```go
const (
	cbBugServer   = "bug_server"   // выбор сервера (Data = индекс хоста или "none")
	cbBugCategory = "bug_category" // выбор категории (Data = код категории)
	cbBugCancel   = "bug_cancel"
)
```

Текстовые кнопки:
```go
const (
	BtnBugReport   = "🛠 Сообщить о проблеме"
	BtnBugSkip     = "⏭ Пропустить"
	BtnBugNoServer = "🤷 Не знаю / все сразу"
)
```

Категории (код → подпись):
```go
// bugCategories — фиксированный список категорий проблем (код callback → подпись).
var bugCategories = []struct{ Code, Label string }{
	{"conn", "🔌 Не подключается"},
	{"slow", "🐢 Медленно работает"},
	{"site", "🌍 Не грузит сайт/сервис"},
	{"other", "✍️ Другое"},
}

func bugCategoryLabel(code string) string {
	for _, c := range bugCategories {
		if c.Code == code {
			return c.Label
		}
	}
	return "Другое"
}
```

Клавиатуры:
```go
// BugServersKeyboard — inline-список серверов для выбора в багрепорте.
func BugServersKeyboard(hosts []remnawave.Host) *tele.ReplyMarkup {
	menu := &tele.ReplyMarkup{}
	var rows []tele.Row
	for i, h := range hosts {
		btn := menu.Data(h.Remark, cbBugServer, fmt.Sprintf("%d", i))
		rows = append(rows, menu.Row(btn))
	}
	rows = append(rows, menu.Row(menu.Data(BtnBugNoServer, cbBugServer, "none")))
	rows = append(rows, menu.Row(menu.Data("🚫 Отмена", cbBugCancel)))
	menu.Inline(rows...)
	return menu
}

// BugCategoriesKeyboard — inline-список категорий проблемы.
func BugCategoriesKeyboard() *tele.ReplyMarkup {
	menu := &tele.ReplyMarkup{}
	var rows []tele.Row
	for _, c := range bugCategories {
		rows = append(rows, menu.Row(menu.Data(c.Label, cbBugCategory, c.Code)))
	}
	rows = append(rows, menu.Row(menu.Data("🚫 Отмена", cbBugCancel)))
	menu.Inline(rows...)
	return menu
}

// BugCommentKeyboard — reply-клавиатура шага ввода комментария.
func BugCommentKeyboard() *tele.ReplyMarkup {
	menu := &tele.ReplyMarkup{ResizeKeyboard: true}
	menu.Reply(menu.Row(menu.Text(BtnBugSkip), menu.Text(BtnCancel)))
	return menu
}
```

**Step 4:** Run → PASS

**Step 5: Commit**

```bash
git add internal/bot/keyboards.go internal/bot/keyboards_test.go
git commit -m "feat: клавиатуры багрепорта"
```

---

### Task 6: Кнопка в главном меню

**Files:**
- Modify: `internal/bot/keyboards.go` (UserMenuKeyboardDynamic)
- Test: `internal/bot/keyboards_test.go`

**Step 1: Падающий тест** — проверить, что в меню есть `BtnBugReport`:

```go
func TestUserMenuHasBugReport(t *testing.T) {
	kb := UserMenuKeyboardDynamic("", false, false)
	var labels []string
	for _, row := range kb.ReplyKeyboard {
		for _, btn := range row {
			labels = append(labels, btn.Text)
		}
	}
	require.Contains(t, labels, BtnBugReport)
}
```

**Step 2:** Run → FAIL

**Step 3: Реализация.** В `UserMenuKeyboardDynamic` добавить строку с кнопкой багрепорта (перед `Инструкции/Инфо` или отдельной строкой):

```go
	rows = append(rows, menu.Row(menu.Text(BtnBugReport)))
```

**Step 4:** Run → PASS. Проверить, что существующие тесты меню не сломались: `go test ./internal/bot/ -run TestUserMenu -v`.

**Step 5: Commit**

```bash
git add internal/bot/keyboards.go internal/bot/keyboards_test.go
git commit -m "feat: кнопка багрепорта в главном меню"
```

---

### Task 7: Handler старта багрепорта + хелпер статуса подписки

**Files:**
- Modify: `internal/bot/bug_report.go`
- Modify: `internal/bot/handlers.go` (роутинг кнопки)

**Step 1:** Реализация `handleBugReportStart` (без отдельного юнит-теста — интеграция с telebot; чистая логика уже покрыта Task 1-5). Логика:

```go
// handleBugReportStart запускает флоу багрепорта: проверка регистрации,
// кулдауна и показ списка серверов.
func (b *Bot) handleBugReportStart(c tele.Context) error {
	telegramID := c.Sender().ID

	user, err := b.db.GetUserByTelegramID(telegramID)
	if err != nil || user == nil {
		return c.Send(MsgNotRegistered, &tele.SendOptions{ParseMode: tele.ModeHTML})
	}
	if b.bugReportOnCooldown(telegramID) {
		return c.Send("Вы недавно уже отправляли сообщение. Попробуйте позже.",
			&tele.SendOptions{ReplyMarkup: b.userKeyboard(telegramID)})
	}

	// Начинаем сессию заново.
	b.clearBugReportSession(telegramID)
	b.bugReportMu.Lock()
	b.bugReportData[telegramID] = bugReportSession{}
	b.bugReportMu.Unlock()

	hosts, err := b.remnawave.GetAllHosts()
	if err != nil || len(hosts) == 0 {
		// Не блокируем — сразу к выбору категории.
		slog.Warn("Bug report: hosts unavailable", "error", err)
		return c.Send("Какая проблема?", &tele.SendOptions{
			ReplyMarkup: BugCategoriesKeyboard(),
		})
	}

	return c.Send("На каком сервере проблема?", &tele.SendOptions{
		ReplyMarkup: BugServersKeyboard(hosts),
	})
}
```

В `handlers.go` в блоке «Кнопки пользователя» добавить:

```go
	case BtnBugReport:
		return b.handleBugReportStart(c)
```

**Step 2:** Run `go build ./...` → success.

**Step 3: Commit**

```bash
git add internal/bot/bug_report.go internal/bot/handlers.go
git commit -m "feat: старт флоу багрепорта"
```

---

### Task 8: Callback-хендлеры выбора сервера и категории

**Files:**
- Modify: `internal/bot/bug_report.go`
- Modify: `internal/bot/handlers.go` (регистрация callback в New)

**Step 1: Реализация** в `bug_report.go`:

```go
// handleBugServerSelected сохраняет выбранный сервер и показывает категории.
func (b *Bot) handleBugServerSelected(c tele.Context) error {
	telegramID := c.Sender().ID
	args := c.Args()
	server := ""
	if len(args) > 0 && args[0] != "none" {
		// Ресолвим индекс из свежего списка хостов.
		if hosts, err := b.remnawave.GetAllHosts(); err == nil {
			if idx, err := strconv.Atoi(args[0]); err == nil && idx >= 0 && idx < len(hosts) {
				server = hosts[idx].Remark
			}
		}
	}
	b.setBugReportServer(telegramID, server)

	if err := c.Edit("Какая проблема?", &tele.SendOptions{
		ReplyMarkup: BugCategoriesKeyboard(),
	}); err != nil {
		return c.RespondAlert("Ошибка")
	}
	return c.Respond()
}

// handleBugCategorySelected сохраняет категорию и просит комментарий.
func (b *Bot) handleBugCategorySelected(c tele.Context) error {
	telegramID := c.Sender().ID
	args := c.Args()
	if len(args) == 0 {
		return c.RespondAlert("Некорректный запрос")
	}
	b.setBugReportCategory(telegramID, bugCategoryLabel(args[0]))

	// Убираем inline-клавиатуру.
	_ = c.Edit("✅ Принято. Опишите проблему ниже.")

	b.userStates.Set(telegramID, StateWaitBugComment)
	if err := c.Send("Опишите проблему одним сообщением или нажмите «Пропустить».",
		&tele.SendOptions{ReplyMarkup: BugCommentKeyboard()}); err != nil {
		return c.RespondAlert("Ошибка")
	}
	return c.Respond()
}

// handleBugCancel отменяет багрепорт.
func (b *Bot) handleBugCancel(c tele.Context) error {
	telegramID := c.Sender().ID
	b.clearBugReportSession(telegramID)
	b.userStates.Delete(telegramID)
	_ = c.Edit("Отменено.")
	_ = c.Send("Возврат в меню.", &tele.SendOptions{ReplyMarkup: b.userKeyboard(telegramID)})
	return c.Respond()
}
```

Добавить импорты `strconv`, `log/slog` если нужно.

В `handlers.go` в `New()` зарегистрировать callback (рядом с device-кнопками):

```go
	bugMenu := &tele.ReplyMarkup{}
	btnBugServer := bugMenu.Data("", cbBugServer)
	btnBugCategory := bugMenu.Data("", cbBugCategory)
	btnBugCancel := bugMenu.Data("", cbBugCancel)
	b.Handle(&btnBugServer, bot.handleBugServerSelected)
	b.Handle(&btnBugCategory, bot.handleBugCategorySelected)
	b.Handle(&btnBugCancel, bot.handleBugCancel)
```

**Step 2:** Run `go build ./...` → success.

**Step 3: Commit**

```bash
git add internal/bot/bug_report.go internal/bot/handlers.go
git commit -m "feat: callback-хендлеры выбора сервера и категории багрепорта"
```

---

### Task 9: Завершение — приём комментария и отправка админу

**Files:**
- Modify: `internal/bot/bug_report.go` (handleBugComment, sendBugReport, subscriptionStatusString)
- Modify: `internal/bot/handlers.go` (state-роутинг StateWaitBugComment + константа состояния)

**Step 1:** Добавить константу состояния в `handlers.go`:

```go
	StateWaitBugComment = "wait_bug_comment" // Ожидание текста багрепорта
```

В `handleTextMessage` в `switch state` добавить кейс (рядом с другими):

```go
	case StateWaitBugComment:
		if text == BtnCancel {
			b.clearBugReportSession(telegramID)
			b.userStates.Delete(telegramID)
			return c.Send("Отменено.", &tele.SendOptions{ReplyMarkup: b.userKeyboard(telegramID)})
		}
		comment := text
		if text == BtnBugSkip {
			comment = ""
		}
		return b.finishBugReport(c, comment)
```

**Step 2:** Реализация в `bug_report.go`:

```go
// subscriptionStatusString — человекочитаемый статус подписки для багрепорта.
func (b *Bot) subscriptionStatusString(telegramID int64) string {
	remUser, err := b.remnawave.GetUserByTelegramID(telegramID)
	if err != nil || remUser == nil {
		return "нет данных"
	}
	switch determineSubscriptionType(remUser, b.isTrialUser(telegramID)) {
	case subTypeTrial:
		return "триал"
	case subTypeGrace:
		return "grace (истекла, ещё доступна)"
	default:
		return "оплачена до " + remUser.ExpireAt.Format("02.01.06")
	}
}

// finishBugReport собирает данные, шлёт админу и завершает флоу.
func (b *Bot) finishBugReport(c tele.Context, comment string) error {
	telegramID := c.Sender().ID
	session, _ := b.getBugReportSession(telegramID)

	report := bugReport{
		telegramID:   telegramID,
		username:     c.Sender().Username,
		firstName:    c.Sender().FirstName,
		server:       session.server,
		category:     session.category,
		comment:      truncateComment(comment),
		subscription: b.subscriptionStatusString(telegramID),
	}

	go b.sendBugReportToAdmin(report)

	b.markBugReportSent(telegramID)
	b.clearBugReportSession(telegramID)
	b.userStates.Delete(telegramID)

	return c.Send("✅ Спасибо! Сообщение отправлено, мы разберёмся.",
		&tele.SendOptions{ReplyMarkup: b.userKeyboard(telegramID)})
}

// sendBugReportToAdmin шлёт багрепорт администратору в личку.
func (b *Bot) sendBugReportToAdmin(report bugReport) {
	admin := &tele.User{ID: b.config.AdminID}
	if _, err := b.bot.Send(admin, buildBugReportMessage(report),
		&tele.SendOptions{ParseMode: tele.ModeHTML}); err != nil {
		slog.Error("Failed to send bug report to admin", "error", err, "telegram_id", report.telegramID)
	}
}
```

**Step 3:** Добавить `BtnBugReport` и `BtnBugSkip` в `isMenuNavigationButton` НЕ нужно (они не должны сбрасывать payment-flow), но проверить, что нажатие кнопок меню в StateWaitBugComment работает корректно — добавить сброс состояния при навигационных кнопках, если потребуется. Запустить `go build ./...`.

**Step 4:** `make tests` → все тесты PASS. `make fmt` → без ошибок.

**Step 5: Commit**

```bash
git add internal/bot/bug_report.go internal/bot/handlers.go
git commit -m "feat: приём комментария и отправка багрепорта админу"
```

---

### Task 10: Документация и финальная проверка

**Files:**
- Modify: `CLAUDE.md` (раздел «Важные заметки» — пункт про багрепорт)
- Create: `docs/progress/2026-06-05-bug-report-progress.md`

**Step 1:** Через агента `dscs-updater` (или вручную) обновить `CLAUDE.md`: добавить пункт о фиче багрепорта и список новых ENV (если появятся — их нет, всё in-memory).

**Step 2:** Создать `docs/progress/2026-06-05-bug-report-progress.md` со ссылкой на план и отметками выполнения.

**Step 3:** Финальный прогон:

```bash
make fmt
make tests
```
Expected: всё зелёное.

**Step 4: Commit**

```bash
git add CLAUDE.md docs/progress/2026-06-05-bug-report-progress.md
git commit -m "docs: описать фичу багрепорта"
```

---

## Чек-лист готовности

- [ ] `make tests` зелёный
- [ ] `make fmt` без ошибок
- [ ] Кнопка «🛠 Сообщить о проблеме» видна зарегистрированному пользователю
- [ ] Флоу: сервер → категория → комментарий → «Спасибо»
- [ ] Багрепорт приходит админу с данными юзера и статусом подписки
- [ ] Кулдаун 10 минут работает
- [ ] Отмена на любом шаге возвращает в меню
- [ ] Нет хостов → шаг сервера пропускается, репорт «не указан»
