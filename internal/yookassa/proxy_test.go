package yookassa

import (
	"encoding/json"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"sync/atomic"
	"testing"

	"github.com/stretchr/testify/require"
)

// startSOCKS5 поднимает минимальный SOCKS5-сервер без авторизации и считает CONNECT.
func startSOCKS5(t *testing.T) (*url.URL, *atomic.Int32) {
	t.Helper()
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	t.Cleanup(func() { _ = listener.Close() })

	var connects atomic.Int32
	go func() {
		for {
			conn, err := listener.Accept()
			if err != nil {
				return
			}
			go serveSOCKS5(conn, &connects)
		}
	}()
	return &url.URL{Scheme: "socks5", Host: listener.Addr().String()}, &connects
}

func serveSOCKS5(conn net.Conn, connects *atomic.Int32) {
	defer conn.Close()
	greeting := make([]byte, 2)
	if _, err := io.ReadFull(conn, greeting); err != nil {
		return
	}
	if _, err := io.ReadFull(conn, make([]byte, greeting[1])); err != nil {
		return
	}
	if _, err := conn.Write([]byte{5, 0}); err != nil {
		return
	}
	// Запрос: версия, CONNECT, резерв, тип адреса IPv4, адрес, порт.
	request := make([]byte, 10)
	if _, err := io.ReadFull(conn, request); err != nil {
		return
	}
	connects.Add(1)
	target := net.TCPAddr{
		IP:   net.IP(request[4:8]),
		Port: int(request[8])<<8 | int(request[9]),
	}
	upstream, err := net.Dial("tcp", target.String())
	if err != nil {
		return
	}
	defer upstream.Close()
	if _, err := conn.Write([]byte{5, 0, 0, 1, 0, 0, 0, 0, 0, 0}); err != nil {
		return
	}
	go func() { _, _ = io.Copy(upstream, conn) }()
	_, _ = io.Copy(conn, upstream)
}

func TestSetProxySendsRequestsThroughProxy(t *testing.T) {
	proxy, connects := startSOCKS5(t)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{
			"id": "pay-1", "status": "pending", "amount": map[string]string{"value": "400.00", "currency": "RUB"},
		})
	}))
	defer server.Close()

	c := NewClientWithBaseURL("shop-1", "secret-1", server.URL)
	c.SetProxy(proxy)

	_, err := c.GetPayment("pay-1")

	require.NoError(t, err)
	require.Equal(t, int32(1), connects.Load())
}

func TestSetProxyDoesNotFallBackToDirectConnection(t *testing.T) {
	var direct atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
		direct.Add(1)
	}))
	defer server.Close()

	// Порт только что освобождён: прокси там не отвечает.
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	deadProxy := &url.URL{Scheme: "socks5", Host: listener.Addr().String()}
	require.NoError(t, listener.Close())

	c := NewClientWithBaseURL("shop-1", "secret-1", server.URL)
	c.SetProxy(deadProxy)
	c.SetRetryBackoff(0)

	_, err = c.GetPayment("pay-1")

	require.Error(t, err)
	require.Zero(t, direct.Load())
}
