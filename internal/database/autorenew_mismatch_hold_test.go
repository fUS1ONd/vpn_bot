package database

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// holdPayment заводит платёж автосписания цикла cycle и возвращает его id.
func holdPayment(t *testing.T, db *DB, telegramID int64, cycle time.Time) int64 {
	t.Helper()
	id, err := db.CreatePayment(&Payment{
		TelegramID: telegramID, Amount: 400, PaymentMethod: "yookassa", Status: "pending",
		Provider: "yookassa", PeriodMonths: 1,
	})
	require.NoError(t, err)
	require.NoError(t, db.RecordAutorenewAttempt(&AutorenewAttempt{
		TelegramID: telegramID, ExpireAt: cycle, AttemptNo: 1,
		Outcome: AutorenewOutcomeUnknown, PaymentID: &id,
	}))
	return id
}

// Несовпавшее «оплачено» по автосписанию держит цикл до разбора владельцем.
// Признак живёт в базе, а не в памяти: перезапуск бота его не снимает.
func TestAutorenewMismatchHold_ДержитЦиклДоРазбора(t *testing.T) {
	db := setupAutorenewDB(t)
	cycle := time.Date(2026, 9, 23, 10, 0, 0, 0, time.UTC)
	id := holdPayment(t, db, 400, cycle)

	hold, err := db.AutorenewMismatchHold(400, cycle)
	require.NoError(t, err)
	assert.False(t, hold.Active, "пока касса не сказала «оплачено», держать нечего")

	require.NoError(t, db.MarkPaidMismatch(id))

	hold, err = db.AutorenewMismatchHold(400, cycle.Add(300*time.Millisecond))
	require.NoError(t, err)
	assert.True(t, hold.Active)
	assert.Nil(t, hold.ReleasedAt)

	other, err := db.AutorenewMismatchHold(400, cycle.AddDate(0, 1, 0))
	require.NoError(t, err)
	assert.False(t, other.Active, "другой цикл не держится")

	resolved, err := db.ResolveAutorenewMismatches(400)
	require.NoError(t, err)
	assert.Equal(t, int64(1), resolved)

	hold, err = db.AutorenewMismatchHold(400, cycle)
	require.NoError(t, err)
	assert.False(t, hold.Active, "владелец разобрал — держать больше нечего")
	require.NotNil(t, hold.ReleasedAt, "момент разбора нужен, чтобы отсчитать grace от него")

	again, err := db.ResolveAutorenewMismatches(400)
	require.NoError(t, err)
	assert.Zero(t, again, "разобранное повторно не разбирается")
}

// Подтверждённый платёж — тоже разбор: подписка продлена по нему.
func TestAutorenewMismatchHold_ПодтверждённыйПлатёжНеДержит(t *testing.T) {
	db := setupAutorenewDB(t)
	cycle := time.Date(2026, 9, 23, 10, 0, 0, 0, time.UTC)
	id := holdPayment(t, db, 401, cycle)
	require.NoError(t, db.MarkPaidMismatch(id))
	require.NoError(t, db.UpdatePaymentStatus(id, "confirmed"))

	hold, err := db.AutorenewMismatchHold(401, cycle)
	require.NoError(t, err)
	assert.False(t, hold.Active)
}
