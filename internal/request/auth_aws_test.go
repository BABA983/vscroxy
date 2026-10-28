package request

import (
	"crypto/sha256"
	"encoding/hex"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

// The expected values below are AWS's: the Signature Version 4 test suite
// (github.com/awslabs/aws-c-auth, tests/aws-signing-test-suite/v4) and the
// S3 developer guide's header-based authentication examples.

func TestAWSSignatureTestSuite(t *testing.T) {
	signer := AWSAuth{AccessKey: "AKIDEXAMPLE", SecretKey: "wJalrXUtnFEMI/K7MDENG+bPxRfiCYEXAMPLEKEY", Service: "service"}
	at := time.Date(2015, 8, 30, 12, 36, 0, 0, time.UTC)
	form := http.Header{"Content-Type": {"application/x-www-form-urlencoded"}, "Content-Length": {"13"}}
	formCharset := http.Header{"Content-Type": {"application/x-www-form-urlencoded; charset=utf-8"}, "Content-Length": {"13"}}
	token := http.Header{"X-Amz-Security-Token": {"6e86291e8372ff2a2260956d9b8aae1d763fbf315fa00fa31553b73ebf194267"}}
	for _, tt := range []struct {
		name, method, path, query string
		header                    http.Header
		body, want                string
	}{
		{"get-vanilla", "GET", "/", "", nil, "", "AWS4-HMAC-SHA256 Credential=AKIDEXAMPLE/20150830/us-east-1/service/aws4_request, SignedHeaders=host;x-amz-date, Signature=5fa00fa31553b73ebf1942676e86291e8372ff2a2260956d9b8aae1d763fbf31"},
		{"get-vanilla-query-order-key-case", "GET", "/", "Param2=value2&Param1=value1", nil, "", "AWS4-HMAC-SHA256 Credential=AKIDEXAMPLE/20150830/us-east-1/service/aws4_request, SignedHeaders=host;x-amz-date, Signature=b97d918cfa904a5beff61c982a1b6f458b799221646efd99d3219ec94cdf2500"},
		{"get-vanilla-query-order-encoded", "GET", "/", "Param-3=Value3&Param=Value2&%E1%88%B4=Value1", nil, "", "AWS4-HMAC-SHA256 Credential=AKIDEXAMPLE/20150830/us-east-1/service/aws4_request, SignedHeaders=host;x-amz-date, Signature=371d3713e185cc334048618a97f809c9ffe339c62934c032af5a0e595648fcac"},
		{"get-vanilla-query-unreserved", "GET", "/", "-._~0123456789ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz=-._~0123456789ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz", nil, "", "AWS4-HMAC-SHA256 Credential=AKIDEXAMPLE/20150830/us-east-1/service/aws4_request, SignedHeaders=host;x-amz-date, Signature=9c3e54bfcdf0b19771a7f523ee5669cdf59bc7cc0884027167c21bb143a40197"},
		{"get-vanilla-utf8-query", "GET", "/", "ሴ=bar", nil, "", "AWS4-HMAC-SHA256 Credential=AKIDEXAMPLE/20150830/us-east-1/service/aws4_request, SignedHeaders=host;x-amz-date, Signature=2cdec8eed098649ff3a119c94853b13c643bcf08f8b0a1d91e12c9027818dd04"},
		{"get-vanilla-empty-query-key", "GET", "/", "Param1=value1", nil, "", "AWS4-HMAC-SHA256 Credential=AKIDEXAMPLE/20150830/us-east-1/service/aws4_request, SignedHeaders=host;x-amz-date, Signature=a67d582fa61cc504c4bae71f336f98b97f1ea3c7a6bfe1b6e45aec72011b9aeb"},
		{"get-unreserved", "GET", "/-._~0123456789ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz", "", nil, "", "AWS4-HMAC-SHA256 Credential=AKIDEXAMPLE/20150830/us-east-1/service/aws4_request, SignedHeaders=host;x-amz-date, Signature=07ef7494c76fa4850883e2b006601f940f8a34d404d0cfa977f52a65bbf5f24f"},
		{"get-utf8", "GET", "/ሴ", "", nil, "", "AWS4-HMAC-SHA256 Credential=AKIDEXAMPLE/20150830/us-east-1/service/aws4_request, SignedHeaders=host;x-amz-date, Signature=8318018e0b0f223aa2bbf98705b62bb787dc9c0e678f255a891fd03141be5d85"},
		{"get-space-normalized", "GET", "/example space/", "", nil, "", "AWS4-HMAC-SHA256 Credential=AKIDEXAMPLE/20150830/us-east-1/service/aws4_request, SignedHeaders=host;x-amz-date, Signature=652487583200325589f1fba4c7e578f72c47cb61beeca81406b39ddec1366741"},
		{"get-relative-relative-normalized", "GET", "/example1/example2/../..", "", nil, "", "AWS4-HMAC-SHA256 Credential=AKIDEXAMPLE/20150830/us-east-1/service/aws4_request, SignedHeaders=host;x-amz-date, Signature=5fa00fa31553b73ebf1942676e86291e8372ff2a2260956d9b8aae1d763fbf31"},
		{"get-slashes-normalized", "GET", "//example//", "", nil, "", "AWS4-HMAC-SHA256 Credential=AKIDEXAMPLE/20150830/us-east-1/service/aws4_request, SignedHeaders=host;x-amz-date, Signature=9a624bd73a37c9a373b5312afbebe7a714a789de108f0bdfe846570885f57e84"},
		{"get-slash-dot-slash-normalized", "GET", "/./", "", nil, "", "AWS4-HMAC-SHA256 Credential=AKIDEXAMPLE/20150830/us-east-1/service/aws4_request, SignedHeaders=host;x-amz-date, Signature=5fa00fa31553b73ebf1942676e86291e8372ff2a2260956d9b8aae1d763fbf31"},
		{"get-slash-pointless-dot-normalized", "GET", "/./example", "", nil, "", "AWS4-HMAC-SHA256 Credential=AKIDEXAMPLE/20150830/us-east-1/service/aws4_request, SignedHeaders=host;x-amz-date, Signature=ef75d96142cf21edca26f06005da7988e4f8dc83a165a80865db7089db637ec5"},
		{"get-vanilla-with-session-token", "GET", "/", "", token, "", "AWS4-HMAC-SHA256 Credential=AKIDEXAMPLE/20150830/us-east-1/service/aws4_request, SignedHeaders=host;x-amz-date;x-amz-security-token, Signature=07ec1639c89043aa0e3e2de82b96708f198cceab042d4a97044c66dd9f74e7f8"},
		{"post-vanilla", "POST", "/", "", nil, "", "AWS4-HMAC-SHA256 Credential=AKIDEXAMPLE/20150830/us-east-1/service/aws4_request, SignedHeaders=host;x-amz-date, Signature=5da7c1a2acd57cee7505fc6676e4e544621c30862966e37dddb68e92efbe5d6b"},
		{"post-vanilla-query", "POST", "/", "Param1=value1", nil, "", "AWS4-HMAC-SHA256 Credential=AKIDEXAMPLE/20150830/us-east-1/service/aws4_request, SignedHeaders=host;x-amz-date, Signature=28038455d6de14eafc1f9222cf5aa6f1a96197d7deb8263271d420d138af7f11"},
		{"post-x-www-form-urlencoded", "POST", "/", "", form, "Param1=value1", "AWS4-HMAC-SHA256 Credential=AKIDEXAMPLE/20150830/us-east-1/service/aws4_request, SignedHeaders=content-length;content-type;host;x-amz-content-sha256;x-amz-date, Signature=d3875051da38690788ef43de4db0d8f280229d82040bfac253562e56c3f20e0b"},
		{"post-x-www-form-urlencoded-parameters", "POST", "/", "", formCharset, "Param1=value1", "AWS4-HMAC-SHA256 Credential=AKIDEXAMPLE/20150830/us-east-1/service/aws4_request, SignedHeaders=content-length;content-type;host;x-amz-content-sha256;x-amz-date, Signature=328d1b9eaadca9f5818ef05e8392801e091653bafec24fcab71e7344e7f51422"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			header := http.Header{"X-Amz-Date": {"20150830T123600Z"}}
			for name, values := range tt.header {
				header[name] = values
			}
			sum := sha256.Sum256([]byte(tt.body))
			payload := hex.EncodeToString(sum[:])
			if tt.body != "" {
				header.Set("X-Amz-Content-Sha256", payload)
			}
			if got := signer.authorization(tt.method, "example.amazonaws.com", tt.path, tt.query, header, payload, at); got != tt.want {
				t.Errorf("got  %s\nwant %s", got, tt.want)
			}
		})
	}
}

// S3 signs the path as it is: the suite's unnormalized cases' canonical URIs.
func TestAWSPathForS3(t *testing.T) {
	for raw, want := range map[string]string{
		"//example//":              "//example//",
		"/example1/example2/../..": "/example1/example2/../..",
		"/example%20space/":        "/example%20space/",
		"":                         "/",
	} {
		if got := awsPath(raw, true); got != want {
			t.Errorf("awsPath(%q, s3) = %q, want %q", raw, got, want)
		}
	}
	// Other services encode the path as sent once more.
	if got := awsPath("/a%20b/../c", false); got != "/c" {
		t.Errorf("normalized = %q", got)
	}
	if got := awsPath("/a%20b/", false); got != "/a%2520b/" {
		t.Errorf("double-encoded = %q", got)
	}
}

func TestAWSSignatureS3Examples(t *testing.T) {
	defer func(clock func() time.Time) { now = clock }(now)
	now = func() time.Time { return time.Date(2013, 5, 24, 0, 0, 0, 0, time.UTC) }
	// The guide's access key ID is not part of what is signed; another one
	// leaves the signature as published.
	auth := Auth{Type: AuthAWS, AWS: AWSAuth{AccessKey: "{{key}}", SecretKey: "{{secret}}", Service: "s3"}}
	vars := map[string]string{"key": "AKIDEXAMPLE", "secret": "wJalrXUtnFEMI/K7MDENG/bPxRfiCYEXAMPLEKEY"}
	for query, signature := range map[string]string{
		"lifecycle":           "fea454ca298b7da1c68078a5d1bdbfbbe0d65c699e0f91ac7a200a0136783543", // GET Bucket Lifecycle
		"max-keys=2&prefix=J": "34b48302e7b5fa45bde8084f4b7868a86f0a534bc59db6670ed5711ef69dc6f7", // GET Bucket (List Objects)
	} {
		req, err := Request{Method: "GET", URL: "https://examplebucket.s3.amazonaws.com/?" + query, Auth: auth}.Prepare(vars)
		if err != nil {
			t.Fatal(err)
		}
		want := "AWS4-HMAC-SHA256 Credential=AKIDEXAMPLE/20130524/us-east-1/s3/aws4_request, SignedHeaders=host;x-amz-content-sha256;x-amz-date, Signature=" + signature
		if got := req.Get("Authorization"); got != want {
			t.Errorf("%s:\n got %s\nwant %s", query, got, want)
		}
		if req.Get("X-Amz-Date") != "20130524T000000Z" || req.Get("X-Amz-Content-Sha256") != "e3b0c44298fc1c149afbf4c8996fb92427ae41e4649b934ca495991b7852b855" {
			t.Errorf("%s: headers %v", query, req.Header)
		}
		// Signing again replaces the signature instead of adding to it.
		before := req.Header.Clone()
		if err := auth.AWS.expand(vars).sign(req, now()); err != nil || len(req.Header) != len(before) || req.Get("Authorization") != want || len(req.Header.Values("X-Amz-Date")) != 1 {
			t.Errorf("signed twice: %v %v", err, req.Header)
		}
	}
}

func TestAWSSignaturePrepare(t *testing.T) {
	defer func(clock func() time.Time) { now = clock }(now)
	now = func() time.Time { return time.Date(2015, 8, 30, 12, 36, 0, 0, time.UTC) }
	r := Request{Method: "POST", URL: "https://example.amazonaws.com/", BodyMode: "URL encoded", Form: []Field{{Name: "Param1", Value: "value1"}},
		Headers: "X-Amz-Meta-Note: a  b\nAccept: */*", Auth: Auth{Type: AuthAWS, AWS: AWSAuth{AccessKey: "AK", SecretKey: "s", Region: "eu-west-1", Service: "{{svc}}", SessionToken: "tok"}}}
	req, err := r.Prepare(map[string]string{"svc": "execute-api"})
	if err != nil {
		t.Fatal(err)
	}
	// The form's Content-Type is set before signing, so it is signed.
	got := req.Get("Authorization")
	if !strings.HasPrefix(got, "AWS4-HMAC-SHA256 Credential=AK/20150830/eu-west-1/execute-api/aws4_request, SignedHeaders=content-type;host;x-amz-content-sha256;x-amz-date;x-amz-meta-note;x-amz-security-token, Signature=") {
		t.Errorf("Authorization = %s", got)
	}
	if req.Get("X-Amz-Security-Token") != "tok" || req.Get("X-Amz-Content-Sha256") != "9095672bbd1f56dfc5b65f3e153adc8731a4a654192329106275f4c7b24d0b6e" {
		t.Errorf("headers %v", req.Header)
	}
	r.Auth.AWS.Service = ""
	if _, err := r.Prepare(nil); err == nil || !strings.Contains(err.Error(), "service") {
		t.Errorf("no service: %v", err)
	}
}

// A pre-request script's header is signed: Execute signs after applying it.
func TestAWSSignatureExecuteSignsScriptHeaders(t *testing.T) {
	var received http.Header
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { received = r.Header.Clone() }))
	defer upstream.Close()
	r := Request{Method: "GET", URL: upstream.URL + "/items?b=2&a=1",
		Pre:  `pm.request.headers.add({key: "X-Amz-Target", value: "Shop.List"});`,
		Auth: Auth{Type: AuthAWS, AWS: AWSAuth{AccessKey: "AK", SecretKey: "{{vault:aws}}", Service: "execute-api"}}}
	in := Context{Vault: func(name, host string) (string, error) { return "secret-for-" + host, nil }}
	out := r.Execute(t.Context(), testProxy(t), in, nil, nil)
	if out.Err != nil {
		t.Fatalf("%s: %v", out.Stage, out.Err)
	}
	if !strings.Contains(received.Get("Authorization"), "SignedHeaders=host;x-amz-content-sha256;x-amz-date;x-amz-target,") {
		t.Errorf("Authorization = %q", received.Get("Authorization"))
	}
	// The signature is the one a signer with the vault's secret computes.
	want := AWSAuth{AccessKey: "AK", SecretKey: "secret-for-127.0.0.1", Service: "execute-api"}
	signed := http.Header{}
	for _, name := range []string{"X-Amz-Date", "X-Amz-Content-Sha256", "X-Amz-Target"} {
		signed.Set(name, received.Get(name))
	}
	at, _ := time.Parse("20060102T150405Z", received.Get("X-Amz-Date"))
	host := strings.TrimPrefix(upstream.URL, "http://")
	if got := want.authorization("GET", host, "/items", "b=2&a=1", signed, received.Get("X-Amz-Content-Sha256"), at); got != received.Get("Authorization") || got != out.Request.Get("Authorization") {
		t.Errorf("received %q\nwant     %q", received.Get("Authorization"), got)
	}
}
