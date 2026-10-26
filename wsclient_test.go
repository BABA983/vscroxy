package main

import (
	"context"
	"errors"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/baba983/vscroxy/internal/capture"
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
	replies, err := wsSend(ctx, proxyAddr, &jsRequest{URL: target, Body: "hello"}, srv.CACertificate(), nil, time.Second)
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

func TestStructuredWebSocketSendsAuthHeadersAndCookiesWithVerifiedTLS(t *testing.T) {
	for _, secure := range []bool{false, true} {
		t.Run(map[bool]string{false: "ws", true: "wss"}[secure], func(t *testing.T) {
			upgrader := websocket.Upgrader{}
			handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				cookie, err := r.Cookie("session")
				if err != nil || cookie.Value != "session-19" || r.Header.Get("Authorization") != "Bearer token-27" || r.Header.Get("X-Client") != "script" || r.URL.EscapedPath() != "/deep/path%2Fseg" || r.URL.RawQuery != "tag=one&tag=two" {
					t.Errorf("upstream handshake: cookie=%v err=%v auth=%q client=%q path=%q query=%q", cookie, err, r.Header.Get("Authorization"), r.Header.Get("X-Client"), r.URL.EscapedPath(), r.URL.RawQuery)
					http.Error(w, "wrong handshake auth/headers/cookies/URL", 401)
					return
				}
				conn, err := upgrader.Upgrade(w, r, nil)
				if err != nil {
					return
				}
				defer conn.Close()
				kind, body, err := conn.ReadMessage()
				if err != nil {
					return
				}
				conn.WriteMessage(kind, append([]byte("echo:"), body...))
				conn.WriteMessage(websocket.CloseMessage, websocket.FormatCloseMessage(websocket.CloseNormalClosure, "done"))
			})
			upstream := httptest.NewUnstartedServer(handler)
			if secure {
				upstream.StartTLS()
			} else {
				upstream.Start()
			}
			defer upstream.Close()
			srv := requestProxy(t)
			srv.SetSSL(capture.SSLConfig{All: true})
			target := strings.Replace(upstream.URL, "127.0.0.1", "localhost", 1) + "/deep/path%2Fseg?tag=one&tag=two"
			u, _ := url.Parse(target)
			jar := &cookieStore{}
			jar.SetCookies(u, []*http.Cookie{{Name: "session", Value: "session-19", Path: "/deep", Secure: secure}})
			target = "ws" + strings.TrimPrefix(target, "http")
			got := executeRequest(t.Context(), srv, savedRequest{Method: "GET", URL: target, Body: "hello", Auth: requestAuth{Type: "Bearer", Token: "{{token}}"}, Pre: `req.set("X-Client","script")`}, map[string]string{"token": "token-27"}, jar)
			if got.err != nil || len(got.replies) != 1 || got.replies[0] != "echo:hello" {
				t.Fatalf("structured WS execution: %+v", got)
			}
		})
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
	srv := requestProxy(t)
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	done := make(chan error, 1)
	go func() {
		_, err := wsSend(ctx, srv.Addr(), &jsRequest{URL: "ws" + upstream.URL[4:], Body: "hello"}, srv.CACertificate(), nil, time.Minute)
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
