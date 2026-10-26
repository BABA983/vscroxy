package app

import (
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/baba983/vscroxy/internal/capture"
)

func TestRegressionChecksRunThroughProxyAndRunner(t *testing.T) {
	var changed atomic.Bool
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/redirect" {
			http.Redirect(w, r, "/never", http.StatusFound)
			return
		}
		w.Header().Add("X-Revision", "old")
		w.Header().Add("X-Revision", "current")
		w.WriteHeader(409) // An expected error response is a valid regression.
		if changed.Load() {
			fmt.Fprint(w, `{"items":[{"a/b":{"~id":9007199254740992}}],"ok":false,"empty":null}`)
		} else {
			fmt.Fprint(w, `{"items":[{"a/b":{"~id":9007199254740993}}],"ok":false,"empty":null}`)
		}
	}))
	defer upstream.Close()
	r := savedRequest{Name: "Conflict", Collection: "Regressions", Method: "GET", URL: upstream.URL,
		Checks: responseChecks{Status: "409", Headers: "x-revision: current", JSON: "/items/0/a~1b/~0id = 9007199254740993\n/ok = false\n/empty = null"}}
	path := filepath.Join(t.TempDir(), "requests.json")
	if err := saveRequests(path, []savedRequest{r}); err != nil {
		t.Fatal(err)
	}
	list, err := loadRequests(path)
	if err != nil {
		t.Fatal(err)
	}
	a := workspaceApp()
	a.srv, a.requests = requestProxy(t), list
	a.addDocument(list[0], "saved:Regressions\x00Conflict")
	a.openRunner()
	a.startRunner()
	waitRunner(t, a)
	if a.runner.completed != 1 || a.runner.failures != 0 || len(a.runner.rows[0].tests) != 5 {
		t.Fatalf("expected 409 with checks should pass: %+v", a.runner.rows)
	}
	changed.Store(true)
	a.startRunner()
	waitRunner(t, a)
	if a.runner.failures != 1 || a.runner.rows[0].tests[2].Pass {
		t.Fatalf("large integer changed by one but passed: %+v", a.runner.rows)
	}
	// Check the captured response itself, not a redirect target's response.
	r.URL, r.Checks = upstream.URL+"/redirect", responseChecks{Status: "302"}
	got := executeRequest(t.Context(), a.srv, r, nil, nil)
	if got.failed() || got.response.Status != 302 {
		t.Fatalf("redirect check: %s", got.summary())
	}
}

func TestRegressionScalarChecksDistinguishTypesMissingAndNumericPrecision(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		fmt.Fprint(w, `{"number":1.0,"text":"1","empty":null,"a=b":"hello","object":{}}`)
	}))
	defer upstream.Close()
	srv := requestProxy(t)
	for _, tc := range []struct {
		check string
		pass  bool
	}{
		{"/number = 1e0", true}, {`/number = "1"`, false},
		{`/text = "\u0031"`, true}, {"/text = 1", false},
		{"/empty = null", true}, {"/missing = null", false},
		{`/a=b = "hello"`, true}, {"/object = null", false},
	} {
		t.Run(tc.check, func(t *testing.T) {
			got := executeRequest(t.Context(), srv, savedRequest{Method: "GET", URL: upstream.URL, Checks: responseChecks{JSON: tc.check}}, nil, nil)
			if got.err != nil || len(got.tests) != 1 || got.failed() == tc.pass {
				t.Fatalf("want pass=%t, got %s %+v", tc.pass, got.summary(), got.tests)
			}
		})
	}
	// Invalid checks and missing variables must fail before sending traffic.
	for _, r := range []savedRequest{
		{URL: upstream.URL, Checks: responseChecks{JSON: "/broken~2pointer = 1"}},
		{URL: upstream.URL, Checks: responseChecks{Headers: "missing-colon"}},
		{URL: upstream.URL, Checks: responseChecks{Status: "999"}},
		{URL: "{{base_url}}/test", Checks: responseChecks{Status: "200"}},
	} {
		got := executeRequest(t.Context(), nil, r, nil, nil)
		if got.err == nil || got.response != nil || !got.failed() {
			t.Fatalf("invalid regression was sent: %+v", got)
		}
	}
}

func TestPortableRegressionReplacesCredentialsAndRerunsWithRecipientsVariables(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		if r.URL.Query().Get("token") != "recipient-query" || r.Header.Get("Authorization") != "recipient-auth" || r.Header.Get("Cookie") != "session=recipient" || string(body) != `{"input":"recipient"}` {
			http.Error(w, "wrong recipient configuration", 400)
			return
		}
		w.WriteHeader(201)
		fmt.Fprint(w, `{"id":73}`)
	}))
	defer upstream.Close()
	r := savedRequest{Name: "secret-name", Collection: "secret-folder", Method: "POST", URL: "https://user:secret-password@private.example/items?token=secret-query&tag=secret-tag#secret-fragment",
		Headers: "Authorization: secret-auth\nCookie: session=secret-cookie\nX-Custom-Key: secret-custom\nContent-Type: application/json\nContent-Length: 9", Body: `{"input":"secret-body"}`,
		Pre: `throw "secret-script"`, Checks: responseChecks{Status: "201", JSON: "/id = 73"}}
	text, _, err := portableRegression(r)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(text, "secret-") || strings.Contains(text, "private.example") || strings.Contains(text, "Content-Length") {
		t.Fatalf("portable export leaked credentials or framing: %s", text)
	}
	shared, err := decodeRegression([]byte(text))
	if err != nil {
		t.Fatal(err)
	}
	vars := map[string]string{"base_url": upstream.URL, "query_1": "recipient-query", "query_2": "recipient-tag", "request_body": `{"input":"recipient"}`}
	for _, line := range strings.Split(shared.Headers, "\n") {
		key, value, _ := strings.Cut(line, ": ")
		if strings.HasPrefix(value, "{{") {
			replacement := "recipient-custom"
			if key == "Authorization" {
				replacement = "recipient-auth"
			} else if key == "Cookie" {
				replacement = "session=recipient"
			}
			vars[strings.TrimSuffix(strings.TrimPrefix(value, "{{"), "}}")] = replacement
		}
	}
	got := executeRequest(t.Context(), requestProxy(t), shared, vars, nil)
	if got.failed() || len(got.tests) != 2 {
		t.Fatalf("recipient run: %s %+v", got.summary(), got.tests)
	}
	if _, _, err := portableRegression(shared); err != nil {
		t.Fatalf("sharing an imported template: %v", err)
	}
	templated := shared
	templated.URL = "{{base_url}}/items/{{ id }}/a%2Fb/%7B%7Bliteral%7D%7D?tag=one&tag=two"
	encoded, _, err := portableRegression(templated)
	if err != nil {
		t.Fatal(err)
	}
	decoded, err := decodeRegression([]byte(encoded))
	if err != nil || decoded.URL != "{{base_url}}/items/{{ id }}/a%2Fb/%7B%7Bliteral%7D%7D?tag={{query_1}}&tag={{query_2}}" {
		t.Fatalf("template or encoded path changed: %s %v", decoded.URL, err)
	}
	for _, injected := range []string{
		strings.Replace(text, `"method": "POST"`, `"pre": "req.set('X','hidden')", "method": "POST"`, 1),
		strings.Replace(text, `"method": "POST"`, `"bodyMode": "Multipart", "method": "POST"`, 1),
		strings.Replace(text, `"version": 1`, `"version": 99`, 1),
	} {
		if _, err := decodeRegression([]byte(injected)); err == nil {
			t.Fatal("accepted executable, file-backed or unknown-version case")
		}
	}
	// Truncation must not turn a JSON-prefix assertion into a pass.
	checks := responseChecks{JSON: "/id = 73"}
	if checks.evaluate(capture.Flow{ResponseBody: `{"id":73}`, ResponseBodySize: 999})[0].Pass {
		t.Fatal("incomplete response passed JSON check")
	}
}
