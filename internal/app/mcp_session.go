package app

import (
	"cmp"
	"context"
	"errors"
	"fmt"
	"maps"
	"net/url"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/baba983/vscroxy/internal/request"
)

// mcpEvent is one bounded timeline entry. Request contents, arguments and
// results live only here and in the session result; they never reach applog.
type mcpEvent struct {
	seq  int
	at   time.Time
	kind string
	name string
	text string
	size int
}

const (
	mcpTimelineLimit = 300
	mcpEventPreview  = 64 << 10
	mcpCloseTimeout  = 7 * time.Second
)

// mcpConnectionInfo describes the live connection without any secret: no
// header or environment values.
type mcpConnectionInfo struct {
	Transport, Endpoint, Command, Timeout string
	Insecure, Proxied                     bool
	EnvCount, ArgsCount                   int
}

// Only this bounded session crosses threads. Documents, editors and the UI
// stay on the UI thread; a replaced session's late updates land on the old
// object.
type mcpSession struct {
	mu       sync.Mutex
	client   *request.MCPClient
	plan     request.MCPPlan
	input    request.Request
	scope    request.Context
	vars     map[string]string
	ctx      context.Context
	cancel   context.CancelFunc
	opCancel context.CancelFunc
	done     chan struct{}
	notify   func()

	state, problem string
	connected      bool
	busy, finished bool
	server         request.MCPServerInfo
	connection     mcpConnectionInfo
	tools          []request.MCPTool
	resources      []request.MCPResource
	templates      []request.MCPResourceTemplate
	prompts        []request.MCPPrompt
	events         []mcpEvent
	seq, evicted   int
	result         *request.MCPResult
	resultSeq      int
	catalogNotice  string
	progress       string
	rpcSeq         int
}

type mcpSnapshot struct {
	state, problem, catalogNotice string
	connected, busy, finished     bool
	server                        request.MCPServerInfo
	connection                    mcpConnectionInfo
	tools                         []request.MCPTool
	resources                     []request.MCPResource
	templates                     []request.MCPResourceTemplate
	prompts                       []request.MCPPrompt
	events                        []mcpEvent
	evicted                       int
	result                        *request.MCPResult
	resultSeq                     int
	progress                      string
}

func (s *mcpSession) snapshot() mcpSnapshot {
	if s == nil {
		return mcpSnapshot{state: "Not connected", finished: true}
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	v := mcpSnapshot{
		state: s.state, problem: s.problem, catalogNotice: s.catalogNotice,
		connected: s.connected, busy: s.busy, finished: s.finished, server: s.server, connection: s.connection,
		tools: slices.Clone(s.tools), resources: slices.Clone(s.resources),
		templates: slices.Clone(s.templates), prompts: slices.Clone(s.prompts),
		events: slices.Clone(s.events), evicted: s.evicted, resultSeq: s.resultSeq,
		progress: s.progress,
	}
	if s.result != nil {
		result := *s.result
		result.Content = slices.Clone(s.result.Content)
		v.result = &result
	}
	return v
}

func (s *mcpSession) changed() {
	if s.notify != nil {
		s.notify()
	}
}

func (s *mcpSession) append(kind, name, text string) {
	s.mu.Lock()
	s.appendLocked(kind, name, text)
	s.mu.Unlock()
	s.changed()
}

func (s *mcpSession) appendLocked(kind, name, text string) {
	s.seq++
	size := len(text)
	if len(s.events) == mcpTimelineLimit {
		s.events = slices.Delete(s.events, 0, 1)
		s.evicted++
	}
	s.events = append(s.events, mcpEvent{seq: s.seq, at: time.Now(), kind: kind, name: ssePreview(name), text: request.MCPPreview(text, mcpEventPreview), size: size})
}

// rpc records a real JSON-RPC request or response. A server-initiated
// request is "In"; the client's response to it is "Out".
func (s *mcpSession) rpc(direction, method, text string) {
	s.mu.Lock()
	s.rpcSeq++
	s.appendLocked("RPC "+direction, method, text)
	s.mu.Unlock()
	s.changed()
}

// progressUpdate keeps one live row per progress token instead of letting a
// chatty server evict the rest of the timeline.
func (s *mcpSession) progressUpdate(token string, progress, total float64, message string) {
	text := fmt.Sprintf("%g", progress)
	if total > 0 {
		text += fmt.Sprintf(" / %g (%.0f%%)", total, progress/total*100)
	}
	if message != "" {
		text += " · " + message
	}
	s.mu.Lock()
	s.progress = text
	name := ssePreview(token)
	if n := len(s.events); n > 0 && s.events[n-1].kind == "Progress" && s.events[n-1].name == name {
		s.events[n-1].text = request.MCPPreview(text, mcpEventPreview)
		s.events[n-1].size = len(text)
	} else {
		s.appendLocked("Progress", token, text)
	}
	s.mu.Unlock()
	s.changed()
}

func (s *mcpSession) rpcCount() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.rpcSeq
}

// openMCPSession connects and initializes in the background, then loads the
// advertised catalog and waits for the connection to end.
func openMCPSession(plan request.MCPPlan, input request.Request, scope request.Context, vars map[string]string, notify func()) *mcpSession {
	ctx, cancel := context.WithCancel(context.Background())
	s := &mcpSession{
		plan: plan, input: input, scope: scope, vars: maps.Clone(vars),
		ctx: ctx, cancel: cancel, done: make(chan struct{}), notify: notify, state: "Connecting",
		connection: mcpConnectionInfo{
			Transport: plan.Transport, Endpoint: mcpEndpointText(plan.URL), Command: plan.Command,
			Timeout: mcpTimeoutText(plan.Timeout), Insecure: plan.Insecure, Proxied: plan.Proxy != "",
			EnvCount: len(plan.Env), ArgsCount: len(plan.Args),
		},
	}
	go func() {
		defer cancel()
		defer close(s.done)
		var timer *time.Timer
		if plan.Timeout > 0 {
			timer = time.AfterFunc(plan.Timeout, cancel)
		}
		handlers := request.MCPHandlers{
			Notification: func(method, text string) {
				s.append("Notification", method, text)
				if strings.Contains(method, "list_changed") {
					s.mu.Lock()
					s.catalogNotice = "Catalog changed; refresh it to see the new entries."
					s.mu.Unlock()
				}
			},
			Log: func(level, logger, text string) {
				name := level
				if logger != "" {
					name = level + " · " + logger
				}
				s.append("Log", name, text)
			},
			Stderr: func(line string) { s.append("stderr", "stdio", line) },
			Progress: func(token string, progress, total float64, message string) {
				s.progressUpdate(token, progress, total, message)
			},
			RPC: func(direction, method, text string) { s.rpc(direction, method, text) },
			BodyLimit: func(limit int64) {
				s.append("System", "Response limit", fmt.Sprintf("The MCP response body exceeded %d bytes; the SDK closes this session. Reduce the response size or use a smaller request.", limit))
				s.mu.Lock()
				cancel := s.cancel
				s.mu.Unlock()
				if cancel != nil {
					cancel()
				}
			},
			HeadersDropped: func(target string) {
				s.append("System", "Headers withheld", "Document headers were not sent to "+target+" because it is not the document's origin.")
			},
			Reconnect: func() {
				s.append("System", "Stream reconnect", "The server closed the standalone GET stream; reconnecting without replaying a POST.")
			},
		}
		client, err := request.DialMCP(ctx, plan, handlers)
		if timer != nil {
			timer.Stop()
		}
		if err == nil && ctx.Err() != nil {
			_ = client.Close()
			err = ctx.Err()
		}
		if err != nil {
			s.mu.Lock()
			s.finished = true
			s.state = "Failed"
			if ctx.Err() != nil {
				s.state = "Disconnected"
			}
			s.problem = err.Error()
			s.appendLocked("System", s.state, err.Error())
			s.mu.Unlock()
			s.changed()
			return
		}
		// The SDK transports own their connection contexts: closing the
		// session context alone does not close them. Tie Close to cancellation
		// so Disconnect, tab close and quit end Wait and the process/HTTP session.
		context.AfterFunc(ctx, func() { _ = client.Close() })
		s.mu.Lock()
		s.client, s.connected, s.state = client, true, "Connected"
		s.server = client.Server()
		s.appendLocked("System", "Connected", mcpConnectedText(s.server, plan))
		s.mu.Unlock()
		s.changed()

		s.refreshCatalog()

		err = client.Wait()
		s.mu.Lock()
		s.finished, s.connected, s.busy = true, false, false
		s.opCancel = nil
		switch {
		case ctx.Err() != nil:
			s.state = "Disconnected"
		case err != nil:
			s.state, s.problem = "Server disconnected", err.Error()
		default:
			s.state = "Disconnected"
		}
		s.appendLocked("System", s.state, s.problem)
		s.mu.Unlock()
		s.changed()
	}()
	return s
}

// mcpEndpointText hides userinfo, query and fragment from a displayed
// endpoint: API keys often travel in the query string.
func mcpEndpointText(raw string) string {
	u, err := url.Parse(raw)
	if err != nil || u.Host == "" {
		return ""
	}
	return u.Scheme + "://" + u.Host + u.EscapedPath()
}

func mcpTimeoutText(timeout time.Duration) string {
	if timeout <= 0 {
		return "none"
	}
	return timeout.String()
}

func mcpConnectedText(info request.MCPServerInfo, plan request.MCPPlan) string {
	var b strings.Builder
	name := info.Name
	if info.Title != "" {
		name = info.Title + " (" + info.Name + ")"
	}
	fmt.Fprintf(&b, "%s %s\nMCP %s · %s", name, info.Version, cmp.Or(info.ProtocolVersion, "unknown"), plan.Transport)
	if info.Instructions != "" {
		b.WriteString("\n\n")
		b.WriteString(info.Instructions)
	}
	return b.String()
}

func (s *mcpSession) setTools(tools []request.MCPTool) {
	s.mu.Lock()
	s.tools = tools
	s.mu.Unlock()
}

func (s *mcpSession) setResources(resources []request.MCPResource, templates []request.MCPResourceTemplate) {
	s.mu.Lock()
	s.resources, s.templates = resources, templates
	s.mu.Unlock()
}

func (s *mcpSession) setPrompts(prompts []request.MCPPrompt) {
	s.mu.Lock()
	s.prompts = prompts
	s.mu.Unlock()
}

// refreshCatalog lists every advertised category. A category that fails does
// not stop the others; the combined error names each one.
func (s *mcpSession) refreshCatalog() bool {
	s.mu.Lock()
	server := s.server
	s.mu.Unlock()
	s.mu.Lock()
	s.catalogNotice = ""
	s.mu.Unlock()
	return s.start("catalog", func(ctx context.Context, client *request.MCPClient) (string, *request.MCPResult, error) {
		client.BeginCatalog()
		var parts, problems []string
		list := func(label string, op func() error) {
			before := s.rpcCount()
			err := op()
			if err == nil && s.rpcCount() == before {
				s.append("Cache", label, "Served from the client's TTL cache; no JSON-RPC request was sent.")
			}
			if err != nil {
				problems = append(problems, label+": "+err.Error())
			}
		}
		if server.Tools {
			var tools []request.MCPTool
			ok := true
			list("tools/list", func() error {
				var err error
				tools, err = client.ListTools(ctx)
				ok = err == nil
				return err
			})
			if ok {
				s.setTools(tools)
				parts = append(parts, fmt.Sprintf("%d %s", len(tools), plural(len(tools), "tool", "tools")))
			}
		}
		if server.Resources {
			var resources []request.MCPResource
			var templates []request.MCPResourceTemplate
			resourcesOK, templatesOK := true, true
			list("resources/list", func() error {
				var err error
				resources, err = client.ListResources(ctx)
				resourcesOK = err == nil
				return err
			})
			list("resources/templates/list", func() error {
				var err error
				templates, err = client.ListResourceTemplates(ctx)
				templatesOK = err == nil
				return err
			})
			if resourcesOK || templatesOK {
				s.setResources(resources, templates)
				parts = append(parts, fmt.Sprintf("%d %s · %d %s", len(resources), plural(len(resources), "resource", "resources"),
					len(templates), plural(len(templates), "template", "templates")))
			}
		}
		if server.Prompts {
			var prompts []request.MCPPrompt
			ok := true
			list("prompts/list", func() error {
				var err error
				prompts, err = client.ListPrompts(ctx)
				ok = err == nil
				return err
			})
			if ok {
				s.setPrompts(prompts)
				parts = append(parts, fmt.Sprintf("%d %s", len(prompts), plural(len(prompts), "prompt", "prompts")))
			}
		}
		if client.CatalogTruncated() {
			s.mu.Lock()
			s.catalogNotice = fmt.Sprintf("Catalog truncated at %d entries or %d MiB; refresh after narrowing it on the server.",
				request.MCPMaxCatalogItems, request.MCPMaxCatalogBytes>>20)
			s.mu.Unlock()
		}
		if len(problems) > 0 {
			return "", nil, errors.New(strings.Join(problems, "; "))
		}
		if len(parts) == 0 {
			return "The server advertises no tools, resources or prompts.", nil, nil
		}
		return strings.Join(parts, " · "), nil, nil
	})
}

// start runs one operation at a time off the UI thread. The RPC middleware
// records the actual request and the timeline records this bounded summary;
// environment values never enter either.
func (s *mcpSession) start(label string, op func(context.Context, *request.MCPClient) (string, *request.MCPResult, error)) bool {
	s.mu.Lock()
	if s.finished || s.client == nil || s.busy {
		s.mu.Unlock()
		return false
	}
	s.busy, s.problem, s.progress = true, "", ""
	client := s.client
	ctx := s.ctx
	var cancel context.CancelFunc
	if s.plan.Timeout > 0 {
		ctx, cancel = context.WithTimeout(ctx, s.plan.Timeout)
	} else {
		ctx, cancel = context.WithCancel(ctx)
	}
	s.opCancel = cancel
	s.mu.Unlock()
	go func() {
		defer cancel()
		summary, result, err := op(ctx, client)
		s.mu.Lock()
		s.busy, s.opCancel = false, nil
		if !s.finished {
			switch {
			case err != nil && errors.Is(ctx.Err(), context.DeadlineExceeded):
				s.problem = "Timed out: " + err.Error()
				s.appendLocked("Error", label, s.problem)
			case err != nil && ctx.Err() != nil:
				s.appendLocked("System", "Cancelled", err.Error())
			case err != nil:
				s.problem = err.Error()
				s.appendLocked("Error", label, err.Error())
			default:
				if result != nil {
					s.result, s.resultSeq = result, s.resultSeq+1
				}
				s.appendLocked("In", label, summary)
			}
		}
		s.mu.Unlock()
		s.changed()
	}()
	return true
}

// callTool prepares the current tool arguments with the connection's original
// endpoint, auth and variable scope.
func (s *mcpSession) callTool(cfg request.MCPConfig) (string, error) {
	plan, err := s.prepare(cfg)
	if err != nil {
		return "", err
	}
	if plan.Tool == "" {
		return "", fmt.Errorf("choose a tool first")
	}
	label := "tools/call " + plan.Tool
	if s.start(label, func(ctx context.Context, client *request.MCPClient) (string, *request.MCPResult, error) {
		result, err := client.CallTool(ctx, plan.Tool, plan.ToolArgs)
		if err != nil {
			return "", nil, err
		}
		return result.Text, &result, nil
	}) {
		return label, nil
	}
	return "", nil
}

func (s *mcpSession) readResource(cfg request.MCPConfig) (string, error) {
	plan, err := s.prepare(cfg)
	if err != nil {
		return "", err
	}
	if plan.ResourceURI == "" {
		return "", fmt.Errorf("enter a resource URI first")
	}
	label := "resources/read " + plan.ResourceURI
	if s.start(label, func(ctx context.Context, client *request.MCPClient) (string, *request.MCPResult, error) {
		before := s.rpcCount()
		result, err := client.ReadResource(ctx, plan.ResourceURI)
		if err != nil {
			return "", nil, err
		}
		if s.rpcCount() == before {
			s.append("Cache", label, "Served from the client's TTL cache; no JSON-RPC request was sent.")
		}
		return result.Text, &result, nil
	}) {
		return label, nil
	}
	return "", nil
}

func (s *mcpSession) getPrompt(cfg request.MCPConfig) (string, error) {
	plan, err := s.prepare(cfg)
	if err != nil {
		return "", err
	}
	if plan.Prompt == "" {
		return "", fmt.Errorf("choose a prompt first")
	}
	label := "prompts/get " + plan.Prompt
	if s.start(label, func(ctx context.Context, client *request.MCPClient) (string, *request.MCPResult, error) {
		result, err := client.GetPrompt(ctx, plan.Prompt, plan.PromptArgs)
		if err != nil {
			return "", nil, err
		}
		return result.Text, &result, nil
	}) {
		return label, nil
	}
	return "", nil
}

// prepare reuses the connection's original endpoint, auth, headers and
// variables; only the operation fields come from the current document.
func (s *mcpSession) prepare(cfg request.MCPConfig) (request.MCPPlan, error) {
	r := s.input
	r.Protocol, r.MCP = "MCP", cfg
	return r.PrepareMCP(s.scope, s.vars)
}

func (s *mcpSession) cancelOp() {
	s.mu.Lock()
	cancel := s.opCancel
	s.mu.Unlock()
	if cancel != nil {
		cancel()
	}
}

func (s *mcpSession) disconnect() {
	s.mu.Lock()
	cancel, client := s.cancel, s.client
	s.mu.Unlock()
	if cancel != nil {
		cancel()
	}
	if client != nil {
		go client.Close()
	}
}

// closeNow closes the session and waits for its transport, including a stdio
// process tree. Quit uses it so os.Exit cannot race the close goroutine.
func (s *mcpSession) closeNow(timeout time.Duration) {
	s.mu.Lock()
	cancel, client, done := s.cancel, s.client, s.done
	s.mu.Unlock()
	if cancel != nil {
		cancel()
	}
	if client != nil {
		go func() { _ = client.Close() }()
	}
	if done == nil {
		return
	}
	select {
	case <-done:
	case <-time.After(timeout):
	}
}
