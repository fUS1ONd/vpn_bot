package remnawave

import (
	"io"
	"net/http"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

// metadataResponse собирает ответ /api/system/metadata с заданной версией панели.
func metadataResponse(version string) *http.Response {
	payload := `{"response":{"version":"` + version + `","build":{"time":"","number":""}}}`
	return &http.Response{
		StatusCode: http.StatusOK,
		Body:       io.NopCloser(strings.NewReader(payload)),
		Header:     make(http.Header),
	}
}

func TestDetectAPIVersionFromMetadata(t *testing.T) {
	tests := []struct {
		panelVersion string
		want         APIVersion
	}{
		{"2.8.1", APIVersionV2},
		{"3.2.3", APIVersionV3},
		{"4.0.0", APIVersionV3}, // версия новее известной — работаем по контракту 3.x
	}

	for _, tc := range tests {
		t.Run(tc.panelVersion, func(t *testing.T) {
			var calls int
			client := NewClient("https://panel.example.com", "test-token", nil)
			client.http = &http.Client{
				Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
					calls++
					require.Equal(t, "/api/system/metadata", r.URL.Path)
					return metadataResponse(tc.panelVersion), nil
				}),
			}

			got, err := client.DetectAPIVersion()
			require.NoError(t, err)
			require.Equal(t, tc.want, got)

			// Версия кэшируется: повторный вопрос не идёт в панель.
			require.Equal(t, tc.want, client.CachedAPIVersion())
			require.Equal(t, 1, calls)
		})
	}
}

// errorResponse собирает ответ панели с кодом ошибки.
func errorResponse(status int, body string) *http.Response {
	return &http.Response{
		StatusCode: status,
		Body:       io.NopCloser(strings.NewReader(body)),
		Header:     make(http.Header),
	}
}

func TestDetectAPIVersionFallsBackToProbe(t *testing.T) {
	tests := []struct {
		name          string
		metadata      func() *http.Response
		probeStatus   int
		want          APIVersion
		wantProbeCall bool
	}{
		{
			name:          "metadata закрыта scope-ом, проба отвечает 404 — это 2.8.x",
			metadata:      func() *http.Response { return errorResponse(http.StatusForbidden, `{"message":"Forbidden"}`) },
			probeStatus:   http.StatusNotFound,
			want:          APIVersionV2,
			wantProbeCall: true,
		},
		{
			name:          "metadata закрыта scope-ом, проба отвечает 400 — это 3.x",
			metadata:      func() *http.Response { return errorResponse(http.StatusForbidden, `{"message":"Forbidden"}`) },
			probeStatus:   http.StatusBadRequest,
			want:          APIVersionV3,
			wantProbeCall: true,
		},
		{
			name:          "версия в metadata неразборчива — падаем на пробу",
			metadata:      func() *http.Response { return metadataResponse("не-версия") },
			probeStatus:   http.StatusBadRequest,
			want:          APIVersionV3,
			wantProbeCall: true,
		},
		{
			name:          "metadata отвечает 500 — падаем на пробу",
			metadata:      func() *http.Response { return errorResponse(http.StatusInternalServerError, `{}`) },
			probeStatus:   http.StatusNotFound,
			want:          APIVersionV2,
			wantProbeCall: true,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			var probeCalled bool
			client := NewClient("https://panel.example.com", "test-token", nil)
			client.http = &http.Client{
				Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
					if r.URL.Path == "/api/system/metadata" {
						return tc.metadata(), nil
					}
					probeCalled = true
					require.Equal(t, "/api/users/"+probeUserUUID, r.URL.Path)
					return errorResponse(tc.probeStatus, `{"message":"probe"}`), nil
				}),
			}

			got, err := client.DetectAPIVersion()
			require.NoError(t, err)
			require.Equal(t, tc.want, got)
			require.Equal(t, tc.wantProbeCall, probeCalled)
		})
	}
}

// Первая проба ответила неожиданным кодом — вывод подтверждает вторая, числовым
// путём: на 2.8.x он не проходит валидацию (там ждут uuid), на 3.x нормален.
func TestDetectAPIVersionUsesSecondProbe(t *testing.T) {
	tests := []struct {
		name         string
		secondStatus int
		want         APIVersion
	}{
		{"числовой путь отвергнут — это 2.8.x", http.StatusBadRequest, APIVersionV2},
		{"числовой путь принят — это 3.x", http.StatusNotFound, APIVersionV3},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			var paths []string
			client := NewClient("https://panel.example.com", "test-token", nil)
			client.http = &http.Client{
				Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
					paths = append(paths, r.URL.Path)
					switch r.URL.Path {
					case "/api/system/metadata":
						return errorResponse(http.StatusForbidden, `{"message":"Forbidden"}`), nil
					case "/api/users/" + probeUserUUID:
						// Неожиданный код: по нему решить нельзя.
						return errorResponse(http.StatusConflict, `{}`), nil
					case "/api/users/1":
						return errorResponse(tc.secondStatus, `{}`), nil
					}
					t.Fatalf("неожиданный путь: %s", r.URL.Path)
					return nil, nil
				}),
			}

			got, err := client.DetectAPIVersion()
			require.NoError(t, err)
			require.Equal(t, tc.want, got)
			require.Contains(t, paths, "/api/users/1")
		})
	}
}

// Протухший токен обязан остаться различимым сквозь ошибку детекта: иначе отказ
// по авторизации прошёл бы молча, вместо сообщения владельцу.
func TestDetectAPIVersionKeepsAuthErrorVisible(t *testing.T) {
	client := NewClient("https://panel.example.com", "test-token", nil)
	client.http = &http.Client{
		Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
			return errorResponse(http.StatusUnauthorized, `{"message":"Unauthorized"}`), nil
		}),
	}

	_, err := client.DetectAPIVersion()
	require.ErrorIs(t, err, ErrPanelVersionUnknown)
	require.True(t, IsAuthError(err), "ошибка детекта должна оставаться распознаваемой как отказ по токену")
}

func TestDetectAPIVersionFailsWhenPanelSilent(t *testing.T) {
	client := NewClient("https://panel.example.com", "test-token", nil)
	client.http = &http.Client{
		Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
			return errorResponse(http.StatusInternalServerError, `{}`), nil
		}),
	}

	got, err := client.DetectAPIVersion()
	require.ErrorIs(t, err, ErrPanelVersionUnknown)
	require.Equal(t, APIVersionUnknown, got)
	require.Equal(t, APIVersionUnknown, client.CachedAPIVersion())
}
