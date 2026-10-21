package main

import (
	"context"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/baba983/vscroxy/desktop/internal/capture"
	"github.com/gorilla/websocket"
)

func TestIsWebSocket(t *testing.T) {
	cases := map[string]bool{
		"ws://example.com/socket":    true,
		"wss://example.com/socket":   true,
		"WSS://example.com/socket":   true,
		"http://example.com/socket":  false,
		"https://example.com/socket": false,
		"example.com/socket":         false,
		"":                           false,
	}
	for raw, want := range cases {
		if got := isWebSocket(raw); got != want {
			t.Errorf("isWebSocket(%q) = %v, want %v", raw, got, want)
		}
	}
}

// TestWSSendThroughTheProxy covers the whole path a composed WebSocket takes:
// the message goes through the proxy, which captures the frames, and the echo
// comes back.
func TestWSSendThroughTheProxy(t *testing.T) {
	upgrader := websocket.Upgrader{}
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		conn, err := upgrader.Upgrade(w, r, nil)
		if err != nil {
			return
		}
		defer conn.Close()
		for {
			kind, data, err := conn.ReadMessage()
			if err != nil {
				return
			}
			if err := conn.WriteMessage(kind, data); err != nil {
				return
			}
		}
	}))
	defer upstream.Close()

	proxyAddr := freeProxyAddr(t)
	srv, err := capture.New(proxyAddr, t.TempDir())
	if err != nil {
		t.Fatalf("proxy: %v", err)
	}
	if err := srv.Start(); err != nil {
		t.Fatalf("start: %v", err)
	}
	defer srv.Close()

	target := "ws://" + strings.TrimPrefix(upstream.URL, "http://")
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	replies, err := wsSend(ctx, proxyAddr, target, "hello", time.Second)
	if err != nil {
		t.Fatalf("send: %v", err)
	}
	if len(replies) != 1 || replies[0] != "hello" {
		t.Fatalf("replies = %q, want the echo", replies)
	}

	// The proxy carried the exchange, so it shows in the table: a ws://
	// connection arrives as a tunnel, while a wss:// one the proxy decrypts
	// shows its frames too.
	select {
	case f := <-srv.Flows():
		if f.URL == "" {
			t.Fatalf("flow = %+v, want the tunnel the WebSocket came through", f)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("the proxy published no flow for the WebSocket")
	}
}

// freeProxyAddr is a loopback address nothing listens on.
func freeProxyAddr(t *testing.T) string {
	t.Helper()
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	addr := l.Addr().String()
	_ = l.Close()
	return addr
}
