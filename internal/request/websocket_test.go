package request

import (
	"context"
	"errors"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/baba983/vscroxy/internal/capture"
	"github.com/gorilla/websocket"
)

// testProxy is a running capture proxy on a free loopback port.
func testProxy(t *testing.T) *capture.Server {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	addr := ln.Addr().String()
	ln.Close()
	srv, err := capture.New(addr, t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if err := srv.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { srv.Close() })
	return srv
}

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
		if got := IsWebSocket(raw); got != want {
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

	srv := testProxy(t)
	proxyAddr := srv.Addr()

	target := "ws://" + strings.TrimPrefix(upstream.URL, "http://")
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	replies, err := wsSend(ctx, proxyAddr, &Prepared{URL: target, Body: "hello"}, srv.CACertificate(), nil, time.Second)
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

func TestWebSocketCancellationInterruptsTheReplyWait(t *testing.T) {
	started := make(chan struct{})
	upgrader := websocket.Upgrader{}
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		conn, err := upgrader.Upgrade(w, r, nil)
		if err != nil {
			return
		}
		defer conn.Close()
		close(started)
		for {
			if _, _, err := conn.ReadMessage(); err != nil {
				return
			}
		}
	}))
	defer upstream.Close()
	srv := testProxy(t)
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	done := make(chan error, 1)
	go func() {
		_, err := wsSend(ctx, srv.Addr(), &Prepared{URL: "ws" + upstream.URL[4:], Body: "hello"}, srv.CACertificate(), nil, time.Minute)
		done <- err
	}()
	select {
	case <-started:
	case <-time.After(3 * time.Second):
		t.Fatal("handshake did not finish")
	}
	cancel()
	select {
	case err := <-done:
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("cancel error: %v", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("cancellation left the socket waiting for replies")
	}
}
