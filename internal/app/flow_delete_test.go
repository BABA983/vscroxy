package app

import (
	"slices"
	"testing"

	"github.com/baba983/vscroxy/locales"
	"github.com/egoist/mygo/ui"
)

func deleteTestApp() *app {
	a := workspaceApp()
	a.flows = append(a.flows, flow{id: 3, method: "GET", scheme: "https", host: "api.example.com", path: "/third", url: "https://api.example.com/third", status: 200, process: "Browser"})
	a.recount()
	a.applyFilter()
	a.pickedID = 1
	a.picked = 0
	a.choosePicked()
	return a
}

func flowIDs(a *app) []int {
	ids := make([]int, len(a.flows))
	for i, f := range a.flows {
		ids[i] = f.id
	}
	return ids
}

func TestDeleteSelectedFlowMovesToNextAndUndoRestores(t *testing.T) {
	a := deleteTestApp()
	a.deleteFlows([]flow{a.flows[0]})
	if got := flowIDs(a); !slices.Equal(got, []int{2, 3}) {
		t.Fatalf("flows after delete = %v, want [2 3]", got)
	}
	if a.pickedID != 2 || !a.chosen.Has(2) {
		t.Fatalf("selection after delete = id %d, chosen %v; want 2", a.pickedID, a.chosen.All())
	}
	if len(a.notices) != 1 || a.notices[0].action != locales.Undo() {
		t.Fatalf("delete toast = %+v, want an Undo action", a.notices)
	}
	if x := pick(a.actions(nil), "flow.undo-delete"); !x.enabled || x.shortcut.key != ui.KeyZ {
		t.Fatalf("undo action = %+v, want an enabled Cmd-Z", x)
	}
	a.undoDelete()
	if got := flowIDs(a); !slices.Equal(got, []int{1, 2, 3}) {
		t.Fatalf("flows after undo = %v, want [1 2 3]", got)
	}
	if a.pickedID != 1 || !a.chosen.Has(1) {
		t.Fatalf("selection after undo = id %d, chosen %v; want 1", a.pickedID, a.chosen.All())
	}
}

func TestDeleteMultipleFlowsRestoresTheirPositions(t *testing.T) {
	a := deleteTestApp()
	a.chosen.Clear()
	a.chosen.Add(1)
	a.chosen.Add(3)
	a.pickedID = 1
	a.picked = 0
	a.deleteFlows(a.selectedFlows())
	if got := flowIDs(a); !slices.Equal(got, []int{2}) {
		t.Fatalf("flows after multi-delete = %v, want [2]", got)
	}
	if a.pickedID != 2 || !a.chosen.Has(2) {
		t.Fatalf("selection after multi-delete = id %d, chosen %v; want 2", a.pickedID, a.chosen.All())
	}
	if x := pick(a.actions(nil), "flow.undo-delete"); !x.enabled || x.shortcut.key != ui.KeyZ {
		t.Fatalf("undo action = %+v, want an enabled Cmd-Z", x)
	}
	a.undoDelete()
	if got := flowIDs(a); !slices.Equal(got, []int{1, 2, 3}) {
		t.Fatalf("flows after undo = %v, want [1 2 3]", got)
	}
	if a.pickedID != 1 || !a.chosen.Has(1) {
		t.Fatalf("selection after undo = id %d, chosen %v; want 1", a.pickedID, a.chosen.All())
	}
}

func TestDeleteLastFlowSelectsThePreviousRow(t *testing.T) {
	a := deleteTestApp()
	a.pickedID = 3
	a.picked = len(a.visible) - 1
	a.deleteFlows([]flow{a.flows[2]})
	if a.pickedID != 2 || !a.chosen.Has(2) {
		t.Fatalf("selection after deleting the last row = id %d, chosen %v; want 2", a.pickedID, a.chosen.All())
	}
}

func TestDeleteActionIsLastInTheRowMenuAndUsesTheShortcut(t *testing.T) {
	a := deleteTestApp()
	tt := ui.NewTester(a.view, 1440, 800)
	tt.Frame()
	if err := tt.RightClick("api.example.com/projects"); err != nil {
		t.Fatal(err)
	}
	menu := tt.Menu()
	last := ""
	for _, label := range menu {
		if label != "-" {
			last = label
		}
	}
	if last != locales.Delete() {
		t.Fatalf("last row-menu item = %q, want Delete; menu %q", last, menu)
	}
	tt.Key(0, ui.KeyEscape)
	tt.Frame()
	if err := tt.Click("api.example.com/projects"); err != nil {
		t.Fatal(err)
	}
	tt.Key(ui.Cmd, flowDeleteKey())
	tt.Frame()
	if len(a.flows) != 2 {
		t.Fatalf("shortcut left %d flows, want 2", len(a.flows))
	}
}

func TestDeleteUndoShortcutRestoresTheRow(t *testing.T) {
	a := deleteTestApp()
	tt := ui.NewTester(a.view, 1440, 800)
	tt.Frame()
	if err := tt.Click("api.example.com/projects"); err != nil {
		t.Fatal(err)
	}
	tt.Key(ui.Cmd, flowDeleteKey())
	tt.Frame()
	if len(a.flows) != 2 {
		t.Fatalf("shortcut left %d flows, want 2", len(a.flows))
	}
	tt.Key(ui.Cmd, ui.KeyZ)
	tt.Frame()
	if len(a.flows) != 3 || a.pickedID != 1 {
		t.Fatalf("undo left %d flows, picked %d; want 3 and 1", len(a.flows), a.pickedID)
	}
}
