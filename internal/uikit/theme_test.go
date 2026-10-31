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
			for _, fill := range []ui.Color{theme.Background, p.Panel, theme.SurfaceHover, theme.SurfacePressed, p.AccentTint} {
				check("body", theme.Text, fill, 4.5)
				check("secondary", theme.TextMuted, fill, 3)
				check("keyboard focus", theme.Focus, fill, 3)
				for _, signal := range []ui.Color{p.Info, p.Warning, p.Danger, p.Accent} {
					check(fmt.Sprintf("signal %v", signal), signal, fill, 4.5)
				}
				for _, method := range []string{"GET", "POST", "PUT", "DELETE", "PATCH", "WS"} {
					check(method, MethodColor(method, theme), fill, 4.5)
				}
			}
			for _, fill := range []ui.Color{theme.Accent, theme.AccentHover, theme.AccentPressed} {
				check("primary action", theme.AccentText, fill, 4.5)
			}
			check("tooltip", theme.InverseText, theme.Inverse, 4.5)
			// Status pills: quiet statuses are untinted, the rest sit on a 12% tint.
			for _, status := range []int{200, 304, 302, 404, 503} {
				ink, fill := StatusColor(status, theme), theme.Background
				if ink != theme.TextMuted {
					fill = ink.Alpha(0.12).Over(theme.Background)
				}
				check(fmt.Sprintf("status %d", status), ink, fill, 4.5)
			}
		})
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
