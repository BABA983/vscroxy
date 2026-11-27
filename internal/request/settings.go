package request

import (
	"crypto/tls"
	"fmt"
	"slices"
	"time"

	"github.com/baba983/vscroxy/internal/capture"
)

// Settings are the transport choices a request, folder or collection makes.
// A nil field inherits the nearest level that sets it, then the defaults: no
// timeout, follow redirects up to 10, verify TLS.
type Settings struct {
	// TimeoutSeconds is how long the HTTP exchange may take; nil inherits, 0
	// waits without a request limit.
	TimeoutSeconds *int `json:"timeoutSeconds,omitempty"`
	// FollowRedirects nil follows; false stops at the first 3xx.
	FollowRedirects *bool `json:"followRedirects,omitempty"`
	// MaxRedirects nil keeps the mode's default; it inherits on its own, so
	// turning following off and on again does not lose a folder's limit.
	MaxRedirects *int `json:"maxRedirects,omitempty"`
	// RedirectMode is capture.RedirectCurl for a cURL import's method and
	// Content-Type behavior; empty follows as an ordinary workspace send.
	RedirectMode string `json:"redirectMode,omitempty"`
	// KeepMethod and DropContentType are cURL's -X and implicit form header
	// rules, applied only while following.
	KeepMethod      bool `json:"keepMethod,omitempty"`
	DropContentType bool `json:"dropContentType,omitempty"`
	// VerifyTLS nil verifies; false accepts any certificate.
	VerifyTLS *bool `json:"verifyTLS,omitempty"`
	// HTTPVersion is "1.1" or "2"; empty negotiates. It applies to HTTP(S)
	// sends through the capture proxy; protocol clients keep their own version.
	HTTPVersion string `json:"httpVersion,omitempty"`
	// DisableURLEncoding sends query rows as typed instead of escaping them.
	DisableURLEncoding *bool `json:"disableUrlEncoding,omitempty"`
	// DisableCookies keeps the send from reading or writing the cookie jar.
	DisableCookies *bool `json:"disableCookies,omitempty"`
	// SocketPath sends HTTP(S) directly to a Unix domain socket on macOS and
	// Linux, or to a named pipe such as \\.\pipe\app on Windows, instead of
	// through the capture proxy. Empty uses the network.
	SocketPath string `json:"socketPath,omitempty"`
	// FollowOriginalMethod keeps the request's method and body when a 301,
	// 302 or 303 redirects; nil keeps Go's switch to GET.
	FollowOriginalMethod *bool `json:"followOriginalMethod,omitempty"`
	// KeepAuthorization sends the Authorization header to a redirect on a
	// different host; nil strips it, as Go does.
	KeepAuthorization *bool `json:"keepAuthorization,omitempty"`
	// RemoveReferer stops the automatic Referer header on a redirect.
	RemoveReferer *bool `json:"removeReferer,omitempty"`
	// StrictHTTP rejects a response whose header names or values are not
	// valid HTTP, instead of reading it leniently. nil accepts it.
	StrictHTTP *bool `json:"strictHttp,omitempty"`
	// DisabledTLSVersions are the TLS versions the handshake may not use. nil
	// keeps Go's defaults; an empty list explicitly enables every supported
	// version. The transport can only express a contiguous range for the
	// outer bounds; a disabled version inside it is rejected after the
	// handshake, before any HTTP request is sent.
	DisabledTLSVersions *[]string `json:"disabledTlsVersions,omitempty"`
	// CipherSuites are the TLS 1.0–1.2 cipher suites the handshake may use.
	// nil keeps Go's defaults. TLS 1.3 suites are chosen by crypto/tls and
	// cannot be configured.
	CipherSuites *[]string `json:"cipherSuites,omitempty"`
}

// IsZero reports whether the settings set nothing and therefore inherit every
// value.
func (s Settings) IsZero() bool {
	return s.TimeoutSeconds == nil && s.FollowRedirects == nil && s.MaxRedirects == nil &&
		s.RedirectMode == "" && !s.KeepMethod && !s.DropContentType &&
		s.VerifyTLS == nil && s.HTTPVersion == "" && s.DisableURLEncoding == nil &&
		s.DisableCookies == nil && s.SocketPath == "" && s.FollowOriginalMethod == nil &&
		s.KeepAuthorization == nil && s.RemoveReferer == nil && s.StrictHTTP == nil &&
		s.DisabledTLSVersions == nil && s.CipherSuites == nil
}

// Clone returns settings that can be edited without changing s.
func (s Settings) Clone() Settings {
	out := s
	if s.TimeoutSeconds != nil {
		value := *s.TimeoutSeconds
		out.TimeoutSeconds = &value
	}
	if s.FollowRedirects != nil {
		value := *s.FollowRedirects
		out.FollowRedirects = &value
	}
	if s.MaxRedirects != nil {
		value := *s.MaxRedirects
		out.MaxRedirects = &value
	}
	if s.VerifyTLS != nil {
		value := *s.VerifyTLS
		out.VerifyTLS = &value
	}
	if s.DisableURLEncoding != nil {
		value := *s.DisableURLEncoding
		out.DisableURLEncoding = &value
	}
	if s.DisableCookies != nil {
		value := *s.DisableCookies
		out.DisableCookies = &value
	}
	if s.StrictHTTP != nil {
		value := *s.StrictHTTP
		out.StrictHTTP = &value
	}
	cloneList := func(list *[]string) *[]string {
		if list == nil {
			return nil
		}
		value := slices.Clone(*list)
		return &value
	}
	out.DisabledTLSVersions = cloneList(s.DisabledTLSVersions)
	out.CipherSuites = cloneList(s.CipherSuites)
	return out
}

// FollowsOriginalMethod reports whether redirects keep the original method and
// body. Off by default, as Go and Postman's default behavior.
func (s Settings) FollowsOriginalMethod() bool {
	return s.FollowOriginalMethod != nil && *s.FollowOriginalMethod
}

// KeepsAuthorization reports whether Authorization survives a redirect to
// another host. Off by default.
func (s Settings) KeepsAuthorization() bool {
	return s.KeepAuthorization != nil && *s.KeepAuthorization
}

// RemovesReferer reports whether a redirect carries no Referer header. Off by
// default, where Go adds the previous URL.
func (s Settings) RemovesReferer() bool {
	return s.RemoveReferer != nil && *s.RemoveReferer
}

// tlsVersionNames are the versions the settings and UI know, oldest first.
var tlsVersionNames = []string{"1.0", "1.1", "1.2", "1.3"}

var tlsVersionIDs = map[string]uint16{
	"1.0": tls.VersionTLS10,
	"1.1": tls.VersionTLS11,
	"1.2": tls.VersionTLS12,
	"1.3": tls.VersionTLS13,
}

// SupportedTLSVersions lists the TLS versions the setting can disable.
func SupportedTLSVersions() []string { return slices.Clone(tlsVersionNames) }

// TLSCipherSuiteNames lists the cipher suites Go can select. TLS 1.3 suites
// are omitted because crypto/tls does not let a client restrict them.
func TLSCipherSuiteNames() []string {
	var names []string
	for _, suite := range tls.CipherSuites() {
		if slices.Contains(suite.SupportedVersions, tls.VersionTLS12) {
			names = append(names, suite.Name)
		}
	}
	return names
}

func tlsVersionID(name string) (uint16, bool) {
	id, ok := tlsVersionIDs[name]
	return id, ok
}

func cipherSuiteID(name string) (uint16, bool) {
	for _, suite := range tls.CipherSuites() {
		if suite.Name == name {
			return suite.ID, true
		}
	}
	return 0, false
}

// TLSVersions resolves the disabled list to the versions a handshake may use,
// or nil when the request keeps Go's defaults.
func (s Settings) TLSVersions() ([]uint16, error) {
	if s.DisabledTLSVersions == nil {
		return nil, nil
	}
	disabled := map[uint16]bool{}
	for _, name := range *s.DisabledTLSVersions {
		id, ok := tlsVersionID(name)
		if !ok {
			return nil, fmt.Errorf("unknown TLS version %q", name)
		}
		disabled[id] = true
	}
	var enabled []uint16
	for _, name := range tlsVersionNames {
		if id := tlsVersionIDs[name]; !disabled[id] {
			enabled = append(enabled, id)
		}
	}
	if len(enabled) == 0 {
		return nil, fmt.Errorf("select at least one TLS version")
	}
	return enabled, nil
}

// TLSCipherSuites resolves the selected suites to their wire IDs, or nil when
// the request keeps Go's defaults.
func (s Settings) TLSCipherSuites() ([]uint16, error) {
	if s.CipherSuites == nil {
		return nil, nil
	}
	if len(*s.CipherSuites) == 0 {
		return nil, fmt.Errorf("select at least one cipher suite")
	}
	ids := make([]uint16, 0, len(*s.CipherSuites))
	for _, name := range *s.CipherSuites {
		id, ok := cipherSuiteID(name)
		if !ok {
			return nil, fmt.Errorf("unknown or unconfigurable cipher suite %q", name)
		}
		ids = append(ids, id)
	}
	return ids, nil
}

// Follows reports whether the settings follow redirects. A zero hop limit
// stops at the first response even when following was also requested.
func (s Settings) Follows() bool {
	if s.MaxRedirects != nil && *s.MaxRedirects == 0 {
		return false
	}
	return s.FollowRedirects == nil || *s.FollowRedirects
}

// RedirectLimit is the number of redirects the settings follow.
func (s Settings) RedirectLimit() int {
	if s.MaxRedirects != nil {
		return *s.MaxRedirects
	}
	if s.RedirectMode == capture.RedirectCurl {
		return 50
	}
	return 10
}

// Verifies reports whether the settings verify the server certificate.
func (s Settings) Verifies() bool {
	return s.VerifyTLS == nil || *s.VerifyTLS
}

// EncodesURL reports whether query rows are escaped when they are written to
// the URL. Off sends them as typed.
func (s Settings) EncodesURL() bool {
	return s.DisableURLEncoding == nil || !*s.DisableURLEncoding
}

// UsesCookies reports whether the send reads and writes the cookie jar.
func (s Settings) UsesCookies() bool {
	return s.DisableCookies == nil || !*s.DisableCookies
}

// Timeout is the request's limit, or 0 when it has none.
func (s Settings) Timeout() time.Duration {
	if s.TimeoutSeconds == nil || *s.TimeoutSeconds <= 0 {
		return 0
	}
	return time.Duration(*s.TimeoutSeconds) * time.Second
}

// Unlimited reports an explicit request timeout of 0: the send has no request
// limit, rather than inheriting a shorter one.
func (s Settings) Unlimited() bool {
	return s.TimeoutSeconds != nil && *s.TimeoutSeconds == 0
}

// Transfer is the policy a send uses: redirects, certificate handling and TLS
// negotiation. Authored requests verify certificates unless they turn
// verification off; capture replay asks for neither and keeps the engine's
// inspection behavior.
func (s Settings) Transfer() (capture.TransferPolicy, error) {
	p := capture.TransferPolicy{Redirects: capture.RedirectPolicy{}, TLS: capture.TLSVerify, HTTPVersion: s.HTTPVersion}
	switch {
	case !s.Follows():
		p.Redirects.Mode = capture.RedirectManual
	case s.RedirectMode == capture.RedirectCurl:
		p.Redirects.Mode = capture.RedirectCurl
		p.Redirects.KeepMethod = s.KeepMethod
		p.Redirects.DropContentType = s.DropContentType
	}
	if s.MaxRedirects != nil {
		p.Redirects.Max = *s.MaxRedirects
	}
	if s.VerifyTLS != nil && !*s.VerifyTLS {
		p.TLS = capture.TLSInsecure
	}
	if s.FollowsOriginalMethod() {
		p.Redirects.KeepMethod = true
		p.Redirects.KeepBody = true
	}
	p.Redirects.KeepAuthorization = s.KeepsAuthorization()
	p.Redirects.RemoveReferer = s.RemovesReferer()
	p.StrictHTTP = s.StrictHTTP != nil && *s.StrictHTTP
	versions, err := s.TLSVersions()
	if err != nil {
		return p, err
	}
	p.TLSVersions = versions
	ciphers, err := s.TLSCipherSuites()
	if err != nil {
		return p, err
	}
	p.TLSCipherSuites = ciphers
	return p, nil
}

// InheritSettings resolves s against the folders the request lives in, the
// collection first: the nearest level that sets a field wins.
func (c Context) InheritSettings(s Settings) Settings {
	var out Settings
	merge := func(v Settings) {
		if v.TimeoutSeconds != nil {
			value := *v.TimeoutSeconds
			out.TimeoutSeconds = &value
		}
		if v.FollowRedirects != nil {
			value := *v.FollowRedirects
			out.FollowRedirects = &value
		}
		if v.MaxRedirects != nil {
			value := *v.MaxRedirects
			out.MaxRedirects = &value
		}
		if v.RedirectMode != "" {
			out.RedirectMode = v.RedirectMode
			out.KeepMethod = v.KeepMethod
			out.DropContentType = v.DropContentType
		}
		if v.VerifyTLS != nil {
			value := *v.VerifyTLS
			out.VerifyTLS = &value
		}
		if v.HTTPVersion != "" {
			out.HTTPVersion = v.HTTPVersion
		}
		if v.DisableURLEncoding != nil {
			value := *v.DisableURLEncoding
			out.DisableURLEncoding = &value
		}
		if v.DisableCookies != nil {
			value := *v.DisableCookies
			out.DisableCookies = &value
		}
		if v.SocketPath != "" {
			out.SocketPath = v.SocketPath
		}
		if v.FollowOriginalMethod != nil {
			value := *v.FollowOriginalMethod
			out.FollowOriginalMethod = &value
		}
		if v.KeepAuthorization != nil {
			value := *v.KeepAuthorization
			out.KeepAuthorization = &value
		}
		if v.RemoveReferer != nil {
			value := *v.RemoveReferer
			out.RemoveReferer = &value
		}
		if v.StrictHTTP != nil {
			value := *v.StrictHTTP
			out.StrictHTTP = &value
		}
		if v.DisabledTLSVersions != nil {
			value := slices.Clone(*v.DisabledTLSVersions)
			out.DisabledTLSVersions = &value
		}
		if v.CipherSuites != nil {
			value := slices.Clone(*v.CipherSuites)
			out.CipherSuites = &value
		}
	}
	for _, f := range c.Folders {
		merge(f.Settings)
	}
	merge(s)
	return out
}

// Timeout is how long a send of r may take under in; 0 has no request limit.
func (r Request) Timeout(in Context) time.Duration {
	return in.InheritSettings(r.Settings).Timeout()
}

// TimeoutUnlimited reports whether r explicitly asks for no request limit.
func (r Request) TimeoutUnlimited(in Context) bool {
	return in.InheritSettings(r.Settings).Unlimited()
}
