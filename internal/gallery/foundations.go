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
		{ID: "type", Title: "Type", Note: "Five sizes, two weights; monospace is wire data only", Height: 620, build: typePlate},
		{ID: "space", Title: "Space, corners and sizes", Note: "Named spacing, six radii, bar, control and row heights, and field widths", Height: 1000, build: spacePlate},
		{ID: "insets", Title: "Insets", Note: "The padding each surface gives its content", Height: 560, build: insetPlate},
		{ID: "elevation", Title: "Elevation", Note: "Three shadows and the dialog scrim, Light and Dark", Height: 520, build: elevationPlate},
		{ID: "icons", Title: "Icons", Note: "16 inline with a label, 14 inline in text", Height: 420, build: iconPlate},
		{ID: "motion", Title: "Motion", Note: "No decorative transition; what still moves is state or the user's own drag", Height: 360, build: motionPlate},
	}}
}

// paletteFields is every semantic role, in the order the Palette declares them.
var paletteFields = []struct {
	name  string
	color func(*uikit.Palette) ui.Color
}{
	{"Window", func(p *uikit.Palette) ui.Color { return p.Window }},
	{"Background", func(p *uikit.Palette) ui.Color { return p.Background }},
	{"Input", func(p *uikit.Palette) ui.Color { return p.Input }},
	{"Border", func(p *uikit.Palette) ui.Color { return p.Border }},
	{"Muted", func(p *uikit.Palette) ui.Color { return p.Muted }},
	{"MutedHover", func(p *uikit.Palette) ui.Color { return p.MutedHover }},
	{"MutedPressed", func(p *uikit.Palette) ui.Color { return p.MutedPressed }},
	{"Foreground", func(p *uikit.Palette) ui.Color { return p.Foreground }},
	{"MutedForeground", func(p *uikit.Palette) ui.Color { return p.MutedForeground }},
	{"Faint", func(p *uikit.Palette) ui.Color { return p.Faint }},
	{"Primary", func(p *uikit.Palette) ui.Color { return p.Primary }},
	{"PrimaryHover", func(p *uikit.Palette) ui.Color { return p.PrimaryHover }},
	{"PrimaryPressed", func(p *uikit.Palette) ui.Color { return p.PrimaryPressed }},
	{"PrimaryForeground", func(p *uikit.Palette) ui.Color { return p.PrimaryForeground }},
	{"Accent", func(p *uikit.Palette) ui.Color { return p.Accent }},
	{"Ring", func(p *uikit.Palette) ui.Color { return p.Ring }},
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
	{"Scrim", func(p *uikit.Palette) ui.Color { return p.Scrim }},
	{"Shadow", func(p *uikit.Palette) ui.Color { return p.Shadow }},
}

func palettePlate(c *ui.Context) {
	t := c.Theme()
	ui.Row(c).Gap(t.Space(uikit.Space2XL)).AlignItems(ui.Start).Children(func() {
		for _, theme := range []struct {
			name string
			dark bool
		}{{"Light", false}, {"Dark", true}} {
			ui.Themed(c, uikit.Theme(theme.dark)).Grow(1).MinWidth(0).Gap(t.Space(uikit.SpaceXS)).Padding(t.Space(uikit.SpaceL)).Radius(uikit.RadiusPanel).Children(func() {
				uikit.Label(c, theme.name)
				ui.Column(c).Gap(0).BorderWidth(1, 0, 0, 0).BorderColor(c.Theme().Border).Children(func() {
					for _, field := range paletteFields {
						pal := uikit.Colors(c.Theme())
						ui.Row(c).Height(32).Gap(t.Space(uikit.SpaceL)).AlignItems(ui.Center).
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
	ui.Box(c).Size(20, 20).Shrink(0).Radius(uikit.RadiusInner).Background(pal.Background).
		Border(1, pal.Border).Children(func() {
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
		{"Small", uikit.TextSmall, uikit.Small, "The certificate is checked against the system roots."},
		{"Body", uikit.TextBody, uikit.Body, "The request was replayed with the draft's headers."},
		{"Title", uikit.TextTitle, uikit.Heading, "Request editor"},
		{"Display", uikit.TextDisplay, uikit.Display, "Choose where to start"},
	}
	ui.Column(c).Gap(t.Space(uikit.SpaceXS)).Children(func() {
		for _, row := range rows {
			ui.Row(c).MinHeight(44).Gap(t.Space(uikit.SpaceXL)).AlignItems(ui.Center).
				BorderWidth(0, 0, 1, 0).BorderColor(t.Border).Children(func() {
				ui.Column(c).Width(uikit.WidthNarrow).Gap(0).Children(func() {
					uikit.Label(c, row.role)
					uikit.Meta(c, fmt.Sprintf("%g px · %g at 1.35×", row.size, uikit.Scaled(row.size)))
				})
				row.sample(c, row.text).Grow(1).SingleLine()
			})
		}
	})
	ui.Row(c).Gap(t.Space(uikit.SpaceXL)).Children(func() {
		uikit.Body(c, "Body 400").Grow(1)
		ui.Text(c, "Body 500").FontSize(uikit.Scaled(uikit.TextBody)).FontWeight(500).Grow(1)
		uikit.Mono(c, "Mono 400 · 0O1lI").Grow(1)
	})
}

func spacePlate(c *ui.Context) {
	t := c.Theme()
	ui.Column(c).Gap(t.Space(uikit.SpaceM)).Children(func() {
		uikit.Label(c, "Spacing · each name is a multiple of the 4-point grid")
		for _, s := range []struct {
			name string
			n    float32
		}{{"SpaceHair", uikit.SpaceHair}, {"SpaceXS", uikit.SpaceXS}, {"SpaceS", uikit.SpaceS}, {"SpaceM", uikit.SpaceM}, {"SpaceCompact", uikit.SpaceCompact}, {"SpaceL", uikit.SpaceL}, {"SpaceXL", uikit.SpaceXL}, {"SpaceXXL", uikit.SpaceXXL}, {"Space2XL", uikit.Space2XL}, {"Space3XL", uikit.Space3XL}} {
			ui.Row(c).Gap(t.Space(uikit.SpaceL)).AlignItems(ui.Center).Children(func() {
				uikit.Meta(c, s.name).Width(uikit.WidthNarrow)
				ui.Box(c).Height(12).Width(t.Space(s.n)).Radius(2).Background(uikit.Colors(t).MutedPressed)
				uikit.Meta(c, fmt.Sprintf("%g px", t.Space(s.n)))
			})
		}
	})
	ui.Column(c).Gap(t.Space(uikit.SpaceM)).Children(func() {
		uikit.Label(c, "Corners")
		ui.Row(c).Gap(t.Space(uikit.SpaceXL)).Children(func() {
			for _, r := range []struct {
				name   string
				radius float32
			}{{"RadiusInner", uikit.RadiusInner}, {"RadiusControl", uikit.RadiusControl}, {"RadiusPanel", uikit.RadiusPanel}, {"RadiusMenu", uikit.RadiusMenu}, {"RadiusCard", uikit.RadiusCard}, {"RadiusDialog", uikit.RadiusDialog}} {
				ui.Column(c).Gap(t.Space(uikit.SpaceXS)).Children(func() {
					ui.Box(c).Size(96, 56).Radius(r.radius).Background(uikit.Colors(t).Muted).
						Border(uikit.HairlineWidth, t.Border)
					uikit.Label(c, r.name)
					uikit.Meta(c, fmt.Sprintf("%g px", r.radius))
				})
			}
		})
	})
	ui.Column(c).Gap(t.Space(uikit.SpaceM)).Children(func() {
		uikit.Label(c, "Bars, controls and rows")
		for _, h := range []struct {
			name   string
			height float32
		}{{"HeightBar", uikit.HeightBar}, {"HeightTabs", uikit.HeightTabs}, {"HeightStatus", uikit.HeightStatus}, {"HeightFooter", uikit.HeightFooter}, {"HeightControl", uikit.HeightControl}, {"HeightControlSmall", uikit.HeightControlSmall}, {"HeightRow", uikit.HeightRow}} {
			ui.Row(c).Gap(t.Space(uikit.SpaceL)).AlignItems(ui.Center).Children(func() {
				uikit.Meta(c, h.name).Width(uikit.WidthNarrow)
				ui.Box(c).Height(h.height).Width(uikit.WidthField).Radius(2).Background(uikit.Colors(t).Muted)
				uikit.Meta(c, fmt.Sprintf("%g px", h.height))
			})
		}
	})
	ui.Column(c).Gap(t.Space(uikit.SpaceM)).Children(func() {
		uikit.Label(c, "Widths for fields and pickers")
		for _, w := range []struct {
			name  string
			width float32
		}{{"WidthNarrow", uikit.WidthNarrow}, {"WidthPicker", uikit.WidthPicker}, {"WidthField", uikit.WidthField}, {"WidthFieldWide", uikit.WidthFieldWide}, {"WidthWide", uikit.WidthWide}} {
			ui.Row(c).Gap(t.Space(uikit.SpaceL)).AlignItems(ui.Center).Children(func() {
				uikit.Meta(c, w.name).Width(uikit.WidthNarrow)
				ui.Box(c).Height(uikit.HeightControlSmall).Width(w.width).Radius(2).Background(uikit.Colors(t).Muted)
				uikit.Meta(c, fmt.Sprintf("%g px", w.width))
			})
		}
	})
}

// insetPlate shows each inset: the padding a surface gives its content, drawn
// as the surface with a block of content inside it.
func insetPlate(c *ui.Context) {
	t, p := c.Theme(), uikit.Colors(c.Theme())
	insets := []struct {
		name string
		x, y float32
	}{
		{"ControlPaddingX · default control", uikit.ControlPaddingX, 0},
		{"ControlPaddingXSmall · small control, chip", uikit.ControlPaddingXSmall, 0},
		{"PanelPadding · panel and pane header", uikit.PanelPadding, uikit.PanelPadding},
		{"PopoverPadding · popover", uikit.PopoverPadding, uikit.PopoverPadding},
		{"DialogPadding · dialog", uikit.DialogPadding, uikit.DialogPadding},
		{"TipPadding · tooltip", uikit.TipPaddingX, uikit.TipPaddingY},
		{"CardTipPadding · hover card", uikit.CardTipPaddingX, uikit.CardTipPaddingY},
		{"ToastPadding · toast", uikit.ToastPaddingX, uikit.ToastPaddingY},
		{"CardPadding · settings row", uikit.CardPaddingX, uikit.CardRowPaddingY},
	}
	ui.Column(c).Gap(t.Space(uikit.SpaceM)).Children(func() {
		uikit.Label(c, "Insets · the padding a surface gives its content, in points")
		for _, in := range insets {
			ui.Row(c).Gap(t.Space(uikit.SpaceL)).AlignItems(ui.Center).Children(func() {
				uikit.Meta(c, fmt.Sprintf("%s · %g × %g", in.name, in.x, in.y)).Width(300)
				ui.Row(c).Padding(in.y, in.x).Background(p.Window).Radius(uikit.RadiusPanel).
					InsetShadow(0, 0, 0, uikit.HairlineWidth, p.Border).Children(func() {
					ui.Box(c).Size(72, 12).Radius(uikit.RadiusInner).Background(p.Muted)
				})
			})
		}
	})
}

// elevationPlate shows the three floating shadows and the scrim in both
// themes, each on the canvas it would cover.
func elevationPlate(c *ui.Context) {
	ui.Row(c).Gap(c.Theme().Space(uikit.Space2XL)).AlignItems(ui.Start).Children(func() {
		for _, theme := range []struct {
			name string
			dark bool
		}{{"Light", false}, {"Dark", true}} {
			ui.Themed(c, uikit.Theme(theme.dark)).Width(480).Padding(c.Theme().Space(uikit.Space2XL)).
				Gap(c.Theme().Space(uikit.SpaceXL)).Radius(uikit.RadiusCard).Children(func() {
				uikit.Label(c, theme.name)
				for _, s := range []struct {
					name   string
					shadow uikit.Shadow
					radius float32
				}{
					{"ShadowMenu · RadiusMenu", uikit.ShadowMenu, uikit.RadiusMenu},
					{"ShadowPopover · RadiusCard", uikit.ShadowPopover, uikit.RadiusCard},
					{"ShadowDialog · RadiusDialog", uikit.ShadowDialog, uikit.RadiusDialog},
				} {
					s.shadow.Apply(ui.Column(c).Height(58).Padding(c.Theme().Space(uikit.SpaceM), c.Theme().Space(uikit.SpaceL)).
						Radius(s.radius).Background(uikit.Colors(c.Theme()).Background).
						Border(1, uikit.Colors(c.Theme()).Border), theme.dark).Children(func() {
						uikit.Meta(c, s.name).SingleLine()
					})
				}
				ui.Row(c).Gap(c.Theme().Space(uikit.SpaceL)).AlignItems(ui.Center).Children(func() {
					swatch(c, uikit.Colors(c.Theme()).Scrim, uikit.Colors(c.Theme()))
					uikit.Mono(c, "Scrim · black 35%")
					uikit.Hint(c, "dims the screen under a dialog")
				})
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
	ui.Row(c).Gap(t.Space(uikit.SpaceM)).AlignItems(ui.Center).Children(func() {
		uikit.Label(c, "Size").Width(uikit.WidthNarrow)
		uikit.Label(c, "16 · IconLarge").Width(uikit.WidthField)
		uikit.Label(c, "14 · IconInline").Width(uikit.WidthField)
	})
	ui.Column(c).Gap(0).Children(func() {
		for _, i := range icons {
			ui.Row(c).Height(36).Gap(t.Space(uikit.SpaceM)).AlignItems(ui.Center).
				BorderWidth(0, 0, 1, 0).BorderColor(t.Border).Children(func() {
				uikit.Mono(c, i.name).Width(uikit.WidthNarrow)
				ui.Icon(c, i.icon).Size(uikit.IconLarge, uikit.IconLarge).Width(uikit.WidthField).TextColor(t.Text)
				ui.Icon(c, i.icon).Size(uikit.IconInline, uikit.IconInline).Width(uikit.WidthField).TextColor(t.Text)
			})
		}
	})
	ui.Row(c).Gap(t.Space(uikit.SpaceM)).AlignItems(ui.Center).Children(func() {
		uikit.Hint(c, "An icon takes the ink of the text it sits in:").Width(uikit.WidthFieldWide)
		uikit.Body(c, "Search").TextColor(t.Text)
		ui.Icon(c, lucide.Search()).Size(uikit.IconInline, uikit.IconInline)
		uikit.Meta(c, "12 results")
	})
}

func motionPlate(c *ui.Context) {
	t := c.Theme()
	ui.Column(c).Gap(t.Space(uikit.SpaceL)).Children(func() {
		uikit.Heading(c, "No decorative motion")
		uikit.Prose(c, "A hover face, a tip, a menu, a toast, a dialog and a sidebar region arrive and leave at once. The kit has no transition tokens: a screen cannot name a duration, because there is none to name.")
		ui.Column(c).Gap(t.Space(uikit.SpaceM)).Padding(t.Space(uikit.SpaceL)).Radius(uikit.RadiusPanel).Background(uikit.Colors(t).Background).
			Border(1, uikit.Colors(t).Border).Children(func() {
			for _, line := range []string{
				"Removed: hover fades, popover rises, toast entrances, dialog fades, region collapse and pane springs.",
				"Kept: the recording dot's pulse, which reports a state rather than a transition.",
				"Kept: the user's own drags, splitter moves and scrolling.",
			} {
				uikit.Body(c, line)
			}
		})
		uikit.Hint(c, "Reduced motion is still respected by MyGo for the scrolling it animates; the app has no transition left for it to end.")
	})
}
