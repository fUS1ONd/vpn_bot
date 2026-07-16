package database

import (
	"database/sql"
	"os"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestPaymentProviderMigrationBackfillsLegacyPlategaRows(t *testing.T) {
	path := t.TempDir() + "/legacy.db"
	legacy, err := sql.Open("sqlite3", path)
	require.NoError(t, err)
	_, err = legacy.Exec(`CREATE TABLE payments (id INTEGER PRIMARY KEY AUTOINCREMENT, telegram_id INTEGER NOT NULL, moderator_id INTEGER, amount INTEGER NOT NULL, payment_method TEXT NOT NULL, status TEXT NOT NULL DEFAULT 'pending', platega_transaction_id TEXT UNIQUE, redirect_url TEXT, expires_at TIMESTAMP, created_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP, confirmed_at TIMESTAMP)`)
	require.NoError(t, err)
	_, err = legacy.Exec(`INSERT INTO payments (telegram_id, amount, payment_method, status, platega_transaction_id) VALUES (1, 500, 'crypto', 'pending', 'legacy-tx')`)
	require.NoError(t, err)
	require.NoError(t, legacy.Close())
	db, err := New(path)
	require.NoError(t, err)
	t.Cleanup(func() { db.Close() })
	p, err := db.GetPaymentByPlategaTxID("legacy-tx")
	require.NoError(t, err)
	require.NotNil(t, p)
	require.Equal(t, "platega", p.Provider)
	require.NotNil(t, p.ProviderPaymentID)
	require.Equal(t, "legacy-tx", *p.ProviderPaymentID)
}

func TestCreatePayment(t *testing.T) {
	dbFile := "test_payments.db"
	db, err := New(dbFile)
	require.NoError(t, err)
	defer func() {
		db.Close()
		os.Remove(dbFile)
	}()

	p := &Payment{
		TelegramID:    12345,
		Amount:        500,
		PaymentMethod: "sbp",
		Status:        "pending",
	}

	id, err := db.CreatePayment(p)
	require.NoError(t, err)
	assert.Greater(t, id, int64(0))

	// Получаем созданный платёж
	got, err := db.GetPaymentByID(id)
	require.NoError(t, err)
	require.NotNil(t, got)

	assert.Equal(t, int64(12345), got.TelegramID)
	assert.Equal(t, 500, got.Amount)
	assert.Equal(t, "sbp", got.PaymentMethod)
	assert.Equal(t, "pending", got.Status)
	assert.Nil(t, got.ModeratorID)
	assert.Nil(t, got.PlategaTransactionID)
	assert.Nil(t, got.ConfirmedAt)
}

func TestCreatePaymentPreservesProviderFeeSnapshot(t *testing.T) {
	db, err := New(t.TempDir() + "/payments.db")
	require.NoError(t, err)
	t.Cleanup(func() { db.Close() })
	fee := 350
	id, err := db.CreatePayment(&Payment{TelegramID: 1, Amount: 500, PaymentMethod: "yookassa", Status: "pending", Provider: "yookassa", ProviderFeeBasisPoints: &fee})
	require.NoError(t, err)
	p, err := db.GetPaymentByID(id)
	require.NoError(t, err)
	require.NotNil(t, p.ProviderFeeBasisPoints)
	assert.Equal(t, 350, *p.ProviderFeeBasisPoints)
}

func TestGetPendingPayment(t *testing.T) {
	dbFile := "test_payments_pending.db"
	db, err := New(dbFile)
	require.NoError(t, err)
	defer func() {
		db.Close()
		os.Remove(dbFile)
	}()

	// Нет платежей — возвращает nil
	got, err := db.GetPendingPayment(12345)
	require.NoError(t, err)
	assert.Nil(t, got)

	// Создаём активный PENDING платёж
	future := time.Now().UTC().Add(30 * time.Minute)
	p := &Payment{
		TelegramID:    12345,
		Amount:        500,
		PaymentMethod: "card",
		Status:        "pending",
		ExpiresAt:     &future,
	}
	id, err := db.CreatePayment(p)
	require.NoError(t, err)

	got, err = db.GetPendingPayment(12345)
	require.NoError(t, err)
	require.NotNil(t, got)
	assert.Equal(t, id, got.ID)

	// Протухший PENDING — не возвращается
	past := time.Now().UTC().Add(-1 * time.Minute)
	expired := &Payment{
		TelegramID:    99999,
		Amount:        300,
		PaymentMethod: "sbp",
		Status:        "pending",
		ExpiresAt:     &past,
	}
	_, err = db.CreatePayment(expired)
	require.NoError(t, err)

	got, err = db.GetPendingPayment(99999)
	require.NoError(t, err)
	assert.Nil(t, got)
}

func TestPendingPaymentExpiryHandlesTimezoneAwareExpiresAt(t *testing.T) {
	dbFile := "test_payments_pending_timezone.db"
	db, err := New(dbFile)
	require.NoError(t, err)
	defer func() {
		db.Close()
		os.Remove(dbFile)
	}()

	expiredAtLocal := time.Now().UTC().
		Add(-5 * time.Minute).
		In(time.FixedZone("MSK", 3*60*60)).
		Format("2006-01-02 15:04:05.999999999-07:00")

	res, err := db.Conn().Exec(
		`INSERT INTO payments (telegram_id, amount, payment_method, status, expires_at) VALUES (?, ?, ?, 'pending', ?)`,
		54321, 500, "sbp", expiredAtLocal,
	)
	require.NoError(t, err)

	id, err := res.LastInsertId()
	require.NoError(t, err)

	got, err := db.GetPendingPayment(54321)
	require.NoError(t, err)
	assert.Nil(t, got, "просроченный платёж со смещением timezone не должен возвращаться как активный pending")

	expired, err := db.ExpireOldPendingPayments()
	require.NoError(t, err)
	assert.Equal(t, int64(1), expired, "просроченный платёж со смещением timezone должен протухать")

	stored, err := db.GetPaymentByID(id)
	require.NoError(t, err)
	require.NotNil(t, stored)
	assert.Equal(t, "expired", stored.Status)
}

func TestGetPaymentByPlategaTxID(t *testing.T) {
	dbFile := "test_payments_tx.db"
	db, err := New(dbFile)
	require.NoError(t, err)
	defer func() {
		db.Close()
		os.Remove(dbFile)
	}()

	txID := "platega-tx-abc123"
	p := &Payment{
		TelegramID:           12345,
		Amount:               500,
		PaymentMethod:        "sbp",
		Status:               "pending",
		PlategaTransactionID: &txID,
	}
	id, err := db.CreatePayment(p)
	require.NoError(t, err)

	// Находим по transaction ID
	got, err := db.GetPaymentByPlategaTxID(txID)
	require.NoError(t, err)
	require.NotNil(t, got)
	assert.Equal(t, id, got.ID)
	assert.Equal(t, txID, *got.PlategaTransactionID)

	// Несуществующий — nil
	got, err = db.GetPaymentByPlategaTxID("nonexistent")
	require.NoError(t, err)
	assert.Nil(t, got)
}

func TestConfirmPayment(t *testing.T) {
	dbFile := "test_payments_confirm.db"
	db, err := New(dbFile)
	require.NoError(t, err)
	defer func() {
		db.Close()
		os.Remove(dbFile)
	}()

	p := &Payment{
		TelegramID:    12345,
		Amount:        500,
		PaymentMethod: "sbp",
		Status:        "pending",
	}
	id, err := db.CreatePayment(p)
	require.NoError(t, err)

	// Подтверждаем платёж
	err = db.ConfirmPayment(id)
	require.NoError(t, err)

	got, err := db.GetPaymentByID(id)
	require.NoError(t, err)
	require.NotNil(t, got)
	assert.Equal(t, "confirmed", got.Status)
	assert.NotNil(t, got.ConfirmedAt)
}

func TestConfirmPaymentPreservesExistingConfirmedAt(t *testing.T) {
	dbFile := "test_payments_confirm_preserve.db"
	db, err := New(dbFile)
	require.NoError(t, err)
	defer func() {
		db.Close()
		os.Remove(dbFile)
	}()

	p := &Payment{
		TelegramID:    12345,
		Amount:        500,
		PaymentMethod: "sbp",
		Status:        "pending",
	}
	id, err := db.CreatePayment(p)
	require.NoError(t, err)

	require.NoError(t, db.ConfirmPayment(id))

	original := time.Date(2026, time.March, 1, 12, 0, 0, 0, time.UTC)
	_, err = db.Conn().Exec(`UPDATE payments SET confirmed_at = ? WHERE id = ?`, original, id)
	require.NoError(t, err)

	require.NoError(t, db.UpdatePaymentStatus(id, "confirmed_not_activated"))
	require.NoError(t, db.ConfirmPayment(id))

	got, err := db.GetPaymentByID(id)
	require.NoError(t, err)
	require.NotNil(t, got)
	require.NotNil(t, got.ConfirmedAt)
	assert.Equal(t, "confirmed", got.Status)
	assert.True(t, got.ConfirmedAt.Equal(original), "confirmed_at не должен перезаписываться при retry")
}

func TestGetConfirmedPaymentsByMonth(t *testing.T) {
	dbFile := "test_payments_confirmed_month.db"
	db, err := New(dbFile)
	require.NoError(t, err)
	defer func() {
		db.Close()
		os.Remove(dbFile)
	}()

	targetMonth := time.Date(2026, time.March, 1, 0, 0, 0, 0, time.UTC)
	targetAdminConfirmedAt := targetMonth.Add(9 * 24 * time.Hour)
	targetModeratorConfirmedAt := targetMonth.Add(10 * 24 * time.Hour)
	targetNotActivatedConfirmedAt := targetMonth.Add(11 * 24 * time.Hour)
	targetChargebackedConfirmedAt := targetMonth.Add(12 * 24 * time.Hour)
	previousMonthConfirmedAt := targetMonth.AddDate(0, -1, 0).Add(20 * 24 * time.Hour)

	modID := int64(100)

	adminPaymentID, err := db.CreatePayment(&Payment{
		TelegramID:    200,
		Amount:        1000,
		PaymentMethod: "sbp",
		Status:        "pending",
	})
	require.NoError(t, err)
	require.NoError(t, db.ConfirmPayment(adminPaymentID))
	_, err = db.Conn().Exec(`UPDATE payments SET confirmed_at = ? WHERE id = ?`, targetAdminConfirmedAt, adminPaymentID)
	require.NoError(t, err)

	moderatorPaymentID, err := db.CreatePayment(&Payment{
		TelegramID:    201,
		ModeratorID:   &modID,
		Amount:        500,
		PaymentMethod: "card",
		Status:        "pending",
	})
	require.NoError(t, err)
	require.NoError(t, db.ConfirmPayment(moderatorPaymentID))
	_, err = db.Conn().Exec(`UPDATE payments SET confirmed_at = ? WHERE id = ?`, targetModeratorConfirmedAt, moderatorPaymentID)
	require.NoError(t, err)
	_, err = db.CreateEarning(&ModeratorEarning{
		PaymentID:     moderatorPaymentID,
		ModeratorID:   modID,
		GrossAmount:   500,
		PlategaFee:    60,
		WithdrawalFee: 8,
		NetAmount:     432,
		SharePercent:  15,
		ShareAmount:   66,
	})
	require.NoError(t, err)

	previousMonthPaymentID, err := db.CreatePayment(&Payment{
		TelegramID:    202,
		ModeratorID:   &modID,
		Amount:        700,
		PaymentMethod: "sbp",
		Status:        "pending",
	})
	require.NoError(t, err)
	require.NoError(t, db.ConfirmPayment(previousMonthPaymentID))
	_, err = db.Conn().Exec(`UPDATE payments SET confirmed_at = ? WHERE id = ?`, previousMonthConfirmedAt, previousMonthPaymentID)
	require.NoError(t, err)
	_, err = db.CreateEarning(&ModeratorEarning{
		PaymentID:     previousMonthPaymentID,
		ModeratorID:   modID,
		GrossAmount:   700,
		PlategaFee:    70,
		WithdrawalFee: 12,
		NetAmount:     618,
		SharePercent:  15,
		ShareAmount:   92,
	})
	require.NoError(t, err)

	notActivatedPaymentID, err := db.CreatePayment(&Payment{
		TelegramID:    203,
		ModeratorID:   &modID,
		Amount:        800,
		PaymentMethod: "sbp",
		Status:        "pending",
	})
	require.NoError(t, err)
	require.NoError(t, db.ConfirmPayment(notActivatedPaymentID))
	_, err = db.Conn().Exec(`UPDATE payments SET status = 'confirmed_not_activated', confirmed_at = ? WHERE id = ?`, targetNotActivatedConfirmedAt, notActivatedPaymentID)
	require.NoError(t, err)
	_, err = db.CreateEarning(&ModeratorEarning{
		PaymentID:     notActivatedPaymentID,
		ModeratorID:   modID,
		GrossAmount:   800,
		PlategaFee:    80,
		WithdrawalFee: 14,
		NetAmount:     706,
		SharePercent:  15,
		ShareAmount:   105,
	})
	require.NoError(t, err)

	chargebackedPaymentID, err := db.CreatePayment(&Payment{
		TelegramID:    204,
		Amount:        600,
		PaymentMethod: "sbp",
		Status:        "pending",
	})
	require.NoError(t, err)
	require.NoError(t, db.ConfirmPayment(chargebackedPaymentID))
	_, err = db.Conn().Exec(`UPDATE payments SET status = 'chargebacked', confirmed_at = ? WHERE id = ?`, targetChargebackedConfirmedAt, chargebackedPaymentID)
	require.NoError(t, err)

	payments, err := db.GetConfirmedPaymentsByMonth(2026, 3)
	require.NoError(t, err)
	require.Len(t, payments, 3, "chargebacked платежи не должны попадать в выручку")

	byTelegramID := make(map[int64]MonthlyConfirmedPayment, len(payments))
	for _, payment := range payments {
		byTelegramID[payment.TelegramID] = payment
	}

	adminPayment, ok := byTelegramID[200]
	require.True(t, ok)
	assert.Nil(t, adminPayment.ModeratorID)
	assert.Equal(t, 1000, adminPayment.Amount)
	assert.Equal(t, 0, adminPayment.ShareAmount)

	moderatorPayment, ok := byTelegramID[201]
	require.True(t, ok)
	require.NotNil(t, moderatorPayment.ModeratorID)
	assert.Equal(t, modID, *moderatorPayment.ModeratorID)
	assert.Equal(t, 500, moderatorPayment.Amount)
	assert.Equal(t, 66, moderatorPayment.ShareAmount)

	_, ok = byTelegramID[202]
	assert.False(t, ok, "платёж из предыдущего месяца не должен попадать в выборку")

	notActivatedPayment, ok := byTelegramID[203]
	require.True(t, ok)
	require.NotNil(t, notActivatedPayment.ModeratorID)
	assert.Equal(t, modID, *notActivatedPayment.ModeratorID)
	assert.Equal(t, 800, notActivatedPayment.Amount)
	assert.Equal(t, 105, notActivatedPayment.ShareAmount)

	_, ok = byTelegramID[204]
	assert.False(t, ok, "chargebacked платёж не должен попадать в выручку")
}

func TestCountFirstPaymentsByMonth_IncludesFinanciallyConfirmedStatuses(t *testing.T) {
	dbFile := "test_payments_first_payments_month.db"
	db, err := New(dbFile)
	require.NoError(t, err)
	defer func() {
		db.Close()
		os.Remove(dbFile)
	}()

	firstConfirmedID, err := db.CreatePayment(&Payment{
		TelegramID:    301,
		Amount:        500,
		PaymentMethod: "sbp",
		Status:        "pending",
	})
	require.NoError(t, err)
	require.NoError(t, db.ConfirmPayment(firstConfirmedID))
	_, err = db.Conn().Exec(`UPDATE payments SET confirmed_at = ? WHERE id = ?`, time.Date(2026, time.March, 5, 12, 0, 0, 0, time.UTC), firstConfirmedID)
	require.NoError(t, err)

	firstNotActivatedID, err := db.CreatePayment(&Payment{
		TelegramID:    302,
		Amount:        500,
		PaymentMethod: "sbp",
		Status:        "pending",
	})
	require.NoError(t, err)
	require.NoError(t, db.ConfirmPayment(firstNotActivatedID))
	_, err = db.Conn().Exec(`UPDATE payments SET status = 'confirmed_not_activated', confirmed_at = ? WHERE id = ?`, time.Date(2026, time.March, 6, 12, 0, 0, 0, time.UTC), firstNotActivatedID)
	require.NoError(t, err)

	chargebackedID, err := db.CreatePayment(&Payment{
		TelegramID:    303,
		Amount:        500,
		PaymentMethod: "sbp",
		Status:        "pending",
	})
	require.NoError(t, err)
	require.NoError(t, db.ConfirmPayment(chargebackedID))
	_, err = db.Conn().Exec(`UPDATE payments SET status = 'chargebacked', confirmed_at = ? WHERE id = ?`, time.Date(2026, time.March, 7, 12, 0, 0, 0, time.UTC), chargebackedID)
	require.NoError(t, err)

	previousMonthID, err := db.CreatePayment(&Payment{
		TelegramID:    304,
		Amount:        500,
		PaymentMethod: "sbp",
		Status:        "pending",
	})
	require.NoError(t, err)
	require.NoError(t, db.ConfirmPayment(previousMonthID))
	_, err = db.Conn().Exec(`UPDATE payments SET confirmed_at = ? WHERE id = ?`, time.Date(2026, time.February, 20, 12, 0, 0, 0, time.UTC), previousMonthID)
	require.NoError(t, err)

	secondMarchPaymentID, err := db.CreatePayment(&Payment{
		TelegramID:    304,
		Amount:        500,
		PaymentMethod: "sbp",
		Status:        "pending",
	})
	require.NoError(t, err)
	require.NoError(t, db.ConfirmPayment(secondMarchPaymentID))
	_, err = db.Conn().Exec(`UPDATE payments SET confirmed_at = ? WHERE id = ?`, time.Date(2026, time.March, 8, 12, 0, 0, 0, time.UTC), secondMarchPaymentID)
	require.NoError(t, err)

	count, err := db.CountFirstPaymentsByMonth(2026, 3)
	require.NoError(t, err)
	assert.Equal(t, 2, count, "chargebacked платежи не должны считаться как первые оплаты")
}

func TestCountTrialsByMonthKeepsHistoricalTrialAfterSwitchToUnlimited(t *testing.T) {
	dbFile := "test_payments_trials_history.db"
	db, err := New(dbFile)
	require.NoError(t, err)
	defer func() {
		db.Close()
		os.Remove(dbFile)
	}()

	code, err := db.CreateInviteWithPrice(100, 30, 500)
	require.NoError(t, err)
	require.NoError(t, db.ClaimInvite(code, 555))

	usedAt := time.Date(2026, time.March, 5, 12, 0, 0, 0, time.UTC)
	_, err = db.Conn().Exec(`UPDATE invites SET used_at = ? WHERE code = ?`, usedAt, code)
	require.NoError(t, err)

	count, err := db.CountTrialsByMonth(2026, 3)
	require.NoError(t, err)
	assert.Equal(t, 1, count)

	require.NoError(t, db.UpdateInviteExpireDays(555, nil))

	count, err = db.CountTrialsByMonth(2026, 3)
	require.NoError(t, err)
	assert.Equal(t, 1, count, "исторический trial не должен исчезать из статистики после перевода на бессрочный тариф")
}

func TestUpdatePaymentStatusIfNot(t *testing.T) {
	dbFile := "test_payments_status_if_not.db"
	db, err := New(dbFile)
	require.NoError(t, err)
	defer func() {
		db.Close()
		os.Remove(dbFile)
	}()

	// Создаём платёж со статусом "confirmed"
	p := &Payment{
		TelegramID:    12345,
		Amount:        500,
		PaymentMethod: "sbp",
		Status:        "pending",
	}
	id, err := db.CreatePayment(p)
	require.NoError(t, err)
	require.NoError(t, db.ConfirmPayment(id))

	// Первый вызов: статус не "chargebacked" → должен обновить
	updated, err := db.UpdatePaymentStatusIfNot(id, "chargebacked", "chargebacked")
	require.NoError(t, err)
	assert.True(t, updated, "должен обновить, т.к. статус был не chargebacked")

	// Второй вызов: статус уже "chargebacked" → не должен обновлять
	updated, err = db.UpdatePaymentStatusIfNot(id, "chargebacked", "chargebacked")
	require.NoError(t, err)
	assert.False(t, updated, "не должен обновлять повторно")
}

func TestExpireOldPendingPayments(t *testing.T) {
	dbFile := "test_payments_expire.db"
	db, err := New(dbFile)
	require.NoError(t, err)
	defer func() {
		db.Close()
		os.Remove(dbFile)
	}()

	// Создаём протухший платёж напрямую
	past := time.Now().UTC().Add(-2 * time.Minute)
	p := &Payment{
		TelegramID:    12345,
		Amount:        500,
		PaymentMethod: "sbp",
		Status:        "pending",
		ExpiresAt:     &past,
	}
	id, err := db.CreatePayment(p)
	require.NoError(t, err)

	n, err := db.ExpireOldPendingPayments()
	require.NoError(t, err)
	assert.Equal(t, int64(1), n)

	got, err := db.GetPaymentByID(id)
	require.NoError(t, err)
	assert.Equal(t, "expired", got.Status)
}

func TestHasConfirmedPayment(t *testing.T) {
	dbFile := "test_payments_has.db"
	db, err := New(dbFile)
	require.NoError(t, err)
	defer func() {
		db.Close()
		os.Remove(dbFile)
	}()

	// Без платежей — false
	has, err := db.HasConfirmedPayment(12345)
	require.NoError(t, err)
	assert.False(t, has)

	// Создаём PENDING — всё ещё false
	p := &Payment{
		TelegramID:    12345,
		Amount:        500,
		PaymentMethod: "sbp",
		Status:        "pending",
	}
	id, err := db.CreatePayment(p)
	require.NoError(t, err)

	has, err = db.HasConfirmedPayment(12345)
	require.NoError(t, err)
	assert.False(t, has)

	// Подтверждаем — true
	require.NoError(t, db.ConfirmPayment(id))
	has, err = db.HasConfirmedPayment(12345)
	require.NoError(t, err)
	assert.True(t, has)
}

func TestHasConfirmedPaymentTreatsConfirmedNotActivatedAsPaid(t *testing.T) {
	dbFile := "test_payments_has_confirmed_not_activated.db"
	db, err := New(dbFile)
	require.NoError(t, err)
	defer func() {
		db.Close()
		os.Remove(dbFile)
	}()

	p := &Payment{
		TelegramID:    12345,
		Amount:        500,
		PaymentMethod: "sbp",
		Status:        "pending",
	}
	id, err := db.CreatePayment(p)
	require.NoError(t, err)

	require.NoError(t, db.ConfirmPayment(id))
	require.NoError(t, db.UpdatePaymentStatus(id, "confirmed_not_activated"))

	has, err := db.HasConfirmedPayment(12345)
	require.NoError(t, err)
	assert.True(t, has, "confirmed_not_activated должен считаться подтверждённой оплатой для защитных проверок")
}

func TestHasConfirmedPaymentSinceTreatsConfirmedNotActivatedAsPaid(t *testing.T) {
	dbFile := "test_payments_has_since_confirmed_not_activated.db"
	db, err := New(dbFile)
	require.NoError(t, err)
	defer func() {
		db.Close()
		os.Remove(dbFile)
	}()

	p := &Payment{
		TelegramID:    12345,
		Amount:        500,
		PaymentMethod: "sbp",
		Status:        "pending",
	}
	id, err := db.CreatePayment(p)
	require.NoError(t, err)

	require.NoError(t, db.ConfirmPayment(id))
	require.NoError(t, db.UpdatePaymentStatus(id, "confirmed_not_activated"))

	since := time.Now().UTC().Add(-1 * time.Hour)
	has, err := db.HasConfirmedPaymentSince(12345, since)
	require.NoError(t, err)
	assert.True(t, has, "confirmed_not_activated должен защищать пользователя в проверках scheduler по времени")
}
