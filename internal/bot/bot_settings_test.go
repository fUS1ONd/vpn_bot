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
