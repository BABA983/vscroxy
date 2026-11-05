package imports

import (
	"encoding/base64"
	"errors"
	"fmt"
	"math"
	"net/http"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"unicode/utf16"
	"unicode/utf8"

	"github.com/baba983/vscroxy/internal/capture"
	"github.com/baba983/vscroxy/internal/request"
	"golang.org/x/net/http/httpguts"
	"mvdan.cc/sh/v3/expand"
	"mvdan.cc/sh/v3/syntax"
)

// CurlStyle is the command-line dialect of a pasted request.
type CurlStyle int

const (
	CurlBash CurlStyle = iota
	CurlCMD
	CurlPowerShell
)

var curlVariable = regexp.MustCompile(`\{\{([A-Za-z_][A-Za-z0-9_]*)\}\}`)

// DetectCurl names the dialect of source without parsing it. PowerShell's
// Copy as PowerShell output starts with its session prelude or
// Invoke-WebRequest. Everything else is parsed as Bash first, because cmd.exe
// quoting can look like ordinary shell text when it appears inside a value;
// only a caret-prefixed first argument, a caret-prefixed --url value, or a
// failed Bash parse with a CMD continuation marks Chrome's CMD output.
func DetectCurl(source string) CurlStyle {
	source = strings.TrimSpace(strings.TrimPrefix(source, "\ufeff"))
	lower := strings.ToLower(source)
	switch {
	case strings.HasPrefix(lower, "$session"), strings.HasPrefix(lower, "invoke-webrequest"), strings.HasPrefix(lower, "invoke-restmethod"):
		return CurlPowerShell
	}
	if !strings.HasPrefix(lower, "curl ") && !strings.HasPrefix(lower, "curl.exe ") {
		return CurlBash
	}
	if args, err := parseCurlBash(source); err == nil {
		if curlArgsAreCMD(args) {
			return CurlCMD
		}
		return CurlBash
	}
	// Bash parsing rejects cmd.exe's ^ line continuation and its caret
	// quoting when they split the command. Do not treat every Bash error as
	// CMD: shell substitutions and operators must keep failing as Bash.
	if hasCMDQuoteOrContinuation(source) {
		return CurlCMD
	}
	return CurlBash
}

func curlArgsAreCMD(args []string) bool {
	if len(args) < 2 {
		return false
	}
	if strings.HasPrefix(args[1], "^") {
		return true
	}
	for i := 1; i < len(args); i++ {
		if args[i] == "--url" && i+1 < len(args) && strings.HasPrefix(args[i+1], "^") {
			return true
		}
		if strings.HasPrefix(args[i], "--url=^") {
			return true
		}
	}
	return false
}

func hasCMDQuoteOrContinuation(source string) bool {
	if strings.Contains(source, `^"`) {
		return true
	}
	for line := range strings.SplitSeq(source, "\n") {
		if strings.HasSuffix(strings.TrimRight(line, " \t\r"), "^") {
			return true
		}
	}
	return false
}

// ParseCurl reads one request from a Bash/POSIX curl command, Chrome's
// Copy as cURL (cmd) output, or Chrome's Copy as PowerShell
// (Invoke-WebRequest) output. It never executes a shell, reads a file or
// follows a substitution. Unsupported options fail rather than quietly
// changing authentication or transport behavior.
func ParseCurl(source string) (request.Request, []string, error) {
	source = strings.TrimSpace(strings.TrimPrefix(source, "\ufeff"))
	if len(source) > 16<<20 {
		return request.Request{}, nil, errors.New("cURL input is limited to 16 MiB")
	}
	switch DetectCurl(source) {
	case CurlPowerShell:
		return parsePowerShell(source)
	case CurlCMD:
		args, err := parseCurlCMD(source)
		if err != nil {
			return request.Request{}, nil, err
		}
		return parseCurlArgs(args)
	default:
		args, err := parseCurlBash(source)
		if err != nil {
			return request.Request{}, nil, err
		}
		return parseCurlArgs(args)
	}
}

// parseCurlBash reads a Bash/POSIX command into its literal argv. It decodes
// quoting only; no shell, environment or glob expansion is run.
func parseCurlBash(source string) ([]string, error) {
	file, err := syntax.NewParser(syntax.Variant(syntax.LangBash)).Parse(strings.NewReader(source), "")
	if err != nil {
		return nil, fmt.Errorf("invalid shell quoting: %w", err)
	}
	if len(file.Stmts) != 1 {
		return nil, errors.New("paste exactly one curl command")
	}
	stmt := file.Stmts[0]
	call, ok := stmt.Cmd.(*syntax.CallExpr)
	if !ok || len(stmt.Redirs) > 0 || stmt.Background || stmt.Negated || stmt.Coprocess || stmt.Disown || len(call.Assigns) > 0 {
		return nil, errors.New("shell operators, redirections and assignments are not supported")
	}
	var args []string
	for _, word := range call.Args {
		value, err := curlLiteral(word.Parts, false)
		if err != nil {
			return nil, err
		}
		args = append(args, value)
	}
	return args, nil
}

// parseCurlCMD decodes Chrome's Copy as cURL (cmd) output. cmd.exe removes
// caret escapes, then the Windows C runtime parses quotes and backslashes;
// both layers are reversed here.
func parseCurlCMD(source string) ([]string, error) {
	line, err := cmdUnescape(source)
	if err != nil {
		return nil, err
	}
	args, err := splitWindowsArgs(line)
	if err != nil {
		return nil, err
	}
	if len(args) == 0 {
		return nil, errors.New("paste exactly one curl command")
	}
	return args, nil
}

// cmdUnescape reverses cmd.exe's caret quoting. Chrome writes ^" around each
// argument, ^% for %, ^\n\n for a newline in an argument and ^<newline> for
// a line continuation.
func cmdUnescape(source string) (string, error) {
	var b strings.Builder
	for i := 0; i < len(source); i++ {
		c := source[i]
		if c != '^' {
			b.WriteByte(c)
			continue
		}
		if i+1 >= len(source) {
			return "", errors.New("unfinished caret escape in CMD cURL")
		}
		next := source[i+1]
		switch {
		case next == '%':
			b.WriteByte('%')
			i++
		case next == '\n':
			// A caret newline is a line continuation. A second newline is the
			// newline Chrome encoded inside an argument.
			i++
			if i+1 < len(source) && source[i+1] == '\n' {
				b.WriteByte('\n')
				i++
			}
		case next == '\r' && i+2 < len(source) && source[i+2] == '\n':
			i += 2
			if i+1 < len(source) && source[i+1] == '\n' {
				b.WriteByte('\n')
				i++
			}
		default:
			b.WriteByte(next)
			i++
		}
	}
	return b.String(), nil
}

// splitWindowsArgs parses the command line curl.exe receives, using the
// Microsoft C runtime's quoting rules.
func splitWindowsArgs(line string) ([]string, error) {
	var args []string
	var current strings.Builder
	quoted, started := false, false
	for i := 0; i < len(line); i++ {
		c := line[i]
		switch {
		case c == '"':
			quoted = !quoted
			started = true
		case c == '\\':
			n := 0
			for i < len(line) && line[i] == '\\' {
				n++
				i++
			}
			if i < len(line) && line[i] == '"' {
				current.WriteString(strings.Repeat(`\`, n/2))
				if n%2 == 1 {
					current.WriteByte('"')
				} else {
					quoted = !quoted
					started = true
				}
				continue
			}
			current.WriteString(strings.Repeat(`\`, n))
			if i < len(line) {
				current.WriteByte(line[i])
			} else {
				i--
			}
		case (c == ' ' || c == '\t' || c == '\r' || c == '\n') && !quoted:
			if started {
				args = append(args, current.String())
				current.Reset()
				started = false
			}
		default:
			current.WriteByte(c)
			started = true
		}
	}
	if quoted {
		return nil, errors.New("unclosed quote in CMD cURL")
	}
	if started {
		args = append(args, current.String())
	}
	return args, nil
}

// parseCurlArgs reads the options of one curl invocation. args[0] is the
// executable.
func parseCurlArgs(args []string) (request.Request, []string, error) {
	manual := false
	r := request.Request{Name: "Imported cURL", Settings: request.Settings{FollowRedirects: &manual}}
	if len(args) == 0 || !strings.EqualFold(args[0], "curl") && !strings.EqualFold(args[0], "curl.exe") {
		return r, nil, errors.New("command must start with curl (Bash, CMD or PowerShell format)")
	}
	headers, explicit := http.Header{}, map[string]bool{}
	defaults := http.Header{}
	var data []string
	var get, head, jsonBody, globoff, afterDash bool
	for i := 1; i < len(args); i++ {
		arg := args[i]
		if arg == "--" && !afterDash {
			afterDash = true
			continue
		}
		if afterDash || !strings.HasPrefix(arg, "-") {
			if r.URL != "" {
				return r, nil, errors.New("multiple URLs are not supported; import one request at a time")
			}
			r.URL = arg
			continue
		}
		option, value, attached := strings.Cut(arg, "=")
		if !strings.HasPrefix(arg, "--") && len(arg) > 2 && strings.ContainsRune("XHdbuAem", rune(arg[1])) {
			option, value, attached = arg[:2], arg[2:], true
		}
		switch option {
		case "-L", "--location", "--no-location":
			if attached {
				return r, nil, fmt.Errorf("%s takes no value", option)
			}
			follow := option != "--no-location"
			r.Settings.FollowRedirects = &follow
			if follow {
				r.Settings.RedirectMode = capture.RedirectCurl
			}
			continue
		case "-k", "--insecure":
			if attached {
				return r, nil, fmt.Errorf("%s takes no value", option)
			}
			verify := false
			r.Settings.VerifyTLS = &verify
			continue
		case "-G", "--get", "-I", "--head", "--compressed", "-g", "--globoff", "-q", "--disable", "--path-as-is", "-s", "--silent", "-S", "--show-error", "-sS", "-v", "--verbose", "-i", "--include":
			if attached {
				return r, nil, fmt.Errorf("%s takes no value", option)
			}
			get = get || option == "-G" || option == "--get"
			head = head || option == "-I" || option == "--head"
			globoff = globoff || option == "-g" || option == "--globoff"
			continue
		case "--url", "-X", "--request", "-H", "--header", "-d", "--data", "--data-ascii", "--data-raw", "--data-binary", "--data-urlencode", "--json", "-b", "--cookie", "-u", "--user", "-A", "--user-agent", "-e", "--referer", "--max-redirs", "-m", "--max-time":
			if !attached {
				i++
				if i >= len(args) {
					return r, nil, fmt.Errorf("%s needs a value", option)
				}
				value = args[i]
			}
		default:
			return r, nil, fmt.Errorf("unsupported cURL option %s; no request was imported", option)
		}
		switch option {
		case "--url":
			if r.URL != "" {
				return r, nil, errors.New("multiple URLs are not supported")
			}
			r.URL = value
		case "-X", "--request":
			if !httpguts.ValidHeaderFieldName(value) {
				return r, nil, errors.New("invalid HTTP method")
			}
			r.Method = value
		case "-H", "--header":
			name, v, colon := strings.Cut(value, ":")
			if !colon {
				name = strings.TrimSuffix(value, ";")
			}
			if !httpguts.ValidHeaderFieldName(name) || !httpguts.ValidHeaderFieldValue(v) {
				return r, nil, errors.New("invalid literal header; @header files are not supported")
			}
			explicit[http.CanonicalHeaderKey(name)] = true
			if colon && strings.TrimSpace(v) != "" || strings.HasSuffix(value, ";") && !colon {
				headers.Add(name, strings.TrimSpace(v))
			} else {
				headers.Del(name)
			}
		case "-b", "--cookie":
			if !strings.Contains(value, "=") {
				return r, nil, errors.New("cookie files are not read; use a literal name=value cookie")
			}
			defaults.Set("Cookie", strings.TrimPrefix(defaults.Get("Cookie")+"; "+value, "; "))
		case "-u", "--user":
			if !strings.Contains(value, ":") {
				return r, nil, errors.New("use --user 'username:password'; interactive password prompts are not supported")
			}
			defaults.Set("Authorization", "Basic "+base64.StdEncoding.EncodeToString([]byte(value)))
		case "-A", "--user-agent":
			defaults.Set("User-Agent", value)
		case "-e", "--referer":
			defaults.Set("Referer", value)
		case "--max-redirs":
			n, err := strconv.Atoi(value)
			if err != nil || n < 0 || n > 1000 {
				return r, nil, errors.New("--max-redirs needs a whole number from 0 to 1000")
			}
			r.Settings.MaxRedirects = &n
			if n == 0 {
				follow := false
				r.Settings.FollowRedirects = &follow
			}
		case "--max-time", "-m":
			f, err := strconv.ParseFloat(value, 64)
			if err != nil || math.IsNaN(f) || math.IsInf(f, 0) || f < 0 || f > 3600 {
				return r, nil, errors.New("--max-time needs seconds from 0 to 3600")
			}
			n := int(math.Ceil(f))
			r.Settings.TimeoutSeconds = &n
		default:
			if (option == "--json") != jsonBody && len(data) > 0 {
				return r, nil, errors.New("mixing --json with other data options is not supported")
			}
			jsonBody = option == "--json"
			if option == "--data-urlencode" {
				if name, content, found := strings.Cut(value, "="); found {
					value = url.QueryEscape(content)
					if name != "" {
						value = name + "=" + value
					}
				} else if strings.Contains(value, "@") {
					return r, nil, errors.New("@files and stdin are not read; paste a literal request body")
				} else {
					value = url.QueryEscape(value)
				}
			} else if option != "--data-raw" && strings.HasPrefix(value, "@") {
				return r, nil, errors.New("@files and stdin are not read; paste a literal request body")
			}
			data = append(data, value)
		}
	}
	if !globoff {
		// Chrome escapes URL brackets and braces so curl does not read them as
		// a glob; without -g/--globoff, put the literal characters back.
		r.URL = strings.NewReplacer(`\[`, "[", `\]`, "]", `\{`, "{", `\}`, "}").Replace(r.URL)
	}
	checkURL := r.URL
	if match := curlVariable.FindStringIndex(checkURL); match != nil && match[0] == 0 {
		checkURL = "https://placeholder.invalid" + checkURL[match[1]:]
	}
	u, err := url.Parse(curlVariable.ReplaceAllString(checkURL, "placeholder"))
	if err != nil || u.Host == "" || u.Scheme != "http" && u.Scheme != "https" {
		return r, nil, errors.New("one absolute HTTP(S) URL is required ({{base_url}} is also supported)")
	}
	if head && len(data) > 0 && !get {
		return r, nil, errors.New("--head with a request body is not supported")
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
	if r.Settings.RedirectMode == capture.RedirectCurl {
		r.Settings.KeepMethod = r.Method != ""
		r.Settings.DropContentType = !jsonBody && !explicit["Content-Type"] && defaults.Get("Content-Type") != ""
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
				return r, nil, errors.New("invalid HTTP header value")
			}
		}
	}
	r.HeaderList = HeadersOf(headers)
	FinishHeaders(&r)
	return r, nil, nil
}

// curlLiteral decodes literal quoting only. No shell/environment/glob
// expansion is run.
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

// parsePowerShell reads Chrome's Copy as PowerShell output. It understands
// the Invoke-WebRequest subset Chrome writes: -Uri, -Method, -Headers,
// -ContentType, -Body, -WebSession and -UseBasicParsing, plus the session
// prelude that carries the user agent and cookies.
func parsePowerShell(source string) (request.Request, []string, error) {
	r := request.Request{Name: "Imported cURL"}
	statements, err := powerShellStatements(source)
	if err != nil {
		return r, nil, err
	}
	var cookies []powerShellCookieValue
	var userAgent string
	var sawInvoke bool
	var warnings []string
	for _, statement := range statements {
		statement = strings.TrimSpace(statement)
		if statement == "" {
			continue
		}
		switch {
		case strings.HasPrefix(statement, "$session = New-Object"):
			continue
		case strings.HasPrefix(statement, "$session.UserAgent"):
			_, value, ok := strings.Cut(statement, "=")
			if !ok {
				return r, nil, errors.New("invalid PowerShell session user agent")
			}
			userAgent, err = powerShellString(strings.TrimSpace(value))
			if err != nil {
				return r, nil, fmt.Errorf("PowerShell session user agent: %w", err)
			}
			if !httpguts.ValidHeaderFieldValue(userAgent) {
				return r, nil, errors.New("invalid PowerShell session user agent")
			}
		case strings.HasPrefix(statement, "$session.Cookies.Add"):
			name, value, path, domain, err := powerShellCookie(statement)
			if err != nil {
				return r, nil, err
			}
			if !httpguts.ValidHeaderFieldName(name) || !httpguts.ValidHeaderFieldValue(value) {
				return r, nil, errors.New("invalid PowerShell cookie")
			}
			cookies = append(cookies, powerShellCookieValue{name + "=" + value, path, domain})
		case strings.HasPrefix(strings.ToLower(statement), "invoke-webrequest"):
			if sawInvoke {
				return r, nil, errors.New("paste exactly one Invoke-WebRequest command")
			}
			sawInvoke = true
			warnings, err = powerShellRequest(&r, statement, userAgent, cookieValues(cookies), warnings)
			if err != nil {
				return r, nil, err
			}
		case strings.HasPrefix(strings.ToLower(statement), "invoke-restmethod"):
			return r, nil, errors.New("Invoke-RestMethod is not supported; use Chrome's Copy as PowerShell (Invoke-WebRequest)")
		default:
			return r, nil, errors.New("unsupported PowerShell statement; paste only Chrome's Copy as PowerShell output")
		}
	}
	if !sawInvoke {
		return r, nil, errors.New("paste one Invoke-WebRequest command")
	}
	host := ""
	if u, err := url.Parse(curlVariable.ReplaceAllString(r.URL, "placeholder")); err == nil {
		host = u.Hostname()
	}
	for _, cookie := range cookies {
		if cookie.path != "" && cookie.path != "/" || cookie.domain != "" && !strings.EqualFold(cookie.domain, host) {
			warnings = append(warnings, "cookie domain/path were dropped; the cookie is sent to the imported URL")
			break
		}
	}
	return r, warnings, nil
}

type powerShellCookieValue struct{ value, path, domain string }

// cookieValues renders session cookies for the Cookie header.
func cookieValues(cookies []powerShellCookieValue) []string {
	values := make([]string, len(cookies))
	for i, cookie := range cookies {
		values[i] = cookie.value
	}
	return values
}

// powerShellRequest applies the parameters of one Invoke-WebRequest command.
func powerShellRequest(r *request.Request, statement, userAgent string, cookies, warnings []string) ([]string, error) {
	tokens, err := powerShellTokens(strings.TrimSpace(statement))
	if err != nil {
		return warnings, err
	}
	if len(tokens) == 0 || !strings.EqualFold(tokens[0].value, "Invoke-WebRequest") {
		return warnings, errors.New("paste one Invoke-WebRequest command")
	}
	var headers http.Header
	var contentType, body string
	var hasBody bool
	var webSession string
	for i := 1; i < len(tokens); i++ {
		token := tokens[i]
		if !strings.HasPrefix(token.value, "-") {
			return warnings, fmt.Errorf("unexpected Invoke-WebRequest argument %q", token.value)
		}
		name := strings.ToLower(strings.TrimPrefix(token.value, "-"))
		switch name {
		case "usebasicparsing":
			continue
		case "skipcertificatecheck":
			verify := false
			r.Settings.VerifyTLS = &verify
			continue
		case "uri", "method", "contenttype", "websession", "maximumredirection", "timeoutsec":
			i++
			if i >= len(tokens) {
				return warnings, fmt.Errorf("Invoke-WebRequest -%s needs a value", name)
			}
			value, err := powerShellValue(tokens[i])
			if err != nil {
				return warnings, fmt.Errorf("Invoke-WebRequest -%s: %w", name, err)
			}
			switch name {
			case "uri":
				r.URL = value
			case "method":
				r.Method = strings.ToUpper(value)
			case "contenttype":
				contentType = value
			case "websession":
				webSession = value
			case "maximumredirection":
				n, err := strconv.Atoi(value)
				if err != nil || n < 0 || n > 1000 {
					return warnings, errors.New("Invoke-WebRequest -MaximumRedirection needs a whole number from 0 to 1000")
				}
				follow := n > 0
				r.Settings.FollowRedirects = &follow
				r.Settings.MaxRedirects = &n
			case "timeoutsec":
				n, err := strconv.Atoi(value)
				if err != nil || n < 0 || n > 3600 {
					return warnings, errors.New("Invoke-WebRequest -TimeoutSec needs a whole number from 0 to 3600")
				}
				r.Settings.TimeoutSeconds = &n
			}
		case "headers":
			i++
			if i >= len(tokens) || tokens[i].value != "@{" {
				return warnings, errors.New("Invoke-WebRequest -Headers must be a @{...} hashtable")
			}
			headers, i, err = powerShellHeaders(tokens, i+1)
			if err != nil {
				return warnings, err
			}
		case "body":
			i++
			if i >= len(tokens) {
				return warnings, errors.New("Invoke-WebRequest -Body needs a value")
			}
			body, hasBody, i, err = powerShellBody(tokens, i)
			if err != nil {
				return warnings, err
			}
		default:
			return warnings, fmt.Errorf("unsupported Invoke-WebRequest parameter -%s; no request was imported", name)
		}
	}
	if webSession != "" && webSession != "$session" {
		warnings = append(warnings, "web session "+webSession+" was not resolved; only this command's session prelude is read")
	}
	if r.URL == "" {
		return warnings, errors.New("Invoke-WebRequest -Uri is required")
	}
	if r.Method == "" {
		r.Method = "GET"
	}
	r.HeaderList = append(r.HeaderList, HeadersOf(headers)...)
	if userAgent != "" {
		AddHeader(r, "User-Agent", userAgent)
	}
	if len(cookies) > 0 {
		existing := ""
		for _, row := range r.HeaderList {
			if strings.EqualFold(row.Name, "Cookie") {
				existing = row.Value
				break
			}
		}
		value := strings.Join(cookies, "; ")
		if existing != "" {
			value = existing + "; " + value
		}
		SetHeader(r, "Cookie", value)
	}
	if contentType != "" {
		AddHeader(r, "Content-Type", contentType)
	}
	if hasBody {
		r.BodyMode, r.Body = "Raw", body
		if contentType == "" {
			AddHeader(r, "Content-Type", "application/x-www-form-urlencoded")
		}
	}
	if err := validateCurlURL(r.URL); err != nil {
		return warnings, err
	}
	FinishHeaders(r)
	return warnings, nil
}

func validateCurlURL(raw string) error {
	check := raw
	if match := curlVariable.FindStringIndex(check); match != nil && match[0] == 0 {
		check = "https://placeholder.invalid" + check[match[1]:]
	}
	u, err := url.Parse(curlVariable.ReplaceAllString(check, "placeholder"))
	if err != nil || u.Host == "" || u.Scheme != "http" && u.Scheme != "https" {
		return errors.New("one absolute HTTP(S) URL is required ({{base_url}} is also supported)")
	}
	return nil
}

// powerShellStatements splits a PowerShell script at top-level newlines and
// semicolons, keeping hashtables and quoted strings together.
func powerShellStatements(source string) ([]string, error) {
	var out []string
	var current strings.Builder
	var quote byte
	depth := 0
	for i := 0; i < len(source); i++ {
		c := source[i]
		if quote != 0 {
			current.WriteByte(c)
			if c == '`' && quote == '"' && i+1 < len(source) {
				i++
				current.WriteByte(source[i])
				continue
			}
			if c == quote {
				if quote == '\'' && i+1 < len(source) && source[i+1] == '\'' {
					i++
					current.WriteByte(source[i])
					continue
				}
				quote = 0
			}
			continue
		}
		switch c {
		case '\'', '"':
			quote = c
			current.WriteByte(c)
		case '`':
			// Outside a string, a backtick escapes the next character; before
			// a newline it continues the same statement.
			if i+1 < len(source) {
				i++
				if source[i] == '\r' && i+1 < len(source) && source[i+1] == '\n' {
					i++
				} else if source[i] != '\n' && source[i] != '\r' {
					current.WriteByte(source[i])
				}
			}
		case '#':
			// A comment runs to the end of the line.
			for i < len(source) && source[i] != '\n' {
				i++
			}
			if i < len(source) {
				current.WriteByte('\n')
			}
		case '{', '(', '[':
			depth++
			current.WriteByte(c)
		case '}', ')', ']':
			depth--
			if depth < 0 {
				return nil, errors.New("unbalanced PowerShell brackets")
			}
			current.WriteByte(c)
		case '\n', ';':
			if depth == 0 {
				out = append(out, current.String())
				current.Reset()
			} else {
				current.WriteByte(' ')
			}
		default:
			current.WriteByte(c)
		}
	}
	if quote != 0 {
		return nil, errors.New("unclosed PowerShell string")
	}
	if depth != 0 {
		return nil, errors.New("unbalanced PowerShell brackets")
	}
	out = append(out, current.String())
	return out, nil
}

type psToken struct {
	value  string
	quoted bool
}

// powerShellTokens reads the Invoke-WebRequest statement into words and the
// punctuation the parameter parser needs.
func powerShellTokens(statement string) ([]psToken, error) {
	var out []psToken
	for i := 0; i < len(statement); {
		c := statement[i]
		if c == ' ' || c == '\t' || c == '\r' || c == '\n' {
			i++
			continue
		}
		if strings.HasPrefix(statement[i:], "@{") {
			out = append(out, psToken{value: "@{"})
			i += 2
			continue
		}
		if strings.ContainsRune("(){},;=", rune(c)) {
			out = append(out, psToken{value: string(c)})
			i++
			continue
		}
		if c == '\'' || c == '"' {
			value, next, err := powerShellQuoted(statement, i)
			if err != nil {
				return nil, err
			}
			out = append(out, psToken{value: value, quoted: true})
			i = next
			continue
		}
		var b strings.Builder
		for i < len(statement) {
			c = statement[i]
			if c == ' ' || c == '\t' || c == '\r' || c == '\n' || strings.ContainsRune("(){},;=", rune(c)) {
				break
			}
			if c == '`' {
				i++
				if i >= len(statement) {
					return nil, errors.New("unfinished PowerShell backtick escape")
				}
				b.WriteByte(statement[i])
				i++
				continue
			}
			b.WriteByte(c)
			i++
		}
		out = append(out, psToken{value: b.String()})
	}
	return out, nil
}

// powerShellQuoted reads a single- or double-quoted PowerShell string. It
// supports the backtick escapes Chrome writes and rejects interpolation of
// variables, except Chrome's $([char]N) for a non-printable character.
func powerShellQuoted(s string, start int) (string, int, error) {
	quote := s[start]
	var b strings.Builder
	for i := start + 1; i < len(s); i++ {
		c := s[i]
		if c == quote {
			if quote == '\'' && i+1 < len(s) && s[i+1] == '\'' {
				b.WriteByte('\'')
				i++
				continue
			}
			return b.String(), i + 1, nil
		}
		if c == '`' && quote == '"' {
			i++
			if i >= len(s) {
				return "", 0, errors.New("unfinished PowerShell backtick escape")
			}
			switch s[i] {
			case 'n':
				b.WriteByte('\n')
			case 'r':
				b.WriteByte('\r')
			case 't':
				b.WriteByte('\t')
			case '0':
				b.WriteByte(0)
			default:
				b.WriteByte(s[i])
			}
			continue
		}
		if c == '$' && quote == '"' {
			unit, next, ok := powerShellCodeUnit(s, i)
			if !ok {
				return "", 0, errors.New("PowerShell variables in strings are not supported; paste literal values")
			}
			if utf16.IsSurrogate(rune(unit)) {
				if unit >= 0xD800 && unit <= 0xDBFF {
					if low, after, ok := powerShellCodeUnit(s, next); ok && low >= 0xDC00 && low <= 0xDFFF {
						b.WriteRune(utf16.DecodeRune(rune(unit), rune(low)))
						i = after - 1
						continue
					}
				}
				b.WriteRune(utf8.RuneError)
				i = next - 1
				continue
			}
			b.WriteRune(rune(unit))
			i = next - 1
			continue
		}
		b.WriteByte(c)
	}
	return "", 0, errors.New("unclosed PowerShell string")
}

// powerShellCodeUnit reads Chrome's $([char]N) escape. N is a UTF-16 code
// unit; a surrogate pair is combined by the caller.
func powerShellCodeUnit(s string, start int) (uint16, int, bool) {
	const prefix = "$([char]"
	if !strings.HasPrefix(s[start:], prefix) {
		return 0, start, false
	}
	end := strings.IndexByte(s[start+len(prefix):], ')')
	if end < 0 {
		return 0, start, false
	}
	number := strings.TrimSpace(s[start+len(prefix) : start+len(prefix)+end])
	n, err := strconv.Atoi(number)
	if err != nil || n < 0 || n > 0xFFFF {
		return 0, start, false
	}
	return uint16(n), start + len(prefix) + end + 1, true
}

// powerShellString reads a complete quoted PowerShell string.
func powerShellString(source string) (string, error) {
	if source == "" || source[0] != '\'' && source[0] != '"' {
		return "", errors.New("expected a quoted string")
	}
	value, next, err := powerShellQuoted(source, 0)
	if err != nil {
		return "", err
	}
	if strings.TrimSpace(source[next:]) != "" {
		return "", errors.New("unexpected text after the string")
	}
	return value, nil
}

// powerShellValue reads a parameter value: a quoted string or a bare word.
func powerShellValue(token psToken) (string, error) {
	if token.value == "" {
		return "", errors.New("empty value")
	}
	return token.value, nil
}

// powerShellHeaders reads @{ "name" = "value"; ... } into a header.
var powerShellPseudoHeaders = map[string]bool{
	"authority": true, "method": true, "path": true, "scheme": true, "version": true, "protocol": true,
}

func powerShellHeaders(tokens []psToken, at int) (http.Header, int, error) {
	headers := http.Header{}
	for at < len(tokens) {
		if tokens[at].value == "}" {
			return headers, at, nil
		}
		if tokens[at].value == ";" {
			at++
			continue
		}
		if at+2 >= len(tokens) || tokens[at+1].value != "=" {
			return nil, 0, errors.New("PowerShell -Headers needs name=value pairs")
		}
		name, err := powerShellValue(tokens[at])
		if err != nil {
			return nil, 0, err
		}
		value, err := powerShellValue(tokens[at+2])
		if err != nil {
			return nil, 0, err
		}
		if powerShellPseudoHeaders[strings.ToLower(name)] {
			at += 3
			continue
		}
		if !httpguts.ValidHeaderFieldName(name) || !httpguts.ValidHeaderFieldValue(value) {
			return nil, 0, fmt.Errorf("invalid PowerShell header %q", name)
		}
		headers.Add(name, value)
		at += 3
	}
	return nil, 0, errors.New("PowerShell -Headers hashtable is not closed")
}

// powerShellBody reads a string body, or Chrome's
// ([System.Text.Encoding]::UTF8.GetBytes("...")) wrapper for non-ASCII text.
func powerShellBody(tokens []psToken, at int) (string, bool, int, error) {
	if at >= len(tokens) {
		return "", false, 0, errors.New("Invoke-WebRequest -Body needs a value")
	}
	if tokens[at].value == "(" {
		if at+5 >= len(tokens) || !strings.EqualFold(tokens[at+1].value, "[System.Text.Encoding]::UTF8.GetBytes") ||
			tokens[at+2].value != "(" || tokens[at+4].value != ")" || tokens[at+5].value != ")" {
			return "", false, 0, errors.New("unsupported PowerShell -Body expression; paste a literal string body")
		}
		value, err := powerShellValue(tokens[at+3])
		return value, true, at + 6, err
	}
	value, err := powerShellValue(tokens[at])
	return value, true, at + 1, err
}

// powerShellCookie reads $session.Cookies.Add((New-Object
// System.Net.Cookie("name", "value", "/", "domain"))).
func powerShellCookie(statement string) (name, value, path, domain string, err error) {
	tokens, err := powerShellTokens(statement)
	if err != nil {
		return "", "", "", "", err
	}
	at := -1
	for i, token := range tokens {
		if strings.EqualFold(token.value, "Cookie") || strings.HasSuffix(strings.ToLower(token.value), ".cookie") {
			at = i
			break
		}
	}
	if at < 0 || at+1 >= len(tokens) || tokens[at+1].value != "(" {
		return "", "", "", "", errors.New("invalid PowerShell cookie; expected New-Object System.Net.Cookie(name, value)")
	}
	var values []string
	for i := at + 2; i < len(tokens) && tokens[i].value != ")"; i++ {
		if tokens[i].value == "," {
			continue
		}
		values = append(values, tokens[i].value)
	}
	if len(values) < 2 {
		return "", "", "", "", errors.New("invalid PowerShell cookie; expected a name and value")
	}
	name, value = values[0], values[1]
	if len(values) > 2 {
		path = values[2]
	}
	if len(values) > 3 {
		domain = values[3]
	}
	return name, value, path, domain, nil
}
