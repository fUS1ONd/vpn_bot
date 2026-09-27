package platega_test

import (
	"fmt"
	"io"
	"net/http"
	"strings"
	"testing"

	"github.com/fus1ond/vpn_bot/internal/platega"
	"github.com/stretchr/testify/require"
)

// Сумма в ответе Platega — число с плавающей точкой; к рублям она приводится
// округлением, а не усечением: 399.9999999 — это 400, а не ложное несовпадение.
// Половина рубля округляется вверх, всё, что меньше, — вниз.
func TestProviderGetPaymentRoundsAmount(t *testing.T) {
	cases := []struct {
		amount string
		want   int
	}{
		{"399.9999999", 400},
		{"400.0000001", 400},
		{"399.5", 400},
		{"399.4999999", 399},
		{"400.5", 401},
	}
	for _, tc := range cases {
		t.Run(tc.amount, func(t *testing.T) {
			client := platega.NewClientWithBaseURL("merchant", "secret", "https://platega.test")
			client.SetHTTPClient(&http.Client{
				Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
					body := fmt.Sprintf(`{"id":"tx-round","paymentDetails":{"amount":%s,"currency":"RUB"},"status":"CONFIRMED"}`, tc.amount)
					return &http.Response{StatusCode: http.StatusOK, Body: io.NopCloser(strings.NewReader(body)), Header: make(http.Header)}, nil
				}),
			})

			payment, err := platega.NewProvider(client).GetPayment("tx-round")

			require.NoError(t, err)
			require.Equal(t, tc.want, payment.Amount)
		})
	}
}
