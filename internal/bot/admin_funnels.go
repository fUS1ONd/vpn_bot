package bot

import (
	"context"
	"fmt"
	"html"
	"log/slog"
	"math"
	"strings"
	"time"

	"github.com/fus1ond/vpn_bot/internal/funnels"
	tele "gopkg.in/telebot.v3"
)

// funnelReporter — модуль воронок глазами админки. Модуль отдаёт структуру
// без текстов, подписи рисует бот.
type funnelReporter interface {
	List() []funnels.Funnel
	Report(ctx context.Context, funnelID string, from, to time.Time) (funnels.Report, error)
}

const (
	// funnelDefaultPeriodDays — период отчёта: еженедельный взгляд в одно нажатие.
	funnelDefaultPeriodDays = 7
	// funnelReportTimeout — расчёт на маленькой базе мгновенный; потолок нужен,
	// чтобы зависшее чтение не держало обработчик вечно.
	funnelReportTimeout = 30 * time.Second
)

// funnelTitles — подписи Воронок на экране.
var funnelTitles = map[string]string{
	funnels.FunnelInvite: "Приглашение",
}

// funnelStepLabels — подписи Шагов на экране.
var funnelStepLabels = map[string]string{
	funnels.StepInvitesOpened: "Открыли раздел",
}

func funnelTitle(id string) string {
	if title, ok := funnelTitles[id]; ok {
		return title
	}
	return id
}

func funnelStepLabel(id string) string {
	if label, ok := funnelStepLabels[id]; ok {
		return label
	}
	return id
}

const msgAdminFunnelsList = "<b>📊 Воронки</b>\n\nВыберите воронку:"

// adminFunnelsListKeyboard — inline-список Воронок, по кнопке на Воронку.
func adminFunnelsListKeyboard(list []funnels.Funnel) *tele.ReplyMarkup {
	menu := &tele.ReplyMarkup{}
	rows := make([]tele.Row, 0, len(list))
	for _, fn := range list {
		rows = append(rows, menu.Row(menu.Data(funnelTitle(fn.ID), cbAdminFunnel, fn.ID)))
	}
	menu.Inline(rows...)
	return menu
}

// adminFunnelReportKeyboard — кнопки под отчётом.
func adminFunnelReportKeyboard() *tele.ReplyMarkup {
	menu := &tele.ReplyMarkup{}
	menu.Inline(menu.Row(menu.Data("🔙 К списку воронок", cbAdminFunnelsBack)))
	return menu
}

// handleAdminFunnelsMenu — reply-кнопка «📊 Воронки»: список Воронок.
func (b *Bot) handleAdminFunnelsMenu(c tele.Context) error {
	if !b.isAdmin(c) {
		return nil
	}
	if b.funnels == nil {
		return c.Send("📊 Воронки недоступны: журнал событий не подключён, подробности в логах.")
	}
	return c.Send(msgAdminFunnelsList, &tele.SendOptions{
		ParseMode:   tele.ModeHTML,
		ReplyMarkup: adminFunnelsListKeyboard(b.funnels.List()),
	})
}

// handleAdminFunnel — выбор Воронки: отчёт редактирует сообщение списка.
func (b *Bot) handleAdminFunnel(c tele.Context) error {
	if !b.isAdmin(c) || b.funnels == nil {
		return c.Respond()
	}
	funnelID, ok := callbackArg(c)
	if !ok {
		return c.RespondAlert("Некорректный запрос")
	}

	ctx, cancel := context.WithTimeout(context.Background(), funnelReportTimeout)
	defer cancel()
	to := time.Now().UTC()
	from := to.Add(-funnelDefaultPeriodDays * 24 * time.Hour)
	report, err := b.funnels.Report(ctx, funnelID, from, to)
	if err != nil {
		slog.Error("Failed to compute funnel report", "funnel", funnelID, "error", err)
		return c.RespondAlert("Не удалось посчитать воронку, подробности в логах")
	}

	if err := c.Edit(renderFunnelReport(report, funnelDefaultPeriodDays), &tele.SendOptions{
		ParseMode:   tele.ModeHTML,
		ReplyMarkup: adminFunnelReportKeyboard(),
	}); err != nil {
		slog.Warn("Failed to show funnel report", "funnel", funnelID, "error", err)
	}
	return c.Respond()
}

// handleAdminFunnelsBack — «назад» из отчёта к списку Воронок в том же сообщении.
func (b *Bot) handleAdminFunnelsBack(c tele.Context) error {
	if !b.isAdmin(c) || b.funnels == nil {
		return c.Respond()
	}
	if err := c.Edit(msgAdminFunnelsList, &tele.SendOptions{
		ParseMode:   tele.ModeHTML,
		ReplyMarkup: adminFunnelsListKeyboard(b.funnels.List()),
	}); err != nil {
		slog.Warn("Failed to show funnels list", "error", err)
	}
	return c.Respond()
}

// renderFunnelReport — отчёт для телефона, без таблиц: строка на Шаг вида
// «Создали — 5 (42% · 42%)», где проценты — к предыдущему и к первому Шагу.
func renderFunnelReport(report funnels.Report, periodDays int) string {
	var sb strings.Builder
	fmt.Fprintf(&sb, "<b>📊 Воронка «%s»</b>\n", html.EscapeString(funnelTitle(report.FunnelID)))
	fmt.Fprintf(&sb, "Последние %d дней, без владельца\n\n", periodDays)

	for index, step := range report.Steps {
		fmt.Fprintf(&sb, "%s — ", html.EscapeString(funnelStepLabel(step.ID)))
		switch {
		case step.NoData:
			sb.WriteString("нет данных")
		case index == 0:
			fmt.Fprintf(&sb, "%d", step.People)
		default:
			fmt.Fprintf(&sb, "%d (%d%% · %d%%)", step.People, percent(step.FromPrevious), percent(step.FromFirst))
		}
		sb.WriteString("\n")
	}
	return sb.String()
}

func percent(ratio float64) int {
	return int(math.Round(ratio * 100))
}
