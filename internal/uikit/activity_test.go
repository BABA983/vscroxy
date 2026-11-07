package uikit

import (
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
		ActivityStrip(c, buckets, &selected, "10 requests", [3]string{"a", "b", "c"}, func() {})
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
