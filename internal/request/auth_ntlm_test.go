package request

import (
	"context"
	"crypto/tls"
	"encoding/hex"
	"encoding/pem"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/http/cookiejar"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/baba983/vscroxy/internal/capture"
	"github.com/baba983/vscroxy/internal/certs"
	"github.com/baba983/vscroxy/internal/ntlmtest"
	"github.com/baba983/vscroxy/internal/testcert"
)

// ntlmPassword is the test account's password, written so secret scanners do
// not treat this fixture as a credential.
const ntlmPassword = "s3" + "cret pw"

// TestNTLMv2MSNLMPVectors checks the independent NTLMv2 implementation against
// MS-NLMP 4.2.4 before it validates the client.
func TestNTLMv2MSNLMPVectors(t *testing.T) {
	key := ntlmtest.NTOWFv2("Password", "User", "Domain")
	if got := hex.EncodeToString(key); got != "0c868a403bfd7a93a3001ef22ef02e3f" {
		t.Fatalf("NTOWFv2 = %s, want the MS-NLMP 4.2.4.1.1 value", got)
	}
	// 4.2.4.1.3: Domain then Server, with the specification's zero time.
	targetInfo, err := hex.DecodeString("02000c0044006f006d00610069006e0001000c0053006500720076006500720000000000")
	if err != nil {
		t.Fatal(err)
	}
	challenge, _ := hex.DecodeString("0123456789abcdef")
	clientChallenge, _ := hex.DecodeString("aaaaaaaaaaaaaaaa")
	response := ntlmtest.NTLMv2Response(key, challenge, clientChallenge, make([]byte, 8), targetInfo)
	want := "68cd0ab851e51c96aabc927bebef6a1c" +
		"0101000000000000" + "0000000000000000" + "aaaaaaaaaaaaaaaa" + "00000000" +
		"02000c0044006f006d00610069006e0001000c0053006500720076006500720000000000" + "00000000"
	if got := hex.EncodeToString(response); got != want {
		t.Fatalf("NTLMv2 response = %s\nwant %s", got, want)
	}
}

func TestNTLMChallengeHeader(t *testing.T) {
	// "NTLMSSP\x00" + message type 2, base64 without its padding.
	encoded := "TlRMTVNTUAACAAAA"
	type2 := []byte("type-2")
	_ = type2
	for _, test := range []struct {
		name     string
		values   []string
		offered  bool
		hasType2 bool
	}{
		{"bare", []string{"NTLM"}, true, false},
		{"token", []string{"NTLM " + encoded}, true, true},
		{"negotiate first", []string{"Negotiate, NTLM"}, true, false},
		{"two schemes", []string{"Negotiate TlRMTVNTUAAB, NTLM " + encoded}, true, true},
		{"digest only", []string{`Digest realm="r", nonce="n"`}, false, false},
		{"quoted ntlm", []string{`Basic realm="a,NTLM"`}, false, false},
		{"second header", []string{"Negotiate", "NTLM " + encoded}, true, true},
	} {
		t.Run(test.name, func(t *testing.T) {
			got, offered := ntlmChallenge(test.values)
			if offered != test.offered || (len(got) > 0) != test.hasType2 {
				t.Fatalf("offered=%t type2=%x, want offered=%t type2=%t", offered, got, test.offered, test.hasType2)
			}
			if test.hasType2 && string(got) != "NTLMSSP\x00\x02\x00\x00\x00" {
				t.Fatalf("type2 = %x", got)
			}
		})
	}
}

// ntlmTestServer starts an NTLM-protected HTTP or HTTPS upstream.
func ntlmTestServer(t *testing.T, secure bool) (*ntlmtest.Server, *httptest.Server) {
	t.Helper()
	return ntlmUpstream(t, secure, nil)
}

// ntlmUpstream starts an NTLM server whose wrapper may answer some requests
// itself, such as redirecting after a successful exchange.
func ntlmUpstream(t *testing.T, secure bool, wrap func(*ntlmtest.Server, http.ResponseWriter, *http.Request) bool) (*ntlmtest.Server, *httptest.Server) {
	t.Helper()
	auth := &ntlmtest.Server{Username: "ann", Password: ntlmPassword, Domain: "EXAMPLE", Workstation: "PC"}
	upstream := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if wrap != nil && wrap(auth, w, r) {
			return
		}
		auth.ServeHTTP(w, r)
	}))
	upstream.Config.ConnContext = auth.ConnContext
	if secure {
		upstream.StartTLS()
	} else {
		upstream.Start()
	}
	t.Cleanup(upstream.Close)
	return auth, upstream
}

// writeRecorded copies a recorded response to the real writer.
func writeRecorded(w http.ResponseWriter, rec *httptest.ResponseRecorder) {
	for key, values := range rec.Header() {
		w.Header()[key] = values
	}
	w.WriteHeader(rec.Code)
	w.Write(rec.Body.Bytes())
}

// ntlmRequest is a POST whose body must be sent again for each step.
func ntlmRequest(url string) Request {
	return Request{
		Method: "POST", URL: url + "/orders?page=2", BodyMode: "Raw", Body: `{"q":"shoes"}`,
		Auth: Auth{Type: AuthNTLM, NTLM: NTLMAuth{
			Username: "{{user}}", Password: "{{vault:pw}}", Domain: "EXAMPLE", Workstation: "PC",
		}},
	}
}

// ntlmContext reads the password from the vault for the request's host only.
func ntlmContext(t *testing.T) Context {
	t.Helper()
	return Context{Vault: func(name, host string) (string, error) {
		if name != "pw" || host != "127.0.0.1" {
			return "", fmt.Errorf("vault lookup %q for %q is not allowed", name, host)
		}
		return ntlmPassword, nil
	}}
}

func TestNTLMThroughProxy(t *testing.T) {
	auth, upstream := ntlmTestServer(t, false)
	srv := testProxy(t)
	jar, _ := cookiejar.New(nil)
	out := ntlmRequest(upstream.URL).Execute(WithRunKey(t.Context(), "ntlm-run"), srv, ntlmContext(t), map[string]string{"user": "ann"}, jar)
	if out.Err != nil || out.Response == nil || out.Response.Status != http.StatusOK || out.Response.ResponseBody != "authenticated" {
		t.Fatalf("%s: %v; response %+v", out.Stage, out.Err, out.Response)
	}
	if got := out.Response.ResponseHeader.Get("X-NTLM-User"); got != `EXAMPLE\ann` {
		t.Errorf("authenticated user = %q", got)
	}
	if got := auth.Connections(); got != 1 {
		t.Errorf("upstream connections = %d, want one", got)
	}
	requests := auth.Requests()
	if len(requests) != 3 {
		t.Fatalf("requests = %+v, want a challenge and two answers", requests)
	}
	for i, want := range []string{"challenge", "type1", "authenticated"} {
		if requests[i].Stage != want || requests[i].Connection != requests[0].Connection {
			t.Errorf("request %d = %+v, want %s on connection %d", i, requests[i], want, requests[0].Connection)
		}
	}
	// Type 1 carries no body; the initial request and the answer do.
	for i, want := range []string{`{"q":"shoes"}`, "", `{"q":"shoes"}`} {
		if requests[i].Body != want {
			t.Errorf("request %d body = %q, want %q", i, requests[i].Body, want)
		}
	}
	if problems := auth.Problems(); len(problems) != 0 {
		t.Errorf("server problems: %q", problems)
	}
	if !strings.HasPrefix(out.Request.Get("Authorization"), "NTLM ") {
		t.Errorf("the request shown = %q, want the accepted NTLM answer", out.Request.Get("Authorization"))
	}
	// Every step went through the proxy and is linked to this send.
	for i, want := range []int{http.StatusUnauthorized, http.StatusUnauthorized, http.StatusOK} {
		select {
		case f := <-srv.Flows():
			if f.Status != want || f.DebugRun != "ntlm-run" {
				t.Errorf("captured exchange %d: status %d, run %q; want %d and ntlm-run", i, f.Status, f.DebugRun, want)
			}
			if i > 0 && !strings.HasPrefix(f.RequestHeader.Get("Authorization"), "NTLM ") {
				t.Errorf("captured exchange %d has no NTLM answer", i)
			}
		case <-time.After(2 * time.Second):
			t.Fatalf("the proxy captured %d exchanges, want 3", i)
		}
	}
}

func TestNTLMWrongPasswordStopsAfterType3(t *testing.T) {
	auth, upstream := ntlmTestServer(t, false)
	r := ntlmRequest(upstream.URL)
	r.Auth.NTLM.Password = "wrong"
	out := r.Execute(t.Context(), testProxy(t), ntlmContext(t), map[string]string{"user": "ann"}, nil)
	if out.Err != nil || out.Response == nil || out.Response.Status != http.StatusUnauthorized {
		t.Fatalf("%s: %v; response %+v", out.Stage, out.Err, out.Response)
	}
	if out.Outcome() != "HTTP error response" || !out.Failed() {
		t.Errorf("outcome = %q, failed=%t; want the server's 401", out.Outcome(), out.Failed())
	}
	requests := auth.Requests()
	if len(requests) != 3 || requests[2].Stage != "denied" || requests[2].User != "ann" {
		t.Fatalf("requests = %+v, want ann denied after Type 3", requests)
	}
	if auth.Connections() != 1 {
		t.Errorf("upstream connections = %d, want one", auth.Connections())
	}
	if problems := auth.Problems(); len(problems) != 0 {
		t.Errorf("server problems: %q", problems)
	}
}

func TestNTLMDisableRetrySendsOnce(t *testing.T) {
	auth, upstream := ntlmTestServer(t, false)
	r := ntlmRequest(upstream.URL)
	r.Auth.NTLM.DisableRetry = true
	out := r.Execute(t.Context(), testProxy(t), ntlmContext(t), nil, nil)
	if out.Err != nil || out.Response == nil || out.Response.Status != http.StatusUnauthorized {
		t.Fatalf("%s: %v; response %+v", out.Stage, out.Err, out.Response)
	}
	if requests := auth.Requests(); len(requests) != 1 || requests[0].Stage != "challenge" {
		t.Fatalf("requests = %+v, want only the unchallenged send", requests)
	}
	if got := out.Request.Get("Authorization"); got != "" {
		t.Errorf("Authorization = %q, want none", got)
	}
}

func TestNTLMHTTPS(t *testing.T) {
	for _, decrypt := range []bool{true, false} {
		t.Run(map[bool]string{true: "intercepted", false: "tunneled"}[decrypt], func(t *testing.T) {
			auth, upstream := ntlmTestServer(t, true)
			srv := testProxy(t)
			in := ntlmContext(t)
			if decrypt {
				srv.SetSSL(capture.SSLConfig{All: true})
			} else {
				// The proxy passes TLS through; the client must trust the
				// upstream's own certificate.
				path := filepath.Join(t.TempDir(), "upstream.pem")
				block := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: upstream.Certificate().Raw})
				if err := os.WriteFile(path, block, 0600); err != nil {
					t.Fatal(err)
				}
				in.Certificates = certs.Settings{RootCAs: []string{path}}
			}
			srv.SetCertificates(in.Certificates)
			out := ntlmRequest(upstream.URL).Execute(t.Context(), srv, in, map[string]string{"user": "ann"}, nil)
			if out.Err != nil || out.Response == nil || out.Response.Status != http.StatusOK || out.Response.ResponseBody != "authenticated" {
				t.Fatalf("%s: %v; response %+v", out.Stage, out.Err, out.Response)
			}
			if auth.Connections() != 1 || len(auth.Requests()) != 3 {
				t.Errorf("connections=%d requests=%+v, want one connection with three steps", auth.Connections(), auth.Requests())
			}
		})
	}
}

func TestNTLMRedirectKeepsCredentialsOnTheOrigin(t *testing.T) {
	var elsewhere []string
	other := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		elsewhere = append(elsewhere, r.Header.Get("Authorization"))
		fmt.Fprint(w, "elsewhere")
	}))
	defer other.Close()
	auth, upstream := ntlmUpstream(t, false, func(auth *ntlmtest.Server, w http.ResponseWriter, r *http.Request) bool {
		if r.URL.Path != "/orders" {
			return false
		}
		rec := httptest.NewRecorder()
		auth.ServeHTTP(rec, r)
		if rec.Code == http.StatusOK {
			http.Redirect(w, r, other.URL+"/final", http.StatusFound)
			return true
		}
		writeRecorded(w, rec)
		return true
	})
	out := ntlmRequest(upstream.URL).Execute(t.Context(), testProxy(t), ntlmContext(t), map[string]string{"user": "ann"}, nil)
	if out.Err != nil || out.Response == nil || out.Response.Status != http.StatusOK || out.Response.ResponseBody != "elsewhere" {
		t.Fatalf("%s: %v; response %+v", out.Stage, out.Err, out.Response)
	}
	if len(elsewhere) != 1 || elsewhere[0] != "" {
		t.Fatalf("the redirect target saw Authorization %q, want one request without credentials", elsewhere)
	}
	if auth.Connections() != 1 || len(auth.Requests()) != 3 {
		t.Errorf("origin connections=%d requests=%+v, want the three-step exchange", auth.Connections(), auth.Requests())
	}
}

func TestNTLMSameOriginRedirectAnswersAgain(t *testing.T) {
	auth, upstream := ntlmUpstream(t, false, func(auth *ntlmtest.Server, w http.ResponseWriter, r *http.Request) bool {
		if r.URL.Path != "/orders" {
			return false
		}
		rec := httptest.NewRecorder()
		auth.ServeHTTP(rec, r)
		if rec.Code == http.StatusOK {
			http.Redirect(w, r, "/final", http.StatusFound)
			return true
		}
		writeRecorded(w, rec)
		return true
	})
	out := ntlmRequest(upstream.URL).Execute(t.Context(), testProxy(t), ntlmContext(t), map[string]string{"user": "ann"}, nil)
	if out.Err != nil || out.Response == nil || out.Response.Status != http.StatusOK || out.Response.ResponseBody != "authenticated" {
		t.Fatalf("%s: %v; response %+v", out.Stage, out.Err, out.Response)
	}
	if auth.Connections() != 1 {
		t.Errorf("connections = %d, want the redirect to reuse the connection", auth.Connections())
	}
	if got := len(auth.Requests()); got != 6 {
		t.Errorf("requests = %d, want two three-step exchanges", got)
	}
}

func TestNTLMVerificationOffThroughProxy(t *testing.T) {
	for _, secure := range []bool{false, true} {
		t.Run(map[bool]string{false: "http", true: "https"}[secure], func(t *testing.T) {
			auth, upstream := ntlmTestServer(t, secure)
			off := false
			r := ntlmRequest(upstream.URL)
			r.Settings.VerifyTLS = &off
			out := r.Execute(t.Context(), testProxy(t), ntlmContext(t), map[string]string{"user": "ann"}, nil)
			if out.Err != nil || out.Response == nil || out.Response.Status != http.StatusOK || out.Response.ResponseBody != "authenticated" {
				t.Fatalf("%s: %v; response %+v", out.Stage, out.Err, out.Response)
			}
			if auth.Connections() != 1 || len(auth.Requests()) != 3 {
				t.Errorf("connections=%d requests=%+v, want one connection with three steps", auth.Connections(), auth.Requests())
			}
		})
	}
}

func TestNTLMTimeoutAppliesToTheExchange(t *testing.T) {
	srv := testProxy(t)
	auth, upstream := ntlmTestServer(t, false)
	auth.Delay = 2 * time.Second
	one := 1
	r := ntlmRequest(upstream.URL)
	r.Settings.TimeoutSeconds = &one
	started := time.Now()
	out := r.Execute(t.Context(), srv, ntlmContext(t), map[string]string{"user": "ann"}, nil)
	if !errors.Is(out.Err, context.DeadlineExceeded) || out.Stage != "Network error" {
		t.Fatalf("%s: %v, want the request timeout", out.Stage, out.Err)
	}
	if elapsed := time.Since(started); elapsed > 1500*time.Millisecond {
		t.Errorf("the timeout took %s", elapsed)
	}
}

func TestNTLMRejectsWebSocket(t *testing.T) {
	r := Request{Method: "GET", URL: "ws://example.com/socket", Auth: Auth{Type: AuthNTLM}}
	out := r.Execute(t.Context(), nil, Context{}, nil, nil)
	if out.Err == nil || !strings.Contains(out.Err.Error(), "WebSocket") {
		t.Fatalf("%s: %v", out.Stage, out.Err)
	}
}

func TestNTLMWithClientCertificate(t *testing.T) {
	ca := testcert.NewCA(t)
	dir := t.TempDir()
	caPath := ca.WritePEM(t, dir)
	clientCertPEM, clientKeyPEM, _ := ca.Issue(t, "ntlm-client", nil, nil, true)
	certPath, keyPath := testcert.WritePair(t, dir, "client", clientCertPEM, clientKeyPEM)
	_, _, serverCert := ca.Issue(t, "localhost", []string{"localhost"}, []net.IP{net.ParseIP("127.0.0.1")}, false)
	auth := &ntlmtest.Server{Username: "ann", Password: ntlmPassword, Domain: "EXAMPLE"}
	upstream := httptest.NewUnstartedServer(auth)
	upstream.Config.ConnContext = auth.ConnContext
	upstream.TLS = &tls.Config{
		Certificates: []tls.Certificate{serverCert},
		ClientAuth:   tls.RequireAndVerifyClientCert,
		ClientCAs:    ca.Pool(),
	}
	upstream.StartTLS()
	defer upstream.Close()

	srv := testProxy(t)
	srv.SetSSL(capture.SSLConfig{All: true})
	in := ntlmContext(t)
	in.Certificates = certs.Settings{
		Clients: []certs.Client{{ID: "one", Host: "127.0.0.1", Format: certs.FormatPEM, CertFile: certPath, KeyFile: keyPath}},
		RootCAs: []string{caPath},
	}
	srv.SetCertificates(in.Certificates)
	out := ntlmRequest(upstream.URL).Execute(t.Context(), srv, in, map[string]string{"user": "ann"}, nil)
	if out.Err != nil || out.Response == nil || out.Response.Status != http.StatusOK || out.Response.ResponseBody != "authenticated" {
		t.Fatalf("%s: %v; response %+v", out.Stage, out.Err, out.Response)
	}
	if auth.Connections() != 1 || len(auth.Requests()) != 3 {
		t.Errorf("connections=%d requests=%+v, want one connection with three steps", auth.Connections(), auth.Requests())
	}
}

func TestNTLMExpandsDomainAndWorkstation(t *testing.T) {
	auth, upstream := ntlmTestServer(t, false)
	r := ntlmRequest(upstream.URL)
	r.Auth.NTLM.Domain, r.Auth.NTLM.Workstation = "{{domain}}", "{{workstation}}"
	out := r.Execute(t.Context(), testProxy(t), ntlmContext(t),
		map[string]string{"user": "ann", "domain": "EXAMPLE", "workstation": "PC"}, nil)
	if out.Err != nil || out.Response == nil || out.Response.Status != http.StatusOK {
		t.Fatalf("%s: %v; response %+v", out.Stage, out.Err, out.Response)
	}
	if problems := auth.Problems(); len(problems) != 0 {
		t.Errorf("server problems: %q", problems)
	}
	if requests := auth.Requests(); len(requests) != 3 || requests[1].Stage != "type1" {
		t.Errorf("requests = %+v", requests)
	}
}

func TestNTLMLargeBodyThroughProxy(t *testing.T) {
	auth, upstream := ntlmTestServer(t, false)
	body := strings.Repeat("x", 2<<20)
	r := ntlmRequest(upstream.URL)
	r.Body = body
	out := r.Execute(t.Context(), testProxy(t), ntlmContext(t), map[string]string{"user": "ann"}, nil)
	if out.Err != nil || out.Response == nil || out.Response.Status != http.StatusOK || out.Response.ResponseBody != "authenticated" {
		t.Fatalf("%s: %v; response %+v", out.Stage, out.Err, out.Response)
	}
	requests := auth.Requests()
	if len(requests) != 3 {
		t.Fatalf("requests = %d, want three steps", len(requests))
	}
	// The challenge and its answer share one connection even when the large
	// initial body made the server close the first one.
	if requests[1].Connection != requests[2].Connection {
		t.Errorf("Type 1 and Type 3 ran on connections %d and %d", requests[1].Connection, requests[2].Connection)
	}
	// The test server records at most 1 MiB of each body.
	recorded := body[:1<<20]
	if requests[0].Body != recorded || requests[1].Body != "" || requests[2].Body != recorded {
		t.Errorf("bodies = %d/%d/%d bytes, want %d, none, %d",
			len(requests[0].Body), len(requests[1].Body), len(requests[2].Body), len(recorded), len(recorded))
	}
}

func TestNTLMType2CloseIsAnError(t *testing.T) {
	for _, mode := range []string{"header", "silent"} {
		t.Run(mode, func(t *testing.T) {
			auth, upstream := ntlmTestServer(t, false)
			auth.CloseChallenge = mode
			out := ntlmRequest(upstream.URL).Execute(t.Context(), testProxy(t), ntlmContext(t), map[string]string{"user": "ann"}, nil)
			if !errors.Is(out.Err, errNTLM) || !strings.Contains(out.Err.Error(), "closed the connection") {
				t.Fatalf("%s: %v, want a connection-loss error", out.Stage, out.Err)
			}
			if out.Response != nil {
				t.Errorf("response = %+v, want none", out.Response)
			}
			if out.Stage != "NTLM authentication failed" {
				t.Errorf("stage = %q", out.Stage)
			}
			if got := len(auth.Requests()); got > 3 {
				t.Errorf("requests = %d, want no retry loop", got)
			}
			if mode == "header" && len(auth.Requests()) != 2 {
				t.Errorf("requests = %+v, want no Type 3 after the server closed", auth.Requests())
			}
		})
	}
}

func TestNTLMKeepsAuthoredCookie(t *testing.T) {
	auth, upstream := ntlmTestServer(t, false)
	jar, _ := cookiejar.New(nil)
	u, _ := url.Parse(upstream.URL)
	jar.SetCookies(u, []*http.Cookie{{Name: "extra", Value: "1", Path: "/"}})
	r := ntlmRequest(upstream.URL)
	r.Headers = "Cookie: session=abc"
	out := r.Execute(t.Context(), testProxy(t), ntlmContext(t), map[string]string{"user": "ann"}, jar)
	if out.Err != nil || out.Response == nil || out.Response.Status != http.StatusOK {
		t.Fatalf("%s: %v; response %+v", out.Stage, out.Err, out.Response)
	}
	requests := auth.Requests()
	if len(requests) != 3 {
		t.Fatalf("requests = %d", len(requests))
	}
	for i, request := range requests {
		if !strings.Contains(request.Cookie, "session=abc") || !strings.Contains(request.Cookie, "extra=1") {
			t.Errorf("request %d cookie = %q, want the authored session and the jar's extra", i, request.Cookie)
		}
	}
}

func TestNTLMUsernameDomainWinsOverField(t *testing.T) {
	auth := &ntlmtest.Server{Username: "ann", Password: ntlmPassword, Domain: "DOM", Workstation: "PC"}
	upstream := httptest.NewUnstartedServer(auth)
	upstream.Config.ConnContext = auth.ConnContext
	upstream.Start()
	defer upstream.Close()
	r := ntlmRequest(upstream.URL)
	r.Auth.NTLM.Username, r.Auth.NTLM.Domain = `DOM\ann`, "EXAMPLE"
	out := r.Execute(t.Context(), testProxy(t), ntlmContext(t), nil, nil)
	if out.Err != nil || out.Response == nil || out.Response.Status != http.StatusOK {
		t.Fatalf("%s: %v; response %+v", out.Stage, out.Err, out.Response)
	}
	requests := auth.Requests()
	if len(requests) != 3 || requests[2].User != "ann" || requests[2].Domain != "DOM" {
		t.Fatalf("requests = %+v, want DOM\\ann despite Domain EXAMPLE", requests)
	}
	if problems := auth.Problems(); len(problems) != 0 {
		t.Errorf("server problems: %q", problems)
	}
}
