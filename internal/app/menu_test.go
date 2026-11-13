package app

import (
	"slices"
	"testing"

	"github.com/baba983/vscroxy/locales"
	"github.com/egoist/mygo/ui"
)

// A collection request's right-click menu and its ⋯ menu are one menu: the
// same actions, in the same order, wherever the pointer finds them.
func TestCollectionRequestMenusMatch(t *testing.T) {
	a := workspaceApp()
	tt := ui.NewTester(a.view, 1440, 800)
	tt.Frame()
	row := "List projects"
	more := locales.RequestActionsPrefix(row)
	if err := tt.RightClick(row); err != nil {
		t.Fatalf("right-click: %v", err)
	}
	want := tt.Menu()
	if len(want) == 0 {
		t.Fatal("the request's right-click menu is empty")
	}
	tt.Key(0, ui.KeyEscape)
	tt.Frame()
	if err := tt.Click(more); err != nil {
		t.Fatalf("open ⋯: %v", err)
	}
	got := tt.Menu()
	if !slices.Equal(got, want) {
		t.Fatalf("⋯ menu %q, right-click menu %q", got, want)
	}
}

// The table row's menu is the exchange menu the inspector offers: the
// inspector adds its own pane choices, but every row action is there in the
// same order.
func TestTableRowMenuIsInTheInspectorMenu(t *testing.T) {
	a := workspaceApp()
	tt := ui.NewTester(a.view, 1440, 800)
	tt.Frame()
	if err := tt.Click("api.example.com/projects"); err != nil {
		t.Fatalf("select the row: %v", err)
	}
	tt.Frame()
	if err := tt.RightClick("api.example.com/projects"); err != nil {
		t.Fatalf("right-click: %v", err)
	}
	row := tt.Menu()
	if len(row) == 0 {
		t.Fatal("the row's menu is empty")
	}
	tt.Key(0, ui.KeyEscape)
	tt.Frame()
	if err := tt.Click(locales.InspectorOptions()); err != nil {
		t.Fatalf("open the inspector menu: %v", err)
	}
	inspector := tt.Menu()
	next := 0
	for _, label := range inspector {
		if next < len(row) && label == row[next] {
			next++
		}
	}
	if next != len(row) {
		t.Fatalf("the inspector menu is missing row actions from %q: %q", row[next:], inspector)
	}
}

// A header's right-click menu changes the table: choosing a column hides it.
func TestTableHeaderMenuChoosesAColumn(t *testing.T) {
	a := workspaceApp()
	tt := ui.NewTester(a.view, 1440, 800)
	tt.Frame()
	if !a.columnShown("status") {
		t.Fatal("the status column starts hidden")
	}
	if err := tt.RightClick("Status"); err != nil {
		t.Fatalf("right-click the header: %v", err)
	}
	if err := tt.ChooseMenuItem("Status"); err != nil {
		t.Fatalf("choose the column: %v", err)
	}
	tt.Frame()
	if a.columnShown("status") {
		t.Fatal("choosing Status in the header menu left the column shown")
	}
}
