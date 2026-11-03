package cli_test

import (
	"bytes"
	"context"
	"encoding/json/v2"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/baba983/vscroxy/internal/cli"
	"github.com/baba983/vscroxy/internal/postman"
	"github.com/baba983/vscroxy/internal/request"
	"github.com/baba983/vscroxy/internal/runner"
)

func readLoadReport(t *testing.T, path string) (runner.LoadReport, []byte) {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var report runner.LoadReport
	if err := json.Unmarshal(data, &report, json.RejectUnknownMembers(true)); err != nil || !report.Done || report.Version != 1 {
		t.Fatalf("invalid final report: %s (%v)", data, err)
	}
	return report, data
}

func TestLoadNativeAndPostmanPrivateUserState(t *testing.T) {
	for _, format := range []string{"native", "postman"} {
		t.Run(format, func(t *testing.T) {
			root := t.TempDir()
			home, temp := filepath.Join(root, "home"), filepath.Join(root, "temp")
			if err := os.MkdirAll(temp, 0o700); err != nil {
				t.Fatal(err)
			}
			t.Setenv("HOME", home)
			t.Setenv("USERPROFILE", home)
			t.Setenv("TMPDIR", temp)
			t.Setenv("TMP", temp)
			t.Setenv("TEMP", temp)
			profile := filepath.Join(home, ".vscroxy", "env.json")
			beforeProfile := write(t, profile, map[string]string{"must": "remain private and untouched"})
			var hits atomic.Int64
			var mu sync.Mutex
			users := map[string]int{}
			upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				hits.Add(1)
				user := r.Header.Get("X-User")
				if user == "" || r.Header.Get("Authorization") != "Bearer load-private-token" {
					http.Error(w, "missing private state", 401)
					return
				}
				mu.Lock()
				users[user]++
				mu.Unlock()
				if r.URL.Path == "/login" {
					http.SetCookie(w, &http.Cookie{Name: "session", Value: user, Path: "/"})
				} else if cookie, err := r.Cookie("session"); err != nil || cookie.Value != user {
					http.Error(w, "shared cookie jar", 400)
					return
				}
				fmt.Fprint(w, `{"private":"response-private-value"}`)
			}))
			defer upstream.Close()
			requests := []request.Request{
				{ID: "login", Name: "secret-request-name", Collection: "Shop", Method: "GET", URL: "{{base}}/login?token={{token}}", Auth: request.Auth{Type: request.AuthInherit}, Headers: "X-User: {{user}}", Pre: `
pm.expect(Number(pm.environment.get("counter") || 0)).to.equal(pm.info.iteration);
if (pm.info.iteration === 0) {
  pm.expect(pm.globals.get("visited")).to.equal(undefined);
  pm.expect(pm.collectionVariables.get("visited")).to.equal(undefined);
  pm.variables.set("user", pm.variables.replaceIn("{{$guid}}"));
}
pm.environment.set("counter", String(pm.info.iteration + 1));
pm.globals.set("visited", pm.variables.get("user"));
pm.collectionVariables.set("visited", pm.variables.get("user"));
`, Post: `console.log("console-private-value", pm.response.text());`},
				{ID: "check", Name: "secret-other-name", Collection: "Shop/Items", Method: "GET", URL: "{{base}}/check", Auth: request.Auth{Type: request.AuthInherit}, Headers: "X-User: {{user}}", Pre: `
pm.expect(pm.globals.get("visited")).to.equal(pm.variables.get("user"));
pm.expect(pm.collectionVariables.get("visited")).to.equal(pm.variables.get("user"));
`, Post: `pm.test("assertion-private-value", () => pm.expect(pm.response.code).to.equal(200)); if (pm.info.iteration === 1) pm.execution.setNextRequest(null);`},
			}
			folder := request.Folder{Path: "Shop", Auth: request.Auth{Type: "Bearer", Token: "{{token}}"}}
			input := filepath.Join(root, "collections")
			inputs := map[string][]byte{profile: beforeProfile}
			if format == "native" {
				path := filepath.Join(input, "Shop", "folder.json")
				inputs[path] = write(t, path, folder)
				for _, r := range requests {
					path := filepath.Join(input, filepath.FromSlash(r.Collection), r.ID+".request.json")
					inputs[path] = write(t, path, r)
				}
				// Selection must exclude another collection, even with the same
				// prefix. Its network destination must never be contacted.
				path = filepath.Join(input, "Shopping", "excluded.request.json")
				inputs[path] = write(t, path, request.Request{ID: "excluded", Name: "Excluded", Method: "GET", URL: upstream.URL + "/excluded"})
			} else {
				data, _, err := postman.WriteCollection("Shop", requests, []request.Folder{folder})
				if err != nil {
					t.Fatal(err)
				}
				input = filepath.Join(root, "postman.json")
				if err := os.WriteFile(input, data, 0o600); err != nil {
					t.Fatal(err)
				}
				inputs[input] = data
			}
			env := filepath.Join(root, "env.json")
			inputs[env] = write(t, env, map[string]any{"active": "wrong", "environments": []any{map[string]any{"name": "local", "vars": map[string]string{"base": "http://wrong.invalid", "token": "wrong"}, "current": map[string]string{"token": "load-private-token"}}}})
			reportPath := filepath.Join(root, "reports", "load.json")
			resolver := net.DefaultResolver
			// Run twice: per-run state must reset as well as per-user state.
			for range 2 {
				var out, errOut bytes.Buffer
				code := cli.Run(t.Context(), []string{"load", "--users", "4", "--duration", "10s", "--folder", "Shop", "--env", env, "--environment", "local", "--var", "base=http://wrong.invalid", "--var", "base=" + upstream.URL, "--report-json", reportPath, input}, &out, &errOut)
				if code != 0 || errOut.Len() != 0 {
					t.Fatalf("exit=%d stdout=%s stderr=%s", code, &out, &errOut)
				}
				report, data := readLoadReport(t, reportPath)
				if report.Reason != "finished" || report.Users != 4 || report.Total.Completed != 16 || report.Total.Failed != 0 || report.Total.Skipped != 0 || report.Total.Interrupted != 0 || len(report.Requests) != 2 || report.Total.LatencySamples != 16 || report.Total.RequestsPerSec <= 0 || report.Total.P99Millis < report.Total.P50Millis || report.Total.ResponseBytes == 0 {
					t.Fatalf("report=%s", data)
				}
				for _, want := range []string{"16 completed", "0 failed", "0 skipped", "0 interrupted", "completed/s", "P50", "P95", "P99", "scripts/auth/network/checks", "excludes skips"} {
					if !strings.Contains(out.String(), want) {
						t.Errorf("output missing %q: %s", want, &out)
					}
				}
				for _, secret := range []string{"load-private-token", "response-private-value", "console-private-value", "assertion-private-value", "secret-request-name", "secret-other-name", upstream.URL} {
					if strings.Contains(out.String()+errOut.String()+string(data), secret) {
						t.Errorf("output leaked %q", secret)
					}
				}
				if net.DefaultResolver != resolver {
					t.Fatal("load left process DNS resolver changed")
				}
				if entries, err := os.ReadDir(temp); err != nil || len(entries) != 0 {
					t.Fatalf("temporary runtime/body leak: %v %v", entries, err)
				}
			}
			if hits.Load() != 32 {
				t.Fatalf("hits=%d", hits.Load())
			}
			mu.Lock()
			defer mu.Unlock()
			if len(users) != 8 {
				t.Fatalf("users were shared across runs: %v", users)
			}
			for _, count := range users {
				if count != 4 {
					t.Fatalf("per-user requests=%d", count)
				}
			}
			for path, before := range inputs {
				if after, err := os.ReadFile(path); err != nil || !bytes.Equal(before, after) {
					t.Fatalf("input/profile changed: %s (%v)", path, err)
				}
			}
			info, err := os.Stat(reportPath)
			if err != nil || runtime.GOOS != "windows" && info.Mode().Perm() != 0o600 {
				t.Fatalf("report not private: %v %v", info, err)
			}
			info, err = os.Stat(filepath.Dir(reportPath))
			if err != nil || runtime.GOOS != "windows" && info.Mode().Perm() != 0o700 {
				t.Fatalf("report directory not private: %v %v", info, err)
			}
		})
	}
}

func TestLoadOutcomes(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { fmt.Fprint(w, "ok") }))
	defer upstream.Close()
	for _, tc := range []struct {
		name, pre, post, reason  string
		flags                    []string
		code                     int
		completed, failed, skips int64
	}{
		{name: "explicit stop defaults", post: `pm.execution.setNextRequest(null)`, reason: "finished", completed: 1},
		{name: "failure", post: `pm.test("private-failure", () => pm.expect(false).to.be.true); pm.execution.setNextRequest(null)`, reason: "finished", code: 1, completed: 1, failed: 1},
		{name: "bail", post: `pm.test("private-failure", () => pm.expect(false).to.be.true)`, flags: []string{"--bail"}, reason: "failure", code: 1, completed: 1, failed: 1},
		{name: "default no bail", post: `pm.test("private-failure", () => pm.expect(false).to.be.true)`, flags: []string{"--max-attempts", "3"}, reason: "attempt-limit", code: 2, completed: 3, failed: 3},
		{name: "budget", flags: []string{"--max-attempts", "3"}, reason: "attempt-limit", code: 2, completed: 3},
		{name: "unknown branch", post: `pm.execution.setNextRequest("private-missing-branch")`, reason: "execution-error", code: 2, completed: 1},
		{name: "skip budget", pre: `pm.execution.skipRequest()`, flags: []string{"--max-attempts", "3"}, reason: "attempt-limit", code: 2, skips: 3},
		{name: "max bounds", flags: []string{"--users", "64", "--duration", "10m", "--request-timeout", "1m", "--max-attempts", "1000000"}, post: `pm.execution.setNextRequest(null)`, reason: "finished", completed: 64},
	} {
		t.Run(tc.name, func(t *testing.T) {
			root := t.TempDir()
			input, output := filepath.Join(root, "collections"), filepath.Join(root, "load.json")
			write(t, filepath.Join(input, "one.request.json"), request.Request{ID: "one", Name: "private-name", Method: "GET", URL: upstream.URL, Pre: tc.pre, Post: tc.post})
			args := append([]string{"load", "--report-json", output}, tc.flags...)
			var out, errOut bytes.Buffer
			if code := cli.Run(t.Context(), append(args, input), &out, &errOut); code != tc.code {
				t.Fatalf("exit=%d want=%d stdout=%s stderr=%s", code, tc.code, &out, &errOut)
			}
			report, data := readLoadReport(t, output)
			if report.Reason != tc.reason || report.Total.Completed != tc.completed || report.Total.Failed != tc.failed || report.Total.Skipped != tc.skips || report.Total.Attempts != tc.completed+tc.skips || report.Total.LatencySamples != tc.completed {
				t.Fatalf("report=%s", data)
			}
			if strings.Contains(out.String()+errOut.String()+string(data), "private-") {
				t.Fatalf("raw details leaked: %s %s %s", &out, &errOut, data)
			}
		})
	}
}

func TestLoadDurationAndRequestTimeout(t *testing.T) {
	for _, requestTimeout := range []bool{false, true} {
		t.Run(fmt.Sprint(requestTimeout), func(t *testing.T) {
			upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if requestTimeout {
					<-r.Context().Done()
					return
				}
				select {
				case <-r.Context().Done():
				case <-time.After(20 * time.Millisecond):
					fmt.Fprint(w, "ok")
				}
			}))
			defer upstream.Close()
			root := t.TempDir()
			input, output := filepath.Join(root, "collections"), filepath.Join(root, "load.json")
			write(t, filepath.Join(input, "one.request.json"), request.Request{ID: "one", Name: "One", Method: "GET", URL: upstream.URL})
			args := []string{"load", "--duration", "1s", "--report-json", output}
			if requestTimeout {
				args = append(args, "--request-timeout", "20ms", "--bail")
			}
			var out, errOut bytes.Buffer
			code := cli.Run(t.Context(), append(args, input), &out, &errOut)
			report, data := readLoadReport(t, output)
			if requestTimeout {
				if code != 1 || report.Reason != "failure" || report.Total.Failed != 1 || report.Total.Interrupted != 0 || report.Total.Errors != 1 {
					t.Fatalf("request timeout: exit=%d report=%s", code, data)
				}
			} else if code != 0 || report.Reason != "duration" || report.Total.Completed < 1 || report.Total.Failed != 0 || report.Total.Attempts != report.Total.Completed+report.Total.Interrupted || report.DurationMillis < 1000 {
				t.Fatalf("normal duration: exit=%d report=%s", code, data)
			}
		})
	}
}

func TestLoadCancellationAndParentDeadline(t *testing.T) {
	for _, mode := range []string{"cancel", "deadline", "already-cancelled", "already-expired"} {
		t.Run(mode, func(t *testing.T) {
			ctx, cancel := context.WithCancel(t.Context())
			defer cancel()
			var expire context.CancelCauseFunc
			if mode == "deadline" {
				ctx, expire = context.WithCancelCause(t.Context())
				defer expire(nil)
			}
			if mode == "already-expired" {
				ctx, cancel = context.WithDeadline(t.Context(), time.Time{})
				defer cancel()
			}
			if mode == "already-cancelled" {
				cancel()
			}
			upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Path == "/done" {
					fmt.Fprint(w, "ok")
					return
				}
				if mode == "cancel" {
					cancel()
				}
				if mode == "deadline" {
					// Delegate a real parent timeout only once a request is in
					// flight. CA generation/setup on a busy machine must not
					// consume the deadline before partial metrics exist.
					deadlineCtx, stop := context.WithTimeout(t.Context(), 20*time.Millisecond)
					defer stop()
					<-deadlineCtx.Done()
					expire(deadlineCtx.Err())
				}
				<-r.Context().Done()
			}))
			defer upstream.Close()
			root := t.TempDir()
			input, output := filepath.Join(root, "collections"), filepath.Join(root, "load.json")
			write(t, filepath.Join(input, "a.request.json"), request.Request{ID: "a", Name: "Done", Method: "GET", URL: upstream.URL + "/done"})
			write(t, filepath.Join(input, "b.request.json"), request.Request{ID: "b", Name: "Wait", Method: "GET", URL: upstream.URL + "/wait"})
			var out, errOut bytes.Buffer
			code := cli.Run(ctx, []string{"load", "--report-json", output, input}, &out, &errOut)
			wantCode, reason := 130, "cancelled"
			if strings.Contains(mode, "deadline") || mode == "already-expired" {
				wantCode, reason = 124, "timed-out"
			}
			report, data := readLoadReport(t, output)
			if code != wantCode || report.Reason != reason || report.Total.Failed != 0 {
				t.Fatalf("exit=%d want=%d report=%s stdout=%s stderr=%s", code, wantCode, data, &out, &errOut)
			}
			if strings.HasPrefix(mode, "already-") {
				if report.Total.Attempts != 0 {
					t.Fatalf("already cancelled made attempts: %s", data)
				}
			} else if report.Total.Completed != 1 || report.Total.Interrupted != 1 || report.Total.Attempts != 2 {
				t.Fatalf("partial metrics lost: %s", data)
			}
		})
	}
}

func TestLoadRejectsInvalidFlagsAndInputs(t *testing.T) {
	root := t.TempDir()
	input := filepath.Join(root, "collections")
	write(t, filepath.Join(input, "one.request.json"), request.Request{ID: "one", Name: "Never send", Method: "GET", URL: "http://127.0.0.1:1"})
	for _, flags := range [][]string{
		{"--users", "0"}, {"--users", "65"}, {"--users", "-1"},
		{"--duration", "0"}, {"--duration", "999ms"}, {"--duration", "601s"},
		{"--request-timeout", "0"}, {"--request-timeout", "-1s"}, {"--request-timeout", "61s"},
		{"--max-attempts", "0"}, {"--max-attempts", "-1"}, {"--max-attempts", "1000001"},
		{"--data", "anything"}, {"--report-junit", "anything"}, {"--include-details"},
		{"--iterations", "1"}, {"--timeout", "1s"}, {"--var", "missing-equals"}, {"--var", " =value"},
		{"--environment", "local"}, {"--env", filepath.Join(root, "missing")}, {"--folder", "missing"},
	} {
		t.Run(strings.Join(flags, " "), func(t *testing.T) {
			output := filepath.Join(t.TempDir(), "report.json")
			args := append([]string{"load", "--report-json", output}, flags...)
			var out, errOut bytes.Buffer
			if code := cli.Run(t.Context(), append(args, input), &out, &errOut); code != 2 || out.Len() != 0 {
				t.Fatalf("exit=%d stdout=%s stderr=%s", code, &out, &errOut)
			}
			if _, err := os.Stat(output); !errors.Is(err, os.ErrNotExist) {
				t.Fatalf("setup error wrote a report: %v", err)
			}
		})
	}
	for _, args := range [][]string{{"load"}, {"load", input, input}, {"load", filepath.Join(root, "missing")}, {"load", "--users", "not-an-int", input}} {
		if code := cli.Run(t.Context(), args, io.Discard, io.Discard); code != 2 {
			t.Fatalf("%v: exit=%d", args, code)
		}
	}
	for _, args := range [][]string{{"--help"}, {"load", "--help"}} {
		var out bytes.Buffer
		if code := cli.Run(t.Context(), args, &out, &out); code != 0 || !strings.Contains(out.String(), "vscroxy-cli load") {
			t.Fatalf("help: exit=%d output=%s", code, &out)
		}
	}
	for _, bad := range []string{"ws", "wss", "too-many", "malformed"} {
		t.Run(bad, func(t *testing.T) {
			input := filepath.Join(t.TempDir(), "collections")
			r := request.Request{ID: "one", Name: "Never send", Method: "GET", URL: bad + "://127.0.0.1:1"}
			if bad == "too-many" {
				for i := range 201 {
					r.ID = fmt.Sprint(i)
					write(t, filepath.Join(input, r.ID+".request.json"), r)
				}
			} else {
				path := filepath.Join(input, "one.request.json")
				write(t, path, r)
				if bad == "malformed" {
					if err := os.WriteFile(path, []byte(`{"private-input":`), 0o600); err != nil {
						t.Fatal(err)
					}
				}
			}
			var out bytes.Buffer
			if code := cli.Run(t.Context(), []string{"load", input}, &out, &out); code != 2 || strings.Contains(out.String(), "private-input") {
				t.Fatalf("exit=%d output=%s", code, &out)
			}
		})
	}
}

func TestLoadReportCannotAliasInputs(t *testing.T) {
	root := t.TempDir()
	input := filepath.Join(root, "collections")
	requestPath := filepath.Join(input, "Shop", "one.request.json")
	folderPath := filepath.Join(input, "Shop", "folder.json")
	env := filepath.Join(root, "env.json")
	postmanPath := filepath.Join(root, "postman.json")
	r := request.Request{ID: "one", Name: "Never send", Collection: "Shop", Method: "GET", URL: "http://127.0.0.1:1"}
	original := map[string][]byte{
		requestPath: write(t, requestPath, r),
		folderPath:  write(t, folderPath, request.Folder{Path: "Shop"}),
		env:         write(t, env, map[string]any{"globals": map[string]string{"private": "secret"}}),
	}
	data, _, err := postman.WriteCollection("Shop", []request.Request{r}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(postmanPath, data, 0o600); err != nil {
		t.Fatal(err)
	}
	original[postmanPath] = data
	for _, source := range []string{requestPath, folderPath, env, postmanPath} {
		for _, kind := range []string{"direct", "symlink", "hardlink", "directory-symlink"} {
			t.Run(filepath.Base(source)+"/"+kind, func(t *testing.T) {
				destination := source
				if kind != "direct" {
					destination = filepath.Join(t.TempDir(), "alias")
					switch kind {
					case "symlink":
						err = os.Symlink(source, destination)
					case "hardlink":
						err = os.Link(source, destination)
					case "directory-symlink":
						err = os.Symlink(filepath.Dir(source), destination)
						destination = filepath.Join(destination, filepath.Base(source))
					}
					if err != nil {
						t.Fatal(err)
					}
				}
				collection := input
				if source == postmanPath {
					collection = postmanPath
				}
				var out bytes.Buffer
				code := cli.Run(t.Context(), []string{"load", "--env", env, "--report-json", destination, collection}, &out, &out)
				if code != 2 || !strings.Contains(out.String(), "overlaps an input") {
					t.Fatalf("exit=%d output=%s", code, &out)
				}
				if got, err := os.ReadFile(destination); err != nil || !bytes.Equal(got, original[source]) {
					t.Fatalf("alias changed: %q %v", got, err)
				}
			})
		}
	}
	alias := filepath.Join(root, "directory-alias")
	if err := os.Symlink(input, alias); err != nil {
		t.Fatal(err)
	}
	for _, output := range []string{input, filepath.Join(input, "new", "report.json"), filepath.Join(alias, "new", "report.json")} {
		var out bytes.Buffer
		if code := cli.Run(t.Context(), []string{"load", "--report-json", output, input}, &out, &out); code != 2 || !strings.Contains(out.String(), "overlaps an input") {
			t.Fatalf("exit=%d output=%s", code, &out)
		}
	}
	for path, before := range original {
		if after, err := os.ReadFile(path); err != nil || !bytes.Equal(before, after) {
			t.Fatalf("input changed: %s %v", path, err)
		}
	}
}

type loadFailWriter struct{}

func (loadFailWriter) Write([]byte) (int, error) { return 0, errors.New("output disconnected") }

func TestLoadOutputFailures(t *testing.T) {
	root := t.TempDir()
	input := filepath.Join(root, "collections")
	write(t, filepath.Join(input, "one.request.json"), request.Request{ID: "one", Name: "Skip", Method: "GET", URL: "http://127.0.0.1:1", Pre: `pm.execution.setNextRequest(null); pm.execution.skipRequest()`})
	output := filepath.Join(root, "directory")
	keep := filepath.Join(output, "keep.json")
	before := write(t, keep, "preserve-me")
	var out, errOut bytes.Buffer
	if code := cli.Run(t.Context(), []string{"load", "--max-attempts", "1", "--report-json", output, input}, &out, &errOut); code != 2 || !strings.Contains(errOut.String(), "Could not write report") {
		t.Fatalf("exit=%d stdout=%s stderr=%s", code, &out, &errOut)
	}
	if got, err := os.ReadFile(keep); err != nil || !bytes.Equal(got, before) {
		t.Fatal("write failure changed destination")
	}
	if leftovers, err := filepath.Glob(filepath.Join(root, ".vscroxy-*")); err != nil || len(leftovers) != 0 {
		t.Fatalf("temporary output leak: %v %v", leftovers, err)
	}
	output = filepath.Join(root, "load.json")
	if code := cli.Run(t.Context(), []string{"load", "--max-attempts", "1", "--report-json", output, input}, loadFailWriter{}, io.Discard); code != 2 {
		t.Fatalf("stdout failure exit=%d", code)
	}
	report, _ := readLoadReport(t, output) // file report survives a broken stdout pipe
	if report.Reason != "finished" || report.Total.Skipped != 1 {
		t.Fatalf("stdout failure masked an execution error: %+v", report)
	}
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if code := cli.Run(ctx, []string{"load", "--report-json", output, input}, io.Discard, loadFailWriter{}); code != 2 {
		t.Fatalf("stderr output failure must take precedence over cancellation: exit=%d", code)
	}
	report, _ = readLoadReport(t, output)
	if report.Reason != "cancelled" {
		t.Fatalf("output failure lost cancelled report: %+v", report)
	}
}
