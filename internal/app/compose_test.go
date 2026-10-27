package app

import (
	"bytes"
	"compress/gzip"
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/baba983/vscroxy/internal/capture"
)

func TestCapturedDraftUsesCompleteDecodedPayload(t *testing.T) {
	body := strings.Repeat("中文\x00x", (maxBodyView/8)+1)
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var reader io.Reader = r.Body
		if r.Header.Get("Content-Encoding") == "gzip" {
			zr, err := gzip.NewReader(r.Body)
			if err != nil {
				t.Error(err)
				return
			}
			defer zr.Close()
			reader = zr
		}
		got, _ := io.ReadAll(reader)
		if string(got) != body || len(r.Header.Values("X-Repeat")) != 2 || r.URL.RawQuery != "q=one&q=two" {
			http.Error(w, "payload or headers changed", 400)
			return
		}
		w.WriteHeader(201)
	}))
	defer upstream.Close()
	srv := requestProxy(t)
	var compressed bytes.Buffer
	zw := gzip.NewWriter(&compressed)
	zw.Write([]byte(body))
	zw.Close()
	proxyURL, _ := url.Parse("http://" + srv.Addr())
	tr := &http.Transport{Proxy: http.ProxyURL(proxyURL)}
	defer tr.CloseIdleConnections()
	req, _ := http.NewRequestWithContext(t.Context(), "POST", upstream.URL+"?q=one&q=two", &compressed)
	req.Header = http.Header{"Content-Encoding": {"gzip"}, "X-Repeat": {"a", "b"}}
	resp, err := (&http.Client{Transport: tr}).Do(req)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	a := workspaceApp()
	a.flows = nil
	select {
	case captured := <-srv.Flows():
		a.pending = []capture.Flow{captured}
	case <-time.After(5 * time.Second):
		t.Fatal("no captured request")
	}
	a.drain()
	f := a.flows[0]
	if f.reqFile == "" || f.reqSize != len(body) || f.reqHeader.Get("Content-Encoding") != "gzip" {
		t.Fatal("test must exercise a real decoded, spilled gzip capture")
	}
	a.composeFrom(f)
	if a.requestDocument == nil || a.composeBody != body {
		t.Fatal("draft did not retain the full spilled body")
	}
	file := filepath.Join(t.TempDir(), "requests.json")
	if err := saveRequests(file, []savedRequest{a.requestDocument.value()}); err != nil {
		t.Fatal(err)
	}
	list, err := loadRequests(file)
	if err != nil {
		t.Fatal(err)
	}
	got := executeRequest(t.Context(), srv, list[0], nil, nil)
	if got.err != nil || got.response == nil || got.response.Status != 201 {
		t.Fatalf("replay failed: %+v, response %+v", got, got.response)
	}
	a.requests = nil
	a.previewCapture(f, true)
	a.confirmCaptureRequest()
	if len(a.requests) != 1 || a.requests[0].Body != body || parseHeaderBlock(a.requests[0].Headers).Get("Content-Encoding") != "" {
		t.Fatal("save captured request bypassed conversion contract")
	}
	a.regressionFrom(f)
	if a.composeBody != body || a.origin == nil || a.checks.Status != "201" || parseHeaderBlock(a.composeHeaders).Get("Content-Encoding") != "" {
		t.Fatal("regression draft bypassed the complete decoded conversion contract")
	}
}

func TestCapturedDraftRejectsUnavailablePayload(t *testing.T) {
	for _, f := range []flow{
		{reqBody: "abc", reqSize: 20},
		{reqBody: "abc", reqError: "interrupted"},
		{reqFile: filepath.Join(t.TempDir(), "missing")},
		{reqBody: string([]byte{0xff, 0x00})},
		{reqTrailer: http.Header{"X-Trailer": {"value"}}},
		{kind: "connect"},
		{method: "CONNECT"}, // Imported sessions may not carry a Kind.
	} {
		a := workspaceApp()
		a.requests = nil
		a.composeFrom(f)
		a.regressionFrom(f)
		a.previewCapture(f, true)
		a.confirmCaptureRequest()
		if len(a.documents) != 0 || len(a.requests) != 0 || a.message == "" {
			t.Fatalf("unreplayable capture silently converted: %+v", f)
		}
	}
}

func TestParseHeaderBlock(t *testing.T) {
	h := parseHeaderBlock("Content-Type: application/json\nX-Empty: \n\n# a comment\nnot a header\nAccept: text/html, */*\n")
	if got := h.Get("Content-Type"); got != "application/json" {
		t.Errorf("Content-Type = %q", got)
	}
	if got := h.Get("Accept"); got != "text/html, */*" {
		t.Errorf("Accept = %q", got)
	}
	if got := h.Get("X-Empty"); got != "" || len(h["X-Empty"]) != 1 {
		t.Errorf("X-Empty = %q, want one empty value", got)
	}
	if got := h.Get("not a header"); got != "" {
		t.Errorf("a line without a colon became a header: %q", got)
	}
	if len(h) != 3 {
		t.Errorf("headers = %v, want three", h)
	}
}

func TestHeaderBlockRoundTrip(t *testing.T) {
	h := http.Header{"Content-Type": {"text/plain"}, "X-Two": {"a", "b"}}
	back := parseHeaderBlock(headerBlock(h))
	for key, values := range h {
		got := back[key]
		if len(got) != len(values) {
			t.Fatalf("%s = %v, want %v", key, got, values)
		}
		for i := range values {
			if got[i] != values[i] {
				t.Errorf("%s[%d] = %q, want %q", key, i, got[i], values[i])
			}
		}
	}
}

func TestExpandVars(t *testing.T) {
	vars := map[string]string{"base": "http://127.0.0.1:8099", "token": "abc"}
	cases := []struct{ in, want string }{
		{"{{base}}/page.html", "http://127.0.0.1:8099/page.html"},
		{"{{base}}/x?t={{token}}", "http://127.0.0.1:8099/x?t=abc"},
		{"Bearer {{token}}", "Bearer abc"},
		// A name with no value stays as written, so the mistake shows.
		{"{{missing}}/x", "{{missing}}/x"},
		{"no variables here", "no variables here"},
		{"{{base}", "{{base}"},
		{"", ""},
	}
	for _, c := range cases {
		if got := expandVars(c.in, vars); got != c.want {
			t.Errorf("expandVars(%q) = %q, want %q", c.in, got, c.want)
		}
	}
	if got := expandVars("{{base}}", nil); got != "{{base}}" {
		t.Errorf("with no variables = %q, want it unchanged", got)
	}
}

func TestEnvironmentTablePreservesValuesAndRejectsDuplicateKeys(t *testing.T) {
	a := workspaceApp()
	a.envsPath = filepath.Join(t.TempDir(), "env.json")
	a.openVars()
	a.varsRows = []requestField{{Name: "base", Value: "http://a.test"}, {Name: "empty"}, {Name: "spaced", Value: "  = x  "}}
	a.saveVars()
	envs, _, err := loadEnvs(a.envsPath)
	if err != nil || len(envs) != 1 || envs[0].Vars["spaced"] != "  = x  " || len(envs[0].Vars) != 3 {
		t.Fatalf("environment table lost literal values: %+v %v", envs, err)
	}
	a.openVars()
	a.varsRows = append(a.varsRows, requestField{Name: " base ", Value: "replacement"})
	a.saveVars()
	if !a.varsOpen || a.varsProblem == "" || a.activeVars()["base"] != "http://a.test" {
		t.Fatal("duplicate key silently replaced a value")
	}
}

func TestEnvsRoundTrip(t *testing.T) {
	path := t.TempDir() + "/env.json"
	envs := []environment{
		{Name: "dev", Vars: map[string]string{"base": "http://127.0.0.1:8099"}},
		{Name: "live", Vars: map[string]string{"base": "https://example.com"}},
	}
	if err := saveEnvs(path, envs, "live"); err != nil {
		t.Fatalf("save: %v", err)
	}
	got, active, err := loadEnvs(path)
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if active != "live" {
		t.Errorf("active = %q, want live", active)
	}
	if len(got) != 2 || got[0].Name != "dev" || got[0].Vars["base"] != "http://127.0.0.1:8099" {
		t.Fatalf("envs = %+v", got)
	}
	if missing, active, err := loadEnvs(t.TempDir() + "/none.json"); err != nil || missing != nil || active != "" {
		t.Errorf("missing file = %v, %q, %v; want nothing", missing, active, err)
	}
}

func TestPreRequestScriptChangesTheRequest(t *testing.T) {
	req := &jsRequest{
		Method: "GET",
		URL:    "http://127.0.0.1:8099/page.html",
		header: http.Header{},
	}
	run := &scriptRun{vars: map[string]string{"token": "abc"}}
	src := `
		req.method = "POST"
		req.body = JSON.stringify({name: "vscroxy"})
		req.set("Authorization", "Bearer " + vars.get("token"))
		req.set("X-Drop", "1")
		req.remove("X-Drop")
		vars.set("last", req.url)
		log("ready")
	`
	if err := runScript(src, run, map[string]any{"req": req}); err != nil {
		t.Fatalf("run: %v", err)
	}
	if req.Method != "POST" {
		t.Errorf("method = %q, want POST", req.Method)
	}
	if got, want := req.Get("Authorization"), "Bearer abc"; got != want {
		t.Errorf("Authorization = %q, want %q", got, want)
	}
	if got := req.Get("X-Drop"); got != "" {
		t.Errorf("X-Drop = %q, want it removed", got)
	}
	if want := `{"name":"vscroxy"}`; req.Body != want {
		t.Errorf("body = %q, want %q", req.Body, want)
	}
	if run.vars["last"] != "http://127.0.0.1:8099/page.html" {
		t.Errorf("vars.last = %q", run.vars["last"])
	}
	if len(run.logs) != 1 || run.logs[0] != "ready" {
		t.Errorf("logs = %v", run.logs)
	}
}

func TestPostResponseScriptAsserts(t *testing.T) {
	res := &jsResponse{
		Status: 200,
		Body:   `{"id": 7, "name": "vscroxy"}`,
		header: http.Header{"Content-Type": {"application/json"}},
	}
	run := &scriptRun{vars: map[string]string{}}
	src := `
		test("status is 200", res.status === 200)
		test("content type is json", res.get("Content-Type") === "application/json")
		var body = res.json()
		test("the name is vscroxy", body.name === "vscroxy")
		test("a wrong one fails", res.status === 404)
		vars.set("id", String(body.id))
	`
	if err := runScript(src, run, map[string]any{"res": res}); err != nil {
		t.Fatalf("run: %v", err)
	}
	if len(run.tests) != 4 {
		t.Fatalf("tests = %+v, want four", run.tests)
	}
	for i, want := range []bool{true, true, true, false} {
		if run.tests[i].Pass != want {
			t.Errorf("test %d (%s) pass = %v, want %v", i, run.tests[i].Name, run.tests[i].Pass, want)
		}
	}
	if run.passed() != 3 {
		t.Errorf("passed = %d, want 3", run.passed())
	}
	if run.vars["id"] != "7" {
		t.Errorf("vars.id = %q, want 7", run.vars["id"])
	}
}

func TestScriptErrorIsReported(t *testing.T) {
	run := &scriptRun{vars: map[string]string{}}
	if err := runScript("this is not javascript(", run, nil); err == nil {
		t.Fatal("a broken script ran without an error")
	}
	// An empty script is fine and asserts nothing.
	if err := runScript("   \n", run, nil); err != nil || len(run.tests) != 0 {
		t.Errorf("empty script = %v, %+v", err, run.tests)
	}
}

func TestCancellingRunningScriptStopsItsLoop(t *testing.T) {
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	entered, done := make(chan struct{}), make(chan error, 1)
	go func() {
		done <- runScriptContext(ctx, `entered(); for (;;) {}`, &scriptRun{}, map[string]any{"entered": func() { close(entered) }})
	}()
	select {
	case <-entered:
	case <-time.After(3 * time.Second):
		t.Fatal("script did not start")
	}
	cancel()
	select {
	case err := <-done:
		if err == nil {
			t.Fatal("interrupted script reported success")
		}
	case <-time.After(time.Second):
		t.Fatal("script ignored cancellation")
	}
}
