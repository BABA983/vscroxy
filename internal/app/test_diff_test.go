package app

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/baba983/vscroxy/internal/capture"
	"github.com/baba983/vscroxy/internal/request"
	"github.com/baba983/vscroxy/internal/uikit"
	"github.com/baba983/vscroxy/locales"
	"github.com/egoist/mygo/ui"
)

// testDiffFixture is a send whose only failed check carries a Chai diff.
func testDiffFixture(t *testing.T, a *app) {
	t.Helper()
	a.addDocument(a.requests[0], "saved:"+a.requests[0].ID)
	a.sent, a.sentSet = a.draft(), true
	a.response = &capture.Flow{Status: 200, Millis: 12, Bytes: 22, ResponseHeader: http.Header{"Content-Type": {"application/json"}}, ResponseBody: `{"b":[2]}`}
	a.composeTests = []request.TestResult{{
		Name: "body shape", Pass: false, Error: "expected { a: 1 } to deeply equal { a: 1 }",
		Expected: "{\n  \"b\": [\n    3\n  ]\n}", Actual: "{\n  \"b\": [\n    2\n  ]\n}", HasDiff: true,
	}}
	a.responseTab = resViewTests
}

func TestShowDiffOpensExpectedAgainstActual(t *testing.T) {
	for _, runner := range []bool{false, true} {
		t.Run(fmt.Sprint("runner=", runner), func(t *testing.T) {
			a := workspaceApp()
			testDiffFixture(t, a)
			if runner {
				a.runner = collectionRunner{open: true, collection: "Workspace", iterations: "1"}
				a.runner.rows = []runnerRow{{name: "List projects", failed: true, tests: a.composeTests}}
			}
			tt := ui.NewTester(a.view, 1200, 760)
			tt.Frame()
			if err := tt.Click(locales.ShowDiff()); err != nil {
				t.Fatal(err)
			}
			tt.Frame()
			if !a.testDiffOpen || a.testDiff == nil {
				t.Fatal("Show diff did not open the comparison")
			}
			if got := a.testDiff.Old.String(); got != a.composeTests[0].Expected {
				t.Errorf("left side = %q, want expected %q", got, a.composeTests[0].Expected)
			}
			if got := a.testDiff.New.String(); got != a.composeTests[0].Actual {
				t.Errorf("right side = %q, want actual %q", got, a.composeTests[0].Actual)
			}
			if !tt.HasText(locales.ExpectedValue()) || !tt.HasText(locales.ActualValue()) {
				t.Errorf("comparison is missing its side labels: %v", tt.Texts())
			}
		})
	}
}

func TestExperimentRecordPersistsAssertionDiff(t *testing.T) {
	want := request.TestResult{Name: "body shape", Error: "expected 3 to deeply equal 2",
		Expected: "{\n  \"b\": [\n    3\n  ]\n}", Actual: "{\n  \"b\": [\n    2\n  ]\n}", HasDiff: true}
	record := snapshotExperiment(0, savedRequest{Name: "Flaky", Method: "GET", URL: "https://example.test/"}, "Local",
		request.Execution{Tests: []request.TestResult{want}})
	var log experimentLog
	if err := log.retain(record, experimentLimits{}); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(t.TempDir(), "experiments.json")
	if err := saveExperiments(path, log, experimentLimits{}); err != nil {
		t.Fatal(err)
	}
	restored, err := loadExperiments(path, experimentLimits{})
	if err != nil {
		t.Fatal(err)
	}
	if len(restored.Records) != 1 || len(restored.Records[0].Tests) != 1 || restored.Records[0].Tests[0] != want {
		t.Fatalf("persisted tests = %+v, want %+v", restored.Records, want)
	}
}

// A failing assertion on the first send becomes passing on the retry, while a
// passing one becomes failing. The rerun must show both and count the failure.
func TestRequestPanelRerunShowsNewFailures(t *testing.T) {
	var hits atomic.Int32
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if hits.Add(1) == 1 {
			w.WriteHeader(http.StatusServiceUnavailable)
			fmt.Fprint(w, `{"ok":true}`)
			return
		}
		fmt.Fprint(w, `{"ok":false}`)
	}))
	defer upstream.Close()
	a := workspaceApp()
	a.srv = requestProxy(t)
	a.addDocument(savedRequest{Name: "Flaky", Method: "GET", URL: upstream.URL,
		Post: `pm.test("status 200", function () { pm.expect(pm.response.code).to.equal(200) });
			pm.test("body ok", function () { pm.expect(pm.response.json().ok).to.equal(true) });`}, "saved:flaky")
	a.sendComposed()
	waitAppSignal(t, a, func() bool { return !a.composeWaiting }, a.drain,
		func() string { return "first send did not finish" })
	if len(a.composeTests) != 2 || a.composeTests[0].Pass || !a.composeTests[1].Pass {
		t.Fatalf("first send reported %+v, want A failing and B passing", a.composeTests)
	}
	a.responseTab = resViewTests

	tt := ui.NewTester(a.view, 1200, 760)
	tt.Frame()
	if err := tt.Click(locales.RerunFailed()); err != nil {
		t.Fatal(err)
	}
	waitAppSignal(t, a, func() bool { return !a.composeWaiting }, a.drain,
		func() string { return "rerun did not finish" })
	if len(a.composeTests) != 2 {
		t.Fatalf("rerun reported %+v, want both assertions", a.composeTests)
	}
	byName := make(map[string]request.TestResult)
	for _, test := range a.composeTests {
		byName[test.Name] = test
	}
	if !byName["status 200"].Pass || byName["body ok"].Pass {
		t.Errorf("rerun reported %+v, want A passing and B failing", a.composeTests)
	}
	if !strings.Contains(a.composeResult, "1/2 tests") {
		t.Errorf("summary %q does not count the new failure", a.composeResult)
	}
}

func TestRerunFailedRequestsOnlyRunsFailures(t *testing.T) {
	paths := make(chan string, 8)
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		paths <- r.URL.Path
		if r.URL.Path == "/fail" {
			w.WriteHeader(http.StatusServiceUnavailable)
			return
		}
		fmt.Fprint(w, `{"ok":true}`)
	}))
	defer upstream.Close()
	a := workspaceApp()
	a.srv = requestProxy(t)
	a.envs = []environment{{Name: "Local", Vars: map[string]string{"base": upstream.URL}}}
	a.activeEnv = "Local"
	a.requests = []savedRequest{
		{Name: "Good", Collection: "Demo", Method: "GET", URL: "{{base}}/good"},
		{Name: "Bad", Collection: "Demo", Method: "GET", URL: "{{base}}/fail"},
	}
	a.openRunner()
	a.runner.collection, a.runner.iterations = "Demo", "1"
	a.runner.load(a.requests)

	a.startRunner()
	waitRunner(t, a)
	if a.runner.completed != 2 || a.runner.failures != 1 {
		t.Fatalf("first run = %+v, completed %d failed %d status %q rows %d", a.runner.rows, a.runner.completed, a.runner.failures, a.runner.status, len(a.runner.rows))
	}
	for len(paths) > 0 {
		<-paths
	}

	tt := ui.NewTester(a.view, 1200, 760)
	tt.Frame()
	if err := tt.Click(locales.RerunFailed()); err != nil {
		t.Fatal(err)
	}
	waitRunner(t, a)
	if a.runner.completed != 1 || a.runner.failures != 1 {
		t.Fatalf("rerun = %+v, want one failed request", a.runner.rows)
	}
	var visited []string
	for len(paths) > 0 {
		visited = append(visited, <-paths)
	}
	if len(visited) != 1 || visited[0] != "/fail" {
		t.Fatalf("rerun visited %v, want only /fail", visited)
	}
}

// A data-driven failure is retried with its own row and the run's starting
// variables, not with the variables later iterations changed.
func TestRerunFailedRequestsKeepsDataRowAndStartingVariables(t *testing.T) {
	queries := make(chan string, 8)
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		queries <- r.URL.RawQuery
		if r.URL.Query().Get("sku") == "B-2" {
			w.WriteHeader(http.StatusServiceUnavailable)
			return
		}
		fmt.Fprint(w, `{"ok":true}`)
	}))
	defer upstream.Close()
	a := workspaceApp()
	a.srv = requestProxy(t)
	a.envs = []environment{{Name: "Local", Vars: map[string]string{"base": upstream.URL, "mark": "initial"}}}
	a.activeEnv = "Local"
	a.requests = []savedRequest{{Name: "Order", Collection: "Data", Method: "GET",
		URL:  "{{base}}/?sku={{sku}}&mark={{mark}}",
		Post: `pm.environment.set("mark", "changed"); pm.test("status 200", function () { pm.expect(pm.response.code).to.equal(200) });`}}
	a.openRunner()
	a.runner.collection = "Data"
	a.runner.load(a.requests)
	rows, err := request.ReadIterationData("orders.csv", []byte("sku\nA-1\nB-2\nC-3\n"))
	if err != nil {
		t.Fatal(err)
	}
	a.runner.useData("orders.csv", rows, nil)
	a.runner.stopOnFailure = false
	a.startRunner()
	waitRunner(t, a)
	if a.runner.completed != 3 || a.runner.failures != 1 {
		t.Fatalf("first run = %+v, failures %d", a.runner.rows, a.runner.failures)
	}
	for len(queries) > 0 {
		<-queries
	}
	if got := a.activeVars()["mark"]; got != "changed" {
		t.Fatalf("script's variable write = %q, want changed", got)
	}

	tt := ui.NewTester(a.view, 1200, 760)
	tt.Frame()
	if err := tt.Click(locales.RerunFailed()); err != nil {
		t.Fatal(err)
	}
	waitRunner(t, a)
	if a.runner.completed != 1 || a.runner.failures != 1 {
		t.Fatalf("rerun = %+v, want one failed attempt", a.runner.rows)
	}
	var got []string
	for len(queries) > 0 {
		got = append(got, <-queries)
	}
	if want := []string{"sku=B-2&mark=initial"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("rerun queries = %q, want %q", got, want)
	}
	// A second failed-only rerun keeps that row too, even though the first
	// rerun was displayed as a single iteration.
	tt.Frame()
	if err := tt.Click(locales.RerunFailed()); err != nil {
		t.Fatal(err)
	}
	waitRunner(t, a)
	got = nil
	for len(queries) > 0 {
		got = append(got, <-queries)
	}
	if want := []string{"sku=B-2&mark=initial"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("second rerun queries = %q, want %q", got, want)
	}
}

// TestTestDiffScreenshots captures the failed check with its read-only
// comparison open in both themes. Set VSCROXY_TEST_DIFF_SCREENSHOTS to a
// directory to write the 2x images.
func TestTestDiffScreenshots(t *testing.T) {
	dir := os.Getenv("VSCROXY_TEST_DIFF_SCREENSHOTS")
	if dir == "" {
		t.Skip("set VSCROXY_TEST_DIFF_SCREENSHOTS to an output directory")
	}
	for _, dark := range []bool{false, true} {
		appearance := "light"
		if dark {
			appearance = "dark"
		}
		t.Run(appearance, func(t *testing.T) {
			a := workspaceApp()
			testDiffFixture(t, a)
			theme := uikit.Theme(dark)
			tt := ui.NewTester(func(c *ui.Context) {
				c.SetTheme(theme)
				a.view(c)
			}, 1300, 820)
			tt.SetLocale("en-US")
			tt.SetScale(2)
			tt.Frame()
			if err := tt.Click(locales.ShowDiff()); err != nil {
				t.Fatal(err)
			}
			tt.Frame()
			if !a.testDiffOpen || !tt.HasText(locales.ExpectedValue()) {
				t.Fatal("the comparison did not open")
			}
			writeFeatureScreenshot(t, dir, "test-diff-"+appearance+".png", tt.Image())
		})
	}
}
