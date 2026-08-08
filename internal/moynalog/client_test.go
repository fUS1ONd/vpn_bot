package moynalog

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

func response(status int, body string) *http.Response {
	return &http.Response{StatusCode: status, Body: io.NopCloser(strings.NewReader(body)), Header: make(http.Header)}
}

const authOK = `{"token":"tok-1","refreshToken":"ref-1","profile":{"inn":"123"}}`

func testClient(t *testing.T, handler roundTripFunc) *Client {
	t.Helper()
	c := NewClientWithBaseURL("123456789012", "secret", "https://lknpd.test/api/v1")
	c.SetHTTPClient(&http.Client{Transport: handler})
	return c
}

func TestCreateIncomeLogsInOnDemandAndReturnsReceiptUUID(t *testing.T) {
	var paths []string
	var income map[string]any
	c := testClient(t, func(r *http.Request) (*http.Response, error) {
		paths = append(paths, r.URL.Path)
		switch r.URL.Path {
		case "/api/v1/auth/lkfl":
			var body map[string]any
			require.NoError(t, json.NewDecoder(r.Body).Decode(&body))
			require.Equal(t, "123456789012", body["username"])
			require.Equal(t, "secret", body["password"])
			device := body["deviceInfo"].(map[string]any)
			require.Len(t, device["sourceDeviceId"], deviceIDLength)
			require.Equal(t, "WEB", device["sourceType"])
			return response(http.StatusOK, authOK), nil
		case "/api/v1/income":
			require.Equal(t, "Bearer tok-1", r.Header.Get("Authorization"))
			require.NoError(t, json.NewDecoder(r.Body).Decode(&income))
			return response(http.StatusOK, `{"approvedReceiptUuid":"202zzz"}`), nil
		}
		return nil, fmt.Errorf("unexpected path %s", r.URL.Path)
	})

	uuid, err := c.CreateIncome(IncomeRequest{
		Name:          "Sarvizza - Подписка на месяц (k7m2xq)",
		Amount:        400,
		OperationTime: time.Date(2026, 8, 7, 21, 43, 31, 0, time.UTC),
	})
	require.NoError(t, err)
	require.Equal(t, "202zzz", uuid)
	require.Equal(t, []string{"/api/v1/auth/lkfl", "/api/v1/income"}, paths)

	require.Equal(t, "2026-08-08T00:43:31.000+03:00", income["operationTime"])
	require.Equal(t, "400", income["totalAmount"])
	services := income["services"].([]any)
	require.Equal(t, "Sarvizza - Подписка на месяц (k7m2xq)", services[0].(map[string]any)["name"])
	client := income["client"].(map[string]any)
	require.Nil(t, client["displayName"])
	require.Nil(t, client["contactPhone"])
	require.Equal(t, "FROM_INDIVIDUAL", client["incomeType"])
}

func TestClientReusesTokenAcrossRequests(t *testing.T) {
	logins := 0
	c := testClient(t, func(r *http.Request) (*http.Response, error) {
		if r.URL.Path == "/api/v1/auth/lkfl" {
			logins++
			return response(http.StatusOK, authOK), nil
		}
		return response(http.StatusOK, `{"approvedReceiptUuid":"u"}`), nil
	})
	for i := 0; i < 3; i++ {
		_, err := c.CreateIncome(IncomeRequest{Name: "n", Amount: 1, OperationTime: time.Now().UTC()})
		require.NoError(t, err)
	}
	require.Equal(t, 1, logins, "токен должен переиспользоваться, а не запрашиваться на каждый запрос")
}

func TestUnauthorizedTriggersReloginAndSingleRetry(t *testing.T) {
	logins, attempts := 0, 0
	c := testClient(t, func(r *http.Request) (*http.Response, error) {
		if r.URL.Path == "/api/v1/auth/lkfl" {
			logins++
			return response(http.StatusOK, fmt.Sprintf(`{"token":"tok-%d"}`, logins)), nil
		}
		attempts++
		if attempts == 1 {
			return response(http.StatusUnauthorized, `{"code":"UNAUTHORIZED","message":"token expired"}`), nil
		}
		require.Equal(t, "Bearer tok-2", r.Header.Get("Authorization"))
		return response(http.StatusOK, `{"approvedReceiptUuid":"after-relogin"}`), nil
	})

	uuid, err := c.CreateIncome(IncomeRequest{Name: "n", Amount: 1, OperationTime: time.Now().UTC()})
	require.NoError(t, err)
	require.Equal(t, "after-relogin", uuid)
	require.Equal(t, 2, logins)
	require.Equal(t, 2, attempts)
}

func TestSecondUnauthorizedInARowIsAuthError(t *testing.T) {
	attempts := 0
	c := testClient(t, func(r *http.Request) (*http.Response, error) {
		if r.URL.Path == "/api/v1/auth/lkfl" {
			return response(http.StatusOK, authOK), nil
		}
		attempts++
		return response(http.StatusUnauthorized, `{"code":"E","message":"нет доступа"}`), nil
	})

	_, err := c.CreateIncome(IncomeRequest{Name: "n", Amount: 1, OperationTime: time.Now().UTC()})
	require.Error(t, err)
	require.Equal(t, KindAuth, ErrorKind(err))
	require.Equal(t, "нет доступа", ErrorMessage(err))
	require.Equal(t, 2, attempts, "повтор ровно один")
}

func TestFailedLoginIsAuthError(t *testing.T) {
	c := testClient(t, func(r *http.Request) (*http.Response, error) {
		return response(http.StatusUnauthorized, `{"code":"E","message":"неверный пароль"}`), nil
	})
	_, err := c.CreateIncome(IncomeRequest{Name: "n", Amount: 1, OperationTime: time.Now().UTC()})
	require.Equal(t, KindAuth, ErrorKind(err))
	require.Equal(t, "неверный пароль", ErrorMessage(err))
}

func TestStatusMapsToBehaviourKind(t *testing.T) {
	for _, tc := range []struct {
		status int
		want   Kind
	}{
		{http.StatusBadRequest, KindBadRequest},
		{http.StatusForbidden, KindBadRequest},
		{http.StatusNotFound, KindBadRequest},
		{http.StatusInternalServerError, KindServer},
		{http.StatusBadGateway, KindServer},
	} {
		t.Run(fmt.Sprint(tc.status), func(t *testing.T) {
			c := testClient(t, func(r *http.Request) (*http.Response, error) {
				if r.URL.Path == "/api/v1/auth/lkfl" {
					return response(http.StatusOK, authOK), nil
				}
				return response(tc.status, `{"code":"C","message":"нельзя"}`), nil
			})
			_, err := c.CreateIncome(IncomeRequest{Name: "n", Amount: 1, OperationTime: time.Now().UTC()})
			require.Equal(t, tc.want, ErrorKind(err))
			require.Equal(t, "нельзя", ErrorMessage(err))
		})
	}
}

// Создание чека — единственный неидемпотентный запрос: оборвавшаяся связь могла
// оборваться уже после того, как ФНС приняла чек. Слепой повтор дал бы дубль.
func TestBrokenConnectionOnCreateGivesUnknown(t *testing.T) {
	c := testClient(t, func(r *http.Request) (*http.Response, error) {
		if r.URL.Path == "/api/v1/auth/lkfl" {
			return response(http.StatusOK, authOK), nil
		}
		return nil, fmt.Errorf("context deadline exceeded")
	})
	_, err := c.CreateIncome(IncomeRequest{Name: "n", Amount: 1, OperationTime: time.Now().UTC()})
	require.Equal(t, KindUnknown, ErrorKind(err))
}

// Чтение списка доходов ничего не меняет, поэтому его обрыв — обычный ретрай.
func TestTransportErrorOnReadIsRetryable(t *testing.T) {
	c := testClient(t, func(r *http.Request) (*http.Response, error) {
		return nil, fmt.Errorf("dial tcp: no such host")
	})
	_, err := c.ListIncomes(time.Now().UTC(), time.Now().UTC())
	require.Equal(t, KindTransport, ErrorKind(err))
}

func TestUnreadableBodyGivesUnknownWithoutPanic(t *testing.T) {
	c := testClient(t, func(r *http.Request) (*http.Response, error) {
		if r.URL.Path == "/api/v1/auth/lkfl" {
			return response(http.StatusOK, authOK), nil
		}
		return response(http.StatusOK, `<html>шлюз прилёг`), nil
	})
	uuid, err := c.CreateIncome(IncomeRequest{Name: "n", Amount: 1, OperationTime: time.Now().UTC()})
	require.Empty(t, uuid)
	require.Equal(t, KindUnknown, ErrorKind(err))
}

func TestSuccessWithoutReceiptUUIDGivesUnknown(t *testing.T) {
	c := testClient(t, func(r *http.Request) (*http.Response, error) {
		if r.URL.Path == "/api/v1/auth/lkfl" {
			return response(http.StatusOK, authOK), nil
		}
		return response(http.StatusOK, `{}`), nil
	})
	_, err := c.CreateIncome(IncomeRequest{Name: "n", Amount: 1, OperationTime: time.Now().UTC()})
	require.Equal(t, KindUnknown, ErrorKind(err))
}

func TestListIncomesSendsOffsetDatesAndParsesContent(t *testing.T) {
	var query url.Values
	c := testClient(t, func(r *http.Request) (*http.Response, error) {
		if r.URL.Path == "/api/v1/auth/lkfl" {
			return response(http.StatusOK, authOK), nil
		}
		require.Equal(t, "/api/v1/incomes", r.URL.Path)
		query = r.URL.Query()
		return response(http.StatusOK, `{"content":[
			{"approvedReceiptUuid":"202aaa","name":"Подписка (k7m2xq)","totalAmount":400,"operationTime":"2026-08-08T00:43:31+03:00","services":[{"name":"Подписка (k7m2xq)"}]},
			{"approvedReceiptUuid":"202bbb","name":"Подписка (zzz111)","totalAmount":"450","operationTime":"2026-08-08T10:00:00+03:00","cancellationInfo":{"comment":"ошибка"}}
		]}`), nil
	})

	incomes, err := c.ListIncomes(
		time.Date(2026, 8, 7, 0, 0, 0, 0, time.UTC),
		time.Date(2026, 8, 9, 0, 0, 0, 0, time.UTC),
	)
	require.NoError(t, err)
	require.Equal(t, "2026-08-07T03:00:00+03:00", query.Get("from"))
	require.Equal(t, "2026-08-09T03:00:00+03:00", query.Get("to"))

	require.Len(t, incomes, 2)
	require.Equal(t, "202aaa", incomes[0].ApprovedReceiptUUID)
	require.Equal(t, 400.0, incomes[0].TotalAmount)
	require.True(t, incomes[0].Matches("k7m2xq"))
	require.False(t, incomes[0].Matches("zzz111"))
	require.False(t, incomes[0].Canceled)
	require.Equal(t, 450.0, incomes[1].TotalAmount)
	require.True(t, incomes[1].Canceled)
}

func TestCancelIncomeSendsReceiptUUID(t *testing.T) {
	var body map[string]any
	c := testClient(t, func(r *http.Request) (*http.Response, error) {
		if r.URL.Path == "/api/v1/auth/lkfl" {
			return response(http.StatusOK, authOK), nil
		}
		require.Equal(t, "/api/v1/cancel", r.URL.Path)
		require.NoError(t, json.NewDecoder(r.Body).Decode(&body))
		return response(http.StatusOK, `{"incomeInfo":{}}`), nil
	})
	require.NoError(t, c.CancelIncome("202aaa", "Чек сформирован ошибочно", time.Date(2026, 8, 7, 21, 43, 31, 0, time.UTC)))
	require.Equal(t, "202aaa", body["receiptUuid"])
	require.Equal(t, "Чек сформирован ошибочно", body["comment"])
	require.Equal(t, "2026-08-08T00:43:31.000+03:00", body["operationTime"])
}

func TestDeviceIDIsStableForINN(t *testing.T) {
	first := NewClient("123456789012", "a").deviceID()
	second := NewClient("123456789012", "b").deviceID()
	other := NewClient("210987654321", "a").deviceID()
	require.Equal(t, first, second, "идентификатор устройства зависит только от ИНН")
	require.NotEqual(t, first, other)
	require.Len(t, first, deviceIDLength)
}

// Смещение зашито в код, а не унаследовано от процесса: правка TZ в докере не должна
// тихо сдвинуть все будущие чеки.
func TestMoscowFormattingIgnoresProcessTimezone(t *testing.T) {
	for _, tz := range []string{"UTC", "America/Los_Angeles", "Asia/Tokyo"} {
		t.Run(tz, func(t *testing.T) {
			t.Setenv("TZ", tz)
			require.Equal(t, "2026-08-04T02:33:01.000+03:00", FormatMoscow(time.Date(2026, 8, 3, 23, 33, 1, 0, time.UTC)))
			require.Equal(t, "2026-08-08T00:43:31.000+03:00", FormatMoscow(time.Date(2026, 8, 7, 21, 43, 31, 0, time.UTC)))
		})
	}
}

// Обрыв на входе в кабинет означает, что /income не уходил: чека точно нет, и это
// обычный ретрай, а не «судьба неизвестна».
func TestBrokenConnectionOnLoginIsRetryableNotUnknown(t *testing.T) {
	c := testClient(t, func(r *http.Request) (*http.Response, error) {
		return nil, fmt.Errorf("dial tcp 213.24.64.181: connect: network is unreachable")
	})
	_, err := c.CreateIncome(IncomeRequest{Name: "n", Amount: 1, OperationTime: time.Now().UTC()})
	require.Equal(t, KindTransport, ErrorKind(err),
		"неудачный вход не должен превращаться в неопределённую судьбу чека")
}

// Полная страница доходов означает, что выборка могла обрезаться: сказать «метки
// нет» по такой выборке — значит позвать сверку пробить дубль.
func TestFullIncomesPageIsAmbiguousRatherThanTruncated(t *testing.T) {
	items := make([]map[string]any, incomesPageLimit)
	for i := range items {
		items[i] = map[string]any{"approvedReceiptUuid": fmt.Sprintf("202x%d", i), "name": "Подписка"}
	}
	body, err := json.Marshal(map[string]any{"content": items})
	require.NoError(t, err)

	c := testClient(t, func(r *http.Request) (*http.Response, error) {
		if r.URL.Path == "/api/v1/auth/lkfl" {
			return response(http.StatusOK, authOK), nil
		}
		return response(http.StatusOK, string(body)), nil
	})

	incomes, err := c.ListIncomes(time.Now().UTC().Add(-time.Hour), time.Now().UTC())
	require.Nil(t, incomes)
	require.Equal(t, KindUnknown, ErrorKind(err))
}
