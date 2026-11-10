package app

import (
	"bytes"
	"encoding/csv"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
	"unicode/utf8"
)

func captureTableRow(t *testing.T, a *app, f flow) map[string]string {
	t.Helper()
	a.flows = []flow{f}
	table, warnings := a.snapshotCaptureTable(a.flows)
	if len(warnings) != 0 {
		t.Fatalf("unexpected warnings: %v", warnings)
	}
	data, err := table.csv(exportOptions{})
	if err != nil {
		t.Fatal(err)
	}
	rows, err := csv.NewReader(bytes.NewReader(data)).ReadAll()
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != 2 {
		t.Fatalf("rows = %d, want header and one record", len(rows))
	}
	row := map[string]string{}
	for i, name := range rows[0] {
		row[name] = rows[1][i]
	}
	return row
}

func TestCaptureCSVEscapesRFC4180AndFormulas(t *testing.T) {
	a := workspaceApp()
	a.customColumns = []customColumn{{Source: sourceRequestHeader, Name: "=X-Title"}}
	f := flow{
		id: 1, method: "=GET", host: "+host", path: "/p", url: "https://api.example.com/p",
		status: 200, millis: 47, bytes: 83, process: "Browser, \"quoted\"\nnext",
		at:        time.Date(2026, 1, 15, 10, 30, 0, 0, time.UTC),
		reqHeader: http.Header{"=X-Title": {"-value"}},
	}
	row := captureTableRow(t, a, f)
	want := map[string]string{
		"#":                   "1",
		"Status":              "200",
		"Method":              "'=GET",
		"Host":                "'+host",
		"Path":                "https://api.example.com/p",
		"Client":              "Browser, \"quoted\"\nnext",
		"Duration":            "47",
		"Size":                "83",
		"Time":                "2026-01-15T10:30:00Z",
		"'=X-Title (request)": "'-value",
	}
	for name, value := range want {
		if row[name] != value {
			t.Fatalf("%q = %q, want %q (row %v)", name, row[name], value, row)
		}
	}
	if _, ok := row["Remote IP"]; !ok {
		t.Fatal("visible Remote IP column is missing")
	}
}

func TestCaptureCSVQuotesCommasQuotesAndNewlines(t *testing.T) {
	a := workspaceApp()
	f := flow{id: 1, method: "GET", host: "api.example.com", path: "/p", url: "https://api.example.com/p",
		process: "Browser, \"quoted\"\nnext", at: time.Date(2026, 1, 15, 10, 30, 0, 0, time.UTC)}
	a.flows = []flow{f}
	table, _ := a.snapshotCaptureTable(a.flows)
	data, err := table.csv(exportOptions{})
	if err != nil {
		t.Fatal(err)
	}
	raw := string(data)
	if !strings.Contains(raw, "\r\n") {
		t.Fatal("CSV did not use RFC 4180 CRLF records")
	}
	if !strings.Contains(raw, `"Browser, ""quoted""`+"\r\nnext\"") {
		t.Fatalf("embedded quote or newline was not quoted: %q", raw)
	}
	rows, err := csv.NewReader(bytes.NewReader(data)).ReadAll()
	if err != nil {
		t.Fatal(err)
	}
	if got := rows[1][6]; got != f.process {
		t.Fatalf("round trip = %q, want %q", got, f.process)
	}
}

func TestCaptureCSVRedactionScopes(t *testing.T) {
	a := workspaceApp()
	headerAuth := customColumn{Source: sourceRequestHeader, Name: "Authorization"}
	headerType := customColumn{Source: sourceResponseHeader, Name: "Content-Type"}
	queryToken := customColumn{Source: sourceQuery, Name: "token"}
	bodyToken := customColumn{Source: sourceResponseJSON, Name: "/token"}
	a.customColumns = []customColumn{headerAuth, headerType, queryToken, bodyToken}
	f := flow{
		id: 1, method: "GET", host: "api.example.com", path: "/p?token=secret&page=2",
		url: "https://user:pass@api.example.com/p?token=secret&page=2", status: 200,
		reqHeader:  http.Header{"Authorization": {"Bearer header-secret"}},
		respHeader: http.Header{"Content-Type": {"application/json"}},
		columnData: map[string]*columnCell{bodyToken.id(): {value: "body-secret"}},
	}
	row := func(options exportOptions) map[string]string {
		a.flows = []flow{f}
		table, _ := a.snapshotCaptureTable(a.flows)
		data, err := table.csv(options)
		if err != nil {
			t.Fatal(err)
		}
		rows, err := csv.NewReader(bytes.NewReader(data)).ReadAll()
		if err != nil {
			t.Fatal(err)
		}
		out := map[string]string{}
		for i, name := range rows[0] {
			out[name] = rows[1][i]
		}
		return out
	}
	raw := row(exportOptions{})
	if raw["Path"] != f.url || raw["Authorization (request)"] != "Bearer header-secret" ||
		raw["?token"] != "secret" || raw["/token (response JSON)"] != "body-secret" {
		t.Fatalf("unredacted row changed: %v", raw)
	}
	safe := row(safeExportOptions())
	if safe["Path"] != "https://api.example.com/p?token=REDACTED&page=REDACTED" ||
		safe["Authorization (request)"] != exportReplacement ||
		safe["Content-Type (response)"] != "application/json" ||
		safe["?token"] != exportReplacement || safe["/token (response JSON)"] != exportReplacement {
		t.Fatalf("safe row = %v", safe)
	}
	headers := row(exportOptions{Headers: true})
	if headers["Authorization (request)"] != exportReplacement || headers["Content-Type (response)"] != "application/json" ||
		headers["Path"] != f.url || headers["?token"] != "secret" || headers["/token (response JSON)"] != "body-secret" {
		t.Fatalf("headers-only row = %v", headers)
	}
	query := row(exportOptions{Query: true})
	if query["Path"] != "https://user:pass@api.example.com/p?token=REDACTED&page=REDACTED" ||
		query["Authorization (request)"] != "Bearer header-secret" || query["?token"] != exportReplacement ||
		query["/token (response JSON)"] != "body-secret" {
		t.Fatalf("query-only row = %v", query)
	}
	bodies := row(exportOptions{Bodies: true})
	if bodies["/token (response JSON)"] != exportReplacement || bodies["Authorization (request)"] != "Bearer header-secret" ||
		bodies["?token"] != "secret" || bodies["Path"] != f.url {
		t.Fatalf("bodies-only row = %v", bodies)
	}
	auth := row(exportOptions{Auth: true})
	if auth["Path"] != "https://api.example.com/p?token=secret&page=2" || auth["Authorization (request)"] != "Bearer header-secret" {
		t.Fatalf("auth-only row = %v", auth)
	}
}

func TestCaptureMarkdownEscapesTableSyntax(t *testing.T) {
	a := workspaceApp()
	cc := customColumn{Source: sourceResponseJSON, Name: "a|b"}
	a.customColumns = []customColumn{cc}
	f := flow{id: 1, method: "GET", host: "api.example.com", path: "/p", url: "https://api.example.com/a|b",
		columnData: map[string]*columnCell{cc.id(): {value: "line1\nline2|x"}}}
	a.flows = []flow{f}
	table, _ := a.snapshotCaptureTable(a.flows)
	text := table.markdown()
	if !strings.Contains(text, `a\|b`) || !strings.Contains(text, `a\|b (response JSON)`) || !strings.Contains(text, `line1<br>line2\|x`) {
		t.Fatalf("markdown did not escape pipes or line breaks:\n%s", text)
	}
	if lines := strings.Count(strings.TrimSuffix(text, "\n"), "\n") + 1; lines != 3 {
		t.Fatalf("table has %d lines, want header, separator and one row:\n%s", lines, text)
	}
}

func TestCaptureColumnsFollowVisibleOrderAndCustomColumns(t *testing.T) {
	a := workspaceApp()
	hidden := customColumn{Source: sourceRequestHeader, Name: "X-Trace"}
	a.hiddenColumns = []string{"host", "remote", hidden.id()}
	a.columns.Order = []string{"method", "id", "url"}
	a.customColumns = []customColumn{{Source: sourceHeader, Name: "X-Trace"}, hidden}
	var ids []string
	for _, col := range a.captureColumns() {
		ids = append(ids, col.id)
	}
	// # keeps the left edge however the rest is arranged.
	want := []string{"id", "method", "url", "status", "client", "duration", "size", "captured", "custom:Header:x-trace"}
	if strings.Join(ids, ",") != strings.Join(want, ",") {
		t.Fatalf("columns = %v, want %v", ids, want)
	}
}

func TestRawHTTPHandlesBinaryLargeMissingAndTrailers(t *testing.T) {
	large := strings.Repeat("a", rawCopyBodyLimit+10)
	f := flow{
		id: 7, method: "POST", host: "api.example.com", path: "/p", url: "https://api.example.com/p",
		status: 200, reqBody: "\x00\xff", reqSize: 2, resBody: large, resSize: len(large),
		reqHeader:  http.Header{"Content-Type": {"application/octet-stream"}, ":method": {"POST"}},
		respHeader: http.Header{"Content-Type": {"text/plain"}},
		reqTrailer: http.Header{"X-Req-Trailer": {"r"}}, resTrailer: http.Header{"X-Res-Trailer": {"s"}},
	}
	text, err := rawHTTP([]flow{f})
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{
		"POST /p HTTP/1.1\r\nHost: api.example.com\r\n",
		"Content-Type: application/octet-stream\r\n",
		"[binary body omitted: 2 bytes]",
		"HTTP/1.1 200 OK\r\n",
		"[body truncated: 1048576 of 1048586 bytes]",
		"[request trailers omitted: X-Req-Trailer]",
		"[response trailers omitted: X-Res-Trailer]",
	} {
		if !strings.Contains(text, want) {
			t.Fatalf("raw text lacks %q:\n%s", want, text)
		}
	}
	if strings.Contains(text, ":method:") {
		t.Fatalf("HTTP/2 pseudo-header leaked into HTTP/1.1 text:\n%s", text)
	}
	noResponse := flow{id: 8, method: "GET", path: "/p"}
	if text, err := rawHTTP([]flow{noResponse}); err != nil || !strings.Contains(text, "[no response received]") {
		t.Fatalf("no-response raw text = %q, %v", text, err)
	}
	incomplete := flow{id: 9, method: "GET", path: "/p", status: 200, resError: "read failed"}
	if text, err := rawHTTP([]flow{incomplete}); err != nil || !strings.Contains(text, "[body incomplete: read failed]") {
		t.Fatalf("incomplete raw text = %q, %v", text, err)
	}
	multi, err := rawHTTP([]flow{f, noResponse})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(multi, "# Capture #7\r\n") || !strings.Contains(multi, "# Capture #8\r\n") {
		t.Fatalf("multi-capture separators missing:\n%s", multi)
	}
}

func TestRawHTTPFallsBackToFormattedHeaders(t *testing.T) {
	f := flow{id: 1, method: "GET", path: "/p", status: 201,
		reqHeaders: "GET /p HTTP/1.1\nX-Request: yes",
		resHeaders: "HTTP/1.1 201 Created\nX-Response: yes"}
	text, err := rawHTTP([]flow{f})
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"X-Request: yes\r\n", "HTTP/1.1 201 Created\r\n", "X-Response: yes\r\n"} {
		if !strings.Contains(text, want) {
			t.Fatalf("raw text lacks %q:\n%s", want, text)
		}
	}
}

func TestRawHTTPLimits(t *testing.T) {
	if _, err := rawHTTP(nil); err == nil {
		t.Fatal("empty selection was accepted")
	}
	flows := make([]flow, rawCopyFlowLimit+1)
	for i := range flows {
		flows[i] = flow{id: i + 1, method: "GET", path: "/p", status: 200}
	}
	if _, err := rawHTTP(flows); err == nil || !strings.Contains(err.Error(), "limited to 50") {
		t.Fatalf("flow limit error = %v", err)
	}
}

func TestCSVSafeKeepsNumbersAndEscapesFormulas(t *testing.T) {
	for _, tc := range []struct{ in, want string }{
		{"-5", "-5"},
		{"-5.5", "-5.5"},
		{"-1e3", "-1e3"},
		{"+5", "+5"},
		{"  -5", "  -5"},
		{"-1+2", "'-1+2"},
		{"-CMD", "'-CMD"},
		{"+SUM(A1)", "'+SUM(A1)"},
		{"=1+1", "'=1+1"},
		{"@cmd", "'@cmd"},
		{"-", "'-"},
	} {
		if got := csvSafe(tc.in); got != tc.want {
			t.Fatalf("csvSafe(%q) = %q, want %q", tc.in, got, tc.want)
		}
	}
}

func TestRawHTTPDropsDecodedBodyFramingHeadersAndKeepsWireOrder(t *testing.T) {
	f := flow{
		id: 1, method: "POST", host: "api.example.com", path: "/p", url: "https://api.example.com/p",
		status: 200, reqBody: "hello", reqSize: 5, resBody: "world", resSize: 5,
		reqHeader: http.Header{
			"Content-Encoding": {"gzip"}, "Content-Length": {"10"}, "Transfer-Encoding": {"chunked"},
			"Content-MD5": {"m"}, "Digest": {"d"}, "Content-Digest": {"cd"}, "Repr-Digest": {"rd"},
			"Z-Last": {"z"}, "X-Request": {"r"},
		},
		respHeader: http.Header{
			"Content-Encoding": {"br"}, "Content-Length": {"20"}, "Transfer-Encoding": {"chunked"},
			"X-Response": {"s"},
		},
	}
	text, err := rawHTTP([]flow{f})
	if err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"Content-Encoding", "Content-Length", "Transfer-Encoding", "Content-MD5", "Digest", "Content-Digest", "Repr-Digest"} {
		if strings.Contains(text, name+":") {
			t.Fatalf("decoded body kept framing header %s:\n%s", name, text)
		}
	}
	for _, want := range []string{"X-Request: r", "Z-Last: z", "X-Response: s"} {
		if !strings.Contains(text, want) {
			t.Fatalf("raw text lacks %q:\n%s", want, text)
		}
	}
	ordered := rawHeaders(http.Header{"Z": {"z"}, "A": {"a"}, "X-Extra": {"x"}}, "GET / HTTP/1.1\nZ: z\nA: a")
	var names []string
	for _, h := range ordered {
		names = append(names, h.name)
	}
	if strings.Join(names, ",") != "Z,A,X-Extra" {
		t.Fatalf("header order = %v, want captured order then missing headers", names)
	}
	fallback := rawHeaders(http.Header{"Z": {"z"}, "A": {"a"}}, "")
	if fallback[0].name != "A" || fallback[1].name != "Z" {
		t.Fatalf("structured-only order = %v, want sorted", fallback)
	}
}

func TestRawHTTPTruncatesUTF8AtCharacterBoundary(t *testing.T) {
	body := strings.Repeat("中", rawCopyBodyLimit/3+10)
	for _, useFile := range []bool{false, true} {
		f := flow{id: 1, method: "GET", path: "/p", status: 200, resSize: len(body)}
		if useFile {
			path := filepath.Join(t.TempDir(), "body.txt")
			if err := os.WriteFile(path, []byte(body), 0600); err != nil {
				t.Fatal(err)
			}
			f.resFile = path
		} else {
			f.resBody = body
		}
		text, err := rawHTTP([]flow{f})
		if err != nil {
			t.Fatal(err)
		}
		if strings.Contains(text, "[binary body omitted") {
			t.Fatalf("file=%t: large UTF-8 body was marked binary", useFile)
		}
		if !strings.Contains(text, "[body truncated:") {
			t.Fatalf("file=%t: missing truncation marker:\n%s", useFile, text)
		}
		if !utf8.ValidString(text) {
			t.Fatalf("file=%t: truncation split a UTF-8 character", useFile)
		}
	}
}
