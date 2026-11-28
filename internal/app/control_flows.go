package app

import (
	"cmp"
	"encoding/base64"
	"io"
	"net/http"
	"os"
	"slices"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/baba983/vscroxy/internal/control"
)

// Text body mode is deliberately small: enough for an agent to read a JSON
// error or a short HTML page, not a response download. Raw/unredacted bodies
// remain behind details:true and the MCP --allow-sensitive capability.
const (
	flowTextBodyDefault = 8 << 10
	flowTextBodyMax     = 64 << 10
)

type flowParams struct {
	ID       int    `json:"id"`
	Limit    int    `json:"limit"`
	Before   int    `json:"before"`
	After    int    `json:"after"`
	Details  bool   `json:"details"`
	Body     string `json:"body"`
	MaxBytes int    `json:"maxBytes"`
	Host     string `json:"host"`
	Method   string `json:"method"`
	App      string `json:"app"`
	Path     string `json:"path"`
	Status   int    `json:"status"`
	Since    string `json:"since"`
	Until    string `json:"until"`
}

func flowInvalid(field, message, hint string) error {
	return controlFaultf("invalid-flow-parameter", field, message, hint)
}

// validate names the field and its range instead of one generic rejection for
// every malformed flow query. Values supplied by the client are never echoed.
func (p flowParams) validate(action string) error {
	if p.Limit < 0 || p.Limit > 200 {
		return flowInvalid("limit", "limit must be between 1 and 200", "omit limit for the default 50")
	}
	if p.Before < 0 {
		return flowInvalid("before", "before must be a non-negative flow ID", "use an id from a previous page's nextBefore")
	}
	if p.After < 0 {
		return flowInvalid("after", "after must be a non-negative flow ID", "use an id from a previous page's nextAfter")
	}
	if p.Before != 0 && p.After != 0 {
		return flowInvalid("before, after", "before and after cannot be combined", "use before to page older or after to page newer")
	}
	if p.Status != 0 && (p.Status < 100 || p.Status > 999) {
		return flowInvalid("status", "status must be between 100 and 999", "omit status to match every response status")
	}
	if p.Since != "" {
		if _, err := time.Parse(time.RFC3339Nano, p.Since); err != nil {
			return flowInvalid("since", "since must be an RFC 3339 time", "example: 2026-11-27T10:00:00Z")
		}
	}
	if p.Until != "" {
		if _, err := time.Parse(time.RFC3339Nano, p.Until); err != nil {
			return flowInvalid("until", "until must be an RFC 3339 time", "example: 2026-11-27T10:05:00Z")
		}
	}
	if p.Body != "" && p.Body != "text" {
		return flowInvalid("body", `body must be "text" or omitted`, `body:"text" returns redacted textual bodies without --allow-sensitive`)
	}
	if action != "flows.get" && (p.Body != "" || p.MaxBytes != 0) {
		return flowInvalid("body, maxBytes", "body and maxBytes are only valid for flows.get", "flows.list returns summaries without bodies")
	}
	if p.MaxBytes < 0 || p.MaxBytes > flowTextBodyMax {
		return flowInvalid("maxBytes", "maxBytes must be between 1 and 65536", "omit maxBytes for 8192")
	}
	if p.MaxBytes != 0 && p.Body != "text" {
		return flowInvalid("maxBytes", `maxBytes requires body:"text"`, `add body:"text" or omit maxBytes`)
	}
	if p.Details && p.Body == "text" {
		return flowInvalid("details, body", "details and body:text cannot be combined", "use details:true for raw data or body:text for a redacted text view")
	}
	return nil
}

func (a *app) controlFlows(command control.Command) (any, error) {
	p, err := controlParams[flowParams](command)
	if err != nil {
		return nil, flowInvalid("", "invalid flow parameters", "check field names and types; see docs/automation.md")
	}
	if err := p.validate(command.Action); err != nil {
		return nil, err
	}
	if command.Action == "flows.get" {
		if p.ID <= 0 {
			return nil, flowInvalid("id", "id must be a positive flow ID", "list ids with flows.list")
		}
		i := slices.IndexFunc(a.flows, func(f flow) bool { return f.live && f.id == p.ID })
		if i < 0 {
			return nil, controlFaultf("flow-not-found", "id", "live flow not found",
				"list ids with flows.list; replay works on live flows only")
		}
		f := a.flows[i]
		switch {
		case p.Details:
			return controlFlow(f, true), nil
		case p.Body == "text":
			maxBytes := p.MaxBytes
			if maxBytes == 0 {
				maxBytes = flowTextBodyDefault
			}
			return controlFlowText(f, maxBytes), nil
		default:
			return controlFlow(f, false), nil
		}
	}

	// Times are inclusive and compared with each flow's start time.
	var since, until time.Time
	if p.Since != "" {
		since, _ = time.Parse(time.RFC3339Nano, p.Since)
	}
	if p.Until != "" {
		until, _ = time.Parse(time.RFC3339Nano, p.Until)
	}
	// Concurrent captures can be delivered out of ID order. Cursor paging
	// must use ID order, independent of arrival or the table's sort.
	indices := make([]int, 0, len(a.flows))
	for i, f := range a.flows {
		if f.live {
			indices = append(indices, i)
		}
	}
	ascending := p.After != 0
	slices.SortFunc(indices, func(i, j int) int {
		if ascending {
			return cmp.Compare(a.flows[i].id, a.flows[j].id)
		}
		return cmp.Compare(a.flows[j].id, a.flows[i].id)
	})
	limit := cmp.Or(p.Limit, 50)
	rows := make([]map[string]any, 0, min(limit, len(indices)))
	last, next := 0, 0
	for _, i := range indices {
		f := a.flows[i]
		if ascending {
			if f.id <= p.After {
				continue
			}
		} else if p.Before != 0 && f.id >= p.Before {
			continue
		}
		if !p.matches(f, since, until) {
			continue
		}
		// Find one match beyond the page. Without it, an exact-limit page is
		// also the last page, so the next cursor stays zero.
		if len(rows) == limit {
			next = last
			break
		}
		rows = append(rows, controlFlow(f, false))
		last = f.id
	}
	page := map[string]any{"items": rows, "nextBefore": 0, "nextAfter": 0}
	if ascending {
		page["nextAfter"] = next
	} else {
		page["nextBefore"] = next
	}
	return page, nil
}

func (p flowParams) matches(f flow, since, until time.Time) bool {
	if p.Host != "" && !strings.EqualFold(f.host, p.Host) && !strings.EqualFold(hostOnly(f.host), p.Host) {
		return false
	}
	if p.Method != "" && !strings.EqualFold(f.method, p.Method) {
		return false
	}
	if p.Status != 0 && f.status != p.Status {
		return false
	}
	if !since.IsZero() && f.at.Before(since) || !until.IsZero() && f.at.After(until) {
		return false
	}
	if p.App != "" && !strings.EqualFold(f.process, p.App) {
		return false
	}
	if p.Path != "" && !strings.Contains(strings.ToLower(f.path), strings.ToLower(p.Path)) {
		return false
	}
	return true
}

// controlFlow is the summary shape shared by flows.list, flows.get and the
// text mode. Raw details are added only when the caller passed details:true.
func controlFlow(f flow, details bool) map[string]any {
	value := map[string]any{
		"id": f.id, "method": f.method, "url": redactExportURL(f.url, safeExportOptions()),
		"status": f.status, "bytes": f.bytes, "millis": f.millis, "at": f.at,
		"requestContentType": f.bodyContentType(false), "responseContentType": f.bodyContentType(true),
	}
	if details {
		value["url"] = f.url
		value["requestHeaders"], value["responseHeaders"] = f.reqHeader.Clone(), f.respHeader.Clone()
		for _, side := range []struct {
			name, body, file string
			size             int
		}{{"request", f.reqBody, f.reqFile, f.reqSize}, {"response", f.resBody, f.resFile, f.resSize}} {
			preview := side.body[:min(len(side.body), 64<<10)]
			value[side.name+"BodyBase64"] = base64.StdEncoding.EncodeToString([]byte(preview))
			value[side.name+"BodyTruncated"] = side.file != "" || side.size > len(preview)
		}
	}
	return value
}

// controlFlowText returns headers and bodies under the same field-redaction
// policy as the safe HAR export: Content-Type and Accept keep their values,
// every other header value becomes exportReplacement, and URL query values are
// already replaced by redactExportURL in the summary.
func controlFlowText(f flow, maxBytes int) map[string]any {
	value := controlFlow(f, false)
	value["requestHeaders"] = controlRedactedHeaders(f.reqHeader, f.reqHeaders)
	value["responseHeaders"] = controlRedactedHeaders(f.respHeader, f.resHeaders)
	for _, side := range []struct {
		name, head, file string
		size             int
		problem          string
		contentType      string
	}{
		{"request", f.reqBody, f.reqFile, f.reqSize, f.reqError, f.bodyContentType(false)},
		{"response", f.resBody, f.resFile, f.resSize, f.resError, f.bodyContentType(true)},
	} {
		data, total, truncated, incomplete := controlFlowBody(side.head, side.file, side.size, side.problem, maxBytes)
		encoding := "base64"
		body := base64.StdEncoding.EncodeToString(data)
		if flowTextContentType(side.contentType) {
			if text, ok := flowTextBytes(data); ok {
				encoding, body = "text", string(text)
				if truncated {
					body += "\n…Truncated"
				}
			}
		}
		value[side.name+"Body"] = body
		value[side.name+"BodyEncoding"] = encoding
		value[side.name+"BodyBytes"] = total
		value[side.name+"BodyTruncated"] = truncated
		value[side.name+"BodyIncomplete"] = incomplete
	}
	return value
}

func controlRedactedHeaders(structured http.Header, block string) map[string][]string {
	headers := structured
	if len(headers) == 0 {
		headers = make(http.Header)
		for _, pair := range headerPairs(block, nil) {
			headers.Add(pair.Name, pair.Value)
		}
	}
	result := make(map[string][]string, len(headers))
	for name, values := range headers {
		out := make([]string, len(values))
		for i, value := range values {
			if exportPublicHeader(name) {
				out[i] = value
			} else {
				out[i] = exportReplacement
			}
		}
		result[name] = out
	}
	return result
}

// controlFlowBody reads at most limit bytes. total is the body's stored size,
// available is what this process still has (inline head or spill file), so a
// capture that lost bytes is reported as incomplete rather than truncated.
func controlFlowBody(head, file string, size int, problem string, limit int) (data []byte, total int, truncated, incomplete bool) {
	data = []byte(head)
	total, available := max(size, len(head)), len(head)
	incomplete = problem != ""
	if file != "" {
		info, err := os.Stat(file)
		if err != nil || !info.Mode().IsRegular() {
			return data[:min(len(data), limit)], total, false, true
		}
		available = int(info.Size())
		total = max(total, available)
		f, err := os.Open(file)
		if err != nil {
			return data[:min(len(data), limit)], total, false, true
		}
		defer f.Close()
		want := min(available, limit)
		data = make([]byte, want)
		n, err := io.ReadFull(f, data)
		data = data[:n]
		if err != nil || n < want {
			incomplete = true
		}
	}
	if available > limit {
		data = data[:min(len(data), limit)]
		truncated = true
	}
	if available < total {
		incomplete = true
	}
	return data, total, truncated, incomplete
}

// flowTextContentType decides whether a body is a text document. An empty or
// unknown media type is sniffed by flowTextBytes instead of being forced to
// base64.
func flowTextContentType(contentType string) bool {
	media, _, _ := strings.Cut(contentType, ";")
	media = strings.ToLower(strings.TrimSpace(media))
	if media == "" {
		return true
	}
	if strings.HasPrefix(media, "text/") || strings.HasSuffix(media, "+json") || strings.HasSuffix(media, "+xml") {
		return true
	}
	switch media {
	case "application/json", "application/xml", "application/javascript", "application/ecmascript",
		"application/x-javascript", "application/x-www-form-urlencoded", "application/graphql",
		"application/x-ndjson", "application/ndjson", "application/jsonl", "application/yaml",
		"application/x-yaml", "application/toml", "application/sql", "application/rtf",
		"application/x-sh", "application/x-httpd-php", "application/xhtml+xml":
		return true
	}
	return false
}

// flowTextBytes accepts UTF-8 without binary control bytes. A prefix cut at
// maxBytes may end mid-rune; only that trailing partial rune is dropped.
func flowTextBytes(data []byte) ([]byte, bool) {
	text := data
	if !utf8.Valid(text) {
		for i := 0; i < utf8.UTFMax && len(text) > 0; i++ {
			text = text[:len(text)-1]
			if utf8.Valid(text) {
				break
			}
		}
		if !utf8.Valid(text) {
			return nil, false
		}
	}
	for _, b := range text {
		if b == 0 || b < 0x20 && b != '\n' && b != '\r' && b != '\t' && b != '\f' && b != '\b' {
			return nil, false
		}
	}
	return text, true
}
