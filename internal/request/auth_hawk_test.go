package request

import (
	"net/http"
	"strings"
	"testing"
)

// Hawk's protocol example (github.com/mozilla/hawk, API.md): the GET
// request and its MAC, then the payload-validated POST request.
func TestHawkProtocolExample(t *testing.T) {
	req := &Prepared{Method: "GET", URL: "http://example.com:8000/resource/1?b=1&a=2", Header: http.Header{}}
	auth := HawkAuth{
		AuthID: "dh37fgj492je", AuthKey: "werxhqb98rpaxn39848xrunpaw3489ruxnpa98w4rxn",
		Algorithm: "sha256", Timestamp: "1353832234", Nonce: "j4h3g2", ExtraData: "some-app-ext-data",
	}
	if err := auth.sign(req); err != nil {
		t.Fatal(err)
	}
	want := `Hawk id="dh37fgj492je", ts="1353832234", nonce="j4h3g2", ext="some-app-ext-data", mac="6R4rV5iE+NPoym+WwjeHzjAGXUtLNIxmo1vpMofpLAE="`
	if got := req.Get("Authorization"); got != want {
		t.Errorf("got  %s\nwant %s", got, want)
	}
}

func TestHawkPayloadValidationExample(t *testing.T) {
	req := &Prepared{
		Method: "POST", URL: "http://example.com:8000/resource/1?b=1&a=2",
		Body: "Thank you for flying Hawk", Header: http.Header{"Content-Type": {"text/plain; charset=utf-8"}},
	}
	auth := HawkAuth{
		AuthID: "dh37fgj492je", AuthKey: "werxhqb98rpaxn39848xrunpaw3489ruxnpa98w4rxn",
		Algorithm: "sha256", Timestamp: "1353832234", Nonce: "j4h3g2", ExtraData: "some-app-ext-data",
		IncludePayloadHash: true,
	}
	if err := auth.sign(req); err != nil {
		t.Fatal(err)
	}
	want := `Hawk id="dh37fgj492je", ts="1353832234", nonce="j4h3g2", hash="Yi9LfIIFRtBEPt74PVmbTF/xVAwPn7ub15ePICfgnuY=", ext="some-app-ext-data", mac="aSe1DERmZuRl3pI36/9BdZmnErTw3sNzOOAUlfeKjVw="`
	if got := req.Get("Authorization"); got != want {
		t.Errorf("got  %s\nwant %s", got, want)
	}
}

func TestHawkAppAndDelegation(t *testing.T) {
	req := &Prepared{Method: "GET", URL: "https://example.com/resource", Header: http.Header{}}
	auth := HawkAuth{
		AuthID: "id", AuthKey: "key", Timestamp: "1353832234", Nonce: "j4h3g2",
		App: "24s23423f34dx", Delegation: "234sz34tww3sd",
	}
	if err := auth.sign(req); err != nil {
		t.Fatal(err)
	}
	header := req.Get("Authorization")
	if !strings.Contains(header, `, app="24s23423f34dx", dlg="234sz34tww3sd"`) {
		t.Errorf("app and delegation missing: %s", header)
	}
}

func TestHawkPrepareExpandsAndDefaults(t *testing.T) {
	r := Request{Method: "GET", URL: "https://api.example.com/me", Auth: Auth{Type: AuthHawk, Hawk: HawkAuth{
		AuthID: "{{id}}", AuthKey: "{{vault:key}}", Timestamp: "1353832234", Nonce: "j4h3g2",
	}}}
	req, err := r.Prepare(map[string]string{"id": "hawk-id", "vault:key": "hawk-key"})
	if err != nil {
		t.Fatal(err)
	}
	header := req.Get("Authorization")
	if !strings.HasPrefix(header, `Hawk id="hawk-id", ts="1353832234", nonce="j4h3g2"`) {
		t.Errorf("header = %s", header)
	}
	if !strings.Contains(header, `mac="`) {
		t.Errorf("no MAC: %s", header)
	}
}

func TestHawkNeedsCredentials(t *testing.T) {
	req := &Prepared{Method: "GET", URL: "https://example.com/", Header: http.Header{}}
	if err := (HawkAuth{AuthKey: "key"}).sign(req); err == nil {
		t.Error("a missing auth ID was accepted")
	}
	if err := (HawkAuth{AuthID: "id"}).sign(req); err == nil {
		t.Error("a missing auth key was accepted")
	}
}
