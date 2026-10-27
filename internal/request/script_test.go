package request

import (
	"context"
	"net/http"
	"testing"
	"time"
)

func TestPreRequestScriptChangesTheRequest(t *testing.T) {
	req := &Prepared{
		Method: "GET",
		URL:    "http://127.0.0.1:8099/page.html",
		Header: http.Header{},
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
		Header: http.Header{"Content-Type": {"application/json"}},
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
