package app

import (
	"testing"
	"time"

	"github.com/egoist/mygo/ui"
)

// The capture table's cells show the row's card, not a tip that repeats the
// cell: the method, status, host and path are all ways into the full URL,
// status and client that a narrow column cannot hold.
func TestCaptureCellsShowTheRowCard(t *testing.T) {
	a := workspaceApp()
	tt := ui.NewTester(a.view, 1280, 800)
	tt.Frame()
	tt.Frame()
	cell, ok := tt.Find("GET")
	if !ok {
		t.Fatalf("no GET cell: %v", tt.Texts())
	}
	tt.Move(cell.X+cell.W/2, cell.Y+cell.H/2)
	tt.Advance(700 * time.Millisecond)
	tt.Frame()
	tt.Frame()
	if !tt.HasText("URL") {
		t.Fatalf("the method cell showed no card: %v", tt.Texts())
	}
	if !tt.HasText("https://api.example.com/projects") {
		t.Fatalf("the card does not carry the full URL: %v", tt.Texts())
	}
}
