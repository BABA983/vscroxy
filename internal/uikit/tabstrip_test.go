package uikit

import (
	"fmt"
	"testing"

	"github.com/egoist/mygo/ui"
)

// The list of open tabs is an icon-only button with its count in the tip, and
// it steps aside while the fixed Live tab is all there is to list.
func TestTabStripOverflowIsAnIconUntilThereAreTabs(t *testing.T) {
	strip := func(tabs []DocumentTab) *ui.Tester {
		tt := ui.NewTester(func(c *ui.Context) {
			DocumentTabs(c, DocumentTabsOptions{Tabs: tabs, OnOverflow: func(*ui.Menu) {}})
		}, 400, 32)
		tt.Frame()
		return tt
	}
	live := strip([]DocumentTab{{Title: "Live", Fixed: true}})
	if _, ok := live.Find("All open tabs (1)"); ok {
		t.Fatalf("the overflow button lists only Live: %q", live.Texts())
	}
	open := strip([]DocumentTab{{Title: "Live", Fixed: true}, {Title: "Request", Closable: true}})
	if _, ok := open.Find("All open tabs (2)"); !ok {
		t.Fatalf("the overflow button has no count in its tip: %q", open.Texts())
	}
	for _, text := range open.Texts() {
		if text == "2" {
			t.Fatalf("the overflow button still draws its count: %q", open.Texts())
		}
	}
}

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
