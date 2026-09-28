package bot

import (
	"testing"

	"github.com/stretchr/testify/require"
	tele "gopkg.in/telebot.v3"
)

// Регрессия: бот обязан подписываться на callback_query, иначе Telegram не
// присылает нажатия inline-кнопок и управление устройствами не работает.
func TestBuildBotSettingsSubscribesToCallbackQuery(t *testing.T) {
	settings := buildBotSettings("test-token")

	poller, ok := settings.Poller.(*tele.LongPoller)
	require.True(t, ok, "poller должен быть *tele.LongPoller")
	require.Contains(t, poller.AllowedUpdates, "callback_query",
		"LongPoller должен явно подписываться на callback_query")
	require.Contains(t, poller.AllowedUpdates, "message",
		"LongPoller должен по-прежнему получать обычные сообщения")
}

// Шаг «отправил» воронки приглашения — выбор inline-результата: без подписки на
// chosen_inline_result Telegram его не пришлёт.
func TestBuildBotSettingsSubscribesToChosenInlineResult(t *testing.T) {
	poller, ok := buildBotSettings("test-token").Poller.(*tele.LongPoller)
	require.True(t, ok)
	require.Contains(t, poller.AllowedUpdates, "chosen_inline_result")
	require.Contains(t, poller.AllowedUpdates, "inline_query")
}
