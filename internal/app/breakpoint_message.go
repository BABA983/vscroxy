package app

// The breakpoint editor's message model. A paused request is edited as
// fields: the URL, headers and body. Its query string and a form body also
// show as tables of rows, and the raw tab shows the whole message as text.
// Each view writes back into the same fields, and the fields are what the
// proxy receives.

import (
	"net/url"
	"strings"
	"time"

	"github.com/baba983/vscroxy/internal/capture"
	"github.com/baba983/vscroxy/internal/request"
	"github.com/baba983/vscroxy/locales"
)

// decodeRows reads an application/x-www-form-urlencoded string as rows, in
// order and with repeats. It reports false when a name or value is not valid
// escaping, and the text stays the editor's.
func decodeRows(s string) ([]request.Field, bool) {
	var rows []request.Field
	for part := range strings.SplitSeq(s, "&") {
		if part == "" {
			continue
		}
		k, v, _ := strings.Cut(part, "=")
		key, err := url.QueryUnescape(k)
		if err != nil {
			return nil, false
		}
		value, err := url.QueryUnescape(v)
		if err != nil {
			return nil, false
		}
		rows = append(rows, request.Field{Name: key, Value: value})
	}
	return rows, true
}

// encodeRows writes rows as a query string. Unchecked and empty rows are left
// out, as the request editor sends them.
func encodeRows(rows []request.Field) string {
	var parts []string
	for _, r := range rows {
		if r.Disabled || r.Name == "" && r.Value == "" {
			continue
		}
		parts = append(parts, url.QueryEscape(r.Name)+"="+url.QueryEscape(r.Value))
	}
	return strings.Join(parts, "&")
}

// splitQuery separates a URL into its base and the rows of its query. A query
// that does not decode is returned with ok false, and the URL stays as text.
func splitQuery(raw string) (base string, rows []request.Field, ok bool) {
	base, query, found := strings.Cut(raw, "?")
	if !found {
		return raw, nil, true
	}
	rows, ok = decodeRows(query)
	if !ok {
		return raw, nil, false
	}
	return base, rows, true
}

// isFormBody reports whether a header block says the body is form-encoded.
func isFormBody(headers string) bool {
	ct := request.ParseHeaderBlock(headers).Get("Content-Type")
	mediaType, _, _ := strings.Cut(ct, ";")
	return strings.EqualFold(strings.TrimSpace(mediaType), "application/x-www-form-urlencoded")
}

// splitTables moves a request's query and form body into their tables, and
// the rest stays in the text fields. A query row set replaces the URL's query
// on apply; a form body is written from its rows.
func (a *app) splitTables() {
	a.bpQuery, a.bpForm, a.bpFormRows = nil, nil, false
	if a.bp == nil || a.bp.Phase != capture.PhaseRequest {
		return
	}
	if base, rows, ok := splitQuery(a.bpURL); ok && len(rows) > 0 {
		a.bpURL, a.bpQuery = base, rows
	}
	if !a.bp.BodyReadOnly && isFormBody(a.bpHeaders) {
		if rows, ok := decodeRows(a.bpBody); ok {
			a.bpForm, a.bpFormRows = rows, true
		}
	}
}

// composedURL is the URL the editor sends: its base with the query table's
// rows, when there are any.
func (a *app) composedURL() string {
	if len(a.bpQuery) == 0 {
		return a.bpURL
	}
	base, _, _ := strings.Cut(a.bpURL, "?")
	if query := encodeRows(a.bpQuery); query != "" {
		return base + "?" + query
	}
	return base
}

// composedBody is the body the editor sends: the form table's rows when the
// form is shown as a table, otherwise the text.
func (a *app) composedBody() string {
	if a.bpFormRows {
		return encodeRows(a.bpForm)
	}
	return a.bpBody
}

// rawMessage writes the paused message as HTTP/1.1 text: the start line, the
// headers, a blank line and the body. A body the text editor cannot show is
// left out, and the raw tab says so.
func (a *app) rawMessage() string {
	start := "HTTP/1.1 " + a.bpStatus
	if a.bp.Phase != capture.PhaseResponse {
		start = a.bpMethod + " " + a.composedURL() + " HTTP/1.1"
	}
	head := start
	if a.bpHeaders != "" {
		head += "\n" + a.bpHeaders
	}
	if a.bp.BodyReadOnly {
		return head + "\n\n"
	}
	return head + "\n\n" + a.composedBody()
}

// rawFields is what a raw message says, once read back.
type rawFields struct {
	method, url, status, headers, body string
}

// parseRawMessage reads the raw text back. The first blank line ends the head,
// and the body after it is kept byte for byte. It returns what is wrong with
// the start line, or "".
func parseRawMessage(raw string, response bool) (rawFields, string) {
	var f rawFields
	head, body := splitRawMessage(raw)
	head = strings.ReplaceAll(head, "\r\n", "\n")
	first, headers, _ := strings.Cut(head, "\n")
	fields := strings.Fields(first)
	if response {
		if len(fields) < 2 {
			return f, locales.RawNeedsStatusLine()
		}
		f.status = fields[1]
	} else {
		if len(fields) < 2 {
			return f, locales.RawNeedsRequestLine()
		}
		f.method, f.url = fields[0], fields[1]
	}
	f.headers, f.body = headers, body
	return f, ""
}

// splitRawMessage cuts a raw message at its first blank line, which is CRLF
// or LF, whichever comes first.
func splitRawMessage(raw string) (head, body string) {
	at, sep := len(raw), 0
	if i := strings.Index(raw, "\n\n"); i >= 0 {
		at, sep = i, 2
	}
	if i := strings.Index(raw, "\r\n\r\n"); i >= 0 && i < at {
		at, sep = i, 4
	}
	if sep == 0 {
		return raw, ""
	}
	return raw[:at], raw[at+sep:]
}

// syncRaw makes the fields follow the raw text while the raw tab is open. A
// text that does not read as a message keeps the last good fields and says why.
func (a *app) syncRaw() {
	if !a.bpRawMode || a.bp == nil {
		return
	}
	f, problem := parseRawMessage(a.bpRaw, a.bp.Phase == capture.PhaseResponse)
	a.bpRawNote = problem
	if problem != "" {
		return
	}
	a.bpMethod, a.bpURL, a.bpStatus, a.bpHeaders = f.method, f.url, f.status, f.headers
	if !a.bp.BodyReadOnly {
		a.bpBody = f.body
	}
	a.splitTables()
}

// breakWaitOptions are the times an unanswered pause may last, in seconds.
var breakWaitOptions = []int{30, 60, 120, 300, 600}

// defaultBreakWaitSeconds is what a pause lasts when no time is saved.
const defaultBreakWaitSeconds = 120

// breakWaitLabel says a wait in the app's language.
func breakWaitLabel(seconds int) string {
	switch seconds {
	case 30:
		return locales.BreakWait30Seconds()
	case 60:
		return locales.BreakWait1Minute()
	case 300:
		return locales.BreakWait5Minutes()
	case 600:
		return locales.BreakWait10Minutes()
	}
	return locales.BreakWait2Minutes()
}

// waitSeconds is how long an unanswered pause lasts, in seconds.
func (a *app) waitSeconds() int {
	if a.breakWaitSeconds <= 0 {
		return defaultBreakWaitSeconds
	}
	return a.breakWaitSeconds
}

// breakWait is how long an unanswered pause lasts.
func (a *app) breakWait() time.Duration {
	return time.Duration(a.waitSeconds()) * time.Second
}

// breakWaitLabels are the wait choices the select shows, in order.
func breakWaitLabels() []string {
	labels := make([]string, len(breakWaitOptions))
	for i, seconds := range breakWaitOptions {
		labels[i] = breakWaitLabel(seconds)
	}
	return labels
}

// setBreakWait saves the wait the select shows and applies it to the proxy,
// for the pauses that come next.
func (a *app) setBreakWait() {
	for _, seconds := range breakWaitOptions {
		if breakWaitLabel(seconds) == a.bpWait {
			a.breakWaitSeconds = seconds
		}
	}
	if a.srv != nil {
		a.srv.SetBreakWait(a.breakWait())
	}
}

// toggleRaw switches between the fields and the raw message. Leaving the raw
// tab with text that does not read keeps it open and says why.
func (a *app) toggleRaw() {
	if a.bpRawMode {
		a.syncRaw()
		if a.bpRawNote == "" {
			a.bpRawMode = false
		}
		return
	}
	a.bpRaw, a.bpRawNote, a.bpRawMode = a.rawMessage(), "", true
}
