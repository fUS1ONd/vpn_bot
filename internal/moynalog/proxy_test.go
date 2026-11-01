package moynalog

import (
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

// TestSetProxySendsRequestsThroughProxy: прокси получает соединение, а сервер
// напрямую — ни одного запроса, даже когда прокси не довёл запрос до конца.
func TestSetProxySendsRequestsThroughProxy(t *testing.T) {
	var direct atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
		direct.Add(1)
	}))
	defer server.Close()

	listener, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	defer listener.Close()
	var greetings atomic.Int32
	go func() {
		for {
			conn, err := listener.Accept()
			if err != nil {
				return
			}
			// Первый байт SOCKS5-приветствия — версия протокола.
			buf := make([]byte, 1)
			if _, err := conn.Read(buf); err == nil && buf[0] == 5 {
				greetings.Add(1)
			}
			_ = conn.Close()
		}
	}()

	c := NewClientWithBaseURL("123456789012", "secret", server.URL)
	c.SetProxy(&url.URL{Scheme: "socks5", Host: listener.Addr().String()})

	_, err = c.ListIncomes(time.Now().Add(-time.Hour), time.Now())

	require.Error(t, err)
	require.Equal(t, KindTransport, ErrorKind(err))
	require.Positive(t, greetings.Load())
	require.Zero(t, direct.Load())
}
