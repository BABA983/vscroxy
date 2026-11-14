package app

import (
	"cmp"
	"fmt"
	"github.com/baba983/vscroxy/internal/uikit"
	"net/http"
	"net/url"
	"slices"
	"strings"

	"github.com/baba983/vscroxy/internal/inspect"
	"github.com/baba983/vscroxy/internal/request"
	"github.com/baba983/vscroxy/locales"
	"github.com/egoist/mygo"
	"github.com/egoist/mygo/ui"
	"github.com/egoist/mygo/ui/editor"
)

// pathEditor edits the values of the URL's :name path segments. The names come
// from the URL, so there are no rows to add or remove; a blank value is sent as
// {{name}}.
func (a *app) pathEditor(c *ui.Context, d *requestDocument, t *ui.Theme) {
	d.pathRows = pathRowsFor(d.composeURL, d.pathRows)
	if len(d.pathRows) == 0 {
		return
	}
	noun := locales.Parameter()
	ui.Column(c).Gap(t.Space(2)).Children(func() {
		uikit.Label(c, locales.PathParameters())
		for i := range d.pathRows {
			r := &d.pathRows[i]
			ui.Row(c.Key(fmt.Sprintf("path-row-%d", i))).Height(uikit.HeightControl).Gap(t.Space(2)).AlignItems(ui.Center).Children(func() {
				ui.Text(c, ":"+r.Name).Font(uikit.FontMono).FontSize(uikit.Scaled(uikit.TextBody)).Width(t.Space(27)).SingleLine()
				ui.Box(c).Basis(0).Grow(1).MinWidth(0).FillHeight().Children(func() {
					a.variableGridInput(c, fmt.Sprintf("path-value-%d", i), &r.Value, locales.Value(), locales.ValueOfRow(noun, i+1), d.composeCollection, false)
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
	uikit.Meta(c, locales.EditsUpdateTheUrlRepeated())
	if d.queryError != "" {
		ui.Text(c, d.queryError).FontSize(uikit.Scaled(uikit.TextMeta)).TextColor(t.Danger)
	}
	if a.requestFieldsEditor(c, &d.queryRows, fieldsOptions{collection: d.composeCollection}, locales.Parameter()) {
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
	// filters are the captured values a row's context menu offers, by name.
	filters map[string][]string
}

// Fields are rows rather than a map: duplicate names, disabled entries and file
// parts all survive saving. Only Send/export reads a selected file.
func (a *app) requestFieldsEditor(c *ui.Context, rows *[]request.Field, opts fieldsOptions, noun string, actions ...func()) bool {
	t := c.Theme()
	changed, remove := false, -1
	ui.Column(c).Grow(1).MinHeight(0).Children(func() {
		// Column titles stay above the rows as they scroll, so every cell
		// reads as a column rather than its own form.
		ui.Row(c).Height(uikit.HeightControl).AlignItems(ui.Center).Children(func() {
			ui.Box(c).Width(uikit.HeightLarge).FillHeight()
			// The cell's padding stays on the label inside it. On the flex
			// item itself, padding joins its base size and would make the
			// header columns wider than the rows' cells.
			// The key column keeps room for a common header name and the
			// value column for a common media type: neither is squeezed to a
			// cut-off word, and a longer value reads in full on hover.
			ui.Box(c).Basis(0).Grow(1).MinWidth(t.Space(27)).FillHeight().Children(func() {
				uikit.Label(c, locales.Key()).FillWidth().Padding(t.Space(1), t.Space(2))
			})
			ui.Box(c).Basis(0).Grow(2).MinWidth(t.Space(30)).FillHeight().Children(func() {
				uikit.Label(c, locales.Value()).FillWidth().Padding(t.Space(1), t.Space(2))
			})
			if opts.secret {
				ui.Box(c).Width(uikit.HeightControl)
			}
			if opts.file {
				uikit.Label(c, locales.File()).Width(64)
			}
			ui.Box(c).Width(uikit.HeightLarge)
		})
		ui.Scroll(c).Grow(1).MinHeight(0).Children(func() {
			for i := range *rows {
				r := &(*rows)[i]
				row := ui.Row(c.Key(i)).Height(uikit.HeightControl).AlignItems(ui.Center)
				row.Children(func() {
					ui.Box(c).Width(uikit.HeightLarge).FillHeight().Center().Children(func() {
						enabled := !r.Disabled
						if uikit.Tip(c, ui.Checkbox(c, &enabled, "").Label(locales.EnableRow(noun, i+1)), locales.UncheckedRowsStayHereAnd()).Changed() {
							r.Disabled, changed = !enabled, true
						}
					})
					ui.Box(c).Basis(0).Grow(1).MinWidth(t.Space(27)).FillHeight().Children(func() {
						field := a.variableGridInput(c, fmt.Sprintf("%s-key-%d", noun, i+1), &r.Name, locales.Key(), locales.KeyOfRow(noun, i+1), opts.collection, false)
						cellBorder(t, field, false, false)
						if values, ok := opts.filters[r.Name]; ok {
							// The capture's own values are what a filter can
							// search for, as the reader offers them.
							field.PopoverMenu(a.headerFilterMenu(c, r.Name, values))
						}
						if field.Changed() {
							changed = true
						}
					})
					// Masked until the reader reveals this row with its eye: the
					// keyboard alone never shows a credential, so tabbing
					// through a table cannot put one on a shared screen.
					secret := opts.secret && sensitiveHeader(r.Name)
					masked := secret && !r.Revealed
					ui.Box(c).Basis(0).Grow(2).MinWidth(t.Space(30)).FillHeight().Children(func() {
						value := a.variableGridInput(c, fmt.Sprintf("%s-value-%d", noun, i+1), &r.Value, locales.Value(), locales.ValueOfRow(noun, i+1), opts.collection, masked)
						if masked {
							value.Password()
						}
						cellBorder(t, value, masked, secret && r.Revealed)
						if !masked && len(r.Value) > 20 {
							// A long value reads in full on hover, without the click
							// that would move the keyboard into it.
							uikit.Tip(c, value, r.Value)
						}
						if value.Changed() {
							changed = true
						}
					})
					if opts.secret && sensitiveHeader(r.Name) {
						label := locales.RevealRow(noun, i+1)
						if r.Revealed {
							label = locales.HideRow(noun, i+1)
						}
						reveal := uikit.IconButton(c, label, locales.CredentialsAreMaskedByDefault(), eyeIcon)
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
							if ui.Checkbox(c, &r.File, "File").Label(locales.FilePartLabel(i + 1)).Changed() {
								changed = true
							}
						})
					}
					ui.Box(c).Width(uikit.HeightLarge).Center().Children(func() {
						label := locales.RemoveRow(noun, i+1)
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
				// The row sits on the scroll's bottom edge; keep the focus
				// ring inside it instead of clipping it against the border.
				if uikit.ToolbarButton(c, locales.AddNoun(noun), locales.AddNoun(noun), false).FocusRingOffset(-2).Children(func() {
					ui.Icon(c, plusIcon).Size(uikit.IconInline, uikit.IconInline)
					uikit.Body(c, locales.AddNoun(noun))
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
		FillHeight().Padding(0, c.Theme().Space(2)).Font(uikit.FontMono).FontSize(uikit.Scaled(uikit.TextBody)).
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
			d.bulkEditor = &editor.State{Buffer: editor.NewBuffer(d.composeHeaders), NoScrollPastEnd: true, SoftWrap: true, Label: locales.RequestHeaders()}
			d.bulkFor = d.composeHeaders
		}
		requestCodeEditor(c, d.bulkEditor)
		if text := d.bulkEditor.Buffer.String(); text != d.composeHeaders {
			d.composeHeaders, d.bulkFor = text, text
		}
		return
	}
	if a.requestFieldsEditor(c, &d.headerRows, fieldsOptions{secret: true, collection: d.composeCollection, filters: a.capturedHeaders()}, locales.NounHeader()) {
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
	request.AuthDigest, request.AuthNTLM, request.AuthAWS, request.AuthJWT, request.AuthHawk, request.AuthEdgeGrid}

// authFields edits one Auth, a request's or a folder's; collection is where
// it is used, for the variables an OAuth 2.0 token request expands.
func (a *app) authFields(c *ui.Context, auth *request.Auth, types []string, collection string) {
	t := c.Theme()
	if auth.Type != request.AuthInherit {
		uikit.Meta(c, locales.PreferVariablesOrVaultName())
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
		ui.Field(c, locales.TypeText(), func() {
			uikit.Select(c, &auth.Type, types).Label(locales.AuthorizationType()).Width(240)
		})
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
				uikit.SelectLabels(c, &auth.Location, []string{"Header", "Query"}, optionLabel).Label(locales.ApiKeyLocation()).Width(160)
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
					sensitiveArea(&o.PrivateKey, "Private key").Height(72).Font(uikit.FontMono).FontSize(uikit.Scaled(uikit.TextBody)).Radius(uikit.RadiusControl).Placeholder("-----BEGIN PRIVATE KEY----- … or {{vault:oauth1_key}}")
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
				ui.Checkbox(c, &o.IncludeBodyHash, "Hash a non-form body")
			}).Description(locales.AddsOauthBodyHashForm())
			ui.Field(c, locales.EmptyParameters(), func() {
				ui.Checkbox(c, &o.AddEmptyParamsToSign, "Include in the signature")
			})
			ui.Field(c, locales.Timestamp(), func() { input(&o.Timestamp, "Timestamp").Placeholder(locales.Now()).Width(200) })
			ui.Field(c, locales.Nonce(), func() { input(&o.Nonce, "Nonce").Placeholder(locales.Random()).Width(200) })
			ui.Field(c, locales.Version(), func() { input(&o.Version, "Version").Placeholder("1.0").Width(120) })
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
				ui.Checkbox(c, &n.DisableRetry, locales.DisableRetryingTheRequest())
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
			ui.Field(c, locales.Algorithm(), func() { uikit.Select(c, &jwt.Algorithm, request.JWTAlgorithms).Width(160) })
			if strings.HasPrefix(jwt.Algorithm, "HS") {
				ui.Field(c, locales.Secret(), func() {
					password(&jwt.Secret, "Secret").Placeholder("{{vault:jwt_secret}}")
				})
				ui.Field(c, "", func() { ui.Checkbox(c, &jwt.SecretBase64, "Secret is base64-encoded") })
			} else {
				ui.Field(c, locales.PrivateKey(), func() {
					sensitiveArea(&jwt.PrivateKey, "Private key").Height(72).Font(uikit.FontMono).FontSize(uikit.Scaled(uikit.TextBody)).Radius(uikit.RadiusControl).Placeholder("-----BEGIN PRIVATE KEY----- … or {{vault:jwt_key}}")
				})
			}
			ui.Field(c, locales.Payload(), func() {
				area(&jwt.Payload, "Payload").Height(72).Font(uikit.FontMono).FontSize(uikit.Scaled(uikit.TextBody)).Radius(uikit.RadiusControl).Placeholder("{\"sub\": \"{{user_id}}\"}")
			})
			ui.Field(c, locales.ExtraHeader(), func() {
				input(&jwt.Header, "Extra header").Placeholder(locales.OptionalEGKidKey()).Font(uikit.FontMono)
			})
			ui.Field(c, locales.AddTokenTo(), func() {
				if uikit.SelectLabels(c, &addTo, []string{"Header", "Query"}, optionLabel).Width(160).Changed() {
					jwt.AddTo = ""
					if addTo == "Query" {
						jwt.AddTo = "Query"
					}
				}
			})
			if jwt.AddTo == "Query" {
				ui.Field(c, locales.QueryParameter(), func() { input(&jwt.QueryParam, "Query parameter").Placeholder("token").Width(200) })
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
				uikit.Select(c, &h.Algorithm, request.HawkAlgorithms).Width(160)
			})
			ui.Field(c, locales.PayloadHash(), func() {
				ui.Checkbox(c, &h.IncludePayloadHash, "Include payload hash")
			}).Description(locales.CoversContentTypeAndThe())
			ui.Field(c, locales.Timestamp(), func() { input(&h.Timestamp, "Timestamp").Placeholder(locales.Now()).Width(200) })
			ui.Field(c, locales.Nonce(), func() { input(&h.Nonce, "Nonce").Placeholder(locales.Random()).Width(200) })
			ui.Field(c, locales.Ext(), func() { input(&h.ExtraData, "ext").Placeholder(locales.ApplicationSpecificDataOptional()) })
			ui.Field(c, locales.App(), func() { input(&h.App, "App").Placeholder(locales.OzApplicationIdOptional()) })
			ui.Field(c, locales.Delegation(), func() { input(&h.Delegation, "Delegation").Placeholder(locales.OzDelegatedByIdOptional()) })
			if h.User != "" {
				ui.Field(c, locales.User(), func() { input(&h.User, "User") }).
					Description(locales.KeptForPostmanTheRequest())
			}
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
			ui.Field(c, locales.Timestamp(), func() { input(&e.Timestamp, "Timestamp").Placeholder(locales.Now()).Width(240) })
			ui.Field(c, locales.Nonce(), func() { input(&e.Nonce, "Nonce").Placeholder(locales.RandomUuid()).Width(240) })
			ui.Field(c, locales.HeadersToSign(), func() {
				input(&e.HeadersToSign, "Headers to sign").Placeholder(locales.CommaSeparatedEGX())
			}).Description(locales.NamesAreSignedInThis())
		}
	}).Gap(t.Space(3))
}

func (a *app) requestBodyEditor(c *ui.Context, d *requestDocument, t *ui.Theme) {
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
			uikit.Segmented(c, &d.graphqlTab, locales.Query(), locales.Variables())
		})
		a.graphqlSchemaRow(c, d)
		if d.graphqlTab == 1 {
			uikit.Hint(c, locales.JsonObjectVariablesSupportedBlank())
			authoredText(c.Key("graphql-variables"), &d.graphql.Variables, &d.varsEditor, &d.varsFor, "GraphQL variables", "json")
			a.wireVariableEditor(d.varsEditor, d.composeCollection)
			a.setVariableDiagnostics(d.varsEditor, d.graphql.Variables, d.composeCollection)
		} else {
			uikit.Hint(c, locales.QueryOrMutationVariablesSupported())
			authoredText(c.Key("graphql-query"), &d.graphql.Query, &d.queryEditor, &d.queryFor, "GraphQL query", "GraphQL")
			a.wireGraphQLQueryEditor(d)
			a.setGraphQLDiagnostics(d)
		}
	} else if d.bodyMode == "Binary" {
		uikit.Meta(c, locales.BinaryFileUpTo16())
		ui.Row(c).Gap(t.Space(2)).Wrap().AlignItems(ui.Center).Children(func() {
			a.variableInput(c, "body-file", &d.bodyFile, "Binary body file", d.composeCollection).Placeholder(locales.LocalPathOrUploadPath()).Font(uikit.FontMono).Grow(1).MinWidth(120)
			if uikit.SecondaryButton(c, locales.ChooseFile()).Label(locales.ChooseBinaryBodyFile()).Clicked() && a.win != nil {
				win, before := a.win, d.bodyFile
				go func() {
					paths, err := mygo.Dialog.Open(mygo.OpenDialogOptions{Title: locales.ChooseBinaryBodyFile()})
					win.Update(func() {
						if err != nil {
							a.fail(locales.ChooseBinaryBodyFile(), err)
						} else if len(paths) > 0 && slices.Contains(a.documents, d) && d.bodyMode == "Binary" && d.bodyFile == before {
							d.bodyFile = paths[0]
						}
					})
				}()
			}
		})
		uikit.Hint(c, locales.OnlyThePathIsSaved())
	} else {
		uikit.Meta(c, locales.FormDataFileValuesAre())
		a.requestFieldsEditor(c, &d.formFields, fieldsOptions{file: true, collection: d.composeCollection}, locales.Field(), func() {
			if uikit.ToolbarButton(c, locales.AttachFile(), locales.AddAFilePartOnly(), false).Children(func() {
				ui.Icon(c, fileInputIcon).Size(uikit.IconInline, uikit.IconInline)
				uikit.Body(c, locales.AttachFile())
			}).Clicked() && a.win != nil {
				go func() {
					paths, err := mygo.Dialog.Open(mygo.OpenDialogOptions{Title: locales.AttachRequestFile()})
					if err == nil && len(paths) > 0 {
						a.win.Update(func() { d.formFields = append(d.formFields, request.Field{Name: "file", Value: paths[0], File: true}) })
					}
				}()
			}
		})
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
		a.protobufReader(c, draftFlow(d), 1)
	case formats[view] == inspect.BodyFormat(ct):
		a.structured[1].view(c, inspectionBody{text: body}, ct)
	case formats[view] == locales.Preview():
		a.previewBody(c, draftFlow(d), body, 1)
	case formats[view] == locales.Hex():
		a.hexBody(c, body, 1)
	default:
		if a.bodyPrettyFor[1] != body {
			a.bodyPrettyFor[1], a.bodyText[1] = body, nil
		}
		a.prettyBody(c, body, 1)
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
		d.bodyEditor = &editor.State{Buffer: buf, NoScrollPastEnd: true, SoftWrap: true, Label: locales.RequestBody()}
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
	// The editor sits on the panel itself: an input is a well or a field on
	// chrome, and this is neither, so it draws no box of its own.
	return ui.Box(c).Grow(1).MinHeight(0).FillWidth().Children(func() {
		editor.View(c, state).Grow(1).FillWidth().FocusRing(true)
	})
}

// looksJSON is the cheap test that decides the body editor's highlighting and
// whether formatting can apply; the formatter itself validates the bytes.
func looksJSON(body string) bool {
	trimmed := strings.TrimSpace(body)
	return strings.HasPrefix(trimmed, "{") || strings.HasPrefix(trimmed, "[")
}
