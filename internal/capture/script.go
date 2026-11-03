package capture

import (
	"context"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/lqqyt2423/go-mitmproxy/proxy"
)

// ScriptRunner runs the scripts Script rules name. capture cannot run them
// itself: the pm runtime is internal/request's, which builds on capture.
type ScriptRunner interface {
	// Hooks says which hooks the named script defines: neither when it is
	// missing or does not parse.
	Hooks(script string) (onRequest, onResponse bool)
	// Run runs the hook of x's phase on x, which it edits in place.
	Run(ctx context.Context, script string, x *ScriptExchange) error
}

// ScriptExchange is an exchange as a rule script reads and edits it. In the
// request phase the request's fields are what the script may change; in the
// response phase, the response's. A body that is not text, or is larger
// than 1 MiB, is left out and stays as it is: Binary says so. In the mock
// phase the request is read-only and the script generates the response.
type ScriptExchange struct {
	Phase string // "request", "response" or "mock"
	Rule  string // the rule's name, or its script's
	App   string

	Method       string
	URL          string
	Header       http.Header
	Body         []byte
	BinaryBody   bool
	Status       int
	Response     http.Header
	ResponseBody []byte
	BinaryResult bool
	// Locals are pm.variables, which the scripts of one exchange share from
	// its request to its response.
	Locals map[string]string
}

// CheckScriptName says what is wrong with a script's name, which names its
// file: name.js in the scripts folder.
func CheckScriptName(name string) error {
	switch {
	case strings.TrimSpace(name) == "":
		return fmt.Errorf("enter a name")
	case name != strings.TrimSpace(name) || strings.HasPrefix(name, "."):
		return fmt.Errorf("names cannot start or end with a space or start with a dot")
	case utf8.RuneCountInString(name) > 64:
		return fmt.Errorf("names are at most 64 characters")
	case strings.ContainsAny(name, `/\:*?"<>|`) || strings.ContainsFunc(name, unicode.IsControl):
		return fmt.Errorf(`names cannot contain / \ : * ? " < > |`)
	}
	return nil
}

// SetScriptRunner sets what runs Script rules' scripts; without one they do
// nothing.
func (s *Server) SetScriptRunner(r ScriptRunner) {
	s.scriptMu.Lock()
	s.scripts = r
	s.scriptMu.Unlock()
}

func (s *Server) scriptRunner() ScriptRunner {
	s.scriptMu.RLock()
	defer s.scriptMu.RUnlock()
	return s.scripts
}

// textBody is a body a script may read and edit.
func textBody(body []byte, encoding string) bool {
	return encoding == "" && len(body) <= maxScriptBody && utf8.Valid(body) && !strings.ContainsRune(string(body), 0)
}

// runRequestScripts runs the onRequest hooks of the Script rules that match
// f, in their order, each seeing what the ones before it changed, and keeps
// those with onResponse for its response.
func (s *Server) runRequestScripts(f *proxy.Flow) {
	runner := s.scriptRunner()
	d := debugOf(f)
	if runner == nil || d == nil || f.Request == nil || f.Request.URL == nil {
		return
	}
	s.rulesMu.RLock()
	list := s.rules
	s.rulesMu.RUnlock()
	for _, r := range list {
		if r.Type != RuleScript {
			continue
		}
		req := s.ruleRequest(f)
		if !r.matches(req) {
			continue
		}
		onRequest, onResponse := runner.Hooks(r.Target)
		if !onRequest && !onResponse {
			continue
		}
		s.hit(r)
		if r.Scenario != "" {
			recordAction(f, "Scenario · "+r.Scenario)
		}
		if onResponse {
			d.scripts = append(d.scripts, r)
		}
		if !onRequest {
			continue
		}
		if d.locals == nil {
			d.locals = map[string]string{}
		}
		binary := !textBody(f.Request.Body, f.Request.Header.Get("Content-Encoding"))
		x := &ScriptExchange{Phase: "request", Rule: scriptRuleName(r), App: req.App,
			Method: f.Request.Method, URL: f.Request.URL.String(), Header: f.Request.Header.Clone(), BinaryBody: binary, Locals: d.locals}
		if !binary {
			x.Body = f.Request.Body
		}
		if err := runner.Run(scriptContext(f), r.Target, x); err != nil {
			s.scriptErrors.Add(1)
			recordAction(f, "Request script "+r.Target+" failed")
			continue
		}
		if x.Method != "" {
			f.Request.Method = x.Method
		}
		if u, err := url.Parse(x.URL); err == nil && u.Host != "" {
			f.Request.URL = u
		}
		f.Request.Header = x.Header
		if !binary && string(x.Body) != string(f.Request.Body) {
			f.Request.Body = x.Body
			f.Request.Header.Del("Content-Length")
		}
		recordAction(f, "Request script "+r.Target)
	}
}

// scriptsResponse reports whether a script will edit f's response, so the
// Response hook leaves publishing to the stream modifier.
func scriptsResponse(f *proxy.Flow) bool {
	d := debugOf(f)
	return d != nil && len(d.scripts) > 0
}

// runResponseScripts runs the onResponse hooks of the rules that matched f's
// request, with body as f's decoded response body. It reports whether one
// ran, and whether one changed the body, which f.Response.Body then holds.
func (s *Server) runResponseScripts(f *proxy.Flow, body []byte) (ran, bodyEdited bool) {
	runner := s.scriptRunner()
	d := debugOf(f)
	if runner == nil || d == nil || f.Response == nil {
		return false, false
	}
	binary := !textBody(body, "")
	for _, r := range d.scripts {
		x := &ScriptExchange{Phase: "response", Rule: scriptRuleName(r), App: s.clientProcess(f).Name,
			Method: f.Request.Method, URL: f.Request.URL.String(), Header: f.Request.Header.Clone(),
			Status: f.Response.StatusCode, Response: f.Response.Header.Clone(), BinaryResult: binary, Locals: d.locals}
		if textBody(f.Request.Body, f.Request.Header.Get("Content-Encoding")) {
			x.Body = f.Request.Body
		} else {
			x.BinaryBody = true
		}
		if !binary {
			x.ResponseBody = body
		}
		if x.Response == nil {
			x.Response = http.Header{}
		}
		if err := runner.Run(scriptContext(f), r.Target, x); err != nil {
			s.scriptErrors.Add(1)
			recordAction(f, "Response script "+r.Target+" failed")
			continue
		}
		ran = true
		if x.Status > 0 {
			f.Response.StatusCode = x.Status
		}
		f.Response.Header = x.Response
		if !binary && string(x.ResponseBody) != string(body) {
			body, bodyEdited = x.ResponseBody, true
		}
		recordAction(f, "Response script "+r.Target)
	}
	if bodyEdited {
		f.Response.Body = body
	}
	return ran, bodyEdited
}

// scriptContext is the context a hook runs in: the request's values, but not
// its cancellation, which comes once the request has gone upstream, before
// the response hook. Scripts have their own time limits.
func scriptContext(f *proxy.Flow) context.Context {
	return context.WithoutCancel(f.Request.Raw().Context())
}

func scriptRuleName(r Rule) string {
	if r.Name != "" {
		return r.Name
	}
	return strings.TrimSuffix(r.Target, ".js")
}
