package main

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

// Тесты стенда защищают ровно одно: заглушка обязана отвечать в контракте
// Remnawave, иначе бот на стенде пойдёт не тем путём, что в проде, и проверка
// интерфейса окажется проверкой заглушки.

func post(t *testing.T, srv *httptest.Server, path, body string) (*http.Response, map[string]any) {
	t.Helper()

	resp, err := http.Post(srv.URL+path, "application/json", strings.NewReader(body))
	require.NoError(t, err)
	t.Cleanup(func() { _ = resp.Body.Close() })

	var parsed map[string]any
	_ = json.NewDecoder(resp.Body).Decode(&parsed)
	return resp, parsed
}

func get(t *testing.T, srv *httptest.Server, path string) (*http.Response, map[string]any) {
	t.Helper()

	resp, err := http.Get(srv.URL + path)
	require.NoError(t, err)
	t.Cleanup(func() { _ = resp.Body.Close() })

	var parsed map[string]any
	_ = json.NewDecoder(resp.Body).Decode(&parsed)
	return resp, parsed
}

// createUser заводит пользователя тем же запросом, что шлёт бот при регистрации.
func createUser(t *testing.T, srv *httptest.Server, telegramID int64) (uuid string, id int64) {
	t.Helper()

	_, parsed := post(t, srv, "/api/users",
		fmt.Sprintf(`{"username":"tester","telegramId":%d,"expireAt":"2026-10-01T00:00:00Z"}`, telegramID))

	u := parsed["response"].(map[string]any)
	return u["uuid"].(string), int64(u["id"].(float64))
}

func devicesOf(t *testing.T, parsed map[string]any) []any {
	t.Helper()

	resp := parsed["response"].(map[string]any)
	return resp["devices"].([]any)
}

func TestDevicesLifecycle(t *testing.T) {
	srv := httptest.NewServer(newServer())
	defer srv.Close()

	uuid, id := createUser(t, srv, 555)

	// Пока пульт не выдал устройств, список пуст, а не null: бот разбирает
	// массив, и null сломал бы экран.
	_, parsed := get(t, srv, "/api/hwid/devices/"+uuid)
	require.Empty(t, devicesOf(t, parsed))

	_, _ = post(t, srv, "/mock/user", `{"telegramId":555,"devices":3}`)

	t.Run("чтение по UUID — адресация 2.8.x", func(t *testing.T) {
		_, parsed := get(t, srv, "/api/hwid/devices/"+uuid)
		require.Len(t, devicesOf(t, parsed), 3)
		require.EqualValues(t, 3, parsed["response"].(map[string]any)["total"])
	})

	t.Run("чтение по числовому id — адресация 3.x", func(t *testing.T) {
		_, parsed := get(t, srv, fmt.Sprintf("/api/hwid/devices/%d", id))
		require.Len(t, devicesOf(t, parsed), 3)
	})

	t.Run("удаление по userUuid возвращает оставшихся", func(t *testing.T) {
		_, parsed := post(t, srv, "/api/hwid/devices/delete",
			fmt.Sprintf(`{"userUuid":%q,"hwid":"hwid-2"}`, uuid))

		remaining := devicesOf(t, parsed)
		require.Len(t, remaining, 2)
		for _, d := range remaining {
			require.NotEqual(t, "hwid-2", d.(map[string]any)["hwid"])
		}
	})

	t.Run("удаление по userId — то же самое на 3.x", func(t *testing.T) {
		_, parsed := post(t, srv, "/api/hwid/devices/delete",
			fmt.Sprintf(`{"userId":%d,"hwid":"hwid-1"}`, id))
		require.Len(t, devicesOf(t, parsed), 1)
	})

	t.Run("сброс всех", func(t *testing.T) {
		_, parsed := post(t, srv, "/api/hwid/devices/delete-all",
			fmt.Sprintf(`{"userUuid":%q}`, uuid))
		require.Empty(t, devicesOf(t, parsed))
	})
}

// «delete» и «delete-all» лежат под тем же префиксом, что и идентификатор
// пользователя. Если разбор перепутает их, бот получит 404 вместо удаления.
func TestDeletePathsNotMistakenForUser(t *testing.T) {
	srv := httptest.NewServer(newServer())
	defer srv.Close()

	uuid, _ := createUser(t, srv, 777)
	_, _ = post(t, srv, "/mock/user", `{"telegramId":777,"devices":2}`)

	resp, _ := post(t, srv, "/api/hwid/devices/delete",
		fmt.Sprintf(`{"userUuid":%q,"hwid":"hwid-1"}`, uuid))
	require.Equal(t, http.StatusOK, resp.StatusCode)

	resp, _ = post(t, srv, "/api/hwid/devices/delete-all", fmt.Sprintf(`{"userUuid":%q}`, uuid))
	require.Equal(t, http.StatusOK, resp.StatusCode)
}

func TestControlSetsExpiryRelativeToNow(t *testing.T) {
	srv := httptest.NewServer(newServer())
	defer srv.Close()

	uuid, _ := createUser(t, srv, 111)

	_, _ = post(t, srv, "/mock/user", `{"telegramId":111,"expireInHours":-73}`)

	_, parsed := get(t, srv, "/api/users/"+uuid)
	expireAt, err := time.Parse(time.RFC3339, parsed["response"].(map[string]any)["expireAt"].(string))
	require.NoError(t, err)

	// -73 часа — окно grace-кика. Допуск в минуту: между записью и чтением
	// проходит реальное время.
	require.WithinDuration(t, time.Now().UTC().Add(-73*time.Hour), expireAt, time.Minute)
}

func TestControlSetsStatusAndTraffic(t *testing.T) {
	srv := httptest.NewServer(newServer())
	defer srv.Close()

	uuid, _ := createUser(t, srv, 222)

	_, _ = post(t, srv, "/mock/user", `{"telegramId":222,"status":"DISABLED","usedTrafficGB":1.5}`)

	_, parsed := get(t, srv, "/api/users/"+uuid)
	u := parsed["response"].(map[string]any)
	require.Equal(t, "DISABLED", u["status"])
	require.EqualValues(t, int64(1.5*1024*1024*1024), u["userTraffic"].(map[string]any)["usedTrafficBytes"])
}

func TestControlRejectsBadRequests(t *testing.T) {
	srv := httptest.NewServer(newServer())
	defer srv.Close()

	t.Run("неизвестный пользователь", func(t *testing.T) {
		resp, _ := post(t, srv, "/mock/user", `{"telegramId":404404}`)
		require.Equal(t, http.StatusNotFound, resp.StatusCode)
	})

	t.Run("без telegramId", func(t *testing.T) {
		resp, _ := post(t, srv, "/mock/user", `{"expireInHours":1}`)
		require.Equal(t, http.StatusBadRequest, resp.StatusCode)
	})

	t.Run("GET вместо POST", func(t *testing.T) {
		resp, _ := get(t, srv, "/mock/user")
		require.Equal(t, http.StatusBadRequest, resp.StatusCode)
	})

	t.Run("битый JSON", func(t *testing.T) {
		resp, _ := post(t, srv, "/mock/user", `{`)
		require.Equal(t, http.StatusBadRequest, resp.StatusCode)
	})
}

func TestFailureInjection(t *testing.T) {
	srv := httptest.NewServer(newServer())
	defer srv.Close()

	createUser(t, srv, 333)
	_, _ = post(t, srv, "/mock/fail", `{"count":2,"status":503}`)

	for range 2 {
		resp, parsed := get(t, srv, "/api/users")
		require.Equal(t, http.StatusServiceUnavailable, resp.StatusCode)
		// Бот разбирает тело ошибки как {message, errorCode} — заглушка обязана
		// давать тот же формат, иначе на стенде не увидеть, что видит пользователь.
		require.NotEmpty(t, parsed["message"])
		require.NotEmpty(t, parsed["errorCode"])
	}

	// Счётчик исчерпан — дальше отвечаем как обычно.
	resp, _ := get(t, srv, "/api/users")
	require.Equal(t, http.StatusOK, resp.StatusCode)
}

// Детект версии панели из-под отказов исключён: провалившийся детект увёл бы
// стенд в состояние, которое проверять никто не собирался.
func TestFailureInjectionSparesMetadata(t *testing.T) {
	srv := httptest.NewServer(newServer())
	defer srv.Close()

	_, _ = post(t, srv, "/mock/fail", `{"count":5,"status":500}`)

	resp, parsed := get(t, srv, "/api/system/metadata")
	require.Equal(t, http.StatusOK, resp.StatusCode)
	require.Equal(t, panelVersion, parsed["response"].(map[string]any)["version"])
}

// Пульт обязан оставаться живым при включённых отказах, иначе их нечем было бы
// выключить.
func TestControlSurvivesFailureInjection(t *testing.T) {
	srv := httptest.NewServer(newServer())
	defer srv.Close()

	createUser(t, srv, 444)
	_, _ = post(t, srv, "/mock/fail", `{"count":10,"status":500}`)

	resp, _ := post(t, srv, "/mock/user", `{"telegramId":444,"devices":1}`)
	require.Equal(t, http.StatusOK, resp.StatusCode)

	resp, _ = post(t, srv, "/mock/fail", `{"count":0}`)
	require.Equal(t, http.StatusOK, resp.StatusCode)

	resp, _ = get(t, srv, "/api/users")
	require.Equal(t, http.StatusOK, resp.StatusCode)
}

func TestLatencyDelaysPanelButNotControl(t *testing.T) {
	srv := httptest.NewServer(newServer())
	defer srv.Close()

	createUser(t, srv, 666)
	_, _ = post(t, srv, "/mock/latency", `{"ms":150}`)

	start := time.Now()
	get(t, srv, "/api/users")
	require.GreaterOrEqual(t, time.Since(start), 150*time.Millisecond)

	// Пульт не под задержкой: выключить её должно быть можно мгновенно.
	start = time.Now()
	post(t, srv, "/mock/latency", `{"ms":0}`)
	require.Less(t, time.Since(start), 150*time.Millisecond)

	start = time.Now()
	get(t, srv, "/api/users")
	require.Less(t, time.Since(start), 150*time.Millisecond)
}

func TestUnknownUserReturnsPanelErrorFormat(t *testing.T) {
	srv := httptest.NewServer(newServer())
	defer srv.Close()

	resp, parsed := get(t, srv, "/api/users/no-such-uuid")
	require.Equal(t, http.StatusNotFound, resp.StatusCode)
	require.NotEmpty(t, parsed["message"])
}
