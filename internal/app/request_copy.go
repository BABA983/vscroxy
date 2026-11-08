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
	"github.com/baba983/vscroxy/locales"
	"github.com/egoist/mygo/ui"
)

// Stable command IDs also feed the native menu and command palette.
var requestCopyFormats = []struct{ id, label, format string }{
	{"flow.copy-curl", "Copy as cURL", "cURL"},
	{"flow.copy-curl-cmd", "Copy as cURL (CMD)", "cURL (CMD)"},
	{"flow.copy-powershell", "Copy as PowerShell", "PowerShell"},
	{"flow.copy-fetch", "Copy as fetch (browser)", "JavaScript (fetch)"},
	{"flow.copy-node-fetch", "Copy as fetch (Node.js)", "Node.js (fetch)"},
	{"flow.copy-swift", "Copy as Swift", "Swift (URLSession)"},
	{"flow.copy-kotlin", "Copy as Kotlin", "Kotlin (OkHttp)"},
	{"flow.copy-java", "Copy as Java", "Java (java.net.http)"},
	{"flow.copy-csharp", "Copy as C#", "C# (HttpClient)"},
	{"flow.copy-php", "Copy as PHP", "PHP (cURL)"},
	{"flow.copy-ruby", "Copy as Ruby", "Ruby (Net::HTTP)"},
	{"flow.copy-rust", "Copy as Rust", "Rust (reqwest)"},
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
		if items[i].id == "flow.copy-raw" {
			items[i].label = "Raw HTTP"
		}
	}
	return submenu(locales.CopyAs(), items...)
}

func (a *app) copyRequest(c *ui.Context, f flow, d *requestDocument, format string) {
	if d != nil {
		req, err := a.draftPrepared(d)
		if err != nil {
			a.fail("Copy request", err)
			return
		}
		effective := a.transferSettings(d)
		a.copyText(c, format, func() (string, error) { return requestCopyTextPrepared(req, format, &effective) })
		return
	}
	f.reqHeader, f.reqTrailer = f.reqHeader.Clone(), f.reqTrailer.Clone()
	a.copyText(c, format, func() (string, error) { return requestCopyText(f, format, nil) })
}

// requestCopyTextPrepared writes an already prepared draft. Passing Prepared
// directly keeps a Binary body's source path, which Snippet uses to generate
// a file reference instead of inlining bytes.
func requestCopyTextPrepared(req *request.Prepared, format string, settings *request.Settings) (string, error) {
	if format == "cURL" || format == "cURL (CMD)" {
		f := flow{method: req.Method, url: req.URL, reqHeader: req.Header, reqBody: req.Body}
		if settings != nil {
			return curlCommandForSettings(f, *settings, format == "cURL (CMD)")
		}
		return curlCommandFor(f, format == "cURL (CMD)")
	}
	return requestSnippetText(req.Method, req.URL, req.Body, req.BodyFile, req.Header, false, format)
}

func requestCopyText(f flow, format string, settings *request.Settings) (string, error) {
	if format == "cURL" || format == "cURL (CMD)" {
		if settings != nil {
			return curlCommandForSettings(f, *settings, format == "cURL (CMD)")
		}
		return curlCommandFor(f, format == "cURL (CMD)")
	}
	body, err := copyRequestBody(f)
	if err != nil {
		return "", err
	}
	decoded := f.id != 0 && f.reqHeader.Get("Content-Encoding") != ""
	return requestSnippetText(f.method, f.url, body, "", f.reqHeader, decoded, format)
}

// requestSnippetText removes headers clients compute themselves and then
// writes the snippet. A captured decoded body also omits the stale encoding
// and digest headers.
func requestSnippetText(method, rawURL, body, bodyFile string, source http.Header, decoded bool, format string) (string, error) {
	headers := http.Header{}
	for _, name := range slices.Sorted(maps.Keys(source)) {
		for _, value := range source[name] {
			headers.Add(name, value)
		}
	}
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
	return request.Snippet(format, &request.Prepared{Method: method, URL: rawURL, Header: headers, Body: body, BodyFile: bodyFile})
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
