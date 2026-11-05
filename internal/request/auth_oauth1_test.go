package request

import (
	"crypto"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha1"
	"crypto/x509"
	"encoding/base64"
	"net/http"
	"net/url"
	"strings"
	"testing"
	"time"
)

// RFC 5849 section 3.1 and 3.4.1.1, with erratum 2550's POST signature
// r6/TJjbCOr97/+UU0NsvSne7s5g=. The query and form body are the RFC's.
func oauth1RFCRequest(t *testing.T) (*Prepared, OAuth1Auth) {
	t.Helper()
	return &Prepared{
		Method: "POST",
		URL:    "http://example.com/request?b5=%3D%253D&a3=a&c%40=&a2=r%20b",
		Body:   "c2&a3=2+q",
		Header: http.Header{"Content-Type": {"application/x-www-form-urlencoded"}},
	}, OAuth1Auth{
		ConsumerKey:     "9djdj82h48djs9d2",
		ConsumerSecret:  "j49sk3j29djd",
		Token:           "kkk9d7dh3k39sjv7",
		TokenSecret:     "dh893hdasih9",
		SignatureMethod: "HMAC-SHA1",
		Timestamp:       "137131201",
		Nonce:           "7d8f3e4a",
		Realm:           "Example",
		AddTo:           "Header",
	}
}

func TestOAuth1RFC5849Vector(t *testing.T) {
	req, auth := oauth1RFCRequest(t)
	if err := auth.sign(req, time.Unix(0, 0)); err != nil {
		t.Fatal(err)
	}
	want := `OAuth realm="Example", oauth_consumer_key="9djdj82h48djs9d2", oauth_token="kkk9d7dh3k39sjv7", ` +
		`oauth_signature_method="HMAC-SHA1", oauth_timestamp="137131201", oauth_nonce="7d8f3e4a", ` +
		`oauth_signature="r6%2FTJjbCOr97%2F%2BUU0NsvSne7s5g%3D"`
	if got := req.Get("Authorization"); got != want {
		t.Errorf("got  %s\nwant %s", got, want)
	}
}

// The RFC base string with oauth_signature_method=HMAC-SHA256, signed with
// HMAC-SHA256 by an independent implementation.
func TestOAuth1HMACSHA256(t *testing.T) {
	req, auth := oauth1RFCRequest(t)
	auth.SignatureMethod = "HMAC-SHA256"
	if err := auth.sign(req, time.Unix(0, 0)); err != nil {
		t.Fatal(err)
	}
	want := `oauth_signature="ypAxjNip%2B%2BDm0fTM%2BgCl8wAo6ufSnseu1WHxL7py3BU%3D"`
	if got := req.Get("Authorization"); !strings.Contains(got, want) {
		t.Errorf("got  %s\nwant it to contain %s", got, want)
	}
}

func TestOAuth1Plaintext(t *testing.T) {
	req, auth := oauth1RFCRequest(t)
	auth.SignatureMethod = "PLAINTEXT"
	if err := auth.sign(req, time.Unix(0, 0)); err != nil {
		t.Fatal(err)
	}
	want := `oauth_signature="j49sk3j29djd%26dh893hdasih9"`
	if got := req.Get("Authorization"); !strings.Contains(got, want) {
		t.Errorf("got  %s\nwant it to contain %s", got, want)
	}
}

// The RFC signature does not depend on where the parameters are sent.
func TestOAuth1Placements(t *testing.T) {
	req, auth := oauth1RFCRequest(t)
	auth.AddTo = "Query"
	if err := auth.sign(req, time.Unix(0, 0)); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(req.URL, "oauth_signature=r6%2FTJjbCOr97%2F%2BUU0NsvSne7s5g%3D") {
		t.Errorf("query placement: %s", req.URL)
	}
	if !strings.Contains(req.URL, "b5=%3D%253D") || req.Get("Authorization") != "" {
		t.Errorf("query placement changed the original query or kept a header: %s", req.URL)
	}
	req, auth = oauth1RFCRequest(t)
	auth.AddTo = "Query"
	req.Header.Set("Authorization", "Bearer stale")
	if err := auth.sign(req, time.Unix(0, 0)); err != nil {
		t.Fatal(err)
	}
	if req.Get("Authorization") != "" {
		t.Errorf("query placement kept the old Authorization: %s", req.Get("Authorization"))
	}

	req, auth = oauth1RFCRequest(t)
	auth.AddTo = "Body"
	if err := auth.sign(req, time.Unix(0, 0)); err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(req.Body, "c2&a3=2+q&oauth_consumer_key=") ||
		!strings.Contains(req.Body, "oauth_signature=r6%2FTJjbCOr97%2F%2BUU0NsvSne7s5g%3D") {
		t.Errorf("body placement: %s", req.Body)
	}
}

// Automatic follows Postman: a form POST puts the parameters in the body,
// a GET puts them in the query.
func TestOAuth1AutomaticPlacement(t *testing.T) {
	req, auth := oauth1RFCRequest(t)
	auth.AddTo = "Auto"
	if err := auth.sign(req, time.Unix(0, 0)); err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(req.Body, "c2&a3=2+q&oauth_consumer_key=") || strings.Contains(req.URL, "oauth_consumer_key=") {
		t.Errorf("form POST: body %q, URL %q", req.Body, req.URL)
	}

	req = &Prepared{Method: "GET", URL: "https://example.com/request?x=1", Header: http.Header{}}
	auth = OAuth1Auth{ConsumerKey: "ck", ConsumerSecret: "cs", Timestamp: "137131201", Nonce: "7d8f3e4a", AddTo: "Auto"}
	if err := auth.sign(req, time.Unix(0, 0)); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(req.URL, "oauth_consumer_key=ck") || req.Body != "" {
		t.Errorf("GET: body %q, URL %q", req.Body, req.URL)
	}
}

func TestOAuth1BodyNeedsFormEncoding(t *testing.T) {
	req := &Prepared{Method: "POST", URL: "https://example.com/upload", Body: `{"a":1}`, Header: http.Header{"Content-Type": {"application/json"}}}
	auth := OAuth1Auth{ConsumerKey: "ck", ConsumerSecret: "cs", Timestamp: "137131201", Nonce: "7d8f3e4a", AddTo: "Body"}
	if err := auth.sign(req, time.Unix(0, 0)); err == nil {
		t.Error("a JSON body accepted OAuth 1.0 body placement")
	}
}

// A second signing replaces the parameters of the first, rather than
// signing them again.
func TestOAuth1SigningAgainReplaces(t *testing.T) {
	req, auth := oauth1RFCRequest(t)
	auth.AddTo = "Query"
	for range 2 {
		if err := auth.sign(req, time.Unix(0, 0)); err != nil {
			t.Fatal(err)
		}
	}
	if n := strings.Count(req.URL, "oauth_consumer_key="); n != 1 {
		t.Errorf("oauth_consumer_key appears %d times: %s", n, req.URL)
	}
}

func TestOAuth1BodyHash(t *testing.T) {
	req := &Prepared{
		Method: "POST",
		URL:    "https://api.example.com/upload",
		Body:   `{"hello":"world"}`,
		Header: http.Header{"Content-Type": {"application/json"}},
	}
	auth := OAuth1Auth{
		ConsumerKey: "key", ConsumerSecret: "secret", Token: "token", TokenSecret: "token-secret",
		SignatureMethod: "HMAC-SHA1", Timestamp: "137131201", Nonce: "7d8f3e4a",
		IncludeBodyHash: true,
	}
	if err := auth.sign(req, time.Unix(0, 0)); err != nil {
		t.Fatal(err)
	}
	sum := sha1.Sum([]byte(`{"hello":"world"}`))
	want := `oauth_body_hash="` + url.QueryEscape(base64.StdEncoding.EncodeToString(sum[:])) + `"`
	if got := req.Get("Authorization"); !strings.Contains(got, want) {
		t.Errorf("got  %s\nwant it to contain %s", got, want)
	}
}

func TestOAuth1RSA(t *testing.T) {
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	req, auth := oauth1RFCRequest(t)
	auth.SignatureMethod = "RSA-SHA256"
	der, err := x509.MarshalPKCS8PrivateKey(key)
	if err != nil {
		t.Fatal(err)
	}
	auth.PrivateKey = pemKey(t, "PRIVATE KEY", der, nil)
	if err := auth.sign(req, time.Unix(0, 0)); err != nil {
		t.Fatal(err)
	}
	header := req.Get("Authorization")
	const marker = `oauth_signature="`
	i := strings.Index(header, marker)
	if i < 0 {
		t.Fatalf("no signature: %s", header)
	}
	encoded := header[i+len(marker):]
	encoded, _, _ = strings.Cut(encoded, `"`)
	signature, err := url.QueryUnescape(encoded)
	if err != nil {
		t.Fatal(err)
	}
	raw, err := base64.StdEncoding.DecodeString(signature)
	if err != nil {
		t.Fatal(err)
	}
	// Rebuild the RFC base string with RSA-SHA256 as the method; the
	// signature must verify against the public key.
	base := "POST&http%3A%2F%2Fexample.com%2Frequest&a2%3Dr%2520b%26a3%3D2%2520q%26a3%3Da%26b5%3D%253D%25253D%26c%2540%3D%26c2%3D%26oauth_consumer_key%3D9djdj82h48djs9d2%26oauth_nonce%3D7d8f3e4a%26oauth_signature_method%3DRSA-SHA256%26oauth_timestamp%3D137131201%26oauth_token%3Dkkk9d7dh3k39sjv7"
	sum := crypto.SHA256.New()
	sum.Write([]byte(base))
	if err := rsa.VerifyPKCS1v15(&key.PublicKey, crypto.SHA256, sum.Sum(nil), raw); err != nil {
		t.Errorf("RSA-SHA256 signature does not verify: %v", err)
	}
}

func TestOAuth1BaseURL(t *testing.T) {
	for raw, want := range map[string]string{
		"http://EXAMPLE.COM:80/r%20v/X?id=123": "http://example.com/r%20v/X",
		"https://www.example.net:8080/?q=1":    "https://www.example.net:8080/",
		"https://example.com/a%2Fb":            "https://example.com/a%2Fb",
		"http://[::1]:8080/x":                  "http://[::1]:8080/x",
	} {
		u, err := url.Parse(raw)
		if err != nil {
			t.Fatal(err)
		}
		if got := oauth1BaseURL(u); got != want {
			t.Errorf("oauth1BaseURL(%q) = %q, want %q", raw, got, want)
		}
	}
}

func TestOAuth1PrepareDefaultsVersionAndExpands(t *testing.T) {
	r := Request{Method: "GET", URL: "https://api.example.com/me", Auth: Auth{Type: AuthOAuth1, OAuth1: OAuth1Auth{
		ConsumerKey: "{{consumer}}", ConsumerSecret: "{{vault:secret}}", Token: "token", TokenSecret: "token-secret",
		Timestamp: "137131201", Nonce: "7d8f3e4a",
	}}}
	req, err := r.Prepare(map[string]string{"consumer": "ck", "vault:secret": "cs"})
	if err != nil {
		t.Fatal(err)
	}
	header := req.Get("Authorization")
	if !strings.Contains(header, `oauth_consumer_key="ck"`) || !strings.Contains(header, `oauth_version="1.0"`) {
		t.Errorf("header = %s", header)
	}
}
