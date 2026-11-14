package gallery

import (
	"fmt"

	"github.com/baba983/vscroxy/internal/uikit"
	"github.com/egoist/mygo/ui"
	"github.com/oneo-me/onevra/source/icons/lucide"
)

// foundations is the token plates: the palette in both themes, the four text
// sizes, spacing and corners, the two icon sizes and the kit's durations.
func (g *Gallery) foundations() *Group {
	return &Group{ID: "foundations", Title: "Foundations", Entries: []*Entry{
		{ID: "palette", Title: "Palette", Note: "Every Palette field, Light and Dark side by side", Height: 1120, build: palettePlate},
		{ID: "type", Title: "Type", Note: "Four sizes, two weights; monospace is wire data only", Height: 520, build: typePlate},
		{ID: "space", Title: "Space and corners", Note: "One spacing unit, three radii, the bar and control heights", Height: 560, build: spacePlate},
		{ID: "icons", Title: "Icons", Note: "16 inline with a label, 14 inline in text", Height: 420, build: iconPlate},
		{ID: "motion", Title: "Motion", Note: "Four durations and the kit's one spring", Height: 380, build: motionPlate},
	}}
}

// paletteFields is every semantic role, in the order the Palette declares them.
var paletteFields = []struct {
	name  string
	color func(*uikit.Palette) ui.Color
}{
	{"Chrome", func(p *uikit.Palette) ui.Color { return p.Chrome }},
	{"Canvas", func(p *uikit.Palette) ui.Color { return p.Canvas }},
	{"Well", func(p *uikit.Palette) ui.Color { return p.Well }},
	{"Hairline", func(p *uikit.Palette) ui.Color { return p.Hairline }},
	{"Fill", func(p *uikit.Palette) ui.Color { return p.Fill }},
	{"FillHover", func(p *uikit.Palette) ui.Color { return p.FillHover }},
	{"FillPressed", func(p *uikit.Palette) ui.Color { return p.FillPressed }},
	{"Text", func(p *uikit.Palette) ui.Color { return p.Text }},
	{"TextMuted", func(p *uikit.Palette) ui.Color { return p.TextMuted }},
	{"TextFaint", func(p *uikit.Palette) ui.Color { return p.TextFaint }},
	{"Accent", func(p *uikit.Palette) ui.Color { return p.Accent }},
	{"AccentHover", func(p *uikit.Palette) ui.Color { return p.AccentHover }},
	{"AccentPressed", func(p *uikit.Palette) ui.Color { return p.AccentPressed }},
	{"AccentText", func(p *uikit.Palette) ui.Color { return p.AccentText }},
	{"AccentTint", func(p *uikit.Palette) ui.Color { return p.AccentTint }},
	{"Success", func(p *uikit.Palette) ui.Color { return p.Success }},
	{"Info", func(p *uikit.Palette) ui.Color { return p.Info }},
	{"Warning", func(p *uikit.Palette) ui.Color { return p.Warning }},
	{"Danger", func(p *uikit.Palette) ui.Color { return p.Danger }},
	{"MethodGet", func(p *uikit.Palette) ui.Color { return p.MethodGet }},
	{"MethodPost", func(p *uikit.Palette) ui.Color { return p.MethodPost }},
	{"MethodPatch", func(p *uikit.Palette) ui.Color { return p.MethodPatch }},
	{"Socket", func(p *uikit.Palette) ui.Color { return p.Socket }},
	{"Inverse", func(p *uikit.Palette) ui.Color { return p.Inverse }},
	{"InverseText", func(p *uikit.Palette) ui.Color { return p.InverseText }},
	{"Shadow", func(p *uikit.Palette) ui.Color { return p.Shadow }},
}

func palettePlate(c *ui.Context) {
	t := c.Theme()
	ui.Row(c).Gap(t.Space(6)).AlignItems(ui.Start).Children(func() {
		for _, theme := range []struct {
			name string
			dark bool
		}{{"Light", false}, {"Dark", true}} {
			ui.Themed(c, uikit.Theme(theme.dark)).Grow(1).MinWidth(0).Gap(t.Space(1)).Padding(t.Space(3)).Radius(uikit.RadiusPanel).Children(func() {
				uikit.Label(c, theme.name)
				ui.Column(c).Gap(0).BorderWidth(1, 0, 0, 0).BorderColor(c.Theme().Border).Children(func() {
					for _, field := range paletteFields {
						pal := uikit.Colors(c.Theme())
						ui.Row(c).Height(32).Gap(t.Space(3)).AlignItems(ui.Center).
							BorderWidth(0, 0, 1, 0).BorderColor(c.Theme().Border).Children(func() {
							ui.Text(c, field.name).FontSize(uikit.Scaled(uikit.TextBody)).Grow(1).SingleLine()
							swatch(c, field.color(pal), pal)
							uikit.Mono(c, colorHex(field.color(pal))).Width(150).TextColor(c.Theme().TextMuted)
						})
					}
				})
			})
		}
	})
}

// swatch is one color over the surface it is used on, edged so a light color
// on a light canvas still has an edge.
func swatch(c *ui.Context, color ui.Color, pal *uikit.Palette) {
	ui.Box(c).Size(20, 20).Shrink(0).Radius(uikit.RadiusInner).Background(pal.Canvas).
		Border(1, pal.Hairline).Children(func() {
		ui.Box(c).Fill().Radius(uikit.RadiusInner).Background(color)
	})
}

// colorHex is a color as CSS writes it, with its alpha when it has one.
func colorHex(color ui.Color) string {
	hex := fmt.Sprintf("#%02x%02x%02x", color.R, color.G, color.B)
	if color.A < 255 {
		return fmt.Sprintf("%s · %d%%", hex, int(float32(color.A)/255*100+0.5))
	}
	return hex
}

func typePlate(c *ui.Context) {
	t := c.Theme()
	rows := []struct {
		role   string
		size   float32
		sample func(*ui.Context, string) ui.Element
		text   string
	}{
		{"Meta", uikit.TextMeta, uikit.Meta, "12 ms · 4.2 KB · 15:04:05 · 1,159 requests"},
		{"Data", uikit.TextData, uikit.Mono, "GET /v1/projects?page=2&limit=50"},
		{"Body", uikit.TextBody, uikit.Body, "The request was replayed with the draft's headers."},
		{"Title", uikit.TextTitle, uikit.Heading, "Request editor"},
	}
	ui.Column(c).Gap(t.Space(1)).Children(func() {
		for _, row := range rows {
			ui.Row(c).MinHeight(44).Gap(t.Space(4)).AlignItems(ui.Center).
				BorderWidth(0, 0, 1, 0).BorderColor(t.Border).Children(func() {
				ui.Column(c).Width(120).Gap(0).Children(func() {
					uikit.Label(c, row.role)
					uikit.Meta(c, fmt.Sprintf("%g px · %g at 1.35×", row.size, uikit.Scaled(row.size)))
				})
				row.sample(c, row.text).Grow(1).SingleLine()
			})
		}
	})
	ui.Row(c).Gap(t.Space(4)).Children(func() {
		uikit.Body(c, "Body 400").Grow(1)
		ui.Text(c, "Body 500").FontSize(uikit.Scaled(uikit.TextBody)).FontWeight(500).Grow(1)
		uikit.Mono(c, "Mono 400 · 0O1lI").Grow(1)
	})
}

func spacePlate(c *ui.Context) {
	t := c.Theme()
	ui.Column(c).Gap(t.Space(2)).Children(func() {
		uikit.Label(c, "Spacing · Space(n) is n × 4")
		for _, n := range []float32{0.5, 1, 1.5, 2, 3, 4, 6, 8} {
			ui.Row(c).Gap(t.Space(3)).AlignItems(ui.Center).Children(func() {
				uikit.Meta(c, fmt.Sprintf("Space(%g)", n)).Width(88)
				ui.Box(c).Height(12).Width(t.Space(n)).Radius(2).Background(uikit.Colors(t).FillPressed)
				uikit.Meta(c, fmt.Sprintf("%g px", t.Space(n)))
			})
		}
	})
	ui.Column(c).Gap(t.Space(2)).Children(func() {
		uikit.Label(c, "Corners")
		ui.Row(c).Gap(t.Space(4)).Children(func() {
			for _, r := range []struct {
				name   string
				radius float32
			}{{"RadiusInner", uikit.RadiusInner}, {"RadiusControl", uikit.RadiusControl}, {"RadiusPanel", uikit.RadiusPanel}} {
				ui.Column(c).Gap(t.Space(1)).Children(func() {
					ui.Box(c).Size(96, 56).Radius(r.radius).Background(uikit.Colors(t).Fill).
						Border(1, t.Border)
					uikit.Label(c, r.name)
					uikit.Meta(c, fmt.Sprintf("%g px", r.radius))
				})
			}
		})
	})
	ui.Column(c).Gap(t.Space(2)).Children(func() {
		uikit.Label(c, "Bars and controls")
		for _, h := range []struct {
			name   string
			height float32
		}{{"ControlHeight", uikit.ControlHeight}, {"ControlHeightSmall", uikit.ControlHeightSmall}, {"HeightBar", uikit.HeightBar}, {"HeightTabs", uikit.HeightTabs}, {"HeightLarge", uikit.HeightLarge}, {"HeightFooter", uikit.HeightFooter}, {"HeightControl", uikit.HeightControl}, {"HeightStatus", uikit.HeightStatus}} {
			ui.Row(c).Gap(t.Space(3)).AlignItems(ui.Center).Children(func() {
				uikit.Meta(c, h.name).Width(120)
				ui.Box(c).Height(h.height).Width(160).Radius(2).Background(uikit.Colors(t).Fill)
				uikit.Meta(c, fmt.Sprintf("%g px", h.height))
			})
		}
	})
}

func iconPlate(c *ui.Context) {
	t := c.Theme()
	icons := []struct {
		name string
		icon *ui.SVG
	}{
		{"LayoutList", lucide.LayoutList()},
		{"Search", lucide.Search()},
		{"Settings", lucide.Settings()},
		{"CircleAlert", lucide.CircleAlert()},
		{"Clock", lucide.Clock()},
		{"ChevronDown", lucide.ChevronDown()},
		{"X", lucide.X()},
	}
	ui.Row(c).Gap(t.Space(2)).AlignItems(ui.Center).Children(func() {
		uikit.Label(c, "Size").Width(120)
		uikit.Label(c, "16 · IconLarge").Width(160)
		uikit.Label(c, "14 · IconInline").Width(160)
	})
	ui.Column(c).Gap(0).Children(func() {
		for _, i := range icons {
			ui.Row(c).Height(36).Gap(t.Space(2)).AlignItems(ui.Center).
				BorderWidth(0, 0, 1, 0).BorderColor(t.Border).Children(func() {
				uikit.Mono(c, i.name).Width(120)
				ui.Icon(c, i.icon).Size(uikit.IconLarge, uikit.IconLarge).Width(160).TextColor(t.Text)
				ui.Icon(c, i.icon).Size(uikit.IconInline, uikit.IconInline).Width(160).TextColor(t.Text)
			})
		}
	})
	ui.Row(c).Gap(t.Space(2)).AlignItems(ui.Center).Children(func() {
		uikit.Hint(c, "An icon takes the ink of the text it sits in:").Width(200)
		uikit.Body(c, "Search").TextColor(t.Text)
		ui.Icon(c, lucide.Search()).Size(uikit.IconInline, uikit.IconInline)
		uikit.Meta(c, "12 results")
	})
}

func motionPlate(c *ui.Context) {
	t := c.Theme()
	ui.Column(c).Gap(t.Space(3)).Children(func() {
		for _, d := range []struct {
			name    string
			millis  float32
			usedFor string
		}{
			{"DurationFast", 80, "something leaving"},
			{"DurationHover", 120, "a face under the pointer"},
			{"DurationStandard", 160, "a chosen face or a changing number"},
			{"DurationSlow", 200, "a panel opening or closing"},
		} {
			ui.Row(c).Gap(t.Space(3)).AlignItems(ui.Center).Children(func() {
				uikit.Mono(c, d.name).Width(180)
				ui.Box(c).Height(10).Width(d.millis * 3).Radius(5).Background(uikit.Colors(t).Accent)
				uikit.Meta(c, fmt.Sprintf("%g ms", d.millis)).Width(64)
				uikit.Hint(c, d.usedFor)
			})
		}
	})
	ui.Row(c).Gap(t.Space(3)).AlignItems(ui.Center).Children(func() {
		uikit.Mono(c, "MotionSpring").Width(180)
		ui.Box(c).Height(10).Width(200).Radius(5).Background(uikit.Colors(t).AccentTint)
		uikit.Meta(c, "200 ms · no bounce")
		uikit.Hint(c, "panes and regions settle instead of stopping dead")
	})
}
