package bot

import (
	"io"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	tele "gopkg.in/telebot.v3"
)

// telegramMethodCapture запоминает, какие методы Bot API вызвал бот.
type telegramMethodCapture struct {
	mu      sync.Mutex
	methods []string
	fail    bool
}

func (c *telegramMethodCapture) transport() roundTripFunc {
	return func(r *http.Request) (*http.Response, error) {
		c.mu.Lock()
		parts := strings.Split(r.URL.Path, "/")
		c.methods = append(c.methods, parts[len(parts)-1])
		fail := c.fail
		c.mu.Unlock()

		if fail {
			return &http.Response{
				StatusCode: http.StatusBadRequest,
				Body:       io.NopCloser(strings.NewReader(`{"ok":false,"error_code":400,"description":"Bad Request: user not found"}`)),
				Header:     make(http.Header),
			}, nil
		}
		return &http.Response{
			StatusCode: http.StatusOK,
			Body:       io.NopCloser(strings.NewReader(`{"ok":true,"result":true}`)),
			Header:     make(http.Header),
		}, nil
	}
}

func (c *telegramMethodCapture) called() []string {
	c.mu.Lock()
	defer c.mu.Unlock()
	out := make([]string, len(c.methods))
	copy(out, c.methods)
	return out
}

func TestKickFromCommunity(t *testing.T) {
	t.Run("bans and immediately unbans", func(t *testing.T) {
		b, _ := setupTestBot(t)
		enableCommunity(b)
		capture := &telegramMethodCapture{}
		b.bot = newOfflineTelegramBotForTest(t, capture.transport())

		b.kickFromCommunity(401)

		// Чёрный список чата не нужен: вход закрыт гейтом, а «разбана» нет.
		assert.Equal(t, []string{"kickChatMember", "unbanChatMember"}, capture.called())
	})

	t.Run("feature disabled changes nothing", func(t *testing.T) {
		b, _ := setupTestBot(t)
		capture := &telegramMethodCapture{}
		b.bot = newOfflineTelegramBotForTest(t, capture.transport())

		b.kickFromCommunity(402)

		assert.Empty(t, capture.called())
	})

	// Кик не прошёл — бан всё равно состоялся, но человек остался в сообществе.
	// Узнать об этом случайно нельзя, поэтому владельцу уходит алерт.
	t.Run("telegram failure alerts the owner", func(t *testing.T) {
		b, _ := setupTestBot(t)
		enableCommunity(b)
		capture := &telegramMethodCapture{fail: true}
		b.bot = newOfflineTelegramBotForTest(t, capture.transport())

		assert.NotPanics(t, func() { b.kickFromCommunity(403) })
		assert.Equal(t, []string{"kickChatMember", "sendMessage"}, capture.called())
	})
}

// Бан фиксируется в базе даже когда Telegram отказал в кике: доступ отобран,
// разбирать кик владелец будет по логам.
func TestProcessBanUserPersistsBanWhenCommunityKickFails(t *testing.T) {
	b, db := setupTestBot(t)
	enableCommunity(b)
	capture := &telegramMethodCapture{fail: true}
	b.bot = newOfflineTelegramBotForTest(t, capture.transport())

	const targetID int64 = 404
	_, err := db.CreateUser(targetID, "victim", "Victim", strPtrTest("uuid-ban-kick"), nil, nil, nil)
	require.NoError(t, err)
	b.remnawave.SetHTTPClient(&http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
		return &http.Response{StatusCode: http.StatusOK, Body: io.NopCloser(strings.NewReader(`{"response":{}}`)), Header: make(http.Header)}, nil
	})})

	ctx := &MockContext{sender: &tele.User{ID: b.config.AdminID}}
	require.NoError(t, b.processBanUser(ctx, strconv.FormatInt(targetID, 10)))

	banned, err := db.IsBanned(targetID)
	require.NoError(t, err)
	assert.True(t, banned)
}
