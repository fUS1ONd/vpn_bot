package database

import (
	"os"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestNotificationsLifecycle(t *testing.T) {
	dbFile := "test_notifications.db"
	db, err := New(dbFile)
	require.NoError(t, err)
	defer func() {
		db.Close()
		os.Remove(dbFile)
	}()

	sent, err := db.WasNotificationSent(123, "expire_3d")
	require.NoError(t, err)
	assert.False(t, sent)

	require.NoError(t, db.MarkNotificationSent(123, "expire_3d"))

	sent, err = db.WasNotificationSent(123, "expire_3d")
	require.NoError(t, err)
	assert.True(t, sent)

	// Повторная запись не должна падать
	require.NoError(t, db.MarkNotificationSent(123, "expire_3d"))

	require.NoError(t, db.ClearNotifications(123))
	sent, err = db.WasNotificationSent(123, "expire_3d")
	require.NoError(t, err)
	assert.False(t, sent)
}

// Владелец — тоже пользователь бота и платит тестовыми платежами. Если его оплата
// стирает маркеры по чекам, уже отправленные сообщения о застрявших чеках зазвучат
// заново, а суточная сводка начнёт отсчёт с нуля.
func TestClearNotificationsKeepsReceiptMarkers(t *testing.T) {
	db, err := New(t.TempDir() + "/notifications.db")
	require.NoError(t, err)
	defer db.Close()

	require.NoError(t, db.MarkNotificationSent(999, "expire_3d"))
	require.NoError(t, db.MarkNotificationSent(999, "receipts_summary"))
	require.NoError(t, db.MarkNotificationSent(999, "receipt_stuck:42"))
	require.NoError(t, db.MarkNotificationSent(999, "receipt_rejected:42"))
	require.NoError(t, db.MarkNotificationSent(999, "receipts_integration_disabled"))

	require.NoError(t, db.ClearNotifications(999))

	for _, notificationType := range []string{"receipts_summary", "receipt_stuck:42", "receipt_rejected:42", "receipts_integration_disabled"} {
		sent, err := db.WasNotificationSent(999, notificationType)
		require.NoError(t, err)
		assert.True(t, sent, "маркер %s должен пережить оплату", notificationType)
	}

	sent, err := db.WasNotificationSent(999, "expire_3d")
	require.NoError(t, err)
	assert.False(t, sent, "маркеры подписки оплата по-прежнему снимает")
}

func TestPaymentsNeedingReceiptSkipsRejected(t *testing.T) {
	db, err := New(t.TempDir() + "/rejected.db")
	require.NoError(t, err)
	defer db.Close()

	id, err := db.CreatePayment(&Payment{TelegramID: 42, Amount: 400, PaymentMethod: "yookassa", Status: "pending", Provider: "yookassa"})
	require.NoError(t, err)
	_, err = db.Conn().Exec(`UPDATE payments SET status = 'confirmed', confirmed_at = CURRENT_TIMESTAMP WHERE id = ?`, id)
	require.NoError(t, err)

	_, err = db.ClaimReceipt(id, "abc123", time.Now().UTC(), 400)
	require.NoError(t, err)
	require.NoError(t, db.MarkReceiptFailed(id, ReceiptStateRejected, "сумма превышает лимит"))

	pending, err := db.PaymentsNeedingReceipt()
	require.NoError(t, err)
	assert.Empty(t, pending, "отвергнутый ФНС чек не берётся в повтор")
}
