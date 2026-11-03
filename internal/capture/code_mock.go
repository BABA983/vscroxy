package capture

import (
	"net/http"
	"strconv"

	"github.com/lqqyt2423/go-mitmproxy/proxy"
	"golang.org/x/net/http/httpguts"
)

// codeMock runs only the winning response rule. No failure here can fall
// through to an upstream: an absent/broken script is still a local 502.
func (s *Server) codeMock(f *proxy.Flow, r Rule) *proxy.Response {
	fail := func(status int, message string) *proxy.Response {
		s.scriptErrors.Add(1)
		recordAction(f, "Code Mock "+r.Target+" failed")
		return &proxy.Response{StatusCode: status,
			Header: http.Header{"Content-Type": {"text/plain; charset=utf-8"}},
			Body:   []byte("code mock: " + message + "\n")}
	}
	if f.Stream || len(f.Request.Body) > maxScriptBody {
		return fail(http.StatusRequestEntityTooLarge, "requires a complete buffered request body of at most 1 MiB")
	}
	if !textBody(f.Request.Body, f.Request.Header.Get("Content-Encoding")) {
		return fail(http.StatusUnsupportedMediaType, "requires an uncompressed UTF-8 request body")
	}
	runner := s.scriptRunner()
	if runner == nil {
		return fail(http.StatusBadGateway, "script unavailable")
	}
	d := debugOf(f)
	if d.locals == nil {
		d.locals = map[string]string{}
	}
	x := &ScriptExchange{Phase: "mock", Rule: scriptRuleName(r), App: s.clientProcess(f).Name,
		Method: f.Request.Method, URL: f.Request.URL.String(), Header: f.Request.Header.Clone(),
		Body: f.Request.Body, Locals: d.locals}
	if err := runner.Run(scriptContext(f), r.Target, x); err != nil {
		// Script errors may include request/response secrets. The script
		// Console owns diagnostics; never echo an exception to the client.
		return fail(http.StatusBadGateway, "script failed; see Scripts Console")
	}
	if x.Status < 200 || x.Status > 599 || len(x.ResponseBody) > maxScriptBody {
		return fail(http.StatusBadGateway, "response must have status 200–599 and a body of at most 1 MiB")
	}
	headers := make(http.Header)
	size := 0
	for name, values := range x.Response {
		if !httpguts.ValidHeaderFieldName(name) {
			return fail(http.StatusBadGateway, "invalid response headers")
		}
		for _, value := range values {
			size += len(name) + len(value) + 4
			if size > 64<<10 || !httpguts.ValidHeaderFieldValue(value) {
				return fail(http.StatusBadGateway, "invalid response headers or headers exceed 64 KiB")
			}
			headers.Add(name, value)
		}
	}
	StripHopHeaders(headers)
	// setBody produces decoded text, never a compressed/framed wire body.
	headers.Del("Content-Encoding")
	headers.Del("Content-Length")
	body := x.ResponseBody
	switch x.Status {
	case http.StatusNoContent, http.StatusResetContent, http.StatusNotModified:
		body = nil
	}
	if x.Status != http.StatusNoContent && x.Status != http.StatusNotModified {
		headers.Set("Content-Length", strconv.Itoa(len(body)))
	}
	if f.Request.Method == http.MethodHead {
		body = nil
	}
	recordAction(f, "Code Mock · "+r.Target)
	return &proxy.Response{StatusCode: x.Status, Header: headers, Body: body}
}

// streamCodeMock handles the path that skipped Request because its body
// passed the engine's buffering threshold. Other response rules retain their
// existing behavior and order.
func (s *Server) streamCodeMock(f *proxy.Flow) bool {
	s.rulesMu.RLock()
	list := s.rules
	s.rulesMu.RUnlock()
	i := firstResponseRule(list, s.ruleRequest(f))
	if i < 0 || list[i].Type != RuleCodeMock {
		return false
	}
	r := list[i]
	s.hit(r)
	if r.Scenario != "" {
		recordAction(f, "Scenario · "+r.Scenario)
	}
	s.answer(f, s.codeMock(f, r))
	return true
}
