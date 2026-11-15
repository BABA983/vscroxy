package request

import (
	"net/http"
	"strings"
)

// SensitiveHeaders are request and response headers whose values are
// credentials or session state. Documentation, repro bundles and session
// replay hide or strip them by default.
var SensitiveHeaders = map[string]bool{
	"Authorization":        true,
	"Proxy-Authorization":  true,
	"Cookie":               true,
	"Set-Cookie":           true,
	"X-Api-Key":            true,
	"X-Auth-Token":         true,
	"Api-Key":              true,
	"X-Amz-Security-Token": true,
	// Code is the OAuth authorization code query parameter. Redacting a
	// header or JSON field of this name too is the safe default.
	"Code": true,
}

// IsSensitiveHeader reports whether name carries a credential or session
// state. Names are matched case-insensitively.
func IsSensitiveHeader(name string) bool {
	if SensitiveHeaders[http.CanonicalHeaderKey(name)] {
		return true
	}
	lower := strings.ToLower(name)
	return strings.Contains(lower, "token") || strings.Contains(lower, "secret") || strings.Contains(lower, "password") ||
		strings.Contains(lower, "api-key") || strings.Contains(lower, "api_key") || strings.Contains(lower, "apikey") ||
		strings.Contains(lower, "session") || strings.Contains(lower, "signature") || strings.Contains(lower, "credential") ||
		strings.Contains(lower, "auth")
}
