package capture

import (
	"crypto/rand"
	"encoding/json/jsontext"
	"encoding/json/v2"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
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

// key is what a rule does, without its name and switches.
func (r Rule) key() string {
	when, _ := json.Marshal(r.When)
	return strings.Join([]string{string(r.Type), r.MatchMode, r.Match, r.Method, strings.Join(r.Apps, "\n"), string(when), r.Target,
		r.PathFrom, r.PathTo, fmt.Sprint(r.KeepHost)}, "\x00")
}

// RulePreview explains a saved rule's part in one exchange. Step is its place
// in the order the proxy applies the rules that act on it, from 1, and 0 for
// a rule that does not act; Reason then says why.
type RulePreview struct {
	Index  int
	Step   int
	Reason string
}

// PreviewRules follows the proxy for one request: the first matching request
// breakpoint pauses it, the first matching Map or Block rule answers or
// redirects it, and the first matching response breakpoint pauses a response
// that came from upstream, matched against the redirected URL. Breakpoints
// act only while breaking is on. Only Map Remote acts on a WebSocket. It does not read response files, contact
// upstreams or run scripts.
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
	answered := winner >= 0 && rules[winner].Type != RuleMapRemote
	first := map[RuleType]int{}
	step := 0
	act := func(i int) {
		step++
		preview[i].Step = step
	}
	stage := func(r Rule) int { // the order the proxy reaches each kind in
		switch {
		case r.Type == RuleBreak:
			return 0
		case responseRule(r.Type):
			return 1
		}
		return 2
	}
	for phase := range 3 {
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
			case responseRule(r.Type):
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
			if !responseRule(r.Type) {
				return fail("only Map Local, Map Remote and Block rules can belong to a scenario")
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
				return fail("choose a local response file")
			}
			if r.Status != 0 && (r.Status < 200 || r.Status > 599) {
				return fail("response status must be between 200 and 599")
			}
		case RuleBlock, RuleBreak, RuleBreakResponse:
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
	h := f.ResponseHeader.Clone()
	if h == nil {
		h = make(http.Header)
	}
	StripHopHeaders(h)
	// Captures contain decoded bytes. Do not retain stale wire framing or
	// validators for the compressed representation that was received.
	for _, name := range []string{"Content-Encoding", "Content-Length", "Content-MD5", "Digest", "ETag"} {
		h.Del(name)
	}
	r := Rule{Name: "Mock " + f.Method + " " + f.Path, Type: RuleMapLocal, Match: f.URL, Method: f.Method, Status: f.Status, Headers: h,
		Target: filepath.Join(dir, rand.Text()+".bin")}
	if err := ValidateRules([]Rule{r}); err != nil {
		return Rule{}, err
	}
	if err := store.WriteFile(r.Target, body); err != nil {
		return Rule{}, err
	}
	return r, nil
}
