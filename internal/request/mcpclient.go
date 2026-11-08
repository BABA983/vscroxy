package request

import (
	"cmp"
	"context"
	"encoding/base64"
	"encoding/json/jsontext"
	"encoding/json/v2"
	"fmt"
	"io"
	"log/slog"
	"maps"
	"mime"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"slices"
	"strings"
	"sync"
	"sync/atomic"
	"unicode/utf8"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// Bounds on untrusted server output. The transport limits one frame; the
// catalog, a result and the UI timeline are bounded separately.
const (
	MCPMaxFrame         = 8 << 20
	MCPMaxCatalogItems  = 1000
	MCPMaxCatalogBytes  = 4 << 20
	MCPMaxCatalogString = 1 << 10
	MCPMaxSchema        = 16 << 10
	MCPMaxContentItems  = 100
	MCPMaxMediaBytes    = 1 << 20
	MCPMaxResultBytes   = 4 << 20
	MCPMaxResultText    = 1 << 20
	MCPMaxInstructions  = 64 << 10
)

// MCPServerInfo is the initialize result the session shows.
type MCPServerInfo struct {
	Name, Title, Version               string
	ProtocolVersion                    string
	Instructions                       string
	Capabilities                       string
	Tools, Resources, Prompts, Logging bool
}

type MCPTool struct {
	Name, Title, Description  string
	InputSchema, OutputSchema string
	Annotations               string
}

type MCPResource struct {
	URI, Name, Title, Description, MIMEType string
	Size                                    int64
}

type MCPResourceTemplate struct {
	URITemplate, Name, Title, Description, MIMEType string
}

type MCPPromptArgument struct {
	Name, Title, Description string
	Required                 bool
}

type MCPPrompt struct {
	Name, Title, Description string
	Arguments                []MCPPromptArgument
}

// MCPContent is one result block. Data is raw media bytes, bounded by
// MCPMaxMediaBytes; Size is the original size before any truncation.
type MCPContent struct {
	Kind, Text, MIMEType, URI, Name, Title, Description, Role string
	Data                                                      []byte
	Size                                                      int64
	Truncated                                                 bool
}

// MCPResult is one tool call, resource read or prompt get.
type MCPResult struct {
	Kind        string
	Title       string
	IsError     bool
	Content     []MCPContent
	Structured  string
	Description string
	Text        string
	Bytes       int
	Truncated   bool
}

// MCPHandlers receive server-initiated activity off the UI thread. Text is
// already bounded; implementations must not log it.
type MCPHandlers struct {
	Notification func(method, text string)
	Log          func(level, logger, text string)
	Stderr       func(line string)
	// Reconnect reports a new standalone SSE GET after the first one. POSTs
	// are never replayed by the transport.
	Reconnect func()
	// Progress receives notifications/progress for this client's requests.
	Progress func(token string, progress, total float64, message string)
	// RPC reports JSON-RPC traffic that the document's operation labels do not
	// already show: initialize, logging setup, subscriptions, pings, and
	// server-initiated requests such as sampling, elicitation and roots.
	RPC func(direction, method, text string)
}

// MCPClient owns one initialized MCP session.
type MCPClient struct {
	session         *mcp.ClientSession
	info            MCPServerInfo
	protocolVersion string
	nextProgress    atomic.Uint64
	catalogBudget   atomic.Int64
	catalogCut      atomic.Bool
}

func (c *MCPClient) Server() MCPServerInfo { return c.info }

func (c *MCPClient) Close() error { return c.session.Close() }

func (c *MCPClient) Wait() error { return c.session.Wait() }

// DialMCP connects and initializes one session. The caller owns ctx and must
// Close the client when done.
func DialMCP(ctx context.Context, plan MCPPlan, handlers MCPHandlers) (*MCPClient, error) {
	var transport mcp.Transport
	var stdio *mcpStdioTransport
	switch plan.Transport {
	case MCPTransportStdio:
		cmd := exec.Command(plan.Command, plan.Args...)
		cmd.Dir = plan.WorkingDir
		cmd.Env = mcpEnvironment(plan.Env)
		if handlers.Stderr != nil {
			cmd.Stderr = &mcpLineWriter{fn: handlers.Stderr}
		}
		stdio = &mcpStdioTransport{cmd: cmd}
		transport = stdio
	case MCPTransportSSE:
		transport = &mcp.SSEClientTransport{Endpoint: plan.URL, HTTPClient: mcpHTTPClient(plan, handlers), MaxEventSize: MCPMaxFrame}
	case MCPTransportStreamableHTTP:
		// Retry the standalone GET stream, never a POST. The transport reports
		// each reconnection through the HTTP wrapper's callback.
		transport = &mcp.StreamableClientTransport{Endpoint: plan.URL, HTTPClient: mcpHTTPClient(plan, handlers), MaxRetries: 2, MaxEventSize: MCPMaxFrame}
	default:
		return nil, fmt.Errorf("unsupported MCP transport %q", plan.Transport)
	}
	client := mcp.NewClient(&mcp.Implementation{Name: "vscroxy", Version: "0.1.0"}, &mcp.ClientOptions{
		Logger: slog.New(slog.NewTextHandler(io.Discard, nil)),
		// A debugger advertises the server-initiated methods so their traffic
		// reaches the timeline. Sampling is declined: this app has no model to
		// call, but the request and its error response remain visible.
		Capabilities: &mcp.ClientCapabilities{RootsV2: &mcp.RootCapabilities{}},
		CreateMessageHandler: func(context.Context, *mcp.CreateMessageRequest) (*mcp.CreateMessageResult, error) {
			return nil, fmt.Errorf("vscroxy does not provide sampling")
		},
		ElicitationHandler: func(context.Context, *mcp.ElicitRequest) (*mcp.ElicitResult, error) {
			return &mcp.ElicitResult{Action: "decline"}, nil
		},
		ToolListChangedHandler: func(context.Context, *mcp.ToolListChangedRequest) {
			mcpNotify(handlers, "notifications/tools/list_changed", "")
		},
		PromptListChangedHandler: func(context.Context, *mcp.PromptListChangedRequest) {
			mcpNotify(handlers, "notifications/prompts/list_changed", "")
		},
		ResourceListChangedHandler: func(context.Context, *mcp.ResourceListChangedRequest) {
			mcpNotify(handlers, "notifications/resources/list_changed", "")
		},
		ResourceUpdatedHandler: func(_ context.Context, req *mcp.ResourceUpdatedNotificationRequest) {
			text := ""
			if req.Params != nil {
				text = req.Params.URI
			}
			mcpNotify(handlers, "notifications/resources/updated", text)
		},
		ProgressNotificationHandler: func(_ context.Context, req *mcp.ProgressNotificationClientRequest) {
			if req.Params == nil {
				return
			}
			p := req.Params
			if handlers.Progress != nil {
				handlers.Progress(fmt.Sprint(p.ProgressToken), p.Progress, p.Total, p.Message)
				return
			}
			text := fmt.Sprintf("%g", p.Progress)
			if p.Total > 0 {
				text += fmt.Sprintf(" / %g", p.Total)
			}
			if p.Message != "" {
				text += " · " + p.Message
			}
			mcpNotify(handlers, "notifications/progress", text)
		},
		LoggingMessageHandler: func(_ context.Context, req *mcp.LoggingMessageRequest) {
			if handlers.Log == nil || req.Params == nil {
				return
			}
			handlers.Log(string(req.Params.Level), req.Params.Logger, mcpValueText(req.Params.Data))
		},
	})
	client.AddSendingMiddleware(mcpRPCFragment(handlers, "Out"))
	client.AddReceivingMiddleware(mcpRPCFragment(handlers, "In"))
	session, err := client.Connect(ctx, transport, &mcp.ClientSessionOptions{ProtocolVersion: plan.ProtocolVersion})
	if err != nil {
		if stdio != nil {
			_ = stdio.Close()
		}
		return nil, err
	}
	negotiated := ""
	if result := session.InitializeResult(); result != nil {
		negotiated = result.ProtocolVersion
	}
	if plan.ProtocolVersion != "" && negotiated != plan.ProtocolVersion {
		_ = session.Close()
		return nil, fmt.Errorf("server negotiated MCP %s, but the document requires %s", cmp.Or(negotiated, "an unknown version"), plan.ProtocolVersion)
	}
	if result := session.InitializeResult(); result != nil {
		if result.Capabilities != nil && result.Capabilities.Logging != nil && !mcpAtLeast2026(negotiated) {
			// A legacy server sends notifications/message only after the client
			// chooses a level; a debugger wants all of them. 2026-07-28 carries
			// the level in each request's _meta instead.
			_ = session.SetLoggingLevel(ctx, &mcp.SetLoggingLevelParams{Level: mcp.LoggingLevel("debug")})
		}
	}
	info := MCPServerInfo{}
	if result := session.InitializeResult(); result != nil {
		info.ProtocolVersion = result.ProtocolVersion
		info.Instructions = mcpTruncate(result.Instructions, MCPMaxInstructions)
		if result.ServerInfo != nil {
			info.Name, info.Title, info.Version = result.ServerInfo.Name, result.ServerInfo.Title, result.ServerInfo.Version
		}
		info.Capabilities = mcpTruncate(mcpJSONText(result.Capabilities), MCPMaxSchema)
		if result.Capabilities != nil {
			info.Tools = result.Capabilities.Tools != nil
			info.Resources = result.Capabilities.Resources != nil
			info.Prompts = result.Capabilities.Prompts != nil
			info.Logging = result.Capabilities.Logging != nil
		}
	}
	if info.Capabilities == "" {
		info.Capabilities = "{}"
	}
	c := &MCPClient{session: session, info: info, protocolVersion: negotiated}
	c.catalogBudget.Store(MCPMaxCatalogBytes)
	return c, nil
}

// mcpRPCFragment records JSON-RPC requests and server-initiated requests.
// Notifications keep their typed handlers, and responses to the document's
// own operations are shown by the session's result entries.
func mcpRPCFragment(handlers MCPHandlers, direction string) mcp.Middleware {
	return func(next mcp.MethodHandler) mcp.MethodHandler {
		return func(ctx context.Context, method string, req mcp.Request) (mcp.Result, error) {
			record := handlers.RPC != nil && !strings.HasPrefix(method, "notifications/")
			if record {
				handlers.RPC(direction, method, mcpParamsText(req.GetParams()))
			}
			result, err := next(ctx, method, req)
			if record && direction == "In" {
				if err != nil {
					handlers.RPC("Out", method+" error", err.Error())
				} else {
					handlers.RPC("Out", method+" response", mcpResultText(result))
				}
			}
			return result, err
		}
	}
}

func mcpParamsText(params mcp.Params) string {
	if params == nil {
		return ""
	}
	return mcpTruncate(mcpJSONText(params), 64<<10)
}

func mcpResultText(result mcp.Result) string {
	if result == nil {
		return ""
	}
	return mcpTruncate(mcpJSONText(result), 64<<10)
}

func mcpAtLeast2026(version string) bool {
	return version >= "2026-07-28"
}

// paramsMeta is the per-request metadata a 2026 server needs for logging and
// a progress stream.
func (c *MCPClient) paramsMeta(progress bool) mcp.Meta {
	meta := mcp.Meta{}
	if mcpAtLeast2026(c.protocolVersion) {
		meta[mcp.MetaKeyLogLevel] = "debug"
	}
	if progress {
		meta["progressToken"] = fmt.Sprintf("vscroxy-%d", c.nextProgress.Add(1))
	}
	return meta
}

func mcpNotify(handlers MCPHandlers, method, text string) {
	if handlers.Notification != nil {
		handlers.Notification(method, mcpTruncate(text, MCPMaxResultText))
	}
}

// mcpHTTPClient applies the document's headers and TLS policy to every HTTP
// request of the transport. The capture proxy is used when it is running.
func mcpHTTPClient(plan MCPPlan, handlers MCPHandlers) *http.Client {
	base := http.DefaultTransport.(*http.Transport).Clone()
	base.Proxy = nil
	base.TLSClientConfig = nil
	base.ForceAttemptHTTP2 = true
	if plan.Proxy != "" {
		if u, err := url.Parse("http://" + plan.Proxy); err == nil {
			base.Proxy = http.ProxyURL(u)
		}
	}
	origin, _ := url.Parse(plan.URL)
	transport := plan.Certificates.Transport(base, plan.ProxyCA, plan.Insecure)
	client := &http.Client{
		Transport: &mcpHeaderTransport{base: transport, header: plan.Header, origin: origin, onReconnect: handlers.Reconnect},
		Jar:       plan.Jar,
	}
	if origin != nil && origin.Host != "" {
		client.CheckRedirect = func(req *http.Request, via []*http.Request) error {
			if !mcpSameOrigin(req.URL, origin) {
				return fmt.Errorf("MCP does not follow cross-origin redirects")
			}
			return nil
		}
	}
	return client
}

type mcpHeaderTransport struct {
	base        http.RoundTripper
	header      http.Header
	origin      *url.URL
	onReconnect func()
	sseGets     atomic.Int32
}

func (t *mcpHeaderTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	sameOrigin := mcpSameOrigin(req.URL, t.origin)
	if req.Method == http.MethodGet && sameOrigin && strings.Contains(req.Header.Get("Accept"), "text/event-stream") {
		if t.sseGets.Add(1) > 1 && t.onReconnect != nil {
			t.onReconnect()
		}
	}
	var resp *http.Response
	var err error
	if len(t.header) == 0 || !sameOrigin {
		resp, err = t.base.RoundTrip(req)
	} else {
		clone := req.Clone(req.Context())
		for name, values := range t.header {
			if mcpTransportHeader(name) {
				continue
			}
			clone.Header.Del(name)
			for _, value := range values {
				clone.Header.Add(name, value)
			}
		}
		resp, err = t.base.RoundTrip(clone)
	}
	if err != nil {
		return nil, err
	}
	return mcpBoundResponse(resp)
}

// mcpBoundResponse bounds a JSON (or error) body. SSE streams are bounded per
// event by the transport; a plain JSON response is read whole by the SDK, so
// it needs its own limit.
func mcpBoundResponse(resp *http.Response) (*http.Response, error) {
	if resp == nil || resp.Body == nil {
		return resp, nil
	}
	mediaType, _, _ := mime.ParseMediaType(resp.Header.Get("Content-Type"))
	if mediaType == "text/event-stream" {
		return resp, nil
	}
	if resp.ContentLength > MCPMaxFrame {
		resp.Body.Close()
		return nil, fmt.Errorf("MCP response body exceeds %d bytes", MCPMaxFrame)
	}
	resp.Body = &mcpLimitedBody{ReadCloser: resp.Body, limit: MCPMaxFrame}
	return resp, nil
}

type mcpLimitedBody struct {
	io.ReadCloser
	read  int64
	limit int64
}

func (b *mcpLimitedBody) Read(p []byte) (int, error) {
	remaining := b.limit - b.read
	if remaining < 0 {
		return 0, fmt.Errorf("MCP response body exceeds %d bytes", b.limit)
	}
	if int64(len(p)) > remaining+1 {
		p = p[:remaining+1]
	}
	n, err := b.ReadCloser.Read(p)
	b.read += int64(n)
	if b.read > b.limit {
		return n, fmt.Errorf("MCP response body exceeds %d bytes", b.limit)
	}
	return n, err
}

// mcpSameOrigin reports whether u has the same scheme and host as origin.
// Document headers and auth are never sent to another origin, including the
// endpoint of a legacy SSE stream.
func mcpSameOrigin(u, origin *url.URL) bool {
	if u == nil || origin == nil || origin.Host == "" {
		return false
	}
	return strings.EqualFold(u.Scheme, origin.Scheme) && strings.EqualFold(u.Host, origin.Host)
}

// mcpTransportHeader names the headers the MCP transport owns. A user's
// value for one of these would break framing or session negotiation.
func mcpTransportHeader(name string) bool {
	lower := strings.ToLower(name)
	if strings.HasPrefix(lower, "mcp-") {
		return true
	}
	switch lower {
	case "accept", "content-type", "content-length", "host", "connection", "transfer-encoding", "last-event-id":
		return true
	}
	return false
}

// mcpEnvironment merges the document's variables over this process's
// environment, so PATH and other ordinary settings keep working.
func mcpEnvironment(extra []string) []string {
	if len(extra) == 0 {
		return nil
	}
	values := map[string]string{}
	for _, entry := range os.Environ() {
		if name, value, ok := strings.Cut(entry, "="); ok {
			values[name] = value
		}
	}
	for _, entry := range extra {
		if name, value, ok := strings.Cut(entry, "="); ok {
			values[name] = value
		}
	}
	names := slices.Sorted(maps.Keys(values))
	out := make([]string, 0, len(names))
	for _, name := range names {
		out = append(out, name+"="+values[name])
	}
	return out
}

// mcpLineWriter turns a stdio server's stderr into bounded timeline lines.
type mcpLineWriter struct {
	mu  sync.Mutex
	buf []byte
	fn  func(string)
}

func (w *mcpLineWriter) Write(p []byte) (int, error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.buf = append(w.buf, p...)
	for {
		at := strings.IndexByte(string(w.buf), '\n')
		if at < 0 {
			if len(w.buf) > 64<<10 {
				w.fn(mcpTruncate(string(w.buf), 64<<10))
				w.buf = nil
			}
			return len(p), nil
		}
		line := strings.TrimRight(string(w.buf[:at]), "\r")
		w.buf = w.buf[at+1:]
		if line != "" {
			w.fn(mcpTruncate(line, 64<<10))
		}
	}
}

// ListTools reads every page up to the catalog bound.
func (c *MCPClient) ListTools(ctx context.Context) ([]MCPTool, error) {
	var out []MCPTool
	cursor := ""
	for page := 0; page < 100; page++ {
		result, err := c.session.ListTools(ctx, &mcp.ListToolsParams{Meta: c.paramsMeta(false), Cursor: cursor})
		if err != nil {
			return nil, err
		}
		for _, tool := range result.Tools {
			if tool == nil {
				continue
			}
			if len(out) >= MCPMaxCatalogItems {
				c.catalogCut.Store(true)
				return out, nil
			}
			item := MCPTool{
				Name: c.catalogString(tool.Name), Title: c.catalogString(tool.Title),
				Description:  c.catalogString(tool.Description),
				InputSchema:  c.catalogSchema(tool.InputSchema, MCPMaxSchema),
				OutputSchema: c.catalogSchema(tool.OutputSchema, MCPMaxSchema),
				Annotations:  c.catalogSchema(tool.Annotations, MCPMaxCatalogString),
			}
			if !c.catalogConsume(len(item.Name) + len(item.Title) + len(item.Description) + len(item.InputSchema) + len(item.OutputSchema) + len(item.Annotations)) {
				c.catalogCut.Store(true)
				return out, nil
			}
			out = append(out, item)
		}
		if result.NextCursor == "" {
			cursor = ""
			break
		}
		if result.NextCursor == cursor {
			c.catalogCut.Store(true)
			cursor = ""
			break
		}
		cursor = result.NextCursor
	}
	if cursor != "" {
		c.catalogCut.Store(true)
	}
	return out, nil
}

// ListResources reads every page up to the catalog bound.
func (c *MCPClient) ListResources(ctx context.Context) ([]MCPResource, error) {
	var out []MCPResource
	cursor := ""
	for page := 0; page < 100; page++ {
		result, err := c.session.ListResources(ctx, &mcp.ListResourcesParams{Meta: c.paramsMeta(false), Cursor: cursor})
		if err != nil {
			return nil, err
		}
		for _, resource := range result.Resources {
			if resource == nil {
				continue
			}
			if len(out) >= MCPMaxCatalogItems {
				c.catalogCut.Store(true)
				return out, nil
			}
			item := MCPResource{
				URI: c.catalogString(resource.URI), Name: c.catalogString(resource.Name),
				Title: c.catalogString(resource.Title), Description: c.catalogString(resource.Description),
				MIMEType: c.catalogString(resource.MIMEType), Size: resource.Size,
			}
			if !c.catalogConsume(len(item.URI) + len(item.Name) + len(item.Title) + len(item.Description) + len(item.MIMEType)) {
				c.catalogCut.Store(true)
				return out, nil
			}
			out = append(out, item)
		}
		if result.NextCursor == "" {
			cursor = ""
			break
		}
		if result.NextCursor == cursor {
			c.catalogCut.Store(true)
			cursor = ""
			break
		}
		cursor = result.NextCursor
	}
	if cursor != "" {
		c.catalogCut.Store(true)
	}
	return out, nil
}

// ListResourceTemplates reads every page up to the catalog bound.
func (c *MCPClient) ListResourceTemplates(ctx context.Context) ([]MCPResourceTemplate, error) {
	var out []MCPResourceTemplate
	cursor := ""
	for page := 0; page < 100; page++ {
		result, err := c.session.ListResourceTemplates(ctx, &mcp.ListResourceTemplatesParams{Meta: c.paramsMeta(false), Cursor: cursor})
		if err != nil {
			return nil, err
		}
		for _, template := range result.ResourceTemplates {
			if template == nil {
				continue
			}
			if len(out) >= MCPMaxCatalogItems {
				c.catalogCut.Store(true)
				return out, nil
			}
			item := MCPResourceTemplate{
				URITemplate: c.catalogString(template.URITemplate), Name: c.catalogString(template.Name),
				Title: c.catalogString(template.Title), Description: c.catalogString(template.Description),
				MIMEType: c.catalogString(template.MIMEType),
			}
			if !c.catalogConsume(len(item.URITemplate) + len(item.Name) + len(item.Title) + len(item.Description) + len(item.MIMEType)) {
				c.catalogCut.Store(true)
				return out, nil
			}
			out = append(out, item)
		}
		if result.NextCursor == "" {
			cursor = ""
			break
		}
		if result.NextCursor == cursor {
			c.catalogCut.Store(true)
			cursor = ""
			break
		}
		cursor = result.NextCursor
	}
	if cursor != "" {
		c.catalogCut.Store(true)
	}
	return out, nil
}

// ListPrompts reads every page up to the catalog bound.
func (c *MCPClient) ListPrompts(ctx context.Context) ([]MCPPrompt, error) {
	var out []MCPPrompt
	cursor := ""
	for page := 0; page < 100; page++ {
		result, err := c.session.ListPrompts(ctx, &mcp.ListPromptsParams{Meta: c.paramsMeta(false), Cursor: cursor})
		if err != nil {
			return nil, err
		}
		for _, prompt := range result.Prompts {
			if prompt == nil {
				continue
			}
			if len(out) >= MCPMaxCatalogItems {
				c.catalogCut.Store(true)
				return out, nil
			}
			item := MCPPrompt{Name: c.catalogString(prompt.Name), Title: c.catalogString(prompt.Title),
				Description: c.catalogString(prompt.Description)}
			size := len(item.Name) + len(item.Title) + len(item.Description)
			for _, argument := range prompt.Arguments {
				if argument == nil {
					continue
				}
				if len(item.Arguments) >= MCPMaxCatalogItems {
					c.catalogCut.Store(true)
					break
				}
				a := MCPPromptArgument{Name: c.catalogString(argument.Name), Title: c.catalogString(argument.Title),
					Description: c.catalogString(argument.Description), Required: argument.Required}
				size += len(a.Name) + len(a.Title) + len(a.Description)
				item.Arguments = append(item.Arguments, a)
			}
			if !c.catalogConsume(size) {
				c.catalogCut.Store(true)
				return out, nil
			}
			out = append(out, item)
		}
		if result.NextCursor == "" {
			cursor = ""
			break
		}
		if result.NextCursor == cursor {
			c.catalogCut.Store(true)
			cursor = ""
			break
		}
		cursor = result.NextCursor
	}
	if cursor != "" {
		c.catalogCut.Store(true)
	}
	return out, nil
}

// CallTool calls one tool and converts its content into a bounded result.
func (c *MCPClient) CallTool(ctx context.Context, name string, arguments jsontext.Value) (MCPResult, error) {
	var args any
	if len(arguments) > 0 {
		if err := json.Unmarshal(arguments, &args); err != nil {
			return MCPResult{}, fmt.Errorf("tool arguments: %w", err)
		}
	}
	result, err := c.session.CallTool(ctx, &mcp.CallToolParams{Meta: c.paramsMeta(true), Name: name, Arguments: args})
	if err != nil {
		return MCPResult{}, err
	}
	return mcpToolResult(result), nil
}

// ReadResource reads one resource and converts its contents into a bounded
// result.
func (c *MCPClient) ReadResource(ctx context.Context, uri string) (MCPResult, error) {
	result, err := c.session.ReadResource(ctx, &mcp.ReadResourceParams{Meta: c.paramsMeta(true), URI: uri})
	if err != nil {
		return MCPResult{}, err
	}
	out := MCPResult{Kind: "Resource", Title: uri}
	budget := MCPMaxResultBytes
	for _, contents := range result.Contents {
		if contents == nil || len(out.Content) == MCPMaxContentItems {
			out.Truncated = true
			break
		}
		block := MCPContent{Kind: "resource", URI: contents.URI, MIMEType: contents.MIMEType}
		switch {
		case contents.Text != "" || contents.Blob == nil:
			block.Kind, block.Text, block.Size = "text", contents.Text, int64(len(contents.Text))
		default:
			block.Kind, block.Size = "blob", int64(len(contents.Blob))
			block.Data = mcpMedia(contents.Blob, &budget, &block.Truncated)
		}
		if block.Kind == "text" {
			block.Text, block.Truncated = mcpText(block.Text, &budget)
		}
		out.Bytes += int(block.Size)
		out.Content = append(out.Content, block)
	}
	mcpAssemble(&out)
	return out, nil
}

// GetPrompt gets one prompt and converts its messages into a bounded result.
func (c *MCPClient) GetPrompt(ctx context.Context, name string, arguments jsontext.Value) (MCPResult, error) {
	var args map[string]string
	if len(arguments) > 0 {
		if err := json.Unmarshal(arguments, &args); err != nil {
			return MCPResult{}, fmt.Errorf("prompt arguments: %w", err)
		}
	}
	result, err := c.session.GetPrompt(ctx, &mcp.GetPromptParams{Meta: c.paramsMeta(true), Name: name, Arguments: args})
	if err != nil {
		return MCPResult{}, err
	}
	out := MCPResult{Kind: "Prompt", Title: name}
	if result.Description != "" {
		out.Description = mcpTruncate(result.Description, MCPMaxResultText)
		out.Bytes += len(result.Description)
	}
	budget := MCPMaxResultBytes
	for _, message := range result.Messages {
		if message == nil || len(out.Content) == MCPMaxContentItems {
			out.Truncated = true
			break
		}
		if block, ok := mcpContentBlock(message.Content, string(message.Role), &budget); ok {
			out.Content = append(out.Content, block)
			out.Bytes += int(block.Size)
		}
	}
	mcpAssemble(&out)
	return out, nil
}

func mcpToolResult(result *mcp.CallToolResult) MCPResult {
	out := MCPResult{Kind: "Tool", IsError: result.IsError}
	budget := MCPMaxResultBytes
	for _, content := range result.Content {
		if len(out.Content) == MCPMaxContentItems {
			out.Truncated = true
			break
		}
		if block, ok := mcpContentBlock(content, "", &budget); ok {
			out.Content = append(out.Content, block)
			out.Bytes += int(block.Size)
		}
	}
	if result.StructuredContent != nil {
		structured := mcpJSONText(result.StructuredContent)
		out.Bytes += len(structured)
		out.Structured = mcpTruncate(structured, MCPMaxResultText)
		if len(out.Structured) < len(structured) {
			out.Truncated = true
		}
	}
	mcpAssemble(&out)
	return out
}

func mcpContentBlock(content mcp.Content, role string, budget *int) (MCPContent, bool) {
	switch c := content.(type) {
	case *mcp.TextContent:
		if c == nil {
			return MCPContent{}, false
		}
		block := MCPContent{Kind: "text", Role: role, Size: int64(len(c.Text))}
		block.Text, block.Truncated = mcpText(c.Text, budget)
		return block, true
	case *mcp.ImageContent:
		if c == nil {
			return MCPContent{}, false
		}
		block := MCPContent{Kind: "image", Role: role, MIMEType: c.MIMEType, Size: int64(len(c.Data))}
		block.Data = mcpMedia(c.Data, budget, &block.Truncated)
		return block, true
	case *mcp.AudioContent:
		if c == nil {
			return MCPContent{}, false
		}
		block := MCPContent{Kind: "audio", Role: role, MIMEType: c.MIMEType, Size: int64(len(c.Data))}
		block.Data = mcpMedia(c.Data, budget, &block.Truncated)
		return block, true
	case *mcp.EmbeddedResource:
		if c == nil || c.Resource == nil {
			return MCPContent{}, false
		}
		block := MCPContent{Kind: "resource", Role: role, URI: c.Resource.URI, MIMEType: c.Resource.MIMEType}
		if c.Resource.Blob == nil {
			block.Text, block.Size = c.Resource.Text, int64(len(c.Resource.Text))
			block.Text, block.Truncated = mcpText(block.Text, budget)
		} else {
			block.Size = int64(len(c.Resource.Blob))
			block.Data = mcpMedia(c.Resource.Blob, budget, &block.Truncated)
		}
		return block, true
	case *mcp.ResourceLink:
		if c == nil {
			return MCPContent{}, false
		}
		size := int64(0)
		if c.Size != nil {
			size = *c.Size
		}
		return MCPContent{Kind: "resource_link", Role: role, URI: c.URI, Name: c.Name, Title: c.Title,
			Description: mcpTruncate(c.Description, MCPMaxSchema), MIMEType: c.MIMEType, Size: size}, true
	default:
		return MCPContent{Kind: "unknown", Role: role, Text: mcpTruncate(fmt.Sprintf("%T", content), 1024)}, true
	}
}

// mcpText keeps at most what the result budget allows and reports whether it
// cut the text. It never splits a UTF-8 sequence.
func mcpText(text string, budget *int) (string, bool) {
	if *budget <= 0 {
		return "", text != ""
	}
	if len(text) <= *budget {
		*budget -= len(text)
		return text, false
	}
	kept := mcpTruncate(text, *budget)
	*budget = 0
	return kept, true
}

// mcpMedia keeps bounded media bytes; a truncated image is not decodable, so
// its bytes are dropped rather than cut.
func mcpMedia(data []byte, budget *int, truncated *bool) []byte {
	if len(data) == 0 {
		return nil
	}
	if len(data) > MCPMaxMediaBytes || len(data) > *budget {
		*truncated = true
		return nil
	}
	*budget -= len(data)
	return data
}

// mcpAssemble renders the human-readable result shown in the timeline and the
// Result tab, bounded to MCPMaxResultText.
func mcpAssemble(result *MCPResult) {
	var b strings.Builder
	if result.IsError {
		b.WriteString("isError: true\n\n")
	}
	if result.Description != "" {
		b.WriteString("[description]\n")
		b.WriteString(result.Description)
		b.WriteString("\n\n")
	}
	for _, block := range result.Content {
		b.WriteString("[")
		if block.Role != "" {
			b.WriteString(block.Role)
			b.WriteString(" · ")
		}
		b.WriteString(block.Kind)
		if block.MIMEType != "" {
			b.WriteString(" · ")
			b.WriteString(block.MIMEType)
		}
		if block.Size > 0 {
			b.WriteString(fmt.Sprintf(" · %d B", block.Size))
		}
		b.WriteString("]")
		if block.URI != "" {
			b.WriteString(" ")
			b.WriteString(block.URI)
		}
		b.WriteString("\n")
		switch block.Kind {
		case "resource_link":
			if block.Name != "" {
				b.WriteString(block.Name)
				b.WriteString("\n")
			}
			if block.Description != "" {
				b.WriteString(block.Description)
				b.WriteString("\n")
			}
		case "image", "audio", "blob":
			if len(block.Data) == 0 {
				b.WriteString("(data omitted or too large)\n")
			} else if len(block.Data) <= 48<<10 {
				b.WriteString(base64.StdEncoding.EncodeToString(block.Data))
				b.WriteString("\n")
			} else {
				b.WriteString(fmt.Sprintf("(base64 omitted from text; %d B shown in Result)\n", len(block.Data)))
			}
		default:
			b.WriteString(block.Text)
			if block.Truncated {
				b.WriteString("\n(truncated)")
			}
			b.WriteString("\n")
		}
		b.WriteString("\n")
		if b.Len() > MCPMaxResultText {
			break
		}
	}
	if result.Structured != "" {
		b.WriteString("[structuredContent]\n")
		b.WriteString(result.Structured)
		b.WriteString("\n")
	}
	if result.Truncated || b.Len() > MCPMaxResultText {
		result.Truncated = true
	}
	text := b.String()
	if len(text) > MCPMaxResultText {
		text = mcpTruncate(text, MCPMaxResultText) + "\n… result text truncated; " + fmt.Sprint(result.Bytes) + " B total"
	}
	if text == "" {
		text = "(no content)"
	}
	result.Text = text
}

// BeginCatalog resets the per-refresh catalog budget. The app calls it before
// listing so several categories share one bound.
func (c *MCPClient) BeginCatalog() {
	c.catalogBudget.Store(MCPMaxCatalogBytes)
	c.catalogCut.Store(false)
}

// CatalogTruncated reports whether the last catalog read hit the item or byte
// limit.
func (c *MCPClient) CatalogTruncated() bool { return c.catalogCut.Load() }

func (c *MCPClient) catalogConsume(n int) bool {
	for {
		left := c.catalogBudget.Load()
		if left < int64(n) {
			c.catalogBudget.Store(0)
			return false
		}
		if c.catalogBudget.CompareAndSwap(left, left-int64(n)) {
			return true
		}
	}
}

func (c *MCPClient) catalogString(s string) string {
	if len(s) > MCPMaxCatalogString {
		c.catalogCut.Store(true)
	}
	return mcpTruncate(s, MCPMaxCatalogString)
}

// catalogSchema pretty-prints a schema or annotation value and marks the
// catalog cut when its JSON is longer than the display limit.
func (c *MCPClient) catalogSchema(v any, limit int) string {
	if v == nil {
		return ""
	}
	data, err := json.Marshal(v, jsontext.WithIndent("  "))
	if err != nil {
		return ""
	}
	if len(data) > limit {
		c.catalogCut.Store(true)
	}
	return mcpTruncate(string(data), limit)
}

// mcpSchemaText is pretty JSON for a schema or annotation value, bounded to
// the requested share of the catalog.
func mcpSchemaText(v any, limit int) string {
	if v == nil {
		return ""
	}
	data, err := json.Marshal(v, jsontext.WithIndent("  "))
	if err != nil {
		return ""
	}
	return mcpTruncate(string(data), limit)
}

// mcpValueText keeps a string log value readable and renders other values as
// JSON.
func mcpValueText(v any) string {
	if text, ok := v.(string); ok {
		return mcpTruncate(text, MCPMaxResultText)
	}
	return mcpTruncate(mcpJSONText(v), MCPMaxResultText)
}

// mcpTruncate cuts s to at most limit bytes without splitting UTF-8.
func mcpTruncate(s string, limit int) string {
	if limit <= 0 || len(s) <= limit {
		return s
	}
	cut := limit
	for cut > 0 && !utf8.ValidString(s[:cut]) {
		cut--
	}
	return s[:cut]
}

// MCPPreview is the bounded text the UI timeline retains for one entry.
func MCPPreview(text string, limit int) string {
	if limit <= 0 {
		limit = 64 << 10
	}
	if len(text) <= limit {
		return text
	}
	kept := mcpTruncate(text, limit)
	return kept + fmt.Sprintf("\n\nRetained %d of %d bytes", len(kept), len(text))
}
