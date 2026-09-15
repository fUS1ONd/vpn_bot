package bot

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/fus1ond/vpn_bot/internal/config"
	"github.com/fus1ond/vpn_bot/internal/database"
	"github.com/fus1ond/vpn_bot/internal/moynalog"
	"github.com/fus1ond/vpn_bot/internal/paymentprovider"
	"github.com/fus1ond/vpn_bot/internal/platega"
	"github.com/fus1ond/vpn_bot/internal/remnawave"
	"github.com/fus1ond/vpn_bot/internal/yookassa"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// Краевые случаи сверки зависших платежей: границы сроков, ответы провайдеров,
// одновременная работа таймера, планировщика, вебхука и кнопки.

const edgeUserID = int64(4242)

// edgePanel — панель, которая умеет продлевать подписку и считает продления:
// двойное продление по одному платежу видно именно по их числу.
type edgePanel struct {
	mu       sync.Mutex
	patches  int
	expireAt time.Time
}

func (p *edgePanel) patchCount() int {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.patches
}

func (p *edgePanel) client() *remnawave.Client {
	client := newTestPanelClient()
	client.SetHTTPClient(&http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
		expire := p.expireAt
		if expire.IsZero() {
			expire = time.Now().UTC().Add(24 * time.Hour)
		}
		switch {
		case r.Method == http.MethodGet && r.URL.Path == "/api/users/uuid-4242":
			return jsonResponse(http.StatusOK, fmt.Sprintf(
				`{"response":{"uuid":"uuid-4242","status":"ACTIVE","expireAt":%q}}`, expire.Format(time.RFC3339))), nil
		case r.Method == http.MethodPatch && r.URL.Path == "/api/users":
			p.mu.Lock()
			p.patches++
			p.mu.Unlock()
			return jsonResponse(http.StatusOK, `{"response":{}}`), nil
		case r.Method == http.MethodGet && r.URL.Path == "/api/users/by-telegram-id/4242":
			return jsonResponse(http.StatusOK, fmt.Sprintf(
				`{"response":[{"uuid":"uuid-4242","telegramId":4242,"status":"ACTIVE","expireAt":%q}]}`, expire.Format(time.RFC3339))), nil
		}
		return jsonResponse(http.StatusInternalServerError, `{}`), nil
	})})
	return client
}

// edgeYooKassa — касса с программируемым ответом на GET: краевым случаям нужен
// не только статус, но и код ответа, и возможность задержать ответ.
type edgeYooKassa struct {
	mu    sync.Mutex
	gets  int
	onGet func(n int, id string) (int, string)
}

func (s *edgeYooKassa) getCount() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.gets
}

func (s *edgeYooKassa) client() *yookassa.Client {
	c := yookassa.NewClientWithBaseURL("shop", "secret", "https://yookassa.test")
	c.SetRetryBackoff(0)
	c.SetHTTPClient(&http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
		if r.Method == http.MethodPost {
			return jsonResponse(http.StatusOK, `{"id":"yoo-edge-new","status":"pending","amount":{"value":"400.00","currency":"RUB"},`+
				`"confirmation":{"confirmation_url":"https://pay.example/yoo-edge-new"},"recipient":{"account_id":"shop"}}`), nil
		}
		s.mu.Lock()
		s.gets++
		n := s.gets
		s.mu.Unlock()
		id := strings.TrimPrefix(r.URL.Path, "/v3/payments/")
		code, body := s.onGet(n, id)
		return jsonResponse(code, body), nil
	})})
	return c
}

// yooPaymentBody собирает ответ кассы по платежу.
func yooPaymentBody(id, status, capturedAt string, amount int, shop, currency string) string {
	captured := ""
	if capturedAt != "" {
		captured = fmt.Sprintf(`,"captured_at":%q`, capturedAt)
	}
	return fmt.Sprintf(`{"id":%q,"status":%q,"amount":{"value":"%d.00","currency":%q},"recipient":{"account_id":%q}%s}`,
		id, status, amount, currency, shop, captured)
}

// yooSays — простой сценарий: один и тот же ответ на каждый GET.
func yooSays(status, capturedAt string) func(int, string) (int, string) {
	return func(_ int, id string) (int, string) {
		return http.StatusOK, yooPaymentBody(id, status, capturedAt, 400, "shop", "RUB")
	}
}

// edgePlatega — Platega с программируемым ответом на GET статуса транзакции.
type edgePlatega struct {
	mu    sync.Mutex
	gets  int
	onGet func(n int, id string) (int, string)
}

func (s *edgePlatega) getCount() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.gets
}

func (s *edgePlatega) client() *platega.Client {
	c := platega.NewClientWithBaseURL("merchant", "secret", "https://platega.test")
	c.SetHTTPClient(&http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
		s.mu.Lock()
		s.gets++
		n := s.gets
		s.mu.Unlock()
		id := strings.TrimPrefix(r.URL.Path, "/transaction/")
		code, body := s.onGet(n, id)
		return jsonResponse(code, body), nil
	})})
	return c
}

func plategaSays(status string) func(int, string) (int, string) {
	return func(_ int, id string) (int, string) {
		return http.StatusOK, fmt.Sprintf(
			`{"id":%q,"status":%q,"paymentDetails":{"amount":400,"currency":"RUB"},"paymentMethod":"SBPQR","expiresIn":"00:00:00"}`,
			id, status)
	}
}

// edgeEnv — бот с обеими кассами, панелью и перехваченным Telegram.
type edgeEnv struct {
	bot      *Bot
	db       *database.DB
	tg       *telegramCapture
	yoo      *edgeYooKassa
	platega  *edgePlatega
	panel    *edgePanel
	shutdown chan struct{}
	stopOnce sync.Once
}

// stop закрывает shutdownCh ровно один раз: этим Stop() и отменяет таймеры.
func (e *edgeEnv) stop() { e.stopOnce.Do(func() { close(e.shutdown) }) }

func newEdgeEnv(t *testing.T) *edgeEnv {
	t.Helper()
	db, err := database.New(t.TempDir() + "/reconcile_edge.db")
	require.NoError(t, err)

	price := 400
	_, err = db.CreateUser(edgeUserID, "payer", "Payer", strPtrTest("uuid-4242"), nil, &price, nil)
	require.NoError(t, err)

	env := &edgeEnv{
		db:       db,
		yoo:      &edgeYooKassa{onGet: yooSays("pending", "")},
		platega:  &edgePlatega{onGet: plategaSays(platega.StatusPending)},
		panel:    &edgePanel{},
		shutdown: make(chan struct{}),
	}
	env.bot = &Bot{
		db:                 db,
		config:             &config.Config{AdminID: 999, YooKassaShopID: "shop", YooKassaReturnURL: "https://t.me/testbot"},
		userStates:         newStateMap(),
		remnawave:          env.panel.client(),
		yookassa:           env.yoo.client(),
		platega:            env.platega.client(),
		shutdownCh:         env.shutdown,
		paymentRetryDelays: []time.Duration{time.Hour},
	}
	env.tg = captureTelegram(t, env.bot)
	t.Cleanup(func() {
		env.stop()
		db.Close()
	})
	return env
}

// pending создаёт PENDING-платёж указанного провайдера, созданный age назад.
func (e *edgeEnv) pending(t *testing.T, provider string, age time.Duration) int64 {
	t.Helper()
	externalID := fmt.Sprintf("%s-%d", provider, time.Now().UnixNano())
	p := &database.Payment{
		TelegramID: edgeUserID, Amount: 400, PaymentMethod: provider, Status: "pending",
		Provider: provider, ProviderPaymentID: &externalID,
	}
	if provider == paymentprovider.Platega {
		p.PlategaTransactionID = &externalID
	}
	id, err := e.db.CreatePayment(p)
	require.NoError(t, err)
	_, err = e.db.Conn().Exec(`UPDATE payments SET created_at = ? WHERE id = ?`,
		time.Now().UTC().Add(-age).Format("2006-01-02 15:04:05"), id)
	require.NoError(t, err)
	return id
}

func (e *edgeEnv) status(t *testing.T, id int64) string {
	t.Helper()
	p, err := e.db.GetPaymentByID(id)
	require.NoError(t, err)
	require.NotNil(t, p)
	return p.Status
}

func (e *edgeEnv) externalID(t *testing.T, id int64) string {
	t.Helper()
	p, err := e.db.GetPaymentByID(id)
	require.NoError(t, err)
	require.NotNil(t, p.ProviderPaymentID)
	return *p.ProviderPaymentID
}

// createdAt возвращает момент создания платежа так, как его видит сверка.
func (e *edgeEnv) createdAt(t *testing.T, id int64) time.Time {
	t.Helper()
	p, err := e.db.GetPaymentByID(id)
	require.NoError(t, err)
	return p.CreatedAt
}

// setExpiresAt пишет срок жизни ссылки строкой — ровно так, как это делает драйвер.
func (e *edgeEnv) setExpiresAt(t *testing.T, id int64, raw string) {
	t.Helper()
	_, err := e.db.Conn().Exec(`UPDATE payments SET expires_at = ? WHERE id = ?`, raw, id)
	require.NoError(t, err)
}

// --- Ось времени и границ ---

// Сутки — заявленный предел жизни pending: раньше закрывать нельзя (касса ещё
// может провести оплату), позже — незачем. Ошибка на секунду в любую сторону тихо
// меняет судьбу платежа, по которому человек ещё платит.
func TestНеоплаченныйПлатёжЗакрываетсяРовноЧерезСуткиИНиСекундойРаньше(t *testing.T) {
	for _, tc := range []struct {
		name  string
		shift time.Duration
		want  string
	}{
		{"за секунду до суток", -time.Second, "pending"},
		{"ровно сутки", 0, "expired"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			env := newEdgeEnv(t)
			env.yoo.onGet = yooSays("pending", "")
			id := env.pending(t, paymentprovider.YooKassa, 20*time.Minute)

			now := env.createdAt(t, id).Add(24 * time.Hour).Add(tc.shift)
			env.bot.reconcilePendingPayment(id, now, "test")

			assert.Equal(t, tc.want, env.status(t, id))
		})
	}
}

// Срок жизни ссылки у провайдера и наши сутки — два разных предела, и решает
// более ранний. Иначе бот либо держит мёртвую ссылку сутки, либо закрывает
// платёж, по которому касса ещё принимает деньги.
func TestСрокомСлужитБолееРаннийИзСутокИСрокаПровайдера(t *testing.T) {
	t.Run("срок провайдера позже суток — закрываем по суткам", func(t *testing.T) {
		env := newEdgeEnv(t)
		env.yoo.onGet = yooSays("pending", "")
		id := env.pending(t, paymentprovider.YooKassa, 20*time.Minute)
		env.setExpiresAt(t, id, time.Now().UTC().Add(72*time.Hour).Format("2006-01-02 15:04:05.999999999-07:00"))

		env.bot.reconcilePendingPayment(id, env.createdAt(t, id).Add(24*time.Hour), "test")

		assert.Equal(t, "expired", env.status(t, id))
	})

	t.Run("срок провайдера ещё не наступил — платёж жив", func(t *testing.T) {
		env := newEdgeEnv(t)
		env.yoo.onGet = yooSays("pending", "")
		id := env.pending(t, paymentprovider.YooKassa, 20*time.Minute)
		env.setExpiresAt(t, id, time.Now().UTC().Add(time.Hour).Format("2006-01-02 15:04:05.999999999-07:00"))

		env.bot.reconcilePendingPayment(id, time.Now().UTC(), "test")

		assert.Equal(t, "pending", env.status(t, id))
	})
}

// В базе живут сроки со смещением часового пояса (так их писал драйвер раньше).
// Прочитанный как местное время, такой срок сдвинется на три часа: живой платёж
// закроется раньше времени либо мёртвый переживёт свой срок.
func TestСрокСоСмещениемЧасовогоПоясаСравниваетсяКакМомент(t *testing.T) {
	msk := time.FixedZone("MSK", 3*60*60)
	for _, tc := range []struct {
		name    string
		expires time.Time
		want    string
	}{
		{"срок прошёл минуту назад", time.Now().UTC().Add(-time.Minute).In(msk), "expired"},
		{"срок наступит через час", time.Now().UTC().Add(time.Hour).In(msk), "pending"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			env := newEdgeEnv(t)
			env.yoo.onGet = yooSays("pending", "")
			id := env.pending(t, paymentprovider.YooKassa, 20*time.Minute)
			env.setExpiresAt(t, id, tc.expires.Format("2006-01-02 15:04:05.999999999-07:00"))

			env.bot.reconcilePendingPayment(id, time.Now().UTC(), "test")

			assert.Equal(t, tc.want, env.status(t, id))
		})
	}
}

// Планировщик передаёт момент прохода. Если выборка зависших платежей опирается
// на местное представление этого момента, а не на сам момент, то при московской
// локали платежи начнут сверяться на три часа позже — то есть почти не сверяться.
func TestВыборкаЗависшихПлатежейНеЗависитОтЧасовогоПоясаПереданногоМомента(t *testing.T) {
	env := newEdgeEnv(t)
	env.yoo.onGet = yooSays("succeeded", "")
	id := env.pending(t, paymentprovider.YooKassa, 20*time.Minute)

	env.bot.reconcilePendingPayments(time.Now().In(time.FixedZone("MSK", 3*60*60)))

	assert.NotEqual(t, "pending", env.status(t, id), "платёж старше 15 минут обязан попасть в сверку")
}

// --- Ось ответов провайдера ---

// «Касса не знает такого платежа» до срока — не приговор: это может быть
// задержка на стороне кассы. Закрыть платёж по первому 404 — закрыть платёж,
// по которому человек прямо сейчас платит.
func TestНеизвестныйКассеПлатёжДоСрокаОстаётсяЖивым(t *testing.T) {
	env := newEdgeEnv(t)
	env.yoo.onGet = func(int, string) (int, string) {
		return http.StatusNotFound, `{"type":"error","code":"not_found"}`
	}
	id := env.pending(t, paymentprovider.YooKassa, time.Hour)

	env.bot.reconcilePendingPayments(time.Now().UTC())

	assert.Equal(t, "pending", env.status(t, id))
}

// Касса лежит, а срок вышел: держать pending вечно нельзя — он блокирует новую
// оплату (GetPendingPayment отдаёт его как активный) и висит в отчётах.
func TestНедоступнаяКассаКСрокуНеМешаетЗакрытьПлатёж(t *testing.T) {
	env := newEdgeEnv(t)
	env.yoo.onGet = func(int, string) (int, string) {
		return http.StatusInternalServerError, `{"type":"error","code":"internal_server_error"}`
	}
	id := env.pending(t, paymentprovider.YooKassa, 20*time.Minute)

	env.bot.reconcilePendingPayment(id, env.createdAt(t, id).Add(25*time.Hour), "test")

	assert.Equal(t, "expired", env.status(t, id))
}

// Ответ кассы, не сошедшийся с локальной записью, оплатой не считается: иначе
// подписку выдаст чужой платёж или платёж на другую сумму, и расхождение с
// реестром кассы всплывёт только при сверке отчётов.
func TestОтветНеСошедшийсяСЛокальнойЗаписьюНеСчитаетсяОплатой(t *testing.T) {
	for _, tc := range []struct {
		name string
		body func(id string) string
	}{
		{"чужая сумма", func(id string) string { return yooPaymentBody(id, "succeeded", "", 1, "shop", "RUB") }},
		{"чужой магазин", func(id string) string { return yooPaymentBody(id, "succeeded", "", 400, "other-shop", "RUB") }},
		{"чужая валюта", func(id string) string { return yooPaymentBody(id, "succeeded", "", 400, "shop", "USD") }},
		{"чужой идентификатор", func(string) string { return yooPaymentBody("yoo-alien", "succeeded", "", 400, "shop", "RUB") }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			env := newEdgeEnv(t)
			env.yoo.onGet = func(_ int, id string) (int, string) { return http.StatusOK, tc.body(id) }
			id := env.pending(t, paymentprovider.YooKassa, time.Hour)

			env.bot.reconcilePendingPayments(time.Now().UTC())

			assert.Equal(t, "pending", env.status(t, id), "расхождение — не повод подтверждать оплату")
			assert.Zero(t, env.panel.patchCount(), "подписка не продлевается по несошедшемуся ответу")
			assert.Empty(t, env.tg.matching("Оплата прошла"))
		})
	}
}

// Ожидание списания и статус, которого бот не знает, — это не «оплачено» и не
// «отменено». Принять их за оплату — выдать подписку за деньги, которых нет.
func TestПромежуточныеИНезнакомыеСтатусыНеПодтверждаютИНеОтменяютПлатёж(t *testing.T) {
	for _, status := range []string{"waiting_for_capture", "неведомый_статус"} {
		t.Run(status, func(t *testing.T) {
			env := newEdgeEnv(t)
			env.yoo.onGet = yooSays(status, "")
			id := env.pending(t, paymentprovider.YooKassa, time.Hour)

			env.bot.reconcilePendingPayments(time.Now().UTC())

			assert.Equal(t, "pending", env.status(t, id))
			assert.Zero(t, env.panel.patchCount())
			assert.Empty(t, env.tg.all(), "человеку нечего сообщать о промежуточном статусе")
		})
	}
}

// Платёж, не доехавший до кассы, спрашивать не у кого: у него нет идентификатора
// в кассе. Но и висеть вечно он не должен — GetPendingPayment отдаёт его как
// активный, и человек не может начать оплату заново.
func TestПлатёжБезИдентификатораВКассеНеСверяетсяНоЗакрываетсяКСроку(t *testing.T) {
	env := newEdgeEnv(t)
	env.yoo.onGet = yooSays("succeeded", "")
	id, err := env.db.CreatePayment(&database.Payment{
		TelegramID: edgeUserID, Amount: 400, PaymentMethod: "yookassa", Status: "pending",
		Provider: paymentprovider.YooKassa,
	})
	require.NoError(t, err)

	env.bot.reconcilePendingPayment(id, time.Now().UTC(), "test")
	assert.Equal(t, "pending", env.status(t, id))
	assert.Zero(t, env.yoo.getCount(), "спрашивать кассу не о чем")

	env.bot.reconcilePendingPayment(id, env.createdAt(t, id).Add(25*time.Hour), "test")
	assert.Equal(t, "expired", env.status(t, id))
}

// --- Ось повтора ---

// Планировщик проходит каждые полчаса, а таймер — отдельно. Повторная обработка
// одного и того же платежа не должна ни продлевать подписку дважды, ни слать
// второе сообщение об оплате.
func TestПовторныйПроходПоОплаченномуПлатежуНеПродлеваетПодпискуДважды(t *testing.T) {
	env := newEdgeEnv(t)
	env.yoo.onGet = yooSays("succeeded", "2026-09-14T11:00:50.202Z")
	id := env.pending(t, paymentprovider.YooKassa, time.Hour)

	env.bot.reconcilePendingPayments(time.Now().UTC())
	env.bot.reconcilePendingPayments(time.Now().UTC())

	assert.Equal(t, "confirmed", env.status(t, id))
	assert.Equal(t, 1, env.panel.patchCount(), "подписка продлевается один раз на платёж")
	assert.Len(t, env.tg.matching("Оплата прошла"), 1)
}

// То же для отмены: второй проход не должен повторять сообщение об отмене —
// человек получит его столько раз, сколько проходов случилось до его ответа.
func TestПовторныйПроходПоОтменённомуПлатежуНеПовторяетСообщениеОбОтмене(t *testing.T) {
	env := newEdgeEnv(t)
	env.yoo.onGet = yooSays("canceled", "")
	id := env.pending(t, paymentprovider.YooKassa, time.Hour)

	env.bot.reconcilePendingPayments(time.Now().UTC())
	env.bot.reconcilePendingPayments(time.Now().UTC())

	assert.Equal(t, "canceled", env.status(t, id))
	assert.Len(t, env.tg.matching("Платёж отменён"), 1)
}

// --- Ось конкурентности ---

// На один платёж смотрят четыре пути. Если хотя бы два доведут подтверждение до
// конца, подписка продлится на два месяца за одни деньги, а человек получит два
// сообщения об оплате.
func TestТаймерПланировщикИВебхукВместеПродлеваютПодпискуОдинРаз(t *testing.T) {
	env := newEdgeEnv(t)
	env.yoo.onGet = yooSays("succeeded", "2026-09-14T11:00:50Z")
	id := env.pending(t, paymentprovider.YooKassa, time.Hour)
	external := env.externalID(t, id)

	var wg sync.WaitGroup
	wg.Add(3)
	go func() { defer wg.Done(); env.bot.reconcilePendingPayment(id, time.Now().UTC(), "timer") }()
	go func() { defer wg.Done(); env.bot.reconcilePendingPayments(time.Now().UTC()) }()
	go func() { defer wg.Done(); _ = env.bot.HandleYooKassaWebhook("payment.succeeded", external) }()
	wg.Wait()

	assert.Equal(t, "confirmed", env.status(t, id))
	assert.Equal(t, 1, env.panel.patchCount(), "одна оплата — одно продление")
	assert.Len(t, env.tg.matching("Оплата прошла"), 1, "и одно сообщение человеку")
}

// Кнопка «Проверить оплату» и сверка идут к кассе одновременно: человек нажал
// ровно в момент прохода планировщика. Продление всё равно одно.
func TestКнопкаПроверитьОплатуОдновременноСоСверкойНеПродлеваетПодпискуДважды(t *testing.T) {
	env := newEdgeEnv(t)
	env.yoo.onGet = yooSays("succeeded", "2026-09-14T11:00:50Z")
	id := env.pending(t, paymentprovider.YooKassa, time.Hour)

	var wg sync.WaitGroup
	wg.Add(2)
	go func() { defer wg.Done(); env.bot.reconcilePendingPayments(time.Now().UTC()) }()
	go func() { defer wg.Done(); _, _ = env.bot.checkPaymentStatus(edgeUserID) }()
	wg.Wait()

	assert.Equal(t, "confirmed", env.status(t, id))
	assert.Equal(t, 1, env.panel.patchCount())
	assert.LessOrEqual(t, len(env.tg.matching("Оплата прошла")), 1)
}

// Вебхук об отмене приходит ровно тогда, когда сверка отменяет тот же платёж
// (у Platega это один и тот же момент — истечение ссылки). Сообщение об отмене
// человек должен получить один раз, а не по числу путей, которые до него дошли.
func TestОтменаСверкойИВебхукомНеСообщаетЧеловекуОбОтменеДважды(t *testing.T) {
	env := newEdgeEnv(t)
	gate := make(chan struct{})
	env.yoo.onGet = func(n int, id string) (int, string) {
		if n == 1 {
			<-gate // первый GET — от сверки: держим её под мьютексом платежа
		}
		return http.StatusOK, yooPaymentBody(id, "canceled", "", 400, "shop", "RUB")
	}
	id := env.pending(t, paymentprovider.YooKassa, time.Hour)
	external := env.externalID(t, id)

	var wg sync.WaitGroup
	wg.Add(1)
	go func() { defer wg.Done(); env.bot.reconcilePendingPayment(id, time.Now().UTC(), "timer") }()
	require.Eventually(t, func() bool { return env.yoo.getCount() == 1 }, 2*time.Second, 5*time.Millisecond)

	wg.Add(1)
	go func() { defer wg.Done(); _ = env.bot.HandleYooKassaWebhook("payment.canceled", external) }()
	time.Sleep(100 * time.Millisecond) // вебхук успевает прочитать платёж и встать на мьютексе
	close(gate)
	wg.Wait()

	assert.Equal(t, "canceled", env.status(t, id))
	assert.Len(t, env.tg.matching("Платёж отменён"), 1, "человек узнаёт об отмене один раз")
}

// То же для Platega: её callback об отмене приходит по истечении ссылки, то есть
// в ту же минуту, когда до платежа добирается первая сверка.
func TestОтменаСверкойИКоллбэкомPlategaНеСообщаетЧеловекуОбОтменеДважды(t *testing.T) {
	env := newEdgeEnv(t)
	gate := make(chan struct{})
	env.platega.onGet = func(n int, id string) (int, string) {
		if n == 1 {
			<-gate
		}
		return plategaSays(platega.StatusCanceled)(n, id)
	}
	id := env.pending(t, paymentprovider.Platega, time.Hour)
	external := env.externalID(t, id)

	var wg sync.WaitGroup
	wg.Add(1)
	go func() { defer wg.Done(); env.bot.reconcilePendingPayment(id, time.Now().UTC(), "timer") }()
	require.Eventually(t, func() bool { return env.platega.getCount() == 1 }, 2*time.Second, 5*time.Millisecond)

	handler := env.bot.PaymentCallbackHandler()
	wg.Add(1)
	go func() {
		defer wg.Done()
		_ = handler.HandlePaymentCallback(platega.CallbackPayload{ID: external, Amount: 400, Currency: "RUB", Status: platega.StatusCanceled})
	}()
	time.Sleep(100 * time.Millisecond)
	close(gate)
	wg.Wait()

	assert.Equal(t, "canceled", env.status(t, id))
	assert.Len(t, env.tg.matching("Платёж отменён"), 1)
}

// Пока сверка ждала мьютекс, платёж мог подтвердить вебхук. Решение по
// устаревшему снимку перевело бы подтверждённый платёж в отменённый.
func TestПлатёжПодтверждённыйПокаСверкаЖдалаМьютексБольшеНеСверяется(t *testing.T) {
	env := newEdgeEnv(t)
	env.yoo.onGet = yooSays("canceled", "")
	id := env.pending(t, paymentprovider.YooKassa, time.Hour)

	mu := getPaymentMutex(edgeUserID)
	mu.Lock()
	done := make(chan struct{})
	go func() {
		defer close(done)
		env.bot.reconcilePendingPayment(id, time.Now().UTC(), "timer")
	}()
	time.Sleep(50 * time.Millisecond) // сверка успевает прочитать pending и встать на мьютексе
	require.NoError(t, env.db.ConfirmPayment(id))
	mu.Unlock()
	<-done

	assert.Equal(t, "confirmed", env.status(t, id))
	assert.Zero(t, env.yoo.getCount(), "по уже решённому платежу в кассу не ходят")
}

// --- Остановка бота и недоступная база ---

// Запланированная первая сверка живёт в памяти. На остановке она обязана
// отмениться: поход в кассу и подтверждение оплаты на выключении означают
// запись, которую бот уже не доведёт до конца.
func TestОстановкаБотаОтменяетЗапланированнуюПервуюСверку(t *testing.T) {
	env := newEdgeEnv(t)
	env.yoo.onGet = yooSays("succeeded", "")
	env.bot.pendingCheckDelay = 200 * time.Millisecond
	id := env.pending(t, paymentprovider.YooKassa, 0)

	env.bot.schedulePendingPaymentCheck(id)
	env.stop()
	time.Sleep(400 * time.Millisecond)

	assert.Zero(t, env.yoo.getCount(), "после остановки сверка в кассу не идёт")
	assert.Equal(t, "pending", env.status(t, id))
}

// База может закрыться раньше фоновых горутин (остановка бота, перезапуск).
// Паника в горутине сверки уронит проход планировщика целиком — вместе с
// уведомлениями, отключениями и автокиками, которые идут следом.
func TestСверкаНаЗакрытойБазеНеПаникуетИНеИдётВКассу(t *testing.T) {
	env := newEdgeEnv(t)
	env.yoo.onGet = yooSays("succeeded", "")
	id := env.pending(t, paymentprovider.YooKassa, time.Hour)
	require.NoError(t, env.db.Close())

	assert.NotPanics(t, func() {
		env.bot.reconcilePendingPayments(time.Now().UTC())
		env.bot.reconcilePendingPayment(id, time.Now().UTC(), "timer")
	})
	assert.Zero(t, env.yoo.getCount())
}

// --- Platega ---

// Сверяются оба провайдера. Потерянный callback Platega — та же потеря денег,
// что и потерянный вебхук кассы.
func TestСверкаPlategaПринимаетОплатуПотерянногоКоллбэка(t *testing.T) {
	for _, status := range []string{platega.StatusConfirmed, platega.StatusManualConfirmed} {
		t.Run(status, func(t *testing.T) {
			env := newEdgeEnv(t)
			env.platega.onGet = plategaSays(status)
			id := env.pending(t, paymentprovider.Platega, time.Hour)

			env.bot.reconcilePendingPayments(time.Now().UTC())

			assert.Equal(t, "confirmed", env.status(t, id))
			assert.Equal(t, 1, env.panel.patchCount(), "подписка выдана")
		})
	}
}

// Отмена в Platega доносится до человека тем же сообщением, что и по callback:
// иначе он ждёт зачисления, которого не будет.
func TestСверкаPlategaПереноситОтмену(t *testing.T) {
	env := newEdgeEnv(t)
	env.platega.onGet = plategaSays(platega.StatusCanceled)
	id := env.pending(t, paymentprovider.Platega, time.Hour)

	env.bot.reconcilePendingPayments(time.Now().UTC())

	assert.Equal(t, "canceled", env.status(t, id))
	assert.Len(t, env.tg.matching("Платёж отменён"), 1)
}

// Chargeback, найденный сверкой, обязан привести к тому же, что и chargeback из
// callback: деньги отозваны, и доступ у отозвавшего остаться не может.
func TestСверкаPlategaОбрабатываетChargebackКакCallback(t *testing.T) {
	env := newEdgeEnv(t)
	env.platega.onGet = plategaSays(platega.StatusChargebacked)
	id := env.pending(t, paymentprovider.Platega, time.Hour)

	env.bot.reconcilePendingPayments(time.Now().UTC())

	assert.Equal(t, "chargebacked", env.status(t, id))
	banned, err := env.db.IsBanned(edgeUserID)
	require.NoError(t, err)
	assert.True(t, banned, "chargeback = бан, независимо от того, кто его обнаружил")
}

// Ссылка Platega живёт около 15 минут, и её срок — предел жизни платежа. До
// срока платёж не закрывают: крипта и СБП подтверждаются не мгновенно.
func TestНеоплаченныйPlategaЖивётДоСрокаСсылкиИЗакрываетсяПослеНего(t *testing.T) {
	env := newEdgeEnv(t)
	env.platega.onGet = plategaSays(platega.StatusPending)
	id := env.pending(t, paymentprovider.Platega, 20*time.Minute)
	env.setExpiresAt(t, id, time.Now().UTC().Add(5*time.Minute).Format("2006-01-02 15:04:05.999999999-07:00"))

	env.bot.reconcilePendingPayment(id, time.Now().UTC(), "test")
	assert.Equal(t, "pending", env.status(t, id))

	env.bot.reconcilePendingPayment(id, time.Now().UTC().Add(10*time.Minute), "test")
	assert.Equal(t, "expired", env.status(t, id))
}

// --- Ось субъекта ---

// Пока платёж висел, человека могли выгнать (автокик, бан) — записи в базе уже
// нет. Деньги от этого никуда не делись: их нельзя ни потерять, ни оставить без
// следа, по которому владелец разберёт случай руками.
func TestОплатаПоПлатежуУжеУдалённогоПользователяНеТеряетсяИДоходитДоВладельца(t *testing.T) {
	env := newEdgeEnv(t)
	env.yoo.onGet = yooSays("succeeded", "2026-09-14T11:00:50Z")
	id := env.pending(t, paymentprovider.YooKassa, time.Hour)
	require.NoError(t, env.db.DeleteUser(edgeUserID))

	env.bot.reconcilePendingPayments(time.Now().UTC())

	stored, err := env.db.GetPaymentByID(id)
	require.NoError(t, err)
	assert.NotEqual(t, "pending", stored.Status, "оплата зафиксирована, а не забыта")
	require.NotNil(t, stored.ConfirmedAt)
	assert.NotEmpty(t, env.tg.matching("активация подписки невозможна"), "владелец узнаёт о деньгах без услуги")
}

// Тестовый платёж администратора проверяет кассу, а не доступ. Подтверждённый
// сверкой, он не должен трогать подписку: бессрочный доступ владельца тихо
// превратился бы в месячный.
func TestТестовыйПлатёжПодтверждённыйСверкойНеТрогаетПодписку(t *testing.T) {
	env := newEdgeEnv(t)
	env.yoo.onGet = yooSays("succeeded", "2026-09-14T11:00:50Z")
	id := env.pending(t, paymentprovider.YooKassa, time.Hour)
	_, err := env.db.Conn().Exec(`UPDATE payments SET is_test = 1 WHERE id = ?`, id)
	require.NoError(t, err)

	env.bot.reconcilePendingPayments(time.Now().UTC())

	assert.Equal(t, "confirmed", env.status(t, id))
	assert.Zero(t, env.panel.patchCount(), "тестовая оплата не меняет доступ")
	assert.Len(t, env.tg.matching("Платёжная система работает"), 1)
}

// --- Стык с чеками ---

// edgeFNS — кабинет «Мой налог», который запоминает тела запросов на пробитие.
type edgeFNS struct {
	mu      sync.Mutex
	created []map[string]any
}

func (s *edgeFNS) lastCreated(t *testing.T) map[string]any {
	t.Helper()
	s.mu.Lock()
	defer s.mu.Unlock()
	require.NotEmpty(t, s.created, "ожидали хотя бы одну попытку пробития")
	return s.created[len(s.created)-1]
}

func (s *edgeFNS) client(t *testing.T) *moynalog.Client {
	t.Helper()
	client := moynalog.NewClientWithBaseURL("123456789012", "secret", "https://lknpd.test/api/v1")
	client.SetHTTPClient(&http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
		switch r.URL.Path {
		case "/api/v1/auth/lkfl":
			return jsonResponse(http.StatusOK, `{"token":"tok"}`), nil
		case "/api/v1/income":
			var body map[string]any
			require.NoError(t, json.NewDecoder(r.Body).Decode(&body))
			s.mu.Lock()
			s.created = append(s.created, body)
			n := len(s.created)
			s.mu.Unlock()
			return jsonResponse(http.StatusOK, fmt.Sprintf(`{"approvedReceiptUuid":"202edge%d"}`, n)), nil
		}
		return nil, fmt.Errorf("unexpected path %s", r.URL.Path)
	})})
	return client
}

// Бот узнаёт об оплате сверкой — то есть заведомо позже списания. Дата дохода в
// чеке обязана остаться датой списания: на стыке месяцев это другой налоговый
// период, и разъехавшийся чек находится только при сверке с кабинетом ФНС.
func TestЧекПоПодтверждённомуСверкойПлатежуДатируетсяСписанием(t *testing.T) {
	env := newEdgeEnv(t)
	fns := &edgeFNS{}
	env.bot.moynalog = fns.client(t)
	env.bot.config.MoynalogServiceName = "Sarvizza - Подписка на месяц"
	env.yoo.onGet = yooSays("succeeded", "2026-09-14T11:00:50.202Z")
	id := env.pending(t, paymentprovider.YooKassa, time.Hour)

	env.bot.reconcilePendingPayments(time.Now().UTC())
	env.bot.waitReceipts()

	require.Equal(t, "confirmed", env.status(t, id))
	assert.Equal(t, "2026-09-14T14:00:50.202+03:00", fns.lastCreated(t)["operationTime"],
		"доход датируется списанием кассы, а не моментом, когда о нём узнал бот")
}

// --- Стыки с остальным проходом планировщика ---

// Сверка стоит первым шагом прохода не случайно: человек, у которого потерялся
// вебхук, к моменту grace-кика уже заплатил. Кик удаляет учётку безвозвратно —
// это худшее, что бот может сделать с заплатившим.
func TestПроходПланировщикаНеУдаляетЗаплатившегоЧейВебхукПотерялся(t *testing.T) {
	env := newEdgeEnv(t)
	env.yoo.onGet = yooSays("succeeded", "2026-09-14T11:00:50Z")
	expireAt := time.Now().UTC().Add(-96 * time.Hour)
	env.panel.expireAt = expireAt
	env.bot.remnawave = env.panel.client()

	deleted := make(chan struct{}, 1)
	env.bot.remnawave.SetHTTPClient(&http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
		switch {
		case r.Method == http.MethodDelete:
			deleted <- struct{}{}
			return jsonResponse(http.StatusOK, `{"response":{}}`), nil
		case r.Method == http.MethodGet && r.URL.Path == "/api/users":
			return jsonResponse(http.StatusOK, fmt.Sprintf(
				`{"response":{"users":[{"uuid":"uuid-4242","username":"payer","status":"EXPIRED","telegramId":4242,"expireAt":%q}],"total":1}}`,
				expireAt.Format(time.RFC3339))), nil
		case r.Method == http.MethodGet && r.URL.Path == "/api/users/uuid-4242":
			return jsonResponse(http.StatusOK, fmt.Sprintf(
				`{"response":{"uuid":"uuid-4242","status":"EXPIRED","expireAt":%q}}`, expireAt.Format(time.RFC3339))), nil
		case r.Method == http.MethodPatch && r.URL.Path == "/api/users":
			env.panel.mu.Lock()
			env.panel.patches++
			env.panel.mu.Unlock()
			return jsonResponse(http.StatusOK, `{"response":{}}`), nil
		case r.Method == http.MethodGet && r.URL.Path == "/api/users/by-telegram-id/4242":
			return jsonResponse(http.StatusOK, fmt.Sprintf(
				`{"response":[{"uuid":"uuid-4242","telegramId":4242,"status":"ACTIVE","expireAt":%q}]}`,
				time.Now().UTC().AddDate(0, 1, 0).Format(time.RFC3339))), nil
		}
		return jsonResponse(http.StatusInternalServerError, `{}`), nil
	})})

	id := env.pending(t, paymentprovider.YooKassa, time.Hour)

	env.bot.runSubscriptionSchedulerPass()

	assert.Equal(t, "confirmed", env.status(t, id), "оплата принимается до решения о кике")
	select {
	case <-deleted:
		t.Fatal("заплативший удалён из панели")
	default:
	}
}
