package remnawave

import (
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

// Владелец обновляет панель, не перезапуская бота: первый же запрос по старому
// контракту получает 400, клиент перечитывает версию и повторяет запрос.
func TestUserRequestRetriesAfterPanelUpgrade(t *testing.T) {
	var paths []string
	client := newVersionedClient(t, APIVersionV2, func(r *http.Request) (*http.Response, error) {
		paths = append(paths, r.URL.Path)
		switch r.URL.Path {
		case "/api/users/uuid-1":
			return errorResponse(http.StatusBadRequest, `{"message":"Invalid number"}`), nil
		case "/api/system/metadata":
			return metadataResponse("3.2.3"), nil
		case "/api/users/42":
			return userResponse(t, `"id":42,"username":"alice","status":"ACTIVE"`), nil
		}
		t.Fatalf("неожиданный путь: %s", r.URL.Path)
		return nil, nil
	})

	user, err := client.GetUser(UserRef{UUID: "uuid-1", ID: 42})
	require.NoError(t, err)
	require.Equal(t, int64(42), user.ID)
	require.Equal(t, []string{"/api/users/uuid-1", "/api/system/metadata", "/api/users/42"}, paths)
	require.Equal(t, APIVersionV3, client.CachedAPIVersion())
}

// Повторный 400 уже по новому контракту — честная ошибка наверх, а не третий круг.
func TestUserRequestFailsWhenRetryAlsoRejected(t *testing.T) {
	client := newVersionedClient(t, APIVersionV2, func(r *http.Request) (*http.Response, error) {
		if r.URL.Path == "/api/system/metadata" {
			return metadataResponse("3.2.3"), nil
		}
		return errorResponse(http.StatusBadRequest, `{"message":"Invalid"}`), nil
	})

	_, err := client.GetUser(UserRef{UUID: "uuid-1", ID: 42})
	require.Error(t, err)
	status, ok := APIStatusCode(err)
	require.True(t, ok)
	require.Equal(t, http.StatusBadRequest, status)
}

// 404 в ре-детект не входит: это штатное «пользователя нет в панели», которое
// scheduler получает на автокиках регулярно.
func TestUserRequestDoesNotRedetectOnNotFound(t *testing.T) {
	var metadataCalls int
	client := newVersionedClient(t, APIVersionV2, func(r *http.Request) (*http.Response, error) {
		if r.URL.Path == "/api/system/metadata" {
			metadataCalls++
			return metadataResponse("3.2.3"), nil
		}
		return errorResponse(http.StatusNotFound, `{"errorCode":"A025"}`), nil
	})

	err := client.DeleteUser(UserRef{UUID: "uuid-1", ID: 42})
	require.ErrorIs(t, err, ErrUserNotFound)
	require.Zero(t, metadataCalls)
	require.Equal(t, APIVersionV2, client.CachedAPIVersion())
}

// Кэш версии читается из каждого вызова, а бот многопоточный: параллельные
// вызовы обязаны сложиться в один запрос metadata.
func TestDetectAPIVersionIsRaceFreeAndSingleFlight(t *testing.T) {
	var mu sync.Mutex
	var metadataCalls int

	client := NewClient("https://panel.example.com", "test-token", nil)
	client.http = &http.Client{
		Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
			if r.URL.Path == "/api/system/metadata" {
				mu.Lock()
				metadataCalls++
				mu.Unlock()
				time.Sleep(10 * time.Millisecond)
				return metadataResponse("3.2.3"), nil
			}
			return userResponse(t, `"id":42,"username":"alice"`), nil
		}),
	}

	var wg sync.WaitGroup
	for i := 0; i < 20; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, err := client.GetUser(UserRef{ID: 42})
			require.NoError(t, err)
		}()
	}
	wg.Wait()

	mu.Lock()
	defer mu.Unlock()
	require.Equal(t, 1, metadataCalls)
}

// 3.x валидирует expireAt регуляркой: локальное время получит 400, а на 2.8.x
// прошло бы молча. Значит формат обязан проверяться тестом, а не вкусом.
func TestEnableUserSendsUTCExpireAt(t *testing.T) {
	moscow := time.FixedZone("MSK", 3*60*60)
	expireAt := time.Date(2026, time.September, 13, 15, 4, 5, 0, moscow)

	var body updateUserBody
	client := newVersionedClient(t, APIVersionV3, func(r *http.Request) (*http.Response, error) {
		require.Equal(t, http.MethodPatch, r.Method)
		require.Equal(t, "/api/users", r.URL.Path)
		require.NoError(t, json.NewDecoder(r.Body).Decode(&body))
		return userResponse(t, `"id":42`), nil
	})

	require.NoError(t, client.EnableUser(UserRef{ID: 42}, expireAt))
	require.NotNil(t, body.ExpireAt)
	require.True(t, strings.HasSuffix(*body.ExpireAt, "Z"), "expireAt должен быть в UTC: %s", *body.ExpireAt)
	require.Equal(t, "2026-09-13T12:04:05Z", *body.ExpireAt)
	require.Equal(t, int64(42), body.ID)
	require.Empty(t, body.UUID)
}

// PATCH на 2.8.x кладёт в тело uuid, а не id.
func TestUpdateUserSendsUUIDOnV2(t *testing.T) {
	var body updateUserBody
	client := newVersionedClient(t, APIVersionV2, func(r *http.Request) (*http.Response, error) {
		require.NoError(t, json.NewDecoder(r.Body).Decode(&body))
		return userResponse(t, `"uuid":"uuid-1"`), nil
	})

	require.NoError(t, client.UpdateUsername(UserRef{UUID: "uuid-1", ID: 42}, "alice"))
	require.Equal(t, "uuid-1", body.UUID)
	require.Zero(t, body.ID)
	require.Equal(t, "alice", *body.Username)
}

// HWID-тела на 3.x несут userId числом.
func TestHwidBodiesUseUserIDOnV3(t *testing.T) {
	var deletePayload, deleteAllPayload map[string]any

	client := newVersionedClient(t, APIVersionV3, func(r *http.Request) (*http.Response, error) {
		switch r.URL.Path {
		case "/api/hwid/devices/delete":
			require.NoError(t, json.NewDecoder(r.Body).Decode(&deletePayload))
		case "/api/hwid/devices/delete-all":
			require.NoError(t, json.NewDecoder(r.Body).Decode(&deleteAllPayload))
		case "/api/hwid/devices/42":
		default:
			t.Fatalf("неожиданный путь: %s", r.URL.Path)
		}
		return jsonResponse(`{"response":{"total":0,"devices":[]}}`), nil
	})

	ref := UserRef{ID: 42}
	_, err := client.DeleteUserHwidDevice(ref, "hw-a")
	require.NoError(t, err)
	require.NoError(t, client.DeleteAllUserHwidDevices(ref))
	_, err = client.GetUserHwidDevices(ref)
	require.NoError(t, err)

	require.Equal(t, float64(42), deletePayload["userId"])
	require.Equal(t, "hw-a", deletePayload["hwid"])
	require.Equal(t, float64(42), deleteAllPayload["userId"])
	require.NotContains(t, deletePayload, "userUuid")
}

// DELETE на 3.x отвечает 204 без тела — это успех, а не ошибка разбора.
func TestDeleteUserAcceptsNoContent(t *testing.T) {
	client := newVersionedClient(t, APIVersionV3, func(r *http.Request) (*http.Response, error) {
		require.Equal(t, http.MethodDelete, r.Method)
		require.Equal(t, "/api/users/42", r.URL.Path)
		return &http.Response{
			StatusCode: http.StatusNoContent,
			Body:       io.NopCloser(strings.NewReader("")),
			Header:     make(http.Header),
		}, nil
	})

	require.NoError(t, client.DeleteUser(UserRef{ID: 42}))
}

// Перевыпуск ссылки: путь меняет только идентификатор, тело остаётся прежним.
func TestRevokeUserSubscriptionPathPerVersion(t *testing.T) {
	tests := []struct {
		version  APIVersion
		ref      UserRef
		wantPath string
	}{
		{APIVersionV2, UserRef{UUID: "uuid-1"}, "/api/users/uuid-1/actions/revoke"},
		{APIVersionV3, UserRef{ID: 42}, "/api/users/42/actions/revoke"},
	}

	for _, tc := range tests {
		t.Run(tc.version.String(), func(t *testing.T) {
			client := newVersionedClient(t, tc.version, func(r *http.Request) (*http.Response, error) {
				require.Equal(t, http.MethodPost, r.Method)
				require.Equal(t, tc.wantPath, r.URL.Path)
				body, _ := io.ReadAll(r.Body)
				require.JSONEq(t, `{}`, string(body))
				return userResponse(t, `"id":42,"subscriptionUrl":"https://sub/new"`), nil
			})

			user, err := client.RevokeUserSubscription(tc.ref)
			require.NoError(t, err)
			require.Equal(t, "https://sub/new", user.SubscriptionURL)
		})
	}
}
