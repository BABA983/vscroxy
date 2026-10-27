package app

import (
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"github.com/baba983/vscroxy/internal/capture"
	"github.com/baba983/vscroxy/internal/request"
	"github.com/gorilla/websocket"
)

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
			got := savedRequest{Method: "GET", URL: target, Body: "hello", Auth: request.Auth{Type: "Bearer", Token: "{{token}}"}, Pre: `req.set("X-Client","script")`}.Execute(t.Context(), srv, request.Context{}, map[string]string{"token": "token-27"}, jar)
			if got.Err != nil || len(got.Replies) != 1 || got.Replies[0] != "echo:hello" {
				t.Fatalf("structured WS execution: %+v", got)
			}
		})
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
