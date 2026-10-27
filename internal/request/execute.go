package request

import (
	"context"
	"errors"
	"fmt"
	"maps"
	"net/http"
	"net/url"
	"slices"
	"strings"
	"time"

	"github.com/baba983/vscroxy/internal/capture"
)

// Execution is one send's outcome: the request as sent, the response or
// WebSocket replies, check and script results, the variables afterwards, and
// the Stage that failed when Err is set.
type Execution struct {
	Request        *Prepared
	Response       *capture.Flow
	Replies        []string
	Tests          []TestResult
	Vars           map[string]string
	Err            error
	Stage          string
	ExpectedStatus int
	Console        []ConsoleMessage
	Variables      []ResolvedVariable
}

type debugRunKey struct{}

// WithRunKey tags the sends Execute makes under ctx with key, so the captured
// wire exchanges link back to this run. The key never leaves the process.
func WithRunKey(ctx context.Context, key string) context.Context {
	return context.WithValue(ctx, debugRunKey{}, key)
}

// Execute runs pre-request script, encoding, send through srv, checks and
// post-response script, in that order. A single send and each Runner step
// use it alike, with the same proxy and cookie jar.
func (r Request) Execute(ctx context.Context, srv *capture.Server, vars map[string]string, jar http.CookieJar) (out Execution) {
	run := &scriptRun{vars: maps.Clone(vars), phase: "setup"}
	out = Execution{Vars: run.vars, Stage: "Request setup failed"}
	defer func() {
		out.Console = slices.Clone(run.console)
		if out.Err != nil {
			out.Console = append(out.Console, ConsoleMessage{run.phase, "error", out.Err.Error()})
		}
	}()
	status, _, _, err := r.Checks.Parse()
	if err != nil {
		out.Err = err
		return out
	}
	out.ExpectedStatus = status
	// Pre-scripts edit authored templates, not an already encoded request.
	// Generate variables first; expand and encode exactly once afterwards.
	template := &Prepared{Method: r.Method, URL: r.URL, Body: r.Body,
		Header: ParseHeaderBlock(r.Headers), editedHeaders: map[string]bool{}}
	out.Stage = "Pre-request script failed"
	run.phase = "pre-request"
	if err := runScriptContext(ctx, r.Pre, run, map[string]any{"req": template}); err != nil {
		out.Err = fmt.Errorf("Pre-request script: %w", err)
		out.Tests, out.Vars = run.tests, run.vars
		return out
	}
	r.Method, r.URL, r.Headers = template.Method, template.URL, HeaderBlock(template.Header)
	if template.editedBody {
		r.BodyMode = "Raw" // Explicit script bodies override structured forms.
	}
	r.Body = template.Body
	authHeader := "Authorization"
	if r.Auth.Type == "API key" {
		authHeader = ExpandVars(r.Auth.Key, run.vars)
	}
	if template.editedHeaders[http.CanonicalHeaderKey(authHeader)] && (r.Auth.Type != "API key" || r.Auth.Location != "Query") {
		r.Auth = Auth{}
	}
	out.Stage, run.phase = "Request setup failed", "setup"
	out.Tests, out.Vars = run.tests, run.vars
	out.Variables = r.ResolveVariables(run.vars, run.written)
	if r.Checks != (Checks{}) {
		var missing []string
		for _, name := range r.Variables() {
			if _, ok := run.vars[name]; !ok {
				missing = append(missing, name)
			}
		}
		if len(missing) > 0 {
			out.Err = fmt.Errorf("Set environment variables before running: %s", strings.Join(missing, ", "))
			return out
		}
	}
	req, err := r.Prepare(run.vars)
	if err != nil {
		out.Err = err
		return out
	}
	// Generated Content-Type/auth must not undo explicit set/remove calls.
	for name := range template.editedHeaders {
		req.Remove(name)
		for _, value := range template.Header.Values(name) {
			req.Header.Add(name, ExpandVars(value, run.vars))
		}
	}
	out.Request = req
	if IsWebSocket(req.URL) && r.Checks != (Checks{}) {
		out.Err = fmt.Errorf("response checks require an HTTP(S) request, not a WebSocket exchange")
		return out
	}
	u, err := url.Parse(req.URL)
	if err != nil || u.Hostname() == "" || u.Scheme != "http" && u.Scheme != "https" && u.Scheme != "ws" && u.Scheme != "wss" {
		out.Stage, out.Err = "Request setup failed", fmt.Errorf("use an absolute HTTP(S) or WebSocket URL with a host")
		out.Tests, out.Vars = run.tests, run.vars
		return out
	}
	out.Stage, run.phase = "Network error", "network"
	if IsWebSocket(req.URL) {
		out.Replies, out.Err = wsSend(ctx, srv.Addr(), req, srv.CACertificate(), jar, 2*time.Second)
		if r.Post != "" && out.Err == nil {
			out.Stage = "Post-response script failed"
			out.Err = fmt.Errorf("post-response scripts require an HTTP response")
		}
	} else {
		runKey, _ := ctx.Value(debugRunKey{}).(string)
		got, err := srv.ReplayWithCookies(ctx, capture.Flow{DebugRun: runKey, Method: req.Method, URL: req.URL, RequestHeader: req.Header, RequestBody: req.Body}, jar, r.Checks == (Checks{}))
		out.Err = err
		if err == nil {
			out.Response = &got
			run.tests = append(run.tests, r.Checks.Evaluate(got)...)
			res := &jsResponse{Status: got.Status, Body: got.ResponseBody, Header: got.ResponseHeader}
			out.Stage = "Post-response script failed"
			run.phase = "post-response"
			if err := runScriptContext(ctx, r.Post, run, map[string]any{"res": res, "req": req}); err != nil {
				out.Err = fmt.Errorf("Post-response script: %w", err)
			}
		}
	}
	out.Tests, out.Vars = run.tests, run.vars
	return out
}

// Outcome separates transport/setup failures from HTTP responses and checks.
// A failed assertion never turns a received response into a network failure.
func (r Execution) Outcome() string {
	switch {
	case errors.Is(r.Err, context.Canceled):
		return "Cancelled"
	case errors.Is(r.Err, context.DeadlineExceeded):
		return "Timed out"
	case r.Err != nil:
		return r.Stage
	case r.Response != nil && r.Response.Status >= 400:
		return "HTTP error response"
	case slices.ContainsFunc(r.Tests, func(t TestResult) bool { return !t.Pass }):
		return "Checks failed"
	case r.Response == nil:
		return "WebSocket completed"
	default:
		return "Response received"
	}
}

func (r Execution) Failed() bool {
	if r.Err != nil || r.Response != nil && r.Response.Status >= 400 && r.Response.Status != r.ExpectedStatus {
		return true
	}
	return slices.ContainsFunc(r.Tests, func(t TestResult) bool { return !t.Pass })
}
