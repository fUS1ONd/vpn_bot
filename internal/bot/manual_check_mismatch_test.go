package bot

import (
	"fmt"
	"net/http"
	"testing"
	"time"

	"github.com/fus1ond/vpn_bot/internal/paymentprovider"
	"github.com/fus1ond/vpn_bot/internal/platega"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	tele "gopkg.in/telebot.v3"
)

// mismatchUserText — смысл текста человеку при несовпадении: проверка не прошла,
// платить повторно не нужно.
const mismatchUserText = "платить повторно не нужно"

// checkPaymentByButton нажимает reply-кнопку «Проверить оплату» и возвращает
// текст, который увидел человек.
func checkPaymentByButton(t *testing.T, env *edgeEnv) string {
	t.Helper()
	ctx := &MockContext{sender: &tele.User{ID: edgeUserID}, message: &tele.Message{}}
	require.NoError(t, env.bot.handleCheckPayment(ctx))
	msg, ok := ctx.sentMsg.(string)
	require.True(t, ok)
	return msg
}

// plategaSaysAmount — Platega отвечает статусом и суммой, которая может не
// совпасть с записью (у записи — 400).
func plategaSaysAmount(status string, amount int) func(int, string) (int, string) {
	return func(_ int, id string) (int, string) {
		return http.StatusOK, fmt.Sprintf(
			`{"id":%q,"status":%q,"paymentDetails":{"amount":%d,"currency":"RUB"},"paymentMethod":"SBPQR","expiresIn":"00:00:00"}`,
			id, status, amount)
	}
}

// Platega говорит «оплачено», но на другую сумму: такой ответ подписку не
// выдаёт, человек узнаёт, что платить второй раз не нужно, владелец — о случае.
func TestManualCheckRejectsPlategaPaidWithOtherAmount(t *testing.T) {
	env := newEdgeEnv(t)
	env.platega.onGet = plategaSaysAmount(platega.StatusConfirmed, 100)
	id := env.pending(t, paymentprovider.Platega, 5*time.Minute)

	first := checkPaymentByButton(t, env)
	second := checkPaymentByButton(t, env)

	assert.Equal(t, "pending", env.status(t, id), "по несовпавшему ответу подписка не выдаётся")
	assert.Zero(t, env.panel.patchCount())
	assert.Contains(t, first, mismatchUserText)
	assert.Contains(t, second, mismatchUserText)
	assert.NotContains(t, first, "включится сама")
	assert.Len(t, env.tg.matching(mismatchAlertMarker), 1, "одно оповещение на платёж, сколько бы раз ни нажали")
}

// «🔄 Я оплатил» на платёжном экране, ЮKassa отвечает «оплачено» в чужой магазин.
func TestManualCheckRejectsYooKassaPaidToForeignRecipient(t *testing.T) {
	env := newEdgeEnv(t)
	env.yoo.onGet = func(_ int, id string) (int, string) {
		return http.StatusOK, yooPaymentBody(id, "succeeded", "", 400, "other-shop", "RUB")
	}
	id := env.pending(t, paymentprovider.YooKassa, 5*time.Minute)
	ctx := &MockContext{sender: &tele.User{ID: edgeUserID}, message: &tele.Message{ID: 55}, callback: &tele.Callback{}}

	require.NoError(t, env.bot.handlePayCheckCallback(ctx))

	assert.Equal(t, "pending", env.status(t, id))
	assert.Zero(t, env.panel.patchCount())
	shown := fmt.Sprint(ctx.editedMsg, ctx.sentMsg)
	assert.Contains(t, shown, mismatchUserText, "экран оплаты сменяется итогом: платить по нему снова не нужно")
	alerts := env.tg.matching(mismatchAlertMarker)
	require.Len(t, alerts, 1)
	assert.Contains(t, alerts[0].Text, "other-shop")
}

// Ответ сошёлся с записью — ручная проверка выдаёт подписку, как и раньше.
func TestManualCheckAcceptsMatchedPlategaPayment(t *testing.T) {
	env := newEdgeEnv(t)
	env.platega.onGet = plategaSaysAmount(platega.StatusConfirmed, 400)
	id := env.pending(t, paymentprovider.Platega, 5*time.Minute)

	msg := checkPaymentByButton(t, env)

	assert.Equal(t, "confirmed", env.status(t, id))
	assert.Contains(t, msg, "Оплата прошла")
	assert.Empty(t, env.tg.matching(mismatchAlertMarker))
}

// Провайдер говорит «не оплачено», хоть ответ и не сошёлся: денег нет — владельца
// не тревожим, а человеку честно говорим, что оплата не поступила.
func TestManualCheckTreatsUnpaidMismatchAsNotPaidYet(t *testing.T) {
	env := newEdgeEnv(t)
	env.platega.onGet = plategaSaysAmount(platega.StatusPending, 100)
	id := env.pending(t, paymentprovider.Platega, 5*time.Minute)

	msg := checkPaymentByButton(t, env)

	assert.Equal(t, "pending", env.status(t, id))
	assert.Contains(t, msg, "Оплата пока не поступила")
	assert.Empty(t, env.tg.matching(mismatchAlertMarker))
}

// Отмену из несовпавшего ответа к записи мы не применяем, поэтому и человеку не
// говорим «платёж отменён»: экран и база расходились бы.
func TestManualCheckDoesNotShowCancellationFromMismatchedAnswer(t *testing.T) {
	env := newEdgeEnv(t)
	env.platega.onGet = plategaSaysAmount(platega.StatusCanceled, 100)
	id := env.pending(t, paymentprovider.Platega, 5*time.Minute)

	msg := checkPaymentByButton(t, env)

	assert.Equal(t, "pending", env.status(t, id))
	assert.Contains(t, msg, "Оплата пока не поступила")
	assert.Empty(t, env.tg.matching(mismatchAlertMarker))
}
