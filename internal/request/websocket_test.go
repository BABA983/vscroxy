package request

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/baba983/vscroxy/internal/capture"
	"github.com/gorilla/websocket"
)

// testProxy is a running capture proxy on an OS-assigned loopback port.
func testProxy(t *testing.T) *capture.Server {
	t.Helper()
	srv, err := capture.New("127.0.0.1:0", t.TempDir())
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

func TestWebSocketSubprotocolAndBinaryThroughWSAndWSS(t *testing.T) {
	for _, secure := range []bool{false, true} {
		for _, protocol := range []string{"binary.v2", ""} {
			t.Run(fmt.Sprintf("TLS=%t/selected=%s", secure, protocol), func(t *testing.T) {
				upgrader := websocket.Upgrader{}
				if protocol != "" {
					upgrader.Subprotocols = []string{protocol}
				}
				upstream := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					if r.Header.Get("Authorization") != "Bearer local-only" || !slices.Equal(websocket.Subprotocols(r), []string{"binary.v1", "binary.v2"}) {
						http.Error(w, "wrong handshake", 400)
						return
					}
					c, err := upgrader.Upgrade(w, r, nil)
					if err != nil {
						return
					}
					defer c.Close()
					kind, data, err := c.ReadMessage()
					if err == nil {
						c.WriteMessage(kind, data)
					}
				}))
				if secure {
					upstream.StartTLS()
				} else {
					upstream.Start()
				}
				defer upstream.Close()
				proxy := testProxy(t)
				proxy.SetSSL(capture.SSLConfig{All: true})
				header := http.Header{"Authorization": {"Bearer local-only"}, "Sec-Websocket-Protocol": {"binary.v1, binary.v2"}}
				ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
				defer cancel()
				c, err := DialWebSocket(ctx, proxy.Addr(), &Prepared{URL: "ws" + strings.TrimPrefix(upstream.URL, "http"), Header: header}, proxy.CACertificate(), nil)
				if err != nil {
					t.Fatal(err)
				}
				defer c.Close()
				if c.Subprotocol() != protocol || header.Get("Sec-WebSocket-Protocol") != "binary.v1, binary.v2" {
					t.Fatalf("wrong selection or mutated authored headers: %q", c.Subprotocol())
				}
				want := []byte{0xff, 0, 0x73, 0xc3, 0x28}
				c.SetReadDeadline(time.Now().Add(5 * time.Second))
				if err := c.WriteMessage(websocket.BinaryMessage, want); err != nil {
					t.Fatal(err)
				}
				kind, got, err := c.ReadMessage()
				if err != nil || kind != websocket.BinaryMessage || !bytes.Equal(got, want) {
					t.Fatalf("binary changed: kind=%d bytes=%x err=%v", kind, got, err)
				}
			})
		}
	}
	for _, value := range []string{"one, one", "one,,two", "has space", "bad\r\nvalue"} {
		if c, err := DialWebSocket(t.Context(), "", &Prepared{Header: http.Header{"Sec-Websocket-Protocol": {value}}}, nil, nil); err == nil {
			c.Close()
			t.Fatalf("accepted invalid protocols %q", value)
		}
	}
	for _, secure := range []bool{false, true} {
		t.Run(fmt.Sprintf("reject-unoffered/TLS=%t", secure), func(t *testing.T) {
			upstream := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				upgrader := websocket.Upgrader{}
				c, err := upgrader.Upgrade(w, r, http.Header{"Sec-Websocket-Protocol": {"unoffered"}})
				if err == nil {
					c.Close()
				}
			}))
			if secure {
				upstream.StartTLS()
			} else {
				upstream.Start()
			}
			defer upstream.Close()
			proxy := testProxy(t)
			proxy.SetSSL(capture.SSLConfig{All: true})
			ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
			defer cancel()
			c, err := DialWebSocket(ctx, proxy.Addr(), &Prepared{URL: "ws" + strings.TrimPrefix(upstream.URL, "http"), Header: http.Header{"Sec-Websocket-Protocol": {"offered"}}}, proxy.CACertificate(), nil)
			if err == nil {
				c.Close()
				t.Fatal("accepted an unoffered upstream protocol")
			}
		})
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
