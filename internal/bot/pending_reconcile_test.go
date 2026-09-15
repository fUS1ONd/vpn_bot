package bot

import (
	"fmt"
	"io"
	"net/http"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/fus1ond/vpn_bot/internal/config"
	"github.com/fus1ond/vpn_bot/internal/database"
	"github.com/fus1ond/vpn_bot/internal/paymentprovider"
	"github.com/fus1ond/vpn_bot/internal/yookassa"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const reconcileTestUserID = int64(42)

// yooKassaStub — касса, которая отвечает на создание платежа и на запрос его
// состояния. Вебхуков она не шлёт вовсе: ровно тот случай, от которого защищает сверка.
type yooKassaStub struct {
	mu         sync.Mutex
	code       int    // код ответа на GET; 0 — успех
	status     string // статус платежа в кассе
	capturedAt string // момент списания; пусто — поля нет
	gets       int
}

func (s *yooKassaStub) set(status, capturedAt string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.status, s.capturedAt = status, capturedAt
}

func (s *yooKassaStub) getCount() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.gets
}

func (s *yooKassaStub) client() *yookassa.Client {
	c := yookassa.NewClientWithBaseURL("shop", "secret", "https://yookassa.test")
	c.SetRetryBackoff(0)
	c.SetHTTPClient(&http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
		if r.Method == http.MethodPost {
			return jsonResponse(http.StatusOK, `{"id":"yoo-new","status":"pending","amount":{"value":"400.00","currency":"RUB"},`+
				`"confirmation":{"confirmation_url":"https://pay.example/yoo-new"},"recipient":{"account_id":"shop"}}`), nil
		}
		s.mu.Lock()
		defer s.mu.Unlock()
		s.gets++
		if s.code != 0 {
			return jsonResponse(s.code, `{"type":"error","code":"not_found"}`), nil
		}
		captured := ""
		if s.capturedAt != "" {
			captured = fmt.Sprintf(`,"captured_at":%q`, s.capturedAt)
		}
		id := strings.TrimPrefix(r.URL.Path, "/v3/payments/")
		return jsonResponse(http.StatusOK, fmt.Sprintf(
			`{"id":%q,"status":%q,"amount":{"value":"400.00","currency":"RUB"},"recipient":{"account_id":"shop"}%s}`,
			id, s.status, captured)), nil
	})})
	return c
}

// newReconcileTestBot собирает бота с кассой-заглушкой и недоступной панелью:
// подтверждённая оплата уходит в confirmed_not_activated — деньги зафиксированы,
// а активацию дальше повторяет существующий retry, который здесь не проверяется.
func newReconcileTestBot(t *testing.T, stub *yooKassaStub) (*Bot, *database.DB, *telegramCapture) {
	t.Helper()
	db, err := database.New(t.TempDir() + "/reconcile.db")
	require.NoError(t, err)
	t.Cleanup(func() { db.Close() })

	price := 400
	_, err = db.CreateUser(reconcileTestUserID, "payer", "Payer", strPtrTest("uuid-42"), nil, &price, nil)
	require.NoError(t, err)

	panel := newTestPanelClient()
	panel.SetHTTPClient(&http.Client{Transport: roundTripFunc(func(*http.Request) (*http.Response, error) {
		return &http.Response{StatusCode: http.StatusInternalServerError, Body: io.NopCloser(strings.NewReader(`{}`)), Header: make(http.Header)}, nil
	})})

	shutdown := make(chan struct{})
	t.Cleanup(func() { close(shutdown) })

	b := &Bot{
		db:                 db,
		config:             &config.Config{AdminID: 999, YooKassaShopID: "shop", YooKassaReturnURL: "https://t.me/testbot"},
		userStates:         newStateMap(),
		remnawave:          panel,
		yookassa:           stub.client(),
		shutdownCh:         shutdown,
		paymentRetryDelays: []time.Duration{time.Hour},
	}
	capture := captureTelegram(t, b)
	return b, db, capture
}

// pendingYooKassaPayment создаёт платёж, созданный age назад и так и не получивший вебхук.
func pendingYooKassaPayment(t *testing.T, db *database.DB, age time.Duration) int64 {
	t.Helper()
	externalID := fmt.Sprintf("yoo-%d", time.Now().UnixNano())
	id, err := db.CreatePayment(&database.Payment{
		TelegramID: reconcileTestUserID, Amount: 400, PaymentMethod: "yookassa", Status: "pending",
		Provider: paymentprovider.YooKassa, ProviderPaymentID: &externalID,
	})
	require.NoError(t, err)
	_, err = db.Conn().Exec(`UPDATE payments SET created_at = ? WHERE id = ?`,
		time.Now().UTC().Add(-age).Format("2006-01-02 15:04:05"), id)
	require.NoError(t, err)
	return id
}

func paymentStatus(t *testing.T, db *database.DB, id int64) string {
	t.Helper()
	p, err := db.GetPaymentByID(id)
	require.NoError(t, err)
	require.NotNil(t, p)
	return p.Status
}

// Случай платежа №135: деньги списаны, вебхук не дошёл, человек кнопку не нажал.
func TestReconcileConfirmsPaidPaymentWhoseWebhookWasLost(t *testing.T) {
	stub := &yooKassaStub{}
	stub.set("succeeded", "2026-09-14T11:00:50.202Z")
	b, db, _ := newReconcileTestBot(t, stub)
	id := pendingYooKassaPayment(t, db, 20*time.Minute)

	b.reconcilePendingPayments(time.Now().UTC())

	stored, err := db.GetPaymentByID(id)
	require.NoError(t, err)
	assert.Equal(t, "confirmed_not_activated", stored.Status, "оплата принята, хотя вебхука не было")
	require.NotNil(t, stored.ConfirmedAt)
	require.NotNil(t, stored.ProviderPaidAt, "момент списания сохраняется для чека")
	assert.True(t, stored.ProviderPaidAt.Equal(time.Date(2026, 9, 14, 11, 0, 50, 202_000_000, time.UTC)))
}

// Первые 15 минут платёж ждёт вебхука и кассу не дёргает.
func TestReconcileLeavesFreshPaymentToWebhook(t *testing.T) {
	stub := &yooKassaStub{}
	stub.set("succeeded", "")
	b, db, _ := newReconcileTestBot(t, stub)
	id := pendingYooKassaPayment(t, db, 5*time.Minute)

	b.reconcilePendingPayments(time.Now().UTC())

	assert.Equal(t, "pending", paymentStatus(t, db, id))
	assert.Zero(t, stub.getCount(), "свежий платёж не сверяется")
}

func TestReconcileAppliesCancellationWhoseWebhookWasLost(t *testing.T) {
	stub := &yooKassaStub{}
	stub.set("canceled", "")
	b, db, capture := newReconcileTestBot(t, stub)
	id := pendingYooKassaPayment(t, db, 2*time.Hour)

	b.reconcilePendingPayments(time.Now().UTC())

	assert.Equal(t, "canceled", paymentStatus(t, db, id))
	assert.Len(t, capture.matching("Платёж отменён"), 1, "человек узнаёт об отмене, как и по вебхуку")
}

// Брошенный платёж внутри суток остаётся pending: касса может ещё провести оплату.
func TestReconcileKeepsUnpaidPaymentWithinADay(t *testing.T) {
	stub := &yooKassaStub{}
	stub.set("pending", "")
	b, db, _ := newReconcileTestBot(t, stub)
	id := pendingYooKassaPayment(t, db, 3*time.Hour)

	b.reconcilePendingPayments(time.Now().UTC())

	assert.Equal(t, "pending", paymentStatus(t, db, id))
	assert.Equal(t, 1, stub.getCount())
}

func TestReconcileExpiresPaymentUnresolvedForADay(t *testing.T) {
	stub := &yooKassaStub{}
	stub.set("pending", "")
	b, db, _ := newReconcileTestBot(t, stub)
	id := pendingYooKassaPayment(t, db, 25*time.Hour)

	b.reconcilePendingPayments(time.Now().UTC())

	assert.Equal(t, "expired", paymentStatus(t, db, id))
	assert.Equal(t, 1, stub.getCount(), "перед закрытием касса спрошена")
}

// Последняя сверка перед закрытием: оплаченный платёж не закрывается по возрасту.
func TestReconcileAcceptsPaymentFoundPaidAtDeadline(t *testing.T) {
	stub := &yooKassaStub{}
	stub.set("succeeded", "2026-09-14T11:00:50Z")
	b, db, _ := newReconcileTestBot(t, stub)
	id := pendingYooKassaPayment(t, db, 25*time.Hour)

	b.reconcilePendingPayments(time.Now().UTC())

	assert.Equal(t, "confirmed_not_activated", paymentStatus(t, db, id))
}

// Платёж, которого касса не знает (старый магазин или ключ), не сверяется вечно.
func TestReconcileExpiresPaymentProviderCannotFindAfterADay(t *testing.T) {
	stub := &yooKassaStub{code: http.StatusNotFound}
	b, db, _ := newReconcileTestBot(t, stub)
	id := pendingYooKassaPayment(t, db, 25*time.Hour)

	b.reconcilePendingPayments(time.Now().UTC())

	assert.Equal(t, "expired", paymentStatus(t, db, id))
}

func TestReconcileKeepsPaymentWhenProviderUnavailable(t *testing.T) {
	stub := &yooKassaStub{code: http.StatusInternalServerError}
	b, db, _ := newReconcileTestBot(t, stub)
	id := pendingYooKassaPayment(t, db, time.Hour)

	b.reconcilePendingPayments(time.Now().UTC())

	assert.Equal(t, "pending", paymentStatus(t, db, id), "сбой кассы внутри суток — повод спросить позже, а не закрывать")
}

// Срок жизни, который назвал провайдер, тоже не закрывает платёж вслепую.
func TestReconcileChecksProviderBeforeHonouringProviderExpiry(t *testing.T) {
	stub := &yooKassaStub{}
	stub.set("succeeded", "")
	b, db, _ := newReconcileTestBot(t, stub)
	id := pendingYooKassaPayment(t, db, 20*time.Minute)
	past := time.Now().UTC().Add(-time.Minute)
	p, err := db.GetPaymentByID(id)
	require.NoError(t, err)
	require.NoError(t, db.SetProviderPaymentDetails(id, *p.ProviderPaymentID, "https://pay.example", &past))

	b.reconcilePendingPayments(time.Now().UTC())

	assert.Equal(t, "confirmed_not_activated", paymentStatus(t, db, id))
}

// Истёкшая ссылка платёж не закрывает: провайдер ещё может провести оплату.
func TestReconcileKeepsPaymentPastProviderExpiry(t *testing.T) {
	stub := &yooKassaStub{}
	stub.set("pending", "")
	b, db, _ := newReconcileTestBot(t, stub)
	id := pendingYooKassaPayment(t, db, 20*time.Minute)
	past := time.Now().UTC().Add(-time.Minute)
	p, err := db.GetPaymentByID(id)
	require.NoError(t, err)
	require.NoError(t, db.SetProviderPaymentDetails(id, *p.ProviderPaymentID, "https://pay.example", &past))

	b.reconcilePendingPayments(time.Now().UTC())

	assert.Equal(t, "pending", paymentStatus(t, db, id))
}

// Уже подтверждённый вебхуком платёж сверка не трогает.
func TestReconcileIgnoresPaymentsNoLongerPending(t *testing.T) {
	stub := &yooKassaStub{}
	stub.set("succeeded", "")
	b, db, _ := newReconcileTestBot(t, stub)
	id := pendingYooKassaPayment(t, db, time.Hour)
	require.NoError(t, db.ConfirmPayment(id))

	b.reconcilePendingPayments(time.Now().UTC())

	assert.Equal(t, "confirmed", paymentStatus(t, db, id))
	assert.Zero(t, stub.getCount())
}

// Новый платёж получает свою первую сверку сам, не дожидаясь тика планировщика.
func TestNewPaymentIsReconciledAfterFirstCheckDelay(t *testing.T) {
	stub := &yooKassaStub{}
	stub.set("succeeded", "")
	b, db, _ := newReconcileTestBot(t, stub)
	b.pendingCheckDelay = 20 * time.Millisecond

	payment, _, err := b.createPaymentForProvider(reconcileTestUserID, paymentprovider.YooKassa)
	require.NoError(t, err)

	require.Eventually(t, func() bool {
		return paymentStatus(t, db, payment.ID) == "confirmed_not_activated"
	}, 2*time.Second, 10*time.Millisecond, "таймер первой сверки подтверждает оплату без вебхука")
}

// Повторное нажатие «Оплатить» возвращает ту же ссылку и не плодит таймеры.
func TestReusedPendingPaymentDoesNotScheduleSecondCheck(t *testing.T) {
	stub := &yooKassaStub{}
	stub.set("pending", "")
	b, _, _ := newReconcileTestBot(t, stub)
	b.pendingCheckDelay = 20 * time.Millisecond

	first, _, err := b.createPaymentForProvider(reconcileTestUserID, paymentprovider.YooKassa)
	require.NoError(t, err)
	second, _, err := b.createPaymentForProvider(reconcileTestUserID, paymentprovider.YooKassa)
	require.NoError(t, err)
	require.Equal(t, first.ID, second.ID)

	require.Eventually(t, func() bool { return stub.getCount() >= 1 }, 2*time.Second, 10*time.Millisecond)
	time.Sleep(100 * time.Millisecond)
	assert.Equal(t, 1, stub.getCount(), "один платёж — одна первая сверка")
}
