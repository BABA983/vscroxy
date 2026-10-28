package request

import (
	"crypto/md5"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"net/http"
	"net/http/cookiejar"
	"net/http/httptest"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"
)

// The responses expected here are the RFCs' own examples.
func TestDigestRFCExamples(t *testing.T) {
	const (
		sha  = `Digest realm="http-auth@example.org", qop="auth, auth-int", algorithm=SHA-256, nonce="7ypf/xlj9XXwfDPEoM4URrv/xwf94BcCAzFZH4GiTo0v", opaque="FQhe/qaU925kfnzjCev0ciny7QMkPqMAFRtzCUYo5tdS"`
		md   = `Digest realm="http-auth@example.org", qop="auth, auth-int", algorithm=MD5, nonce="7ypf/xlj9XXwfDPEoM4URrv/xwf94BcCAzFZH4GiTo0v", opaque="FQhe/qaU925kfnzjCev0ciny7QMkPqMAFRtzCUYo5tdS"`
		rest = `nonce="7ypf/xlj9XXwfDPEoM4URrv/xwf94BcCAzFZH4GiTo0v", uri="/dir/index.html"`
	)
	cnonce := "f2/wE4q74E6zIJEtWaHKaf5wv/H5QzzpXusqGemxURZJ"
	tail := `, qop=auth, nc=00000001, cnonce="` + cnonce + `", response="%s", opaque="FQhe/qaU925kfnzjCev0ciny7QMkPqMAFRtzCUYo5tdS"`
	for _, tt := range []struct {
		name   string
		values []string
		want   string
	}{
		// RFC 7616 §3.9.1: the first challenge the client supports is answered.
		{"RFC 7616 SHA-256", []string{sha, md}, `Digest username="Mufasa", realm="http-auth@example.org", ` + rest + `, algorithm=SHA-256` + fmt.Sprintf(tail, "753927fa0e85d155564e2e272a28d1802ca10daf4496794697cf8db5856cb6c1")},
		{"RFC 7616 MD5", []string{md}, `Digest username="Mufasa", realm="http-auth@example.org", ` + rest + `, algorithm=MD5` + fmt.Sprintf(tail, "8ca523f5e9506fed4657c9700eebdbec")},
	} {
		got, err := digestAuthorization(tt.values, "Mufasa", "Circle of Life", "GET", "/dir/index.html", cnonce)
		if err != nil || got != tt.want {
			t.Errorf("%s: %v\n got %s\nwant %s", tt.name, err, got, tt.want)
		}
	}
	// RFC 2617 §3.5: no algorithm means MD5, and none is echoed.
	got, err := digestAuthorization([]string{`Digest realm="testrealm@host.com", qop="auth,auth-int", nonce="dcd98b7102dd2f0e8b11d0f600bfb0c093", opaque="5ccc069c403ebaf9f0171e9517f40e41"`},
		"Mufasa", "Circle Of Life", "GET", "/dir/index.html", "0a4f113b")
	want := `Digest username="Mufasa", realm="testrealm@host.com", nonce="dcd98b7102dd2f0e8b11d0f600bfb0c093", uri="/dir/index.html", qop=auth, nc=00000001, cnonce="0a4f113b", response="6629fae49393a05397450978507c4ef1", opaque="5ccc069c403ebaf9f0171e9517f40e41"`
	if err != nil || got != want {
		t.Errorf("RFC 2617: %v\n got %s\nwant %s", err, got, want)
	}
}

func TestDigestChallenges(t *testing.T) {
	got := parseChallenges([]string{
		`Newauth realm="apps", type=1, title="Login to \"apps\"", Basic realm="simple"`,
		`Bearer dGVzdA==, Digest realm="a, \"b\"",nonce=n1 , qop="auth-int,auth"`,
	})
	want := []challenge{
		{"newauth", map[string]string{"realm": "apps", "type": "1", "title": `Login to "apps"`}},
		{"basic", map[string]string{"realm": "simple"}},
		{"bearer", map[string]string{}},
		{"digest", map[string]string{"realm": `a, "b"`, "nonce": "n1", "qop": "auth-int,auth"}},
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("got  %+v\nwant %+v", got, want)
	}

	// Legacy RFC 2069: no qop, so the response is H(HA1:nonce:HA2).
	h := func(s string) string { sum := md5.Sum([]byte(s)); return hex.EncodeToString(sum[:]) }
	answer, err := digestAuthorization([]string{`Basic realm="x"`, `Digest realm="r", nonce="n"`}, `a"b`, "pw", "POST", "/x?y=1", "c")
	response := h(h(`a"b:r:pw`) + ":n:" + h("POST:/x?y=1"))
	if want := `Digest username="a\"b", realm="r", nonce="n", uri="/x?y=1", response="` + response + `"`; err != nil || answer != want {
		t.Errorf("legacy: %v\n got %s\nwant %s", err, answer, want)
	}
	// -sess hashes the nonces into HA1.
	s := func(text string) string { sum := sha256.Sum256([]byte(text)); return hex.EncodeToString(sum[:]) }
	answer, _ = digestAuthorization([]string{`Digest realm="r", nonce="n", algorithm=SHA-256-sess, qop=auth`}, "u", "p", "GET", "/", "c")
	if response := s(s(s("u:r:p")+":n:c") + ":n:00000001:c:auth:" + s("GET:/")); !strings.Contains(answer, `response="`+response+`"`) || !strings.Contains(answer, "algorithm=SHA-256-sess") {
		t.Errorf("SHA-256-sess: %s", answer)
	}

	for _, tt := range []struct{ value, want string }{
		{`Basic realm="x"`, ""},
		{`Digest realm="r", nonce="n", qop="auth-int"`, "qop=auth"},
		{`Digest realm="r", nonce="n", algorithm=SHA-512-256`, "algorithm SHA-512-256"},
	} {
		answer, err := digestAuthorization([]string{tt.value}, "u", "p", "GET", "/", "c")
		if answer != "" || tt.want == "" && err != nil || tt.want != "" && (err == nil || !strings.Contains(err.Error(), tt.want)) {
			t.Errorf("%s: %q, %v", tt.value, answer, err)
		}
	}
}

// digestServer checks Digest credentials as RFC 7616 says, with SHA-256.
type digestServer struct {
	*httptest.Server
	mu       sync.Mutex
	requests []string // each request's Authorization header
}

func newDigestServer(t *testing.T, qop string) *digestServer {
	s := &digestServer{}
	s.Server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		s.mu.Lock()
		s.requests = append(s.requests, r.Header.Get("Authorization"))
		s.mu.Unlock()
		if r.URL.Path == "/open" {
			fmt.Fprint(w, "open")
			return
		}
		h := func(s string) string { sum := sha256.Sum256([]byte(s)); return hex.EncodeToString(sum[:]) }
		got := parseChallenges([]string{r.Header.Get("Authorization")})
		if len(got) == 1 && got[0].scheme == "digest" {
			p := got[0].params
			ha1 := h("ann:shop:" + "s3cret pw")
			want := h(ha1 + ":srv-nonce:" + p["nc"] + ":" + p["cnonce"] + ":auth:" + h(r.Method+":"+r.URL.RequestURI()))
			cookie, _ := r.Cookie("visit")
			if p["username"] == "ann" && p["uri"] == r.URL.RequestURI() && p["opaque"] == "op" && p["qop"] == "auth" && p["nc"] == "00000001" &&
				p["cnonce"] != "" && p["response"] == want && cookie != nil && cookie.Value == "1" {
				fmt.Fprint(w, "welcome")
				return
			}
		}
		http.SetCookie(w, &http.Cookie{Name: "visit", Value: "1", Path: "/"})
		w.Header().Add("WWW-Authenticate", `Basic realm="shop"`)
		w.Header().Add("WWW-Authenticate", `Digest realm="shop", qop="`+qop+`", algorithm=SHA-256, nonce="srv-nonce", opaque="op"`)
		w.WriteHeader(http.StatusUnauthorized)
	}))
	t.Cleanup(s.Close)
	return s
}

func (s *digestServer) log() []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]string(nil), s.requests...)
}

func TestDigestExecuteAnswersTheChallenge(t *testing.T) {
	upstream := newDigestServer(t, "auth,auth-int")
	srv := testProxy(t)
	jar, _ := cookiejar.New(nil)
	r := Request{Method: "GET", URL: upstream.URL + "/orders?page=2&q=a%20b",
		Auth:   Auth{Type: AuthDigest, Username: "{{user}}", Password: "{{vault:pw}}"},
		Checks: Checks{Status: "200"},
		Post:   `pm.test("body", () => pm.expect(pm.response.text()).to.equal("welcome"))`}
	in := Context{Vault: func(name, host string) (string, error) {
		if name != "pw" || host != "127.0.0.1" {
			return "", fmt.Errorf("not allowed")
		}
		return "s3cret pw", nil
	}}
	out := r.Execute(t.Context(), srv, in, map[string]string{"user": "ann"}, jar)
	if out.Err != nil || out.Response == nil || out.Response.Status != 200 || out.Response.ResponseBody != "welcome" {
		t.Fatalf("%s: %v; response %+v", out.Stage, out.Err, out.Response)
	}
	if len(out.Tests) != 2 || !out.Tests[0].Pass || !out.Tests[1].Pass {
		t.Errorf("checks and scripts see the answer's response: %+v", out.Tests)
	}
	if !strings.HasPrefix(out.Request.Get("Authorization"), `Digest username="ann", realm="shop"`) {
		t.Errorf("the request shown = %q", out.Request.Get("Authorization"))
	}
	if sent := upstream.log(); len(sent) != 2 || sent[0] != "" || !strings.HasPrefix(sent[1], "Digest ") {
		t.Errorf("the server saw %q, want one plain request and one answer", sent)
	}
	// Both exchanges went through the proxy.
	for i, want := range []int{401, 200} {
		select {
		case f := <-srv.Flows():
			if f.Status != want || (want == 401) != (f.RequestHeader.Get("Authorization") == "") {
				t.Errorf("captured exchange %d: %d with Authorization %q", i, f.Status, f.RequestHeader.Get("Authorization"))
			}
		case <-time.After(2 * time.Second):
			t.Fatalf("the proxy captured %d exchanges, want 2", i)
		}
	}

	// A response that is no challenge is the result: one send only.
	open := r
	open.URL, open.Checks, open.Post = upstream.URL+"/open", Checks{}, ""
	if out := open.Execute(t.Context(), srv, in, map[string]string{"user": "ann"}, jar); out.Err != nil || out.Response.ResponseBody != "open" || len(upstream.log()) != 3 {
		t.Errorf("no challenge: %v, %d requests", out.Err, len(upstream.log()))
	}
	// Wrong credentials: the answer's 401 is the result, with no third try.
	out = r.Execute(t.Context(), srv, in, map[string]string{"user": "bob"}, jar)
	if out.Err != nil || out.Response.Status != 401 || len(upstream.log()) != 5 {
		t.Errorf("wrong user: %v, %+v, %d requests", out.Err, out.Response, len(upstream.log()))
	}
}

func TestDigestExecuteRefusesAuthInt(t *testing.T) {
	upstream := newDigestServer(t, "auth-int")
	r := Request{Method: "GET", URL: upstream.URL + "/", Auth: Auth{Type: AuthDigest, Username: "ann", Password: "s3cret pw"}}
	out := r.Execute(t.Context(), testProxy(t), Context{}, nil, nil)
	if out.Err == nil || !strings.Contains(out.Err.Error(), "qop=auth") || out.Stage != "Digest authentication failed" || out.Response == nil || out.Response.Status != 401 {
		t.Errorf("%s: %v; response %+v", out.Stage, out.Err, out.Response)
	}
	if len(upstream.log()) != 1 {
		t.Errorf("sent %d requests, want 1", len(upstream.log()))
	}
}
