package capture

import (
	"bufio"
	"bytes"
	"compress/gzip"
	"context"
	"crypto/tls"
	"crypto/x509"
	"encoding/base64"
	"encoding/pem"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/gorilla/websocket"
	"google.golang.org/protobuf/types/known/wrapperspb"

	"github.com/baba983/vscroxy/internal/certs"
	"github.com/baba983/vscroxy/internal/hosts"
	"github.com/baba983/vscroxy/internal/rules"
	upstreamcfg "github.com/baba983/vscroxy/internal/upstream"
)

// freeAddr reserves an address the proxy can listen on.
func freeAddr(t *testing.T) string {
	t.Helper()
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("reserve address: %v", err)
	}
	addr := l.Addr().String()
	if err := l.Close(); err != nil {
		t.Fatalf("release address: %v", err)
	}
	return addr
}

func startServer(t *testing.T) (*Server, string) {
	t.Helper()
	dir := t.TempDir()
	s, err := New("127.0.0.1:0", dir)
	if err != nil {
		t.Fatalf("new server: %v", err)
	}
	if err := s.Start(); err != nil {
		t.Fatalf("start server: %v", err)
	}
	t.Cleanup(func() { _ = s.Close() })
	return s, filepath.Join(dir, "mitmproxy-ca-cert.pem")
}

func clientFor(t *testing.T, s *Server, roots *x509.CertPool) *http.Client {
	t.Helper()
	u, err := url.Parse("http://" + s.Addr())
	if err != nil {
		t.Fatalf("parse proxy url: %v", err)
	}
	tr := &http.Transport{Proxy: http.ProxyURL(u)}
	if roots != nil {
		tr.TLSClientConfig = &tls.Config{RootCAs: roots}
	}
	return &http.Client{Transport: tr, Timeout: 5 * time.Second}
}

// trustUpstream trusts an httptest server's self-signed certificate through
// the same settings a user configures, so a replay's client leg verifies it.
func trustUpstream(t *testing.T, s *Server, upstream *httptest.Server) {
	t.Helper()
	path := filepath.Join(t.TempDir(), "upstream.pem")
	data := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: upstream.Certificate().Raw})
	if err := os.WriteFile(path, data, 0o600); err != nil {
		t.Fatal(err)
	}
	s.SetCertificates(certs.Settings{RootCAs: []string{path}})
}

func waitFrame(t *testing.T, s *Server) FrameEvent {
	t.Helper()
	select {
	case fe := <-s.Frames():
		return fe
	case <-time.After(5 * time.Second):
		t.Fatal("no frame captured")
		return FrameEvent{}
	}
}

func waitStreamEnd(t *testing.T, s *Server, id, messages int) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if end := s.StreamEnd(id); !end.At.IsZero() {
			if end.Messages != messages {
				t.Fatalf("ended with %d messages, want %d", end.Messages, messages)
			}
			return
		}
		time.Sleep(time.Millisecond)
	}
	t.Fatal("no stream completion recorded")
}

func waitFlow(t *testing.T, s *Server) Flow {
	t.Helper()
	select {
	case f := <-s.Flows():
		return f
	case <-time.After(5 * time.Second):
		t.Fatal("no flow captured")
		return Flow{}
	}
}

func TestStartRejectsAnOccupiedExplicitAddress(t *testing.T) {
	busy, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer busy.Close()
	s, err := New(busy.Addr().String(), t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	if err := s.Start(); err == nil {
		t.Fatal("Start reported success on another application's listener")
	}
	if s.Running() {
		t.Fatal("failed startup reported a running proxy")
	}
}

func TestCloseStopsStartedProxy(t *testing.T) {
	for _, inUse := range []bool{false, true} {
		t.Run(fmt.Sprintf("in-use=%v", inUse), func(t *testing.T) {
			upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				fmt.Fprint(w, "received")
			}))
			defer upstream.Close()
			for range 20 {
				s, err := New("127.0.0.1:0", t.TempDir())
				if err != nil {
					t.Fatal(err)
				}
				if err := s.Start(); err != nil {
					t.Fatal(err)
				}
				if inUse {
					client := clientFor(t, s, nil)
					resp, err := client.Get(upstream.URL)
					if err != nil {
						t.Fatal(err)
					}
					_, _ = io.Copy(io.Discard, resp.Body)
					_ = resp.Body.Close()
					client.CloseIdleConnections()
				}
				if err := s.Close(); err != nil {
					t.Fatal(err)
				}
				if s.Running() {
					t.Fatal("closed proxy is still running")
				}
				if _, ok := <-s.Flows(); ok {
					// Drain the one completed exchange, if present.
					if _, ok := <-s.Flows(); ok {
						t.Fatal("flow stream did not close")
					}
				}
				if err := s.Close(); err != nil {
					t.Fatal(err)
				}
				if err := s.Start(); !errors.Is(err, net.ErrClosed) {
					t.Fatalf("restart closed proxy: %v", err)
				}
			}
		})
	}
}

func TestDefaultProxyFallsBackToAnOwnedPort(t *testing.T) {
	// Keep the competing listener bound for the entire test. If a real app
	// already owns 7890, leave it alone and still exercise the fallback.
	busy, err := net.Listen("tcp", "127.0.0.1:7890")
	if err == nil {
		other := &http.Server{Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			fmt.Fprint(w, "existing application")
		})}
		go other.Serve(busy)
		t.Cleanup(func() { _ = other.Close() })
	} else {
		t.Logf("preferred port already unavailable: %v", err)
	}
	s, err := New("", t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = s.Close() })
	if s.Running() {
		t.Fatal("proxy reported running before Start")
	}
	if err := s.Start(); err != nil {
		t.Fatal(err)
	}
	addr := s.Addr()
	host, port, err := net.SplitHostPort(addr)
	if err != nil {
		t.Fatal(err)
	}
	n, err := strconv.Atoi(port)
	if err != nil || host != "127.0.0.1" || n < 1024 || n == 7890 || !s.Running() {
		t.Fatalf("fallback listener = %q, running = %v", addr, s.Running())
	}
	t.Logf("occupied 127.0.0.1:7890 → bound %s", addr)
	if err := s.Start(); err == nil || s.Addr() != addr {
		t.Fatal("a second Start must not acquire another port")
	}
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, "through our proxy")
	}))
	defer upstream.Close()
	resp, err := clientFor(t, s, nil).Get(upstream.URL + "/fallback")
	if err != nil {
		t.Fatal(err)
	}
	body, err := io.ReadAll(resp.Body)
	resp.Body.Close()
	if err != nil || string(body) != "through our proxy" {
		t.Fatalf("forwarded body = %q, error = %v", body, err)
	}
	f := waitFlow(t, s)
	if f.URL != upstream.URL+"/fallback" || f.ResponseBody != "through our proxy" {
		t.Fatalf("wrong captured exchange: %+v", f)
	}
	replayed, err := s.Replay(t.Context(), f)
	if err != nil || replayed.ResponseBody != "through our proxy" {
		t.Fatalf("replay on actual port: body = %q, error = %v", replayed.ResponseBody, err)
	}
	if busy != nil {
		client := &http.Client{Transport: &http.Transport{}, Timeout: 5 * time.Second}
		resp, err := client.Get("http://" + busy.Addr().String())
		if err != nil {
			t.Fatal(err)
		}
		body, err := io.ReadAll(resp.Body)
		resp.Body.Close()
		if err != nil || string(body) != "existing application" {
			t.Fatalf("competing application was affected: %q, %v", body, err)
		}
	}
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}
	if s.Running() {
		t.Fatal("closed proxy still reported running")
	}
	ln, err := net.Listen("tcp", addr)
	if err != nil {
		t.Fatalf("Close did not release %s: %v", addr, err)
	}
	defer ln.Close()
	if err := s.Start(); !errors.Is(err, net.ErrClosed) {
		t.Fatalf("Start after Close = %v", err)
	}
}

func TestCloseImmediatelyAfterStartReleasesThePort(t *testing.T) {
	s, err := New("127.0.0.1:0", t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = s.Close() })
	if err := s.Start(); err != nil {
		t.Fatal(err)
	}
	addr := s.Addr()
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}
	ln, err := net.Listen("tcp", addr)
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()
	if s.Running() {
		t.Fatal("closed proxy reported running")
	}
}

func TestReplayTrustsTheProxyCA(t *testing.T) {
	upstream := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, "replayed")
	}))
	defer upstream.Close()

	s, caPath := startServer(t)
	s.SetSSL(SSLConfig{All: true})
	pem, err := os.ReadFile(caPath)
	if err != nil {
		t.Fatalf("read proxy CA: %v", err)
	}
	roots := x509.NewCertPool()
	if !roots.AppendCertsFromPEM(pem) {
		t.Fatal("proxy CA is not PEM")
	}
	u, err := url.Parse(upstream.URL)
	if err != nil {
		t.Fatalf("parse upstream url: %v", err)
	}
	u.Host = "localhost:" + u.Port()
	resp, err := clientFor(t, s, roots).Get(u.String() + "/replay")
	if err != nil {
		t.Fatalf("request through proxy: %v", err)
	}
	_ = resp.Body.Close()
	f := waitFlow(t, s)

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if _, err := s.Replay(ctx, f); err != nil {
		t.Fatalf("replay of an HTTPS flow: %v", err)
	}
	if again := waitFlow(t, s); again.ResponseBody != "replayed" {
		t.Fatalf("replayed body = %q", again.ResponseBody)
	}
}

func TestIgnoredHostsAreNotShown(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, "noise")
	}))
	defer upstream.Close()

	s, _ := startServer(t)
	s.SetIgnore([]string{"127.0.0.1"})
	resp, err := clientFor(t, s, nil).Get(upstream.URL + "/noise")
	if err != nil {
		t.Fatalf("request through proxy: %v", err)
	}
	body, err := io.ReadAll(resp.Body)
	_ = resp.Body.Close()
	if err != nil || string(body) != "noise" {
		t.Fatalf("body = %q, %v; want the response untouched", body, err)
	}
	select {
	case f := <-s.Flows():
		t.Fatalf("ignored flow was published: %+v", f)
	case <-time.After(300 * time.Millisecond):
	}
}

func TestFailedRequestShowsAnError(t *testing.T) {
	s, _ := startServer(t)
	dead := freeAddr(t) // reserved and released, so nothing listens there
	resp, err := clientFor(t, s, nil).Get("http://" + dead + "/gone")
	if err == nil {
		_ = resp.Body.Close()
	}

	f := waitFlow(t, s)
	if f.Status != http.StatusBadGateway {
		t.Fatalf("status = %d, want 502", f.Status)
	}
	if !strings.Contains(f.ResponseBody, "vscroxy:") {
		t.Fatalf("body = %q, want the reason", f.ResponseBody)
	}
	if f.Conn.ErrorStage != "TCP" || f.Conn.Error == "" || !strings.Contains(f.Conn.Log, dead) {
		t.Fatalf("connection failure was not diagnosed: %+v", f.Conn)
	}
}

// A client can send several targets over one forward-proxy connection. Each
// target needs its own upstream socket: reusing the previous one would send
// this request to the previous server, or hide an unreachable target.
func TestForwardProxyFollowsTargetOnOneConnection(t *testing.T) {
	for _, mode := range []struct {
		name     string
		upstream bool
	}{
		{"direct", false},
		{"upstream proxy", true},
	} {
		t.Run(mode.name, func(t *testing.T) {
			upstream := func(name string) *httptest.Server {
				return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					fmt.Fprintf(w, "%s %s", name, r.Host)
				}))
			}
			first, second := upstream("first"), upstream("second")
			defer first.Close()
			defer second.Close()
			s, _ := startServer(t)
			var upstreamAddr string
			if mode.upstream {
				inner, _ := startServer(t)
				upstreamAddr = inner.Addr()
				if err := s.SetUpstreamConfig(upstreamcfg.Settings{Default: "http://" + upstreamAddr}, nil); err != nil {
					t.Fatal(err)
				}
			}
			u, err := url.Parse("http://" + s.Addr())
			if err != nil {
				t.Fatal(err)
			}
			// One connection to the proxy, so every request shares its ConnContext.
			transport := &http.Transport{Proxy: http.ProxyURL(u), MaxConnsPerHost: 1}
			defer transport.CloseIdleConnections()
			client := &http.Client{Transport: transport, Timeout: 5 * time.Second}
			for i, tc := range []struct{ target, name string }{
				{first.URL, "first"},
				{second.URL, "second"},
				{first.URL, "first"},
			} {
				resp, err := client.Get(tc.target)
				if err != nil {
					t.Fatalf("request %d to %s: %v", i+1, tc.target, err)
				}
				body, err := io.ReadAll(resp.Body)
				resp.Body.Close()
				host := strings.TrimPrefix(tc.target, "http://")
				if err != nil || resp.StatusCode != http.StatusOK || string(body) != tc.name+" "+host {
					t.Fatalf("request %d to %s: %d %q (%v), want 200 %q", i+1, tc.target, resp.StatusCode, body, err, tc.name+" "+host)
				}
				f := waitFlow(t, s)
				if f.Host != host || f.Conn.Server != host {
					t.Fatalf("request %d went to %q for %q, want %q", i+1, f.Conn.Server, f.Host, host)
				}
				if i > 0 && !f.Conn.Reused {
					t.Fatalf("request %d did not reuse the client's proxy connection: %+v", i+1, f.Conn)
				}
				if mode.upstream && !strings.Contains(f.Conn.Log, "via proxy "+upstreamAddr) {
					t.Fatalf("request %d did not go through the upstream proxy: %s", i+1, f.Conn.Log)
				}
			}
			// An unreachable new target must fail to connect, not answer from the
			// previous upstream's socket.
			closed := freeAddr(t)
			resp, err := client.Get("http://" + closed + "/failure")
			if err != nil {
				t.Fatal(err)
			}
			io.Copy(io.Discard, resp.Body)
			resp.Body.Close()
			f := waitFlow(t, s)
			if resp.StatusCode != http.StatusBadGateway || f.Conn.ErrorStage != "TCP" {
				t.Fatalf("unreachable target: status %d, connection %+v", resp.StatusCode, f.Conn)
			}
			// The failed dial must not poison the client's connection for the next
			// target either.
			resp, err = client.Get(second.URL)
			if err != nil {
				t.Fatal(err)
			}
			body, err := io.ReadAll(resp.Body)
			resp.Body.Close()
			if host := strings.TrimPrefix(second.URL, "http://"); err != nil || string(body) != "second "+host {
				t.Fatalf("after a failed dial: %q (%v), want %q", body, err, "second "+host)
			}
			f = waitFlow(t, s)
			if !f.Conn.Reused || f.Conn.Server != strings.TrimPrefix(second.URL, "http://") {
				t.Fatalf("after a failed dial: %+v", f.Conn)
			}
		})
	}
}

func TestConnectionLogDistinguishesTLSFailuresAndReuse(t *testing.T) {
	upstream := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, "secure result")
	}))
	defer upstream.Close()
	s, caPath := startServer(t)
	s.SetSSL(SSLConfig{All: true})
	// The client deliberately does NOT trust the proxy's CA.
	if resp, err := clientFor(t, s, nil).Get(upstream.URL); err == nil {
		resp.Body.Close()
		t.Fatal("untrusted intercepted certificate was accepted")
	}
	failed := waitFlow(t, s)
	if failed.Method != "CONNECT" || failed.Status != 200 || failed.Conn.ErrorStage != "TLS client" || failed.Conn.Error == "" {
		t.Fatalf("TLS rejection should remain distinct from CONNECT 200: %+v", failed)
	}
	if !strings.Contains(failed.Conn.Log, "No HTTP response from the target") {
		t.Fatalf("misleading connection log: %s", failed.Conn.Log)
	}
	pem, err := os.ReadFile(caPath)
	if err != nil {
		t.Fatal(err)
	}
	roots := x509.NewCertPool()
	if !roots.AppendCertsFromPEM(pem) {
		t.Fatal("invalid CA")
	}
	client := clientFor(t, s, roots)
	defer client.CloseIdleConnections()
	for i := range 2 {
		resp, err := client.Get(upstream.URL)
		if err != nil {
			t.Fatal(err)
		}
		body, err := io.ReadAll(resp.Body)
		resp.Body.Close()
		if err != nil || string(body) != "secure result" {
			t.Fatalf("body %q: %v", body, err)
		}
		f := waitFlow(t, s)
		if f.Conn.Error != "" || f.Conn.Reused != (i == 1) {
			t.Fatalf("connection: %+v", f.Conn)
		}
		for _, want := range []string{"[TCP]", "[TLS upstream]", "[TLS client]", "SAN:", "127.0.0.1", "ALPN:", "verification: skipped"} {
			if !strings.Contains(f.Conn.Log, want) {
				t.Fatalf("missing %q in %s", want, f.Conn.Log)
			}
		}
	}
	// A TLS URL aimed at a plaintext HTTP server fails on the OTHER TLS side.
	plain := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {}))
	defer plain.Close()
	if resp, err := client.Get(strings.Replace(plain.URL, "http:", "https:", 1)); err == nil {
		resp.Body.Close()
		t.Fatal("plaintext server accepted as TLS")
	}
	f := waitFlow(t, s)
	if f.Conn.ErrorStage != "TLS upstream" || f.Conn.Error == "" {
		t.Fatalf("upstream failure: %+v", f.Conn)
	}
}

func TestConnectionLogDNSFailure(t *testing.T) {
	previous := net.DefaultResolver
	net.DefaultResolver = &net.Resolver{PreferGo: true, Dial: func(ctx context.Context, network, address string) (net.Conn, error) {
		return nil, &net.DNSError{Name: "failure.test", Err: "fixture name not found", IsNotFound: true}
	}}
	t.Cleanup(func() { net.DefaultResolver = previous })
	s, _ := startServer(t)
	resp, err := clientFor(t, s, nil).Get("http://failure.test/diagnostic")
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	f := waitFlow(t, s)
	if f.Status != 502 || f.Conn.ErrorStage != "DNS" || !strings.Contains(f.Conn.Log, "failure.test") {
		t.Fatalf("DNS failure misclassified: %+v", f.Conn)
	}
}

func TestDNSMappingAppliesToEveryApp(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprintf(w, "host=%s", r.Host)
	}))
	defer upstream.Close()
	_, port, err := net.SplitHostPort(strings.TrimPrefix(upstream.URL, "http://"))
	if err != nil {
		t.Fatalf("parse upstream address: %v", err)
	}

	s, _ := startServer(t)
	setHosts(t, s, "127.0.0.1 every.test")
	target := "http://every.test:" + port + "/x"
	resp, err := clientFor(t, s, nil).Get(target)
	if err != nil {
		t.Fatalf("request through the mapping: %v", err)
	}
	body, err := io.ReadAll(resp.Body)
	_ = resp.Body.Close()
	if err != nil {
		t.Fatalf("read body: %v", err)
	}
	// The dial goes to the address the rule gives; the request keeps the name
	// in its URL and Host header, which is what makes a virtual host work.
	if want := "host=every.test:" + port; string(body) != want {
		t.Fatalf("body = %q, want %q", body, want)
	}

	var mapped Flow
	for mapped.URL == "" {
		f := waitFlow(t, s)
		if f.URL == target {
			mapped = f
		}
	}
	if want := "every.test → 127.0.0.1"; mapped.DNSMapped != want || mapped.HostProfile != "Test:1" {
		t.Fatalf("DNSMapped = %q from %q, want %q from Test:1", mapped.DNSMapped, mapped.HostProfile, want)
	}
	if mapped.Conn.Remote != "127.0.0.1" {
		t.Fatalf("Conn.Remote = %q, want the address dialled", mapped.Conn.Remote)
	}
	if mapped.Conn.Proto == "" {
		t.Fatal("Conn.Proto is empty, want the HTTP version the client spoke")
	}
	if want := "every.test:" + port; mapped.Conn.Server != want {
		t.Fatalf("Conn.Server = %q, want %q", mapped.Conn.Server, want)
	}
	if mapped.Conn.Client == "" || mapped.Conn.Proxy == "" {
		t.Fatalf("client %q, proxy %q; want both addresses", mapped.Conn.Client, mapped.Conn.Proxy)
	}
	if want := "127.0.0.1"; mapped.Conn.Resolved != want {
		t.Fatalf("Conn.Resolved = %q, want %q", mapped.Conn.Resolved, want)
	}
}

func TestDNSMappingsResolveTheNameElsewhere(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprintf(w, "host=%s", r.Host)
	}))
	defer upstream.Close()
	_, port, err := net.SplitHostPort(strings.TrimPrefix(upstream.URL, "http://"))
	if err != nil {
		t.Fatalf("parse upstream address: %v", err)
	}
	exe, err := os.Executable()
	if err != nil {
		t.Fatalf("executable path: %v", err)
	}
	app := filepath.Base(exe)

	s, _ := startServer(t)
	target := "http://mapped.test:" + port + "/x"
	// Nothing resolves this name before the mapping: the proxy answers the
	// gateway error it gives a request whose server it cannot reach.
	before, err := clientFor(t, s, nil).Get(target)
	if err != nil {
		t.Fatalf("request before the mapping: %v", err)
	}
	_ = before.Body.Close()
	if before.StatusCode != http.StatusBadGateway {
		t.Fatalf("status before the mapping = %d, want 502", before.StatusCode)
	}

	// A rule for another app does not apply to this one.
	setHosts(t, s, "127.0.0.1 mapped.test @other-app")
	other, err := clientFor(t, s, nil).Get(target)
	if err != nil {
		t.Fatalf("request for another app: %v", err)
	}
	_ = other.Body.Close()
	if other.StatusCode != http.StatusBadGateway {
		t.Fatalf("status for another app = %d, want 502", other.StatusCode)
	}

	// The rule for this app points the dial at the address, and the request
	// keeps the name it asked for.
	setHosts(t, s, "# Staging\nmapped.test = 127.0.0.1 @"+app)
	resp, err := clientFor(t, s, nil).Get(target)
	if err != nil {
		t.Fatalf("request through the mapping: %v", err)
	}
	body, err := io.ReadAll(resp.Body)
	_ = resp.Body.Close()
	if err != nil {
		t.Fatalf("read body: %v", err)
	}
	if want := "host=mapped.test:" + port; string(body) != want {
		t.Fatalf("body = %q, want %q", body, want)
	}

	var mapped Flow
	for mapped.URL == "" {
		f := waitFlow(t, s)
		// The requests the mapping did not apply to were published too, as
		// the gateway error the proxy gives an unreachable server.
		if f.URL == target && f.Status == http.StatusOK {
			mapped = f
		}
	}
	if want := "mapped.test → 127.0.0.1"; mapped.DNSMapped != want || mapped.HostProfile != "Test:2" {
		t.Fatalf("DNSMapped = %q from %q, want %q from Test:2", mapped.DNSMapped, mapped.HostProfile, want)
	}
	if hit, ok := s.LastDNSMatch(); !ok || hit.Name != "mapped.test" || hit.IP.String() != "127.0.0.1" || hit.Profile != "Test" {
		t.Fatalf("last match = %+v, %v", hit, ok)
	}
}

// setHosts maps hosts as one profile named Test that is on.
func setHosts(t *testing.T, s *Server, text string) {
	t.Helper()
	set := hosts.Set{Profiles: []hosts.Profile{{Name: "Test", On: true, Text: text}}}
	if err := s.SetHosts(set.Rules()); err != nil {
		t.Fatalf("set hosts: %v", err)
	}
}

func TestDropConnectionsMakesClientsConnectAgain(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, "ok")
	}))
	defer upstream.Close()
	s, _ := startServer(t)
	client := clientFor(t, s, nil)
	get := func() Flow {
		t.Helper()
		resp, err := client.Get(upstream.URL + "/x")
		if err != nil {
			t.Fatalf("request: %v", err)
		}
		_, _ = io.Copy(io.Discard, resp.Body)
		_ = resp.Body.Close()
		return waitFlow(t, s)
	}
	get()
	if f := get(); !f.Conn.Reused {
		t.Fatalf("second request = %+v, want the kept-alive connection", f.Conn)
	}
	if n := s.DropConnections(); n != 1 {
		t.Fatalf("dropped %d connections, want 1", n)
	}
	if f := get(); f.Conn.Reused {
		t.Fatalf("after dropping, Conn = %+v; want a new connection", f.Conn)
	}
}

func TestIgnoredAppsAreNotShown(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, "noise")
	}))
	defer upstream.Close()

	s, _ := startServer(t)
	exe, err := os.Executable()
	if err != nil {
		t.Fatalf("executable path: %v", err)
	}
	s.SetIgnoreApps([]string{filepath.Base(exe)})
	resp, err := clientFor(t, s, nil).Get(upstream.URL + "/noise")
	if err != nil {
		t.Fatalf("request through proxy: %v", err)
	}
	_ = resp.Body.Close()
	select {
	case f := <-s.Flows():
		t.Fatalf("ignored app's flow was published: %+v", f)
	case <-time.After(300 * time.Millisecond):
	}
}

func TestAllowListShowsOnlyAllowedHostsAndApps(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, "ok")
	}))
	defer upstream.Close()
	exe, err := os.Executable()
	if err != nil {
		t.Fatalf("executable path: %v", err)
	}

	s, _ := startServer(t)
	get := func(path string) {
		t.Helper()
		resp, err := clientFor(t, s, nil).Get(upstream.URL + path)
		if err != nil {
			t.Fatalf("request through proxy: %v", err)
		}
		body, err := io.ReadAll(resp.Body)
		_ = resp.Body.Close()
		if err != nil || string(body) != "ok" {
			t.Fatalf("body = %q, %v; want the response untouched", body, err)
		}
	}
	hidden := func(path string) {
		t.Helper()
		get(path)
		select {
		case f := <-s.Flows():
			t.Fatalf("flow outside the allow list was published: %+v", f)
		case <-time.After(300 * time.Millisecond):
		}
	}
	shown := func(path string) {
		t.Helper()
		get(path)
		if f := waitFlow(t, s); !strings.HasSuffix(f.URL, path) {
			t.Fatalf("published %q, want %q", f.URL, path)
		}
	}

	s.SetAllow(AllowList{On: true, Hosts: []string{"example.com"}, Apps: []string{"some-other-app"}})
	hidden("/unlisted")
	s.SetAllow(AllowList{On: true})
	hidden("/empty-list")
	s.SetAllow(AllowList{On: true, Apps: []string{strings.ToUpper(filepath.Base(exe))}})
	shown("/by-app")
	s.SetAllow(AllowList{On: true, Hosts: []string{"127.0.0.1"}})
	shown("/by-host")
	s.SetIgnore([]string{"127.0.0.1"})
	hidden("/ignored-wins")
	s.SetIgnore(nil)
	s.SetAllow(AllowList{Hosts: []string{"example.com"}})
	shown("/off")
}

func TestDiscardRemovesOnlyThisCapturesSpillFiles(t *testing.T) {
	s, _ := startServer(t)
	_, spilled, _, problem := s.keepBody(uuid.New(), "res", make([]byte, maxBody+1))
	if spilled == "" {
		t.Fatalf("no spill file: %s", problem)
	}
	outside := filepath.Join(t.TempDir(), "res-"+uuid.NewString()+".bin")
	if err := os.WriteFile(outside, []byte("keep"), 0o600); err != nil {
		t.Fatal(err)
	}
	s.Discard([]string{spilled, outside, ""})
	if _, err := os.Stat(spilled); !os.IsNotExist(err) {
		t.Fatalf("spill file survived Discard: %v", err)
	}
	if _, err := os.Stat(outside); err != nil {
		t.Fatalf("Discard removed a file outside the spill directory: %v", err)
	}
}

func TestTimingSplitsWaitAndDownload(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		time.Sleep(40 * time.Millisecond)
		_, _ = w.Write([]byte("ok"))
	}))
	defer upstream.Close()

	s, _ := startServer(t)
	resp, err := clientFor(t, s, nil).Get(upstream.URL + "/slow")
	if err != nil {
		t.Fatalf("request through proxy: %v", err)
	}
	_ = resp.Body.Close()

	f := waitFlow(t, s)
	if f.Wait < 20 {
		t.Fatalf("wait = %d ms, want at least 20", f.Wait)
	}
	if f.Wait+f.Download != f.Millis {
		t.Fatalf("wait %d + download %d != total %d", f.Wait, f.Download, f.Millis)
	}
	if f.Timing == nil {
		t.Fatal("missing request-local timing")
	}
	for _, name := range []string{"Proxy", "TCP", "Connection", "Send", "Wait", "Receive"} {
		if !slices.ContainsFunc(f.Timing.Phases, func(p TimingPhase) bool { return p.Name == name && !p.Incomplete && !p.Failed }) {
			t.Fatalf("missing completed %s: %+v", name, f.Timing)
		}
	}
	for _, p := range f.Timing.Phases {
		if p.StartNS < 0 || p.DurationNS < 0 || p.StartNS+p.DurationNS > f.Timing.TotalNS {
			t.Fatalf("phase outside timeline: %+v, total %v", p, f.Timing.TotalNS)
		}
	}
}

func TestTimingIsRequestLocalAcrossReuseMappingAndFailure(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { io.WriteString(w, "ok") }))
	defer upstream.Close()
	tlsUpstream := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { io.WriteString(w, "tls") }))
	defer tlsUpstream.Close()
	s, _ := startServer(t)
	client := clientFor(t, s, nil)
	t.Cleanup(client.CloseIdleConnections)
	get := func(target string) Flow {
		t.Helper()
		resp, err := client.Get(target)
		if err != nil {
			t.Fatal(err)
		}
		_, err = io.Copy(io.Discard, resp.Body)
		resp.Body.Close()
		if err != nil {
			t.Fatal(err)
		}
		f := waitFlow(t, s)
		if f.Timing == nil {
			t.Fatal("missing timing", f)
		}
		return f
	}
	first := get(strings.Replace(upstream.URL, "127.0.0.1", "localhost", 1))
	if !slices.ContainsFunc(first.Timing.Phases, func(p TimingPhase) bool { return p.Name == "DNS" }) {
		t.Fatalf("DNS missing: %+v", first.Timing)
	}
	second := get(strings.Replace(upstream.URL, "127.0.0.1", "localhost", 1))
	if !second.Timing.Reused || slices.ContainsFunc(second.Timing.Phases, func(p TimingPhase) bool { return p.Name == "TCP" || p.Name == "DNS" || p.Name == "TLS" }) {
		t.Fatalf("reused request inherited setup: %+v", second.Timing)
	}
	if err := s.SetRules([]Rule{{Type: RuleMapRemote, Match: "/mapped", Target: tlsUpstream.URL}}); err != nil {
		t.Fatal(err)
	}
	mapped := get(upstream.URL + "/mapped")
	if !slices.ContainsFunc(mapped.Timing.Phases, func(p TimingPhase) bool { return p.Name == "TLS" && !p.Failed && !p.Incomplete }) {
		t.Fatalf("separate TLS transport not traced: %+v", mapped.Timing)
	}
	if err := s.SetRules([]Rule{{Type: RuleBlock, Match: "/local", Block: BlockRefuse}}); err != nil {
		t.Fatal(err)
	}
	local := get(upstream.URL + "/local")
	if len(local.Timing.Phases) != 1 || local.Timing.Phases[0].Name != "Proxy" || local.Timing.Phases[0].DurationNS != local.Timing.TotalNS {
		t.Fatalf("local reply invented network phases: %+v", local.Timing)
	}
	if err := s.SetRules(nil); err != nil {
		t.Fatal(err)
	}
	// Independent failure control: do not reuse the previous target's socket.
	client.CloseIdleConnections()
	failed := get("http://" + freeAddr(t) + "/failure")
	if !slices.ContainsFunc(failed.Timing.Phases, func(p TimingPhase) bool { return p.Name == "TCP" && p.Failed }) ||
		slices.ContainsFunc(failed.Timing.Phases, func(p TimingPhase) bool { return p.Name == "Receive" }) {
		t.Fatalf("failed dial showed success: %+v", failed.Timing)
	}
}

func TestTunnelsUntilADomainIsDecrypted(t *testing.T) {
	const body = `{"source":"decrypted HTTPS","items":[3,7]}`
	upstream := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		io.WriteString(w, body)
	}))
	defer upstream.Close()

	s, caPath := startServer(t)
	pem, err := os.ReadFile(caPath)
	if err != nil {
		t.Fatalf("read proxy CA: %v", err)
	}
	roots := x509.NewCertPool()
	if !roots.AppendCertsFromPEM(pem) {
		t.Fatal("proxy CA is not PEM")
	}
	u, err := url.Parse(upstream.URL)
	if err != nil {
		t.Fatalf("parse upstream url: %v", err)
	}
	u.Host = "localhost:" + u.Port()

	// Nothing is decrypted yet: the client sees the upstream's certificate,
	// which it does not trust, and the tunnel shows as a CONNECT flow.
	if _, err := clientFor(t, s, roots).Get(u.String() + "/before"); err == nil {
		t.Fatal("request succeeded, want a TLS error before decryption")
	}
	tunnel := waitFlow(t, s)
	if tunnel.Method != "CONNECT" || tunnel.Kind != "connect" {
		t.Fatalf("tunnel = %+v, want a CONNECT flow", tunnel)
	}

	// Decrypted now: the proxy's CA is what the client sees.
	// Older UI versions stored the CONNECT authority, including its port.
	s.SetSSL(SSLConfig{Hosts: []string{u.Host}})
	resp, err := clientFor(t, s, roots).Get(u.String() + "/after")
	if err != nil {
		t.Fatalf("request after decryption: %v", err)
	}
	data, err := io.ReadAll(resp.Body)
	_ = resp.Body.Close()
	if err != nil || string(data) != body {
		t.Fatalf("HTTPS response = %q, %v", data, err)
	}
	f := waitFlow(t, s)
	if f.Status != 200 || f.Kind != "http" || f.Path != "/after" || f.ResponseBody != body {
		t.Fatalf("flow = %+v, want the decrypted path and complete JSON response", f)
	}
	// MITM setup predates the HTTP request. It belongs to connection history,
	// not this request's axis, even on the first exchange of a new connection.
	if f.Timing == nil || slices.ContainsFunc(f.Timing.Phases, func(p TimingPhase) bool { return p.Name == "DNS" || p.Name == "TCP" || p.Name == "TLS" }) {
		t.Fatalf("pre-request setup charged to HTTP: %+v", f.Timing)
	}
	// IP clients send no SNI. Trust must still verify the certificate against
	// the CONNECT target, without disabling TLS verification in the client.
	u, _ = url.Parse(upstream.URL)
	s.SetSSL(SSLConfig{Hosts: []string{u.Host}})
	resp, err = clientFor(t, s, roots).Get(u.String() + "/ip-without-sni")
	if err != nil {
		t.Fatalf("verified IP request without SNI: %v", err)
	}
	data, err = io.ReadAll(resp.Body)
	resp.Body.Close()
	if err != nil || string(data) != body {
		t.Fatalf("IP response = %q, %v", data, err)
	}
	if f := waitFlow(t, s); f.Path != "/ip-without-sni" || f.ResponseBody != body {
		t.Fatalf("IP capture = %+v", f)
	}
}

func TestSSLDomainRulesIgnorePorts(t *testing.T) {
	for _, tc := range []struct {
		rule, host string
		want       bool
	}{
		{"Example.COM.:8443", "api.example.com:9443", true},
		{"example.com:8443", "notexample.com:8443", false},
		{"127.0.0.1:18443", "127.0.0.1", true},
		{"[2001:db8::1]:8443", "[2001:db8::1]:9443", true},
		{"2001:db8::1", "[2001:db8::1]", true},
		{"[2001:db8::1]:8443", "2001:db8::2", false},
		{"[ab].example.com", "a.example.com:8443", true},
	} {
		if got := (SSLConfig{Hosts: []string{tc.rule}}).Decrypts(tc.host); got != tc.want {
			t.Errorf("rule %q for %q = %v, want %v", tc.rule, tc.host, got, tc.want)
		}
	}
}

func TestSpillsLargeBodiesToDisk(t *testing.T) {
	big := bytes.Repeat([]byte("x"), 200<<10)
	sizes := make(chan int, 4)
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, err := io.ReadAll(r.Body)
		if err != nil {
			t.Errorf("read upstream body: %v", err)
		}
		sizes <- len(body)
		_, _ = w.Write(big)
	}))
	defer upstream.Close()

	s, _ := startServer(t)
	resp, err := clientFor(t, s, nil).Post(upstream.URL+"/big", "text/plain", bytes.NewReader(big))
	if err != nil {
		t.Fatalf("request through proxy: %v", err)
	}
	_ = resp.Body.Close()
	if got := <-sizes; got != len(big) {
		t.Fatalf("upstream got %d bytes, want %d", got, len(big))
	}

	f := waitFlow(t, s)
	if f.RequestBodySize != len(big) || f.ResponseBodySize != len(big) {
		t.Fatalf("sizes = %d, %d; want %d", f.RequestBodySize, f.ResponseBodySize, len(big))
	}
	if len(f.RequestBody) != maxBody || len(f.ResponseBody) != maxBody {
		t.Fatalf("inline bodies = %d, %d; want the %d-byte head", len(f.RequestBody), len(f.ResponseBody), maxBody)
	}
	for _, path := range []string{f.RequestBodyFile, f.ResponseBodyFile} {
		if path == "" {
			t.Fatalf("body was not spilled: %+v", f)
		}
		data, err := os.ReadFile(path)
		if err != nil {
			t.Fatalf("read spilled body: %v", err)
		}
		if len(data) != len(big) {
			t.Fatalf("spilled body = %d bytes, want %d", len(data), len(big))
		}
	}

	// A replay must send the whole body, not the head the flow kept.
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if _, err := s.Replay(ctx, f); err != nil {
		t.Fatalf("replay: %v", err)
	}
	if got := <-sizes; got != len(big) {
		t.Fatalf("replayed body = %d bytes, want %d", got, len(big))
	}

	dir := filepath.Dir(f.ResponseBodyFile)
	if err := s.Close(); err != nil {
		t.Fatalf("close: %v", err)
	}
	if _, err := os.Stat(dir); !os.IsNotExist(err) {
		t.Fatalf("spill directory survived close: %v", err)
	}
}

func TestFlowNamesClientProcess(t *testing.T) {
	if runtime.GOOS != "linux" {
		t.Skip("process attribution reads /proc, which only exists on Linux")
	}
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {}))
	defer upstream.Close()

	s, _ := startServer(t)
	resp, err := clientFor(t, s, nil).Get(upstream.URL + "/who")
	if err != nil {
		t.Fatalf("request through proxy: %v", err)
	}
	_ = resp.Body.Close()

	f := waitFlow(t, s)
	exe, err := os.Executable()
	if err != nil {
		t.Fatalf("executable path: %v", err)
	}
	if want := filepath.Base(exe); f.Process != want {
		t.Fatalf("process = %q, want %q", f.Process, want)
	}
}

func TestCapturesHTTP(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("X-Upstream", "yes")
		fmt.Fprint(w, `{"ok":true}`)
	}))
	defer upstream.Close()

	s, _ := startServer(t)
	resp, err := clientFor(t, s, nil).Get(upstream.URL + "/hello?x=1")
	if err != nil {
		t.Fatalf("request through proxy: %v", err)
	}
	body, err := io.ReadAll(resp.Body)
	_ = resp.Body.Close()
	if err != nil {
		t.Fatalf("read body: %v", err)
	}
	if string(body) != `{"ok":true}` {
		t.Fatalf("body = %q", body)
	}

	f := waitFlow(t, s)
	if f.Method != "GET" || f.Status != 200 || f.Path != "/hello?x=1" {
		t.Fatalf("flow = %+v", f)
	}
	if !strings.Contains(f.ResponseHeaders, "X-Upstream: yes") {
		t.Fatalf("response headers missing upstream header: %q", f.ResponseHeaders)
	}
	if !strings.Contains(f.ResponseBody, `"ok":true`) {
		t.Fatalf("response body = %q", f.ResponseBody)
	}
	if f.Millis < 0 {
		t.Fatalf("negative duration: %d", f.Millis)
	}
}

func TestCapturesHTTPS(t *testing.T) {
	upstream := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, `{"secure":true}`)
	}))
	defer upstream.Close()

	s, caPath := startServer(t)
	s.SetSSL(SSLConfig{All: true})
	pem, err := os.ReadFile(caPath)
	if err != nil {
		t.Fatalf("read proxy CA: %v", err)
	}
	roots := x509.NewCertPool()
	if !roots.AppendCertsFromPEM(pem) {
		t.Fatal("proxy CA is not PEM")
	}

	u, err := url.Parse(upstream.URL)
	if err != nil {
		t.Fatalf("parse upstream url: %v", err)
	}
	u.Host = "localhost:" + u.Port()
	resp, err := clientFor(t, s, roots).Get(u.String() + "/secure")
	if err != nil {
		t.Fatalf("https through proxy: %v", err)
	}
	body, err := io.ReadAll(resp.Body)
	_ = resp.Body.Close()
	if err != nil {
		t.Fatalf("read body: %v", err)
	}
	if string(body) != `{"secure":true}` {
		t.Fatalf("body = %q", body)
	}

	f := waitFlow(t, s)
	if f.Status != 200 || !strings.HasPrefix(f.Host, "localhost:") {
		t.Fatalf("flow = %+v", f)
	}
	if !strings.Contains(f.ResponseBody, `"secure":true`) {
		t.Fatalf("decrypted body = %q", f.ResponseBody)
	}
}

func startServerWithRules(t *testing.T, rules ...Rule) (*Server, string) {
	t.Helper()
	dir := t.TempDir()
	s, err := New("127.0.0.1:0", dir)
	if err != nil {
		t.Fatalf("new server: %v", err)
	}
	s.SetRules(rules)
	if err := s.Start(); err != nil {
		t.Fatalf("start server: %v", err)
	}
	t.Cleanup(func() { _ = s.Close() })
	return s, filepath.Join(dir, "mitmproxy-ca-cert.pem")
}

func TestMapRemote(t *testing.T) {
	first := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, "first")
	}))
	defer first.Close()
	second := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, "second")
	}))
	defer second.Close()

	s, _ := startServerWithRules(t, Rule{Type: RuleMapRemote, Match: first.URL, Target: second.URL})
	resp, err := clientFor(t, s, nil).Get(first.URL + "/path?q=1")
	if err != nil {
		t.Fatalf("request through proxy: %v", err)
	}
	body, _ := io.ReadAll(resp.Body)
	_ = resp.Body.Close()
	if string(body) != "second" {
		t.Fatalf("body = %q, want the mapped upstream", body)
	}
	f := waitFlow(t, s)
	if !strings.HasPrefix(f.URL, first.URL) {
		t.Fatalf("flow URL = %q, want the URL the client asked for", f.URL)
	}
}

func TestMapLocal(t *testing.T) {
	called := 0
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		called++
		fmt.Fprint(w, "upstream")
	}))
	defer upstream.Close()

	file := filepath.Join(t.TempDir(), "mock.json")
	if err := os.WriteFile(file, []byte(`{"mocked":true}`), 0o600); err != nil {
		t.Fatalf("write mock: %v", err)
	}
	s, _ := startServerWithRules(t, Rule{Type: RuleMapLocal, Match: "/mocked", Target: file})
	resp, err := clientFor(t, s, nil).Get(upstream.URL + "/mocked")
	if err != nil {
		t.Fatalf("request through proxy: %v", err)
	}
	body, _ := io.ReadAll(resp.Body)
	_ = resp.Body.Close()
	if string(body) != `{"mocked":true}` {
		t.Fatalf("body = %q", body)
	}
	if called != 0 {
		t.Fatalf("upstream was called %d times, want 0", called)
	}
	f := waitFlow(t, s)
	if f.Status != 200 {
		t.Fatalf("flow status = %d", f.Status)
	}
}

func TestBlock(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, "upstream")
	}))
	defer upstream.Close()

	s, _ := startServerWithRules(t, Rule{Type: RuleBlock, Match: "/blocked"})
	resp, err := clientFor(t, s, nil).Get(upstream.URL + "/blocked")
	if err != nil {
		t.Fatalf("request through proxy: %v", err)
	}
	_ = resp.Body.Close()
	if resp.StatusCode != http.StatusForbidden {
		t.Fatalf("status = %d, want 403", resp.StatusCode)
	}
}

func TestBlockStillAppliesToGRPC(t *testing.T) {
	s, conn := startGRPCThroughProxy(t)
	s.SetRules([]Rule{{Type: RuleBlock, Match: grpcMethod("Echo")}})
	ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
	defer cancel()
	if err := conn.Invoke(ctx, grpcMethod("Echo"), wrapperspb.String("blocked"), new(wrapperspb.StringValue)); err == nil {
		t.Fatal("a blocked gRPC call reached the upstream")
	}
	f := waitFlowFor(t, s, "/Echo")
	if f.Status != http.StatusForbidden || f.ResponseBody != "blocked by vscroxy\n" || f.ResponseHeader.Get("X-Server-Header") != "" {
		t.Fatalf("gRPC block response: status %d, body %q, headers %v", f.Status, f.ResponseBody, f.ResponseHeader)
	}
}

func TestReplay(t *testing.T) {
	calls := 0
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		fmt.Fprintf(w, `{"call":%d}`, calls)
	}))
	defer upstream.Close()

	s, _ := startServer(t)
	resp, err := clientFor(t, s, nil).Get(upstream.URL + "/replay")
	if err != nil {
		t.Fatalf("request through proxy: %v", err)
	}
	_ = resp.Body.Close()
	first := waitFlow(t, s)

	reply, err := s.Replay(t.Context(), first)
	if err != nil {
		t.Fatalf("replay: %v", err)
	}
	if reply.Status != 200 || reply.ResponseBody != `{"call":2}` || reply.Bytes != 10 {
		t.Fatalf("send returned the wrong response: %+v", reply)
	}
	second := waitFlow(t, s)
	if second.URL != first.URL || second.Method != first.Method || second.ReplayOf != first.ID || first.ReplayOf != 0 {
		t.Fatalf("replayed %s %s, want %s %s", second.Method, second.URL, first.Method, first.URL)
	}
	if calls != 2 {
		t.Fatalf("upstream calls = %d, want 2", calls)
	}
}

func TestConcurrentReplayLineageSurvivesHTTPSRedirects(t *testing.T) {
	upstream := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.Contains(fmt.Sprint(r.Header), "local-run-") {
			t.Error("local correlation leaked into upstream headers")
		}
		if r.URL.Path == "/start" {
			http.Redirect(w, r, "/finish", http.StatusFound)
			return
		}
		fmt.Fprint(w, r.Header.Get("X-Test-Case"))
	}))
	defer upstream.Close()
	s, _ := startServer(t)
	s.SetSSL(SSLConfig{All: true})
	var wg sync.WaitGroup
	for _, source := range []int{17, 41, 0} {
		wg.Go(func() {
			r, err := s.Replay(t.Context(), Flow{ID: source, DebugRun: fmt.Sprintf("local-run-%d", source), Method: "GET", URL: upstream.URL + "/start", RequestHeader: http.Header{"X-Test-Case": {strconv.Itoa(source)}}})
			if err != nil || r.ResponseBody != strconv.Itoa(source) {
				t.Errorf("source %d got %+v: %v", source, r, err)
			}
		})
	}
	wg.Go(func() {
		client := clientFor(t, s, s.caPool)
		defer client.CloseIdleConnections()
		r, err := client.Get(upstream.URL + "/finish")
		if err != nil {
			t.Error(err)
			return
		}
		defer r.Body.Close()
		io.Copy(io.Discard, r.Body)
	})
	wg.Wait()
	seen := map[int]int{}
	for range 7 {
		f := waitFlow(t, s)
		want, _ := strconv.Atoi(f.RequestHeader.Get("X-Test-Case"))
		if f.ReplayOf != want {
			t.Fatalf("concurrent replay/redirect mixed its source: %+v", f)
		}
		wantRun := ""
		if f.RequestHeader.Get("X-Test-Case") != "" {
			wantRun = fmt.Sprintf("local-run-%d", want)
		}
		if f.DebugRun != wantRun {
			t.Fatalf("wire exchange from %d has run %q, want %q", want, f.DebugRun, wantRun)
		}
		seen[f.ReplayOf]++
	}
	if seen[17] != 2 || seen[41] != 2 || seen[0] != 3 {
		t.Fatalf("redirect lineage = %v", seen)
	}
}

func TestBreakpointEditsRequest(t *testing.T) {
	first := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, "first")
	}))
	defer first.Close()
	second := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		fmt.Fprintf(w, "second|%s|%s|%s|%s|%s|%s", r.Method, r.Host, r.Header.Get("Authorization"), r.Header.Get("Cookie"), strings.Join(r.Header.Values("X-Multi"), ","), body)
	}))
	defer second.Close()

	s, _ := startServerWithRules(t, Rule{Type: RuleBreak, Match: first.URL})
	s.SetBreak(true)
	client := clientFor(t, s, nil)

	type result struct {
		body string
		err  error
	}
	done := make(chan result, 1)
	go func() {
		resp, err := client.Get(first.URL + "/bp")
		if err != nil {
			done <- result{err: err}
			return
		}
		body, err := io.ReadAll(resp.Body)
		_ = resp.Body.Close()
		done <- result{body: string(body), err: err}
	}()

	var bp Breakpoint
	select {
	case bp = <-s.Breakpoints():
	case <-time.After(5 * time.Second):
		t.Fatal("no breakpoint")
	}
	if bp.Method != "GET" || !strings.HasPrefix(bp.URL, first.URL) {
		t.Fatalf("breakpoint = %+v", bp)
	}
	if !s.Resume(bp.ID, Decision{Method: "POST", URL: second.URL + "/bp", Body: "edited payload", Header: http.Header{"Host": {"virtual.test"}, "Authorization": {"Bearer changed"}, "Cookie": {"session=new"}, "X-Multi": {"one", "two"}}}) {
		t.Fatal("resume found no waiter")
	}
	r := <-done
	if r.err != nil {
		t.Fatalf("request: %v", r.err)
	}
	if r.body != "second|POST|virtual.test|Bearer changed|session=new|one,two|edited payload" {
		t.Fatalf("body = %q, want the edited URL's upstream", r.body)
	}
	f := waitFlow(t, s)
	if f.Conn.Error != "" || !strings.Contains(f.Conn.Log, "separate upstream transport") || !strings.Contains(f.Conn.Log, strings.TrimPrefix(second.URL, "http://")) {
		t.Fatalf("rewritten URL's real transport missing: %+v", f.Conn)
	}
}

func TestBreakpointAborts(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, "upstream")
	}))
	defer upstream.Close()

	s, _ := startServerWithRules(t, Rule{Type: RuleBreak, Match: upstream.URL})
	s.SetBreak(true)
	client := clientFor(t, s, nil)

	status := make(chan int, 1)
	go func() {
		resp, err := client.Get(upstream.URL + "/abort")
		if err != nil {
			status <- 0
			return
		}
		_ = resp.Body.Close()
		status <- resp.StatusCode
	}()

	select {
	case bp := <-s.Breakpoints():
		s.Resume(bp.ID, Decision{Abort: true})
	case <-time.After(5 * time.Second):
		t.Fatal("no breakpoint")
	}
	if got := <-status; got != http.StatusBadGateway {
		t.Fatalf("status = %d, want 502", got)
	}
}

func TestBreakpointDurationExcludesPause(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, "ok")
	}))
	defer upstream.Close()

	s, _ := startServerWithRules(t, Rule{Type: RuleBreak, Match: upstream.URL})
	s.SetBreak(true)
	client := clientFor(t, s, nil)

	go func() {
		resp, err := client.Get(upstream.URL + "/slow-human")
		if err == nil {
			_ = resp.Body.Close()
		}
	}()
	select {
	case bp := <-s.Breakpoints():
		time.Sleep(500 * time.Millisecond)
		s.Resume(bp.ID, Decision{Method: "GET", URL: bp.URL})
	case <-time.After(5 * time.Second):
		t.Fatal("no breakpoint")
	}

	f := waitFlow(t, s)
	if f.Millis >= 400 {
		t.Fatalf("flow took %d ms: the pause is counted as network time", f.Millis)
	}
}

// fakeScripts runs Go functions as the scripts Script rules name.
type fakeScripts map[string][3]func(x *ScriptExchange) error

func (f fakeScripts) Hooks(script string) (bool, bool, bool) {
	return f[script][0] != nil, f[script][1] != nil, f[script][2] != nil
}

func (f fakeScripts) Run(_ context.Context, script string, x *ScriptExchange) error {
	hook := f[script][0]
	if x.Phase == "response" {
		hook = f[script][1]
	}
	if x.Phase == "message" {
		hook = f[script][2]
	}
	return hook(x)
}

func startServerWithScripts(t *testing.T, scripts fakeScripts, list ...Rule) *Server {
	t.Helper()
	s, _ := startServer(t)
	s.SetScriptRunner(scripts)
	if err := s.SetRules(list); err != nil {
		t.Fatal(err)
	}
	return s
}

// Every matching Script rule runs, in order, each seeing what the ones
// before it changed; pm.variables carry from request to response.
func TestScriptRulesRunInOrder(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("X-From-Script") != "yes" || r.URL.Path != "/new" || r.Header.Get("X-Skipped") != "" {
			t.Errorf("upstream got %s %v", r.URL.Path, r.Header)
		}
		w.WriteHeader(http.StatusInternalServerError)
		fmt.Fprint(w, "original")
	}))
	defer upstream.Close()
	var order []string
	s := startServerWithScripts(t, fakeScripts{
		"first.js": {func(x *ScriptExchange) error {
			order = append(order, "first")
			x.Header.Set("X-From-Script", "yes")
			x.Locals["seen"] = "1"
			return nil
		}, nil},
		"second.js": {func(x *ScriptExchange) error {
			order = append(order, "second:"+x.Header.Get("X-From-Script"))
			x.URL = strings.Replace(x.URL, "/old", "/new", 1)
			return nil
		}, func(x *ScriptExchange) error {
			if x.Locals["seen"] != "1" || string(x.ResponseBody) != "original" {
				t.Errorf("response hook: locals %v, body %q", x.Locals, x.ResponseBody)
			}
			x.Status, x.ResponseBody = 201, []byte(`{"scripted":true}`)
			x.Response["Set-Cookie"] = []string{"session=from-script; Path=/", "csrf=separate; Path=/"}
			return nil
		}},
		"off.js": {func(x *ScriptExchange) error { x.Header.Set("X-Skipped", "1"); return nil }, nil},
	},
		Rule{Type: RuleScript, Match: "/old", Target: "first.js"},
		Rule{Type: RuleScript, Match: "/elsewhere", Target: "off.js"},
		Rule{Type: RuleScript, Match: "/old", Target: "off.js", Scenario: "Off"},
		Rule{Type: RuleScript, Match: "/old", Target: "second.js"},
	)
	resp, err := clientFor(t, s, nil).Get(upstream.URL + "/old")
	if err != nil {
		t.Fatal(err)
	}
	body, _ := io.ReadAll(resp.Body)
	_ = resp.Body.Close()
	if resp.StatusCode != 201 || string(body) != `{"scripted":true}` {
		t.Fatalf("client got %d %q", resp.StatusCode, body)
	}
	if cookies := resp.Cookies(); len(cookies) != 2 || cookies[0].Value != "from-script" || cookies[1].Value != "separate" {
		t.Fatalf("cookies = %q", resp.Header.Values("Set-Cookie"))
	}
	if !slices.Equal(order, []string{"first", "second:yes"}) {
		t.Fatalf("order = %v", order)
	}
	f := waitFlow(t, s)
	if !strings.HasSuffix(f.URL, "/new") || f.Status != 201 || !strings.Contains(f.ResponseBody, "scripted") {
		t.Fatalf("flow = %s %d %q", f.URL, f.Status, f.ResponseBody)
	}
}

func TestEmptyHooksPreserveRepeatedHeaders(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if got := r.Header.Values("X-Repeat"); len(got) != 2 || got[0] != "first" || got[1] != "second" {
			t.Errorf("request headers = %q", got)
		}
		http.SetCookie(w, &http.Cookie{Name: "session", Value: "abc", Path: "/"})
		http.SetCookie(w, &http.Cookie{Name: "csrf", Value: "xyz", Path: "/"})
		fmt.Fprint(w, "ok")
	}))
	defer upstream.Close()
	nothing := func(*ScriptExchange) error { return nil }
	s := startServerWithScripts(t, fakeScripts{"empty.js": {nothing, nothing}}, Rule{Type: RuleScript, Match: "/", Target: "empty.js"})
	req, _ := http.NewRequestWithContext(t.Context(), "GET", upstream.URL, nil)
	req.Header["X-Repeat"] = []string{"first", "second"}
	resp, err := clientFor(t, s, nil).Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if cookies := resp.Cookies(); len(cookies) != 2 || cookies[0].Name != "session" || cookies[1].Name != "csrf" {
		t.Fatalf("cookies = %+v, headers = %q", cookies, resp.Header.Values("Set-Cookie"))
	}
}

// A failing script leaves the exchange as it was, and is counted.
func TestScriptErrorsAreCounted(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("X-Half") != "" {
			t.Errorf("a failed hook's edit reached the server")
		}
		fmt.Fprint(w, "ok")
	}))
	defer upstream.Close()
	s := startServerWithScripts(t, fakeScripts{"bad.js": {func(x *ScriptExchange) error {
		x.Header.Set("X-Half", "1")
		return errors.New("script timed out")
	}, nil}}, Rule{Type: RuleScript, Match: "/", Target: "bad.js"})
	resp, err := clientFor(t, s, nil).Get(upstream.URL + "/loop")
	if err != nil {
		t.Fatal(err)
	}
	_ = resp.Body.Close()
	if resp.StatusCode != 200 || s.ScriptErrors() == 0 {
		t.Fatalf("status %d, script errors %d", resp.StatusCode, s.ScriptErrors())
	}
}

func TestResponseBreakpointEdits(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Add("Set-Cookie", "old=1")
		w.WriteHeader(http.StatusInternalServerError)
		fmt.Fprint(w, "original")
	}))
	defer upstream.Close()

	s, _ := startServerWithRules(t, Rule{Type: RuleBreakResponse, Match: upstream.URL})
	s.SetBreak(true)
	client := clientFor(t, s, nil)

	type result struct {
		status int
		body   string
		err    error
		header http.Header
	}
	done := make(chan result, 1)
	go func() {
		resp, err := client.Get(upstream.URL + "/edit")
		if err != nil {
			done <- result{err: err}
			return
		}
		body, err := io.ReadAll(resp.Body)
		_ = resp.Body.Close()
		done <- result{status: resp.StatusCode, body: string(body), err: err, header: resp.Header}
	}()

	var bp Breakpoint
	select {
	case bp = <-s.Breakpoints():
	case <-time.After(5 * time.Second):
		t.Fatal("no response breakpoint")
	}
	if bp.Phase != PhaseResponse || bp.Status != http.StatusInternalServerError || bp.Body != "original" {
		t.Fatalf("breakpoint = %+v", bp)
	}
	if bp.Header.Get("Set-Cookie") != "old=1" {
		t.Fatalf("missing response headers: %+v", bp)
	}
	if !s.Resume(bp.ID, Decision{Status: 201, Body: `{"edited":true}`, Header: http.Header{"Set-Cookie": {"session=new; HttpOnly", "locale=zh"}, "Content-Type": {"application/json"}}}) {
		t.Fatal("resume found no waiter")
	}
	r := <-done
	if r.err != nil {
		t.Fatalf("request: %v", r.err)
	}
	if r.status != 201 || r.body != `{"edited":true}` {
		t.Fatalf("client got %d %q", r.status, r.body)
	}
	if got := strings.Join(r.header.Values("Set-Cookie"), "|"); got != "session=new; HttpOnly|locale=zh" {
		t.Fatalf("Set-Cookie = %q", got)
	}

	f := waitFlow(t, s)
	if f.Status != 201 || !strings.Contains(f.ResponseBody, "edited") {
		t.Fatalf("flow = %d %q", f.Status, f.ResponseBody)
	}
}

func TestResponseBreakpointAborts(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, "original")
	}))
	defer upstream.Close()

	s, _ := startServerWithRules(t, Rule{Type: RuleBreakResponse, Match: upstream.URL})
	s.SetBreak(true)
	client := clientFor(t, s, nil)

	status := make(chan int, 1)
	go func() {
		resp, err := client.Get(upstream.URL + "/abort")
		if err != nil {
			status <- 0
			return
		}
		_ = resp.Body.Close()
		status <- resp.StatusCode
	}()

	select {
	case bp := <-s.Breakpoints():
		s.Resume(bp.ID, Decision{Abort: true})
	case <-time.After(5 * time.Second):
		t.Fatal("no response breakpoint")
	}
	if got := <-status; got != http.StatusBadGateway {
		t.Fatalf("status = %d, want 502", got)
	}
}

func TestBreakpointOriginalPreservesWireMessage(t *testing.T) {
	var wire bytes.Buffer
	gz := gzip.NewWriter(&wire)
	gz.Write([]byte("payload\x00\xff"))
	if err := gz.Close(); err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		phase    RuleType
		original bool
	}{{RuleBreak, true}, {RuleBreakResponse, true}, {RuleBreak, false}, {RuleBreakResponse, false}} {
		t.Run(fmt.Sprintf("%s/original=%t", tc.phase, tc.original), func(t *testing.T) {
			upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				body, _ := io.ReadAll(r.Body)
				if !bytes.Equal(body, wire.Bytes()) || r.Header.Get("Content-Encoding") != "gzip" || r.Header.Get("Authorization") != "Bearer original" || r.Header.Get("X-Leak") != "" {
					t.Errorf("request was changed: %q, %v", body, r.Header)
				}
				if tc.phase == RuleBreak && !tc.original && r.Header.Get("X-Edited") != "yes" {
					t.Error("request header-only edit did not reach upstream")
				}
				w.Header().Set("Content-Encoding", "gzip")
				w.Header().Add("Set-Cookie", "a=1")
				w.Header().Add("Set-Cookie", "b=2")
				w.WriteHeader(201)
				w.Write(wire.Bytes())
			}))
			defer upstream.Close()
			s, _ := startServerWithRules(t, Rule{Type: tc.phase, Match: upstream.URL})
			s.SetBreak(true)
			client := clientFor(t, s, nil)
			client.Transport.(*http.Transport).DisableCompression = true
			done := make(chan error, 1)
			go func() {
				req, _ := http.NewRequestWithContext(t.Context(), "POST", upstream.URL, bytes.NewReader(wire.Bytes()))
				req.Header.Set("Content-Encoding", "gzip")
				req.Header.Set("Authorization", "Bearer original")
				resp, err := client.Do(req)
				if err != nil {
					done <- err
					return
				}
				defer resp.Body.Close()
				body, err := io.ReadAll(resp.Body)
				if err == nil && (resp.StatusCode != 201 || !bytes.Equal(body, wire.Bytes()) || resp.Header.Get("Content-Encoding") != "gzip" || len(resp.Header.Values("Set-Cookie")) != 2) {
					err = fmt.Errorf("response was changed: %d %q %v", resp.StatusCode, body, resp.Header)
				}
				if tc.phase == RuleBreakResponse && !tc.original && resp.Header.Get("X-Edited") != "yes" {
					err = fmt.Errorf("response header-only edit was lost")
				}
				done <- err
			}()
			select {
			case bp := <-s.Breakpoints():
				if !bp.BodyReadOnly {
					t.Fatal("binary body offered as editable text")
				}
				header := bp.Header.Clone()
				bp.Header.Set("X-Leak", "must not mutate the live exchange")
				d := Decision{Original: true, Method: "DELETE", URL: "http://wrong.invalid/", Status: 503, Header: http.Header{}, Body: "discard me"}
				if !tc.original {
					header.Set("X-Edited", "yes")
					d = Decision{Header: header, KeepBody: true}
				}
				s.Resume(bp.ID, d)
			case <-t.Context().Done():
				t.Fatal("no breakpoint")
			}
			if err := <-done; err != nil {
				t.Fatal(err)
			}
		})
	}
}

func TestBreakpointAbortStatuses(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { fmt.Fprint(w, "upstream") }))
	defer upstream.Close()
	for _, phase := range []RuleType{RuleBreak, RuleBreakResponse} {
		for _, status := range []int{429, 503} {
			t.Run(fmt.Sprintf("%s/%d", phase, status), func(t *testing.T) {
				s, _ := startServerWithRules(t, Rule{Type: phase, Match: upstream.URL})
				s.SetBreak(true)
				client := clientFor(t, s, nil)
				done := make(chan int, 1)
				go func() {
					resp, err := client.Get(upstream.URL)
					if err != nil {
						done <- 0
						return
					}
					resp.Body.Close()
					done <- resp.StatusCode
				}()
				select {
				case bp := <-s.Breakpoints():
					s.Resume(bp.ID, Decision{Abort: true, AbortStatus: status})
				case <-t.Context().Done():
					t.Fatal("no breakpoint")
				}
				if got := <-done; got != status {
					t.Fatalf("got %d, want %d", got, status)
				}
			})
		}
	}
}

func TestWebSocketFrames(t *testing.T) {
	upgrader := websocket.Upgrader{CheckOrigin: func(*http.Request) bool { return true }}
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		conn, err := upgrader.Upgrade(w, r, nil)
		if err != nil {
			return
		}
		defer conn.Close()
		for {
			kind, msg, err := conn.ReadMessage()
			if err != nil {
				return
			}
			_ = conn.WriteMessage(kind, append([]byte("echo:"), msg...))
		}
	}))
	defer upstream.Close()

	s, _ := startServer(t)
	s.SetSSL(SSLConfig{All: true})
	u, err := url.Parse(upstream.URL)
	if err != nil {
		t.Fatalf("parse upstream: %v", err)
	}
	proxyURL, err := url.Parse("http://" + s.Addr())
	if err != nil {
		t.Fatalf("parse proxy: %v", err)
	}
	dialer := websocket.Dialer{Proxy: http.ProxyURL(proxyURL)}
	conn, _, err := dialer.Dial("ws://"+u.Host+"/ws", nil)
	if err != nil {
		t.Fatalf("dial websocket: %v", err)
	}
	if err := conn.WriteMessage(websocket.TextMessage, []byte("hello")); err != nil {
		t.Fatalf("write: %v", err)
	}
	_, msg, err := conn.ReadMessage()
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	if string(msg) != "echo:hello" {
		t.Fatalf("message = %q", msg)
	}
	_ = conn.Close()

	f := waitFlow(t, s)
	if f.Kind != "ws" || f.Status != http.StatusSwitchingProtocols {
		t.Fatalf("flow = kind %q status %d", f.Kind, f.Status)
	}
	out := waitFrame(t, s)
	if out.FlowID != f.ID || out.Frame.Direction != "out" || out.Frame.Data != "hello" {
		t.Fatalf("first frame = %+v", out)
	}
	in := waitFrame(t, s)
	if in.Frame.Direction != "in" || !strings.Contains(in.Frame.Data, "echo:hello") {
		t.Fatalf("second frame = %+v", in)
	}
	waitStreamEnd(t, s, f.ID, 2)
}

func waitMessageBreakpoint(t *testing.T, s *Server) Breakpoint {
	t.Helper()
	select {
	case bp := <-s.Breakpoints():
		if bp.Phase != PhaseMessage {
			t.Fatalf("not a message pause: %+v", bp)
		}
		return bp
	case <-time.After(10 * time.Second):
		t.Fatal("message did not pause")
		return Breakpoint{}
	}
}

func TestWebSocketMessageBreakpoints(t *testing.T) {
	for _, secure := range []bool{false, true} {
		t.Run(fmt.Sprintf("TLS=%t", secure), func(t *testing.T) {
			u := websocket.Upgrader{}
			upstream := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				c, err := u.Upgrade(w, r, nil)
				if err != nil {
					return
				}
				defer c.Close()
				for {
					kind, data, err := c.ReadMessage()
					if err != nil {
						return
					}
					if c.WriteMessage(kind, data) != nil {
						return
					}
				}
			}))
			if secure {
				upstream.StartTLS()
			} else {
				upstream.Start()
			}
			defer upstream.Close()
			s, ca := startServer(t)
			s.SetSSL(SSLConfig{All: true})
			if err := s.SetRules([]Rule{{Type: RuleBreakMessage, Match: "/socket"}}); err != nil {
				t.Fatal(err)
			}
			s.SetBreak(true)
			var c *websocket.Conn
			if secure {
				u, _ := url.Parse(upstream.URL)
				c = dialWSS(t, s, ca, "localhost:"+u.Port(), "/socket")
			} else {
				c = dialWS(t, s, "ws"+strings.TrimPrefix(upstream.URL, "http")+"/socket")
			}
			c.SetReadDeadline(time.Now().Add(10 * time.Second))
			resume := func(direction, kind, body string, d Decision) {
				t.Helper()
				bp := waitMessageBreakpoint(t, s)
				if bp.Direction != direction || bp.MessageType != kind || bp.Body != body || bp.BodyReadOnly {
					t.Fatalf("wrong current message: %+v", bp)
				}
				if !s.Resume(bp.ID, d) {
					t.Fatal("pause expired")
				}
			}
			read := func(kind int, want []byte) {
				t.Helper()
				k, got, err := c.ReadMessage()
				if err != nil || k != kind || !bytes.Equal(got, want) {
					t.Fatalf("got %d %x %v, want %d %x", k, got, err, kind, want)
				}
			}
			if err := c.WriteMessage(websocket.TextMessage, []byte("original")); err != nil {
				t.Fatal(err)
			}
			resume("out", "text", "original", Decision{Body: "edited-out"})
			resume("in", "text", "edited-out", Decision{Body: "edited-in"})
			read(websocket.TextMessage, []byte("edited-in"))
			original, edited := []byte{255, 0, 115, 195, 40}, []byte{0, 254, 128, 1}
			if err := c.WriteMessage(websocket.BinaryMessage, original); err != nil {
				t.Fatal(err)
			}
			resume("out", "binary", base64.StdEncoding.EncodeToString(original), Decision{Body: base64.StdEncoding.EncodeToString(edited)})
			resume("in", "binary", base64.StdEncoding.EncodeToString(edited), Decision{Original: true, Body: "invalid ignored edit"})
			read(websocket.BinaryMessage, edited)
			c.WriteMessage(websocket.TextMessage, []byte("drop-only-this"))
			resume("out", "text", "drop-only-this", Decision{Abort: true})
			// A following round trip is a barrier proving the dropped message
			// was not forwarded, without timing an absence on the wire.
			s.SetBreak(false)
			c.WriteMessage(websocket.TextMessage, []byte("still-open"))
			read(websocket.TextMessage, []byte("still-open"))
			c.Close()
			f := waitFlow(t, s)
			for i, want := range []string{"edited-out", "edited-in", string(edited), string(edited), "drop-only-this", "still-open", "still-open"} {
				fr := waitFrame(t, s).Frame
				if fr.Seq != i+1 || fr.Data != want || (i == 4) != (fr.Kind == "dropped text") {
					t.Fatalf("frame %d: %+v", i, fr)
				}
			}
			waitStreamEnd(t, s, f.ID, 7)
		})
	}
}

func TestWebSocketDuplexPausesRelease(t *testing.T) {
	for _, finish := range []string{"client close", "server close", "proxy shutdown", "breakpoints off"} {
		t.Run(finish, func(t *testing.T) {
			stop := make(chan struct{})
			received := make(chan string, 1)
			u := websocket.Upgrader{}
			upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				c, err := u.Upgrade(w, r, nil)
				if err != nil {
					return
				}
				defer c.Close()
				go func() { <-stop; c.Close() }()
				c.WriteMessage(websocket.TextMessage, []byte("server-push"))
				_, data, err := c.ReadMessage()
				if err == nil {
					received <- string(data)
				}
				<-stop
			}))
			defer upstream.Close()
			defer close(stop)
			s, _ := startServer(t)
			s.SetSSL(SSLConfig{All: true})
			if err := s.SetRules([]Rule{{Type: RuleBreakMessage, Match: "/socket"}}); err != nil {
				t.Fatal(err)
			}
			s.SetBreak(true)
			c := dialWS(t, s, "ws"+strings.TrimPrefix(upstream.URL, "http")+"/socket")
			c.WriteMessage(websocket.TextMessage, []byte("client-send"))
			one, two := waitMessageBreakpoint(t, s), waitMessageBreakpoint(t, s)
			if one.Direction == two.Direction {
				t.Fatal("duplex messages confused")
			}
			switch finish {
			case "client close":
				c.Close()
			case "server close":
				stop <- struct{}{}
			case "proxy shutdown":
				if err := s.Close(); err != nil {
					t.Fatal(err)
				}
			case "breakpoints off":
				s.SetBreak(false)
				c.SetReadDeadline(time.Now().Add(10 * time.Second))
				_, data, err := c.ReadMessage()
				if err != nil || string(data) != "server-push" {
					t.Fatalf("original push: %q %v", data, err)
				}
				select {
				case data := <-received:
					if data != "client-send" {
						t.Fatal(data)
					}
				case <-time.After(10 * time.Second):
					t.Fatal("original send not released")
				}
			}
			for _, bp := range []Breakpoint{one, two} {
				select {
				case <-bp.Done:
				case <-time.After(10 * time.Second):
					t.Fatal("pause survived connection lifetime")
				}
				if s.Resume(bp.ID, Decision{Abort: true}) {
					t.Fatal("expired pause accepted decision")
				}
			}
		})
	}
}

func TestSSEEvents(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(http.StatusOK)
		fmt.Fprint(w, "id: 7\nevent: tick\ndata: one\n\n")
		w.(http.Flusher).Flush()
		fmt.Fprint(w, "data: two\n\n")
		w.(http.Flusher).Flush()
	}))
	defer upstream.Close()

	s, _ := startServer(t)
	resp, err := clientFor(t, s, nil).Get(upstream.URL + "/events")
	if err != nil {
		t.Fatalf("request: %v", err)
	}
	body, _ := io.ReadAll(resp.Body)
	_ = resp.Body.Close()
	if !strings.Contains(string(body), "one") || !strings.Contains(string(body), "two") {
		t.Fatalf("body = %q", body)
	}

	f := waitFlow(t, s)
	if f.Kind != "sse" {
		t.Fatalf("flow = kind %q", f.Kind)
	}
	first := waitFrame(t, s)
	if first.FlowID != f.ID || first.Frame.Data != "one" || first.Frame.Kind != "event" {
		t.Fatalf("first event = %+v, want an SSE event", first)
	}
	if first.Frame.Seq != 1 || first.Frame.Direction != "in" {
		t.Errorf("first event = %+v, want the first the server sent", first.Frame)
	}
	// The engine parses the event's own name and id, and the frame keeps them:
	// the kind says what the message is, not what the server called it.
	if first.Frame.EventName != "tick" || first.Frame.EventID != "7" {
		t.Errorf("first event = name %q id %q, want tick and 7", first.Frame.EventName, first.Frame.EventID)
	}
	if first.Frame.Size != 3 || first.Frame.Kept != 3 || first.Frame.File != "" {
		t.Errorf("first event = size %d kept %d file %q, want the three bytes inline", first.Frame.Size, first.Frame.Kept, first.Frame.File)
	}
	second := waitFrame(t, s)
	if second.Frame.Data != "two" {
		t.Fatalf("second event = %+v", second)
	}
	if second.Frame.EventName != "message" {
		t.Errorf("second event = name %q, want the engine's default name", second.Frame.EventName)
	}
	if second.Frame.Seq != 2 {
		t.Errorf("second event = seq %d, want 2", second.Frame.Seq)
	}
	waitStreamEnd(t, s, f.ID, 2)
}

func TestSSEEventFraming(t *testing.T) {
	for _, tc := range []struct {
		name string
		wire string
		data []string
	}{
		{name: "empty data", wire: "data:\n\n", data: []string{""}},
		{name: "colonless data", wire: "data\n\n", data: []string{""}},
		{name: "CRLF", wire: "data: one\r\n\r\ndata: two\r\n\r\n", data: []string{"one", "two"}},
		{name: "CR", wire: "data: one\r\rdata: two\r\r", data: []string{"one", "two"}},
		{name: "mixed endings", wire: "data: one\r\n\ndata: two\n\r", data: []string{"one", "two"}},
		{name: "whitespace and empty lines", wire: " data: ignored\ndata:  leading  \ndata:\ndata:\ttrailing \n\n", data: []string{" leading  \n\n\ttrailing "}},
		{name: "empty data lines", wire: "data:\ndata:\n\n", data: []string{"\n"}},
		{name: "no data", wire: ": heartbeat\n\nevent: tick\nid: 7\n\n\n"},
		{name: "EOF after line", wire: "data:partial\n"},
		{name: "EOF within line", wire: "data:partial"},
		{name: "complete then EOF", wire: "data: complete\n\ndata:partial\n", data: []string{"complete"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			finish := make(chan struct{})
			release := sync.OnceFunc(func() { close(finish) })
			upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Content-Type", "text/event-stream")
				io.WriteString(w, tc.wire)
				w.(http.Flusher).Flush()
				<-finish
			}))
			defer upstream.Close()
			defer release()
			s, _ := startServer(t)
			resp, err := clientFor(t, s, nil).Get(upstream.URL)
			if err != nil {
				t.Fatal(err)
			}
			defer resp.Body.Close()
			// Keep EOF separate from the bytes, as on a long-lived SSE stream.
			body := make([]byte, len(tc.wire))
			if _, err := io.ReadFull(resp.Body, body); err != nil {
				t.Fatal(err)
			}
			release()
			tail, err := io.ReadAll(resp.Body)
			resp.Body.Close()
			if err != nil || string(body) != tc.wire || len(tail) != 0 {
				t.Fatalf("forwarded body = %q + %q, error %v; want original %q", body, tail, err, tc.wire)
			}
			f := waitFlow(t, s)
			// Frames are emitted by Capture's SSEMessage hook. Completion gives
			// an exact count, including unwanted EOF events or omitted empty data.
			waitStreamEnd(t, s, f.ID, len(tc.data))
			for i, want := range tc.data {
				got := waitFrame(t, s)
				if got.FlowID != f.ID || got.Frame.Seq != i+1 || got.Frame.Data != want || got.Frame.Size != len(want) || got.Frame.EventName != "message" {
					t.Fatalf("event %d = %+v, want data %q (%d bytes)", i+1, got, want, len(want))
				}
			}
		})
	}
}

func TestSSEFramingAcrossReads(t *testing.T) {
	steps := []struct {
		wire  string
		event *Frame
	}{
		{wire: "event: tick\r"},
		{wire: "\n"},
		{wire: "id: 7\r"},
		{wire: "\n"},
		{wire: "data: first\r"},
		{wire: "\n"},
		{wire: "data:\r"},
		{wire: "\n"},
		{wire: "\r", event: &Frame{Data: "first\n", EventName: "tick", EventID: "7"}},
		{wire: "\n"},
		{wire: "data:\r"},
		{wire: "\r", event: &Frame{Data: "", EventName: "message"}},
		{wire: "data: unfinished\n"},
	}
	next := make(chan struct{})
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		for _, step := range steps {
			io.WriteString(w, step.wire)
			w.(http.Flusher).Flush()
			select {
			case <-next:
			case <-r.Context().Done():
				return
			}
		}
	}))
	defer upstream.Close()
	defer close(next)
	s, _ := startServer(t)
	resp, err := clientFor(t, s, nil).Get(upstream.URL)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	f := waitFlow(t, s)
	for i, step := range steps {
		body := make([]byte, len(step.wire))
		if _, err := io.ReadFull(resp.Body, body); err != nil || string(body) != step.wire {
			t.Fatalf("step %d: forwarded %q, error %v; want %q", i, body, err, step.wire)
		}
		// The reader calls SSEMessage before forwarding bytes. The upstream
		// waits here, forcing CR and LF into separate reads and proving that
		// a lone CR dispatches without waiting for the next byte or EOF.
		select {
		case got := <-s.Frames():
			want := step.event
			if want == nil || got.FlowID != f.ID || got.Frame.Data != want.Data || got.Frame.EventName != want.EventName || got.Frame.EventID != want.EventID {
				t.Fatalf("step %d: event = %+v, want %+v", i, got, want)
			}
		default:
			if step.event != nil {
				t.Fatalf("step %d: no event before the next write; want %+v", i, step.event)
			}
		}
		next <- struct{}{}
	}
	if tail, err := io.ReadAll(resp.Body); err != nil || len(tail) != 0 {
		t.Fatalf("tail = %q, error %v", tail, err)
	}
	waitStreamEnd(t, s, f.ID, 2)
}

func TestSSECompletionWithoutMessagesAndWithFullQueue(t *testing.T) {
	for _, count := range []int{0, queue + 3} {
		t.Run(strconv.Itoa(count), func(t *testing.T) {
			upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Content-Type", "text/event-stream")
				w.WriteHeader(200)
				w.(http.Flusher).Flush()
				for i := range count {
					fmt.Fprintf(w, "data: %d\n\n", i)
				}
			}))
			defer upstream.Close()
			s, _ := startServer(t)
			resp, err := clientFor(t, s, nil).Get(upstream.URL)
			if err != nil {
				t.Fatal(err)
			}
			_, err = io.Copy(io.Discard, resp.Body)
			resp.Body.Close()
			if err != nil {
				t.Fatal(err)
			}
			f := waitFlow(t, s)
			// No frame consumer: completion must survive a saturated queue,
			// and the final sequence must include the three dropped messages.
			waitStreamEnd(t, s, f.ID, count)
			if got := s.FrameDropped(); got != int64(max(count-queue, 0)) {
				t.Fatalf("dropped %d messages", got)
			}
		})
	}
}

// wsEchoServer answers every message with the same bytes, so a test can send
// one and see both frames the proxy captured.
func wsEchoServer(t *testing.T) (*httptest.Server, *string) {
	t.Helper()
	var seen string
	upgrader := websocket.Upgrader{CheckOrigin: func(*http.Request) bool { return true }}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		seen = r.URL.RequestURI()
		conn, err := upgrader.Upgrade(w, r, nil)
		if err != nil {
			return
		}
		defer conn.Close()
		for {
			kind, msg, err := conn.ReadMessage()
			if err != nil {
				return
			}
			if err := conn.WriteMessage(kind, msg); err != nil {
				return
			}
		}
	}))
	t.Cleanup(server.Close)
	return server, &seen
}

// dialWS opens a WebSocket through the proxy, as a client library does: a
// CONNECT tunnel the proxy decrypts when the host is decrypted, then the
// handshake inside it.
func dialWS(t *testing.T, s *Server, target string) *websocket.Conn {
	t.Helper()
	proxyURL, err := url.Parse("http://" + s.Addr())
	if err != nil {
		t.Fatalf("parse proxy: %v", err)
	}
	conn, _, err := (&websocket.Dialer{Proxy: http.ProxyURL(proxyURL)}).Dial(target, nil)
	if err != nil {
		t.Fatalf("dial %s: %v", target, err)
	}
	t.Cleanup(func() { _ = conn.Close() })
	return conn
}

// dialWSS opens a wss:// WebSocket through the proxy the way a browser does:
// CONNECT, TLS the proxy decrypts with its own certificate, then the handshake
// on that connection. Each step waits for the one before it, so no hello races
// the response to the CONNECT.
func dialWSS(t *testing.T, s *Server, caPath, target, path string) *websocket.Conn {
	t.Helper()
	host, port, ok := strings.Cut(target, ":")
	if !ok {
		t.Fatalf("target %q has no port", target)
	}
	raw, err := net.Dial("tcp", s.Addr())
	if err != nil {
		t.Fatalf("dial proxy: %v", err)
	}
	t.Cleanup(func() { _ = raw.Close() })
	fmt.Fprintf(raw, "CONNECT %s HTTP/1.1\r\nHost: %s\r\n\r\n", target, target)
	resp, err := http.ReadResponse(bufio.NewReader(raw), &http.Request{Method: http.MethodConnect})
	if err != nil {
		t.Fatalf("connect: %v", err)
	}
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("connect = %s", resp.Status)
	}
	pem, err := os.ReadFile(caPath)
	if err != nil {
		t.Fatalf("read proxy CA: %v", err)
	}
	roots := x509.NewCertPool()
	if !roots.AppendCertsFromPEM(pem) {
		t.Fatal("proxy CA is not PEM")
	}
	conn := tls.Client(raw, &tls.Config{RootCAs: roots, ServerName: host})
	if err := conn.HandshakeContext(t.Context()); err != nil {
		t.Fatalf("handshake: %v", err)
	}
	dialer := websocket.Dialer{
		// The TLS handshake is done: gorilla speaks the WebSocket handshake
		// on this connection as it is.
		NetDialTLSContext: func(context.Context, string, string) (net.Conn, error) { return conn, nil },
	}
	ws, _, err := dialer.Dial("wss://"+net.JoinHostPort(host, port)+path, nil)
	if err != nil {
		t.Fatalf("handshake: %v", err)
	}
	t.Cleanup(func() { _ = ws.Close() })
	return ws
}

// TestWebSocketKeepsLongMessagesWhole covers a message past the inline
// preview: the frame says how long it was, keeps its head in memory and the
// whole of it in the spill file, so the UI can show and save all of it.
func TestWebSocketKeepsLongMessagesWhole(t *testing.T) {
	upstream, _ := wsEchoServer(t)
	s, _ := startServer(t)
	s.SetSSL(SSLConfig{All: true})
	conn := dialWS(t, s, "ws"+strings.TrimPrefix(upstream.URL, "http")+"/stream")

	long := strings.Repeat("0123456789abcdef", 2560) // 40 KiB
	if err := conn.WriteMessage(websocket.TextMessage, []byte(long)); err != nil {
		t.Fatalf("write: %v", err)
	}
	if _, msg, err := conn.ReadMessage(); err != nil || string(msg) != long {
		t.Fatalf("echo = %d bytes, %v; want the whole message", len(msg), err)
	}
	flow := waitFlow(t, s)
	if flow.Kind != "ws" {
		t.Fatalf("flow = kind %q", flow.Kind)
	}
	sent := waitFrame(t, s)
	if sent.FlowID != flow.ID || sent.Frame.Direction != "out" || sent.Frame.Kind != "text" {
		t.Fatalf("frame = %+v", sent)
	}
	frame := sent.Frame
	if frame.Seq != 1 {
		t.Errorf("seq = %d, want the first message numbered 1", frame.Seq)
	}
	if frame.Size != len(long) || frame.Kept != len(long) {
		t.Errorf("size = %d, kept = %d, want the whole %d", frame.Size, frame.Kept, len(long))
	}
	if len(frame.Data) != maxFrameBytes || !strings.HasPrefix(long, frame.Data) {
		t.Errorf("data = %d bytes, want the head of the message inline", len(frame.Data))
	}
	if frame.File == "" {
		t.Fatal("the message was not spilled")
	}
	spilled, err := os.ReadFile(frame.File)
	if err != nil {
		t.Fatalf("read spill: %v", err)
	}
	if string(spilled) != long {
		t.Errorf("spill = %d bytes, want the whole %d", len(spilled), len(long))
	}
	echo := waitFrame(t, s)
	if echo.Frame.Seq != 2 || echo.Frame.Direction != "in" || echo.Frame.Data != long[:maxFrameBytes] {
		t.Errorf("echo = %+v, want the message back", echo.Frame)
	}
}

// The patched engine exposes the real upgrade request, including escaped path
// and query, rather than the CONNECT envelope.
func TestWebSocketKeepsThePathAndQuery(t *testing.T) {
	upstream, seen := wsEchoServer(t)
	s, _ := startServer(t)
	s.SetSSL(SSLConfig{All: true})
	target := "ws" + strings.TrimPrefix(upstream.URL, "http")
	conn := dialWS(t, s, target+"/deep/path%2Fseg?q=%2F")
	if err := conn.WriteMessage(websocket.TextMessage, []byte("hi")); err != nil {
		t.Fatalf("write: %v", err)
	}
	if _, _, err := conn.ReadMessage(); err != nil {
		t.Fatalf("read: %v", err)
	}
	flow := waitFlow(t, s)
	if want := target + "/deep/path%2Fseg?q=%2F"; flow.URL != want {
		t.Errorf("url = %q, want %q", flow.URL, want)
	}
	if flow.Scheme != "ws" || flow.Path != "/deep/path%2Fseg?q=%2F" {
		t.Errorf("flow = scheme %q path %q, want the actual upgrade URL", flow.Scheme, flow.Path)
	}
	if *seen != "/deep/path%2Fseg?q=%2F" {
		t.Errorf("upstream saw %q, want the path the client asked for", *seen)
	}
	if frame := waitFrame(t, s); frame.Frame.Data != "hi" {
		t.Errorf("frame = %+v, want the message", frame.Frame)
	}
}

// TestWSSKeepsThePathAndQuery covers a decrypted wss:// URL with a path and
// query that do not look like the host: the row shows the request the client
// made, escapes and all, and the frames of that connection are captured.
func TestWSSKeepsThePathAndQuery(t *testing.T) {
	upgrader := websocket.Upgrader{CheckOrigin: func(*http.Request) bool { return true }}
	var seen string
	upstream := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		seen = r.URL.RequestURI()
		conn, err := upgrader.Upgrade(w, r, nil)
		if err != nil {
			return
		}
		defer conn.Close()
		for {
			kind, msg, err := conn.ReadMessage()
			if err != nil {
				return
			}
			if err := conn.WriteMessage(kind, msg); err != nil {
				return
			}
		}
	}))
	defer upstream.Close()

	s, caPath := startServer(t)
	s.SetSSL(SSLConfig{All: true})
	u, err := url.Parse(upstream.URL)
	if err != nil {
		t.Fatalf("parse upstream: %v", err)
	}
	const path = "/deep/path%2Fseg?q=%2F&z=a+b"
	conn := dialWSS(t, s, caPath, "localhost:"+u.Port(), path)
	if err := conn.WriteMessage(websocket.TextMessage, []byte("hi")); err != nil {
		t.Fatalf("write: %v", err)
	}
	if _, _, err := conn.ReadMessage(); err != nil {
		t.Fatalf("read: %v", err)
	}
	if seen != path {
		t.Errorf("upstream saw %q, want %q", seen, path)
	}
	flow := waitFlow(t, s)
	if want := "wss://localhost:" + u.Port() + path; flow.URL != want {
		t.Errorf("url = %q, want %q", flow.URL, want)
	}
	if flow.Scheme != "wss" || flow.Path != path || flow.Kind != "ws" {
		t.Errorf("flow = scheme %q path %q kind %q", flow.Scheme, flow.Path, flow.Kind)
	}
	if frame := waitFrame(t, s); frame.Frame.Data != "hi" {
		t.Errorf("frame = %+v, want the message", frame.Frame)
	}
}

// TestFrameQueueLossIsVisible covers a stream the UI cannot keep up with: the
// frames a full queue could not take leave gaps in the sequence numbers the UI
// shows, and the server counts them, so a lost message never passes for one
// that never happened.
func TestFrameQueueLossIsVisible(t *testing.T) {
	upstream, _ := wsEchoServer(t)
	s, _ := startServer(t)
	s.SetSSL(SSLConfig{All: true})
	conn := dialWS(t, s, "ws"+strings.TrimPrefix(upstream.URL, "http")+"/burst")
	roundTrip := func(msg string) {
		t.Helper()
		if err := conn.WriteMessage(websocket.TextMessage, []byte(msg)); err != nil {
			t.Fatalf("write %q: %v", msg, err)
		}
		if _, _, err := conn.ReadMessage(); err != nil {
			t.Fatalf("read %q: %v", msg, err)
		}
	}
	roundTrip("first")
	waitFlow(t, s)

	// Nothing reads the frames while the burst runs, so the queue fills and
	// what follows is dropped rather than kept without limit.
	for i := range queue + 200 {
		roundTrip(fmt.Sprintf("message %d", i))
	}
	if dropped := s.FrameDropped(); dropped == 0 {
		t.Fatal("the queue dropped frames without counting them")
	}
	var seqs []int
	for {
		select {
		case ev := <-s.Frames():
			seqs = append(seqs, ev.Frame.Seq)
			continue
		case <-time.After(200 * time.Millisecond):
		}
		break
	}
	if len(seqs) == 0 {
		t.Fatal("no frame was captured")
	}
	if len(seqs) >= 2*(queue+201) {
		t.Fatalf("frames = %d, want the queue to have dropped some", len(seqs))
	}

	// A message after the burst is numbered past what was dropped, so the gap
	// says how many the UI never saw.
	roundTrip("after the burst")
	last := waitFrame(t, s)
	if last.Frame.Data != "after the burst" {
		t.Fatalf("frame = %q, want the message after the burst", last.Frame.Data)
	}
	if last.Frame.Seq <= seqs[len(seqs)-1]+1 {
		t.Errorf("seq = %d after %d, want the numbering to count the dropped ones", last.Frame.Seq, seqs[len(seqs)-1])
	}
	if gaps := last.Frame.Seq - seqs[len(seqs)-1] - 1; gaps < int(s.FrameDropped()) {
		t.Errorf("gap = %d, counted drops = %d, want the gap to show them", gaps, s.FrameDropped())
	}
}

func TestUpstreamProxy(t *testing.T) {
	upstream := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, "hello")
	}))
	defer upstream.Close()

	inner, _ := startServer(t)
	outer, caPath := startServer(t)
	outer.SetSSL(SSLConfig{All: true})
	if err := outer.SetUpstreamConfig(upstreamcfg.Settings{Default: "http://" + inner.Addr()}, nil); err != nil {
		t.Fatalf("set upstream: %v", err)
	}
	if got := outer.UpstreamConfig().Default; got != "http://"+inner.Addr() {
		t.Fatalf("upstream = %q", got)
	}

	pem, err := os.ReadFile(caPath)
	if err != nil {
		t.Fatalf("read CA: %v", err)
	}
	roots := x509.NewCertPool()
	if !roots.AppendCertsFromPEM(pem) {
		t.Fatal("CA is not PEM")
	}
	u, err := url.Parse(upstream.URL)
	if err != nil {
		t.Fatalf("parse upstream: %v", err)
	}
	u.Host = "localhost:" + u.Port()
	resp, err := clientFor(t, outer, roots).Get(u.String() + "/chain")
	if err != nil {
		t.Fatalf("request: %v", err)
	}
	body, _ := io.ReadAll(resp.Body)
	_ = resp.Body.Close()
	if string(body) != "hello" {
		t.Fatalf("body = %q", body)
	}
	// The inner proxy leaves HTTPS alone, so what it sees is the tunnel the
	// outer proxy opened through it.
	if f := waitFlow(t, inner); f.Kind != "connect" || f.Host == "" {
		t.Fatalf("inner flow = %+v, want the tunnel", f)
	}

	if err := outer.SetUpstreamConfig(upstreamcfg.Settings{Default: "not a proxy"}, nil); err == nil {
		t.Fatal("an address without a scheme was accepted")
	}
	if err := outer.SetUpstreamConfig(upstreamcfg.Settings{}, nil); err != nil || outer.UpstreamConfig().Default != "" {
		t.Fatalf("clearing upstream: %v, %q", err, outer.UpstreamConfig().Default)
	}
}

func TestSavedRulesAndCompleteCapturedMock(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(202)
		fmt.Fprint(w, "real upstream")
	}))
	defer upstream.Close()
	dir := t.TempDir()
	body := bytes.Repeat([]byte{'a', 0, 'z', 255}, 20003)
	spill := filepath.Join(dir, "capture.bin")
	if err := os.WriteFile(spill, body, 0o600); err != nil {
		t.Fatal(err)
	}
	rule, err := NewMockRule(filepath.Join(dir, "mocks"), Flow{
		Method: "GET", URL: upstream.URL + "/asset", Status: 201,
		ResponseBody: string(body[:64]), ResponseBodyFile: spill, ResponseBodySize: len(body),
		ResponseHeader: http.Header{"Content-Type": {"application/custom"}, "Content-Encoding": {"gzip"}, "Content-Length": {"7"}, "Connection": {"X-Hop"}, "X-Hop": {"remove"}, "Set-Cookie": {"a=7", "b=19"}},
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(spill); err != nil {
		t.Fatal(err)
	}
	rules := []Rule{{Type: RuleBlock, Match: "/asset", Disabled: true}, rule, {Type: RuleBlock, Match: "/asset"}}
	path := filepath.Join(dir, "rules.json")
	if err := SaveRules(path, rules); err != nil {
		t.Fatal(err)
	}
	loaded, err := LoadRules(path)
	if err != nil {
		t.Fatal(err)
	}
	s, _ := startServerWithRules(t, loaded...)
	// The running proxy owns its configuration, not the editor's maps.
	loaded[1].Headers.Set("Content-Type", "wrong")
	snapshot := s.Rules()
	snapshot[1].Headers.Set("Content-Type", "also wrong")
	client := clientFor(t, s, nil)
	check := func(method string, status int, want []byte) {
		t.Helper()
		req, _ := http.NewRequestWithContext(t.Context(), method, upstream.URL+"/asset", nil)
		resp, err := client.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		got, err := io.ReadAll(resp.Body)
		resp.Body.Close()
		if err != nil || resp.StatusCode != status || !bytes.Equal(got, want) {
			t.Fatalf("%s: status %d body length %d, want %d/%d: %v", method, resp.StatusCode, len(got), status, len(want), err)
		}
		if status == 201 && (resp.Header.Get("Content-Type") != "application/custom" || resp.Header.Get("Content-Encoding") != "" || resp.Header.Get("X-Hop") != "" || len(resp.Cookies()) != 2) {
			t.Fatalf("mock headers = %v", resp.Header)
		}
	}
	check("GET", 201, body)
	check("POST", 403, []byte("blocked by vscroxy\n")) // method-scoped mock must not handle POST
	rules[0].Disabled = false
	s.SetRules(rules)
	check("GET", 403, []byte("blocked by vscroxy\n")) // first enabled mapping/block wins
	rules[0], rules[1] = rules[1], rules[0]
	s.SetRules(rules)
	check("GET", 201, body) // reordering changes the winner
	before, _ := os.ReadFile(path)
	if err := SaveRules(path, []Rule{{Type: RuleMapRemote, Match: "x", Target: "file:///etc/passwd"}}); err == nil {
		t.Fatal("invalid target accepted")
	}
	after, _ := os.ReadFile(path)
	if !bytes.Equal(before, after) {
		t.Fatal("failed save changed the existing rules")
	}
}

func TestMockRefusesPartialOrStreamingResponse(t *testing.T) {
	dir := t.TempDir()
	for _, f := range []Flow{
		{Method: "GET", URL: "http://example.test", Status: 200, ResponseBody: "head", ResponseBodySize: 65537},
		{Method: "GET", URL: "http://example.test", Status: 200, ResponseBodyFile: filepath.Join(dir, "gone")},
		{Method: "GET", URL: "http://example.test", Status: 200, Kind: "sse"},
		{Method: "GET", URL: "http://example.test", Status: 200, ResponseBody: strings.Repeat("x", maxRuleBody+1)},
	} {
		if _, err := NewMockRule(filepath.Join(dir, "mocks"), f); err == nil {
			t.Fatalf("incomplete/streaming response accepted: size %d kind %q", f.ResponseBodySize, f.Kind)
		}
	}
	if _, err := os.Stat(filepath.Join(dir, "mocks")); !os.IsNotExist(err) {
		t.Fatal("rejected mocks left response files behind")
	}
}

func TestNoCachingAndAppliedRules(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("ETag", `"v1"`)
		w.Header().Set("Last-Modified", "Wed, 21 Oct 2015 07:28:00 GMT")
		w.Header().Set("Cache-Control", "max-age=3600")
		if r.Header.Get("If-None-Match") != "" || r.Header.Get("If-Modified-Since") != "" {
			w.WriteHeader(304)
			return
		}
		io.WriteString(w, r.Header.Get("Cache-Control")+" / "+r.Header.Get("Pragma"))
	}))
	defer upstream.Close()
	s, _ := startServer(t)
	client := clientFor(t, s, nil)
	defer client.CloseIdleConnections()
	for _, on := range []bool{false, true, false} {
		s.SetNoCaching(on)
		req, _ := http.NewRequestWithContext(t.Context(), "GET", upstream.URL, nil)
		req.Header.Set("If-None-Match", `"v1"`)
		req.Header.Set("If-Modified-Since", "Wed, 21 Oct 2015 07:28:00 GMT")
		resp, err := client.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		body, _ := io.ReadAll(resp.Body)
		resp.Body.Close()
		f := waitFlow(t, s)
		if on {
			if resp.StatusCode != 200 || string(body) != "no-cache / no-cache" || resp.Header.Get("ETag") != "" || resp.Header.Get("Last-Modified") != "" || !strings.Contains(resp.Header.Get("Cache-Control"), "no-store") || len(f.Actions) != 1 || f.Actions[0] != "No Caching" {
				t.Fatalf("cache bypass failed: %d %s %v %+v", resp.StatusCode, body, resp.Header, f.Actions)
			}
		} else if resp.StatusCode != 304 || resp.Header.Get("ETag") != `"v1"` || len(f.Actions) != 0 {
			t.Fatalf("normal cache semantics changed: %d %v %+v", resp.StatusCode, resp.Header, f.Actions)
		}
	}
	s.SetNoCaching(true)
	s.SetRules([]Rule{{Type: RuleBlock, Match: "/blocked"}})
	resp, err := client.Get(upstream.URL + "/blocked")
	if err != nil {
		t.Fatal(err)
	}
	io.Copy(io.Discard, resp.Body)
	resp.Body.Close()
	f := waitFlow(t, s)
	s.SetRules(nil)
	if resp.StatusCode != 403 || !strings.Contains(resp.Header.Get("Cache-Control"), "no-store") || len(f.Actions) != 2 || f.Actions[1] != "Block · /blocked" {
		t.Fatalf("local response lost cache policy or historical evidence: %+v %v", f.Actions, resp.Header)
	}
}

// Map Remote changes the port, rewrites the path and, when asked, keeps the
// Host header the client sent; WebSockets, plain and decrypted, follow the
// same rule. A plain request's original host need not exist; a CONNECT's
// host is dialed for its certificate, so the WebSockets' decoys exist but
// must never see a request.
func TestMapRemoteRewritesPathHostAndWebSockets(t *testing.T) {
	var gotPath, gotHost string
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath, gotHost = r.URL.RequestURI(), r.Host
		fmt.Fprint(w, "mapped")
	}))
	defer upstream.Close()
	ws, seen := wsEchoServer(t)
	var decoyHits atomic.Int32
	decoy := func(w http.ResponseWriter, r *http.Request) { decoyHits.Add(1) }
	plainDecoy := httptest.NewServer(http.HandlerFunc(decoy))
	defer plainDecoy.Close()
	tlsDecoy := httptest.NewTLSServer(http.HandlerFunc(decoy))
	defer tlsDecoy.Close()
	plainHost := strings.TrimPrefix(plainDecoy.URL, "http://")
	tlsPort := tlsDecoy.URL[strings.LastIndex(tlsDecoy.URL, ":")+1:]
	s, caPath := startServerWithRules(t,
		Rule{Type: RuleMapRemote, Match: "keep.invalid", Target: upstream.URL + "/base", PathFrom: "/v1/", PathTo: "/api/v2/", KeepHost: true},
		Rule{Type: RuleMapRemote, Match: "plain.invalid", Target: upstream.URL},
		Rule{Type: RuleBlock, Match: "/v1/"}, // does not act on a WebSocket
		Rule{Type: RuleMapRemote, MatchMode: "wildcard", Match: "*/v1/*", Target: ws.URL, PathFrom: "/v1/", PathTo: "/v2/"},
	)
	s.SetSSL(SSLConfig{All: true})
	proxyURL, _ := url.Parse("http://" + s.Addr())
	client := &http.Client{Transport: &http.Transport{Proxy: http.ProxyURL(proxyURL)}}
	get := func(target string) {
		t.Helper()
		resp, err := client.Get(target)
		if err != nil {
			t.Fatal(err)
		}
		body, _ := io.ReadAll(resp.Body)
		resp.Body.Close()
		if string(body) != "mapped" {
			t.Fatalf("GET %s = %d %q", target, resp.StatusCode, body)
		}
	}
	get("http://keep.invalid:8080/v1/users/a%2Fb?q=1") // first match wins over the Block
	if gotPath != "/base/api/v2/users/a%2Fb?q=1" || gotHost != "keep.invalid:8080" {
		t.Fatalf("upstream saw %q with Host %q", gotPath, gotHost)
	}
	if f := waitFlow(t, s); f.URL != "http://keep.invalid:8080/v1/users/a%2Fb?q=1" {
		t.Fatalf("the row shows %q, not the URL the client asked for", f.URL)
	}
	get("http://plain.invalid/x")
	if u, _ := url.Parse(upstream.URL); gotPath != "/x" || gotHost != u.Host {
		t.Fatalf("upstream saw %q with Host %q", gotPath, gotHost)
	}
	waitFlow(t, s)

	echo := func(conn *websocket.Conn) {
		t.Helper()
		if err := conn.WriteMessage(websocket.TextMessage, []byte("hi")); err != nil {
			t.Fatal(err)
		}
		if _, msg, err := conn.ReadMessage(); err != nil || string(msg) != "hi" {
			t.Fatalf("echo = %q, %v", msg, err)
		}
	}
	echo(dialWS(t, s, "ws://"+plainHost+"/v1/room?x=1"))
	if *seen != "/v2/room?x=1" {
		t.Fatalf("plain WebSocket reached %q", *seen)
	}
	f := waitFlow(t, s)
	if f.Kind != "ws" || f.URL != "ws://"+plainHost+"/v1/room?x=1" || !slices.ContainsFunc(f.Actions, func(a string) bool { return strings.HasPrefix(a, "Map Remote") }) {
		t.Fatalf("plain WebSocket row: %q %q %v", f.Kind, f.URL, f.Actions)
	}
	echo(dialWSS(t, s, caPath, "localhost:"+tlsPort, "/v1/secure"))
	if *seen != "/v2/secure" {
		t.Fatalf("decrypted WebSocket reached %q", *seen)
	}
	if f := waitFlow(t, s); f.Kind != "ws" || f.URL != "wss://localhost:"+tlsPort+"/v1/secure" {
		t.Fatalf("decrypted WebSocket row: %q %q", f.Kind, f.URL)
	}
	if decoyHits.Load() != 0 {
		t.Fatal("a mapped WebSocket reached its original server")
	}
}

func TestMayMapHost(t *testing.T) {
	cases := []struct {
		rule Rule
		host string
		want bool
	}{
		{Rule{Match: "api.example.com/v1/"}, "api.example.com:443", true},
		{Rule{Match: "https://example.com/v1/"}, "api.example.com:443", true}, // a URL part: the host ends with it
		{Rule{Match: "api.example.com/v1/"}, "other.test:443", false},
		{Rule{Match: "api.example.com/v1/"}, "api.example.com.evil.test:443", false},
		{Rule{Match: "example"}, "api.example.com:443", true},
		{Rule{Match: "localhost:8443/x"}, "localhost:8443", true},
		{Rule{Match: "/v1/users"}, "anything.test:443", true}, // a path could be on any host
		{Rule{MatchMode: "wildcard", Match: "*.example.com/*"}, "api.example.com:443", true},
		{Rule{MatchMode: "wildcard", Match: "*.example.com/*"}, "example.org:443", false},
		{Rule{MatchMode: "wildcard", Match: "*example*"}, "other.test:443", true},       // no path: could cover anything
		{Rule{MatchMode: "regex", Match: `^https://x\.test/`}, "other.test:443", false}, // names its host
		{Rule{MatchMode: "regex", Match: `^https://x\.test/`}, "x.test:443", true},
		{Rule{MatchMode: "regex", Match: `^https?://localhost:8443/`}, "localhost:8443", true},
		{Rule{MatchMode: "regex", Match: `^https://[a-z]+\.test/`}, "other.test:443", true},
		{Rule{MatchMode: "regex", Match: `x\.test/`}, "other.test:443", true}, // not anchored: found anywhere
		{Rule{Match: "api.example.com/", Disabled: true}, "api.example.com:443", false},
		{Rule{Match: "api.example.com/", Scenario: "Off"}, "api.example.com:443", false},
	}
	for _, tc := range cases {
		tc.rule.Type, tc.rule.Target = RuleMapRemote, "http://127.0.0.1:1"
		if got := mayMapHost([]Rule{tc.rule}, tc.host); got != tc.want {
			t.Errorf("%+v on %s = %v, want %v", tc.rule, tc.host, got, tc.want)
		}
	}
	if mayMapHost([]Rule{{Type: RuleBlock, Match: "/"}}, "x.test:443") {
		t.Error("a Block rule counted")
	}
	if !mayMapHost([]Rule{{Type: RuleMapLocal, Match: "x.test/v1/", Target: "/tmp/m.json"}}, "x.test:443") {
		t.Error("a Map Local rule did not count: its answer needs no server")
	}
}

func TestMockPatternMatchesTheRequestsAnExampleStandsFor(t *testing.T) {
	cases := []struct {
		raw       string
		match, no []string
	}{
		{"https://api.test/users/:id", []string{"https://api.test/users/7", "https://api.test/users/7?x=1"},
			[]string{"https://api.test/users", "https://api.test/users/7/orders", "https://api.test/usersX/7", "http://api.test/users/7", "https://apiXtest/users/7"}},
		{"https://api.test/users?page=1", []string{"https://api.test/users", "https://api.test/users?page=2"}, []string{"https://api.test/users/1"}},
		{"{{baseUrl}}/users", []string{"https://api.test/users", "http://x.test/v1/users?q=1"}, []string{"https://api.test/users/1", "https://api.test/admins"}},
		{"https://{{host}}/v1/{{id}}/items", []string{"https://a.test/v1/9/items"}, []string{"https://a.test/v1/9/8/items", "https://a.test/v1//items"}},
		{"api.test:8080/a.b", []string{"http://api.test:8080/a.b", "https://api.test:8080/a.b"}, []string{"https://api.test:8080/aXb"}},
	}
	for _, tc := range cases {
		m := rules.Matcher{URL: MockPattern(tc.raw), URLMode: rules.URLRegex}
		if err := m.Validate(); err != nil {
			t.Fatalf("%s: %v", tc.raw, err)
		}
		for _, u := range tc.match {
			if !m.Match(rules.Request{URL: u, Method: "GET"}) {
				t.Errorf("%s → %s does not match %s", tc.raw, m.URL, u)
			}
		}
		for _, u := range tc.no {
			if m.Match(rules.Request{URL: u, Method: "GET"}) {
				t.Errorf("%s → %s matches %s", tc.raw, m.URL, u)
			}
		}
	}
}

// An HTTPS host that does not exist, which Map Remote sends elsewhere, is
// answered with a generated certificate instead of failing the CONNECT; one
// no rule maps still fails it.
func TestMapRemoteReachesUnresolvableHTTPSHosts(t *testing.T) {
	var gotPath, gotHost string
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath, gotHost = r.URL.RequestURI(), r.Host
		fmt.Fprint(w, "mapped")
	}))
	defer upstream.Close()
	s, caPath := startServerWithRules(t,
		Rule{Type: RuleMapRemote, MatchMode: "wildcard", Match: "*.dev.invalid/api/*", Target: upstream.URL, KeepHost: true},
	)
	s.SetSSL(SSLConfig{All: true})
	client := clientFor(t, s, sslTestRoots(t, caPath))
	resp, err := client.Get("https://shop.dev.invalid/api/items?id=7")
	if err != nil {
		t.Fatal(err)
	}
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if string(body) != "mapped" || gotPath != "/api/items?id=7" || gotHost != "shop.dev.invalid" {
		t.Fatalf("got %d %q; upstream saw %q with Host %q", resp.StatusCode, body, gotPath, gotHost)
	}
	if f := waitFlow(t, s); f.URL != "https://shop.dev.invalid/api/items?id=7" || len(f.Actions) == 0 {
		t.Fatalf("row %q, actions %v", f.URL, f.Actions)
	}
	// The same host's requests no rule maps still have nowhere to go.
	if resp, err := client.Get("https://shop.dev.invalid/other"); err == nil {
		resp.Body.Close()
		if resp.StatusCode != http.StatusBadGateway {
			t.Fatalf("an unmapped request got %d", resp.StatusCode)
		}
	}
	if _, err := client.Get("https://unmapped.invalid/api/x"); err == nil {
		t.Fatal("a CONNECT to a host no rule maps succeeded")
	}
}

func TestMockOfAnExampleAnswersForAnUnresolvableHTTPSHost(t *testing.T) {
	mock, err := SaveMock(t.TempDir(), Rule{Type: RuleMapLocal, MatchMode: "regex", Match: MockPattern("https://shop.dev.invalid/users/:id"),
		Method: "GET", Status: 201, Headers: http.Header{"Content-Encoding": {"gzip"}, "X-Mock": {"yes"}}}, []byte(`{"id":7}`))
	if err != nil {
		t.Fatal(err)
	}
	if filepath.Ext(mock.Target) != ".json" {
		t.Fatalf("mock file %q", mock.Target)
	}
	s, caPath := startServerWithRules(t, mock)
	s.SetSSL(SSLConfig{All: true})
	client := clientFor(t, s, sslTestRoots(t, caPath))
	resp, err := client.Get("https://shop.dev.invalid/users/7?fields=all")
	if err != nil {
		t.Fatal(err)
	}
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	// The example's decoded body is served as is: gzip would be a lie.
	if resp.StatusCode != 201 || string(body) != `{"id":7}` || resp.Header.Get("X-Mock") != "yes" || resp.Header.Get("Content-Encoding") != "" {
		t.Fatalf("got %d %q %v", resp.StatusCode, body, resp.Header)
	}
	if _, err := client.Get("https://other.invalid/users/7"); err == nil {
		t.Fatal("a CONNECT to a host the mock does not name succeeded")
	}
}

func TestReserveIDsSharesTheLiveCounter(t *testing.T) {
	var server Server
	if first := server.ReserveIDs(3); first != 1 {
		t.Fatalf("first reserved ID = %d, want 1", first)
	}
	if next := int(server.nextID.Add(1)); next != 4 {
		t.Fatalf("next live ID = %d, want 4", next)
	}
	if first := server.ReserveIDs(0); first != 0 {
		t.Fatalf("zero reservation = %d", first)
	}
	var nilServer *Server
	if first := nilServer.ReserveIDs(2); first != 0 {
		t.Fatalf("nil server reservation = %d", first)
	}
}
