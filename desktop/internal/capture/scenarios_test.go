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

	"github.com/baba983/vscroxy/desktop/internal/capture"
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
			preview := capture.PreviewRules(s.Rules(), target, "GET")
			if preview[tc.winner].Reason != "winner" || preview[8].Reason != fmt.Sprintf("shadowed by rule %d", tc.winner+1) || !strings.Contains(preview[2].Reason, "disabled") || preview[1].Reason != "independent breakpoint (requires Break)" || preview[6].Reason != "method does not match" || preview[0].Reason != "URL does not match" {
				t.Fatalf("incorrect preview: %+v", preview)
			}
			if tc.name != "Empty account" && !strings.Contains(preview[2].Reason, `inactive scenario "Empty account"`) {
				t.Fatalf("inactive and disabled reasons not both shown: %+v", preview[2])
			}
			scenarioRequest(t, client, "GET", target, tc.status, tc.body)
			scenarioRequest(t, client, "POST", target, 403, "blocked by vscroxy\n")
			scenarioRequest(t, client, "GET", upstream.URL+"/global", 403, "blocked by vscroxy\n")
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
	if p := capture.PreviewRules(s.Rules(), upstream.URL+"/item", "GET"); p[0].Reason != "winner" || p[3].Reason != "shadowed by rule 1" {
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
