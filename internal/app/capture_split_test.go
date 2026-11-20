package app

import (
	"context"
	"fmt"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/baba983/vscroxy/locales"
	"github.com/egoist/mygo/ui"
)

// splitTestApp has four flows, two filters a group can hold, and a split
// already open, with the second group in focus as it is after Split Right.
func splitTestApp(t *testing.T) *app {
	t.Helper()
	a := workspaceApp()
	a.flows = append(a.flows,
		flow{id: 3, method: "POST", scheme: "https", host: "api.example.com", path: "/login", url: "https://api.example.com/login", status: 500, process: "Browser"},
		flow{id: 4, method: "GET", scheme: "https", host: "other.test", path: "/health", url: "https://other.test/health", status: 200, process: "curl"},
	)
	a.recount()
	a.applyFilter()
	a.views.init(a, filepath.Join(t.TempDir(), "views.json"))
	a.recount()
	a.applyFilter()
	a.views.splitRight(a)
	return a
}

func groupIDs(a *app, group int) []int {
	view := a.views.viewByID(a.views.groups[group])
	if view == nil {
		return nil
	}
	var rows []int
	a.views.withView(a, view, func() {
		for _, at := range a.visible {
			rows = append(rows, a.flows[at].id)
		}
	})
	return rows
}

func groupChosen(a *app, group int) []int {
	view := a.views.viewByID(a.views.groups[group])
	if view == nil {
		return nil
	}
	var ids []int
	for id := range view.chosen.All() {
		ids = append(ids, id)
	}
	slices.Sort(ids)
	return ids
}

func groupFilter(a *app, group int, filter, query string) {
	a.views.focusCaptureGroup(a, group)
	a.filter, a.query = filter, query
	a.views.capture(a)
	a.applyFilter()
}

func TestCaptureGroupsKeepFiltersAndRowsFromTheSameStore(t *testing.T) {
	a := splitTestApp(t)
	groupFilter(a, 0, "errors", "")
	groupFilter(a, 1, "host:other.test", "")
	if got := groupIDs(a, 0); !slices.Equal(got, []int{2, 3}) {
		t.Fatalf("group 1 showed %v, want the failing rows", got)
	}
	if got := groupIDs(a, 1); !slices.Equal(got, []int{4}) {
		t.Fatalf("group 2 showed %v, want the other host", got)
	}
	// A new flow on each host arrives in only the group that matches it.
	a.flows = append(a.flows,
		flow{id: 5, method: "GET", host: "api.example.com", path: "/broken", status: 503},
		flow{id: 6, method: "GET", host: "other.test", path: "/ready", status: 200},
	)
	a.recount()
	a.showArrivals([]int{4, 5})
	if got := groupIDs(a, 0); !slices.Equal(got, []int{2, 3, 5}) {
		t.Fatalf("group 1 missed arriving traffic: %v", got)
	}
	if got := groupIDs(a, 1); !slices.Equal(got, []int{4, 6}) {
		t.Fatalf("group 2 missed arriving traffic: %v", got)
	}
	// Filtering one group never rewrites the other's conditions.
	groupFilter(a, 0, "all", "login")
	if got := groupIDs(a, 0); !slices.Equal(got, []int{2, 3}) {
		t.Fatalf("group 1 search showed %v", got)
	}
	if got := groupIDs(a, 1); !slices.Equal(got, []int{4, 6}) {
		t.Fatalf("group 2 changed when group 1 filtered: %v", got)
	}
	view2 := a.views.viewByID(a.views.groups[1])
	if view2.filter != "host:other.test" || view2.query != "" {
		t.Fatalf("group 2 filter = %q/%q", view2.filter, view2.query)
	}
}

func TestCaptureGroupsKeepSelectionAndDeleteSeparate(t *testing.T) {
	a := splitTestApp(t)
	groupFilter(a, 0, "errors", "")
	groupFilter(a, 1, "host:other.test", "")
	view1, view2 := a.views.viewByID(a.views.groups[0]), a.views.viewByID(a.views.groups[1])

	a.views.focusCaptureGroup(a, 0)
	a.pickedID, a.chosen = 2, func() (s ui.Selection[int]) { s.Add(2); return }()
	a.views.captureGroupFields(a, view1)
	a.views.focusCaptureGroup(a, 1)
	a.pickedID, a.chosen = 4, func() (s ui.Selection[int]) { s.Add(4); return }()
	a.views.captureGroupFields(a, view2)

	if got := groupChosen(a, 0); !slices.Equal(got, []int{2}) {
		t.Fatalf("group 1 choice = %v", got)
	}
	if got := groupChosen(a, 1); !slices.Equal(got, []int{4}) {
		t.Fatalf("group 2 choice = %v", got)
	}
	// Deleting group 1's choice takes the flow from the store both groups
	// read, but leaves group 2's independent choice exactly as it was.
	a.views.focusCaptureGroup(a, 0)
	a.deleteFlows(a.selectedFlows())
	if got := groupChosen(a, 1); !slices.Equal(got, []int{4}) {
		t.Fatalf("deleting in group 1 changed group 2's choice: %v", got)
	}
	if view2.pickedID != 4 {
		t.Fatalf("group 2 inspector moved to %d", view2.pickedID)
	}
	if slices.Contains(flowIDs(a), 2) {
		t.Fatal("deleting group 1's selection left the flow in the store")
	}
	if got := groupIDs(a, 1); !slices.Equal(got, []int{4}) {
		t.Fatalf("group 2 rows after delete = %v", got)
	}
}

func TestCaptureGroupFocusCommandsAndShortcuts(t *testing.T) {
	a := splitTestApp(t)
	groupFilter(a, 0, "errors", "first")
	groupFilter(a, 1, "host:other.test", "second")
	tt := ui.NewTester(a.view, 1500, 900)
	tt.Frame()
	if a.views.focus != 1 || a.query != "second" {
		t.Fatalf("split started in group %d showing %q", a.views.focus, a.query)
	}
	if err := tt.Click(locales.FocusCaptureGroup(1)); err != nil {
		t.Fatal(err)
	}
	tt.Frame()
	if a.views.focus != 0 || a.query != "first" {
		t.Fatalf("focus group 1 command gave group %d and %q", a.views.focus, a.query)
	}
	pick(a.actions(nil), "capture.focus-next-group").run()
	if a.views.focus != 1 || a.query != "second" {
		t.Fatalf("next group command gave group %d and %q", a.views.focus, a.query)
	}
	pick(a.actions(nil), "capture.focus-previous-group").run()
	if a.views.focus != 0 || a.query != "first" {
		t.Fatalf("previous group command gave group %d and %q", a.views.focus, a.query)
	}
	pick(a.actions(nil), "capture.close-group").run()
	if a.views.split || a.views.groups[1] != 0 || a.query != "first" {
		t.Fatalf("close group left split=%v group2=%d query=%q", a.views.split, a.views.groups[1], a.query)
	}
}

func TestBulkActionUsesOnlyFocusedGroupsVisibleSelection(t *testing.T) {
	a := splitTestApp(t)
	groupFilter(a, 0, "errors", "")
	groupFilter(a, 1, "all", "")
	view1 := a.views.viewByID(a.views.groups[0])
	a.views.focusCaptureGroup(a, 0)
	// id 2 is visible to group 1, id 1 is hidden by its filter. id 4 is
	// chosen in the other group and must never join a bulk action here.
	a.chosen.Add(1)
	a.chosen.Add(2)
	a.views.captureGroupFields(a, view1)
	view2 := a.views.viewByID(a.views.groups[1])
	view2.chosen.Add(4)
	tt := ui.NewTester(a.view, 1500, 900)
	tt.Frame()
	if got := groupChosen(a, 0); !slices.Equal(got, []int{2}) {
		t.Fatalf("group 1 kept hidden rows in its choice: %v", got)
	}
	if got := groupChosen(a, 1); !slices.Equal(got, []int{4}) {
		t.Fatalf("group 2 choice changed: %v", got)
	}
	var selected []int
	for _, f := range a.selectedFlows() {
		selected = append(selected, f.id)
	}
	if !slices.Equal(selected, []int{2}) {
		t.Fatalf("bulk action targeted %v, want only the focused group's visible row", selected)
	}
}

func TestCaptureSplitAndFiltersSurviveReload(t *testing.T) {
	dir := t.TempDir()
	a := splitTestApp(t)
	a.settingsPath = filepath.Join(dir, "settings.json")
	groupFilter(a, 1, "host:other.test", "deep")
	groupFilter(a, 0, "errors", "")
	groupFilter(a, 1, "host:other.test", "deep")
	a.views.persistWorkbench(a)
	a.lastSave = time.Time{} // the settings writer debounces live edits
	if err := a.persistSettings(); err != nil {
		t.Fatal(err)
	}
	saved, err := loadSettings(a.settingsPath)
	if err != nil {
		t.Fatal(err)
	}
	if !saved.CaptureSplit || saved.CaptureFocus != 1 || len(saved.CaptureGroups) != 2 {
		t.Fatalf("saved workbench = split %v, focus %d, %d groups", saved.CaptureSplit, saved.CaptureFocus, len(saved.CaptureGroups))
	}
	if saved.CaptureGroups[0].Filter != "errors" || saved.CaptureGroups[1].Query != "deep" {
		t.Fatalf("saved group filters = %+v", saved.CaptureGroups)
	}

	b := workspaceApp()
	b.flows = slices.Clone(a.flows)
	b.recount()
	b.applyFilter()
	b.workbench.captureSplit = saved.CaptureSplit
	b.workbench.captureFocus = saved.CaptureFocus
	b.workbench.captureGroups = saved.CaptureGroups
	b.views.init(b, filepath.Join(dir, "views.json"))
	b.views.restoreWorkbench(b, saved.CaptureGroups, saved.CaptureFocus)
	if !b.views.split || b.views.focus != 1 || len(b.views.groupViews()) != 2 {
		t.Fatalf("reload gave split %v, focus %d, %d views", b.views.split, b.views.focus, len(b.views.groupViews()))
	}
	if got := groupIDs(b, 0); !slices.Equal(got, []int{2, 3}) {
		t.Fatalf("reloaded group 1 showed %v", got)
	}
	if got := groupIDs(b, 1); len(got) != 0 {
		t.Fatalf("reloaded group 2 kept a query nothing matches: %v", got)
	}
	view2 := b.views.viewByID(b.views.groups[1])
	if view2.filter != "host:other.test" || view2.query != "deep" {
		t.Fatalf("reloaded group 2 filter = %q/%q", view2.filter, view2.query)
	}
}

func TestSplitRightAndCloseGroupCommands(t *testing.T) {
	a := workspaceApp()
	a.views.init(a, filepath.Join(t.TempDir(), "views.json"))
	pick(a.actions(nil), "capture.split-right").run()
	a.workbench.commandList = nil
	if !a.views.split || len(a.views.groupViews()) != 2 {
		t.Fatal("Split Right did not open two groups")
	}
	if got := pick(a.actions(nil), "capture.split-right"); got.enabled {
		t.Fatal("Split Right stayed enabled with two groups")
	}
	pick(a.actions(nil), "capture.close-group").run()
	if a.views.split || len(a.views.groupViews()) != 1 {
		t.Fatal("Close Group did not leave one group")
	}
}

// The wide split gives each group its own divider state, so a row is drawn
// below each table's header even though only one group has the keyboard.
func TestCaptureSplitShowsDataRowsInBothGroups(t *testing.T) {
	a := splitTestApp(t)
	groupFilter(a, 0, "errors", "")
	groupFilter(a, 1, "host:other.test", "")
	for i := 5; i <= 14; i++ {
		a.flows = append(a.flows, flow{id: i, method: "GET", host: "other.test", path: fmt.Sprintf("/extra%d", i), status: 200})
	}
	a.recount()
	a.applyFilter()
	view1, view2 := a.views.viewByID(a.views.groups[0]), a.views.viewByID(a.views.groups[1])
	a.views.focusCaptureGroup(a, 0)
	a.pickedID, a.chosen = 2, func() (s ui.Selection[int]) { s.Add(2); return }()
	a.views.captureGroupFields(a, view1)
	a.views.focusCaptureGroup(a, 1)
	a.pickedID, a.chosen = 4, func() (s ui.Selection[int]) { s.Add(4); return }()
	a.views.captureGroupFields(a, view2)
	// Each group's own divider state decides how much room its table gets.
	view1.tablePercent, view2.tablePercent = 20, 80
	tt := ui.NewTester(a.view, 1500, 900)
	tt.Frame()
	tt.Frame()
	dividers := groupDividers(tt)
	if dividers.second.Bounds.Y-dividers.first.Bounds.Y < 120 {
		t.Fatalf("both groups use one table share: dividers at %v and %v", dividers.first.Bounds.Y, dividers.second.Bounds.Y)
	}
	var errorRow, groupRow, groupHeader ui.AccessInfo
	for _, info := range tt.AccessAll() {
		if info.Role != ui.RoleRow {
			continue
		}
		if strings.HasPrefix(info.Name, "2 401 POST") {
			errorRow = info
		}
		if strings.HasPrefix(info.Name, "# Status") && groupHeader.Name == "" {
			groupHeader = info
		}
		if strings.Contains(info.Name, "other.test/health") {
			groupRow = info
		}
	}
	if errorRow.Bounds.H < 20 || !errorRow.Selected || errorRow.Bounds.Y >= dividers.first.Bounds.Y {
		t.Fatalf("group 1 owns no visible, marked data row: %+v", errorRow)
	}
	if groupRow.Bounds.H < 20 || !groupRow.Selected || groupRow.Bounds.Y < groupHeader.Bounds.Y+groupHeader.Bounds.H {
		t.Fatalf("group 2 owns no visible, marked data row: %+v", groupRow)
	}
}

type dividerPair struct{ first, second ui.AccessInfo }

// groupDividers finds the list/inspector divider in each group by its x
// position: group 1 starts at the content edge, group 2 to its right.
func groupDividers(tt *ui.Tester) dividerPair {
	var horizontal []ui.AccessInfo
	for _, info := range tt.AccessAll() {
		if info.Role == ui.RoleSplitter && info.Bounds.W > 400 && info.Bounds.H == 1 {
			horizontal = append(horizontal, info)
		}
	}
	var pair dividerPair
	for _, info := range horizontal {
		if pair.first.Name == "" || info.Bounds.X < pair.first.Bounds.X {
			pair.first, pair.second = info, info
			continue
		}
		if info.Bounds.X > pair.second.Bounds.X {
			pair.second = info
		}
	}
	return pair
}

// An answer for the unfocused group's body condition must rebuild only that
// group's rows. The reader stays blocked until group 1 has the keyboard and a
// selection, so the result cannot be mistaken for a focused-group refresh.
func TestUnfocusedGroupBodySearchRebuildsOnlyItsRows(t *testing.T) {
	a := splitTestApp(t)
	groupFilter(a, 0, "errors", "")
	groupFilter(a, 1, "host:other.test", "")
	a.flows[1].resBody = "needle in the failing flow"
	a.flows[3].resBody = "needle in the other host"

	started, release := make(chan struct{}), make(chan struct{})
	var once sync.Once
	a.views.search = func(ctx context.Context, job bodyJob, needle string) (bool, error) {
		once.Do(func() { close(started) })
		select {
		case <-release:
			return strings.Contains(job.resHead, needle), nil
		case <-ctx.Done():
			return false, ctx.Err()
		}
	}
	a.views.focusCaptureGroup(a, 1)
	view2 := a.views.activeView()
	view2.body = "needle"
	a.views.applyFilter(a)
	<-started

	a.views.focusCaptureGroup(a, 0)
	view1 := a.views.activeView()
	beforeRows, beforeChosen := groupIDs(a, 0), groupChosen(a, 0)
	a.pickedID, a.chosen = 3, func() (s ui.Selection[int]) { s.Add(3); return }()
	a.views.captureGroupFields(a, view1)
	close(release)
	waitAppSignal(t, a,
		func() bool { return slices.Equal(groupIDs(a, 1), []int{4}) },
		func() { a.views.drain(a) },
		func() string {
			return "the unfocused body search never narrowed group 2: " + fmt.Sprint(groupIDs(a, 1))
		},
	)
	if got := groupIDs(a, 0); !slices.Equal(got, beforeRows) {
		t.Fatalf("group 1 rows changed: %v, want %v", got, beforeRows)
	}
	if got := groupChosen(a, 0); !slices.Equal(got, append(beforeChosen, 3)) {
		t.Fatalf("group 1 selection changed: %v", got)
	}
	if got := groupChosen(a, 1); len(got) != 0 {
		t.Fatalf("group 2 selection changed: %v", got)
	}
}

// A row click is real input: it selects the row in its group and moves the
// keyboard focus to that group, as clicking an inactive VS Code editor does.
func TestCaptureSplitRowClickSelectsAndFocusesItsGroup(t *testing.T) {
	a := splitTestApp(t)
	groupFilter(a, 0, "errors", "")
	groupFilter(a, 1, "host:other.test", "")
	tt := ui.NewTester(a.view, 1500, 900)
	tt.Frame()
	tt.Frame()
	row := func(prefix string) ui.AccessInfo {
		for _, info := range tt.AccessAll() {
			if info.Role == ui.RoleRow && strings.HasPrefix(info.Name, prefix) {
				return info
			}
		}
		t.Fatalf("row %q is not drawn", prefix)
		return ui.AccessInfo{}
	}
	clickRow := func(prefix string) {
		t.Helper()
		r := row(prefix)
		tt.ClickAt(r.Bounds.X+80, r.Bounds.Y+r.Bounds.H/2)
		tt.Frame()
		tt.Frame()
	}
	// Give group 2 a real click first, so both groups start with a choice.
	clickRow("4 200 GET")
	if a.pickedID != 4 || a.views.focus != 1 || !row("4 200 GET").Selected {
		t.Fatalf("clicking group 2 gave picked=%d focus=%d, row selected=%v", a.pickedID, a.views.focus, row("4 200 GET").Selected)
	}
	clickRow("3 500 POST")
	if a.pickedID != 3 || a.views.focus != 0 || !row("3 500 POST").Selected {
		t.Fatalf("clicking group 1 gave picked=%d focus=%d, row selected=%v", a.pickedID, a.views.focus, row("3 500 POST").Selected)
	}
	if !row("4 200 GET").Selected {
		t.Fatal("group 2 lost its row when group 1 took the focus")
	}
	clickRow("4 200 GET")
	if a.pickedID != 4 || a.views.focus != 1 || !row("4 200 GET").Selected {
		t.Fatalf("clicking group 2 gave picked=%d focus=%d, row selected=%v", a.pickedID, a.views.focus, row("4 200 GET").Selected)
	}
	if !row("3 500 POST").Selected {
		t.Fatal("the inactive group 1 lost its row marker")
	}
	clickRow("2 401 POST")
	if a.pickedID != 2 || a.views.focus != 0 || !row("2 401 POST").Selected {
		t.Fatalf("clicking group 1 again gave picked=%d focus=%d, row selected=%v", a.pickedID, a.views.focus, row("2 401 POST").Selected)
	}
}
