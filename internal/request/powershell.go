package request

import (
	"errors"
	"fmt"
	"strings"
)

// PowerShell's native HTTP command, not curl.exe with different quoting.
// Like DevTools, reserved headers use their dedicated command parameters.
func powerShellSnippet(method string, req *Prepared, headers []snippetHeader) (string, error) {
	if req.Body != "" && (strings.EqualFold(method, "GET") || strings.EqualFold(method, "HEAD")) {
		return "", errors.New("PowerShell cannot preserve a GET or HEAD body; copy it as cURL instead")
	}
	var b strings.Builder
	b.WriteString("# PowerShell 7+; scripts and variables are not evaluated by vscroxy.\n")
	b.WriteString("$request = @{\n  Uri = " + powerShellQuote(req.URL) + "\n  CustomMethod = " + powerShellQuote(method) + "\n  MaximumRedirection = 0\n  SkipHttpErrorCheck = $true\n  SkipHeaderValidation = $true\n")
	var ordinary []snippetHeader
	seen := make(map[string]bool)
	for _, h := range headers {
		name := strings.ToLower(h.name)
		if seen[name] {
			return "", errors.New("PowerShell cannot preserve header names repeated with different casing; copy it as cURL instead")
		}
		seen[name] = true
		value, err := joined(h, "PowerShell")
		if err != nil {
			return "", err
		}
		// .NET's header encoder cannot reproduce UTF-8 header bytes from
		// arbitrary Unicode strings. Refuse rather than silently re-encode.
		if strings.ContainsFunc(value, func(r rune) bool { return r > 127 }) {
			return "", errors.New("PowerShell export requires ASCII header values; copy it as cURL instead")
		}
		switch strings.ToLower(h.name) {
		case "content-type", "user-agent":
			if len(h.values) != 1 {
				return "", fmt.Errorf("PowerShell cannot preserve repeated %s headers", h.name)
			}
			parameter := "ContentType"
			if strings.EqualFold(h.name, "User-Agent") {
				parameter = "UserAgent"
			}
			b.WriteString("  " + parameter + " = " + powerShellQuote(value) + "\n")
		default:
			ordinary = append(ordinary, snippetHeader{h.name, []string{value}})
		}
	}
	if !seen["content-type"] && strings.EqualFold(method, "POST") {
		return "", errors.New("PowerShell adds a Content-Type to POST requests; supply one explicitly or copy it as cURL instead")
	}
	if len(ordinary) > 0 {
		b.WriteString("  Headers = @{\n")
		for _, h := range ordinary {
			b.WriteString("    " + powerShellQuote(h.name) + " = " + powerShellQuote(h.values[0]) + "\n")
		}
		b.WriteString("  }\n")
	}
	if req.Body != "" {
		b.WriteString("  Body = [System.Text.Encoding]::UTF8.GetBytes(" + powerShellQuote(req.Body) + ")\n")
	}
	b.WriteString("}\n$response = Invoke-WebRequest @request\n$response.StatusCode\n$response.Content\n")
	return b.String(), nil
}

// Double-quoted strings escape every interpolation/quote metacharacter.
// Numeric expressions preserve control characters, Unicode (including smart
// quotes which PowerShell also recognizes as delimiters) and non-BMP text.
func powerShellQuote(s string) string {
	var b strings.Builder
	b.WriteByte('"')
	for _, r := range s {
		switch {
		case r == '`' || r == '$' || r == '"':
			b.WriteByte('`')
			b.WriteRune(r)
		case r < 32 || r > 126:
			if r > 0xffff {
				fmt.Fprintf(&b, "$([char]::ConvertFromUtf32(%d))", r)
			} else {
				fmt.Fprintf(&b, "$([char]%d)", r)
			}
		default:
			b.WriteRune(r)
		}
	}
	b.WriteByte('"')
	return b.String()
}
