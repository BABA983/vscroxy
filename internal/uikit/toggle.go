package uikit

import (
	"github.com/egoist/mygo/ui"
)

// Segmented chooses one of a few mutually exclusive modes, as AppKit draws
// one: a recessed groove and a raised thumb, concentric with it. This is
// the 24-point control; SegmentedLarge is the settings-scope size.
func Segmented(c *ui.Context, selected *int, labels ...string) ui.Element {
	return segmented(c, selected, HeightRow, SpaceHair, RadiusControl, RadiusInner, TextMeta, false, false, labels...)
}

// SegmentedFill is Segmented stretched across its row, each segment an equal
// share with its label centered: a switch between two halves of a place, as
// the sidebar's Traffic and Collections are. Its type is one step smaller so
// three words still read whole in a narrow sidebar.
func SegmentedFill(c *ui.Context, selected *int, labels ...string) ui.Element {
	return segmented(c, selected, HeightControl, SpaceHair, RadiusControl, RadiusInner, TextMeta-1, false, true, labels...)
}

// SegmentedLarge is the settings scope tabs: a 38-point Muted track, a
// 30-point raised canvas segment, 12-point medium text and shadow-sm.
func SegmentedLarge(c *ui.Context, selected *int, labels ...string) ui.Element {
	return segmented(c, selected, 38, SpaceXS, RadiusPanel, RadiusControl, TextSmall, true, false, labels...)
}
func segmented(c *ui.Context, selected *int, height, inset, trackRadius, segmentRadius, textSize float32, raised, fill bool, labels ...string) ui.Element {
	t, p := c.Theme(), Colors(c.Theme())
	parts := ui.SegmentedBase(c, selected, len(labels))
	parts.Track.AlignItems(ui.Stretch).Height(height).Padding(t.Space(inset)).
		Radius(trackRadius).Background(p.Muted).Children(func() {
		for i, label := range labels {
			padY := float32(0)
			if raised {
				padY = t.Space(SpaceS)
			}
			padX := t.Space(SpaceM)
			if raised {
				padX = t.Space(SpaceL)
			}
			s := parts.Segment(i).Label(label).Radius(segmentRadius).
				AlignItems(ui.Center).FontSize(Scaled(textSize)).TextColor(t.TextMuted)
			if fill {
				// Room left over is shared out; a longer label keeps its width.
				// A three-way switch in a narrow sidebar needs every point,
				// so its segments carry no side padding of their own.
				s.Grow(1).Justify(ui.Center).Padding(padY, 0)
			} else {
				s.Padding(padY, padX)
			}
			if raised {
				s.FontWeight(WeightMedium)
			}
			switch {
			case i == *selected:
				s.Background(p.Background).TextColor(t.Text)
				if raised {
					ShadowSmall.Apply(s, t.Dark)
				} else {
					s.InsetShadow(0, 0, 0, HairlineWidth, p.Border)
				}
			case s.Pressed() || s.Hovered():
				s.TextColor(t.Text)
			}
			s.Children(func() { ui.Text(c, label).SingleLine() })
		}
	})
	return parts.Track
}

// Selection is a tinted fill with the row's normal text colors. focused is
// whether the list holding the row has the keyboard focus; without it the
// fill turns to the neutral control face, so the row keeps its shape and
// still shows which one is selected without claiming the keyboard.
//
// Rows that pick a place or a thing (sidebars, trees, pick lists) are
// rounded blocks inset from the edges, as in a macOS source list: Selected.
// Rows of data in a MyGo table or list span its width and add a marker line
// at the leading edge, which keeps a selected row apart from a hovered one
// once the fill turns neutral: ListTheme and RowMarker.
// Switch is the kit's 36-by-20 switch. Its off face is a 20% muted-text track
// and a muted thumb. The on track comes from SwitchOn, so Dark can keep a
// readable edge under its knob.
func Switch(c *ui.Context, on *bool) ui.Element {
	t, p := c.Theme(), Colors(c.Theme())
	sw := ui.SwitchBase(c, on).Size(SettingsControlHeight, switchHeight).Radius(switchHeight / 2)
	target := float32(0)
	if *on {
		target = 1
	}
	pos := target
	// Off is MutedForeground at 20% with a MutedForeground thumb; on it is
	// the ink Primary with a pale thumb. Dark keeps a lighter on track and a
	// white thumb so the knob stays visible.
	off := p.MutedForeground.Alpha(0.2)
	knobOff, knobOn := p.MutedForeground, ui.RGB(255, 255, 255)
	if !t.Dark {
		knobOn = p.PrimaryForeground
	}
	track := off.Mix(p.SwitchOn, pos)
	sw.Draw(func(painter *ui.Painter, r ui.Rect) {
		in := t.Space(SpaceHair)
		d := r.H - 2*in
		painter.Fill(r, track, r.H/2)
		at := pos
		if c.Direction() == ui.RightToLeft {
			at = 1 - pos
		}
		knob := ui.Rect{X: r.X + in + at*(r.W-r.H), Y: r.Y + in, W: d, H: d}
		painter.Fill(knob, knobOff.Mix(knobOn, pos), d/2)
	})
	return sw
}

// SettingsSwitch is the 36-by-20 switch of the looser Settings skeleton.
func SettingsSwitch(c *ui.Context, on *bool, label string) ui.Element {
	return Switch(c, on).Label(label)
}

// ModeItem is one place a ModeSwitch shows: its icon, the name assistive
// technology and tests read, and the shortcut its tip names.
type ModeItem struct {
	Icon  *ui.SVG
	Label string
	Keys  []string
}

// ModeSwitch chooses which place a sidebar shows with a row of icons, as
// Postman's sidebar and VS Code's activity bar do. The chosen icon sits on
// the menu tint in ink; the others are muted until hovered. Each tip names
// the place and its shortcut, since the icon is the only thing drawn.
func ModeSwitch(c *ui.Context, selected *int, items ...ModeItem) ui.Element {
	t, p := c.Theme(), Colors(c.Theme())
	row := ui.Row(c).Shrink(0).Gap(t.Space(SpaceXS)).AlignItems(ui.Center)
	row.Children(func() {
		for i, item := range items {
			on := *selected == i
			b := ui.ButtonBase(c).Label(item.Label).Size(HeightControl, HeightControl).
				Padding(0).Shrink(0).Justify(ui.Center).AlignItems(ui.Center).Radius(RadiusControl).
				Role(ui.RoleTab).Selected(on)
			ink := t.TextMuted
			switch {
			case on:
				b.Background(p.Accent)
				ink = t.Text
			case b.Pressed():
				b.Background(p.MutedPressed)
				ink = t.Text
			case b.Hovered():
				b.Background(p.MutedHover)
				ink = t.Text
			}
			b.Children(func() { ui.Icon(c, item.Icon).Size(IconInline, IconInline).TextColor(ink) })
			if b.Clicked() && !on {
				*selected = i
				c.Invalidate()
			}
			TipKeys(c, b, item.Label, item.Keys...)
		}
	})
	return row
}
