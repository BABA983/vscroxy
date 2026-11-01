package app

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"

	"github.com/baba983/vscroxy/internal/capture"
	"github.com/baba983/vscroxy/internal/request"
)

func TestRegressionChecksRunThroughProxyAndRunner(t *testing.T) {
	var changed atomic.Bool
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer generated-for-current-send" || r.Header.Get("X-Direct") != "kept" {
			http.Error(w, "pre-script changes did not reach current send", 400)
			return
		}
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
		Headers: "Authorization: Bearer {{token}}", Pre: `pm.environment.set("token", "generated-for-current-send"); pm.request.headers.upsert("X-Direct", "kept");`,
		Checks: request.Checks{Status: "409", Headers: "x-revision: current", JSON: "/items/0/a~1b/~0id = 9007199254740993\n/ok = false\n/empty = null"}}
	list := libraryRoundTrip(t, []savedRequest{r})
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
	r.URL, r.Checks = upstream.URL+"/redirect", request.Checks{Status: "302"}
	got := r.Execute(t.Context(), a.srv, request.Context{}, nil, nil)
	if got.Failed() || got.Response.Status != 302 {
		t.Fatalf("redirect check: %s", runSummary(got))
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
			got := savedRequest{Method: "GET", URL: upstream.URL, Checks: request.Checks{JSON: tc.check}}.Execute(t.Context(), srv, request.Context{}, nil, nil)
			if got.Err != nil || len(got.Tests) != 1 || got.Failed() == tc.pass {
				t.Fatalf("want pass=%t, got %s %+v", tc.pass, runSummary(got), got.Tests)
			}
		})
	}
	// Invalid checks and missing variables must fail before sending traffic.
	for _, r := range []savedRequest{
		{URL: upstream.URL, Checks: request.Checks{JSON: "/broken~2pointer = 1"}},
		{URL: upstream.URL, Checks: request.Checks{Headers: "missing-colon"}},
		{URL: upstream.URL, Checks: request.Checks{Status: "999"}},
		{URL: "{{base_url}}/test", Checks: request.Checks{Status: "200"}},
	} {
		got := r.Execute(t.Context(), nil, request.Context{}, nil, nil)
		if got.Err == nil || got.Response != nil || !got.Failed() {
			t.Fatalf("invalid regression was sent: %+v", got)
		}
	}
}

func TestRegressionChecksRejectTruncatedResponse(t *testing.T) {
	// Truncation must not turn a JSON-prefix assertion into a pass.
	checks := request.Checks{JSON: "/id = 73"}
	if checks.Evaluate(capture.Flow{ResponseBody: `{"id":73}`, ResponseBodySize: 999})[0].Pass {
		t.Fatal("incomplete response passed JSON check")
	}
}
