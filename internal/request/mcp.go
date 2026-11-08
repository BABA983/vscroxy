package request

import (
	"encoding/json/jsontext"
	"encoding/json/v2"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/baba983/vscroxy/internal/certs"
)

// MCP transports. The zero value is Streamable HTTP, the current MCP HTTP
// transport; SSE is the legacy HTTP+SSE transport, and stdio runs a local
// command.
const (
	MCPTransportStreamableHTTP = "Streamable HTTP"
	MCPTransportSSE            = "SSE"
	MCPTransportStdio          = "stdio"
)

// MCPConfig is saved with an MCP document. ToolArgs, PromptArgs, Command,
// Args, WorkingDir and Env values are authored templates; PrepareMCP expands
// them with the active variables.
type MCPConfig struct {
	Transport       string  `json:"transport,omitempty"`
	Command         string  `json:"command,omitempty"`
	Args            string  `json:"args,omitempty"` // one argument per line
	WorkingDir      string  `json:"workingDir,omitempty"`
	Env             []Field `json:"env,omitempty"`
	Tool            string  `json:"tool,omitempty"`
	ToolArgs        string  `json:"toolArgs,omitempty"`
	ResourceURI     string  `json:"resourceUri,omitempty"`
	Prompt          string  `json:"prompt,omitempty"`
	PromptArgs      string  `json:"promptArgs,omitempty"`
	TimeoutSeconds  int     `json:"timeoutSeconds,omitzero"`
	ProtocolVersion string  `json:"protocolVersion,omitempty"`
}

// MCPPlan is a document prepared for one connection. Env values are expanded
// secrets and must never be logged.
type MCPPlan struct {
	Transport       string
	URL             string
	Header          http.Header
	Command         string
	Args            []string
	Env             []string
	WorkingDir      string
	Timeout         time.Duration
	ProtocolVersion string
	Insecure        bool
	Certificates    certs.Settings
	// Proxy, ProxyCA and Jar are the capture proxy and cookie jar of the
	// desktop. Tests and other callers may leave them empty.
	Proxy   string
	ProxyCA []byte
	Jar     http.CookieJar

	Tool        string
	ToolArgs    jsontext.Value
	ResourceURI string
	Prompt      string
	PromptArgs  jsontext.Value
}

// MCPTransport normalizes a saved transport name. An empty name is Streamable
// HTTP so a document created before a transport was chosen still connects.
func MCPTransport(name string) (string, error) {
	switch strings.TrimSpace(name) {
	case "", "Streamable HTTP", "streamable-http", "http":
		return MCPTransportStreamableHTTP, nil
	case "SSE", "sse", "http+sse":
		return MCPTransportSSE, nil
	case "stdio", "Stdio":
		return MCPTransportStdio, nil
	}
	return "", fmt.Errorf("unsupported MCP transport %q", name)
}

// PrepareMCP shares HTTP's variables, inherited headers/auth and host-bound
// Vault access. It never runs HTTP scripts or body encoders.
func (r Request) PrepareMCP(in Context, vars map[string]string) (MCPPlan, error) {
	if r.Protocol != "MCP" {
		return MCPPlan{}, fmt.Errorf("not an MCP document")
	}
	if r.Pre != "" || r.Post != "" || r.Checks != (Checks{}) || len(r.TokenSources) > 0 {
		return MCPPlan{}, fmt.Errorf("MCP does not run HTTP scripts, Checks or token bindings; remove them first")
	}
	for _, f := range in.Folders {
		if f.Pre != "" || f.Post != "" {
			return MCPPlan{}, fmt.Errorf("MCP does not run inherited HTTP scripts; use a collection without scripts")
		}
	}
	transport, err := MCPTransport(r.MCP.Transport)
	if err != nil {
		return MCPPlan{}, err
	}
	r.Auth, _ = in.InheritAuth(r.Auth)
	switch transport {
	case MCPTransportStdio:
		if r.Auth.Type != "" && r.Auth.Type != "None" {
			return MCPPlan{}, fmt.Errorf("stdio MCP servers receive credentials through environment variables, not HTTP auth")
		}
	default:
		switch r.Auth.Type {
		case "", "None", "Bearer", "Basic", "API key":
		default:
			return MCPPlan{}, fmt.Errorf("MCP over HTTP supports None, Bearer, Basic or API key auth")
		}
	}
	values := in.Values(vars)
	r = r.withDynamic(values)
	r.Body, r.BodyFile, r.BodyMode, r.Form, r.GraphQL = "", "", "Raw", nil, GraphQLBody{}
	r.MCP.Transport = transport
	// Resolve Vault references before expanding the document's own fields:
	// unlock reads the variable names the raw fields still carry.
	if err := in.unlock(r, values); err != nil {
		return MCPPlan{}, err
	}
	cfg := r.MCP
	cfg.Command = ExpandVars(cfg.Command, values)
	cfg.Args = ExpandVars(cfg.Args, values)
	cfg.WorkingDir = ExpandVars(cfg.WorkingDir, values)
	cfg.Tool = ExpandVars(cfg.Tool, values)
	cfg.ToolArgs = ExpandVars(cfg.ToolArgs, values)
	cfg.ResourceURI = ExpandVars(cfg.ResourceURI, values)
	cfg.Prompt = ExpandVars(cfg.Prompt, values)
	cfg.PromptArgs = ExpandVars(cfg.PromptArgs, values)
	cfg.Env = append([]Field(nil), cfg.Env...)
	for i := range cfg.Env {
		cfg.Env[i].Name = ExpandVars(cfg.Env[i].Name, values)
		cfg.Env[i].Value = ExpandVars(cfg.Env[i].Value, values)
	}
	r.MCP = cfg
	for _, name := range r.Variables() {
		if _, ok := values[name]; !ok {
			return MCPPlan{}, fmt.Errorf("set variable {{%s}} before connecting MCP", name)
		}
	}
	r.Protocol, r.Method = "", "POST"
	plan := MCPPlan{
		Transport: transport, Tool: cfg.Tool, ResourceURI: cfg.ResourceURI, Prompt: cfg.Prompt,
		Certificates: in.Certificates.Clone(),
	}
	plan.Insecure = !in.InheritSettings(r.Settings).Verifies()
	if plan.ToolArgs, err = mcpArguments(cfg.ToolArgs); err != nil {
		return MCPPlan{}, fmt.Errorf("tool arguments: %w", err)
	}
	if plan.PromptArgs, err = mcpArguments(cfg.PromptArgs); err != nil {
		return MCPPlan{}, fmt.Errorf("prompt arguments: %w", err)
	}
	var promptArgs map[string]string
	if err := json.Unmarshal(plan.PromptArgs, &promptArgs); err != nil {
		return MCPPlan{}, fmt.Errorf("prompt arguments: every value must be a string")
	}
	if plan.ProtocolVersion, err = mcpProtocolVersion(cfg.ProtocolVersion); err != nil {
		return MCPPlan{}, err
	}
	settings := in.InheritSettings(r.Settings)
	seconds := cfg.TimeoutSeconds
	if seconds < 0 {
		return MCPPlan{}, fmt.Errorf("MCP timeout must be zero or positive")
	}
	if seconds == 0 {
		switch {
		case settings.Timeout() > 0:
			seconds = int(settings.Timeout() / time.Second)
		case settings.Unlimited():
			seconds = 0
		default:
			seconds = 30
		}
	}
	if seconds > 3600 {
		return MCPPlan{}, fmt.Errorf("MCP timeout must be at most 3600 seconds")
	}
	plan.Timeout = time.Duration(seconds) * time.Second

	switch transport {
	case MCPTransportStdio:
		plan.Command = strings.TrimSpace(cfg.Command)
		if plan.Command == "" {
			return MCPPlan{}, fmt.Errorf("enter the command that starts the stdio MCP server")
		}
		for line := range strings.SplitSeq(cfg.Args, "\n") {
			if arg := strings.TrimRight(line, "\r"); strings.TrimSpace(arg) != "" {
				plan.Args = append(plan.Args, arg)
			}
		}
		plan.WorkingDir = strings.TrimSpace(cfg.WorkingDir)
		seen := map[string]bool{}
		for _, field := range cfg.Env {
			if field.Disabled {
				continue
			}
			name := strings.TrimSpace(field.Name)
			if name == "" {
				continue
			}
			if strings.ContainsAny(name, "=\x00") || strings.ContainsRune(field.Value, 0) {
				return MCPPlan{}, fmt.Errorf("environment variable %q has an invalid name or value", name)
			}
			if seen[name] {
				// Later rows win, as they do in a shell environment.
				for i := len(plan.Env) - 1; i >= 0; i-- {
					if strings.HasPrefix(plan.Env[i], name+"=") {
						plan.Env = append(plan.Env[:i], plan.Env[i+1:]...)
						break
					}
				}
			}
			seen[name] = true
			plan.Env = append(plan.Env, name+"="+field.Value)
		}
	default:
		u, err := url.Parse(strings.TrimSpace(ExpandVars(r.URL, values)))
		if err != nil || u.Scheme == "" || u.Hostname() == "" || u.User != nil || u.Fragment != "" {
			return MCPPlan{}, fmt.Errorf("MCP over HTTP requires an absolute HTTP(S) endpoint without credentials or a fragment")
		}
		if u.Scheme != "http" && u.Scheme != "https" {
			return MCPPlan{}, fmt.Errorf("MCP over HTTP requires an HTTP(S) endpoint")
		}
		r.URL = u.String()
		prepared, err := r.Prepare(values)
		if err != nil {
			return MCPPlan{}, err
		}
		plan.URL, plan.Header = prepared.URL, prepared.Header
	}
	return plan, nil
}

// mcpArguments validates authored JSON arguments; an empty editor means an
// empty object, as a call with no parameters does.
func mcpArguments(text string) (jsontext.Value, error) {
	text = strings.TrimSpace(text)
	if text == "" {
		return jsontext.Value(`{}`), nil
	}
	value := jsontext.Value(text)
	if !value.IsValid() || value.Kind() != '{' {
		return nil, fmt.Errorf("arguments must be a JSON object")
	}
	return value, nil
}

// mcpProtocolVersion maps the saved setting to the version sent in initialize.
// "legacy" is the last protocol before the stateless 2026-07-28 revision.
func mcpProtocolVersion(saved string) (string, error) {
	switch strings.TrimSpace(saved) {
	case "", "auto":
		return "", nil
	case "legacy", "2025-11-25":
		return "2025-11-25", nil
	case "2026-07-28":
		return "2026-07-28", nil
	}
	return "", fmt.Errorf("unsupported MCP protocol version %q", saved)
}

// mcpJSONText is a display form for an MCP result's JSON value.
func mcpJSONText(v any) string {
	if v == nil {
		return ""
	}
	data, err := json.Marshal(v, jsontext.WithIndent("  "))
	if err != nil {
		return fmt.Sprint(v)
	}
	return string(data)
}
