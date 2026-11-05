package capture

import (
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strconv"
	"strings"
	"testing"
)

func freePort(t *testing.T) int {
	t.Helper()
	addr := freeAddr(t)
	_, port, err := net.SplitHostPort(addr)
	if err != nil {
		t.Fatal(err)
	}
	n, err := strconv.Atoi(port)
	if err != nil {
		t.Fatal(err)
	}
	return n
}

func reverseAddr(t *testing.T, s *Server, id string) string {
	t.Helper()
	st, ok := s.ReverseProxyStatus()[id]
	if !ok || st.Addr == "" {
		t.Fatalf("reverse proxy %q is not listening: %+v", id, st)
	}
	return st.Addr
}

func getReverse(t *testing.T, addr, path, host string) (*http.Response, string) {
	t.Helper()
	req, err := http.NewRequest(http.MethodGet, "http://"+addr+path, nil)
	if err != nil {
		t.Fatal(err)
	}
	if host != "" {
		req.Host = host
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("request %s: %v", path, err)
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	return resp, string(body)
}

func TestReverseProxyForwardsPathsAndHostHeader(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprintf(w, "%s %s", r.Host, r.URL.RequestURI())
	}))
	defer upstream.Close()

	s, _ := startServer(t)
	port := freePort(t)
	if err := s.SetReverseProxies([]ReverseProxy{{
		ID: "one", Port: port, Target: upstream.URL + "/base", KeepHost: true, Enabled: true,
	}}); err != nil {
		t.Fatal(err)
	}
	addr := reverseAddr(t, s, "one")

	resp, body := getReverse(t, addr, "/item?q=1", "local.example")
	if resp.StatusCode != 200 || body != "local.example /base/item?q=1" {
		t.Fatalf("kept Host: %d %q", resp.StatusCode, body)
	}
	f := waitFlow(t, s)
	if f.URL != upstream.URL+"/base/item?q=1" || f.RequestHeader.Get("Host") != "local.example" {
		t.Fatalf("kept Host flow: %+v, headers %v", f, f.RequestHeader)
	}
	if n := strings.Count(f.RequestHeaders, "Host: local.example"); n != 1 {
		t.Fatalf("kept Host appears %d times in %q", n, f.RequestHeaders)
	}

	// The same entry with the remote Host: the upstream sees the target's
	// authority, not the local client's.
	if err := s.SetReverseProxies([]ReverseProxy{{
		ID: "one", Port: port, Target: upstream.URL + "/base", Enabled: true,
	}}); err != nil {
		t.Fatal(err)
	}
	addr = reverseAddr(t, s, "one")
	resp, body = getReverse(t, addr, "/item", "local.example")
	wantHost := strings.TrimPrefix(upstream.URL, "http://")
	if resp.StatusCode != 200 || body != wantHost+" /base/item" {
		t.Fatalf("remote Host: %d %q, want %q", resp.StatusCode, body, wantHost+" /base/item")
	}
	f = waitFlow(t, s)
	if f.RequestHeader.Get("Host") != "" {
		t.Fatalf("rewritten Host left a Host header: %v", f.RequestHeader)
	}
	if !strings.Contains(f.RequestHeaders, "Host: "+wantHost) {
		t.Fatalf("request view %q does not show the remote Host", f.RequestHeaders)
	}
}

func TestReverseProxyHTTPSRemoteAndRules(t *testing.T) {
	upstream := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/blocked" {
			t.Errorf("a blocked request reached the TLS upstream")
		}
		fmt.Fprintf(w, "secure %s %s", r.Host, r.URL.Path)
	}))
	defer upstream.Close()

	s, _ := startServer(t)
	port := freePort(t)
	if err := s.SetRules([]Rule{{Type: RuleBlock, Match: "/blocked"}}); err != nil {
		t.Fatal(err)
	}
	if err := s.SetReverseProxies([]ReverseProxy{{
		ID: "tls", Port: port, Target: upstream.URL, Enabled: true,
	}}); err != nil {
		t.Fatal(err)
	}
	addr := reverseAddr(t, s, "tls")

	resp, body := getReverse(t, addr, "/secure", "")
	if resp.StatusCode != 200 || !strings.HasPrefix(body, "secure ") || !strings.HasSuffix(body, " /secure") {
		t.Fatalf("HTTPS remote: %d %q", resp.StatusCode, body)
	}
	f := waitFlow(t, s)
	if f.Scheme != "https" || f.Status != 200 || !strings.HasSuffix(f.Path, "/secure") {
		t.Fatalf("HTTPS flow %+v", f)
	}

	resp, body = getReverse(t, addr, "/blocked", "")
	if resp.StatusCode != http.StatusForbidden || !strings.Contains(body, "blocked by vscroxy") {
		t.Fatalf("blocked: %d %q", resp.StatusCode, body)
	}
	if f := waitFlow(t, s); f.Status != http.StatusForbidden || !strings.Contains(strings.Join(f.Actions, ","), "Block") {
		t.Fatalf("blocked flow %+v", f)
	}
	if s.RuleHits(s.Rules()[0]) != 1 {
		t.Fatalf("rule hits %d, want 1", s.RuleHits(s.Rules()[0]))
	}
}

func TestReverseProxyPortConflictIsReportedAndOthersKeepRunning(t *testing.T) {
	busy, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer busy.Close()
	busyPort := busy.Addr().(*net.TCPAddr).Port
	goodPort := freePort(t)

	s, _ := startServer(t)
	err = s.SetReverseProxies([]ReverseProxy{
		{ID: "busy", Port: busyPort, Target: "http://example.test", Enabled: true},
		{ID: "good", Port: goodPort, Target: "http://example.test", Enabled: true},
	})
	if err == nil || !strings.Contains(err.Error(), strconv.Itoa(busyPort)) {
		t.Fatalf("error %v does not name the busy port %d", err, busyPort)
	}
	status := s.ReverseProxyStatus()
	if status["busy"].Error == "" || status["busy"].Addr != "" {
		t.Fatalf("busy status %+v", status["busy"])
	}
	if status["good"].Addr == "" || status["good"].Error != "" {
		t.Fatalf("good status %+v", status["good"])
	}
}

func TestReverseProxyRejectsInvalidEntries(t *testing.T) {
	s, _ := startServer(t)
	for _, tc := range []struct {
		name  string
		entry ReverseProxy
		want  string
	}{
		{"port below range", ReverseProxy{ID: "a", Port: 80, Target: "http://example.test", Enabled: true}, "1024"},
		{"bad scheme", ReverseProxy{ID: "a", Port: freePort(t), Target: "ftp://example.test", Enabled: true}, "http"},
		{"query", ReverseProxy{ID: "a", Port: freePort(t), Target: "http://example.test/?x=1", Enabled: true}, "query"},
		{"credentials", ReverseProxy{ID: "a", Port: freePort(t), Target: "http://u:p@example.test", Enabled: true}, "credentials"},
		{"duplicate port", ReverseProxy{ID: "a", Port: freePort(t), Target: "http://example.test", Enabled: true}, "already used"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			entries := []ReverseProxy{tc.entry}
			if tc.name == "duplicate port" {
				second := tc.entry
				second.ID = "b"
				entries = append(entries, second)
			}
			err := s.SetReverseProxies(entries)
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("error %v does not contain %q", err, tc.want)
			}
		})
	}
}

func TestReverseProxyHTTPSKeepsOriginalHost(t *testing.T) {
	hostCh := make(chan string, 1)
	upstream := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hostCh <- r.Host
		fmt.Fprint(w, "ok")
	}))
	defer upstream.Close()

	s, _ := startServer(t)
	port := freePort(t)
	if err := s.SetReverseProxies([]ReverseProxy{{
		ID: "tls", Port: port, Target: upstream.URL, KeepHost: true, Enabled: true,
	}}); err != nil {
		t.Fatal(err)
	}
	addr := reverseAddr(t, s, "tls")
	resp, body := getReverse(t, addr, "/secure", "local.example")
	if resp.StatusCode != 200 || body != "ok" {
		t.Fatalf("response %d %q", resp.StatusCode, body)
	}
	if got := <-hostCh; got != "local.example" {
		t.Fatalf("upstream Host %q, want local.example", got)
	}
	f := waitFlow(t, s)
	if f.URL != upstream.URL+"/secure" || f.RequestHeader.Get("Host") != "local.example" {
		t.Fatalf("captured %+v, headers %v", f, f.RequestHeader)
	}
}

func TestJoinReversePath(t *testing.T) {
	for _, tc := range []struct{ base, req, path, raw string }{
		{"http://x/base", "/item", "/base/item", ""},
		{"http://x/base/", "/item", "/base/item", ""},
		{"http://x/", "/item", "/item", ""},
		{"http://x/base", "/", "/base/", ""},
		{"http://x/base", "/a%2Fb", "/base/a/b", "/base/a%2Fb"},
		{"http://x/base%20dir", "/item", "/base dir/item", ""},
	} {
		base, err := url.Parse(tc.base)
		if err != nil {
			t.Fatal(err)
		}
		req, err := url.Parse(tc.req)
		if err != nil {
			t.Fatal(err)
		}
		path, raw := joinReversePath(base, req)
		if path != tc.path || raw != tc.raw {
			t.Errorf("%s + %s = %q, %q; want %q, %q", tc.base, tc.req, path, raw, tc.path, tc.raw)
		}
	}
}

func TestReverseProxyHTTPSWithADNSName(t *testing.T) {
	upstream := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, "named")
	}))
	defer upstream.Close()
	_, port := hostPort(t, upstream.URL)

	s, _ := startServer(t)
	local := freePort(t)
	target := "https://localhost:" + strconv.Itoa(port)
	if err := s.SetReverseProxies([]ReverseProxy{{
		ID: "dns", Port: local, Target: target, Enabled: true,
	}}); err != nil {
		t.Fatal(err)
	}
	resp, body := getReverse(t, reverseAddr(t, s, "dns"), "/secure", "")
	if resp.StatusCode != 200 || body != "named" {
		t.Fatalf("response %d %q", resp.StatusCode, body)
	}
	f := waitFlow(t, s)
	if f.Scheme != "https" || f.Host != "localhost:"+strconv.Itoa(port) || f.URL != target+"/secure" {
		t.Fatalf("captured %+v", f)
	}
}
