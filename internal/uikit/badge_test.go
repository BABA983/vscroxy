package uikit

import (
	"math"
	"testing"

	"github.com/egoist/mygo/ui"
)

// The small status badge tints its face with 10% of its signal color, and the
// quiet status pill is a full-pill, 28-point label.
func TestBadgeTint(t *testing.T) {
	theme := Theme(false)
	p := Colors(theme)
	tt := ui.NewTester(func(c *ui.Context) {
		c.SetTheme(theme)
		ui.Row(c).Gap(8).Padding(8).Children(func() {
			Badge(c, "Error", p.Danger)
			StatusPill(c, "Running", p.Success)
		})
	}, 320, 80)
	tt.Frame()
	image := tt.Image()
	badge, ok := tt.Access("Error")
	if !ok {
		t.Fatal("the badge is missing")
	}
	got := image.RGBAAt(int(badge.Bounds.X+2), int(badge.Bounds.Y+badge.Bounds.H/2))
	want := p.Danger.Alpha(0.10).Over(theme.Background)
	for _, channel := range []struct {
		name      string
		got, want uint8
	}{
		{"R", got.R, want.R}, {"G", got.G, want.G}, {"B", got.B, want.B},
	} {
		if math.Abs(float64(channel.got)-float64(channel.want)) > 2 {
			t.Errorf("badge %s = %d, want %d", channel.name, channel.got, channel.want)
		}
	}
	pill, ok := tt.Access("Running")
	if !ok {
		t.Fatal("the status pill is missing")
	}
	if pill.Bounds.H != HeightControl {
		t.Errorf("status pill height = %.0f, want %.0f", pill.Bounds.H, float32(HeightControl))
	}
}
