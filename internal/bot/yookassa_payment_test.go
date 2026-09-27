package bot

import (
	"io"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/fus1ond/vpn_bot/internal/config"
	"github.com/fus1ond/vpn_bot/internal/database"
	"github.com/fus1ond/vpn_bot/internal/yookassa"
	"github.com/stretchr/testify/require"
)

// Ответ API не сошёлся с записью: подписку не выдаём, но кассе отвечаем успехом —
// повторная доставка ничего не изменит, — а владелец узнаёт о «оплачено» один раз.
func TestYooKassaWebhookAcknowledgesMismatchedPaymentAndNotifiesOwnerOnce(t *testing.T) {
	db, err := database.New(t.TempDir() + "/bot.db")
	require.NoError(t, err)
	t.Cleanup(func() { db.Close() })
	expires := time.Now().Add(time.Hour)
	externalID := "yo-real"
	id, err := db.CreatePayment(&database.Payment{TelegramID: 1, Amount: 500, PaymentMethod: "yookassa", Status: "pending", Provider: "yookassa", ProviderPaymentID: &externalID, ExpiresAt: &expires})
	require.NoError(t, err)
	client := yookassa.NewClientWithBaseURL("shop-1", "secret", "https://yookassa.test")
	client.SetHTTPClient(&http.Client{Transport: roundTripFunc(func(*http.Request) (*http.Response, error) {
		return &http.Response{StatusCode: http.StatusOK, Body: io.NopCloser(strings.NewReader(`{"id":"yo-real","status":"succeeded","amount":{"value":"1.00","currency":"RUB"},"recipient":{"account_id":"shop-1"}}`)), Header: make(http.Header)}, nil
	})})
	b := &Bot{db: db, config: &config.Config{AdminID: 999, YooKassaShopID: "shop-1"}, yookassa: client, userStates: newStateMap()}
	capture := captureTelegram(t, b)

	require.NoError(t, b.HandleYooKassaWebhook("payment.succeeded", externalID))
	require.NoError(t, b.HandleYooKassaWebhook("payment.succeeded", externalID), "повторная доставка тоже получает успех")

	p, err := db.GetPaymentByID(id)
	require.NoError(t, err)
	require.Equal(t, "pending", p.Status, "по несовпавшему ответу подписка не выдаётся")
	alerts := capture.matching(mismatchAlertMarker)
	require.Len(t, alerts, 1, "одно оповещение на платёж, сколько бы доставок ни пришло")
	require.Equal(t, "999", alerts[0].ChatID)
}

func TestYooKassaWebhookAcknowledgesUnmatchedEventAndNotifiesOwner(t *testing.T) {
	db, err := database.New(t.TempDir() + "/bot.db")
	require.NoError(t, err)
	t.Cleanup(func() { db.Close() })
	b := &Bot{db: db, config: &config.Config{AdminID: 999}, userStates: newStateMap()}
	capture := captureTelegram(t, b)

	// Событие о возврате несёт идентификатор возврата, а не платежа: сопоставить
	// его не с чем, но и потерять нельзя.
	require.NoError(t, b.HandleYooKassaWebhook("refund.succeeded", "refund-77"))

	alerts := capture.matching("refund.succeeded")
	require.Len(t, alerts, 1, "владелец должен узнать о несопоставленном событии")
	require.Equal(t, "999", alerts[0].ChatID)
	require.Contains(t, alerts[0].Text, "refund-77")

	// Повторная доставка того же события не превращается в поток сообщений.
	require.NoError(t, b.HandleYooKassaWebhook("refund.succeeded", "refund-77"))
	require.Len(t, capture.matching("refund.succeeded"), 1)
}

func TestYooKassaWebhookProcessesMatchedPaymentAsBefore(t *testing.T) {
	db, err := database.New(t.TempDir() + "/bot.db")
	require.NoError(t, err)
	t.Cleanup(func() { db.Close() })
	externalID := "yo-ok"
	id, err := db.CreatePayment(&database.Payment{TelegramID: 7, Amount: 400, PaymentMethod: "yookassa", Status: "pending", Provider: "yookassa", ProviderPaymentID: &externalID})
	require.NoError(t, err)
	client := yookassa.NewClientWithBaseURL("shop-1", "secret", "https://yookassa.test")
	client.SetHTTPClient(&http.Client{Transport: roundTripFunc(func(*http.Request) (*http.Response, error) {
		return &http.Response{StatusCode: http.StatusOK, Body: io.NopCloser(strings.NewReader(`{"id":"yo-ok","status":"canceled","amount":{"value":"400.00","currency":"RUB"},"recipient":{"account_id":"shop-1"}}`)), Header: make(http.Header)}, nil
	})})
	b := &Bot{db: db, config: &config.Config{AdminID: 999, YooKassaShopID: "shop-1"}, yookassa: client, userStates: newStateMap()}
	capture := captureTelegram(t, b)

	require.NoError(t, b.HandleYooKassaWebhook("payment.canceled", externalID))

	p, err := db.GetPaymentByID(id)
	require.NoError(t, err)
	require.Equal(t, "canceled", p.Status)
	require.Empty(t, capture.matching("не сопоставлено"), "сопоставленное событие не тревожит владельца")
}
