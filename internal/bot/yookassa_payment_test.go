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

func TestYooKassaWebhookRejectsMismatchedAuthoritativePayment(t *testing.T) {
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
	b := &Bot{db: db, config: &config.Config{YooKassaShopID: "shop-1"}, yookassa: client, userStates: newStateMap()}
	require.Error(t, b.HandleYooKassaWebhook(externalID))
	p, err := db.GetPaymentByID(id)
	require.NoError(t, err)
	require.Equal(t, "pending", p.Status)
}

func TestYooKassaWebhookAcknowledgesUnknownPaymentWithoutProviderCall(t *testing.T) {
	db, err := database.New(t.TempDir() + "/bot.db")
	require.NoError(t, err)
	t.Cleanup(func() { db.Close() })
	b := &Bot{db: db, config: &config.Config{}, userStates: newStateMap()}
	require.NoError(t, b.HandleYooKassaWebhook("unknown"))
}
