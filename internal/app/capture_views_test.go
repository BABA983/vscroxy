package app

import (
	"bytes"
	"context"
	"fmt"
	"github.com/baba983/vscroxy/internal/uikit"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/egoist/mygo/ui"
)

// captureViewsApp is an app with a views object loaded from a file in a
// temporary directory, and the flows the test cares about.
func captureViewsApp(t *testing.T, flows ...flow) (*app, *captureViews) {
	t.Helper()
	a := workspaceApp()
	if len(flows) > 0 {
		a.flows = flows
		a.countDomains()
		a.countClients()
		a.applyFilter()
	}
	v := &captureViews{}
	v.init(a, filepath.Join(t.TempDir(), "views.json"))
	return a, v
}

// captureViewsFrame draws what this file owns, the way the app wires it: the
// results come in, the widgets' edits go to the view, the controls draw, and
// the frame's edits go back to the view.
func captureViewsFrame(a *app, v *captureViews) func(*ui.Context) {
	return func(c *ui.Context) {
		c.SetTheme(uikit.Theme(a.dark))
		t := c.Theme()
		v.drain(a)
		v.sync(a)
		ui.Column(c).Fill().Background(t.Background).Children(func() {
			v.sidebar(c, t, a)
			ui.Text(c, fmt.Sprintf("rows: %d", len(a.visible)))
			ui.Text(c, "search: "+a.query)
			ui.Text(c, "filter: "+a.filter)
		})
		v.modals(c, t, a)
		v.capture(a)
	}
}

// settle renders frames until the body search has answered every flow and
// every answer has been applied. Progress is read before the drain: an answer
// that comes in between would otherwise count as done without being applied.
func settle(t *testing.T, a *app, v *captureViews) {
	t.Helper()
	deadline := time.Now().Add(20 * time.Second)
	for {
		s := v.run
		if s == nil {
			return
		}
		done, total := s.progress()
		v.drain(a)
		if done >= total {
			return
		}
		if time.Now().After(deadline) {
			note, _ := v.status()
			t.Fatalf("the body search did not finish: %s", note)
		}
		time.Sleep(2 * time.Millisecond)
	}
}

func visibleIDs(a *app) []int {
	ids := make([]int, 0, len(a.visible))
	for _, at := range a.visible {
		ids = append(ids, a.flows[at].id)
	}
	return ids
}

// TestCaptureViewsKeepTheirOwnConditions checks that narrowing one view leaves
// another as it was, in both directions, and that closing one keeps the rest.
func TestCaptureViewsKeepTheirOwnConditions(t *testing.T) {
	a, v := captureViewsApp(t)
	tt := ui.NewTester(captureViewsFrame(a, v), 1100, 620)
	click := func(label string) {
		t.Helper()
		if err := tt.Click(label); err != nil {
			t.Fatal(err)
		}
	}
	// The first view narrows to the failing exchange, which it keeps while
	// another view shows everything.
	a.filter = "errors"
	tt.Frame()
	if got := visibleIDs(a); len(got) != 1 || got[0] != 2 {
		t.Fatalf("the error filter showed %v", got)
	}
	click("New view")
	if a.filter != "all" || a.query != "" {
		t.Fatalf("a new view inherited %q and %q", a.filter, a.query)
	}
	if got := visibleIDs(a); len(got) != 2 {
		t.Fatalf("the new view showed %v", got)
	}
	// The second view gets its own search.
	a.query = "login"
	tt.Frame()
	if got := visibleIDs(a); len(got) != 1 || got[0] != 2 {
		t.Fatalf("the search showed %v", got)
	}
	click("Capture view: View 1")
	if a.filter != "errors" || a.query != "" {
		t.Fatalf("switching back gave the first view %q and %q", a.filter, a.query)
	}
	if got := visibleIDs(a); len(got) != 1 || got[0] != 2 {
		t.Fatalf("the first view showed %v after switching back", got)
	}
	click("Capture view: View 2")
	if a.query != "login" || a.filter != "all" {
		t.Fatalf("the second view came back with %q and %q", a.query, a.filter)
	}
	// Closing the second view leaves the first one's conditions in place.
	click("Close view: View 2")
	if len(v.views) != 1 || a.filter != "errors" {
		t.Fatalf("closing a view left %d views and %q", len(v.views), a.filter)
	}
	if got := visibleIDs(a); len(got) != 1 || got[0] != 2 {
		t.Fatalf("the remaining view showed %v", got)
	}
}

// TestSavedCaptureFiltersSurviveRestart saves the active view's conditions by
// name, then reads them in a fresh app and applies them.
func TestSavedCaptureFiltersSurviveRestart(t *testing.T) {
	a, v := captureViewsApp(t)
	tt := ui.NewTester(captureViewsFrame(a, v), 1100, 620)
	click := func(label string) {
		t.Helper()
		if err := tt.Click(label); err != nil {
			t.Fatal(err)
		}
	}
	a.filter = "errors"
	a.filterStatus = "4xx"
	a.filterTriage = captureTriage{Host: "api.example.com", Client: "Browser"}
	tt.Frame()
	click("Saved filters…")
	click("Filter name")
	tt.Key(ui.Cmd, ui.KeyA)
	tt.Type("Failing calls")
	click("Save filter")
	if len(v.filters) != 1 || v.filters[0].Name != "Failing calls" {
		t.Fatalf("saving kept %v", v.filters)
	}
	if !tt.HasText(`Saved "Failing calls".`) {
		t.Fatalf("saving did not report what it did: %v", tt.Texts())
	}
	saved, err := loadCaptureFilters(v.path)
	if err != nil || len(saved) != 1 {
		t.Fatalf("the file holds %v (%v)", saved, err)
	}
	if saved[0].Filter != "errors" || saved[0].Status != "4xx" || saved[0].Triage != a.filterTriage {
		t.Fatalf("the saved filter lost its conditions: %+v", saved[0])
	}
	// A restart reads the file back.
	a2 := workspaceApp()
	v2 := &captureViews{}
	v2.init(a2, v.path)
	if len(v2.filters) != 1 || v2.filters[0].Name != "Failing calls" {
		t.Fatalf("the restart kept %v", v2.filters)
	}
	if v2.problem != "" {
		t.Fatalf("the restart reported %q", v2.problem)
	}
	tt2 := ui.NewTester(captureViewsFrame(a2, v2), 1100, 620)
	click2 := func(label string) {
		t.Helper()
		if err := tt2.Click(label); err != nil {
			t.Fatal(err)
		}
	}
	click2("Saved filters…")
	click2(`Apply "Failing calls"`)
	if a2.filter != "errors" || a2.filterStatus != "4xx" || a2.filterTriage != a.filterTriage {
		t.Fatalf("applying gave %q, %q and %+v", a2.filter, a2.filterStatus, a2.filterTriage)
	}
	if got := visibleIDs(a2); len(got) != 1 || got[0] != 2 {
		t.Fatalf("the applied filter showed %v", got)
	}
	// The tab names the filter it came from, and the filter can be renamed and
	// deleted without touching the view.
	click2(`Rename "Failing calls"`)
	click2("Filter name")
	tt2.Key(ui.Cmd, ui.KeyA)
	tt2.Type("Server errors")
	click2("Rename filter")
	if len(v2.filters) != 1 || v2.filters[0].Name != "Server errors" {
		t.Fatalf("renaming kept %v", v2.filters)
	}
	if !tt2.HasText("Capture view: Server errors") {
		t.Fatalf("the view did not follow the rename: %v", tt2.Texts())
	}
	click2(`Delete "Server errors"`)
	if len(v2.filters) != 0 {
		t.Fatalf("deleting kept %v", v2.filters)
	}
	again, err := loadCaptureFilters(v.path)
	if err != nil || len(again) != 0 {
		t.Fatalf("the file holds %v (%v)", again, err)
	}
	if a2.filter != "errors" {
		t.Fatalf("deleting a filter changed the view to %q", a2.filter)
	}
}

// TestSavedCaptureFilterWriteFailureIsVisible checks that a file that cannot be
// written is reported, and never claimed as saved.
func TestSavedCaptureFilterWriteFailureIsVisible(t *testing.T) {
	a, v := captureViewsApp(t)
	blocked := filepath.Join(t.TempDir(), "not-a-directory")
	if err := os.WriteFile(blocked, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	v.path = filepath.Join(blocked, "views.json")
	tt := ui.NewTester(captureViewsFrame(a, v), 1100, 620)
	click := func(label string) {
		t.Helper()
		if err := tt.Click(label); err != nil {
			t.Fatal(err)
		}
	}
	click("Saved filters…")
	click("Filter name")
	tt.Key(ui.Cmd, ui.KeyA)
	tt.Type("Lost")
	click("Save filter")
	if v.problem == "" || !tt.HasText("Could not save filters") {
		t.Fatalf("a failed write was not reported: %q, %v", v.problem, tt.Texts())
	}
	if tt.HasText(`Saved "Lost".`) {
		t.Fatal("a failed write claimed to have saved the filter")
	}
	if len(v.filters) != 0 {
		t.Fatalf("a failed write changed the filters to %v", v.filters)
	}
}

// TestCaptureViewBodyFilterSearchesWholeSpilledBodies checks that the body
// condition reads a spilled body past the 8 MiB the preview shows, that the
// two sides are never joined, and that a body that cannot be read is reported
// instead of being guessed from its head.
func TestCaptureViewBodyFilterSearchesWholeSpilledBodies(t *testing.T) {
	dir := t.TempDir()
	spilled := filepath.Join(dir, "response.bin")
	// The needle sits past the preview limit, at the end of the file.
	body := append(bytes.Repeat([]byte("padding\n"), 9<<20/(8)), []byte("DeepNeedle")...)
	if err := os.WriteFile(spilled, body, 0o600); err != nil {
		t.Fatal(err)
	}
	gone := filepath.Join(dir, "gone.bin")
	a, v := captureViewsApp(t,
		flow{id: 1, method: "GET", host: "api.example.com", path: "/big", url: "https://api.example.com/big", status: 200, resFile: spilled, resSize: len(body)},
		flow{id: 2, method: "GET", host: "api.example.com", path: "/small", url: "https://api.example.com/small", status: 200, resBody: "a small DeepNeedle"},
		flow{id: 3, method: "POST", host: "api.example.com", path: "/split", url: "https://api.example.com/split", status: 200, reqBody: "Deep", resBody: "Needle"},
		flow{id: 4, method: "GET", host: "api.example.com", path: "/gone", url: "https://api.example.com/gone", status: 200, resBody: "a small DeepNeedle", resFile: gone, resSize: 20},
	)
	if strings.Contains(a.flows[0].body(true), "DeepNeedle") {
		t.Fatal("the preview already held the needle, so the search proves nothing")
	}
	v.activeView().body = "deepneedle"
	v.applyFilter(a)
	settle(t, a, v)
	got := visibleIDs(a)
	if len(got) != 2 || got[0] != 1 || got[1] != 2 {
		t.Fatalf("the body condition showed %v: the spill file and the head should match, the split sides and the missing file should not", got)
	}
	if note, failure := v.status(); !failure || !strings.Contains(note, "1 body could not be read in full") {
		t.Fatalf("the unreadable body was not reported: %q (%t)", note, failure)
	}
	// The same view's rows come back without reading anything again.
	v.applyFilter(a)
	if got := visibleIDs(a); len(got) != 2 || got[0] != 1 || got[1] != 2 {
		t.Fatalf("a second pass showed %v", got)
	}
	// A pass that does not know the body condition, as the app's own filter
	// makes on a sort click, is corrected by the next frame.
	a.applyFilter()
	if got := visibleIDs(a); len(got) != 4 {
		t.Fatalf("the app's own pass showed %v", got)
	}
	tt := ui.NewTester(captureViewsFrame(a, v), 1100, 620)
	tt.Frame()
	if got := visibleIDs(a); len(got) != 2 || got[0] != 1 || got[1] != 2 {
		t.Fatalf("the frame after the app's own pass showed %v", got)
	}
	// Hooked at the end of the app's filter, the same pass keeps the body
	// condition without waiting for a frame.
	a.applyFilter()
	v.narrow(a)
	if got := visibleIDs(a); len(got) != 2 || got[0] != 1 || got[1] != 2 {
		t.Fatalf("narrowed in the app's own pass, the view showed %v", got)
	}
	// The other view is untouched by the body condition.
	v.capture(a)
	v.newView(a)
	if got := visibleIDs(a); len(got) != 4 {
		t.Fatalf("the new view showed %v", got)
	}
	v.activate(a, 0)
	if a.filterBody != "" || v.activeView().body != "deepneedle" {
		t.Fatalf("the body condition did not stay with its view: %q", a.filterBody)
	}
	if got := visibleIDs(a); len(got) != 2 || got[0] != 1 || got[1] != 2 {
		t.Fatalf("switching back showed %v", got)
	}
	// Clearing the view's conditions reaches the body one too.
	v.clearFilters(a)
	if v.activeView().body != "" || a.filterBody != "" {
		t.Fatalf("clearing left the body condition %q", v.activeView().body)
	}
	if got := visibleIDs(a); len(got) != 4 {
		t.Fatalf("after clearing, the view showed %v", got)
	}
}

// TestCaptureViewBodyFilterReportsATruncatedHead checks that a flow that kept
// only the head of a body, with no file holding the rest, is reported rather
// than matched on what it kept.
func TestCaptureViewBodyFilterReportsATruncatedHead(t *testing.T) {
	file := filepath.Join(t.TempDir(), "incomplete-body")
	if err := os.WriteFile(file, []byte("needle"), 0600); err != nil {
		t.Fatal(err)
	}
	a, v := captureViewsApp(t,
		flow{id: 1, method: "GET", host: "api.example.com", path: "/cut", url: "https://api.example.com/cut", status: 200, resBody: "the needle is in the head", resSize: 1 << 20},
		flow{id: 2, method: "GET", host: "api.example.com", path: "/cut-file", url: "https://api.example.com/cut-file", status: 200, resBody: "needle", resFile: file, resSize: 1 << 20},
	)
	v.activeView().body = "needle"
	v.applyFilter(a)
	settle(t, a, v)
	if got := visibleIDs(a); len(got) != 0 {
		t.Fatalf("the head of a cut body matched: %v", got)
	}
	if note, failure := v.status(); !failure || !strings.Contains(note, "2 bodies could not be read in full") {
		t.Fatalf("the cut body was not reported: %q (%t)", note, failure)
	}
}

func TestCaptureViewControlsAreWiredIntoTheWorkspace(t *testing.T) {
	a := workspaceApp()
	a.views.init(a, filepath.Join(t.TempDir(), "views.json"))
	t.Cleanup(a.views.stopSearch)
	tt := ui.NewTester(a.view, 1100, 700)
	click := func(label string) {
		t.Helper()
		if err := tt.Click(label); err != nil {
			t.Fatal(err)
		}
	}
	click("Filters…")
	click("Body contains")
	tt.Type("projects")
	click("Apply")
	settle(t, a, &a.views)
	tt.Frame()
	if got := visibleIDs(a); len(got) != 1 || got[0] != 1 || !a.filtersActive() {
		t.Fatalf("workspace body filter: %v", got)
	}
	click("New view")
	if len(a.visible) != 2 || a.filtersActive() {
		t.Fatal("new workspace view inherited another view's body filter")
	}
	click("Capture view: View 1")
	settle(t, a, &a.views)
	tt.Frame()
	if len(a.visible) != 1 || !strings.Contains(a.filterSummary(), "body projects") {
		t.Fatal("returning to a view lost its body condition")
	}
	a.applyFilter()
	if len(a.visible) != 1 {
		t.Fatal("a table rebuild dropped the view's body filter")
	}
}

// TestCaptureViewBodySearchIsCancellable checks that a new body condition
// cancels the search in flight, that pending rows are not shown as matches,
// and that the answer for the new condition is the one applied.
func TestCaptureViewBodySearchIsCancellable(t *testing.T) {
	a, v := captureViewsApp(t,
		flow{id: 1, method: "GET", host: "api.example.com", path: "/alpha", url: "https://api.example.com/alpha", status: 200, resBody: "alpha"},
		flow{id: 2, method: "GET", host: "api.example.com", path: "/beta", url: "https://api.example.com/beta", status: 200, resBody: "beta"},
	)
	started := make(chan string, 8)
	cancelled := make(chan struct{}, 8)
	var calls atomic.Int64
	v.search = func(ctx context.Context, job bodyJob, needle string) (bool, error) {
		calls.Add(1)
		if needle != "alpha" {
			return strings.Contains(job.resHead, needle), nil
		}
		started <- needle
		<-ctx.Done()
		cancelled <- struct{}{}
		return false, ctx.Err()
	}
	v.activeView().body = "alpha"
	v.applyFilter(a)
	// A frame that rebuilds the rows must not hand a body being read to a
	// second reader.
	v.applyFilter(a)
	if note, failure := v.status(); failure || !strings.Contains(note, "Searching bodies…") {
		t.Fatalf("the search in flight was not shown: %q", note)
	}
	if len(a.visible) != 0 {
		t.Fatalf("a row was shown before its body was read: %v", visibleIDs(a))
	}
	for range 2 {
		select {
		case <-started:
		case <-time.After(5 * time.Second):
			t.Fatal("the search never started")
		}
	}
	// Give a duplicate reader the chance to show up.
	time.Sleep(20 * time.Millisecond)
	if n := calls.Load(); n != 2 {
		t.Fatalf("two flows were handed to the reader %d times", n)
	}
	// A new condition cancels the one in flight instead of queueing behind it.
	v.activeView().body = "beta"
	v.applyFilter(a)
	select {
	case <-cancelled:
	case <-time.After(5 * time.Second):
		t.Fatal("the superseded search was not cancelled")
	}
	settle(t, a, v)
	if got := visibleIDs(a); len(got) != 1 || got[0] != 2 {
		t.Fatalf("the new condition showed %v", got)
	}
	if note, failure := v.status(); note != "" || failure {
		t.Fatalf("the finished search still reports %q (%t)", note, failure)
	}
}

// TestBodyFileSearchReadsChunks checks the reader itself: a match that straddles
// two reads, non-ASCII letters in either case, and a read that cuts a rune.
func TestBodyFileSearchReadsChunks(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "body.txt")
	if err := os.WriteFile(path, []byte("Ünïcode ÉTÉ payload"), 0o600); err != nil {
		t.Fatal(err)
	}
	for _, chunk := range []int{1, 2, 3, 4, 5, 7, 11, 64} {
		for _, needle := range []string{"été", "ÉTÉ", "ünïcode", "ÜNÏCODE", "code Été pay"} {
			ok, err := fileContains(t.Context(), path, needle, chunk)
			if err != nil || !ok {
				t.Fatalf("chunk %d: %q was not found (%v)", chunk, needle, err)
			}
		}
		if ok, err := fileContains(t.Context(), path, "absent", chunk); err != nil || ok {
			t.Fatalf("chunk %d: found a needle that is not there (%v)", chunk, err)
		}
	}
	// A cancelled search stops reading.
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if _, err := fileContains(ctx, path, "payload", 4); err == nil {
		t.Fatal("a cancelled search kept reading")
	}
}

// TestSearchBodyDoesNotJoinSides checks that a needle spanning the end of the
// request and the start of the response is not a match.
func TestSearchBodyDoesNotJoinSides(t *testing.T) {
	job := bodyJob{reqHead: "the request ends with nee", resHead: "dle and the response starts"}
	if ok, err := searchBody(t.Context(), job, "needle"); err != nil || ok {
		t.Fatalf("the sides were joined: %t (%v)", ok, err)
	}
	if ok, err := searchBody(t.Context(), job, "response"); err != nil || !ok {
		t.Fatalf("a side was not searched: %t (%v)", ok, err)
	}
}

func TestLegacySidebarFiltersCanCombineWithNewFacets(t *testing.T) {
	a, v := captureViewsApp(t,
		flow{id: 1, host: "api.example.com", process: "Browser"},
		flow{id: 2, host: "api.example.com", process: "CLI"},
		flow{id: 3, host: "other.example.com", process: "Browser"},
	)
	if err := os.WriteFile(v.path, []byte(`[{"name":"API","filter":"host:api.example.com"},{"name":"Browser","filter":"client:Browser"}]`), 0o600); err != nil {
		t.Fatal(err)
	}
	v.load()
	tt := ui.NewTester(captureViewsFrame(a, v), 1100, 620)
	if err := tt.Click("Quick filter: API"); err != nil {
		t.Fatal(err)
	}
	if got := fmt.Sprint(visibleIDs(a)); got != "[1 2]" {
		t.Fatalf("legacy host filter showed %s", got)
	}
	v.openFilter(a)
	tt.Frame()
	if err := tt.Click("Filter app"); err != nil {
		t.Fatal(err)
	}
	tt.Type("Browser")
	if err := tt.Click("Apply"); err != nil {
		t.Fatal(err)
	}
	if got := fmt.Sprint(visibleIDs(a)); got != "[1]" {
		t.Fatalf("new app facet replaced legacy host filter: %s", got)
	}
	if err := tt.Click("Quick filter: Browser"); err != nil {
		t.Fatal(err)
	}
	if got := fmt.Sprint(visibleIDs(a)); got != "[1 3]" {
		t.Fatalf("legacy app filter showed %s", got)
	}
}
