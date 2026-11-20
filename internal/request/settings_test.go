package request

import (
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"errors"
	"fmt"
	"math/big"
	"net"
	"net/http"
	"net/http/cookiejar"
	"net/http/httptest"
	"net/url"
	"os"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/baba983/vscroxy/internal/capture"
	"github.com/baba983/vscroxy/internal/leakcheck"
)

// TestMain trusts httptest's localhost certificate, whose IP SANs make it a
// useful positive control for an IP-addressed TLS send. Tests that need an
// untrusted certificate generate their own.
func TestMain(m *testing.M) {
	var trusted *httptest.Server
	var roots *os.File
	leakcheck.RunWith(m, func() {
		trusted = httptest.NewTLSServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}))
		cert := trusted.Certificate()
		var err error
		roots, err = os.CreateTemp("", "vscroxy-request-roots-*.pem")
		if err != nil {
			panic(err)
		}
		if err := pem.Encode(roots, &pem.Block{Type: "CERTIFICATE", Bytes: cert.Raw}); err != nil {
			panic(err)
		}
		if err := roots.Close(); err != nil {
			panic(err)
		}
		if err := os.Setenv("SSL_CERT_FILE", roots.Name()); err != nil {
			panic(err)
		}
	}, func() {
		trusted.Close()
		os.Remove(roots.Name())
	})
}

// untrustedTLSServer answers with a self-signed certificate for 127.0.0.1
// that is not in the system roots.
func untrustedTLSServer(t *testing.T, handler http.Handler) *httptest.Server {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	template := &x509.Certificate{
		SerialNumber: big.NewInt(1),
		Subject:      pkix.Name{CommonName: "untrusted.test"},
		NotBefore:    time.Now().Add(-time.Hour),
		NotAfter:     time.Now().Add(time.Hour),
		KeyUsage:     x509.KeyUsageDigitalSignature,
		ExtKeyUsage:  []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
		IPAddresses:  []net.IP{net.ParseIP("127.0.0.1")},
	}
	der, err := x509.CreateCertificate(rand.Reader, template, template, &key.PublicKey, key)
	if err != nil {
		t.Fatal(err)
	}
	server := httptest.NewUnstartedServer(handler)
	server.TLS = &tls.Config{Certificates: []tls.Certificate{{Certificate: [][]byte{der}, PrivateKey: key}}}
	server.StartTLS()
	t.Cleanup(server.Close)
	return server
}

// drainFlows returns the flows an exchange published, including a failed
// CONNECT whose error never reaches the replay client as text.
func drainFlows(srv *capture.Server) []capture.Flow {
	var flows []capture.Flow
	for {
		select {
		case f := <-srv.Flows():
			flows = append(flows, f)
		case <-time.After(100 * time.Millisecond):
			return flows
		}
	}
}

// A request's timeout bounds the HTTP exchange, and reports the deadline as
// a timeout rather than as a generic network failure.
func TestRequestTimeoutBoundsTheExchange(t *testing.T) {
	slow := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		select {
		case <-time.After(3 * time.Second):
		case <-r.Context().Done():
		}
	}))
	defer slow.Close()
	fast := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, "ok")
	}))
	defer fast.Close()
	srv := testProxy(t)

	one := 1
	start := time.Now()
	got := (Request{Method: "GET", URL: slow.URL, Settings: Settings{TimeoutSeconds: &one}}).
		Execute(t.Context(), srv, Context{}, nil, nil)
	elapsed := time.Since(start)
	if !errors.Is(got.Err, context.DeadlineExceeded) || got.Outcome() != "Timed out" {
		t.Fatalf("slow request = %+v, want a timeout", got)
	}
	if elapsed < 900*time.Millisecond || elapsed >= 2*time.Second {
		t.Fatalf("timed out after %s, want about 1s", elapsed)
	}

	// The same request without a limit succeeds against a server that answers.
	got = (Request{Method: "GET", URL: fast.URL}).Execute(t.Context(), srv, Context{}, nil, nil)
	if got.Err != nil || got.Response == nil || got.Response.ResponseBody != "ok" {
		t.Fatalf("request without a timeout = %+v", got)
	}
}

// Redirect settings choose whether to follow, how many hops, and Checks keep
// inspecting the original response.
func TestRequestRedirectSettings(t *testing.T) {
	var hits atomic.Int32
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits.Add(1)
		n, _ := strconv.Atoi(r.URL.Query().Get("n"))
		if n >= 2 {
			fmt.Fprint(w, "done")
			return
		}
		http.Redirect(w, r, fmt.Sprintf("/?n=%d", n+1), http.StatusFound)
	}))
	defer upstream.Close()
	srv := testProxy(t)

	for _, tt := range []struct {
		name     string
		settings Settings
		checks   Checks
		status   int
		body     string
		err      string
		hits     int32
	}{
		{name: "default follows", status: 200, body: "done", hits: 3},
		{name: "manual stops", settings: Settings{FollowRedirects: ptr(false)}, status: 302, hits: 1},
		{name: "one hop", settings: Settings{MaxRedirects: ptr(1)}, err: "stopped after 1 redirects", hits: 2},
		{name: "two hops", settings: Settings{MaxRedirects: ptr(2)}, status: 200, body: "done", hits: 3},
		{name: "checks keep the first response", checks: Checks{Status: "302"}, status: 302, hits: 1},
	} {
		t.Run(tt.name, func(t *testing.T) {
			hits.Store(0)
			got := (Request{Method: "GET", URL: upstream.URL + "/?n=0", Settings: tt.settings, Checks: tt.checks}).
				Execute(t.Context(), srv, Context{}, nil, nil)
			if tt.err != "" {
				if got.Err == nil || !strings.Contains(got.Err.Error(), tt.err) {
					t.Fatalf("request = %+v, want error %q", got, tt.err)
				}
			} else if got.Err != nil || got.Response == nil || got.Response.Status != tt.status || tt.body != "" && got.Response.ResponseBody != tt.body {
				t.Fatalf("request = %+v, want %d %q", got, tt.status, tt.body)
			}
			if n := hits.Load(); n != tt.hits {
				t.Fatalf("server saw %d requests, want %d", n, tt.hits)
			}
		})
	}
}

// A zero hop limit wins over an explicit follow flag, as Postman's
// maxRedirects: 0 and curl's --max-redirs 0 mean.
func TestZeroRedirectLimitStops(t *testing.T) {
	follow := true
	if (Settings{FollowRedirects: &follow, MaxRedirects: ptr(0)}).Follows() {
		t.Fatal("zero hop limit followed redirects")
	}
}

// Verification rejects a self-signed server whether the proxy tunnels the TLS
// or intercepts it; turning it off accepts the certificate on both paths.
func TestRequestCertificateVerification(t *testing.T) {
	for _, intercept := range []bool{false, true} {
		for _, verify := range []bool{false, true} {
			t.Run(fmt.Sprintf("intercept=%v/verify=%v", intercept, verify), func(t *testing.T) {
				upstream := untrustedTLSServer(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					fmt.Fprint(w, "secure")
				}))
				srv := testProxy(t)
				srv.SetSSL(capture.SSLConfig{All: intercept})
				value := verify
				execute := func() Execution {
					return (Request{Method: "GET", URL: upstream.URL, Settings: Settings{VerifyTLS: &value}}).
						Execute(t.Context(), srv, Context{}, nil, nil)
				}
				got := execute()
				switch {
				case verify:
					if got.Err == nil || got.Response != nil {
						t.Fatalf("self-signed server was accepted with verification on: %+v", got)
					}
					// An intercepted handshake fails before an HTTP response
					// exists, so the replay sees EOF. The capture still records
					// the certificate error that caused it.
					reason := got.Err.Error()
					for _, f := range drainFlows(srv) {
						reason += " " + f.Conn.Error
					}
					if !strings.Contains(strings.ToLower(reason), "unknown authority") {
						t.Fatalf("verification failed for the wrong reason: %v", reason)
					}
				case intercept:
					// A request's verification-off setting does not weaken
					// the decrypted proxy: it still verifies the upstream.
					if got.Err == nil {
						t.Fatalf("verification off bypassed the proxy's default verification: %+v", got)
					}
					// The explicit per-domain exception accepts it.
					srv.SetSSL(capture.SSLConfig{All: true, AllowUntrusted: []string{"127.0.0.1"}})
					got = execute()
					if got.Err != nil || got.Response == nil || got.Response.Status != 200 || got.Response.ResponseBody != "secure" {
						t.Fatalf("verification off with an allow rule = %+v, want 200", got)
					}
				default:
					// A tunneled connection belongs to the client, so the
					// proxy cannot verify it and turning verification off
					// keeps working.
					if got.Err != nil || got.Response == nil || got.Response.Status != 200 || got.Response.ResponseBody != "secure" {
						t.Fatalf("verification off = %+v, want 200", got)
					}
				}
			})
		}
	}
}

// An IP target sends no SNI. Verification must use the CONNECT host so an IP
// SAN is checked instead of failing the handshake for a missing ServerName.
func TestRequestCertificateVerificationUsesTheIPAddress(t *testing.T) {
	upstream := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, "ip-san")
	}))
	defer upstream.Close()
	srv := testProxy(t)
	srv.SetSSL(capture.SSLConfig{All: true, AllowUntrusted: []string{"127.0.0.1"}})
	verify := true
	got := (Request{Method: "GET", URL: upstream.URL, Settings: Settings{VerifyTLS: &verify}}).
		Execute(t.Context(), srv, Context{}, nil, nil)
	if got.Err != nil || got.Response == nil || got.Response.Status != 200 || got.Response.ResponseBody != "ip-san" {
		t.Fatalf("IP SAN verification = %+v, want 200", got)
	}
}

// A Map Remote rewrite reaches a different host than the CONNECT named. The
// separate client that dials it must honor the request's verification choice
// instead of inheriting the inspection proxy's insecure client.
func TestRequestCertificateVerificationAfterMapRemote(t *testing.T) {
	upstream := untrustedTLSServer(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, "mapped")
	}))
	srv := testProxy(t)
	srv.SetSSL(capture.SSLConfig{All: true})
	if err := srv.SetRules([]capture.Rule{{Type: capture.RuleMapRemote, Match: "localhost", Target: upstream.URL}}); err != nil {
		t.Fatal(err)
	}
	verify := true
	got := (Request{Method: "GET", URL: "https://localhost:1/x", Settings: Settings{VerifyTLS: &verify}}).
		Execute(t.Context(), srv, Context{}, nil, nil)
	if got.Err == nil || !strings.Contains(strings.ToLower(got.Err.Error()), "certificate") {
		t.Fatalf("Map Remote with a self-signed server = %+v, want a certificate error", got)
	}
	reason := got.Err.Error()
	for _, f := range drainFlows(srv) {
		reason += " " + f.Conn.Error
	}
	if !strings.Contains(strings.ToLower(reason), "unknown authority") {
		t.Fatalf("verification failed for the wrong reason: %q", reason)
	}

	// A request's verification-off setting does not weaken the proxy's
	// separate transport for the rewritten host either.
	verify = false
	got = (Request{Method: "GET", URL: "https://localhost:1/x", Settings: Settings{VerifyTLS: &verify}}).
		Execute(t.Context(), srv, Context{}, nil, nil)
	if got.Err == nil || !strings.Contains(strings.ToLower(got.Err.Error()), "certificate") {
		t.Fatalf("verification off after Map Remote = %+v, want a certificate error", got)
	}
	// The explicit per-domain exception accepts the rewritten target.
	srv.SetSSL(capture.SSLConfig{All: true, AllowUntrusted: []string{"127.0.0.1"}})
	got = (Request{Method: "GET", URL: "https://localhost:1/x", Settings: Settings{VerifyTLS: &verify}}).
		Execute(t.Context(), srv, Context{}, nil, nil)
	if got.Err != nil || got.Response == nil || got.Response.Status != 200 || got.Response.ResponseBody != "mapped" {
		t.Fatalf("verification off with an allow rule after Map Remote = %+v, want 200", got)
	}
}

// A folder's settings apply when the request sets none, and the request's own
// field wins over the folder's.
func TestFolderSettingsInheritance(t *testing.T) {
	verify, follow := false, false
	timeout, max := 7, 3
	folders := []Folder{
		{Path: "Shop", Settings: Settings{VerifyTLS: &verify, FollowRedirects: &follow, MaxRedirects: &max}},
		{Path: "Shop/Orders", Settings: Settings{TimeoutSeconds: &timeout}},
	}
	in := Context{Folders: Chain(folders, "Shop/Orders/Items")}
	got := in.InheritSettings(Settings{})
	if got.Verifies() || got.Follows() || got.RedirectLimit() != 3 || got.Timeout() != 7*time.Second {
		t.Fatalf("inherited settings = %+v, want no verification, no redirects, max 3 and 7s", got)
	}

	// Follow and maximum redirects inherit independently: turning following
	// back on must not discard the folder's three-hop limit.
	ownFollow := true
	got = in.InheritSettings(Settings{FollowRedirects: &ownFollow})
	if !got.Follows() || got.RedirectLimit() != 3 {
		t.Fatalf("re-enabled following = %+v, want the folder's three hops", got)
	}
	got = in.InheritSettings(Settings{FollowRedirects: &follow})
	if got.Follows() || got.RedirectLimit() != 3 {
		t.Fatalf("disabled following = %+v, want three hops kept for later", got)
	}

	ownVerify, ownMax := true, 4
	got = in.InheritSettings(Settings{VerifyTLS: &ownVerify, MaxRedirects: &ownMax})
	if !got.Verifies() || got.Follows() || got.RedirectLimit() != 4 || got.Timeout() != 7*time.Second {
		t.Fatalf("request overrides = %+v, want verification, four redirects and the folder's 7s", got)
	}
}

func ptr[T any](v T) *T { return &v }

// HTTP version, URL encoding and the cookie jar inherit like the other
// request settings; an explicit request value wins over the folder's.
func TestHTTPVersionAndRequestSwitchesInherit(t *testing.T) {
	disable, enable := true, false
	in := Context{Folders: []Folder{{Path: "Shop", Settings: Settings{
		HTTPVersion: "2", DisableCookies: &disable, DisableURLEncoding: &disable,
		SocketPath: "/var/run/folder.sock",
	}}}}
	got := in.InheritSettings(Settings{})
	if got.HTTPVersion != "2" || got.UsesCookies() || got.EncodesURL() || got.SocketPath != "/var/run/folder.sock" {
		t.Fatalf("inherited = %+v, want HTTP/2, no cookies, no URL encoding and the folder socket", got)
	}
	if p := got.Transfer(); p.HTTPVersion != "2" {
		t.Fatalf("transfer HTTP version = %q, want 2", p.HTTPVersion)
	}
	got = in.InheritSettings(Settings{HTTPVersion: "1.1", DisableCookies: &enable, DisableURLEncoding: &enable, SocketPath: "/var/run/own.sock"})
	if got.HTTPVersion != "1.1" || !got.UsesCookies() || !got.EncodesURL() || got.SocketPath != "/var/run/own.sock" {
		t.Fatalf("request overrides = %+v, want HTTP/1.1, cookies, URL encoding and the request socket", got)
	}
	if p := got.Transfer(); p.HTTPVersion != "1.1" {
		t.Fatalf("transfer HTTP version = %q, want 1.1", p.HTTPVersion)
	}
}

// A request that turns the cookie jar off sends no jar cookies and stores
// none from the response. An explicit Cookie header still goes out.
func TestDisableCookiesLeavesTheJarAlone(t *testing.T) {
	var got []string
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		got = append(got, r.Header.Get("Cookie"))
		http.SetCookie(w, &http.Cookie{Name: "server", Value: "1", Path: "/"})
	}))
	defer upstream.Close()
	u, err := url.Parse(upstream.URL)
	if err != nil {
		t.Fatal(err)
	}
	jar, _ := cookiejar.New(nil)
	jar.SetCookies(u, []*http.Cookie{{Name: "seed", Value: "1", Path: "/"}})
	disable := true
	r := Request{Method: "GET", URL: upstream.URL, Headers: "Cookie: explicit=1", Settings: Settings{DisableCookies: &disable}}
	if out := r.Execute(t.Context(), testProxy(t), Context{}, nil, jar); out.Err != nil {
		t.Fatalf("%s: %v", out.Stage, out.Err)
	}
	if len(got) != 1 || got[0] != "explicit=1" {
		t.Fatalf("Cookie header = %q, want only the explicit header", got)
	}
	var names []string
	for _, c := range jar.Cookies(u) {
		names = append(names, c.Name)
	}
	if len(names) != 1 || names[0] != "seed" {
		t.Fatalf("jar cookies = %v, want only the seeded cookie", names)
	}
}

// An explicit HTTP version reaches the origin through the proxy. The proxy
// copies the client leg's ALPN offer to its upstream handshake, so forcing
// HTTP/1.1 or HTTP/2 here decides both legs.
func TestHTTPVersionReachesTheOrigin(t *testing.T) {
	upstream := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, r.Proto)
	}))
	upstream.EnableHTTP2 = true
	upstream.StartTLS()
	defer upstream.Close()
	insecure := false
	for _, tc := range []struct{ version, want string }{{"", "HTTP/2.0"}, {"1.1", "HTTP/1.1"}, {"2", "HTTP/2.0"}} {
		r := Request{Method: "GET", URL: upstream.URL, Settings: Settings{HTTPVersion: tc.version, VerifyTLS: &insecure}}
		out := r.Execute(t.Context(), testProxy(t), Context{}, nil, nil)
		if out.Err != nil {
			t.Fatalf("HTTP %s: %s: %v", tc.version, out.Stage, out.Err)
		}
		if out.Response.ResponseProto != tc.want || out.Response.ResponseBody != tc.want {
			t.Errorf("HTTP %s: response %q with body %q, want %q", tc.version, out.Response.ResponseProto, out.Response.ResponseBody, tc.want)
		}
	}
}

// "2" requires HTTP/2 rather than merely offering it. An origin that only
// speaks HTTP/1.1 fails clearly under "2"; Auto falls back to HTTP/1.1.
func TestHTTP2RequiresTheOriginToNegotiateIt(t *testing.T) {
	upstream := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, r.Proto)
	}))
	upstream.StartTLS() // no EnableHTTP2: TLS ALPN offers http/1.1 only.
	defer upstream.Close()
	insecure := false
	for _, tc := range []struct {
		version string
		want    string
	}{{"", "HTTP/1.1"}, {"1.1", "HTTP/1.1"}} {
		r := Request{Method: "GET", URL: upstream.URL, Settings: Settings{HTTPVersion: tc.version, VerifyTLS: &insecure}}
		out := r.Execute(t.Context(), testProxy(t), Context{}, nil, nil)
		if out.Err != nil || out.Response.ResponseProto != tc.want {
			t.Errorf("HTTP %q: %s: %v, response %+v; want %s", tc.version, out.Stage, out.Err, out.Response, tc.want)
		}
	}
	r := Request{Method: "GET", URL: upstream.URL, Settings: Settings{HTTPVersion: "2", VerifyTLS: &insecure}}
	out := r.Execute(t.Context(), testProxy(t), Context{}, nil, nil)
	if out.Err == nil || !strings.Contains(out.Err.Error(), "HTTP/2 was required") {
		t.Fatalf("HTTP 2 against an HTTP/1.1 origin = %s: %v; want a required-HTTP/2 error", out.Stage, out.Err)
	}
}
