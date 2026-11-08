package postman_test

import (
	"slices"
	"testing"

	"github.com/baba983/vscroxy/internal/request"
)

func TestReadCollectionDirV3MCPMessages(t *testing.T) {
	dir := t.TempDir()
	writeV3(t, dir, map[string]string{
		".resources/definition.yaml": "$kind: collection\nname: MCP\n",
		"prompt.request.yaml": `$kind: mcp-request
name: Prompt
transport: stdio
command: server
message: '{"jsonrpc":"2.0","id":1,"method":"prompts/get","params":{"name":"greet","arguments":{"name":"Ada"}}}'
`,
		"list.request.yaml": `$kind: mcp-request
name: List
transport: sse
url: https://example.com/mcp
message: '{"jsonrpc":"2.0","id":1,"method":"tools/list"}'
`,
		"oauth.request.yaml": `$kind: mcp-request
name: OAuth
transport: sse
url: https://example.com/mcp
auth:
  type: oauth2
  credentials:
    - key: grant_type
      value: client_credentials
`,
		"unknown.request.yaml": `$kind: mcp-request
name: Unknown transport
transport: websocket
`,
	})
	col := readV3(t, dir)
	if len(col.Requests) != 3 {
		t.Fatalf("requests = %+v, want the prompt, the list and the OAuth request", col.Requests)
	}
	prompt := v3Request(t, col, "MCP", "Prompt")
	if prompt.Protocol != "MCP" || prompt.MCP.Transport != request.MCPTransportStdio ||
		prompt.MCP.Prompt != "greet" || prompt.MCP.PromptArgs != `{"name":"Ada"}` || prompt.MCP.Command != "server" {
		t.Errorf("prompt = %+v", prompt)
	}
	list := v3Request(t, col, "MCP", "List")
	if list.MCP.Tool != "" || list.MCP.ResourceURI != "" || list.MCP.Prompt != "" {
		t.Errorf("unsupported method became an operation: %+v", list.MCP)
	}
	for _, want := range []string{
		"Could not map 1 MCP JSON-RPC message (tools/list); choose the operation in the MCP editor",
		"Skipped 1 MCP request with transport websocket; the schema supports sse and stdio",
		"Imported 1 MCP OAuth 2.0 auth set; the MCP client uses a Bearer token, Basic or API key, so sign in and use a Bearer token",
	} {
		if !slices.Contains(col.Warnings, want) {
			t.Errorf("warnings %q do not contain %q", col.Warnings, want)
		}
	}
}
