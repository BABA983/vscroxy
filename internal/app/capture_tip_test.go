package app

import (
	"slices"
	"strings"
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
	// A URL in a tip carries zero-width breaks after its slashes, so it can
	// wrap; take them out before checking that the card has the whole URL.
	if !slices.ContainsFunc(tt.Texts(), func(s string) bool {
		return strings.Contains(strings.ReplaceAll(s, "\u200b", ""), "https://api.example.com/projects")
	}) {
		t.Fatalf("the card does not carry the full URL: %v", tt.Texts())
	}
}
