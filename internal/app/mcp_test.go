package app

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/baba983/vscroxy/internal/request"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

type appMCPIn struct {
	Text string `json:"text"`
}

func appMCPFixture() *mcp.Server {
	server := mcp.NewServer(&mcp.Implementation{Name: "app-fixture", Version: "1.0.0"}, nil)
	mcp.AddTool(server, &mcp.Tool{Name: "echo", Description: "Echo text"},
		func(_ context.Context, _ *mcp.CallToolRequest, in appMCPIn) (*mcp.CallToolResult, appMCPIn, error) {
			return &mcp.CallToolResult{Content: []mcp.Content{&mcp.TextContent{Text: "echo: " + in.Text}}}, in, nil
		})
	server.AddResource(&mcp.Resource{URI: "memo://welcome", Name: "welcome", MIMEType: "text/plain"},
		func(context.Context, *mcp.ReadResourceRequest) (*mcp.ReadResourceResult, error) {
			return &mcp.ReadResourceResult{Contents: []*mcp.ResourceContents{{URI: "memo://welcome", MIMEType: "text/plain", Text: "hello app"}}}, nil
		})
	return server
}

func waitMCPSession(t *testing.T, s *mcpSession, done func(mcpSnapshot) bool) mcpSnapshot {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		snapshot := s.snapshot()
		if done(snapshot) {
			return snapshot
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("MCP session did not settle: %+v", s.snapshot())
	return mcpSnapshot{}
}

func TestMCPSessionCatalogAndOperations(t *testing.T) {
	server := appMCPFixture()
	httpServer := httptest.NewServer(mcp.NewStreamableHTTPHandler(func(*http.Request) *mcp.Server { return server }, nil))
	defer httpServer.Close()
	r := request.Request{Protocol: "MCP", URL: httpServer.URL, Method: "POST", MCP: request.MCPConfig{
		Transport: request.MCPTransportStreamableHTTP, ProtocolVersion: "2025-11-25",
		Tool: "echo", ToolArgs: `{"text":"hello"}`, TimeoutSeconds: 10,
	}}
	plan, err := r.PrepareMCP(request.Context{}, nil)
	if err != nil {
		t.Fatal(err)
	}
	s := openMCPSession(plan, r, request.Context{}, nil, nil)
	defer s.disconnect()
	snapshot := waitMCPSession(t, s, func(snapshot mcpSnapshot) bool {
		return snapshot.connected && !snapshot.busy && len(snapshot.tools) == 1 && len(snapshot.resources) == 1
	})
	if snapshot.server.Name != "app-fixture" || !snapshot.server.Tools || !snapshot.server.Resources {
		t.Errorf("server = %+v", snapshot.server)
	}
	if len(snapshot.tools) != 1 || snapshot.tools[0].Name != "echo" {
		t.Errorf("tools = %+v", snapshot.tools)
	}
	if len(snapshot.resources) != 1 || snapshot.resources[0].URI != "memo://welcome" {
		t.Errorf("resources = %+v", snapshot.resources)
	}

	if _, err := s.callTool(request.MCPConfig{Transport: request.MCPTransportStreamableHTTP, Tool: "echo", ToolArgs: `{"text":"hello"}`}); err != nil {
		t.Fatal(err)
	}
	snapshot = waitMCPSession(t, s, func(snapshot mcpSnapshot) bool { return !snapshot.busy && snapshot.result != nil })
	if snapshot.result == nil || !strings.Contains(snapshot.result.Text, "echo: hello") || !strings.Contains(snapshot.result.Structured, `"text": "hello"`) {
		t.Errorf("tool result = %+v", snapshot.result)
	}
	if !strings.Contains(snapshot.events[len(snapshot.events)-1].text, "echo: hello") {
		t.Errorf("timeline = %+v", snapshot.events)
	}

	if _, err := s.readResource(request.MCPConfig{Transport: request.MCPTransportStreamableHTTP, ResourceURI: "memo://welcome"}); err != nil {
		t.Fatal(err)
	}
	snapshot = waitMCPSession(t, s, func(snapshot mcpSnapshot) bool {
		return !snapshot.busy && strings.Contains(snapshot.result.Text, "hello app")
	})
	if snapshot.result.Kind != "Resource" || !strings.Contains(snapshot.result.Text, "hello app") {
		t.Errorf("resource result = %+v", snapshot.result)
	}

	s.disconnect()
	waitMCPSession(t, s, func(snapshot mcpSnapshot) bool { return snapshot.finished })
}

func TestMCPTimelineBound(t *testing.T) {
	s := &mcpSession{}
	for i := range mcpTimelineLimit + 7 {
		s.appendLocked("System", "event", fmt.Sprint(i))
	}
	snapshot := s.snapshot()
	if len(snapshot.events) != mcpTimelineLimit || snapshot.evicted != 7 {
		t.Fatalf("timeline = %d events, %d evicted", len(snapshot.events), snapshot.evicted)
	}
	if snapshot.events[0].seq != 8 || snapshot.events[len(snapshot.events)-1].seq != mcpTimelineLimit+7 {
		t.Errorf("timeline seq = %d..%d", snapshot.events[0].seq, snapshot.events[len(snapshot.events)-1].seq)
	}
}
