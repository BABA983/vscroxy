package app

import (
	"fmt"
	"slices"
	"testing"

	"github.com/egoist/mygo/ui"
)

// The sidebar's middle folds like a pane view: a folded region keeps its
// heading, and the open ones take the room it leaves. Folding the two above
// Collections gives the tree the whole middle, so a request that was below
// its old edge is reachable without scrolling; unfolding gives the room back.
func TestSidebarPanesGiveFoldedRoomToCollections(t *testing.T) {
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

	collections, ok := tt.Find("Collections")
	if !ok {
		t.Fatalf("no Collections heading: %q", tt.Texts())
	}
	// The first request below the tree's edge is the one the fold must
	// reveal; the two folders above the requests keep the fixture honest.
	clipped := ""
	for i := 2; i <= 21; i++ {
		name := fmt.Sprintf("Request %02d", i)
		if row, ok := tt.Find(name); ok && row.H == 0 {
			clipped = name
			break
		}
	}
	if clipped == "" {
		t.Fatal("the fixture has no request below the collections' edge")
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
	if heights := a.currentSettings().SidebarPaneHeights; heights["sources"] <= 0 || heights["sessions"] <= 0 {
		t.Fatalf("the panes' heights are not remembered: %v", heights)
	}
	folded, ok := tt.Find("Collections")
	if !ok || folded.Y >= collections.Y {
		t.Fatalf("Collections did not take the folded room: %+v then %+v", collections, folded)
	}
	row, ok := tt.Find(clipped)
	if !ok || row.H == 0 {
		t.Fatalf("the tree did not grow into the folded room: %s %+v", clipped, row)
	}

	// Unfolding both gives the room back: the request is below the edge
	// again, and the folded state is no longer remembered.
	for _, heading := range []string{"Sources", "Sessions"} {
		if err := tt.Click(heading); err != nil {
			t.Fatal(err)
		}
		tt.Frame()
	}
	if row, ok := tt.Find(clipped); ok && row.H > 0 {
		t.Fatalf("unfolding Sources kept the tree's extra room: %s %+v", clipped, row)
	}
	if slices.Contains(a.closedRegions(), "sources") || slices.Contains(a.closedRegions(), "sessions") {
		t.Fatalf("an unfolded region is still remembered: %v", a.closedRegions())
	}
}

// A short window takes its room from the middle, not from Traffic: every
// scope and view in the fixed top stays visible, as do the tools at the
// bottom, while Sources, Sessions and Collections give way first.
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
	for _, label := range []string{"Client setup", "HTTPS decryption", "Diagnostics"} {
		row, ok := tt.Find(label)
		if !ok || row.H == 0 || row.Y < 0 || row.Y+row.H > 600 {
			t.Fatalf("footer row %q is not visible in a 600-high window: %+v", label, row)
		}
	}
	// The middle still gives Collections its heading, search and room for
	// rows; the whole middle scrolls rather than cutting it out of reach.
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
