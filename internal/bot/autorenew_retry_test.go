package bot

import (
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/fus1ond/vpn_bot/internal/database"
)

// Касса не ответила на T−24ч. Следующий проход повторяет то же обращение тем же
// ключом, пока тот жив: до попытки T−0 почти сутки, и ключ к ней протухнет.
func TestAutorenewResendsUnansweredChargeWithinKeyLifetime(t *testing.T) {
	expireAt := time.Now().UTC().Add(6 * time.Hour)
	stub := &arEdgeStub{expireAt: expireAt, transportErr: true}
	b, db, capture := setupAutorenewEdgeBot(t, stub)

	b.runAutorenewCharges(time.Now().UTC())
	first := stub.callCount()
	require.Positive(t, first)
	alerts := len(messagesTo(capture, b.config.AdminID))

	stub.mu.Lock()
	stub.transportErr = false
	stub.responses = []string{edgeSucceededBody("yo-edge-resend")}
	stub.mu.Unlock()

	b.runAutorenewCharges(time.Now().UTC().Add(30 * time.Minute))
	require.Greater(t, stub.callCount(), first, "повтор в том же окне состоялся")
	require.Equal(t, stub.call(0).Key, stub.call(first).Key, "повтор идёт тем же ключом идемпотентности")

	attempts, err := db.ListAutorenewAttempts(arEdgeUserID, expireAt)
	require.NoError(t, err)
	require.Len(t, attempts, 1, "повтор — та же попытка, а не новая")
	require.Equal(t, database.AutorenewOutcomeSuccess, attempts[0].Outcome)
	require.Len(t, messagesTo(capture, b.config.AdminID), alerts, "повтор не добавляет алертов владельцу")
}

// Повторы при лежащей кассе не шлют владельцу алерт каждые полчаса: о сбое он
// узнал на первом обращении.
func TestAutorenewResendDoesNotRepeatOutageAlert(t *testing.T) {
	expireAt := time.Now().UTC().Add(6 * time.Hour)
	stub := &chargeStub{expireAt: expireAt, kassaErr: true}
	b, _, capture := setupChargeBot(t, stub)

	b.runAutorenewCharges(time.Now().UTC())
	calls := stub.calls.Load()
	b.runAutorenewCharges(time.Now().UTC().Add(30 * time.Minute))

	require.Greater(t, stub.calls.Load(), calls, "повтор в кассу ушёл")
	require.Len(t, capture.matching("ни одна попытка не прошла"), 1)
}

// Выключение ждёт списание, которое уже идёт: иначе человек увидел бы
// «Автопродление выключено», а деньги всё равно ушли бы.
func TestAutorenewDisableWaitsForChargeInProgress(t *testing.T) {
	b, db := setupTestBot(t)
	require.NoError(t, db.SetAutorenewEnabled(chargeUserID, true))

	mu := getPaymentMutex(chargeUserID)
	mu.Lock()
	done := make(chan error, 1)
	go func() { done <- b.disableAutorenew(chargeUserID) }()

	select {
	case <-done:
		mu.Unlock()
		t.Fatal("выключение прошло мимо мьютекса списания")
	case <-time.After(100 * time.Millisecond):
	}
	mu.Unlock()
	require.NoError(t, <-done)

	renewal, err := db.GetAutorenewal(chargeUserID)
	require.NoError(t, err)
	require.False(t, renewal.Enabled)
}

// Сообщение об отказе уходит вне мьютекса платежа: медленный Telegram не должен
// держать вебхук, «Проверить оплату» и новые платежи человека.
func TestAutorenewDeclineNotifiesOutsideMutex(t *testing.T) {
	expireAt := time.Now().UTC().Add(6 * time.Hour)
	stub := &chargeStub{expireAt: expireAt, kassaResponses: []string{canceledBody("yo-mu", "insufficient_funds")}}
	b, db, capture := setupChargeBot(t, stub)

	renewal, err := db.GetAutorenewal(chargeUserID)
	require.NoError(t, err)
	result := b.chargeAutorenewal(renewal, time.Now().UTC())

	require.Empty(t, capture.matching("Не удалось списать"), "под мьютексом ничего не отправляется")
	require.NotNil(t, result.notify)
	result.notify()
	require.Len(t, capture.matching("Не удалось списать"), 1)
}
