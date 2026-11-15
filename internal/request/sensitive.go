package request

import (
	"net/http"
	"net/url"
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
}

// IsSensitiveHeader reports whether name carries a credential or session
// state. Names are matched case-insensitively. code is deliberately not here:
// it is an OAuth query parameter, but a JSON or form field named code is often
// the error detail a repro needs to keep.
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

// IsSensitiveQuery reports whether a URL query parameter is a credential.
// code is the OAuth authorization code; unlike a body field, it is redacted.
func IsSensitiveQuery(name string) bool {
	return IsSensitiveHeader(name) || strings.EqualFold(name, "code")
}

// RedactURL hides credential query values and URL userinfo passwords.
func RedactURL(raw string) string {
	u, err := url.Parse(raw)
	if err != nil {
		return raw
	}
	if u.User != nil {
		if _, set := u.User.Password(); set {
			u.User = url.UserPassword(u.User.Username(), "••••")
		}
	}
	query := u.Query()
	for name := range query {
		if IsSensitiveQuery(name) {
			query.Set(name, "••••")
		}
	}
	u.RawQuery = query.Encode()
	return u.String()
}
