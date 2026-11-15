package capture

import (
	"net/http"
	"net/http/httptest"
	"net/url"
	"sync/atomic"
	"testing"
	"time"

	"github.com/gorilla/websocket"

	"github.com/baba983/vscroxy/internal/rules"
)

// wsDialer opens WebSockets through s.
func wsDialer(t *testing.T, s *Server) *websocket.Dialer {
	t.Helper()
	proxyURL, err := url.Parse("http://" + s.Addr())
	if err != nil {
		t.Fatal(err)
	}
	return &websocket.Dialer{Proxy: http.ProxyURL(proxyURL)}
}

func blockEchoServer(t *testing.T, hits *atomic.Int32) *httptest.Server {
	t.Helper()
	upgrader := websocket.Upgrader{CheckOrigin: func(*http.Request) bool { return true }}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		conn, err := upgrader.Upgrade(w, r, nil)
		if err != nil {
			return
		}
		defer conn.Close()
		hits.Add(1)
		for {
			kind, msg, err := conn.ReadMessage()
			if err != nil {
				return
			}
			_ = conn.WriteMessage(kind, append([]byte("echo:"), msg...))
		}
	}))
	t.Cleanup(server.Close)
	return server
}

func wsTarget(t *testing.T, server *httptest.Server) string {
	t.Helper()
	u, err := url.Parse(server.URL)
	if err != nil {
		t.Fatal(err)
	}
	return "ws://" + u.Host + "/ws"
}

func TestBlockRuleAnswersWebSocketUpgrade(t *testing.T) {
	var hits atomic.Int32
	upstream := blockEchoServer(t, &hits)
	s, _ := startServer(t)
	s.SetSSL(SSLConfig{All: true})
	if err := s.SetRules([]Rule{{Name: "refuse", Type: RuleBlock, Match: "/ws"}}); err != nil {
		t.Fatal(err)
	}
	conn, resp, err := wsDialer(t, s).Dial(wsTarget(t, upstream), nil)
	if err == nil {
		conn.Close()
		t.Fatal("a blocked WebSocket upgraded")
	}
	if resp == nil || resp.StatusCode != http.StatusForbidden {
		t.Fatalf("blocked handshake response: %+v, %v", resp, err)
	}
	f := waitFlow(t, s)
	if f.Kind != "ws" || f.Status != http.StatusForbidden || f.Method != "WS" {
		t.Fatalf("blocked handshake row: kind %q status %d method %q", f.Kind, f.Status, f.Method)
	}
	if !containsAction(f.Actions, "Block · /ws") {
		t.Fatalf("blocked handshake actions: %v", f.Actions)
	}
	if hits.Load() != 0 {
		t.Fatalf("a blocked handshake reached the server %d times", hits.Load())
	}
}

func TestBlockRuleHidesWebSocketUpgrade(t *testing.T) {
	var hits atomic.Int32
	upstream := blockEchoServer(t, &hits)
	s, _ := startServer(t)
	s.SetSSL(SSLConfig{All: true})
	if err := s.SetRules([]Rule{{Type: RuleBlock, Match: "/ws", Block: BlockHide}}); err != nil {
		t.Fatal(err)
	}
	conn, _, err := wsDialer(t, s).Dial(wsTarget(t, upstream), nil)
	if err != nil {
		t.Fatal(err)
	}
	if err := conn.WriteMessage(websocket.TextMessage, []byte("hello")); err != nil {
		t.Fatal(err)
	}
	_ = conn.SetReadDeadline(time.Now().Add(5 * time.Second))
	if _, msg, err := conn.ReadMessage(); err != nil || string(msg) != "echo:hello" {
		t.Fatalf("echo %q: %v", msg, err)
	}
	conn.Close()
	// A hidden handshake leaves no row; the next visible request proves the
	// stream works and that nothing was published for the WebSocket.
	if err := s.SetRules(nil); err != nil {
		t.Fatal(err)
	}
	client := clientFor(t, s, nil)
	if resp, err := client.Get(upstream.URL + "/after"); err != nil {
		t.Fatal(err)
	} else {
		resp.Body.Close()
	}
	f := waitFlow(t, s)
	if f.URL != upstream.URL+"/after" {
		t.Fatalf("the hidden WebSocket has a row: %+v", f)
	}
	if hits.Load() != 1 { // only the WebSocket upgrade; the plain GET is not an upgrade
		t.Fatalf("upstream hits %d", hits.Load())
	}
}

func TestBlockAndHideRefusesWithoutARow(t *testing.T) {
	var hits atomic.Int32
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits.Add(1)
		w.Write([]byte("upstream"))
	}))
	defer upstream.Close()
	s, _ := startServer(t)
	client := clientFor(t, s, nil)
	if err := s.SetRules([]Rule{{Type: RuleBlock, Match: "/refuse-hide", Block: BlockRefuseHide}}); err != nil {
		t.Fatal(err)
	}
	resp, err := client.Get(upstream.URL + "/refuse-hide")
	if err != nil {
		t.Fatal(err)
	}
	body := make([]byte, 64)
	n, _ := resp.Body.Read(body)
	resp.Body.Close()
	if resp.StatusCode != http.StatusForbidden || string(body[:n]) != "blocked by vscroxy\n" {
		t.Fatalf("block and hide answered %d %q", resp.StatusCode, body[:n])
	}
	resp, err = client.Get(upstream.URL + "/after")
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	f := waitFlow(t, s)
	if f.URL != upstream.URL+"/after" {
		t.Fatalf("block and hide left a row: %+v", f)
	}
	if hits.Load() != 1 {
		t.Fatalf("upstream hits %d, want only the visible request", hits.Load())
	}
}

func TestBlockRulePreviewActsOnWebSocketHandshakes(t *testing.T) {
	req := rules.Request{URL: "wss://example.test/ws", Method: "GET", Host: "example.test"}
	p := PreviewRules([]Rule{{Type: RuleBlock, Match: "/ws"}}, req, false)
	if p[0].Step != 1 || p[0].Reason != "" {
		t.Fatalf("Block preview: %+v", p[0])
	}
	p = PreviewRules([]Rule{{Type: RuleMapLocal, Match: "/ws", Target: "/tmp/x"}}, req, false)
	if p[0].Step != 0 || p[0].Reason != "does not act on WebSockets" {
		t.Fatalf("Map Local preview: %+v", p[0])
	}
}

func containsAction(actions []string, want string) bool {
	for _, action := range actions {
		if action == want {
			return true
		}
	}
	return false
}
