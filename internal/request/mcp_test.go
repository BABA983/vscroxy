package request

import (
	"bytes"
	"context"
	"encoding/json/jsontext"
	"encoding/pem"
	"fmt"
	"image"
	"image/color"
	"image/png"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/baba983/vscroxy/internal/certs"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

type mcpEchoIn struct {
	Text string `json:"text"`
}

type mcpEchoOut struct {
	Echo  string `json:"echo"`
	Count int    `json:"count"`
}

type mcpHeadersOut struct {
	Test          string   `json:"test"`
	Authorization string   `json:"authorization"`
	Duplicate     []string `json:"duplicate"`
}

func mcpTestPNG(t *testing.T) []byte {
	t.Helper()
	img := image.NewRGBA(image.Rect(0, 0, 8, 6))
	for y := range 6 {
		for x := range 8 {
			img.Set(x, y, color.RGBA{R: uint8(x * 30), G: uint8(y * 40), B: 180, A: 255})
		}
	}
	var b bytes.Buffer
	if err := png.Encode(&b, img); err != nil {
		t.Fatal(err)
	}
	return b.Bytes()
}

func mcpFixture(t *testing.T) *mcp.Server {
	t.Helper()
	server := mcp.NewServer(&mcp.Implementation{Name: "fixture", Title: "Fixture server", Version: "1.2.3"}, &mcp.ServerOptions{
		Instructions: "Use echo to repeat text.",
	})
	pngData := mcpTestPNG(t)
	mcp.AddTool(server, &mcp.Tool{Name: "echo", Description: "Echo text", Title: "Echo"},
		func(_ context.Context, _ *mcp.CallToolRequest, in mcpEchoIn) (*mcp.CallToolResult, mcpEchoOut, error) {
			return &mcp.CallToolResult{Content: []mcp.Content{&mcp.TextContent{Text: "echo: " + in.Text}}},
				mcpEchoOut{Echo: in.Text, Count: 1}, nil
		})
	mcp.AddTool(server, &mcp.Tool{Name: "fail", Description: "Fail on purpose"},
		func(context.Context, *mcp.CallToolRequest, struct{}) (*mcp.CallToolResult, struct{}, error) {
			return &mcp.CallToolResult{IsError: true, Content: []mcp.Content{&mcp.TextContent{Text: "boom"}}}, struct{}{}, nil
		})
	mcp.AddTool(server, &mcp.Tool{Name: "image", Description: "Return an image"},
		func(context.Context, *mcp.CallToolRequest, struct{}) (*mcp.CallToolResult, struct{}, error) {
			return &mcp.CallToolResult{Content: []mcp.Content{
				&mcp.TextContent{Text: "picture"},
				&mcp.ImageContent{MIMEType: "image/png", Data: pngData},
			}}, struct{}{}, nil
		})
	mcp.AddTool(server, &mcp.Tool{Name: "headers", Description: "Echo request headers"},
		func(_ context.Context, req *mcp.CallToolRequest, _ struct{}) (*mcp.CallToolResult, mcpHeadersOut, error) {
			out := mcpHeadersOut{}
			if req.Extra != nil && req.Extra.Header != nil {
				out.Test = req.Extra.Header.Get("X-Test")
				out.Authorization = req.Extra.Header.Get("Authorization")
				out.Duplicate = req.Extra.Header.Values("X-Duplicate")
			}
			return &mcp.CallToolResult{Content: []mcp.Content{&mcp.TextContent{Text: "headers"}}}, out, nil
		})
	mcp.AddTool(server, &mcp.Tool{Name: "log", Description: "Send a server log notification"},
		func(ctx context.Context, req *mcp.CallToolRequest, _ struct{}) (*mcp.CallToolResult, struct{}, error) {
			if err := req.Session.Log(ctx, &mcp.LoggingMessageParams{Level: mcp.LoggingLevel("info"), Logger: "fixture", Data: "hello log"}); err != nil {
				return nil, struct{}{}, err
			}
			return &mcp.CallToolResult{Content: []mcp.Content{&mcp.TextContent{Text: "logged"}}}, struct{}{}, nil
		})
	mcp.AddTool(server, &mcp.Tool{Name: "slow", Description: "Wait until cancelled"},
		func(ctx context.Context, _ *mcp.CallToolRequest, _ struct{}) (*mcp.CallToolResult, struct{}, error) {
			select {
			case <-ctx.Done():
				return nil, struct{}{}, ctx.Err()
			case <-time.After(5 * time.Second):
				return &mcp.CallToolResult{Content: []mcp.Content{&mcp.TextContent{Text: "late"}}}, struct{}{}, nil
			}
		})
	mcp.AddTool(server, &mcp.Tool{Name: "big", Description: "Return a large text"},
		func(context.Context, *mcp.CallToolRequest, struct{}) (*mcp.CallToolResult, struct{}, error) {
			return &mcp.CallToolResult{Content: []mcp.Content{&mcp.TextContent{Text: strings.Repeat("x", MCPMaxResultBytes+1024)}}}, struct{}{}, nil
		})
	server.AddResource(&mcp.Resource{URI: "memo://welcome", Name: "welcome", Title: "Welcome", MIMEType: "text/plain"},
		func(context.Context, *mcp.ReadResourceRequest) (*mcp.ReadResourceResult, error) {
			return &mcp.ReadResourceResult{Contents: []*mcp.ResourceContents{{URI: "memo://welcome", MIMEType: "text/plain", Text: "Hello memo"}}}, nil
		})
	server.AddResourceTemplate(&mcp.ResourceTemplate{URITemplate: "memo://{name}", Name: "memo", MIMEType: "text/plain"},
		func(_ context.Context, req *mcp.ReadResourceRequest) (*mcp.ReadResourceResult, error) {
			return &mcp.ReadResourceResult{Contents: []*mcp.ResourceContents{{URI: req.Params.URI, MIMEType: "text/plain", Text: "template " + req.Params.URI}}}, nil
		})
	server.AddPrompt(&mcp.Prompt{Name: "greet", Title: "Greet", Description: "A greeting",
		Arguments: []*mcp.PromptArgument{{Name: "name", Description: "Who to greet", Required: true}}},
		func(_ context.Context, req *mcp.GetPromptRequest) (*mcp.GetPromptResult, error) {
			return &mcp.GetPromptResult{Description: "Greeting", Messages: []*mcp.PromptMessage{
				{Role: mcp.Role("user"), Content: &mcp.TextContent{Text: "Hello " + req.Params.Arguments["name"]}},
			}}, nil
		})
	return server
}

func mcpTestPlan(t *testing.T, transport, endpoint, version string) MCPPlan {
	t.Helper()
	r := Request{
		Protocol: "MCP", URL: endpoint, Method: "POST", Headers: "X-Test: fixture\nX-Duplicate: one\nX-Duplicate: two",
		Auth: Auth{Type: "Bearer", Token: "token-73"},
		MCP:  MCPConfig{Transport: transport, ProtocolVersion: version, ToolArgs: `{"text":"hello"}`},
	}
	plan, err := r.PrepareMCP(Context{}, nil)
	if err != nil {
		t.Fatal(err)
	}
	return plan
}

// mcpSeenHeaders records the document headers on every HTTP request. The SSE
// transport does not attach RequestExtra to server calls, so this checks what
// actually reached the server rather than what the SDK exposed to the tool.
type mcpSeenHeaders struct {
	mu   sync.Mutex
	seen http.Header
}

func (s *mcpSeenHeaders) wrap(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		s.mu.Lock()
		if s.seen == nil {
			s.seen = http.Header{}
		}
		for _, name := range []string{"X-Test", "Authorization", "X-Duplicate"} {
			if values := r.Header.Values(name); len(values) > 0 {
				s.seen[name] = values
			}
		}
		s.mu.Unlock()
		next.ServeHTTP(w, r)
	})
}

func TestMCPStreamableHTTP(t *testing.T) {
	server := mcpFixture(t)
	seen := &mcpSeenHeaders{}
	httpServer := httptest.NewServer(seen.wrap(mcp.NewStreamableHTTPHandler(func(*http.Request) *mcp.Server { return server }, nil)))
	defer httpServer.Close()
	testMCPHTTPClient(t, MCPTransportStreamableHTTP, httpServer.URL, seen)
}

func TestMCPSSE(t *testing.T) {
	server := mcpFixture(t)
	seen := &mcpSeenHeaders{}
	httpServer := httptest.NewServer(seen.wrap(mcp.NewSSEHandler(func(*http.Request) *mcp.Server { return server }, nil)))
	defer httpServer.Close()
	testMCPHTTPClient(t, MCPTransportSSE, httpServer.URL, seen)
}

func testMCPHTTPClient(t *testing.T, transport, endpoint string, seen *mcpSeenHeaders) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	plan := mcpTestPlan(t, transport, endpoint, "2025-11-25")
	client, err := DialMCP(ctx, plan, MCPHandlers{})
	if err != nil {
		t.Fatalf("connect %s: %v", transport, err)
	}
	defer client.Close()
	info := client.Server()
	if info.Name != "fixture" || info.Version != "1.2.3" || info.ProtocolVersion != "2025-11-25" || !strings.Contains(info.Capabilities, `"tools"`) {
		t.Errorf("server info = %+v", info)
	}
	tools, err := client.ListTools(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(tools) != 7 {
		t.Fatalf("tools = %d, want 7: %+v", len(tools), tools)
	}
	var echo MCPTool
	for _, tool := range tools {
		if tool.Name == "echo" {
			echo = tool
		}
	}
	if echo.Name == "" || !strings.Contains(echo.InputSchema, `"text"`) {
		t.Errorf("echo tool = %+v", echo)
	}
	resources, err := client.ListResources(ctx)
	if err != nil || len(resources) != 1 || resources[0].URI != "memo://welcome" {
		t.Errorf("resources = %+v, %v", resources, err)
	}
	templates, err := client.ListResourceTemplates(ctx)
	if err != nil || len(templates) != 1 || templates[0].URITemplate != "memo://{name}" {
		t.Errorf("templates = %+v, %v", templates, err)
	}
	prompts, err := client.ListPrompts(ctx)
	if err != nil || len(prompts) != 1 || prompts[0].Name != "greet" || len(prompts[0].Arguments) != 1 {
		t.Errorf("prompts = %+v, %v", prompts, err)
	}

	result, err := client.CallTool(ctx, "echo", jsontextValue(t, `{"text":"hello"}`))
	if err != nil {
		t.Fatal(err)
	}
	if result.IsError || !strings.Contains(result.Text, "echo: hello") || !strings.Contains(result.Structured, `"echo": "hello"`) {
		t.Errorf("echo result = %+v", result)
	}
	if result.Content[0].Kind != "text" || result.Content[0].Text != "echo: hello" {
		t.Errorf("echo content = %+v", result.Content)
	}

	failed, err := client.CallTool(ctx, "fail", jsontextValue(t, `{}`))
	if err != nil {
		t.Fatal(err)
	}
	if !failed.IsError || !strings.Contains(failed.Text, "boom") {
		t.Errorf("fail result = %+v", failed)
	}

	imageResult, err := client.CallTool(ctx, "image", jsontextValue(t, `{}`))
	if err != nil {
		t.Fatal(err)
	}
	if len(imageResult.Content) != 2 || imageResult.Content[1].Kind != "image" || len(imageResult.Content[1].Data) == 0 {
		t.Errorf("image result = %+v", imageResult.Content)
	}

	seen.mu.Lock()
	sent := seen.seen.Clone()
	seen.mu.Unlock()
	if got := sent.Get("X-Test"); got != "fixture" {
		t.Errorf("%s X-Test = %q", transport, got)
	}
	if got := sent.Get("Authorization"); got != "Bearer token-73" {
		t.Errorf("%s Authorization = %q", transport, got)
	}
	if got := sent.Values("X-Duplicate"); len(got) != 2 || got[0] != "one" || got[1] != "two" {
		t.Errorf("%s X-Duplicate = %+v", transport, got)
	}
	if transport == MCPTransportStreamableHTTP {
		headers, err := client.CallTool(ctx, "headers", jsontextValue(t, `{}`))
		if err != nil {
			t.Fatal(err)
		}
		for _, want := range []string{`"test": "fixture"`, `"authorization": "Bearer token-73"`, `"one"`, `"two"`} {
			if !strings.Contains(headers.Structured, want) {
				t.Errorf("headers %s missing from %s", want, headers.Structured)
			}
		}
	}

	read, err := client.ReadResource(ctx, "memo://welcome")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(read.Text, "Hello memo") || read.Content[0].Kind != "text" {
		t.Errorf("resource result = %+v", read)
	}
	template, err := client.ReadResource(ctx, "memo://one")
	if err != nil || !strings.Contains(template.Text, "template memo://one") {
		t.Errorf("template resource = %+v, %v", template, err)
	}
	prompt, err := client.GetPrompt(ctx, "greet", jsontextValue(t, `{"name":"Ada"}`))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(prompt.Text, "user") || !strings.Contains(prompt.Text, "Hello Ada") || prompt.Content[0].Role != "user" ||
		prompt.Description != "Greeting" || prompt.Structured != "" {
		t.Errorf("prompt result = %+v", prompt)
	}
}

func TestMCPStdio(t *testing.T) {
	if os.Getenv("VSCROXY_MCP_STDIO_HELPER") == "1" {
		t.Skip("helper process")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	r := Request{
		Protocol: "MCP", Method: "POST",
		MCP: MCPConfig{Transport: MCPTransportStdio, Command: os.Args[0], Args: "-test.run=TestMCPStdioHelper\n-test.v=false",
			Env: []Field{{Name: "VSCROXY_MCP_STDIO_HELPER", Value: "1"}}, ToolArgs: `{"text":"stdio"}`},
	}
	plan, err := r.PrepareMCP(Context{}, nil)
	if err != nil {
		t.Fatal(err)
	}
	client, err := DialMCP(ctx, plan, MCPHandlers{})
	if err != nil {
		t.Fatalf("connect stdio: %v", err)
	}
	defer client.Close()
	if info := client.Server(); info.Name != "fixture" {
		t.Errorf("stdio server = %+v", info)
	}
	tools, err := client.ListTools(ctx)
	if err != nil || len(tools) != 7 {
		t.Fatalf("stdio tools = %d, %v", len(tools), err)
	}
	result, err := client.CallTool(ctx, "echo", jsontextValue(t, `{"text":"stdio"}`))
	if err != nil || !strings.Contains(result.Text, "echo: stdio") {
		t.Fatalf("stdio echo = %+v, %v", result, err)
	}
}

func TestMCPStdioHelper(t *testing.T) {
	if os.Getenv("VSCROXY_MCP_STDIO_HELPER") != "1" {
		t.Skip("helper process")
	}
	server := mcpFixture(t)
	session, err := server.Connect(context.Background(), &mcp.StdioTransport{}, nil)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	_ = session.Wait()
	os.Exit(0)
}

func TestMCPNotifications(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	var mu sync.Mutex
	var notifications []string
	var logs []string
	r := Request{Protocol: "MCP", Method: "POST", MCP: MCPConfig{Transport: MCPTransportStdio, Command: os.Args[0],
		Args: "-test.run=TestMCPStdioHelper\n-test.v=false", ProtocolVersion: "2025-11-25",
		Env: []Field{{Name: "VSCROXY_MCP_STDIO_HELPER", Value: "1"}}}}
	plan, err := r.PrepareMCP(Context{}, nil)
	if err != nil {
		t.Fatal(err)
	}
	client, err := DialMCP(ctx, plan, MCPHandlers{
		Notification: func(method, text string) {
			mu.Lock()
			notifications = append(notifications, method+" "+text)
			mu.Unlock()
		},
		Log: func(level, logger, text string) {
			mu.Lock()
			logs = append(logs, level+" "+logger+" "+text)
			mu.Unlock()
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	defer client.Close()
	if _, err := client.CallTool(ctx, "log", jsontextValue(t, `{}`)); err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		mu.Lock()
		gotLog := len(logs) > 0
		mu.Unlock()
		if gotLog {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	mu.Lock()
	defer mu.Unlock()
	if len(logs) == 0 || !strings.Contains(logs[0], "hello log") {
		t.Errorf("logs = %+v, notifications = %+v", logs, notifications)
	}
}

func TestMCPCancellation(t *testing.T) {
	server := mcpFixture(t)
	httpServer := httptest.NewServer(mcp.NewStreamableHTTPHandler(func(*http.Request) *mcp.Server { return server }, nil))
	defer httpServer.Close()
	plan := mcpTestPlan(t, MCPTransportStreamableHTTP, httpServer.URL, "2025-11-25")
	client, err := DialMCP(context.Background(), plan, MCPHandlers{})
	if err != nil {
		t.Fatal(err)
	}
	defer client.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 150*time.Millisecond)
	defer cancel()
	start := time.Now()
	_, err = client.CallTool(ctx, "slow", jsontextValue(t, `{}`))
	if err == nil {
		t.Fatal("slow call returned no error")
	}
	if elapsed := time.Since(start); elapsed > 3*time.Second {
		t.Fatalf("cancellation took %v", elapsed)
	}
}

func TestMCPTLS(t *testing.T) {
	server := mcpFixture(t)
	httpServer := httptest.NewTLSServer(mcp.NewStreamableHTTPHandler(func(*http.Request) *mcp.Server { return server }, nil))
	defer httpServer.Close()
	caPath := filepath.Join(t.TempDir(), "server.pem")
	caPEM := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: httpServer.Certificate().Raw})
	if err := os.WriteFile(caPath, caPEM, 0o600); err != nil {
		t.Fatal(err)
	}
	plan := mcpTestPlan(t, MCPTransportStreamableHTTP, httpServer.URL, "2025-11-25")
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	trustSystem := false
	plan.Certificates = certs.Settings{TrustSystemRoots: &trustSystem}
	if client, err := DialMCP(ctx, plan, MCPHandlers{}); err == nil {
		client.Close()
		t.Error("an untrusted self-signed server connected")
	}
	plan.Certificates = certs.Settings{TrustSystemRoots: &trustSystem, RootCAs: []string{caPath}}
	client, err := DialMCP(ctx, plan, MCPHandlers{})
	if err != nil {
		t.Fatalf("custom root: %v", err)
	}
	client.Close()
	plan.Certificates = certs.Settings{}
	plan.Insecure = true
	client, err = DialMCP(ctx, plan, MCPHandlers{})
	if err != nil {
		t.Fatalf("verification off: %v", err)
	}
	client.Close()
}

func TestMCPResultBounds(t *testing.T) {
	server := mcpFixture(t)
	httpServer := httptest.NewServer(mcp.NewStreamableHTTPHandler(func(*http.Request) *mcp.Server { return server }, nil))
	defer httpServer.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	plan := mcpTestPlan(t, MCPTransportStreamableHTTP, httpServer.URL, "2025-11-25")
	client, err := DialMCP(ctx, plan, MCPHandlers{})
	if err != nil {
		t.Fatal(err)
	}
	defer client.Close()
	result, err := client.CallTool(ctx, "big", jsontextValue(t, `{}`))
	if err != nil {
		t.Fatal(err)
	}
	if !result.Truncated || len(result.Text) > MCPMaxResultText+1024 {
		t.Errorf("bounded result: truncated=%v text=%d bytes=%d", result.Truncated, len(result.Text), result.Bytes)
	}
}

func TestPrepareMCP(t *testing.T) {
	t.Run("stdio expands env and arguments", func(t *testing.T) {
		r := Request{Protocol: "MCP", MCP: MCPConfig{
			Transport: MCPTransportStdio, Command: "{{command}}", Args: "one\n\n two \n", WorkingDir: "{{dir}}",
			Env:  []Field{{Name: "TOKEN", Value: "{{token}}"}, {Name: "OFF", Value: "x", Disabled: true}},
			Tool: "echo", ToolArgs: `{"text":"{{token}}"}`,
		}}
		plan, err := r.PrepareMCP(Context{}, map[string]string{"command": "/bin/server", "dir": "/tmp", "token": "secret-73"})
		if err != nil {
			t.Fatal(err)
		}
		if plan.Command != "/bin/server" || plan.WorkingDir != "/tmp" || len(plan.Args) != 2 || plan.Args[0] != "one" || plan.Args[1] != " two " {
			t.Errorf("stdio plan = %+v", plan)
		}
		if len(plan.Env) != 1 || plan.Env[0] != "TOKEN=secret-73" {
			t.Errorf("env = %+v", plan.Env)
		}
		if string(plan.ToolArgs) != `{"text":"secret-73"}` {
			t.Errorf("tool args = %s", plan.ToolArgs)
		}
	})
	t.Run("HTTP auth, query key and headers", func(t *testing.T) {
		r := Request{Protocol: "MCP", URL: "https://mcp.example.com/rpc?x=1", Headers: "X-Test: {{tag}}",
			Auth: Auth{Type: "API key", Key: "key", Value: "{{secret}}", Location: "Query"},
			MCP:  MCPConfig{Transport: MCPTransportSSE, ProtocolVersion: "legacy", TimeoutSeconds: 45}}
		plan, err := r.PrepareMCP(Context{}, map[string]string{"tag": "one", "secret": "s3"})
		if err != nil {
			t.Fatal(err)
		}
		if plan.URL != "https://mcp.example.com/rpc?key=s3&x=1" || plan.Header.Get("X-Test") != "one" {
			t.Errorf("HTTP plan URL=%q header=%v", plan.URL, plan.Header)
		}
		if plan.ProtocolVersion != "2025-11-25" || plan.Timeout != 45*time.Second {
			t.Errorf("HTTP plan version=%q timeout=%v", plan.ProtocolVersion, plan.Timeout)
		}
	})
	t.Run("resolves Vault references after variable expansion", func(t *testing.T) {
		r := Request{Protocol: "MCP", URL: "https://mcp.example.com/rpc", Headers: "Authorization: Bearer {{vault:token}}",
			MCP: MCPConfig{Tool: "echo", ToolArgs: `{"text":"{{vault:token}}"}`}}
		var host string
		plan, err := r.PrepareMCP(Context{Vault: func(name, at string) (string, error) {
			host = at
			if name != "token" {
				t.Fatalf("vault name = %q", name)
			}
			return "secret-73", nil
		}}, nil)
		if err != nil {
			t.Fatal(err)
		}
		if host != "mcp.example.com" || plan.Header.Get("Authorization") != "Bearer secret-73" || string(plan.ToolArgs) != `{"text":"secret-73"}` {
			t.Errorf("vault plan host=%q header=%v args=%s", host, plan.Header, plan.ToolArgs)
		}
	})
	t.Run("rejects scripts and undefined variables", func(t *testing.T) {
		r := Request{Protocol: "MCP", URL: "https://mcp.example.com/rpc", Pre: "pm.test()", MCP: MCPConfig{}}
		if _, err := r.PrepareMCP(Context{}, nil); err == nil || !strings.Contains(err.Error(), "scripts") {
			t.Errorf("script error = %v", err)
		}
		r.Pre = ""
		r.MCP.ToolArgs = `{"text":"{{missing}}"}`
		if _, err := r.PrepareMCP(Context{}, nil); err == nil || !strings.Contains(err.Error(), "missing") {
			t.Errorf("variable error = %v", err)
		}
	})
	t.Run("stdio rejects HTTP auth", func(t *testing.T) {
		r := Request{Protocol: "MCP", Auth: Auth{Type: "Bearer", Token: "x"}, MCP: MCPConfig{Transport: MCPTransportStdio, Command: "server"}}
		if _, err := r.PrepareMCP(Context{}, nil); err == nil || !strings.Contains(err.Error(), "environment") {
			t.Errorf("stdio auth error = %v", err)
		}
	})
}

func jsontextValue(t *testing.T, text string) jsontext.Value {
	t.Helper()
	return jsontext.Value(text)
}
