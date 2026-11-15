package cli_test

import (
	"bytes"
	"encoding/json/v2"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/baba983/vscroxy/internal/cli"
	"github.com/baba983/vscroxy/internal/request"
	"github.com/baba983/vscroxy/internal/runner"
)

func TestRunContractGate(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, `{"name":"Rex"}`)
	}))
	defer upstream.Close()
	root := t.TempDir()
	input := filepath.Join(root, "collections")
	write(t, filepath.Join(input, "Shop", "Pet.request.json"), request.Request{
		ID: "pet", Name: "Pet", Collection: "Shop", Method: "GET", URL: "{{base}}/v1/pets/7",
	})
	good := `openapi: 3.0.3
info: {title: Pets, version: 1.0.0}
servers: [{url: https://api.example.com/v1}]
paths:
  /pets/{id}:
    get:
      parameters:
        - {name: id, in: path, required: true, schema: {type: integer}}
      responses:
        "200":
          description: ok
          content:
            application/json:
              schema:
                type: object
                required: [name]
                properties: {name: {type: string}}
`
	missing := strings.Replace(good, "required: [name]", "required: [age]", 1)
	missing = strings.Replace(missing, "properties: {name: {type: string}}", "properties: {age: {type: integer}}", 1)
	unmatched := `openapi: 3.0.3
info: {title: Pets, version: 1.0.0}
servers: [{url: https://api.example.com/v1}]
paths:
  /other:
    get:
      responses:
        "200": {description: ok}
`
	for name, tc := range map[string]struct {
		spec string
		code int
		want string
	}{
		"match":     {good, 0, "1 checked · 1 matched · 0 drifted"},
		"drift":     {missing, 1, "DRIFT Pet"},
		"unmatched": {unmatched, 1, "no operation in the contract serves this request"},
	} {
		t.Run(name, func(t *testing.T) {
			spec := filepath.Join(root, name+".yaml")
			if err := os.WriteFile(spec, []byte(tc.spec), 0o600); err != nil {
				t.Fatal(err)
			}
			reportPath := filepath.Join(root, name+".json")
			var out, errOut bytes.Buffer
			code := cli.Run(t.Context(), []string{"run", "--var", "base=" + upstream.URL, "--contract", spec, "--report-json", reportPath, input}, &out, &errOut)
			if code != tc.code || !strings.Contains(out.String(), tc.want) {
				t.Fatalf("exit=%d stdout=%s stderr=%s", code, &out, &errOut)
			}
			data, err := os.ReadFile(reportPath)
			if err != nil {
				t.Fatal(err)
			}
			var report runner.Report
			if err := json.Unmarshal(data, &report); err != nil {
				t.Fatal(err)
			}
			if report.Contract == nil || report.Contract.Checked != 1 || report.Contract.Failed != tc.code {
				t.Fatalf("contract report = %+v", report.Contract)
			}
		})
	}
}

func TestRunContractRejectsPathOutsideServerBase(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, `{"name":"Rex"}`)
	}))
	defer upstream.Close()
	root := t.TempDir()
	input := filepath.Join(root, "collections")
	write(t, filepath.Join(input, "Shop", "Pet.request.json"), request.Request{
		ID: "pet", Name: "Pet", Collection: "Shop", Method: "GET", URL: "{{base}}/outside",
	})
	spec := filepath.Join(root, "api.yaml")
	if err := os.WriteFile(spec, []byte(`openapi: 3.0.3
info: {title: Pets, version: 1.0.0}
servers: [{url: https://api.example.com/v1}]
paths:
  /pets/{id}:
    get:
      parameters:
        - {name: id, in: path, required: true, schema: {type: integer}}
      responses:
        "200": {description: ok}
`), 0o600); err != nil {
		t.Fatal(err)
	}
	var out, errOut bytes.Buffer
	code := cli.Run(t.Context(), []string{"run", "--var", "base=" + upstream.URL, "--contract", spec, input}, &out, &errOut)
	if code != 1 || !strings.Contains(out.String(), "no operation in the contract serves this request") {
		t.Fatalf("exit=%d stdout=%s stderr=%s", code, &out, &errOut)
	}
}
