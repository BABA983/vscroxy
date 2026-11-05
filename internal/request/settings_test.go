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
	"net/http/httptest"
	"os"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/baba983/vscroxy/internal/capture"
)

// TestMain trusts httptest's localhost certificate, whose IP SANs make it a
// useful positive control for an IP-addressed TLS send. Tests that need an
// untrusted certificate generate their own.
func TestMain(m *testing.M) {
	trusted := httptest.NewTLSServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}))
	cert := trusted.Certificate()
	file, err := os.CreateTemp("", "vscroxy-request-roots-*.pem")
	if err != nil {
		panic(err)
	}
	if err := pem.Encode(file, &pem.Block{Type: "CERTIFICATE", Bytes: cert.Raw}); err != nil {
		panic(err)
	}
	if err := file.Close(); err != nil {
		panic(err)
	}
	if err := os.Setenv("SSL_CERT_FILE", file.Name()); err != nil {
		panic(err)
	}
	code := m.Run()
	trusted.Close()
	os.Remove(file.Name())
	os.Exit(code)
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
				got := (Request{Method: "GET", URL: upstream.URL, Settings: Settings{VerifyTLS: &value}}).
					Execute(t.Context(), srv, Context{}, nil, nil)
				if verify {
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
					return
				}
				if got.Err != nil || got.Response == nil || got.Response.Status != 200 || got.Response.ResponseBody != "secure" {
					t.Fatalf("verification off = %+v, want 200", got)
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
	srv.SetSSL(capture.SSLConfig{All: true})
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
	if got.Err != nil || got.Response == nil || got.Response.Status != 502 {
		t.Fatalf("Map Remote with a self-signed server = %+v, want a 502", got)
	}
	reason := ""
	for _, f := range drainFlows(srv) {
		reason += " " + f.Conn.Error
	}
	if !strings.Contains(strings.ToLower(reason), "unknown authority") {
		t.Fatalf("verification failed for the wrong reason: %q", reason)
	}

	verify = false
	got = (Request{Method: "GET", URL: "https://localhost:1/x", Settings: Settings{VerifyTLS: &verify}}).
		Execute(t.Context(), srv, Context{}, nil, nil)
	if got.Err != nil || got.Response == nil || got.Response.Status != 200 || got.Response.ResponseBody != "mapped" {
		t.Fatalf("verification off after Map Remote = %+v, want 200", got)
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
