package remnawave

import (
	"io"
	"net/http"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

// newVersionedClient собирает клиент с уже известной версией панели, чтобы тест
// операции не смешивался с тестом детекта.
func newVersionedClient(t *testing.T, version APIVersion, handler roundTripFunc) *Client {
	t.Helper()
	client := NewClient("https://panel.example.com", "test-token", nil)
	client.apiVersion = version
	client.http = &http.Client{Transport: handler}
	return client
}

// userResponse собирает ответ панели с одним пользователем.
func userResponse(t *testing.T, fields string) *http.Response {
	t.Helper()
	return &http.Response{
		StatusCode: http.StatusOK,
		Body:       io.NopCloser(strings.NewReader(`{"response":{` + fields + `}}`)),
		Header:     make(http.Header),
	}
}

func TestGetUserAddressesUserByVersion(t *testing.T) {
	tests := []struct {
		name     string
		version  APIVersion
		ref      UserRef
		wantPath string
	}{
		{
			name:     "2.8.x адресует пользователя UUID",
			version:  APIVersionV2,
			ref:      UserRef{UUID: "uuid-1", ID: 42},
			wantPath: "/api/users/uuid-1",
		},
		{
			name:     "3.x адресует пользователя числовым id",
			version:  APIVersionV3,
			ref:      UserRef{UUID: "uuid-1", ID: 42},
			wantPath: "/api/users/42",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			client := newVersionedClient(t, tc.version, func(r *http.Request) (*http.Response, error) {
				require.Equal(t, http.MethodGet, r.Method)
				require.Equal(t, tc.wantPath, r.URL.Path)
				return userResponse(t, `"id":42,"uuid":"uuid-1","username":"alice","status":"ACTIVE"`), nil
			})

			user, err := client.GetUser(tc.ref)
			require.NoError(t, err)
			require.Equal(t, int64(42), user.ID)
			require.Equal(t, "alice", user.Username)
		})
	}
}

func TestGetUserRejectsIncompleteRef(t *testing.T) {
	tests := []struct {
		name    string
		version APIVersion
		ref     UserRef
		wantErr error
	}{
		{
			name:    "на 3.x без id идти некуда",
			version: APIVersionV3,
			ref:     UserRef{UUID: "uuid-1"},
			wantErr: ErrUserRefMissingID,
		},
		{
			name:    "на 2.8.x без uuid идти некуда",
			version: APIVersionV2,
			ref:     UserRef{ID: 42},
			wantErr: ErrUserRefMissingUUID,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			client := newVersionedClient(t, tc.version, func(r *http.Request) (*http.Response, error) {
				t.Fatalf("запрос не должен уходить в панель: %s", r.URL.Path)
				return nil, nil
			})

			_, err := client.GetUser(tc.ref)
			require.ErrorIs(t, err, tc.wantErr)
		})
	}
}

func TestUserRefComesFromPanelResponse(t *testing.T) {
	// На 3.x uuid в ответе отсутствует, и Ref() обязан отдать ссылку по id —
	// иначе вызывающий код возьмёт пустой UUID и получит 400.
	v3User := User{ID: 42}
	require.Equal(t, UserRef{ID: 42}, v3User.Ref())

	v2User := User{ID: 42, UUID: "uuid-1"}
	require.Equal(t, UserRef{ID: 42, UUID: "uuid-1"}, v2User.Ref())
}

func TestGetUserFailsWhenVersionUnknown(t *testing.T) {
	client := NewClient("https://panel.example.com", "test-token", nil)
	client.http = &http.Client{
		Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
			// Панель недоступна: ни metadata, ни проба не отвечают.
			return errorResponse(http.StatusInternalServerError, `{}`), nil
		}),
	}

	_, err := client.GetUser(UserRef{UUID: "uuid-1", ID: 42})
	require.ErrorIs(t, err, ErrPanelVersionUnknown)
}
