package app

import (
	"cmp"
	"encoding/json/jsontext"
	"encoding/json/v2"
	"fmt"
	"net/url"
	"strings"
	"time"

	"github.com/baba983/vscroxy/internal/postman"
	"github.com/baba983/vscroxy/internal/repro"
	"github.com/baba983/vscroxy/internal/request"
	"github.com/baba983/vscroxy/internal/store"
	"github.com/baba983/vscroxy/locales"
	"github.com/egoist/mygo"
)

// exportReproBundle writes one file with redacted flows, their notes and
// timing, and a Postman collection that vscroxy-cli run can execute.
func (a *app) exportReproBundle(item *savedSession) {
	data, err := buildReproBundle(item.data)
	if err != nil {
		a.fail(locales.ExportReproBundle(), err)
		return
	}
	go func() {
		path, err := mygo.Dialog.Save(mygo.SaveDialogOptions{
			Title: locales.ExportReproBundle(), DefaultPath: "vscroxy-repro.json",
			Filters: []mygo.FileFilter{{Name: "Repro bundle", Extensions: []string{"json"}}},
		})
		if err != nil {
			a.fail(locales.ExportReproBundle(), err)
			return
		}
		if path == "" {
			return
		}
		if err := store.WriteFile(path, data); err != nil {
			a.fail(locales.ExportReproBundle(), err)
			return
		}
		a.done(locales.ReproBundleSavedTo(path))
	}()
}

// buildReproBundle redacts one saved session and embeds a runnable collection.
func buildReproBundle(file sessionFile) ([]byte, error) {
	title := cmp.Or(file.Title, "Saved session")
	collectionName := "Repro · " + title
	var flows []repro.Flow
	var requests []request.Request
	for _, f := range file.Flows {
		reqHeaders := redactReproHeaders(f.ReqHeaders)
		reqBody := redactReproBody(f.ReqBody)
		flows = append(flows, repro.Flow{
			ID: f.ID, Method: f.Method, URL: redactReproURL(f.URL),
			RequestHeaders: reqHeaders, RequestBody: reqBody,
			ResponseStatus: f.Status, ResponseHeaders: redactReproHeaders(f.ResHeaders),
			ResponseBody: redactReproBody(f.ResBody),
			Millis:       f.Millis, Wait: f.Wait, Download: f.Download, Timing: f.Timing, At: f.At,
			Kind: f.Kind, Note: f.Note, Color: f.Color, Pinned: f.Pinned, Strike: f.Strike,
		})
		if f.Kind != "" && f.Kind != "http" {
			continue
		}
		body := reqBody
		if len(f.ReqBytes) > 0 {
			body = ""
		}
		requests = append(requests, request.Request{
			ID: fmt.Sprintf("repro-%d", f.ID), Name: f.Method + " " + cmp.Or(f.Path, requestPath(f.URL)),
			Collection: collectionName, Method: f.Method, URL: redactReproURL(f.URL),
			Headers: reqHeaders, Body: body,
			Description: locales.ReproCollectionDescription(),
		})
	}
	if len(requests) == 0 {
		return nil, fmt.Errorf("this session has no HTTP(S) flows")
	}
	collection, _, err := postman.WriteCollection(collectionName, requests, nil)
	if err != nil {
		return nil, err
	}
	var embedded any
	if err := json.Unmarshal(collection, &embedded); err != nil {
		return nil, err
	}
	bundle := repro.Bundle{
		Format: repro.Format, Version: repro.Version, Title: title, Notes: file.Notes,
		Created: time.Now().UTC(), Flows: flows, Collection: embedded,
	}
	return json.Marshal(bundle, jsontext.WithIndent("  "))
}

// redactReproHeaders hides credentials and session state but keeps the header
// names, so the bundle shows what the request carried.
func redactReproHeaders(block string) string {
	header := request.ParseHeaderBlock(block)
	for name := range header {
		if request.IsSensitiveHeader(name) {
			header[name] = []string{"••••"}
		}
	}
	return request.HeaderBlock(header)
}

// redactReproURL hides credential query values and URL userinfo passwords.
func redactReproURL(raw string) string {
	u, err := url.Parse(raw)
	if err != nil {
		return raw
	}
	if u.User != nil {
		if _, set := u.User.Password(); set {
			u.User = url.UserPassword(u.User.Username(), "••••")
		}
	}
	query := u.Query()
	for name := range query {
		if request.IsSensitiveHeader(name) {
			query.Set(name, "••••")
		}
	}
	u.RawQuery = query.Encode()
	return u.String()
}

// redactReproBody redacts credential-named JSON fields and form values.
func redactReproBody(body string) string {
	if strings.TrimSpace(body) == "" {
		return body
	}
	var value any
	if json.Unmarshal([]byte(body), &value) == nil {
		redacted, err := json.Marshal(redactReproJSON(value))
		if err == nil {
			return string(redacted)
		}
	}
	if values, err := url.ParseQuery(body); err == nil && strings.Contains(body, "=") {
		for name := range values {
			if request.IsSensitiveHeader(name) {
				values.Set(name, "••••")
			}
		}
		return values.Encode()
	}
	return body
}

func redactReproJSON(value any) any {
	switch v := value.(type) {
	case map[string]any:
		for name, child := range v {
			if request.IsSensitiveHeader(name) {
				v[name] = "••••"
				continue
			}
			v[name] = redactReproJSON(child)
		}
		return v
	case []any:
		for i := range v {
			v[i] = redactReproJSON(v[i])
		}
		return v
	default:
		return value
	}
}
