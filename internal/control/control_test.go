package control

import (
	"context"
	"encoding/json/jsontext"
	"encoding/json/v2"
	"errors"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func startTestServer(t *testing.T, dispatch Dispatch) (*Server, *Client, string) {
	t.Helper()
	path := filepath.Join(t.TempDir(), "control.json")
	s, err := Start(path, dispatch)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := s.Close(); err != nil {
			t.Error(err)
		}
	})
	c, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(c.http.CloseIdleConnections)
	return s, c, path
}

func TestRoundTrip(t *testing.T) {
	_, c, path := startTestServer(t, func(ctx context.Context, command Command) (any, error) {
		if _, ok := ctx.Deadline(); !ok {
			return nil, errors.New("missing deadline")
		}
		return command, nil
	})
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if runtime.GOOS != "windows" && info.Mode().Perm() != 0o600 {
		t.Fatalf("mode = %v", info.Mode())
	}
	entries, err := os.ReadDir(filepath.Dir(path))
	if err != nil || len(entries) != 1 {
		t.Fatal("temporary discovery files were not cleaned up")
	}
	host, _, err := net.SplitHostPort(c.address)
	if err != nil || host != "127.0.0.1" {
		t.Fatal("server did not bind IPv4 loopback")
	}
	var result Command
	if err := c.Call(t.Context(), "app.inspect", map[string]any{"count": 3}, &result); err != nil {
		t.Fatal(err)
	}
	if result.Action != "app.inspect" || string(result.Params) != `{"count":3}` {
		t.Fatalf("unexpected round trip: %s %s", result.Action, result.Params)
	}
	if err := c.Call(t.Context(), "app.inspect", nil, &result); err != nil || string(result.Params) != "{}" {
		t.Fatalf("nil arguments: %v", err)
	}
	if err := c.Call(t.Context(), "app.inspect", nil, nil); err != nil {
		t.Fatal(err)
	}
	// Neither environment proxies nor automatic redirects are permitted.
	if c.http.Transport.(*http.Transport).Proxy != nil {
		t.Fatal("client uses a proxy")
	}
	var wg sync.WaitGroup
	for range 12 {
		wg.Go(func() {
			if err := c.Call(t.Context(), "app.inspect", nil, nil); err != nil {
				t.Error(err)
			}
		})
	}
	wg.Wait()
}

func TestRequestValidation(t *testing.T) {
	var calls atomic.Int32
	_, c, _ := startTestServer(t, func(context.Context, Command) (any, error) {
		calls.Add(1)
		return nil, nil
	})
	tests := []struct {
		name   string
		body   string
		mutate func(*http.Request)
		status int
	}{
		{name: "missing auth", mutate: func(r *http.Request) { r.Header.Del("Authorization") }, status: 401},
		{name: "wrong auth", mutate: func(r *http.Request) { r.Header.Set("Authorization", "Bearer wrong") }, status: 401},
		{name: "duplicate auth", mutate: func(r *http.Request) { r.Header.Add("Authorization", "Bearer wrong") }, status: 401},
		{name: "origin", mutate: func(r *http.Request) { r.Header.Set("Origin", "http://evil.example") }, status: 403},
		{name: "null origin", mutate: func(r *http.Request) { r.Header.Set("Origin", "null") }, status: 403},
		{name: "empty origin", mutate: func(r *http.Request) { r.Header["Origin"] = []string{""} }, status: 403},
		{name: "rebound host", mutate: func(r *http.Request) { r.Host = "evil.example" }, status: 403},
		{name: "localhost host", mutate: func(r *http.Request) { r.Host = "localhost:" + strings.Split(c.address, ":")[1] }, status: 403},
		{name: "wrong port", mutate: func(r *http.Request) { r.Host = "127.0.0.1:1" }, status: 403},
		{name: "wrong method", mutate: func(r *http.Request) { r.Method = http.MethodGet }, status: 405},
		{name: "wrong path", mutate: func(r *http.Request) { r.URL.Path = "/wrong" }, status: 404},
		{name: "query", mutate: func(r *http.Request) { r.URL.RawQuery = "extra=1" }, status: 404},
		{name: "unknown field", body: `{"action":"test","params":{},"extra":true}`, status: 400},
		{name: "trailing value", body: `{"action":"test","params":{}} {}`, status: 400},
		{name: "duplicate field", body: `{"action":"test","action":"other","params":{}}`, status: 400},
		{name: "malformed", body: `{"action":`, status: 400},
		{name: "empty action", body: `{"action":"","params":{}}`, status: 400},
		{name: "bad action", body: `{"action":"a b","params":{}}`, status: 400},
		{name: "long action", body: `{"action":"` + strings.Repeat("a", 129) + `","params":{}}`, status: 400},
		{name: "missing params", body: `{"action":"test"}`, status: 400},
		{name: "null params", body: `{"action":"test","params":null}`, status: 400},
		{name: "array params", body: `{"action":"test","params":[]}`, status: 400},
		{name: "string params", body: `{"action":"test","params":"a"}`, status: 400},
		{name: "null request", body: `null`, status: 400},
		{name: "oversize", body: `{"action":"test","params":{"x":"` + strings.Repeat("a", maxRequestBytes) + `"}}`, status: 413},
		{name: "large header", mutate: func(r *http.Request) { r.Header.Set("X-Large", strings.Repeat("x", 32<<10)) }, status: 431},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			body := tt.body
			if body == "" {
				body = `{"action":"test","params":{}}`
			}
			req, err := http.NewRequestWithContext(t.Context(), http.MethodPost, "http://"+c.address+"/v1/call", strings.NewReader(body))
			if err != nil {
				t.Fatal(err)
			}
			req.Header.Set("Authorization", "Bearer "+c.token)
			if tt.mutate != nil {
				tt.mutate(req)
			}
			response, err := c.http.Do(req)
			if err != nil {
				t.Fatal(err)
			}
			defer response.Body.Close()
			data, _ := io.ReadAll(response.Body)
			if response.StatusCode != tt.status {
				t.Fatalf("status = %d, want %d", response.StatusCode, tt.status)
			}
			if strings.Contains(string(data), c.token) {
				t.Fatal("token leaked in error response")
			}
			if response.Header.Get("Access-Control-Allow-Origin") != "" {
				t.Fatal("CORS was enabled")
			}
		})
	}
	if calls.Load() != 0 {
		t.Fatal("invalid request reached dispatch")
	}
}

func TestDiscoveryOwnership(t *testing.T) {
	noop := func(context.Context, Command) (any, error) { return nil, nil }
	t.Run("existing regular file", func(t *testing.T) {
		path := filepath.Join(t.TempDir(), "control.json")
		if err := os.WriteFile(path, []byte("keep me"), 0o600); err != nil {
			t.Fatal(err)
		}
		if s, err := Start(path, noop); err == nil {
			s.Close()
			t.Fatal("replaced existing file")
		}
		data, err := os.ReadFile(path)
		if err != nil || string(data) != "keep me" {
			t.Fatal("existing file changed")
		}
	})
	t.Run("dangling symlink", func(t *testing.T) {
		path := filepath.Join(t.TempDir(), "control.json")
		if err := os.Symlink("missing", path); err != nil {
			t.Skip("symlinks unavailable")
		}
		if s, err := Start(path, noop); err == nil {
			s.Close()
			t.Fatal("replaced symlink")
		}
		if target, err := os.Readlink(path); err != nil || target != "missing" {
			t.Fatal("symlink changed")
		}
	})
	t.Run("concurrent start", func(t *testing.T) {
		path := filepath.Join(t.TempDir(), "control.json")
		winners := make(chan *Server, 16)
		var wg sync.WaitGroup
		for range 16 {
			wg.Go(func() {
				if s, err := Start(path, noop); err == nil {
					winners <- s
				}
			})
		}
		wg.Wait()
		close(winners)
		if len(winners) != 1 {
			t.Errorf("got %d endpoint owners, want 1", len(winners))
		}
		for s := range winners {
			if _, err := Load(path); err != nil {
				t.Error("winner's discovery was not readable:", err)
			}
			s.Close()
		}
	})
	t.Run("replacement preserved at close", func(t *testing.T) {
		s, _, path := startTestServer(t, noop)
		if err := os.Remove(path); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte("new owner"), 0o600); err != nil {
			t.Fatal(err)
		}
		if err := s.Close(); err != nil {
			t.Fatal(err)
		}
		data, err := os.ReadFile(path)
		if err != nil || string(data) != "new owner" {
			t.Fatal("deleted replacement file")
		}
	})
	t.Run("symlink replacement preserved at close", func(t *testing.T) {
		s, _, path := startTestServer(t, noop)
		if err := os.Remove(path); err != nil {
			t.Fatal(err)
		}
		if err := os.Symlink("missing", path); err != nil {
			t.Skip("symlinks unavailable")
		}
		if err := s.Close(); err != nil {
			t.Fatal(err)
		}
		if _, err := os.Readlink(path); err != nil {
			t.Fatal("deleted replacement symlink")
		}
	})
}

func writeDiscovery(t *testing.T, d discovery) string {
	t.Helper()
	data, err := json.Marshal(d)
	if err != nil {
		t.Fatal(err)
	}
	f, err := createPrivateTemp(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	if _, err := f.Write(data); err != nil {
		t.Fatal(err)
	}
	return f.Name()
}

func TestLoadValidation(t *testing.T) {
	valid := discovery{Version: 1, Address: "127.0.0.1:12345", Token: strings.Repeat("ab", 32)}
	for _, address := range []string{"localhost:12345", "example.com:80", "192.0.2.1:80", "0.0.0.0:80", "127.0.0.1:0", "127.0.0.1", "127.0.0.1:80/path", "user@127.0.0.1:80", "[::1%lo]:80", "[2001:db8::1]:80"} {
		t.Run(address, func(t *testing.T) {
			d := valid
			d.Address = address
			if _, err := Load(writeDiscovery(t, d)); err == nil {
				t.Fatal("accepted invalid address")
			} else if strings.Contains(err.Error(), d.Token) {
				t.Fatal("token leaked")
			}
		})
	}
	t.Run("bad version", func(t *testing.T) {
		d := valid
		d.Version = 2
		if _, err := Load(writeDiscovery(t, d)); err == nil {
			t.Fatal("accepted unsupported version")
		}
	})
	t.Run("bad token", func(t *testing.T) {
		d := valid
		d.Token = "not hex"
		if _, err := Load(writeDiscovery(t, d)); err == nil || strings.Contains(err.Error(), d.Token) {
			t.Fatal("invalid token handling")
		}
	})
	t.Run("public permissions", func(t *testing.T) {
		if runtime.GOOS == "windows" {
			t.Skip("Unix permission check")
		}
		for _, mode := range []os.FileMode{0o640, 0o604, 0o644, 0o660} {
			path := writeDiscovery(t, valid)
			if err := os.Chmod(path, mode); err != nil {
				t.Fatal(err)
			}
			if _, err := Load(path); err == nil {
				t.Fatalf("accepted permissions %o", mode)
			}
		}
	})
	t.Run("symlink", func(t *testing.T) {
		path := writeDiscovery(t, valid)
		link := filepath.Join(t.TempDir(), "link")
		if err := os.Symlink(path, link); err != nil {
			t.Skip("symlinks unavailable")
		}
		if _, err := Load(link); err == nil {
			t.Fatal("accepted symlink")
		}
	})
	for _, data := range []string{"{}", "null", strings.Repeat("x", 4097), `{"version":1,"address":"127.0.0.1:80","token":"` + valid.Token + `","unknown":true}`, `{"version":1,"address":"127.0.0.1:80","token":"` + valid.Token + `"} {}`} {
		path := filepath.Join(t.TempDir(), "bad.json")
		if err := os.WriteFile(path, []byte(data), 0o600); err != nil {
			t.Fatal(err)
		}
		if _, err := Load(path); err == nil {
			t.Fatal("accepted malformed discovery")
		}
	}
}

func TestCancellationAndShutdown(t *testing.T) {
	for _, shutdown := range []bool{false, true} {
		t.Run(map[bool]string{false: "client cancellation", true: "server shutdown"}[shutdown], func(t *testing.T) {
			entered, canceled := make(chan struct{}), make(chan struct{})
			s, c, path := startTestServer(t, func(ctx context.Context, _ Command) (any, error) {
				close(entered)
				<-ctx.Done()
				close(canceled)
				return nil, ctx.Err()
			})
			ctx, cancel := context.WithCancel(t.Context())
			defer cancel()
			result := make(chan error, 1)
			go func() { result <- c.Call(ctx, "wait", nil, nil) }()
			select {
			case <-entered:
			case <-time.After(3 * time.Second):
				t.Fatal("dispatch not started")
			}
			if shutdown {
				if err := s.Close(); err != nil {
					t.Fatal(err)
				}
				if _, err := os.Lstat(path); !errors.Is(err, os.ErrNotExist) {
					t.Fatal("discovery file survived close")
				}
				if err := c.Call(t.Context(), "after.close", nil, nil); err == nil {
					t.Fatal("server still serving after close")
				}
			} else {
				cancel()
			}
			select {
			case err := <-result:
				if err == nil || (!shutdown && !errors.Is(err, context.Canceled)) {
					t.Fatalf("unexpected call result: %v", err)
				}
			case <-time.After(3 * time.Second):
				t.Fatal("call did not stop")
			}
			select {
			case <-canceled:
			case <-time.After(3 * time.Second):
				t.Fatal("dispatch context was not canceled")
			}
		})
	}
}

func TestDispatchTimeout(t *testing.T) {
	t.Parallel()
	_, c, _ := startTestServer(t, func(ctx context.Context, _ Command) (any, error) {
		<-ctx.Done()
		return nil, ctx.Err()
	})
	start := time.Now()
	if err := c.Call(t.Context(), "wait", nil, nil); err == nil {
		t.Fatal("dispatch was not timed out")
	}
	if elapsed := time.Since(start); elapsed < dispatchTimeout || elapsed > dispatchTimeout+3*time.Second {
		t.Fatalf("unexpected timeout duration: %s", elapsed)
	}
}

func TestBackendFailuresAreSanitized(t *testing.T) {
	for _, mode := range []string{"error", "panic", "encoding", "oversize"} {
		t.Run(mode, func(t *testing.T) {
			secret := make(chan string, 1)
			_, c, _ := startTestServer(t, func(context.Context, Command) (any, error) {
				token := <-secret
				switch mode {
				case "error":
					return nil, errors.New(token)
				case "panic":
					panic(token)
				case "encoding":
					return jsontext.Value(token), nil
				default:
					return strings.Repeat("x", maxResponseBytes), nil
				}
			})
			secret <- c.token
			if err := c.Call(t.Context(), "fail", nil, nil); err == nil || strings.Contains(err.Error(), c.token) {
				t.Fatal("backend failure did not produce a sanitized error")
			}
		})
	}
}

func TestClientUntrustedResponses(t *testing.T) {
	for _, mode := range []string{"redirect", "oversize", "malformed", "error", "decode"} {
		t.Run(mode, func(t *testing.T) {
			var redirected atomic.Int32
			target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				redirected.Add(1)
			}))
			defer target.Close()
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				token := strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer ")
				switch mode {
				case "redirect":
					http.Redirect(w, r, target.URL, http.StatusTemporaryRedirect)
				case "oversize":
					_, _ = io.WriteString(w, strings.Repeat("x", maxResponseBytes+1))
				case "error":
					w.WriteHeader(http.StatusBadRequest)
					_ = json.MarshalWrite(w, map[string]string{"error": token})
				case "decode":
					_ = json.MarshalWrite(w, token)
				default:
					_, _ = io.WriteString(w, token)
				}
			}))
			defer server.Close()
			d := discovery{Version: 1, Address: strings.TrimPrefix(server.URL, "http://"), Token: strings.Repeat("cd", 32)}
			c, err := Load(writeDiscovery(t, d))
			if err != nil {
				t.Fatal(err)
			}
			defer c.http.CloseIdleConnections()
			var result struct {
				OK bool `json:"ok"`
			}
			if err := c.Call(t.Context(), "test", nil, &result); err == nil || strings.Contains(err.Error(), d.Token) {
				t.Fatal("untrusted response did not produce sanitized error")
			}
			if redirected.Load() != 0 {
				t.Fatal("client followed redirect")
			}
		})
	}
}

func TestClientInvalidCalls(t *testing.T) {
	_, c, _ := startTestServer(t, func(context.Context, Command) (any, error) {
		t.Error("invalid call reached backend")
		return nil, nil
	})
	for _, params := range []any{[]int{1}, "string", 1, make(chan int), map[string]string{"x": strings.Repeat("x", maxRequestBytes)}} {
		if err := c.Call(t.Context(), "test", params, nil); err == nil {
			t.Fatal("accepted invalid parameters")
		}
	}
	if err := c.Call(t.Context(), "bad action", nil, nil); err == nil {
		t.Fatal("accepted invalid action")
	}
}

func TestRequestSizeBoundary(t *testing.T) {
	_, c, _ := startTestServer(t, func(context.Context, Command) (any, error) { return nil, nil })
	base := `{"action":"test","params":{"x":""}}`
	for _, size := range []int{maxRequestBytes, maxRequestBytes + 1} {
		body := `{"action":"test","params":{"x":"` + strings.Repeat("x", size-len(base)) + `"}}`
		req, err := http.NewRequestWithContext(t.Context(), http.MethodPost, "http://"+c.address+"/v1/call", strings.NewReader(body))
		if err != nil {
			t.Fatal(err)
		}
		req.Header.Set("Authorization", "Bearer "+c.token)
		// Exercise chunked requests as well as the Content-Length test above.
		req.ContentLength = -1
		response, err := c.http.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		_, _ = io.Copy(io.Discard, response.Body)
		response.Body.Close()
		want := http.StatusOK
		if size > maxRequestBytes {
			want = http.StatusRequestEntityTooLarge
		}
		if response.StatusCode != want {
			t.Fatalf("size %d: status %d, want %d", size, response.StatusCode, want)
		}
	}
}

func TestStartAndCloseLifecycle(t *testing.T) {
	path := filepath.Join(t.TempDir(), "control.json")
	if s, err := Start(path, nil); err == nil {
		s.Close()
		t.Fatal("accepted nil dispatch")
	}
	if s, err := Start(filepath.Join(t.TempDir(), "missing", "control.json"), func(context.Context, Command) (any, error) { return nil, nil }); err == nil {
		s.Close()
		t.Fatal("created missing discovery parent")
	}
	for range 12 {
		s, err := Start(path, func(context.Context, Command) (any, error) { return nil, nil })
		if err != nil {
			t.Fatal(err)
		}
		var wg sync.WaitGroup
		for range 4 {
			wg.Go(func() {
				if err := s.Close(); err != nil {
					t.Error(err)
				}
			})
		}
		wg.Wait()
		if _, err := os.Lstat(path); !errors.Is(err, os.ErrNotExist) {
			t.Fatal("immediate close did not remove discovery")
		}
	}
}
