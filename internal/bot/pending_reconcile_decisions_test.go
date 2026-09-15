package bot

import (
	"fmt"
	"net/http"
	"net/url"
	"sync"
	"testing"
	"time"

	"github.com/fus1ond/vpn_bot/internal/database"
	"github.com/fus1ond/vpn_bot/internal/moynalog"
	"github.com/fus1ond/vpn_bot/internal/paymentprovider"
	"github.com/fus1ond/vpn_bot/internal/platega"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// Решения владельца по итогам разбора краевых случаев: бюджет времени на проход,
// сверка локально закрытых платежей, вопрос кассе перед киком, отказ от
// переиспользования суточной записи, сверка ответа Platega и окно поиска чека.

// Шаг сверки идёт до уведомлений, отключений и киков. Недоступная касса держит
// каждый платёж до 48 секунд, и без потолка проход съедает собственные тики.
func TestПроходСверкиУкладываетсяВБюджетВремени(t *testing.T) {
	env := newEdgeEnv(t)
	env.bot.reconcilePassBudget = time.Millisecond
	env.yoo.onGet = func(int, string) (int, string) {
		time.Sleep(20 * time.Millisecond)
		return http.StatusOK, yooPaymentBody("any", "pending", "", 400, "shop", "RUB")
	}
	first := env.pending(t, paymentprovider.YooKassa, time.Hour)
	second := env.pending(t, paymentprovider.YooKassa, time.Hour)

	env.bot.reconcilePendingPayments(time.Now().UTC())

	assert.Equal(t, 1, env.yoo.getCount(), "проход останавливается по бюджету, остальное доберёт следующий")
	assert.Equal(t, "pending", env.status(t, first))
	assert.Equal(t, "pending", env.status(t, second))
}

// Бот сам закрывает платёж при смене способа оплаты. Если по нему всё же прошли
// деньги, а уведомление потерялось, спросить кассу больше некому — поэтому сутки
// сверяются и закрытые платежи.
func TestЗакрытыйБотомПлатёжОплаченныйПозжеПринимаетсяСверкой(t *testing.T) {
	for _, status := range []string{"expired", "canceled"} {
		t.Run(status, func(t *testing.T) {
			env := newEdgeEnv(t)
			env.yoo.onGet = yooSays("succeeded", "2026-09-14T11:00:50Z")
			id := env.pending(t, paymentprovider.YooKassa, time.Hour)
			require.NoError(t, env.db.UpdatePaymentStatus(id, status))

			env.bot.reconcilePendingPayments(time.Now().UTC())

			assert.Equal(t, "confirmed", env.status(t, id), "оплата по закрытому платежу не теряется")
			assert.Len(t, env.tg.matching("был локально закрыт"), 1, "владелец узнаёт о таком платеже")
		})
	}
}

// Сутки — общий предел: после них закрытый платёж больше не сверяется, иначе
// касса опрашивалась бы по нему вечно.
func TestЗакрытыйПлатёжСтаршеСутокБольшеНеСверяется(t *testing.T) {
	env := newEdgeEnv(t)
	env.yoo.onGet = yooSays("succeeded", "")
	id := env.pending(t, paymentprovider.YooKassa, 25*time.Hour)
	require.NoError(t, env.db.UpdatePaymentStatus(id, "expired"))

	env.bot.reconcilePendingPayments(time.Now().UTC())

	assert.Equal(t, "expired", env.status(t, id))
	assert.Zero(t, env.yoo.getCount())
}

// Кик удаляет учётку безвозвратно. Свежий платёж (моложе 15 минут) в шаг сверки
// не попадает, поэтому перед удалением бот спрашивает кассу отдельно.
func TestПередКикомБотСверяетДажеСовсемСвежийПлатёж(t *testing.T) {
	env := newEdgeEnv(t)
	env.yoo.onGet = yooSays("succeeded", "2026-09-14T11:00:50Z")
	expireAt := time.Now().UTC().Add(-96 * time.Hour)
	env.panel.expireAt = expireAt

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
			return jsonResponse(http.StatusOK, `{"response":{}}`), nil
		case r.Method == http.MethodGet && r.URL.Path == "/api/users/by-telegram-id/4242":
			return jsonResponse(http.StatusOK, fmt.Sprintf(
				`{"response":[{"uuid":"uuid-4242","telegramId":4242,"status":"ACTIVE","expireAt":%q}]}`,
				time.Now().UTC().AddDate(0, 1, 0).Format(time.RFC3339))), nil
		}
		return jsonResponse(http.StatusInternalServerError, `{}`), nil
	})})

	id := env.pending(t, paymentprovider.YooKassa, time.Minute)

	env.bot.runSubscriptionSchedulerPass()

	assert.Equal(t, "confirmed", env.status(t, id), "оплата минутной давности принимается до решения о кике")
	select {
	case <-deleted:
		t.Fatal("заплативший удалён из панели")
	default:
	}
}

// Запись старше суток переиспользовать нельзя: предел жизни считается от её
// создания, и выданная по ней ссылка закрылась бы ближайшим проходом.
func TestЗаписьСтаршеСутокНеПереиспользуетсяДляНовойОплаты(t *testing.T) {
	env := newEdgeEnv(t)
	old := env.pending(t, paymentprovider.YooKassa, 25*time.Hour)

	fresh, url, err := env.bot.createPaymentForProvider(edgeUserID, paymentprovider.YooKassa)
	require.NoError(t, err)

	assert.NotEqual(t, old, fresh.ID, "старая запись не переиспользуется")
	assert.NotEmpty(t, url)
	assert.Equal(t, "expired", env.status(t, old))
	assert.Equal(t, "pending", env.status(t, fresh.ID))
}

// Свежую запись переиспользуем по-прежнему: две живые ссылки на одну подписку хуже.
func TestЗаписьМоложеСутокПереиспользуетсяКакПрежде(t *testing.T) {
	env := newEdgeEnv(t)
	first, firstURL, err := env.bot.createPaymentForProvider(edgeUserID, paymentprovider.YooKassa)
	require.NoError(t, err)

	second, secondURL, err := env.bot.createPaymentForProvider(edgeUserID, paymentprovider.YooKassa)
	require.NoError(t, err)

	assert.Equal(t, first.ID, second.ID)
	assert.Equal(t, firstURL, secondURL)
}

// Ответ Platega сверяется с локальной записью так же, как ответ ЮKassa: иначе
// подписку выдаст платёж на другую сумму, и расхождение всплывёт только в отчётах.
func TestОтветPlategaНеСошедшийсяСЛокальнойЗаписьюНеСчитаетсяОплатой(t *testing.T) {
	for _, tc := range []struct {
		name string
		body func(id string) string
	}{
		{"чужая сумма", func(id string) string {
			return fmt.Sprintf(`{"id":%q,"status":%q,"paymentDetails":{"amount":100,"currency":"RUB"},"paymentMethod":"SBP"}`, id, platega.StatusConfirmed)
		}},
		{"чужой идентификатор", func(string) string {
			return fmt.Sprintf(`{"id":"foreign-tx","status":%q,"paymentDetails":{"amount":400,"currency":"RUB"},"paymentMethod":"SBP"}`, platega.StatusConfirmed)
		}},
		{"чужая валюта", func(id string) string {
			return fmt.Sprintf(`{"id":%q,"status":%q,"paymentDetails":{"amount":400,"currency":"USD"},"paymentMethod":"SBP"}`, id, platega.StatusConfirmed)
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			env := newEdgeEnv(t)
			id := env.pending(t, paymentprovider.Platega, time.Hour)
			external := env.externalID(t, id)
			env.platega.onGet = func(int, string) (int, string) { return http.StatusOK, tc.body(external) }

			env.bot.reconcilePendingPayment(id, time.Now().UTC(), "test")

			assert.Equal(t, "pending", env.status(t, id))
			assert.Zero(t, env.panel.patchCount(), "подписка не продлевается по несошедшемуся ответу")
		})
	}
}

// Свой ответ Platega принимается: сверка не должна ломать рабочий путь.
func TestСошедшийсяОтветPlategaПринимаетсяКакОплата(t *testing.T) {
	env := newEdgeEnv(t)
	id := env.pending(t, paymentprovider.Platega, time.Hour)
	external := env.externalID(t, id)
	env.platega.onGet = func(int, string) (int, string) {
		return http.StatusOK, fmt.Sprintf(`{"id":%q,"status":%q,"paymentDetails":{"amount":400,"currency":"RUB"},"paymentMethod":"SBP"}`, external, platega.StatusConfirmed)
	}

	env.bot.reconcilePendingPayment(id, time.Now().UTC(), "test")

	assert.Equal(t, "confirmed", env.status(t, id))
}

// windowFNS запоминает границы окна, в котором бот искал свой чек.
type windowFNS struct {
	mu       sync.Mutex
	from, to string
}

func (s *windowFNS) window() (string, string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.from, s.to
}

func (s *windowFNS) client(t *testing.T) *moynalog.Client {
	t.Helper()
	client := moynalog.NewClientWithBaseURL("123456789012", "secret", "https://lknpd.test/api/v1")
	client.SetHTTPClient(&http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
		switch r.URL.Path {
		case "/api/v1/auth/lkfl":
			return jsonResponse(http.StatusOK, `{"token":"tok"}`), nil
		case "/api/v1/incomes":
			q, err := url.ParseQuery(r.URL.RawQuery)
			require.NoError(t, err)
			s.mu.Lock()
			s.from, s.to = q.Get("from"), q.Get("to")
			s.mu.Unlock()
			return jsonResponse(http.StatusOK, `{"content":[]}`), nil
		case "/api/v1/income":
			return jsonResponse(http.StatusOK, `{"approvedReceiptUuid":"202window"}`), nil
		}
		return nil, fmt.Errorf("unexpected path %s", r.URL.Path)
	})})
	return client
}

// Чек ищется по метке в окне вокруг даты дохода. Дата дохода теперь — момент
// списания, а чек мог быть пробит намного позже: окно обязано доходить до
// текущего момента, иначе бот не найдёт свой чек и пробьёт второй.
func TestОкноПоискаЧекаДоходитДоТекущегоМомента(t *testing.T) {
	env := newEdgeEnv(t)
	fns := &windowFNS{}
	env.bot.moynalog = fns.client(t)
	env.bot.config.MoynalogServiceName = "Sarvizza - Подписка на месяц"

	paidAt := time.Now().UTC().Add(-20 * 24 * time.Hour)
	id, err := env.db.CreatePayment(&database.Payment{
		TelegramID: edgeUserID, Amount: 400, PaymentMethod: "yookassa", Status: "confirmed",
		Provider: paymentprovider.YooKassa,
	})
	require.NoError(t, err)
	require.NoError(t, env.db.ConfirmPayment(id))
	require.NoError(t, env.db.SetProviderPaidAt(id, paidAt))
	marker, err := database.NewReceiptMarker()
	require.NoError(t, err)
	_, err = env.db.ClaimReceipt(id, marker, paidAt, 400)
	require.NoError(t, err)
	require.NoError(t, env.db.MarkReceiptFailed(id, database.ReceiptStateUnknown, "ответ ФНС потерялся"))

	env.bot.processReceipt(id)

	from, to := fns.window()
	require.NotEmpty(t, to, "сверка чека обязана сходить в кабинет")
	toMoment, err := time.Parse("2006-01-02T15:04:05-07:00", to)
	require.NoError(t, err)
	fromMoment, err := time.Parse("2006-01-02T15:04:05-07:00", from)
	require.NoError(t, err)
	assert.False(t, toMoment.Before(time.Now().UTC().Add(-time.Minute)), "правая граница окна доходит до сейчас")
	assert.False(t, fromMoment.After(paidAt), "левая граница окна не позже момента списания")
}
