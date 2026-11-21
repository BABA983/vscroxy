package uikit

import (
	"fmt"
	"math"
	"testing"
	"time"

	"github.com/egoist/mygo/ui"
	"github.com/egoist/mygo/ui/editor"
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
			// Dark, so they are held to the non-text 3:1 there.
			type swatch struct {
				name string
				fill ui.Color
			}
			surfaces := []swatch{{"canvas", p.Background}, {"chrome", p.Window}, {"well", p.Input}}
			var faces []swatch
			for _, s := range surfaces {
				faces = append(faces,
					swatch{"hover on " + s.name, p.MutedHover.Over(s.fill)},
					swatch{"pressed on " + s.name, p.MutedPressed.Over(s.fill)},
					swatch{"selection on " + s.name, p.Accent.Over(s.fill)})
			}
			for _, f := range append(surfaces, faces...) {
				check("body on "+f.name, theme.Text, f.fill, 4.5)
				check("secondary on "+f.name, theme.TextMuted, f.fill, 3)
				check("keyboard focus on "+f.name, theme.Focus, f.fill, 3)
			}
			for _, f := range surfaces {
				for _, signal := range []ui.Color{p.Info, p.Warning, p.Danger, p.Primary, p.Success} {
					check(fmt.Sprintf("signal %v on %s", signal, f.name), signal, f.fill, 4.5)
				}
				for _, method := range []string{"GET", "POST", "PUT", "DELETE", "PATCH", "WS"} {
					check(method+" on "+f.name, MethodColor(method, theme), f.fill, 4.5)
				}
			}
			for _, f := range faces {
				for _, signal := range []ui.Color{p.Info, p.Warning, p.Danger, p.Primary, p.Success} {
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
			// A focused field and a keyboard focus ring draw Ring inside their
			// face; it must read against that face and the surface around it,
			// without being the ink accent a primary action is.
			check("field focus edge", p.Ring, p.Background, 3)
			check("field focus edge on a chrome field", p.Ring, p.Background, 3)
			check("field focus edge beside the canvas", p.Ring, p.Background, 3)
			check("field focus edge beside chrome", p.Ring, p.Window, 3)
			// An unchecked box or radio draws Ring as its edge on each surface;
			// its face is a 7% tint, too faint to be the only sign of a box.
			check("checkbox edge on canvas", p.Ring, p.Background, 3)
			check("checkbox edge on chrome", p.Ring, p.Window, 3)
			check("checkbox edge on its well", p.Ring, p.Input, 3)
			check("checkbox edge on its face", p.Ring, p.Muted.Over(p.Background), 3)
			if p.Ring == p.Primary || p.Ring == p.MutedForeground {
				t.Fatal("Ring must be its own focus color, not the accent or muted text")
			}
			check("tooltip", theme.InverseText, theme.Inverse, 4.5)
			// The duration bar is a normal quantity behind its number; a slow
			// one is the danger tint. Both carry body text.
			check("duration on bar", theme.Text, p.Muted.Over(p.Background), 4.5)
			check("slow duration", theme.Text, p.Danger.Alpha(0.16).Over(p.Background), 4.5)
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
		if p.Success == p.Primary || theme.Success != p.Success {
			t.Fatalf("dark=%t: Success must be its own green, not the accent", dark)
		}
	}
}

// The floating radius scale and shadow layers stay fixed; Dark
// swaps the ring color and deepens the menu's lower layers.
func TestFloatingScaleTokens(t *testing.T) {
	if RadiusMenu != 6 || RadiusCard != 12 || RadiusDialog != 16 {
		t.Fatalf("radii = %v/%v/%v, want 6/12/16", RadiusMenu, RadiusCard, RadiusDialog)
	}
	if len(ShadowPopover.layers) != 3 || ShadowPopover.layers[1] != (shadowLayer{0, 4, 6, -1, ui.RGBA(0, 0, 0, 0.1), ui.RGBA(0, 0, 0, 0.1)}) {
		t.Fatalf("popover shadow = %+v", ShadowPopover.layers)
	}
	if len(ShadowDialog.layers) != 2 || ShadowDialog.layers[0].y != 10 || ShadowDialog.layers[0].blur != 15 || ShadowDialog.layers[1].spread != -4 {
		t.Fatalf("dialog shadow = %+v", ShadowDialog.layers)
	}
	if ShadowMenu.layers[0].dark != ui.RGBA(255, 255, 255, 0.1) || ShadowMenu.layers[1].dark.A < 200 {
		t.Fatalf("dark menu shadow = %+v", ShadowMenu.layers)
	}
	if Light.Window != ui.Hex("#eff3ee") || Light.Background != ui.Hex("#f7fbf6") || Light.Input != ui.Hex("#e8ede5") {
		t.Fatalf("Light surfaces = %v/%v/%v", Light.Window, Light.Background, Light.Input)
	}
	if Dark.Window != ui.Hex("#070a08") || Dark.Background != ui.Hex("#101612") || Dark.Input != ui.Hex("#171f1a") {
		t.Fatalf("Dark surfaces = %v/%v/%v", Dark.Window, Dark.Background, Dark.Input)
	}
	if Light.Scrim != ui.RGBA(0, 0, 0, 0.35) {
		t.Fatalf("Light scrim = %v", Light.Scrim)
	}
	if got := Theme(false).TooltipDelay; got != 120*time.Millisecond {
		t.Fatalf("tooltip delay = %v, want 120ms", got)
	}
	if TipPaddingX != 4 || TipPaddingY != 2 || TipMaxWidth != 450 {
		t.Fatalf("tooltip metrics = %d/%d/%d, want 4/2/450", TipPaddingX, TipPaddingY, TipMaxWidth)
	}
	if TextMedium != 15 || LineMedium != 20.0/15.0 || LineMeta != 13.6/11.0 || LineProse != LineBody {
		t.Fatal("typography scale differs from the text and prose scale")
	}
	if CardTipWidth != 320 || CardTipPaddingX != 10 || CardTipPaddingY != 10 || CardTipPaddingBottom != 8 || cardTipDelay != 650*time.Millisecond {
		t.Fatalf("card metrics = %d/%d/%d/%d/%v", CardTipWidth, CardTipPaddingX, CardTipPaddingY, CardTipPaddingBottom, cardTipDelay)
	}
}

// The keyboard ring is one point wide, on the element's edge.
func TestFocusRingOnePoint(t *testing.T) {
	for _, dark := range []bool{false, true} {
		theme := Theme(dark)
		if theme.FocusRingWidth != 1 || theme.FocusRingOffset != 0 {
			t.Errorf("dark=%t: focus ring = %v wide, %v out, want 1/0", dark, theme.FocusRingWidth, theme.FocusRingOffset)
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
			{plot.X + plot.W/4, plot.Y + 11, palette.MutedForeground},
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
	if got := Scaled(TextSmall); got != 18 {
		t.Fatalf("small size at 1.5 = %v, want 18", got)
	}
	if got := Scaled(TextTitle); got != 24 {
		t.Fatalf("title size at 1.5 = %v, want 24", got)
	}
	if got := Scaled(TextDisplay); got != 30 {
		t.Fatalf("display size at 1.5 = %v, want 30", got)
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

// The prose rules set the Markdown view's body, block gaps and heading
// rhythm; MyGo takes all three in ems, so the values cross over.
func TestMarkdownThemeProse(t *testing.T) {
	th := MarkdownTheme(Theme(false))
	if th.FontSize != 13 || th.LineHeight != LineBody || th.CodeSize != 0.85 {
		t.Fatalf("markdown body = %v/%v/code %v", th.FontSize, th.LineHeight, th.CodeSize)
	}
	if th.BlockSpacing != 0.75 || th.ItemSpacing != 0.375 {
		t.Fatalf("markdown gaps = %v/%v, want 0.75/0.375", th.BlockSpacing, th.ItemSpacing)
	}
	for i, want := range []struct {
		size, top, bottom float32
		weight            int
	}{
		{1.5, 2.5, 0.5, 700},
		{1.25, 2, 0.5, 600},
		{1, 1.5, 0.5, 600},
	} {
		h := th.Headings[i]
		if h.Size != want.size || h.Top != want.top || h.Bottom != want.bottom || h.Weight != want.weight {
			t.Errorf("H%d = %+v, want %+v", i+1, h, want)
		}
	}
}

// The editor uses the GitHub Light/Dark token palette that @pierre/diffs emits
// as --diffs-token-light/dark, not the syntax colors used for prose.
func TestEditorThemeTokenPalette(t *testing.T) {
	for _, dark := range []bool{false, true} {
		theme := Theme(dark)
		p := Colors(theme)
		th := EditorTheme(theme)
		for _, tc := range []struct {
			name           string
			got            ui.Color
			light, hexDark string
		}{
			{"foreground", p.EditorForeground, "#24292e", "#e1e4e8"},
			{"keyword", p.EditorKeyword, "#d73a49", "#f97583"},
			{"entity", p.EditorEntity, "#6f42c1", "#b392f0"},
			{"tag", p.EditorTag, "#22863a", "#85e89d"},
			{"variable", p.EditorVariable, "#e36209", "#ffab70"},
			{"constant", p.EditorConstant, "#005cc5", "#79b8ff"},
			{"string", p.EditorString, "#032f62", "#9ecbff"},
			{"regexp", p.EditorRegexp, "#032f62", "#dbedff"},
			{"comment", p.EditorComment, "#6a737d", "#6a737d"},
			{"invalid", p.EditorInvalid, "#b31d28", "#fdaeb7"},
		} {
			hex := tc.light
			if dark {
				hex = tc.hexDark
			}
			if want := ui.Hex(hex); tc.got != want {
				t.Errorf("dark=%t: %s = %v, want %v", dark, tc.name, tc.got, want)
			}
		}
		if th.Text != p.EditorForeground {
			t.Errorf("dark=%t: editor text = %v, want %v", dark, th.Text, p.EditorForeground)
		}
		for _, tc := range []struct {
			kind  editor.TokenKind
			color ui.Color
		}{
			{editor.TokenKeyword, p.EditorKeyword},
			{editor.TokenOperator, p.EditorKeyword},
			{editor.TokenType, p.EditorEntity},
			{editor.TokenFunction, p.EditorEntity},
			{editor.TokenMeta, p.EditorEntity},
			{editor.TokenVariable, p.EditorVariable},
			{editor.TokenProperty, p.EditorConstant},
			{editor.TokenAttribute, p.EditorConstant},
			{editor.TokenConstant, p.EditorConstant},
			{editor.TokenNumber, p.EditorConstant},
			{editor.TokenString, p.EditorString},
			{editor.TokenEscape, p.EditorConstant},
			{editor.TokenRegexp, p.EditorRegexp},
			{editor.TokenTag, p.EditorTag},
			{editor.TokenComment, p.EditorComment},
			{editor.TokenHeading, p.EditorConstant},
			{editor.TokenLink, p.EditorString},
			{editor.TokenCode, p.EditorConstant},
			{editor.TokenInvalid, p.EditorInvalid},
			{editor.TokenInserted, p.DiffAdded},
			{editor.TokenDeleted, p.DiffRemoved},
		} {
			style := th.Tokens[tc.kind]
			if style.Color != tc.color || style.Italic || style.Bold {
				t.Errorf("dark=%t: token %d = %+v, want color %v with no face", dark, tc.kind, style, tc.color)
			}
		}
		if th.Added != p.DiffAdded || th.Deleted != p.DiffRemoved || th.Modified != p.DiffMeta {
			t.Errorf("dark=%t: diff inks = %v/%v/%v", dark, th.Added, th.Deleted, th.Modified)
		}
	}
}
