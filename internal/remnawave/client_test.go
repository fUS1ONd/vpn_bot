package remnawave

import (
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestCreateUserSetsUnlimitedTraffic(t *testing.T) {
	var capturedRequest CreateUserRequest
	expectedExpireAt := time.Date(2026, time.March, 10, 12, 0, 0, 0, time.UTC)

	client := NewClient("https://panel.example.com", "test-token", "")
	client.http = &http.Client{
		Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
			require.Equal(t, http.MethodPost, r.Method)
			require.Equal(t, "/api/users", r.URL.Path)
			require.Equal(t, "Bearer test-token", r.Header.Get("Authorization"))

			err := json.NewDecoder(r.Body).Decode(&capturedRequest)
			require.NoError(t, err)

			createdAt := time.Date(2026, time.January, 1, 0, 0, 0, 0, time.UTC)
			expireAt := time.Date(2099, time.January, 1, 0, 0, 0, 0, time.UTC)

			payload, err := json.Marshal(map[string]any{
				"response": map[string]any{
					"uuid":              "uuid-1",
					"shortUuid":         "short-1",
					"username":          "alice",
					"status":            StatusActive,
					"trafficLimitBytes": 0,
					"subscriptionUrl":   "vless://example",
					"createdAt":         createdAt.Format(time.RFC3339),
					"expireAt":          expireAt.Format(time.RFC3339),
				},
			})
			require.NoError(t, err)

			return &http.Response{
				StatusCode: http.StatusOK,
				Body:       io.NopCloser(strings.NewReader(string(payload))),
				Header:     make(http.Header),
			}, nil
		}),
	}

	user, err := client.CreateUser(12345, "alice", expectedExpireAt)
	require.NoError(t, err)
	require.NotNil(t, user)

	require.Equal(t, int64(12345), capturedRequest.TelegramID)
	require.Equal(t, "alice", capturedRequest.Username)
	require.Equal(t, int64(0), capturedRequest.TrafficLimitBytes)
	require.Equal(t, TrafficStrategyMonth, capturedRequest.TrafficLimitStrategy)
	require.Equal(t, expectedExpireAt.Format(time.RFC3339), capturedRequest.ExpireAt)
}

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(r *http.Request) (*http.Response, error) {
	return f(r)
}
