package bot

import (
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/fus1ond/vpn_bot/internal/database"
	"github.com/fus1ond/vpn_bot/internal/paymentprovider"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	tele "gopkg.in/telebot.v3"
)

const payScreenMessageID = 55

// payScreenCallback — нажатие inline-кнопки платёжного экрана.
func payScreenCallback(data string) *MockContext {
	return &MockContext{
		sender:   &tele.User{ID: arEdgeUserID},
		message:  &tele.Message{ID: payScreenMessageID},
		callback: &tele.Callback{Data: data},
	}
}

// yooPendingBody — ответ кассы на создание платежа; expiresAt == nil значит,
// что касса срока не назвала.
func yooPendingBody(expiresAt *time.Time) string {
	expires := ""
	if expiresAt != nil {
		expires = fmt.Sprintf(`,"expires_at":%q`, expiresAt.UTC().Format(time.RFC3339))
	}
	return `{"id":"yo-screen","status":"pending","amount":{"value":"400.00","currency":"RUB"},` +
		`"recipient":{"account_id":"shop-1"},"confirmation":{"confirmation_url":"https://yookassa.test/pay/screen"}` +
		expires + `}`
}

func editedMarkup(t *testing.T, ctx *MockContext) *tele.ReplyMarkup {
	t.Helper()
	for _, opt := range ctx.editedOpts {
		if opts, ok := opt.(*tele.SendOptions); ok {
			return opts.ReplyMarkup
		}
	}
	return nil
}

func TestPaymentLinkDeadline(t *testing.T) {
	now := time.Date(2026, 9, 26, 9, 0, 0, 0, time.UTC) // 12:00 МСК
	at := func(d time.Duration) *time.Time { v := now.Add(d); return &v }

	assert.Empty(t, paymentLinkDeadline(nil, now), "срок не назван — не выдумываем")
	assert.Empty(t, paymentLinkDeadline(at(-time.Minute), now), "прошедший срок не называем")
	assert.Equal(t, "12:15 МСК", paymentLinkDeadline(at(15*time.Minute), now))
	assert.Equal(t, "27.09 12:00 МСК", paymentLinkDeadline(at(24*time.Hour), now), "не сегодня — с датой")
}

func TestPayButtonShowsInlineMethodScreen(t *testing.T) {
	stub := &arEdgeStub{expireAt: time.Now().UTC().Add(10 * 24 * time.Hour)}
	b, _, _ := setupAutorenewEdgeBot(t, stub)

	ctx := &MockContext{sender: &tele.User{ID: arEdgeUserID}, message: &tele.Message{Text: BtnRenew}}
	require.NoError(t, b.handlePayButton(ctx))

	require.NotEmpty(t, ctx.opts)
	opts, ok := ctx.opts[0].(*tele.SendOptions)
	require.True(t, ok)
	require.NotNil(t, opts.ReplyMarkup)
	assert.Empty(t, opts.ReplyMarkup.ReplyKeyboard, "reply-клавиатура меню остаётся на месте")
	assert.True(t, keyboardHasCallback(opts.ReplyMarkup, cbPayMethod))
	assert.True(t, keyboardHasCallback(opts.ReplyMarkup, cbPayCancel))
	assert.Equal(t, StateNone, b.userStates.Get(arEdgeUserID))
}

func TestPayMethodEditsScreenIntoWaitStep(t *testing.T) {
	expires := time.Now().UTC().Add(time.Hour)
	stub := &arEdgeStub{expireAt: time.Now().UTC().Add(10 * 24 * time.Hour),
		responses: []string{yooPendingBody(&expires)}}
	b, db, _ := setupAutorenewEdgeBot(t, stub)

	ctx := payScreenCallback(paymentprovider.YooKassa)
	require.NoError(t, b.handlePayMethodCallback(ctx))

	assert.True(t, ctx.responded)
	assert.Empty(t, ctx.sentMsgs, "шаг ожидания — то же сообщение, а не новое")
	msg, ok := ctx.editedMsg.(string)
	require.True(t, ok)
	assert.Contains(t, msg, "400 руб.")
	assert.Contains(t, msg, "Ссылка на оплату действует до "+paymentLinkDeadline(&expires, time.Now().UTC())+" — можно вернуться и доплатить.")
	assert.NotContains(t, msg, "https://yookassa.test", "ссылка живёт в кнопке, а не в тексте")
	assert.Contains(t, msg, "разрешаете сохранить способ оплаты", "согласие на автосписание переносится на экран с кнопкой «Оплатить»")

	markup := editedMarkup(t, ctx)
	require.NotNil(t, markup)
	buttons := inlineButtons(markup)
	require.Len(t, buttons, 3)
	assert.Equal(t, "💳 Оплатить 400 ₽", buttons[0].Text)
	assert.Equal(t, "https://yookassa.test/pay/screen", buttons[0].URL)
	assert.Equal(t, cbPayCheck, buttons[1].Unique)
	assert.Equal(t, cbPayCancel, buttons[2].Unique)

	pending, err := db.GetPendingPayment(arEdgeUserID)
	require.NoError(t, err)
	require.NotNil(t, pending)
	assert.Equal(t, strconv.FormatInt(pending.ID, 10), buttons[2].Data)
	messageID, tracked := b.paymentScreens.take(arEdgeUserID, pending.ID)
	require.True(t, tracked, "экран запоминается, чтобы вебхук снял с него кнопки")
	assert.Equal(t, payScreenMessageID, messageID)
}

func TestPayWaitScreenOmitsUnknownDeadline(t *testing.T) {
	stub := &arEdgeStub{expireAt: time.Now().UTC().Add(10 * 24 * time.Hour),
		responses: []string{yooPendingBody(nil)}}
	b, _, _ := setupAutorenewEdgeBot(t, stub)

	ctx := payScreenCallback(paymentprovider.YooKassa)
	require.NoError(t, b.handlePayMethodCallback(ctx))

	msg, ok := ctx.editedMsg.(string)
	require.True(t, ok)
	assert.NotContains(t, msg, "действует до")
}

func TestPayWaitScreenWithoutAutorenewHasNoConsent(t *testing.T) {
	stub := &arEdgeStub{expireAt: time.Now().UTC().Add(10 * 24 * time.Hour),
		responses: []string{yooPendingBody(nil)}}
	b, _ := autorenewFlagOffBot(t, stub)

	ctx := payScreenCallback(paymentprovider.YooKassa)
	require.NoError(t, b.handlePayMethodCallback(ctx))

	msg, ok := ctx.editedMsg.(string)
	require.True(t, ok)
	assert.NotContains(t, msg, "автосписан")
}

func TestPayMethodRespectsMaintenance(t *testing.T) {
	stub := &arEdgeStub{expireAt: time.Now().UTC().Add(10 * 24 * time.Hour),
		responses: []string{yooPendingBody(nil)}}
	b, db, _ := setupAutorenewEdgeBot(t, stub)
	b.setMaintenanceMode(true)

	ctx := payScreenCallback(paymentprovider.YooKassa)
	require.NoError(t, b.handlePayMethodCallback(ctx))

	assert.Contains(t, ctx.editedMsg, "на обслуживании")
	assert.Nil(t, editedMarkup(t, ctx))
	assert.Zero(t, stub.callCount(), "в режиме обслуживания касса не вызывается")
	pending, err := db.GetPendingPayment(arEdgeUserID)
	require.NoError(t, err)
	assert.Nil(t, pending)
}

func TestPayMethodTooFarAheadExplainsLimit(t *testing.T) {
	stub := &arEdgeStub{expireAt: time.Now().UTC().Add(120 * 24 * time.Hour),
		responses: []string{yooPendingBody(nil)}}
	b, _, _ := setupAutorenewEdgeBot(t, stub)
	panel := newTestPanelClient()
	panel.SetHTTPClient(&http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
		if r.Method == http.MethodGet && r.URL.Path == fmt.Sprintf("/api/users/by-telegram-id/%d", arEdgeUserID) {
			payload := fmt.Sprintf(`{"response":[{"uuid":"uuid-edge","telegramId":%d,"username":"u","status":"ACTIVE","expireAt":%q}]}`,
				arEdgeUserID, stub.expireAt.Format(time.RFC3339))
			return &http.Response{StatusCode: http.StatusOK, Header: make(http.Header),
				Body: io.NopCloser(strings.NewReader(payload))}, nil
		}
		return nil, assert.AnError
	})})
	b.remnawave = panel

	ctx := payScreenCallback(paymentprovider.YooKassa)
	require.NoError(t, b.handlePayMethodCallback(ctx))

	assert.Contains(t, ctx.editedMsg, "не раньше чем за 90 дней")
	assert.Zero(t, stub.callCount())
}

func TestPayMethodRejectsUnknownProvider(t *testing.T) {
	stub := &arEdgeStub{expireAt: time.Now().UTC().Add(10 * 24 * time.Hour)}
	b, _, _ := setupAutorenewEdgeBot(t, stub)

	ctx := payScreenCallback("paypal")
	require.NoError(t, b.handlePayMethodCallback(ctx))

	assert.Equal(t, "Некорректный запрос", ctx.alertText)
	assert.Nil(t, ctx.editedMsg)
}

func createScreenPayment(t *testing.T, db *database.DB, status string, expiresAt *time.Time) int64 {
	t.Helper()
	id, err := db.CreatePayment(&database.Payment{
		TelegramID: arEdgeUserID, Amount: 400, PaymentMethod: paymentprovider.YooKassa,
		Status: status, Provider: paymentprovider.YooKassa, ExpiresAt: expiresAt,
	})
	require.NoError(t, err)
	return id
}

func TestPayCancelKeepsLinkAlive(t *testing.T) {
	stub := &arEdgeStub{expireAt: time.Now().UTC().Add(10 * 24 * time.Hour)}
	b, db, _ := setupAutorenewEdgeBot(t, stub)
	expires := time.Now().UTC().Add(time.Hour)
	id := createScreenPayment(t, db, "pending", &expires)
	b.paymentScreens.set(arEdgeUserID, id, payScreenMessageID)

	ctx := payScreenCallback(strconv.FormatInt(id, 10))
	require.NoError(t, b.handlePayCancelCallback(ctx))

	assert.True(t, ctx.responded)
	assert.Equal(t, "Оплата отложена.\n\nСсылка на оплату действует до "+
		paymentLinkDeadline(&expires, time.Now().UTC())+" — можно вернуться и доплатить из меню.", ctx.editedMsg)
	assert.Nil(t, editedMarkup(t, ctx), "клавиатура убирается")
	stored, err := db.GetPaymentByID(id)
	require.NoError(t, err)
	assert.Equal(t, "pending", stored.Status, "отмена экрана платёж не отменяет")
	_, tracked := b.paymentScreens.take(arEdgeUserID, id)
	assert.False(t, tracked)
}

func TestPayCancelWithoutDeadlineDoesNotInventOne(t *testing.T) {
	stub := &arEdgeStub{expireAt: time.Now().UTC().Add(10 * 24 * time.Hour)}
	b, db, _ := setupAutorenewEdgeBot(t, stub)
	id := createScreenPayment(t, db, "pending", nil)

	ctx := payScreenCallback(strconv.FormatInt(id, 10))
	require.NoError(t, b.handlePayCancelCallback(ctx))

	assert.Equal(t, "Оплата отложена.\n\nПлатёж не отменён — можно вернуться и доплатить из меню.", ctx.editedMsg)
}

func TestPayCancelOfClosedPayment(t *testing.T) {
	stub := &arEdgeStub{expireAt: time.Now().UTC().Add(10 * 24 * time.Hour)}
	b, db, _ := setupAutorenewEdgeBot(t, stub)
	expires := time.Now().UTC().Add(time.Hour)
	id := createScreenPayment(t, db, "confirmed", &expires)

	ctx := payScreenCallback(strconv.FormatInt(id, 10))
	require.NoError(t, b.handlePayCancelCallback(ctx))

	assert.Equal(t, "Экран оплаты закрыт.", ctx.editedMsg, "о ссылке закрытого платежа не говорим")
}

func TestPayCancelOnMethodStep(t *testing.T) {
	stub := &arEdgeStub{expireAt: time.Now().UTC().Add(10 * 24 * time.Hour)}
	b, _, _ := setupAutorenewEdgeBot(t, stub)

	ctx := payScreenCallback("")
	require.NoError(t, b.handlePayCancelCallback(ctx))

	assert.Equal(t, "Оплата отменена.", ctx.editedMsg)
	assert.Nil(t, editedMarkup(t, ctx))
}

// «Я оплатил», пока касса ещё ждёт: экран остаётся с кнопками, ответ — всплывашкой.
func TestPayCheckPendingKeepsScreen(t *testing.T) {
	stub := &arEdgeStub{expireAt: time.Now().UTC().Add(10 * 24 * time.Hour)}
	b, db, _ := setupAutorenewEdgeBot(t, stub)
	expires := time.Now().UTC().Add(time.Hour)
	createScreenPayment(t, db, "pending", &expires)

	ctx := payScreenCallback("")
	require.NoError(t, b.handlePayCheckCallback(ctx))

	assert.True(t, ctx.responded)
	assert.Contains(t, ctx.respondText, "Оплата пока не поступила")
	assert.Nil(t, ctx.editedMsg)
	assert.Empty(t, ctx.sentMsgs)
}

func TestPayCheckWithoutPaymentClosesScreen(t *testing.T) {
	stub := &arEdgeStub{expireAt: time.Now().UTC().Add(10 * 24 * time.Hour)}
	b, _, _ := setupAutorenewEdgeBot(t, stub)

	ctx := payScreenCallback("")
	require.NoError(t, b.handlePayCheckCallback(ctx))

	assert.Equal(t, "Активных платежей не найдено.", ctx.editedMsg)
	assert.Nil(t, editedMarkup(t, ctx))
	assert.Empty(t, ctx.sentMsgs)
	assert.True(t, ctx.responded)
}

// Отмена, найденная вебхуком или сверкой, превращает экран в сообщение об
// отмене: второе такое же сообщение следом не нужно.
func TestCanceledPaymentRetiresItsScreen(t *testing.T) {
	stub := &arEdgeStub{expireAt: time.Now().UTC().Add(10 * 24 * time.Hour)}
	b, db, capture := setupAutorenewEdgeBot(t, stub)
	id := createScreenPayment(t, db, "pending", nil)
	b.paymentScreens.set(arEdgeUserID, id, payScreenMessageID)
	var edits []string
	b.editPaymentScreen = func(chatID int64, messageID int, text string) error {
		assert.Equal(t, arEdgeUserID, chatID)
		assert.Equal(t, payScreenMessageID, messageID)
		edits = append(edits, text)
		return nil
	}

	payment, err := db.GetPaymentByID(id)
	require.NoError(t, err)
	require.NoError(t, (&paymentCallbackHandler{bot: b}).handleCanceled(payment))

	assert.Equal(t, []string{paymentScreenCanceledText}, edits)
	assert.Empty(t, capture.matching("Платёж отменён"), "экран уже стал сообщением об отмене")
}

// Экран не поправился — об отмене всё равно сообщаем.
func TestCanceledPaymentWithoutScreenStillNotifies(t *testing.T) {
	stub := &arEdgeStub{expireAt: time.Now().UTC().Add(10 * 24 * time.Hour)}
	b, db, capture := setupAutorenewEdgeBot(t, stub)
	id := createScreenPayment(t, db, "pending", nil)

	payment, err := db.GetPaymentByID(id)
	require.NoError(t, err)
	require.NoError(t, (&paymentCallbackHandler{bot: b}).handleCanceled(payment))

	assert.NotEmpty(t, capture.matching("Платёж отменён"))
}

// Нажатия на старом сообщении не должны отвязывать живой экран нового платежа:
// иначе вебхук об оплате не снимет с него кнопки.
func TestStaleScreenTapsKeepLiveScreenTracked(t *testing.T) {
	stub := &arEdgeStub{expireAt: time.Now().UTC().Add(10 * 24 * time.Hour)}
	b, db, _ := setupAutorenewEdgeBot(t, stub)
	old := createScreenPayment(t, db, "expired", nil)
	live := createScreenPayment(t, db, "pending", nil)
	const liveMessageID = 77
	b.paymentScreens.set(arEdgeUserID, live, liveMessageID)

	require.NoError(t, b.handlePayCancelCallback(payScreenCallback(strconv.FormatInt(old, 10))), "отмена старого ожидания")
	require.NoError(t, b.handlePayCancelCallback(payScreenCallback("")), "отмена выбора способа")
	require.NoError(t, b.handlePayCheckCallback(payScreenCallback("")), "«Я оплатил» на старом сообщении")

	messageID, tracked := b.paymentScreens.take(arEdgeUserID, live)
	require.True(t, tracked)
	assert.Equal(t, liveMessageID, messageID)
}
