package funnels

import (
	"context"
	"testing"
	"time"

	"github.com/fus1ond/vpn_bot/internal/journal"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// Вход Воронки непродливших — напоминание за 3 дня или за сутки, Системное
// событие от бота. Оба напоминания одному человеку — один вошедший.
// Сообщение об истечении подписки — не вход; владелец не считается. Id написаны
// строками намеренно: они уже лежат в журнале.
func TestNonRenewalFunnel_EnteredByReminder(t *testing.T) {
	now := time.Date(2026, 9, 28, 12, 0, 0, 0, time.UTC)
	at := now.Add(-3 * 24 * time.Hour)
	bot := func(id int64, action string) journal.Event {
		return journal.Event{At: at, TelegramID: id, Action: action, Source: journal.SourceBot}
	}

	f := newFixture(t)
	f.record(t,
		bot(1, "remind_3d"),
		bot(1, "remind_1d"),
		bot(2, "remind_1d"),
		bot(3, "remind_3d"),
		bot(5, "expired_notice"),
		bot(ownerID, "remind_3d"),
		journal.Event{At: now.Add(-8 * 24 * time.Hour), TelegramID: 6, Action: "remind_3d", Source: journal.SourceBot},
	)

	report, err := f.open(t).Report(context.Background(), FunnelNonRenewal, now.Add(-7*24*time.Hour), now)
	require.NoError(t, err)

	require.NotEmpty(t, report.Steps)
	assert.Equal(t, StepReminded, report.Steps[0].ID)
	assert.Equal(t, 3, report.Steps[0].People)
}

// После напоминания — те же Шаги, что у Воронки оплаты: вошёл в оплату любым
// входом, платёж создан, оплатил; окно 14 дней от напоминания. Вход в оплату
// раньше напоминания не засчитывается, автосписание и тестовый платёж — не
// оплата человека.
func TestNonRenewalFunnel_PaymentStepsAfterReminder(t *testing.T) {
	now := time.Date(2026, 9, 28, 12, 0, 0, 0, time.UTC)
	from := now.Add(-30 * 24 * time.Hour)
	reminded := from.Add(24 * time.Hour)
	entered := reminded.Add(2 * time.Hour)
	created := entered.Add(time.Minute)
	paid := created.Add(5 * time.Minute)

	f := newFixture(t)
	for id := int64(1); id <= 8; id++ {
		f.record(t, journal.Event{At: reminded, TelegramID: id, Action: ActionReminder1d, Source: journal.SourceBot})
	}
	f.record(t,
		// 1: получил напоминание и ничего не сделал
		// 2: открыл оплату кнопкой под напоминанием и бросил
		journal.Event{At: entered, TelegramID: 2, Action: ActionPayOpen},
		// 3: открыл оплату из меню, создал платёж, но не оплатил
		journal.Event{At: entered, TelegramID: 3, Action: ActionRenewMenu},
		// 4: прошёл до конца
		journal.Event{At: entered, TelegramID: 4, Action: ActionPayOpen},
		// 5: открывал оплату только до напоминания
		journal.Event{At: reminded.Add(-time.Hour), TelegramID: 5, Action: ActionRenewMenu},
		// 6: продлилось автосписанием, хотя в оплату заходил
		journal.Event{At: entered, TelegramID: 6, Action: ActionPayOpen},
		// 7: оплатил через 15 дней после напоминания — окно закрыто
		journal.Event{At: entered, TelegramID: 7, Action: ActionPayOpen},
		// 8: тестовый платёж владельца кассы — не оплата
		journal.Event{At: entered, TelegramID: 8, Action: ActionPayOpen},
	)
	f.manualPayment(t, 3, "pending", false, true, created, time.Time{})
	f.manualPayment(t, 4, "confirmed", false, true, created, paid)
	f.manualPayment(t, 5, "confirmed", false, true, created, paid)
	f.autorenew(t, 6, f.manualPayment(t, 6, "confirmed", false, true, created, paid))
	f.manualPayment(t, 7, "confirmed", false, true, created, reminded.Add(15*24*time.Hour))
	f.manualPayment(t, 8, "confirmed", true, true, created, paid)

	report, err := f.open(t).Report(context.Background(), FunnelNonRenewal, from, now)
	require.NoError(t, err)

	people := stepPeople(report)
	assert.Equal(t, 8, people[StepReminded])
	assert.Equal(t, 6, people[StepPaymentEntered])
	assert.Equal(t, 3, people[StepPaymentCreated])
	assert.Equal(t, 1, people[StepPaymentConfirmed])
	assert.False(t, report.WindowOpen, "окно 14 дней от напоминания давно истекло")
	for _, step := range report.Steps {
		assert.False(t, step.NoData, step.ID)
	}
}

// Напоминание пришло меньше 14 дней назад — человек ещё может продлиться, и
// отчёт помечен «окно ещё не закрыто».
func TestNonRenewalFunnel_WindowOpenForRecentReminder(t *testing.T) {
	now := time.Date(2026, 9, 28, 12, 0, 0, 0, time.UTC)

	f := newFixture(t)
	f.record(t, journal.Event{At: now.Add(-10 * 24 * time.Hour), TelegramID: 1, Action: ActionReminder3d, Source: journal.SourceBot})

	report, err := f.open(t).Report(context.Background(), FunnelNonRenewal, now.Add(-30*24*time.Hour), now)
	require.NoError(t, err)

	assert.True(t, report.WindowOpen)
}
