package app

import (
	"context"
	"encoding/json/jsontext"
	"maps"
	"net/http"
	"slices"
	"strings"
	"time"

	"github.com/baba983/vscroxy/internal/request"
	"github.com/egoist/mygo/ui"
)

// Stable command IDs also feed the native menu and command palette.
var requestCopyFormats = []struct{ id, label, format string }{
	{"flow.copy-curl", "Copy as cURL", "cURL"},
	{"flow.copy-curl-cmd", "Copy as cURL (CMD)", "cURL (CMD)"},
	{"flow.copy-powershell", "Copy as PowerShell", "PowerShell"},
	{"flow.copy-fetch", "Copy as fetch (browser)", "JavaScript (fetch)"},
	{"flow.copy-node-fetch", "Copy as fetch (Node.js)", "Node.js (fetch)"},
}

func (a *app) requestCopyActions(c *ui.Context, f flow, d *requestDocument) []action {
	var items []action
	for _, format := range requestCopyFormats {
		items = append(items, do(format.id, format.label, func() { a.copyRequest(c, f, d, format.format) }).when(d != nil || f.kind == "" && f.url != "", inspectOne))
	}
	return items
}

func copyAsMenu(items []action) action {
	items = append([]action(nil), items...)
	for i := range items {
		items[i].label = strings.TrimPrefix(items[i].label, "Copy as ")
		if items[i].id == "flow.copy-curl" {
			items[i].label = "cURL (Bash)"
		}
	}
	return submenu("Copy as…", items...)
}

func (a *app) harCopyActions(c *ui.Context, flows []flow) []action {
	ok := len(flows) > 0 && len(flows) <= 200
	for _, f := range flows {
		ok = ok && f.kind == ""
	}
	return []action{
		do("flow.copy-har", "Copy as HAR (sanitized)", func() { a.copyHAR(c, flows, false) }).when(ok, "Select 1–200 HTTP requests"),
		do("flow.copy-har-raw", "Copy as HAR (with sensitive data)", func() { a.copyHAR(c, flows, true) }).when(ok, "Select 1–200 HTTP requests"),
	}
}

func (a *app) copyRequest(c *ui.Context, f flow, d *requestDocument, format string) {
	if d != nil {
		req, err := a.draftPrepared(d)
		if err != nil {
			a.fail("Copy request", err)
			return
		}
		f = flow{method: req.Method, url: req.URL, reqHeader: req.Header, reqBody: req.Body}
	}
	f.reqHeader, f.reqTrailer = f.reqHeader.Clone(), f.reqTrailer.Clone()
	a.copyText(c, format, func() (string, error) { return requestCopyText(f, format) })
}

func requestCopyText(f flow, format string) (string, error) {
	if format == "cURL" || format == "cURL (CMD)" {
		return curlCommandFor(f, format == "cURL (CMD)")
	}
	body, err := copyRequestBody(f)
	if err != nil {
		return "", err
	}
	headers := http.Header{}
	for _, name := range slices.Sorted(maps.Keys(f.reqHeader)) {
		for _, value := range f.reqHeader[name] {
			headers.Add(name, value)
		}
	}
	decoded := f.id != 0 && headers.Get("Content-Encoding") != ""
	for _, line := range headers.Values("Connection") {
		for name := range strings.SplitSeq(line, ",") {
			headers.Del(strings.TrimSpace(name))
		}
	}
	for name := range headers {
		if curlSkip[strings.ToLower(name)] || decoded && (name == "Content-Encoding" || name == "Content-Md5" || name == "Digest") {
			delete(headers, name)
		}
	}
	return request.Snippet(format, &request.Prepared{Method: f.method, URL: f.url, Header: headers, Body: body})
}

func (a *app) copyHAR(c *ui.Context, flows []flow, details bool) {
	work, err := harExportWork(flows, details)
	if err != nil {
		a.fail("Copy HAR", err)
		return
	}
	label := "sanitized HAR"
	if details {
		label = "HAR with sensitive data"
	}
	a.copyText(c, label, func() (string, error) {
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		value, err := work(ctx)
		if err != nil {
			return "", err
		}
		return string(value.(jsontext.Value)), nil
	})
}

// No Context or editor state escapes to a worker. Only a successful export
// changes the clipboard; all OS clipboard writes return to the UI thread.
func (a *app) copyText(c *ui.Context, label string, generate func() (string, error)) {
	services, win := c.Services(), a.win
	work := func() {
		text, err := generate()
		finish := func() {
			if err != nil {
				a.fail("Copy "+label, err)
				return
			}
			services.WriteClipboard(text)
			a.done("Copied " + label + "; review credentials before sharing")
		}
		if win == nil {
			finish()
		} else {
			win.Update(finish)
		}
	}
	if win == nil {
		work()
	} else {
		go work()
	}
}
