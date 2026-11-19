package app

import (
	"fmt"
	"image/png"
	"os"
	"path/filepath"
	"testing"

	"github.com/baba983/vscroxy/internal/request"
	"github.com/baba983/vscroxy/locales"
	"github.com/egoist/mygo/ui"
)

// TestTreeDragArtifacts is the opt-in capture of the tree in both themes and
// of a drag with its drop indicator, for reviewing the feature visually.
// Set VSCROXY_TREE_ARTIFACTS to an output directory to generate the images.
func TestTreeDragArtifacts(t *testing.T) {
	dir := os.Getenv("VSCROXY_TREE_ARTIFACTS")
	if dir == "" {
		t.Skip("set VSCROXY_TREE_ARTIFACTS to an output directory")
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	a := libraryApp(t)
	a.page = pageRequest
	a.appearance = "light"
	if err := a.writeLibrary([]savedRequest{
		{Request: request.Request{ID: "2", Name: "req2", Collection: "Shop", Method: "GET", URL: "https://api.example/req2"}},
		{Request: request.Request{ID: "10", Name: "req10", Collection: "Shop", Method: "POST", URL: "https://api.example/req10"}},
		{Request: request.Request{ID: "3", Name: "plain", Collection: "Archive", Method: "GET", URL: "https://api.example/plain"}},
	}, []request.Folder{{Path: "Shop", Description: "shop"}, {Path: "Archive", Description: "archive"}}); err != nil {
		t.Fatal(err)
	}
	a.addDocument(a.requests[0], a.requests[0].key())
	tt := ui.NewTester(a.view, 1100, 700)
	tt.SetScale(2)
	save := func(name string) {
		t.Helper()
		tt.Frame()
		img := tt.Image()
		f, err := os.Create(filepath.Join(dir, name))
		if err != nil {
			t.Fatal(err)
		}
		defer f.Close()
		if err := png.Encode(f, img); err != nil {
			t.Fatal(err)
		}
	}
	save("tree-light.png")
	a.appearance = "dark"
	tt.Frame()
	save("tree-dark.png")
	a.appearance = "light"
	tt.Frame()

	from, ok := tt.Find("req2")
	if !ok {
		t.Fatal("no req2 row")
	}
	to, ok := tt.Find(locales.CollectionPath("Archive"))
	if !ok {
		t.Fatal("no Archive row")
	}
	x0, y0 := from.X+from.W/2, from.Y+from.H/2
	x1, y1 := to.X+to.W/2, to.Y+to.H/2
	save("drag-00-idle.png")
	tt.Press(x0, y0)
	save("drag-01-press.png")
	for step := range 8 {
		p := float32(step+1) / 8
		tt.Move(x0+(x1-x0)*p, y0+(y1-y0)*p)
		save(fmt.Sprintf("drag-%02d-moving.png", step+2))
	}
	// The middle of a folder row is the "move into" target.
	tt.Move(x1, y1)
	save("drag-10-into-folder.png")
	// Its top edge is the gap before it: the insertion line replaces the wash.
	tt.Move(x1, to.Y+2)
	save("drag-11-insert-before.png")
	tt.Move(x1, to.Y+to.H-2)
	save("drag-12-insert-after.png")
	tt.Release(x1, y1)
	save("drag-13-dropped.png")
}
