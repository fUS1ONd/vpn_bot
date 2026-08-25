package bot

import (
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/fus1ond/vpn_bot/internal/config"
	"github.com/fus1ond/vpn_bot/internal/database"
	"github.com/fus1ond/vpn_bot/internal/paymentprovider"
	"github.com/fus1ond/vpn_bot/internal/remnawave"
	"github.com/fus1ond/vpn_bot/internal/yookassa"
)

// Краевые случаи автопродления: касса ответила неудобно, панель легла между
// списанием и активацией, у человека открыта ссылка на ручную оплату.

const arEdgeUserID = int64(7101)

// kassaCall — одно обращение к кассе. Ключ идемпотентности — единственная
// защита от второго списания, поэтому запоминается отдельно.
type kassaCall struct {
	Key  string
	Body map[string]any
}

// arEdgeStub подменяет панель и кассу: умеет ронять активацию после успешного
// списания и запоминать, что именно ушло в кассу.
type arEdgeStub struct {
	mu        sync.Mutex
	calls     []kassaCall
	responses []string

	expireAt time.Time
	status   string

	// panelPatchStatus — код ответа панели на PATCH /api/users. 500 означает
	// «деньги приняты, продлить не смогли».
	panelPatchStatus int

	// transportErr — касса недоступна: ответа нет вообще.
	transportErr bool
}

func (s *arEdgeStub) record(call kassaCall) int {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.calls = append(s.calls, call)
	return len(s.calls) - 1
}

// healPanel поднимает панель обратно.
func (s *arEdgeStub) healPanel() {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.panelPatchStatus = 0
}

func (s *arEdgeStub) patchStatus() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.panelPatchStatus
}

func (s *arEdgeStub) callCount() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return len(s.calls)
}

func (s *arEdgeStub) call(i int) kassaCall {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.calls[i]
}

func setupAutorenewEdgeBot(t *testing.T, stub *arEdgeStub) (*Bot, *database.DB, *telegramCapture) {
	b, db, _, capture := setupAutorenewEdgeBotAt(t, stub)
	return b, db, capture
}

// setupAutorenewEdgeBotAt отдаёт путь к файлу БД: часть случаев живёт во
// времени, и подвинуть его можно только прямым UPDATE.
func setupAutorenewEdgeBotAt(t *testing.T, stub *arEdgeStub) (*Bot, *database.DB, string, *telegramCapture) {
	t.Helper()
	dbPath := t.TempDir() + "/autorenew_edge.db"
	db, err := database.New(dbPath)
	require.NoError(t, err)
	t.Cleanup(func() { db.Close() })

	b := &Bot{
		db: db,
		config: &config.Config{
			AdminID:             999999,
			TrialTrafficLimitGB: 1,
			TermsOfServiceURL:   "https://example.com/terms",
		},
		userStates: newStateMap(),
		remnawave:  newTestPanelClient(),
		shutdownCh: make(chan struct{}),
	}
	b.config.AutorenewEnabled = true
	b.config.YooKassaShopID = "shop-1"
	b.config.YooKassaSecretKey = "secret"
	b.config.YooKassaReturnURL = "https://t.me/test_bot"
	// Фоновый retry активации не должен шуметь в тесте: проверяем проход
	// scheduler, а не таймеры.
	b.paymentRetryDelays = []time.Duration{time.Hour}

	_, err = db.CreateUser(arEdgeUserID, "u", "U", strPtrTest("uuid-edge"), nil, intPtrTest(400), nil)
	require.NoError(t, err)
	require.NoError(t, db.SaveAutorenewMethod(arEdgeUserID, "pm-edge", "•••• 4242"))
	require.NoError(t, db.SetAutorenewEnabled(arEdgeUserID, true))

	if stub.status == "" {
		stub.status = remnawave.StatusActive
	}

	panel := newTestPanelClient()
	panel.SetHTTPClient(&http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
		if patch := stub.patchStatus(); r.Method == http.MethodPatch && patch != 0 {
			return &http.Response{StatusCode: patch, Header: make(http.Header),
				Body: io.NopCloser(strings.NewReader(`{"message":"panel is down"}`))}, nil
		}
		body, err := json.Marshal(map[string]any{"response": map[string]any{
			"uuid":            "uuid-edge",
			"shortUuid":       "short",
			"username":        "u",
			"status":          stub.status,
			"expireAt":        stub.expireAt.Format(time.RFC3339Nano),
			"subscriptionUrl": "https://sub.example.com/short",
		}})
		require.NoError(t, err)
		return &http.Response{StatusCode: http.StatusOK, Header: make(http.Header),
			Body: io.NopCloser(strings.NewReader(string(body)))}, nil
	})})
	b.remnawave = panel

	kassa := yookassa.NewClientWithBaseURL("shop-1", "secret", "https://yookassa.test")
	kassa.SetRetryBackoff(0)
	kassa.SetHTTPClient(&http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
		var body map[string]any
		if r.Body != nil {
			raw, err := io.ReadAll(r.Body)
			require.NoError(t, err)
			_ = json.Unmarshal(raw, &body)
		}
		idx := stub.record(kassaCall{Key: r.Header.Get("Idempotence-Key"), Body: body})
		if stub.transportErr {
			return nil, errors.New("TLS handshake timeout")
		}
		if idx >= len(stub.responses) {
			idx = len(stub.responses) - 1
		}
		return &http.Response{StatusCode: http.StatusOK, Header: make(http.Header),
			Body: io.NopCloser(strings.NewReader(stub.responses[idx]))}, nil
	})})
	b.yookassa = kassa

	capture := captureTelegram(t, b)
	return b, db, dbPath, capture
}

// agePendingPayments сдвигает срок жизни висящих платежей в прошлое.
func agePendingPayments(t *testing.T, dbPath string) {
	t.Helper()
	conn, err := sql.Open("sqlite3", dbPath)
	require.NoError(t, err)
	defer conn.Close()
	_, err = conn.Exec(`UPDATE payments SET expires_at = datetime('now', '-1 hour') WHERE status = 'pending'`)
	require.NoError(t, err)
}

func edgeSucceededBody(id string) string {
	return `{"id":"` + id + `","status":"succeeded","amount":{"value":"400.00","currency":"RUB"},
		"recipient":{"account_id":"shop-1"},"payment_method":{"type":"bank_card","id":"pm-edge","saved":true,"card":{"last4":"4242"}}}`
}

func edgePendingBody(id string) string {
	return `{"id":"` + id + `","status":"pending","amount":{"value":"400.00","currency":"RUB"},
		"recipient":{"account_id":"shop-1"},"payment_method":{"type":"bank_card","id":"pm-edge","saved":true,"card":{"last4":"4242"}}}`
}

// Касса списала в T−24ч, но активация упала: expireAt не сдвинулся, и попытка
// T−0 списала бы за тот же месяц второй раз. Сторожем должен быть признак «в
// этом цикле деньги уже приняты», а не сдвиг expireAt.
func TestAutorenewDoesNotChargeTwiceInOneCycleWhenActivationFailed(t *testing.T) {
	expireAt := time.Now().UTC().Add(6 * time.Hour)
	stub := &arEdgeStub{
		expireAt:         expireAt,
		panelPatchStatus: http.StatusInternalServerError,
		responses:        []string{edgeSucceededBody("yo-edge-1"), edgeSucceededBody("yo-edge-2")},
	}
	b, db, _ := setupAutorenewEdgeBot(t, stub)

	// Попытка T−24ч: касса деньги взяла, панель продлить не дала.
	b.runAutorenewCharges(time.Now().UTC())

	attempts, err := db.ListAutorenewAttempts(arEdgeUserID, expireAt)
	require.NoError(t, err)
	require.Len(t, attempts, 1)
	require.Equal(t, database.AutorenewOutcomeSuccess, attempts[0].Outcome, "касса подтвердила списание")
	require.NotNil(t, attempts[0].PaymentID)
	paid, err := db.GetPaymentByID(*attempts[0].PaymentID)
	require.NoError(t, err)
	require.Equal(t, "confirmed_not_activated", paid.Status, "деньги приняты, подписка не продлена")

	// Попытка T−0 того же цикла: expireAt не сдвинулся, потому что активация
	// не прошла.
	b.runAutorenewCharges(expireAt.Add(time.Minute))

	require.Equal(t, 1, stub.callCount(),
		"в цикле, за который деньги уже приняты кассой, второго обращения к кассе быть не должно")
}

// Касса ответила pending: платёж добирается вебхуком, но вебхук ищет его только
// по id кассы. Не записать id — значит потерять платёж: деньги приняты,
// подписка не выдана.
func TestAutorenewPendingChargeStaysMatchableForWebhook(t *testing.T) {
	expireAt := time.Now().UTC().Add(6 * time.Hour)
	stub := &arEdgeStub{
		expireAt:  expireAt,
		responses: []string{edgePendingBody("yo-edge-p"), edgeSucceededBody("yo-edge-p")},
	}
	b, db, capture := setupAutorenewEdgeBot(t, stub)

	b.runAutorenewCharges(time.Now().UTC())

	stored, err := db.GetPaymentByProviderPaymentID(paymentprovider.YooKassa, "yo-edge-p")
	require.NoError(t, err)
	require.NotNil(t, stored, "платёж автосписания обязан быть найден по id кассы, иначе вебхук по нему потеряется")

	require.NoError(t, b.HandleYooKassaWebhook("payment.succeeded", "yo-edge-p"))
	require.Empty(t, capture.matching("не сопоставлено"),
		"оплата по автосписанию не должна превращаться в несопоставленное событие")
}

// Ручная оплата не должна переиспользовать запись автосписания: её ключ выписан
// под запрос с payment_method_id, и по нему касса откажет обычному платежу —
// человек не смог бы оплатить вовсе.
func TestManualPaymentDoesNotReuseIdempotenceKeyOfAutorenewCharge(t *testing.T) {
	expireAt := time.Now().UTC().Add(6 * time.Hour)
	stub := &arEdgeStub{
		expireAt: expireAt,
		responses: []string{
			edgePendingBody("yo-edge-stuck"),
			`{"id":"yo-edge-manual","status":"pending","amount":{"value":"400.00","currency":"RUB"},
			  "recipient":{"account_id":"shop-1"},"confirmation":{"confirmation_url":"https://yookassa.test/pay/manual"}}`,
		},
	}
	b, _, _ := setupAutorenewEdgeBot(t, stub)

	b.runAutorenewCharges(time.Now().UTC())
	require.Equal(t, 1, stub.callCount())

	_, url, err := b.createPaymentForProvider(arEdgeUserID, paymentprovider.YooKassa)
	require.NoError(t, err)
	require.NotEmpty(t, url, "человеку нужна рабочая ссылка на оплату")

	require.Equal(t, 2, stub.callCount())
	charge, manual := stub.call(0), stub.call(1)
	_, chargeHasConfirmation := charge.Body["confirmation"]
	_, manualHasConfirmation := manual.Body["confirmation"]
	require.False(t, chargeHasConfirmation, "списание по сохранённому способу идёт без confirmation")
	require.True(t, manualHasConfirmation, "ручная оплата идёт с confirmation")
	require.NotEqual(t, charge.Key, manual.Key,
		"ключ идемпотентности списания нельзя переиспользовать для запроса с другими параметрами")
}

// У человека открыта живая ссылка на ручную оплату, и в это окно приходит
// проход scheduler. Мьютекс не помогает: деньги по ссылке уходят в кассе.
// Списав сейчас, мы оставили бы действующую ссылку на второй платёж за месяц.
func TestAutorenewDoesNotLeaveLiveManualPaymentAfterCharging(t *testing.T) {
	expireAt := time.Now().UTC().Add(6 * time.Hour)
	stub := &arEdgeStub{expireAt: expireAt, responses: []string{edgeSucceededBody("yo-edge-race")}}
	b, db, _ := setupAutorenewEdgeBot(t, stub)

	// Ссылка на ручную оплату, выданная минуту назад и живая ещё час.
	linkExpiry := time.Now().UTC().Add(time.Hour)
	redirect := "https://yookassa.test/pay/manual-live"
	manualExternalID := "yo-edge-manual-live"
	manualKey := "manual-key"
	_, err := db.CreatePayment(&database.Payment{
		TelegramID: arEdgeUserID, Amount: 400, PaymentMethod: paymentprovider.YooKassa,
		Status: "pending", Provider: paymentprovider.YooKassa,
		ProviderPaymentID: &manualExternalID, ProviderRequestKey: &manualKey,
		RedirectURL: &redirect, ExpiresAt: &linkExpiry,
	})
	require.NoError(t, err)

	b.runAutorenewCharges(time.Now().UTC())

	pending, err := db.GetPendingPayment(arEdgeUserID)
	require.NoError(t, err)
	if stub.callCount() > 0 {
		require.Nil(t, pending,
			"после автосписания живой ссылки на ручную оплату того же месяца остаться не должно")
	}
}

// Первая попытка не получила ответа: деньги могли уйти. Защита — пойти в T−0 по
// тому же ключу идемпотентности, и она обязана пережить протухание записи:
// между попытками сутки, а живёт запись час.
func TestAutorenewSecondAttemptKeepsIdempotenceKeyOfUnresolvedFirst(t *testing.T) {
	expireAt := time.Now().UTC().Add(6 * time.Hour)
	stub := &arEdgeStub{expireAt: expireAt, transportErr: true}
	b, db, dbPath, _ := setupAutorenewEdgeBotAt(t, stub)

	// Попытка T−24ч: ответа нет, исход неизвестен.
	b.runAutorenewCharges(time.Now().UTC())
	require.Positive(t, stub.callCount())
	attempts, err := db.ListAutorenewAttempts(arEdgeUserID, expireAt)
	require.NoError(t, err)
	require.Len(t, attempts, 1)
	require.Equal(t, database.AutorenewOutcomeUnknown, attempts[0].Outcome)

	// Между попытками прошли сутки, и шаг протухания их заметил.
	agePendingPayments(t, dbPath)
	_, err = db.ExpireOldPendingPayments()
	require.NoError(t, err)

	firstCalls := stub.callCount()
	b.runAutorenewCharges(expireAt.Add(time.Minute))
	require.Greater(t, stub.callCount(), firstCalls, "вторая попытка состоялась")

	require.Equal(t, stub.call(0).Key, stub.call(firstCalls).Key,
		"пока исход первого обращения неизвестен, второе обязано идти по тому же ключу идемпотентности")
}

// Автосписание прошло при лежащей панели, и подписку продлил штатный retry.
// Текст обязан остаться текстом автопродления: «Оплата прошла» подразумевает
// действие пользователя, а «я не платил» — прямая дорога к chargeback.
func TestAutorenewRetryDoesNotReportPaymentAsUserAction(t *testing.T) {
	expireAt := time.Now().UTC().Add(6 * time.Hour)
	stub := &arEdgeStub{
		expireAt:         expireAt,
		panelPatchStatus: http.StatusInternalServerError,
		responses:        []string{edgeSucceededBody("yo-edge-retry")},
	}
	b, db, capture := setupAutorenewEdgeBot(t, stub)

	b.runAutorenewCharges(time.Now().UTC())

	attempts, err := db.ListAutorenewAttempts(arEdgeUserID, expireAt)
	require.NoError(t, err)
	require.Len(t, attempts, 1)
	require.NotNil(t, attempts[0].PaymentID)
	paid, err := db.GetPaymentByID(*attempts[0].PaymentID)
	require.NoError(t, err)
	require.Equal(t, "confirmed_not_activated", paid.Status)

	// Панель поднялась — плановый retry активации доводит платёж до конца.
	stub.healPanel()
	b.retryConfirmedNotActivated()

	updated, err := db.GetPaymentByID(paid.ID)
	require.NoError(t, err)
	require.Equal(t, "confirmed", updated.Status, "retry активации отработал")

	for _, m := range messagesTo(capture, arEdgeUserID) {
		require.NotContains(t, m.Text, "Оплата прошла",
			"человек ничего не оплачивал — про автосписание так писать нельзя")
	}
}

// messagesTo — сообщения конкретному человеку, без алертов владельцу.
func messagesTo(capture *telegramCapture, telegramID int64) []sentMessage {
	var out []sentMessage
	for _, m := range capture.all() {
		if m.ChatID == fmt.Sprint(telegramID) {
			out = append(out, m)
		}
	}
	return out
}

// Касса не ответила в окне T−24ч: попытка израсходована, пользователю не пишем.
// Если при этом молчат и expire_3d/expire_1d, человек с рабочей картой доходит
// до отключения без единого сообщения — и не проверит сам, ведь он специально
// включил автопродление, чтобы не думать.
func TestAutorenewFailureDoesNotLeaveUserWithoutAnyWarning(t *testing.T) {
	expireAt := time.Now().UTC().Add(20 * time.Hour)
	stub := &arEdgeStub{expireAt: expireAt, transportErr: true}
	b, _, capture := setupAutorenewEdgeBot(t, stub)

	// Окно T−24ч: касса недоступна, попытка израсходована молча.
	b.runAutorenewCharges(time.Now().UTC())
	require.Positive(t, stub.callCount(), "попытка списания состоялась")

	// Основной цикл того же прохода: подписке осталось меньше суток.
	ref, ok := b.resolveUserRef(arEdgeUserID)
	require.True(t, ok)
	b.processPaidUser(arEdgeUserID, ref, expireAt, time.Now().UTC())

	require.NotEmpty(t, messagesTo(capture, arEdgeUserID),
		"списание не прошло и предупреждение подавлено — человек остаётся без единого сообщения")
}

// Способ пропал, согласие намеренно осталось живым и оживёт при следующей
// оплате картой. Значит, выключить его человек должен мочь в любом состоянии,
// иначе он уходит спокойным, а списания потом возобновляются.
func TestUserCanWithdrawAutorenewConsentWhenMethodGone(t *testing.T) {
	b, db := autorenewUIBot(t)
	require.NoError(t, db.SaveAutorenewMethod(1, "pm-1", "•••• 4242"))
	require.NoError(t, db.SetAutorenewEnabled(1, true))
	require.NoError(t, db.ClearAutorenewMethod(1))

	view := b.autorenewViewFor(1, activeRemUser(10), priced(400))
	renewal, err := db.GetAutorenewal(1)
	require.NoError(t, err)
	require.True(t, renewal.IsEnabled(), "согласие живо и оживёт при следующей оплате картой")

	require.True(t, keyboardHasCallback(AutorenewScreenKeyboard(view), cbAutorenewDisable),
		"живое согласие обязано отзываться, даже когда Способа нет")
}

// То же со стороны админа: человек с проблемой пишет в поддержку, а не ищет
// кнопку. Кнопка выключения обязана быть привязана к живому согласию, а не к
// состоянию «включено».
func TestAdminCanDisableAutorenewConsentWhenMethodGone(t *testing.T) {
	b, db := autorenewUIBot(t)
	require.NoError(t, db.SaveAutorenewMethod(1, "pm-1", "•••• 4242"))
	require.NoError(t, db.SetAutorenewEnabled(1, true))
	require.NoError(t, db.ClearAutorenewMethod(1))

	// Карточка собирается боевым путём, а не пересборкой связки в тесте:
	// разъехаться могут именно продовые строка и кнопка.
	_, err := db.CreateUser(1, "u", "U", strPtrTest("uuid-admin-ar"), nil, intPtrTest(400), nil)
	require.NoError(t, err)
	stubActivePanelUser(t, b, "uuid-admin-ar", 10)

	_, keyboard, err := b.buildAdminUserInfo(1)
	require.NoError(t, err)

	require.True(t, keyboardHasCallback(keyboard, cbAdminAutorenewOff),
		"админ обязан уметь выключить живое согласие, даже когда Способа нет")
}
