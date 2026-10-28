package request

import (
	"cmp"
	"errors"
	"fmt"
	"maps"
	"net/http"
	"net/url"
	"slices"
	"strconv"
	"strings"
	"unicode/utf8"

	"golang.org/x/net/http/httpguts"
)

// SnippetLanguages lists the languages Snippet writes, in menu order.
var SnippetLanguages = []string{"JavaScript (fetch)", "Node.js (axios)", "Python (requests)", "Go (net/http)", "HTTPie"}

// snippetSkip are the headers every client computes itself. Host is kept
// only when it names another host than the URL does.
var snippetSkip = map[string]bool{
	"Content-Length":    true,
	"Host":              true,
	"Connection":        true,
	"Transfer-Encoding": true,
	"Proxy-Connection":  true,
	"Keep-Alive":        true,
	"Te":                true,
	"Upgrade":           true,
}

// snippetHeader is a header and all its values, in the order sent.
type snippetHeader struct {
	name   string
	values []string
}

// Snippet writes code that sends req in language.
func Snippet(language string, req *Prepared) (string, error) {
	if !utf8.ValidString(req.Body) {
		return "", errors.New("the body is binary; copy it as cURL instead")
	}
	method := cmp.Or(req.Method, "GET")
	if !httpguts.ValidHeaderFieldName(method) {
		return "", fmt.Errorf("invalid HTTP method %q", method)
	}
	var headers []snippetHeader
	for _, name := range slices.Sorted(maps.Keys(req.Header)) {
		values := req.Header[name]
		if !httpguts.ValidHeaderFieldName(name) {
			return "", fmt.Errorf("invalid request header name %q", name)
		}
		for _, v := range values {
			if !httpguts.ValidHeaderFieldValue(v) {
				return "", fmt.Errorf("invalid value for request header %s", name)
			}
		}
		canonical := http.CanonicalHeaderKey(name)
		if len(values) == 0 || snippetSkip[canonical] && !(canonical == "Host" && otherHost(req.URL, values[0])) {
			continue
		}
		if canonical == "Host" {
			values = values[:1]
		}
		headers = append(headers, snippetHeader{name, values})
	}
	switch language {
	case "JavaScript (fetch)":
		return fetchSnippet(method, req, headers)
	case "Node.js (axios)":
		return axiosSnippet(method, req, headers), nil
	case "Python (requests)":
		return pythonSnippet(method, req, headers)
	case "Go (net/http)":
		return goSnippet(method, req, headers), nil
	case "HTTPie":
		return httpieSnippet(method, req, headers)
	}
	return "", fmt.Errorf("no code snippet for %q", language)
}

// otherHost reports whether a Host header names another host than rawURL.
func otherHost(rawURL, host string) bool {
	u, err := url.Parse(rawURL)
	return err != nil || !strings.EqualFold(u.Host, host)
}

// has reports whether the request carries the header named.
func has(headers []snippetHeader, name string) bool {
	return slices.ContainsFunc(headers, func(h snippetHeader) bool { return strings.EqualFold(h.name, name) })
}

// joined is a header's values as the one value a client that cannot repeat
// headers sends, the way HTTP allows a list to be combined.
func joined(h snippetHeader, language string) (string, error) {
	switch {
	case len(h.values) == 1:
		return h.values[0], nil
	case strings.EqualFold(h.name, "Cookie"):
		return strings.Join(h.values, "; "), nil
	case strings.EqualFold(h.name, "Set-Cookie"):
		return "", fmt.Errorf("%s cannot repeat the Set-Cookie header; copy it as cURL instead", language)
	}
	return strings.Join(h.values, ", "), nil
}

func fetchSnippet(method string, req *Prepared, headers []snippetHeader) (string, error) {
	if req.Body != "" && (strings.EqualFold(method, "GET") || strings.EqualFold(method, "HEAD")) {
		return "", fmt.Errorf("fetch cannot send a body with %s; copy it as cURL instead", method)
	}
	var b strings.Builder
	b.WriteString("const response = await fetch(" + cQuote(req.URL, true, false) + ", {\n")
	b.WriteString("  method: " + cQuote(method, true, false) + ",\n")
	if len(headers) > 0 {
		b.WriteString("  headers: {\n")
		for _, h := range headers {
			if strings.EqualFold(h.name, "Host") {
				return "", errors.New("fetch cannot set the Host header; copy it as cURL instead")
			}
			value, err := joined(h, "fetch")
			if err != nil {
				return "", err
			}
			b.WriteString("    " + cQuote(h.name, true, false) + ": " + cQuote(value, true, true) + ",\n")
		}
		b.WriteString("  },\n")
	}
	if req.Body != "" {
		body := jsString(req.Body)
		if !has(headers, "Content-Type") {
			// fetch labels a string body text/plain; bytes go unlabeled.
			body = "new TextEncoder().encode(" + body + ")"
		}
		b.WriteString("  body: " + body + ",\n")
	}
	b.WriteString("  redirect: \"manual\",\n")
	b.WriteString("});\n")
	b.WriteString("console.log(response.status, await response.text());\n")
	return b.String(), nil
}

func axiosSnippet(method string, req *Prepared, headers []snippetHeader) string {
	var b strings.Builder
	b.WriteString("const axios = require(\"axios\");\n\n")
	b.WriteString("axios.request({\n")
	b.WriteString("  method: " + cQuote(method, true, false) + ",\n")
	b.WriteString("  url: " + cQuote(req.URL, true, false) + ",\n")
	// axios labels POST, PUT and PATCH as a form unless told the request has
	// no Content-Type.
	noType := !has(headers, "Content-Type") && slices.Contains([]string{"post", "put", "patch"}, strings.ToLower(method))
	if len(headers) > 0 || noType {
		b.WriteString("  headers: {\n")
		typed := false
		for _, h := range headers {
			if noType && !typed && strings.ToLower(h.name) > "content-type" {
				b.WriteString("    \"Content-Type\": false,\n")
				typed = true
			}
			b.WriteString("    " + cQuote(h.name, true, false) + ": ")
			if len(h.values) == 1 {
				b.WriteString(cQuote(h.values[0], true, true))
			} else {
				// Node sends each element of an array as its own header line.
				quoted := make([]string, len(h.values))
				for i, v := range h.values {
					quoted[i] = cQuote(v, true, true)
				}
				b.WriteString("[" + strings.Join(quoted, ", ") + "]")
			}
			b.WriteString(",\n")
		}
		if noType && !typed {
			b.WriteString("    \"Content-Type\": false,\n")
		}
		b.WriteString("  },\n")
	}
	if req.Body != "" {
		b.WriteString("  data: " + jsString(req.Body) + ",\n")
	}
	b.WriteString("  transformRequest: (data) => data,\n")
	b.WriteString("  transformResponse: (data) => data,\n")
	b.WriteString("  validateStatus: () => true,\n")
	b.WriteString("  maxRedirects: 0,\n")
	b.WriteString("}).then((response) => {\n")
	b.WriteString("  console.log(response.status, response.data);\n")
	b.WriteString("});\n")
	return b.String()
}

func pythonSnippet(method string, req *Prepared, headers []snippetHeader) (string, error) {
	var b strings.Builder
	b.WriteString("import requests\n\n")
	b.WriteString("url = " + cQuote(req.URL, false, false) + "\n")
	args := ""
	if len(headers) > 0 {
		b.WriteString("headers = {\n")
		for _, h := range headers {
			value, err := joined(h, "Python requests")
			if err != nil {
				return "", err
			}
			b.WriteString("    " + cQuote(h.name, false, false) + ": " + cQuote(value, false, true) + ",\n")
		}
		b.WriteString("}\n")
		args += ", headers=headers"
	}
	if req.Body != "" {
		// Bytes, so requests sends the body as it is and labels nothing.
		b.WriteString("data = " + pythonString(req.Body) + ".encode()\n")
		args += ", data=data"
	}
	b.WriteString("\nresponse = requests.request(" + cQuote(method, false, false) + ", url" + args + ", allow_redirects=False)\n")
	b.WriteString("print(response.status_code, response.text)\n")
	return b.String(), nil
}

func goSnippet(method string, req *Prepared, headers []snippetHeader) string {
	var b strings.Builder
	b.WriteString("package main\n\nimport (\n\t\"fmt\"\n\t\"io\"\n\t\"net/http\"\n")
	body := "nil"
	if req.Body != "" {
		b.WriteString("\t\"strings\"\n")
		body = "strings.NewReader(" + goString(req.Body) + ")"
	}
	b.WriteString(")\n\nfunc main() {\n")
	b.WriteString("\treq, err := http.NewRequest(" + strconv.Quote(method) + ", " + strconv.Quote(req.URL) + ", " + body + ")\n")
	b.WriteString("\tif err != nil {\n\t\tpanic(err)\n\t}\n")
	for _, h := range headers {
		if strings.EqualFold(h.name, "Host") {
			// net/http sends req.Host and ignores a Host header.
			b.WriteString("\treq.Host = " + strconv.Quote(h.values[0]) + "\n")
			continue
		}
		for _, v := range h.values {
			b.WriteString("\treq.Header.Add(" + strconv.Quote(h.name) + ", " + strconv.Quote(v) + ")\n")
		}
	}
	b.WriteString(`	client := &http.Client{
		CheckRedirect: func(*http.Request, []*http.Request) error {
			return http.ErrUseLastResponse
		},
	}
	resp, err := client.Do(req)
	if err != nil {
		panic(err)
	}
	defer resp.Body.Close()
	data, err := io.ReadAll(resp.Body)
	if err != nil {
		panic(err)
	}
	fmt.Println(resp.Status)
	fmt.Println(string(data))
}
`)
	return b.String()
}

func httpieSnippet(method string, req *Prepared, headers []snippetHeader) (string, error) {
	args := []string{"http --ignore-stdin " + shellWord(method) + " " + shellWord(req.URL)}
	for _, h := range headers {
		for _, v := range h.values {
			if v != strings.TrimSpace(v) {
				return "", fmt.Errorf("HTTPie trims the value of the %s header; copy it as cURL instead", h.name)
			}
			// HTTPie reads a backslash before a separator as an escape, and
			// a value starting with = or @ as another kind of item.
			for i := range len(v) - 1 {
				if v[i] == '\\' && strings.IndexByte(":;=@", v[i+1]) >= 0 {
					return "", fmt.Errorf("HTTPie cannot send the value of the %s header; copy it as cURL instead", h.name)
				}
			}
			switch {
			case v == "":
				args = append(args, shellWord(h.name+";"))
			case v[0] == '=' || v[0] == '@':
				args = append(args, shellWord(h.name+`:\`+v))
			default:
				args = append(args, shellWord(h.name+":"+v))
			}
		}
	}
	// An empty value stops HTTPie sending its own default.
	defaults := []string{"Accept", "Accept-Encoding", "User-Agent"}
	if req.Body != "" {
		defaults = append(defaults, "Content-Type")
	}
	for _, name := range defaults {
		if !has(headers, name) {
			args = append(args, name+":")
		}
	}
	control := func(r rune) bool { return r < ' ' && r != '\n' && r != '\t' || r == 0x7f }
	if req.Body == "" || !strings.ContainsFunc(req.Body, control) {
		if req.Body != "" {
			args = append(args, "--raw="+shellQuote(req.Body))
		}
		return strings.Join(args, " \\\n  ") + "\n", nil
	}
	// A terminal can change a pasted carriage return or escape even inside
	// quotes, so printf writes those from escapes to HTTPie's stdin.
	var b strings.Builder
	for _, r := range req.Body {
		switch {
		case r == '\\':
			b.WriteString(`\\`)
		case r == '\r':
			b.WriteString(`\r`)
		case control(r):
			fmt.Fprintf(&b, `\0%03o`, r)
		default:
			b.WriteRune(r)
		}
	}
	args[0] = strings.Replace(args[0], " --ignore-stdin", "", 1)
	return "printf '%b' " + shellQuote(b.String()) + " | " + strings.Join(args, " \\\n  ") + "\n", nil
}

// cQuote writes s as a double-quoted JavaScript or Python string. Header
// values are bytes on the wire, and both languages send a string's
// characters below 256 as single bytes, so latin1 escapes each byte above
// ASCII on its own.
func cQuote(s string, js, latin1 bool) string {
	var b strings.Builder
	b.WriteByte('"')
	if latin1 {
		for i := range len(s) {
			if s[i] >= utf8.RuneSelf {
				fmt.Fprintf(&b, `\x%02x`, s[i])
			} else {
				b.WriteString(cEscape(rune(s[i]), js, false))
			}
		}
	} else {
		for _, r := range s {
			b.WriteString(cEscape(r, js, false))
		}
	}
	b.WriteByte('"')
	return b.String()
}

// cEscape is r inside a JavaScript or Python string literal; multiline ones
// keep newlines and tabs as they are.
func cEscape(r rune, js, multiline bool) string {
	switch {
	case r == '\\':
		return `\\`
	case multiline && (r == '\n' || r == '\t'):
		return string(r)
	case r == '\n':
		return `\n`
	case r == '\t':
		return `\t`
	case r == '\r':
		return `\r`
	case r == '"' && !multiline:
		return `\"`
	case strconv.IsPrint(r):
		return string(r)
	case r < 0x100:
		return fmt.Sprintf(`\x%02x`, r)
	case r < 0x10000:
		return fmt.Sprintf(`\u%04x`, r)
	case js:
		return fmt.Sprintf(`\u{%x}`, r)
	}
	return fmt.Sprintf(`\U%08x`, r)
}

// jsString is a body as a JavaScript string, a template literal when it
// has lines.
func jsString(s string) string {
	if !strings.Contains(s, "\n") {
		return cQuote(s, true, false)
	}
	var b strings.Builder
	b.WriteByte('`')
	for i, r := range s {
		switch {
		case r == '`':
			b.WriteString("\\`")
		case r == '$' && strings.HasPrefix(s[i+1:], "{"):
			b.WriteString(`\$`)
		default:
			b.WriteString(cEscape(r, true, true))
		}
	}
	b.WriteByte('`')
	return b.String()
}

// pythonString is a body as a Python string, triple-quoted when it has
// lines and no """ to end it early.
func pythonString(s string) string {
	if !strings.Contains(s, "\n") || strings.Contains(s, `"""`) {
		return cQuote(s, false, false)
	}
	var b strings.Builder
	b.WriteString(`"""`)
	for i, r := range s {
		if r == '"' && i == len(s)-1 {
			b.WriteString(`\"`)
		} else {
			b.WriteString(cEscape(r, false, true))
		}
	}
	b.WriteString(`"""`)
	return b.String()
}

// goString is a body as a Go string, a raw string when it has lines and
// nothing a raw string cannot hold.
func goString(s string) string {
	raw := strings.Contains(s, "\n") && !strings.ContainsFunc(s, func(r rune) bool {
		return r == '`' || r != '\n' && r != '\t' && !strconv.IsPrint(r)
	})
	if raw {
		return "`" + s + "`"
	}
	return strconv.Quote(s)
}

// shellWord is s as one POSIX shell word, quoted only when it needs to be.
func shellWord(s string) string {
	if s != "" && !strings.ContainsFunc(s, func(r rune) bool {
		return !(r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9' || strings.ContainsRune("-_./:,=@%+", r))
	}) {
		return s
	}
	return shellQuote(s)
}

// shellQuote quotes s for a POSIX shell, so a body with quotes survives.
func shellQuote(s string) string {
	return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'"
}
