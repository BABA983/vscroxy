package request

import (
	"context"
	"encoding/base64"
	"encoding/json/jsontext"
	"encoding/json/v2"
	"fmt"
	"io"
	"log/slog"
	"maps"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"slices"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// Bounds on untrusted server output. The transport limits one frame; the
// catalog, a result and the UI timeline are bounded separately.
const (
	MCPMaxFrame        = 8 << 20
	MCPMaxCatalogItems = 1000
	MCPMaxSchema       = 16 << 10
	MCPMaxContentItems = 100
	MCPMaxMediaBytes   = 1 << 20
	MCPMaxResultBytes  = 4 << 20
	MCPMaxResultText   = 1 << 20
	MCPMaxInstructions = 64 << 10
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
}

// MCPClient owns one initialized MCP session.
type MCPClient struct {
	session *mcp.ClientSession
	info    MCPServerInfo
}

func (c *MCPClient) Server() MCPServerInfo { return c.info }

func (c *MCPClient) Close() error { return c.session.Close() }

func (c *MCPClient) Wait() error { return c.session.Wait() }

// DialMCP connects and initializes one session. The caller owns ctx and must
// Close the client when done.
func DialMCP(ctx context.Context, plan MCPPlan, handlers MCPHandlers) (*MCPClient, error) {
	var transport mcp.Transport
	switch plan.Transport {
	case MCPTransportStdio:
		cmd := exec.Command(plan.Command, plan.Args...)
		cmd.Dir = plan.WorkingDir
		cmd.Env = mcpEnvironment(plan.Env)
		if handlers.Stderr != nil {
			cmd.Stderr = &mcpLineWriter{fn: handlers.Stderr}
		}
		transport = &mcp.CommandTransport{Command: cmd, TerminateDuration: 3 * time.Second}
	case MCPTransportSSE:
		transport = &mcp.SSEClientTransport{Endpoint: plan.URL, HTTPClient: mcpHTTPClient(plan), MaxEventSize: MCPMaxFrame}
	case MCPTransportStreamableHTTP:
		// No hidden retries: a debugger shows exactly the requests it made.
		transport = &mcp.StreamableClientTransport{Endpoint: plan.URL, HTTPClient: mcpHTTPClient(plan), MaxRetries: -1, MaxEventSize: MCPMaxFrame}
	default:
		return nil, fmt.Errorf("unsupported MCP transport %q", plan.Transport)
	}
	client := mcp.NewClient(&mcp.Implementation{Name: "vscroxy", Version: "0.1.0"}, &mcp.ClientOptions{
		Logger:       slog.New(slog.NewTextHandler(io.Discard, nil)),
		Capabilities: &mcp.ClientCapabilities{},
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
	session, err := client.Connect(ctx, transport, &mcp.ClientSessionOptions{ProtocolVersion: plan.ProtocolVersion})
	if err != nil {
		return nil, err
	}
	if result := session.InitializeResult(); result != nil && result.Capabilities != nil && result.Capabilities.Logging != nil {
		// A server sends notifications/message only after the client chooses a
		// level; a debugger wants all of them. Failure is not fatal.
		_ = session.SetLoggingLevel(ctx, &mcp.SetLoggingLevelParams{Level: mcp.LoggingLevel("debug")})
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
	return &MCPClient{session: session, info: info}, nil
}

func mcpNotify(handlers MCPHandlers, method, text string) {
	if handlers.Notification != nil {
		handlers.Notification(method, mcpTruncate(text, MCPMaxResultText))
	}
}

// mcpHTTPClient applies the document's headers and TLS policy to every HTTP
// request of the transport. The capture proxy is used when it is running.
func mcpHTTPClient(plan MCPPlan) *http.Client {
	base := http.DefaultTransport.(*http.Transport).Clone()
	base.Proxy = nil
	base.TLSClientConfig = nil
	base.ForceAttemptHTTP2 = true
	if plan.Proxy != "" {
		if u, err := url.Parse("http://" + plan.Proxy); err == nil {
			base.Proxy = http.ProxyURL(u)
		}
	}
	transport := plan.Certificates.Transport(base, plan.ProxyCA, plan.Insecure)
	return &http.Client{Transport: &mcpHeaderTransport{base: transport, header: plan.Header}, Jar: plan.Jar}
}

type mcpHeaderTransport struct {
	base   http.RoundTripper
	header http.Header
}

func (t *mcpHeaderTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	if len(t.header) == 0 {
		return t.base.RoundTrip(req)
	}
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
	return t.base.RoundTrip(clone)
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
	for page := 0; page < 100 && len(out) < MCPMaxCatalogItems; page++ {
		result, err := c.session.ListTools(ctx, &mcp.ListToolsParams{Cursor: cursor})
		if err != nil {
			return nil, err
		}
		for _, tool := range result.Tools {
			if tool == nil {
				continue
			}
			out = append(out, MCPTool{
				Name: tool.Name, Title: tool.Title, Description: mcpTruncate(tool.Description, MCPMaxSchema),
				InputSchema: mcpSchemaText(tool.InputSchema), OutputSchema: mcpSchemaText(tool.OutputSchema),
				Annotations: mcpSchemaText(tool.Annotations),
			})
			if len(out) == MCPMaxCatalogItems {
				break
			}
		}
		if result.NextCursor == "" || result.NextCursor == cursor {
			break
		}
		cursor = result.NextCursor
	}
	return out, nil
}

// ListResources reads every page up to the catalog bound.
func (c *MCPClient) ListResources(ctx context.Context) ([]MCPResource, error) {
	var out []MCPResource
	cursor := ""
	for page := 0; page < 100 && len(out) < MCPMaxCatalogItems; page++ {
		result, err := c.session.ListResources(ctx, &mcp.ListResourcesParams{Cursor: cursor})
		if err != nil {
			return nil, err
		}
		for _, resource := range result.Resources {
			if resource == nil {
				continue
			}
			out = append(out, MCPResource{
				URI: resource.URI, Name: resource.Name, Title: resource.Title,
				Description: mcpTruncate(resource.Description, MCPMaxSchema), MIMEType: resource.MIMEType, Size: resource.Size,
			})
			if len(out) == MCPMaxCatalogItems {
				break
			}
		}
		if result.NextCursor == "" || result.NextCursor == cursor {
			break
		}
		cursor = result.NextCursor
	}
	return out, nil
}

// ListResourceTemplates reads every page up to the catalog bound.
func (c *MCPClient) ListResourceTemplates(ctx context.Context) ([]MCPResourceTemplate, error) {
	var out []MCPResourceTemplate
	cursor := ""
	for page := 0; page < 100 && len(out) < MCPMaxCatalogItems; page++ {
		result, err := c.session.ListResourceTemplates(ctx, &mcp.ListResourceTemplatesParams{Cursor: cursor})
		if err != nil {
			return nil, err
		}
		for _, template := range result.ResourceTemplates {
			if template == nil {
				continue
			}
			out = append(out, MCPResourceTemplate{
				URITemplate: template.URITemplate, Name: template.Name, Title: template.Title,
				Description: mcpTruncate(template.Description, MCPMaxSchema), MIMEType: template.MIMEType,
			})
			if len(out) == MCPMaxCatalogItems {
				break
			}
		}
		if result.NextCursor == "" || result.NextCursor == cursor {
			break
		}
		cursor = result.NextCursor
	}
	return out, nil
}

// ListPrompts reads every page up to the catalog bound.
func (c *MCPClient) ListPrompts(ctx context.Context) ([]MCPPrompt, error) {
	var out []MCPPrompt
	cursor := ""
	for page := 0; page < 100 && len(out) < MCPMaxCatalogItems; page++ {
		result, err := c.session.ListPrompts(ctx, &mcp.ListPromptsParams{Cursor: cursor})
		if err != nil {
			return nil, err
		}
		for _, prompt := range result.Prompts {
			if prompt == nil {
				continue
			}
			item := MCPPrompt{Name: prompt.Name, Title: prompt.Title, Description: mcpTruncate(prompt.Description, MCPMaxSchema)}
			for _, argument := range prompt.Arguments {
				if argument == nil {
					continue
				}
				item.Arguments = append(item.Arguments, MCPPromptArgument{
					Name: argument.Name, Title: argument.Title, Description: mcpTruncate(argument.Description, MCPMaxSchema), Required: argument.Required,
				})
			}
			out = append(out, item)
			if len(out) == MCPMaxCatalogItems {
				break
			}
		}
		if result.NextCursor == "" || result.NextCursor == cursor {
			break
		}
		cursor = result.NextCursor
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
	result, err := c.session.CallTool(ctx, &mcp.CallToolParams{Name: name, Arguments: args})
	if err != nil {
		return MCPResult{}, err
	}
	return mcpToolResult(result), nil
}

// ReadResource reads one resource and converts its contents into a bounded
// result.
func (c *MCPClient) ReadResource(ctx context.Context, uri string) (MCPResult, error) {
	result, err := c.session.ReadResource(ctx, &mcp.ReadResourceParams{URI: uri})
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
	result, err := c.session.GetPrompt(ctx, &mcp.GetPromptParams{Name: name, Arguments: args})
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

// mcpSchemaText is pretty JSON for a schema or annotation value, bounded to
// one tool's share of the catalog.
func mcpSchemaText(v any) string {
	if v == nil {
		return ""
	}
	data, err := json.Marshal(v, jsontext.WithIndent("  "))
	if err != nil {
		return ""
	}
	return mcpTruncate(string(data), MCPMaxSchema)
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
