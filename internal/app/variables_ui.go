package app

import (
	"fmt"
	"maps"
	"slices"
	"strings"

	"github.com/baba983/vscroxy/internal/request"
	"github.com/baba983/vscroxy/internal/uikit"
	"github.com/baba983/vscroxy/locales"
	"github.com/egoist/mygo/ui"
)

// variableRow is one editable row of a variable scope. Initial is shared and
// exportable; Current is local to this machine. HasInitial and HasCurrent keep
// an explicitly empty value, which a map cannot tell from an absent one.
type variableRow struct {
	Name       string
	Initial    string
	Current    string
	HasInitial bool
	HasCurrent bool
	// Credential marks a Current value that came from a credential slot; it
	// stays local and Persist current skips it.
	Credential bool
	Disabled   bool
	Revealed   bool
}

// variableRowsOf shows a scope's Initial and Current values as one row per
// name, including a Current name that has no Initial row.
func variableRowsOf(initial, current map[string]string, credentials map[string]bool) []variableRow {
	rows := make([]variableRow, 0, len(initial)+len(current))
	for _, name := range slices.Sorted(maps.Keys(initial)) {
		rows = append(rows, variableRow{Name: name, Initial: initial[name], HasInitial: true, Credential: credentials[name]})
	}
	for _, name := range slices.Sorted(maps.Keys(current)) {
		value := current[name]
		if at := slices.IndexFunc(rows, func(r variableRow) bool { return r.Name == name }); at >= 0 {
			rows[at].Current, rows[at].HasCurrent, rows[at].Credential = value, true, rows[at].Credential || credentials[name]
			continue
		}
		rows = append(rows, variableRow{Name: name, Current: value, HasCurrent: true, Credential: credentials[name]})
	}
	slices.SortStableFunc(rows, func(a, b variableRow) int { return strings.Compare(a.Name, b.Name) })
	return rows
}

// folderVariableRows shows one collection's or folder's Initial rows with its
// local Current values, including names that only exist locally.
func (a *app) folderVariableRows(path string) []variableRow {
	var initial []request.Variable
	for _, f := range a.folders {
		if f.Path == path {
			initial = f.Variables
			break
		}
	}
	rows := make([]variableRow, 0, len(initial)+len(a.folderCurrent[path]))
	credentials := a.folderCredentials[path]
	for _, v := range initial {
		name := strings.TrimSpace(v.Name)
		rows = append(rows, variableRow{Name: name, Initial: v.Initial, HasInitial: true, Disabled: v.Disabled, Credential: credentials[name]})
	}
	for _, name := range slices.Sorted(maps.Keys(a.folderCurrent[path])) {
		value := a.folderCurrent[path][name]
		if at := slices.IndexFunc(rows, func(r variableRow) bool { return r.Name == name }); at >= 0 {
			rows[at].Current, rows[at].HasCurrent, rows[at].Credential = value, true, rows[at].Credential || credentials[name]
			continue
		}
		rows = append(rows, variableRow{Name: name, Current: value, HasCurrent: true, Credential: credentials[name]})
	}
	slices.SortStableFunc(rows, func(a, b variableRow) int { return strings.Compare(a.Name, b.Name) })
	return rows
}

type variableTableOptions struct {
	enable bool
}

// variableTable edits one scope's rows: name, Initial value, Current value,
// and the actions that persist a Current value as Initial or reset it. It
// reports whether any row changed.
func (a *app) variableTable(c *ui.Context, rows *[]variableRow, opts variableTableOptions) bool {
	t := c.Theme()
	changed, remove := false, -1
	ui.Column(c).Grow(1).MinHeight(0).Border(1, t.Border).Radius(uikit.RadiusControl).Children(func() {
		ui.Row(c).Height(uikit.HeightControl).AlignItems(ui.Center).BorderWidth(0, 0, 1, 0).BorderColor(t.Border).Children(func() {
			if opts.enable {
				ui.Box(c).Width(uikit.HeightLarge).FillHeight().BorderWidth(0, 1, 0, 0).BorderColor(t.Border)
			}
			// Keep the padding inside each column so the title's flex base
			// matches the rows' cells.
			ui.Box(c).Basis(0).Grow(1).MinWidth(0).FillHeight().BorderWidth(0, 1, 0, 0).BorderColor(t.Border).Children(func() {
				uikit.Label(c, locales.Name()).FillWidth().Padding(t.Space(1), t.Space(2))
			})
			ui.Box(c).Basis(0).Grow(2).MinWidth(0).FillHeight().BorderWidth(0, 1, 0, 0).BorderColor(t.Border).Children(func() {
				uikit.Label(c, locales.Initial()).FillWidth().Padding(t.Space(1), t.Space(2))
			})
			ui.Box(c).Basis(0).Grow(2).MinWidth(0).FillHeight().BorderWidth(0, 1, 0, 0).BorderColor(t.Border).Children(func() {
				uikit.Label(c, locales.Current()).FillWidth().Padding(t.Space(1), t.Space(2))
			})
			ui.Box(c).Width(uikit.HeightControl * 4)
		})
		ui.Scroll(c).Grow(1).MinHeight(0).Children(func() {
			for i := range *rows {
				r := &(*rows)[i]
				row := ui.Row(c.Key(i)).Height(uikit.HeightControl).AlignItems(ui.Center).BorderWidth(0, 0, 1, 0).BorderColor(t.Border)
				row.Children(func() {
					if opts.enable {
						ui.Box(c).Width(uikit.HeightLarge).FillHeight().Center().BorderWidth(0, 1, 0, 0).BorderColor(t.Border).Children(func() {
							enabled := !r.Disabled
							if uikit.Tip(c, ui.Checkbox(c, &enabled, "").Label(locales.EnableVariable(i+1)), locales.UncheckedRowsStayHereAnd()).Changed() {
								r.Disabled, changed = !enabled, true
							}
						})
					}
					ui.Box(c).Basis(0).Grow(1).MinWidth(0).FillHeight().BorderWidth(0, 1, 0, 0).BorderColor(t.Border).Children(func() {
						field := requestGridInput(c, &r.Name, "name", fmt.Sprintf("Variable name %d", i+1))
						cellBorder(t, field, false, false)
						if field.Changed() {
							changed = true
						}
					})
					secret := r.Credential || sensitiveVariable(r.Name)
					ui.Box(c).Basis(0).Grow(2).MinWidth(0).FillHeight().BorderWidth(0, 1, 0, 0).BorderColor(t.Border).Children(func() {
						field := requestGridInput(c, &r.Initial, "Shared value", fmt.Sprintf("Variable initial value %d", i+1))
						if secret && !r.Revealed {
							field.Password()
						}
						cellBorder(t, field, secret && !r.Revealed, secret && r.Revealed)
						if field.Changed() {
							r.HasInitial, changed = true, true
						}
					})
					ui.Box(c).Basis(0).Grow(2).MinWidth(0).FillHeight().BorderWidth(0, 1, 0, 0).BorderColor(t.Border).Children(func() {
						field := requestGridInput(c, &r.Current, "Use initial", fmt.Sprintf("Variable current value %d", i+1))
						if !r.Revealed {
							field.Password()
						}
						cellBorder(t, field, !r.Revealed, r.Revealed)
						if field.Changed() {
							r.HasCurrent, changed = true, true
						}
					})
					ui.Row(c).Width(uikit.HeightControl * 4).Children(func() {
						if uikit.IconButton(c, locales.RevealVariable(i+1), locales.RevealOrHideBothValues(), eyeIcon).Clicked() {
							r.Revealed = !r.Revealed
						}
						blocked := r.Credential || sensitiveVariable(r.Name)
						persistTooltip := "Persist current as initial: writes it to the shared file and exports"
						if blocked {
							persistTooltip = "Sensitive values stay local; edit Initial explicitly if you need to export it"
						}
						ui.Row(c).Disabled(!r.HasCurrent || r.Current == "" || blocked).Children(func() {
							if uikit.IconButton(c, locales.PersistCurrentValue(i+1), persistTooltip, persistIcon).Clicked() {
								r.Initial, r.HasInitial, changed = r.Current, true, true
							}
						})
						ui.Row(c).Disabled(!r.HasCurrent).Children(func() {
							if uikit.IconButton(c, locales.ResetCurrentValue(i+1), locales.ResetCurrentUseTheInitial(), resetIcon).Clicked() {
								if !r.HasInitial && r.Initial == "" {
									remove = i // nothing shared remains, so the local-only row goes
								} else {
									r.Current, r.HasCurrent, changed = "", false, true
								}
							}
						})
						// The action column ends at the table's edge, so keep
						// its focus ring inside the button instead of clipping
						// it against the table border.
						if uikit.IconButton(c, locales.RemoveVariableIndex(i+1), locales.RemoveVariable(), closeIcon).FocusRingOffset(-2).Clicked() {
							remove = i
						}
					})
				})
			}
			if remove >= 0 {
				*rows = slices.Delete(*rows, remove, remove+1)
				changed = true
			}
			ui.Row(c).Disabled(a.envsProblem != "").Children(func() {
				// The add row sits on the scroll's bottom edge; keep the
				// focus ring inside it instead of clipping it against the
				// table border.
				if uikit.ToolbarButton(c, locales.AddVariable(), locales.AddVariable(), false).FocusRingOffset(-2).Children(func() {
					ui.Icon(c, plusIcon).Size(uikit.IconInline, uikit.IconInline)
					uikit.Body(c, locales.AddVariable())
				}).Clicked() {
					*rows = append(*rows, variableRow{})
					changed = true
				}
			})
		})
	})
	return changed
}

// persistAllCurrent copies non-sensitive Current values into Initial values.
// Credential slots and credential-like names stay local; the counts say what
// happened so the dialog can report skipped values.
func persistAllCurrent(rows []variableRow) (persisted, skipped int) {
	for i := range rows {
		if !rows[i].HasCurrent || rows[i].Current == "" {
			continue
		}
		if rows[i].Credential || sensitiveVariable(rows[i].Name) {
			skipped++
			continue
		}
		rows[i].Initial, rows[i].HasInitial = rows[i].Current, true
		persisted++
	}
	return persisted, skipped
}

// resetAllCurrent drops every local override, so Initial values apply again.
func resetAllCurrent(rows []variableRow) {
	for i := range rows {
		rows[i].Current, rows[i].HasCurrent = "", false
	}
}

func anyCurrent(rows []variableRow) bool {
	return slices.ContainsFunc(rows, func(r variableRow) bool { return r.HasCurrent })
}

// variablesOf splits edited rows into the Initial values a shared file or an
// export carries and the local Current values. A row with a name and no
// Current value is an Initial row, even when its value is empty; a row with
// only a Current value stays local until the reader persists it. It reports a
// bad name.
func variablesOf(rows []variableRow) (initial, current map[string]string, credentials map[string]bool, err error) {
	initial, current = map[string]string{}, map[string]string{}
	credentials = map[string]bool{}
	seen := map[string]bool{}
	for _, r := range rows {
		name := strings.TrimSpace(r.Name)
		if name == "" && !r.HasInitial && !r.HasCurrent && r.Initial == "" && r.Current == "" {
			continue
		}
		if name == "" || strings.ContainsAny(name, "{}") {
			return nil, nil, nil, fmt.Errorf("variable names must be nonempty and contain no braces")
		}
		if seen[name] {
			return nil, nil, nil, fmt.Errorf("variable names must be unique")
		}
		seen[name] = true
		if r.HasInitial || !r.HasCurrent || r.Initial != "" {
			initial[name] = r.Initial
		}
		if r.Credential {
			credentials[name] = true
		}
		if r.HasCurrent {
			current[name] = r.Current
		}
	}
	if len(credentials) == 0 {
		credentials = nil
	}
	return initial, current, credentials, nil
}
