package app

import (
	"cmp"
	"fmt"
	"net/url"
	"slices"
	"strings"

	"github.com/baba983/vscroxy/internal/request"
	"github.com/egoist/mygo"
	"github.com/egoist/mygo/ui"
	"github.com/egoist/mygo/ui/editor"
)

func (a *app) paramsEditor(c *ui.Context, d *requestDocument, t *ui.Theme) {
	if d.querySource != d.composeURL {
		d.querySource, d.queryRows, d.queryError = d.composeURL, nil, ""
		u, err := url.Parse(d.composeURL)
		if err == nil {
			for part := range strings.SplitSeq(u.RawQuery, "&") {
				if part == "" {
					continue
				}
				key, value, _ := strings.Cut(part, "=")
				key, err = url.QueryUnescape(key)
				if err != nil {
					break
				}
				value, err = url.QueryUnescape(value)
				if err != nil {
					break
				}
				d.queryRows = append(d.queryRows, request.Field{Name: key, Value: value})
			}
		}
		if err != nil {
			d.queryError = err.Error()
		}
	}
	ui.Text(c, "QUERY PARAMETERS · edits update the URL; repeated keys are preserved").FontSize(10).TextColor(t.TextMuted)
	if d.queryError != "" {
		ui.Text(c, d.queryError).FontSize(11).TextColor(t.Danger)
	}
	if requestFieldsEditor(c, &d.queryRows, fieldsOptions{}, "parameter") {
		u, err := url.Parse(d.composeURL)
		if err == nil {
			var parts []string
			for _, row := range d.queryRows {
				if row.Disabled || row.Name == "" && row.Value == "" {
					continue
				}
				parts = append(parts, url.QueryEscape(row.Name)+"="+url.QueryEscape(row.Value))
			}
			u.RawQuery, u.ForceQuery = strings.Join(parts, "&"), false
			d.composeURL = u.String()
			d.querySource = d.composeURL
		}
	}
}

// fieldsOptions says what a row table edits: file parts carry a File flag,
// and header values are masked while their key is a credential.
type fieldsOptions struct {
	file   bool
	secret bool
}

// Fields are rows rather than a map: duplicate names, disabled entries and file
// parts all survive saving. Only Send/export reads a selected file.
func requestFieldsEditor(c *ui.Context, rows *[]request.Field, opts fieldsOptions, noun string, actions ...func()) bool {
	t := c.Theme()
	changed, remove := false, -1
	ui.Column(c).Grow(1).Children(func() {
		// Column titles stay above the rows as they scroll, so every cell
		// reads as a column rather than its own form.
		ui.Row(c).Gap(6).Padding(0, 8, 2, 0).Children(func() {
			ui.Box(c).Width(16)
			ui.Text(c, "KEY").Grow(1).FontSize(10).Bold().TextColor(t.TextMuted)
			ui.Text(c, "VALUE").Grow(2).FontSize(10).Bold().TextColor(t.TextMuted)
			if opts.secret {
				ui.Box(c).Width(24)
			}
			if opts.file {
				ui.Text(c, "FILE").Width(46).FontSize(10).Bold().TextColor(t.TextMuted)
			}
			ui.Box(c).Width(t.Space(7))
		})
		ui.Scroll(c).Grow(1).Gap(4).Padding(0, 8, 0, 0).Children(func() {
			for i := range *rows {
				r := &(*rows)[i]
				ui.Row(c).Key(i).Gap(6).AlignItems(ui.Center).Children(func() {
					enabled := !r.Disabled
					if ui.Checkbox(c, &enabled, "").Label(fmt.Sprintf("Enable %s %d", noun, i+1)).Tooltip("Unchecked rows stay here and are not sent").Changed() {
						r.Disabled, changed = !enabled, true
					}
					if field := ui.TextInput(c, &r.Name).Grow(1).Placeholder("Key").Label(fmt.Sprintf("%s key %d", noun, i+1)); field.Changed() {
						changed = true
					}
					value := ui.TextInput(c, &r.Value).Grow(2).Placeholder("Value").Label(fmt.Sprintf("%s value %d", noun, i+1))
					// Masked until the reader reveals this row with its eye: the
					// keyboard alone never shows a credential, so tabbing
					// through a table cannot put one on a shared screen.
					secret := opts.secret && sensitiveHeader(r.Name)
					masked := secret && !r.Revealed
					if masked {
						value.Password()
					}
					cellBorder(t, value, masked, secret && r.Revealed)
					if !masked && len(r.Value) > 40 {
						// A long value reads in full on hover, without the click
						// that would move the keyboard into it.
						value.Tooltip(r.Value)
					}
					if value.Changed() {
						changed = true
					}
					if opts.secret && sensitiveHeader(r.Name) {
						label := fmt.Sprintf("Reveal %s %d", noun, i+1)
						if r.Revealed {
							label = fmt.Sprintf("Hide %s %d", noun, i+1)
						}
						reveal := ui.ButtonBase(c).Label(label).Size(24, 24).Radius(4).AlignItems(ui.Center).
							TextColor(t.TextMuted).Tooltip("Credentials are masked by default; reveal this row to read it in plain text")
						if r.Revealed {
							reveal.TextColor(t.Accent)
						}
						if reveal.Children(func() { ui.Icon(c, eyeIcon).Size(13, 13) }).Clicked() {
							r.Revealed = !r.Revealed
						}
					} else if opts.secret {
						// Every header keeps the same action column, even when
						// its value does not need an explicit reveal control.
						ui.Box(c).Width(24)
					}
					if opts.file {
						if ui.Checkbox(c, &r.File, "File").Label(fmt.Sprintf("File part %d", i+1)).Changed() {
							changed = true
						}
					}
					if ui.Button(c, "×").Label(fmt.Sprintf("Remove %s %d", noun, i+1)).Clicked() {
						remove = i
					}
				})
			}
			// The spacer carries the row to the full width, so the action stays
			// at the table's left edge rather than in the middle.
			ui.Row(c).Gap(6).Children(func() {
				if ui.ButtonBase(c).Label("Add "+noun).Height(26).Padding(0, 6).Gap(6).Radius(4).AlignItems(ui.Center).
					TextColor(t.TextMuted).Children(func() {
					ui.Icon(c, plusIcon).Size(12, 12)
					ui.Text(c, "Add "+noun).FontSize(12)
				}).Clicked() {
					*rows = append(*rows, request.Field{})
					changed = true
				}
				for _, action := range actions {
					action()
				}
				ui.Spacer(c)
			})
		})
	})
	if remove >= 0 {
		*rows = slices.Delete(*rows, remove, remove+1)
		changed = true
	}
	return changed
}

// cellBorder keeps a cell quiet until the pointer or the keyboard is on it, as
// a spreadsheet does. A revealed credential is the one cell that says so.
func cellBorder(t *ui.Theme, field ui.Element, masked, revealed bool) {
	switch {
	case revealed:
		field.Border(1, t.Accent.Alpha(0.6)).Background(t.Accent.Alpha(0.06))
	case field.Focused():
		field.Border(1, t.Accent)
	case field.Hovered():
		field.Border(1, t.Border)
	default:
		field.Border(1, t.Border.Alpha(0.4))
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
	ui.Row(c).Gap(8).AlignItems(ui.Center).Children(func() {
		ui.Text(c, "HEADERS · unchecked rows are not sent").FontSize(10).TextColor(t.TextMuted).Grow(1)
		label := "Bulk edit"
		if d.headerBulk {
			label = "Table"
		}
		if ui.ButtonBase(c).Label("Toggle header editing mode").Height(22).Padding(0, 8).Gap(5).Radius(4).AlignItems(ui.Center).
			Border(1, t.Border).TextColor(t.TextMuted).Tooltip("Bulk edit replaces the table with one Key: Value per line; switched-off rows are kept").Children(func() {
			ui.Icon(c, formatIcon).Size(11, 11)
			ui.Text(c, label).FontSize(11)
		}).Clicked() {
			if d.headerBulk || !hasSecretHeader(d.headerRows) {
				toggleHeaderMode(d)
			} else {
				// The block shows every value: ask before putting credentials
				// on the screen in plain text.
				d.headerWarn = true
			}
		}
	})
	if d.headerBulk {
		// The block reads in the same editor as the body: line numbers,
		// find and undo, one Key: Value per line.
		if d.bulkEditor == nil || d.bulkFor != d.composeHeaders {
			d.bulkEditor = &editor.State{Buffer: editor.NewBuffer(d.composeHeaders), NoScrollPastEnd: true, SoftWrap: true, Label: "Request headers"}
			d.bulkFor = d.composeHeaders
		}
		editor.View(c, d.bulkEditor).Grow(1).FillWidth()
		if text := d.bulkEditor.Buffer.String(); text != d.composeHeaders {
			d.composeHeaders, d.bulkFor = text, text
		}
		return
	}
	if len(d.headerRows) == 0 {
		d.headerRows = []request.Field{{}}
	}
	if requestFieldsEditor(c, &d.headerRows, fieldsOptions{secret: true}, "header") {
		d.composeHeaders = headerRowsText(d.headerRows)
		d.headerFor = d.composeHeaders
	}
}

func (a *app) authEditor(c *ui.Context, d *requestDocument, t *ui.Theme) {
	ui.Scroll(c).Grow(1).Gap(8).Children(func() {
		a.authFields(c, &d.auth, append([]string{request.AuthInherit}, authTypes...), d.composeCollection)
		if d.auth.Type == request.AuthInherit {
			resolved, from := a.inheritance(d.composeCollection).InheritAuth(d.auth)
			text := "No collection or folder sets auth, so none is sent."
			if from != "" {
				text = "Uses " + resolved.Type + " auth from " + from + "."
			}
			ui.Row(c).Gap(8).AlignItems(ui.Center).Children(func() {
				ui.Text(c, text).FontSize(12).TextColor(t.TextMuted)
				if path := cmp.Or(from, d.composeCollection); path != "" && ui.Button(c, settingsLabel(path)).FontSize(11).Clicked() {
					a.openFolder(path)
				}
			})
		}
	})
}

// authTypes are the auth a request or folder sets itself.
var authTypes = []string{"None", "Bearer", "Basic", "API key", request.AuthOAuth2, request.AuthDigest, request.AuthAWS, request.AuthJWT}

// authFields edits one Auth, a request's or a folder's; collection is where
// it is used, for the variables an OAuth 2.0 token request expands.
func (a *app) authFields(c *ui.Context, auth *request.Auth, types []string, collection string) {
	t := c.Theme()
	ui.Select(c, &auth.Type, types).Label("Authorization type").Width(180)
	if auth.Type != request.AuthInherit {
		ui.Text(c, "Prefer {{variables}} or {{vault:name}} for secrets; these fields are saved in plaintext.").FontSize(11).TextColor(t.TextMuted)
	}
	switch auth.Type {
	case "Bearer":
		ui.TextInput(c, &auth.Token).Label("Bearer token").Placeholder("{{token}}").Password()
	case "Basic":
		ui.TextInput(c, &auth.Username).Label("Username")
		ui.TextInput(c, &auth.Password).Label("Password").Password()
	case "API key":
		ui.Row(c).Gap(8).Children(func() {
			ui.TextInput(c, &auth.Key).Label("API key name").Grow(1)
			ui.TextInput(c, &auth.Value).Label("API key value").Password().Grow(2)
			ui.Select(c, &auth.Location, []string{"Header", "Query"}).Label("API key location").Width(120)
		})
	case request.AuthOAuth2:
		a.oauthFields(c, auth, collection)
	case request.AuthDigest:
		ui.Form(c, func() {
			ui.Field(c, "Username", func() { ui.TextInput(c, &auth.Username) })
			ui.Field(c, "Password", func() { ui.TextInput(c, &auth.Password).Password() }).
				Description("The first send gets the server's challenge; vscroxy answers it and sends again.")
		})
	case request.AuthAWS:
		aws := &auth.AWS
		ui.Form(c, func() {
			ui.Field(c, "Access key", func() { ui.TextInput(c, &aws.AccessKey) })
			ui.Field(c, "Secret key", func() { ui.TextInput(c, &aws.SecretKey).Placeholder("{{vault:aws_secret}}").Password() })
			ui.Field(c, "Region", func() { ui.TextInput(c, &aws.Region).Placeholder("us-east-1").Width(260) })
			ui.Field(c, "Service", func() { ui.TextInput(c, &aws.Service).Placeholder("e.g. execute-api or s3").Width(260) })
			ui.Field(c, "Session token", func() { ui.TextInput(c, &aws.SessionToken).Placeholder("Optional").Password() })
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
		ui.Form(c, func() {
			ui.Field(c, "Algorithm", func() { ui.Select(c, &jwt.Algorithm, request.JWTAlgorithms).Width(150) })
			if strings.HasPrefix(jwt.Algorithm, "HS") {
				ui.Field(c, "Secret", func() { ui.TextInput(c, &jwt.Secret).Placeholder("{{vault:jwt_secret}}").Password() })
				ui.Field(c, "", func() { ui.Checkbox(c, &jwt.SecretBase64, "Secret is base64-encoded") })
			} else {
				ui.Field(c, "Private key", func() {
					ui.TextArea(c, &jwt.PrivateKey).Height(72).Font("monospace").FontSize(11).Placeholder("-----BEGIN PRIVATE KEY----- … or {{vault:jwt_key}}")
				})
			}
			ui.Field(c, "Payload", func() {
				ui.TextArea(c, &jwt.Payload).Height(72).Font("monospace").FontSize(11).Placeholder(`{"sub": "{{user_id}}"}`)
			})
			ui.Field(c, "Extra header", func() { ui.TextInput(c, &jwt.Header).Placeholder(`Optional, e.g. {"kid": "key-1"}`).Font("monospace") })
			ui.Field(c, "Add token to", func() {
				if ui.Select(c, &addTo, []string{"Header", "Query"}).Width(150).Changed() {
					jwt.AddTo = ""
					if addTo == "Query" {
						jwt.AddTo = "Query"
					}
				}
			})
			if jwt.AddTo == "Query" {
				ui.Field(c, "Query parameter", func() { ui.TextInput(c, &jwt.QueryParam).Placeholder("token").Width(200) })
			} else {
				ui.Field(c, "Header prefix", func() { ui.TextInput(c, &jwt.Prefix).Placeholder("Bearer").Width(150) })
			}
		})
	}
}

func (a *app) requestBodyEditor(c *ui.Context, d *requestDocument, t *ui.Theme) {
	// The body type and JSON formatting sit on the editor's tab row, which
	// already names the Body tab they belong to.
	if d.bodyMode == "Raw" {
		a.requestBodyText(c, d)
	} else {
		ui.Text(c, "FORM DATA · file values are local paths, 16 MiB in total").FontSize(10).TextColor(t.TextMuted)
		requestFieldsEditor(c, &d.formFields, fieldsOptions{file: true}, "field", func() {
			if ui.ButtonBase(c).Label("Attach file…").Height(26).Padding(0, 6).Gap(6).Radius(4).AlignItems(ui.Center).
				TextColor(t.TextMuted).Tooltip("Add a file part; only the local path is stored").Children(func() {
				ui.Icon(c, fileInputIcon).Size(12, 12)
				ui.Text(c, "Attach file…").FontSize(12)
			}).Clicked() && a.win != nil {
				go func() {
					paths, err := mygo.Dialog.Open(mygo.OpenDialogOptions{Title: "Attach request file"})
					if err == nil && len(paths) > 0 {
						a.win.Update(func() { d.formFields = append(d.formFields, request.Field{Name: "file", Value: paths[0], File: true}) })
					}
				}()
			}
		})
	}
}

// requestScriptText edits the pre-request or post-response script in the same
// reader as the body: line numbers, JavaScript highlighting, find and undo.
// The document's string stays the source of truth for running and saving.
func (a *app) requestScriptText(c *ui.Context, d *requestDocument) {
	if d.scriptTab == 1 {
		scriptText(c, &d.composePost, &d.postEditor, &d.postFor, "Post-response script")
	} else {
		scriptText(c, &d.composePre, &d.preEditor, &d.preFor, "Pre-request script")
	}
}

// scriptText edits a script in the body's reader; *text stays the source of
// truth, and seen is what the reader was last built from.
func scriptText(c *ui.Context, text *string, state **editor.State, seen *string, label string) {
	if *state == nil || *seen != *text {
		buf := editor.NewBuffer(*text)
		buf.SetLanguage(editor.LanguageNamed("JavaScript"))
		*state = &editor.State{Buffer: buf, NoScrollPastEnd: true, SoftWrap: true, Label: label}
		*seen = *text
	}
	editor.View(c, *state).Grow(1).FillWidth()
	if current := (*state).Buffer.String(); current != *text {
		*text, *seen = current, current
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
		d.bodyEditor = &editor.State{Buffer: buf, NoScrollPastEnd: true, SoftWrap: true, Label: "Request body"}
		d.bodyFor = d.composeBody
	}
	d.bodyEditor.Theme = bodyEditorTheme(c.Theme())
	editor.View(c, d.bodyEditor).Grow(1)
	if text := d.bodyEditor.Buffer.String(); text != d.composeBody {
		d.composeBody = text
		d.bodyFor = text
		if looksJSON(text) && d.bodyEditor.Buffer.Language() == nil {
			d.bodyEditor.Buffer.SetLanguage(editor.LanguageNamed("json"))
		}
	}
}

// looksJSON is the cheap test that decides the body editor's highlighting and
// whether formatting can apply; the formatter itself validates the bytes.
func looksJSON(body string) bool {
	trimmed := strings.TrimSpace(body)
	return strings.HasPrefix(trimmed, "{") || strings.HasPrefix(trimmed, "[")
}
