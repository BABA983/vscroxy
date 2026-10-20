package capture

import (
	"bytes"
	"context"
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
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/gorilla/websocket"
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

func TestReplayTrustsTheProxyCA(t *testing.T) {
	upstream := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, "replayed")
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
	resp, err := clientFor(t, s, roots).Get(u.String() + "/replay")
	if err != nil {
		t.Fatalf("request through proxy: %v", err)
	}
	_ = resp.Body.Close()
	f := waitFlow(t, s)

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := s.Replay(ctx, f); err != nil {
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
}

func TestExclusionsTunnelWithoutDecrypting(t *testing.T) {
	upstream := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {}))
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
	client := clientFor(t, s, roots)

	// Decrypted by default: the client trusts the proxy's CA, not the
	// upstream's own certificate.
	resp, err := client.Get(u.String() + "/before")
	if err != nil {
		t.Fatalf("request before the exclusion: %v", err)
	}
	_ = resp.Body.Close()

	// Tunnelled now: the client sees the upstream's certificate, which it
	// does not trust, so the handshake fails.
	s.SetExclusions([]string{"localhost"})
	// A new client, so the request opens a connection of its own rather than
	// reusing the decrypted one.
	if _, err := clientFor(t, s, roots).Get(u.String() + "/after"); err == nil {
		t.Fatal("request after the exclusion succeeded, want a TLS error")
	}
}

func TestExcluded(t *testing.T) {
	s, _ := startServer(t)
	s.SetExclusions([]string{"example.com", "*.internal.test", "# comment", ".corp.test"})
	cases := map[string]bool{
		"example.com":       true,
		"api.example.com":   true,
		"EXAMPLE.COM":       true,
		"api.internal.test": true,
		"internal.test":     false,
		"corp.test":         true,
		"deep.corp.test":    true,
		"example.org":       false,
		"notexample.com":    false,
	}
	for host, want := range cases {
		if got := s.excluded(host); got != want {
			t.Errorf("excluded(%q) = %v, want %v", host, got, want)
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
	if err := s.Replay(ctx, f); err != nil {
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

func TestBreakpointEditsRequest(t *testing.T) {
	first := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, "first")
	}))
	defer first.Close()
	second := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, "second")
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
	if !s.Resume(bp.ID, Decision{Method: "GET", URL: second.URL + "/bp"}) {
		t.Fatal("resume found no waiter")
	}
	r := <-done
	if r.err != nil {
		t.Fatalf("request: %v", r.err)
	}
	if r.body != "second" {
		t.Fatalf("body = %q, want the edited URL's upstream", r.body)
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

func startServerWithScript(t *testing.T, source string) *Server {
	t.Helper()
	s, _ := startServer(t)
	path := filepath.Join(t.TempDir(), "script.js")
	if err := os.WriteFile(path, []byte(source), 0o600); err != nil {
		t.Fatalf("write script: %v", err)
	}
	sc, err := LoadScript(path)
	if err != nil {
		t.Fatalf("load script: %v", err)
	}
	s.SetScript(sc)
	return s
}

func TestScriptRequest(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("X-From-Script") != "yes" {
			t.Errorf("script header missing: %v", r.Header)
		}
		if r.URL.Path != "/new" {
			t.Errorf("path = %q, want /new", r.URL.Path)
		}
		fmt.Fprint(w, "ok")
	}))
	defer upstream.Close()

	s := startServerWithScript(t, `
function onRequest(req) {
  req.headers["X-From-Script"] = "yes"
  req.url = req.url.replace("/old", "/new")
}`)
	resp, err := clientFor(t, s, nil).Get(upstream.URL + "/old")
	if err != nil {
		t.Fatalf("request: %v", err)
	}
	_ = resp.Body.Close()

	f := waitFlow(t, s)
	if !strings.HasSuffix(f.URL, "/new") {
		t.Fatalf("flow URL = %q, want the scripted URL", f.URL)
	}
}

func TestScriptResponse(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
		fmt.Fprint(w, "original")
	}))
	defer upstream.Close()

	s := startServerWithScript(t, `
function onResponse(res) {
  res.status = 201
  res.body = JSON.stringify({scripted: true})
  res.headers["X-Script"] = "1"
}`)
	resp, err := clientFor(t, s, nil).Get(upstream.URL + "/boom")
	if err != nil {
		t.Fatalf("request: %v", err)
	}
	body, _ := io.ReadAll(resp.Body)
	_ = resp.Body.Close()
	if resp.StatusCode != 201 || string(body) != `{"scripted":true}` || resp.Header.Get("X-Script") != "1" {
		t.Fatalf("client got %d %q %q", resp.StatusCode, body, resp.Header.Get("X-Script"))
	}

	f := waitFlow(t, s)
	if f.Status != 201 || !strings.Contains(f.ResponseBody, "scripted") {
		t.Fatalf("flow = %d %q", f.Status, f.ResponseBody)
	}
}

func TestScriptTimeout(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, "ok")
	}))
	defer upstream.Close()

	s := startServerWithScript(t, `
function onRequest(req) { while (true) {} }`)
	start := time.Now()
	resp, err := clientFor(t, s, nil).Get(upstream.URL + "/loop")
	if err != nil {
		t.Fatalf("request: %v", err)
	}
	_ = resp.Body.Close()
	if elapsed := time.Since(start); elapsed > 3*time.Second {
		t.Fatalf("script held the request for %v", elapsed)
	}
	if s.ScriptErrors() == 0 {
		t.Fatal("the interrupted script was not counted")
	}
}

func TestResponseBreakpointEdits(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
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
		done <- result{status: resp.StatusCode, body: string(body), err: err}
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
	if !s.Resume(bp.ID, Decision{Status: 201, Body: `{"edited":true}`}) {
		t.Fatal("resume found no waiter")
	}
	r := <-done
	if r.err != nil {
		t.Fatalf("request: %v", r.err)
	}
	if r.status != 201 || r.body != `{"edited":true}` {
		t.Fatalf("client got %d %q", r.status, r.body)
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
}

func TestSSEEvents(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(http.StatusOK)
		fmt.Fprint(w, "event: tick\ndata: one\n\n")
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
	if first.FlowID != f.ID || first.Frame.Data != "one" || first.Frame.Kind != "tick" {
		t.Fatalf("first event = %+v", first)
	}
	second := waitFrame(t, s)
	if second.Frame.Data != "two" {
		t.Fatalf("second event = %+v", second)
	}
}

func TestUpstreamProxy(t *testing.T) {
	upstream := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, "hello")
	}))
	defer upstream.Close()

	inner, _ := startServer(t)
	outer, caPath := startServer(t)
	if err := outer.SetUpstream("http://" + inner.Addr()); err != nil {
		t.Fatalf("set upstream: %v", err)
	}
	if got := outer.Upstream(); got != "http://"+inner.Addr() {
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
	f := waitFlow(t, inner)
	if f.Path != "/chain" {
		t.Fatalf("inner flow = %+v", f)
	}

	if err := outer.SetUpstream("not a proxy"); err == nil {
		t.Fatal("an address without a scheme was accepted")
	}
	if err := outer.SetUpstream(""); err != nil || outer.Upstream() != "" {
		t.Fatalf("clearing upstream: %v, %q", err, outer.Upstream())
	}
}
