package database

import (
	"os"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func setupAutorenewDB(t *testing.T) *DB {
	t.Helper()
	dbFile := "test_autorenewals.db"
	db, err := New(dbFile)
	require.NoError(t, err)
	t.Cleanup(func() {
		db.Close()
		os.Remove(dbFile)
	})
	return db
}

func TestAutorenewalAbsentByDefault(t *testing.T) {
	db := setupAutorenewDB(t)

	a, err := db.GetAutorenewal(100)
	require.NoError(t, err)
	assert.Nil(t, a)
	assert.False(t, a.IsEnabled())
	assert.False(t, a.HasMethod())
}

// Согласие и Способ — две независимые сущности: сохранение Способа не включает
// Автопродление, а гашение Способа не гасит согласие.
func TestAutorenewalConsentAndMethodAreIndependent(t *testing.T) {
	db := setupAutorenewDB(t)

	require.NoError(t, db.SaveAutorenewMethod(100, "pm-1", "•••• 4242"))
	a, err := db.GetAutorenewal(100)
	require.NoError(t, err)
	require.NotNil(t, a)
	assert.False(t, a.Enabled, "сохранение Способа не включает Автопродление")
	assert.True(t, a.HasMethod())
	assert.Equal(t, "•••• 4242", *a.MethodTitle)
	assert.Equal(t, 1, a.PeriodMonths)

	require.NoError(t, db.SetAutorenewEnabled(100, true))
	a, err = db.GetAutorenewal(100)
	require.NoError(t, err)
	assert.True(t, a.Enabled)
	assert.True(t, a.HasMethod(), "включение согласия не трогает Способ")

	require.NoError(t, db.ClearAutorenewMethod(100))
	a, err = db.GetAutorenewal(100)
	require.NoError(t, err)
	assert.True(t, a.Enabled, "пропавший Способ не гасит согласие")
	assert.False(t, a.HasMethod())
}

// Согласие можно дать до появления Способа — строка заводится сама.
func TestAutorenewalConsentWithoutMethod(t *testing.T) {
	db := setupAutorenewDB(t)

	require.NoError(t, db.SetAutorenewEnabled(101, true))
	a, err := db.GetAutorenewal(101)
	require.NoError(t, err)
	require.NotNil(t, a)
	assert.True(t, a.Enabled)
	assert.False(t, a.HasMethod())

	// Списывать по такому согласию нечем — в выборку он не попадает.
	list, err := db.ListEnabledAutorenewals()
	require.NoError(t, err)
	assert.Empty(t, list)
}

func TestListEnabledAutorenewals(t *testing.T) {
	db := setupAutorenewDB(t)

	require.NoError(t, db.SaveAutorenewMethod(200, "pm-200", "•••• 1111"))
	require.NoError(t, db.SetAutorenewEnabled(200, true))
	// Способ есть, согласия нет.
	require.NoError(t, db.SaveAutorenewMethod(201, "pm-201", "СБП"))
	// Согласие есть, Способа нет.
	require.NoError(t, db.SetAutorenewEnabled(202, true))

	list, err := db.ListEnabledAutorenewals()
	require.NoError(t, err)
	require.Len(t, list, 1)
	assert.Equal(t, int64(200), list[0].TelegramID)
}

func TestAutorenewAttemptsBoundToCycle(t *testing.T) {
	db := setupAutorenewDB(t)

	cycle := time.Date(2026, 9, 23, 10, 0, 0, 0, time.UTC)
	has, err := db.HasAutorenewAttempt(300, cycle, 1)
	require.NoError(t, err)
	assert.False(t, has)

	paymentID := int64(77)
	require.NoError(t, db.RecordAutorenewAttempt(&AutorenewAttempt{
		TelegramID: 300, ExpireAt: cycle, AttemptNo: 1,
		Outcome: AutorenewOutcomeDeclined, PaymentID: &paymentID,
	}))

	has, err = db.HasAutorenewAttempt(300, cycle, 1)
	require.NoError(t, err)
	assert.True(t, has)

	// Вторая попытка того же цикла ещё не израсходована.
	has, err = db.HasAutorenewAttempt(300, cycle, 2)
	require.NoError(t, err)
	assert.False(t, has)

	// Сдвинулся expireAt — новый цикл, попытки свежие.
	next := cycle.AddDate(0, 1, 0)
	has, err = db.HasAutorenewAttempt(300, next, 1)
	require.NoError(t, err)
	assert.False(t, has)

	attempts, err := db.ListAutorenewAttempts(300, cycle)
	require.NoError(t, err)
	require.Len(t, attempts, 1)
	assert.Equal(t, AutorenewOutcomeDeclined, attempts[0].Outcome)
	require.NotNil(t, attempts[0].PaymentID)
	assert.Equal(t, int64(77), *attempts[0].PaymentID)
}

// Наносекунды из ответа панели не должны разваливать поиск попытки.
func TestAutorenewAttemptCycleKeyIsStable(t *testing.T) {
	db := setupAutorenewDB(t)

	cycle := time.Date(2026, 9, 23, 10, 0, 0, 0, time.UTC)
	require.NoError(t, db.RecordAutorenewAttempt(&AutorenewAttempt{
		TelegramID: 301, ExpireAt: cycle.Add(431 * time.Millisecond), AttemptNo: 1,
		Outcome: AutorenewOutcomeSuccess,
	}))

	has, err := db.HasAutorenewAttempt(301, cycle, 1)
	require.NoError(t, err)
	assert.True(t, has)

	// Та же попытка в другом часовом поясе — тот же цикл.
	has, err = db.HasAutorenewAttempt(301, cycle.In(time.FixedZone("MSK", 3*3600)), 1)
	require.NoError(t, err)
	assert.True(t, has)
}

// Повторная запись той же попытки дописывает исход, а не заводит вторую строку.
func TestAutorenewAttemptRecordIsIdempotent(t *testing.T) {
	db := setupAutorenewDB(t)

	cycle := time.Date(2026, 9, 23, 10, 0, 0, 0, time.UTC)
	require.NoError(t, db.RecordAutorenewAttempt(&AutorenewAttempt{
		TelegramID: 302, ExpireAt: cycle, AttemptNo: 1, Outcome: AutorenewOutcomeUnknown,
	}))
	paymentID := int64(5)
	require.NoError(t, db.RecordAutorenewAttempt(&AutorenewAttempt{
		TelegramID: 302, ExpireAt: cycle, AttemptNo: 1,
		Outcome: AutorenewOutcomeSuccess, PaymentID: &paymentID,
	}))

	attempts, err := db.ListAutorenewAttempts(302, cycle)
	require.NoError(t, err)
	require.Len(t, attempts, 1)
	assert.Equal(t, AutorenewOutcomeSuccess, attempts[0].Outcome)
	require.NotNil(t, attempts[0].PaymentID)
}

// Автопродление гаснет вместе с пользователем: автокик и chargeback удаляют
// строку из users, а вместе с ней должны исчезнуть согласие, Способ и попытки.
func TestDeleteUserWipesAutorenewal(t *testing.T) {
	db := setupAutorenewDB(t)

	_, err := db.CreateUser(400, "gone", "Gone", nil, nil, nil, nil)
	require.NoError(t, err)
	require.NoError(t, db.SaveAutorenewMethod(400, "pm-400", "•••• 9999"))
	require.NoError(t, db.SetAutorenewEnabled(400, true))
	cycle := time.Date(2026, 9, 23, 10, 0, 0, 0, time.UTC)
	require.NoError(t, db.RecordAutorenewAttempt(&AutorenewAttempt{
		TelegramID: 400, ExpireAt: cycle, AttemptNo: 1, Outcome: AutorenewOutcomeSuccess,
	}))

	require.NoError(t, db.DeleteUser(400))

	a, err := db.GetAutorenewal(400)
	require.NoError(t, err)
	assert.Nil(t, a)
	attempts, err := db.ListAutorenewAttempts(400, cycle)
	require.NoError(t, err)
	assert.Empty(t, attempts)
}

// Попытки живут отдельно от notifications_sent именно затем, чтобы успешная
// активация платежа не стирала их историю.
func TestClearNotificationsKeepsAutorenewState(t *testing.T) {
	db := setupAutorenewDB(t)

	require.NoError(t, db.SaveAutorenewMethod(401, "pm-401", "СБП"))
	require.NoError(t, db.SetAutorenewEnabled(401, true))
	cycle := time.Date(2026, 9, 23, 10, 0, 0, 0, time.UTC)
	require.NoError(t, db.RecordAutorenewAttempt(&AutorenewAttempt{
		TelegramID: 401, ExpireAt: cycle, AttemptNo: 1, Outcome: AutorenewOutcomeDeclined,
	}))
	require.NoError(t, db.MarkNotificationSent(401, "expiring_3d"))

	require.NoError(t, db.ClearNotifications(401))

	a, err := db.GetAutorenewal(401)
	require.NoError(t, err)
	require.NotNil(t, a)
	assert.True(t, a.Enabled)
	assert.True(t, a.HasMethod())
	attempts, err := db.ListAutorenewAttempts(401, cycle)
	require.NoError(t, err)
	assert.Len(t, attempts, 1)
}

// period_months заводится сразу и всегда равен 1.
func TestPaymentPeriodMonthsDefaultsToOne(t *testing.T) {
	db := setupAutorenewDB(t)

	id, err := db.CreatePayment(&Payment{
		TelegramID: 500, Amount: 400, PaymentMethod: "card", Status: "pending", Provider: "yookassa",
	})
	require.NoError(t, err)

	p, err := db.GetPaymentByID(id)
	require.NoError(t, err)
	require.NotNil(t, p)
	assert.Equal(t, 1, p.PeriodMonths)
}
