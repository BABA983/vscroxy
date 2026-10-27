package app

import (
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"reflect"
	"runtime"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/baba983/vscroxy/internal/capture"
	"github.com/baba983/vscroxy/internal/request"
	"github.com/egoist/mygo/ui"
)

func TestEditReplayOpensExactBytesWithoutSending(t *testing.T) {
	for _, width := range []int{1280, 960} {
		t.Run(fmt.Sprint(width), func(t *testing.T) {
			a := workspaceApp()
			body := strings.Repeat("中x", 5000) + "tail"
			spill := filepath.Join(t.TempDir(), "request.body")
			if err := os.WriteFile(spill, []byte(body), 0o600); err != nil {
				t.Fatal(err)
			}
			a.flows[0].reqHeader = http.Header{"Content-Encoding": {"gzip"}, "Content-Length": {"123"}, "X-Tag": {"one", "two"}}
			a.flows[0].reqFile, a.flows[0].reqSize = spill, len(body)
			tt := ui.NewTester(a.view, width, 600)
			click := func(label string) {
				t.Helper()
				if err := tt.Click(label); err != nil {
					t.Fatal(err)
				}
			}
			click("Edit & replay")
			if len(a.documents) != 1 || len(a.requests) != 1 || a.composeWaiting || a.response != nil || a.activeEnv != "" {
				t.Fatal("Edit & replay must open one unsent draft without saving it or changing the environment")
			}
			if a.composeBody != body || !strings.HasSuffix(a.composeURL, "/projects") {
				t.Fatal("the draft lost spilled body bytes or its URL")
			}
			if got := request.ParseHeaderBlock(a.composeHeaders); len(got.Values("X-Tag")) != 2 || got.Get("Content-Encoding") != "" || a.flows[0].reqHeader.Get("Content-Encoding") != "gzip" {
				t.Fatal("conversion changed source or duplicate/encoding headers")
			}
			a.requestsPath = filepath.Join(t.TempDir(), "requests.json")
			if !a.saveComposed() {
				t.Fatal(a.message)
			}
			originalID := a.assetID
			click("Request options")
			if err := tt.ChooseMenuItem("Duplicate as new request"); err != nil {
				t.Fatal(err)
			}
			if !a.saveComposed() || a.assetID == originalID || len(a.requests) != 3 {
				t.Fatal("duplicate reused a saved identity")
			}
		})
	}
}

func TestRequestConsoleAndRunsAreVisibleWithoutReexecution(t *testing.T) {
	var sends atomic.Int32
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		sends.Add(1)
		fmt.Fprint(w, "response")
	}))
	defer upstream.Close()
	a := workspaceApp()
	a.srv = requestProxy(t)
	a.addDocument(savedRequest{Name: "Console request", Method: "GET", URL: upstream.URL, Pre: `log("pre-output");`, Post: `log("post-output"); throw new Error("diagnostic");`}, "")
	tt := ui.NewTester(a.view, 960, 600)
	click := func(label string) {
		t.Helper()
		if err := tt.Click(label); err != nil {
			t.Fatal(err)
		}
	}
	click("Send")
	guard := time.NewTimer(time.Minute)
	defer guard.Stop()
	for a.composeWaiting {
		select {
		case <-guard.C:
			t.Fatal("send did not finish")
		default:
		}
		tt.Frame()
		runtime.Gosched()
	}
	click("Response tab: Console")
	if !tt.HasText("pre-request · log · pre-output") || !tt.HasText("post-response · log · post-output") || !tt.HasText("post-response.js:1:") {
		t.Fatal("visible console lost logs or error location")
	}
	click("Runs")
	click("All retained runs…")
	click("Variables")
	tt.Key(0, ui.KeyLeft)
	if a.experiments.tab != 4 || !tt.HasText("post-response · log · post-output") {
		t.Fatal("retained run lost console")
	}
	click("Open as draft")
	if sends.Load() != 1 || a.composeWaiting {
		t.Fatal("opening a run reexecuted the request")
	}
}

func TestScenarioRulesUIControlsSavedAndRunningConfiguration(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, "real response")
	}))
	defer upstream.Close()
	dir := t.TempDir()
	mock, err := capture.NewMockRule(dir, capture.Flow{Method: "GET", URL: upstream.URL + "/projects", Path: "/projects", Status: 201, ResponseBody: "empty response"})
	if err != nil {
		t.Fatal(err)
	}
	mock.Name, mock.Scenario = "Empty response", "Empty account"
	a := workspaceApp()
	a.rulesPath = filepath.Join(dir, "rules.json")
	initial := []capture.Rule{
		{Name: "Disabled override", Type: capture.RuleBlock, Match: "/projects", Scenario: "Empty account", Disabled: true},
		mock,
		{Name: "Server error", Type: capture.RuleMapLocal, Match: "/projects", Target: mock.Target, Status: 503, Scenario: "Error"},
		{Name: "Global block", Type: capture.RuleBlock, Match: "/projects"},
	}
	if err := capture.SaveRules(a.rulesPath, initial); err != nil {
		t.Fatal(err)
	}
	a.rules, err = capture.LoadRules(a.rulesPath)
	if err != nil {
		t.Fatal(err)
	}
	a.srv, err = capture.New("127.0.0.1:0", t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = a.srv.Close() })
	if err := a.srv.SetRules(a.rules); err != nil {
		t.Fatal(err)
	}
	if err := a.srv.Start(); err != nil {
		t.Fatal(err)
	}
	proxyURL, err := url.Parse("http://" + a.srv.Addr())
	if err != nil {
		t.Fatal(err)
	}
	transport := &http.Transport{Proxy: http.ProxyURL(proxyURL)}
	t.Cleanup(transport.CloseIdleConnections)
	client := &http.Client{Transport: transport}
	tt := ui.NewTester(a.view, 1100, 650)
	click := func(label string) {
		t.Helper()
		if err := tt.Click(label); err != nil {
			t.Fatal(err)
		}
	}
	edit := func(label, value string) {
		t.Helper()
		click(label)
		tt.Key(ui.Cmd, ui.KeyA)
		tt.Type(value)
	}
	check := func(active string, status int, body string) []capture.Rule {
		t.Helper()
		loaded, err := capture.LoadRules(a.rulesPath)
		if err != nil || capture.ActiveScenario(loaded) != active || !loaded[0].Disabled {
			t.Fatalf("persisted selection or individual disabled state incorrect: %+v, %v", loaded, err)
		}
		// Compare through the persistence contract: runtime clones canonicalize
		// absent headers to an empty map, which JSON correctly omits again.
		runtimePath := filepath.Join(t.TempDir(), "runtime.json")
		if err := capture.SaveRules(runtimePath, a.srv.Rules()); err != nil {
			t.Fatal(err)
		}
		runtimeRules, err := capture.LoadRules(runtimePath)
		if err != nil || !reflect.DeepEqual(loaded, runtimeRules) {
			t.Fatal("disk and runtime rules diverged")
		}
		req, err := http.NewRequestWithContext(t.Context(), "GET", upstream.URL+"/projects", nil)
		if err != nil {
			t.Fatal(err)
		}
		resp, err := client.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		got, err := io.ReadAll(resp.Body)
		resp.Body.Close()
		if err != nil || resp.StatusCode != status || string(got) != body {
			t.Fatalf("running proxy returned %d %q, want %d %q: %v", resp.StatusCode, got, status, body, err)
		}
		return loaded
	}
	click("More")
	if err := tt.ChooseMenuItem("Debugging rules…"); err != nil {
		t.Fatal(err)
	}
	click("Test URL + method")
	edit("Test URL", upstream.URL+"/projects")
	for _, want := range []string{
		`1 · Disabled override · Block — disabled; inactive scenario "Empty account"`,
		`2 · Empty response · Map Local — inactive scenario "Empty account"`,
		"4 · Global block · Block — winner",
	} {
		if !tt.HasText(want) {
			t.Fatalf("preview missing %q: %q", want, tt.Texts())
		}
	}
	click("Activate scenario")
	check("Empty account", 201, "empty response")
	if !tt.HasText("2 · Empty response · Map Local — winner") || !tt.HasText("4 · Global block · Block — shadowed by rule 2") {
		t.Fatalf("preview did not follow activation: %q", tt.Texts())
	}
	edit("Test method", "get") // method tokens are case-sensitive, just as on the wire
	if !tt.HasText("2 · Empty response · Map Local — method does not match") || !tt.HasText("4 · Global block · Block — winner") {
		t.Fatalf("preview did not use the exact method: %q", tt.Texts())
	}
	edit("Test method", "GET")
	click("Scenario to activate")
	click("Error")
	// Choosing a group alone is not an activation.
	check("Empty account", 201, "empty response")
	click("Activate scenario")
	check("Error", 503, "empty response")
	click("Edit rule 4") // returns from preview to the editor
	edit("Rule scenario", " Error ")
	click("Save rule")
	joined := check("Error", 503, "empty response")
	if joined[3].Scenario != "Error" || !joined[3].ScenarioActive {
		t.Fatal("joining an active group did not inherit its selection state")
	}
	edit("Rule scenario", "Maintenance")
	click("Save rule")
	click("Scenario to activate")
	click("Maintenance")
	click("Activate scenario")
	check("Maintenance", 403, "blocked by vscroxy\n")
	click("Deactivate scenario")
	check("", 200, "real response")
	// Failed persistence must not activate either the editor or the runtime.
	a.rulesPath = t.TempDir()
	click("Activate scenario")
	if capture.ActiveScenario(a.srv.Rules()) != "" || capture.ActiveScenario(a.rules) != "" {
		t.Fatal("failed save activated a scenario")
	}
	if tt.HasText("Active: Maintenance") {
		t.Fatal("failed save falsely reported an active scenario")
	}
}
