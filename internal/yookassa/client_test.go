package yookassa

import (
	"encoding/json"
	"io"
	"net/http"
	"testing"

	"github.com/fus1ond/vpn_bot/internal/paymentprovider"
	"github.com/stretchr/testify/require"
)

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

func TestCreatePaymentSendsAuthenticatedRedirectRequest(t *testing.T) {
	c := NewClientWithBaseURL("shop-1", "secret-1", "https://yookassa.test")
	c.SetHTTPClient(&http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
		require.Equal(t, http.MethodPost, r.Method)
		require.Equal(t, "/v3/payments", r.URL.Path)
		u, p, ok := r.BasicAuth()
		require.True(t, ok)
		require.Equal(t, "shop-1", u)
		require.Equal(t, "secret-1", p)
		require.NotEmpty(t, r.Header.Get("Idempotence-Key"))
		var body map[string]any
		require.NoError(t, json.NewDecoder(r.Body).Decode(&body))
		require.Equal(t, true, body["capture"])
		require.Equal(t, "500.00", body["amount"].(map[string]any)["value"])
		require.Equal(t, "redirect", body["confirmation"].(map[string]any)["type"])
		require.Equal(t, "42", body["metadata"].(map[string]any)["local_payment_id"])
		return response(`{"id":"yo-1","status":"pending","amount":{"value":"500.00","currency":"RUB"},"confirmation":{"confirmation_url":"https://pay/yo-1"},"recipient":{"account_id":"shop-1"}}`), nil
	})})
	p, err := c.CreatePayment(paymentprovider.CreateRequest{Amount: 500, Currency: "RUB", ReturnURL: "https://t.me/bot", Description: "VPN", LocalPaymentID: 42})
	require.NoError(t, err)
	require.Equal(t, "yo-1", p.ID)
	require.Equal(t, paymentprovider.StatusPending, p.Status)
	require.Equal(t, "https://pay/yo-1", p.ConfirmationURL)
}

func TestCreatePaymentReusesCallerSuppliedIdempotenceKey(t *testing.T) {
	var keys []string
	c := NewClientWithBaseURL("shop", "secret", "https://yookassa.test")
	c.SetHTTPClient(&http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
		keys = append(keys, r.Header.Get("Idempotence-Key"))
		return response(`{"id":"yo","status":"pending","amount":{"value":"500.00","currency":"RUB"},"recipient":{"account_id":"shop"}}`), nil
	})})
	request := paymentprovider.CreateRequest{Amount: 500, Currency: "RUB", LocalPaymentID: 1, IdempotenceKey: "persisted-key"}
	_, err := c.CreatePayment(request)
	require.NoError(t, err)
	_, err = c.CreatePayment(request)
	require.NoError(t, err)
	require.Equal(t, []string{"persisted-key", "persisted-key"}, keys)
}

func TestGetPaymentMapsTerminalAndIntermediateStatuses(t *testing.T) {
	for _, tc := range []struct{ remote, want string }{{"succeeded", paymentprovider.StatusSucceeded}, {"canceled", paymentprovider.StatusCanceled}, {"waiting_for_capture", paymentprovider.StatusPending}} {
		t.Run(tc.remote, func(t *testing.T) {
			c := NewClientWithBaseURL("shop", "secret", "https://yookassa.test")
			c.SetHTTPClient(&http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
				return response(`{"id":"yo-2","status":"` + tc.remote + `","amount":{"value":"400.00","currency":"RUB"},"recipient":{"account_id":"shop"}}`), nil
			})})
			p, err := c.GetPayment("yo-2")
			require.NoError(t, err)
			require.Equal(t, tc.want, p.Status)
		})
	}
}

func TestGetPaymentRejectsFractionalRubleAmount(t *testing.T) {
	c := NewClientWithBaseURL("shop", "secret", "https://yookassa.test")
	c.SetHTTPClient(&http.Client{Transport: roundTripFunc(func(*http.Request) (*http.Response, error) {
		return response(`{"id":"yo","status":"succeeded","amount":{"value":"500.01","currency":"RUB"},"recipient":{"account_id":"shop"}}`), nil
	})})
	_, err := c.GetPayment("yo")
	require.ErrorContains(t, err, "invalid ruble payment amount")
}
func TestGetPaymentReturnsAPIError(t *testing.T) {
	c := NewClientWithBaseURL("shop", "secret", "https://yookassa.test")
	c.SetHTTPClient(&http.Client{Transport: roundTripFunc(func(*http.Request) (*http.Response, error) {
		return &http.Response{StatusCode: 401, Body: io.NopCloser(stringReader(`{"description":"bad"}`)), Header: make(http.Header)}, nil
	})})
	_, err := c.GetPayment("bad")
	require.ErrorContains(t, err, "yookassa API error 401")
}
func response(body string) *http.Response {
	return &http.Response{StatusCode: http.StatusOK, Body: io.NopCloser(stringReader(body)), Header: make(http.Header)}
}

type stringReader string

func (s stringReader) Read(p []byte) (int, error) {
	if len(s) == 0 {
		return 0, io.EOF
	}
	n := copy(p, string(s))
	return n, io.EOF
}
