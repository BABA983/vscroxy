package app

import (
	"image"
	"image/draw"
	"image/png"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/baba983/vscroxy/locales"
	"github.com/egoist/mygo/ui"
)

// TestUndoScreenshots exports 2x Tester captures of the Undo toast and the
// palette's Undo/Redo entries in both themes. Set VSCROXY_UNDO_SCREENSHOTS to
// an output directory.
func TestUndoScreenshots(t *testing.T) {
	dir := os.Getenv("VSCROXY_UNDO_SCREENSHOTS")
	if dir == "" {
		t.Skip("set VSCROXY_UNDO_SCREENSHOTS to an output directory")
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	for _, appearance := range []string{"light", "dark"} {
		t.Run(appearance+"-toast", func(t *testing.T) {
			a := deleteTestApp()
			a.appearance = appearance
			tt := ui.NewTester(a.view, 1280, 800)
			tt.SetLocale("en-US")
			tt.SetScale(2)
			tt.Frame()
			if err := tt.Click("api.example.com/projects"); err != nil {
				t.Fatal(err)
			}
			tt.Key(ui.Cmd, flowDeleteKey())
			tt.Frame()
			tt.Advance(300 * time.Millisecond)
			tt.Frame()
			if !tt.HasText(locales.Undo()) {
				t.Fatalf("Undo toast is missing: %v", tt.Texts())
			}
			writeUndoScreenshot(t, filepath.Join(dir, "undo-toast-"+appearance+".png"), tt, true)
		})
		for _, scene := range []struct{ name, query, want string }{
			{name: "undo", query: "undo", want: locales.Undo()},
			{name: "redo", query: "redo", want: locales.Redo()},
		} {
			t.Run(appearance+"-palette-"+scene.name, func(t *testing.T) {
				a := deleteTestApp()
				a.appearance = appearance
				// Two deletes and one undo leave both service commands enabled.
				a.deleteFlows([]flow{a.flows[0]})
				a.deleteFlows([]flow{a.flows[0]})
				a.undo()
				if !a.undoHistory().CanUndo() || !a.undoHistory().CanRedo() {
					t.Fatal("palette fixture needs both an undo and a redo")
				}
				tt := ui.NewTester(a.view, 1280, 800)
				tt.SetLocale("en-US")
				tt.SetScale(2)
				tt.Key(ui.Cmd, ui.KeyK)
				tt.Type(scene.query)
				tt.Frame()
				if !tt.HasText(scene.want) {
					t.Fatalf("%q is missing from the palette: %v", scene.want, tt.Texts())
				}
				writeUndoScreenshot(t, filepath.Join(dir, "undo-palette-"+scene.name+"-"+appearance+".png"), tt, false)
			})
		}
	}
}

func writeUndoScreenshot(t *testing.T, path string, tt *ui.Tester, crop bool) {
	t.Helper()
	img := tt.Image()
	if crop {
		toast, ok := tt.Find(locales.DeletedRequests(1))
		if !ok {
			t.Fatal("the toast text is not drawn")
		}
		scale := 2
		bounds := img.Bounds()
		rect := image.Rect(int(toast.X-36)*scale, int(toast.Y-28)*scale,
			int(toast.X+toast.W+310)*scale, int(toast.Y+toast.H+40)*scale).Intersect(bounds)
		cropped := image.NewRGBA(image.Rect(0, 0, rect.Dx(), rect.Dy()))
		draw.Draw(cropped, cropped.Bounds(), img, rect.Min, draw.Src)
		img = cropped
	}
	f, err := os.Create(path)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	if err := png.Encode(f, img); err != nil {
		t.Fatal(err)
	}
}
