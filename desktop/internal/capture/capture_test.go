package capture

import (
	"crypto/tls"
	"crypto/x509"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
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
	s, err := New(freeAddr(t), dir)
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
	s, err := New(freeAddr(t), dir)
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

	if err := s.Replay(t.Context(), first); err != nil {
		t.Fatalf("replay: %v", err)
	}
	second := waitFlow(t, s)
	if second.URL != first.URL || second.Method != first.Method {
		t.Fatalf("replayed %s %s, want %s %s", second.Method, second.URL, first.Method, first.URL)
	}
	if calls != 2 {
		t.Fatalf("upstream calls = %d, want 2", calls)
	}
}
