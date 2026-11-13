package app

import (
	"slices"
	"testing"

	"github.com/egoist/mygo/ui"
)

// A captured row dragged onto a collection folder is saved there, the same as
// Save to collection, without sending it.
func TestCapturedRowDropsIntoACollection(t *testing.T) {
	a := workspaceApp()
	a.flows = append(a.flows, flow{
		id: 9, method: "POST", scheme: "https", host: "api.example.com",
		path: "/orders", url: "https://api.example.com/orders", status: 201,
	})
	a.applyFilter()
	tt := ui.NewTester(a.view, 1440, 800)
	tt.Frame()
	row, ok := tt.Find("api.example.com/orders")
	if !ok {
		t.Fatalf("the captured row is missing: %q", tt.Texts())
	}
	folder, ok := tt.Find("Collection: Workspace")
	if !ok {
		t.Fatalf("the collection folder is missing: %q", tt.Texts())
	}
	from := [2]float32{row.X + row.W/2, row.Y + row.H/2}
	to := [2]float32{folder.X + folder.W/2, folder.Y + folder.H/2}
	tt.Press(from[0], from[1])
	tt.Move(from[0], from[1]-12)
	tt.Move(to[0], to[1])
	tt.Release(to[0], to[1])
	tt.Frame()
	if !slices.ContainsFunc(a.requests, func(r savedRequest) bool {
		return r.Collection == "Workspace" && r.URL == "https://api.example.com/orders"
	}) {
		t.Fatalf("the drop saved nothing into Workspace: %+v", a.requests)
	}
}
