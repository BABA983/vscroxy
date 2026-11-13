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
		t := c.Theme()
		letter := "?"
		if r := []rune(strings.TrimSpace(label)); len(r) > 0 {
			letter = strings.ToUpper(string(r[0]))
		}
		ui.Row(c).Size(IconLarge, IconLarge).Shrink(0).Justify(ui.Center).AlignItems(ui.Center).
			Radius(RadiusControl).Background(Colors(t).Fill).Children(func() {
			ui.Text(c, letter).FontSize(Scaled(TextMeta)).FontWeight(500).TextColor(ink).SingleLine()
		})
	})
}

// sidebarRow draws one row and adds it to the list being built. The ink is taken
// as the row is built, so the row keeps the pointer it had the frame before and
// asks for one more frame to catch up: a hover is a color change, not a layout
// change.
func sidebarRow(c *ui.Context, id, label string, chosen, active bool, icon func(ink ui.Color)) ui.Element {
	t, p := c.Theme(), Colors(c.Theme())
	hovered := ui.Local(c.Root(), "sidebar-hover:"+id, func() bool { return false })
	ink, iconInk := t.TextMuted, p.TextFaint
	if chosen || *hovered {
		ink, iconInk = t.Text, t.TextMuted
	}
	row := ui.Row(c).Key(id).Height(HeightControl).Shrink(0).AlignItems(ui.Center).Gap(t.Space(2)).
		Padding(0, t.Space(1.5)).Margin(0, t.Space(1.5)).Radius(RadiusControl).
		Role(ui.RoleTreeItem).Level(1).TextColor(ink)
	if chosen {
		Selected(c, row, active)
	}
	row.Children(func() {
		ui.Row(c).Width(IconLarge).Shrink(0).Justify(ui.Center).AlignItems(ui.Center).Children(func() { icon(iconInk) })
		Body(c, label).SingleLine().Grow(1)
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
