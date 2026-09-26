package bot

import (
	"fmt"
	"html"
	"log/slog"
	"strconv"
	"strings"
	"time"

	tele "gopkg.in/telebot.v3"

	"github.com/fus1ond/vpn_bot/internal/remnawave"
)

// bugReport — собранные данные одного багрепорта для отправки админу.
type bugReport struct {
	telegramID   int64
	username     string
	firstName    string
	servers      []string // Remark выбранных хостов; пусто = не указан
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

	server := "не указан"
	if len(r.servers) > 0 {
		escaped := make([]string, len(r.servers))
		for i, s := range r.servers {
			escaped[i] = html.EscapeString(s)
		}
		server = strings.Join(escaped, ", ")
	}
	fmt.Fprintf(&b, "📡 Сервер: %s\n", server)
	fmt.Fprintf(&b, "❗ Проблема: %s\n", html.EscapeString(r.category))

	if r.comment != "" {
		fmt.Fprintf(&b, "💬 «%s»\n", html.EscapeString(r.comment))
	}
	if r.subscription != "" {
		fmt.Fprintf(&b, "\nПодписка: %s", html.EscapeString(r.subscription))
	}
	return b.String()
}

// truncateComment обрезает комментарий пользователя до разумной длины.
func truncateComment(s string) string {
	const max = 1000
	r := []rune(s)
	if len(r) <= max {
		return s
	}
	return string(r[:max]) + "…"
}

// bugReportSession — pending-выбор пользователя в процессе багрепорта.
type bugReportSession struct {
	servers  []string // Remark выбранных хостов; пусто = не указан / все
	category string
}

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

// toggleBugReportServer добавляет/убирает сервер из выбора и возвращает признак,
// выбран ли он теперь (для UX-фидбэка).
func (b *Bot) toggleBugReportServer(telegramID int64, server string) bool {
	b.bugReportMu.Lock()
	defer b.bugReportMu.Unlock()
	s := b.bugReportData[telegramID]
	for i, v := range s.servers {
		if v == server {
			// уже выбран — снимаем
			s.servers = append(s.servers[:i], s.servers[i+1:]...)
			b.bugReportData[telegramID] = s
			return false
		}
	}
	s.servers = append(s.servers, server)
	b.bugReportData[telegramID] = s
	return true
}

// selectedBugReportServers возвращает множество выбранных серверов пользователя.
func (b *Bot) selectedBugReportServers(telegramID int64) map[string]bool {
	b.bugReportMu.RLock()
	defer b.bugReportMu.RUnlock()
	selected := make(map[string]bool)
	for _, v := range b.bugReportData[telegramID].servers {
		selected[v] = true
	}
	return selected
}

// setBugReportCategory сохраняет выбранную категорию в pending-сессию багрепорта.
func (b *Bot) setBugReportCategory(telegramID int64, category string) {
	b.bugReportMu.Lock()
	defer b.bugReportMu.Unlock()
	s := b.bugReportData[telegramID]
	s.category = category
	b.bugReportData[telegramID] = s
}

// getBugReportSession возвращает pending-сессию багрепорта пользователя.
func (b *Bot) getBugReportSession(telegramID int64) (bugReportSession, bool) {
	b.bugReportMu.RLock()
	defer b.bugReportMu.RUnlock()
	s, ok := b.bugReportData[telegramID]
	return s, ok
}

// clearBugReportSession удаляет pending-сессию багрепорта пользователя.
func (b *Bot) clearBugReportSession(telegramID int64) {
	b.bugReportMu.Lock()
	defer b.bugReportMu.Unlock()
	delete(b.bugReportData, telegramID)
}

// enabledHosts возвращает только включённые хосты (isDisabled=false).
// Скрытые (isHidden), но включённые хосты остаются в списке.
func (b *Bot) enabledHosts() ([]remnawave.Host, error) {
	hosts, err := b.remnawave.GetAllHosts()
	if err != nil {
		return nil, err
	}
	enabled := make([]remnawave.Host, 0, len(hosts))
	for _, h := range hosts {
		if !h.IsDisabled {
			enabled = append(enabled, h)
		}
	}
	return enabled, nil
}

// handleBugReportStart запускает флоу багрепорта: проверка регистрации,
// кулдауна и показ списка серверов (или сразу категорий, если хостов нет).
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

	// Начинаем сессию заново — затираем возможный недоигранный флоу.
	b.clearBugReportSession(telegramID)
	b.bugReportMu.Lock()
	b.bugReportData[telegramID] = bugReportSession{}
	b.bugReportMu.Unlock()

	hosts, err := b.enabledHosts()
	if err != nil || len(hosts) == 0 {
		// Хостов нет/ошибка — не блокируем юзера, сразу к выбору категории.
		slog.Warn("Bug report: hosts unavailable", "error", err)
		return c.Send("Какая проблема?", &tele.SendOptions{
			ReplyMarkup: BugCategoriesKeyboard(),
		})
	}

	return c.Send("На каких серверах проблема? Можно выбрать несколько.", &tele.SendOptions{
		ReplyMarkup: BugServersKeyboard(hosts, nil),
	})
}

// handleBugServerToggle переключает выбор сервера и перерисовывает список с галочками.
func (b *Bot) handleBugServerToggle(c tele.Context) error {
	telegramID := c.Sender().ID
	args := c.Args()
	if len(args) == 0 {
		return c.RespondAlert("Некорректный запрос")
	}

	hosts, err := b.enabledHosts()
	if err != nil {
		return c.RespondAlert("Ошибка получения списка серверов")
	}

	idx, err := strconv.Atoi(args[0])
	if err != nil || idx < 0 || idx >= len(hosts) {
		// Список устарел — перерисуем актуальный с текущими галочками.
		_ = c.Edit("На каких серверах проблема? Можно выбрать несколько.", &tele.SendOptions{
			ReplyMarkup: BugServersKeyboard(hosts, b.selectedBugReportServers(telegramID)),
		})
		return c.RespondAlert("Список обновлён, попробуйте снова")
	}

	b.toggleBugReportServer(telegramID, hosts[idx].Remark)

	_ = c.Edit("На каких серверах проблема? Можно выбрать несколько.", &tele.SendOptions{
		ReplyMarkup: BugServersKeyboard(hosts, b.selectedBugReportServers(telegramID)),
	})
	return c.Respond()
}

// handleBugServersDone завершает выбор серверов и показывает категории.
// Используется и для кнопки «Готово», и для «Не знаю / все сразу» (data="none" — сброс выбора).
func (b *Bot) handleBugServersDone(c tele.Context) error {
	telegramID := c.Sender().ID
	args := c.Args()
	if len(args) > 0 && args[0] == "none" {
		// «Не знаю / все сразу» — очищаем выбор серверов.
		b.bugReportMu.Lock()
		s := b.bugReportData[telegramID]
		s.servers = nil
		b.bugReportData[telegramID] = s
		b.bugReportMu.Unlock()
	}

	if err := c.Edit("Какая проблема?", &tele.SendOptions{
		ReplyMarkup: BugCategoriesKeyboard(),
	}); err != nil {
		return c.RespondAlert("Ошибка")
	}
	return c.Respond()
}

// handleBugCategorySelected сохраняет категорию и просит описать проблему.
func (b *Bot) handleBugCategorySelected(c tele.Context) error {
	telegramID := c.Sender().ID
	args := c.Args()
	if len(args) == 0 {
		return c.RespondAlert("Некорректный запрос")
	}
	b.setBugReportCategory(telegramID, bugCategoryLabel(args[0]))

	// Убираем inline-клавиатуру у предыдущего сообщения.
	_ = c.Edit("✅ Принято. Опишите проблему ниже.")

	b.userStates.Set(telegramID, StateWaitBugComment)
	if err := c.Send("Опишите проблему одним сообщением или нажмите «Пропустить».",
		&tele.SendOptions{ReplyMarkup: BugCommentKeyboard()}); err != nil {
		return c.RespondAlert("Ошибка")
	}
	return c.Respond()
}

// handleBugCancel отменяет багрепорт на любом из inline-шагов.
func (b *Bot) handleBugCancel(c tele.Context) error {
	telegramID := c.Sender().ID
	b.clearBugReportSession(telegramID)
	b.userStates.Delete(telegramID)
	_ = c.Edit("Отменено.")
	_ = c.Send("Возврат в меню.", &tele.SendOptions{ReplyMarkup: b.userKeyboard(telegramID)})
	return c.Respond()
}

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
	case subTypeInfinite:
		return "безлимит (бессрочная)"
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
		servers:      session.servers,
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
