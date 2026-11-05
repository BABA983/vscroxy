package request

import (
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
	return out
}

// Follows reports whether the settings follow redirects.
func (s Settings) Follows() bool {
	return s.FollowRedirects == nil || *s.FollowRedirects
}

// MaxRedirects is the number of redirects the settings follow.
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

// Transfer is the policy a send uses: redirects and certificate handling.
// Authored requests verify certificates unless they turn verification off;
// capture replay asks for neither and keeps the engine's inspection behavior.
func (s Settings) Transfer() capture.TransferPolicy {
	p := capture.TransferPolicy{Redirects: capture.RedirectPolicy{}, TLS: capture.TLSVerify}
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
	return p
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
