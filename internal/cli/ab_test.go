package cli_test

import (
	"bytes"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
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
