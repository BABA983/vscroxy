package uikit

import (
	"testing"

	"github.com/egoist/mygo/ui"
)

// A pane that gains room springs to it; with the desktop asking for less
// motion it is at its new size in the frame the size changed.
func TestReduceMotionEndsTransitionsAtOnce(t *testing.T) {
	for _, reduce := range []bool{false, true} {
		wide := false
		view := func(c *ui.Context) {
			width := float32(100)
			if wide {
				width = 300
			}
			ui.Row(c).Fill().Children(func() {
				ui.Column(c).Key("pane").Width(width).ClipX().Transition(PaneMotion).Children(func() {
					ui.Text(c, "Pane body").FillWidth()
				})
			})
		}
		tt := ui.NewTesterWith(view, 400, 300, ui.TesterOptions{Preferences: ui.Preferences{ReduceMotion: reduce, TextScale: 1}})
		tt.Frame()
		before, _ := tt.Find("Pane body")
		wide = true
		tt.Frame()
		after, found := tt.Find("Pane body")
		if !found {
			t.Fatalf("reduce motion %v: the pane lost its content", reduce)
		}
		if reduce {
			if after.W != 300 {
				t.Fatalf("with less motion the pane is still moving: %v of 300", after.W)
			}
			continue
		}
		if before.W != 100 {
			t.Fatalf("the pane did not start at its old size: %v", before.W)
		}
		if after.W >= 300 {
			t.Fatalf("the pane jumped to its new size instead of springing: %v", after.W)
		}
	}
}
