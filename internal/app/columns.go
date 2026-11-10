package app

import (
	"cmp"
	"maps"
	"net/url"
	"slices"
	"strings"

	"github.com/baba983/vscroxy/internal/inspect"
	"github.com/baba983/vscroxy/internal/uikit"
	"github.com/baba983/vscroxy/locales"
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

// hideableColumns are the built-in columns the table can do without, in the
// table's order and under the headings it shows; the URL always stays.
func hideableColumns() []struct{ id, title string } {
	return []struct{ id, title string }{
		{"id", "#"}, {"status", locales.Status()}, {"method", locales.Method()}, {"host", locales.Host()},
		{"remote", locales.RemoteIp()}, {"client", locales.Client()}, {"duration", locales.Duration()},
		{"size", locales.Size()}, {"captured", locales.Time()},
	}
}

// maxCustomColumns is how many the table holds; the menu and the dialog say so.
const maxCustomColumns = 16

// tooManyColumns is why a column cannot be added.
const tooManyColumns = "At most 16 custom columns; remove one before adding another."

// headerColumnLimit is how many of the traffic's headers the menu offers at
// once, the most common first.
const headerColumnLimit = 30

// headerScanLimit is how many of the latest captures the menu reads its
// headers from: a menu is built every frame, and a long session must not make
// that expensive.
const headerScanLimit = 500

// columnsMenu is the table header's menu, shaped like Proxyman's: a check for
// every column the table can show, the traffic's headers a click away, and a
// reset back to the default table.
func (a *app) columnsMenu(m *ui.Menu, id string) { actionMenu(m, a.columnActions(id)) }

func (a *app) columnActions(id string) []action {
	var actions []action
	if cc, ok := a.customColumnAt(id); ok {
		actions = append(actions, do("column.remove", "Remove “"+cc.title()+"” column", func() {
			a.setCustomColumns(slices.DeleteFunc(slices.Clone(a.customColumns), func(x customColumn) bool { return x.id() == id }))
		}))
	}
	for _, col := range hideableColumns() {
		actions = append(actions, a.columnToggle(col.id, col.title).in("columns"))
	}
	for _, cc := range a.customColumns {
		actions = append(actions, a.columnToggle(cc.id(), cc.title()).in("columns"))
	}
	actions = append(actions,
		do("capture.add-column", locales.AddColumn(), a.openColumn).in("add"),
		submenu(locales.RequestHeaders(), a.headerColumnActions(sourceRequestHeader)...).in("add"),
		submenu(locales.ResponseHeaders(), a.headerColumnActions(sourceResponseHeader)...).in("add"),
		do("column.reset", locales.ResetAllColumns(), a.resetColumns).in("reset"),
	)
	return actions
}

// columnToggle shows or hides one column, checked while the table shows it.
func (a *app) columnToggle(id, label string) action {
	shown := !slices.Contains(a.hiddenColumns, id)
	return do("column.toggle", label, func() { a.setColumnHidden(id, shown) }).checked(checkbox(shown))
}

// setColumnHidden takes a column out of the table or puts it back. A custom
// column stays defined either way, so its place and width survive.
func (a *app) setColumnHidden(id string, hidden bool) {
	if hidden {
		a.hiddenColumns = append(slices.Clone(a.hiddenColumns), id)
	} else {
		a.hiddenColumns = slices.DeleteFunc(slices.Clone(a.hiddenColumns), func(h string) bool { return h == id })
	}
	// A body column shown again reads the cells it never had while hidden.
	a.columnRead.dirty = true
	a.persistSettings()
	a.invalidate()
}

// headerColumnActions offers the headers the traffic carries as columns, the
// ones it carries most first. Choosing one adds the column, or shows or hides
// the one it already has. Credential values stay out of the table as they do
// in the inspector, so those headers are only added by name.
func (a *app) headerColumnActions(source string) []action {
	response := source == sourceResponseHeader
	counts := make(map[string]int)
	credentials := false
	for _, f := range a.flows[max(0, len(a.flows)-headerScanLimit):] {
		headers := f.reqHeader
		if response {
			headers = f.respHeader
		}
		for name := range headers {
			if sensitiveHeader(name) {
				credentials = true
				continue
			}
			counts[name]++
		}
	}
	names := slices.SortedFunc(maps.Keys(counts), func(x, y string) int {
		if byCount := cmp.Compare(counts[y], counts[x]); byCount != 0 {
			return byCount
		}
		return cmp.Compare(x, y)
	})
	var actions []action
	for _, name := range names[:min(len(names), headerColumnLimit)] {
		cc := customColumn{Source: source, Name: name}
		exists := slices.ContainsFunc(a.customColumns, func(x customColumn) bool { return x.id() == cc.id() })
		shown := exists && !slices.Contains(a.hiddenColumns, cc.id())
		actions = append(actions, do("column.header", name, func() {
			if !exists {
				a.setCustomColumns(append(slices.Clone(a.customColumns), cc))
				return
			}
			a.setColumnHidden(cc.id(), shown)
		}).checked(checkbox(shown)).when(exists || len(a.customColumns) < maxCustomColumns, tooManyColumns))
	}
	if len(actions) == 0 {
		label := locales.NoRequestHeaders()
		if response {
			label = locales.NoResponseHeaders()
		}
		actions = append(actions, note(label))
	}
	if credentials {
		actions = append(actions, note(locales.CredentialHeadersAreAddedByName()))
	}
	return actions
}

// resetColumns returns the table to its default columns and layout.
func (a *app) resetColumns() {
	a.hiddenColumns = nil
	a.columns = ui.TableLayout{}
	// The table copies the layout back when its frame ends, and this menu is
	// drawn inside that frame, so the live copy goes too.
	if view := a.views.activeView(); view != nil {
		view.list.Columns = ui.TableLayout{}
	}
	a.setCustomColumns(nil)
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
	if len(a.customColumns) >= maxCustomColumns {
		problem = tooManyColumns
	}
	if name != "" && slices.ContainsFunc(a.customColumns, func(cc customColumn) bool {
		return cc.id() == (customColumn{Source: draft.Source, Name: name}).id()
	}) {
		problem = "The table already has this column."
	}
	uikit.Modal(c, &a.columnOpen, func() {
		uikit.DialogColumn(c, uikit.DialogSmall).Children(func() {
			uikit.DialogTitle(c, locales.AddColumn2())
			ui.Scroll(c).MinHeight(0).Gap(t.Space(3)).Padding(0, t.Space(1)).Children(func() {
				ui.Form(c, func() {
					ui.Field(c, locales.From(), func() {
						uikit.SelectLabels(c, &draft.Source, columnSources, optionLabel).Label(locales.ColumnSource()).Width(200)
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
						ui.Field(c, locales.Name(), func() {
							uikit.Input(c, &draft.Name).Label(locales.ColumnNameOrJsonPointer()).Placeholder(placeholder).AutoFocus()
						}).Error(problem)
					} else if problem != "" {
						uikit.Hint(c, problem)
					}
				})
				if draft.body() {
					uikit.Hint(c, locales.ReadsCompleteRetainedJsonOff()).FillWidth()
				}
				uikit.Meta(c, locales.RightClickTheTableS())
			})
			uikit.DialogFooter(c).Children(func() {
				if uikit.SecondaryButton(c, locales.Cancel()).Clicked() {
					a.columnOpen = false
				}
				if uikit.PrimaryButton(c, locales.AddColumn2()).Disabled(name == "" || problem != "").Clicked() {
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
