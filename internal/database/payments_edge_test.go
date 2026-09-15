package database

import (
	"database/sql"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// Отсечка в 15 минут — единственный фильтр выборки зависших платежей. Платёж,
// созданный ровно в момент отсечки, не должен проваливаться мимо неё: иначе он
// ждёт следующего прохода, а на границе суток — того самого прохода, на котором
// его уже закроют без вопроса кассе.
func TestОтсечкаВключаетПлатёжСозданныйРовноВЕёМомент(t *testing.T) {
	db, err := New(t.TempDir() + "/cutoff.db")
	require.NoError(t, err)
	t.Cleanup(func() { db.Close() })

	created := time.Now().UTC().Add(-15 * time.Minute).Truncate(time.Second)
	id, err := db.CreatePayment(&Payment{TelegramID: 1, Amount: 400, PaymentMethod: "yookassa", Status: "pending", Provider: "yookassa"})
	require.NoError(t, err)
	_, err = db.Conn().Exec(`UPDATE payments SET created_at = ? WHERE id = ?`, created.Format("2006-01-02 15:04:05"), id)
	require.NoError(t, err)

	ids, err := db.PendingPaymentIDsCreatedBefore(created)
	require.NoError(t, err)
	assert.Equal(t, []int64{id}, ids, "созданный ровно в момент отсечки — уже зависший")

	ids, err = db.PendingPaymentIDsCreatedBefore(created.Add(-time.Second))
	require.NoError(t, err)
	assert.Empty(t, ids, "созданный позже отсечки ещё ждёт уведомления")
}

// Момент отсечки приходит с дробной частью (now минус 15 минут), а created_at в
// базе хранится с точностью до секунды. Отбрасывание дробной части не должно
// прятать платёж от сверки.
func TestДробныеДолиСекундыВОтсечкеНеПрячутЗависшийПлатёж(t *testing.T) {
	db, err := New(t.TempDir() + "/cutoff_fraction.db")
	require.NoError(t, err)
	t.Cleanup(func() { db.Close() })

	created := time.Now().UTC().Add(-20 * time.Minute).Truncate(time.Second)
	id, err := db.CreatePayment(&Payment{TelegramID: 1, Amount: 400, PaymentMethod: "yookassa", Status: "pending", Provider: "yookassa"})
	require.NoError(t, err)
	_, err = db.Conn().Exec(`UPDATE payments SET created_at = ? WHERE id = ?`, created.Format("2006-01-02 15:04:05"), id)
	require.NoError(t, err)

	ids, err := db.PendingPaymentIDsCreatedBefore(created.Add(999 * time.Millisecond))
	require.NoError(t, err)
	assert.Equal(t, []int64{id}, ids)
}

// Колонка момента списания добавляется миграцией на живой базе, а ошибки ALTER
// TABLE игнорируются намеренно. Если миграция не отработает, отвалится не одна
// новая функция, а чтение любого платежа — то есть вебхук, кнопка и сверка разом.
func TestМиграцияДобавляетМоментСписанияНаСуществующейБазе(t *testing.T) {
	path := t.TempDir() + "/legacy_paid_at.db"
	legacy, err := sql.Open("sqlite3", path)
	require.NoError(t, err)
	_, err = legacy.Exec(`CREATE TABLE payments (id INTEGER PRIMARY KEY AUTOINCREMENT, telegram_id INTEGER NOT NULL,
		moderator_id INTEGER, amount INTEGER NOT NULL, payment_method TEXT NOT NULL, status TEXT NOT NULL DEFAULT 'pending',
		platega_transaction_id TEXT UNIQUE, redirect_url TEXT, expires_at TIMESTAMP,
		created_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP, confirmed_at TIMESTAMP)`)
	require.NoError(t, err)
	_, err = legacy.Exec(`INSERT INTO payments (telegram_id, amount, payment_method, status, platega_transaction_id, created_at)
		VALUES (1, 400, 'yookassa', 'pending', 'legacy-pending', datetime('now', '-1 hour'))`)
	require.NoError(t, err)
	require.NoError(t, legacy.Close())

	db, err := New(path)
	require.NoError(t, err)
	t.Cleanup(func() { db.Close() })

	p, err := db.GetPaymentByPlategaTxID("legacy-pending")
	require.NoError(t, err)
	require.NotNil(t, p, "старый платёж читается после миграции")
	assert.Nil(t, p.ProviderPaidAt)

	ids, err := db.PendingPaymentIDsCreatedBefore(time.Now().UTC().Add(-15 * time.Minute))
	require.NoError(t, err)
	assert.Equal(t, []int64{p.ID}, ids, "старый зависший платёж попадает в сверку")

	paidAt := time.Date(2026, 9, 14, 11, 0, 50, 0, time.UTC)
	require.NoError(t, db.SetProviderPaidAt(p.ID, paidAt))
	stored, err := db.GetPaymentByID(p.ID)
	require.NoError(t, err)
	require.NotNil(t, stored.ProviderPaidAt)
	assert.True(t, stored.ProviderPaidAt.Equal(paidAt))
}
