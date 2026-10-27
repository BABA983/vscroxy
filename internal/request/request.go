// Package request is an authored HTTP or WebSocket request and everything
// that turns it into traffic: {{variable}} expansion, auth and form encoding,
// pre-request and post-response scripts, declarative response checks, and
// sending through the capture proxy so every send is also captured.
//
// It knows nothing about the window: the app stores requests, edits them
// and shows an Execution's result.
package request

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
	"strings"
)

// Request is what a user authors and saves. Names and collection paths are
// editable labels, not asset identities: ID is.
type Request struct {
	ID         string  `json:"id,omitempty"`
	Name       string  `json:"name"`
	Collection string  `json:"collection,omitempty"`
	Method     string  `json:"method"`
	URL        string  `json:"url"`
	Headers    string  `json:"headers,omitempty"`
	Body       string  `json:"body,omitempty"`
	Auth       Auth    `json:"auth,omitzero"`
	BodyMode   string  `json:"bodyMode,omitempty"`
	Form       []Field `json:"form,omitempty"`
	// HeaderList keeps the header table's rows: order, duplicates, empty
	// values and disabled rows. Headers stays the Key: Value block the send,
	// export and script paths read, written from the enabled rows.
	HeaderList []Field `json:"headerList,omitempty"`
	// Pre runs before the send and may change it; Post sees the response.
	Pre    string `json:"pre,omitempty"`
	Post   string `json:"post,omitempty"`
	Checks Checks `json:"checks,omitzero"`
}

type Auth struct {
	Type     string `json:"type,omitempty"`
	Token    string `json:"token,omitempty"`
	Username string `json:"username,omitempty"`
	Password string `json:"password,omitempty"`
	Key      string `json:"key,omitempty"`
	Value    string `json:"value,omitempty"`
	Location string `json:"location,omitempty"`
}

type Field struct {
	Name     string `json:"name"`
	Value    string `json:"value"`
	File     bool   `json:"file,omitzero"`
	Disabled bool   `json:"disabled,omitzero"`
	// Revealed is the table's own state, never saved: a masked credential shows
	// in plain text only when the reader asks for this row.
	Revealed bool `json:"-"`
}

// Prepare is the encoder shared by Send, Runner and cURL export.
// Send/Runner run pre-scripts on templates first, then encode with final vars.
func (r Request) Prepare(vars map[string]string) (*Prepared, error) {
	req := &Prepared{
		Method: strings.ToUpper(strings.TrimSpace(cmp.Or(ExpandVars(r.Method, vars), "GET"))),
		URL:    strings.TrimSpace(ExpandVars(r.URL, vars)), Body: ExpandVars(r.Body, vars),
		Header: ParseHeaderBlock(ExpandVars(r.Headers, vars)),
	}
	if req.URL == "" {
		return nil, fmt.Errorf("the URL is empty")
	}
	switch r.Auth.Type {
	case "", "None", AuthInherit: // Execute resolves Inherit; without a Context there is no parent.
	case "Bearer":
		req.Set("Authorization", "Bearer "+ExpandVars(r.Auth.Token, vars))
	case "Basic":
		value := ExpandVars(r.Auth.Username, vars) + ":" + ExpandVars(r.Auth.Password, vars)
		req.Set("Authorization", "Basic "+base64.StdEncoding.EncodeToString([]byte(value)))
	case "API key":
		key, value := ExpandVars(r.Auth.Key, vars), ExpandVars(r.Auth.Value, vars)
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
			name, value := ExpandVars(field.Name, vars), ExpandVars(field.Value, vars)
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
