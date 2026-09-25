package bot

import (
	"io"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/fus1ond/vpn_bot/internal/config"
	"github.com/fus1ond/vpn_bot/internal/database"
	"github.com/fus1ond/vpn_bot/internal/paymentprovider"
	"github.com/fus1ond/vpn_bot/internal/yookassa"
	"github.com/stretchr/testify/require"
)

func autorenewTestBot(t *testing.T, enabled bool) (*Bot, *database.DB) {
	t.Helper()
	db, err := database.New(t.TempDir() + "/bot.db")
	require.NoError(t, err)
	t.Cleanup(func() { db.Close() })
	cfg := &config.Config{
		AdminID:           999,
		YooKassaShopID:    "shop-1",
		YooKassaSecretKey: "secret",
		TermsOfServiceURL: "https://example.com/terms",
		AutorenewEnabled:  enabled,
	}
	return &Bot{db: db, config: cfg, yookassa: yookassa.NewClient("shop-1", "secret"), userStates: newStateMap()}, db
}

// Выключенный рубильник гасит всё: ни абзаца, ни лишнего поля в запросе к кассе.
func TestAutorenewDisabledChangesNothing(t *testing.T) {
	b, _ := autorenewTestBot(t, false)

	require.False(t, b.autorenewAvailable())
	require.Empty(t, b.autorenewConsentNote())
	require.False(t, b.shouldSavePaymentMethod(paymentprovider.YooKassa, false))
}

func TestAutorenewEnabledShowsConsentNote(t *testing.T) {
	b, _ := autorenewTestBot(t, true)

	require.True(t, b.autorenewAvailable())
	note := b.autorenewConsentNote()
	require.Contains(t, note, "сохранить способ оплаты")
	require.Contains(t, note, "выключено по умолчанию")
	require.Contains(t, note, "https://example.com/terms")
}

// Рубильник включён, но касса не настроена — фича мертва: списать по
// криптоплатежу невозможно физически.
func TestAutorenewNeedsYooKassa(t *testing.T) {
	b, _ := autorenewTestBot(t, true)
	b.yookassa = nil
	require.False(t, b.autorenewAvailable())

	b, _ = autorenewTestBot(t, true)
	b.config.YooKassaSecretKey = ""
	require.False(t, b.autorenewAvailable())
}

func TestShouldSavePaymentMethodScope(t *testing.T) {
	b, _ := autorenewTestBot(t, true)

	require.True(t, b.shouldSavePaymentMethod(paymentprovider.YooKassa, false))
	require.False(t, b.shouldSavePaymentMethod(paymentprovider.YooKassa, true), "тестовый платёж админа способ не сохраняет")
	require.False(t, b.shouldSavePaymentMethod(paymentprovider.Platega, false), "крипта в этой логике не участвует")
}

// Способ пишется по сверенному ответу кассы — и только он, согласие остаётся
// выключенным.
func TestRememberAutorenewMethodDoesNotEnableConsent(t *testing.T) {
	b, db := autorenewTestBot(t, true)
	payment := &database.Payment{ID: 1, TelegramID: 42, Amount: 400}

	b.rememberAutorenewMethod(payment, &paymentprovider.Payment{Status: paymentprovider.StatusSucceeded, SavedMethodID: "pm-1", SavedMethodTitle: "•••• 4242"})

	a, err := db.GetAutorenewal(42)
	require.NoError(t, err)
	require.NotNil(t, a)
	require.True(t, a.HasMethod())
	require.False(t, a.Enabled, "сохранение Способа не включает Автопродление")
}

func TestRememberAutorenewMethodSkipsTestAndUnsaved(t *testing.T) {
	b, db := autorenewTestBot(t, true)

	b.rememberAutorenewMethod(&database.Payment{ID: 1, TelegramID: 43, IsTest: true},
		&paymentprovider.Payment{Status: paymentprovider.StatusSucceeded, SavedMethodID: "pm-1", SavedMethodTitle: "•••• 4242"})
	a, err := db.GetAutorenewal(43)
	require.NoError(t, err)
	require.Nil(t, a, "тестовый платёж админа способ не сохраняет")

	b.rememberAutorenewMethod(&database.Payment{ID: 2, TelegramID: 44}, &paymentprovider.Payment{})
	a, err = db.GetAutorenewal(44)
	require.NoError(t, err)
	require.Nil(t, a, "saved: false — способа нет")
}

func TestRememberAutorenewMethodSilentWhenDisabled(t *testing.T) {
	b, db := autorenewTestBot(t, false)

	b.rememberAutorenewMethod(&database.Payment{ID: 1, TelegramID: 45},
		&paymentprovider.Payment{Status: paymentprovider.StatusSucceeded, SavedMethodID: "pm-1", SavedMethodTitle: "•••• 4242"})

	a, err := db.GetAutorenewal(45)
	require.NoError(t, err)
	require.Nil(t, a)
}

func createYooKassaPayment(t *testing.T, db *database.DB, telegramID int64, externalID string) *database.Payment {
	t.Helper()
	id, err := db.CreatePayment(&database.Payment{
		TelegramID: telegramID, Amount: 400, PaymentMethod: "yookassa", Status: "pending",
		Provider: "yookassa", ProviderPaymentID: &externalID,
	})
	require.NoError(t, err)
	payment, err := db.GetPaymentByID(id)
	require.NoError(t, err)
	return payment
}

func verifiedSaved(externalID, status string) *paymentprovider.Payment {
	return &paymentprovider.Payment{
		ID: externalID, Status: status, Amount: 400, Currency: "RUB", RecipientID: "shop-1",
		SavedMethodID: "pm-55", SavedMethodTitle: "•••• 4242",
	}
}

// Сверенный успешный ответ кассы с сохранённым способом доезжает до БД.
func TestYooKassaVerifiedSucceededStoresSavedMethod(t *testing.T) {
	b, db := autorenewTestBot(t, true)
	payment := createYooKassaPayment(t, db, 55, "yo-saved")

	require.NoError(t, b.verifyYooKassaPayment(payment, verifiedSaved("yo-saved", paymentprovider.StatusSucceeded)))

	a, err := db.GetAutorenewal(55)
	require.NoError(t, err)
	require.NotNil(t, a)
	require.Equal(t, "pm-55", *a.PaymentMethodID)
	require.Equal(t, "•••• 4242", *a.MethodTitle)
	require.False(t, a.Enabled)
}

// Способ, погашенный как мёртвый, не возвращается сверкой отклонённого
// автосписания: касса и в отказе `card_expired` отдаёт saved=true.
func TestCanceledPaymentDoesNotResurrectDeadMethod(t *testing.T) {
	b, db := autorenewTestBot(t, true)
	payment := createYooKassaPayment(t, db, 56, "yo-dead")
	require.NoError(t, db.SaveAutorenewMethod(56, "pm-55", "•••• 4242"))
	require.NoError(t, db.SetAutorenewEnabled(56, true))
	require.NoError(t, db.ClearAutorenewMethod(56))

	require.NoError(t, b.verifyYooKassaPayment(payment, verifiedSaved("yo-dead", paymentprovider.StatusCanceled)))

	a, err := db.GetAutorenewal(56)
	require.NoError(t, err)
	require.False(t, a.HasMethod(), "мёртвая карта не должна вернуться и пойти в следующее списание")
}

// Отвязанный Способ не возвращается ответом по платежу, начатому до отвязки, —
// ни ручному, оплаченному после, ни повторному вебхуку.
func TestUnlinkedMethodNotRestoredByOlderPayment(t *testing.T) {
	b, db := autorenewTestBot(t, true)
	older := createYooKassaPayment(t, db, 57, "yo-older")
	require.NoError(t, db.SaveAutorenewMethod(57, "pm-55", "•••• 4242"))
	require.NoError(t, b.unlinkPaymentMethod(57))

	require.NoError(t, b.verifyYooKassaPayment(older, verifiedSaved("yo-older", paymentprovider.StatusSucceeded)))

	a, err := db.GetAutorenewal(57)
	require.NoError(t, err)
	require.False(t, a.HasMethod(), "после «мы больше не храним его» карта вернуться не должна")

	// Новая оплата после отвязки — свежее согласие: Способ сохраняется.
	newer := createYooKassaPayment(t, db, 57, "yo-newer")
	newer.CreatedAt = time.Now().UTC().Add(time.Second)
	require.NoError(t, b.verifyYooKassaPayment(newer, verifiedSaved("yo-newer", paymentprovider.StatusSucceeded)))

	a, err = db.GetAutorenewal(57)
	require.NoError(t, err)
	require.True(t, a.HasMethod())
	require.False(t, a.Enabled, "согласие отвязка погасила, новая оплата его не включает")
}

// Сквозь вебхук: отменённый платёж Способ не сохраняет — касса отдаёт его
// только в успешном ответе.
func TestYooKassaWebhookCanceledDoesNotStoreMethod(t *testing.T) {
	b, db := autorenewTestBot(t, true)
	externalID := "yo-saved"
	createYooKassaPayment(t, db, 55, externalID)

	client := yookassa.NewClientWithBaseURL("shop-1", "secret", "https://yookassa.test")
	client.SetHTTPClient(&http.Client{Transport: roundTripFunc(func(*http.Request) (*http.Response, error) {
		return &http.Response{StatusCode: http.StatusOK, Header: make(http.Header), Body: io.NopCloser(strings.NewReader(
			`{"id":"yo-saved","status":"canceled","amount":{"value":"400.00","currency":"RUB"},"recipient":{"account_id":"shop-1"},
			  "payment_method":{"type":"bank_card","id":"pm-55","saved":true,"card":{"last4":"4242"}}}`))}, nil
	})})
	b.yookassa = client
	captureTelegram(t, b)

	require.NoError(t, b.HandleYooKassaWebhook("payment.canceled", externalID))

	a, err := db.GetAutorenewal(55)
	require.NoError(t, err)
	require.Nil(t, a)
}
