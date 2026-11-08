package app

import (
	"cmp"
	"context"
	"errors"
	"fmt"
	"maps"
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

// openMCPSession connects and initializes in the background, then loads the
// advertised catalog and waits for the connection to end.
func openMCPSession(plan request.MCPPlan, input request.Request, scope request.Context, vars map[string]string, notify func()) *mcpSession {
	ctx, cancel := context.WithCancel(context.Background())
	s := &mcpSession{
		plan: plan, input: input, scope: scope, vars: maps.Clone(vars),
		ctx: ctx, cancel: cancel, notify: notify, state: "Connecting",
		connection: mcpConnectionInfo{
			Transport: plan.Transport, Endpoint: plan.URL, Command: plan.Command,
			Timeout: mcpTimeoutText(plan.Timeout), Insecure: plan.Insecure, Proxied: plan.Proxy != "",
			EnvCount: len(plan.Env), ArgsCount: len(plan.Args),
		},
	}
	go func() {
		defer cancel()
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
		}
		client, err := request.DialMCP(ctx, plan, handlers)
		if timer != nil {
			timer.Stop()
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
	return s.start("catalog", "tools/list · resources/list · resources/templates/list · prompts/list",
		func(ctx context.Context, client *request.MCPClient) (string, *request.MCPResult, error) {
			var parts, problems []string
			if server.Tools {
				tools, err := client.ListTools(ctx)
				if err != nil {
					problems = append(problems, "tools: "+err.Error())
				} else {
					s.setTools(tools)
					parts = append(parts, fmt.Sprintf("%d %s", len(tools), plural(len(tools), "tool", "tools")))
				}
			}
			if server.Resources {
				resources, err := client.ListResources(ctx)
				templates, templateErr := client.ListResourceTemplates(ctx)
				if err != nil {
					problems = append(problems, "resources: "+err.Error())
				}
				if templateErr != nil {
					problems = append(problems, "resource templates: "+templateErr.Error())
				}
				if err == nil {
					s.setResources(resources, templates)
					parts = append(parts, fmt.Sprintf("%d %s · %d %s", len(resources), plural(len(resources), "resource", "resources"),
						len(templates), plural(len(templates), "template", "templates")))
				}
			}
			if server.Prompts {
				prompts, err := client.ListPrompts(ctx)
				if err != nil {
					problems = append(problems, "prompts: "+err.Error())
				} else {
					s.setPrompts(prompts)
					parts = append(parts, fmt.Sprintf("%d %s", len(prompts), plural(len(prompts), "prompt", "prompts")))
				}
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

// start runs one operation at a time off the UI thread. The timeline records
// the request label and arguments and the bounded result, never a secret value
// from the environment.
func (s *mcpSession) start(label, text string, op func(context.Context, *request.MCPClient) (string, *request.MCPResult, error)) bool {
	s.mu.Lock()
	if s.finished || s.client == nil || s.busy {
		s.mu.Unlock()
		return false
	}
	s.busy, s.problem = true, ""
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
	s.append("Out", label, text)
	s.changed()
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
	text := plan.Tool + "\n" + string(plan.ToolArgs)
	if s.start(label, text, func(ctx context.Context, client *request.MCPClient) (string, *request.MCPResult, error) {
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
	if s.start(label, plan.ResourceURI, func(ctx context.Context, client *request.MCPClient) (string, *request.MCPResult, error) {
		result, err := client.ReadResource(ctx, plan.ResourceURI)
		if err != nil {
			return "", nil, err
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
	text := plan.Prompt + "\n" + string(plan.PromptArgs)
	if s.start(label, text, func(ctx context.Context, client *request.MCPClient) (string, *request.MCPResult, error) {
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
