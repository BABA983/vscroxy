package request

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
)

// A Postman export carries Checks as pm.test code. Run in the pm runtime, it
// must report what the checks themselves report, case by case.
func TestChecksScriptMatchesEvaluate(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Header().Add("X-Multi", "a")
		w.Header().Add("X-Multi", "b")
		fmt.Fprint(w, `{"id": 42, "ratio": 1.0, "a/b": "slash", "items": [{"ok": true}], "none": null, "text": "line\u2028sep"}`)
	}))
	defer upstream.Close()
	srv := testProxy(t)

	for i, checks := range []Checks{
		// Every check passes.
		{
			Status:  "200",
			Headers: "content-type: application/json\nX-Multi: b",
			JSON:    "/id = 42\n/ratio = 1\n/a~1b = \"slash\"\n/items/0/ok = true\n/none = null\n/text = \"line\u2028sep\"",
		},
		// Every check fails: a number is not a string, an index has one
		// spelling, null is not false, the body itself is not a scalar.
		{
			Status:  "201",
			Headers: "X-Multi: c\nX-Missing: ",
			JSON:    "/id = \"42\"\n/items/00/ok = true\n/items/1 = true\n/missing = null\n/none = false\n = 1",
		},
	} {
		script, err := checks.Script()
		if err != nil || script == "" {
			t.Fatalf("Script() = %q, %v", script, err)
		}
		r := Request{Name: "checks", Method: "GET", URL: upstream.URL, Checks: checks, Post: script}
		out := r.Execute(t.Context(), srv, Context{}, nil, nil)
		if out.Err != nil {
			t.Fatalf("%s: %v\n%s", out.Stage, out.Err, script)
		}
		n := len(out.Tests) / 2
		if n == 0 || len(out.Tests) != 2*n {
			t.Fatalf("checks and script reported %d results in all: %+v", len(out.Tests), out.Tests)
		}
		for j, check := range out.Tests[:n] {
			got := out.Tests[n+j]
			if check.Pass != (i == 0) {
				t.Errorf("set %d: check %q pass=%v", i, check.Name, check.Pass)
			}
			if got.Name != check.Name || got.Pass != check.Pass {
				t.Errorf("script %q pass=%v (%s), checks %q pass=%v", got.Name, got.Pass, got.Error, check.Name, check.Pass)
			}
		}
	}
}

func TestChecksScriptIsEmptyWithoutChecks(t *testing.T) {
	if script, err := (Checks{}).Script(); script != "" || err != nil {
		t.Fatalf("Script() = %q, %v; want nothing", script, err)
	}
	if _, err := (Checks{Status: "abc"}).Script(); err == nil {
		t.Fatal("invalid checks wrote a script")
	}
}
