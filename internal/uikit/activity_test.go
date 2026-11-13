package uikit

import (
	"image"
	"testing"

	"github.com/egoist/mygo/ui"
)

// A press the system cancels must not leave a time range behind, while a
// normal release still selects one.
func TestActivityCanceledPressKeepsSelection(t *testing.T) {
	var selected ActivityRange
	buckets := make([]ActivityBucket, 10)
	for i := range buckets {
		buckets[i][0] = 1
	}
	tt := ui.NewTester(func(c *ui.Context) {
		ActivityStrip(c, buckets, &selected, "10:30:05", func() {})
	}, 600, 120)
	plot, ok := tt.Find("Activity time range")
	if !ok {
		t.Fatal("activity plot missing")
	}
	y := plot.Y + plot.H/2
	tt.Press(plot.X+plot.W*0.2, y)
	tt.Move(plot.X+plot.W*0.7, y)
	tt.CancelPress()
	if selected != (ActivityRange{}) {
		t.Fatalf("canceled press left range %+v", selected)
	}

	tt.Press(plot.X+plot.W*0.2, y)
	tt.Move(plot.X+plot.W*0.7, y)
	tt.Release(plot.X+plot.W*0.7, y)
	if selected.To <= selected.From {
		t.Fatalf("normal press selected no range: %+v", selected)
	}
}

// A bucket with one request beside a busy one still draws a bar a person can
// see, and a chosen window is a face under the bars even where it holds no
// traffic.
func TestActivityStripDrawsReadableBarsAndABand(t *testing.T) {
	buckets := make([]ActivityBucket, 60)
	buckets[10][0] = 1
	buckets[30][0] = 20
	var selected ActivityRange
	tt := ui.NewTester(func(c *ui.Context) {
		ActivityStrip(c, buckets, &selected, "10:30:05", func() {})
	}, 600, 120)
	plot, ok := tt.Find("Activity time range")
	if !ok {
		t.Fatal("activity plot missing")
	}
	barHeight := func(im *image.RGBA, bucket int) int {
		x := int(plot.X + plot.W*(float32(bucket)+0.5)/60)
		height := 0
		for y := int(plot.Y+plot.H) - 1; y >= int(plot.Y); y-- {
			p := im.RGBAAt(x, y)
			if p.R >= 0x80 || p.G >= 0x80 || p.B >= 0x80 {
				break
			}
			height++
		}
		return height
	}
	im := tt.Image()
	one, busy := barHeight(im, 10), barHeight(im, 30)
	if one < 4 {
		t.Fatalf("a one-request bucket draws %d px, want a readable bar", one)
	}
	if busy <= one {
		t.Fatalf("a busy bucket draws %d px, no taller than a single request's %d", busy, one)
	}
	// Choose an empty stretch and look at its face.
	y := plot.Y + plot.H/2
	tt.Press(plot.X+plot.W*40/60, y)
	tt.Move(plot.X+plot.W*50/60, y)
	tt.Release(plot.X+plot.W*50/60, y)
	tt.Frame()
	im = tt.Image()
	band := im.RGBAAt(int(plot.X+plot.W*45/60), int(plot.Y+plot.H/2))
	canvas := im.RGBAAt(int(plot.X+plot.W*5/60), int(plot.Y+plot.H/2))
	if canvas.R-band.R < 15 {
		t.Fatalf("the chosen window's face is %v on a canvas of %v, too close to see", band, canvas)
	}
}

// The pane's menu and its extra tab are words, not empty strips: ButtonBase
// has no look of its own, so both have to draw their label.
func TestPaneMenusDrawTheirWords(t *testing.T) {
	tt := ui.NewTester(func(c *ui.Context) {
		ui.Row(c).Gap(8).Children(func() {
			MoreMenu(c, "Inspector options", func(*ui.Menu) {})
			TabMenu(c, "More", "Pane options", func(*ui.Menu) {})
		})
	}, 300, 60)
	tt.Frame()
	if _, ok := tt.Find("⋯"); !ok {
		t.Fatalf("the menu has no glyph: %q", tt.Texts())
	}
	if _, ok := tt.Find("More"); !ok {
		t.Fatalf("the tab has no label: %q", tt.Texts())
	}
}
