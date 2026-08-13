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

	client := NewClient("https://panel.example.com", "test-token", nil)
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

	user, err := client.CreateUser(12345, "alice", expectedExpireAt, 0)
	require.NoError(t, err)
	require.NotNil(t, user)

	require.Equal(t, int64(12345), capturedRequest.TelegramID)
	require.Equal(t, "alice", capturedRequest.Username)
	require.Equal(t, int64(0), capturedRequest.TrafficLimitBytes)
	require.Equal(t, TrafficStrategyMonth, capturedRequest.TrafficLimitStrategy)
	require.Equal(t, expectedExpireAt.Format(time.RFC3339), capturedRequest.ExpireAt)
}

func TestCreateUserSetsMultipleInternalSquads(t *testing.T) {
	var capturedRequest CreateUserRequest
	expectedExpireAt := time.Date(2026, time.March, 11, 15, 0, 0, 0, time.UTC)

	client := NewClient("https://panel.example.com", "test-token", []string{"uuid-1", "uuid-2"})
	client.http = &http.Client{
		Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
			require.Equal(t, http.MethodPost, r.Method)
			require.Equal(t, "/api/users", r.URL.Path)
			require.NoError(t, json.NewDecoder(r.Body).Decode(&capturedRequest))

			payload, err := json.Marshal(map[string]any{
				"response": map[string]any{
					"uuid":              "uuid-2",
					"shortUuid":         "short-2",
					"username":          "bob",
					"status":            StatusActive,
					"trafficLimitBytes": 0,
					"subscriptionUrl":   "vless://example",
					"createdAt":         time.Date(2026, time.March, 1, 0, 0, 0, 0, time.UTC).Format(time.RFC3339),
					"expireAt":          expectedExpireAt.Format(time.RFC3339),
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

	user, err := client.CreateUser(54321, "bob", expectedExpireAt, 0)
	require.NoError(t, err)
	require.NotNil(t, user)
	require.Equal(t, []string{"uuid-1", "uuid-2"}, capturedRequest.ActiveInternalSquads)
}

func TestCreateUserOmitsEmptyInternalSquads(t *testing.T) {
	var capturedBody map[string]any
	expectedExpireAt := time.Date(2026, time.March, 12, 18, 0, 0, 0, time.UTC)

	client := NewClient("https://panel.example.com", "test-token", nil)
	client.http = &http.Client{
		Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
			require.Equal(t, http.MethodPost, r.Method)
			require.Equal(t, "/api/users", r.URL.Path)
			require.NoError(t, json.NewDecoder(r.Body).Decode(&capturedBody))

			payload, err := json.Marshal(map[string]any{
				"response": map[string]any{
					"uuid":              "uuid-3",
					"shortUuid":         "short-3",
					"username":          "carol",
					"status":            StatusActive,
					"trafficLimitBytes": 0,
					"subscriptionUrl":   "vless://example",
					"createdAt":         time.Date(2026, time.March, 2, 0, 0, 0, 0, time.UTC).Format(time.RFC3339),
					"expireAt":          expectedExpireAt.Format(time.RFC3339),
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

	user, err := client.CreateUser(98765, "carol", expectedExpireAt, 0)
	require.NoError(t, err)
	require.NotNil(t, user)
	_, exists := capturedBody["activeInternalSquads"]
	require.False(t, exists)
}

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(r *http.Request) (*http.Response, error) {
	return f(r)
}

func TestGetUserHwidDevicesCount(t *testing.T) {
	client := newVersionedClient(t, APIVersionV2, func(r *http.Request) (*http.Response, error) {
		require.Equal(t, http.MethodGet, r.Method)
		require.Equal(t, "/api/hwid/devices/uuid-1", r.URL.Path)

		payload := `{"response":{"total":2,"devices":[{"hwid":"a"},{"hwid":"b"}]}}`
		return &http.Response{
			StatusCode: http.StatusOK,
			Body:       io.NopCloser(strings.NewReader(payload)),
			Header:     make(http.Header),
		}, nil
	})

	count, err := client.GetUserHwidDevicesCount(UserRef{UUID: "uuid-1"})
	require.NoError(t, err)
	require.Equal(t, 2, count)
}

func TestGetUserHwidDevices(t *testing.T) {
	client := newVersionedClient(t, APIVersionV2, func(r *http.Request) (*http.Response, error) {
		require.Equal(t, http.MethodGet, r.Method)
		require.Equal(t, "/api/hwid/devices/uuid-1", r.URL.Path)

		payload := `{"response":{"total":2,"devices":[` +
			`{"hwid":"hw-a","platform":"iOS","deviceModel":"iPhone 14","createdAt":"2026-01-01T00:00:00Z"},` +
			`{"hwid":"hw-b","platform":"Android","deviceModel":"Pixel 7","createdAt":"2026-01-02T00:00:00Z"}]}}`
		return &http.Response{
			StatusCode: http.StatusOK,
			Body:       io.NopCloser(strings.NewReader(payload)),
			Header:     make(http.Header),
		}, nil
	})

	devices, err := client.GetUserHwidDevices(UserRef{UUID: "uuid-1"})
	require.NoError(t, err)
	require.Len(t, devices, 2)
	require.Equal(t, "hw-a", devices[0].Hwid)
	require.Equal(t, "iOS", devices[0].Platform)
	require.Equal(t, "iPhone 14", devices[0].DeviceModel)
	require.Equal(t, "hw-b", devices[1].Hwid)
}

func TestDeleteUserHwidDevice(t *testing.T) {
	client := newVersionedClient(t, APIVersionV2, func(r *http.Request) (*http.Response, error) {
		require.Equal(t, http.MethodPost, r.Method)
		require.Equal(t, "/api/hwid/devices/delete", r.URL.Path)

		body, _ := io.ReadAll(r.Body)
		require.JSONEq(t, `{"userUuid":"uuid-1","hwid":"hw-a"}`, string(body))

		// API возвращает обновлённый список (осталось одно устройство)
		payload := `{"response":{"total":1,"devices":[{"hwid":"hw-b","platform":"Android","deviceModel":"Pixel 7"}]}}`
		return &http.Response{
			StatusCode: http.StatusOK,
			Body:       io.NopCloser(strings.NewReader(payload)),
			Header:     make(http.Header),
		}, nil
	})

	devices, err := client.DeleteUserHwidDevice(UserRef{UUID: "uuid-1"}, "hw-a")
	require.NoError(t, err)
	require.Len(t, devices, 1)
	require.Equal(t, "hw-b", devices[0].Hwid)
}

func TestDeleteAllUserHwidDevices(t *testing.T) {
	client := newVersionedClient(t, APIVersionV2, func(r *http.Request) (*http.Response, error) {
		require.Equal(t, http.MethodPost, r.Method)
		require.Equal(t, "/api/hwid/devices/delete-all", r.URL.Path)

		body, _ := io.ReadAll(r.Body)
		require.JSONEq(t, `{"userUuid":"uuid-1"}`, string(body))

		payload := `{"response":{"total":0,"devices":[]}}`
		return &http.Response{
			StatusCode: http.StatusOK,
			Body:       io.NopCloser(strings.NewReader(payload)),
			Header:     make(http.Header),
		}, nil
	})

	err := client.DeleteAllUserHwidDevices(UserRef{UUID: "uuid-1"})
	require.NoError(t, err)
}
