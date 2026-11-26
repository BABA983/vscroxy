package uikit

import (
	"fmt"

	"github.com/baba983/vscroxy/locales"
	"github.com/egoist/mygo/ui"
)

// TimingPhase is one part of a response's time, as the Timing tab colors it.
type TimingPhase struct {
	Name   string
	Millis int
	Color  ui.Color
}

// Timing is how a response's time divides into its phases, in order, and
// what the phases cannot name, as a reused connection; no phases for an
// exchange that never reached a server.
type Timing struct {
	Phases []TimingPhase
	Note   string
}

// ResponseMeta is what a response reports in its pane header, at the end of
// the row: how long it took, then its size and type. Narrow panes elide it,
// with the full text on hover; the duration's hover card splits it into its
// phases.
func ResponseMeta(c *ui.Context, duration, detail string, timing Timing) ui.Element {
	t := c.Theme()
	return ui.Row(c).MinWidth(0).Shrink(1).AlignItems(ui.Center).Label(locales.ResponseResult()).Children(func() {
		d := Tip(c, Meta(c, duration).Font(FontMono).FixedLineHeight(responseMetaLine).Shrink(0), duration)
		if len(timing.Phases) > 0 {
			d.Description(locales.TimingHoverHelp())
			ui.TooltipBase(c, d, func(tip ui.Element) {
				tip.AttachTo(d, ui.AnchorBottomStart, ui.AnchorTopStart).Margin(t.Space(SpaceS), 0, 0, 0)
				timingCard(c, tip, duration, timing)
			})
		}
		if detail != "" {
			Tip(c, Meta(c, " · "+detail).Font(FontMono).FixedLineHeight(responseMetaLine).
				SingleLine().Shrink(1).MinWidth(0), detail)
		}
	})
}

// timingCard lists the phases of a response's time: a bar of their shares,
// then each with its milliseconds, the total and what the phases leave out.
func timingCard(c *ui.Context, card ui.Element, total string, timing Timing) {
	t, pal := c.Theme(), Colors(c.Theme())
	sum := 0
	for _, p := range timing.Phases {
		sum += max(p.Millis, 0)
	}
	PopoverPanel(c, card).Width(WidthWide).Padding(PopoverPadding).Gap(t.Space(SpaceS)).Children(func() {
		ui.Row(c).FillWidth().Height(timingBarHeight).Radius(timingBarHeight / 2).Background(pal.Muted).Children(func() {
			for _, p := range timing.Phases {
				if p.Millis > 0 {
					ui.Box(c).Grow(float32(p.Millis)).FillHeight().Background(p.Color)
				}
			}
			if sum == 0 {
				ui.Spacer(c)
			}
		})
		row := func(dot ui.Color, name, value string, strong bool) {
			ui.Row(c).FillWidth().Gap(t.Space(SpaceM)).AlignItems(ui.Center).Children(func() {
				ui.Box(c).Size(DotSize, DotSize).Radius(DotSize / 2).Background(dot)
				label := ui.Text(c, name).FontSize(Scaled(TextMeta)).TextColor(t.Text).Grow(1)
				v := ui.Text(c, value).Font(FontMono).FontSize(Scaled(TextMeta)).FontFeatures("tnum").TextColor(t.Text)
				if strong {
					label.FontWeight(WeightMedium)
					v.FontWeight(WeightMedium)
				}
			})
		}
		for _, p := range timing.Phases {
			row(p.Color, p.Name, fmt.Sprintf("%d ms", p.Millis), false)
		}
		ui.Divider(c)
		row(ui.Color{}, "Total", total, true)
		if timing.Note != "" {
			Meta(c, timing.Note)
		}
	})
}

// Sparkline draws counts oldest first as a smooth line over a soft shade
// that fades to the baseline, scaled to the largest, in a box the size of
// a line of meta text; no counts draws it flat.
func Sparkline(c *ui.Context, counts []int, ink ui.Color) ui.Element {
	peak := 1
	for _, n := range counts {
		peak = max(peak, n)
	}
	return ui.Box(c).Size(sparklineWidth, sparklineHeight).Shrink(0).Draw(func(p *ui.Painter, r ui.Rect) {
		const stroke = 1.25
		bottom, height := r.Y+r.H-stroke/2, r.H-stroke
		if len(counts) < 2 {
			p.Line(r.X, bottom, r.X+r.W, bottom, stroke, ink)
			return
		}
		x := func(i int) float32 { return r.X + r.W*float32(i)/float32(len(counts)-1) }
		y := func(i int) float32 { return bottom - height*float32(counts[i])/float32(peak) }
		// Through the midpoints between samples, with each sample as the
		// control point: a curve that never overshoots below zero.
		var line ui.Path
		line.MoveTo(x(0), y(0))
		for i := 1; i < len(counts)-1; i++ {
			line.QuadTo(x(i), y(i), (x(i)+x(i+1))/2, (y(i)+y(i+1))/2)
		}
		last := len(counts) - 1
		line.LineTo(x(last), y(last))
		shade := line
		shade.LineTo(x(last), r.Y+r.H).LineTo(x(0), r.Y+r.H).Close()
		p.FillPathGradient(&shade, ui.LinearGradient{From: ink.Alpha(0.45), To: ink.Alpha(0.04), Angle: 180})
		p.StrokePath(&line, stroke, ink)
	})
}
