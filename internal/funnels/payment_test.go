package funnels

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/fus1ond/vpn_bot/internal/journal"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// Вход в оплату — любое Действие, открывающее экран оплаты: главное меню,
// продление, кнопка под уведомлением, «Продлить вручную» после неудачного
// автосписания и старая reply-клавиатура способов. Повторный вход — один
// человек; открытие другого раздела — не вход. Id написаны строками намеренно:
// они уже лежат в журнале, и переименование константы не должно их менять.
func TestPaymentFunnel_EnteredByAnyEntry(t *testing.T) {
	now := time.Date(2026, 9, 28, 12, 0, 0, 0, time.UTC)
	at := now.Add(-time.Hour)

	f := newFixture(t)
	f.record(t,
		journal.Event{At: at, TelegramID: 1, Action: "pay_menu"},
		journal.Event{At: at.Add(time.Minute), TelegramID: 1, Action: "renew_menu"},
		journal.Event{At: at, TelegramID: 2, Action: "renew_menu"},
		journal.Event{At: at, TelegramID: 3, Action: "pay_open"},
		journal.Event{At: at, TelegramID: 4, Action: "ar_pay"},
		journal.Event{At: at, TelegramID: 5, Action: "pay_yookassa_reply"},
		journal.Event{At: at, TelegramID: 6, Action: "pay_crypto_reply"},
		journal.Event{At: at, TelegramID: 7, Action: ActionInvitesOpen},
		journal.Event{At: at, TelegramID: ownerID, Action: "pay_menu"},
	)

	report, err := f.open(t).Report(context.Background(), FunnelPayment, now.Add(-7*24*time.Hour), now)
	require.NoError(t, err)

	require.NotEmpty(t, report.Steps)
	assert.Equal(t, StepPaymentEntered, report.Steps[0].ID)
	assert.Equal(t, 6, report.Steps[0].People)
}

// «Выбрал способ» — нажатие способа на экране оплаты или повтор создания
// платежа тем же способом после сбоя. Бросивший на выборе способа дальше
// входа не проходит; выбор позже 24 часов от входа не засчитывается.
func TestPaymentFunnel_MethodChosen(t *testing.T) {
	now := time.Date(2026, 9, 28, 12, 0, 0, 0, time.UTC)
	at := now.Add(-3 * 24 * time.Hour)

	f := newFixture(t)
	f.record(t,
		// 1: вошёл и бросил на выборе способа
		journal.Event{At: at, TelegramID: 1, Action: ActionPayMenu},
		// 2: вошёл и выбрал способ
		journal.Event{At: at, TelegramID: 2, Action: ActionRenewMenu},
		journal.Event{At: at.Add(time.Minute), TelegramID: 2, Action: ActionPayMethod, Param: PayMethodYooKassa},
		// 3: повторил создание платежа тем же способом
		journal.Event{At: at, TelegramID: 3, Action: ActionPayOpen},
		journal.Event{At: at.Add(time.Minute), TelegramID: 3, Action: ActionRetryPayment, Param: PayMethodCrypto},
		// 4: выбрал способ через 25 часов — окно 24 часа закрыто
		journal.Event{At: at, TelegramID: 4, Action: ActionPayMenu},
		journal.Event{At: at.Add(25 * time.Hour), TelegramID: 4, Action: ActionPayMethod, Param: PayMethodYooKassa},
	)

	report, err := f.open(t).Report(context.Background(), FunnelPayment, now.Add(-7*24*time.Hour), now)
	require.NoError(t, err)

	people := stepPeople(report)
	assert.Equal(t, 4, people[StepPaymentEntered])
	assert.Equal(t, 2, people[StepPaymentMethodChosen])
}

// manualPayment кладёт ручной платёж так, как его пишет бот: created_at —
// CURRENT_TIMESTAMP (целые секунды), confirmed_at — datetime('now') или NULL,
// provider_payment_id — только если касса выдала платёж.
func (f fixture) manualPayment(t *testing.T, telegramID int64, status string, isTest, issued bool, createdAt, confirmedAt time.Time) int64 {
	t.Helper()
	var providerID, confirmed any
	if issued {
		providerID = fmt.Sprintf("prov-%d-%d", telegramID, createdAt.UnixNano())
	}
	if !confirmedAt.IsZero() {
		confirmed = sqliteNow(confirmedAt)
	}
	res := f.exec(t, `INSERT INTO payments (telegram_id, amount, payment_method, status, provider, provider_payment_id, is_test, created_at, confirmed_at)
		VALUES (?, 400, 'bank_card', ?, 'yookassa', ?, ?, ?, ?)`,
		telegramID, status, providerID, isTest, sqliteNow(createdAt), confirmed)
	id, err := res.LastInsertId()
	require.NoError(t, err)
	return id
}

// «Платёж создан» и «подтверждён» — из таблицы платежей. Создан — касса
// выдала платёж (сорвавшееся создание не считается), подтверждён — деньги
// приняты, включая упавшую активацию. Тестовые платежи и автосписания не
// считаются ни на одном из двух Шагов. Подтверждение позже 24 часов от входа
// не засчитывается.
func TestPaymentFunnel_CreatedAndConfirmed(t *testing.T) {
	now := time.Date(2026, 9, 28, 12, 0, 0, 0, time.UTC)
	at := now.Add(-3 * 24 * time.Hour)
	chosen := at.Add(time.Minute)
	created := chosen.Add(time.Second)
	paid := created.Add(5 * time.Minute)

	f := newFixture(t)
	for id := int64(1); id <= 8; id++ {
		f.record(t,
			journal.Event{At: at, TelegramID: id, Action: ActionPayMenu},
			journal.Event{At: chosen, TelegramID: id, Action: ActionPayMethod, Param: PayMethodYooKassa},
		)
	}
	// 1: платёж создан, но не подтверждён
	f.manualPayment(t, 1, "pending", false, true, created, time.Time{})
	// 2: создан и подтверждён
	f.manualPayment(t, 2, "confirmed", false, true, created, paid)
	// 3: касса не ответила — платёж не создан
	f.manualPayment(t, 3, "pending", false, false, created, time.Time{})
	// 4: тестовый платёж
	f.manualPayment(t, 4, "confirmed", true, true, created, paid)
	// 5: автосписание — не решение человека
	f.autorenew(t, 5, f.manualPayment(t, 5, "confirmed", false, true, created, paid))
	// 6: подтверждён через 25 часов от входа — окно закрыто
	f.manualPayment(t, 6, "confirmed", false, true, created, at.Add(25*time.Hour))
	// 7: деньги приняты, активация упала — это оплата
	f.manualPayment(t, 7, "confirmed_not_activated", false, true, created, paid)
	// 8: платёж отменён
	f.manualPayment(t, 8, "canceled", false, true, created, time.Time{})

	report, err := f.open(t).Report(context.Background(), FunnelPayment, now.Add(-7*24*time.Hour), now)
	require.NoError(t, err)

	people := stepPeople(report)
	assert.Equal(t, 8, people[StepPaymentMethodChosen])
	assert.Equal(t, 5, people[StepPaymentCreated])
	assert.Equal(t, 2, people[StepPaymentConfirmed])
	for _, step := range report.Steps {
		assert.False(t, step.NoData, step.ID)
	}
}

// Выбор способа, по которому бот вернул живой pending того же способа (ссылку
// выдали раньше, человек вернулся заплатить), — тоже «платёж создан», в момент
// выбора: иначе вернувшийся платить выпадал бы из Воронки. Pending другого
// способа, истёкший к выбору или старше суток бот не переиспользует.
func TestPaymentFunnel_ReusedPendingCountsAsCreated(t *testing.T) {
	now := time.Date(2026, 9, 28, 12, 0, 0, 0, time.UTC)
	from := now.Add(-7 * 24 * time.Hour)
	entered := from.Add(time.Hour)
	chosen := entered.Add(time.Minute)
	earlier := from.Add(-2 * time.Hour)

	f := newFixture(t)
	for id := int64(1); id <= 5; id++ {
		f.record(t,
			journal.Event{At: entered, TelegramID: id, Action: ActionPayOpen},
			journal.Event{At: chosen, TelegramID: id, Action: ActionPayMethod, Param: PayMethodYooKassa},
		)
	}
	insert := func(telegramID int64, provider string, createdAt, expiresAt, confirmedAt time.Time) {
		var expires, confirmed any
		if !expiresAt.IsZero() {
			expires = expiresAt.UTC()
		}
		status := "pending"
		if !confirmedAt.IsZero() {
			confirmed, status = sqliteNow(confirmedAt), "confirmed"
		}
		f.exec(t, `INSERT INTO payments (telegram_id, amount, payment_method, status, provider, provider_payment_id, is_test, created_at, expires_at, confirmed_at)
			VALUES (?, 400, 'bank_card', ?, ?, ?, 0, ?, ?, ?)`,
			telegramID, status, provider, fmt.Sprintf("prov-%d", telegramID), sqliteNow(createdAt), expires, confirmed)
	}
	// 1: вернулся к живому pending и оплатил по нему
	insert(1, "yookassa", earlier, chosen.Add(time.Hour), chosen.Add(10*time.Minute))
	// 2: живой pending без срока — ссылка выдана, ещё не оплачен
	insert(2, "yookassa", earlier, time.Time{}, time.Time{})
	// 3: pending другого способа — бот его закрыл бы и создал новый
	insert(3, "platega", earlier, time.Time{}, time.Time{})
	// 4: pending истёк до выбора
	insert(4, "yookassa", earlier, chosen.Add(-time.Minute), time.Time{})
	// 5: pending старше суток бот не переиспользует
	insert(5, "yookassa", chosen.Add(-25*time.Hour), time.Time{}, time.Time{})

	report, err := f.open(t).Report(context.Background(), FunnelPayment, from, now)
	require.NoError(t, err)

	people := stepPeople(report)
	assert.Equal(t, 5, people[StepPaymentMethodChosen])
	assert.Equal(t, 2, people[StepPaymentCreated])
	assert.Equal(t, 1, people[StepPaymentConfirmed])
}

// Бот пишет created_at платежа целыми секундами, а время Действия — с долями:
// платёж, созданный в ту же секунду, что и выбор способа, не «раньше» выбора.
func TestPaymentFunnel_CreatedInSameSecondAsChoice(t *testing.T) {
	now := time.Date(2026, 9, 28, 12, 0, 0, 0, time.UTC)
	at := time.Date(2026, 9, 26, 10, 0, 0, 0, time.UTC)
	chosen := at.Add(time.Minute + 700*time.Millisecond)

	f := newFixture(t)
	f.record(t,
		journal.Event{At: at, TelegramID: 1, Action: ActionPayMenu},
		journal.Event{At: chosen, TelegramID: 1, Action: ActionPayMethod, Param: PayMethodCrypto},
	)
	// создан через 0.2 с после выбора: CURRENT_TIMESTAMP срежет доли до 10:01:00
	f.manualPayment(t, 1, "pending", false, true, chosen.Add(200*time.Millisecond), time.Time{})

	report, err := f.open(t).Report(context.Background(), FunnelPayment, now.Add(-7*24*time.Hour), now)
	require.NoError(t, err)

	assert.Equal(t, 1, stepPeople(report)[StepPaymentCreated])
}
