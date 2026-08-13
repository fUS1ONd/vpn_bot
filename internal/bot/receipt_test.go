package bot

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/fus1ond/vpn_bot/internal/config"
	"github.com/fus1ond/vpn_bot/internal/database"
	"github.com/fus1ond/vpn_bot/internal/moynalog"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// fnsStub — подменённый кабинет ФНС: запоминает пробитые чеки и отвечает по сценарию.
type fnsStub struct {
	mu       sync.Mutex
	created  []map[string]any // тела запросов на создание чека
	incomes  []map[string]any // что вернуть в списке доходов
	failWith func(attempt int) (int, string)
}

func (s *fnsStub) createdCount() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return len(s.created)
}

func (s *fnsStub) lastCreated(t *testing.T) map[string]any {
	t.Helper()
	s.mu.Lock()
	defer s.mu.Unlock()
	require.NotEmpty(t, s.created, "ожидали хотя бы одну попытку пробития")
	return s.created[len(s.created)-1]
}

func newReceiptTestBot(t *testing.T, stub *fnsStub) (*Bot, *database.DB, *telegramCapture) {
	t.Helper()
	db, err := database.New(t.TempDir() + "/receipts.db")
	require.NoError(t, err)
	t.Cleanup(func() { db.Close() })

	client := moynalog.NewClientWithBaseURL("123456789012", "secret", "https://lknpd.test/api/v1")
	client.SetHTTPClient(&http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
		switch {
		case r.URL.Path == "/api/v1/auth/lkfl":
			return jsonResponse(http.StatusOK, `{"token":"tok"}`), nil
		case r.URL.Path == "/api/v1/income":
			stub.mu.Lock()
			attempt := len(stub.created) + 1
			var body map[string]any
			require.NoError(t, json.NewDecoder(r.Body).Decode(&body))
			stub.created = append(stub.created, body)
			fail := stub.failWith
			stub.mu.Unlock()
			if fail != nil {
				if status, payload := fail(attempt); status != 0 {
					return jsonResponse(status, payload), nil
				}
			}
			return jsonResponse(http.StatusOK, fmt.Sprintf(`{"approvedReceiptUuid":"202auto%d"}`, attempt)), nil
		case r.URL.Path == "/api/v1/incomes":
			stub.mu.Lock()
			payload, err := json.Marshal(map[string]any{"content": stub.incomes})
			stub.mu.Unlock()
			require.NoError(t, err)
			return jsonResponse(http.StatusOK, string(payload)), nil
		}
		return nil, fmt.Errorf("unexpected path %s", r.URL.Path)
	})})

	b := &Bot{
		db:         db,
		config:     &config.Config{AdminID: 999, MoynalogServiceName: "Sarvizza - Подписка на месяц"},
		userStates: newStateMap(),
		moynalog:   client,
		shutdownCh: make(chan struct{}),
	}
	capture := captureTelegram(t, b)
	return b, db, capture
}

func jsonResponse(status int, body string) *http.Response {
	return &http.Response{
		StatusCode: status,
		Body:       io.NopCloser(strings.NewReader(body)),
		Header:     make(http.Header),
	}
}

// confirmedPayment создаёт подтверждённый платёж указанного провайдера.
func confirmedPayment(t *testing.T, db *database.DB, provider string, amount int, confirmedAt time.Time) int64 {
	t.Helper()
	id, err := db.CreatePayment(&database.Payment{
		TelegramID: 42, Amount: amount, PaymentMethod: provider, Status: "pending", Provider: provider,
	})
	require.NoError(t, err)
	_, err = db.Conn().Exec(`UPDATE payments SET status = 'confirmed', confirmed_at = ? WHERE id = ?`, confirmedAt.UTC(), id)
	require.NoError(t, err)
	return id
}

func TestReceiptIssuedForConfirmedPayment(t *testing.T) {
	stub := &fnsStub{}
	b, db, _ := newReceiptTestBot(t, stub)
	id := confirmedPayment(t, db, "yookassa", 400, time.Date(2026, 8, 7, 21, 43, 31, 0, time.UTC))

	b.processReceipt(id)

	receipt, err := db.GetReceipt(id)
	require.NoError(t, err)
	require.NotNil(t, receipt)
	assert.Equal(t, database.ReceiptStateCreated, receipt.State)
	require.NotNil(t, receipt.ReceiptUUID)
	assert.Equal(t, "202auto1", *receipt.ReceiptUUID)

	body := stub.lastCreated(t)
	services := body["services"].([]any)
	name := services[0].(map[string]any)["name"].(string)
	assert.Equal(t, fmt.Sprintf("Sarvizza - Подписка на месяц (%s)", receipt.Marker), name,
		"наименование — базовая часть из окружения плюс метка")
	assert.Equal(t, 400.0, services[0].(map[string]any)["amount"], "сумма чека — полная сумма платежа")
	assert.Equal(t, "400", body["totalAmount"])

	client := body["client"].(map[string]any)
	assert.Nil(t, client["displayName"], "покупатель — физлицо без имени")
	assert.Nil(t, client["contactPhone"], "и без телефона")
}

// Платёж 96 подтверждён 7 августа по UTC, но в кабинете обязан лечь восьмым.
func TestReceiptOperationTimeIsMoscowRegardlessOfProcessTimezone(t *testing.T) {
	for _, tz := range []string{"UTC", "America/Los_Angeles", "Asia/Tokyo"} {
		t.Run(tz, func(t *testing.T) {
			t.Setenv("TZ", tz)
			for _, tc := range []struct {
				name        string
				confirmedAt time.Time
				want        string
			}{
				{"платёж 95", time.Date(2026, 8, 3, 23, 33, 1, 0, time.UTC), "2026-08-04T02:33:01.000+03:00"},
				{"платёж 96", time.Date(2026, 8, 7, 21, 43, 31, 0, time.UTC), "2026-08-08T00:43:31.000+03:00"},
			} {
				t.Run(tc.name, func(t *testing.T) {
					stub := &fnsStub{}
					b, db, _ := newReceiptTestBot(t, stub)
					id := confirmedPayment(t, db, "yookassa", 400, tc.confirmedAt)

					b.processReceipt(id)

					assert.Equal(t, tc.want, stub.lastCreated(t)["operationTime"])
				})
			}
		})
	}
}

func TestPlategaPaymentIsSkippedSilently(t *testing.T) {
	stub := &fnsStub{}
	b, db, capture := newReceiptTestBot(t, stub)
	id := confirmedPayment(t, db, "platega", 400, time.Now().UTC())

	b.processReceipt(id)

	assert.Zero(t, stub.createdCount(), "чеки по Platega не пробиваются")
	receipt, err := db.GetReceipt(id)
	require.NoError(t, err)
	assert.Nil(t, receipt, "и запись о чеке не заводится")
	assert.Empty(t, capture.all(), "решение владельца не повод тревожить владельца")
}

func TestTestPaymentIsIssuedLikeAnyOther(t *testing.T) {
	stub := &fnsStub{}
	b, db, _ := newReceiptTestBot(t, stub)
	id := confirmedPayment(t, db, "yookassa", 10, time.Now().UTC())
	_, err := db.Conn().Exec(`UPDATE payments SET is_test = 1 WHERE id = ?`, id)
	require.NoError(t, err)

	b.processReceipt(id)

	receipt, err := db.GetReceipt(id)
	require.NoError(t, err)
	require.NotNil(t, receipt)
	assert.Equal(t, database.ReceiptStateCreated, receipt.State,
		"деньги идут через боевой магазин — реестр должен сходиться без исключений")
}

func TestUnavailableTaxServiceLeavesReceiptUnfinishedAndSilent(t *testing.T) {
	stub := &fnsStub{failWith: func(int) (int, string) {
		return http.StatusInternalServerError, `{"code":"E","message":"сервис недоступен"}`
	}}
	b, db, capture := newReceiptTestBot(t, stub)
	id := confirmedPayment(t, db, "yookassa", 400, time.Now().UTC())

	b.processReceipt(id)

	receipt, err := db.GetReceipt(id)
	require.NoError(t, err)
	require.NotNil(t, receipt)
	assert.Equal(t, database.ReceiptStatePending, receipt.State)
	assert.Equal(t, 1, receipt.Attempts)
	require.NotNil(t, receipt.LastError)
	assert.Equal(t, "сервис недоступен", *receipt.LastError)
	assert.Empty(t, capture.all(), "недоступность ФНС владельца не будит")

	pending, err := db.PaymentsNeedingReceipt()
	require.NoError(t, err)
	require.Len(t, pending, 1, "недоделанный чек остаётся в очереди на добивание")
}

// Провал пробития не влияет ни на активацию подписки, ни на ответ вебхуку.
func TestPaymentPathSucceedsWhenTaxServiceIsDown(t *testing.T) {
	stub := &fnsStub{failWith: func(int) (int, string) {
		return http.StatusInternalServerError, `{"message":"лежит"}`
	}}
	b, db, _ := newReceiptTestBot(t, stub)
	id := confirmedPayment(t, db, "yookassa", 400, time.Now().UTC())
	payment, err := db.GetPaymentByID(id)
	require.NoError(t, err)

	handler := &paymentCallbackHandler{bot: b}
	handler.finalizeActivatedPayment(payment, false)
	b.waitReceipts()

	updated, err := db.GetPaymentByID(id)
	require.NoError(t, err)
	assert.Equal(t, "confirmed", updated.Status, "платёж остаётся подтверждённым")

	receipt, err := db.GetReceipt(id)
	require.NoError(t, err)
	require.NotNil(t, receipt)
	assert.Equal(t, database.ReceiptStatePending, receipt.State)
}

func TestPaymentPathIssuesReceipt(t *testing.T) {
	stub := &fnsStub{}
	b, db, _ := newReceiptTestBot(t, stub)
	id := confirmedPayment(t, db, "yookassa", 400, time.Now().UTC())
	payment, err := db.GetPaymentByID(id)
	require.NoError(t, err)

	handler := &paymentCallbackHandler{bot: b}
	handler.finalizeActivatedPayment(payment, false)
	b.waitReceipts()

	receipt, err := db.GetReceipt(id)
	require.NoError(t, err)
	require.NotNil(t, receipt)
	assert.Equal(t, database.ReceiptStateCreated, receipt.State)
	assert.Equal(t, 1, stub.createdCount())
}

func TestDisabledIntegrationIssuesNothing(t *testing.T) {
	stub := &fnsStub{}
	b, db, _ := newReceiptTestBot(t, stub)
	b.moynalog = nil
	id := confirmedPayment(t, db, "yookassa", 400, time.Now().UTC())

	b.processReceipt(id)
	b.issueReceiptAsync(id)
	b.waitReceipts()

	assert.Zero(t, stub.createdCount())
	receipt, err := db.GetReceipt(id)
	require.NoError(t, err)
	assert.Nil(t, receipt)
}

func TestUnreadableAnswerLeavesReceiptUnknown(t *testing.T) {
	stub := &fnsStub{failWith: func(int) (int, string) { return http.StatusOK, `<html>шлюз` }}
	b, db, _ := newReceiptTestBot(t, stub)
	id := confirmedPayment(t, db, "yookassa", 400, time.Now().UTC())

	b.processReceipt(id)

	receipt, err := db.GetReceipt(id)
	require.NoError(t, err)
	require.NotNil(t, receipt)
	assert.Equal(t, database.ReceiptStateUnknown, receipt.State,
		"потерянный ответ не даёт ни дубля, ни пропуска — только сверку")
}

func TestSchedulerFinishesReceiptLeftUnfinishedByPaymentPath(t *testing.T) {
	// Первая попытка (платёжный путь) падает, вторая (плановый проход) проходит.
	stub := &fnsStub{failWith: func(attempt int) (int, string) {
		if attempt == 1 {
			return http.StatusInternalServerError, `{"message":"лежит"}`
		}
		return 0, ""
	}}
	b, db, _ := newReceiptTestBot(t, stub)
	id := confirmedPayment(t, db, "yookassa", 400, time.Now().UTC())

	b.processReceipt(id)
	receipt, err := db.GetReceipt(id)
	require.NoError(t, err)
	require.Equal(t, database.ReceiptStatePending, receipt.State)

	b.issuePendingReceipts()

	receipt, err = db.GetReceipt(id)
	require.NoError(t, err)
	assert.Equal(t, database.ReceiptStateCreated, receipt.State)
	assert.Equal(t, 2, receipt.Attempts, "число попыток видно в базе")
	assert.Equal(t, 2, stub.createdCount())
}

func TestRetriesDoNotStopAfterRepeatedFailures(t *testing.T) {
	stub := &fnsStub{failWith: func(int) (int, string) {
		return http.StatusInternalServerError, `{"message":"лежит"}`
	}}
	b, db, _ := newReceiptTestBot(t, stub)
	id := confirmedPayment(t, db, "yookassa", 400, time.Now().UTC())

	for i := 0; i < 5; i++ {
		b.issuePendingReceipts()
	}

	receipt, err := db.GetReceipt(id)
	require.NoError(t, err)
	assert.Equal(t, 5, receipt.Attempts, "повторы не прекращаются после серии неудач")
	assert.Equal(t, database.ReceiptStatePending, receipt.State)
}

func TestConcurrentPaymentPathAndSchedulerIssueExactlyOneReceipt(t *testing.T) {
	stub := &fnsStub{}
	b, db, _ := newReceiptTestBot(t, stub)
	id := confirmedPayment(t, db, "yookassa", 400, time.Now().UTC())

	var wg sync.WaitGroup
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			if i%2 == 0 {
				b.processReceipt(id)
				return
			}
			b.issuePendingReceipts()
		}(i)
	}
	wg.Wait()

	assert.Equal(t, 1, stub.createdCount(), "два кодовых пути дают ровно один чек")
	receipt, err := db.GetReceipt(id)
	require.NoError(t, err)
	assert.Equal(t, database.ReceiptStateCreated, receipt.State)
}

func TestSeededManualReceiptsAreNotIssuedAgainByScheduler(t *testing.T) {
	stub := &fnsStub{}
	b, db, _ := newReceiptTestBot(t, stub)
	seedManualPaymentsForTest(t, db)

	b.issuePendingReceipts()

	assert.Zero(t, stub.createdCount(), "13 ручных чеков повторно не пробиваются")
}

func TestSchedulerPassIssuesReceiptsAlongsideItsOtherSteps(t *testing.T) {
	stub := &fnsStub{}
	b, db, _ := newReceiptTestBot(t, stub)
	b.remnawave = newTestPanelClient()
	b.remnawave.SetHTTPClient(&http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
		return jsonResponse(http.StatusOK, `{"response":{"users":[],"total":0}}`), nil
	})})
	id := confirmedPayment(t, db, "yookassa", 400, time.Now().UTC())

	b.runSubscriptionSchedulerPass()

	receipt, err := db.GetReceipt(id)
	require.NoError(t, err)
	require.NotNil(t, receipt)
	assert.Equal(t, database.ReceiptStateCreated, receipt.State)
}

// seedManualPaymentsForTest воспроизводит прод-ситуацию: 13 платежей, по которым
// владелец пробил чеки руками, и засев, связывающий их с кабинетом.
func seedManualPaymentsForTest(t *testing.T, db *database.DB) {
	t.Helper()
	for _, seed := range database.ManualReceiptSeeds() {
		_, err := db.Conn().Exec(
			`INSERT INTO payments (id, telegram_id, amount, payment_method, status, provider, confirmed_at)
			 VALUES (?, 42, ?, 'yookassa', 'confirmed', 'yookassa', ?)`,
			seed.PaymentID, seed.Amount, seed.ConfirmedAt,
		)
		require.NoError(t, err)
	}
	require.NoError(t, db.SeedManualReceipts())
}

// makeReceiptUnknown приводит чек в состояние «ответ потерян».
func makeReceiptUnknown(t *testing.T, b *Bot, db *database.DB, paymentID int64) *database.Receipt {
	t.Helper()
	b.processReceipt(paymentID)
	receipt, err := db.GetReceipt(paymentID)
	require.NoError(t, err)
	require.NotNil(t, receipt)
	require.Equal(t, database.ReceiptStateUnknown, receipt.State)
	return receipt
}

func TestReconcileFindsLostReceiptByMarkerAndDoesNotIssueSecond(t *testing.T) {
	stub := &fnsStub{failWith: func(attempt int) (int, string) {
		if attempt == 1 {
			return http.StatusOK, `<html>ответ потерялся`
		}
		return 0, ""
	}}
	b, db, _ := newReceiptTestBot(t, stub)
	id := confirmedPayment(t, db, "yookassa", 400, time.Now().UTC())

	receipt := makeReceiptUnknown(t, b, db, id)
	// Чек на самом деле создался — он лежит в кабинете с нашей меткой.
	stub.mu.Lock()
	stub.incomes = []map[string]any{{
		"approvedReceiptUuid": "202lost",
		"name":                "Sarvizza - Подписка на месяц (" + receipt.Marker + ")",
		"totalAmount":         400,
		"operationTime":       moynalog.FormatMoscow(receipt.OperationTime),
	}}
	stub.mu.Unlock()

	b.issuePendingReceipts()

	updated, err := db.GetReceipt(id)
	require.NoError(t, err)
	assert.Equal(t, database.ReceiptStateCreated, updated.State)
	require.NotNil(t, updated.ReceiptUUID)
	assert.Equal(t, "202lost", *updated.ReceiptUUID)
	assert.Equal(t, 1, stub.createdCount(), "второй чек не создаётся")
}

func TestReconcileIssuesAgainWhenNothingMatches(t *testing.T) {
	stub := &fnsStub{failWith: func(attempt int) (int, string) {
		if attempt == 1 {
			return http.StatusOK, `<html>ответ потерялся`
		}
		return 0, ""
	}}
	b, db, _ := newReceiptTestBot(t, stub)
	id := confirmedPayment(t, db, "yookassa", 400, time.Now().UTC())
	makeReceiptUnknown(t, b, db, id)

	// В кабинете есть чужой чек — но не наш.
	stub.mu.Lock()
	stub.incomes = []map[string]any{{"approvedReceiptUuid": "202other", "name": "Другое (zzz999)", "totalAmount": 400}}
	stub.mu.Unlock()

	b.issuePendingReceipts()

	updated, err := db.GetReceipt(id)
	require.NoError(t, err)
	assert.Equal(t, database.ReceiptStateCreated, updated.State)
	require.NotNil(t, updated.ReceiptUUID)
	assert.Equal(t, "202auto2", *updated.ReceiptUUID, "чек пробит заново")
}

func TestReconcileKeepsStateAndAlertsOnAmbiguousMatch(t *testing.T) {
	stub := &fnsStub{failWith: func(attempt int) (int, string) {
		if attempt == 1 {
			return http.StatusOK, `<html>ответ потерялся`
		}
		return 0, ""
	}}
	b, db, capture := newReceiptTestBot(t, stub)
	id := confirmedPayment(t, db, "yookassa", 400, time.Now().UTC())
	receipt := makeReceiptUnknown(t, b, db, id)

	name := "Sarvizza - Подписка на месяц (" + receipt.Marker + ")"
	stub.mu.Lock()
	stub.incomes = []map[string]any{
		{"approvedReceiptUuid": "202one", "name": name, "totalAmount": 400},
		{"approvedReceiptUuid": "202two", "name": name, "totalAmount": 400},
	}
	stub.mu.Unlock()

	b.issuePendingReceipts()

	updated, err := db.GetReceipt(id)
	require.NoError(t, err)
	assert.Equal(t, database.ReceiptStateUnknown, updated.State, "в неоднозначной ситуации бот ничего не решает")
	assert.Nil(t, updated.ReceiptUUID)
	assert.Equal(t, 1, stub.createdCount(), "и второй чек не пробивает")
	require.Len(t, capture.matching(receipt.Marker), 1, "владелец получает разбор на руки")
}

func TestReconcileKeepsUnknownWhenTaxServiceIsDown(t *testing.T) {
	stub := &fnsStub{failWith: func(attempt int) (int, string) {
		if attempt == 1 {
			return http.StatusOK, `<html>ответ потерялся`
		}
		return 0, ""
	}}
	b, db, _ := newReceiptTestBot(t, stub)
	id := confirmedPayment(t, db, "yookassa", 400, time.Now().UTC())
	makeReceiptUnknown(t, b, db, id)

	// Список доходов недоступен: судьба чека по-прежнему неизвестна.
	b.moynalog.SetHTTPClient(&http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
		if r.URL.Path == "/api/v1/auth/lkfl" {
			return jsonResponse(http.StatusOK, `{"token":"tok"}`), nil
		}
		return jsonResponse(http.StatusInternalServerError, `{"message":"лежит"}`), nil
	})})

	b.issuePendingReceipts()

	updated, err := db.GetReceipt(id)
	require.NoError(t, err)
	assert.Equal(t, database.ReceiptStateUnknown, updated.State,
		"пробить вслепую при неизвестной судьбе — значит рискнуть дублем")
}

func TestSummaryIsSilentWhenNothingIsPending(t *testing.T) {
	stub := &fnsStub{}
	b, db, capture := newReceiptTestBot(t, stub)
	id := confirmedPayment(t, db, "yookassa", 400, time.Now().UTC())

	b.issuePendingReceipts()

	receipt, err := db.GetReceipt(id)
	require.NoError(t, err)
	require.Equal(t, database.ReceiptStateCreated, receipt.State)
	assert.Empty(t, capture.all(), "когда непробитых нет, сообщений нет вообще")
}

func TestSummaryAppearsOnceADayWhilePendingReceiptsExist(t *testing.T) {
	stub := &fnsStub{failWith: func(int) (int, string) {
		return http.StatusInternalServerError, `{"message":"лежит"}`
	}}
	b, db, capture := newReceiptTestBot(t, stub)
	confirmedPayment(t, db, "yookassa", 400, time.Now().UTC())
	confirmedPayment(t, db, "yookassa", 450, time.Now().UTC().Add(-2*time.Hour))

	b.issuePendingReceipts()
	summaries := capture.matching("Не пробито чеков")
	require.Len(t, summaries, 1)
	assert.Contains(t, summaries[0].Text, "2")

	// Следующий проход через полчаса сводку не повторяет.
	b.issuePendingReceipts()
	assert.Len(t, capture.matching("Не пробито чеков"), 1)

	// А через сутки — повторяет.
	_, err := db.Conn().Exec(
		`UPDATE notifications_sent SET sent_at = datetime('now', '-25 hours') WHERE type = ?`,
		notificationReceiptsSummary,
	)
	require.NoError(t, err)
	b.issuePendingReceipts()
	assert.Len(t, capture.matching("Не пробито чеков"), 2)
}

func TestAuthErrorAlertsOnFirstAttempt(t *testing.T) {
	stub := &fnsStub{failWith: func(int) (int, string) {
		return http.StatusUnauthorized, `{"message":"неверный пароль"}`
	}}
	b, db, capture := newReceiptTestBot(t, stub)
	id := confirmedPayment(t, db, "yookassa", 400, time.Now().UTC())

	b.processReceipt(id)

	alerts := capture.matching("не принял вход")
	require.Len(t, alerts, 1, "пароль сам не починится — сообщаем с первой неудачи")
	assert.Contains(t, alerts[0].Text, "неверный пароль")
	assert.Equal(t, "999", alerts[0].ChatID)

	// Повторные проходы не превращают это в поток одинаковых сообщений.
	b.issuePendingReceipts()
	assert.Len(t, capture.matching("не принял вход"), 1)
}

func TestBadRequestAlertsWithTaxServiceMessage(t *testing.T) {
	stub := &fnsStub{failWith: func(int) (int, string) {
		return http.StatusBadRequest, `{"message":"сумма превышает лимит"}`
	}}
	b, db, capture := newReceiptTestBot(t, stub)
	id := confirmedPayment(t, db, "yookassa", 400, time.Now().UTC())

	b.processReceipt(id)

	alerts := capture.matching("отвергла чек")
	require.Len(t, alerts, 1)
	assert.Contains(t, alerts[0].Text, "сумма превышает лимит")
}

func TestUnavailableTaxServiceIsSilentForFirstDayThenReported(t *testing.T) {
	stub := &fnsStub{failWith: func(int) (int, string) {
		return http.StatusInternalServerError, `{"message":"лежит"}`
	}}
	b, db, capture := newReceiptTestBot(t, stub)
	id := confirmedPayment(t, db, "yookassa", 400, time.Now().UTC())

	b.issuePendingReceipts()
	assert.Empty(t, capture.matching("не пробит больше суток"), "в первые сутки недоступность ФНС не будит")

	// Платёж состарился — застрявший случай теряться не должен.
	_, err := db.Conn().Exec(`UPDATE payments SET confirmed_at = datetime('now', '-26 hours') WHERE id = ?`, id)
	require.NoError(t, err)
	b.issuePendingReceipts()

	stuck := capture.matching("не пробит больше суток")
	require.Len(t, stuck, 1)
	assert.Contains(t, stuck[0].Text, "лежит", "в сообщении виден текст последней ошибки")
}

func TestDisabledIntegrationWarnsOwnerOnceWhenReceiptsAreMissing(t *testing.T) {
	stub := &fnsStub{}
	b, db, capture := newReceiptTestBot(t, stub)
	b.moynalog = nil
	confirmedPayment(t, db, "yookassa", 400, time.Now().UTC())

	b.issuePendingReceipts()
	b.issuePendingReceipts()

	warnings := capture.matching("Интеграция с «Мой налог» выключена")
	require.Len(t, warnings, 1, "предупреждение разовое")
	assert.Zero(t, stub.createdCount())
}

func TestDisabledIntegrationIsSilentWithoutMissingReceipts(t *testing.T) {
	stub := &fnsStub{}
	b, _, capture := newReceiptTestBot(t, stub)
	b.moynalog = nil

	b.issuePendingReceipts()

	assert.Empty(t, capture.all(), "нечего пробивать — не о чем предупреждать")
}

// Оборванная связь при создании чека уводит в unknown, а не в слепой повтор:
// иначе самый вероятный «потерянный ответ» дал бы дубль в кабинете.
func TestBrokenConnectionOnCreateGoesThroughReconciliation(t *testing.T) {
	attempts := 0
	b, db, _ := newReceiptTestBot(t, &fnsStub{})
	b.moynalog.SetHTTPClient(&http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
		if r.URL.Path == "/api/v1/auth/lkfl" {
			return jsonResponse(http.StatusOK, `{"token":"tok"}`), nil
		}
		if r.URL.Path == "/api/v1/income" {
			attempts++
			return nil, fmt.Errorf("context deadline exceeded")
		}
		// Чек на самом деле создался — сверка найдёт его по метке.
		receipt, err := db.GetReceipt(1)
		require.NoError(t, err)
		return jsonResponse(http.StatusOK, fmt.Sprintf(
			`{"content":[{"approvedReceiptUuid":"202lost","name":"Подписка (%s)","totalAmount":400}]}`, receipt.Marker)), nil
	})})
	id := confirmedPayment(t, db, "yookassa", 400, time.Now().UTC())
	require.Equal(t, int64(1), id)

	b.processReceipt(id)
	receipt, err := db.GetReceipt(id)
	require.NoError(t, err)
	require.Equal(t, database.ReceiptStateUnknown, receipt.State)

	b.issuePendingReceipts()

	updated, err := db.GetReceipt(id)
	require.NoError(t, err)
	assert.Equal(t, database.ReceiptStateCreated, updated.State)
	require.NotNil(t, updated.ReceiptUUID)
	assert.Equal(t, "202lost", *updated.ReceiptUUID)
	assert.Equal(t, 1, attempts, "повторного пробития вслепую не было")
}

// Застрявший чек живёт неделями и переживает перезапуски — сообщение о нём нет.
func TestStuckReceiptAlertSurvivesRestart(t *testing.T) {
	stub := &fnsStub{failWith: func(int) (int, string) {
		return http.StatusInternalServerError, `{"message":"лежит"}`
	}}
	b, db, capture := newReceiptTestBot(t, stub)
	id := confirmedPayment(t, db, "yookassa", 400, time.Now().UTC().Add(-26*time.Hour))

	b.issuePendingReceipts()
	require.Len(t, capture.matching("не пробит больше суток"), 1)

	// Перезапуск: маркеры в памяти обнуляются, маркер в базе — нет.
	b.receiptAlerted = sync.Map{}
	b.issuePendingReceipts()

	assert.Len(t, capture.matching("не пробит больше суток"), 1)
	_ = id
}

// Отвергнутый ФНС запрос через полчаса будет отвергнут ровно так же. Бесконечный
// повтор копил бы attempts и стучался в кабинет каждые полчаса без единого шанса.
func TestRejectedReceiptIsNotRetried(t *testing.T) {
	stub := &fnsStub{failWith: func(int) (int, string) {
		return http.StatusBadRequest, `{"message":"сумма превышает лимит"}`
	}}
	b, db, capture := newReceiptTestBot(t, stub)
	id := confirmedPayment(t, db, "yookassa", 400, time.Now().UTC())

	b.processReceipt(id)

	receipt, err := db.GetReceipt(id)
	require.NoError(t, err)
	require.Equal(t, database.ReceiptStateRejected, receipt.State)

	for i := 0; i < 3; i++ {
		b.issuePendingReceipts()
	}

	assert.Equal(t, 1, stub.createdCount(), "повторных обращений к ФНС не было")
	receipt, err = db.GetReceipt(id)
	require.NoError(t, err)
	assert.Equal(t, 1, receipt.Attempts)

	// Сообщение единственное и после перезапуска не повторяется: отвергнутый чек
	// больше не попадёт ни в проход, ни в сводку, и напомнить о нём будет нечему.
	b.receiptAlerted = sync.Map{}
	b.issuePendingReceipts()
	assert.Len(t, capture.matching("отвергла чек"), 1)
}

// Пароль не платёжеспецифичен: продолжать проход — значит на каждом платеже сделать
// ещё два неудачных входа подряд и подвести кабинет под блокировку.
func TestAuthFailureStopsThePassInsteadOfHammeringTheCabinet(t *testing.T) {
	var logins int
	b, db, _ := newReceiptTestBot(t, &fnsStub{})
	b.moynalog.SetHTTPClient(&http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
		if r.URL.Path == "/api/v1/auth/lkfl" {
			logins++
			return jsonResponse(http.StatusUnauthorized, `{"message":"неверный пароль"}`), nil
		}
		return nil, fmt.Errorf("до пробития дело дойти не должно")
	})})
	for i := 0; i < 5; i++ {
		confirmedPayment(t, db, "yookassa", 400, time.Now().UTC())
	}

	b.issuePendingReceipts()
	assert.Equal(t, 1, logins, "после первого отказа проход останавливается")

	// Пароль поправили — следующий проход работает как обычно, без ручного вмешательства.
	b.moynalog.SetHTTPClient(&http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
		if r.URL.Path == "/api/v1/auth/lkfl" {
			return jsonResponse(http.StatusOK, `{"token":"tok"}`), nil
		}
		return jsonResponse(http.StatusOK, `{"approvedReceiptUuid":"202fixed"}`), nil
	})})
	b.issuePendingReceipts()

	pending, err := db.PaymentsNeedingReceipt()
	require.NoError(t, err)
	assert.Empty(t, pending, "все чеки пробиты следующим проходом")
}

// Чек в кабинете есть, а записать его uuid не вышло. Оставить pending — позвать
// плановый проход пробить второй чек: ровно тот дубль, ради которого таблица и есть.
func TestReceiptCreatedButNotPersistedGoesToUnknown(t *testing.T) {
	stub := &fnsStub{}
	b, db, _ := newReceiptTestBot(t, stub)
	id := confirmedPayment(t, db, "yookassa", 400, time.Now().UTC())

	// Триггер валит именно переход в created — так же, как это сделала бы заблокированная база.
	_, err := db.Conn().Exec(`CREATE TRIGGER fail_created BEFORE UPDATE ON receipts
		WHEN NEW.state = 'created' BEGIN SELECT RAISE(ABORT, 'database is locked'); END`)
	require.NoError(t, err)

	b.processReceipt(id)

	receipt, err := db.GetReceipt(id)
	require.NoError(t, err)
	assert.Equal(t, database.ReceiptStateUnknown, receipt.State,
		"судьба чека неизвестна — разрешит сверка по метке, а не повторное пробитие")

	// Сверка находит пробитый чек по метке и второго не создаёт.
	stub.mu.Lock()
	stub.incomes = []map[string]any{{
		"approvedReceiptUuid": "202auto1",
		"name":                fmt.Sprintf("Sarvizza - Подписка на месяц (%s)", receipt.Marker),
	}}
	stub.mu.Unlock()
	_, err = db.Conn().Exec(`DROP TRIGGER fail_created`)
	require.NoError(t, err)

	b.issuePendingReceipts()

	updated, err := db.GetReceipt(id)
	require.NoError(t, err)
	assert.Equal(t, database.ReceiptStateCreated, updated.State)
	assert.Equal(t, 1, stub.createdCount(), "второго чека в кабинете не появилось")
}

// Остановка бота не должна ронять счётчик пробитий в гонку и не должна бросать
// начатое пробитие на полпути.
func TestStopClosesReceiptsWithoutRacingTheCounter(t *testing.T) {
	stub := &fnsStub{}
	b, db, _ := newReceiptTestBot(t, stub)
	id := confirmedPayment(t, db, "yookassa", 400, time.Now().UTC())

	b.stopReceipts()
	b.issueReceiptAsync(id)
	b.waitReceipts()

	assert.Zero(t, stub.createdCount(), "во время остановки новые пробития не начинаются")

	receipt, err := db.GetReceipt(id)
	require.NoError(t, err)
	assert.Nil(t, receipt, "и следов в базе не оставляют — чек добьёт проход после перезапуска")
}
