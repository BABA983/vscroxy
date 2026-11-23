package app

import (
	"fmt"
	"slices"
	"testing"

	"github.com/egoist/mygo/ui"
)

// The sidebar shows one half at a time. Traffic folds like a pane view: a
// folded region keeps its heading and is remembered. Collections has the
// sidebar to itself, so a long tree is on screen without the traffic's
// regions above it.
func TestSidebarHalvesShareTheMiddle(t *testing.T) {
	a := workspaceApp()
	for i := range 20 {
		a.requests = append(a.requests, savedRequest{
			Name:       fmt.Sprintf("Request %02d", i+2),
			Collection: "Workspace/API",
			Method:     "GET",
			URL:        fmt.Sprintf("https://api.example.com/projects/%d", i+2),
		})
	}
	tt := ui.NewTester(a.view, 1280, 800)
	tt.Frame()
	if _, ok := tt.Find("Search collections"); ok {
		t.Fatal("the Traffic half shows the collections")
	}
	for _, heading := range []string{"Sources", "Sessions"} {
		if err := tt.Click(heading); err != nil {
			t.Fatalf("fold %s: %v", heading, err)
		}
		tt.Frame()
	}
	if *a.regionOpen("sources") || *a.regionOpen("sessions") {
		t.Fatal("clicking a heading did not fold its region")
	}
	if !slices.Contains(a.closedRegions(), "sources") || !slices.Contains(a.closedRegions(), "sessions") {
		t.Fatalf("folded regions are not remembered: %v", a.closedRegions())
	}

	showCollections(t, tt)
	for _, gone := range []string{"Sources", "Sessions", "All requests"} {
		if row, ok := tt.Find(gone); ok && row.H > 0 {
			t.Fatalf("the Collections half still shows %q: %+v", gone, row)
		}
	}
	if row, ok := tt.Find("Request 21"); !ok || row.H == 0 || row.Y+row.H > 800 {
		t.Fatalf("the tree did not take the whole middle: Request 21 %+v", row)
	}
	// Switching back finds the traffic as it was left.
	if err := tt.Click("Traffic"); err != nil {
		t.Fatal(err)
	}
	tt.Frame()
	if *a.regionOpen("sources") {
		t.Fatal("switching halves reopened a folded region")
	}
}

// A short window takes its room from the middle, not from Traffic: every
// scope and view in the fixed top stays visible, as does the footer's layout
// switch, while Sources and Sessions give way first. Collections keeps its
// heading, search and rows in the same window.
func TestSidebarTrafficStaysVisibleInShortWindow(t *testing.T) {
	a := workspaceApp()
	tt := ui.NewTester(a.view, 960, 600)
	tt.Frame()
	tt.Frame()
	for _, label := range []string{"All requests", "Errors", "Slow", "Pinned", "Overview"} {
		row, ok := tt.Find(label)
		if !ok || row.H == 0 || row.Y < 0 || row.Y+row.H > 600 {
			t.Fatalf("traffic row %q is not visible in a 600-high window: %+v", label, row)
		}
	}
	if row, ok := tt.Find("Stacked layout"); !ok || row.H == 0 || row.Y+row.H > 600 {
		t.Fatalf("the footer's layout switch is not visible in a 600-high window: %+v", row)
	}
	showCollections(t, tt)
	collections, ok := tt.Find("Collections")
	if !ok || collections.H == 0 {
		t.Fatalf("Collections heading is not visible: %+v", collections)
	}
	search, ok := tt.Find("Search collections")
	if !ok || search.H == 0 {
		t.Fatalf("Collections search is not visible: %+v", search)
	}
	if search.Y < collections.Y+collections.H {
		t.Fatalf("search sits above the Collections heading: heading %+v, search %+v", collections, search)
	}
	if row, ok := tt.Find("List projects"); !ok || row.H == 0 || row.Y+row.H > 600 {
		t.Fatalf("Collections has no request row in a 600-high window: %+v", row)
	}
}
