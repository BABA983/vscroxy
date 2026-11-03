package capture_test

import (
	"bytes"
	"encoding/json/v2"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/baba983/vscroxy/internal/capture"
	"github.com/baba983/vscroxy/internal/rules"
)

func scenarioProxy(t *testing.T) (*capture.Server, *http.Client) {
	t.Helper()
	s, err := capture.New("127.0.0.1:0", t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = s.Close() })
	if err := s.Start(); err != nil {
		t.Fatal(err)
	}
	u, err := url.Parse("http://" + s.Addr())
	if err != nil {
		t.Fatal(err)
	}
	transport := &http.Transport{Proxy: http.ProxyURL(u)}
	t.Cleanup(transport.CloseIdleConnections)
	return s, &http.Client{Transport: transport}
}

func scenarioRequest(t *testing.T, client *http.Client, method, target string, status int, body string) {
	t.Helper()
	req, err := http.NewRequestWithContext(t.Context(), method, target, nil)
	if err != nil {
		t.Fatal(err)
	}
	resp, err := client.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	got, err := io.ReadAll(resp.Body)
	if err != nil || resp.StatusCode != status || string(got) != body {
		t.Fatalf("%s %s: %d %q, want %d %q (read: %v)", method, target, resp.StatusCode, got, status, body, err)
	}
}

func TestScenarioSavedSelectionAndPreviewFollowLiveProxy(t *testing.T) {
	var upstreamHits atomic.Int32
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		upstreamHits.Add(1)
		fmt.Fprint(w, "upstream")
	}))
	defer upstream.Close()
	remote := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(203)
		fmt.Fprint(w, "remote "+r.URL.RequestURI())
	}))
	defer remote.Close()
	dir := t.TempDir()
	local := func(name string, status int, scenario string) capture.Rule {
		t.Helper()
		r, err := capture.NewMockRule(dir, capture.Flow{Method: "GET", URL: upstream.URL + "/item", Path: "/item", Status: status, ResponseBody: name})
		if err != nil {
			t.Fatal(err)
		}
		r.Scenario = scenario
		return r
	}
	rules := []capture.Rule{
		{Type: capture.RuleBlock, Match: "/global"},
		{Type: capture.RuleBreak, Match: "/item", Method: "GET"},
		{Type: capture.RuleBlock, Match: "/item", Scenario: "Empty account", Disabled: true},
		local("empty", 201, "Empty account"),
		local("error", 503, "Server error"),
		{Type: capture.RuleMapRemote, Match: "/item", Method: "GET", Target: remote.URL, Scenario: "Remote"},
		{Type: capture.RuleBlock, Match: "/item", Method: "POST"},
		local("global fallback", 202, ""),
		{Type: capture.RuleBlock, Match: "/item"},
	}
	s, client := scenarioProxy(t)
	path := filepath.Join(dir, "rules.json")
	hits := map[int]int{} // GETs each winner answered, across the cases
	for _, tc := range []struct {
		name   string
		winner int
		status int
		body   string
	}{
		{"Empty account", 3, 201, "empty"},
		{"Server error", 4, 503, "error"},
		{"Remote", 5, 203, "remote /item?q=7"},
		{"", 7, 202, "global fallback"},
		{"Empty account", 3, 201, "empty"},
	} {
		t.Run(fmt.Sprintf("%s-%d", tc.name, tc.winner), func(t *testing.T) {
			next, err := capture.SelectScenario(rules, tc.name)
			if err != nil {
				t.Fatal(err)
			}
			if capture.ActiveScenario(rules) != "" { // selection must not mutate its input
				t.Fatal("selection mutated the editor's snapshot")
			}
			if err := capture.SaveRules(path, next); err != nil {
				t.Fatal(err)
			}
			loaded, err := capture.LoadRules(path)
			if err != nil || capture.ActiveScenario(loaded) != tc.name || !loaded[2].Disabled {
				t.Fatalf("selection/disabled state did not survive restart: %+v, %v", loaded, err)
			}
			if err := s.SetRules(loaded); err != nil {
				t.Fatal(err)
			}
			target := upstream.URL + "/item?q=7"
			preview := capture.PreviewRules(s.Rules(), previewRequest("GET", target), false)
			if preview[tc.winner].Step != 1 || preview[8].Reason != fmt.Sprintf("shadowed by rule %d", tc.winner+1) || !strings.Contains(preview[2].Reason, "disabled") || preview[1].Reason != "breakpoints are off" || preview[6].Reason != "method does not match" || preview[0].Reason != "URL does not match" {
				t.Fatalf("incorrect preview: %+v", preview)
			}
			if tc.name != "Empty account" && !strings.Contains(preview[2].Reason, `inactive scenario "Empty account"`) {
				t.Fatalf("inactive and disabled reasons not both shown: %+v", preview[2])
			}
			scenarioRequest(t, client, "GET", target, tc.status, tc.body)
			scenarioRequest(t, client, "POST", target, 403, "blocked by vscroxy\n")
			scenarioRequest(t, client, "GET", upstream.URL+"/global", 403, "blocked by vscroxy\n")
			hits[tc.winner]++
			if got := s.RuleHits(loaded[tc.winner]); got != hits[tc.winner] {
				t.Fatalf("rule %d counted %d hits, want %d", tc.winner+1, got, hits[tc.winner])
			}
			if s.RuleHits(loaded[8]) != 0 {
				t.Fatal("a shadowed rule counted a hit")
			}
			found := false
			for range 3 {
				select {
				case f := <-s.Flows():
					if f.Method == "GET" && f.URL == target {
						found = true
						if tc.name != "" && !slices.Contains(f.Actions, "Scenario · "+tc.name) {
							t.Fatalf("captured response lost the scenario that produced it: %+v", f.Actions)
						}
					}
				case <-t.Context().Done():
					t.Fatal(t.Context().Err())
				}
			}
			if !found {
				t.Fatal("the scenario response was not captured")
			}
		})
	}
	if upstreamHits.Load() != 0 {
		t.Fatal("mocked/blocked requests contacted the upstream")
	}
	scenarioRequest(t, client, "GET", upstream.URL+"/unmatched", 200, "upstream")
	// A global rule moved ahead of the active scenario still wins.
	next, err := capture.SelectScenario(rules, "Empty account")
	if err != nil {
		t.Fatal(err)
	}
	next[0], next[7] = next[7], next[0]
	if err := capture.SaveRules(path, next); err != nil {
		t.Fatal(err)
	}
	next, err = capture.LoadRules(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := s.SetRules(next); err != nil {
		t.Fatal(err)
	}
	scenarioRequest(t, client, "GET", upstream.URL+"/item", 202, "global fallback")
	if p := capture.PreviewRules(s.Rules(), previewRequest("GET", upstream.URL+"/item"), false); p[0].Step != 1 || p[3].Reason != "shadowed by rule 1" {
		t.Fatalf("global order diverged from proxy: %+v", p)
	}
}

func TestScenarioValidationIsIdenticalOnDiskAndRuntime(t *testing.T) {
	s, client := scenarioProxy(t)
	path := filepath.Join(t.TempDir(), "rules.json")
	baseline := []capture.Rule{{Type: capture.RuleBlock, Match: "/item"}}
	if err := capture.SaveRules(path, baseline); err != nil {
		t.Fatal(err)
	}
	loaded, err := capture.LoadRules(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := s.SetRules(loaded); err != nil {
		t.Fatal(err)
	}
	before, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	for name, invalid := range map[string][]capture.Rule{
		"multiple active":     {{Type: capture.RuleBlock, Match: "x", Scenario: "A", ScenarioActive: true}, {Type: capture.RuleBlock, Match: "x", Scenario: "B", ScenarioActive: true}},
		"partial active":      {{Type: capture.RuleBlock, Match: "x", Scenario: "A", ScenarioActive: true}, {Type: capture.RuleBlock, Match: "x", Scenario: "A"}},
		"ungrouped active":    {{Type: capture.RuleBlock, Match: "x", ScenarioActive: true}},
		"untrimmed":           {{Type: capture.RuleBlock, Match: "x", Scenario: " A "}},
		"blank":               {{Type: capture.RuleBlock, Match: "x", Scenario: " "}},
		"control":             {{Type: capture.RuleBlock, Match: "x", Scenario: "A\nB"}},
		"request breakpoint":  {{Type: capture.RuleBreak, Match: "x", Scenario: "A"}},
		"response breakpoint": {{Type: capture.RuleBreakResponse, Match: "x", Scenario: "A"}},
	} {
		t.Run(name, func(t *testing.T) {
			if err := capture.SaveRules(path, invalid); err == nil {
				t.Fatal("invalid configuration saved")
			}
			after, err := os.ReadFile(path)
			if err != nil || !bytes.Equal(before, after) {
				t.Fatal("failed save replaced the previous rules")
			}
			data, err := json.Marshal(invalid)
			if err != nil {
				t.Fatal(err)
			}
			badPath := filepath.Join(t.TempDir(), "invalid.json")
			if err := os.WriteFile(badPath, data, 0o600); err != nil {
				t.Fatal(err)
			}
			if _, err := capture.LoadRules(badPath); err == nil {
				t.Fatal("invalid configuration loaded")
			}
			snapshot := s.Rules()
			if err := s.SetRules(invalid); err == nil || !reflect.DeepEqual(snapshot, s.Rules()) {
				t.Fatal("runtime accepted invalid rules or lost the previous snapshot")
			}
			scenarioRequest(t, client, "GET", "http://127.0.0.1:1/item", 403, "blocked by vscroxy\n")
		})
	}
	if _, err := capture.SelectScenario(loaded, "missing"); err == nil {
		t.Fatal("unknown scenario accepted")
	}
}

func TestScenarioOldRulesFilesRemainGlobal(t *testing.T) {
	path := filepath.Join(t.TempDir(), "rules.json")
	if err := os.WriteFile(path, []byte(`[{"type":"block","match":"/item","method":"GET"}]`), 0o600); err != nil {
		t.Fatal(err)
	}
	rules, err := capture.LoadRules(path)
	if err != nil || capture.ActiveScenario(rules) != "" || rules[0].Disabled {
		t.Fatalf("old rules changed: %+v, %v", rules, err)
	}
	if err := capture.SaveRules(path, rules); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(path)
	if err != nil || bytes.Contains(data, []byte("scenario")) {
		t.Fatalf("scenario metadata added to an old file: %s, %v", data, err)
	}
	s, client := scenarioProxy(t)
	if err := s.SetRules(rules); err != nil {
		t.Fatal(err)
	}
	scenarioRequest(t, client, "GET", "http://127.0.0.1:1/item", 403, "blocked by vscroxy\n")
}

func TestScenarioSwitchDoesNotChangeAnInFlightResponse(t *testing.T) {
	entered, release := make(chan struct{}), make(chan struct{})
	var once sync.Once
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		close(entered)
		<-release
		fmt.Fprint(w, "original response")
	}))
	defer upstream.Close()
	defer once.Do(func() { close(release) })
	s, client := scenarioProxy(t)
	path := filepath.Join(t.TempDir(), "rules.json")
	rules := []capture.Rule{{Type: capture.RuleBlock, Match: "/item", Scenario: "Offline"}}
	if err := capture.SaveRules(path, rules); err != nil {
		t.Fatal(err)
	}
	loaded, err := capture.LoadRules(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := s.SetRules(loaded); err != nil {
		t.Fatal(err)
	}
	type result struct {
		body string
		err  error
	}
	done := make(chan result, 1)
	go func() {
		req, err := http.NewRequestWithContext(t.Context(), "GET", upstream.URL+"/item", nil)
		if err != nil {
			done <- result{err: err}
			return
		}
		resp, err := client.Do(req)
		if err != nil {
			done <- result{err: err}
			return
		}
		defer resp.Body.Close()
		body, err := io.ReadAll(resp.Body)
		done <- result{body: string(body), err: err}
	}()
	select {
	case <-entered:
	case got := <-done:
		t.Fatalf("request ended before reaching upstream: %+v", got)
	case <-t.Context().Done():
		t.Fatal(t.Context().Err())
	}
	next, err := capture.SelectScenario(loaded, "Offline")
	if err != nil {
		t.Fatal(err)
	}
	if err := capture.SaveRules(path, next); err != nil {
		t.Fatal(err)
	}
	next, err = capture.LoadRules(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := s.SetRules(next); err != nil {
		t.Fatal(err)
	}
	scenarioRequest(t, client, "GET", upstream.URL+"/item", 403, "blocked by vscroxy\n")
	once.Do(func() { close(release) })
	if got := <-done; got.err != nil || got.body != "original response" {
		t.Fatalf("in-flight response changed: %+v", got)
	}
}

func TestPreviewRulesFollowsTheProxysOrder(t *testing.T) {
	rules := []capture.Rule{
		{Type: capture.RuleBreakResponse, Match: "remote.test/v2/item"},
		{Type: capture.RuleBreak, Match: "/item"},
		{Type: capture.RuleBreak, Match: "/item"},
		{Type: capture.RuleMapRemote, Match: "/item", Target: "http://remote.test/v2"},
		{Type: capture.RuleBlock, Match: "/item"},
		{Type: capture.RuleBreakResponse, Match: "example.com"},
	}
	steps := func(p []capture.RulePreview) []int {
		var got []int
		for _, r := range p {
			got = append(got, r.Step)
		}
		return got
	}
	// The response breakpoint is listed first but acts last, on the URL the
	// request was redirected to.
	p := capture.PreviewRules(rules, previewRequest("GET", "https://example.com/item"), true)
	if got := steps(p); !slices.Equal(got, []int{3, 1, 0, 2, 0, 0}) {
		t.Fatalf("steps %v: %+v", got, p)
	}
	if p[2].Reason != "rule 2 pauses first" || p[4].Reason != "shadowed by rule 4" || p[5].Reason != "URL does not match" {
		t.Fatalf("reasons: %+v", p)
	}
	p = capture.PreviewRules(rules, previewRequest("GET", "https://example.com/item"), false)
	if got := steps(p); !slices.Equal(got, []int{0, 0, 0, 1, 0, 0}) || p[0].Reason != "breakpoints are off" {
		t.Fatalf("without breaking: %+v", p)
	}
	// A rule that answers the request itself leaves no response to pause.
	rules[3] = capture.Rule{Type: capture.RuleBlock, Match: "/item"}
	rules[0].Match = "/item"
	p = capture.PreviewRules(rules, previewRequest("GET", "https://example.com/item"), true)
	if got := steps(p); !slices.Equal(got, []int{0, 1, 0, 2, 0, 0}) || p[0].Reason != "rule 4 answers first" {
		t.Fatalf("answered: %+v", p)
	}
	// A hidden request still goes upstream, so a response breakpoint pauses it.
	hide := []capture.Rule{{Type: capture.RuleBlock, Match: "/item", Block: capture.BlockHide}, {Type: capture.RuleBreakResponse, Match: "/item"}}
	if got := steps(capture.PreviewRules(hide, previewRequest("GET", "https://example.com/item"), true)); !slices.Equal(got, []int{1, 2}) {
		t.Fatalf("hide: %v", got)
	}
	// Only Map Remote acts on a WebSocket: the Block above it does not shadow it.
	rules[5] = capture.Rule{Type: capture.RuleMapRemote, Match: "/item", Target: "http://remote.test"}
	p = capture.PreviewRules(rules, previewRequest("GET", "wss://example.com/item"), true)
	if got := steps(p); !slices.Equal(got, []int{0, 0, 0, 0, 0, 1}) || p[3].Reason != "does not act on WebSockets" || p[1].Reason != "does not act on WebSockets" {
		t.Fatalf("WebSocket: %+v", p)
	}
	// Message hooks match the mapped URL; breakpoints win separately per
	// direction, even when the Map Remote rule is listed after them.
	wsRules := []capture.Rule{
		{Type: capture.RuleScript, Match: "remote.test/v2/item", Target: "message.js"},
		{Type: capture.RuleBreakMessage, Match: "remote.test/v2/item", Direction: "out"},
		{Type: capture.RuleBreakMessage, Match: "remote.test/v2/item"},
		{Type: capture.RuleBreakMessage, Match: "remote.test/v2/item", Direction: "in"},
		{Type: capture.RuleBreak, Match: "/item"},
		{Type: capture.RuleMapRemote, Match: "example.com/item", Target: "https://remote.test/v2"},
		{Type: capture.RuleScript, Match: "example.com/item", Target: "original.js"},
	}
	p = capture.PreviewRules(wsRules, previewRequest("GET", "wss://example.com/item"), true)
	if got := steps(p); !slices.Equal(got, []int{2, 3, 4, 0, 0, 1, 0}) || p[3].Reason == "" || p[6].Reason != "URL does not match" {
		t.Fatalf("WebSocket message stages: %+v", p)
	}
	wsRules[1].Direction = "sideways"
	if err := capture.ValidateRules(wsRules); err == nil {
		t.Fatal("invalid message direction accepted")
	}
}

func previewRequest(method, raw string) rules.Request {
	u, _ := url.Parse(raw)
	return rules.Request{URL: raw, Method: method, Host: u.Hostname()}
}

func TestRulesMatchWildcardsRegexAppsAndHeadersOnTheProxy(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, "upstream")
	}))
	defer upstream.Close()
	s, client := scenarioProxy(t)
	list := []capture.Rule{
		// No test client is this app, so the rule never applies.
		{Type: capture.RuleBlock, Match: "/a", Apps: []string{"no-such-app"}},
		{Type: capture.RuleBlock, Match: "/a", When: []rules.HeaderCondition{{Name: "X-Env", Op: rules.HeaderEquals, Value: "staging"}}},
		{Type: capture.RuleBlock, MatchMode: rules.URLWildcard, Match: "127.0.0.1:*/w/*/x"},
		{Type: capture.RuleBlock, MatchMode: rules.URLRegex, Match: `/r/\d+$`},
	}
	if err := s.SetRules(list); err != nil {
		t.Fatal(err)
	}
	do := func(path string, header http.Header, status int) {
		t.Helper()
		req, err := http.NewRequestWithContext(t.Context(), "GET", upstream.URL+path, nil)
		if err != nil {
			t.Fatal(err)
		}
		req.Header = header
		resp, err := client.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		resp.Body.Close()
		if resp.StatusCode != status {
			t.Fatalf("GET %s %v: %d, want %d", path, header, resp.StatusCode, status)
		}
	}
	do("/a", nil, 200)
	do("/a", http.Header{"X-Env": {"prod"}}, 200)
	do("/a", http.Header{"X-Env": {"staging"}}, 403)
	do("/w/1/x", nil, 403)
	do("/w/1/x/y", nil, 200)
	do("/r/42", nil, 403)
	do("/r/42/y", nil, 200)
	if s.RuleHits(list[0]) != 0 || s.RuleHits(list[1]) != 1 {
		t.Fatal("hits went to the wrong rules")
	}
	bad := []capture.Rule{{Type: capture.RuleBlock, MatchMode: rules.URLRegex, Match: "("}}
	if err := s.SetRules(bad); err == nil || !strings.Contains(err.Error(), "URL regex") {
		t.Fatalf("an invalid regex was accepted: %v", err)
	}
}

func TestBlockRefusesDropsOrHides(t *testing.T) {
	var hits atomic.Int32
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits.Add(1)
		fmt.Fprint(w, "upstream")
	}))
	defer upstream.Close()
	s, client := scenarioProxy(t)
	list := []capture.Rule{
		{Type: capture.RuleBlock, Match: "/refuse"},
		{Type: capture.RuleBlock, Match: "/drop", Block: capture.BlockDrop},
		{Type: capture.RuleBlock, Match: "/hide", Block: capture.BlockHide},
	}
	if err := s.SetRules(list); err != nil {
		t.Fatal(err)
	}
	next := func() capture.Flow {
		t.Helper()
		select {
		case f := <-s.Flows():
			return f
		case <-t.Context().Done():
			t.Fatal(t.Context().Err())
		}
		return capture.Flow{}
	}
	scenarioRequest(t, client, "GET", upstream.URL+"/refuse", 403, "blocked by vscroxy\n")
	if f := next(); f.Status != 403 {
		t.Fatalf("refused row: %+v", f.Status)
	}
	// A dropped request gets no answer at all and never reaches the server.
	// POST: a client retries an idempotent request on a closed connection.
	req, _ := http.NewRequestWithContext(t.Context(), "POST", upstream.URL+"/drop", nil)
	if resp, err := client.Do(req); err == nil {
		resp.Body.Close()
		t.Fatalf("a dropped request was answered %d", resp.StatusCode)
	}
	if f := next(); f.Conn.Error == "" || !slices.ContainsFunc(f.Actions, func(a string) bool { return strings.HasPrefix(a, "Block · drop") }) {
		t.Fatalf("dropped row: error %q, actions %v", f.Conn.Error, f.Actions)
	}
	// A hidden request goes through but leaves no row.
	scenarioRequest(t, client, "GET", upstream.URL+"/hide", 200, "upstream")
	scenarioRequest(t, client, "GET", upstream.URL+"/shown", 200, "upstream")
	if f := next(); !strings.HasSuffix(f.URL, "/shown") {
		t.Fatalf("the hidden request has a row: %s", f.URL)
	}
	if hits.Load() != 2 || s.RuleHits(list[1]) != 1 || s.RuleHits(list[2]) != 1 {
		t.Fatalf("upstream hits %d, rule hits %d %d", hits.Load(), s.RuleHits(list[1]), s.RuleHits(list[2]))
	}
	if err := s.SetRules([]capture.Rule{{Type: capture.RuleBlock, Match: "/x", Block: "tarpit"}}); err == nil {
		t.Fatal("an unknown Block action was accepted")
	}
}

func TestMapLocalFolderAnswersFromItsFilesOrTheServer(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, "upstream")
	}))
	defer upstream.Close()
	root := t.TempDir()
	dir := filepath.Join(root, "site")
	for name, body := range map[string]string{
		"site/users/42.json": `{"id":42}`,
		"site/index.html":    "home",
		"secret.txt":         "outside the folder",
	} {
		p := filepath.Join(root, filepath.FromSlash(name))
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	s, client := scenarioProxy(t)
	rule := capture.Rule{Type: capture.RuleMapLocal, Match: "/api/", Target: dir, PathFrom: "/api"}
	if err := s.SetRules([]capture.Rule{rule}); err != nil {
		t.Fatal(err)
	}
	get := func(path string) (*http.Response, string) {
		t.Helper()
		req, _ := http.NewRequestWithContext(t.Context(), "GET", upstream.URL+path, nil)
		req.URL.Opaque = "//" + req.URL.Host + path // keep .. for the proxy to see
		resp, err := client.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		defer resp.Body.Close()
		body, _ := io.ReadAll(resp.Body)
		return resp, string(body)
	}
	if resp, body := get("/api/users/42.json"); body != `{"id":42}` || resp.Header.Get("Content-Type") != "application/json" {
		t.Fatalf("file: %q %q", body, resp.Header.Get("Content-Type"))
	}
	if _, body := get("/api/"); body != "home" {
		t.Fatalf("index: %q", body)
	}
	if _, body := get("/api/users/7.json"); body != "upstream" {
		t.Fatalf("a missing file was not left to the server: %q", body)
	}
	if _, body := get("/api/../secret.txt"); body == "outside the folder" {
		t.Fatal("a path escaped the folder")
	}
	if s.RuleHits(rule) != 2 {
		t.Fatalf("hits %d, want the two answered", s.RuleHits(rule))
	}
}
