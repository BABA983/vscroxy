package uikit

// The sidebar's own list. MyGo's ui.Sidebar is a scroll that owns its items, so
// a tree inside it cannot scroll on its own, and its rows take only an SVG, so
// an app's own icon had no slot. This list is the same source list without the
// scroll: the region that holds it scrolls, or the tree inside it does, and a
// row's icon is either a glyph or a bitmap.

import (
	"cmp"
	"slices"
	"strings"
	"time"

	"github.com/egoist/mygo/ui"
)

// splitterHandle is the hit height of the divider between two panes: a grab
// area a few points taller than its one-point line.
const splitterHandle = 7

// sidebarList is one list's state across frames: the rows it holds, the one the
// keyboard is on, and the letters typed to find a row.
type sidebarList struct {
	chosen *string
	ids    []string
	labels []string
	// changed tells the next build that a click or the keys moved the choice,
	// and pending is the id they chose: the app may have put its own choice
	// back in the meantime, as a region synced from its filter does.
	changed bool
	pending string
	typed   string
	typedAt time.Time
}

// sidebarCurrent is the list being built, for the rows to add themselves to,
// and where the chosen row goes for assistive technology: the row is an element
// of this pass, so it is handed to the build that made it, never kept.
type sidebarCurrent struct {
	list   *sidebarList
	active func(ui.Element)
}

func currentSidebarList(c *ui.Context) *sidebarCurrent {
	return ui.Local(c.Root(), "sidebar-list", func() sidebarCurrent { return sidebarCurrent{} })
}

// SidebarList is a sidebar region's list: rows that choose one id, moved with
// the arrows, Home, End and the first letters of a row, and read as a tree. It
// does not scroll: give it a Scroll, or a tree that scrolls inside it. rows
// builds the rows, and onChoose runs once, with the id chosen, after the choice
// moved by a click or the keys.
func SidebarList(c *ui.Context, key string, chosen *string, onChoose func(id string), rows func()) ui.Element {
	list := ui.Local(c.Root(), "sidebar:"+key, func() sidebarList { return sidebarList{} })
	list.chosen = chosen
	list.ids, list.labels = nil, nil
	var active ui.Element
	cur := currentSidebarList(c)
	savedList, savedActive := cur.list, cur.active
	cur.list, cur.active = list, func(row ui.Element) { active = row }
	// The choice is the row's tint, as a source list's is; the list itself
	// draws no ring around the whole region.
	el := ui.Column(c).Key(key).Focusable().FocusRing(false).Shrink(0).Gap(0)
	el.HandleInput(func(ev ui.InputEvent) bool { return list.key(ev) })
	el.Children(rows)
	cur.list, cur.active = savedList, savedActive
	if active.Valid() {
		el.ActiveDescendant(active)
	}
	if list.changed {
		list.changed = false
		onChoose(list.pending)
		c.Invalidate()
	}
	return el
}

// key moves the choice as a source list does, and reports whether it took the
// key: the arrows never scroll the region around the list.
func (l *sidebarList) key(ev ui.InputEvent) bool {
	if ev.Kind != ui.InputKeyDown || ev.Mods != 0 || len(l.ids) == 0 {
		return false
	}
	at := slices.Index(l.ids, *l.chosen)
	switch ev.Key {
	case ui.KeyDown:
		l.choose(min(at+1, len(l.ids)-1))
		return true
	case ui.KeyUp:
		if at < 0 {
			l.choose(0)
		} else {
			l.choose(max(at-1, 0))
		}
		return true
	case ui.KeyHome:
		l.choose(0)
		return true
	case ui.KeyEnd:
		l.choose(len(l.ids) - 1)
		return true
	}
	r, ok := typedRune(ev.Key)
	if !ok {
		return false
	}
	// The first letters of a row choose it, and the same letter again goes on
	// to the next row starting with it.
	now := time.Now()
	if now.Sub(l.typedAt) > time.Second {
		l.typed = ""
	}
	l.typed += string(r)
	l.typedAt = now
	text, from := l.typed, at
	if strings.Count(text, text[:1]) == len(text) {
		text = text[:1]
		if at >= 0 {
			from = at + 1
		}
	}
	for k := range len(l.ids) {
		j := (max(from, 0) + k) % len(l.ids)
		if strings.HasPrefix(strings.ToLower(l.labels[j]), strings.ToLower(text)) {
			l.choose(j)
			return true
		}
	}
	return false
}

// choose makes row at the choice, and says whether it moved.
func (l *sidebarList) choose(at int) bool {
	if at < 0 || at >= len(l.ids) || l.ids[at] == *l.chosen {
		return false
	}
	*l.chosen, l.pending, l.changed = l.ids[at], l.ids[at], true
	return true
}

// typedRune is the letter, digit or space a key types without modifiers.
func typedRune(key ui.Key) (rune, bool) {
	switch {
	case key >= ui.KeyA && key <= ui.KeyZ:
		return 'a' + rune(key-ui.KeyA), true
	case key >= ui.Key0 && key <= ui.Key9:
		return '0' + rune(key-ui.Key0), true
	case key == ui.KeySpace:
		return ' ', true
	}
	return 0, false
}

// SidebarItem is a row of a SidebarList that chooses id: 24 high, inset from the
// edge, a fixed icon slot, the label, and whatever the caller adds after it, as
// a count.
func SidebarItem(c *ui.Context, id string, icon *ui.SVG, label string, chosen, active bool) ui.Element {
	return sidebarRow(c, id, label, chosen, active, func(ink ui.Color) {
		ui.Icon(c, cmp.Or(icon, SidebarDot)).Size(IconLarge, IconLarge).TextColor(ink)
	})
}

// SidebarItemImage is SidebarItem whose icon is a bitmap, as an app's own icon
// is.
func SidebarItemImage(c *ui.Context, id string, src ui.ImageSource, label string, chosen, active bool) ui.Element {
	return sidebarRow(c, id, label, chosen, active, func(ui.Color) {
		ui.Image(c, src).Size(IconLarge, IconLarge)
	})
}

// SidebarItemLetter is SidebarItem for a row named rather than pictured, as a
// host is: its first letter stands in a block where a site's icon would, so the
// column of dots that all meant the same thing is gone.
func SidebarItemLetter(c *ui.Context, id, label string, chosen, active bool) ui.Element {
	return sidebarRow(c, id, label, chosen, active, func(ink ui.Color) {
		LetterSquare(c, label, IconLarge, ink)
	})
}

// LetterSquare is a name's first letter in a neutral block, where an icon
// would sit: the control face, not a color of its own, so an app the system
// cannot picture and a host without a favicon look alike. size is the block's
// edge; ink colors the letter.
func LetterSquare(c *ui.Context, label string, size float32, ink ui.Color) ui.Element {
	t := c.Theme()
	letter := "?"
	if r := []rune(strings.TrimSpace(label)); len(r) > 0 {
		letter = strings.ToUpper(string(r[0]))
	}
	return ui.Row(c).Size(size, size).Shrink(0).Justify(ui.Center).AlignItems(ui.Center).
		Radius(RadiusControl).Background(Colors(t).Muted).Children(func() {
		ui.Text(c, letter).FontSize(Scaled(TextMeta)).FontWeight(WeightMedium).TextColor(ink).SingleLine()
	})
}

// sidebarRow draws one row and adds it to the list being built. The ink is taken
// as the row is built, so the row keeps the pointer it had the frame before and
// asks for one more frame to catch up: a hover is a color change, not a layout
// change.
func sidebarRow(c *ui.Context, id, label string, chosen, active bool, icon func(ink ui.Color)) ui.Element {
	return sidebarRowSized(c, id, label, chosen, active, icon,
		HeightRow, RadiusControl, SpaceS, TextBody, false)
}

// SettingsSidebarItem is the settings navigation row: 32 points tall,
// 8-point corners, 12-point text and a raised canvas face when selected.
func SettingsSidebarItem(c *ui.Context, id string, icon *ui.SVG, label string, chosen, active bool) ui.Element {
	return sidebarRowSized(c, id, label, chosen, active, func(ink ui.Color) {
		ui.Icon(c, cmp.Or(icon, SidebarDot)).Size(IconLarge, IconLarge).TextColor(ink)
	}, HeightSidebarRow, RadiusPanel, SpaceCompact, TextSmall, true)
}

func sidebarRowSized(c *ui.Context, id, label string, chosen, active bool, icon func(ink ui.Color), height, radius, padX, textSize float32, nav bool) ui.Element {
	t, p := c.Theme(), Colors(c.Theme())
	key := id
	if nav {
		key = "settings-" + id
	}
	hovered := ui.Local(c.Root(), "sidebar-hover:"+key, func() bool { return false })
	ink, iconInk := t.TextMuted, p.Faint
	if chosen || *hovered {
		ink, iconInk = t.Text, t.TextMuted
	}
	row := ui.Row(c).Key(id).Height(height).Shrink(0).AlignItems(ui.Center).Gap(t.Space(SpaceS)).
		Padding(0, t.Space(padX)).Margin(0, t.Space(SpaceS)).Radius(radius).
		Role(ui.RoleTreeItem).Level(1).TextColor(ink)
	if chosen {
		if nav {
			row.Background(p.Background)
			ShadowNavSelected.Apply(row, t.Dark)
		} else {
			Selected(c, row, active)
		}
	}
	row.Children(func() {
		ui.Row(c).Width(IconLarge).Shrink(0).Justify(ui.Center).AlignItems(ui.Center).Children(func() { icon(iconInk) })
		ui.Text(c, label).FontSize(Scaled(textSize)).TextColor(ink).SingleLine().Grow(1)
	})
	if row.Hovered() != *hovered {
		*hovered = row.Hovered()
		c.Invalidate()
	}
	if cur := currentSidebarList(c); cur.list != nil {
		list := cur.list
		if row.Clicked() && *list.chosen != id {
			*list.chosen, list.pending, list.changed = id, id, true
		}
		list.ids = append(list.ids, id)
		list.labels = append(list.labels, label)
		if chosen && cur.active != nil {
			cur.active(row)
		}
	}
	return row
}

// SidebarPane is one region of a SidebarPanes stack: a heading that stays,
// a body that folds away, and the body's height while it is not the last
// open region.
type SidebarPane struct {
	ID    string
	Title string
	// Open folds the body away. A nil Open is a region that cannot fold.
	Open *bool
	// Height is the body's height in DIPs while another open region below
	// it grows; Default is what a double click on a divider restores. The
	// app keeps Height across frames and launches.
	Height  *float32
	Default float32
	// Min is the smallest body height while the region is open, in DIPs.
	Min float32
	// Trailing draws what the heading offers at its far end, as the Sources
	// switch or the Collections menu.
	Trailing func()
	Body     func()
}

// SidebarPanes stacks foldable regions under fixed chrome, as VS Code's
// PaneView does. A heading stays while its body folds away, so a folded
// region costs one row. The last open region takes the room the others
// leave, which is why the collections list grows to the bottom once the
// regions above it fold; the regions above it keep their own heights, so a
// short list does not stretch into blank space. A divider between two open
// bodies drags: it changes the heights above it in DIPs, and the last open
// body gives or takes the difference. A double click puts both sides back to
// their defaults.
//
// available is the height the fixed chrome leaves. The stack fills it while
// the regions' minimums fit; when they do not, every body goes to its
// minimum and the whole middle scrolls, rather than a body being cut off by
// the window. Each body also scrolls inside itself, so a list longer than
// its region keeps its heading and the regions below it.
func SidebarPanes(c *ui.Context, key string, available float32, panes []SidebarPane) ui.Element {
	t, p := c.Theme(), Colors(c.Theme())
	last := -1
	for i, pane := range panes {
		if pane.Open == nil || *pane.Open {
			last = i
		}
	}
	// The minimum is every heading plus every open body's least height and
	// the dividers between open bodies: the point where the middle stops
	// compressing and starts scrolling.
	least, compactLeast := float32(0), float32(0)
	open := 0
	for _, pane := range panes {
		least += HeightStatus + t.Space(SpaceM)
		compactLeast += HeightStatus
		if pane.Open == nil || *pane.Open {
			least += max(pane.Min, HeightRow)
			compactLeast += max(pane.Min, HeightRow)
			if open > 0 {
				least += 7
				compactLeast += 7
			}
			open++
		}
	}
	// A short middle drops the headings' breathing room before it scrolls:
	// the regions keep their rows, and Collections keeps its few.
	compact := available > 0 && available < least
	minimum := least
	if compact {
		minimum = compactLeast
	}
	stackH := max(available, minimum)
	scroll := ui.Scroll(c).Key(key).Grow(1).MinHeight(0)
	scroll.Children(func() {
		stack := ui.Column(c).Key(key + "-stack").FillWidth().Height(stackH)
		var handles, lines []ui.Element
		var gaps [][2]int
		bodies := make([]ui.Element, len(panes))
		stack.Children(func() {
			previous := -1
			for i, pane := range panes {
				open := pane.Open == nil || *pane.Open
				// A folded region between two open ones keeps its heading,
				// but the divider belongs to the two bodies it resizes, so
				// it sits just above the next open heading.
				if open && previous >= 0 {
					handle := ui.Column(c).Key("divider:" + panes[previous].ID).Height(splitterHandle).Shrink(0).FillWidth().
						Justify(ui.Center).Cursor(ui.CursorResizeNS)
					var line ui.Element
					handle.Children(func() {
						line = ui.Box(c).Height(HairlineWidth).Background(p.Border)
					})
					handles, lines = append(handles, handle), append(lines, line)
					gaps = append(gaps, [2]int{previous, i})
				}
				SidebarHeading(c, pane.Title, pane.Open, pane.Trailing, compact)
				if !open {
					continue
				}
				body := ui.Column(c).Key(pane.ID + "-body").MinHeight(max(pane.Min, HeightRow)).Clip()
				if i == last {
					body.Grow(1)
				} else {
					body.Height(heightOf(pane)).Shrink(1)
				}
				body.Children(pane.Body)
				bodies[i] = body
				previous = i
			}
		})
		for i, handle := range handles {
			a, b := panes[gaps[i][0]], panes[gaps[i][1]]
			_, dy, held := handle.Dragged()
			if held || handle.Hovered() {
				lines[i].Background(t.Text.Alpha(0.14))
			}
			if handle.DoubleClicked() {
				if a.Height != nil && a.Default > 0 {
					*a.Height = a.Default
				}
				if b.Height != nil && b.Default > 0 {
					*b.Height = b.Default
				}
				c.Invalidate()
				continue
			}
			if !held || dy == 0 || !bodies[gaps[i][0]].Valid() || !bodies[gaps[i][1]].Valid() {
				continue
			}
			ha, hb := bodies[gaps[i][0]].Bounds().H, bodies[gaps[i][1]].Bounds().H
			if ha+hb <= 0 {
				continue
			}
			dy = max(dy, a.Min-ha)
			dy = min(dy, hb-b.Min)
			if dy == 0 {
				continue
			}
			if gaps[i][1] == last {
				// The growing body below takes the difference; only the body
				// above changes its own height.
				if a.Height != nil {
					*a.Height = max(ha+dy, a.Min)
				}
			} else {
				if a.Height != nil {
					*a.Height = max(ha+dy, a.Min)
				}
				if b.Height != nil {
					*b.Height = max(hb-dy, b.Min)
				}
			}
			c.Invalidate()
		}
	})
	return scroll
}

// heightOf is a pane's body height: what the app kept, its default, or a
// control row's room.
func heightOf(p SidebarPane) float32 {
	switch {
	case p.Height != nil && *p.Height > 0:
		return *p.Height
	case p.Default > 0:
		return p.Default
	}
	return HeightRow * 3
}
