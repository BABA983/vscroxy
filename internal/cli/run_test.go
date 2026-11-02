package cli_test

import (
	"bytes"
	"context"
	"encoding/json/v2"
	"encoding/xml"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/baba983/vscroxy/internal/cli"
	"github.com/baba983/vscroxy/internal/postman"
	"github.com/baba983/vscroxy/internal/request"
	"github.com/baba983/vscroxy/internal/runner"
)

func write(t *testing.T, path string, value any) []byte {
	t.Helper()
	data, err := json.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, data, 0o600); err != nil {
		t.Fatal(err)
	}
	return data
}

func TestRunNativeCollectionAndPostman(t *testing.T) {
	for _, format := range []string{"native", "postman"} {
		t.Run(format, func(t *testing.T) {
			// Native Checks carry an expected error status. Postman exports
			// checks as scripts, not that native HTTP-failure policy.
			status := 200
			if format == "native" {
				status = 409
			}
			var hits atomic.Int32
			upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				hits.Add(1)
				if r.Header.Get("Authorization") != "Bearer run-private-token" {
					http.Error(w, "missing inherited auth", 401)
					return
				}
				if r.URL.Path == "/login" {
					http.SetCookie(w, &http.Cookie{Name: "session", Value: "private-cookie", Path: "/"})
					fmt.Fprint(w, `{"id":37}`)
					return
				}
				cookie, err := r.Cookie("session")
				var body struct {
					Query     string `json:"query"`
					Variables struct {
						ID int64 `json:"id"`
					} `json:"variables"`
				}
				data, _ := io.ReadAll(r.Body)
				if err != nil || cookie.Value != "private-cookie" || json.Unmarshal(data, &body) != nil || body.Query != "query { item(id: 37) { id } }" || body.Variables.ID != 9007199254740993 || r.Header.Get("X-Row") != "row-value" {
					http.Error(w, "variables, cookie or GraphQL encoding changed", 400)
					return
				}
				w.WriteHeader(status)
				fmt.Fprint(w, `{"ok":true}`)
			}))
			defer upstream.Close()
			root := t.TempDir()
			requests := []request.Request{
				{ID: "login", Name: "Login", Collection: "Shop", Method: "GET", URL: "{{base}}/login", Auth: request.Auth{Type: request.AuthInherit}, Post: `pm.environment.set("id", pm.response.json().id); pm.globals.set("visited", "yes"); pm.collectionVariables.set("ready", "true");`},
				{ID: "query", Name: "Query", Collection: "Shop/Items", Method: "POST", URL: "{{base}}/graphql?secret={{token}}", Auth: request.Auth{Type: request.AuthInherit}, BodyMode: "GraphQL", GraphQL: request.GraphQLBody{Query: "query { item(id: {{id}}) { id } }", Variables: `{"id":9007199254740993}`}, Headers: "X-Row: {{row}}", Checks: request.Checks{Status: "409", JSON: "/ok = true"}, Pre: `pm.expect(pm.globals.get("visited")).to.equal("yes"); pm.expect(pm.collectionVariables.get("ready")).to.equal("true")`, Post: `console.log(pm.environment.get("token")); pm.test(pm.environment.get("token"), () => pm.expect(true).to.be.true); pm.execution.setNextRequest(null);`},
			}
			requests[1].Checks.Status = fmt.Sprint(status)
			folders := []request.Folder{{Path: "Shop", Auth: request.Auth{Type: "Bearer", Token: "{{token}}"}}}
			input := filepath.Join(root, "collections")
			inputs := map[string][]byte{}
			if format == "native" {
				inputs[filepath.Join(input, "Shop", "folder.json")] = write(t, filepath.Join(input, "Shop", "folder.json"), folders[0])
				for _, r := range requests {
					path := filepath.Join(input, filepath.FromSlash(r.Collection), r.Name+".request.json")
					inputs[path] = write(t, path, r)
				}
			} else {
				data, _, err := postman.WriteCollection("Shop", requests, folders)
				if err != nil {
					t.Fatal(err)
				}
				input = filepath.Join(root, "collection.json")
				if err := os.WriteFile(input, data, 0o600); err != nil {
					t.Fatal(err)
				}
				inputs[input] = data
			}
			env := filepath.Join(root, "env.json")
			inputs[env] = write(t, env, map[string]any{"active": "local", "environments": []any{map[string]any{"name": "local", "vars": map[string]string{"base": "http://wrong.invalid", "token": "shared"}, "current": map[string]string{"token": "run-private-token"}}}})
			data := filepath.Join(root, "rows.json")
			inputs[data] = write(t, data, []map[string]string{{"row": "row-value"}, {"row": "must-not-run"}})
			reportPath, junitPath := filepath.Join(root, "reports", "run.json"), filepath.Join(root, "reports", "run.xml")
			var out, errOut bytes.Buffer
			code := cli.Run(t.Context(), []string{"run", "--env", env, "--var", "base=" + upstream.URL, "--data", data, "--folder", "Shop", "--report-json", reportPath, "--report-junit", junitPath, input}, &out, &errOut)
			if code != 0 || hits.Load() != 2 || !strings.Contains(out.String(), "2 completed · 0 failed") {
				t.Fatalf("exit=%d hits=%d stdout=%s stderr=%s", code, hits.Load(), &out, &errOut)
			}
			jsonData, err := os.ReadFile(reportPath)
			if err != nil {
				t.Fatal(err)
			}
			var report runner.Report
			if err := json.Unmarshal(jsonData, &report); err != nil || report.Completed != 2 || report.Failed != 0 || !report.Stopped || report.Incomplete || report.Results[1].Status != status {
				t.Fatalf("report: %+v %v", report, err)
			}
			xmlData, _ := os.ReadFile(junitPath)
			var suite struct {
				Tests int `xml:"tests,attr"`
			}
			if xml.Unmarshal(xmlData, &suite) != nil || suite.Tests != 2 {
				t.Fatalf("JUnit: %s", xmlData)
			}
			for _, output := range []string{out.String(), errOut.String(), string(jsonData), string(xmlData)} {
				for _, secret := range []string{"run-private-token", "private-cookie", "9007199254740993"} {
					if strings.Contains(output, secret) {
						t.Errorf("default output leaked %q", secret)
					}
				}
			}
			for path, before := range inputs {
				after, _ := os.ReadFile(path)
				if !bytes.Equal(before, after) {
					t.Fatalf("input modified: %s", path)
				}
			}
		})
	}
}

func TestRunFailureBudgetAndSelection(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { fmt.Fprint(w, "ok") }))
	defer upstream.Close()
	for _, tc := range []struct {
		name, script string
		flags        []string
		code, count  int
		incomplete   bool
	}{
		{"failure continues", `pm.test("failure",()=>pm.expect(false).to.be.true)`, nil, 1, 2, false},
		{"bail", `pm.test("failure",()=>pm.expect(false).to.be.true)`, []string{"--bail"}, 1, 1, false},
		{"loop budget", `pm.execution.setNextRequest("First")`, []string{"--max-steps", "3"}, 2, 3, true},
		{"exact budget", "", []string{"--max-steps", "2"}, 0, 2, false},
		{"unknown jump", `pm.execution.setNextRequest("does-not-exist")`, nil, 2, 1, true},
		{"skip", `pm.execution.skipRequest()`, nil, 0, 2, false},
		{"empty selection", "", []string{"--folder", "missing"}, 2, 0, false},
		{"zero iterations", "", []string{"--iterations", "0"}, 2, 0, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			root := t.TempDir()
			input := filepath.Join(root, "collections")
			r := request.Request{Name: "First", Method: "GET", URL: upstream.URL, Pre: tc.script}
			write(t, filepath.Join(input, "first.request.json"), r)
			write(t, filepath.Join(input, "last.request.json"), request.Request{Name: "Last", Method: "GET", URL: upstream.URL})
			output := filepath.Join(root, "run.json")
			args := append([]string{"run", "--report-json", output}, tc.flags...)
			var stdout, stderr bytes.Buffer
			code := cli.Run(t.Context(), append(args, input), &stdout, &stderr)
			if code != tc.code {
				t.Fatalf("exit=%d want=%d stdout=%s stderr=%s", code, tc.code, &stdout, &stderr)
			}
			if tc.count > 0 {
				data, _ := os.ReadFile(output)
				var report runner.Report
				if err := json.Unmarshal(data, &report); err != nil || report.Completed != tc.count || report.Incomplete != tc.incomplete {
					t.Fatalf("report=%s, err=%v", data, err)
				}
			}
		})
	}
}

func TestRunCancellationAndDeadline(t *testing.T) {
	for _, deadline := range []bool{false, true} {
		t.Run(fmt.Sprint(deadline), func(t *testing.T) {
			ctx, cancel := context.WithCancel(t.Context())
			defer cancel()
			if deadline {
				ctx, cancel = context.WithDeadline(t.Context(), time.Time{})
				defer cancel()
			}
			upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if !deadline {
					cancel()
				}
				<-r.Context().Done()
			}))
			defer upstream.Close()
			root := t.TempDir()
			input, output := filepath.Join(root, "collections"), filepath.Join(root, "run.json")
			write(t, filepath.Join(input, "one.request.json"), request.Request{Name: "Wait", Method: "GET", URL: upstream.URL})
			args := []string{"run", "--report-json", output}
			want, attempts := 130, 1
			if deadline {
				want, attempts = 124, 0
			}
			var stdout, stderr bytes.Buffer
			code := cli.Run(ctx, append(args, input), &stdout, &stderr)
			if code != want {
				t.Fatalf("exit=%d want=%d stdout=%s stderr=%s", code, want, &stdout, &stderr)
			}
			data, _ := os.ReadFile(output)
			var report runner.Report
			if json.Unmarshal(data, &report) != nil || !report.Incomplete || report.Completed != attempts || report.Failed != attempts {
				t.Fatalf("cancelled report=%s", data)
			}
		})
	}
}

func TestRunDoesNotOverwriteInputs(t *testing.T) {
	root := t.TempDir()
	input := filepath.Join(root, "collections")
	path := filepath.Join(input, "one.request.json")
	before := write(t, path, request.Request{Name: "Never send", Method: "GET", URL: "http://127.0.0.1:1"})
	alias := filepath.Join(root, "alias")
	if err := os.Symlink(input, alias); err != nil {
		t.Fatal(err)
	}
	for _, destination := range []string{path, filepath.Join(alias, "one.request.json"), filepath.Join(alias, "new", "report.json")} {
		var out bytes.Buffer
		if code := cli.Run(t.Context(), []string{"run", "--report-json", destination, input}, &out, &out); code != 2 || !strings.Contains(out.String(), "overlaps an input") {
			t.Fatalf("destination %s: exit=%d %s", destination, code, &out)
		}
	}
	after, _ := os.ReadFile(path)
	if !slices.Equal(before, after) {
		t.Fatal("input overwritten")
	}
}
