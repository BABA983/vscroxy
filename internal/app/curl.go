package app

import (
	"cmp"
	"encoding/base64"
	"errors"
	"fmt"
	"github.com/baba983/vscroxy/internal/uikit"
	"io"
	"maps"
	"net/http"
	"net/url"
	"os"
	"slices"
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/baba983/vscroxy/internal/request"
	"github.com/egoist/mygo/ui"
	"golang.org/x/net/http/httpguts"
	"mvdan.cc/sh/v3/expand"
	"mvdan.cc/sh/v3/syntax"
)

// curlSkip removes wire framing and hop-by-hop headers. Host is retained:
// virtual hosts need not match the URL's authority.
var curlSkip = map[string]bool{
	"connection":          true,
	"proxy-connection":    true,
	"content-length":      true,
	"transfer-encoding":   true,
	"keep-alive":          true,
	"te":                  true,
	"trailer":             true,
	"upgrade":             true,
	"proxy-authorization": true,
}

// curlCommand writes the flow's request as a curl command line, the way it
// would be run from a terminal.
func curlCommand(f flow) (string, error) {
	return curlCommandFor(f, false)
}

// CMD uses curl.exe, never PowerShell's historical curl alias. Its command
// line and encoding limits are stricter than Bash's; refuse lossy output.
func curlCommandFor(f flow, cmd bool) (string, error) {
	body, err := copyRequestBody(f)
	if err != nil {
		return "", err
	}
	headers := http.Header{}
	for key, values := range f.reqHeader {
		if !httpguts.ValidHeaderFieldName(key) {
			return "", fmt.Errorf("invalid request header name %q", key)
		}
		for _, value := range values {
			if !httpguts.ValidHeaderFieldValue(value) {
				return "", fmt.Errorf("invalid value for request header %s", key)
			}
			headers.Add(key, value)
		}
	}
	skip := maps.Clone(curlSkip)
	for _, value := range headers.Values("Connection") {
		for token := range strings.SplitSeq(value, ",") {
			skip[strings.ToLower(strings.TrimSpace(token))] = true
		}
	}
	// Capture stores decoded bodies but original wire headers. Drafts (id 0)
	// already contain the bytes their author intends to send.
	if f.id != 0 && headers.Get("Content-Encoding") != "" {
		for _, name := range []string{"content-encoding", "content-md5", "digest"} {
			skip[name] = true
		}
	}
	// Encode control bytes as well: terminals can translate literal CRs and
	// interpret escape sequences when a command is pasted, even inside quotes.
	stdinBody := len(body)+3*strings.Count(body, "'")+2 > 32<<10 || !utf8.ValidString(body) || strings.IndexFunc(body, func(r rune) bool {
		return unicode.IsControl(r) && r != '\n' && r != '\t'
	}) >= 0
	quote := shellQuote
	if cmd {
		if stdinBody {
			return "", errors.New("CMD cannot preserve this binary, control-byte or large body; copy as cURL (Bash) instead")
		}
		quote = cmdQuote
	}
	var b strings.Builder
	if stdinBody {
		b.WriteString("base64 -d <<'VSCROXY_BODY' | ")
	}
	if cmd {
		b.WriteString("curl.exe -q --path-as-is")
	} else {
		b.WriteString("curl -q --path-as-is")
	}
	if strings.ContainsAny(f.url, "{}[]") {
		b.WriteString(" --globoff")
	}
	method := cmp.Or(strings.TrimSpace(f.method), "GET")
	hasBody := body != "" || method != "HEAD" && headers.Get("Content-Length") != ""
	if !httpguts.ValidHeaderFieldName(method) {
		return "", errors.New("invalid HTTP method")
	}
	if method == "HEAD" {
		if body != "" {
			return "", errors.New("cURL HEAD export with a body is not supported")
		}
		b.WriteString(" --head")
	} else if method != "GET" || hasBody {
		b.WriteString(" -X " + quote(method))
	}
	for _, key := range slices.Sorted(maps.Keys(headers)) {
		if skip[strings.ToLower(key)] {
			continue
		}
		for _, value := range headers[key] {
			line := key + ": " + value
			if value == "" {
				line = key + ";"
			}
			if len(line) > 32<<10 {
				return "", errors.New("request header exceeds the portable cURL argument limit (32 KiB)")
			}
			b.WriteString(" -H " + quote(line))
		}
	}
	if headers.Get("Accept-Encoding") != "" && !skip["accept-encoding"] {
		b.WriteString(" --compressed")
	}
	for _, name := range []string{"Accept", "User-Agent", "Content-Type"} {
		if len(headers.Values(name)) == 0 || skip[strings.ToLower(name)] {
			b.WriteString(" -H " + quote(name+":"))
		}
	}
	if hasBody {
		if stdinBody {
			b.WriteString(" --data-binary @-")
		} else {
			b.WriteString(" --data-raw " + quote(body))
		}
	}
	if len(f.url) > 32<<10 || b.Len() > 128<<10 {
		return "", errors.New("request URL or headers exceed the portable cURL argument limit")
	}
	b.WriteString(" --url " + quote(f.url))
	if stdinBody {
		encoded := base64.StdEncoding.EncodeToString([]byte(body))
		for len(encoded) > 0 {
			n := min(len(encoded), 76)
			b.WriteString("\n" + encoded[:n])
			encoded = encoded[n:]
		}
		b.WriteString("\nVSCROXY_BODY\n")
	}
	if b.Len() > 16<<20 {
		return "", errors.New("cURL clipboard export is limited to 16 MiB")
	}
	if !utf8.ValidString(b.String()) || strings.ContainsRune(b.String(), 0) {
		return "", errors.New("cURL arguments must be UTF-8 text without NUL bytes")
	}
	if cmd {
		// ! is expanded even inside quotes with delayed expansion enabled;
		// non-ASCII command text depends on the console's active code page.
		if len(b.String()) > 8000 || strings.ContainsFunc(b.String(), func(r rune) bool { return r > 126 || r == '!' || r < 32 && r != '\n' }) {
			return "", errors.New("CMD export requires ASCII without delayed-expansion markers and at most 8000 characters; use Bash or PowerShell instead")
		}
	}
	return b.String(), nil
}

// All request-copy formats use retained full bodies, never display previews.
func copyRequestBody(f flow) (string, error) {
	if f.kind == "connect" || f.kind == "ws" || len(f.reqTrailer) > 0 {
		return "", errors.New("request export requires HTTP without request trailers; tunnels and WebSocket exchanges are not replayable this way")
	}
	if f.reqError != "" {
		return "", errors.New("request body is incomplete")
	}
	if f.reqSize > 16<<20 {
		return "", errors.New("request clipboard export is limited to 16 MiB")
	}
	body := f.reqBody
	if f.reqFile != "" {
		file, err := os.Open(f.reqFile)
		if err != nil {
			return "", errors.New("request body is unavailable")
		}
		defer file.Close()
		data, err := io.ReadAll(io.LimitReader(file, 16<<20+1))
		if err != nil {
			return "", errors.New("request body could not be read")
		}
		body = string(data)
	}
	if len(body) > 16<<20 {
		return "", errors.New("request clipboard export is limited to 16 MiB")
	}
	if f.reqSize > len(body) {
		return "", errors.New("request body is incomplete; refusing to copy a truncated request")
	}
	return body, nil
}

// Adapted from Chromium DevTools generateCurlCommand's escapeStringWin.
// Quotes survive both cmd.exe and the Windows C runtime argument parser.
// See THIRD_PARTY_NOTICES.md. Intended for interactive CMD, not .bat files.
func cmdQuote(s string) string {
	var b strings.Builder
	b.WriteString(`^"`)
	s = strings.ReplaceAll(strings.ReplaceAll(s, `\`, `\\`), `"`, `\"`)
	for i, r := range s {
		switch {
		case r == '\n':
			b.WriteString("^\n\n")
		case r == '%':
			b.WriteString("^%")
			if i+1 < len(s) && (s[i+1] >= 'a' && s[i+1] <= 'z' || s[i+1] >= 'A' && s[i+1] <= 'Z' || s[i+1] >= '0' && s[i+1] <= '9' || s[i+1] == '_') {
				b.WriteByte('^')
			}
		case r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9' || strings.ContainsRune(" _-:=+~'/.,?;*", r):
			b.WriteRune(r)
		default:
			b.WriteByte('^')
			b.WriteRune(r)
		}
	}
	b.WriteString(`^"`)
	return b.String()
}

// shellQuote quotes s for a POSIX shell, so a body with quotes survives.
func shellQuote(s string) string {
	return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'"
}

// parseCurl reads request data, never executes a shell or reads @files. Unknown
// options fail rather than quietly changing authentication or transport behavior.
func parseCurl(source string) (savedRequest, error) {
	r := savedRequest{Name: "Imported cURL"}
	if len(source) > 16<<20 {
		return r, errors.New("cURL input is limited to 16 MiB")
	}
	file, err := syntax.NewParser(syntax.Variant(syntax.LangBash)).Parse(strings.NewReader(source), "")
	if err != nil {
		return r, fmt.Errorf("invalid shell quoting: %w", err)
	}
	if len(file.Stmts) != 1 {
		return r, errors.New("paste exactly one curl command")
	}
	stmt := file.Stmts[0]
	call, ok := stmt.Cmd.(*syntax.CallExpr)
	if !ok || len(stmt.Redirs) > 0 || stmt.Background || stmt.Negated || stmt.Coprocess || stmt.Disown || len(call.Assigns) > 0 {
		return r, errors.New("shell operators, redirections and assignments are not supported")
	}
	var args []string
	for _, word := range call.Args {
		value, err := curlLiteral(word.Parts, false)
		if err != nil {
			return r, err
		}
		args = append(args, value)
	}
	if len(args) == 0 || args[0] != "curl" && args[0] != "curl.exe" {
		return r, errors.New("command must start with curl (Bash/POSIX format)")
	}
	headers, explicit := http.Header{}, map[string]bool{}
	defaults := http.Header{}
	var data []string
	var get, head, jsonBody, afterDash bool
	for i := 1; i < len(args); i++ {
		arg := args[i]
		if arg == "--" && !afterDash {
			afterDash = true
			continue
		}
		if afterDash || !strings.HasPrefix(arg, "-") {
			if r.URL != "" {
				return r, errors.New("multiple URLs are not supported; import one request at a time")
			}
			r.URL = arg
			continue
		}
		option, value, attached := strings.Cut(arg, "=")
		if !strings.HasPrefix(arg, "--") && len(arg) > 2 && strings.ContainsRune("XHdbuAe", rune(arg[1])) {
			option, value, attached = arg[:2], arg[2:], true
		}
		switch option {
		case "-G", "--get", "-I", "--head", "--compressed", "-g", "--globoff", "-q", "--disable", "--path-as-is", "-s", "--silent", "-S", "--show-error", "-sS", "-v", "--verbose", "-i", "--include":
			if attached {
				return r, fmt.Errorf("%s takes no value", option)
			}
			get = get || option == "-G" || option == "--get"
			head = head || option == "-I" || option == "--head"
			continue
		case "--url", "-X", "--request", "-H", "--header", "-d", "--data", "--data-ascii", "--data-raw", "--data-binary", "--data-urlencode", "--json", "-b", "--cookie", "-u", "--user", "-A", "--user-agent", "-e", "--referer":
			if !attached {
				i++
				if i >= len(args) {
					return r, fmt.Errorf("%s needs a value", option)
				}
				value = args[i]
			}
		default:
			return r, fmt.Errorf("unsupported cURL option %s; no request was imported", option)
		}
		switch option {
		case "--url":
			if r.URL != "" {
				return r, errors.New("multiple URLs are not supported")
			}
			r.URL = value
		case "-X", "--request":
			if !httpguts.ValidHeaderFieldName(value) {
				return r, errors.New("invalid HTTP method")
			}
			r.Method = value
		case "-H", "--header":
			name, v, colon := strings.Cut(value, ":")
			if !colon {
				name = strings.TrimSuffix(value, ";")
			}
			if !httpguts.ValidHeaderFieldName(name) || !httpguts.ValidHeaderFieldValue(v) {
				return r, errors.New("invalid literal header; @header files are not supported")
			}
			explicit[http.CanonicalHeaderKey(name)] = true
			if colon && strings.TrimSpace(v) != "" || strings.HasSuffix(value, ";") && !colon {
				headers.Add(name, strings.TrimSpace(v))
			} else {
				headers.Del(name)
			}
		case "-b", "--cookie":
			if !strings.Contains(value, "=") {
				return r, errors.New("cookie files are not read; use a literal name=value cookie")
			}
			defaults.Set("Cookie", strings.TrimPrefix(defaults.Get("Cookie")+"; "+value, "; "))
		case "-u", "--user":
			if !strings.Contains(value, ":") {
				return r, errors.New("use --user 'username:password'; interactive password prompts are not supported")
			}
			defaults.Set("Authorization", "Basic "+base64.StdEncoding.EncodeToString([]byte(value)))
		case "-A", "--user-agent":
			defaults.Set("User-Agent", value)
		case "-e", "--referer":
			defaults.Set("Referer", value)
		default:
			if (option == "--json") != jsonBody && len(data) > 0 {
				return r, errors.New("mixing --json with other data options is not supported")
			}
			jsonBody = option == "--json"
			if option == "--data-urlencode" {
				if name, content, found := strings.Cut(value, "="); found {
					value = url.QueryEscape(content)
					if name != "" {
						value = name + "=" + value
					}
				} else if strings.Contains(value, "@") {
					return r, errors.New("@files and stdin are not read; paste a literal request body")
				} else {
					value = url.QueryEscape(value)
				}
			} else if option != "--data-raw" && strings.HasPrefix(value, "@") {
				return r, errors.New("@files and stdin are not read; paste a literal request body")
			}
			data = append(data, value)
		}
	}
	checkURL := r.URL
	if match := requestVariable.FindStringIndex(checkURL); match != nil && match[0] == 0 {
		checkURL = "https://placeholder.invalid" + checkURL[match[1]:]
	}
	u, err := url.Parse(requestVariable.ReplaceAllString(checkURL, "placeholder"))
	if err != nil || u.Host == "" || u.Scheme != "http" && u.Scheme != "https" {
		return r, errors.New("one absolute HTTP(S) URL is required ({{base_url}} is also supported)")
	}
	if head && len(data) > 0 && !get {
		return r, errors.New("--head with a request body is not supported")
	}
	body := strings.Join(data, "&")
	if jsonBody {
		body = strings.Join(data, "")
		defaults.Set("Accept", "application/json")
	}
	if get && len(data) > 0 {
		base, fragment, hasFragment := strings.Cut(r.URL, "#")
		separator := "?"
		if strings.Contains(base, "?") {
			separator = "&"
		}
		r.URL = base + separator + body
		if hasFragment {
			r.URL += "#" + fragment
		}
	} else if len(data) > 0 {
		r.Body = body
		contentType := "application/x-www-form-urlencoded"
		if jsonBody {
			contentType = "application/json"
		}
		defaults.Set("Content-Type", contentType)
	}
	if r.Method == "" {
		r.Method = "GET"
		if head {
			r.Method = "HEAD"
		} else if len(data) > 0 && !get {
			r.Method = "POST"
		}
	}
	for name, values := range defaults {
		if !explicit[name] {
			headers[name] = values
		}
	}
	for _, values := range headers {
		for _, value := range values {
			if !httpguts.ValidHeaderFieldValue(value) {
				return r, errors.New("invalid HTTP header value")
			}
		}
	}
	r.Headers = request.HeaderBlock(headers)
	return r, nil
}

// Decode literal quoting only. No shell/environment/glob expansion is run.
func curlLiteral(parts []syntax.WordPart, quoted bool) (string, error) {
	var b strings.Builder
	for _, part := range parts {
		switch p := part.(type) {
		case *syntax.Lit:
			for i := 0; i < len(p.Value); i++ {
				if p.Value[i] == '\\' && i+1 < len(p.Value) && (!quoted || strings.ContainsRune("$`\"\\", rune(p.Value[i+1]))) {
					i++
				}
				b.WriteByte(p.Value[i])
			}
		case *syntax.SglQuoted:
			value := p.Value
			if p.Dollar {
				var err error
				value, _, err = expand.Format(nil, value, nil)
				if err != nil {
					return "", err
				}
			}
			b.WriteString(value)
		case *syntax.DblQuoted:
			if p.Dollar {
				return "", errors.New("locale-translated shell strings are not supported")
			}
			value, err := curlLiteral(p.Parts, true)
			if err != nil {
				return "", err
			}
			b.WriteString(value)
		default:
			return "", errors.New("shell substitutions are not supported; paste literal values instead")
		}
	}
	value := b.String()
	if strings.ContainsRune(value, 0) || !utf8.ValidString(value) {
		return "", errors.New("cURL arguments must be UTF-8 text without NUL bytes")
	}
	return value, nil
}

func (a *app) openCurlImport() {
	a.curlOpen, a.curlInput, a.curlProblem = true, "", ""
}

func (a *app) curlImportModal(c *ui.Context, t *ui.Theme) {
	if !a.curlOpen {
		return
	}
	uikit.Modal(c, &a.curlOpen, func() {
		uikit.DialogColumn(c, uikit.DialogMedium).Children(func() {
			uikit.DialogTitle(c, "Import cURL")
			ui.Scroll(c).MinHeight(0).Shrink(1).Gap(t.Space(4)).Children(func() {
				uikit.Hint(c, "Paste one Bash/POSIX curl command. It opens an unsent request tab; nothing is executed.")
				if ui.TextArea(c, &a.curlInput).Height(190).Font(uikit.FontMono).FontSize(uikit.TextBody).Radius(uikit.RadiusControl).Label("cURL command").Placeholder("curl 'https://api.example.com/projects' \\\n  -H 'Accept: application/json'").AutoFocus().Changed() {
					a.curlProblem = ""
				}
				if a.curlProblem != "" {
					ui.Text(c, a.curlProblem).FontSize(uikit.TextBody).TextColor(t.Danger)
				}
			})
			uikit.DialogFooter(c).Children(func() {
				if uikit.SecondaryButton(c, "Cancel").Clicked() {
					a.curlOpen, a.curlInput = false, ""
				}
				if uikit.PrimaryButton(c, "Import request").Disabled(strings.TrimSpace(a.curlInput) == "").Clicked() {
					r, err := parseCurl(a.curlInput)
					if err != nil {
						a.curlProblem = err.Error()
						return
					}
					a.addDocument(r, "")
					a.curlOpen, a.curlInput = false, ""
					a.done("Imported cURL as an unsent request; review headers and body before sending")
				}
			})
		})
	})
}
