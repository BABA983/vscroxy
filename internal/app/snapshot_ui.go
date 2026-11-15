package app

import (
	"encoding/json/jsontext"
	"strings"

	"github.com/baba983/vscroxy/internal/request"
	"github.com/baba983/vscroxy/locales"
)

// snapshotHeaders keeps response headers that describe the payload, dropping
// values that change on every response so a promoted snapshot stays useful.
func snapshotHeaders(block string) string {
	header := request.ParseHeaderBlock(block)
	for _, name := range []string{
		"Age", "Connection", "Content-Encoding", "Content-Length", "Date", "ETag",
		"Keep-Alive", "Last-Modified", "Server", "Set-Cookie", "Transfer-Encoding",
	} {
		header.Del(name)
	}
	return request.HeaderBlock(header)
}

// requestPath is a request URL's path with its host and {{base}} variable
// removed, for matching a snapshot to captures of the same endpoint.
func requestPath(raw string) string {
	raw = strings.TrimSpace(raw)
	if i := strings.Index(raw, "://"); i >= 0 {
		rest := raw[i+3:]
		if j := strings.IndexByte(rest, '/'); j >= 0 {
			raw = rest[j:]
		} else {
			raw = "/"
		}
	}
	if strings.HasPrefix(raw, "{{") {
		if end := strings.Index(raw, "}}"); end >= 0 {
			raw = raw[end+2:]
		}
	}
	raw, _, _ = strings.Cut(raw, "?")
	raw, _, _ = strings.Cut(raw, "#")
	if !strings.HasPrefix(raw, "/") {
		raw = "/" + raw
	}
	return raw
}

// learnSnapshotVary compares the snapshot with complete captures of the same
// method and path, then records the fields that changed.
func (a *app) learnSnapshotVary(d *requestDocument) {
	snapshot := d.checks.Snapshot
	if snapshot == nil {
		return
	}
	method, path := d.value().Method, requestPath(d.value().URL)
	var bodies []string
	for _, f := range a.flows {
		if !strings.EqualFold(f.method, method) || !pathMatches(path, requestPath(f.url)) {
			continue
		}
		if f.resError != "" {
			continue
		}
		body := f.resBody
		if f.resFile != "" {
			body = string(readBodyFile(f.resFile))
		} else if f.resSize > len(f.resBody) {
			continue
		}
		if !jsontext.Value(body).IsValid() {
			continue
		}
		bodies = append(bodies, body)
	}
	if len(bodies) == 0 {
		a.inform(locales.SnapshotNoCaptures())
		return
	}
	snapshot.Vary = request.LearnVaryingFields(snapshot.Body, bodies)
	if len(snapshot.Vary) == 0 {
		a.inform(locales.SnapshotNoVaryingFields())
		return
	}
	a.inform(locales.SnapshotLearned(len(snapshot.Vary)))
}

// pathMatches compares a request path template, such as /pets/{{id}} or
// /pets/:id, with a captured literal path. Only whole segments are variables.
func pathMatches(template, actual string) bool {
	want, got := strings.Split(template, "/"), strings.Split(actual, "/")
	if len(want) != len(got) {
		return false
	}
	for i := range want {
		if want[i] == got[i] {
			continue
		}
		if strings.HasPrefix(want[i], "{{") && strings.HasSuffix(want[i], "}}") || strings.HasPrefix(want[i], "{") && strings.HasSuffix(want[i], "}") || strings.HasPrefix(want[i], ":") {
			if got[i] == "" {
				return false
			}
			continue
		}
		return false
	}
	return true
}
