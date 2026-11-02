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
	"github.com/baba983/vscroxy/internal/rules"
	"github.com/egoist/mygo/ui"
)

func TestEditingACaptureOpensExactBytesWithoutSending(t *testing.T) {
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
			// A spilled body is read from disk only when asked for.
			if a.requestDocument != nil || !tt.HasText("Large request body kept on disk") {
				t.Fatal("choosing a capture read its spilled body")
			}
			click("Edit request")
			if a.requestDocument == nil || len(a.documents) != 0 || len(a.requests) != 1 || a.composeWaiting || a.response != nil || a.activeEnv != "" {
				t.Fatal("editing a capture must open it in place, unsent, without saving it or changing the environment")
			}
			if a.composeBody != body || !strings.HasSuffix(a.composeURL, "/projects") {
				t.Fatal("the draft lost spilled body bytes or its URL")
			}
			if got := request.ParseHeaderBlock(a.composeHeaders); len(got.Values("X-Tag")) != 2 || got.Get("Content-Encoding") != "" || a.flows[0].reqHeader.Get("Content-Encoding") != "gzip" {
				t.Fatal("conversion changed source or duplicate/encoding headers")
			}
			a.library.root = filepath.Join(t.TempDir(), "collections")
			if !a.saveComposed() {
				t.Fatal(a.message)
			}
			originalID := a.assetID
			a.openSaved(a.savedKey)
			tt.Frame()
			click("Request options")
			if err := chooseMenu(tt, "Duplicate as new request"); err != nil {
				t.Fatal(err)
			}
			if !a.saveComposed() || a.assetID == originalID || len(a.requests) != 3 {
				t.Fatal("duplicate reused a saved identity")
			}
		})
	}
}

func TestACapturedRequestIsEditedInPlaceAndDiscardedBackToTheCapture(t *testing.T) {
	var sends atomic.Int32
	upstream := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) { sends.Add(1) }))
	defer upstream.Close()
	a := workspaceApp()
	a.srv = requestProxy(t)
	captured := upstream.URL + "/projects"
	a.flows[0].url = captured
	tt := ui.NewTester(a.view, 1280, 800)
	click := func(label string) {
		t.Helper()
		if err := tt.Click(label); err != nil {
			t.Fatal(err)
		}
	}
	readURL := func() string {
		t.Helper()
		click("URL")
		tt.Key(ui.Cmd, ui.KeyA)
		tt.Key(ui.Cmd, ui.KeyC)
		return tt.Clipboard()
	}
	click("api.example.com/projects")
	if readURL() != captured || tt.HasText("Edited") || len(a.flowDrafts) != 0 {
		t.Fatal("choosing a capture did not open it unchanged, without a draft")
	}
	// The first edit makes the capture's draft; the capture keeps its bytes.
	tt.Key(ui.Cmd, ui.KeyA)
	tt.Type(upstream.URL + "/edited")
	tt.Frame()
	if a.flowDrafts[1] == nil || !tt.HasText("Edited") || a.flows[0].url != captured {
		t.Fatal("editing did not keep a draft beside an unchanged capture")
	}
	click("api.example.com/login")
	if a.requestDocument == nil || a.requestDocument == a.flowDrafts[1] || a.flowDrafts[2] != nil {
		t.Fatal("another capture opened the edited draft")
	}
	click("api.example.com/projects")
	if readURL() != upstream.URL+"/edited" {
		t.Fatal("the edit did not stay with its capture")
	}
	click("Inspector options")
	if err := chooseMenu(tt, "Discard edits"); err != nil {
		t.Fatal(err)
	}
	if readURL() != captured || tt.HasText("Edited") || len(a.flowDrafts) != 0 {
		t.Fatal("discarding edits did not reopen the capture as it was")
	}
	if sends.Load() != 0 {
		t.Fatal("editing sent a request without Send")
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
	a.addDocument(savedRequest{Name: "Console request", Method: "GET", URL: upstream.URL, Pre: `console.log("pre-output");`, Post: `console.log("post-output"); throw new Error("diagnostic");`}, "")
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
	click("Collection options")
	if err := chooseMenu(tt, "History"); err != nil {
		t.Fatal(err)
	}
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
	a.rules, err = a.readRules()
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
	if err := chooseMenu(tt, "Debugging rules…"); err != nil {
		t.Fatal(err)
	}
	// acts reports the rule the badge numbered step sits in, by its row.
	acts := func(step string) string {
		t.Helper()
		badge, ok := tt.Find("Acts " + step)
		if !ok {
			return ""
		}
		for i, r := range a.rules {
			row, ok := tt.Find(fmt.Sprintf("Rule %d: %s", i+1, r.Name))
			if ok && badge.Y >= row.Y && badge.Y+badge.H <= row.Y+row.H {
				return r.Name
			}
		}
		t.Fatalf("badge %q is in no rule's row", step)
		return ""
	}
	chooseScenario := func(name string) {
		t.Helper()
		click("Active scenario")
		if err := chooseMenu(tt, name); err != nil {
			t.Fatal(err)
		}
	}
	edit("Test a request", upstream.URL+"/projects")
	if acts("first") != "Global block" || acts("second") != "" || !tt.HasText("1 rule acts, in the order numbered") {
		t.Fatalf("the test did not number the global rule alone: %q", tt.Texts())
	}
	chooseScenario("Empty account")
	check("Empty account", 201, "empty response")
	if acts("first") != "Empty response" {
		t.Fatal("the test did not follow activation")
	}
	edit("Test a request", "get "+upstream.URL+"/projects") // method tokens are case-sensitive, just as on the wire
	if acts("first") != "Global block" {
		t.Fatal("the test did not use the exact method")
	}
	edit("Test a request", "")
	chooseScenario("Error")
	check("Error", 503, "empty response")
	click("Rule 4: Global block")
	edit("Rule scenario", " Error ")
	click("Save rule")
	joined := check("Error", 503, "empty response")
	if joined[3].Scenario != "Error" || !joined[3].ScenarioActive {
		t.Fatal("joining an active group did not inherit its selection state")
	}
	edit("Rule scenario", "Maintenance")
	click("Save rule")
	chooseScenario("Maintenance")
	check("Maintenance", 403, "blocked by vscroxy\n")
	chooseScenario("No scenario")
	check("", 200, "real response")
	// Failed persistence must not activate either the editor or the runtime.
	a.rulesPath = t.TempDir()
	chooseScenario("Maintenance")
	if capture.ActiveScenario(a.srv.Rules()) != "" || capture.ActiveScenario(a.rules) != "" {
		t.Fatal("failed save activated a scenario")
	}
	if tt.HasText("Scenario: Maintenance") {
		t.Fatal("failed save falsely reported an active scenario")
	}
}

func TestCollectionExamplesBecomeAScenarioThatAnswersForEachRequest(t *testing.T) {
	dir := t.TempDir()
	a := workspaceApp()
	a.page = pageRequest
	a.rulesPath = filepath.Join(dir, "rules.json")
	a.folders = []request.Folder{{Path: "Shop", Variables: []request.Field{{Name: "base", Value: "https://api.test"}}}}
	user := request.Example{Name: "200 OK", Status: 200, Headers: "Content-Type: application/json\nContent-Encoding: gzip", Body: `{"id":7}`,
		Request: request.ExampleRequest{Method: "GET", URL: "{{base}}/users/:id"}}
	missing := request.Example{Name: "Missing", Status: 404, Body: "no such user", Request: user.Request}
	a.requests = append(a.requests,
		savedRequest{Request: request.Request{Name: "Get user", Collection: "Shop/Users", Method: "GET", URL: "{{base}}/users/:id", Examples: []request.Example{user, missing}}},
		savedRequest{Request: request.Request{Name: "List users", Collection: "Shop", Method: "GET", URL: "{{base}}/users",
			Examples: []request.Example{{Name: "Two", Status: 200, Body: "[1,2]", Request: request.ExampleRequest{Method: "GET", URL: "{{base}}/users?page=1"}}}}},
		savedRequest{Request: request.Request{Name: "Elsewhere", Collection: "Shopping", Method: "GET", URL: "https://x.test",
			Examples: []request.Example{{Name: "Other", Status: 200, Body: "x"}}}},
	)
	global := capture.Rule{Name: "Global block", Type: capture.RuleBlock, Match: "/admin"}
	a.rules = []capture.Rule{global}
	tt := ui.NewTester(a.view, 1280, 800)
	mock := func(label string) {
		t.Helper()
		if err := tt.RightClick("Collection: Shop"); err != nil {
			t.Fatal(err)
		}
		if err := chooseMenu(tt, label); err != nil {
			t.Fatal(err)
		}
	}
	answer := func(url string, header http.Header) (capture.Rule, bool) {
		t.Helper()
		for _, p := range capture.PreviewRules(a.rules, rules.Request{Method: "GET", URL: url, Header: header}, false) {
			if p.Step > 0 && a.rules[p.Index].Type == capture.RuleMapLocal {
				return a.rules[p.Index], true
			}
		}
		return capture.Rule{}, false
	}
	body := func(r capture.Rule) string {
		t.Helper()
		data, err := os.ReadFile(r.Target)
		if err != nil {
			t.Fatal(err)
		}
		return string(data)
	}

	mock("Mock “Shop” from examples")
	if !a.toolsOpen || a.toolsPage != "rules" {
		t.Fatal("the new scenario is not shown in Tools")
	}
	tt.Key(0, ui.KeyEscape)
	if len(a.rules) != 4 || a.rules[0].Name != global.Name {
		t.Fatalf("rules = %+v", a.rules)
	}
	for _, r := range a.rules[1:] {
		if r.Scenario != "Shop" || r.ScenarioActive {
			t.Fatalf("a new scenario changed traffic: %+v", r)
		}
	}
	if err := capture.ValidateRules(a.rules); err != nil {
		t.Fatal(err)
	}
	a.selectRuleScenario("Shop")
	if r, ok := answer("https://api.test/users/7?expand=1", nil); !ok || r.Status != 200 || body(r) != `{"id":7}` || r.Headers.Get("Content-Encoding") != "" {
		t.Fatalf("default answer = %+v %v", r, ok)
	}
	if r, ok := answer("https://api.test/users/7", http.Header{"X-Mock-Response-Name": {"Missing"}}); !ok || r.Status != 404 || body(r) != "no such user" {
		t.Fatalf("named answer = %+v %v", r, ok)
	}
	if r, ok := answer("https://api.test/users", nil); !ok || body(r) != "[1,2]" {
		t.Fatalf("list answer = %+v %v", r, ok)
	}
	if _, ok := answer("https://x.test/", nil); ok {
		t.Fatal("a collection that only shares the name's prefix was mocked")
	}

	// Rebuilding replaces the scenario's rules and their files, keeping it on.
	old := a.rules[1].Target
	mock("Rebuild scenario “Shop” from examples")
	if len(a.rules) != 4 || !a.rules[1].ScenarioActive || capture.ActiveScenario(a.rules) != "Shop" {
		t.Fatalf("rebuilt = %+v", a.rules)
	}
	if _, err := os.Stat(old); !os.IsNotExist(err) {
		t.Fatalf("replaced mock file kept: %v", err)
	}
	if saved, err := capture.LoadRules(a.rulesPath); err != nil || len(saved) != 4 {
		t.Fatalf("rules on disk = %d, %v", len(saved), err)
	}
}

func TestMockingAnExampleOpensARuleThatAnswersWithIt(t *testing.T) {
	a := workspaceApp()
	a.page = pageRequest
	a.rulesPath = filepath.Join(t.TempDir(), "rules.json")
	a.requests[0].URL = "{{host}}/projects"
	a.requests[0].Examples = []request.Example{{ID: "e1", Name: "Empty", Status: 200, Headers: "Content-Type: application/json", Body: `{"projects":[]}`,
		Request: request.ExampleRequest{Method: "GET", URL: "{{host}}/projects"}}}
	a.globals = map[string]string{"host": "https://api.example.com"}
	tt := ui.NewTester(a.view, 1280, 800)
	for _, label := range []string{"GET  List projects", "Examples", "Open example: Empty", "Mock this example"} {
		if err := tt.Click(label); err != nil {
			t.Fatal(label, err)
		}
	}
	r := a.ruleUI.draft
	if !a.toolsOpen || a.ruleUI.index != -1 || r.Type != capture.RuleMapLocal || r.Status != 200 || a.ruleUI.unsavedMock != r.Target {
		t.Fatalf("draft %+v (index %d, unsaved %q)", r, a.ruleUI.index, a.ruleUI.unsavedMock)
	}
	if !(rules.Matcher{URL: r.Match, URLMode: r.MatchMode}).Match(rules.Request{URL: "https://api.example.com/projects?limit=7"}) {
		t.Fatalf("pattern %q does not match the request", r.Match)
	}
	if data, err := os.ReadFile(r.Target); err != nil || string(data) != `{"projects":[]}` {
		t.Fatalf("mock file %q: %v", data, err)
	}
	if len(a.rules) != 0 {
		t.Fatal("the rule applied before it was saved")
	}
}
