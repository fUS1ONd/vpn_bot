package bot

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/fus1ond/vpn_bot/internal/database"
	"github.com/fus1ond/vpn_bot/internal/remnawave"
	"github.com/fus1ond/vpn_bot/internal/yookassa"
)

const chargeUserID = int64(7001)

// chargeStub подменяет и панель, и кассу: списание ходит в обе.
type chargeStub struct {
	// kassaResponses — тела ответов кассы по порядку обращений.
	kassaResponses []string
	kassaErr       bool
	calls          atomic.Int32
	expireAt       time.Time
	status         string
	enabled        atomic.Bool
}

func setupChargeBot(t *testing.T, stub *chargeStub) (*Bot, *database.DB, *telegramCapture) {
	t.Helper()
	b, db := setupTestBot(t)
	b.config.AutorenewEnabled = true
	b.config.YooKassaShopID = "shop-1"
	b.config.YooKassaSecretKey = "secret"

	_, err := db.CreateUser(chargeUserID, "u", "U", strPtrTest("uuid-charge"), nil, intPtrTest(400), nil)
	require.NoError(t, err)
	require.NoError(t, db.SaveAutorenewMethod(chargeUserID, "pm-1", "•••• 4242"))
	require.NoError(t, db.SetAutorenewEnabled(chargeUserID, true))

	if stub.status == "" {
		stub.status = remnawave.StatusActive
	}

	panel := newTestPanelClient()
	panel.SetHTTPClient(&http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
		body, err := json.Marshal(map[string]any{"response": map[string]any{
			"uuid":            "uuid-charge",
			"shortUuid":       "short",
			"username":        "u",
			"status":          stub.status,
			"expireAt":        stub.expireAt.Format(time.RFC3339Nano),
			"subscriptionUrl": "https://sub.example.com/short",
		}})
		require.NoError(t, err)
		return &http.Response{StatusCode: http.StatusOK, Header: make(http.Header), Body: io.NopCloser(strings.NewReader(string(body)))}, nil
	})})
	b.remnawave = panel

	kassa := yookassa.NewClientWithBaseURL("shop-1", "secret", "https://yookassa.test")
	kassa.SetRetryBackoff(0)
	kassa.SetHTTPClient(&http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
		if stub.kassaErr {
			stub.calls.Add(1)
			return nil, errors.New("TLS handshake timeout")
		}
		idx := int(stub.calls.Add(1)) - 1
		if idx >= len(stub.kassaResponses) {
			idx = len(stub.kassaResponses) - 1
		}
		return &http.Response{StatusCode: http.StatusOK, Header: make(http.Header),
			Body: io.NopCloser(strings.NewReader(stub.kassaResponses[idx]))}, nil
	})})
	b.yookassa = kassa

	capture := captureTelegram(t, b)
	return b, db, capture
}

func succeededBody(id string, amount string) string {
	return `{"id":"` + id + `","status":"succeeded","amount":{"value":"` + amount + `","currency":"RUB"},
		"recipient":{"account_id":"shop-1"},"payment_method":{"type":"bank_card","id":"pm-1","saved":true,"card":{"last4":"4242"}}}`
}

func canceledBody(id, reason string) string {
	return `{"id":"` + id + `","status":"canceled","amount":{"value":"400.00","currency":"RUB"},
		"recipient":{"account_id":"shop-1"},"cancellation_details":{"party":"payment_network","reason":"` + reason + `"}}`
}

// Успех в окне T−24ч: подписка продлена, попытка записана, текст отличается от
// штатного «Оплата прошла!».
func TestAutorenewChargeSucceedsAtT24(t *testing.T) {
	expireAt := time.Now().UTC().Add(6 * time.Hour)
	stub := &chargeStub{expireAt: expireAt, kassaResponses: []string{succeededBody("yo-1", "400.00")}}
	b, db, capture := setupChargeBot(t, stub)

	b.runAutorenewCharges(time.Now().UTC())

	attempts, err := db.ListAutorenewAttempts(chargeUserID, expireAt)
	require.NoError(t, err)
	require.Len(t, attempts, 1)
	require.Equal(t, 1, attempts[0].AttemptNo)
	require.Equal(t, database.AutorenewOutcomeSuccess, attempts[0].Outcome)

	msgs := capture.matching("продлена автоматически")
	require.Len(t, msgs, 1)
	require.NotContains(t, msgs[0].Text, "Оплата прошла")
}

// Провал в T−24ч: попытка израсходована, пользователь предупреждён, Способ жив.
func TestAutorenewChargeDeclinedAtT24(t *testing.T) {
	expireAt := time.Now().UTC().Add(6 * time.Hour)
	stub := &chargeStub{expireAt: expireAt, kassaResponses: []string{canceledBody("yo-2", "insufficient_funds")}}
	b, db, capture := setupChargeBot(t, stub)

	b.runAutorenewCharges(time.Now().UTC())

	attempts, err := db.ListAutorenewAttempts(chargeUserID, expireAt)
	require.NoError(t, err)
	require.Len(t, attempts, 1)
	require.Equal(t, database.AutorenewOutcomeDeclined, attempts[0].Outcome)

	renewal, err := db.GetAutorenewal(chargeUserID)
	require.NoError(t, err)
	require.True(t, renewal.HasMethod(), "обычный отказ карты Способ не гасит")
	require.True(t, renewal.Enabled)

	require.Len(t, capture.matching("Не удалось списать"), 1)
}

// Вторая попытка в T−0 не пишет пользователю: он вчера уже всё прочитал.
func TestAutorenewSecondAttemptStaysSilent(t *testing.T) {
	expireAt := time.Now().UTC().Add(-time.Minute)
	stub := &chargeStub{expireAt: expireAt, kassaResponses: []string{canceledBody("yo-3", "insufficient_funds")}}
	b, db, capture := setupChargeBot(t, stub)

	b.runAutorenewCharges(time.Now().UTC())

	attempts, err := db.ListAutorenewAttempts(chargeUserID, expireAt)
	require.NoError(t, err)
	require.Len(t, attempts, 1)
	require.Equal(t, autorenewAttemptCount, attempts[0].AttemptNo)
	require.Empty(t, capture.matching("Не удалось списать"))
}

// Обе попытки цикла расходуются по одному разу: повторный проход не списывает.
func TestAutorenewAttemptsAreSpentOnce(t *testing.T) {
	expireAt := time.Now().UTC().Add(6 * time.Hour)
	stub := &chargeStub{expireAt: expireAt, kassaResponses: []string{canceledBody("yo-4", "insufficient_funds")}}
	b, db, _ := setupChargeBot(t, stub)

	b.runAutorenewCharges(time.Now().UTC())
	b.runAutorenewCharges(time.Now().UTC())

	attempts, err := db.ListAutorenewAttempts(chargeUserID, expireAt)
	require.NoError(t, err)
	require.Len(t, attempts, 1)
	require.Equal(t, int32(1), stub.calls.Load(), "второй проход в кассу не ходит")
}

// В grace не пробуем: отключённый пользователь уже увидел, что доступа нет.
func TestAutorenewSkipsGrace(t *testing.T) {
	expireAt := time.Now().UTC().Add(-30 * time.Hour)
	stub := &chargeStub{expireAt: expireAt, status: remnawave.StatusDisabled,
		kassaResponses: []string{succeededBody("yo-5", "400.00")}}
	b, db, _ := setupChargeBot(t, stub)

	b.runAutorenewCharges(time.Now().UTC())

	require.Equal(t, int32(0), stub.calls.Load())
	attempts, err := db.ListAutorenewAttempts(chargeUserID, expireAt)
	require.NoError(t, err)
	require.Empty(t, attempts)
}

// В режиме обслуживания не списываем, и попытку не расходуем.
func TestAutorenewSkipsMaintenance(t *testing.T) {
	expireAt := time.Now().UTC().Add(6 * time.Hour)
	stub := &chargeStub{expireAt: expireAt, kassaResponses: []string{succeededBody("yo-6", "400.00")}}
	b, db, _ := setupChargeBot(t, stub)
	b.maintenanceMode.Store(true)

	b.runAutorenewCharges(time.Now().UTC())

	require.Equal(t, int32(0), stub.calls.Load())
	attempts, err := db.ListAutorenewAttempts(chargeUserID, expireAt)
	require.NoError(t, err)
	require.Empty(t, attempts, "попытка не израсходована: это наше решение не идти в кассу")
}

// Пропавший Способ гасит Способ, но не согласие.
func TestAutorenewMethodGoneKeepsConsent(t *testing.T) {
	expireAt := time.Now().UTC().Add(6 * time.Hour)
	stub := &chargeStub{expireAt: expireAt, kassaResponses: []string{canceledBody("yo-7", "permission_revoked")}}
	b, db, _ := setupChargeBot(t, stub)

	b.runAutorenewCharges(time.Now().UTC())

	renewal, err := db.GetAutorenewal(chargeUserID)
	require.NoError(t, err)
	require.False(t, renewal.HasMethod())
	require.True(t, renewal.Enabled, "согласие переживает пропавший Способ")

	attempts, err := db.ListAutorenewAttempts(chargeUserID, expireAt)
	require.NoError(t, err)
	require.Equal(t, database.AutorenewOutcomeMethodGone, attempts[0].Outcome)
}

// pending — исход неизвестен: попытка израсходована, пользователь молчит,
// платёж живёт как обычный незавершённый.
func TestAutorenewPendingIsSpentAndSilent(t *testing.T) {
	expireAt := time.Now().UTC().Add(6 * time.Hour)
	stub := &chargeStub{expireAt: expireAt, kassaResponses: []string{
		`{"id":"yo-8","status":"pending","amount":{"value":"400.00","currency":"RUB"},"recipient":{"account_id":"shop-1"}}`}}
	b, db, capture := setupChargeBot(t, stub)

	b.runAutorenewCharges(time.Now().UTC())

	attempts, err := db.ListAutorenewAttempts(chargeUserID, expireAt)
	require.NoError(t, err)
	require.Len(t, attempts, 1)
	require.Equal(t, database.AutorenewOutcomeUnknown, attempts[0].Outcome)
	require.Empty(t, capture.matching("продлена автоматически"))
	require.Empty(t, capture.matching("Не удалось списать"))
}

// Транспортный сбой: попытка израсходована, пользователь молчит, владелец
// получает один алерт — все попытки прохода не дошли до кассы.
func TestAutorenewTransportFailureAlertsOwner(t *testing.T) {
	expireAt := time.Now().UTC().Add(6 * time.Hour)
	stub := &chargeStub{expireAt: expireAt, kassaErr: true}
	b, db, capture := setupChargeBot(t, stub)

	b.runAutorenewCharges(time.Now().UTC())

	attempts, err := db.ListAutorenewAttempts(chargeUserID, expireAt)
	require.NoError(t, err)
	require.Len(t, attempts, 1)
	require.Equal(t, database.AutorenewOutcomeUnknown, attempts[0].Outcome)
	require.Len(t, capture.matching("ни одна попытка не дошла"), 1)
}

// Обычный отказ карты алерт не поднимает: касса работает и просто отказала.
func TestAutorenewDeclineDoesNotAlertOwner(t *testing.T) {
	expireAt := time.Now().UTC().Add(6 * time.Hour)
	stub := &chargeStub{expireAt: expireAt, kassaResponses: []string{canceledBody("yo-9", "insufficient_funds")}}
	b, _, capture := setupChargeBot(t, stub)

	b.runAutorenewCharges(time.Now().UTC())

	require.Empty(t, capture.matching("ни одна попытка не дошла"))
}

// Ручная оплата между попытками сдвигает expireAt: окно неактуально, в кассу
// не идём.
func TestAutorenewSkipsAfterManualPayment(t *testing.T) {
	stub := &chargeStub{expireAt: time.Now().UTC().Add(20 * 24 * time.Hour),
		kassaResponses: []string{succeededBody("yo-10", "400.00")}}
	b, _, _ := setupChargeBot(t, stub)

	b.runAutorenewCharges(time.Now().UTC())

	require.Equal(t, int32(0), stub.calls.Load())
}

// Выключенное согласие и отсутствующий Способ в шаг не попадают.
func TestAutorenewChargeRequiresConsentAndMethod(t *testing.T) {
	expireAt := time.Now().UTC().Add(6 * time.Hour)
	stub := &chargeStub{expireAt: expireAt, kassaResponses: []string{succeededBody("yo-11", "400.00")}}
	b, db, _ := setupChargeBot(t, stub)

	require.NoError(t, db.SetAutorenewEnabled(chargeUserID, false))
	b.runAutorenewCharges(time.Now().UTC())
	require.Equal(t, int32(0), stub.calls.Load())

	require.NoError(t, db.SetAutorenewEnabled(chargeUserID, true))
	require.NoError(t, db.ClearAutorenewMethod(chargeUserID))
	b.runAutorenewCharges(time.Now().UTC())
	require.Equal(t, int32(0), stub.calls.Load())
}

// Пользователь с включённым автопродлением не получает expire_3d и expire_1d —
// но ровно до тех пор, пока автопродление ещё обещает сработать.
func TestAutorenewSuppressesExpiryNotices(t *testing.T) {
	expireAt := time.Now().UTC().Add(6 * time.Hour)
	stub := &chargeStub{expireAt: expireAt}
	b, db, _ := setupChargeBot(t, stub)

	require.True(t, b.autorenewSuppressesExpiryNotice(chargeUserID, expireAt))

	// Попытка этого цикла провалилась — предупреждение возвращается, иначе
	// человек дойдёт до отключения без единого сообщения.
	require.NoError(t, db.RecordAutorenewAttempt(&database.AutorenewAttempt{
		TelegramID: chargeUserID, ExpireAt: expireAt, AttemptNo: 1,
		Outcome: database.AutorenewOutcomeUnknown,
	}))
	require.False(t, b.autorenewSuppressesExpiryNotice(chargeUserID, expireAt))

	require.NoError(t, db.ClearAutorenewMethod(chargeUserID))
	require.False(t, b.autorenewSuppressesExpiryNotice(chargeUserID, expireAt), "без Способа списания не будет — предупредить надо")
}

// Выросшую цену называем явно.
func TestAutorenewSuccessMentionsPriceGrowth(t *testing.T) {
	expireAt := time.Now().UTC().Add(6 * time.Hour)
	stub := &chargeStub{expireAt: expireAt, kassaResponses: []string{succeededBody("yo-12", "500.00")}}
	b, db, capture := setupChargeBot(t, stub)

	// Прошлое успешное списание на 400 ₽.
	oldID, err := db.CreatePayment(&database.Payment{
		TelegramID: chargeUserID, Amount: 400, PaymentMethod: "yookassa", Status: "confirmed", Provider: "yookassa"})
	require.NoError(t, err)
	require.NoError(t, db.RecordAutorenewAttempt(&database.AutorenewAttempt{
		TelegramID: chargeUserID, ExpireAt: expireAt.AddDate(0, -1, 0), AttemptNo: 1,
		Outcome: database.AutorenewOutcomeSuccess, PaymentID: &oldID}))

	require.NoError(t, db.UpdateSubscriptionPrice(chargeUserID, 500))

	b.runAutorenewCharges(time.Now().UTC())

	msgs := capture.matching("Цена подписки изменилась")
	require.Len(t, msgs, 1)
	require.Contains(t, msgs[0].Text, "400")
}

// Пропавший Способ не оставляет второй попытки: обещать её значит отправить
// человека класть деньги на мёртвую карту.
func TestAutorenewMethodGoneMessageDoesNotPromiseRetry(t *testing.T) {
	expireAt := time.Now().UTC().Add(6 * time.Hour)
	stub := &chargeStub{expireAt: expireAt, kassaResponses: []string{canceledBody("yo-mg", "card_expired")}}
	b, _, capture := setupChargeBot(t, stub)

	b.runAutorenewCharges(time.Now().UTC())

	msgs := capture.matching("Не удалось списать")
	require.Len(t, msgs, 1)
	require.Contains(t, msgs[0].Text, "больше не действует")
	require.NotContains(t, msgs[0].Text, "Следующая попытка")
}

// Обычный отказ карты вторую попытку оставляет — про неё и говорим.
func TestAutorenewDeclineMessagePromisesRetry(t *testing.T) {
	expireAt := time.Now().UTC().Add(6 * time.Hour)
	stub := &chargeStub{expireAt: expireAt, kassaResponses: []string{canceledBody("yo-d", "insufficient_funds")}}
	b, _, capture := setupChargeBot(t, stub)

	b.runAutorenewCharges(time.Now().UTC())

	msgs := capture.matching("Не удалось списать")
	require.Len(t, msgs, 1)
	require.Contains(t, msgs[0].Text, "Следующая попытка")
}

// Отказ кассы по 4xx — это ответ кассы, а не её недоступность: алерт «ни одна
// попытка не дошла» подсказывал бы владельцу неверное.
func TestAutorenewClientErrorIsNotAnOutage(t *testing.T) {
	require.False(t, isYooKassaOutage(errors.New("yookassa API error 400: bad request")))
	require.False(t, isYooKassaOutage(errors.New("yookassa API error 404: not found")))
	require.True(t, isYooKassaOutage(errors.New("send request: TLS handshake timeout")))
	require.True(t, isYooKassaOutage(errors.New("yookassa API error 502: bad gateway")))
}
