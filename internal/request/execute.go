package request

import (
	"cmp"
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

// errRequestTimeout is the cause of a send's own deadline, distinct from the
// caller's overall budget.
var errRequestTimeout = errors.New("request timeout")

// sendContext bounds the HTTP exchange by limit, when there is one.
func sendContext(ctx context.Context, limit time.Duration) (context.Context, context.CancelFunc) {
	if limit <= 0 {
		return ctx, func() {}
	}
	return context.WithTimeoutCause(ctx, limit, errRequestTimeout)
}

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
	if r.Protocol != "" {
		return Execution{Stage: "Request setup failed", Vars: vars, Err: fmt.Errorf("%s documents must be opened in their interactive protocol client; Runner supports HTTP and WebSocket sends", r.Protocol)}
	}
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
	// A source attempt invalidates old tokens even when declarative checks,
	// inherited scripts, or compilation of its own script fail before Pre.
	// Clear every available matching slot before reporting a missing one.
	for _, source := range r.TokenSources {
		_, exists := run.vars[source.Variable]
		if source.Environment == "" || source.Environment != in.Environment || source.Variable == "" || !exists {
			out.Err = errors.New("select the token source environment with its local Current slots")
			continue
		}
		run.vars[source.Variable] = ""
	}
	if out.Err != nil {
		return out
	}
	status, _, _, err := r.Checks.Parse()
	if err != nil {
		out.Err = err
		return out
	}
	out.ExpectedStatus = status
	r.Auth, _ = in.InheritAuth(r.Auth)
	// Pre-scripts edit authored templates, not an already encoded request.
	// Generate variables first; expand and encode exactly once afterwards.
	// Auth is r's own copy, so a script's edits reach the send, not the document.
	template := &Prepared{Method: r.Method, URL: FillPath(r.URL, r.PathVariables), Body: r.Body,
		Header: ParseHeaderBlock(r.Headers), Auth: &r.Auth, editedHeaders: map[string]bool{}}
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
	authHeader, authInHeader := "Authorization", true
	switch r.Auth.Type {
	case "API key":
		authHeader = ExpandVars(r.Auth.Key, effective)
		authInHeader = r.Auth.Location != "Query"
	case AuthJWT:
		authInHeader = r.Auth.JWT.AddTo != "Query"
	case AuthOAuth1:
		authInHeader = oauth1AddsToHeader(r.Auth.OAuth1)
	}
	if template.editedHeaders[http.CanonicalHeaderKey(authHeader)] && authInHeader {
		r.Auth = Auth{}
	}
	out.Stage, run.phase = "Request setup failed", "setup"
	out.Tests, out.Vars = run.tests, run.vars
	input := maps.Clone(run.vars)
	maps.Copy(input, run.data)
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
	if r.Auth.Type == AuthOAuth2 {
		out.Stage = "Access token failed"
		o, err := in.OAuth2Config(r.Auth, effective)
		var t Token
		if err == nil {
			t, err = cmp.Or(in.Tokens, &Tokens{}).get(o, ProxySender(ctx, srv))
		}
		if err != nil {
			out.Err = err
			return out
		}
		r.Auth.Token, r.Auth.OAuth2.HeaderPrefix = t.Access, o.HeaderPrefix
		out.Stage = "Request setup failed"
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
	// Sign again: the script's headers are part of what is signed.
	switch r.Auth.Type {
	case AuthAWS:
		if err := r.Auth.AWS.expand(effective).sign(req, now()); err != nil {
			out.Err = err
			return out
		}
	case AuthOAuth1:
		if err := r.Auth.OAuth1.expand(effective).defaults().sign(req, now()); err != nil {
			out.Err = err
			return out
		}
	case AuthHawk:
		if err := r.Auth.Hawk.expand(effective).sign(req); err != nil {
			out.Err = err
			return out
		}
	case AuthEdgeGrid:
		if err := r.Auth.EdgeGrid.expand(effective).sign(req); err != nil {
			out.Err = err
			return out
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
	if in.HTTPOnly && IsWebSocket(req.URL) {
		out.Stage, out.Err = "Request setup failed", errors.New("load runs support HTTP(S) requests only")
		return out
	}
	if IsWebSocket(req.URL) && r.Auth.Type == AuthNTLM {
		out.Stage, out.Err = "Request setup failed", errors.New("NTLM auth needs an HTTP(S) request, not a WebSocket exchange")
		return out
	}
	out.Stage, run.phase = "Network error", "network"
	settings := in.InheritSettings(r.Settings)
	// Load the configured client certificate and roots before the request
	// reaches the proxy, so a missing file or bad passphrase is reported here
	// instead of as an opaque CONNECT failure. The HTTP send itself uses the
	// capture server's snapshot, which the app and CLI set from this same
	// configuration; direct clients (WebSocket/SSE/gRPC/Socket.IO) use
	// in.Certificates themselves.
	if in.Certificates.Configured() && srv != nil {
		if err := in.Certificates.ValidateURL(req.URL, srv.CACertificate(), !settings.Verifies()); err != nil {
			out.Stage, out.Err = "Request setup failed", err
			return out
		}
	}
	if IsWebSocket(req.URL) {
		out.Replies, out.Err = wsSend(ctx, srv.Addr(), req, srv.CACertificate(), in.Certificates, jar, 2*time.Second)
		if out.Err == nil && (r.Post != "" || slices.ContainsFunc(in.Folders, func(f Folder) bool { return f.Post != "" })) {
			out.Stage = "Post-response script failed"
			out.Err = fmt.Errorf("post-response scripts require an HTTP response")
		}
	} else {
		policy := settings.Transfer()
		if r.Checks != (Checks{}) {
			policy.Redirects.Mode = capture.RedirectManual
		}
		send := func() (capture.Flow, error) {
			attemptCtx, cancel := sendContext(ctx, settings.Timeout())
			defer cancel()
			got, err := srv.ReplayWithPolicy(attemptCtx, capture.Flow{DebugRun: runKey, Method: req.Method, URL: req.URL, RequestHeader: req.Header, RequestBody: req.Body}, jar, policy)
			if err != nil && errors.Is(context.Cause(attemptCtx), errRequestTimeout) {
				err = fmt.Errorf("no response within %s: %w", settings.Timeout(), context.DeadlineExceeded)
			}
			return got, err
		}
		var got capture.Flow
		if r.Auth.Type == AuthNTLM {
			// The challenge and its answer must share one connection, which
			// the ordinary replay transport does not keep; the whole exchange
			// gets the request's timeout.
			authCtx, cancel := sendContext(ctx, settings.Timeout())
			flow := capture.Flow{DebugRun: runKey, Method: req.Method, URL: req.URL, RequestHeader: req.Header, RequestBody: req.Body}
			got, err = ntlmSend(authCtx, srv, flow, req, r.Auth.NTLM.expand(effective), jar, policy)
			timedOut := errors.Is(context.Cause(authCtx), errRequestTimeout)
			cancel()
			if err != nil && timedOut {
				err = fmt.Errorf("no response within %s: %w", settings.Timeout(), context.DeadlineExceeded)
			}
			if errors.Is(err, ErrNTLM) {
				out.Stage = "NTLM authentication failed"
			}
		} else {
			got, err = send()
			if err == nil && r.Auth.Type == AuthDigest {
				// The challenge is answered once; the answer's response is the result.
				answer, failed := digestAnswer(got, req, ExpandVars(r.Auth.Username, effective), ExpandVars(r.Auth.Password, effective))
				if failed != nil {
					out.Response, out.Stage, out.Err = &got, "Digest authentication failed", failed
					out.Tests, out.Vars = run.tests, run.vars
					return out
				}
				if answer != "" {
					req.Header.Set("Authorization", answer)
					got, err = send()
				}
			}
		}
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
