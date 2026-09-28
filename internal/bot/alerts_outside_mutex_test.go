package bot

import (
	"fmt"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/fus1ond/vpn_bot/internal/database"
	"github.com/fus1ond/vpn_bot/internal/paymentprovider"
	"github.com/fus1ond/vpn_bot/internal/platega"
	"github.com/fus1ond/vpn_bot/internal/remnawave"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// Алерт владельцу, как и сообщение человеку, уходит вне getPaymentMutex:
// медленный Telegram не должен держать ручную оплату, «Проверить оплату»,
// перевыпуск ссылки и автосписание того же человека. Решение «сообщать или
// нет» принимается под мьютексом, наружу выходит только сетевой вызов.

// hangingAdminTelegram — Telegram, у которого отправка владельцу висит, пока
// тест её не отпустит. Сообщения остальным уходят сразу.
type hangingAdminTelegram struct {
	entered chan struct{}
	release chan struct{}
}

func hangAdminTelegram(t *testing.T, b *Bot) *hangingAdminTelegram {
	t.Helper()
	tg := &hangingAdminTelegram{entered: make(chan struct{}, 16), release: make(chan struct{})}
	admin := fmt.Sprint(b.config.AdminID)
	b.bot = newOfflineTelegramBotForTest(t, roundTripFunc(func(r *http.Request) (*http.Response, error) {
		if !strings.Contains(r.URL.Path, "/sendMessage") {
			return nil, nil
		}
		body, err := io.ReadAll(r.Body)
		require.NoError(t, err)
		if parseSentMessage(string(body)).ChatID == admin {
			tg.entered <- struct{}{}
			<-tg.release
		}
		return jsonResponse(http.StatusOK,
			`{"ok":true,"result":{"message_id":1,"date":1710000000,"chat":{"id":1,"type":"private"},"text":"ok"}}`), nil
	}))
	return tg
}

// assertAlertLeavesMutexFree запускает вход и, пока отправка владельцу висит,
// проверяет, что мьютекс платежей человека свободен.
func assertAlertLeavesMutexFree(t *testing.T, b *Bot, telegramID int64, run func()) {
	t.Helper()
	tg := hangAdminTelegram(t, b)
	done := make(chan struct{})
	go func() {
		defer close(done)
		run()
	}()

	select {
	case <-tg.entered:
	case <-time.After(3 * time.Second):
		close(tg.release)
		<-done
		t.Fatal("предпосылка: вход должен был написать владельцу")
	}

	mu := getPaymentMutex(telegramID)
	free := mu.TryLock()
	if free {
		mu.Unlock()
	}
	close(tg.release)
	<-done

	assert.True(t, free, "отправка алерта владельцу держит мьютекс платежей человека")
}

func TestАлертВладельцуНеДержитМьютексПлатежей(t *testing.T) {
	mismatchSucceeded := func(_ int, id string) (int, string) {
		return http.StatusOK, yooPaymentBody(id, "succeeded", "", 400, "other-shop", "RUB")
	}
	plategaCallback := func(env *edgeEnv, id int64, status string) func() {
		ext := env.externalID(t, id)
		return func() {
			_ = env.bot.PaymentCallbackHandler().HandlePaymentCallback(
				platega.CallbackPayload{ID: ext, Amount: 400, Currency: "RUB", Status: status})
		}
	}

	cases := map[string]func(t *testing.T, env *edgeEnv) func(){
		"callback: непринятая оплата": func(t *testing.T, env *edgeEnv) func() {
			id := env.pending(t, paymentprovider.Platega, time.Hour)
			require.NoError(t, env.db.UpdatePaymentStatus(id, "expired"))
			return plategaCallback(env, id, platega.StatusConfirmed)
		},
		"callback: активация невозможна": func(t *testing.T, env *edgeEnv) func() {
			id := env.pending(t, paymentprovider.Platega, time.Hour)
			require.NoError(t, env.db.DeleteUser(edgeUserID))
			return plategaCallback(env, id, platega.StatusConfirmed)
		},
		"callback: chargeback": func(t *testing.T, env *edgeEnv) func() {
			id := env.pending(t, paymentprovider.Platega, time.Hour)
			require.NoError(t, env.db.ConfirmPayment(id))
			return plategaCallback(env, id, platega.StatusChargebacked)
		},
		"вебхук: воскрешённый платёж": func(t *testing.T, env *edgeEnv) func() {
			env.yoo.onGet = yooSays("succeeded", "")
			id := env.pending(t, paymentprovider.YooKassa, time.Hour)
			require.NoError(t, env.db.UpdatePaymentStatus(id, "expired"))
			ext := env.externalID(t, id)
			return func() { _ = env.bot.HandleYooKassaWebhook("payment.succeeded", ext) }
		},
		"вебхук: несовпадение": func(t *testing.T, env *edgeEnv) func() {
			env.yoo.onGet = mismatchSucceeded
			ext := env.externalID(t, env.pending(t, paymentprovider.YooKassa, time.Hour))
			return func() { _ = env.bot.HandleYooKassaWebhook("payment.succeeded", ext) }
		},
		"сверка: несовпадение": func(t *testing.T, env *edgeEnv) func() {
			env.yoo.onGet = mismatchSucceeded
			id := env.pending(t, paymentprovider.YooKassa, time.Hour)
			return func() { env.bot.reconcilePendingPayment(id, time.Now().UTC(), "scheduler") }
		},
		"ручная проверка: несовпадение": func(t *testing.T, env *edgeEnv) func() {
			env.yoo.onGet = mismatchSucceeded
			env.pending(t, paymentprovider.YooKassa, time.Hour)
			return func() { _, _ = env.bot.checkPaymentStatus(edgeUserID) }
		},
		"retry активации: активация невозможна": func(t *testing.T, env *edgeEnv) func() {
			id := env.pending(t, paymentprovider.Platega, time.Hour)
			require.NoError(t, env.db.ConfirmPayment(id))
			require.NoError(t, env.db.UpdatePaymentStatus(id, "confirmed_not_activated"))
			require.NoError(t, env.db.DeleteUser(edgeUserID))
			return func() { env.bot.retryConfirmedPaymentActivation(id, "scheduler") }
		},
	}
	for name, prepare := range cases {
		t.Run(name, func(t *testing.T) {
			env := newEdgeEnv(t)
			run := prepare(t, env)
			assertAlertLeavesMutexFree(t, env.bot, edgeUserID, run)
		})
	}
}

func TestАвтосписание_АлертВладельцуНеДержитМьютексПлатежей(t *testing.T) {
	stub := &arEdgeStub{expireAt: time.Now().UTC().Add(6 * time.Hour), responses: []string{
		autorenewMismatchBody("yo-mm-owner-mutex", "succeeded"),
	}}
	b, _, _ := setupAutorenewEdgeBot(t, stub)

	assertAlertLeavesMutexFree(t, b, arEdgeUserID, func() { b.runAutorenewCharges(time.Now().UTC()) })
}

// Шов userRef сообщает владельцу о сбое связки (здесь — неоднозначное
// совпадение в панели 3.x) и под мьютексом платежей делает это тоже после Unlock.
func TestАлертОСбоеСвязкиНеДержитМьютексПлатежей(t *testing.T) {
	const telegramID = int64(5031)
	ambiguous := func(r *http.Request) (*http.Response, error) {
		return panelJSON(fmt.Sprintf(`{"response":{"users":[{"id":7,"telegramId":%d},{"id":8,"telegramId":%d}],"hasMore":true}}`,
			telegramID, telegramID)), nil
	}
	cases := map[string]func(t *testing.T, b *Bot) func(){
		"перевыпуск ссылки": func(t *testing.T, b *Bot) func() {
			return func() { _, _ = b.applyRevoke(telegramID) }
		},
		"продление админом": func(t *testing.T, b *Bot) func() {
			return func() { _, _ = b.applyAdminExtend(telegramID) }
		},
		"retry активации": func(t *testing.T, b *Bot) func() {
			ext := "platega-ambiguous"
			id, err := b.db.CreatePayment(&database.Payment{
				TelegramID: telegramID, Amount: 400, PaymentMethod: paymentprovider.Platega, Status: "pending",
				Provider: paymentprovider.Platega, ProviderPaymentID: &ext, PlategaTransactionID: &ext,
			})
			require.NoError(t, err)
			require.NoError(t, b.db.ConfirmPayment(id))
			require.NoError(t, b.db.UpdatePaymentStatus(id, "confirmed_not_activated"))
			return func() { b.retryConfirmedPaymentActivation(id, "scheduler") }
		},
	}
	for name, prepare := range cases {
		t.Run(name, func(t *testing.T) {
			b, db := newUserRefBot(t, t.TempDir()+"/ambiguous.db", remnawave.APIVersionV3, ambiguous)
			b.paymentRetryDelays = []time.Duration{time.Hour}
			_, err := db.CreateUser(telegramID, "dup", "Dup", strPtrTest("uuid-5031"), nil, intPtrTest(400), nil)
			require.NoError(t, err)
			run := prepare(t, b)
			assertAlertLeavesMutexFree(t, b, telegramID, run)
		})
	}
}
