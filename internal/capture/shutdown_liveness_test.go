package capture

import (
	"context"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	"github.com/lqqyt2423/go-mitmproxy/proxy"
)

// closeReturns proves shutdown does not wait on a stalled exchange forever.
// The timer is only the failure guard; the closed channel is the evidence.
func closeReturns(t *testing.T, s *Server) {
	t.Helper()
	waitClose(t, s.Close)
}

func proxyCloseReturns(t *testing.T, s *Server) {
	t.Helper()
	waitClose(t, s.proxy.Close)
}

func waitClose(t *testing.T, closer func() error) {
	t.Helper()
	closed := make(chan struct{})
	go func() {
		defer close(closed)
		_ = closer()
	}()
	select {
	case <-closed:
	case <-time.After(5 * time.Second):
		t.Fatal("Close did not return with a request in flight")
	}
}

func TestCloseReturnsWithPausedBreakpoint(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		io.WriteString(w, "ok")
	}))
	defer upstream.Close()
	s, _ := startServerWithRules(t, Rule{Type: RuleBreak, Match: upstream.URL})
	s.SetBreak(true)
	client := clientFor(t, s, nil)
	defer client.CloseIdleConnections()

	done := make(chan struct{})
	go func() {
		defer close(done)
		resp, err := client.Get(upstream.URL + "/paused")
		if err == nil {
			_, _ = io.Copy(io.Discard, resp.Body)
			_ = resp.Body.Close()
		}
	}()
	select {
	case bp := <-s.Breakpoints():
		if bp.URL == "" {
			t.Fatal("empty breakpoint")
		}
	case <-time.After(5 * time.Second):
		t.Fatal("no breakpoint before Close")
	}
	closeReturns(t, s)
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("the paused request did not end with the proxy")
	}
}

func TestCloseReturnsWithStalledUpstreamBody(t *testing.T) {
	headersSent := make(chan struct{})
	release := make(chan struct{})
	var releaseOnce sync.Once
	defer releaseOnce.Do(func() { close(release) })
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Length", "10")
		w.WriteHeader(http.StatusOK)
		w.(http.Flusher).Flush()
		close(headersSent)
		<-release
		io.WriteString(w, "abcdefghij")
	}))
	defer upstream.Close()
	s, _ := startServer(t)
	client := clientFor(t, s, nil)
	defer client.CloseIdleConnections()

	done := make(chan struct{})
	go func() {
		defer close(done)
		resp, err := client.Get(upstream.URL + "/stalled-body")
		if err == nil {
			_, _ = io.Copy(io.Discard, resp.Body)
			_ = resp.Body.Close()
		}
	}()
	select {
	case <-headersSent:
	case <-time.After(5 * time.Second):
		t.Fatal("the upstream did not send headers before Close")
	}
	closeReturns(t, s)
	releaseOnce.Do(func() { close(release) })
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("the stalled response did not end with the Proxy")
	}
}

type requestHeadersAddon struct {
	proxy.BaseAddon
	seen chan struct{}
	once sync.Once
}

func (a *requestHeadersAddon) Requestheaders(*proxy.Flow) {
	a.once.Do(func() { close(a.seen) })
}

func TestCloseReturnsWithStalledClientBody(t *testing.T) {
	s, err := New("127.0.0.1:0", t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	// Addons are read by the accept loop, so install this before Start.
	seen := &requestHeadersAddon{seen: make(chan struct{})}
	s.proxy.Addons = append(s.proxy.Addons, seen)
	if err := s.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = s.Close() })

	conn, err := net.Dial("tcp", s.Addr())
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	if _, err := fmt.Fprint(conn, "POST http://example.test/stalled HTTP/1.1\r\nHost: example.test\r\nContent-Length: 10\r\n\r\nabc"); err != nil {
		t.Fatal(err)
	}
	select {
	case <-seen.seen:
	case <-time.After(5 * time.Second):
		t.Fatal("the proxy did not read the client headers before Close")
	}
	closeReturns(t, s)
}

type waitingScriptRunner struct {
	started chan struct{}
	once    sync.Once
}

func (r *waitingScriptRunner) Hooks(string) (bool, bool, bool) { return true, false, false }

// Run holds the same context the app hands to its workspace-trust permission.
func (r *waitingScriptRunner) Run(ctx context.Context, _ string, _ *ScriptExchange) error {
	r.once.Do(func() { close(r.started) })
	<-ctx.Done()
	return ctx.Err()
}

func httpsCloseServer(t *testing.T, rules ...Rule) (*Server, string) {
	t.Helper()
	var s *Server
	var ca string
	if len(rules) == 0 {
		s, ca = startServer(t)
	} else {
		s, ca = startServerWithRules(t, rules...)
	}
	s.SetSSL(SSLConfig{All: true, AllowUntrusted: []string{"127.0.0.1"}})
	return s, ca
}

func TestHTTPSIdleKeepAliveCloseReturns(t *testing.T) {
	for _, direct := range []bool{false, true} {
		name := "capture"
		if direct {
			name = "proxy"
		}
		t.Run(name, func(t *testing.T) {
			upstream := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				io.WriteString(w, "ok")
			}))
			defer upstream.Close()
			s, ca := httpsCloseServer(t)
			client := clientFor(t, s, sslTestRoots(t, ca))
			defer client.CloseIdleConnections()

			done := make(chan struct{})
			go func() {
				defer close(done)
				resp, err := client.Get(upstream.URL + "/keep-alive")
				if err == nil {
					_, _ = io.Copy(io.Discard, resp.Body)
					_ = resp.Body.Close()
				}
			}()
			select {
			case <-done:
			case <-time.After(5 * time.Second):
				t.Fatal("HTTPS exchange did not finish before Close")
			}
			if direct {
				proxyCloseReturns(t, s)
			} else {
				closeReturns(t, s)
			}
		})
	}
}

func TestHTTPSPausedBreakpointCloseReturns(t *testing.T) {
	for _, direct := range []bool{false, true} {
		name := "capture"
		if direct {
			name = "proxy"
		}
		t.Run(name, func(t *testing.T) {
			upstream := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				io.WriteString(w, "ok")
			}))
			defer upstream.Close()
			s, ca := httpsCloseServer(t, Rule{Type: RuleBreak, Match: upstream.URL})
			s.SetBreak(true)
			client := clientFor(t, s, sslTestRoots(t, ca))
			defer client.CloseIdleConnections()

			done := make(chan struct{})
			go func() {
				defer close(done)
				resp, err := client.Get(upstream.URL + "/paused")
				if err == nil {
					_, _ = io.Copy(io.Discard, resp.Body)
					_ = resp.Body.Close()
				}
			}()
			select {
			case bp := <-s.Breakpoints():
				if bp.Phase != PhaseRequest {
					t.Fatalf("breakpoint phase = %v, want request", bp.Phase)
				}
			case <-time.After(5 * time.Second):
				t.Fatal("no HTTPS request breakpoint before Close")
			}
			if direct {
				proxyCloseReturns(t, s)
			} else {
				closeReturns(t, s)
			}
			select {
			case <-done:
			case <-time.After(5 * time.Second):
				t.Fatal("the paused HTTPS request did not end with the proxy")
			}
		})
	}
}

func TestHTTP2IdleKeepAliveCloseReturns(t *testing.T) {
	for _, direct := range []bool{false, true} {
		name := "capture"
		if direct {
			name = "proxy"
		}
		t.Run(name, func(t *testing.T) {
			upstream := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				io.WriteString(w, "ok")
			}))
			upstream.EnableHTTP2 = true
			upstream.StartTLS()
			defer upstream.Close()
			s, ca := httpsCloseServer(t)
			client := clientFor(t, s, sslTestRoots(t, ca))
			client.Transport.(*http.Transport).ForceAttemptHTTP2 = true
			defer client.CloseIdleConnections()

			done := make(chan struct{})
			proto := make(chan int, 1)
			go func() {
				defer close(done)
				resp, err := client.Get(upstream.URL + "/h2")
				if err == nil {
					proto <- resp.ProtoMajor
					_, _ = io.Copy(io.Discard, resp.Body)
					_ = resp.Body.Close()
				}
			}()
			select {
			case <-done:
			case <-time.After(5 * time.Second):
				t.Fatal("HTTP/2 exchange did not finish before Close")
			}
			select {
			case major := <-proto:
				if major != 2 {
					t.Skipf("the MITM negotiated HTTP/%d, not h2", major)
				}
			default:
				t.Fatal("the HTTP/2 send got no response")
			}
			if direct {
				proxyCloseReturns(t, s)
			} else {
				closeReturns(t, s)
			}
		})
	}
}

func TestCloseReturnsWithScriptWaitingForPermission(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		io.WriteString(w, "ok")
	}))
	defer upstream.Close()
	runner := &waitingScriptRunner{started: make(chan struct{})}
	s, _ := startServer(t)
	s.SetScriptRunner(runner)
	if err := s.SetRules([]Rule{{Type: RuleScript, Target: "waiting.js", Match: upstream.URL}}); err != nil {
		t.Fatal(err)
	}
	client := clientFor(t, s, nil)
	defer client.CloseIdleConnections()

	done := make(chan struct{})
	go func() {
		defer close(done)
		resp, err := client.Get(upstream.URL + "/script")
		if err == nil {
			_, _ = io.Copy(io.Discard, resp.Body)
			_ = resp.Body.Close()
		}
	}()
	select {
	case <-runner.started:
	case <-time.After(5 * time.Second):
		t.Fatal("the rule script did not start before Close")
	}
	closeReturns(t, s)
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("the scripted request did not end with the proxy")
	}
}
