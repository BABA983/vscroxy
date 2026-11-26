package uikit

import (
	"math"
	"testing"

	"github.com/egoist/mygo/ui"
)

// LinkButton paints its own label: the accessibility name alone left the
// inline action blank.
func TestLinkButtonShowsItsText(t *testing.T) {
	tt := ui.NewTester(func(c *ui.Context) { LinkButton(c, "Add property") }, 320, 80)
	tt.Frame()
	if !tt.HasText("Add property") {
		t.Fatalf("LinkButton did not draw its label: %q", tt.Texts())
	}
}

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
		{"Primary", HeightControl},
		{"Secondary", HeightSecondary},
		{"Toolbar", HeightControl},
		{"Chip", HeightControlSmall},
		{"Compact", HeightControlSmall},
		{"Link", HeightControlSmall},
		{"Footer", HeightControlSmall},
		{"Icon", HeightControl},
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

// The primary button is ink, and hover and pressed share one face: the ink
// lifts in Light and lowers in Dark. Disabled keeps the resting face at
// MyGo's half opacity. None of them changes the button's geometry.
func TestPrimaryButtonFaces(t *testing.T) {
	for _, tc := range []struct {
		dark                     bool
		rest, active, foreground ui.Color
	}{
		{false, ui.Hex("#171917"), ui.Hex("#2f312f"), ui.Hex("#fafaf8")},
		{true, ui.Hex("#eaf3e9"), ui.Hex("#d3dbd2"), ui.Hex("#0b0d0b")},
	} {
		theme := Theme(tc.dark)
		p := Colors(theme)
		if p.Primary != tc.rest || p.PrimaryHover != tc.active || p.PrimaryPressed != tc.active || p.PrimaryForeground != tc.foreground {
			t.Fatalf("dark=%t: primary palette = %v/%v/%v/%v", tc.dark, p.Primary, p.PrimaryHover, p.PrimaryPressed, p.PrimaryForeground)
		}
		tt := ui.NewTester(func(c *ui.Context) {
			c.SetTheme(theme)
			ui.Column(c).Gap(8).Background(theme.Background).Children(func() {
				PrimaryButton(c, "Rest")
				PrimaryButtonState(c, "Hover", ButtonHover)
				PrimaryButtonState(c, "Pressed", ButtonPressed)
				PrimaryButton(c, "Disabled").Disabled(true)
			})
		}, 260, 240)
		tt.Frame()
		image := tt.Image()
		for _, face := range []struct {
			label string
			want  ui.Color
		}{
			{"Rest", tc.rest},
			{"Hover", tc.active},
			{"Pressed", tc.active},
			{"Disabled", tc.rest.Alpha(0.5).Over(theme.Background)},
		} {
			button, ok := tt.AccessByRole(ui.RoleButton, face.label)
			if !ok {
				t.Fatalf("dark=%t: %s is missing", tc.dark, face.label)
			}
			r := button.Bounds
			got := image.RGBAAt(int(r.X+6), int(r.Y+r.H/2))
			for _, channel := range []struct {
				name      string
				got, want uint8
			}{
				{"R", got.R, face.want.R}, {"G", got.G, face.want.G}, {"B", got.B, face.want.B},
			} {
				if math.Abs(float64(channel.got)-float64(channel.want)) > 1 {
					t.Errorf("dark=%t: %s %s = %d, want %d", tc.dark, face.label, channel.name, channel.got, channel.want)
				}
			}
		}
	}
}
