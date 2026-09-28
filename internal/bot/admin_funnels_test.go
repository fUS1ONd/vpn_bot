package bot

import (
	"context"
	"errors"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/fus1ond/vpn_bot/internal/config"
	"github.com/fus1ond/vpn_bot/internal/funnels"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	tele "gopkg.in/telebot.v3"
)

const funnelsAdminID int64 = 999999

// fakeFunnels — модуль воронок с заранее посчитанным отчётом.
type fakeFunnels struct {
	report     funnels.Report
	err        error
	askedID    string
	askedFrom  time.Time
	askedTo    time.Time
	reportCall int
}

func (f *fakeFunnels) List() []funnels.Funnel {
	return []funnels.Funnel{{ID: funnels.FunnelInvite, Steps: []string{funnels.StepInvitesOpened}}}
}

func (f *fakeFunnels) Report(_ context.Context, id string, from, to time.Time) (funnels.Report, error) {
	f.reportCall++
	f.askedID, f.askedFrom, f.askedTo = id, from, to
	return f.report, f.err
}

func funnelsTestBot(reporter funnelReporter) *Bot {
	return &Bot{config: &config.Config{AdminID: funnelsAdminID}, funnels: reporter}
}

func funnelScreenButtons(t *testing.T, opts []any) []tele.InlineButton {
	t.Helper()
	for _, opt := range opts {
		if sendOpts, ok := opt.(*tele.SendOptions); ok && sendOpts.ReplyMarkup != nil {
			var buttons []tele.InlineButton
			for _, row := range sendOpts.ReplyMarkup.InlineKeyboard {
				buttons = append(buttons, row...)
			}
			return buttons
		}
	}
	t.Fatal("сообщение без inline-клавиатуры")
	return nil
}

// Владелец открывает «📊 Воронки» и получает список Воронок кнопками.
func TestAdminFunnelsMenu_ListsFunnels(t *testing.T) {
	b := funnelsTestBot(&fakeFunnels{})
	ctx := &MockContext{sender: &tele.User{ID: funnelsAdminID}, message: &tele.Message{Text: BtnAdminFunnels}}

	require.NoError(t, b.handleAdminFunnelsMenu(ctx))

	buttons := funnelScreenButtons(t, ctx.opts)
	require.Len(t, buttons, 1)
	assert.Equal(t, "Приглашение", buttons[0].Text)
	assert.Equal(t, cbAdminFunnel, buttons[0].Unique)
	assert.Equal(t, funnels.FunnelInvite, buttons[0].Data)
}

// Кнопка «📊 Воронки» доходит до экрана через общий роутинг текста.
func TestAdminFunnelsMenu_RoutedFromReplyButton(t *testing.T) {
	b := funnelsTestBot(&fakeFunnels{})
	b.userStates = newStateMap()
	ctx := &MockContext{sender: &tele.User{ID: funnelsAdminID}, message: &tele.Message{Text: BtnAdminFunnels}}

	require.NoError(t, b.handleTextMessage(ctx))

	buttons := funnelScreenButtons(t, ctx.opts)
	assert.Equal(t, cbAdminFunnel, buttons[0].Unique)
}

// Экран воронок — только для владельца.
func TestAdminFunnelsMenu_NotForUsers(t *testing.T) {
	reporter := &fakeFunnels{}
	b := funnelsTestBot(reporter)
	ctx := &MockContext{sender: &tele.User{ID: 42}, message: &tele.Message{Text: BtnAdminFunnels}}

	require.NoError(t, b.handleAdminFunnelsMenu(ctx))
	assert.Nil(t, ctx.sentMsg)

	cb := &MockContext{sender: &tele.User{ID: 42}, callback: &tele.Callback{}, args: []string{funnels.FunnelInvite}}
	require.NoError(t, b.handleAdminFunnel(cb))
	assert.Nil(t, cb.editedMsg)
	assert.Zero(t, reporter.reportCall)
}

// Выбор Воронки редактирует то же сообщение отчётом за последние 7 дней с
// кнопкой «назад» к списку.
func TestAdminFunnel_ShowsReportInPlace(t *testing.T) {
	reporter := &fakeFunnels{report: funnels.Report{
		FunnelID: funnels.FunnelInvite,
		Steps:    []funnels.StepReport{{ID: funnels.StepInvitesOpened, People: 5, FromPrevious: 1, FromFirst: 1}},
	}}
	b := funnelsTestBot(reporter)
	ctx := &MockContext{sender: &tele.User{ID: funnelsAdminID}, callback: &tele.Callback{}, args: []string{funnels.FunnelInvite}}

	before := time.Now()
	require.NoError(t, b.handleAdminFunnel(ctx))

	assert.Equal(t, funnels.FunnelInvite, reporter.askedID)
	assert.WithinDuration(t, before, reporter.askedTo, time.Second)
	assert.Equal(t, 7*24*time.Hour, reporter.askedTo.Sub(reporter.askedFrom))

	text, ok := ctx.editedMsg.(string)
	require.True(t, ok, "отчёт редактирует сообщение списка")
	assert.Contains(t, text, "Приглашение")
	assert.Contains(t, text, "Открыли раздел — 5")
	assert.Nil(t, ctx.sentMsg, "новое сообщение не отправляется")

	buttons := funnelScreenButtons(t, ctx.editedOpts)
	require.Len(t, buttons, 4)
	assert.Equal(t, cbAdminFunnelsBack, buttons[3].Unique)
}

// Под отчётом — кнопки периода 7/30/90: выбранный помечен, каждая пересчитывает
// ту же Воронку за свой период в том же сообщении.
func TestAdminFunnel_PeriodButtons(t *testing.T) {
	reporter := &fakeFunnels{report: funnels.Report{FunnelID: funnels.FunnelInvite}}
	b := funnelsTestBot(reporter)
	ctx := &MockContext{sender: &tele.User{ID: funnelsAdminID}, callback: &tele.Callback{}, args: []string{funnels.FunnelInvite, "30"}}

	require.NoError(t, b.handleAdminFunnel(ctx))

	assert.Equal(t, 30*24*time.Hour, reporter.askedTo.Sub(reporter.askedFrom))
	text, ok := ctx.editedMsg.(string)
	require.True(t, ok)
	assert.Contains(t, text, "Последние 30 дней")

	buttons := funnelScreenButtons(t, ctx.editedOpts)
	require.Len(t, buttons, 4)
	for i, days := range []string{"7", "30", "90"} {
		assert.Equal(t, cbAdminFunnel, buttons[i].Unique)
		assert.Equal(t, funnels.FunnelInvite+"|"+days, buttons[i].Data)
		assert.Contains(t, buttons[i].Text, days+" дн")
	}
	assert.Contains(t, buttons[1].Text, "•", "выбранный период помечен")
	assert.NotContains(t, buttons[0].Text, "•")
}

// Период вне списка 7/30/90 (подделанная кнопка) — отчёт за 7 дней, а не за
// произвольный срок.
func TestAdminFunnel_UnknownPeriodFallsBackToDefault(t *testing.T) {
	reporter := &fakeFunnels{report: funnels.Report{FunnelID: funnels.FunnelInvite}}
	b := funnelsTestBot(reporter)
	ctx := &MockContext{sender: &tele.User{ID: funnelsAdminID}, callback: &tele.Callback{}, args: []string{funnels.FunnelInvite, "100000"}}

	require.NoError(t, b.handleAdminFunnel(ctx))

	assert.Equal(t, 7*24*time.Hour, reporter.askedTo.Sub(reporter.askedFrom))
}

// Окно части когорты не истекло — пометка, чтобы недосчитанную конверсию не
// приняли за провал.
func TestRenderFunnelReport_WindowOpen(t *testing.T) {
	report := funnels.Report{FunnelID: funnels.FunnelInvite, Steps: []funnels.StepReport{{ID: "first", People: 3, FromPrevious: 1, FromFirst: 1}}}
	assert.NotContains(t, strings.ToLower(renderFunnelReport(report, 7)), "окно ещё не закрыто")

	report.WindowOpen = true
	assert.Contains(t, strings.ToLower(renderFunnelReport(report, 7)), "окно ещё не закрыто")
}

// «Нет данных» у Шага «отправил» подсказывает владельцу причину: выбор
// inline-результата не приходит без /setinlinefeedback.
func TestRenderFunnelReport_SentNoDataHint(t *testing.T) {
	report := funnels.Report{FunnelID: funnels.FunnelInvite, Steps: []funnels.StepReport{
		{ID: funnels.StepInvitesOpened, People: 3, FromPrevious: 1, FromFirst: 1},
		{ID: funnels.StepInviteSent, NoData: true},
	}}
	text := renderFunnelReport(report, 7)
	assert.Contains(t, text, "Отправили — нет данных")
	assert.Contains(t, text, "/setinlinefeedback")
}

// Следующие Шаги показывают конверсии к предыдущему и к первому; «нет данных»
// — не ноль.
func TestRenderFunnelReport_ConversionsAndNoData(t *testing.T) {
	text := renderFunnelReport(funnels.Report{
		FunnelID: funnels.FunnelInvite,
		Steps: []funnels.StepReport{
			{ID: "first", People: 12, FromPrevious: 1, FromFirst: 1},
			{ID: "second", People: 5, FromPrevious: 5.0 / 12, FromFirst: 5.0 / 12},
			{ID: "third", NoData: true},
		},
	}, 7)

	assert.Contains(t, text, "— 12\n")
	assert.Contains(t, text, "— 5 (42% · 42%)")
	assert.Contains(t, text, "— нет данных")
}

// Панель не ответила: у Шага устройства «нет данных» с подсказкой, а «Оплатили»
// посчитан от регистрации — конверсии к неизвестному Шагу нет, к первому есть.
func TestRenderFunnelReport_StepAfterNoData(t *testing.T) {
	text := renderFunnelReport(funnels.Report{
		FunnelID: funnels.FunnelOnboarding,
		Steps: []funnels.StepReport{
			{ID: funnels.StepRegistered, People: 10, FromPrevious: 1, FromFirst: 1},
			{ID: funnels.StepDeviceConnected, NoData: true},
			{ID: funnels.StepPaymentConfirmed, People: 3, FromFirst: 0.3},
		},
	}, 7)

	assert.Contains(t, text, "Подключили устройство — нет данных")
	assert.Contains(t, text, "Оплатили — 3 (— · 30%)")
	assert.Contains(t, text, "Панель не ответила")
}

// Ошибка расчёта не оставляет владельца с вечной крутилкой.
func TestAdminFunnel_ReportError(t *testing.T) {
	b := funnelsTestBot(&fakeFunnels{err: errors.New("disk")})
	ctx := &MockContext{sender: &tele.User{ID: funnelsAdminID}, callback: &tele.Callback{}, args: []string{funnels.FunnelInvite}}

	require.NoError(t, b.handleAdminFunnel(ctx))
	assert.True(t, ctx.responded)
	assert.NotEmpty(t, ctx.alertText)
}

// «Назад» возвращает список Воронок в то же сообщение.
func TestAdminFunnelsBack_ReturnsList(t *testing.T) {
	b := funnelsTestBot(&fakeFunnels{})
	ctx := &MockContext{sender: &tele.User{ID: funnelsAdminID}, callback: &tele.Callback{}}

	require.NoError(t, b.handleAdminFunnelsBack(ctx))

	buttons := funnelScreenButtons(t, ctx.editedOpts)
	assert.Equal(t, cbAdminFunnel, buttons[0].Unique)
	assert.True(t, ctx.responded)
}

// Без модуля воронок владелец видит объяснение, а не молчание.
func TestAdminFunnelsMenu_WithoutModule(t *testing.T) {
	b := funnelsTestBot(nil)
	ctx := &MockContext{sender: &tele.User{ID: funnelsAdminID}, message: &tele.Message{Text: BtnAdminFunnels}}

	require.NoError(t, b.handleAdminFunnelsMenu(ctx))
	assert.NotNil(t, ctx.sentMsg)
}

// У каждой Воронки и каждого Шага настоящего модуля есть подпись на экране:
// новая Воронка без подписи показала бы владельцу сырой id.
func TestFunnelLabels_CoverModule(t *testing.T) {
	dir := t.TempDir()
	module, err := funnels.New(filepath.Join(dir, "events.db"), filepath.Join(dir, "bot.db"), nil, nil)
	require.NoError(t, err)
	defer module.Close()

	for _, fn := range module.List() {
		assert.Contains(t, funnelTitles, fn.ID, "у Воронки %q нет подписи", fn.ID)
		for _, stepID := range fn.Steps {
			assert.Contains(t, funnelStepLabels, stepID, "у Шага %q нет подписи", stepID)
		}
	}
}
