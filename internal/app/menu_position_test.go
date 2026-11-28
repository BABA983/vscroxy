package app

import (
	"testing"

	"github.com/baba983/vscroxy/locales"
	"github.com/egoist/mygo/ui"
)

// A right-click menu opens with its top-left at the pointer, and the row it
// acts on stays the marked one after the pointer moves onto the menu.
func TestContextMenuOpensAtThePointerAndMarksItsRow(t *testing.T) {
	a := workspaceApp()
	a.sidebarMode = sidebarCollections
	a.requests = append(a.requests, savedRequest{Name: "Second", Collection: "Workspace/API", Method: "POST", URL: "https://api.example.com/second"})
	tt := ui.NewTester(a.view, 1400, 900)
	tt.SetLocale("en-US")
	tt.Frame()
	tt.Frame()
	if err := tt.Click("List projects"); err != nil {
		t.Fatal(err)
	}
	tt.Frame()
	row, ok := tt.Find("Second")
	if !ok {
		t.Fatalf("the second request is not in the tree: %v", tt.Texts())
	}
	x, y := row.X+row.W/2, row.Y+row.H/2
	tt.RightClickAt(x, y)
	tt.Frame()
	item, ok := tt.Find(locales.Open())
	if !ok {
		t.Fatalf("the tree menu did not open: %v", tt.Menu())
	}
	if item.X < x-1 || item.X > x+8 || item.Y < y-1 || item.Y > y+8 {
		t.Fatalf("the menu's first item %v did not open at the pointer (%g, %g)", item, x, y)
	}
	if a.requestChoice != a.requests[1].key() {
		t.Fatalf("the right-clicked row is not the marked one: %q", a.requestChoice)
	}
}
