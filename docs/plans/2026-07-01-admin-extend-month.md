# Ручное продление подписки админом — план реализации

> **For Claude:** REQUIRED SUB-SKILL: Use superpowers:executing-plans to implement this plan task-by-task.

**Goal:** Дать админу кнопку в карточке пользователя для ручного продления подписки на 1 месяц (техническое продление без финансового учёта).

**Architecture:** Продление трогает только Remnawave (`EnableUser` двигает expireAt, ставит ACTIVE, снимает лимит трафика). Логика расчёта даты выносится в чистую функцию `nextMonthExpireAt`, переиспользуемую обычной оплатой. UX: inline-кнопка в карточке → подтверждение → продление. Защита от дабл-клика через `getPaymentMutex` + `c.Edit`.

**Tech Stack:** Go, telebot.v3, Remnawave HTTP API, httptest для тестов.

**Дизайн-документ:** `docs/plans/2026-07-01-admin-extend-month-design.md`

---

## Общие правила

- После каждой задачи: `make fmt` и `make tests` (из CLAUDE.md, обязательно).
- Комментарии в коде — на русском.
- Формат коммитов: `<type>: <описание>` на русском (feat/fix/refactor/chore/test).
- Никакого авторства Claude в коммитах.

---

## Task 1: Чистая функция `nextMonthExpireAt` + рефактор activateSubscription

**Files:**
- Create: `internal/bot/admin_extend.go`
- Create: `internal/bot/admin_extend_test.go`
- Modify: `internal/bot/payment.go` (функция `activateSubscription`, строки ~305-330)

**Step 1: Написать падающие тесты**

В `internal/bot/admin_extend_test.go`:

```go
package bot

import (
	"testing"
	"time"

	"github.com/fus1ond/vpn_bot/internal/remnawave"
)

func TestNextMonthExpireAt(t *testing.T) {
	now := time.Date(2026, 3, 10, 12, 0, 0, 0, time.UTC)

	tests := []struct {
		name   string
		status string
		expire time.Time
		want   time.Time
	}{
		{
			name:   "активная подписка в будущем — плюсуем к expireAt",
			status: remnawave.StatusActive,
			expire: time.Date(2026, 3, 20, 12, 0, 0, 0, time.UTC),
			want:   time.Date(2026, 4, 20, 12, 0, 0, 0, time.UTC),
		},
		{
			name:   "истёкшая (не ACTIVE) — считаем от now",
			status: remnawave.StatusExpired,
			expire: time.Date(2026, 3, 1, 12, 0, 0, 0, time.UTC),
			want:   time.Date(2026, 4, 10, 12, 0, 0, 0, time.UTC),
		},
		{
			name:   "disabled grace — считаем от now",
			status: remnawave.StatusDisabled,
			expire: time.Date(2026, 3, 5, 12, 0, 0, 0, time.UTC),
			want:   time.Date(2026, 4, 10, 12, 0, 0, 0, time.UTC),
		},
		{
			name:   "ACTIVE но дата в прошлом — считаем от now",
			status: remnawave.StatusActive,
			expire: time.Date(2026, 3, 1, 12, 0, 0, 0, time.UTC),
			want:   time.Date(2026, 4, 10, 12, 0, 0, 0, time.UTC),
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			remUser := &remnawave.User{Status: tt.status, ExpireAt: tt.expire}
			got := nextMonthExpireAt(remUser, now)
			if !got.Equal(tt.want) {
				t.Errorf("nextMonthExpireAt() = %v, want %v", got, tt.want)
			}
		})
	}
}
```

**Step 2: Запустить тест — убедиться, что не компилируется/падает**

Run: `make tests`
Expected: FAIL — `nextMonthExpireAt` не определена.

**Step 3: Реализовать функцию**

В `internal/bot/admin_extend.go`:

```go
package bot

import (
	"time"

	"github.com/fus1ond/vpn_bot/internal/remnawave"
)

// nextMonthExpireAt считает новую дату окончания подписки при продлении на месяц.
// Если подписка активна и не истекла — плюсуем к текущему expireAt (не теряем остаток).
// Иначе (триал истёк, grace period, disabled) — считаем от now.
func nextMonthExpireAt(remUser *remnawave.User, now time.Time) time.Time {
	if remUser.ExpireAt.After(now) && remUser.Status == remnawave.StatusActive {
		return remUser.ExpireAt.AddDate(0, 1, 0)
	}
	return now.AddDate(0, 1, 0)
}
```

**Step 4: Запустить тест — убедиться, что проходит**

Run: `make tests`
Expected: PASS для `TestNextMonthExpireAt`.

**Step 5: Рефакторить `activateSubscription`**

В `internal/bot/payment.go` заменить блок расчёта даты (строки ~317-326):

```go
	now := time.Now().UTC()
	var newExpireAt time.Time
	if remUser.ExpireAt.After(now) && remUser.Status == "ACTIVE" {
		newExpireAt = remUser.ExpireAt.AddDate(0, 1, 0)
	} else {
		newExpireAt = now.AddDate(0, 1, 0)
	}
```

на:

```go
	newExpireAt := nextMonthExpireAt(remUser, time.Now().UTC())
```

**Step 6: Прогнать все тесты и форматирование**

Run: `make fmt && make tests`
Expected: PASS всё (включая существующие тесты платежей — поведение эквивалентно).

**Step 7: Commit**

```bash
git add internal/bot/admin_extend.go internal/bot/admin_extend_test.go internal/bot/payment.go
git commit -m "refactor: вынести расчёт даты продления в nextMonthExpireAt"
```

---

## Task 2: Клавиатура карточки юзера с кнопкой продления

**Files:**
- Modify: `internal/bot/keyboards.go` (константы + новая функция клавиатуры)
- Modify: `internal/bot/admin.go` (`processAdminUserInfo`, строки ~303-306)

**Step 1: Добавить Unique-константы**

В `internal/bot/keyboards.go` после блока констант багрепорта (строка ~26):

```go
// Unique-идентификаторы inline-кнопок ручного продления подписки админом
const (
	cbAdminExtendMonth   = "adm_ext_month"  // запрос продления (Data = targetID)
	cbAdminExtendConfirm = "adm_ext_ok"      // подтверждение (Data = targetID)
	cbAdminExtendCancel  = "adm_ext_cancel"  // отмена (Data = targetID)
)
```

**Step 2: Добавить функцию клавиатуры карточки**

В `internal/bot/keyboards.go` (рядом с другими admin-клавиатурами):

```go
// AdminUserInfoKeyboard — inline-клавиатура карточки пользователя.
// Кнопка «Продлить на месяц» скрыта для безлимитных подписок (expireAt год >= 2099).
func AdminUserInfoKeyboard(targetID int64, remUser *remnawave.User) *tele.ReplyMarkup {
	menu := &tele.ReplyMarkup{}
	var rows []tele.Row

	if remUser != nil && remUser.ExpireAt.Year() < 2099 {
		extend := menu.Data("➕ Продлить на месяц", cbAdminExtendMonth, fmt.Sprintf("%d", targetID))
		rows = append(rows, menu.Row(extend))
	}

	menu.Inline(rows...)
	return menu
}
```

**Step 3: Подключить клавиатуру в карточке**

В `internal/bot/admin.go`, `processAdminUserInfo`, заменить финальный `c.Send` (строки ~303-306):

```go
	return c.Send(msg.String(), &tele.SendOptions{
		ParseMode:   tele.ModeHTML,
		ReplyMarkup: AdminManageKeyboard(),
	})
```

на:

```go
	return c.Send(msg.String(), &tele.SendOptions{
		ParseMode:   tele.ModeHTML,
		ReplyMarkup: b.AdminUserInfoKeyboard(targetID, remUser),
	})
```

> Примечание: `AdminUserInfoKeyboard` без ресивера — сделать функцией пакета (без `b.`).
> В вызове тогда `AdminUserInfoKeyboard(targetID, remUser)`.

**Step 4: Прогнать форматирование и тесты**

Run: `make fmt && make tests`
Expected: PASS (существующий `TestProcessAdminUserInfo`, если он проверяет reply-клаву — поправить его под inline).

**Step 5: Commit**

```bash
git add internal/bot/keyboards.go internal/bot/admin.go
git commit -m "feat: inline-кнопка продления в карточке пользователя"
```

---

## Task 3: Хендлер запроса продления (показ подтверждения)

**Files:**
- Modify: `internal/bot/admin_extend.go`
- Modify: `internal/bot/keyboards.go` (клавиатура подтверждения)
- Test: `internal/bot/admin_extend_test.go`

**Step 1: Добавить клавиатуру подтверждения в keyboards.go**

```go
// AdminExtendConfirmKeyboard — кнопки подтверждения/отмены продления.
func AdminExtendConfirmKeyboard(targetID int64) *tele.ReplyMarkup {
	menu := &tele.ReplyMarkup{}
	idStr := fmt.Sprintf("%d", targetID)
	ok := menu.Data("✅ Подтвердить", cbAdminExtendConfirm, idStr)
	cancel := menu.Data("❌ Отмена", cbAdminExtendCancel, idStr)
	menu.Inline(menu.Row(ok, cancel))
	return menu
}
```

**Step 2: Реализовать `handleAdminExtendMonth` в admin_extend.go**

```go
// parseAdminExtendTargetID парсит targetID из callback-данных.
func parseAdminExtendTargetID(c tele.Context) (int64, bool) {
	args := c.Args()
	if len(args) == 0 {
		return 0, false
	}
	id, err := strconv.ParseInt(strings.TrimSpace(args[0]), 10, 64)
	if err != nil {
		return 0, false
	}
	return id, true
}

// handleAdminExtendMonth показывает экран подтверждения продления.
func (b *Bot) handleAdminExtendMonth(c tele.Context) error {
	if !b.isAdmin(c) {
		return c.Respond()
	}

	targetID, ok := parseAdminExtendTargetID(c)
	if !ok {
		return c.RespondAlert("Некорректный запрос")
	}

	dbUser, err := b.db.GetUserByTelegramID(targetID)
	if err != nil || dbUser == nil {
		return c.RespondAlert("Пользователь не найден")
	}

	remUser, err := b.remnawave.GetUser(dbUser.RemnawaveUUID)
	if err != nil {
		slog.Error("Failed to load Remnawave user for extend", "error", err, "telegram_id", targetID)
		return c.RespondAlert("Ошибка получения данных подписки")
	}

	newExpireAt := nextMonthExpireAt(remUser, time.Now().UTC())
	text := fmt.Sprintf(
		"Продлить подписку %s до <b>%s</b>?",
		formatUserLabel(dbUser.FirstName, dbUser.Username, dbUser.TelegramID),
		newExpireAt.Format("02.01.2006"),
	)

	return c.Edit(text, &tele.SendOptions{
		ParseMode:   tele.ModeHTML,
		ReplyMarkup: AdminExtendConfirmKeyboard(targetID),
	})
}
```

Добавить импорты в admin_extend.go: `fmt`, `log/slog`, `strconv`, `strings`, `tele "gopkg.in/telebot.v3"`.

**Step 3: Тест на парсинг targetID**

```go
func TestParseAdminExtendTargetID(t *testing.T) {
	// валидный
	c := &MockContext{args: []string{"12345"}}
	id, ok := parseAdminExtendTargetID(c)
	if !ok || id != 12345 {
		t.Errorf("ожидали (12345,true), got (%d,%v)", id, ok)
	}
	// пустой
	c2 := &MockContext{args: nil}
	if _, ok := parseAdminExtendTargetID(c2); ok {
		t.Error("ожидали ok=false для пустых args")
	}
	// невалидный
	c3 := &MockContext{args: []string{"abc"}}
	if _, ok := parseAdminExtendTargetID(c3); ok {
		t.Error("ожидали ok=false для нечислового args")
	}
}
```

> Проверить, что `MockContext` в тестах поддерживает поле `args` и метод `Args()`.
> Если нет — добавить в MockContext (см. существующий mock в admin_test.go / handlers_test.go).

**Step 4: Прогнать тесты**

Run: `make fmt && make tests`
Expected: PASS.

**Step 5: Commit**

```bash
git add internal/bot/admin_extend.go internal/bot/admin_extend_test.go internal/bot/keyboards.go
git commit -m "feat: экран подтверждения ручного продления"
```

---

## Task 4: Хендлеры подтверждения и отмены (само продление)

**Files:**
- Modify: `internal/bot/admin_extend.go`
- Test: `internal/bot/admin_extend_test.go`

**Step 1: Реализовать `handleAdminExtendConfirm` и `handleAdminExtendCancel`**

```go
// extendedSubscriptionMessage — текст пользователю о ручном продлении.
func (b *Bot) extendedSubscriptionMessage(telegramID int64) string {
	remUser, _ := b.remnawave.GetUserByTelegramID(telegramID)
	if remUser != nil {
		return fmt.Sprintf(
			"✅ Ваша подписка продлена до <b>%s</b>.\n\nЛимит трафика снят — пользуйтесь без ограничений.",
			remUser.ExpireAt.Format("02.01.2006"),
		)
	}
	return "✅ Ваша подписка продлена."
}

// handleAdminExtendConfirm выполняет продление на месяц.
func (b *Bot) handleAdminExtendConfirm(c tele.Context) error {
	if !b.isAdmin(c) {
		return c.Respond()
	}

	targetID, ok := parseAdminExtendTargetID(c)
	if !ok {
		return c.RespondAlert("Некорректный запрос")
	}

	// Сериализуем с платёжными операциями по этому юзеру (callback от Platega и т.п.).
	mu := getPaymentMutex(targetID)
	mu.Lock()
	defer mu.Unlock()

	dbUser, err := b.db.GetUserByTelegramID(targetID)
	if err != nil || dbUser == nil {
		return c.RespondAlert("Пользователь не найден")
	}

	// Перечитываем свежего remUser: дата могла измениться (юзер мог сам оплатить).
	remUser, err := b.remnawave.GetUser(dbUser.RemnawaveUUID)
	if err != nil {
		slog.Error("Failed to reload Remnawave user before extend", "error", err, "telegram_id", targetID)
		return c.RespondAlert("Ошибка получения данных подписки")
	}

	newExpireAt := nextMonthExpireAt(remUser, time.Now().UTC())

	if err := b.remnawave.EnableUser(dbUser.RemnawaveUUID, newExpireAt); err != nil {
		slog.Error("Failed to extend subscription", "error", err, "telegram_id", targetID)
		return c.RespondAlert("❌ Не удалось продлить. Попробуйте ещё раз.")
	}

	// Очищаем маркеры уведомлений (юзер мог быть в grace period).
	b.db.ClearNotifications(targetID)

	// Уведомляем пользователя.
	_ = b.sendSchedulerMessageWithKeyboard(targetID, b.extendedSubscriptionMessage(targetID), b.userKeyboard(targetID))

	// Убираем кнопки, показываем результат админу.
	_ = c.Edit(fmt.Sprintf("✅ Подписка продлена до <b>%s</b>.", newExpireAt.Format("02.01.2006")), &tele.SendOptions{
		ParseMode: tele.ModeHTML,
	})
	return c.Respond()
}

// handleAdminExtendCancel отменяет продление.
func (b *Bot) handleAdminExtendCancel(c tele.Context) error {
	if !b.isAdmin(c) {
		return c.Respond()
	}
	_ = c.Edit("Продление отменено.")
	return c.Respond()
}
```

> Проверить точные сигнатуры хелперов перед использованием:
> `sendSchedulerMessageWithKeyboard`, `userKeyboard`, `ClearNotifications`,
> `GetUserByTelegramID` (remnawave), `formatUserLabel`. Все они уже используются
> в payment.go / admin.go — скопировать вызовы как есть.

**Step 2: Тест confirm-flow с httptest-моком Remnawave**

По образцу `admin_test.go` (строки ~224+): поднять httptest-сервер, отдающий
JSON пользователя на GET и принимающий PATCH. Проверить:
- не-админ → EnableUser не вызывается
- невалидный targetID → RespondAlert, EnableUser не вызывается
- успех → PATCH ушёл с `trafficLimitBytes=0` и корректным `expireAt`

```go
func TestHandleAdminExtendConfirm_NotAdmin(t *testing.T) {
	// bot с AdminID != sender → EnableUser не должен вызываться
	// (детали mock — по образцу admin_test.go)
}
```

> Точную структуру мока взять из существующего теста `admin_test.go:224-260`.

**Step 3: Прогнать тесты**

Run: `make fmt && make tests`
Expected: PASS.

**Step 4: Commit**

```bash
git add internal/bot/admin_extend.go internal/bot/admin_extend_test.go
git commit -m "feat: продление подписки на месяц по подтверждению админа"
```

---

## Task 5: Регистрация inline-обработчиков

**Files:**
- Modify: `internal/bot/handlers.go` (в `New`, рядом с devices/bug_report, строки ~165-175)

**Step 1: Добавить регистрацию**

После блока багрепорта:

```go
	// Inline-кнопки ручного продления подписки админом (роутинг по Unique)
	extMenu := &tele.ReplyMarkup{}
	btnExtMonth := extMenu.Data("", cbAdminExtendMonth)
	btnExtConfirm := extMenu.Data("", cbAdminExtendConfirm)
	btnExtCancel := extMenu.Data("", cbAdminExtendCancel)

	b.Handle(&btnExtMonth, bot.handleAdminExtendMonth)
	b.Handle(&btnExtConfirm, bot.handleAdminExtendConfirm)
	b.Handle(&btnExtCancel, bot.handleAdminExtendCancel)
```

**Step 2: Прогнать тесты и форматирование**

Run: `make fmt && make tests`
Expected: PASS.

**Step 3: Commit**

```bash
git add internal/bot/handlers.go
git commit -m "feat: регистрация обработчиков продления подписки"
```

---

## Task 6: Удаление мёртвого кода ExtendUserSubscription

**Files:**
- Modify: `internal/remnawave/client.go` (удалить `ExtendUserSubscription`, `CalculateExtendedExpireAt`)
- Modify: `internal/remnawave/client_test.go` (удалить их тесты)

**Step 1: Проверить, что функции точно нигде не используются**

Run: `grep -rn "ExtendUserSubscription\|CalculateExtendedExpireAt" internal/ cmd/ | grep -v "_test.go\|client.go"`
Expected: пусто (подтверждение мёртвости).

**Step 2: Удалить функции из client.go и их тесты из client_test.go**

Удалить: `CalculateExtendedExpireAt` (client.go:385-400), `ExtendUserSubscription`
(client.go:402+), соответствующие тесты в client_test.go.
Проверить, не осиротели ли импорты (например `fmt`).

**Step 3: Прогнать тесты и форматирование**

Run: `make fmt && make tests`
Expected: PASS, ничего не сломалось.

**Step 4: Commit**

```bash
git add internal/remnawave/client.go internal/remnawave/client_test.go
git commit -m "chore: удалить неиспользуемый ExtendUserSubscription"
```

---

## Task 7: Ручная проверка и документация

**Step 1: Собрать бота**

Run: `make down && make up && make logs`
Expected: бот стартует без ошибок.

**Step 2: Ручной прогон (в Telegram)**

1. Пригласить/взять триального юзера.
2. Админ → «Инфо о пользователе» → ввести его TG ID.
3. Проверить: в карточке есть кнопка «➕ Продлить на месяц».
4. Нажать → появляется подтверждение с датой.
5. Нажать «Подтвердить» → кнопки исчезают, показывается «✅ Продлено до …».
6. Проверить: юзер получил уведомление, в Remnawave expireAt +1мес, лимит трафика снят.
7. Проверить безлимитного (админского) юзера — кнопки продления НЕТ.

**Step 3: Обновить документацию**

Через субагента обновить CLAUDE.md (добавить пункт в «Важные заметки» про ручное
продление) и создать `docs/progress/2026-07-01-admin-extend-month.md` со ссылкой на план.

**Step 4: Финальный коммит документации**

```bash
git add CLAUDE.md docs/progress/2026-07-01-admin-extend-month.md
git commit -m "docs: описание ручного продления подписки админом"
```
