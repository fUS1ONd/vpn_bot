package bot

import (
	"github.com/fus1ond/vpn_bot/internal/remnawave"

	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"testing"

	"github.com/stretchr/testify/require"
)

// sentMessage — сообщение, ушедшее в Telegram из-под теста.
type sentMessage struct {
	ChatID string
	Text   string
}

// telegramCapture подменяет Telegram Bot API и запоминает отправленные сообщения:
// проверяем наблюдаемое поведение — что получил владелец, — а не порядок вызовов.
type telegramCapture struct {
	mu       sync.Mutex
	messages []sentMessage
}

func captureTelegram(t *testing.T, b *Bot) *telegramCapture {
	t.Helper()
	capture := &telegramCapture{}
	b.bot = newOfflineTelegramBotForTest(t, roundTripFunc(func(r *http.Request) (*http.Response, error) {
		if !strings.Contains(r.URL.Path, "/sendMessage") {
			return nil, nil
		}
		body, err := io.ReadAll(r.Body)
		require.NoError(t, err)
		capture.mu.Lock()
		capture.messages = append(capture.messages, parseSentMessage(string(body)))
		capture.mu.Unlock()
		return &http.Response{
			StatusCode: http.StatusOK,
			Body: io.NopCloser(strings.NewReader(
				`{"ok":true,"result":{"message_id":1,"date":1710000000,"chat":{"id":1,"type":"private"},"text":"ok"}}`)),
			Header: make(http.Header),
		}, nil
	}))
	return capture
}

// parseSentMessage разбирает тело запроса к Bot API: telebot шлёт JSON, но
// form-кодирование тоже поддерживаем, чтобы тест не зависел от его внутренностей.
func parseSentMessage(body string) sentMessage {
	var payload struct {
		ChatID any    `json:"chat_id"`
		Text   string `json:"text"`
	}
	if err := json.Unmarshal([]byte(body), &payload); err == nil && payload.Text != "" {
		return sentMessage{ChatID: fmt.Sprint(payload.ChatID), Text: payload.Text}
	}
	values, err := url.ParseQuery(body)
	if err != nil {
		return sentMessage{}
	}
	return sentMessage{ChatID: values.Get("chat_id"), Text: values.Get("text")}
}

func (c *telegramCapture) all() []sentMessage {
	c.mu.Lock()
	defer c.mu.Unlock()
	out := make([]sentMessage, len(c.messages))
	copy(out, c.messages)
	return out
}

// matching возвращает сообщения, содержащие подстроку.
func (c *telegramCapture) matching(substr string) []sentMessage {
	var out []sentMessage
	for _, m := range c.all() {
		if strings.Contains(m.Text, substr) {
			out = append(out, m)
		}
	}
	return out
}

// strPtrTest и i64PtrTest — короткие конструкторы указателей для тестов.
func strPtrTest(s string) *string { return &s }
func i64PtrTest(n int64) *int64   { return &n }

// newTestPanelClient — клиент панели для тестов. Версия контракта задаётся явно
// (2.8.x): иначе каждый фейк панели в тестах был бы обязан отвечать ещё и на
// /api/system/metadata, хотя проверяет он совсем другое.
func newTestPanelClient() *remnawave.Client {
	client := remnawave.NewClient("https://panel.example.com", "test-token", nil)
	client.SetAPIVersion(remnawave.APIVersionV2)
	return client
}

// errorResponseForTest собирает ответ панели с кодом ошибки.
func errorResponseForTest(status int, body string) *http.Response {
	return &http.Response{
		StatusCode: status,
		Body:       io.NopCloser(strings.NewReader(body)),
		Header:     make(http.Header),
	}
}
