package app

import (
	"github.com/baba983/vscroxy/internal/uikit"
	"net/url"
	"slices"
	"strings"

	"github.com/egoist/mygo/ui"
)

// customColumn is a column the user added to the traffic table: a header
// or a query parameter of each exchange.
type customColumn struct {
	Source string `json:"source"`
	Name   string `json:"name"`
}

// The places a custom column reads from. "Header" is the response's header,
// or the request's when the response has none.
const (
	sourceHeader         = "Header"
	sourceRequestHeader  = "Request header"
	sourceResponseHeader = "Response header"
	sourceQuery          = "Query parameter"
)

var columnSources = []string{sourceHeader, sourceRequestHeader, sourceResponseHeader, sourceQuery}

// id names the column in the saved layout, so its width and place stay.
func (cc customColumn) id() string {
	return "custom:" + cc.Source + ":" + strings.ToLower(cc.Name)
}

// title is the column's heading: the name, marked by where it comes from.
func (cc customColumn) title() string {
	switch cc.Source {
	case sourceRequestHeader:
		return cc.Name + " (request)"
	case sourceResponseHeader:
		return cc.Name + " (response)"
	case sourceQuery:
		return "?" + cc.Name
	}
	return cc.Name
}

// value is what the column shows for f.
func (cc customColumn) value(f flow) string {
	switch cc.Source {
	case sourceRequestHeader:
		return strings.Join(f.reqHeader.Values(cc.Name), ", ")
	case sourceResponseHeader:
		return strings.Join(f.respHeader.Values(cc.Name), ", ")
	case sourceQuery:
		u, err := url.Parse(f.url)
		if err != nil {
			return ""
		}
		return strings.Join(u.Query()[cc.Name], ", ")
	}
	if value := f.respHeader.Get(cc.Name); value != "" {
		return value
	}
	return f.reqHeader.Get(cc.Name)
}

// header says the column holds a header, which the cell's menu can filter by.
func (cc customColumn) header() bool { return cc.Source != sourceQuery }

// customColumnAt is the custom column with id, if it is one.
func (a *app) customColumnAt(id string) (customColumn, bool) {
	i := slices.IndexFunc(a.customColumns, func(cc customColumn) bool { return cc.id() == id })
	if i < 0 {
		return customColumn{}, false
	}
	return a.customColumns[i], true
}

// hideableColumns are the built-in columns the table can do without; the URL
// always stays.
var hideableColumns = []struct{ id, title string }{
	{"id", "#"}, {"method", "Method"}, {"host", "Host"}, {"client", "Client"}, {"status", "Status"},
	{"duration", "Duration"}, {"size", "Size"}, {"captured", "Time"},
}

// columnsMenu is the table header's menu: the built-in columns to show or
// hide, the custom ones to remove, and adding one.
func (a *app) columnsMenu(m *ui.Menu, id string) {
	if cc, ok := a.customColumnAt(id); ok && m.Item("Remove “"+cc.title()+"” column").Chosen() {
		a.setCustomColumns(slices.DeleteFunc(slices.Clone(a.customColumns), func(x customColumn) bool { return x.id() == id }))
	}
	if m.Item("Add column…").Chosen() {
		a.openColumn()
	}
	m.Separator()
	for _, col := range hideableColumns {
		shown := !slices.Contains(a.hiddenColumns, col.id)
		if m.Item(col.title).Checked(shown).Chosen() {
			if shown {
				a.hiddenColumns = append(slices.Clone(a.hiddenColumns), col.id)
			} else {
				a.hiddenColumns = slices.DeleteFunc(slices.Clone(a.hiddenColumns), func(h string) bool { return h == col.id })
			}
			a.persistSettings()
			a.invalidate()
		}
	}
	for _, cc := range a.customColumns {
		if m.Item(cc.title()).Checked(true).Chosen() {
			a.setCustomColumns(slices.DeleteFunc(slices.Clone(a.customColumns), func(x customColumn) bool { return x.id() == cc.id() }))
		}
	}
}

// openColumn asks for a column to add.
func (a *app) openColumn() {
	a.columnDraft = customColumn{Source: sourceHeader}
	a.columnOpen = true
}

// columnModal adds a column from a header or a query parameter.
func (a *app) columnModal(c *ui.Context, t *ui.Theme) {
	if !a.columnOpen {
		return
	}
	draft := &a.columnDraft
	name := strings.TrimSpace(draft.Name)
	problem := ""
	if name != "" && slices.ContainsFunc(a.customColumns, func(cc customColumn) bool {
		return cc.id() == (customColumn{Source: draft.Source, Name: name}).id()
	}) {
		problem = "The table already has this column."
	}
	uikit.Modal(c, &a.columnOpen, func() {
		uikit.DialogColumn(c, uikit.DialogSmall).Children(func() {
			uikit.DialogTitle(c, "Add column")
			ui.Scroll(c).MinHeight(0).Gap(t.Space(3)).Children(func() {
				ui.Form(c, func() {
					ui.Field(c, "From", func() {
						uikit.Select(c, &draft.Source, columnSources).Width(200)
					}).
						Description(map[string]string{
							sourceHeader:         "The response's header, or the request's when the response has none.",
							sourceRequestHeader:  "What the client sent.",
							sourceResponseHeader: "What the server answered.",
							sourceQuery:          "A parameter of the URL's query.",
						}[draft.Source])
					placeholder := "x-request-id"
					if draft.Source == sourceQuery {
						placeholder = "page"
					}
					ui.Field(c, "Name", func() {
						uikit.Input(c, &draft.Name).Placeholder(placeholder).AutoFocus()
					}).Error(problem)
				})
				uikit.Meta(c, "Right-click the table's header to hide columns or remove this one.")
			})
			uikit.DialogFooter(c).Children(func() {
				if uikit.SecondaryButton(c, "Cancel").Clicked() {
					a.columnOpen = false
				}
				if uikit.PrimaryButton(c, "Add column").Disabled(name == "" || problem != "").Clicked() {
					a.setCustomColumns(append(slices.Clone(a.customColumns), customColumn{Source: draft.Source, Name: name}))
					a.columnOpen = false
				}
			})
		})
	})
}

// setCustomColumns changes the custom columns and keeps the choice.
func (a *app) setCustomColumns(columns []customColumn) {
	a.customColumns = columns
	a.applyFilter()
	a.persistSettings()
	a.invalidate()
}
