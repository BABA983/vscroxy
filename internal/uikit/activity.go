package uikit

import (
	"math"

	"github.com/baba983/vscroxy/locales"
	"github.com/egoist/mygo/ui"
)

// ActivityBucket stacks normal responses, redirects, client errors, server
// errors and slow ones, in that order. A bucket's height measures its total,
// not its worst code.
type ActivityBucket [5]int

// ActivityRange selects whole buckets, From inclusive and To exclusive.
// Its zero value leaves the table unfiltered.
type ActivityRange struct{ From, To int }

// ActivityStrip is a 24-DIP row: its summary, the stacked bars and the latest
// time at the end. Drag a new range anywhere except the two handles, which
// resize it. Arrows move a range, Shift+arrows extend it, and Escape clears it.
// changed runs after pointer, keyboard and Clear edits, without retaining
// build-scoped elements.
func ActivityStrip(c *ui.Context, buckets []ActivityBucket, selected *ActivityRange, summary, last string, changed func()) ui.Element {
	t, palette := c.Theme(), Colors(c.Theme())
	strip := ui.Row(c).Height(HeightStatus).Shrink(0).FillWidth().Padding(0, 12).Gap(8).AlignItems(ui.Center).
		Label(locales.ActivityStrip())
	strip.Border(0, t.Border).BorderWidth(0, 0, 1, 0)
	strip.Children(func() {
		// What the strip says keeps its room whether or not a range is
		// chosen: a brush must never move the bars under the pointer that
		// started it.
		ui.Row(c).Width(240).Shrink(1).MinWidth(0).Gap(8).AlignItems(ui.Center).Children(func() {
			Tip(c, ui.Text(c, summary).FontSize(TextMeta).TextColor(t.TextMuted).NoWrap().Ellipsis("…").Grow(1).MinWidth(0), summary).
				ContentTransition(ui.ContentTransition{Numeric: true})
			clear := SecondaryButton(c.Key("clear"), locales.ClearX()).Label(locales.ClearActivityRange()).
				Height(18).PaddingX(4).FontSize(TextMeta).Shrink(0)
			if selected.To > selected.From {
				clear.OnClick(func() { *selected = ActivityRange{}; changed() })
			} else {
				clear.Disabled(true).Opacity(0)
			}
		})
		plot := ui.Box(c.Key("plot")).Height(14).Grow(1).MinWidth(40).Focusable().Role(ui.RoleGroup).Label(locales.ActivityTimeRange()).
			Description(locales.ActivityDragHelp())
		state := ui.Local(plot, "brush", func() activityGesture { return activityGesture{} })
		// The handle itself must survive a build, just as the gesture does.
		bound := ui.Local(plot, "handle", func() ui.Handle { return ui.Handle{} })
		plot.Bind(bound)
		n := len(buckets)
		plot.HandleInput(func(ev ui.InputEvent) bool {
			if n == 0 {
				return false
			}
			before := *selected
			defer func() {
				if before != *selected {
					changed()
				}
			}()
			switch ev.Kind {
			case ui.InputPointerDown, ui.InputPointerMove, ui.InputPointerUp:
				// A canceled release ends the press without committing it:
				// put the range back where the press found it.
				if ev.Kind == ui.InputPointerUp && ev.Canceled && state.held {
					*selected, state.held = state.original, false
					return true
				}
				if ev.Button != 0 || ev.Kind != ui.InputPointerDown && !state.held {
					return false
				}
				r := bound.Bounds(c)
				x := max(0, min(float64(n), float64(ev.X/max(r.W, 1))*float64(n)))
				if ev.Kind == ui.InputPointerDown {
					state.held, state.edge = true, 0
					state.original = *selected
					state.anchor = min(n-1, int(x))
					if selected.To > selected.From {
						lo, hi := float32(selected.From)*r.W/float32(n), float32(selected.To)*r.W/float32(n)
						if math.Abs(float64(ev.X-lo)) <= 5 && math.Abs(float64(ev.X-lo)) <= math.Abs(float64(ev.X-hi)) {
							state.edge = -1
						} else if math.Abs(float64(ev.X-hi)) <= 5 {
							state.edge = 1
						}
					}
				}
				switch state.edge {
				case -1:
					selected.From = max(0, min(selected.To-1, int(math.Round(x))))
				case 1:
					selected.To = min(n, max(selected.From+1, int(math.Round(x))))
				default:
					at := min(n-1, int(x))
					*selected = ActivityRange{From: min(state.anchor, at), To: max(state.anchor, at) + 1}
				}
				if ev.Kind == ui.InputPointerUp {
					state.held = false
				}
				return true
			case ui.InputKeyDown:
				if ev.Key == ui.KeyEscape {
					*selected = ActivityRange{}
					state.held = false
					return true
				}
				if ev.Key != ui.KeyLeft && ev.Key != ui.KeyRight || ev.Mods != 0 && ev.Mods != ui.Shift {
					return false
				}
				step := 1
				if ev.Key == ui.KeyLeft {
					step = -1
				}
				if selected.To <= selected.From {
					*selected = ActivityRange{From: n - 1, To: n}
				} else if ev.Mods == ui.Shift {
					selected.To = min(n, max(selected.From+1, selected.To+step))
				} else if selected.From+step >= 0 && selected.To+step <= n {
					selected.From += step
					selected.To += step
				}
				return true
			}
			return false
		})
		rangeNow := *selected
		peak := 1
		for _, bucket := range buckets {
			total := 0
			for _, count := range bucket {
				total += count
			}
			peak = max(peak, total)
		}
		plot.Draw(func(p *ui.Painter, r ui.Rect) {
			if n == 0 {
				return
			}
			width := r.W / float32(n)
			active := rangeNow.To > rangeNow.From
			if active {
				p.Fill(ui.Rect{X: r.X + width*float32(rangeNow.From), Y: r.Y, W: width * float32(rangeNow.To-rangeNow.From), H: r.H}, palette.AccentTint, 0)
			}
			// Normal traffic is a neutral face; redirects, client and server
			// errors and slow requests carry their signal color.
			inks := [5]ui.Color{palette.FillHover, palette.Info, palette.Warning, palette.Danger, palette.Danger}
			for i, bucket := range buckets {
				bottom := r.Y + r.H
				for status, count := range bucket {
					height := (r.H - 2) * float32(count) / float32(peak)
					ink := inks[status]
					if active && (i < rangeNow.From || i >= rangeNow.To) {
						ink = ink.Alpha(float32(ink.A) / 255 * 0.3)
					}
					p.Fill(ui.Rect{X: r.X + float32(i)*width + 1, Y: bottom - height, W: max(1, width-2), H: height}, ink, 0)
					bottom -= height
				}
			}
			p.Line(r.X, r.Y+r.H, r.X+r.W, r.Y+r.H, 1, t.Border)
			if active {
				for _, at := range []int{rangeNow.From, rangeNow.To} {
					x := r.X + width*float32(at)
					p.Line(x, r.Y, x, r.Y+r.H, 1, palette.Accent)
					p.Fill(ui.Rect{X: x - 2, Y: r.Y + 5, W: 4, H: r.H - 10}, palette.Accent, 1)
				}
			}
		})
		ui.Text(c, last).Font(FontMono).FontSize(TextMeta).TextColor(t.TextMuted).NoWrap()
	})
	return strip
}

type activityGesture struct {
	held     bool
	anchor   int
	edge     int
	original ActivityRange
}
