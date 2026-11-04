// Package rules decides which traffic a rule, a list or a Host profile
// covers. Every place that matches traffic uses it, so a pattern means the
// same thing in the HTTPS, ignore and allow lists, DNS mappings and rules.
package rules

import (
	"fmt"
	"net/http"
	"path"
	"regexp"
	"slices"
	"strings"
	"sync"

	"golang.org/x/net/http/httpguts"
)

// Request is what a matcher looks at in an exchange.
type Request struct {
	URL        string // the full URL
	Method     string
	Host       string // the host name, without a port
	App        string // the client's executable name
	Header     http.Header
	Operations []string // verified selected GraphQL operation names
}

// How a Matcher's URL reads.
const (
	URLContains = ""         // a literal part of the URL
	URLWildcard = "wildcard" // the whole URL, * for any text and ? for one character; the scheme may be left out
	URLRegex    = "regex"    // a regular expression found anywhere in the URL
)

// How a HeaderCondition reads its value.
const (
	HeaderPresent  = "present"
	HeaderEquals   = "equals"
	HeaderContains = "contains"
	HeaderMatches  = "matches" // a regular expression found anywhere in a value
)

// HeaderOps are the header comparisons, in the order an editor offers them.
var HeaderOps = []string{HeaderEquals, HeaderContains, HeaderMatches, HeaderPresent}

// HeaderCondition needs a request header: present, or with one value that
// compares as Op says.
type HeaderCondition struct {
	Name  string `json:"name"`
	Op    string `json:"op"`
	Value string `json:"value,omitempty"`
}

// Matcher covers the requests that satisfy every condition it sets; an empty
// field matches any request.
type Matcher struct {
	URL       string // read as URLMode says
	URLMode   string
	Method    string   // the exact method
	Hosts     []string // host patterns, see Host
	Apps      []string // app patterns, see App
	Headers   []HeaderCondition
	Operation string // exact, case-sensitive GraphQL operation name
}

// Match reports whether r satisfies every condition of m.
func (m Matcher) Match(r Request) bool {
	return len(m.Mismatch(r)) == 0
}

// Mismatch explains, one reason per condition, why r is not covered; none
// means it is. A matcher that does not Validate covers nothing.
func (m Matcher) Mismatch(r Request) []string {
	var reasons []string
	if m.Method != "" && m.Method != r.Method {
		reasons = append(reasons, "method does not match")
	}
	if !m.url(r.URL) {
		reasons = append(reasons, "URL does not match")
	}
	if len(m.Hosts) > 0 && !Host(r.Host, m.Hosts) {
		reasons = append(reasons, "host does not match")
	}
	if len(m.Apps) > 0 && !App(r.App, m.Apps) {
		reasons = append(reasons, "app does not match")
	}
	if m.Operation != "" && (!validOperation(m.Operation) || !slices.Contains(r.Operations, m.Operation)) {
		reasons = append(reasons, "GraphQL operation does not match")
	}
	for _, h := range m.Headers {
		values := r.Header.Values(h.Name)
		switch {
		case len(values) == 0:
			reasons = append(reasons, fmt.Sprintf("no %s header", h.Name))
		case !h.matches(values):
			reasons = append(reasons, fmt.Sprintf("%s header does not match", h.Name))
		}
	}
	return reasons
}

// Validate reports a pattern or condition the matcher cannot use.
func (m Matcher) Validate() error {
	if m.Operation != "" && !validOperation(m.Operation) {
		return fmt.Errorf("GraphQL operation must be an exact name: [_A-Za-z][_0-9A-Za-z]*")
	}
	switch m.URLMode {
	case URLContains, URLWildcard:
	case URLRegex:
		if _, err := compile(m.URL); err != nil {
			return fmt.Errorf("URL regex: %w", err)
		}
	default:
		return fmt.Errorf("unknown URL match %q", m.URLMode)
	}
	for _, h := range m.Headers {
		if !httpguts.ValidHeaderFieldName(h.Name) {
			return fmt.Errorf("invalid header name %q", h.Name)
		}
		switch h.Op {
		case HeaderPresent, HeaderEquals, HeaderContains:
		case HeaderMatches:
			if _, err := compile(h.Value); err != nil {
				return fmt.Errorf("%s header regex: %w", h.Name, err)
			}
		default:
			return fmt.Errorf("unknown header comparison %q", h.Op)
		}
	}
	return nil
}

func validOperation(name string) bool {
	re, _ := compile(`^[_A-Za-z][_0-9A-Za-z]*$`)
	return re.MatchString(name)
}

func (m Matcher) url(raw string) bool {
	switch m.URLMode {
	case URLContains:
		return strings.Contains(raw, m.URL)
	case URLWildcard:
		re, err := compile(wildcard(m.URL))
		return err == nil && re.MatchString(raw)
	case URLRegex:
		re, err := compile(m.URL)
		return err == nil && re.MatchString(raw)
	}
	return false
}

// wildcard is the regular expression for a wildcard URL pattern.
func wildcard(pattern string) string {
	re := regexp.QuoteMeta(pattern)
	re = strings.NewReplacer(`\*`, ".*", `\?`, ".").Replace(re)
	if !strings.Contains(pattern, "://") {
		re = `[a-zA-Z][a-zA-Z0-9+.-]*://` + re
	}
	return "^" + re + "$"
}

func (h HeaderCondition) matches(values []string) bool {
	for _, v := range values {
		switch h.Op {
		case HeaderPresent:
			return true
		case HeaderEquals:
			if v == h.Value {
				return true
			}
		case HeaderContains:
			if strings.Contains(v, h.Value) {
				return true
			}
		case HeaderMatches:
			if re, err := compile(h.Value); err == nil && re.MatchString(v) {
				return true
			}
		}
	}
	return false
}

// compiled keeps the regular expressions rules use, which are matched on
// every request.
var compiled sync.Map // string → *regexp.Regexp

func compile(expr string) (*regexp.Regexp, error) {
	if re, ok := compiled.Load(expr); ok {
		return re.(*regexp.Regexp), nil
	}
	re, err := regexp.Compile(expr)
	if err != nil {
		return nil, err
	}
	compiled.Store(expr, re)
	return re, nil
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
