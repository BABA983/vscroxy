package runner_test

import (
	"context"
	"encoding/json/v2"
	"errors"
	"fmt"
	"maps"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/baba983/vscroxy/internal/capture"
	"github.com/baba983/vscroxy/internal/request"
	"github.com/baba983/vscroxy/internal/runner"
)

func loadProxy(t *testing.T) *capture.Server {
	t.Helper()
	srv, err := capture.New("127.0.0.1:0", t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if err := srv.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = srv.Close() })
	return srv
}

func TestLoadSummaryAndPrivacy(t *testing.T) {
	const secret = "private-runtime-value-948"
	host := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/expected" {
			w.WriteHeader(404)
		} else if r.URL.Path == "/error" {
			w.WriteHeader(503)
		}
		fmt.Fprint(w, secret)
	}))
	defer host.Close()
	steps := []runner.Step{
		{Request: request.Request{ID: "expected", URL: host.URL + "/expected", Checks: request.Checks{Status: "404"}}},
		{Request: request.Request{ID: "check", URL: host.URL, Post: `pm.test("` + secret + `", () => { throw new Error("` + secret + `") });`}},
		{Request: request.Request{ID: "error", URL: host.URL + "/error"}},
		{Request: request.Request{ID: "skip", URL: host.URL, Pre: `pm.execution.skipRequest()`}},
		{Request: request.Request{ID: "setup", URL: host.URL, Checks: request.Checks{Status: "invalid"}}},
		{Request: request.Request{ID: "stop", URL: host.URL + "?secret=" + secret, Headers: "Authorization: Bearer " + secret, Post: `console.log("` + secret + `"); pm.execution.setNextRequest(null)`}},
	}
	var snapshots []runner.LoadReport
	report, err := runner.RunLoad(t.Context(), loadProxy(t), steps, runner.LoadOptions{Users: 2, Duration: time.Minute}, func(r runner.LoadReport) { snapshots = append(snapshots, r) })
	if err != nil || !report.Done || report.Reason != "finished" {
		t.Fatalf("completion = %+v, %v", report, err)
	}
	s := report.Total
	if s.Attempts != 12 || s.Completed != 10 || s.Failed != 6 || s.Skipped != 2 || s.Interrupted != 0 || s.Errors != 2 || s.HTTPResponses != 8 || s.ResponseBytes != int64(8*len(secret)) || s.LatencySamples != 10 || s.FailureRate != .6 {
		t.Fatalf("incorrect aggregates: %+v", s)
	}
	if s.RequestsPerSec <= 0 || s.P50Millis <= 0 || s.P50Millis > s.P95Millis || s.P95Millis > s.P99Millis || s.P99Millis > s.MaxMillis {
		t.Fatalf("invalid distribution: %+v", s)
	}
	for i, row := range report.Requests {
		if row.Index != i || row.ID != steps[i].Request.ID || row.Stats.Attempts != 2 {
			t.Fatalf("request aggregate %+v", row)
		}
	}
	data, err := json.Marshal(snapshots)
	if err != nil || strings.Contains(string(data), secret) || strings.Contains(string(data), host.URL) {
		t.Fatalf("report leaked execution data: %s (%v)", data, err)
	}
	if !reflect.DeepEqual(snapshots[len(snapshots)-1], report) {
		t.Fatal("final callback differs from returned snapshot")
	}
}

func TestLoadUsersKeepIndependentScopesCookiesAndTokens(t *testing.T) {
	var next atomic.Int64
	var mu sync.Mutex
	seen := map[string][]int{}
	host := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/oauth" {
			w.Header().Set("Content-Type", "application/json")
			fmt.Fprintf(w, `{"access_token":"vu-%d","expires_in":3600,"token_type":"Bearer"}`, next.Add(1))
			return
		}
		token := strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer ")
		n, _ := strconv.Atoi(r.Header.Get("X-Iteration"))
		if cookie, err := r.Cookie("vu"); n == 1 && err == nil || n == 2 && (err != nil || cookie.Value != token) {
			t.Errorf("VU cookie isolation failed: token %s iteration %d cookie %v, %v", token, n, cookie, err)
		}
		http.SetCookie(w, &http.Cookie{Name: "vu", Value: token, Path: "/"})
		mu.Lock()
		seen[token] = append(seen[token], n)
		mu.Unlock()
		fmt.Fprint(w, "ok")
	}))
	defer host.Close()
	vars, globals := map[string]string{"count": "0"}, map[string]string{"g": "0"}
	folders := []request.Folder{{Path: "API", Variables: []request.Variable{{Name: "c", Initial: "0"}}}}
	steps := []runner.Step{{Request: request.Request{ID: "one", Collection: "API", URL: host.URL, Auth: request.Auth{Type: request.AuthOAuth2, OAuth2: request.OAuth2{Grant: request.GrantClientCredentials, TokenURL: host.URL + "/oauth", ClientID: "test"}}, Pre: `
const n = pm.info.iteration + 1;
if (+pm.environment.get("count") !== n-1 || +pm.globals.get("g") !== n-1 || +pm.collectionVariables.get("c") !== n-1) throw new Error("scope crossed users or reset between loops");
pm.environment.set("count", String(n)); pm.globals.set("g", String(n)); pm.collectionVariables.set("c", String(n));
pm.request.headers.upsert({key:"X-Iteration",value:String(n)});`, Post: `if (pm.info.iteration === 1) pm.execution.setNextRequest(null);`}, Context: request.Context{Globals: globals, Folders: folders, Tokens: &request.Tokens{}}}}
	before, _ := json.Marshal(steps[0].Request)
	report, err := runner.RunLoad(t.Context(), loadProxy(t), steps, runner.LoadOptions{Users: 4, Duration: time.Minute, Vars: vars}, nil)
	if err != nil || report.Total.Completed != 8 || report.Total.Failed != 0 || next.Load() != 4 || len(seen) != 4 {
		t.Fatalf("isolation: report %+v, err %v, tokens %d, users %v", report, err, next.Load(), seen)
	}
	for token, counts := range seen {
		if !reflect.DeepEqual(counts, []int{1, 2}) {
			t.Fatalf("user %s iterations %v", token, counts)
		}
	}
	after, _ := json.Marshal(steps[0].Request)
	if string(before) != string(after) || !maps.Equal(vars, map[string]string{"count": "0"}) || !maps.Equal(globals, map[string]string{"g": "0"}) || folders[0].Variables[0].Value() != "0" {
		t.Fatal("load mutated caller inputs")
	}
}

func TestLoadDurationCancellationAndAttemptBudget(t *testing.T) {
	for _, mode := range []string{"duration", "cancelled", "attempt-limit", "failure", "branch"} {
		t.Run(mode, func(t *testing.T) {
			host := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if mode == "duration" || mode == "cancelled" {
					<-r.Context().Done()
					return
				}
				fmt.Fprint(w, "ok")
			}))
			defer host.Close()
			srv := loadProxy(t)
			ctx, cancel := context.WithCancel(t.Context())
			defer cancel()
			if mode == "cancelled" {
				timer := time.AfterFunc(100*time.Millisecond, cancel)
				defer timer.Stop()
			}
			step := runner.Step{Request: request.Request{URL: host.URL}}
			opts := runner.LoadOptions{Users: 3, Duration: time.Second, MaxAttempts: 17}
			if mode == "failure" {
				step.Request.Pre = `throw new Error("private")`
				opts.StopOnFailure = true
			}
			if mode == "branch" {
				step.Request.Post = `pm.execution.setNextRequest("missing")`
			}
			r, err := runner.RunLoad(ctx, srv, []runner.Step{step}, opts, nil)
			if !r.Done || r.Reason != mode && !(mode == "branch" && r.Reason == "execution-error") {
				t.Fatalf("report=%+v, err=%v", r, err)
			}
			switch mode {
			case "duration":
				if err != nil || r.Total.Interrupted != 3 || r.Total.Failed != 0 || r.Total.LatencySamples != 0 {
					t.Fatalf("normal duration misclassified: %+v %v", r, err)
				}
			case "cancelled":
				if !errors.Is(err, context.Canceled) || r.Total.Interrupted != 3 || r.Total.Failed != 0 {
					t.Fatalf("cancelled: %+v %v", r, err)
				}
			case "attempt-limit":
				if !errors.Is(err, runner.ErrStepLimit) || r.Total.Attempts != 17 {
					t.Fatalf("budget not exact: %+v %v", r, err)
				}
			case "failure":
				if err != nil || r.Total.Failed == 0 {
					t.Fatalf("bail: %+v %v", r, err)
				}
			case "branch":
				if err == nil {
					t.Fatal("unknown branch silently succeeded")
				}
			}
		})
	}
}

func TestLoadTimeoutAndDynamicWebSocketRefusal(t *testing.T) {
	var sends atomic.Int64
	host := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		sends.Add(1)
		<-r.Context().Done()
	}))
	defer host.Close()
	srv := loadProxy(t)
	report, err := runner.RunLoad(t.Context(), srv, []runner.Step{{Request: request.Request{URL: host.URL}}}, runner.LoadOptions{Users: 1, Duration: time.Second, Timeout: 30 * time.Millisecond, StopOnFailure: true}, nil)
	if err != nil || report.Reason != "failure" || report.Total.Errors != 1 || report.Total.Interrupted != 0 {
		t.Fatalf("request timeout must be failure, not load interruption: %+v %v", report, err)
	}
	// Whether the timed-out request reached the host before its budget ran
	// out is a race with the proxy's start; what matters is that the
	// WebSocket the script switched to never does.
	reached := sends.Load()
	report, err = runner.RunLoad(t.Context(), srv, []runner.Step{{Request: request.Request{URL: host.URL, Pre: `pm.request.url = "ws://127.0.0.1:1/";`}}}, runner.LoadOptions{Users: 1, Duration: time.Second, StopOnFailure: true}, nil)
	if err != nil || report.Total.Failed != 1 || report.Total.HTTPResponses != 0 || sends.Load() != reached {
		t.Fatalf("script changed workload to WebSocket: %+v %v", report, err)
	}
}

func TestLoadValidation(t *testing.T) {
	srv := loadProxy(t)
	base := runner.LoadOptions{Users: 1, Duration: time.Second}
	for _, bad := range []runner.LoadOptions{{}, {Users: 65, Duration: time.Second}, {Users: 1, Duration: time.Millisecond}, {Users: 1, Duration: 11 * time.Minute}, {Users: 1, Duration: time.Second, Timeout: -1}, {Users: 1, Duration: time.Second, MaxAttempts: 1000001}} {
		if _, err := runner.RunLoad(t.Context(), srv, []runner.Step{{}}, bad, nil); err == nil {
			t.Fatalf("accepted bad options: %+v", bad)
		}
	}
	for _, steps := range [][]runner.Step{nil, make([]runner.Step, 201), {{Request: request.Request{URL: "wss://localhost/"}}}} {
		if _, err := runner.RunLoad(t.Context(), srv, steps, base, nil); err == nil {
			t.Fatal("accepted invalid selection")
		}
	}
	if _, err := runner.RunLoad(t.Context(), nil, []runner.Step{{}}, base, nil); err == nil {
		t.Fatal("accepted stopped proxy")
	}
}

func TestLoadThresholdsParseEvaluateAndCompare(t *testing.T) {
	thresholds, err := runner.ParseThresholds("p95<500ms, errors<1%, failureRate<2, rps>=10, completed>2")
	if err != nil {
		t.Fatal(err)
	}
	metrics := runner.LoadMetrics{P95Millis: 123.4, Errors: 1, Completed: 200, FailureRate: 0.005, RequestsPerSec: 20}
	results := runner.EvaluateThresholds(thresholds, metrics)
	if len(results) != 5 {
		t.Fatalf("results = %+v", results)
	}
	for _, result := range results {
		if !result.Pass {
			t.Errorf("%s failed: got %v want %s%v", result.Text, result.Got, result.Op, result.Want)
		}
	}
	// A slower P95 and a higher error share must fail.
	results = runner.EvaluateThresholds(thresholds, runner.LoadMetrics{P95Millis: 900, Errors: 5, Completed: 100, FailureRate: 0.05, RequestsPerSec: 5})
	if results[0].Pass || results[1].Pass || results[2].Pass || results[3].Pass {
		t.Fatalf("bad metrics passed: %+v", results)
	}
	for _, bad := range []string{"p95", "p95<", "p95<soon", "unknown<1", "errors<lots", "p95<-1ms"} {
		if _, err := runner.ParseThresholds(bad); err == nil {
			t.Errorf("%q accepted", bad)
		}
	}

	before := runner.LoadReport{Version: 1, StartedAt: time.Date(2026, 11, 1, 0, 0, 0, 0, time.UTC),
		Total: runner.LoadMetrics{P95Millis: 100}, Requests: []runner.LoadRequest{{ID: "one", Stats: runner.LoadMetrics{P95Millis: 100, FailureRate: 0.01, RequestsPerSec: 10}}}}
	after := runner.LoadReport{Version: 1, Total: runner.LoadMetrics{P95Millis: 150}, Requests: []runner.LoadRequest{{ID: "one", Stats: runner.LoadMetrics{P95Millis: 120, FailureRate: 0.02, RequestsPerSec: 8}}}}
	comparison := runner.CompareLoad(before, after)
	if comparison.Total.Before != 100 || comparison.Total.After != 150 || comparison.Total.ChangePercent != 50 {
		t.Fatalf("total comparison = %+v", comparison.Total)
	}
	if len(comparison.Requests) != 1 || comparison.Requests[0].P95Millis.ChangePercent != 20 || comparison.Requests[0].FailureRate.ChangePercent != 100 {
		t.Fatalf("request comparison = %+v", comparison.Requests)
	}
}

func TestLoadProfilesAndPerUserData(t *testing.T) {
	// Plans are deterministic; the run itself only checks the profile is kept.
	base := runner.LoadOptions{Users: 4, Duration: 10 * time.Second, Profile: runner.LoadProfileRamp, RampSeconds: 8}
	if err := base.Validate(); err != nil {
		t.Fatal(err)
	}
	spike := runner.LoadOptions{Users: 2, Duration: 10 * time.Second, Profile: runner.LoadProfileSpike, SpikeUsers: 2, SpikeSeconds: 3, SpikeAtSeconds: 4}
	if err := spike.Validate(); err != nil {
		t.Fatal(err)
	}
	for _, bad := range []runner.LoadOptions{
		{Users: 2, Duration: 10 * time.Second, Profile: "wave"},
		{Users: 2, Duration: 10 * time.Second, Profile: runner.LoadProfileRamp, RampSeconds: 11},
		{Users: 2, Duration: 10 * time.Second, Profile: runner.LoadProfileSpike, SpikeUsers: 1},
		{Users: 64, Duration: 10 * time.Second, Profile: runner.LoadProfileSpike, SpikeUsers: 1, SpikeSeconds: 1},
		{Users: 2, Duration: 10 * time.Second, Profile: runner.LoadProfileSpike, SpikeUsers: 1, SpikeSeconds: 2, SpikeAtSeconds: 9},
	} {
		if err := bad.Validate(); err == nil {
			t.Errorf("invalid profile accepted: %+v", bad)
		}
	}

	var mu sync.Mutex
	var paths []string
	host := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		paths = append(paths, r.URL.Path)
		mu.Unlock()
	}))
	defer host.Close()
	steps := []runner.Step{{Request: request.Request{ID: "row", Method: "GET", URL: host.URL + "/{{row}}"}}}
	rows := []map[string]string{{"row": "a"}, {"row": "b"}}
	_, err := runner.Run(t.Context(), loadProxy(t), steps, runner.Options{Iterations: 3, Data: rows, CycleData: true}, func(runner.Result) error { return nil })
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(paths, []string{"/a", "/b", "/a"}) {
		t.Fatalf("rows = %v, want the file cycled", paths)
	}

	report, err := runner.RunLoad(t.Context(), loadProxy(t), steps, runner.LoadOptions{Users: 2, Duration: time.Second, Profile: runner.LoadProfileSpike, SpikeUsers: 1, SpikeSeconds: 1, Data: [][]map[string]string{rows}}, nil)
	if err != nil || report.Profile != runner.LoadProfileSpike || report.PeakUsers != 3 {
		t.Fatalf("spike report = %+v, %v", report, err)
	}
}
