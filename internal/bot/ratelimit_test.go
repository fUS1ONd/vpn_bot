package bot

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	tele "gopkg.in/telebot.v3"
)

// newTestLimiter создаёт лимитер, cleanup-горутина которого умирает вместе с тестом.
func newTestLimiter(t *testing.T, rate float64, burst int) *userRateLimiter {
	t.Helper()
	done := make(chan struct{})
	t.Cleanup(func() { close(done) })
	return newUserRateLimiter(rate, burst, done)
}

// drainBucket опустошает бакет пользователя, чтобы следующий апдейт был отсечён.
func drainBucket(t *testing.T, rl *userRateLimiter, telegramID int64, burst int) {
	t.Helper()
	for i := 0; i < burst; i++ {
		require.True(t, rl.allow(telegramID), "бурст должен пропустить %d-й апдейт", i+1)
	}
	require.False(t, rl.allow(telegramID), "после бурста лимитер должен отсекать")
}

// TestClaimTextWarning_OnlyOncePerBucket: предупреждение о превышении лимита
// столбится один раз на жизнь бакета — дальше молчим, чтобы не усиливать флуд
// за счёт собственной квоты исходящих.
func TestClaimTextWarning_OnlyOncePerBucket(t *testing.T) {
	rl := newTestLimiter(t, 3, 5)
	drainBucket(t, rl, 42, 5)

	assert.True(t, rl.claimTextWarning(42), "первое превышение должно дать предупреждение")
	assert.False(t, rl.claimTextWarning(42), "второе превышение должно молчать")
	assert.False(t, rl.claimTextWarning(42))
}

// TestClaimTextWarning_PerUser: флаг предупреждения живёт в бакете пользователя
// и не гасит предупреждение соседу.
func TestClaimTextWarning_PerUser(t *testing.T) {
	rl := newTestLimiter(t, 3, 5)
	drainBucket(t, rl, 1, 5)
	drainBucket(t, rl, 2, 5)

	assert.True(t, rl.claimTextWarning(1))
	assert.True(t, rl.claimTextWarning(2), "у второго пользователя свой бакет и своё предупреждение")
}

// TestClaimTextWarning_NoBucket: без бакета предупреждать не о чем — молчим,
// а не заводим запись на пустом месте.
func TestClaimTextWarning_NoBucket(t *testing.T) {
	rl := newTestLimiter(t, 3, 5)
	assert.False(t, rl.claimTextWarning(777))
}

// TestRateLimitMiddleware_CallbackAlwaysResponds: нажатие inline-кнопки при
// превышении лимита гасит «часики» ответом на callback, а хендлер не зовётся.
func TestRateLimitMiddleware_CallbackAlwaysResponds(t *testing.T) {
	b := &Bot{userLimiter: newTestLimiter(t, 3, 5), userStates: newStateMap()}
	drainBucket(t, b.userLimiter, 42, 5)

	called := false
	handler := b.rateLimitMiddleware(func(tele.Context) error {
		called = true
		return nil
	})

	ctx := &MockContext{
		sender:   &tele.User{ID: 42},
		callback: &tele.Callback{Data: "\fsub_card"},
	}
	require.NoError(t, handler(ctx))

	assert.False(t, called, "отсечённый апдейт не должен доходить до хендлера")
	assert.True(t, ctx.responded, "часики на кнопке должны быть погашены")
	assert.NotEmpty(t, ctx.respondText, "ответ должен объяснять причину")
	assert.Nil(t, ctx.sentMsg, "всплывашка не уходит в чат отдельным сообщением")
}

// TestRateLimitMiddleware_CallbackRespondsEveryTime: всплывашка не тратит квоту
// исходящих, поэтому дедупликации здесь нет — отвечаем на каждое нажатие.
func TestRateLimitMiddleware_CallbackRespondsEveryTime(t *testing.T) {
	b := &Bot{userLimiter: newTestLimiter(t, 3, 5), userStates: newStateMap()}
	drainBucket(t, b.userLimiter, 42, 5)

	handler := b.rateLimitMiddleware(func(tele.Context) error { return nil })

	for i := 0; i < 3; i++ {
		ctx := &MockContext{
			sender:   &tele.User{ID: 42},
			callback: &tele.Callback{Data: "\fsub_card"},
		}
		require.NoError(t, handler(ctx))
		assert.True(t, ctx.responded, "нажатие %d должно получить ответ", i+1)
	}
}

// TestRateLimitMiddleware_TextWarnsOnce: текстовое сообщение при превышении
// получает одно предупреждение, следующие отсекаются молча.
func TestRateLimitMiddleware_TextWarnsOnce(t *testing.T) {
	b := &Bot{userLimiter: newTestLimiter(t, 3, 5), userStates: newStateMap()}
	drainBucket(t, b.userLimiter, 42, 5)

	handler := b.rateLimitMiddleware(func(tele.Context) error {
		t.Fatal("отсечённый апдейт не должен доходить до хендлера")
		return nil
	})

	first := &MockContext{sender: &tele.User{ID: 42}, message: &tele.Message{Text: "привет"}}
	require.NoError(t, handler(first))
	assert.NotNil(t, first.sentMsg, "на первое превышение бот должен объясниться")

	second := &MockContext{sender: &tele.User{ID: 42}, message: &tele.Message{Text: "привет"}}
	require.NoError(t, handler(second))
	assert.Nil(t, second.sentMsg, "повторные превышения молчат")
}

// TestRateLimitMiddleware_InlineQueryAlwaysAnswered: inline-запрос уходит на
// каждую набранную букву и под лимит попадает легче всего. Молчание оставило бы
// в поле ввода часики, которые не разрешатся ничем.
func TestRateLimitMiddleware_InlineQueryAlwaysAnswered(t *testing.T) {
	b := &Bot{userLimiter: newTestLimiter(t, 3, 5), userStates: newStateMap()}
	drainBucket(t, b.userLimiter, 42, 5)

	handler := b.rateLimitMiddleware(func(tele.Context) error {
		t.Fatal("отсечённый апдейт не должен доходить до хендлера")
		return nil
	})

	for i := 0; i < 3; i++ {
		ctx := &MockContext{sender: &tele.User{ID: 42}, query: &tele.Query{ID: "q", Sender: &tele.User{ID: 42}}}
		require.NoError(t, handler(ctx))
		require.NotNil(t, ctx.queryAnswer, "запрос %d остался без ответа", i+1)
		assert.Empty(t, ctx.queryAnswer.Results)
		assert.Equal(t, MsgRateLimitedInline, ctx.queryAnswer.SwitchPMText)
		assert.NotEmpty(t, ctx.queryAnswer.SwitchPMParameter, "Telegram отвергнет текст без параметра")
	}
}

// TestRateLimitMiddleware_PassesThroughWhenAllowed: в пределах лимита апдейт
// доходит до хендлера, и лимитер ничего не отвечает от себя.
func TestRateLimitMiddleware_PassesThroughWhenAllowed(t *testing.T) {
	b := &Bot{userLimiter: newTestLimiter(t, 3, 5), userStates: newStateMap()}

	called := false
	handler := b.rateLimitMiddleware(func(tele.Context) error {
		called = true
		return nil
	})

	ctx := &MockContext{sender: &tele.User{ID: 42}, message: &tele.Message{Text: "привет"}}
	require.NoError(t, handler(ctx))

	assert.True(t, called)
	assert.False(t, ctx.responded)
	assert.Nil(t, ctx.sentMsg)
}

// TestRateLimitMiddleware_KeepsUserState: превышение лимита не должно ронять
// начатый флоу — middleware не знает про экраны и состояния.
func TestRateLimitMiddleware_KeepsUserState(t *testing.T) {
	b := &Bot{userLimiter: newTestLimiter(t, 3, 5), userStates: newStateMap()}
	b.userStates.Set(42, StateWaitInvite)
	drainBucket(t, b.userLimiter, 42, 5)

	handler := b.rateLimitMiddleware(func(tele.Context) error { return nil })
	ctx := &MockContext{sender: &tele.User{ID: 42}, message: &tele.Message{Text: "код"}}
	require.NoError(t, handler(ctx))

	assert.Equal(t, StateWaitInvite, b.userStates.Get(42), "состояние должно пережить отсечку")
}
