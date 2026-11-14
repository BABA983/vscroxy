package uikit

import (
	"testing"

	"github.com/egoist/mygo/ui"
)

// The kit has two control heights: the default for forms, toolbars and
// primary actions, and the small one for inline actions and chips.
func TestControlHeights(t *testing.T) {
	tt := ui.NewTester(func(c *ui.Context) {
		ui.Column(c).Gap(4).Children(func() {
			PrimaryButton(c, "").Label("Primary")
			SecondaryButton(c, "").Label("Secondary")
			ToolbarButton(c, "", "", false).Label("Toolbar")
			ChipButton(c, "", "").Label("Chip")
			CompactButton(c, "").Label("Compact")
			LinkButton(c, "").Label("Link")
			FooterButton(c, "", "", false).Label("Footer")
			IconButton(c, "Icon", "Icon", closeIcon)
		})
	}, 480, 360)
	tt.Frame()
	for _, tc := range []struct {
		label string
		want  float32
	}{
		{"Primary", ControlHeight},
		{"Secondary", ControlHeight},
		{"Toolbar", ControlHeight},
		{"Chip", ControlHeightSmall},
		{"Compact", ControlHeightSmall},
		{"Link", ControlHeightSmall},
		{"Footer", ControlHeightSmall},
		{"Icon", ControlHeight},
	} {
		r, ok := tt.Find(tc.label)
		if !ok {
			t.Fatalf("%s is missing", tc.label)
		}
		if r.H != tc.want {
			t.Errorf("%s height = %.0f, want %.0f", tc.label, r.H, tc.want)
		}
	}
}
