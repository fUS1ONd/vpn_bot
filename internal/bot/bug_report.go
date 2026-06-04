package bot

import (
	"fmt"
	"html"
	"log/slog"
	"strconv"
	"strings"
	"time"

	tele "gopkg.in/telebot.v3"
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
	server   string // Remark выбранного хоста или "" = не указан
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

// setBugReportServer сохраняет выбранный сервер в pending-сессию багрепорта.
func (b *Bot) setBugReportServer(telegramID int64, server string) {
	b.bugReportMu.Lock()
	defer b.bugReportMu.Unlock()
	s := b.bugReportData[telegramID]
	s.server = server
	b.bugReportData[telegramID] = s
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

	hosts, err := b.remnawave.GetAllHosts()
	if err != nil || len(hosts) == 0 {
		// Хостов нет/ошибка — не блокируем юзера, сразу к выбору категории.
		slog.Warn("Bug report: hosts unavailable", "error", err)
		return c.Send("Какая проблема?", &tele.SendOptions{
			ReplyMarkup: BugCategoriesKeyboard(),
		})
	}

	return c.Send("На каком сервере проблема?", &tele.SendOptions{
		ReplyMarkup: BugServersKeyboard(hosts),
	})
}

// handleBugServerSelected сохраняет выбранный сервер и показывает категории.
func (b *Bot) handleBugServerSelected(c tele.Context) error {
	telegramID := c.Sender().ID
	args := c.Args()
	server := ""
	if len(args) > 0 && args[0] != "none" {
		// Ресолвим индекс из свежего списка хостов (список мог измениться).
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
