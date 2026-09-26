package bot

import (
	"net/http"
	"sync"
	"testing"
	"time"

	"github.com/fus1ond/vpn_bot/internal/paymentprovider"
	"github.com/fus1ond/vpn_bot/internal/remnawave"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	tele "gopkg.in/telebot.v3"
)

// answerLog — контекст callback, который считает ответы и сообщения. Сторож
// отвечает из своей горутины, поэтому учёт под мьютексом.
type answerLog struct {
	*MockContext

	mu        sync.Mutex
	responses []*tele.CallbackResponse
	sent      []any
	edits     []any
}

func newAnswerLog(callback bool) *answerLog {
	ctx := &MockContext{sender: &tele.User{ID: 4242}, message: &tele.Message{ID: 7}}
	if callback {
		ctx.callback = &tele.Callback{}
	}
	return &answerLog{MockContext: ctx}
}

func (l *answerLog) Respond(resp ...*tele.CallbackResponse) error {
	l.mu.Lock()
	defer l.mu.Unlock()
	var r *tele.CallbackResponse
	if len(resp) > 0 {
		r = resp[0]
	}
	l.responses = append(l.responses, r)
	return nil
}

func (l *answerLog) Send(what any, _ ...any) error {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.sent = append(l.sent, what)
	return nil
}

func (l *answerLog) Edit(what any, opts ...any) error {
	l.mu.Lock()
	l.edits = append(l.edits, what)
	l.mu.Unlock()
	return l.MockContext.Edit(what, opts...)
}

func (l *answerLog) snapshot() ([]*tele.CallbackResponse, []any) {
	l.mu.Lock()
	defer l.mu.Unlock()
	return append([]*tele.CallbackResponse(nil), l.responses...), append([]any(nil), l.sent...)
}

func runGuarded(b *Bot, ctx tele.Context, handler tele.HandlerFunc) error {
	return b.callbackDeadlineMiddleware(handler)(ctx)
}

func TestCallbackGuardKeepsFastAlert(t *testing.T) {
	b := &Bot{callbackAnswerDeadline: time.Second}
	ctx := newAnswerLog(true)

	require.NoError(t, runGuarded(b, ctx, func(c tele.Context) error {
		return c.RespondAlert("Ошибка сброса устройств")
	}))

	responses, sent := ctx.snapshot()
	require.Len(t, responses, 1, "ответ на нажатие ровно один")
	assert.True(t, responses[0].ShowAlert)
	assert.Equal(t, "Ошибка сброса устройств", responses[0].Text)
	assert.Empty(t, sent)
}

func TestCallbackGuardAnswersSlowHandlerOnDeadline(t *testing.T) {
	b := &Bot{callbackAnswerDeadline: 10 * time.Millisecond}
	ctx := newAnswerLog(true)

	require.NoError(t, runGuarded(b, ctx, func(c tele.Context) error {
		require.Eventually(t, func() bool {
			responses, _ := ctx.snapshot()
			return len(responses) == 1
		}, time.Second, time.Millisecond, "часики гаснут по сроку, не дожидаясь обработчика")
		return c.RespondAlert("Ошибка отвязки устройства")
	}))

	responses, sent := ctx.snapshot()
	require.Len(t, responses, 1, "второй ответ Telegram отверг бы")
	assert.Nil(t, responses[0])
	assert.Equal(t, []any{"Ошибка отвязки устройства"}, sent, "опоздавшая ошибка приходит сообщением")
}

func TestCallbackGuardDropsLateToast(t *testing.T) {
	b := &Bot{callbackAnswerDeadline: 10 * time.Millisecond}
	ctx := newAnswerLog(true)

	require.NoError(t, runGuarded(b, ctx, func(c tele.Context) error {
		time.Sleep(30 * time.Millisecond)
		return c.Respond(&tele.CallbackResponse{Text: "Устройство отвязано"})
	}))

	responses, sent := ctx.snapshot()
	require.Len(t, responses, 1)
	assert.Empty(t, sent, "toast об успехе дублировал бы уже видимый итог")
}

func TestCallbackGuardAnswersForSilentHandler(t *testing.T) {
	b := &Bot{callbackAnswerDeadline: time.Second}
	ctx := newAnswerLog(true)

	require.NoError(t, runGuarded(b, ctx, func(tele.Context) error { return nil }))

	responses, _ := ctx.snapshot()
	assert.Len(t, responses, 1, "обработчик без ответа не оставляет часики крутиться")
}

func TestCallbackGuardIgnoresMessages(t *testing.T) {
	b := &Bot{callbackAnswerDeadline: time.Millisecond}
	ctx := newAnswerLog(false)

	var got tele.Context
	require.NoError(t, runGuarded(b, ctx, func(c tele.Context) error {
		got = c
		return nil
	}))

	responses, _ := ctx.snapshot()
	assert.Empty(t, responses)
	assert.Same(t, ctx, got, "сообщения идут мимо сторожа")
}

func TestPayMethodShowsCreatingBeforeCheckout(t *testing.T) {
	expires := time.Now().UTC().Add(time.Hour)
	stub := &arEdgeStub{expireAt: time.Now().UTC().Add(10 * 24 * time.Hour),
		responses: []string{yooPendingBody(&expires)}}
	b, _, _ := setupAutorenewEdgeBot(t, stub)

	ctx := newAnswerLog(true)
	ctx.sender = &tele.User{ID: arEdgeUserID}
	ctx.callback = &tele.Callback{Data: paymentprovider.YooKassa}
	require.NoError(t, b.handlePayMethodCallback(ctx))

	require.Len(t, ctx.edits, 2)
	assert.Equal(t, paymentScreenCreatingText, ctx.edits[0], "экран сразу говорит, что нажатие принято")
	assert.Contains(t, ctx.edits[1], "Нажмите «💳 Оплатить»", "итог заменяет промежуточный экран")
}

func TestRegistrationProgressIsRemoved(t *testing.T) {
	for _, tc := range []struct {
		name   string
		status int
	}{
		{"успех", http.StatusOK},
		{"ошибка панели", http.StatusInternalServerError},
	} {
		t.Run(tc.name, func(t *testing.T) {
			b, db := setupTestBot(t)
			invite, err := db.CreateInviteWithExpiry(999, nil)
			require.NoError(t, err)

			client := remnawave.NewClient("https://panel.example.com", "test-token", nil)
			client.SetHTTPClient(&http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
				body := `{"response":{"uuid":"uuid-progress","username":"p","status":"ACTIVE",` +
					`"subscriptionUrl":"https://sub.example.com/x","expireAt":"2099-01-01T00:00:00Z"}}`
				if tc.status != http.StatusOK {
					body = `{"message":"boom"}`
				}
				return jsonResponse(tc.status, body), nil
			})})
			b.remnawave = client

			var progress []string
			var deleted []int
			b.sendCardMessage = func(_ tele.Context, msg string, _ *tele.ReplyMarkup) (int, error) {
				progress = append(progress, msg)
				return 321, nil
			}
			b.deleteCardMessage = func(_ tele.Context, id int) error {
				deleted = append(deleted, id)
				return nil
			}

			ctx := &MockContext{sender: &tele.User{ID: 7002, Username: "p"}, message: &tele.Message{}}
			require.NoError(t, b.processInviteCode(ctx, invite.Code))

			assert.Equal(t, []string{MsgAccountCreating}, progress)
			assert.Equal(t, []int{321}, deleted, "промежуточное сообщение не остаётся в истории")
			assert.NotEmpty(t, ctx.sentMsgs, "итог приходит отдельным сообщением")
		})
	}
}

func TestStatusShowsTyping(t *testing.T) {
	b, db := setupTestBot(t)
	_, err := db.CreateUser(7003, "t", "T", strPtrTest("uuid-typing"), nil, nil, nil)
	require.NoError(t, err)

	typed := 0
	b.notifyTyping = func(tele.Context) error {
		typed++
		return nil
	}

	ctx := &MockContext{sender: &tele.User{ID: 7003}, message: &tele.Message{}}
	_ = b.handleStatus(ctx)

	assert.Equal(t, 1, typed)
}
