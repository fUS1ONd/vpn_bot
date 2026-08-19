package bot

import (
	"io"
	"net/http"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	tele "gopkg.in/telebot.v3"
)

// joinRequestContext — контекст заявки на вступление: MockContext не умеет ни
// ChatJoinRequest(), ни Bot(), а тонкий обработчик пользуется обоими.
type joinRequestContext struct {
	MockContext
	bot     *tele.Bot
	request *tele.ChatJoinRequest
}

func (c *joinRequestContext) Bot() *tele.Bot { return c.bot }

func (c *joinRequestContext) ChatJoinRequest() *tele.ChatJoinRequest { return c.request }

// newJoinRequestContext собирает заявку от указанного пользователя в Канал.
func newJoinRequestContext(b *Bot, telegramID int64) *joinRequestContext {
	sender := &tele.User{ID: telegramID}
	return &joinRequestContext{
		MockContext: MockContext{sender: sender},
		bot:         b.bot,
		request: &tele.ChatJoinRequest{
			Chat:   &tele.Chat{ID: b.config.CommunityChatID},
			Sender: sender,
		},
	}
}

// setPanelUnavailable роняет любой запрос к панели пятисоткой — так выглядит
// недоступная Remnawave для предиката «Платящий».
func setPanelUnavailable(t *testing.T, b *Bot) {
	t.Helper()
	b.remnawave.SetHTTPClient(&http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
		return &http.Response{
			StatusCode: http.StatusInternalServerError,
			Body:       io.NopCloser(strings.NewReader(`{"message":"internal error"}`)),
			Header:     make(http.Header),
		}, nil
	})})
}

// Недоступность панели не должна превращаться в отказ: отклонённая заявка
// выглядит как решение бота, а не как сбой. Платящий, подавший заявку в минуту
// недоступности Remnawave, молча получал бы отказ без объяснения — заявка
// исчезает, а человеку остаётся догадаться подать её заново. Заявка,
// оставленная висеть, обратима: её разберёт владелец, которому уходит алерт.
func TestJoinRequestIsNotDeclinedWhenPanelIsDownSoRequestStaysPending(t *testing.T) {
	b, db := setupTestBot(t)
	enableCommunity(b)
	capture := &telegramMethodCapture{}
	b.bot = newOfflineTelegramBotForTest(t, capture.transport())

	makePayingUser(t, b, db, 701, "uuid-join-panel-down")
	setPanelUnavailable(t, b)

	ctx := newJoinRequestContext(b, 701)
	require.NoError(t, b.handleChatJoinRequest(ctx))

	assert.NotContains(t, capture.called(), "declineChatJoinRequest",
		"сбой зависимости не должен выглядеть как отказ по существу")
}

// setPanelUserMissing отвечает 404 на любой запрос к панели — так выглядит
// рассинхрон: пользователь есть в базе бота и уже удалён из Remnawave.
func setPanelUserMissing(t *testing.T, b *Bot) {
	t.Helper()
	b.remnawave.SetHTTPClient(&http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
		return &http.Response{
			StatusCode: http.StatusNotFound,
			Body:       io.NopCloser(strings.NewReader(`{"message":"user not found"}`)),
			Header:     make(http.Header),
		}, nil
	})})
}

// 404 панели — не сбой, а ответ по существу: доступа у человека нет. Оставить
// такую заявку висеть значит запереть её навсегда: рассинхрон базы и панели сам
// не исчезнет, повторную заявку Telegram подать не даст, а объяснения человек
// так и не получит.
func TestJoinRequestIsDeclinedWithExplanationWhenPanelHasNoSuchUser(t *testing.T) {
	b, db := setupTestBot(t)
	enableCommunity(b)
	capture := &telegramMethodCapture{}
	b.bot = newOfflineTelegramBotForTest(t, capture.transport())

	makePayingUser(t, b, db, 703, "uuid-join-panel-404")
	setPanelUserMissing(t, b)

	ctx := newJoinRequestContext(b, 703)
	require.NoError(t, b.handleChatJoinRequest(ctx))

	assert.Contains(t, capture.called(), "declineChatJoinRequest")
	assert.Contains(t, capture.called(), "sendMessage", "отказ по существу объясняется в личку")
}

// Заявка, оставшаяся без решения, висит в Telegram, и бот к ней не вернётся:
// апдейт приходит один раз, а подать её заново пользователь не может. Молча
// оставлять человека в этом состоянии нельзя — владельцу уходит алерт.
func TestPendingJoinRequestAlertsOwnerOncePerUser(t *testing.T) {
	b, db := setupTestBot(t)
	enableCommunity(b)
	capture := &telegramMethodCapture{}
	b.bot = newOfflineTelegramBotForTest(t, capture.transport())

	makePayingUser(t, b, db, 704, "uuid-join-pending-alert")
	setPanelUnavailable(t, b)

	require.NoError(t, b.handleChatJoinRequest(newJoinRequestContext(b, 704)))
	require.Equal(t, []string{"sendMessage"}, capture.called())

	// Повтор владельцу не нужен: при недоступной панели заявок может прийти много.
	require.NoError(t, b.handleChatJoinRequest(newJoinRequestContext(b, 704)))
	assert.Equal(t, []string{"sendMessage"}, capture.called())
}

// Пометка «в Канале» ставится до похода в Telegram: если ApproveJoinRequest
// упал (сеть, 429, бот потерял права админа), пользователь остаётся вне Канала,
// но для бота он уже «вступил» — упоминания Канала ему больше не показываются
// никогда. Тихая потеря: заявка висит неодобренной, а человек про сообщество
// больше не услышит.
func TestFailedApprovalDoesNotMarkUserAsJoinedSoMentionsSurvive(t *testing.T) {
	b, db := setupTestBot(t)
	enableCommunity(b)
	capture := &telegramMethodCapture{fail: true}
	b.bot = newOfflineTelegramBotForTest(t, capture.transport())

	makePayingUser(t, b, db, 702, "uuid-join-approve-fails")

	ctx := newJoinRequestContext(b, 702)
	require.NoError(t, b.handleChatJoinRequest(ctx))
	require.Contains(t, capture.called(), "approveChatJoinRequest")

	member, err := db.IsCommunityMember(702)
	require.NoError(t, err)
	assert.False(t, member, "неодобренная заявка не делает пользователя участником")
}
