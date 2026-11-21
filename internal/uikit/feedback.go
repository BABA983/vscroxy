package uikit

import "github.com/egoist/mygo/ui"

// Feedback tells a person that work is under way or that something needs
// reading. Each one is quiet: a spinner and a bar are text-colored, and a
// callout marks its kind with an icon, never a filled card.

// progressHeight is a pill: twelve points tall, fully rounded.
const progressHeight = 12

// Spinner is work of unknown length, in the muted text color, named by label.
func Spinner(c *ui.Context, label string) ui.Element {
	return ui.Spinner(c).Label(label).TextColor(c.Theme().TextMuted)
}

// ProgressBar shows work as a fraction from 0 to 1 over the muted track; a
// negative value shows activity of unknown length. It is named by label.
func ProgressBar(c *ui.Context, value float64, label string) ui.Element {
	t, p := c.Theme(), Colors(c.Theme())
	bar := ui.Progress(c, value).Label(label).Height(progressHeight).Radius(progressHeight / 2)
	if value < 0 || value > 1 {
		return bar.TextColor(p.Success.Alpha(0.7)).Background(p.MutedForeground.Alpha(0.2))
	}
	// Composite once against the panel before painting: the track and fill
	// overlap, and translucent layers would darken that seam.
	track := p.MutedForeground.Alpha(0.2).Over(t.Background)
	fill := p.Success.Alpha(0.7).Over(track)
	bar.Draw(func(painter *ui.Painter, r ui.Rect) {
		painter.Fill(r, track, r.H/2)
		w := float32(value) * r.W
		if w <= 0 {
			return
		}
		part := ui.Rect{X: r.X, Y: r.Y, W: w, H: r.H}
		if w >= r.W {
			// A finished bar is the track's pill, not a square-ended fill.
			painter.Fill(part, fill, r.H/2)
			return
		}
		// Until then the fill rounds only its left cap; its right edge is
		// flat where the rest of the track begins.
		painter.FillRadii(part, [4]float32{r.H / 2, 0, 0, r.H / 2}, fill)
	})
	return bar
}

// CalloutKind is what a callout says about its subject.
type CalloutKind int

const (
	CalloutInfo CalloutKind = iota
	CalloutSuccess
	CalloutWarning
	CalloutDanger
)

// calloutIconTop puts a 16-point icon's center on the first 20-point line of
// body text: (20 - 16) / 2.
const calloutIconTop = (LineBody*TextBody - IconLarge) / 2

// Callout states one thing a person must read where it applies: a line of
// text with its kind's icon, and optionally the actions that answer it at the
// far end. It has no face of its own, so it sits in the flow of its section.
func Callout(c *ui.Context, kind CalloutKind, text string, actions func()) ui.Element {
	t, p := c.Theme(), Colors(c.Theme())
	icon, ink := infoIcon, p.Info
	switch kind {
	case CalloutSuccess:
		icon, ink = successIcon, p.Success
	case CalloutWarning:
		icon, ink = warningIcon, p.Warning
	case CalloutDanger:
		icon, ink = errorIcon, p.Danger
	}
	// The icon is centered on the first line, so a wrapped sentence keeps its
	// mark beside the start of the text rather than between its lines.
	row := ui.Row(c).FillWidth().Gap(t.Space(SpaceS)).AlignItems(ui.Start)
	row.Children(func() {
		ui.Icon(c, icon).Size(IconLarge, IconLarge).TextColor(ink).Shrink(0).Margin(calloutIconTop, 0, 0, 0)
		Body(c, text).Grow(1).MinWidth(0).Selectable()
		if actions != nil {
			ui.Row(c).Gap(t.Space(SpaceM)).AlignItems(ui.Center).Shrink(0).Children(actions)
		}
	})
	return row
}

// Badge is a count or a short status. Its 18-point small badge is tinted
// with 10% of the signal color, so ink colors both the label and its face.
func Badge(c *ui.Context, label string, ink ui.Color) ui.Element {
	t := c.Theme()
	return ui.Text(c, label).FontSize(Scaled(TextMeta)).FontWeight(WeightMedium).FontFeatures("tnum").
		TextColor(ink).Background(ink.Alpha(0.10)).Radius(RadiusInner).Padding(0, t.Space(SpaceS)).
		Height(HeightBadge).Center().SingleLine().Shrink(0)
}
