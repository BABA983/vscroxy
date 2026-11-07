package app

import (
	"github.com/baba983/vscroxy/internal/inspect"
	"github.com/baba983/vscroxy/internal/uikit"
	"net/url"
	"slices"
	"strings"

	"github.com/egoist/mygo/ui"
)

// customColumn reads a header, URL parameter, or scalar body field.
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
	sourceRequestJSON    = "Request JSON"
	sourceResponseJSON   = "Response JSON"
	sourceGraphQL        = "GraphQL operation"
)

var columnSources = []string{sourceHeader, sourceRequestHeader, sourceResponseHeader, sourceQuery, sourceRequestJSON, sourceResponseJSON, sourceGraphQL}

var graphqlColumn = customColumn{Source: sourceGraphQL, Name: "operation"}

// id names the column in the saved layout, so its width and place stay.
func (cc customColumn) id() string {
	name := cc.Name
	if cc.Source == sourceGraphQL {
		name = "operation"
	}
	if cc.header() {
		name = strings.ToLower(name)
	}
	return "custom:" + cc.Source + ":" + name
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
	case sourceRequestJSON:
		return cc.Name + " (request JSON)"
	case sourceResponseJSON:
		return cc.Name + " (response JSON)"
	case sourceGraphQL:
		return "GraphQL operation"
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
func (cc customColumn) header() bool {
	return cc.Source == sourceHeader || cc.Source == sourceRequestHeader || cc.Source == sourceResponseHeader
}

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
	{"remote", "Remote IP"}, {"duration", "Duration"}, {"size", "Size"}, {"captured", "Time"},
}

// columnsMenu is the table header's menu: the built-in columns to show or
// hide, the custom ones to remove, and adding one.
func (a *app) columnsMenu(m *ui.Menu, id string) { actionMenu(m, a.columnActions(id)) }

func (a *app) columnActions(id string) []action {
	var actions []action
	removeCustom := func(id string) func() {
		return func() {
			a.setCustomColumns(slices.DeleteFunc(slices.Clone(a.customColumns), func(x customColumn) bool { return x.id() == id }))
		}
	}
	if cc, ok := a.customColumnAt(id); ok {
		actions = append(actions, do("column.remove", "Remove “"+cc.title()+"” column", removeCustom(id)))
	}
	actions = append(actions, do("capture.add-column", "Add column…", a.openColumn))
	for _, col := range hideableColumns {
		shown := !slices.Contains(a.hiddenColumns, col.id)
		actions = append(actions, do("column.toggle", col.title, func() {
			if shown {
				a.hiddenColumns = append(slices.Clone(a.hiddenColumns), col.id)
			} else {
				a.hiddenColumns = slices.DeleteFunc(slices.Clone(a.hiddenColumns), func(h string) bool { return h == col.id })
			}
			a.persistSettings()
			a.invalidate()
		}).checked(checkbox(shown)).in("columns"))
	}
	for _, cc := range a.customColumns {
		actions = append(actions, do("column.remove", cc.title(), removeCustom(cc.id())).checked(checkOn).in("columns"))
	}
	return actions
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
	if draft.Source == sourceGraphQL {
		name = "operation"
	}
	problem := ""
	if draft.body() {
		if err := inspect.ValidatePointer(name); err != nil {
			problem = err.Error()
		}
	}
	if len(a.customColumns) >= 16 {
		problem = "At most 16 custom columns; remove one before adding another."
	}
	if name != "" && slices.ContainsFunc(a.customColumns, func(cc customColumn) bool {
		return cc.id() == (customColumn{Source: draft.Source, Name: name}).id()
	}) {
		problem = "The table already has this column."
	}
	uikit.Modal(c, &a.columnOpen, func() {
		uikit.DialogColumn(c, uikit.DialogSmall).Children(func() {
			uikit.DialogTitle(c, "Add column")
			ui.Scroll(c).MinHeight(0).Gap(t.Space(3)).Padding(0, t.Space(1)).Children(func() {
				ui.Form(c, func() {
					ui.Field(c, "From", func() {
						uikit.Select(c, &draft.Source, columnSources).Label("Column source").Width(200)
					}).
						Description(map[string]string{
							sourceHeader:         "The response's header, or the request's when the response has none.",
							sourceRequestHeader:  "What the client sent.",
							sourceResponseHeader: "What the server answered.",
							sourceQuery:          "A parameter of the URL's query.",
							sourceRequestJSON:    "Scalar request field. JSON pointer, case-sensitive; ~1 escapes / and ~0 escapes ~.",
							sourceResponseJSON:   "Scalar response field. JSON pointer, case-sensitive; ~1 escapes / and ~0 escapes ~.",
							sourceGraphQL:        "Selected operations from GET parameters or complete POST bodies. Names are case-sensitive; batches keep request order.",
						}[draft.Source])
					placeholder := "x-request-id"
					if draft.Source == sourceQuery {
						placeholder = "page"
					}
					if draft.body() {
						placeholder = "/data/user/id"
					}
					if draft.Source != sourceGraphQL {
						ui.Field(c, "Name", func() {
							uikit.Input(c, &draft.Name).Label("Column name or JSON pointer").Placeholder(placeholder).AutoFocus()
						}).Error(problem)
					} else if problem != "" {
						uikit.Hint(c, problem)
					}
				})
				if draft.body() {
					uikit.Hint(c, "Reads complete retained JSON off-thread, up to 1 MiB. Scalar cells up to 4 KiB; unavailable bodies show an explanation. Sorting is lexical.").FillWidth()
				}
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
	a.columnRead.stop()
	for i := range a.flows {
		a.flows[i].columnData = nil
	}
	a.customColumns = columns
	a.applyFilter()
	a.persistSettings()
	a.invalidate()
}
