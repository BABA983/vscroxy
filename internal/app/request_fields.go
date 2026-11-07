package app

import (
	"cmp"
	"fmt"
	"github.com/baba983/vscroxy/internal/uikit"
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
	uikit.Meta(c, "Edits update the URL; repeated keys are preserved.")
	if d.queryError != "" {
		ui.Text(c, d.queryError).FontSize(uikit.TextMeta).TextColor(t.Danger)
	}
	if a.requestFieldsEditor(c, &d.queryRows, fieldsOptions{collection: d.composeCollection}, "parameter") {
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
	file       bool
	secret     bool
	collection string
}

// Fields are rows rather than a map: duplicate names, disabled entries and file
// parts all survive saving. Only Send/export reads a selected file.
func (a *app) requestFieldsEditor(c *ui.Context, rows *[]request.Field, opts fieldsOptions, noun string, actions ...func()) bool {
	t := c.Theme()
	changed, remove := false, -1
	ui.Column(c).Grow(1).MinHeight(0).Border(1, t.Border).Radius(uikit.RadiusControl).Children(func() {
		// Column titles stay above the rows as they scroll, so every cell
		// reads as a column rather than its own form.
		ui.Row(c).Height(uikit.HeightControl).AlignItems(ui.Center).BorderWidth(0, 0, 1, 0).BorderColor(t.Border).Children(func() {
			ui.Box(c).Width(uikit.HeightLarge).FillHeight().BorderWidth(0, 1, 0, 0).BorderColor(t.Border)
			uikit.Label(c, "Key").Basis(0).Grow(1).MinWidth(0).FillHeight().Padding(t.Space(1), t.Space(2)).BorderWidth(0, 1, 0, 0).BorderColor(t.Border)
			uikit.Label(c, "Value").Basis(0).Grow(2).MinWidth(0).Padding(t.Space(1), t.Space(2))
			if opts.secret {
				ui.Box(c).Width(uikit.HeightControl)
			}
			if opts.file {
				uikit.Label(c, "File").Width(64)
			}
			ui.Box(c).Width(uikit.HeightLarge)
		})
		ui.Scroll(c).Grow(1).MinHeight(0).Children(func() {
			for i := range *rows {
				r := &(*rows)[i]
				row := ui.Row(c).Key(i).Height(uikit.HeightControl).AlignItems(ui.Center).BorderWidth(0, 0, 1, 0).BorderColor(t.Border)
				row.Children(func() {
					ui.Box(c).Width(uikit.HeightLarge).FillHeight().Center().BorderWidth(0, 1, 0, 0).BorderColor(t.Border).Children(func() {
						enabled := !r.Disabled
						if ui.Checkbox(c, &enabled, "").Label(fmt.Sprintf("Enable %s %d", noun, i+1)).Tooltip("Unchecked rows stay here and are not sent").Changed() {
							r.Disabled, changed = !enabled, true
						}
					})
					ui.Box(c).Basis(0).Grow(1).MinWidth(0).FillHeight().BorderWidth(0, 1, 0, 0).BorderColor(t.Border).Children(func() {
						field := a.variableGridInput(c, fmt.Sprintf("%s-key-%d", noun, i+1), &r.Name, "Key", fmt.Sprintf("%s key %d", noun, i+1), opts.collection, false)
						cellBorder(t, field, false, false)
						if field.Changed() {
							changed = true
						}
					})
					// Masked until the reader reveals this row with its eye: the
					// keyboard alone never shows a credential, so tabbing
					// through a table cannot put one on a shared screen.
					secret := opts.secret && sensitiveHeader(r.Name)
					masked := secret && !r.Revealed
					ui.Box(c).Basis(0).Grow(2).MinWidth(0).FillHeight().Children(func() {
						value := a.variableGridInput(c, fmt.Sprintf("%s-value-%d", noun, i+1), &r.Value, "Value", fmt.Sprintf("%s value %d", noun, i+1), opts.collection, masked)
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
					})
					if opts.secret && sensitiveHeader(r.Name) {
						label := fmt.Sprintf("Reveal %s %d", noun, i+1)
						if r.Revealed {
							label = fmt.Sprintf("Hide %s %d", noun, i+1)
						}
						reveal := uikit.IconButton(c, label, "Credentials are masked by default; reveal this row to read it in plain text", eyeIcon)
						if r.Revealed {
							reveal.TextColor(t.Accent)
						}
						if reveal.Clicked() {
							r.Revealed = !r.Revealed
						}
					} else if opts.secret {
						// Every header keeps the same action column, even when
						// its value does not need an explicit reveal control.
						ui.Box(c).Width(uikit.HeightControl)
					}
					if opts.file {
						ui.Box(c).Width(64).Children(func() {
							if ui.Checkbox(c, &r.File, "File").Label(fmt.Sprintf("File part %d", i+1)).Changed() {
								changed = true
							}
						})
					}
					ui.Box(c).Width(uikit.HeightLarge).Center().Children(func() {
						label := fmt.Sprintf("Remove %s %d", noun, i+1)
						// The action column ends at the grid's edge, so keep
						// its focus ring inside the button instead of clipping
						// it against the table border.
						button := uikit.IconButton(c, label, label, closeIcon).FocusRingOffset(-2)
						// Keep the control mounted, named and in the Tab order even
						// while quiet. Any focus in the row reveals its action.
						if !row.Hovered() && !row.FocusWithin() {
							button.Opacity(0)
						}
						if button.Clicked() {
							remove = i
						}
					})
				})
			}
			// The spacer carries the row to the full width, so the action stays
			// at the table's left edge rather than in the middle.
			ui.Row(c).MinHeight(uikit.HeightControl).Gap(t.Space(2)).Children(func() {
				if uikit.ToolbarButton(c, "Add "+noun, "Add "+noun, false).Children(func() {
					ui.Icon(c, plusIcon).Size(uikit.IconInline, uikit.IconInline)
					uikit.Body(c, "Add "+noun)
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

// Base inputs keep the grid's cells borderless at rest without losing native
// text editing, password handling, accessibility or keyboard focus rings.
func requestGridInput(c *ui.Context, value *string, placeholder, label string) ui.Element {
	return ui.TextInputBase(c, value).Label(label).Placeholder(placeholder).FillWidth().MinWidth(0).
		FillHeight().Padding(0, c.Theme().Space(2)).Font(uikit.FontMono).FontSize(uikit.TextBody).
		Border(1, ui.Color{})
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
		field.Border(1, t.Border).Background(t.SurfaceHover)
	default:
		field.Border(1, ui.Color{})
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

func (a *app) headerModeButton(c *ui.Context, d *requestDocument, t *ui.Theme) {
	label := "Bulk edit"
	if d.headerBulk {
		label = "Table"
	}
	uikit.ToolbarButton(c, "Toggle header editing mode", "Bulk edit replaces the table with one Key: Value per line; switched-off rows are kept", d.headerBulk).Children(func() {
		ui.Icon(c, formatIcon).Size(uikit.IconInline, uikit.IconInline)
		uikit.Body(c, label)
	}).OnClick(func() {
		if d.headerBulk || !hasSecretHeader(d.headerRows) {
			toggleHeaderMode(d)
		} else {
			// The block shows every value: ask before putting credentials
			// on the screen in plain text.
			d.headerWarn = true
		}
	})
}

// headerEditor edits headers as rows, so order, duplicates, empty values and
// disabled rows all survive. Bulk edit shows the Key: Value block for pasting.
func (a *app) headerEditor(c *ui.Context, d *requestDocument, t *ui.Theme) {
	if d.composeHeaders != d.headerFor {
		// The block changed elsewhere: an import, a script or the bulk editor.
		d.headerRows, d.headerFor = parseHeaderRows(d.composeHeaders), d.composeHeaders
	}
	if d.headerBulk {
		// The block reads in the same editor as the body: line numbers,
		// find and undo, one Key: Value per line.
		if d.bulkEditor == nil || d.bulkFor != d.composeHeaders {
			d.bulkEditor = &editor.State{Buffer: editor.NewBuffer(d.composeHeaders), NoScrollPastEnd: true, SoftWrap: true, Label: "Request headers"}
			d.bulkFor = d.composeHeaders
		}
		requestCodeEditor(c, d.bulkEditor)
		if text := d.bulkEditor.Buffer.String(); text != d.composeHeaders {
			d.composeHeaders, d.bulkFor = text, text
		}
		return
	}
	if len(d.headerRows) == 0 {
		d.headerRows = []request.Field{{}}
	}
	if a.requestFieldsEditor(c, &d.headerRows, fieldsOptions{secret: true, collection: d.composeCollection}, "header") {
		d.composeHeaders = headerRowsText(d.headerRows)
		d.headerFor = d.composeHeaders
	}
}

func (a *app) authEditor(c *ui.Context, d *requestDocument, t *ui.Theme) {
	ui.Scroll(c).Grow(1).Gap(t.Space(3)).Padding(0, t.Space(1)).Children(func() {
		a.authFields(c, &d.auth, append([]string{request.AuthInherit}, authTypes...), d.composeCollection)
		if d.auth.Type == request.AuthInherit {
			resolved, from := a.inheritance(d.composeCollection).InheritAuth(d.auth)
			text := "No collection or folder sets auth, so none is sent."
			if from != "" {
				text = "Uses " + resolved.Type + " auth from " + from + "."
			}
			ui.Row(c).Gap(t.Space(2)).AlignItems(ui.Center).Children(func() {
				uikit.Meta(c, text)
				if path := cmp.Or(from, d.composeCollection); path != "" && uikit.SecondaryButton(c, settingsLabel(path)).Clicked() {
					a.openFolder(path)
				}
			})
		}
	})
}

// authTypes are the auth a request or folder sets itself.
var authTypes = []string{"None", "Bearer", "Basic", "API key", request.AuthOAuth2, request.AuthOAuth1,
	request.AuthDigest, request.AuthAWS, request.AuthJWT, request.AuthHawk, request.AuthEdgeGrid}

// authFields edits one Auth, a request's or a folder's; collection is where
// it is used, for the variables an OAuth 2.0 token request expands.
func (a *app) authFields(c *ui.Context, auth *request.Auth, types []string, collection string) {
	t := c.Theme()
	if auth.Type != request.AuthInherit {
		uikit.Meta(c, "Prefer {{variables}} or {{vault:name}} for secrets; these fields are saved in plaintext.")
	}
	input := func(value *string, label string) ui.Element {
		return a.variableInput(c, "auth-"+label, value, label, collection)
	}
	area := func(value *string, label string) ui.Element {
		return a.variableTextArea(c, "auth-"+label, value, label, collection)
	}
	password := func(value *string, label string) ui.Element {
		return a.variablePasswordInput(c, "auth-"+label, value, label, collection)
	}
	sensitiveArea := func(value *string, label string) ui.Element {
		return a.variableSensitiveTextArea(c, "auth-"+label, value, label, collection)
	}
	ui.Form(c, func() {
		ui.Field(c, "Type", func() {
			uikit.Select(c, &auth.Type, types).Label("Authorization type").Width(240)
		})
		switch auth.Type {
		case "Bearer":
			ui.Field(c, "Bearer token", func() {
				password(&auth.Token, "Bearer token").Placeholder("{{token}}")
			})
		case "Basic":
			ui.Field(c, "Username", func() { input(&auth.Username, "Username") })
			ui.Field(c, "Password", func() { password(&auth.Password, "Password") })
		case "API key":
			ui.Field(c, "API key name", func() { input(&auth.Key, "API key name") })
			ui.Field(c, "API key value", func() { password(&auth.Value, "API key value") })
			ui.Field(c, "Location", func() {
				uikit.Select(c, &auth.Location, []string{"Header", "Query"}).Label("API key location").Width(160)
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
			ui.Field(c, "Consumer key", func() { input(&o.ConsumerKey, "Consumer key") })
			ui.Field(c, "Consumer secret", func() {
				password(&o.ConsumerSecret, "Consumer secret").Placeholder("{{vault:oauth1_secret}}")
			})
			ui.Field(c, "Token", func() { input(&o.Token, "Token").Placeholder("Optional for two-legged auth") })
			ui.Field(c, "Token secret", func() { password(&o.TokenSecret, "Token secret") })
			ui.Field(c, "Signature method", func() {
				uikit.Select(c, &o.SignatureMethod, request.OAuth1Methods).Width(180)
			})
			if strings.HasPrefix(o.SignatureMethod, "RSA-") {
				ui.Field(c, "Private key", func() {
					sensitiveArea(&o.PrivateKey, "Private key").Height(72).Font(uikit.FontMono).FontSize(uikit.TextBody).Radius(uikit.RadiusControl).Placeholder("-----BEGIN PRIVATE KEY----- … or {{vault:oauth1_key}}")
				})
			}
			ui.Field(c, "Add authorization to", func() {
				if uikit.Select(c, &addTo, []string{"Header", "Query", "Body", "Automatic"}).Width(180).Changed() {
					o.AddTo = addTo
					if addTo == "Header" {
						o.AddTo = ""
					} else if addTo == "Automatic" {
						o.AddTo = "Auto"
					}
				}
			}).Description("Automatic uses a form body for POST or PUT, the query otherwise.")
			ui.Field(c, "Body hash", func() {
				ui.Checkbox(c, &o.IncludeBodyHash, "Hash a non-form body")
			}).Description("Adds oauth_body_hash; form bodies are signed as parameters.")
			ui.Field(c, "Empty parameters", func() {
				ui.Checkbox(c, &o.AddEmptyParamsToSign, "Include in the signature")
			})
			ui.Field(c, "Timestamp", func() { input(&o.Timestamp, "Timestamp").Placeholder("Now").Width(200) })
			ui.Field(c, "Nonce", func() { input(&o.Nonce, "Nonce").Placeholder("Random").Width(200) })
			ui.Field(c, "Version", func() { input(&o.Version, "Version").Placeholder("1.0").Width(120) })
			ui.Field(c, "Realm", func() { input(&o.Realm, "Realm").Placeholder("Optional") })
			ui.Field(c, "Callback URL", func() { input(&o.Callback, "Callback URL").Placeholder("Optional, oauth_callback") })
			ui.Field(c, "Verifier", func() { input(&o.Verifier, "Verifier").Placeholder("Optional, oauth_verifier") })
		case request.AuthDigest:
			ui.Field(c, "Username", func() { input(&auth.Username, "Username") })
			ui.Field(c, "Password", func() { password(&auth.Password, "Password") }).
				Description("The first send gets the server's challenge; vscroxy answers it and sends again.")
		case request.AuthAWS:
			aws := &auth.AWS
			ui.Field(c, "Access key", func() { input(&aws.AccessKey, "Access key") })
			ui.Field(c, "Secret key", func() {
				password(&aws.SecretKey, "Secret key").Placeholder("{{vault:aws_secret}}")
			})
			ui.Field(c, "Region", func() { input(&aws.Region, "Region").Placeholder("us-east-1").Width(260) })
			ui.Field(c, "Service", func() {
				input(&aws.Service, "Service").Placeholder("e.g. execute-api or s3").Width(260)
			})
			ui.Field(c, "Session token", func() {
				password(&aws.SessionToken, "Session token").Placeholder("Optional")
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
			ui.Field(c, "Algorithm", func() { uikit.Select(c, &jwt.Algorithm, request.JWTAlgorithms).Width(160) })
			if strings.HasPrefix(jwt.Algorithm, "HS") {
				ui.Field(c, "Secret", func() {
					password(&jwt.Secret, "Secret").Placeholder("{{vault:jwt_secret}}")
				})
				ui.Field(c, "", func() { ui.Checkbox(c, &jwt.SecretBase64, "Secret is base64-encoded") })
			} else {
				ui.Field(c, "Private key", func() {
					sensitiveArea(&jwt.PrivateKey, "Private key").Height(72).Font(uikit.FontMono).FontSize(uikit.TextBody).Radius(uikit.RadiusControl).Placeholder("-----BEGIN PRIVATE KEY----- … or {{vault:jwt_key}}")
				})
			}
			ui.Field(c, "Payload", func() {
				area(&jwt.Payload, "Payload").Height(72).Font(uikit.FontMono).FontSize(uikit.TextBody).Radius(uikit.RadiusControl).Placeholder(`{"sub": "{{user_id}}"}`)
			})
			ui.Field(c, "Extra header", func() {
				input(&jwt.Header, "Extra header").Placeholder(`Optional, e.g. {"kid": "key-1"}`).Font(uikit.FontMono)
			})
			ui.Field(c, "Add token to", func() {
				if uikit.Select(c, &addTo, []string{"Header", "Query"}).Width(160).Changed() {
					jwt.AddTo = ""
					if addTo == "Query" {
						jwt.AddTo = "Query"
					}
				}
			})
			if jwt.AddTo == "Query" {
				ui.Field(c, "Query parameter", func() { input(&jwt.QueryParam, "Query parameter").Placeholder("token").Width(200) })
			} else {
				ui.Field(c, "Header prefix", func() { input(&jwt.Prefix, "Header prefix").Placeholder("Bearer").Width(150) })
			}
		case request.AuthHawk:
			h := &auth.Hawk
			if h.Algorithm == "" {
				h.Algorithm = request.HawkAlgorithms[0]
			}
			ui.Field(c, "Auth ID", func() { input(&h.AuthID, "Auth ID") })
			ui.Field(c, "Auth key", func() {
				password(&h.AuthKey, "Auth key").Placeholder("{{vault:hawk_key}}")
			})
			ui.Field(c, "Algorithm", func() {
				uikit.Select(c, &h.Algorithm, request.HawkAlgorithms).Width(160)
			})
			ui.Field(c, "Payload hash", func() {
				ui.Checkbox(c, &h.IncludePayloadHash, "Include payload hash")
			}).Description("Covers Content-Type and the body in the MAC.")
			ui.Field(c, "Timestamp", func() { input(&h.Timestamp, "Timestamp").Placeholder("Now").Width(200) })
			ui.Field(c, "Nonce", func() { input(&h.Nonce, "Nonce").Placeholder("Random").Width(200) })
			ui.Field(c, "ext", func() { input(&h.ExtraData, "ext").Placeholder("Application-specific data, optional") })
			ui.Field(c, "App", func() { input(&h.App, "App").Placeholder("Oz application ID, optional") })
			ui.Field(c, "Delegation", func() { input(&h.Delegation, "Delegation").Placeholder("Oz delegated-by ID, optional") })
			if h.User != "" {
				ui.Field(c, "User", func() { input(&h.User, "User") }).
					Description("Kept for Postman; the request MAC does not cover it.")
			}
		case request.AuthEdgeGrid:
			e := &auth.EdgeGrid
			ui.Field(c, "Access token", func() { input(&e.AccessToken, "Access token") })
			ui.Field(c, "Client token", func() { input(&e.ClientToken, "Client token") })
			ui.Field(c, "Client secret", func() {
				password(&e.ClientSecret, "Client secret").Placeholder("{{vault:edgegrid_secret}}")
			})
			ui.Field(c, "Base URL", func() {
				input(&e.BaseURL, "Base URL").Placeholder("Request host by default")
			}).Description("The host the signature covers; a scheme and path are ignored.")
			ui.Field(c, "Timestamp", func() { input(&e.Timestamp, "Timestamp").Placeholder("Now").Width(240) })
			ui.Field(c, "Nonce", func() { input(&e.Nonce, "Nonce").Placeholder("Random UUID").Width(240) })
			ui.Field(c, "Headers to sign", func() {
				input(&e.HeadersToSign, "Headers to sign").Placeholder("Comma-separated, e.g. X-Test1,X-Test2")
			}).Description("Names are signed in this order, with whitespace collapsed.")
		}
	}).Gap(t.Space(3))
}

func (a *app) requestBodyEditor(c *ui.Context, d *requestDocument, t *ui.Theme) {
	// The body type and JSON formatting sit on the editor's tab row, which
	// already names the Body tab they belong to.
	if d.bodyMode == "Raw" {
		a.requestBodyText(c, d)
	} else if d.bodyMode == "GraphQL" {
		ui.Row(c).Children(func() {
			uikit.Segmented(c, &d.graphqlTab, "Query", "Variables")
		})
		if d.graphqlTab == 1 {
			uikit.Hint(c, "JSON object · {{variables}} supported · blank sends {}")
			authoredText(c.Key("graphql-variables"), &d.graphql.Variables, &d.varsEditor, &d.varsFor, "GraphQL variables", "json")
			a.wireVariableEditor(d.varsEditor, d.composeCollection)
			a.setVariableDiagnostics(d.varsEditor, d.graphql.Variables, d.composeCollection)
		} else {
			uikit.Hint(c, "Query or mutation · {{variables}} supported · sent as JSON")
			authoredText(c.Key("graphql-query"), &d.graphql.Query, &d.queryEditor, &d.queryFor, "GraphQL query", "GraphQL")
			a.wireVariableEditor(d.queryEditor, d.composeCollection)
			a.setVariableDiagnostics(d.queryEditor, d.graphql.Query, d.composeCollection)
		}
	} else if d.bodyMode == "Binary" {
		uikit.Meta(c, "BINARY FILE · up to 16 MiB, sent unchanged")
		ui.Row(c).Gap(t.Space(2)).Wrap().AlignItems(ui.Center).Children(func() {
			a.variableInput(c, "body-file", &d.bodyFile, "Binary body file", d.composeCollection).Placeholder("Local path or {{upload_path}}").Font(uikit.FontMono).Grow(1).MinWidth(120)
			if uikit.SecondaryButton(c, "Choose file…").Label("Choose binary body file").Clicked() && a.win != nil {
				win, before := a.win, d.bodyFile
				go func() {
					paths, err := mygo.Dialog.Open(mygo.OpenDialogOptions{Title: "Choose binary body file"})
					win.Update(func() {
						if err != nil {
							a.fail("Choose binary body file", err)
						} else if len(paths) > 0 && slices.Contains(a.documents, d) && d.bodyMode == "Binary" && d.bodyFile == before {
							d.bodyFile = paths[0]
						}
					})
				}()
			}
		})
		uikit.Hint(c, "Only the path is saved. Content-Type defaults to application/octet-stream.")
	} else {
		uikit.Meta(c, "FORM DATA · file values are local paths, 16 MiB in total")
		a.requestFieldsEditor(c, &d.formFields, fieldsOptions{file: true, collection: d.composeCollection}, "field", func() {
			if uikit.ToolbarButton(c, "Attach file…", "Add a file part; only the local path is stored", false).Children(func() {
				ui.Icon(c, fileInputIcon).Size(uikit.IconInline, uikit.IconInline)
				uikit.Body(c, "Attach file…")
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
		authoredText(c, &d.composePost, &d.postEditor, &d.postFor, "Post-response script", "JavaScript")
	} else {
		authoredText(c, &d.composePre, &d.preEditor, &d.preFor, "Pre-request script", "JavaScript")
	}
}

// authoredText edits a template in the body's reader; *text stays the source of
// truth, and seen is what the reader was last built from.
func authoredText(c *ui.Context, text *string, state **editor.State, seen *string, label, language string) {
	if *state == nil || *seen != *text {
		buf := editor.NewBuffer(*text)
		buf.SetLanguage(editor.LanguageNamed(language))
		*state = &editor.State{Buffer: buf, NoScrollPastEnd: true, SoftWrap: true, Label: label}
		*seen = *text
	}
	requestCodeEditor(c, *state)
	if current := (*state).Buffer.String(); current != *text {
		*text, *seen = current, current
		// Tools built above the editor must see the new text next frame.
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
		d.bodyEditor = &editor.State{Buffer: buf, NoScrollPastEnd: true, SoftWrap: true, Label: "Request body"}
		d.bodyFor = d.composeBody
	}
	requestCodeEditor(c, d.bodyEditor)
	a.wireVariableEditor(d.bodyEditor, d.composeCollection)
	a.setVariableDiagnostics(d.bodyEditor, d.composeBody, d.composeCollection)
	if text := d.bodyEditor.Buffer.String(); text != d.composeBody {
		d.composeBody = text
		d.bodyFor = text
		if looksJSON(text) && d.bodyEditor.Buffer.Language() == nil {
			d.bodyEditor.Buffer.SetLanguage(editor.LanguageNamed("json"))
		}
	}
}

// Authored body, scripts, bulk headers and saved examples share one editor
// surface. The editor still owns editing, scrolling, find and accessibility.
func requestCodeEditor(c *ui.Context, state *editor.State) ui.Element {
	t := c.Theme()
	state.Font = ui.Font{Family: uikit.FontMono, Size: uikit.TextBody}
	state.Theme = uikit.EditorTheme(t)
	return ui.Box(c).Grow(1).MinHeight(0).FillWidth().Border(1, t.Border).Radius(uikit.RadiusControl).Children(func() {
		editor.View(c, state).Grow(1).FillWidth().FocusRing(true)
	})
}

// looksJSON is the cheap test that decides the body editor's highlighting and
// whether formatting can apply; the formatter itself validates the bytes.
func looksJSON(body string) bool {
	trimmed := strings.TrimSpace(body)
	return strings.HasPrefix(trimmed, "{") || strings.HasPrefix(trimmed, "[")
}
