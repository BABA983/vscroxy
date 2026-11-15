// Package httpfile reads and writes JetBrains HTTP Client and VS Code REST
// Client .http files. It converts to the app's request model and never reads a
// file a request refers to: a body written as "< ./file" is reported instead.
package httpfile

import (
	"fmt"
	"maps"
	"net/url"
	"slices"
	"strings"

	"github.com/baba983/vscroxy/internal/request"
)

const maxFile = 16 << 20

// Document is one .http file. Variables are the file's @name = value lines;
// they become the imported collection's variables.
type Document struct {
	Name      string
	Variables []request.Variable
	Requests  []request.Request
	Warnings  []string
}

// Parse reads one .http file. A request is named by "### Name" or "# @name",
// and its body is the text after the first blank line. Unsupported parts
// (protocol requests, response handlers, file bodies) are reported, not
// silently changed.
func Parse(name string, data []byte) (Document, error) {
	if len(data) > maxFile {
		return Document{}, fmt.Errorf(".http files are limited to 16 MiB")
	}
	text := strings.ReplaceAll(strings.TrimPrefix(string(data), "\ufeff"), "\r\n", "\n")
	text = strings.ReplaceAll(text, "\r", "\n")
	doc := Document{Name: strings.TrimSpace(name)}
	if doc.Name == "" {
		doc.Name = "HTTP file"
	}
	var warnings []string
	var current *builder
	skip := false
	flush := func() {
		if current == nil {
			return
		}
		if r, ok := current.request(doc.Name, len(doc.Requests)+1, &warnings); ok {
			doc.Requests = append(doc.Requests, r)
		}
		current = nil
	}
	for i, line := range strings.Split(text, "\n") {
		trimmed := strings.TrimSpace(line)
		if strings.HasPrefix(line, "###") {
			flush()
			skip = false
			current = &builder{line: i + 1, name: strings.TrimSpace(strings.TrimLeft(trimmed, "#"))}
			continue
		}
		if skip {
			continue
		}
		if current == nil {
			if trimmed == "" || strings.HasPrefix(trimmed, "#") || strings.HasPrefix(trimmed, "//") {
				continue
			}
			if variable, ok := parseVariable(trimmed); ok {
				doc.Variables = setVariable(doc.Variables, variable)
				continue
			}
			current = &builder{line: i + 1}
		}
		if current.add(line, &warnings) {
			flush()
			skip = true
		}
	}
	flush()
	doc.Warnings = dedupe(warnings)
	return doc, nil
}

// builder is one request while it is read: its attributes, request line,
// headers and body.
type builder struct {
	line     int
	name     string
	method   string
	url      string
	settings request.Settings
	headers  []request.Field
	body     []string
	phase    int // 0 attributes, 1 headers, 2 body
	skip     string
}

// add consumes one line and reports whether the request ended (a separator was
// handled by Parse, so this only happens for a protocol request).
func (b *builder) add(line string, warnings *[]string) bool {
	trimmed := strings.TrimSpace(line)
	if b.phase == 2 {
		if b.skip != "" {
			return false
		}
		if strings.HasPrefix(trimmed, "> {%") || strings.HasPrefix(trimmed, "> {{") || strings.HasPrefix(trimmed, "> ./") || strings.HasPrefix(trimmed, "> <") {
			if b.skip == "" {
				*warnings = append(*warnings, fmt.Sprintf("line %d: response handlers are not imported", b.line))
				b.skip = "handler"
			}
			return false
		}
		b.body = append(b.body, line)
		return false
	}
	if trimmed == "" {
		if b.phase == 1 {
			b.phase = 2
		}
		return false
	}
	if strings.HasPrefix(trimmed, "#") || strings.HasPrefix(trimmed, "//") {
		b.attribute(strings.TrimSpace(strings.TrimPrefix(strings.TrimPrefix(trimmed, "#"), "//")), warnings)
		return false
	}
	if b.phase == 0 {
		if variable, ok := parseVariable(trimmed); ok {
			*warnings = append(*warnings, fmt.Sprintf("line %d: a variable inside a request was ignored", b.line))
			_ = variable
			return false
		}
		return b.requestLine(trimmed, warnings)
	}
	// Header lines. A continuation starts with whitespace, as HTTP allows.
	if line[0] == ' ' || line[0] == '\t' {
		if len(b.headers) > 0 {
			last := &b.headers[len(b.headers)-1]
			last.Value = strings.TrimSpace(last.Value + " " + trimmed)
			return false
		}
	}
	name, value, ok := strings.Cut(line, ":")
	if !ok || strings.TrimSpace(name) == "" {
		*warnings = append(*warnings, fmt.Sprintf("line %d: %q is not a header; the body starts here", b.line, trimmed))
		b.phase = 2
		b.body = append(b.body, line)
		return false
	}
	b.headers = append(b.headers, request.Field{Name: strings.TrimSpace(name), Value: strings.TrimSpace(value)})
	return false
}

// attribute reads "# @name value" and the transport attributes .http files
// use. An unknown attribute is reported because it may change the request.
func (b *builder) attribute(text string, warnings *[]string) {
	if !strings.HasPrefix(text, "@") {
		return
	}
	name, value, _ := strings.Cut(text[1:], " ")
	value = strings.TrimSpace(value)
	switch strings.ToLower(strings.TrimSpace(name)) {
	case "name":
		if value != "" {
			b.name = value
		}
	case "timeout":
		var ms int
		if _, err := fmt.Sscanf(value, "%d", &ms); err == nil && ms >= 0 {
			seconds := (ms + 999) / 1000
			b.settings.TimeoutSeconds = &seconds
		} else {
			*warnings = append(*warnings, fmt.Sprintf("line %d: timeout %q is not a whole number of milliseconds", b.line, value))
		}
	case "no-redirect":
		follow := false
		b.settings.FollowRedirects = &follow
	case "no-cookie-jar", "no-log", "no-client-cert":
		*warnings = append(*warnings, fmt.Sprintf("line %d: @%s is not imported", b.line, name))
	default:
		*warnings = append(*warnings, fmt.Sprintf("line %d: @%s is not imported", b.line, name))
	}
}

// requestLine reads "METHOD URL [HTTP/version]". A bare URL is a GET, as the
// clients accept.
func (b *builder) requestLine(line string, warnings *[]string) bool {
	fields := strings.Fields(line)
	if len(fields) == 0 {
		return false
	}
	method, target := strings.ToUpper(fields[0]), ""
	if len(fields) > 1 {
		target = fields[1]
	}
	if target == "" && strings.Contains(fields[0], "://") {
		method, target = "GET", fields[0]
	}
	switch method {
	case "WEBSOCKET", "WS", "WSS", "GRPC", "GRAPHQL", "MQTT", "MCP", "SOCKETIO":
		*warnings = append(*warnings, fmt.Sprintf("line %d: %s requests are not imported from .http", b.line, method))
		b.skip = "protocol"
		return true
	}
	if target == "" {
		*warnings = append(*warnings, fmt.Sprintf("line %d: the request has no URL", b.line))
		return true
	}
	if !validMethod(method) {
		*warnings = append(*warnings, fmt.Sprintf("line %d: %q is not an HTTP method", b.line, method))
		return true
	}
	b.method, b.url, b.phase = method, target, 1
	return false
}

func validMethod(method string) bool {
	if method == "" {
		return false
	}
	for _, r := range method {
		if r < 'A' || r > 'Z' {
			return false
		}
	}
	return true
}

// request finishes a request. A request that never had a request line is
// dropped with a warning.
func (b *builder) request(collection string, index int, warnings *[]string) (request.Request, bool) {
	if b.skip == "protocol" || b.method == "" || b.url == "" {
		return request.Request{}, false
	}
	name := strings.TrimSpace(b.name)
	if name == "" {
		name = fmt.Sprintf("Request %d", index)
	}
	body := strings.Join(b.body, "\n")
	if len(b.body) > 0 {
		body = strings.TrimSuffix(body, "\n")
	}
	if strings.HasPrefix(strings.TrimSpace(body), "< ") || strings.HasPrefix(strings.TrimSpace(body), "<@ ") {
		*warnings = append(*warnings, fmt.Sprintf("line %d: a file body is not imported", b.line))
		body = ""
	}
	r := request.Request{
		Name: name, Collection: collection, Method: b.method, URL: b.url,
		Body: body, HeaderList: b.headers, Settings: b.settings,
	}
	r.Headers = headerBlock(b.headers)
	return r, true
}

// headerBlock writes the header rows as the request's Key: Value block.
func headerBlock(rows []request.Field) string {
	var b strings.Builder
	for _, row := range rows {
		if row.Name != "" {
			b.WriteString(row.Name + ": " + row.Value + "\n")
		}
	}
	return strings.TrimSuffix(b.String(), "\n")
}

// parseVariable reads "@name = value".
func parseVariable(line string) (request.Variable, bool) {
	if !strings.HasPrefix(line, "@") {
		return request.Variable{}, false
	}
	name, value, ok := strings.Cut(line[1:], "=")
	if !ok {
		return request.Variable{}, false
	}
	name = strings.TrimSpace(name)
	if name == "" || strings.ContainsAny(name, " \t") {
		return request.Variable{}, false
	}
	return request.Variable{Name: name, Initial: strings.TrimSpace(value)}, true
}

func setVariable(list []request.Variable, variable request.Variable) []request.Variable {
	for i := range list {
		if list[i].Name == variable.Name {
			list[i] = variable
			return list
		}
	}
	return append(list, variable)
}

func dedupe(list []string) []string {
	var out []string
	seen := map[string]bool{}
	for _, line := range list {
		if line == "" || seen[line] {
			continue
		}
		seen[line] = true
		out = append(out, line)
	}
	return out
}

// Write renders requests and the collection's variables as one .http file.
// It applies auth and body modes, but never reads a file a request refers to:
// a binary or multipart body, or an auth that cannot be applied without one,
// is left out with a warning.
func Write(name string, requests []request.Request, variables []request.Variable) ([]byte, []string, error) {
	var b strings.Builder
	var warnings []string
	writtenVars := 0
	for _, variable := range variables {
		if variable.Disabled || strings.TrimSpace(variable.Name) == "" {
			continue
		}
		if strings.ContainsAny(variable.Name, " \t") {
			warnings = append(warnings, fmt.Sprintf("variable %q has a space and was not exported", variable.Name))
			continue
		}
		if strings.ContainsAny(variable.Initial, "\r\n") {
			warnings = append(warnings, fmt.Sprintf("variable %q has a line break and was not exported", variable.Name))
			continue
		}
		b.WriteString("@" + variable.Name + " = " + variable.Initial + "\n")
		writtenVars++
	}
	if writtenVars > 0 {
		b.WriteString("\n")
	}
	written := 0
	for _, r := range requests {
		if r.Protocol != "" {
			warnings = append(warnings, fmt.Sprintf("%s requests are not exported to .http", r.Protocol))
			continue
		}
		req, problem := prepare(r)
		if problem != "" {
			warnings = append(warnings, fmt.Sprintf("%s: %s", r.Name, problem))
		}
		if req == nil {
			continue
		}
		if written > 0 {
			b.WriteString("\n")
		}
		written++
		if strings.ContainsAny(req.URL, " \t") {
			warnings = append(warnings, fmt.Sprintf("%s: the URL has whitespace, which splits the request line", r.Name))
		}
		name := strings.TrimSpace(r.Name)
		if name == "" {
			name = fmt.Sprintf("Request %d", written)
		}
		name = strings.Join(strings.Fields(name), " ")
		b.WriteString("### " + name + "\n")
		if r.Settings.TimeoutSeconds != nil {
			fmt.Fprintf(&b, "# @timeout %d\n", *r.Settings.TimeoutSeconds*1000)
		}
		if r.Settings.FollowRedirects != nil && !*r.Settings.FollowRedirects {
			b.WriteString("# @no-redirect\n")
		}
		b.WriteString(req.Method + " " + req.URL + "\n")
		names := slices.Sorted(maps.Keys(req.Header))
		for _, header := range names {
			if strings.EqualFold(header, "Content-Length") || strings.EqualFold(header, "Transfer-Encoding") || strings.EqualFold(header, "Host") {
				continue
			}
			for _, value := range req.Header[header] {
				b.WriteString(header + ": " + value + "\n")
			}
		}
		if req.Body != "" {
			b.WriteString("\n")
			b.WriteString(req.Body)
			if !strings.HasSuffix(req.Body, "\n") {
				b.WriteString("\n")
			}
			if strings.Contains("\n"+req.Body, "\n###") {
				warnings = append(warnings, fmt.Sprintf("%s: the body has a line starting with ###, which splits the request", r.Name))
			}
		}
	}
	return []byte(b.String()), dedupe(warnings), nil
}

// prepare applies auth and structured bodies. Binary and multipart bodies are
// not read: their request keeps its method, URL and headers but no body.
func prepare(r request.Request) (*request.Prepared, string) {
	if r.BodyMode == "Binary" || r.BodyMode == "Multipart" {
		req := &request.Prepared{Method: strings.ToUpper(strings.TrimSpace(r.Method)), URL: strings.TrimSpace(r.URL),
			Header: request.ParseHeaderBlock(r.Headers)}
		if req.Method == "" {
			req.Method = "GET"
		}
		if req.URL == "" {
			return nil, "the request has no URL"
		}
		problem := ""
		if r.BodyMode == "Binary" {
			problem = "binary bodies are not exported"
		} else {
			problem = "multipart bodies are not exported"
		}
		if r.Auth.Type != "" && r.Auth.Type != request.AuthInherit && r.Auth.Type != "None" {
			problem += "; auth was not applied"
		}
		return req, problem
	}
	req, err := r.Prepare(nil)
	if err != nil {
		fallback := &request.Prepared{Method: strings.ToUpper(strings.TrimSpace(r.Method)), URL: strings.TrimSpace(r.URL),
			Header: request.ParseHeaderBlock(r.Headers), Body: r.Body}
		if fallback.Method == "" {
			fallback.Method = "GET"
		}
		if fallback.URL == "" {
			return nil, err.Error()
		}
		return fallback, "auth or body settings were not applied: " + err.Error()
	}
	if _, err := url.Parse(req.URL); err != nil {
		return nil, "the URL is invalid: " + err.Error()
	}
	return req, ""
}
