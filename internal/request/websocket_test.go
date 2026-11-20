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
	"sync"
	"testing"
	"time"

	"github.com/baba983/vscroxy/internal/capture"
	"github.com/baba983/vscroxy/internal/certs"
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

// trackWS closes the test server's upgraded sockets at the end of a test.
// httptest.Server.Close does not own hijacked connections, so a handler
// reading from one would otherwise outlive the test that opened it.
func trackWS(t *testing.T) func(*websocket.Conn) {
	t.Helper()
	var mu sync.Mutex
	var conns []*websocket.Conn
	t.Cleanup(func() {
		mu.Lock()
		open := slices.Clone(conns)
		mu.Unlock()
		for _, conn := range open {
			_ = conn.Close()
		}
	})
	return func(conn *websocket.Conn) {
		mu.Lock()
		conns = append(conns, conn)
		mu.Unlock()
	}
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
				c, err := DialWebSocket(ctx, proxy.Addr(), &Prepared{URL: "ws" + strings.TrimPrefix(upstream.URL, "http"), Header: header}, proxy.CACertificate(), certs.Settings{}, nil)
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
		if c, err := DialWebSocket(t.Context(), "", &Prepared{Header: http.Header{"Sec-Websocket-Protocol": {value}}}, nil, certs.Settings{}, nil); err == nil {
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
			c, err := DialWebSocket(ctx, proxy.Addr(), &Prepared{URL: "ws" + strings.TrimPrefix(upstream.URL, "http"), Header: http.Header{"Sec-Websocket-Protocol": {"offered"}}}, proxy.CACertificate(), certs.Settings{}, nil)
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
	track := trackWS(t)
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		conn, err := upgrader.Upgrade(w, r, nil)
		if err != nil {
			return
		}
		track(conn)
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
	exchanges, pushed, err := wsSendMessages(ctx, proxyAddr, &Prepared{URL: target}, []WebSocketMessage{{Payload: "hello"}}, time.Second, srv.CACertificate(), certs.Settings{}, nil)
	if err != nil {
		t.Fatalf("send: %v", err)
	}
	if len(exchanges) != 1 || len(exchanges[0].Replies) != 1 || exchanges[0].Replies[0] != "hello" || len(pushed) != 0 {
		t.Fatalf("exchanges = %+v, pushed %q, want the echo", exchanges, pushed)
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
	track := trackWS(t)
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		conn, err := upgrader.Upgrade(w, r, nil)
		if err != nil {
			return
		}
		track(conn)
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
		_, _, err := wsSendMessages(ctx, srv.Addr(), &Prepared{URL: "ws" + upstream.URL[4:]}, []WebSocketMessage{{Payload: "hello"}}, time.Minute, srv.CACertificate(), certs.Settings{}, nil)
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

// wsEchoServer echoes every message and records the frames it saw.
func wsEchoServer(t *testing.T, seen *[][]string) *httptest.Server {
	t.Helper()
	upgrader := websocket.Upgrader{}
	track := trackWS(t)
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		conn, err := upgrader.Upgrade(w, r, nil)
		if err != nil {
			return
		}
		track(conn)
		defer conn.Close()
		for {
			kind, data, err := conn.ReadMessage()
			if err != nil {
				return
			}
			*seen = append(*seen, []string{map[int]string{websocket.TextMessage: "text", websocket.BinaryMessage: "binary"}[kind], string(data)})
			if err := conn.WriteMessage(kind, append([]byte("echo:"), data...)); err != nil {
				return
			}
		}
	}))
}

func TestWebSocketSendsEverySavedMessageInOrder(t *testing.T) {
	var seen [][]string
	upstream := wsEchoServer(t, &seen)
	defer upstream.Close()
	srv := testProxy(t)
	messages := []WebSocketMessage{{Payload: "login"}, {Payload: "subscribe"}, {Payload: "ping"}}
	exchanges, pushed, err := wsSendMessages(t.Context(), srv.Addr(), &Prepared{URL: "ws" + upstream.URL[4:]}, messages, 400*time.Millisecond, srv.CACertificate(), certs.Settings{}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(pushed) != 0 {
		t.Fatalf("pushed = %q", pushed)
	}
	if len(exchanges) != 3 {
		t.Fatalf("exchanges = %+v", exchanges)
	}
	for i, want := range []string{"echo:login", "echo:subscribe", "echo:ping"} {
		if len(exchanges[i].Replies) != 1 || exchanges[i].Replies[0] != want {
			t.Fatalf("exchange %d = %+v, want %q", i, exchanges[i], want)
		}
		if exchanges[i].Message.Payload != messages[i].Payload {
			t.Fatalf("exchange %d kept %q, want %q", i, exchanges[i].Message.Payload, messages[i].Payload)
		}
	}
	if len(seen) != 3 || seen[0][1] != "login" || seen[1][1] != "subscribe" || seen[2][1] != "ping" {
		t.Fatalf("server saw %v", seen)
	}
}

func TestWebSocketMessageEncodingsSendBinaryFrames(t *testing.T) {
	var seen [][]string
	upstream := wsEchoServer(t, &seen)
	defer upstream.Close()
	srv := testProxy(t)
	messages := []WebSocketMessage{
		{Encoding: "Hex", Payload: "00 ff 10"},
		{Encoding: "Base64", Payload: "AAEC"},
		{Payload: "plain"},
	}
	if _, _, err := wsSendMessages(t.Context(), srv.Addr(), &Prepared{URL: "ws" + upstream.URL[4:]}, messages, 300*time.Millisecond, srv.CACertificate(), certs.Settings{}, nil); err != nil {
		t.Fatal(err)
	}
	want := [][]string{{"binary", "\x00\xff\x10"}, {"binary", "\x00\x01\x02"}, {"text", "plain"}}
	if !slices.EqualFunc(seen, want, slices.Equal[[]string]) {
		t.Fatalf("server saw %v, want %v", seen, want)
	}
	for _, bad := range []WebSocketMessage{
		{Encoding: "Hex", Payload: "zz"},
		{Encoding: "Base64", Payload: "!"},
		{Encoding: "Utf16", Payload: "x"},
		{Encoding: "Text", Payload: string([]byte{0xff})},
	} {
		if _, _, err := bad.Decode(); err == nil {
			t.Fatalf("%+v decoded", bad)
		}
	}
}

func TestWebSocketWithoutMessagesListensForPushes(t *testing.T) {
	upgrader := websocket.Upgrader{}
	track := trackWS(t)
	done := make(chan struct{})
	t.Cleanup(func() { close(done) })
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		conn, err := upgrader.Upgrade(w, r, nil)
		if err != nil {
			return
		}
		track(conn)
		defer conn.Close()
		conn.WriteMessage(websocket.TextMessage, []byte("first"))
		conn.WriteMessage(websocket.TextMessage, []byte("second"))
		select {
		case <-r.Context().Done():
		case <-done:
		}
	}))
	defer upstream.Close()
	srv := testProxy(t)
	exchanges, pushed, err := wsSendMessages(t.Context(), srv.Addr(), &Prepared{URL: "ws" + upstream.URL[4:]}, nil, 500*time.Millisecond, srv.CACertificate(), certs.Settings{}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(exchanges) != 0 || !slices.Equal(pushed, []string{"first", "second"}) {
		t.Fatalf("exchanges = %+v, pushed = %q", exchanges, pushed)
	}
}

func TestWebSocketStopsWhenTheServerClosesBeforeTheLastMessage(t *testing.T) {
	upgrader := websocket.Upgrader{}
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		conn, err := upgrader.Upgrade(w, r, nil)
		if err != nil {
			return
		}
		conn.ReadMessage()
		conn.WriteMessage(websocket.TextMessage, []byte("only"))
		conn.WriteMessage(websocket.CloseMessage, websocket.FormatCloseMessage(websocket.CloseNormalClosure, ""))
		conn.Close()
	}))
	defer upstream.Close()
	srv := testProxy(t)
	messages := []WebSocketMessage{{Payload: "one"}, {Payload: "two"}}
	exchanges, _, err := wsSendMessages(t.Context(), srv.Addr(), &Prepared{URL: "ws" + upstream.URL[4:]}, messages, time.Second, srv.CACertificate(), certs.Settings{}, nil)
	if err == nil || !strings.Contains(err.Error(), "before every message") {
		t.Fatalf("err = %v, exchanges = %+v", err, exchanges)
	}
	if len(exchanges) != 1 || !slices.Equal(exchanges[0].Replies, []string{"only"}) {
		t.Fatalf("exchanges = %+v", exchanges)
	}
}

func TestWebSocketExecutionSendsTheSavedListWithVariables(t *testing.T) {
	var seen [][]string
	upstream := wsEchoServer(t, &seen)
	defer upstream.Close()
	srv := testProxy(t)
	req := Request{
		Method: "GET", URL: "ws" + upstream.URL[4:],
		WebSocket: WebSocketConfig{Messages: []WebSocketMessage{{Payload: "hello {{name}}"}}, WaitMS: 400},
	}
	got := req.Execute(t.Context(), srv, Context{}, map[string]string{"name": "world"}, nil)
	if got.Err != nil {
		t.Fatal(got.Err)
	}
	if len(got.WebSocket) != 1 || !slices.Equal(got.WebSocket[0].Replies, []string{"echo:hello world"}) {
		t.Fatalf("exchanges = %+v", got.WebSocket)
	}
	if !slices.Equal(got.Replies, []string{"echo:hello world"}) {
		t.Fatalf("replies = %q", got.Replies)
	}
	if len(seen) != 1 || seen[0][1] != "hello world" {
		t.Fatalf("server saw %v", seen)
	}
}

func TestWebSocketExecutionFallsBackToTheBody(t *testing.T) {
	var seen [][]string
	upstream := wsEchoServer(t, &seen)
	defer upstream.Close()
	srv := testProxy(t)
	got := Request{Method: "GET", URL: "ws" + upstream.URL[4:], Body: "from the body"}.Execute(t.Context(), srv, Context{}, nil, nil)
	if got.Err != nil || !slices.Equal(got.Replies, []string{"echo:from the body"}) {
		t.Fatalf("got %+v", got)
	}
}

func TestWebSocketWaitDefaultsAndClamps(t *testing.T) {
	cases := map[int]time.Duration{0: DefaultWebSocketWait, -5: DefaultWebSocketWait, 500: 500 * time.Millisecond, 600000: MaxWebSocketWait}
	for ms, want := range cases {
		if got := (WebSocketConfig{WaitMS: ms}).Wait(); got != want {
			t.Fatalf("WaitMS %d = %v, want %v", ms, got, want)
		}
	}
}

// A WebSocket send now exposes its replies to Checks and post-response
// scripts as a synthetic 101 response with a JSON body.
func TestWebSocketExecuteRunsChecksAndPostScript(t *testing.T) {
	var seen [][]string
	upstream := wsEchoServer(t, &seen)
	srv := testProxy(t)
	r := Request{
		Name: "Echo", URL: "ws" + upstream.URL[4:],
		WebSocket: WebSocketConfig{Messages: []WebSocketMessage{{Payload: "one"}, {Payload: "two"}}, WaitMS: 150},
		Checks:    Checks{Status: "101", Headers: "X-Vscroxy-Sent: 2\nX-Vscroxy-Received: 2", JSON: `/0 = "echo:one"` + "\n" + `/1 = "echo:two"`},
		Post:      `pm.test("two replies", () => pm.expect(pm.response.json()).to.have.length(2)); pm.globals.set("ws-done", "yes")`,
	}
	got := r.Execute(t.Context(), srv, Context{}, nil, nil)
	if got.Err != nil {
		t.Fatalf("err: %v", got.Err)
	}
	if got.Response == nil || got.Response.Status != 101 || got.Response.ResponseBody != `["echo:one","echo:two"]` {
		t.Fatalf("response: %+v", got.Response)
	}
	if len(got.Tests) != 6 {
		t.Fatalf("tests: %+v", got.Tests)
	}
	for _, test := range got.Tests {
		if !test.Pass {
			t.Errorf("test %q failed: %s", test.Name, test.Error)
		}
	}
	if len(got.Writes) != 1 || got.Writes[0] != (Write{Scope: "global", Name: "ws-done", Value: "yes"}) {
		t.Fatalf("writes: %+v", got.Writes)
	}
}
