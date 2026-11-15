package request

import (
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/cookiejar"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// startUnixHTTPServer serves HTTP on a Unix domain socket and reports each
// request. The socket lives in a short temporary directory because macOS
// limits a socket path to roughly a hundred bytes.
func startUnixHTTPServer(t *testing.T, handler http.Handler) string {
	t.Helper()
	dir, err := os.MkdirTemp("", "vscroxy-sock")
	if err != nil {
		t.Fatal(err)
	}
	socket := filepath.Join(dir, "api.sock")
	listener, err := net.Listen("unix", socket)
	if err != nil {
		os.RemoveAll(dir)
		t.Fatal(err)
	}
	server := &http.Server{Handler: handler}
	go func() { _ = server.Serve(listener) }()
	t.Cleanup(func() {
		_ = server.Close()
		_ = listener.Close()
		_ = os.RemoveAll(dir)
	})
	return socket
}

func TestUnixSocketSend(t *testing.T) {
	type seenRequest struct {
		host, path, body, test, cookie string
	}
	seen := make(chan seenRequest, 1)
	socket := startUnixHTTPServer(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		seen <- seenRequest{host: r.Host, path: r.URL.RequestURI(), body: string(body),
			test: r.Header.Get("X-Test"), cookie: r.Header.Get("Cookie")}
		w.Header().Set("X-Reply", "socket")
		fmt.Fprint(w, "hello from socket")
	}))
	jar, err := cookiejar.New(nil)
	if err != nil {
		t.Fatal(err)
	}
	u, _ := url.Parse("http://socket.test/")
	jar.SetCookies(u, []*http.Cookie{{Name: "sid", Value: "abc", Path: "/"}})
	r := Request{Method: "POST", URL: "http://socket.test/orders?x=1", Headers: "X-Test: yes",
		BodyMode: "Raw", Body: "payload", Settings: Settings{SocketPath: socket}}
	out := r.Execute(t.Context(), testProxy(t), Context{}, nil, jar)
	if out.Err != nil {
		t.Fatalf("%s: %v", out.Stage, out.Err)
	}
	if out.Response == nil || out.Response.Status != http.StatusOK || out.Response.ResponseBody != "hello from socket" {
		t.Fatalf("response = %+v", out.Response)
	}
	if out.Response.ResponseHeader.Get("X-Reply") != "socket" {
		t.Fatalf("response headers = %v", out.Response.ResponseHeader)
	}
	got := <-seen
	if got.host != "socket.test" || got.path != "/orders?x=1" || got.body != "payload" || got.test != "yes" {
		t.Fatalf("server saw %+v", got)
	}
	if got.cookie != "sid=abc" {
		t.Fatalf("server saw cookie %q", got.cookie)
	}
}

func TestUnixSocketUsesInheritedPath(t *testing.T) {
	socket := startUnixHTTPServer(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, "inherited")
	}))
	r := Request{Method: "GET", URL: "http://socket.test/"}
	in := Context{Folders: []Folder{{Settings: Settings{SocketPath: socket}}}}
	out := r.Execute(t.Context(), testProxy(t), in, nil, nil)
	if out.Err != nil {
		t.Fatalf("%s: %v", out.Stage, out.Err)
	}
	if out.Response == nil || out.Response.ResponseBody != "inherited" {
		t.Fatalf("response = %+v", out.Response)
	}
}

func TestUnixSocketRejectsProtocolClients(t *testing.T) {
	for _, tt := range []struct {
		name string
		r    Request
	}{
		{"grpc", Request{Protocol: "gRPC", URL: "https://example.test/service"}},
		{"websocket", Request{Method: "GET", URL: "ws://example.test/socket"}},
	} {
		t.Run(tt.name, func(t *testing.T) {
			tt.r.Settings.SocketPath = "/tmp/not-used.sock"
			out := tt.r.Execute(t.Context(), testProxy(t), Context{}, nil, nil)
			if out.Err == nil || !strings.Contains(out.Err.Error(), "Unix socket") {
				t.Fatalf("err = %v", out.Err)
			}
		})
	}
}

func TestUnixSocketMissingPathIsReported(t *testing.T) {
	dir, err := os.MkdirTemp("", "vscroxy-sock")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(dir)
	missing := filepath.Join(dir, "missing.sock")
	r := Request{Method: "GET", URL: "http://socket.test/", Settings: Settings{SocketPath: missing}}
	out := r.Execute(t.Context(), testProxy(t), Context{}, nil, nil)
	if out.Err == nil || !strings.Contains(out.Err.Error(), missing) {
		t.Fatalf("err = %v", out.Err)
	}
}
