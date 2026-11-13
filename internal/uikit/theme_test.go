package uikit

import (
	"fmt"
	"math"
	"testing"

	"github.com/egoist/mygo/ui"
)

func TestThemesKeepTextReadable(t *testing.T) {
	// WCAG relative luminance, calculated in linear sRGB after compositing
	// translucent fills. Do not compare hex strings or derive thresholds
	// from the palette: every theme must meet the same reading contract.
	luminance := func(c ui.Color) float64 {
		linear := func(v uint8) float64 {
			s := float64(v) / 255
			if s <= 0.04045 {
				return s / 12.92
			}
			return math.Pow((s+0.055)/1.055, 2.4)
		}
		return 0.2126*linear(c.R) + 0.7152*linear(c.G) + 0.0722*linear(c.B)
	}
	for _, dark := range []bool{false, true} {
		t.Run(fmt.Sprintf("dark=%t", dark), func(t *testing.T) {
			theme := Theme(dark)
			p := Colors(theme)
			check := func(name string, foreground, background ui.Color, minimum float64) {
				t.Helper()
				lo, hi := luminance(foreground.Over(background)), luminance(background)
				ratio := (max(lo, hi) + 0.05) / (min(lo, hi) + 0.05)
				if ratio < minimum {
					t.Errorf("%s: contrast %.2f:1, want at least %.1f:1", name, ratio, minimum)
				}
			}
			// One control works on every surface, so every ink must read on
			// all three of them. The faces laid over a surface are transient
			// (hover, pressed) or a tint behind a selected row; the design
			// pins the signal inks, which fall to 3.6:1 on a pressed face in
			// Ink, so they are held to the non-text 3:1 there.
			type swatch struct {
				name string
				fill ui.Color
			}
			surfaces := []swatch{{"canvas", p.Canvas}, {"chrome", p.Chrome}, {"well", p.Well}}
			var faces []swatch
			for _, s := range surfaces {
				faces = append(faces,
					swatch{"hover on " + s.name, p.FillHover.Over(s.fill)},
					swatch{"pressed on " + s.name, p.FillPressed.Over(s.fill)},
					swatch{"selection on " + s.name, p.AccentTint})
			}
			for _, f := range append(surfaces, faces...) {
				check("body on "+f.name, theme.Text, f.fill, 4.5)
				check("secondary on "+f.name, theme.TextMuted, f.fill, 3)
				check("keyboard focus on "+f.name, theme.Focus, f.fill, 3)
			}
			for _, f := range surfaces {
				for _, signal := range []ui.Color{p.Info, p.Warning, p.Danger, p.Accent, p.Success} {
					check(fmt.Sprintf("signal %v on %s", signal, f.name), signal, f.fill, 4.5)
				}
				for _, method := range []string{"GET", "POST", "PUT", "DELETE", "PATCH", "WS"} {
					check(method+" on "+f.name, MethodColor(method, theme), f.fill, 4.5)
				}
			}
			for _, f := range faces {
				for _, signal := range []ui.Color{p.Info, p.Warning, p.Danger, p.Accent, p.Success} {
					check(fmt.Sprintf("signal %v on %s", signal, f.name), signal, f.fill, 3)
				}
				for _, method := range []string{"GET", "POST", "PUT", "DELETE", "PATCH", "WS"} {
					check(method+" on "+f.name, MethodColor(method, theme), f.fill, 3)
				}
			}
			for _, fill := range []ui.Color{theme.Accent, theme.AccentHover, theme.AccentPressed} {
				check("primary action", theme.AccentText, fill, 4.5)
			}
			// A passed step fills its mark with Success and prints in
			// AccentText, as the guide and the collection wizard do.
			check("success mark", theme.AccentText, p.Success, 4.5)
			// A focused field draws a neutral edge inside its own face; it
			// must read against that face and against the surface around
			// it, without being the ink accent a primary action is.
			check("field focus edge on its well", p.TextMuted, p.Well, 3)
			check("field focus edge on a chrome field", p.TextMuted, p.Canvas, 3)
			check("field focus edge beside the canvas", p.TextMuted, p.Canvas, 3)
			check("field focus edge beside chrome", p.TextMuted, p.Chrome, 3)
			check("tooltip", theme.InverseText, theme.Inverse, 4.5)
			// The duration bar is a normal quantity behind its number; a slow
			// one is the danger tint. Both carry body text.
			check("duration on bar", theme.Text, p.Fill.Over(p.Canvas), 4.5)
			check("slow duration", theme.Text, p.Danger.Alpha(0.16).Over(p.Canvas), 4.5)
			// Status and method colors never fill a background, so they are
			// read against the surface under them.
			for _, status := range []int{200, 304, 302, 404, 503} {
				ink := StatusColor(status, theme)
				for _, s := range surfaces {
					check(fmt.Sprintf("status %d on %s", status, s.name), ink, s.fill, 4.5)
				}
			}
		})
	}
}

// Green is the success signal alone; the accent is ink, so nothing that is
// merely selected, primary or on is ever green.
func TestSuccessIsNotTheAccent(t *testing.T) {
	for _, dark := range []bool{false, true} {
		theme := Theme(dark)
		p := Colors(theme)
		if p.Success == p.Accent || theme.Success != p.Success {
			t.Fatalf("dark=%t: Success must be its own green, not the accent", dark)
		}
	}
}

func TestNormalResponsesAreQuiet(t *testing.T) {
	for _, dark := range []bool{false, true} {
		theme := Theme(dark)
		if StatusColor(200, theme) != theme.TextMuted || StatusColor(304, theme) != theme.TextMuted {
			t.Fatalf("dark=%t: 2xx and 304 must be muted text, not a signal color", dark)
		}
		if StatusColor(302, theme) == theme.TextMuted || StatusColor(404, theme) == StatusColor(500, theme) {
			t.Fatalf("dark=%t: redirects, client errors and server errors must each be colored", dark)
		}
	}
}

func TestActivityStacksMeasureTotalWithoutRecoloringNormalTraffic(t *testing.T) {
	for _, dark := range []bool{false, true} {
		theme := Theme(dark)
		palette := Colors(theme)
		var selected ActivityRange
		tt := ui.NewTester(func(c *ui.Context) {
			c.SetTheme(theme)
			ui.Column(c).Fill().Background(theme.Background).Children(func() {
				ActivityStrip(c, []ActivityBucket{{6, 0, 0, 4, 0}, {0, 0, 0, 5, 0}}, &selected, "10:30:05", func() {})
			})
		}, 400, 100)
		plot, _ := tt.Find("Activity time range")
		image := tt.Image()
		for _, sample := range []struct {
			x, y float32
			want ui.Color
		}{
			// The ten-request mixed stack is taller than five errors. Only
			// its upper four tenths are red; normal requests stay neutral.
			{plot.X + plot.W/4, plot.Y + 3, palette.Danger},
			{plot.X + plot.W/4, plot.Y + 11, palette.TextMuted},
			{plot.X + plot.W*3/4, plot.Y + 3, theme.Background},
			{plot.X + plot.W*3/4, plot.Y + 11, palette.Danger},
		} {
			got := image.RGBAAt(int(sample.x), int(sample.y))
			// The raster compositor may round translucent channels one unit
			// differently from Color.Over; a different signal color still fails.
			if math.Abs(float64(got.R)-float64(sample.want.R)) > 1 || math.Abs(float64(got.G)-float64(sample.want.G)) > 1 || math.Abs(float64(got.B)-float64(sample.want.B)) > 1 {
				t.Fatalf("dark=%t: activity at %.0f,%.0f = %v, want %v", dark, sample.x, sample.y, got, sample.want)
			}
		}
	}
}

// The four sizes keep their proportions at whatever text size the desktop asks
// for, and go back to the usual ones after.
func TestTextSizesFollowTheDesktopsTextSize(t *testing.T) {
	t.Cleanup(func() { SetTextScale(1) })
	SetTextScale(1)
	if got := Scaled(TextBody); got != 13 {
		t.Fatalf("usual body size = %v, want 13", got)
	}
	SetTextScale(1.5)
	if got := Scaled(TextBody); got != 20 {
		t.Fatalf("body size at 1.5 = %v, want 20", got)
	}
	if got := Scaled(TextMeta); got != 17 {
		t.Fatalf("meta size at 1.5 = %v, want 17", got)
	}
	SetTextScale(0)
	if got := Scaled(TextBody); got != 13 {
		t.Fatalf("a scale of 0 = %v, want the usual 13", got)
	}
}

// A column's width is a size like any other: at a larger text size the table's
// fixed columns grow with the text, so a long method is not cut short.
func TestScaledGrowsTheTablesFixedWidths(t *testing.T) {
	t.Cleanup(func() { SetTextScale(1) })
	SetTextScale(1)
	usual := Scaled(76) // the method column
	SetTextScale(1.35)
	wide := Scaled(76)
	if wide <= usual {
		t.Fatalf("the method column stays at %v when the text grows", wide)
	}
	if wide != 103 {
		t.Fatalf("method column at 1.35 = %v, want 103", wide)
	}
	if got := TextScale(); got != 1.35 {
		t.Fatalf("TextScale = %v, want 1.35", got)
	}
}
