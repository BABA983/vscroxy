package request

import (
	"context"
	"net/http"
	"net/http/httptest"
	"net/url"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/baba983/vscroxy/internal/capture"
	"github.com/gorilla/websocket"
)

// wsScripts adapts parsed rule scripts to capture.ScriptRunner, as the app's
// script store does.
type wsScripts map[string]*ProxyScript

func (s wsScripts) Hooks(name string) (bool, bool, bool) {
	p := s[name]
	if p == nil {
		return false, false, false
	}
	return p.OnRequest, p.OnResponse, p.OnMessage
}

func (s wsScripts) Run(ctx context.Context, name string, x *capture.ScriptExchange) error {
	p := s[name]
	if p == nil {
		return nil
	}
	_, err := p.Run(ctx, x, ProxyEnv{})
	return err
}

// wsScriptProxy starts a proxy whose /socket rule runs source on each message.
func wsScriptProxy(t *testing.T, source string) (*capture.Server, *websocket.Conn, *httptest.Server) {
	t.Helper()
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
			if conn.WriteMessage(kind, data) != nil {
				return
			}
		}
	}))
	t.Cleanup(upstream.Close)
	script, err := ParseProxyScript("inject", source)
	if err != nil {
		t.Fatal(err)
	}
	srv := testProxy(t)
	// A WebSocket through the proxy arrives in a CONNECT tunnel; decrypting
	// (inspecting) the tunnel is what lets the message hooks see it.
	srv.SetSSL(capture.SSLConfig{All: true})
	srv.SetScriptRunner(wsScripts{"inject.js": script})
	if err := srv.SetRules([]capture.Rule{{Type: capture.RuleScript, Match: "/socket", Target: "inject.js"}}); err != nil {
		t.Fatal(err)
	}
	proxyURL, _ := url.Parse("http://" + srv.Addr())
	target := "ws" + strings.TrimPrefix(upstream.URL, "http") + "/socket"
	conn, _, err := (&websocket.Dialer{Proxy: http.ProxyURL(proxyURL)}).Dial(target, nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = conn.Close() })
	return srv, conn, upstream
}

func TestWebSocketScriptInjectsMessages(t *testing.T) {
	_, conn, _ := wsScriptProxy(t, `export function onMessage(message) {
	pm.websocket.send("from script: " + message.text());
	pm.websocket.sendBase64("AP8=");
}`)
	conn.SetReadDeadline(time.Now().Add(10 * time.Second))
	if err := conn.WriteMessage(websocket.TextMessage, []byte("hello")); err != nil {
		t.Fatal(err)
	}
	var kinds []int
	var texts []string
	for range 3 {
		kind, data, err := conn.ReadMessage()
		if err != nil {
			t.Fatalf("read: %v (got %v %q)", err, kinds, texts)
		}
		kinds = append(kinds, kind)
		texts = append(texts, string(data))
	}
	if !slices.Equal(kinds, []int{websocket.TextMessage, websocket.TextMessage, websocket.BinaryMessage}) {
		t.Fatalf("kinds = %v", kinds)
	}
	if texts[0] != "hello" || texts[1] != "from script: hello" || !slices.Equal([]byte(texts[2]), []byte{0, 255}) {
		t.Fatalf("texts = %q", texts)
	}
}

// A script on a message the server sent still addresses the server: the
// upstream echoes the injected frame back to the client.
func TestWebSocketScriptSendsToServerFromServerMessage(t *testing.T) {
	upgrader := websocket.Upgrader{}
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		conn, err := upgrader.Upgrade(w, r, nil)
		if err != nil {
			return
		}
		defer conn.Close()
		if conn.WriteMessage(websocket.TextMessage, []byte("welcome")) != nil {
			return
		}
		kind, data, err := conn.ReadMessage()
		if err != nil {
			return
		}
		conn.WriteMessage(kind, append([]byte("echo:"), data...))
	}))
	t.Cleanup(upstream.Close)
	script, err := ParseProxyScript("inject", `export function onMessage(message) {
	if (message.direction === "in") pm.websocket.send("reply:" + message.text());
}`)
	if err != nil {
		t.Fatal(err)
	}
	srv := testProxy(t)
	srv.SetSSL(capture.SSLConfig{All: true})
	srv.SetScriptRunner(wsScripts{"inject.js": script})
	if err := srv.SetRules([]capture.Rule{{Type: capture.RuleScript, Match: "/socket", Target: "inject.js"}}); err != nil {
		t.Fatal(err)
	}
	proxyURL, _ := url.Parse("http://" + srv.Addr())
	target := "ws" + strings.TrimPrefix(upstream.URL, "http") + "/socket"
	conn, _, err := (&websocket.Dialer{Proxy: http.ProxyURL(proxyURL)}).Dial(target, nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = conn.Close() })
	conn.SetReadDeadline(time.Now().Add(10 * time.Second))
	for _, want := range []string{"welcome", "echo:reply:welcome"} {
		kind, data, err := conn.ReadMessage()
		if err != nil {
			t.Fatalf("read %q: %v", want, err)
		}
		if kind != websocket.TextMessage || string(data) != want {
			t.Fatalf("got %d %q, want %q", kind, data, want)
		}
	}
}

func TestWebSocketScriptClosesTheConnection(t *testing.T) {
	_, conn, _ := wsScriptProxy(t, `export function onMessage(message) {
	pm.websocket.close(4001, "done by script");
}`)
	conn.SetReadDeadline(time.Now().Add(10 * time.Second))
	if err := conn.WriteMessage(websocket.TextMessage, []byte("hello")); err != nil {
		t.Fatal(err)
	}
	// The message is forwarded and echoed, then the connection closes.
	for {
		if _, _, err := conn.ReadMessage(); err != nil {
			closed, ok := err.(*websocket.CloseError)
			if !ok || closed.Code != 4001 || closed.Text != "done by script" {
				t.Fatalf("close = %v", err)
			}
			return
		}
	}
}

func TestWebSocketScriptValidatesInjectedFrames(t *testing.T) {
	script, err := ParseProxyScript("inject", `export function onMessage(message) {
	pm.websocket.sendBase64("not base64!");
}`)
	if err != nil {
		t.Fatal(err)
	}
	x := &capture.ScriptExchange{Phase: "message", URL: "ws://api.test/socket", Header: http.Header{},
		Message: &capture.ScriptMessage{Type: "text", Direction: "out", Data: []byte("hello")}, Locals: map[string]string{}}
	if _, err := script.Run(t.Context(), x, ProxyEnv{}); err == nil || !strings.Contains(err.Error(), "base64") {
		t.Fatalf("err = %v", err)
	}
	if len(x.Send) != 0 || x.Close != nil {
		t.Fatalf("a failed script published %+v", x)
	}
	script, err = ParseProxyScript("inject", `export function onMessage(message) {
	pm.websocket.close(1005, "reserved");
}`)
	if err != nil {
		t.Fatal(err)
	}
	x = &capture.ScriptExchange{Phase: "message", URL: "ws://api.test/socket", Header: http.Header{},
		Message: &capture.ScriptMessage{Type: "text", Direction: "out", Data: []byte("hello")}, Locals: map[string]string{}}
	if _, err := script.Run(t.Context(), x, ProxyEnv{}); err == nil || !strings.Contains(err.Error(), "1005") {
		t.Fatalf("err = %v", err)
	}
	if x.Close != nil {
		t.Fatal("a reserved close code was accepted")
	}
}
