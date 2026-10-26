package app

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"runtime"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/baba983/vscroxy/internal/capture"
	"github.com/egoist/mygo/ui"
)

func TestDebugHistoryLinksExactSendsAndSurvivesRestartAndReusedIDs(t *testing.T) {
	var hits atomic.Int32
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits.Add(1)
		fmt.Fprint(w, "unmocked result")
	}))
	defer upstream.Close()
	a := workspaceApp()
	a.srv = requestProxy(t)
	dir := t.TempDir()
	path := filepath.Join(dir, "experiments.json")
	a.loadExperiments(path)
	defer a.experiments.close()
	for i := range a.flows {
		a.flows[i].method, a.flows[i].url = "GET", upstream.URL+"/same"
		a.flows[i].resBody = []string{"baseline A", "baseline B"}[i]
	}
	a.applyFilter()
	session := filepath.Join(dir, "original.json")
	if err := writeSession(session, a.flows, nil); err != nil {
		t.Fatal(err)
	}
	rule, err := capture.NewMockRule(dir, capture.Flow{Method: "GET", URL: upstream.URL + "/same", Status: 201, ResponseBody: "mocked result"})
	if err != nil {
		t.Fatal(err)
	}
	rule.Scenario, rule.ScenarioActive = "Empty account", true
	if err := a.srv.SetRules([]capture.Rule{rule}); err != nil {
		t.Fatal(err)
	}
	a.composeFrom(a.flows[0])
	tt := ui.NewTester(a.view, 1280, 800)
	click := func(label string) {
		t.Helper()
		if err := tt.Click(label); err != nil {
			t.Fatal(err)
		}
	}
	copyText := func(label string) string {
		t.Helper()
		click(label)
		tt.Key(ui.Cmd, ui.KeyA)
		tt.Key(ui.Cmd, ui.KeyC)
		return tt.Clipboard()
	}
	waitRuns := func(n int) {
		t.Helper()
		guard := time.NewTimer(time.Minute)
		defer guard.Stop()
		for len(a.experiments.log.Records) < n {
			select {
			case <-guard.C:
				t.Fatal("send did not finish")
			default:
			}
			tt.Frame()
			runtime.Gosched()
		}
	}
	click("Rename request")
	tt.Key(ui.Cmd, ui.KeyA)
	tt.Type("Draft A")
	click("Done")
	click("Send")
	waitRuns(1)
	// Deliver the wire event after the client result, and change today's rules.
	// This must still show the actual scenario that produced the response.
	select {
	case wire := <-a.srv.Flows():
		a.pending = append(a.pending, wire)
	case <-time.After(time.Minute):
		t.Fatal("missing captured mock exchange")
	}
	if err := a.srv.SetRules(nil); err != nil {
		t.Fatal(err)
	}
	tt.Frame()
	click("Request options")
	if err := tt.ChooseMenuItem("Send history"); err != nil {
		t.Fatal(err)
	}
	if !tt.HasText("Recorded interventions: Scenario · Empty account · Map Local") {
		t.Fatalf("lost the applied scenario: %v", tt.Texts())
	}
	if copyText("Old text") != "baseline A" || copyText("New text") != "mocked result" {
		t.Fatal("source comparison did not use the original capture and exact send")
	}
	click("Open linked draft 1")
	click("Scripts")
	click("Pre-request script")
	tt.Type(`throw new Error("intentional preflight failure")`)
	click("Send")
	waitRuns(2)
	a.composeFrom(a.flows[1])
	tt.Frame()
	click("Rename request")
	tt.Key(ui.Cmd, ui.KeyA)
	tt.Type("Draft B")
	click("Done")
	click("Send")
	waitRuns(3)
	// Same URL, different capture: neither its draft nor run belongs to A.
	a.requestDocument = a.documents[0]
	tt.Frame()
	click("Request options")
	if err := tt.ChooseMenuItem("Send history"); err != nil {
		t.Fatal(err)
	}
	if tt.HasText("Experiment #3") || !tt.HasText("Experiment #1") || !tt.HasText("Experiment #2") || tt.HasText("Open linked draft 2") {
		t.Fatal("same-URL sources mixed their debugging histories")
	}
	click("Experiment #2")
	if !tt.HasText("No wire evidence retained yet; no rule outcome can be inferred.") {
		t.Fatal("failed preflight invented a wire exchange")
	}
	click("Experiment #1")
	click("Open originating draft")
	click("Scripts")
	if copyText("Pre-request script") != `throw new Error("intentional preflight failure")` {
		t.Fatal("returning to the originating draft overwrote its unsent edits")
	}
	click("Request options")
	if err := tt.ChooseMenuItem("Send history"); err != nil {
		t.Fatal(err)
	}
	click("Experiment #1")
	a.replaceSession([]flow{{id: 1, method: "GET", url: upstream.URL + "/same", resBody: "unrelated reused ID"}}, nil)
	tt.Frame()
	click("Inspect source capture") // disabled; must not navigate to reused ID 1
	if !tt.HasText("Original capture unavailable; snapshot retained.") || copyText("Old text") != "baseline A" {
		t.Fatal("a reused ID replaced the source snapshot or became a false link")
	}
	click("Close")
	a.experiments.close()
	b := workspaceApp()
	b.loadExperiments(path)
	defer b.experiments.close()
	tt = ui.NewTester(b.view, 960, 600)
	click("Request tab: Draft A")
	click("Request options")
	if err := tt.ChooseMenuItem("Send history"); err != nil {
		t.Fatal(err)
	}
	click("Experiment #1")
	if copyText("Old text") != "baseline A" || copyText("New text") != "mocked result" || tt.HasText("Experiment #3") {
		t.Fatal("restart lost source snapshots or history membership")
	}
	if !strings.Contains(strings.Join(tt.Texts(), "\n"), "Scenario · Empty account") {
		t.Fatal("restart lost recorded rule evidence")
	}
	flows, frames, err := readSession(session)
	if err != nil {
		t.Fatal(err)
	}
	b.replaceSession(flows, frames)
	tt.Frame()
	click("Inspect source capture")
	if tt.HasText("Source snapshot · Capture #1") || b.pickedID != 1 || b.workspace != 0 || hits.Load() != 1 {
		t.Fatal("saved-session link failed, or opening history sent a request")
	}
}
