package cli_test

import (
	"bytes"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/baba983/vscroxy/internal/cli"
	"github.com/baba983/vscroxy/internal/request"
)

func abEnvironmentFile(t *testing.T, path, base, variant string) {
	t.Helper()
	write(t, path, map[string]any{
		"active": "local",
		"environments": []any{map[string]any{
			"name": "local", "vars": map[string]string{"base": base, "variant": variant},
		}},
	})
}

func TestABReplayCollectionReportsStructuralDifferences(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if r.Header.Get("X-Variant") == "b" {
			fmt.Fprint(w, `{"id":"1","name":"B","extra":true}`)
			return
		}
		fmt.Fprint(w, `{"id":1,"name":"A"}`)
	}))
	defer upstream.Close()
	root := t.TempDir()
	input := filepath.Join(root, "collections")
	write(t, filepath.Join(input, "Shop", "Item.request.json"), request.Request{
		ID: "item", Name: "Item", Collection: "Shop", Method: "GET", URL: "{{base}}/items/1",
		Headers: "X-Variant: {{variant}}",
	})
	envA, envB := filepath.Join(root, "a.json"), filepath.Join(root, "b.json")
	abEnvironmentFile(t, envA, upstream.URL, "a")
	abEnvironmentFile(t, envB, upstream.URL, "b")
	jsonPath, junitPath := filepath.Join(root, "ab.json"), filepath.Join(root, "ab.xml")
	var out, errOut bytes.Buffer
	code := cli.Run(t.Context(), []string{"ab", "--a", envA, "--b", envB, "--report-json", jsonPath, "--report-junit", junitPath, input}, &out, &errOut)
	if code != 1 {
		t.Fatalf("exit=%d stdout=%s stderr=%s", code, &out, &errOut)
	}
	data, err := os.ReadFile(jsonPath)
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{`"different":1`, "/id: type number vs string", "/extra: only on B"} {
		if !strings.Contains(string(data), want) {
			t.Errorf("report lacks %q: %s", want, data)
		}
	}
	junit, err := os.ReadFile(junitPath)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(junit), `<failure message=`) || !strings.Contains(string(junit), `tests="1"`) {
		t.Errorf("JUnit = %s", junit)
	}

	abEnvironmentFile(t, envB, upstream.URL, "a")
	code = cli.Run(t.Context(), []string{"ab", "--a", envA, "--b", envB, "--report-json", filepath.Join(root, "same.json"), input}, &out, &errOut)
	if code != 0 {
		t.Fatalf("same shapes exit=%d stdout=%s stderr=%s", code, &out, &errOut)
	}
}

func TestABReplaySessionRewritesBases(t *testing.T) {
	shape := func(body string) *httptest.Server {
		return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Content-Type", "application/json")
			fmt.Fprint(w, body)
		}))
	}
	a, b := shape(`{"id":1}`), shape(`{"id":"1"}`)
	defer a.Close()
	defer b.Close()
	root := t.TempDir()
	session := filepath.Join(root, "session.json")
	if err := os.WriteFile(session, []byte(`{"version":1,"title":"saved","flows":[{"id":1,"method":"GET","url":"http://prod.example/items/1","reqHeaders":"Accept: application/json","status":200}]}`), 0o600); err != nil {
		t.Fatal(err)
	}
	report := filepath.Join(root, "ab.json")
	var out, errOut bytes.Buffer
	code := cli.Run(t.Context(), []string{"ab", "--base-a", a.URL, "--base-b", b.URL, "--report-json", report, session}, &out, &errOut)
	if code != 1 {
		t.Fatalf("exit=%d stdout=%s stderr=%s", code, &out, &errOut)
	}
	data, err := os.ReadFile(report)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(data), "http://prod.example/items/1") || !strings.Contains(string(data), "/id: type number vs string") {
		t.Errorf("report = %s", data)
	}
}

func TestABSessionSafetyDefaults(t *testing.T) {
	type seen struct {
		method, path, authorization, cookie, apiKey string
	}
	var mu sync.Mutex
	var requests []seen
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		requests = append(requests, seen{r.Method, r.URL.Path, r.Header.Get("Authorization"), r.Header.Get("Cookie"), r.Header.Get("X-Api-Key")})
		mu.Unlock()
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, `{"ok":true}`)
	}))
	defer upstream.Close()
	root := t.TempDir()
	session := filepath.Join(root, "session.json")
	if err := os.WriteFile(session, []byte(`{"version":1,"flows":[
		{"id":1,"method":"GET","url":"http://prod.example/safe","reqHeaders":"Authorization: Bearer captured-secret\nCookie: session=captured-cookie\nX-Api-Key: captured-key","status":200},
		{"id":2,"method":"POST","url":"http://prod.example/danger","reqHeaders":"Content-Type: application/json","reqBody":"{\"x\":1}","status":200}
	]}`), 0o600); err != nil {
		t.Fatal(err)
	}
	report := filepath.Join(root, "report.json")
	var out, errOut bytes.Buffer
	code := cli.Run(t.Context(), []string{"ab", "--base-a", upstream.URL, "--base-b", upstream.URL, "--report-json", report, session}, &out, &errOut)
	if code != 0 {
		t.Fatalf("exit=%d stdout=%s stderr=%s", code, &out, &errOut)
	}
	mu.Lock()
	got := append([]seen(nil), requests...)
	mu.Unlock()
	if len(got) != 2 || got[0].method != "GET" || got[1].method != "GET" {
		t.Fatalf("requests = %+v", got)
	}
	for _, request := range got {
		if request.authorization != "" || request.cookie != "" || request.apiKey != "" {
			t.Fatalf("credential leaked: %+v", request)
		}
	}
	data, err := os.ReadFile(report)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(data), `"skipped"`) || !strings.Contains(string(data), "POST") || !strings.Contains(string(data), "/danger") {
		t.Fatalf("report = %s", data)
	}

	requests = nil
	out.Reset()
	errOut.Reset()
	code = cli.Run(t.Context(), []string{"ab", "--base-a", upstream.URL, "--base-b", upstream.URL, "--include-unsafe", "--keep-credentials", "--report-json", report, session}, &out, &errOut)
	if code != 0 || !strings.Contains(errOut.String(), "Warning: --include-unsafe") {
		t.Fatalf("exit=%d stderr=%s", code, &errOut)
	}
	mu.Lock()
	got = append([]seen(nil), requests...)
	mu.Unlock()
	if len(got) != 4 {
		t.Fatalf("opt-in requests = %+v", got)
	}
	var posts int
	for _, request := range got {
		if request.method == "POST" {
			posts++
		}
		if request.method == "GET" && (request.authorization != "Bearer captured-secret" || request.cookie != "session=captured-cookie") {
			t.Fatalf("captured credentials missing: %+v", request)
		}
	}
	if posts != 2 {
		t.Fatalf("POSTs = %d", posts)
	}

	requests = nil
	out.Reset()
	errOut.Reset()
	code = cli.Run(t.Context(), []string{"ab", "--base-a", upstream.URL, "--base-b", upstream.URL, "--header-a", "Authorization: Bearer A", "--header-b", "Authorization: Bearer B", "--report-json", report, session}, &out, &errOut)
	if code != 0 {
		t.Fatalf("exit=%d stdout=%s stderr=%s", code, &out, &errOut)
	}
	mu.Lock()
	got = append([]seen(nil), requests...)
	mu.Unlock()
	if len(got) != 2 || got[0].authorization != "Bearer A" || got[1].authorization != "Bearer B" {
		t.Fatalf("supplied headers = %+v", got)
	}
}

func TestABSessionOnlyUnsafeMethodsReportsSkipped(t *testing.T) {
	root := t.TempDir()
	session := filepath.Join(root, "post.json")
	if err := os.WriteFile(session, []byte(`{"version":1,"flows":[{"id":1,"method":"POST","url":"http://prod.example/orders","reqHeaders":"Content-Type: application/json","reqBody":"{}"}]}`), 0o600); err != nil {
		t.Fatal(err)
	}
	report := filepath.Join(root, "report.json")
	var out, errOut bytes.Buffer
	code := cli.Run(t.Context(), []string{"ab", "--base-a", "http://127.0.0.1:1", "--base-b", "http://127.0.0.1:1", "--report-json", report, session}, &out, &errOut)
	if code != 0 || !strings.Contains(out.String(), "SKIP POST /orders") {
		t.Fatalf("exit=%d stdout=%s stderr=%s", code, &out, &errOut)
	}
	data, err := os.ReadFile(report)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(data), `"skipped"`) || !strings.Contains(string(data), "/orders") {
		t.Fatalf("report = %s", data)
	}
}
