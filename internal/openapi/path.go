package openapi

import (
	"regexp"
	"strings"
)

var braceVariable = regexp.MustCompile(`\{\{[^{}]+\}\}`)
var pathParameter = regexp.MustCompile(`\{[^{}]+\}`)
var colonParameter = regexp.MustCompile(`:[A-Za-z_][A-Za-z0-9_]*`)

// NormalizePath is a URL's path with every path parameter written :param, so a
// spec template and a saved request can be compared. Hosts, schemes and
// queries are not part of the identity. OpenAPI Sync and contract matching
// share it so the two features agree on what one endpoint is.
func NormalizePath(raw string) string {
	raw = strings.TrimSpace(raw)
	if i := strings.Index(raw, "://"); i >= 0 {
		rest := raw[i+3:]
		if j := strings.IndexByte(rest, '/'); j >= 0 {
			raw = rest[j:]
		} else {
			raw = "/"
		}
	}
	// A leading {{base}} is the host/base path, not a path parameter.
	if strings.HasPrefix(raw, "{{") {
		if end := strings.Index(raw, "}}"); end >= 0 {
			raw = raw[end+2:]
		}
	}
	raw, _, _ = strings.Cut(raw, "?")
	raw, _, _ = strings.Cut(raw, "#")
	raw = braceVariable.ReplaceAllString(raw, ":param")
	raw = pathParameter.ReplaceAllString(raw, ":param")
	raw = colonParameter.ReplaceAllString(raw, ":param")
	if !strings.HasPrefix(raw, "/") {
		raw = "/" + raw
	}
	return raw
}
