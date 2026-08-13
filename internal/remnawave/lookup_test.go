package remnawave

import (
	"io"
	"net/http"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

func jsonResponse(body string) *http.Response {
	return &http.Response{
		StatusCode: http.StatusOK,
		Body:       io.NopCloser(strings.NewReader(body)),
		Header:     make(http.Header),
	}
}

func TestGetUserByTelegramIDOnV2ReadsArray(t *testing.T) {
	client := newVersionedClient(t, APIVersionV2, func(r *http.Request) (*http.Response, error) {
		require.Equal(t, "/api/users/by-telegram-id/12345", r.URL.Path)
		// Панель 2.8.1 отдаёт массив пользователей, а не один объект.
		return jsonResponse(`{"response":[{"uuid":"uuid-1","id":7,"username":"alice","status":"ACTIVE","telegramId":12345}]}`), nil
	})

	user, err := client.GetUserByTelegramID(12345)
	require.NoError(t, err)
	require.NotNil(t, user)
	require.Equal(t, "uuid-1", user.UUID)
	require.Equal(t, int64(7), user.ID)
}

func TestGetUserByTelegramIDOnV3UsesStream(t *testing.T) {
	client := newVersionedClient(t, APIVersionV3, func(r *http.Request) (*http.Response, error) {
		require.Equal(t, "/api/users/stream", r.URL.Path)
		require.Equal(t, "12345", r.URL.Query().Get("telegramId"))
		// Второй элемент нужен только чтобы заметить дубликат telegramId.
		require.Equal(t, "2", r.URL.Query().Get("size"))
		return jsonResponse(`{"response":{"users":[{"id":7,"username":"alice","status":"ACTIVE","telegramId":12345}],"hasMore":false}}`), nil
	})

	user, err := client.GetUserByTelegramID(12345)
	require.NoError(t, err)
	require.NotNil(t, user)
	require.Equal(t, int64(7), user.ID)
	require.Empty(t, user.UUID)
}

func TestGetUserByTelegramIDReturnsNilWhenAbsent(t *testing.T) {
	tests := []struct {
		name    string
		version APIVersion
		respond func() *http.Response
	}{
		{
			name:    "2.8.x отдаёт пустой массив",
			version: APIVersionV2,
			respond: func() *http.Response { return jsonResponse(`{"response":[]}`) },
		},
		{
			name:    "2.8.x отвечает 404",
			version: APIVersionV2,
			respond: func() *http.Response { return errorResponse(http.StatusNotFound, `{"errorCode":"A025"}`) },
		},
		{
			name:    "3.x отдаёт пустой stream",
			version: APIVersionV3,
			respond: func() *http.Response { return jsonResponse(`{"response":{"users":[],"hasMore":false}}`) },
		},
		{
			name:    "3.x отвечает 404",
			version: APIVersionV3,
			respond: func() *http.Response { return errorResponse(http.StatusNotFound, `{"errorCode":"A025"}`) },
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			client := newVersionedClient(t, tc.version, func(r *http.Request) (*http.Response, error) {
				return tc.respond(), nil
			})

			user, err := client.GetUserByTelegramID(12345)
			require.NoError(t, err)
			require.Nil(t, user)
		})
	}
}

func TestGetUserByTelegramIDFiltersForeignMatches(t *testing.T) {
	// Фильтр панели по telegramId в OpenAPI не выражен, а /api/users с filters
	// вообще ищет подстрокой: 123 находит и 1234. Точное сравнение — на нашей стороне.
	tests := []struct {
		name    string
		version APIVersion
		respond func() *http.Response
	}{
		{
			name:    "2.8.x вернул чужого пользователя",
			version: APIVersionV2,
			respond: func() *http.Response {
				return jsonResponse(`{"response":[{"uuid":"uuid-x","id":9,"telegramId":1234}]}`)
			},
		},
		{
			name:    "3.x вернул чужого пользователя",
			version: APIVersionV3,
			respond: func() *http.Response {
				return jsonResponse(`{"response":{"users":[{"id":9,"telegramId":1234}],"hasMore":false}}`)
			},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			client := newVersionedClient(t, tc.version, func(r *http.Request) (*http.Response, error) {
				return tc.respond(), nil
			})

			user, err := client.GetUserByTelegramID(123)
			require.NoError(t, err)
			require.Nil(t, user)
		})
	}
}

func TestGetUserByTelegramIDRejectsDuplicates(t *testing.T) {
	// Панель не запрещает дубликаты telegramId. Молча выбрать первого — значит
	// с некоторой вероятностью привязать чужой аккаунт к платежу.
	client := newVersionedClient(t, APIVersionV3, func(r *http.Request) (*http.Response, error) {
		return jsonResponse(`{"response":{"users":[{"id":7,"telegramId":12345},{"id":8,"telegramId":12345}],"hasMore":true}}`), nil
	})

	user, err := client.GetUserByTelegramID(12345)
	require.ErrorIs(t, err, ErrMultipleUsersForTelegramID)
	require.Nil(t, user)
}
