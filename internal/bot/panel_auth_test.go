package bot

import (
	"net/http"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/fus1ond/vpn_bot/internal/remnawave"
)

// Отказ панели по токену обязан дойти до владельца — и ровно один раз, пока
// проблема не исчезнет: проход scheduler идёт каждые полчаса.
func TestPanelAuthErrorReachesOwnerOnce(t *testing.T) {
	b, _ := setupSchedulerTestBot(t)
	capture := captureTelegram(t, b)

	client := newTestPanelClient()
	client.SetHTTPClient(&http.Client{
		Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
			return errorResponseForTest(http.StatusUnauthorized, `{"message":"Unauthorized"}`), nil
		}),
	})
	b.remnawave = client

	_, err := client.GetUser(remnawave.UserRef{UUID: "uuid-1"})
	require.Error(t, err)

	b.reportPanelAuthError(err, "тест")
	b.reportPanelAuthError(err, "тест")

	alerts := capture.matching("не приняла API-токен")
	require.Len(t, alerts, 1)
	assert.Equal(t, "999", alerts[0].ChatID)

	// Проблема ушла — следующий отказ снова достоин сообщения.
	b.reportPanelAuthError(nil, "тест")
	b.reportPanelAuthError(err, "тест")
	assert.Len(t, capture.matching("не приняла API-токен"), 2)
}

// Обычная недоступность панели владельца не будит: это не проблема токена.
func TestPanelServerErrorDoesNotAlertOwner(t *testing.T) {
	b, _ := setupSchedulerTestBot(t)
	capture := captureTelegram(t, b)

	client := newTestPanelClient()
	client.SetHTTPClient(&http.Client{
		Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
			return errorResponseForTest(http.StatusInternalServerError, `{}`), nil
		}),
	})
	b.remnawave = client

	_, err := client.GetUser(remnawave.UserRef{UUID: "uuid-1"})
	require.Error(t, err)

	b.reportPanelAuthError(err, "тест")
	assert.Empty(t, capture.matching("не приняла API-токен"))
}
