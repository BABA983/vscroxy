package app

import (
	"cmp"
	"fmt"
	"net/http"
	"net/url"
	"slices"
	"strconv"
	"strings"

	"github.com/baba983/vscroxy/internal/uikit"

	"github.com/baba983/vscroxy/internal/inspect"
	"github.com/baba983/vscroxy/internal/request"
	"github.com/baba983/vscroxy/locales"
	"github.com/egoist/mygo"
	"github.com/egoist/mygo/ui"
	"github.com/egoist/mygo/ui/editor"
)

// numberText seeds a number field's text from its value the first time the
// field is drawn. A caller that resets the value from elsewhere resets the
// text with it.
func numberText(text *string, value float64) {
	if *text == "" {
		*text = strconv.Itoa(int(value))
	}
}

// pathEditor edits the values of the URL's :name path segments. The names come
// from the URL, so there are no rows to add or remove; a blank value is sent as
// {{name}}.
func (a *app) pathEditor(c *ui.Context, d *requestDocument, t *ui.Theme) {
	d.pathRows = pathRowsFor(d.composeURL, d.pathRows)
	if len(d.pathRows) == 0 {
		return
	}
	noun := locales.Parameter()
	ui.Column(c).Gap(t.Space(uikit.SpaceM)).Children(func() {
		uikit.Label(c, locales.PathParameters())
		for i := range d.pathRows {
			r := &d.pathRows[i]
			ui.Row(c.Key(fmt.Sprintf("path-row-%d", i))).Height(uikit.HeightRow).Gap(t.Space(uikit.SpaceM)).AlignItems(ui.Center).Children(func() {
				ui.Row(c).Width(uikit.WidthNarrow).Gap(t.Space(uikit.SpaceXS)).AlignItems(ui.Center).Children(func() {
					uikit.DataBody(c, ":"+r.Name).SingleLine().Shrink(1)
					a.unicodeMarker(c, fmt.Sprintf("path-name-hint-%d", i), r.Name, false)
				})
				ui.Box(c).Basis(0).Grow(1).MinWidth(0).FillHeight().Children(func() {
					ui.Row(c).FillWidth().AlignItems(ui.Center).Gap(t.Space(uikit.SpaceXS)).Children(func() {
						a.variableGridInput(c, fmt.Sprintf("path-value-%d", i), &r.Value, locales.Value(), locales.ValueOfRow(noun, i+1), d.composeCollection, false, true).Grow(1).MinWidth(0)
						a.unicodeMarker(c, fmt.Sprintf("path-value-hint-%d", i), r.Value, false)
					})
				})
			})
		}
	})
}

// pathRowsFor keeps the value of each :name segment of url, in URL order, from
// the rows that already hold it. A name the URL no longer has is dropped, and a
// name with no row is blank.
func pathRowsFor(url string, rows []request.Field) []request.Field {
	var out []request.Field
	for _, name := range request.PathNames(url) {
		if slices.ContainsFunc(out, func(f request.Field) bool { return f.Name == name }) {
			continue
		}
		value := ""
		if at := slices.IndexFunc(rows, func(f request.Field) bool { return f.Name == name }); at >= 0 {
			value = rows[at].Value
		}
		out = append(out, request.Field{Name: name, Value: value})
	}
	return out
}

func (a *app) paramsEditor(c *ui.Context, d *requestDocument, t *ui.Theme) {
	a.pathEditor(c, d, t)
	if d.querySource != d.composeURL {
		rows, err := queryRowsFor(d.composeURL, d.queryRows)
		d.querySource, d.queryRows, d.queryError = d.composeURL, rows, ""
		if err != nil {
			d.queryError = err.Error()
		}
	}
	if d.queryError != "" {
		uikit.ErrorText(c, d.queryError)
	}
	// Query params heads its table only when Path variables stands above it:
	// on its own, the Params tab already says what the table holds.
	queryCaption := ""
	if len(d.pathRows) > 0 {
		queryCaption = locales.QueryParams()
	}
	if a.requestFieldsEditor(c, &d.queryRows, fieldsOptions{collection: d.composeCollection, caption: queryCaption,
		key: "parameter", focus: d.unicodeHandle, description: true, measure: &d.fieldTablePane, seen: &d.fieldTableWidth}, locales.Parameter()) {
		u, err := url.Parse(d.composeURL)
		if err == nil {
			encode := a.inheritance(d.composeCollection).InheritSettings(d.settings).EncodesURL()
			var parts []string
			for _, row := range d.queryRows {
				if row.Disabled || row.Name == "" && row.Value == "" {
					continue
				}
				if encode {
					parts = append(parts, url.QueryEscape(row.Name)+"="+url.QueryEscape(row.Value))
				} else {
					parts = append(parts, row.Name+"="+row.Value)
				}
			}
			u.RawQuery, u.ForceQuery = strings.Join(parts, "&"), false
			d.composeURL = u.String()
			d.querySource = d.composeURL
		}
	}
}

// queryRowsFor reads a URL's query rows in their written order and carries a
// description from the previous rows by key occurrence. A key that was
// renamed loses its description, as the row is a new one.
func queryRowsFor(raw string, previous []request.Field) ([]request.Field, error) {
	rows, err := parseQueryRows(raw)
	if err != nil {
		return nil, err
	}
	descriptions := map[string][]string{}
	for _, row := range previous {
		descriptions[row.Name] = append(descriptions[row.Name], row.Description)
	}
	seen := map[string]int{}
	for i := range rows {
		name := rows[i].Name
		if at := seen[name]; at < len(descriptions[name]) {
			rows[i].Description = descriptions[name][at]
		}
		seen[name]++
	}
	return rows, nil
}

// parseQueryRows reads the URL's query pairs, preserving order, duplicates
// and escapes. net/url's Query() sorts keys and loses that order.
func parseQueryRows(raw string) ([]request.Field, error) {
	u, err := url.Parse(raw)
	if err != nil {
		return nil, err
	}
	var rows []request.Field
	for part := range strings.SplitSeq(u.RawQuery, "&") {
		if part == "" {
			continue
		}
		key, value, _ := strings.Cut(part, "=")
		key, err = url.QueryUnescape(key)
		if err != nil {
			return nil, err
		}
		value, err = url.QueryUnescape(value)
		if err != nil {
			return nil, err
		}
		rows = append(rows, request.Field{Name: key, Value: value})
	}
	return rows, nil
}

// queryDescriptionsFor aligns the table's descriptions with the URL's query
// pairs. A row no longer in the URL contributes nothing.
func queryDescriptionsFor(raw string, rows []request.Field) []string {
	parsed, err := parseQueryRows(raw)
	if err != nil {
		return nil
	}
	descriptions := map[string][]string{}
	for _, row := range rows {
		if row.Disabled || row.Name == "" {
			continue
		}
		descriptions[row.Name] = append(descriptions[row.Name], row.Description)
	}
	seen := map[string]int{}
	out := make([]string, len(parsed))
	any := false
	for i, row := range parsed {
		at := seen[row.Name]
		if at < len(descriptions[row.Name]) {
			out[i] = descriptions[row.Name][at]
			any = any || out[i] != ""
		}
		seen[row.Name]++
	}
	if !any {
		return nil
	}
	return out
}

// fieldsOptions says what a row table edits: file parts carry a File flag,
// and header values are masked while their key is a credential.
type fieldsOptions struct {
	file bool
	// fileRoom reserves the Value cell's file-toggle room even when the
	// table is a URL-encoded one. Both form body modes then decide the
	// Description column from the same width, so switching mode does not
	// make a column appear or disappear at the same pane size.
	fileRoom bool
	secret   bool
	// description adds the row's documentation column.
	description bool
	collection  string
	// caption names the table above its grid, as "Query Params" or "Headers".
	caption string
	// headerEnd draws the table's own actions at the end of the last titled
	// header cell, as Postman puts Bulk Edit there instead of on a toolbar.
	headerEnd func()
	// key is the locale-independent name used for row focus handles and the
	// table's own empty-row state.
	key string
	// focus hands a row cell its handle, so a send warning can open it.
	focus func(string) *ui.Handle
	// measure receives the table's laid-out width, which decides whether the
	// Description column fits. seen keeps the last non-zero width while the
	// table is not mounted, so a mode switch does not flash a column.
	measure *ui.Handle
	seen    *float32
	// filters are the captured values a row's context menu offers, by name.
	filters map[string][]string
}

// fieldsKey is stable across languages: a localized noun labels the row, but
// the handle a warning opens must keep its name when the app's locale changes.
func fieldsKey(opts fieldsOptions, noun, part string, row int) string {
	return fmt.Sprintf("%s-%s-%d", cmp.Or(opts.key, noun), part, row)
}

// Fields are rows rather than a map: duplicate names, disabled entries and file
// parts all survive saving. Only Send/export reads a selected file. The table
// itself is the kit's grid: its trailing empty row adds a row when typed into.
func (a *app) requestFieldsEditor(c *ui.Context, rows *[]request.Field, opts fieldsOptions, noun string) bool {
	t := c.Theme()
	changed, remove := false, -1
	// A grid cell's minimum describes its content; each editable titled
	// column also keeps GridCellPadding at both sides.
	cellInset := 2 * t.Space(uikit.GridCellPadding)
	keyGrow, valueGrow, keyMin, valueMin := float32(1), float32(2), float32(uikit.WidthNarrow), float32(uikit.WidthPicker)
	// The value cell also holds its row actions: the mask/file toggle and the
	// delete action. Keep their room inside the column minimum, so a wide
	// value can never squeeze them into its text. A table with no masked or
	// file row spends the second action's room on the Description column.
	valueMin += uikit.HeightRowAction + t.Space(uikit.SpaceXS)
	if opts.file || opts.fileRoom || opts.secret && slices.ContainsFunc(*rows, func(r request.Field) bool { return sensitiveHeader(r.Name) }) {
		valueMin += uikit.HeightRowAction + t.Space(uikit.SpaceXS)
	}
	showDescription := false
	if opts.description {
		// The table's own width from the frame before, as the pane's tab row
		// measures itself. Moving the divider or stacking the panes changes it
		// on the next frame.
		tableWidth := float32(0)
		if opts.measure != nil {
			tableWidth = opts.measure.Bounds(c).W
		}
		if tableWidth > 0 && opts.seen != nil {
			*opts.seen = tableWidth
		}
		if tableWidth == 0 && opts.seen != nil {
			tableWidth = *opts.seen
		}
		if tableWidth == 0 {
			// No table has been laid out yet in this pane: use the request
			// pane's committed width, less the content's side padding, and
			// only fall back to the window before the first frame.
			if b := a.editorReqPane.Bounds(c); b.W > 0 {
				tableWidth = b.W - 2*t.Space(uikit.SpaceL)
			}
		}
		if tableWidth > 0 {
			left := tableWidth - uikit.HeightControl - keyMin - valueMin - 2*cellInset
			showDescription = left >= uikit.WidthNarrow+cellInset
		} else {
			// No layout yet: use the window so the first frame at a wide size
			// does not flash the narrow layout.
			width, _ := c.Size()
			showDescription = width >= uikit.WidthWide*6+uikit.HeightControl*2
		}
	}
	descriptionGrow, descriptionMin := float32(2), float32(uikit.WidthNarrow)
	columns := []uikit.KeyValueColumn{
		{
			Width: uikit.HeightControl,
			Cell: func(row int, _, _ ui.Element) {
				r := &(*rows)[row]
				enabled := !r.Disabled
				ui.Box(c).FillWidth().FillHeight().Center().Children(func() {
					if uikit.Tip(c, uikit.OutlineCheckbox(c, &enabled, "").Label(locales.EnableRow(noun, row+1)), locales.UncheckedRowsStayHereAnd()).Changed() {
						r.Disabled, changed = !enabled, true
					}
				})
			},
		},
		{
			Title: locales.Key(), Grow: keyGrow, MinWidth: keyMin,
			Cell: func(row int, _ ui.Element, cell ui.Element) {
				r := &(*rows)[row]
				ui.Row(c).FillWidth().FillHeight().AlignItems(ui.Center).Gap(t.Space(uikit.SpaceXS)).Children(func() {
					keyName := fieldsKey(opts, noun, "key", row+1)
					field := a.variableGridInput(c, keyName, &r.Name, locales.Key(), locales.KeyOfRow(noun, row+1), opts.collection, false, false).
						Grow(1).MinWidth(0)
					if opts.focus != nil {
						field.Bind(opts.focus(keyName))
					}
					field = uikit.TruncatedCell(c, field, r.Name, false, t.Space(uikit.GridCellPadding), t.Background)
					cellBorder(t, cell, field, false, false)
					if values, ok := opts.filters[r.Name]; ok {
						// The capture's own values are what a filter can
						// search for, as the reader offers them.
						uikit.ContextMenu(c, field, a.headerFilterMenu(c, r.Name, values))
					}
					a.unicodeMarker(c, fmt.Sprintf("%s-key-hint-%d", noun, row+1), r.Name, false)
					if field.Changed() {
						changed = true
					}
				})
			},
		},
		{
			Title: locales.Value(), Grow: valueGrow, MinWidth: valueMin,
			Cell: func(row int, line, cell ui.Element) {
				r := &(*rows)[row]
				// Masked until the reader reveals this row with its eye: the
				// keyboard alone never shows a credential, so tabbing
				// through a table cannot put one on a shared screen.
				secret := opts.secret && sensitiveHeader(r.Name)
				masked := secret && !r.Revealed
				ui.Row(c).FillWidth().FillHeight().AlignItems(ui.Center).Gap(t.Space(uikit.SpaceXS)).Children(func() {
					valueKey := fieldsKey(opts, noun, "value", row+1)
					value := a.variableGridInput(c, valueKey, &r.Value, locales.Value(), locales.ValueOfRow(noun, row+1), opts.collection, masked, false).
						Grow(1).MinWidth(0)
					if opts.focus != nil {
						value.Bind(opts.focus(valueKey))
					}
					if masked {
						value.Password()
					}
					fill := t.Background
					if secret && r.Revealed {
						// The cell tint is composited the same way under the
						// ellipsis patch, so the cut leaves no seam.
						fill = t.Accent.Alpha(0.06).Over(t.Background)
						value.Background(t.Accent.Alpha(0.06))
					}
					// A long value reads in full on hover, without the click
					// that would move the keyboard into it.
					value = uikit.TruncatedCell(c, value, r.Value, masked, t.Space(uikit.GridCellPadding), fill)
					cellBorder(t, cell, value, masked, secret && r.Revealed)
					// Scan the raw value even when masked; the hint then names
					// only code points, kinds and positions.
					a.unicodeMarker(c, fmt.Sprintf("%s-value-hint-%d", noun, row+1), r.Value, masked)
					if value.Changed() {
						changed = true
					}
					if opts.file {
						toggle := uikit.RowIconButton(c, locales.FilePartLabel(row+1), locales.SendAsFile(), fileInputIcon)
						if r.File {
							toggle.Background(t.Accent.Alpha(0.18)).TextColor(t.Accent)
						}
						if toggle.Clicked() {
							r.File = !r.File
							changed = true
						}
					}
					if secret {
						label := locales.RevealRow(noun, row+1)
						if r.Revealed {
							label = locales.HideRow(noun, row+1)
						}
						reveal := uikit.RowIconButton(c, label, locales.CredentialsAreMaskedByDefault(), eyeIcon).Shrink(0)
						if r.Revealed {
							reveal.TextColor(t.Accent)
						}
						if reveal.Clicked() {
							r.Revealed = !r.Revealed
						}
					}
					label := locales.RemoveRow(noun, row+1)
					button := uikit.RowIconButton(c, label, label, closeIcon).FocusRingOffset(-2).Shrink(0)
					// Keep the control mounted, named and in the Tab order even while
					// quiet. Any focus in the row reveals its action.
					if !line.Hovered() && !line.FocusWithin() {
						button.Opacity(0)
					}
					if button.Clicked() {
						remove = row
					}
				})
			},
		},
	}
	descriptionColumn := -1
	if showDescription {
		descriptionColumn = len(columns)
		columns = append(columns, uikit.KeyValueColumn{
			Title: locales.Description(), Grow: descriptionGrow, MinWidth: descriptionMin,
			Cell: func(row int, _ ui.Element, cell ui.Element) {
				r := &(*rows)[row]
				ui.Row(c).FillWidth().FillHeight().AlignItems(ui.Center).Gap(t.Space(uikit.SpaceXS)).Children(func() {
					field := a.variableGridInput(c, fieldsKey(opts, noun, "description", row+1), &r.Description,
						locales.Description(), locales.DescriptionOfRow(noun, row+1), opts.collection, false, false).
						Grow(1).MinWidth(0)
					if opts.focus != nil {
						field.Bind(opts.focus(fieldsKey(opts, noun, "description", row+1)))
					}
					field = uikit.TruncatedCell(c, field, r.Description, false, t.Space(uikit.GridCellPadding), t.Background)
					cellBorder(t, cell, field, false, false)
					a.unicodeMarker(c, fmt.Sprintf("%s-description-hint-%d", noun, row+1), r.Description, false)
					if field.Changed() {
						changed = true
					}
				})
			},
		})
	}
	var emptyRowText map[int]string
	if descriptionColumn >= 0 {
		emptyRowText = map[int]string{descriptionColumn: locales.Description()}
	}
	uikit.KeyValueTable(c, uikit.KeyValueTableOptions{
		Key:              "kv:" + cmp.Or(opts.key, noun),
		Caption:          opts.caption,
		HeaderEnd:        opts.headerEnd,
		Columns:          columns,
		Rows:             len(*rows),
		KeyColumn:        1,
		ValueColumn:      2,
		KeyPlaceholder:   locales.Key(),
		ValuePlaceholder: locales.Value(),
		KeyLabel:         locales.KeyOfRow(noun, len(*rows)+1),
		ValueLabel:       locales.ValueOfRow(noun, len(*rows)+1),
		EmptyRowText:     emptyRowText,
		Measure:          opts.measure,
		OnAdd: func(key, value string, valueCell bool) {
			*rows = append(*rows, request.Field{Name: key, Value: value})
			changed = true
			if opts.focus != nil {
				part := "key"
				if valueCell {
					part = "value"
				}
				if handle := opts.focus(fieldsKey(opts, noun, part, len(*rows))); handle != nil {
					handle.Focus(c)
				}
			}
		},
	})
	if remove >= 0 {
		*rows = slices.Delete(*rows, remove, remove+1)
		changed = true
	}
	return changed
}

// Base inputs keep the grid's cells borderless at rest without losing native
// text editing, password handling, accessibility or keyboard focus rings.
func requestGridInput(c *ui.Context, value *string, placeholder, label string) ui.Element {
	return uikit.InputControl(c, value).Label(label).Placeholder(placeholder).FillWidth().MinWidth(0).
		FillHeight().Padding(0, c.Theme().Space(uikit.SpaceM)).Font(uikit.FontMono).
		Border(1, ui.Color{})
}

// cellBorder lights the grid cell around a focused input, so the outline
// lands on the table's own lines instead of around an inset field. A revealed
// credential is the one cell that says so with a tint; at rest a cell is only
// its text.
func cellBorder(t *ui.Theme, cell, field ui.Element, masked, revealed bool) {
	switch {
	case revealed:
		field.Background(t.Accent.Alpha(0.06))
	case field.Focused():
		// A border on the cell, not a stroke over it: the cell's own box is
		// the grid's line, row actions included.
		cell.BorderWidth(uikit.HairlineWidth, uikit.HairlineWidth, uikit.HairlineWidth, uikit.HairlineWidth).
			BorderColor(t.Accent)
	}
	if masked {
		field.TextColor(t.TextMuted)
	}
}

// mergeHeaderRows puts the rows the block cannot carry back under the ones it
// does, so switching to bulk edit and back keeps switched-off headers.
func mergeHeaderRows(rows, off []request.Field) []request.Field {
	return append(rows, off...)
}

// hasSecretHeader reports whether a table holds a value worth masking.
func hasSecretHeader(rows []request.Field) bool {
	return slices.ContainsFunc(rows, func(row request.Field) bool {
		return !row.Disabled && row.Value != "" && sensitiveHeader(row.Name)
	})
}

// toggleHeaderMode swaps the table and the Key: Value block, keeping the rows
// the block cannot carry.
func toggleHeaderMode(d *requestDocument) {
	if d.headerBulk {
		d.headerRows, d.headerFor = mergeHeaderRows(parseHeaderRows(d.composeHeaders), d.headerOff), d.composeHeaders
	} else {
		d.headerOff = slices.DeleteFunc(slices.Clone(d.headerRows), func(row request.Field) bool { return !row.Disabled })
		d.composeHeaders, d.headerFor = headerRowsText(d.headerRows), headerRowsText(d.headerRows)
	}
	d.headerBulk = !d.headerBulk
}

// headerRowsText is the Key: Value block saved, sent and copied: the enabled
// rows in their order, duplicates included.
func headerRowsText(rows []request.Field) string {
	var b strings.Builder
	for _, row := range rows {
		if row.Disabled || row.Name == "" {
			continue
		}
		b.WriteString(row.Name + ": " + row.Value + "\n")
	}
	return strings.TrimSuffix(b.String(), "\n")
}

// headerRowsKept drops the blank row a new table starts with, so an empty
// table never makes a document look edited.
func headerRowsKept(rows []request.Field) []request.Field {
	kept := slices.DeleteFunc(slices.Clone(rows), func(row request.Field) bool {
		return row.Name == "" && row.Value == "" && !row.Disabled
	})
	if len(kept) == 0 {
		return nil // the table's blank row is not an edit of a request without headers
	}
	return kept
}

// parseHeaderRows reads that block back into rows, keeping order and repeats.
func parseHeaderRows(text string) []request.Field {
	var rows []request.Field
	for _, line := range strings.Split(text, "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		key, value, ok := strings.Cut(line, ":")
		if !ok || strings.TrimSpace(key) == "" {
			continue
		}
		rows = append(rows, request.Field{Name: strings.TrimSpace(key), Value: strings.TrimSpace(value)})
	}
	return rows
}

// headerEditor edits headers as rows, so order, duplicates, empty values and
// disabled rows all survive. Bulk edit shows the Key: Value block for pasting.
func (a *app) headerEditor(c *ui.Context, d *requestDocument, t *ui.Theme) {
	if d.composeHeaders != d.headerFor {
		// The block changed elsewhere: an import, a script or the bulk editor.
		d.headerRows, d.headerFor = parseHeaderRows(d.composeHeaders), d.composeHeaders
	}
	if d.headerBulk {
		ui.Row(c).Height(uikit.HeightControl).AlignItems(ui.Center).Children(func() {
			uikit.Small(c, locales.Headers()).TextColor(t.Text)
			ui.Spacer(c)
			if uikit.LinkButton(c, locales.BulkEdit()).Clicked() {
				a.toggleHeaderBulk(d)
			}
		})
		// The editor keeps the block verbatim; the marker reads the raw text
		// rather than its soft-wrapped display.
		a.unicodeMarker(c, "bulk-headers", d.composeHeaders, false)
		// The block reads in the same editor as the body: line numbers,
		// find and undo, one Key: Value per line.
		if d.bulkEditor == nil || d.bulkFor != d.composeHeaders {
			d.bulkEditor = &editor.State{Buffer: editor.NewBuffer(d.composeHeaders), NoScrollPastEnd: true, SoftWrap: true, Label: locales.RequestHeaders()}
			d.bulkFor = d.composeHeaders
		}
		a.installUnicodeHover(d.bulkEditor)
		a.wireVariableEditor(d.bulkEditor, d.composeCollection)
		a.appendUnicodeDiagnostics(d.bulkEditor, d.composeHeaders)
		requestCodeEditor(c, d.bulkEditor, d.unicodeHandle("header-bulk"))
		a.bindQuickFixShortcut(c, d.unicodeHandle("header-bulk"))
		a.trackEditorQuickFix(d, problemFieldHeaderRaw, d.bulkEditor)
		a.quickFixHandleMenu(c, d.unicodeHandle("header-bulk"), d.bulkEditor)
		if text := d.bulkEditor.Buffer.String(); text != d.composeHeaders {
			d.composeHeaders, d.bulkFor = text, text
		}
		return
	}
	if a.requestFieldsEditor(c, &d.headerRows, fieldsOptions{secret: true, description: true, collection: d.composeCollection,
		measure: &d.fieldTablePane, seen: &d.fieldTableWidth, headerEnd: func() {
			if uikit.LinkButton(c, locales.BulkEdit()).Clicked() {
				a.toggleHeaderBulk(d)
			}
		}, key: "header", focus: d.unicodeHandle, filters: a.capturedHeaders()}, locales.NounHeader()) {
		d.composeHeaders = headerRowsText(d.headerRows)
		d.headerFor = d.composeHeaders
	}
}

// toggleHeaderBulk opens the block, asking first when it would put a
// credential on the screen.
func (a *app) toggleHeaderBulk(d *requestDocument) {
	if d.headerBulk || !hasSecretHeader(d.headerRows) {
		toggleHeaderMode(d)
		return
	}
	d.headerWarn = true
}

func (a *app) authEditor(c *ui.Context, d *requestDocument, t *ui.Theme) {
	inherited := d.auth.Type == request.AuthInherit
	resolved, from := a.inheritance(d.composeCollection).InheritAuth(d.auth)
	shown, typeName := &d.auth, optionLabel(d.auth.Type)
	if inherited {
		shown, typeName = &resolved, optionLabel(resolved.Type)
	}
	ui.Scroll(c).Grow(1).MinHeight(0).FillWidth().Gap(t.Space(uikit.SpaceL)).Padding(0, t.Space(uikit.SpaceXS)).Children(func() {
		columns := ui.Row(c).FillWidth().Gap(t.Space(uikit.SpaceM)).AlignItems(ui.Start)
		if a.editorPaneWidthAt(c, &a.editorReqPane, draftPercent) < 460 {
			columns = ui.Column(c).FillWidth().Gap(t.Space(uikit.SpaceL))
		}
		columns.Children(func() {
			ui.Column(c).Width(uikit.WidthFieldWide).Shrink(0).Gap(t.Space(uikit.SpaceM)).Children(func() {
				uikit.Label(c, locales.AuthType())
				uikit.Select(c, &d.auth.Type, append([]string{request.AuthInherit}, authTypes...)).
					Label(locales.AuthorizationType()).FillWidth()
				uikit.Hint(c, locales.PreferVariablesOrVaultName())
			})
			ui.Column(c).Grow(1).MinWidth(0).Gap(t.Space(uikit.SpaceM)).Children(func() {
				ui.Row(c).Gap(t.Space(uikit.SpaceS)).AlignItems(ui.Center).Children(func() {
					uikit.BodyStrong(c, typeName)
					if inherited {
						uikit.Badge(c, locales.Inherited(), t.TextMuted)
					}
				})
				if inherited {
					ui.Column(c).Disabled(true).Children(func() {
						ui.Form(c, func() { a.authFieldRows(c, shown, d.composeCollection, &d.asapExpiryText, d.unicodeHandle) })
					})
					text := locales.NoAuthSet()
					if from != "" {
						text = locales.UsesAuthFrom(resolved.Type, from)
					}
					ui.Row(c).Gap(t.Space(uikit.SpaceM)).AlignItems(ui.Center).Children(func() {
						uikit.Hint(c, text)
						if path := cmp.Or(from, d.composeCollection); path != "" && uikit.SecondaryButton(c, settingsLabel(path)).Clicked() {
							a.openFolder(path)
						}
					})
				} else {
					ui.Form(c, func() { a.authFieldRows(c, shown, d.composeCollection, &d.asapExpiryText, d.unicodeHandle) })
				}
			})
		})
		if d.auth.Type == request.AuthOAuth2 {
			a.savedOAuthTokens(c)
		}
	})
}

// authTypes are the auth a request or folder sets itself.
var authTypes = []string{"None", "Bearer", "Basic", "API key", request.AuthOAuth2, request.AuthOAuth1,
	request.AuthDigest, request.AuthNTLM, request.AuthAWS, request.AuthJWT, request.AuthHawk, request.AuthEdgeGrid, request.AuthASAP}

// authFields edits one Auth in a Form: its type, then the fields that type
// uses. The request tab puts the type beside the fields instead.
func (a *app) authFields(c *ui.Context, auth *request.Auth, types []string, collection string, asapExpiryText *string, focus ...func(string) *ui.Handle) {
	t := c.Theme()
	if auth.Type != request.AuthInherit {
		uikit.Meta(c, locales.PreferVariablesOrVaultName())
	}
	ui.Form(c, func() {
		ui.Field(c, locales.TypeText(), func() {
			uikit.Select(c, &auth.Type, types).Label(locales.AuthorizationType()).Width(uikit.WidthWide)
		})
		a.authFieldRows(c, auth, collection, asapExpiryText, focus...)
	}).Gap(t.Space(uikit.SpaceL))
}

// authFieldRows draws the fields the chosen type uses, inside a Form so their
// labels line up with the control beside them.
func (a *app) authFieldRows(c *ui.Context, auth *request.Auth, collection string, asapExpiryText *string, focus ...func(string) *ui.Handle) {
	bind := func(element ui.Element, label string) ui.Element {
		if len(focus) > 0 && focus[0] != nil {
			if handle := focus[0]("auth-" + label); handle != nil {
				element.Bind(handle)
			}
		}
		return element
	}
	input := func(value *string, label string) ui.Element {
		return bind(a.variableInput(c, "auth-"+label, value, label, collection), label).FillWidth()
	}
	area := func(value *string, label string) ui.Element {
		return bind(a.variableTextArea(c, "auth-"+label, value, label, collection), label).FillWidth()
	}
	password := func(value *string, label string) ui.Element {
		return bind(a.variablePasswordInput(c, "auth-"+label, value, label, collection), label).FillWidth()
	}
	sensitiveArea := func(value *string, label string) ui.Element {
		return bind(a.variableSensitiveTextArea(c, "auth-"+label, value, label, collection), label).FillWidth()
	}
	switch auth.Type {
	case "Bearer":
		ui.Field(c, locales.BearerToken(), func() {
			password(&auth.Token, "Bearer token").Placeholder(locales.Token())
		})
	case "Basic":
		ui.Field(c, locales.Username(), func() { input(&auth.Username, "Username") })
		ui.Field(c, locales.Password(), func() { password(&auth.Password, "Password") })
	case "API key":
		ui.Field(c, locales.ApiKeyName(), func() { input(&auth.Key, "API key name") })
		ui.Field(c, locales.ApiKeyValue(), func() { password(&auth.Value, "API key value") })
		ui.Field(c, locales.Location(), func() {
			uikit.SelectLabels(c, &auth.Location, []string{"Header", "Query"}, optionLabel).Label(locales.ApiKeyLocation()).FillWidth()
		})
	case request.AuthOAuth2:
		a.oauthFields(c, auth, collection)
	case request.AuthOAuth1:
		o := &auth.OAuth1
		if o.SignatureMethod == "" {
			o.SignatureMethod = request.OAuth1Methods[0]
		}
		addTo := cmp.Or(o.AddTo, "Header")
		if addTo == "Auto" {
			addTo = "Automatic"
		}
		ui.Field(c, locales.ConsumerKey(), func() { input(&o.ConsumerKey, "Consumer key") })
		ui.Field(c, locales.ConsumerSecret(), func() {
			password(&o.ConsumerSecret, "Consumer secret").Placeholder("{{vault:oauth1_secret}}")
		})
		ui.Field(c, locales.Token2(), func() { input(&o.Token, "Token").Placeholder(locales.OptionalForTwoLeggedAuth()) })
		ui.Field(c, locales.TokenSecret(), func() { password(&o.TokenSecret, "Token secret") })
		ui.Field(c, locales.SignatureMethod(), func() {
			uikit.Select(c, &o.SignatureMethod, request.OAuth1Methods).Width(180)
		})
		if strings.HasPrefix(o.SignatureMethod, "RSA-") {
			ui.Field(c, locales.PrivateKey(), func() {
				sensitiveArea(&o.PrivateKey, "Private key").Height(72).Font(uikit.FontMono).Radius(uikit.RadiusControl).Placeholder("-----BEGIN PRIVATE KEY----- … or {{vault:oauth1_key}}")
			})
		}
		ui.Field(c, locales.AddAuthorizationTo(), func() {
			if uikit.SelectLabels(c, &addTo, []string{"Header", "Query", "Body", "Automatic"}, optionLabel).Width(180).Changed() {
				o.AddTo = addTo
				if addTo == "Header" {
					o.AddTo = ""
				} else if addTo == "Automatic" {
					o.AddTo = "Auto"
				}
			}
		}).Description(locales.AutomaticUsesAFormBody())
		ui.Field(c, locales.BodyHash(), func() {
			uikit.Checkbox(c, &o.IncludeBodyHash, locales.HashNonFormBody())
		}).Description(locales.AddsOauthBodyHashForm())
		ui.Field(c, locales.EmptyParameters(), func() {
			uikit.Checkbox(c, &o.AddEmptyParamsToSign, locales.IncludeInSignature())
		})
		ui.Field(c, locales.Timestamp(), func() { input(&o.Timestamp, "Timestamp").Placeholder(locales.Now()).Width(uikit.WidthFieldWide) })
		ui.Field(c, locales.Nonce(), func() { input(&o.Nonce, "Nonce").Placeholder(locales.Random()).Width(uikit.WidthFieldWide) })
		ui.Field(c, locales.Version(), func() { input(&o.Version, "Version").Placeholder("1.0").Width(uikit.WidthNarrow) })
		ui.Field(c, locales.Realm(), func() { input(&o.Realm, "Realm").Placeholder(locales.Optional()) })
		ui.Field(c, locales.CallbackUrl(), func() { input(&o.Callback, "Callback URL").Placeholder(locales.OptionalOauthCallback()) })
		ui.Field(c, locales.Verifier(), func() { input(&o.Verifier, "Verifier").Placeholder(locales.OptionalOauthVerifier()) })
	case request.AuthDigest:
		ui.Field(c, locales.Username(), func() { input(&auth.Username, "Username") })
		ui.Field(c, locales.Password(), func() { password(&auth.Password, "Password") }).
			Description(locales.TheFirstSendGetsThe())
	case request.AuthNTLM:
		n := &auth.NTLM
		ui.Field(c, locales.Username(), func() { input(&n.Username, "Username") }).
			Description(locales.NtlmUsernameHint())
		ui.Field(c, locales.Password(), func() {
			password(&n.Password, "Password").Placeholder("{{vault:ntlm_password}}")
		})
		ui.Field(c, locales.Domain(), func() { input(&n.Domain, "Domain").Placeholder(locales.Optional()) })
		ui.Field(c, locales.Workstation(), func() { input(&n.Workstation, "Workstation").Placeholder(locales.OptionalWorkstationExample()) })
		ui.Field(c, locales.Retry(), func() {
			uikit.Checkbox(c, &n.DisableRetry, locales.DisableRetryingTheRequest())
		}).Description(locales.NtlmDisableRetryHint())
	case request.AuthAWS:
		aws := &auth.AWS
		ui.Field(c, locales.AccessKey(), func() { input(&aws.AccessKey, "Access key") })
		ui.Field(c, locales.SecretKey(), func() {
			password(&aws.SecretKey, "Secret key").Placeholder("{{vault:aws_secret}}")
		})
		ui.Field(c, locales.Region(), func() { input(&aws.Region, "Region").Placeholder("us-east-1").Width(260) })
		ui.Field(c, locales.Service(), func() {
			input(&aws.Service, "Service").Placeholder("e.g. execute-api or s3").Width(260)
		})
		ui.Field(c, locales.SessionToken(), func() {
			password(&aws.SessionToken, "Session token").Placeholder(locales.Optional())
		})
	case request.AuthJWT:
		jwt := &auth.JWT
		if jwt.Algorithm == "" {
			jwt.Algorithm = request.JWTAlgorithms[0]
		}
		addTo := "Header"
		if jwt.AddTo == "Query" {
			addTo = "Query"
		}
		ui.Field(c, locales.Algorithm(), func() { uikit.Select(c, &jwt.Algorithm, request.JWTAlgorithms).Width(uikit.WidthField) })
		if strings.HasPrefix(jwt.Algorithm, "HS") {
			ui.Field(c, locales.Secret(), func() {
				password(&jwt.Secret, "Secret").Placeholder("{{vault:jwt_secret}}")
			})
			ui.Field(c, "", func() { uikit.Checkbox(c, &jwt.SecretBase64, locales.SecretIsBase64()) })
		} else {
			ui.Field(c, locales.PrivateKey(), func() {
				sensitiveArea(&jwt.PrivateKey, "Private key").Height(72).Font(uikit.FontMono).Radius(uikit.RadiusControl).Placeholder("-----BEGIN PRIVATE KEY----- … or {{vault:jwt_key}}")
			})
		}
		ui.Field(c, locales.Payload(), func() {
			area(&jwt.Payload, "Payload").Height(72).Font(uikit.FontMono).Radius(uikit.RadiusControl).Placeholder("{\"sub\": \"{{user_id}}\"}")
		})
		ui.Field(c, locales.ExtraHeader(), func() {
			input(&jwt.Header, "Extra header").Placeholder(locales.OptionalEGKidKey()).Font(uikit.FontMono)
		})
		ui.Field(c, locales.AddTokenTo(), func() {
			if uikit.SelectLabels(c, &addTo, []string{"Header", "Query"}, optionLabel).Width(uikit.WidthField).Changed() {
				jwt.AddTo = ""
				if addTo == "Query" {
					jwt.AddTo = "Query"
				}
			}
		})
		if jwt.AddTo == "Query" {
			ui.Field(c, locales.QueryParameter(), func() { input(&jwt.QueryParam, "Query parameter").Placeholder("token").Width(uikit.WidthFieldWide) })
		} else {
			ui.Field(c, locales.HeaderPrefix(), func() { input(&jwt.Prefix, "Header prefix").Placeholder(locales.Bearer()).Width(150) })
		}
	case request.AuthHawk:
		h := &auth.Hawk
		if h.Algorithm == "" {
			h.Algorithm = request.HawkAlgorithms[0]
		}
		ui.Field(c, locales.AuthId(), func() { input(&h.AuthID, "Auth ID") })
		ui.Field(c, locales.AuthKey(), func() {
			password(&h.AuthKey, "Auth key").Placeholder("{{vault:hawk_key}}")
		})
		ui.Field(c, locales.Algorithm(), func() {
			uikit.Select(c, &h.Algorithm, request.HawkAlgorithms).Width(uikit.WidthField)
		})
		ui.Field(c, locales.PayloadHash(), func() {
			uikit.Checkbox(c, &h.IncludePayloadHash, locales.IncludePayloadHash())
		}).Description(locales.CoversContentTypeAndThe())
		ui.Field(c, locales.Timestamp(), func() { input(&h.Timestamp, "Timestamp").Placeholder(locales.Now()).Width(uikit.WidthFieldWide) })
		ui.Field(c, locales.Nonce(), func() { input(&h.Nonce, "Nonce").Placeholder(locales.Random()).Width(uikit.WidthFieldWide) })
		ui.Field(c, locales.Ext(), func() { input(&h.ExtraData, "ext").Placeholder(locales.ApplicationSpecificDataOptional()) })
		ui.Field(c, locales.App(), func() { input(&h.App, "App").Placeholder(locales.OzApplicationIdOptional()) })
		ui.Field(c, locales.Delegation(), func() { input(&h.Delegation, "Delegation").Placeholder(locales.OzDelegatedByIdOptional()) })
		if h.User != "" {
			ui.Field(c, locales.User(), func() { input(&h.User, "User") }).
				Description(locales.KeptForPostmanTheRequest())
		}
	case request.AuthASAP:
		s := &auth.ASAP
		if s.Algorithm == "" {
			s.Algorithm = request.ASAPAlgorithms[0]
		}
		ui.Field(c, locales.Algorithm(), func() { uikit.Select(c, &s.Algorithm, request.ASAPAlgorithms).Width(uikit.WidthField) })
		ui.Field(c, locales.Issuer(), func() { input(&s.Issuer, "Issuer") })
		ui.Field(c, locales.Audience(), func() { input(&s.Audience, "Audience") })
		ui.Field(c, locales.KeyId(), func() { input(&s.KeyID, "Key ID") })
		ui.Field(c, locales.PrivateKey(), func() {
			sensitiveArea(&s.PrivateKey, "Private key").Height(72).Font(uikit.FontMono).Radius(uikit.RadiusControl).Placeholder("-----BEGIN PRIVATE KEY----- … or {{vault:asap_key}}")
		})
		ui.Field(c, locales.Subject(), func() { input(&s.Subject, "Subject").Placeholder(locales.OptionalDefaultsToTheIssuer()) })
		ui.Field(c, locales.AdditionalClaims(), func() {
			area(&s.Claims, "Additional claims").Height(72).Font(uikit.FontMono).Radius(uikit.RadiusControl).Placeholder(`{"custom": "{{value}}"}`)
		})
		ui.Field(c, locales.Expiry(), func() {
			expiry := float64(cmp.Or(s.Expiry, 3600))
			numberText(asapExpiryText, expiry)
			uikit.NumberField(c, asapExpiryText, &expiry, 1, 86400, 1, "s", locales.Expiry(), "", func() {
				s.Expiry = int(expiry)
			})
			uikit.Meta(c, locales.AsapExpiryHint())
		})
	case request.AuthEdgeGrid:
		e := &auth.EdgeGrid
		ui.Field(c, locales.AccessToken(), func() { input(&e.AccessToken, "Access token") })
		ui.Field(c, locales.ClientToken(), func() { input(&e.ClientToken, "Client token") })
		ui.Field(c, locales.ClientSecret(), func() {
			password(&e.ClientSecret, "Client secret").Placeholder("{{vault:edgegrid_secret}}")
		})
		ui.Field(c, locales.BaseUrl(), func() {
			input(&e.BaseURL, "Base URL").Placeholder(locales.RequestHostByDefault())
		}).Description(locales.TheHostTheSignatureCovers())
		ui.Field(c, locales.Timestamp(), func() { input(&e.Timestamp, "Timestamp").Placeholder(locales.Now()).Width(uikit.WidthWide) })
		ui.Field(c, locales.Nonce(), func() { input(&e.Nonce, "Nonce").Placeholder(locales.RandomUuid()).Width(uikit.WidthWide) })
		ui.Field(c, locales.HeadersToSign(), func() {
			input(&e.HeadersToSign, "Headers to sign").Placeholder(locales.CommaSeparatedEGX())
		}).Description(locales.NamesAreSignedInThis())
	}
}

func (a *app) requestBodyEditor(c *ui.Context, d *requestDocument, t *ui.Theme) {
	if request.IsWebSocket(d.composeURL) {
		a.webSocketMessagesEditor(c, d, t)
		return
	}
	if d.bodyMode == "None" {
		ui.Column(c).Grow(1).FillWidth().Center().Children(func() {
			uikit.Hint(c, locales.ThisRequestDoesNotHaveABody())
		})
		return
	}
	// The body type and JSON formatting sit on the editor's tab row, which
	// already names the Body tab they belong to.
	if d.bodyMode == "Raw" {
		// Reading is a view of the same bytes: the editor is the default,
		// and the rest are what the capture's readers used to show.
		if d.bodyRead > 0 {
			a.draftBodyView(c, d)
			return
		}
		a.requestBodyText(c, d)
	} else if d.bodyMode == "GraphQL" {
		ui.Row(c).Children(func() {
			uikit.SubTabs(c, &d.graphqlTab, locales.Body(), locales.Query(), locales.Variables())
		})
		a.graphqlSchemaRow(c, d)
		ops, _ := d.graphqlOperations()
		subscription := false
		for _, op := range ops {
			subscription = subscription || op.Type == "subscription"
		}
		if subscription {
			transport := d.graphql.Transport
			label := "HTTP POST"
			if transport != "" {
				label = transport
			}
			ui.Row(c).Gap(t.Space(uikit.SpaceM)).AlignItems(ui.Center).Children(func() {
				uikit.Meta(c, locales.GraphqlTransport()).Grow(1)
				if uikit.Select(c, &label, []string{"HTTP POST", "graphql-ws", "graphql-transport-ws"}).Width(190).Changed() {
					if label == "HTTP POST" {
						d.graphql.Transport = ""
					} else {
						d.graphql.Transport = label
					}
				}
				if d.graphql.Transport != "" {
					wait := float64(cmp.Or(d.graphql.WaitMS, 5000))
					numberText(&d.graphqlWaitText, wait)
					uikit.NumberField(c, &d.graphqlWaitText, &wait, 100, 60000, 100, "ms", locales.WaitMilliseconds(), "", func() {
						d.graphql.WaitMS = int(wait)
					})
				}
			})
			if d.graphql.Transport != "" {
				uikit.Hint(c, locales.GraphqlSubscriptionHint())
			}
		}
		if d.graphqlTab == 1 {
			uikit.Hint(c, locales.JsonObjectVariablesSupportedBlank())
			authoredText(c.Key("graphql-variables"), &d.graphql.Variables, &d.varsEditor, &d.varsFor, locales.GraphqlVariables(), "json")
			a.wireVariableEditor(d.varsEditor, d.composeCollection)
			a.setVariableDiagnostics(d.varsEditor, d.graphql.Variables, d.composeCollection)
		} else {
			uikit.Hint(c, locales.QueryOrMutationVariablesSupported())
			a.authoredTextOutlined(c.Key("graphql-query"), &d.graphql.Query, &d.queryEditor, &d.queryFor,
				locales.GraphqlQuery(), "GraphQL", outlineTarget{key: "draft:graphql", kind: outlineGraphQL},
				d.unicodeHandle("graphql-query"))
			a.wireGraphQLQueryEditor(d)
			a.installUnicodeHover(d.queryEditor)
			a.setGraphQLDiagnostics(d)
			a.appendUnicodeDiagnostics(d.queryEditor, d.graphql.Query)
		}
	} else if d.bodyMode == "Binary" {
		uikit.Meta(c, locales.BinaryFileUpTo16())
		ui.Row(c).Gap(t.Space(uikit.SpaceM)).Wrap().AlignItems(ui.Center).Children(func() {
			a.variableInput(c, "body-file", &d.bodyFile, locales.BinaryBodyFile(), d.composeCollection).Placeholder(locales.LocalPathOrUploadPath()).Font(uikit.FontMono).Grow(1).MinWidth(uikit.WidthNarrow)
			if uikit.SecondaryButton(c, locales.ChooseFile()).Label(locales.ChooseBinaryBodyFile()).Clicked() && a.win != nil {
				win, before := a.win, d.bodyFile
				go guarded(func() {
					paths, err := mygo.Dialog.Open(mygo.OpenDialogOptions{Title: locales.ChooseBinaryBodyFile()})
					win.Update(func() {
						if err != nil {
							a.fail(locales.ChooseBinaryBodyFile(), err)
						} else if len(paths) > 0 && slices.Contains(a.documents, d) && d.bodyMode == "Binary" && d.bodyFile == before {
							d.bodyFile = paths[0]
						}
					})
				})
			}
		})
		uikit.Hint(c, locales.OnlyThePathIsSaved())
	} else {
		caption := locales.UrlEncoded()
		opts := fieldsOptions{file: d.bodyMode == "Multipart", fileRoom: true, description: true, collection: d.composeCollection,
			measure: &d.fieldTablePane, seen: &d.fieldTableWidth, caption: caption, key: "field", focus: d.unicodeHandle}
		if d.bodyMode == "Multipart" {
			opts.caption = locales.FormData()
			opts.headerEnd = func() {
				if uikit.ToolbarButton(c, locales.AttachFile(), locales.AddAFilePartOnly(), false).Children(func() {
					ui.Icon(c, fileInputIcon).Size(uikit.IconInline, uikit.IconInline)
					uikit.Body(c, locales.AttachFile())
				}).Clicked() && a.win != nil {
					go guarded(func() {
						paths, err := mygo.Dialog.Open(mygo.OpenDialogOptions{Title: locales.AttachRequestFile()})
						if err == nil && len(paths) > 0 {
							a.win.Update(func() { d.formFields = append(d.formFields, request.Field{Name: "file", Value: paths[0], File: true}) })
						}
					})
				}
			}
		}
		a.requestFieldsEditor(c, &d.formFields, opts, locales.Field())
	}
}

// webSocketMessagesEditor is the Body tab of a ws/wss request: the messages the
// saved request sends, in order, and the reply window after each one.
func (a *app) webSocketMessagesEditor(c *ui.Context, d *requestDocument, t *ui.Theme) {
	remove := -1
	ui.Column(c).Grow(1).MinHeight(0).Children(func() {
		ui.Row(c).Gap(t.Space(uikit.SpaceM)).Wrap().AlignItems(ui.Center).Children(func() {
			uikit.Meta(c, locales.WebSocketMessagesHelp()).Grow(1).MinWidth(uikit.WidthFieldWide)
			numberText(&d.wsWaitText, d.wsWait)
			uikit.NumberField(c, &d.wsWaitText, &d.wsWait, 0, 60000, 100, "ms", locales.WebSocketReplyWindowMs(), "", nil)
		})
		ui.Scroll(c).Grow(1).MinHeight(0).Gap(t.Space(uikit.SpaceM)).Children(func() {
			for i := range d.wsMessages {
				message := &d.wsMessages[i]
				row := ui.Row(c.Key(i)).AlignItems(ui.Start).Gap(t.Space(uikit.SpaceM))
				row.Children(func() {
					uikit.Caption(c, strconv.Itoa(i+1)).
						TextColor(t.TextMuted).Width(t.Space(uikit.SpaceL)).Padding(t.Space(uikit.SpaceM), 0)
					if message.Encoding == "" {
						message.Encoding = "Text"
					}
					uikit.SelectLabels(c, &message.Encoding, []string{"Text", "Hex", "Base64"}, optionLabel).
						Label(locales.WebsocketMessageEncoding()).Width(104)
					uikit.TextArea(c, &message.Payload).Label(locales.WebSocketMessageOf(i + 1)).
						Placeholder(locales.TextHexBytesOrPadded()).Height(54).Font(uikit.FontMono).
						Grow(1).MinWidth(0)
					label := locales.RemoveMessage(i + 1)
					if uikit.RowIconButton(c, label, label, closeIcon).Clicked() {
						remove = i
					}
				})
			}
			ui.Row(c).MinHeight(uikit.HeightRow).Children(func() {
				if uikit.ToolbarButton(c, locales.AddMessage(), locales.AddMessage(), false).Children(func() {
					ui.Icon(c, plusIcon).Size(uikit.IconInline, uikit.IconInline)
					uikit.Body(c, locales.AddMessage())
				}).Clicked() {
					d.wsMessages = append(d.wsMessages, request.WebSocketMessage{})
				}
			})
		})
	})
	if remove >= 0 {
		d.wsMessages = slices.Delete(d.wsMessages, remove, remove+1)
	}
}

// draftBodyFormats are the ways the Body view can show the draft's bytes: the
// editor first, then the read-only representations its content type and bytes
// offer. The list is stable while the body is, so the menu keeps its choice.
func (a *app) draftBodyFormats(d *requestDocument) []string {
	formats := []string{locales.EditBody(), locales.Pretty(), locales.Hex()}
	ct := draftContentType(d)
	switch {
	case strings.HasPrefix(ct, "image/"), strings.Contains(ct, "html"):
		formats = append(formats, locales.Preview())
	}
	if looksProto(d.composeBody, ct) {
		formats = append(formats, "Protobuf")
	}
	if format := inspect.BodyFormat(ct); format != "" {
		formats = append(formats, format)
	}
	return formats
}

// draftContentType is the Content-Type the draft would send.
func draftContentType(d *requestDocument) string {
	for _, row := range parseHeaderRows(d.composeHeaders) {
		if !row.Disabled && strings.EqualFold(row.Name, "Content-Type") {
			return strings.ToLower(strings.TrimSpace(row.Value))
		}
	}
	return ""
}

// looksProto reports whether a body is protobuf: its content type says so, or
// its bytes decode as one.
func looksProto(body, contentType string) bool {
	if grpcContentType(contentType) || strings.Contains(contentType, "protobuf") {
		return true
	}
	if body == "" || len(body) > 512<<10 {
		return false
	}
	_, err := decodeProto([]byte(body))
	return err == nil
}

// draftFlow is the draft as the readers take it: its bytes and headers, so a
// structured or protobuf view reads what Send would send.
func draftFlow(d *requestDocument) flow {
	h := make(http.Header)
	for _, row := range parseHeaderRows(d.composeHeaders) {
		if !row.Disabled && row.Name != "" {
			h.Add(row.Name, row.Value)
		}
	}
	f := flow{id: d.id, method: d.composeMethod, url: d.composeURL, kind: "http",
		reqBody: d.composeBody, reqHeader: h, reqSize: len(d.composeBody)}
	// A reader that infers from the target reads the draft's own address, as
	// a captured flow carries it: the gRPC method in the path names the
	// message type a protobuf body decodes as.
	if u, err := url.Parse(d.composeURL); err == nil && u.Host != "" {
		f.scheme, f.host, f.path = u.Scheme, u.Host, cmp.Or(u.Path, "/")
	}
	return f
}

// draftBodyView shows the draft's body in the chosen read-only form. Nothing
// here writes to the draft: the bytes stay the editor's.
func (a *app) draftBodyView(c *ui.Context, d *requestDocument) {
	formats := a.draftBodyFormats(d)
	view := min(max(d.bodyRead, 0), len(formats)-1)
	body, ct := d.composeBody, draftContentType(d)
	switch {
	case view <= 0:
		a.requestBodyText(c, d)
	case formats[view] == "Protobuf":
		if a.protobufReadElsewhere(c, 1) {
			return
		}
		a.protobufReader(c, draftFlow(d), 1)
	case formats[view] == inspect.BodyFormat(ct):
		a.structured[1].view(c, a, inspectionBody{text: body}, ct, !a.bodyNoWrap[1])
	case formats[view] == locales.Preview():
		a.previewBody(c, draftFlow(d), body, 1)
	case formats[view] == locales.Hex():
		a.hexBody(c, body, 1)
	default:
		if a.bodyPrettyFor[1] != body {
			a.bodyPrettyFor[1], a.bodyText[1] = body, nil
		}
		a.prettyBody(c, &a.jsonCrumbs[1], "draft", body, 1, a.jsonReaderFor(c, 1, "draft", body, ct))
	}
}

// requestScriptsEditor is the request's Scripts tab: a narrow list of the two
// scripts beside the chosen one's editor, with the snippet menu floating at
// the editor's bottom-right corner as Postman puts it.
func (a *app) requestScriptsEditor(c *ui.Context, d *requestDocument, t *ui.Theme) {
	p := uikit.Colors(t)
	ui.Row(c).Grow(1).FillWidth().FillHeight().MinHeight(0).Gap(0).AlignItems(ui.Start).Children(func() {
		ui.Column(c).Width(160).Shrink(0).MinHeight(0).Padding(0, t.Space(uikit.SpaceM)).Gap(t.Space(uikit.SpaceXS)).Children(func() {
			for i, item := range []struct {
				label   string
				content string
			}{
				{locales.PreRequest(), d.composePre},
				{locales.PostResponse(), d.composePost},
			} {
				row := uikit.RowButton(c, item.label).Height(uikit.HeightGridRow).FontSize(uikit.Scaled(uikit.TextSmall)).
					Padding(0, t.Space(uikit.SpaceM)).Justify(ui.Start).TextColor(t.TextMuted)
				switch {
				case d.scriptTab == i:
					row.Background(p.Accent).TextColor(t.Text)
				case row.Hovered():
					row.Background(p.MutedHover)
				}
				row.Children(func() {
					ui.Text(c, item.label).FontSize(uikit.Scaled(uikit.TextSmall)).SingleLine()
					if strings.TrimSpace(item.content) != "" {
						ui.Box(c).Size(uikit.DotSize, uikit.DotSize).Radius(uikit.DotSize / 2).
							Background(t.Success).Shrink(0).Label(locales.HasContent())
					}
				})
				if row.Clicked() {
					d.scriptTab = i
					c.Invalidate()
				}
			}
		})
		ui.Box(c).Width(uikit.HairlineWidth).FillHeight().Shrink(0).Background(t.Border)
		ui.Box(c).Grow(1).MinWidth(0).MinHeight(0).FillHeight().Children(func() {
			a.requestScriptText(c, d)
			st := d.preEditor
			if d.scriptTab == 1 {
				st = d.postEditor
			}
			if st != nil {
				ui.Box(c).Absolute().Right(t.Space(uikit.SpaceL)).Bottom(t.Space(uikit.SpaceL)).
					Background(t.Background).Border(uikit.HairlineWidth, t.Border).Radius(uikit.RadiusControl).
					Padding(0, t.Space(uikit.SpaceXS)).Children(func() {
					uikit.ToolbarSelect(c, locales.InsertSnippet(), scriptSnippetMenu(st, "request")).Label(locales.InsertSnippet())
				})
			}
		})
	})
}

// requestScriptText edits the pre-request or post-response script in the same
// reader as the body: line numbers, JavaScript highlighting, find and undo.
// The document's string stays the source of truth for running and saving.
func (a *app) requestScriptText(c *ui.Context, d *requestDocument) {
	if d.scriptTab == 1 {
		a.authoredTextOutlinedHint(c, &d.composePost, &d.postEditor, &d.postFor,
			locales.PostResponseScript(), "JavaScript", locales.PostResponseScriptPlaceholder(),
			outlineTarget{key: "draft:script:post", kind: outlineScript},
			d.unicodeHandle(problemFieldPost))
		a.installUnicodeHover(d.postEditor)
		a.wireVariableEditor(d.postEditor, d.composeCollection)
		a.bindQuickFixShortcut(c, d.unicodeHandle(problemFieldPost))
		a.trackEditorQuickFix(d, problemFieldPost, d.postEditor)
		a.quickFixHandleMenu(c, d.unicodeHandle(problemFieldPost), d.postEditor)
	} else {
		a.authoredTextOutlinedHint(c, &d.composePre, &d.preEditor, &d.preFor,
			locales.PreRequestScript(), "JavaScript", locales.PreRequestScriptPlaceholder(),
			outlineTarget{key: "draft:script:pre", kind: outlineScript},
			d.unicodeHandle(problemFieldPre))
		a.installUnicodeHover(d.preEditor)
		a.wireVariableEditor(d.preEditor, d.composeCollection)
		a.bindQuickFixShortcut(c, d.unicodeHandle(problemFieldPre))
		a.trackEditorQuickFix(d, problemFieldPre, d.preEditor)
		a.quickFixHandleMenu(c, d.unicodeHandle(problemFieldPre), d.preEditor)
	}
}

// authoredText edits a template in the body's reader; *text stays the source of
// truth, and seen is what the reader was last built from.
func authoredText(c *ui.Context, text *string, state **editor.State, seen *string, label, language string, focus ...*ui.Handle) {
	if *state == nil || *seen != *text {
		buf := editor.NewBuffer(*text)
		buf.SetLanguage(editor.LanguageNamed(language))
		*state = &editor.State{Buffer: buf, NoScrollPastEnd: true, SoftWrap: true, Label: label}
		*seen = *text
	}
	requestCodeEditor(c, *state, focus...)
	if current := (*state).Buffer.String(); current != *text {
		*text, *seen = current, current
		// Tools built above the editor must see the new text next frame.
		c.Invalidate()
	}
}

// authoredTextOutlined is authoredText for the three bodies with document
// symbols. The outline reads the same editor state, so editing the buffer is
// the only thing that recomputes its symbols.
func (a *app) authoredTextOutlined(c *ui.Context, text *string, state **editor.State, seen *string,
	label, language string, target outlineTarget, focus ...*ui.Handle) {
	a.authoredTextOutlinedHint(c, text, state, seen, label, language, "", target, focus...)
}

// authoredTextOutlinedHint is authoredTextOutlined with a placeholder for an
// empty document, as a script editor shows before the first line is typed.
func (a *app) authoredTextOutlinedHint(c *ui.Context, text *string, state **editor.State, seen *string,
	label, language, placeholder string, target outlineTarget, focus ...*ui.Handle) {
	if *state == nil || *seen != *text {
		buf := editor.NewBuffer(*text)
		buf.SetLanguage(editor.LanguageNamed(language))
		*state = &editor.State{Buffer: buf, NoScrollPastEnd: true, SoftWrap: true, Label: label, Placeholder: placeholder}
		*seen = *text
	}
	(*state).Placeholder = placeholder
	if len(focus) > 0 && focus[0] != nil && target.focus == nil {
		handle := focus[0]
		target.focus = func() { handle.Focus(c) }
	}
	a.outlineEditor(c, *state, target, func() { requestCodeEditor(c, *state, focus...) })
	if current := (*state).Buffer.String(); current != *text {
		*text, *seen = current, current
		c.Invalidate()
	}
}

// requestBodyText edits the raw body in the same reader the inspector uses:
// line numbers, JSON highlighting, find and undo. The document's string stays
// the source of truth for saving, sending and copying.
func (a *app) requestBodyText(c *ui.Context, d *requestDocument) {
	if d.bodyEditor == nil || d.bodyFor != d.composeBody {
		buf := editor.NewBuffer(d.composeBody)
		if looksJSON(d.composeBody) {
			buf.SetLanguage(editor.LanguageNamed("json"))
		}
		d.bodyEditor = &editor.State{Buffer: buf, NoScrollPastEnd: true, SoftWrap: true, Label: locales.RequestBody()}
		d.bodyFor = d.composeBody
	}
	// The request body keeps no path bar: its tool row and the text are all
	// that belongs above the editor. The path is still tracked and offered in
	// the body's menu, so Copy JSONPath knows the node under the caret.
	a.jsonPathTrack(&d.jsonCrumbs, "draft", d.bodyEditor, jsonCrumbSource{})
	a.bodyMenu(c, d.bodyEditor, &d.jsonCrumbs, "draft", jsonCrumbSource{})
	bodyFocus := d.unicodeHandle("body")
	a.outlineEditor(c, d.bodyEditor, outlineTarget{key: "draft", kind: outlineJSON,
		crumbs: &d.jsonCrumbs, focus: func() { bodyFocus.Focus(c) }}, func() {
		requestCodeEditor(c, d.bodyEditor, bodyFocus)
	})
	a.bindQuickFixShortcut(c, d.unicodeHandle("body"))
	a.trackEditorQuickFix(d, problemFieldBody, d.bodyEditor)
	a.quickFixHandleMenu(c, d.unicodeHandle("body"), d.bodyEditor)
	a.wireVariableEditor(d.bodyEditor, d.composeCollection)
	a.setVariableDiagnostics(d.bodyEditor, d.composeBody, d.composeCollection)
	if text := d.bodyEditor.Buffer.String(); text != d.composeBody {
		d.composeBody = text
		d.bodyFor = text
		d.problemBodyGen++
		if looksJSON(text) && d.bodyEditor.Buffer.Language() == nil {
			d.bodyEditor.Buffer.SetLanguage(editor.LanguageNamed("json"))
		}
	}
}

// Authored body, scripts, bulk headers and saved examples share one editor
// surface. The editor still owns editing, scrolling, find and accessibility.
func requestCodeEditor(c *ui.Context, state *editor.State, focus ...*ui.Handle) ui.Element {
	t := c.Theme()
	state.Font = ui.Font{Family: uikit.FontMono, Size: uikit.TextBody}
	state.Theme = uikit.EditorTheme(t)
	// The editor sits on the panel itself: an input is a well or a field on
	// chrome, and this is neither, so it draws no box of its own.
	return ui.Box(c).Grow(1).MinHeight(0).FillWidth().Children(func() {
		view := editor.View(c, state).Grow(1).FillWidth().FocusRing(true)
		if len(focus) > 0 && focus[0] != nil {
			view.Bind(focus[0])
		}
	})
}

// looksJSON is the cheap test that decides the body editor's highlighting and
// whether formatting can apply; the formatter itself validates the bytes.
func looksJSON(body string) bool {
	trimmed := strings.TrimSpace(body)
	return strings.HasPrefix(trimmed, "{") || strings.HasPrefix(trimmed, "[")
}
