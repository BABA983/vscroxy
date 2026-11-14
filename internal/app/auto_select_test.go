package app

import (
	"testing"
	"time"

	"github.com/baba983/vscroxy/internal/capture"
	"github.com/egoist/mygo/ui"
)

// New traffic is selected as it arrives while Auto-select is on.
func TestAutoSelectChoosesNewTraffic(t *testing.T) {
	a := workspaceApp()
	a.autoSelect = true
	tt := ui.NewTester(a.view, 1280, 800)
	tt.Frame()
	a.pending = []capture.Flow{{ID: 99, Method: "GET", Host: "new.test", Path: "/new", Kind: "http", Status: 200, At: time.Now()}}
	a.drain()
	if !a.autoSelect {
		t.Fatal("new traffic turned Auto-select off")
	}
	if a.picked != len(a.visible)-1 {
		t.Fatalf("picked row %d of %d, want the newest", a.picked, len(a.visible))
	}
	if f, ok := a.current(); !ok || f.host != "new.test" {
		t.Fatalf("the inspector shows %+v, want the new request", f)
	}
}

// A manual pick is the reader taking over: Auto-select turns off.
func TestManualPickTurnsAutoSelectOff(t *testing.T) {
	a := workspaceApp()
	a.autoSelect = true
	tt := ui.NewTester(a.view, 1280, 800)
	tt.Frame()
	if err := tt.Click("api.example.com/login"); err != nil {
		t.Fatal(err)
	}
	tt.Frame()
	if a.autoSelect {
		t.Fatal("a manual pick left Auto-select on")
	}
}

// A draft being edited keeps the row under the cursor; the arrivals are
// counted on the footer instead.
func TestEditingKeepsTheChosenRow(t *testing.T) {
	a := workspaceApp()
	a.autoSelect = true
	tt := ui.NewTester(a.view, 1280, 800)
	tt.Frame()
	a.requestDocument = &requestDocument{composeURL: "https://draft.test"}
	before := a.picked
	a.pending = []capture.Flow{{ID: 99, Method: "GET", Host: "new.test", Path: "/new", Kind: "http", Status: 200, At: time.Now()}}
	a.drain()
	if a.picked != before {
		t.Fatalf("editing moved the selection from %d to %d", before, a.picked)
	}
	if a.newRows != 1 {
		t.Fatalf("new rows = %d, want 1", a.newRows)
	}
}

// Auto-select keeps choosing across several arrivals; a second one is not a
// manual pick.
func TestAutoSelectKeepsChoosing(t *testing.T) {
	a := workspaceApp()
	a.autoSelect = true
	tt := ui.NewTester(a.view, 1280, 800)
	tt.Frame()
	for i, path := range []string{"/one", "/two", "/three"} {
		a.pending = []capture.Flow{{ID: 100 + i, Method: "GET", Host: "new.test", Path: path, Kind: "http", Status: 200, At: time.Now()}}
		a.drain()
		tt.Frame()
		if !a.autoSelect {
			t.Fatalf("arrival %d turned Auto-select off", i+1)
		}
		if a.picked != len(a.visible)-1 {
			t.Fatalf("arrival %d picked %d of %d", i+1, a.picked, len(a.visible))
		}
	}
}

// An empty table normalizes its row to -1. That is not a manual pick: the
// first request must still arrive selected and leave Auto-select on.
func TestAutoSelectSurvivesTheEmptyTable(t *testing.T) {
	a := workspaceApp()
	a.flows, a.visible = nil, nil
	a.autoSelect = true
	tt := ui.NewTester(a.view, 1280, 800)
	tt.Frame()
	if !a.autoSelect {
		t.Fatal("the empty table turned Auto-select off")
	}
	a.pending = []capture.Flow{{ID: 99, Method: "GET", Host: "new.test", Path: "/new", Kind: "http", Status: 200, At: time.Now()}}
	a.drain()
	tt.Frame()
	if !a.autoSelect {
		t.Fatal("the first arrival turned Auto-select off")
	}
	if a.picked != len(a.visible)-1 {
		t.Fatalf("picked row %d of %d, want the newest", a.picked, len(a.visible))
	}
}
