package main

import (
	"bytes"
	"cmp"
	"encoding/base64"
	"fmt"
	"io"
	"mime/multipart"
	"net/url"
	"os"
	"path/filepath"
	"slices"
	"strings"

	"github.com/egoist/mygo"
	"github.com/egoist/mygo/ui"
)

type requestAuth struct {
	Type     string `json:"type,omitempty"`
	Token    string `json:"token,omitempty"`
	Username string `json:"username,omitempty"`
	Password string `json:"password,omitempty"`
	Key      string `json:"key,omitempty"`
	Value    string `json:"value,omitempty"`
	Location string `json:"location,omitempty"`
}

type requestField struct {
	Name     string `json:"name"`
	Value    string `json:"value"`
	File     bool   `json:"file,omitzero"`
	Disabled bool   `json:"disabled,omitzero"`
}

// prepareRequest is shared by Send, Runner and cURL export. Scripts run after
// encoding and may deliberately override the generated auth/header/body.
func prepareRequest(r savedRequest, vars map[string]string) (*jsRequest, error) {
	req := &jsRequest{
		Method: strings.ToUpper(strings.TrimSpace(cmp.Or(expandVars(r.Method, vars), "GET"))),
		URL:    strings.TrimSpace(expandVars(r.URL, vars)), Body: expandVars(r.Body, vars),
		header: parseHeaderBlock(expandVars(r.Headers, vars)),
	}
	if req.URL == "" {
		return nil, fmt.Errorf("the URL is empty")
	}
	switch r.Auth.Type {
	case "", "None":
	case "Bearer":
		req.Set("Authorization", "Bearer "+expandVars(r.Auth.Token, vars))
	case "Basic":
		value := expandVars(r.Auth.Username, vars) + ":" + expandVars(r.Auth.Password, vars)
		req.Set("Authorization", "Basic "+base64.StdEncoding.EncodeToString([]byte(value)))
	case "API key":
		key, value := expandVars(r.Auth.Key, vars), expandVars(r.Auth.Value, vars)
		if key == "" {
			return nil, fmt.Errorf("API key name is empty")
		}
		if r.Auth.Location == "Query" {
			u, err := url.Parse(req.URL)
			if err != nil {
				return nil, err
			}
			q, err := url.ParseQuery(u.RawQuery)
			if err != nil {
				return nil, err
			}
			q.Set(key, value)
			u.RawQuery = q.Encode()
			req.URL = u.String()
		} else {
			req.Set(key, value)
		}
	default:
		return nil, fmt.Errorf("unsupported auth type %q", r.Auth.Type)
	}
	switch r.BodyMode {
	case "", "Raw":
	case "URL encoded", "Multipart":
		var body bytes.Buffer
		writer := multipart.NewWriter(&body)
		values := url.Values{}
		for _, field := range r.Form {
			if field.Disabled || field.Name == "" && field.Value == "" {
				continue
			}
			name, value := expandVars(field.Name, vars), expandVars(field.Value, vars)
			if r.BodyMode == "URL encoded" {
				if field.File {
					return nil, fmt.Errorf("URL encoded bodies cannot contain files")
				}
				values.Add(name, value)
			} else if field.File {
				info, err := os.Stat(value)
				if err != nil || !info.Mode().IsRegular() || info.Size() > 16<<20 {
					return nil, fmt.Errorf("form files must be regular files up to 16 MiB")
				}
				file, err := os.Open(value)
				if err != nil {
					return nil, fmt.Errorf("open form file: %w", err)
				}
				part, err := writer.CreateFormFile(name, filepath.Base(value))
				if err == nil {
					_, err = io.Copy(part, io.LimitReader(file, (16<<20)+1))
				}
				file.Close()
				if err != nil {
					return nil, err
				}
			} else if err := writer.WriteField(name, value); err != nil {
				return nil, err
			}
			if body.Len() > 16<<20 {
				return nil, fmt.Errorf("form body exceeds 16 MiB")
			}
		}
		if r.BodyMode == "URL encoded" {
			req.Body = values.Encode()
			req.Set("Content-Type", "application/x-www-form-urlencoded")
		} else {
			if err := writer.Close(); err != nil {
				return nil, err
			}
			req.Body = body.String()
			req.Set("Content-Type", writer.FormDataContentType())
		}
		if len(req.Body) > 16<<20 {
			return nil, fmt.Errorf("form body exceeds 16 MiB")
		}
		req.Remove("Content-Length")
	default:
		return nil, fmt.Errorf("unsupported body type %q", r.BodyMode)
	}
	return req, nil
}

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
				d.queryRows = append(d.queryRows, requestField{Name: key, Value: value})
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
	if requestFieldsEditor(c, &d.queryRows, false, "parameter") {
		u, err := url.Parse(d.composeURL)
		if err == nil {
			var parts []string
			for _, row := range d.queryRows {
				if row.Name != "" || row.Value != "" {
					parts = append(parts, url.QueryEscape(row.Name)+"="+url.QueryEscape(row.Value))
				}
			}
			u.RawQuery, u.ForceQuery = strings.Join(parts, "&"), false
			d.composeURL = u.String()
			d.querySource = d.composeURL
		}
	}
}

// Fields are rows rather than a map: duplicate names, disabled entries and file
// parts all survive saving. Only Send/export reads a selected file.
func requestFieldsEditor(c *ui.Context, rows *[]requestField, form bool, noun string) bool {
	changed, remove := false, -1
	ui.Scroll(c).Grow(1).Gap(6).Padding(0, 8, 0, 0).Children(func() {
		for i := range *rows {
			r := &(*rows)[i]
			ui.Row(c).Key(i).Gap(6).AlignItems(ui.Center).Children(func() {
				if form {
					enabled := !r.Disabled
					if ui.Checkbox(c, &enabled, "").Label(fmt.Sprintf("Enable %s %d", noun, i+1)).Changed() {
						r.Disabled, changed = !enabled, true
					}
				}
				if ui.TextInput(c, &r.Name).Grow(1).Placeholder("Key").Label(fmt.Sprintf("%s key %d", noun, i+1)).Changed() {
					changed = true
				}
				if ui.TextInput(c, &r.Value).Grow(2).Placeholder("Value").Label(fmt.Sprintf("%s value %d", noun, i+1)).Changed() {
					changed = true
				}
				if form {
					if ui.Checkbox(c, &r.File, "File").Label(fmt.Sprintf("File part %d", i+1)).Changed() {
						changed = true
					}
				}
				if ui.Button(c, "×").Label(fmt.Sprintf("Remove %s %d", noun, i+1)).Clicked() {
					remove = i
				}
			})
		}
		if ui.Button(c, "Add "+noun).Clicked() {
			*rows = append(*rows, requestField{})
			changed = true
		}
	})
	if remove >= 0 {
		*rows = slices.Delete(*rows, remove, remove+1)
		changed = true
	}
	return changed
}

func (a *app) authEditor(c *ui.Context, d *requestDocument, t *ui.Theme) {
	ui.Scroll(c).Grow(1).Gap(8).Children(func() {
		ui.Select(c, &d.auth.Type, []string{"None", "Bearer", "Basic", "API key"}).Label("Authorization type").Width(180)
		ui.Text(c, "Use {{variables}} for secrets. Explicit auth overrides that header; scripts run last. Saving stores these fields locally.").FontSize(11).TextColor(t.TextMuted)
		switch d.auth.Type {
		case "Bearer":
			ui.TextInput(c, &d.auth.Token).Label("Bearer token").Placeholder("{{token}}").Password()
		case "Basic":
			ui.TextInput(c, &d.auth.Username).Label("Username")
			ui.TextInput(c, &d.auth.Password).Label("Password").Password()
		case "API key":
			ui.Row(c).Gap(8).Children(func() {
				ui.TextInput(c, &d.auth.Key).Label("API key name").Grow(1)
				ui.TextInput(c, &d.auth.Value).Label("API key value").Password().Grow(2)
				ui.Select(c, &d.auth.Location, []string{"Header", "Query"}).Label("API key location").Width(120)
			})
		}
	})
}

func (a *app) requestBodyEditor(c *ui.Context, d *requestDocument, t *ui.Theme) {
	if ui.Select(c, &d.bodyMode, []string{"Raw", "URL encoded", "Multipart"}).Label("Body type").Width(180).Changed() && d.bodyMode != "Raw" {
		d.requestPercent = max(d.requestPercent, 65)
	}
	if d.bodyMode == "Raw" {
		ui.TextArea(c.Key("body"), &d.composeBody).Grow(1).Font("monospace").FontSize(12).Label("Request body")
	} else {
		ui.Text(c, "FORM DATA · duplicate names supported; File values are local paths (16 MiB total limit)").FontSize(10).TextColor(t.TextMuted)
		requestFieldsEditor(c, &d.formFields, true, "field")
		if ui.Button(c, "Attach file…").Clicked() && a.win != nil {
			go func() {
				paths, err := mygo.Dialog.Open(mygo.OpenDialogOptions{Title: "Attach request file"})
				if err == nil && len(paths) > 0 {
					a.win.Update(func() { d.formFields = append(d.formFields, requestField{Name: "file", Value: paths[0], File: true}) })
				}
			}()
		}
	}
}
