package uikit

import (
	"math"
	"testing"

	"github.com/egoist/mygo/ui"
)

// Tabs use the row's control height and a 5% ink wash under the chosen word;
// SubTabs keep their smaller, underlined word row.
func TestTabsUsePaneHeightAndWash(t *testing.T) {
	for _, tc := range []struct {
		name string
		dark bool
	}{{"light", false}, {"dark", true}} {
		t.Run(tc.name, func(t *testing.T) {
			theme := Theme(tc.dark)
			p := Colors(theme)
			selected := 0
			tt := ui.NewTester(func(c *ui.Context) {
				c.SetTheme(theme)
				ui.Column(c).Fill().Background(theme.Background).Gap(4).Children(func() {
					Tabs(c, &selected, "Pane", "Params", "Headers")
					SubTabs(c, &selected, "Response", "Body", "Cookies")
				})
			}, 320, 120)
			tt.Frame()
			for _, tc := range []struct {
				name string
				want float32
			}{
				{"Pane: Params", HeightRow},
				{"Response: Body", HeightControlSmall},
			} {
				a, ok := tt.Access(tc.name)
				if !ok {
					t.Fatalf("%s is missing", tc.name)
				}
				if a.Bounds.H != tc.want {
					t.Errorf("%s height = %.0f, want %.0f", tc.name, a.Bounds.H, tc.want)
				}
			}
			img := tt.Image()
			sample := func(name string) ui.Color {
				t.Helper()
				a, ok := tt.Access(name)
				if !ok {
					t.Fatalf("%s is missing", name)
				}
				got := img.RGBAAt(int(a.Bounds.X+3), int(a.Bounds.Y+a.Bounds.H/2))
				return ui.RGB(got.R, got.G, got.B)
			}
			same := func(got, want ui.Color) bool {
				return math.Abs(float64(int(got.R)-int(want.R))) <= 2 &&
					math.Abs(float64(int(got.G)-int(want.G))) <= 2 &&
					math.Abs(float64(int(got.B)-int(want.B))) <= 2
			}
			if got, want := sample("Pane: Params"), p.Foreground.Alpha(0.05).Over(theme.Background); !same(got, want) {
				t.Errorf("chosen tab face = %v, want %v", got, want)
			}
			if got, want := sample("Pane: Headers"), theme.Background; !same(got, want) {
				t.Errorf("resting tab face = %v, want %v", got, want)
			}
		})
	}
}
