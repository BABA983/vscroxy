package uikit

import (
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/egoist/mygo/ui"
)

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
	// EmptyRowText gives a column that has no input in the trailing row, such
	// as Description, the same muted placeholder the Key and Value cells show.
	EmptyRowText map[int]string
	// Measure receives the grid's laid-out width, which a caller can read on
	// the next frame to decide whether an optional column fits.
	Measure *ui.Handle
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
		// A table without a row to add is read-only: its rows grow with what
		// they show, so a wrapped header value pushes the next row and the
		// rule below it down instead of drawing over them. Editable rows keep
		// one control height so their inputs line up.
		readOnly := o.OnAdd == nil
		// The table is as tall as its rows, so its hairline closes under the
		// trailing row instead of running down the pane. A long table scrolls
		// inside the pane and keeps its header in place.
		draw := func() {
			// The header is sticky and carries the table's top edge, so the
			// hairline stays under the scroll bar's viewport when rows scroll
			// under it. The table's own box, which scrolls away, keeps only its
			// other three edges.
			table := ui.Column(c).Key(o.Key).FillWidth().
				BorderWidth(0, HairlineWidth, HairlineWidth, HairlineWidth).BorderColor(p.Border).
				Radius(RadiusControl).Clip()
			if o.Measure != nil {
				table.Bind(o.Measure)
			}
			table.Children(func() {
				header := ui.Row(c).FillWidth().Height(HeightControl).AlignItems(ui.Stretch).
					BorderWidth(HairlineWidth, 0, HairlineWidth, 0).BorderColor(p.Border).
					Background(t.Background).Sticky().Top(0)
				header.Children(func() {
					for i, col := range o.Columns {
						cell := gridCell(c, col, i > 0, p.Border, false)
						cell.Children(func() {
							if col.Title != "" {
								Label(c, col.Title).FillWidth()
							}
						})
					}
				})
				for row := 0; row < o.Rows; row++ {
					line := ui.Row(c).FillWidth().AlignItems(ui.Stretch).
						BorderWidth(0, 0, HairlineWidth, 0).BorderColor(p.Border)
					if readOnly {
						line.MinHeight(HeightControl)
					} else {
						line.Height(HeightControl)
					}
					line.Children(func() {
						for i, col := range o.Columns {
							cell := gridCell(c, col, i > 0, p.Border, readOnly)
							if col.Cell != nil {
								cell.Children(func() { col.Cell(row, line) })
							}
						}
					})
				}
				if o.OnAdd != nil {
					o.emptyRow(c, table)
				}
			})
		}
		if o.NoScroll {
			draw()
			return
		}
		// The scroll bar floats over the container's end. Keep the table's
		// full width for its header and cells and leave the bar's gutter
		// outside it, so no column's text is ever drawn under the bar.
		ui.Scroll(c).Grow(1).MinHeight(0).PaddingEnd(ScrollbarInset(t)).Children(draw)
	})
	return column
}

// gridCell is one column of a header or data row. Every cell after the first
// carries the column's rule, so the grid is drawn by its cells rather than by
// overlaid lines.
func gridCell(c *ui.Context, col KeyValueColumn, rule bool, border ui.Color, readOnly bool) ui.Element {
	// An editable titled column keeps the same inset from the dividers on
	// both sides, so a key's text stops clear of the rule instead of running
	// under it. The narrow leading and action columns have no title and stay
	// centered in their fixed width. MinWidth describes the content, so the
	// column asks for room for the inset too.
	inset := float32(0)
	if !readOnly && col.Title != "" {
		inset = c.Theme().Space(GridCellPadding)
	}
	cell := ui.Box(c).FillHeight().MinWidth(col.MinWidth + 2*inset).AlignItems(ui.Center)
	if readOnly {
		// A read-only cell starts at the row's top with the same vertical
		// room a one-line control has, so its text can grow downward, and
		// the same horizontal inset as the column's title and its editable
		// cells, so text lines up under the title.
		cell.Justify(ui.Start).Padding(c.Theme().Space(SpaceXS), c.Theme().Space(SpaceM))
	} else {
		cell.Justify(ui.Center)
		if inset > 0 {
			cell.Padding(0, inset)
		}
	}
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

// truncatedCut is the longest prefix of a cell's text that still leaves room
// for an ellipsis. It is kept in the field's own state, so a cell measures
// again only after its text or width changes.
type truncatedCut struct {
	text                 string
	avail                float32
	truncated            bool
	prefixW              float32
	ellipsisW, ellipsisH float32
}

// truncateGap keeps the patch clear of the last kept glyph, whose ink can
// overhang its advance, as a g's tail does.
const truncateGap = 1.5

// TruncatedCell ends an unfocused grid cell's text with an ellipsis where it
// does not fit, and gives the whole value as its tip. It paints over the
// glyphs after the longest whole-rune prefix that fits with an ellipsis,
// instead of replacing the input, so the field keeps its focus, caret,
// selection and place in Tab's order; the visible prefix keeps the field's
// own colors, variable references included. inset is the field's text inset:
// its border plus padding. fill and hoverFill are the cell's background under
// the field and while the pointer is on it; a masked value shows bullets and
// keeps its text out of the tip.
func TruncatedCell(c *ui.Context, field ui.Element, value string, masked bool, inset float32, fill, hoverFill ui.Color) ui.Element {
	if value == "" || field.Focused() {
		return field
	}
	avail := field.Bounds().W - 2*inset
	if avail <= 0 {
		return field
	}
	t := c.Theme()
	text, ink := value, t.Text
	if masked {
		text, ink = strings.Repeat("•", utf8.RuneCountInString(value)), t.TextMuted
	}
	size := Scaled(TextBody)
	cut := ui.Local(field, "truncated-cell", func() truncatedCut { return truncatedCut{} })
	if cut.text != text || cut.avail != avail {
		*cut = truncatedCut{text: text, avail: avail}
		fullW, _ := c.MeasureText(0, ui.Span{Text: text, Size: size})
		if cut.truncated = fullW > avail; cut.truncated {
			cut.ellipsisW, cut.ellipsisH = c.MeasureText(0, ui.Span{Text: "…", Size: size})
			runes := []rune(text)
			// The longest prefix whose width plus the ellipsis fits the text
			// box. Widths grow with the prefix, so a binary search finds it
			// without measuring every rune of a long value.
			lo, hi := 0, len(runes)
			for lo < hi {
				mid := (lo + hi + 1) / 2
				w, _ := c.MeasureText(0, ui.Span{Text: string(runes[:mid]), Size: size})
				if w+truncateGap+cut.ellipsisW <= avail {
					lo = mid
				} else {
					hi = mid - 1
				}
			}
			for lo > 0 && unicode.IsSpace(runes[lo-1]) {
				lo--
			}
			cut.prefixW, _ = c.MeasureText(0, ui.Span{Text: string(runes[:lo]), Size: size})
		}
	}
	if !cut.truncated {
		return field
	}
	if field.Hovered() {
		// The field paints hoverFill over the cell's background; the patch
		// covers that same composite, not a second wash of the same color.
		fill = hoverFill.Over(fill)
	}
	field.DrawOver(func(p *ui.Painter, r ui.Rect) {
		x := r.X + inset + cut.prefixW + truncateGap
		end := r.X + r.W - inset
		if x < end {
			p.Fill(ui.Rect{X: x, Y: r.Y + inset, W: end - x, H: r.H - 2*inset}, fill, 0)
		}
		p.Text(x, r.Y+inset+max((r.H-2*inset-cut.ellipsisH)/2, 0), "…", size, ink)
	})
	// The tip is for the pointer: a focused cell already scrolls its text
	// with the caret, and a tip on focus would cover the row above it.
	if masked || !field.Hovered() {
		return field
	}
	return Tip(c, field, value)
}

// emptyRow is the trailing row that adds a row when typed into. It keeps its
// typed text in the table's own state, so no screen needs a draft row.
func (o KeyValueTableOptions) emptyRow(c *ui.Context, table ui.Element) {
	t := c.Theme()
	newKey := ui.Local(table, "new-key", func() string { return "" })
	newValue := ui.Local(table, "new-value", func() string { return "" })
	line := ui.Row(c).FillWidth().Height(HeightControl).AlignItems(ui.Stretch)
	var keyInput, valueInput ui.Element
	haveKey, haveValue := false, false
	line.Children(func() {
		for i, col := range o.Columns {
			cell := gridCell(c, col, i > 0, Colors(t).Border, false)
			switch i {
			case o.KeyColumn:
				cell.Children(func() {
					keyInput = emptyGridInput(c, newKey, o.KeyPlaceholder, o.KeyLabel)
					haveKey = true
				})
			case o.ValueColumn:
				cell.Children(func() {
					valueInput = emptyGridInput(c, newValue, o.ValuePlaceholder, o.ValueLabel)
					haveValue = true
				})
			default:
				if text := o.EmptyRowText[i]; text != "" {
					cell.Children(func() {
						emptyGridPlaceholder(c, text)
					})
				}
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
// keyboard reaches it. The cell's own inset keeps it clear of the dividers.
func emptyGridInput(c *ui.Context, value *string, placeholder, label string) ui.Element {
	return InputControl(c, value).Label(label).Placeholder(placeholder).FillWidth().MinWidth(0).
		FillHeight().Padding(0, 0).Border(HairlineWidth, ui.Color{})
}

// emptyGridPlaceholder is the trailing row's read-only cell. It uses the same
// body type and vertical centering as emptyGridInput, so a Description
// placeholder lines up with Key and Value without becoming an input that adds
// a row.
func emptyGridPlaceholder(c *ui.Context, text string) ui.Element {
	return ui.Row(c).FillWidth().FillHeight().AlignItems(ui.Center).Children(func() {
		ui.Text(c, text).FontSize(Scaled(TextBody)).LineHeight(LineBody).
			TextColor(c.Theme().TextMuted).SingleLine()
	})
}
