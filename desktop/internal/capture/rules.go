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

	"golang.org/x/net/http/httpguts"
)

func (r Rule) matches(raw, method string) bool {
	return !r.Disabled && (r.Method == "" || r.Method == method) && strings.Contains(raw, r.Match)
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
	for i, r := range rules {
		fail := func(message string) error { return fmt.Errorf("rule %d: %s", i+1, message) }
		if strings.TrimSpace(r.Match) == "" {
			return fail("enter a URL substring to match")
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

// SaveRules replaces the file only after validation and a complete write.
// Callers apply the new rules to the running proxy only after this succeeds.
func SaveRules(path string, rules []Rule) error {
	if err := ValidateRules(rules); err != nil {
		return err
	}
	data, err := json.Marshal(rules, jsontext.WithIndent("  "))
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	f, err := os.CreateTemp(filepath.Dir(path), ".rules-*")
	if err != nil {
		return err
	}
	defer os.Remove(f.Name())
	if _, err = f.Write(append(data, '\n')); err == nil {
		err = f.Sync()
	}
	closeErr := f.Close()
	if err != nil {
		return err
	}
	if closeErr != nil {
		return closeErr
	}
	return os.Rename(f.Name(), path)
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

func stripHopHeaders(h http.Header) {
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
	stripHopHeaders(h)
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
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return Rule{}, err
	}
	if err := os.WriteFile(r.Target, body, 0o600); err != nil {
		return Rule{}, err
	}
	return r, nil
}
