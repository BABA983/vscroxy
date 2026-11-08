package app

import (
	"cmp"
	"encoding/base64"
	"errors"
	"fmt"
	"io"
	"maps"
	"net/http"
	"os"
	"slices"
	"strconv"
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/baba983/vscroxy/internal/capture"
	"github.com/baba983/vscroxy/internal/imports"
	"github.com/baba983/vscroxy/internal/request"
	"github.com/baba983/vscroxy/internal/uikit"
	"github.com/baba983/vscroxy/locales"
	"github.com/egoist/mygo/ui"
	"golang.org/x/net/http/httpguts"
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
	return curlCommandWith(f, nil, cmd)
}

// curlCommandForSettings writes an authored request as curl, including its
// transfer settings; nil settings keep the captured exchange's plain command.
func curlCommandForSettings(f flow, settings request.Settings, cmd bool) (string, error) {
	return curlCommandWith(f, &settings, cmd)
}

func curlCommandWith(f flow, settings *request.Settings, cmd bool) (string, error) {
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
	if settings != nil {
		// Only values the author set: cURL's own defaults fill the rest.
		if settings.FollowRedirects != nil && !*settings.FollowRedirects {
			// Manual: cURL stops at the first response without -L.
		} else if settings.FollowRedirects != nil || settings.MaxRedirects != nil || settings.RedirectMode == capture.RedirectCurl {
			b.WriteString(" -L")
		}
		if settings.MaxRedirects != nil && settings.Follows() {
			b.WriteString(" --max-redirs " + strconv.Itoa(*settings.MaxRedirects))
		}
		if settings.VerifyTLS != nil && !*settings.VerifyTLS {
			b.WriteString(" -k")
		}
		if settings.TimeoutSeconds != nil && *settings.TimeoutSeconds > 0 {
			b.WriteString(" --max-time " + strconv.Itoa(*settings.TimeoutSeconds))
		}
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

// parseCurl reads one request from a Bash/POSIX curl command, Chrome's
// Copy as cURL (cmd) output, or Chrome's Copy as PowerShell output.
func parseCurl(source string) (savedRequest, error) {
	r, _, err := imports.ParseCurl(source)
	return savedRequest{Request: r}, err
}

// parseCurlImport also returns what the command held but the request cannot:
// a cookie's domain/path, for example.
func parseCurlImport(source string) (savedRequest, []string, error) {
	r, warnings, err := imports.ParseCurl(source)
	return savedRequest{Request: r}, warnings, err
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
			uikit.DialogTitle(c, locales.ImportCurl2())
			ui.Scroll(c).MinHeight(0).Shrink(1).Gap(t.Space(4)).Padding(0, t.Space(1)).Children(func() {
				uikit.Hint(c, locales.PasteOneCurlCommandBash())
				if ui.TextArea(c, &a.curlInput).Height(190).Font(uikit.FontMono).FontSize(uikit.TextBody).Radius(uikit.RadiusControl).Label(locales.CurlCommand()).Placeholder("curl 'https://api.example.com/projects' \\\n  -H 'Accept: application/json'").AutoFocus().Changed() {
					a.curlProblem = ""
				}
				if a.curlProblem != "" {
					ui.Text(c, a.curlProblem).FontSize(uikit.TextBody).TextColor(t.Danger)
				}
			})
			uikit.DialogFooter(c).Children(func() {
				if uikit.SecondaryButton(c, locales.Cancel()).Clicked() {
					a.curlOpen, a.curlInput = false, ""
				}
				if uikit.PrimaryButton(c, locales.ImportRequest()).Disabled(strings.TrimSpace(a.curlInput) == "").Clicked() {
					r, warnings, err := parseCurlImport(a.curlInput)
					if err != nil {
						a.curlProblem = err.Error()
						return
					}
					a.addDocument(r, "")
					a.curlOpen, a.curlInput = false, ""
					if len(warnings) > 0 {
						a.warn("Imported cURL; " + strings.Join(warnings, "; "))
						return
					}
					a.done("Imported cURL as an unsent request; review headers and body before sending")
				}
			})
		})
	})
}
