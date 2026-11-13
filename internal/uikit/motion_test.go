package uikit

import (
	"testing"
	"time"

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

// A face under the pointer changes at once; the one the pointer leaves fades
// back to rest, so the frames after it left are neither the hover color nor
// the resting one. A face that drops its transition on the hovered frame is a
// new element to MyGo and jumps back instead.
func TestHoverFaceFadesAfterThePointerLeaves(t *testing.T) {
	view := func(c *ui.Context) {
		ui.Column(c).Fill().Padding(24).AlignItems(ui.Start).Children(func() {
			FooterButton(c, "Face", "", false)
		})
	}
	tt := ui.NewTester(view, 240, 120)
	tt.Frame()
	face, found := tt.Find("Face")
	if !found || face.W < 12 {
		t.Fatalf("no face to hover: %+v", face)
	}
	sample := func() uint8 {
		return tt.Image().RGBAAt(int(face.X)+1, int(face.Y+face.H/2)).R
	}
	rest := sample()
	tt.Move(face.X+face.W/2, face.Y+face.H/2)
	tt.Frame()
	hover := sample()
	if hover == rest {
		t.Fatalf("the face did not change under the pointer: %d", hover)
	}
	tt.Move(232, 112)
	tt.Frame()
	tt.Advance(40 * time.Millisecond)
	leaving := sample()
	lo, hi := min(rest, hover), max(rest, hover)
	if leaving <= lo+1 || leaving >= hi-1 {
		t.Fatalf("40 ms after the pointer left the face is already %d, between %d and %d", leaving, lo, hi)
	}
	tt.Advance(200 * time.Millisecond)
	if back := sample(); back != rest {
		t.Fatalf("the face settled at %d instead of its resting %d", back, rest)
	}
}
