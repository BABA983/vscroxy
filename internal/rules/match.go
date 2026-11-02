// Package rules decides which traffic a rule, a list or a Host profile
// covers. Every place that matches traffic uses it, so a pattern means the
// same thing in the HTTPS, ignore and allow lists, DNS mappings and rules.
package rules

import (
	"path"
	"strings"
)

// Request is what a matcher looks at in an exchange.
type Request struct {
	URL    string // the full URL
	Method string
	Host   string // the host name, without a port
	App    string // the client's executable name
}

// Matcher covers the requests that satisfy every condition it sets; an empty
// field matches any request.
type Matcher struct {
	URL    string   // a substring of the URL
	Method string   // the exact method
	Hosts  []string // host patterns, see Host
	Apps   []string // app patterns, see App
}

// Match reports whether r satisfies every condition of m.
func (m Matcher) Match(r Request) bool {
	return len(m.Mismatch(r)) == 0
}

// Mismatch explains, one reason per condition, why r is not covered; none
// means it is.
func (m Matcher) Mismatch(r Request) []string {
	var reasons []string
	if m.Method != "" && m.Method != r.Method {
		reasons = append(reasons, "method does not match")
	}
	if !strings.Contains(r.URL, m.URL) {
		reasons = append(reasons, "URL does not match")
	}
	if len(m.Hosts) > 0 && !Host(r.Host, m.Hosts) {
		reasons = append(reasons, "host does not match")
	}
	if len(m.Apps) > 0 && !App(r.App, m.Apps) {
		reasons = append(reasons, "app does not match")
	}
	return reasons
}

// Host reports whether host matches one of the patterns, ignoring case and a
// trailing dot: a host with its subdomains, a leading dot for subdomains only,
// or a path.Match glob such as *.example.com. Blank patterns and patterns
// starting with # match nothing, so a list's comments can be passed as is.
func Host(host string, patterns []string) bool {
	host = strings.ToLower(strings.TrimSuffix(host, "."))
	if host == "" {
		return false
	}
	for _, pattern := range patterns {
		pattern = strings.ToLower(strings.TrimSpace(pattern))
		if pattern == "" || strings.HasPrefix(pattern, "#") {
			continue
		}
		if ok, _ := path.Match(pattern, host); ok {
			return true
		}
		if rest, ok := strings.CutPrefix(pattern, "."); ok {
			if host == rest || strings.HasSuffix(host, pattern) {
				return true
			}
			continue
		}
		if !strings.ContainsAny(pattern, "*?[") && (host == pattern || strings.HasSuffix(host, "."+pattern)) {
			return true
		}
	}
	return false
}

// App reports whether an app matches one of the patterns, ignoring case: a
// path.Match glob such as Google Chrome*, against the name or, for a path, the
// executable's name. Blank patterns and patterns starting with # match nothing.
func App(app string, patterns []string) bool {
	app = strings.ToLower(strings.TrimSpace(app))
	if app == "" {
		return false
	}
	name := app[strings.LastIndexAny(app, `/\`)+1:]
	for _, pattern := range patterns {
		pattern = strings.ToLower(strings.TrimSpace(pattern))
		if pattern == "" || strings.HasPrefix(pattern, "#") {
			continue
		}
		if ok, _ := path.Match(pattern, app); ok {
			return true
		}
		if ok, _ := path.Match(pattern, name); ok {
			return true
		}
	}
	return false
}
