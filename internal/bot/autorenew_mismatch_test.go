package bot

import (
	"fmt"
	"io"
	"net/http"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// autorenewMismatchUserMarker — фрагмент сообщения человеку о несовпавшем
// автосписании: главное в нём — не платить второй раз.
const autorenewMismatchUserMarker = "повторно не нужно"

// autorenewMismatchBody — ответ кассы по автосписанию с чужим получателем:
// деньги (если статус succeeded) списаны, но ответ не сошёлся с записью.
func autorenewMismatchBody(id, status string) string {
	return `{"id":"` + id + `","status":"` + status + `","amount":{"value":"400.00","currency":"RUB"},
		"recipient":{"account_id":"other-shop"}}`
}

// userMismatchNotices — сообщения человеку о несовпавшем автосписании.
func userMismatchNotices(capture *telegramCapture) []sentMessage {
	var out []sentMessage
	for _, m := range messagesTo(capture, arEdgeUserID) {
		if strings.Contains(m.Text, autorenewMismatchUserMarker) {
			out = append(out, m)
		}
	}
	return out
}

// Деньги списаны без участия человека, а подписка не продлена: он должен
// узнать об этом от нас, иначе заплатит второй раз руками или пойдёт в банк.
// Вебхук и сверка по тому же платежу второго сообщения не дают.
func TestАвтосписание_НесовпадениеПриСписании_ЧеловекПолучаетОдноСообщение(t *testing.T) {
	expireAt := time.Now().UTC().Add(6 * time.Hour)
	stub := &arEdgeStub{expireAt: expireAt, responses: []string{
		autorenewMismatchBody("yo-mm-user-charge", "succeeded"),
	}}
	b, _, capture := setupAutorenewEdgeBot(t, stub)

	b.runAutorenewCharges(time.Now().UTC())
	require.NoError(t, b.HandleYooKassaWebhook("payment.succeeded", "yo-mm-user-charge"))
	b.reconcilePendingPayments(time.Now().UTC().Add(time.Hour))

	notices := userMismatchNotices(capture)
	require.Len(t, notices, 1, "одно сообщение человеку на платёж, с какого бы входа ни пришло")
	assert.Contains(t, notices[0].Text, "400 ₽")
	assert.Contains(t, notices[0].Text, "автопродление")
	assert.NotContains(t, notices[0].Text, "попытка", "второй попытки по названному кассой платежу не будет")
}

// Касса ответила на автосписание pending, а сверка позже увидела «оплачено» с
// несовпадением: человек узнаёт о списании от сверки, и один раз, сколько бы
// проходов ни повторили то же несовпадение.
func TestАвтосписание_ДозрелоДоОплаченоНаСверке_ЧеловекПолучаетОдноСообщение(t *testing.T) {
	expireAt := time.Now().UTC().Add(6 * time.Hour)
	stub := &arEdgeStub{expireAt: expireAt, responses: []string{
		autorenewMismatchBody("yo-mm-user-reconcile", "pending"),
		autorenewMismatchBody("yo-mm-user-reconcile", "succeeded"),
	}}
	b, _, capture := setupAutorenewEdgeBot(t, stub)

	b.runAutorenewCharges(time.Now().UTC())
	require.Empty(t, userMismatchNotices(capture), "за pending денег нет — писать не о чем")

	b.reconcilePendingPayments(time.Now().UTC().Add(time.Hour))
	b.reconcilePendingPayments(time.Now().UTC().Add(2 * time.Hour))

	assert.Len(t, userMismatchNotices(capture), 1)
}

// Несовпавший pending, дозревший до canceled: денег нет — ни сверка, ни вебхук
// человеку про списание не пишут.
func TestАвтосписание_НесовпадениеПриОтмене_ЧеловекуНичегоНеПишем(t *testing.T) {
	expireAt := time.Now().UTC().Add(6 * time.Hour)
	stub := &arEdgeStub{expireAt: expireAt, responses: []string{
		autorenewMismatchBody("yo-mm-user-canceled", "pending"),
		autorenewMismatchBody("yo-mm-user-canceled", "canceled"),
	}}
	b, _, capture := setupAutorenewEdgeBot(t, stub)

	b.runAutorenewCharges(time.Now().UTC())
	b.reconcilePendingPayments(time.Now().UTC().Add(time.Hour))
	require.NoError(t, b.HandleYooKassaWebhook("payment.canceled", "yo-mm-user-canceled"))

	assert.Empty(t, userMismatchNotices(capture))
}

// Telegram не принял сообщение человеку (429 — точно не доставлено): пометка
// снимается, и следующий вход доводит сообщение. Владелец при этом получает своё
// одно сообщение — его дедупликация от недоставки человеку не зависит.
func TestАвтосписание_СообщениеЧеловекуНеДоставлено_СледующийВходПовторяет(t *testing.T) {
	expireAt := time.Now().UTC().Add(6 * time.Hour)
	stub := &arEdgeStub{expireAt: expireAt, responses: []string{
		autorenewMismatchBody("yo-mm-user-retry", "succeeded"),
	}}
	b, _, _ := setupAutorenewEdgeBot(t, stub)
	tg := userTelegram(t, b, true)

	b.runAutorenewCharges(time.Now().UTC())
	require.Empty(t, userMismatchNotices(tg.capture), "предпосылка: первое сообщение человеку отвергнуто")

	b.reconcilePendingPayments(time.Now().UTC().Add(time.Hour))
	b.reconcilePendingPayments(time.Now().UTC().Add(2 * time.Hour))

	assert.Len(t, userMismatchNotices(tg.capture), 1, "следующий вход доводит сообщение, дальше — тишина")
	assert.Len(t, messagesTo(tg.capture, b.config.AdminID), 1, "владелец получил одно сообщение")
}

// Недоставка владельцу не гасит сообщение человеку и не дублирует его.
func TestАвтосписание_АлертВладельцуНеДоставлен_ЧеловекПолучаетОдноСообщение(t *testing.T) {
	expireAt := time.Now().UTC().Add(6 * time.Hour)
	stub := &arEdgeStub{expireAt: expireAt, responses: []string{
		autorenewMismatchBody("yo-mm-owner-retry", "succeeded"),
	}}
	b, _, _ := setupAutorenewEdgeBot(t, stub)
	delivered := failFirstTelegramSend(t, b) // первым уходит алерт владельцу

	b.runAutorenewCharges(time.Now().UTC())
	b.reconcilePendingPayments(time.Now().UTC().Add(time.Hour))

	assert.Len(t, userMismatchNotices(delivered), 1)
	assert.Len(t, delivered.matching(mismatchAlertMarker), 1, "владелец получил своё на следующем входе")
}

// Сообщение человеку уходит вне getPaymentMutex на всех трёх входах: медленный
// Telegram не должен держать платежи человека.
func TestАвтосписание_СообщениеЧеловекуУходитВнеМьютекса(t *testing.T) {
	entries := map[string]func(b *Bot){
		"автосписание": func(b *Bot) { b.runAutorenewCharges(time.Now().UTC()) },
		"сверка": func(b *Bot) {
			b.runAutorenewCharges(time.Now().UTC())
			b.reconcilePendingPayments(time.Now().UTC().Add(time.Hour))
		},
		"вебхук": func(b *Bot) {
			b.runAutorenewCharges(time.Now().UTC())
			require.NoError(t, b.HandleYooKassaWebhook("payment.succeeded", "yo-mm-user-mutex"))
		},
	}
	for name, run := range entries {
		t.Run(name, func(t *testing.T) {
			responses := []string{autorenewMismatchBody("yo-mm-user-mutex", "succeeded")}
			if name != "автосписание" {
				responses = []string{
					autorenewMismatchBody("yo-mm-user-mutex", "pending"),
					autorenewMismatchBody("yo-mm-user-mutex", "succeeded"),
				}
			}
			stub := &arEdgeStub{expireAt: time.Now().UTC().Add(6 * time.Hour), responses: responses}
			b, _, _ := setupAutorenewEdgeBot(t, stub)
			tg := userTelegram(t, b, false)

			run(b)

			require.Len(t, userMismatchNotices(tg.capture), 1)
			assert.False(t, tg.lockedDuringSend.Load(), "сообщение человеку отправлено под мьютексом платежей")
		})
	}
}

// Обычный платёж с несовпавшим «оплачено»: человеку ничего нового не уходит —
// на ручной проверке он уже видит свой текст, а сверка и вебхук молчат.
func TestНесовпадение_ОбычныйПлатёж_ЧеловекуНичегоНовогоНеПишем(t *testing.T) {
	stub := &yooKassaStub{}
	stub.set("succeeded", "")
	stub.setMismatch("", "other-shop")
	b, db, capture := newReconcileTestBot(t, stub)
	pendingYooKassaPayment(t, db, time.Hour)

	b.reconcilePendingPayments(time.Now().UTC())

	require.Len(t, capture.matching(mismatchAlertMarker), 1, "предпосылка: владелец узнал о несовпадении")
	assert.Empty(t, messagesTo(capture, reconcileTestUserID))
}

// userTelegramStub — Telegram, который запоминает доставленные сообщения,
// может отвергнуть первое сообщение человеку и отмечает, была ли отправка
// человеку сделана под мьютексом его платежей.
type userTelegramStub struct {
	capture          *telegramCapture
	lockedDuringSend atomic.Bool
}

func userTelegram(t *testing.T, b *Bot, failFirstToUser bool) *userTelegramStub {
	t.Helper()
	stub := &userTelegramStub{capture: &telegramCapture{}}
	var userSends atomic.Int32
	b.bot = newOfflineTelegramBotForTest(t, roundTripFunc(func(r *http.Request) (*http.Response, error) {
		if !strings.Contains(r.URL.Path, "/sendMessage") {
			return nil, nil
		}
		body, err := io.ReadAll(r.Body)
		require.NoError(t, err)
		msg := parseSentMessage(string(body))
		if msg.ChatID == fmt.Sprint(arEdgeUserID) {
			mu := getPaymentMutex(arEdgeUserID)
			if mu.TryLock() {
				mu.Unlock()
			} else {
				stub.lockedDuringSend.Store(true)
			}
			if userSends.Add(1) == 1 && failFirstToUser {
				return &http.Response{StatusCode: http.StatusTooManyRequests, Header: make(http.Header),
					Body: io.NopCloser(strings.NewReader(
						`{"ok":false,"error_code":429,"description":"Too Many Requests: retry after 1","parameters":{"retry_after":1}}`))}, nil
			}
		}
		stub.capture.mu.Lock()
		stub.capture.messages = append(stub.capture.messages, msg)
		stub.capture.mu.Unlock()
		return &http.Response{StatusCode: http.StatusOK, Header: make(http.Header),
			Body: io.NopCloser(strings.NewReader(
				`{"ok":true,"result":{"message_id":1,"date":1710000000,"chat":{"id":1,"type":"private"},"text":"ok"}}`))}, nil
	}))
	return stub
}

// То же через вебхук ЮKassa: повторная доставка второго сообщения не даёт.
func TestАвтосписание_ДозрелоДоОплаченоНаВебхуке_ЧеловекПолучаетОдноСообщение(t *testing.T) {
	expireAt := time.Now().UTC().Add(6 * time.Hour)
	stub := &arEdgeStub{expireAt: expireAt, responses: []string{
		autorenewMismatchBody("yo-mm-user-webhook", "pending"),
		autorenewMismatchBody("yo-mm-user-webhook", "succeeded"),
	}}
	b, _, capture := setupAutorenewEdgeBot(t, stub)

	b.runAutorenewCharges(time.Now().UTC())
	require.NoError(t, b.HandleYooKassaWebhook("payment.succeeded", "yo-mm-user-webhook"))
	require.NoError(t, b.HandleYooKassaWebhook("payment.succeeded", "yo-mm-user-webhook"))

	assert.Len(t, userMismatchNotices(capture), 1)
}
