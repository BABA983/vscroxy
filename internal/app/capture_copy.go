package app

import (
	"bytes"
	"cmp"
	"encoding/csv"
	"errors"
	"fmt"
	"io"
	"maps"
	"net/http"
	"net/url"
	"os"
	"slices"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/egoist/mygo/ui"
)

const (
	captureTableFlowLimit = 200
	rawCopyFlowLimit      = 50
	rawCopyBodyLimit      = 1 << 20
	rawCopyTotalLimit     = 4 << 20
)

// captureColumn is one visible column of the table, in the order the user
// arranged it. Custom columns keep the definition that produced their cells.
type captureColumn struct {
	id, title string
	custom    *customColumn
}

// captureColumns mirrors the table: the built-ins the user did not hide, then
// custom columns, arranged by the saved column order.
func (a *app) captureColumns() []captureColumn {
	columns := []captureColumn{
		{id: "id", title: "#"},
		{id: "status", title: "Status"},
		{id: "method", title: "Method"},
		{id: "host", title: "Host"},
		{id: "remote", title: "Remote IP"},
		{id: "url", title: "Path"},
		{id: "client", title: "Client"},
		{id: "duration", title: "Duration"},
		{id: "size", title: "Size"},
		{id: "captured", title: "Time"},
	}
	columns = slices.DeleteFunc(columns, func(col captureColumn) bool { return slices.Contains(a.hiddenColumns, col.id) })
	for i := range a.customColumns {
		cc := a.customColumns[i]
		columns = append(columns, captureColumn{id: cc.id(), title: cc.title(), custom: &cc})
	}
	ordered := make([]captureColumn, 0, len(columns))
	used := make(map[string]bool, len(columns))
	for _, id := range a.columns.Order {
		i := slices.IndexFunc(columns, func(col captureColumn) bool { return col.id == id })
		if i >= 0 && !used[id] {
			ordered = append(ordered, columns[i])
			used[id] = true
		}
	}
	for _, col := range columns {
		if !used[col.id] {
			ordered = append(ordered, col)
		}
	}
	return ordered
}

type redactScope uint8

const (
	redactNone redactScope = iota
	redactURL
	redactHeader
	redactQuery
	redactBody
)

type captureCell struct {
	value string
	scope redactScope
	name  string // header name, for redactHeader
}

func plainCell(value string) captureCell { return captureCell{value: value} }

// captureCellValue is the underlying value the table shows, not its color or
// bar. Numeric fields stay numeric for CSV consumers; empty values stay empty.
func (a *app) captureCellValue(f flow, col captureColumn) captureCell {
	switch col.id {
	case "id":
		return plainCell(strconv.Itoa(f.id))
	case "status":
		switch {
		case f.conn.error != "":
			return plainCell("Failed")
		case f.kind == "connect":
			return plainCell("Tunnel")
		case f.status != 0:
			return plainCell(strconv.Itoa(f.status))
		}
		return captureCell{}
	case "method":
		return plainCell(f.method)
	case "host":
		return plainCell(f.host)
	case "remote":
		return plainCell(f.conn.remote)
	case "url":
		return captureCell{value: cmp.Or(f.url, f.path, "/"), scope: redactURL}
	case "client":
		return plainCell(f.process)
	case "duration":
		return plainCell(strconv.Itoa(f.millis))
	case "size":
		return plainCell(strconv.Itoa(f.bytes))
	case "captured":
		if f.at.IsZero() {
			return captureCell{}
		}
		return plainCell(f.at.UTC().Format(time.RFC3339))
	}
	if col.custom == nil {
		return captureCell{}
	}
	cc := *col.custom
	switch {
	case cc.Source == sourceRequestJSON || cc.Source == sourceResponseJSON:
		return captureCell{value: a.columnValue(cc, f), scope: redactBody}
	case cc.Source == sourceGraphQL:
		return plainCell(a.columnValue(cc, f))
	case cc.Source == sourceQuery:
		return captureCell{value: cc.value(f), scope: redactQuery}
	default:
		return captureCell{value: cc.value(f), scope: redactHeader, name: cc.Name}
	}
}

type captureTable struct {
	header []string
	rows   [][]captureCell
}

// snapshotCaptureTable reads the UI-owned column cache on the UI thread, so a
// clipboard or export worker only sees immutable cells.
func (a *app) snapshotCaptureTable(flows []flow) (captureTable, []string) {
	columns := a.captureColumns()
	table := captureTable{header: make([]string, len(columns)), rows: make([][]captureCell, 0, len(flows))}
	for i, col := range columns {
		table.header[i] = col.title
	}
	pending := 0
	for _, f := range flows {
		row := make([]captureCell, len(columns))
		for i, col := range columns {
			row[i] = a.captureCellValue(f, col)
			if col.custom != nil && col.custom.derived() {
				if cell := f.columnData[col.custom.id()]; cell != nil && cell.pending {
					pending++
				}
			}
		}
		table.rows = append(table.rows, row)
	}
	var warnings []string
	if pending > 0 {
		warnings = append(warnings, fmt.Sprintf("%d body-derived cells were still loading and are empty; export again after they finish.", pending))
	}
	return table, warnings
}

// markdown writes a GitHub-flavored table. Pipes and line breaks are escaped
// so a value cannot split a row or end the table.
func (t captureTable) markdown() string {
	var b strings.Builder
	b.WriteString("| ")
	for i, title := range t.header {
		if i > 0 {
			b.WriteString(" | ")
		}
		b.WriteString(markdownCell(title))
	}
	b.WriteString(" |\n|")
	for range t.header {
		b.WriteString(" --- |")
	}
	b.WriteString("\n")
	for _, row := range t.rows {
		b.WriteString("| ")
		for i, cell := range row {
			if i > 0 {
				b.WriteString(" | ")
			}
			b.WriteString(markdownCell(cell.value))
		}
		b.WriteString(" |\n")
	}
	return b.String()
}

func markdownCell(value string) string {
	value = strings.ReplaceAll(value, "\\", "\\\\")
	value = strings.ReplaceAll(value, "|", "\\|")
	value = strings.ReplaceAll(value, "\r\n", "<br>")
	value = strings.ReplaceAll(value, "\n", "<br>")
	value = strings.ReplaceAll(value, "\r", "<br>")
	return value
}

// csv writes RFC 4180 rows with CRLF line endings. Formula-leading cells are
// prefixed with an apostrophe after redaction, so neither a URL query nor a
// body field can become a spreadsheet formula.
func (t captureTable) csv(options exportOptions) ([]byte, error) {
	var b bytes.Buffer
	w := csv.NewWriter(&b)
	w.UseCRLF = true
	header := make([]string, len(t.header))
	for i, title := range t.header {
		header[i] = csvSafe(title)
	}
	if err := w.Write(header); err != nil {
		return nil, err
	}
	row := make([]string, len(t.header))
	for _, cells := range t.rows {
		for i := range row {
			if i < len(cells) {
				row[i] = csvSafe(cells[i].exportValue(options))
			} else {
				row[i] = ""
			}
		}
		if err := w.Write(row); err != nil {
			return nil, err
		}
	}
	w.Flush()
	return b.Bytes(), w.Error()
}

// exportValue applies the same field scopes as the JSON export dialog to the
// columns that read those fields.
func (c captureCell) exportValue(options exportOptions) string {
	switch c.scope {
	case redactURL:
		return redactExportURL(c.value, options)
	case redactHeader:
		if options.Headers && c.value != "" && !exportPublicHeader(c.name) {
			return exportReplacement
		}
	case redactQuery:
		if options.Query && c.value != "" {
			return exportReplacement
		}
	case redactBody:
		if options.Bodies && c.value != "" {
			return exportReplacement
		}
	}
	return c.value
}

// csvSafe prevents spreadsheet formula injection. Leading whitespace is
// trimmed for the check because spreadsheet apps commonly ignore it.
func csvSafe(value string) string {
	trimmed := strings.TrimLeft(value, " \t\r\n")
	if trimmed == "" {
		return value
	}
	switch trimmed[0] {
	case '+', '-':
		if _, err := strconv.ParseFloat(trimmed, 64); err == nil {
			return value // A complete number such as -5 stays a number.
		}
		return "'" + value
	case '=', '@':
		return "'" + value
	}
	return value
}

// captureCopyActions adds the table-shaped formats to the existing Copy as
// menu. Raw HTTP is bounded more tightly than a table because each row carries
// both messages.
func (a *app) captureCopyActions(c *ui.Context, flows []flow) []action {
	ok := len(flows) > 0 && len(flows) <= captureTableFlowLimit
	for _, f := range flows {
		ok = ok && f.kind == ""
	}
	rawOK := ok && len(flows) <= rawCopyFlowLimit
	reason := fmt.Sprintf("Select 1–%d HTTP requests", captureTableFlowLimit)
	rawReason := fmt.Sprintf("Select 1–%d HTTP requests", rawCopyFlowLimit)
	return []action{
		do("flow.copy-markdown", "Copy as Markdown table", func() { a.copyMarkdown(c, flows) }).when(ok, reason),
		do("flow.copy-csv", "Copy as CSV", func() { a.copyCSV(c, flows) }).when(ok, reason),
		do("flow.copy-raw", "Copy as raw HTTP", func() { a.copyRaw(c, flows) }).when(rawOK, rawReason),
		do("flow.copy-har", "Copy as HAR (sanitized)", func() { a.copyHAR(c, flows, false) }).when(ok, reason),
		do("flow.copy-har-raw", "Copy as HAR (with sensitive data)", func() { a.copyHAR(c, flows, true) }).when(ok, reason),
	}
}

func (a *app) copyMarkdown(c *ui.Context, flows []flow) {
	table, _ := a.snapshotCaptureTable(flows)
	a.copyText(c, "Markdown table", func() (string, error) { return table.markdown(), nil })
}

func (a *app) copyCSV(c *ui.Context, flows []flow) {
	table, _ := a.snapshotCaptureTable(flows)
	a.copyText(c, "CSV", func() (string, error) {
		data, err := table.csv(exportOptions{})
		if err != nil {
			return "", err
		}
		return string(data), nil
	})
}

func (a *app) copyRaw(c *ui.Context, flows []flow) {
	snapshot := make([]flow, len(flows))
	for i, f := range flows {
		f.reqHeader, f.respHeader = f.reqHeader.Clone(), f.respHeader.Clone()
		f.reqTrailer, f.resTrailer = f.reqTrailer.Clone(), f.resTrailer.Clone()
		snapshot[i] = f
	}
	a.copyText(c, "Raw HTTP", func() (string, error) { return rawHTTP(snapshot) })
}

type rawHeader struct{ name, value string }

// Decoded bodies are reframed, so these wire headers no longer describe the
// bytes. The list matches capture.clearBodyEncoding.
var rawFramingHeaders = map[string]bool{
	"content-length":    true,
	"transfer-encoding": true,
	"content-encoding":  true,
	"content-md5":       true,
	"digest":            true,
	"content-digest":    true,
	"repr-digest":       true,
}

// rawHeaders keeps the captured wire order when the formatted block has it,
// then adds any structured headers the block lacks. Structured-only captures
// have no order left, so their names are sorted.
func rawHeaders(structured http.Header, formatted string) []rawHeader {
	headers := formattedRawHeaders(formatted)
	seen := make(map[string]bool, len(headers))
	for _, h := range headers {
		seen[strings.ToLower(h.name)] = true
	}
	for _, name := range slices.Sorted(maps.Keys(structured)) {
		lower := strings.ToLower(name)
		if strings.HasPrefix(name, ":") || seen[lower] {
			continue // HTTP/2 pseudo-headers are not HTTP/1.1 header lines.
		}
		for _, value := range structured[name] {
			headers = append(headers, rawHeader{name, value})
		}
	}
	return slices.DeleteFunc(headers, func(h rawHeader) bool { return rawFramingHeaders[strings.ToLower(h.name)] })
}

func formattedRawHeaders(formatted string) []rawHeader {
	var headers []rawHeader
	for i, line := range strings.Split(strings.ReplaceAll(formatted, "\r\n", "\n"), "\n") {
		if strings.TrimSpace(line) == "" || i == 0 && (strings.HasPrefix(line, "HTTP/") || strings.Contains(line, " HTTP/")) {
			continue
		}
		name, value, ok := strings.Cut(line, ":")
		if !ok {
			continue
		}
		name, value = strings.TrimSpace(name), strings.TrimSpace(value)
		if name != "" && !strings.HasPrefix(name, ":") {
			headers = append(headers, rawHeader{name, value})
		}
	}
	return headers
}

func rawHTTP(flows []flow) (string, error) {
	if len(flows) == 0 {
		return "", errors.New("select at least one HTTP request")
	}
	if len(flows) > rawCopyFlowLimit {
		return "", fmt.Errorf("raw HTTP copy is limited to %d requests", rawCopyFlowLimit)
	}
	var b strings.Builder
	for i, f := range flows {
		if f.kind != "" {
			return "", errors.New("raw HTTP copy requires HTTP requests without streams or tunnels")
		}
		if i > 0 {
			b.WriteString("\r\n\r\n")
		}
		if len(flows) > 1 {
			fmt.Fprintf(&b, "# Capture #%d\r\n", f.id)
		}
		request, response := rawRequest(f), rawResponse(f)
		b.WriteString(request)
		b.WriteString("\r\n\r\n")
		b.WriteString(response)
		if b.Len() > rawCopyTotalLimit {
			return "", fmt.Errorf("raw HTTP copy exceeds %d MiB; select fewer requests", rawCopyTotalLimit>>20)
		}
	}
	return b.String(), nil
}

func rawRequest(f flow) string {
	var b strings.Builder
	method := cmp.Or(f.method, "GET")
	target := f.path
	host := f.host
	if parsed, err := url.Parse(f.url); err == nil {
		if target == "" {
			target = parsed.RequestURI()
		}
		if host == "" {
			host = parsed.Host
		}
	}
	if target == "" {
		target = "/"
	}
	fmt.Fprintf(&b, "%s %s HTTP/1.1\r\n", method, target)
	headers := rawHeaders(f.reqHeader, f.reqHeaders)
	hasHost := slices.ContainsFunc(headers, func(h rawHeader) bool { return strings.EqualFold(h.name, "Host") })
	if host != "" && !hasHost {
		fmt.Fprintf(&b, "Host: %s\r\n", host)
	}
	for _, h := range headers {
		fmt.Fprintf(&b, "%s: %s\r\n", h.name, h.value)
	}
	b.WriteString("\r\n")
	b.WriteString(rawBody(f, false))
	if len(f.reqTrailer) > 0 {
		fmt.Fprintf(&b, "\r\n[request trailers omitted: %s]", strings.Join(slices.Sorted(maps.Keys(f.reqTrailer)), ", "))
	}
	return b.String()
}

func rawResponse(f flow) string {
	if f.status == 0 {
		return "[no response received]"
	}
	var b strings.Builder
	fmt.Fprintf(&b, "HTTP/1.1 %d %s\r\n", f.status, http.StatusText(f.status))
	for _, h := range rawHeaders(f.respHeader, f.resHeaders) {
		fmt.Fprintf(&b, "%s: %s\r\n", h.name, h.value)
	}
	b.WriteString("\r\n")
	b.WriteString(rawBody(f, true))
	if len(f.resTrailer) > 0 {
		fmt.Fprintf(&b, "\r\n[response trailers omitted: %s]", strings.Join(slices.Sorted(maps.Keys(f.resTrailer)), ", "))
	}
	return b.String()
}

// rawBody shows a text body, and says exactly why it is missing, cut short or
// replaced. Binary bytes are never copied into a text clipboard.
func rawBody(f flow, response bool) string {
	head, file, size, problem := f.reqBody, f.reqFile, f.reqSize, f.reqError
	if response {
		head, file, size, problem = f.resBody, f.resFile, f.resSize, f.resError
	}
	if problem != "" {
		return "[body incomplete: " + problem + "]"
	}
	body := head
	if file != "" {
		reader, err := os.Open(file)
		if err != nil {
			return "[body unavailable]"
		}
		defer reader.Close()
		data, err := io.ReadAll(io.LimitReader(reader, rawCopyBodyLimit+1))
		if err != nil {
			return "[body unavailable]"
		}
		body = string(data)
		if info, err := reader.Stat(); err == nil && info.Size() > int64(len(body)) {
			size = int(info.Size())
		}
	}
	if size == 0 {
		size = len(body)
	}
	visible := body
	truncated := len(body) > rawCopyBodyLimit
	if truncated {
		// A byte limit can land inside a multi-byte character. Cut before the
		// partial rune so a large UTF-8 body is not mistaken for binary data.
		cut := rawCopyBodyLimit
		for cut > 0 && !utf8.RuneStart(body[cut]) {
			cut--
		}
		if cut == 0 {
			return fmt.Sprintf("[binary body omitted: %d bytes]", size)
		}
		visible = body[:cut]
	}
	if !rawText(visible) {
		return fmt.Sprintf("[binary body omitted: %d bytes]", size)
	}
	if truncated {
		return visible + fmt.Sprintf("\r\n[body truncated: %d of %d bytes]", len(visible), size)
	}
	if len(body) < size {
		return body + fmt.Sprintf("\r\n[body incomplete: %d of %d bytes]", len(body), size)
	}
	return body
}

// rawText rejects bytes that would be corrupted by a text clipboard, not just
// invalid UTF-8: NUL and other control bytes are binary too.
func rawText(body string) bool {
	if !utf8.ValidString(body) || strings.ContainsRune(body, 0) {
		return false
	}
	return !strings.ContainsFunc(body, func(r rune) bool {
		return r < 0x20 && r != '\t' && r != '\n' && r != '\r'
	})
}
