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
	// Locals are pm.variables, which a Runner hands to the next request.
	Locals map[string]string
	// Writes are the scripts' global, collection and vault changes, in order.
	Writes []Write
	// Skipped is a pre-request script's pm.execution.skipRequest(): nothing
	// was sent. Next is pm.execution.setNextRequest: nil keeps the Runner's
	// order, "" stops the run, otherwise a request's name or ID.
	Skipped bool
	Next    *string
}

type debugRunKey struct{}

// WithRunKey tags the sends Execute makes under ctx with key, so the captured
// wire exchanges link back to this run. The key never leaves the process.
func WithRunKey(ctx context.Context, key string) context.Context {
	return context.WithValue(ctx, debugRunKey{}, key)
}

// Execute runs the pre-request scripts, encoding, send through srv, checks
// and post-response scripts, in that order. Scripts run collection first,
// then each folder, then the request's own, as Postman runs them. A single
// send and each Runner step use it alike, with the same proxy and cookie jar.
//
// vars are the environment's values; Execution.Vars returns them with the
// scripts' writes, never the inherited, dynamic or vault values.
func (r Request) Execute(ctx context.Context, srv *capture.Server, in Context, vars map[string]string, jar http.CookieJar) (out Execution) {
	run := in.scriptRun(r, vars)
	runKey, _ := ctx.Value(debugRunKey{}).(string)
	run.send = func(req *Prepared) (capture.Flow, error) {
		return srv.ReplayWithCookies(ctx, capture.Flow{DebugRun: runKey, Method: req.Method, URL: req.URL, RequestHeader: req.Header, RequestBody: req.Body}, jar, true)
	}
	if jar != nil {
		run.cookies = func(target string) []*http.Cookie {
			u, err := url.Parse(target)
			if err != nil || u.Host == "" {
				return nil
			}
			return jar.Cookies(u)
		}
	}
	out = Execution{Vars: run.vars, Stage: "Request setup failed"}
	defer func() {
		out.Locals, out.Writes, out.Next = run.locals, run.writes, run.next
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
	r.Auth, _ = in.InheritAuth(r.Auth)
	// Pre-scripts edit authored templates, not an already encoded request.
	// Generate variables first; expand and encode exactly once afterwards.
	template := &Prepared{Method: r.Method, URL: r.URL, Body: r.Body,
		Header: ParseHeaderBlock(r.Headers), editedHeaders: map[string]bool{}}
	run.target = func() string { return ExpandVars(template.URL, run.effective()) }
	out.Stage = "Pre-request script failed"
	if err := run.scripts(ctx, "pre-request", in.Folders, func(f Folder) string { return f.Pre }, r.Pre, map[string]any{"req": template}); err != nil {
		out.Err = err
		out.Tests, out.Vars = run.tests, run.vars
		return out
	}
	if run.skipped {
		out.Stage, out.Skipped, out.Tests = "", true, run.tests
		return out
	}
	r.Method, r.URL, r.Headers = template.Method, template.URL, HeaderBlock(template.Header)
	if template.editedBody {
		r.BodyMode = "Raw" // Explicit script bodies override structured forms.
	}
	r.Body = template.Body
	effective := run.effective()
	authHeader := "Authorization"
	if r.Auth.Type == "API key" {
		authHeader = ExpandVars(r.Auth.Key, effective)
	}
	if template.editedHeaders[http.CanonicalHeaderKey(authHeader)] && (r.Auth.Type != "API key" || r.Auth.Location != "Query") {
		r.Auth = Auth{}
	}
	out.Stage, run.phase = "Request setup failed", "setup"
	out.Tests, out.Vars = run.tests, run.vars
	input := maps.Clone(run.vars)
	maps.Copy(input, run.locals)
	out.Variables = r.ResolveVariables(in.Apply(run.writes, r.Collection), input, run.written)
	r = r.withDynamic(effective)
	if r.Checks != (Checks{}) {
		var missing []string
		for _, name := range r.Variables() {
			if _, ok := effective[name]; !ok && !strings.HasPrefix(name, "vault:") {
				missing = append(missing, name)
			}
		}
		if len(missing) > 0 {
			out.Err = fmt.Errorf("Set environment variables before running: %s", strings.Join(missing, ", "))
			return out
		}
	}
	if err := in.unlock(r, effective); err != nil {
		out.Err = err
		return out
	}
	req, err := r.Prepare(effective)
	if err != nil {
		out.Err = err
		return out
	}
	// Generated Content-Type/auth must not undo explicit set/remove calls.
	for name := range template.editedHeaders {
		req.Remove(name)
		for _, value := range template.Header.Values(name) {
			req.Header.Add(name, ExpandVars(expandDynamic(value, effective), effective))
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
		if out.Err == nil && (r.Post != "" || slices.ContainsFunc(in.Folders, func(f Folder) bool { return f.Post != "" })) {
			out.Stage = "Post-response script failed"
			out.Err = fmt.Errorf("post-response scripts require an HTTP response")
		}
	} else {
		got, err := srv.ReplayWithCookies(ctx, capture.Flow{DebugRun: runKey, Method: req.Method, URL: req.URL, RequestHeader: req.Header, RequestBody: req.Body}, jar, r.Checks == (Checks{}))
		out.Err = err
		if err == nil {
			out.Response = &got
			run.tests = append(run.tests, r.Checks.Evaluate(got)...)
			res := &jsResponse{Status: got.Status, Body: got.ResponseBody, Header: got.ResponseHeader, Millis: got.Millis, Bytes: got.Bytes}
			run.target = func() string { return req.URL }
			out.Stage = "Post-response script failed"
			out.Err = run.scripts(ctx, "post-response", in.Folders, func(f Folder) string { return f.Post }, r.Post, map[string]any{"res": res, "req": req})
		}
	}
	out.Tests, out.Vars = run.tests, run.vars
	return out
}

// scripts runs one phase's scripts: each folder's, the collection first,
// then the request's own. The console tags folder output with its path.
func (run *scriptRun) scripts(ctx context.Context, phase string, folders []Folder, src func(Folder) string, own string, objects map[string]any) error {
	title := strings.ToUpper(phase[:1]) + phase[1:] + " script"
	for _, f := range folders {
		run.phase = phase + " · " + f.Path
		if err := runScriptContext(ctx, src(f), run, objects); err != nil {
			return fmt.Errorf("%s (%s): %w", title, f.Path, err)
		}
		if run.skipped {
			return nil
		}
	}
	run.phase = phase
	if err := runScriptContext(ctx, own, run, objects); err != nil {
		return fmt.Errorf("%s: %w", title, err)
	}
	return nil
}

// hostOf is the host a URL sends to, "" when it has none.
func hostOf(target string) string {
	u, err := url.Parse(target)
	if err != nil {
		return ""
	}
	return u.Hostname()
}

// unlock adds the {{vault:name}} values r uses to vars, each read for the
// host r is sent to, so a secret restricted to other domains stays locked.
func (in Context) unlock(r Request, vars map[string]string) error {
	host := hostOf(ExpandVars(r.URL, vars))
	for _, variable := range r.Variables() {
		name, ok := vaultName(variable)
		if !ok {
			continue
		}
		if in.Vault == nil {
			return fmt.Errorf("{{%s}}: the vault is not available", variable)
		}
		value, err := in.Vault(name, host)
		if err != nil {
			return fmt.Errorf("{{%s}}: %w", variable, err)
		}
		vars[variable] = value
	}
	return nil
}

// Outcome separates transport/setup failures from HTTP responses and checks.
// A failed assertion never turns a received response into a network failure.
func (r Execution) Outcome() string {
	switch {
	case r.Skipped:
		return "Skipped by script"
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
	if r.Skipped {
		return false
	}
	if r.Err != nil || r.Response != nil && r.Response.Status >= 400 && r.Response.Status != r.ExpectedStatus {
		return true
	}
	return slices.ContainsFunc(r.Tests, func(t TestResult) bool { return !t.Pass })
}
