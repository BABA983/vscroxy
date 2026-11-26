package uikit

import (
	"github.com/egoist/mygo/ui"
)

// ListTheme is the theme a MyGo table, list or tree paints its selection
// from. Scope it to the list with SetTheme; rows draw RowMarker themselves.
func ListTheme(t *ui.Theme, focused bool) *ui.Theme {
	p := Colors(t)
	list := *t
	list.Accent, list.AccentText = p.Accent, t.Text
	if !focused {
		list.Accent = p.Muted
	}
	return &list
}

// DataList is a list of data: the selection is the app's tint plus a marker,
// never the solid accent a menu uses. It applies ListTheme and draws RowMarker
// for the selected row, so a caller cannot forget either. Build rows with
// Rows, and do not reset the row context's theme inside the callback.
func DataList(c *ui.Context, state *ui.ListState, count int, rows func(ui.ListRow)) ui.Element {
	t := c.Theme()
	focused := state != nil && state.Handle.Focused(c)
	c.SetTheme(ListTheme(t, focused))
	list := ui.List(c, state, count).Rows(func(row ui.ListRow) {
		if row.Selected() {
			RowMarker(row.Context, focused)
		}
		rows(row)
	})
	c.SetTheme(t)
	return list
}

// DataListIndexed is DataList for the original index callback. It draws the
// marker from the state's selected index, which the list keeps current.
func DataListIndexed(c *ui.Context, state *ui.ListState, count int, row func(int)) ui.Element {
	t := c.Theme()
	focused := state != nil && state.Handle.Focused(c)
	c.SetTheme(ListTheme(t, focused))
	list := ui.List(c, state, count, func(i int) {
		if state != nil && state.Selected != nil && *state.Selected == i {
			RowMarker(c, focused)
		}
		row(i)
	})
	c.SetTheme(t)
	return list
}

// marker paints the selection marker on the leading edge of r.
func marker(t *ui.Theme, focused bool) func(p *ui.Painter, r ui.Rect) {
	// The palette's accent, not t.Accent: inside ListTheme that is the fill.
	pal := Colors(t)
	color := pal.Primary
	if !focused {
		color = pal.Faint
	}
	return func(p *ui.Painter, r ui.Rect) {
		inset := min(t.Space(SpaceS), r.H/4)
		p.Fill(ui.Rect{X: r.X, Y: r.Y + inset, W: 2, H: r.H - 2*inset}, color, 1)
	}
}

// RowMarker adds the marker to the row being built by a MyGo list or table,
// whose own selection fill comes from ListTheme.
func RowMarker(c *ui.Context, focused bool) {
	ui.Box(c).Absolute().Left(0).Top(0).Bottom(0).Width(rowMarkerBox).Draw(marker(c.Theme(), focused))
}

// Selected gives a rounded row the selection look.
func Selected(c *ui.Context, e ui.Element, focused bool) ui.Element {
	t, p := c.Theme(), Colors(c.Theme())
	// MyGo paints its own selection for list rows, with the solid Accent.
	// Composite our wash on the surface before painting it, so the softer
	// face completely covers that ink instead of tinting over it.
	fill := p.Accent.Over(p.Background)
	if !focused {
		fill = p.Muted.Over(p.Background)
	}
	return e.Background(fill).TextColor(t.Text)
}
