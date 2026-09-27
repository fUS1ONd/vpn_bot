package bot

import (
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// mismatchAlertMarker — фрагмент оповещения владельцу о несовпадении ответа кассы.
const mismatchAlertMarker = "не сошёлся с записью"

// Касса говорит «оплачено», но на другую сумму: подписку не выдаём, а владелец
// узнаёт об этом один раз, сколько бы проходов сверки ни увидели несовпадение.
func TestReconcileReportsPaidMismatchOnceAndDoesNotConfirm(t *testing.T) {
	stub := &yooKassaStub{}
	stub.set("succeeded", "")
	stub.setMismatch("100.00", "")
	b, db, capture := newReconcileTestBot(t, stub)
	id := pendingYooKassaPayment(t, db, time.Hour)

	for range 3 {
		b.reconcilePendingPayments(time.Now().UTC())
	}

	assert.Equal(t, "pending", paymentStatus(t, db, id), "по несовпавшему ответу подписка не выдаётся")
	alerts := capture.matching(mismatchAlertMarker)
	require.Len(t, alerts, 1, "одно оповещение на платёж")
	assert.Equal(t, "999", alerts[0].ChatID)
	assert.Contains(t, alerts[0].Text, "100")
	assert.Contains(t, alerts[0].Text, "400")
}

// Сменили магазин — получатель в ответе чужой: это тоже несовпадение, а не молчание.
func TestReconcileReportsPaidForeignRecipientOnce(t *testing.T) {
	stub := &yooKassaStub{}
	stub.set("succeeded", "")
	stub.setMismatch("", "other-shop")
	b, db, capture := newReconcileTestBot(t, stub)
	id := pendingYooKassaPayment(t, db, time.Hour)

	b.reconcilePendingPayments(time.Now().UTC())
	b.reconcilePendingPayments(time.Now().UTC())

	assert.Equal(t, "pending", paymentStatus(t, db, id))
	alerts := capture.matching(mismatchAlertMarker)
	require.Len(t, alerts, 1)
	assert.Contains(t, alerts[0].Text, "other-shop")
}

// За неоплаченным несовпавшим платежом денег нет — тревожить владельца не о чем.
func TestReconcileDoesNotReportUnpaidMismatch(t *testing.T) {
	stub := &yooKassaStub{}
	stub.set("pending", "")
	stub.setMismatch("100.00", "")
	b, db, capture := newReconcileTestBot(t, stub)
	id := pendingYooKassaPayment(t, db, time.Hour)

	b.reconcilePendingPayments(time.Now().UTC())

	assert.Equal(t, "pending", paymentStatus(t, db, id))
	assert.Empty(t, capture.matching(mismatchAlertMarker))
}

// Несовпавший pending, который позже стал «оплачено», своё оповещение получает.
func TestReconcileReportsMismatchOnceItBecomesPaid(t *testing.T) {
	stub := &yooKassaStub{}
	stub.set("pending", "")
	stub.setMismatch("100.00", "")
	b, db, capture := newReconcileTestBot(t, stub)
	pendingYooKassaPayment(t, db, time.Hour)

	b.reconcilePendingPayments(time.Now().UTC())
	require.Empty(t, capture.matching(mismatchAlertMarker))

	stub.set("succeeded", "")
	b.reconcilePendingPayments(time.Now().UTC())

	assert.Len(t, capture.matching(mismatchAlertMarker), 1)
}

// Недоступная касса — это молчание: ждём, через сутки закрываем, владельца не тревожим.
func TestReconcileTreatsUnavailableProviderAsSilence(t *testing.T) {
	stub := &yooKassaStub{code: 500}
	stub.set("succeeded", "")
	b, db, capture := newReconcileTestBot(t, stub)
	fresh := pendingYooKassaPayment(t, db, time.Hour)
	old := pendingYooKassaPayment(t, db, 25*time.Hour)

	b.reconcilePendingPayments(time.Now().UTC())

	assert.Equal(t, "pending", paymentStatus(t, db, fresh))
	assert.Equal(t, "expired", paymentStatus(t, db, old))
	assert.Empty(t, capture.matching(mismatchAlertMarker))
}

// Несовпадение на автосписании: владелец получает одно сообщение с заголовком
// автосписания и подробностями. Платёж остаётся висеть, и сверка увидит то же
// несовпадение — второго сообщения за тот же платёж быть не должно.
func TestAutorenewMismatchIsNotReportedAgainByReconcile(t *testing.T) {
	expireAt := time.Now().UTC().Add(6 * time.Hour)
	stub := &arEdgeStub{expireAt: expireAt, responses: []string{
		`{"id":"yo-edge-mismatch","status":"succeeded","amount":{"value":"400.00","currency":"RUB"},
		  "recipient":{"account_id":"other-shop"}}`,
	}}
	b, _, capture := setupAutorenewEdgeBot(t, stub)

	b.runAutorenewCharges(time.Now().UTC())
	b.reconcilePendingPayments(time.Now().UTC().Add(time.Hour))

	var mismatchAlerts []string
	for _, m := range messagesTo(capture, b.config.AdminID) {
		if strings.Contains(m.Text, "не сошёлся") {
			mismatchAlerts = append(mismatchAlerts, m.Text)
		}
	}
	require.Len(t, mismatchAlerts, 1, "одно сообщение о несовпадении на платёж автосписания")
	alert := mismatchAlerts[0]
	assert.Contains(t, alert, "Автосписание")
	assert.Contains(t, alert, "Сумма: наша 400 ₽, в ответе 400")
	assert.Contains(t, alert, "yo-edge-mismatch")
	assert.Contains(t, alert, "<code>RUB</code>")
	assert.Contains(t, alert, "other-shop")
	assert.Contains(t, alert, "<b>succeeded</b>")
}
