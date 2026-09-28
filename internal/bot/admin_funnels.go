package bot

import (
	"context"
	"fmt"
	"html"
	"log/slog"
	"math"
	"slices"
	"strconv"
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
	// funnelDefaultPeriodDays — период отчёта по умолчанию: еженедельный взгляд
	// в одно нажатие.
	funnelDefaultPeriodDays = 7
	// funnelReportTimeout — расчёт на маленькой базе мгновенный; потолок нужен,
	// чтобы зависшее чтение не держало обработчик вечно.
	funnelReportTimeout = 30 * time.Second
)

// funnelPeriodsDays — периоды, между которыми переключается отчёт. Период из
// кнопки принимается только из этого списка: данные кнопки может подделать клиент.
var funnelPeriodsDays = []int{7, 30, 90}

// funnelTitles — подписи Воронок на экране.
var funnelTitles = map[string]string{
	funnels.FunnelInvite:     "Приглашение",
	funnels.FunnelPayment:    "Оплата",
	funnels.FunnelOnboarding: "Онбординг",
	funnels.FunnelNonRenewal: "Непродлившие",
}

// funnelStepLabels — подписи Шагов на экране.
var funnelStepLabels = map[string]string{
	funnels.StepInvitesOpened:    "Открыли раздел",
	funnels.StepInviteCreated:    "Создали",
	funnels.StepInviteSent:       "Отправили",
	funnels.StepFriendRegistered: "Друг зарегистрировался",
	funnels.StepFriendPaid:       "Друг оплатил",

	funnels.StepPaymentEntered:      "Открыли оплату",
	funnels.StepPaymentMethodChosen: "Выбрали способ",
	funnels.StepPaymentCreated:      "Платёж создан",
	funnels.StepPaymentConfirmed:    "Оплатили",

	funnels.StepRegistered:      "Зарегистрировались",
	funnels.StepDeviceConnected: "Подключили устройство",

	funnels.StepReminded: "Получили напоминание",
}

// funnelNoDataHints — подсказка владельцу, почему у Шага «нет данных», если
// причина известна заранее.
var funnelNoDataHints = map[string]string{
	funnels.StepInviteSent: "«Отправили» считается по выбору результата «Поделиться»: " +
		"включите у @BotFather /setinlinefeedback (100%).",
	funnels.StepDeviceConnected: "Панель недоступна или не ответила: «Подключили устройство» не посчитан, " +
		"следующий Шаг считается от регистрации.",
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

// adminFunnelReportKeyboard — кнопки под отчётом: периоды (выбранный помечен)
// и «назад» к списку. Период едет вторым аргументом той же кнопки Воронки.
func adminFunnelReportKeyboard(funnelID string, periodDays int) *tele.ReplyMarkup {
	menu := &tele.ReplyMarkup{}
	periods := make([]tele.Btn, 0, len(funnelPeriodsDays))
	for _, days := range funnelPeriodsDays {
		label := fmt.Sprintf("%d дн", days)
		if days == periodDays {
			label = "• " + label
		}
		periods = append(periods, menu.Data(label, cbAdminFunnel, funnelID, strconv.Itoa(days)))
	}
	menu.Inline(
		menu.Row(periods...),
		menu.Row(menu.Data("🔙 К списку воронок", cbAdminFunnelsBack)),
	)
	return menu
}

// funnelPeriodArg — период отчёта из второго аргумента кнопки; нет его или он
// вне списка — период по умолчанию.
func funnelPeriodArg(c tele.Context) int {
	args := c.Args()
	if len(args) < 2 {
		return funnelDefaultPeriodDays
	}
	days, err := strconv.Atoi(strings.TrimSpace(args[1]))
	if err != nil || !slices.Contains(funnelPeriodsDays, days) {
		return funnelDefaultPeriodDays
	}
	return days
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

// handleAdminFunnel — выбор Воронки или периода: отчёт редактирует то же сообщение.
func (b *Bot) handleAdminFunnel(c tele.Context) error {
	if !b.isAdmin(c) || b.funnels == nil {
		return c.Respond()
	}
	funnelID, ok := callbackArg(c)
	if !ok {
		return c.RespondAlert("Некорректный запрос")
	}

	periodDays := funnelPeriodArg(c)

	ctx, cancel := context.WithTimeout(context.Background(), funnelReportTimeout)
	defer cancel()
	to := time.Now().UTC()
	from := to.Add(-time.Duration(periodDays) * 24 * time.Hour)
	report, err := b.funnels.Report(ctx, funnelID, from, to)
	if err != nil {
		slog.Error("Failed to compute funnel report", "funnel", funnelID, "error", err)
		return c.RespondAlert("Не удалось посчитать воронку, подробности в логах")
	}

	if err := c.Edit(renderFunnelReport(report, periodDays), &tele.SendOptions{
		ParseMode:   tele.ModeHTML,
		ReplyMarkup: adminFunnelReportKeyboard(funnelID, periodDays),
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
// «Создали — 5 (42% · 42%)», где проценты — к предыдущему и к первому Шагу,
// под ними — пометки «окно ещё не закрыто» и подсказки к «нет данных».
func renderFunnelReport(report funnels.Report, periodDays int) string {
	var sb strings.Builder
	fmt.Fprintf(&sb, "<b>📊 Воронка «%s»</b>\n", html.EscapeString(funnelTitle(report.FunnelID)))
	fmt.Fprintf(&sb, "Последние %d дней, без владельца\n\n", periodDays)

	var hints []string
	for index, step := range report.Steps {
		fmt.Fprintf(&sb, "%s — ", html.EscapeString(funnelStepLabel(step.ID)))
		switch {
		case step.NoData:
			sb.WriteString("нет данных")
			if hint, ok := funnelNoDataHints[step.ID]; ok {
				hints = append(hints, hint)
			}
		case index == 0:
			fmt.Fprintf(&sb, "%d", step.People)
		case report.Steps[index-1].NoData:
			// Предыдущий Шаг неизвестен, а этот посчитан (Шаг после
			// необязательного): конверсии к неизвестному нет.
			fmt.Fprintf(&sb, "%d (— · %d%%)", step.People, percent(step.FromFirst))
		default:
			fmt.Fprintf(&sb, "%d (%d%% · %d%%)", step.People, percent(step.FromPrevious), percent(step.FromFirst))
		}
		sb.WriteString("\n")
	}

	if report.WindowOpen {
		sb.WriteString("\n⏳ Окно ещё не закрыто: часть вошедших ещё может пройти шаги, конверсия недосчитана.\n")
	}
	for _, hint := range hints {
		fmt.Fprintf(&sb, "\nℹ️ %s\n", html.EscapeString(hint))
	}
	return sb.String()
}

func percent(ratio float64) int {
	return int(math.Round(ratio * 100))
}
