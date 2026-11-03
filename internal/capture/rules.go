package capture

import (
	"crypto/rand"
	"encoding/json/jsontext"
	"encoding/json/v2"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/baba983/vscroxy/internal/rules"
	"github.com/baba983/vscroxy/internal/store"
	"golang.org/x/net/http/httpguts"
)

func (r Rule) matches(req rules.Request) bool {
	return r.matchReason(req) == ""
}

// matcher is what a rule matches.
func (r Rule) matcher() rules.Matcher {
	return rules.Matcher{URL: r.Match, URLMode: r.MatchMode, Method: r.Method, Apps: r.Apps, Headers: r.When}
}

func (r Rule) matchReason(req rules.Request) string {
	var reasons []string
	if r.Disabled {
		reasons = append(reasons, "disabled")
	}
	if r.Scenario != "" && !r.ScenarioActive {
		reasons = append(reasons, fmt.Sprintf("inactive scenario %q", r.Scenario))
	}
	reasons = append(reasons, r.matcher().Mismatch(req)...)
	return strings.Join(reasons, "; ")
}

func responseRule(kind RuleType) bool {
	return kind == RuleMapLocal || kind == RuleMapRemote || kind == RuleBlock
}

// ScenarioRule reports whether a rule of kind may belong to a scenario:
// those that change traffic, not breakpoints.
func ScenarioRule(kind RuleType) bool {
	return responseRule(kind) || kind == RuleScript
}

// firstResponseRule is the Map or Block rule that acts on req: the first
// that matches, or for a WebSocket handshake the first Map Remote rule, since
// the others do not act on WebSockets.
func firstResponseRule(list []Rule, req rules.Request) int {
	ws := webSocketURL(req.URL)
	return slices.IndexFunc(list, func(r Rule) bool {
		return responseRule(r.Type) && (!ws || r.Type == RuleMapRemote) && r.matches(req)
	})
}

func webSocketURL(raw string) bool {
	return strings.HasPrefix(raw, "ws://") || strings.HasPrefix(raw, "wss://")
}

// mayMapHost reports whether an active Map Remote rule could send a request
// to hostport elsewhere, or a Map Local rule answer it without the server.
// A CONNECT names only the host, so a rule counts unless what it matches
// names another host: a path-only pattern, a wildcard without a path or a
// regular expression that does not start with a literal host could match any.
func mayMapHost(list []Rule, hostport string) bool {
	host := strings.ToLower(hostport)
	if h, _, err := net.SplitHostPort(host); err == nil {
		host = h
	}
	for _, r := range list {
		if r.Type != RuleMapRemote && r.Type != RuleMapLocal || r.Disabled || r.Scenario != "" && !r.ScenarioActive {
			continue
		}
		pattern := strings.ToLower(r.Match)
		if _, rest, ok := strings.Cut(pattern, "://"); ok {
			pattern = rest
		}
		named, _, hasPath := strings.Cut(pattern, "/")
		switch {
		case r.MatchMode == rules.URLRegex:
			if literal, ok := regexHost(r.Match); !ok || literal == host || literal == strings.ToLower(hostport) {
				return true
			}
		case named == "":
			return true
		case r.MatchMode == rules.URLWildcard:
			if !hasPath || (rules.Matcher{URL: "x://" + named + "/", URLMode: rules.URLWildcard}).Match(rules.Request{URL: "x://" + strings.ToLower(hostport) + "/"}) ||
				(rules.Matcher{URL: "x://" + named + "/", URLMode: rules.URLWildcard}).Match(rules.Request{URL: "x://" + host + "/"}) {
				return true
			}
		case !hasPath:
			if strings.Contains(strings.ToLower(hostport), named) {
				return true
			}
		default: // a URL part with a path: the host part ends the URL's host
			if strings.HasSuffix(host, named) || strings.HasSuffix(strings.ToLower(hostport), named) {
				return true
			}
		}
	}
	return false
}

// regexHost is the host a regular expression anchored at the start of the
// URL names literally, as MockPattern writes them: ^https://api\.test/….
func regexHost(expr string) (string, bool) {
	rest, ok := strings.CutPrefix(expr, "^")
	if !ok {
		return "", false
	}
	for _, scheme := range []string{"https?://", "https://", "http://"} {
		if after, found := strings.CutPrefix(rest, scheme); found {
			rest = after
			break
		}
	}
	end := strings.Index(rest, "/")
	if end <= 0 {
		return "", false
	}
	host := strings.ReplaceAll(rest[:end], `\.`, ".")
	// An unescaped dot is taken for itself, as people write hosts in patterns.
	if strings.ContainsAny(host, `\+*?()|[]{}^$`) {
		return "", false
	}
	return strings.ToLower(host), true
}

// key is what a rule does, without its name and switches.
func (r Rule) key() string {
	when, _ := json.Marshal(r.When)
	return strings.Join([]string{string(r.Type), r.MatchMode, r.Match, r.Method, strings.Join(r.Apps, "\n"), string(when), r.Target,
		r.PathFrom, r.PathTo, fmt.Sprint(r.KeepHost), r.Block}, "\x00")
}

// RulePreview explains a saved rule's part in one exchange. Step is its place
// in the order the proxy applies the rules that act on it, from 1, and 0 for
// a rule that does not act; Reason then says why.
type RulePreview struct {
	Index  int
	Step   int
	Reason string
}

// PreviewRules follows the proxy for one request: every matching Script rule
// runs, in order, then the first matching request breakpoint pauses it, the
// first matching Map or Block rule answers or redirects it, and the first
// matching response breakpoint pauses a response that came from upstream,
// matched against the redirected URL. Breakpoints act only while breaking is
// on. Only Map Remote acts on a WebSocket. It does not read response files,
// contact upstreams or run scripts, so it cannot know what a script changes.
func PreviewRules(rules []Rule, req rules.Request, breaking bool) []RulePreview {
	preview := make([]RulePreview, len(rules))
	upstream := req
	winner := firstResponseRule(rules, req)
	if winner >= 0 && rules[winner].Type == RuleMapRemote {
		if from, err := url.Parse(req.URL); err == nil {
			if to, err := mapURL(from, rules[winner]); err == nil {
				upstream.URL, upstream.Host = to.String(), to.Hostname()
			}
		}
	}
	answered := winner >= 0 && rules[winner].Type != RuleMapRemote && rules[winner].Block != BlockHide
	first := map[RuleType]int{}
	step := 0
	act := func(i int) {
		step++
		preview[i].Step = step
	}
	stage := func(r Rule) int { // the order the proxy reaches each kind in
		switch {
		case r.Type == RuleScript:
			return 0
		case r.Type == RuleBreak:
			return 1
		case responseRule(r.Type):
			return 2
		}
		return 3
	}
	for phase := range 4 {
		for i, r := range rules {
			preview[i].Index = i
			if stage(r) != phase {
				continue
			}
			at := req
			if r.Type == RuleBreakResponse {
				at = upstream
			}
			reason := r.matchReason(at)
			switch {
			case webSocketURL(req.URL) && r.Type != RuleMapRemote:
				reason = "does not act on WebSockets"
			case reason != "":
			case responseRule(r.Type) && i != winner:
				reason = fmt.Sprintf("shadowed by rule %d", winner+1)
			case responseRule(r.Type) || r.Type == RuleScript:
				act(i)
			case !breaking:
				reason = "breakpoints are off"
			case r.Type == RuleBreakResponse && answered:
				reason = fmt.Sprintf("rule %d answers first", winner+1)
			default:
				if j, seen := first[r.Type]; seen {
					reason = fmt.Sprintf("rule %d pauses first", j+1)
				} else {
					first[r.Type] = i
					act(i)
				}
			}
			preview[i].Reason = reason
		}
	}
	return preview
}

// ActiveScenario returns the selection embedded in a validated rules snapshot.
// Empty means only ungrouped rules apply. Individual Disabled flags are separate.
func ActiveScenario(rules []Rule) string {
	for _, r := range rules {
		if r.ScenarioActive {
			return r.Scenario
		}
	}
	return ""
}

// SelectScenario returns a new snapshot with exactly one named group active,
// or none for an empty name. SaveRules and SetRules consume this same snapshot.
// All members carry the same group state; switching never changes Disabled.
func SelectScenario(rules []Rule, name string) ([]Rule, error) {
	next := cloneRules(rules)
	found := name == ""
	for i := range next {
		next[i].ScenarioActive = name != "" && next[i].Scenario == name
		found = found || next[i].ScenarioActive
	}
	if !found {
		return nil, fmt.Errorf("unknown scenario %q", name)
	}
	if err := ValidateRules(next); err != nil {
		return nil, err
	}
	return next, nil
}

func cloneRules(rules []Rule) []Rule {
	copy := slices.Clone(rules)
	for i := range copy {
		copy[i].Headers = make(http.Header)
		for name, values := range rules[i].Headers {
			for _, value := range values {
				copy[i].Headers.Add(name, value)
			}
		}
	}
	return copy
}

// ValidateRules checks the same contract for disk files and the rule editor.
// Local files need not exist yet: they may be produced by a build later.
func ValidateRules(rules []Rule) error {
	groups := make(map[string]bool)
	active := ""
	for i, r := range rules {
		fail := func(message string) error { return fmt.Errorf("rule %d: %s", i+1, message) }
		if r.Scenario == "" {
			if r.ScenarioActive {
				return fail("an ungrouped rule cannot activate a scenario")
			}
		} else {
			if !ScenarioRule(r.Type) {
				return fail("only Map Local, Map Remote, Block and Script rules can belong to a scenario")
			}
			if r.Scenario != strings.TrimSpace(r.Scenario) || !utf8.ValidString(r.Scenario) || strings.ContainsFunc(r.Scenario, unicode.IsControl) {
				return fail("scenario names must be trimmed text without control characters")
			}
			if enabled, exists := groups[r.Scenario]; exists && enabled != r.ScenarioActive {
				return fail("all rules in a scenario must share its active state")
			}
			groups[r.Scenario] = r.ScenarioActive
			if r.ScenarioActive {
				if active != "" && active != r.Scenario {
					return fail("only one scenario can be active")
				}
				active = r.Scenario
			}
		}
		if strings.TrimSpace(r.Match) == "" {
			return fail("enter a URL to match")
		}
		if err := r.matcher().Validate(); err != nil {
			return fail(err.Error())
		}
		if r.Method != "" && !httpguts.ValidHeaderFieldName(r.Method) {
			return fail("invalid HTTP method")
		}
		switch r.Type {
		case RuleMapRemote:
			u, err := url.Parse(r.Target)
			if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Hostname() == "" || u.User != nil || u.Fragment != "" {
				return fail("Map Remote needs an http(s) base URL without credentials or fragment")
			}
			if r.PathFrom != "" && (!strings.HasPrefix(r.PathFrom, "/") || !strings.HasPrefix(r.PathTo, "/")) {
				return fail("a path rewrite goes from a path starting with / to another")
			}
			if r.PathFrom == "" && r.PathTo != "" {
				return fail("enter the path to rewrite")
			}
		case RuleMapLocal:
			if strings.TrimSpace(r.Target) == "" {
				return fail("choose a local response file or folder")
			}
			if r.PathFrom != "" && !strings.HasPrefix(r.PathFrom, "/") || r.PathTo != "" {
				return fail("a folder's path prefix starts with /")
			}
			if r.Status != 0 && (r.Status < 200 || r.Status > 599) {
				return fail("response status must be between 200 and 599")
			}
		case RuleBlock:
			if !slices.Contains(BlockActions, r.Block) {
				return fail(fmt.Sprintf("unknown Block action %q", r.Block))
			}
		case RuleScript:
			name, ok := strings.CutSuffix(r.Target, ".js")
			if !ok || CheckScriptName(name) != nil {
				return fail("choose a script")
			}
		case RuleBreak, RuleBreakResponse:
		default:
			return fail("unknown rule type")
		}
		for name, values := range r.Headers {
			if !httpguts.ValidHeaderFieldName(name) {
				return fail("invalid response header name")
			}
			for _, value := range values {
				if !httpguts.ValidHeaderFieldValue(value) {
					return fail("invalid response header value")
				}
			}
		}
	}
	return nil
}

// MarshalRules validates rules and encodes them as a rules file holds them.
func MarshalRules(rules []Rule) ([]byte, error) {
	if err := ValidateRules(rules); err != nil {
		return nil, err
	}
	data, err := json.Marshal(rules, jsontext.WithIndent("  "))
	if err != nil {
		return nil, err
	}
	return append(data, '\n'), nil
}

// SaveRules replaces the file only after validation and a complete write.
// Callers apply the new rules to the running proxy only after this succeeds.
func SaveRules(path string, rules []Rule) error {
	data, err := MarshalRules(rules)
	if err != nil {
		return err
	}
	return store.WriteFile(path, data)
}

func readMockBody(path string) ([]byte, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	body, err := io.ReadAll(io.LimitReader(f, maxRuleBody+1))
	if err == nil && len(body) > maxRuleBody {
		err = fmt.Errorf("local responses are limited to 8 MiB; refusing to serve a truncated body")
	}
	return body, err
}

// StripHopHeaders removes the headers that belong to the connection a message
// came on, including any the Connection header names.
func StripHopHeaders(h http.Header) {
	for _, connection := range h.Values("Connection") {
		for name := range strings.SplitSeq(connection, ",") {
			h.Del(strings.TrimSpace(name))
		}
	}
	for _, name := range hopHeaders {
		h.Del(name)
	}
}

// NewMockRule keeps a complete captured response in an app-owned directory,
// independent of a capture's temporary spill files. The caller owns the new
// file and must remove it if saving the rule fails. Stream previews, missing
// spill files and oversized bodies are errors, never silent partial mocks.
func NewMockRule(dir string, f Flow) (Rule, error) {
	if f.Method == "CONNECT" || (f.Kind != "" && f.Kind != "http") || f.Status < 200 || f.Status > 599 {
		return Rule{}, fmt.Errorf("mock requires a completed HTTP response, not a tunnel or stream")
	}
	body := []byte(f.ResponseBody)
	if f.ResponseBodyFile != "" {
		var err error
		body, err = readMockBody(f.ResponseBodyFile)
		if err != nil {
			return Rule{}, err
		}
	}
	if len(body) > maxRuleBody || (f.ResponseBodySize > 0 && len(body) != f.ResponseBodySize) {
		return Rule{}, fmt.Errorf("complete response body unavailable or larger than 8 MiB")
	}
	r := Rule{Name: "Mock " + f.Method + " " + f.Path, Type: RuleMapLocal, Match: f.URL, Method: f.Method, Status: f.Status, Headers: f.ResponseHeader}
	return SaveMock(dir, r, body)
}

// SaveMock writes body to a new file in dir and returns r answering from it.
// The file is named for what body holds, so its type shows, and r's headers
// lose those that described the bytes as they came on the wire. The caller
// owns the new file and must remove it if saving the rule fails.
func SaveMock(dir string, r Rule, body []byte) (Rule, error) {
	if len(body) > maxRuleBody {
		return Rule{}, fmt.Errorf("local responses are limited to 8 MiB")
	}
	h := r.Headers.Clone()
	if h == nil {
		h = make(http.Header)
	}
	StripHopHeaders(h)
	// Captures and examples hold decoded bytes. Do not retain stale wire
	// framing or validators for the compressed representation received.
	for _, name := range []string{"Content-Encoding", "Content-Length", "Content-MD5", "Digest", "ETag"} {
		h.Del(name)
	}
	ext := ".bin"
	switch {
	case jsontext.Value(body).IsValid():
		ext = ".json"
	case utf8.Valid(body):
		ext = ".txt"
	}
	r.Headers, r.Target = h, filepath.Join(dir, rand.Text()+ext)
	if err := ValidateRules([]Rule{r}); err != nil {
		return Rule{}, err
	}
	if err := store.WriteFile(r.Target, body); err != nil {
		return Rule{}, err
	}
	return r, nil
}

// MockPattern is the regular expression that matches the requests a mock of
// raw answers: raw is an authored URL with the variables known filled in.
// A {{variable}} still in it matches one path segment, or the scheme, host
// and any base path when the URL starts with one; a :name segment matches
// one segment. The query and fragment are left out: any query matches. With
// no scheme, http and https both match.
func MockPattern(raw string) string {
	raw, _, _ = strings.Cut(raw, "#")
	if q := strings.Index(raw, "?"); q >= 0 && !strings.Contains(raw[q:], "}}") {
		raw = raw[:q]
	}
	var b strings.Builder
	b.WriteString("^")
	if !strings.HasPrefix(raw, "{{") && !strings.Contains(raw, "://") {
		b.WriteString("https?://")
	}
	for i, part := range strings.SplitAfter(raw, "/") {
		segment, slash := strings.CutSuffix(part, "/")
		if strings.HasPrefix(segment, ":") && len(segment) > 1 && i > 0 {
			b.WriteString("[^/?#]+")
		} else {
			for {
				start := strings.Index(segment, "{{")
				end := strings.Index(segment[max(start, 0):], "}}")
				if start < 0 || end < 0 {
					b.WriteString(regexp.QuoteMeta(segment))
					break
				}
				b.WriteString(regexp.QuoteMeta(segment[:start]))
				if i == 0 && start == 0 {
					b.WriteString("[^?#]*") // a base URL: scheme, host, maybe a path
				} else {
					b.WriteString("[^/?#]+")
				}
				segment = segment[start+end+2:]
			}
		}
		if slash {
			b.WriteString("/")
		}
	}
	b.WriteString(`(\?.*)?$`)
	return b.String()
}
