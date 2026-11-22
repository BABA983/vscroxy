package uikit

import "github.com/egoist/mygo/ui"

// KeyValueColumn is one column of a KeyValueTable. Title is the column's
// heading, empty for the narrow leading, reveal, file and action columns.
// Width fixes a column; Grow shares the rest with the other flexible columns.
// Cell draws one row's content; line is the row itself, so a cell can reveal
// its action only while the row is hovered or focused.
type KeyValueColumn struct {
	Title    string
	Width    float32
	Grow     float32
	MinWidth float32
	Cell     func(row int, line ui.Element)
}

// KeyValueTableOptions is the shared grid of key/value rows used by Params,
// Headers and the form bodies. The grid draws the outer hairline, the header
// row, the row and column rules, and the trailing empty row; the screen draws
// the cells.
type KeyValueTableOptions struct {
	// Key keeps the empty row's typed key and value across frames. It must be
	// unique among the tables on screen.
	Key     string
	Caption string
	// CaptionEnd draws a control at the caption's right end, such as a file
	// picker for a form table.
	CaptionEnd func()
	Columns    []KeyValueColumn
	// Rows is the number of saved rows. OnAdd adds one more: the empty row
	// that adds a row when its Key or Value is typed into.
	Rows int
	// HeaderEnd draws the header row's own controls at its right end, such as
	// Bulk Edit.
	HeaderEnd func()
	// OnAdd receives the typed key and value. valueCell says the value was
	// the cell typed into, so the caller can focus the same cell in the row it
	// adds.
	OnAdd            func(key, value string, valueCell bool)
	KeyColumn        int
	ValueColumn      int
	KeyPlaceholder   string
	ValuePlaceholder string
	KeyLabel         string
	ValueLabel       string
	// NoScroll leaves the rows in the caller's scroll, for a table that shares
	// its scroll with other content.
	NoScroll bool
}

// KeyValueTable draws the key/value grid. The caption sits above the outer
// hairline; the header row keeps its place while the rows scroll, unless the
// caller owns the scroll with NoScroll.
func KeyValueTable(c *ui.Context, o KeyValueTableOptions) ui.Element {
	t, p := c.Theme(), Colors(c.Theme())
	column := ui.Column(c).FillWidth().Gap(t.Space(SpaceM))
	if !o.NoScroll {
		column.Grow(1).MinHeight(0)
	}
	column.Children(func() {
		if o.Caption != "" || o.CaptionEnd != nil {
			ui.Row(c).FillWidth().AlignItems(ui.Center).Children(func() {
				if o.Caption != "" {
					Small(c, o.Caption).TextColor(t.Text)
				}
				if o.CaptionEnd != nil {
					ui.Spacer(c)
					o.CaptionEnd()
				}
			})
		}
		// The table is as tall as its rows, so its hairline closes under the
		// trailing row instead of running down the pane. A long table scrolls
		// inside the pane and keeps its header in place.
		draw := func() {
			table := ui.Column(c).Key(o.Key).FillWidth().Border(HairlineWidth, p.Border).Radius(RadiusControl).Clip()
			table.Children(func() {
				header := ui.Row(c).FillWidth().Height(HeightControl).AlignItems(ui.Stretch).
					BorderWidth(0, 0, HairlineWidth, 0).BorderColor(p.Border).
					Background(t.Background).Sticky().Top(0)
				header.Children(func() {
					for i, col := range o.Columns {
						cell := gridCell(c, col, i > 0, p.Border)
						cell.Children(func() {
							if col.Title != "" {
								Label(c, col.Title).FillWidth().Padding(0, t.Space(SpaceM))
							}
						})
					}
					if o.HeaderEnd != nil {
						ui.Row(c).Absolute().Right(0).Top(0).Bottom(0).Padding(0, t.Space(SpaceM)).
							AlignItems(ui.Center).Children(o.HeaderEnd)
					}
				})
				for row := 0; row < o.Rows; row++ {
					line := ui.Row(c).FillWidth().Height(HeightControl).AlignItems(ui.Stretch).
						BorderWidth(0, 0, HairlineWidth, 0).BorderColor(p.Border)
					line.Children(func() {
						for i, col := range o.Columns {
							cell := gridCell(c, col, i > 0, p.Border)
							if col.Cell != nil {
								cell.Children(func() { col.Cell(row, line) })
							}
						}
					})
				}
				if o.OnAdd != nil {
					o.emptyRow(c, table, t.Space(SpaceM))
				}
			})
		}
		if o.NoScroll {
			draw()
			return
		}
		ui.Scroll(c).Grow(1).MinHeight(0).Children(draw)
	})
	return column
}

// gridCell is one column of a header or data row. Every cell after the first
// carries the column's rule, so the grid is drawn by its cells rather than by
// overlaid lines.
func gridCell(c *ui.Context, col KeyValueColumn, rule bool, border ui.Color) ui.Element {
	cell := ui.Box(c).FillHeight().MinWidth(col.MinWidth).AlignItems(ui.Center).Justify(ui.Center)
	if col.Width > 0 {
		cell.Width(col.Width)
	} else {
		cell.Basis(0).Grow(col.Grow)
	}
	if rule {
		cell.BorderWidth(0, 0, 0, HairlineWidth).BorderColor(border)
	}
	return cell
}

// emptyRow is the trailing row that adds a row when typed into. It keeps its
// typed text in the table's own state, so no screen needs a draft row.
func (o KeyValueTableOptions) emptyRow(c *ui.Context, table ui.Element, pad float32) {
	t := c.Theme()
	newKey := ui.Local(table, "new-key", func() string { return "" })
	newValue := ui.Local(table, "new-value", func() string { return "" })
	line := ui.Row(c).FillWidth().Height(HeightControl).AlignItems(ui.Stretch)
	var keyInput, valueInput ui.Element
	haveKey, haveValue := false, false
	line.Children(func() {
		for i, col := range o.Columns {
			cell := gridCell(c, col, i > 0, Colors(t).Border)
			switch i {
			case o.KeyColumn:
				cell.Children(func() {
					keyInput = emptyGridInput(c, newKey, o.KeyPlaceholder, o.KeyLabel, pad)
					haveKey = true
				})
			case o.ValueColumn:
				cell.Children(func() {
					valueInput = emptyGridInput(c, newValue, o.ValuePlaceholder, o.ValueLabel, pad)
					haveValue = true
				})
			}
		}
	})
	if !haveKey && !haveValue {
		return
	}
	changedKey, changedValue := haveKey && keyInput.Changed(), haveValue && valueInput.Changed()
	if !changedKey && !changedValue {
		return
	}
	if *newKey == "" && *newValue == "" {
		return
	}
	o.OnAdd(*newKey, *newValue, changedValue)
	*newKey, *newValue = "", ""
	c.Invalidate()
}

// emptyGridInput is the placeholder cell of the trailing row. It is a body
// text input, as the saved rows are, with no face until the pointer or the
// keyboard reaches it.
func emptyGridInput(c *ui.Context, value *string, placeholder, label string, pad float32) ui.Element {
	return InputControl(c, value).Label(label).Placeholder(placeholder).FillWidth().MinWidth(0).
		FillHeight().Padding(0, pad).Border(HairlineWidth, ui.Color{})
}
