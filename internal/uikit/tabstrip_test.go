package uikit

import (
	"fmt"
	"testing"

	"github.com/egoist/mygo/ui"
)

// A tab strip is one row, so a mouse wheel's vertical notch moves it
// sideways rather than doing nothing.
func TestTabStripWheelMovesSideways(t *testing.T) {
	tt := ui.NewTester(func(c *ui.Context) {
		ui.Row(c).Fill().Children(func() {
			tabStripOn(c, ui.Color{}, func() {
				for i := range 10 {
					ui.Box(c).Width(100).Height(32).Label(fmt.Sprintf("tab %d", i))
				}
			}).Fill()
		})
	}, 300, 32)
	tt.Frame()
	before, ok := tt.Find("tab 5")
	if !ok {
		t.Fatal("no tab 5")
	}
	tt.Move(150, 16)
	tt.Frame()
	tt.Wheel(150, 16, 0, 120)
	tt.Frame()
	after, ok := tt.Find("tab 5")
	if !ok {
		t.Fatal("no tab 5 after the wheel")
	}
	if got := before.X - after.X; got != 120 {
		t.Fatalf("the wheel moved the strip %.0f, want 120", got)
	}
}
