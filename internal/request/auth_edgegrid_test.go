package request

import (
	"net/http"
	"strings"
	"testing"
)

// The expected headers below are Akamai's: the EdgeGridV1SignerTest cases in
// github.com/akamai/AkamaiOPEN-edgegrid-java, including the cases it took
// from the official Python SDK's testdata.json.
func TestEdgeGridOfficialVectors(t *testing.T) {
	basic := EdgeGridAuth{
		ClientSecret: "12rvdn/myhSSiuYAC6ZPGaI91ezhdbYd7WyTRKhGxms=",
		ClientToken:  "akaa-k7glklzuxkkh2ycw-oadjrtwpvpn6yjoj",
		AccessToken:  "akaa-dm5g2bfwoodqnc6k-ju7vlao2gz6oz234",
		BaseURL:      "https://control.akamai.com",
		Timestamp:    "20160804T07:00:00+0000",
		Nonce:        "ec9d20ee-1e9b-4c1f-925a-f0017754f86c",
	}
	python := EdgeGridAuth{
		ClientSecret:  "xxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxx=",
		ClientToken:   "akab-client-token-xxx-xxxxxxxxxxxxxxxx",
		AccessToken:   "akab-access-token-xxx-xxxxxxxxxxxxxxxx",
		BaseURL:       "https://akaa-baseurl-xxxxxxxxxxx-xxxxxxxxxxxxx.luna.akamaiapis.net",
		Timestamp:     "20140321T19:34:21+0000",
		Nonce:         "nonce-xx-xxxx-xxxx-xxxx-xxxxxxxxxxxx",
		HeadersToSign: "X-Test1,X-Test2,X-Test3",
	}
	const basicPrefix = "EG1-HMAC-SHA256 client_token=akaa-k7glklzuxkkh2ycw-oadjrtwpvpn6yjoj;" +
		"access_token=akaa-dm5g2bfwoodqnc6k-ju7vlao2gz6oz234;timestamp=20160804T07:00:00+0000;" +
		"nonce=ec9d20ee-1e9b-4c1f-925a-f0017754f86c;signature="
	const pythonPrefix = "EG1-HMAC-SHA256 client_token=akab-client-token-xxx-xxxxxxxxxxxxxxxx;" +
		"access_token=akab-access-token-xxx-xxxxxxxxxxxxxxxx;timestamp=20140321T19:34:21+0000;" +
		"nonce=nonce-xx-xxxx-xxxx-xxxx-xxxxxxxxxxxx;signature="
	for _, tt := range []struct {
		name, method, url, body string
		headers                 http.Header
		auth                    EdgeGridAuth
		want                    string
	}{
		{"GET", "GET", "https://any-hostname-at-all.com/check", "", nil, basic,
			basicPrefix + "8GpKbZnIx4XEw/zXtQdbVwIu0zJSG0RpNiVTSyIUwr0="},
		{"GET with query", "GET", "https://control.akamai.com/check?maciek=value", "", nil, basic,
			basicPrefix + "x10Wq9yA03bt+1nvPJPgVdReeIp91yLWjR0UPDSbL1Q="},
		{"POST", "POST", "https://any-hostname-at-all.com/send", "x=y&a=b", nil, basic,
			basicPrefix + "fN+xqlaSh0P07vBQ5cSCNK8gCYJfFIltzl6xrTjC6i0="},
		{"PUT ignores body", "PUT", "https://control.akamai.com/send", "x=y&a=b", nil, basic,
			basicPrefix + "OFMfMK4ROsW+TTmauMOjyrFuBr7jcJ1b6sb0+jIYj24="},
		{"escaped query", "GET", "https://control.akamai.com/api/v1/data?query=%7B%22foo%22%3A%22bar%22%7D", "", nil, basic,
			basicPrefix + "qNVm6x91VcPKfUqCOeqvEXvNkqfD9kNnj3qVXYIWQyA="},
		{"escaped path", "GET", "https://control.akamai.com/api/v1/foo%20resource", "", nil, basic,
			basicPrefix + "cXz7CtWUEi1WvNhNDYrcveufmfXBQc/yqO/JhHNloi4="},
		{"python simple GET", "GET", "https://akaa-baseurl-xxxxxxxxxxx-xxxxxxxxxxxxx.luna.akamaiapis.net/", "", nil, python,
			pythonPrefix + "tL+y4hxyHxgWVD30X3pWnGKHcPzmrIF+LThiAOhMxYU="},
		{"python GET with querystring", "GET", "https://akaa-baseurl-xxxxxxxxxxx-xxxxxxxxxxxxx.luna.akamaiapis.net/testapi/v1/t1?p1=1&p2=2", "", nil, python,
			pythonPrefix + "hKDH1UlnQySSHjvIcZpDMbQHihTQ0XyVAKZaApabdeA="},
		{"python POST inside limit", "POST", "https://akaa-baseurl-xxxxxxxxxxx-xxxxxxxxxxxxx.luna.akamaiapis.net/testapi/v1/t3", "datadatadatadatadatadatadatadata", nil, python,
			pythonPrefix + "hXm4iCxtpN22m4cbZb4lVLW5rhX8Ca82vCFqXzSTPe4="},
		{"python POST empty body", "POST", "https://akaa-baseurl-xxxxxxxxxxx-xxxxxxxxxxxxx.luna.akamaiapis.net/testapi/v1/t6", "", nil, python,
			pythonPrefix + "1gEDxeQGD5GovIkJJGcBaKnZ+VaPtrc4qBUHixjsPCQ="},
		{"python header signing", "GET", "https://akaa-baseurl-xxxxxxxxxxx-xxxxxxxxxxxxx.luna.akamaiapis.net/testapi/v1/t4", "",
			http.Header{"X-Test1": {"test-simple-header"}}, python,
			pythonPrefix + "8F9AybcRw+PLxnvT+H0JRkjROrrUgsxJTnRXMzqvcwY="},
		{"python header spaces", "GET", "https://akaa-baseurl-xxxxxxxxxxx-xxxxxxxxxxxxx.luna.akamaiapis.net/testapi/v1/t4", "",
			http.Header{"X-Test1": {"     first-thing      second-thing"}}, python,
			pythonPrefix + "WtnneL539UadAAOJwnsXvPqT4Kt6z7HMgBEwAFpt3+c="},
		{"python headers out of order", "GET", "https://akaa-baseurl-xxxxxxxxxxx-xxxxxxxxxxxxx.luna.akamaiapis.net/testapi/v1/t4", "",
			http.Header{"X-Test2": {"t2"}, "X-Test1": {"t1"}, "X-Test3": {"t3"}}, python,
			pythonPrefix + "Wus73Nx8jOYM+kkBFF2q8D1EATRIMr0WLWwpLBgkBqY="},
		{"python extra header", "GET", "https://akaa-baseurl-xxxxxxxxxxx-xxxxxxxxxxxxx.luna.akamaiapis.net/testapi/v1/t5", "",
			http.Header{"X-Test2": {"t2"}, "X-Test1": {"t1"}, "X-Test3": {"t3"}, "X-Extra": {"this won't be included"}}, python,
			pythonPrefix + "Knd/jc0A5Ghhizjayr0AUUvl2MZjBpS3FDSzvtq4Ixc="},
		{"python PUT", "PUT", "https://akaa-baseurl-xxxxxxxxxxx-xxxxxxxxxxxxx.luna.akamaiapis.net/testapi/v1/t6", "PPPPPPPPPPPPPPPPPPPPPPPPPPPPPPP", nil, python,
			pythonPrefix + "GNBWEYSEWOLtu+7dD52da2C39aX/Jchpon3K/AmBqBU="},
	} {
		t.Run(tt.name, func(t *testing.T) {
			req := &Prepared{Method: tt.method, URL: tt.url, Body: tt.body, Header: tt.headers}
			if req.Header == nil {
				req.Header = http.Header{}
			}
			if err := tt.auth.sign(req); err != nil {
				t.Fatal(err)
			}
			if got := req.Get("Authorization"); got != tt.want {
				t.Errorf("got  %s\nwant %s", got, tt.want)
			}
		})
	}
}

func TestEdgeGridPrepareExpandsAndDefaults(t *testing.T) {
	r := Request{Method: "GET", URL: "https://api.example.com/me", Auth: Auth{Type: AuthEdgeGrid, EdgeGrid: EdgeGridAuth{
		AccessToken: "{{access}}", ClientToken: "{{client}}", ClientSecret: "{{vault:secret}}",
	}}}
	req, err := r.Prepare(map[string]string{"access": "at", "client": "ct", "vault:secret": "cs"})
	if err != nil {
		t.Fatal(err)
	}
	header := req.Get("Authorization")
	if !strings.HasPrefix(header, "EG1-HMAC-SHA256 client_token=ct;access_token=at;timestamp=") {
		t.Errorf("header = %s", header)
	}
	if !strings.Contains(header, ";nonce=") || !strings.Contains(header, ";signature=") {
		t.Errorf("header = %s", header)
	}
}

func TestEdgeGridNeedsCredentials(t *testing.T) {
	req := &Prepared{Method: "GET", URL: "https://example.com/", Header: http.Header{}}
	if err := (EdgeGridAuth{ClientToken: "ct", ClientSecret: "cs"}).sign(req); err == nil {
		t.Error("a missing access token was accepted")
	}
}
