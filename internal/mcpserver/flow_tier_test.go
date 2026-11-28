package mcpserver_test

import (
	"context"
	"slices"
	"strings"
	"sync"
	"testing"

	"github.com/baba983/vscroxy/internal/control"
	"github.com/baba983/vscroxy/internal/mcpserver"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

var flowsTierWrites = []string{
	"breakpoints.resolve", "capture.recording", "collection-recording.start", "collection-recording.stop",
	"collection.load", "collection.run", "flows.annotate", "flows.replay", "mock.start",
	"mock.stop", "request.run", "requests.save", "rules.create", "rules.delete", "runs.cancel",
}

func rejected(t *testing.T, cs *mcp.ClientSession, name string, args any) bool {
	t.Helper()
	result, err := cs.CallTool(t.Context(), &mcp.CallToolParams{Name: name, Arguments: args})
	return err != nil || result == nil || result.IsError
}

func TestFlowsGetTextModeNeedsNoSensitive(t *testing.T) {
	var mu sync.Mutex
	var last control.Command
	path := endpoint(t, func(_ context.Context, c control.Command) (any, error) {
		mu.Lock()
		last = c
		mu.Unlock()
		return map[string]any{"id": 1, "requestBody": "hello", "requestBodyEncoding": "text"}, nil
	})
	cs := connect(t, path, mcpserver.Options{}, "2025-11-25")
	result := call(t, cs, "flows.get", map[string]any{"id": 1, "body": "text", "maxBytes": 4096})
	if result.IsError {
		t.Fatalf("text body mode was refused without --allow-sensitive: %+v", result)
	}
	requireJSON(t, result, `{"id":1,"requestBody":"hello","requestBodyEncoding":"text"}`)
	mu.Lock()
	forwarded := last
	mu.Unlock()
	if forwarded.Action != "flows.get" || !strings.Contains(string(forwarded.Params), `"body":"text"`) || !strings.Contains(string(forwarded.Params), `"maxBytes":4096`) {
		t.Fatalf("forwarded params = %s", forwarded.Params)
	}
	if !rejected(t, cs, "flows.get", map[string]any{"id": 1, "details": true}) {
		t.Fatal("details:true was allowed without --allow-sensitive")
	}

	sensitive := connect(t, path, mcpserver.Options{AllowSensitive: true}, "2025-11-25")
	if rejected(t, sensitive, "flows.get", map[string]any{"id": 1, "details": true}) {
		t.Fatal("details:true was refused with --allow-sensitive")
	}

	listed, err := cs.ListTools(t.Context(), nil)
	if err != nil {
		t.Fatal(err)
	}
	for _, tool := range listed.Tools {
		if tool.Name != "flows.get" {
			continue
		}
		props, _ := tool.InputSchema.(map[string]any)["properties"].(map[string]any)
		body, _ := props["body"].(map[string]any)
		maxBytes, _ := props["maxBytes"].(map[string]any)
		if got, _ := body["enum"].([]any); !slices.Equal(got, []any{"text"}) {
			t.Fatalf("body enum = %v", body["enum"])
		}
		if maxBytes["minimum"] != 1.0 || maxBytes["maximum"] != 65536.0 || maxBytes["default"] != nil {
			t.Fatalf("maxBytes schema = %v", maxBytes)
		}
		return
	}
	t.Fatal("flows.get is missing")
}

func TestFlowErrorMetadataReachesMCP(t *testing.T) {
	path := endpoint(t, func(context.Context, control.Command) (any, error) {
		return map[string]string{"error": "live flow not found", "code": "flow-not-found", "field": "id", "hint": "list ids with flows.list"}, nil
	})
	cs := connect(t, path, mcpserver.Options{}, "2025-11-25")
	result := call(t, cs, "flows.get", map[string]any{"id": 99, "body": "text"})
	if !result.IsError {
		t.Fatal("flow error was not marked as an MCP error")
	}
	text := resultJSON(t, result)
	for _, want := range []string{"flow-not-found", "list ids with flows.list", "live flow not found"} {
		if !strings.Contains(text, want) {
			t.Fatalf("MCP flow error is missing %q: %s", want, text)
		}
	}
}

func TestAllowWriteFlowsRegistersTrafficToolsOnly(t *testing.T) {
	path := endpoint(t, func(_ context.Context, c control.Command) (any, error) {
		if c.Action == "rules.list" {
			return []map[string]any{
				{"index": 0, "key": "block-key", "type": "block"},
				{"index": 1, "key": "map-key", "type": "map_local"},
			}, nil
		}
		return map[string]any{"action": c.Action, "params": c.Params}, nil
	})
	cs := connect(t, path, mcpserver.Options{AllowWrite: mcpserver.WriteFlows}, "2025-11-25")
	listed, err := cs.ListTools(t.Context(), nil)
	if err != nil {
		t.Fatal(err)
	}
	var names []string
	for _, tool := range listed.Tools {
		names = append(names, tool.Name)
	}
	want := append(slices.Clone(queries), flowsTierWrites...)
	slices.Sort(want)
	slices.Sort(names)
	if !slices.Equal(names, want) {
		t.Fatalf("flows tier tools = %v\nwant %v", names, want)
	}
	for _, name := range []string{"settings.set", "system-proxy.set-enabled", "ssl.update", "rules.import", "rules.set-enabled", "rules.update", "sessions.open", "sessions.close", "sessions.save", "flows.clear", "token-links.apply"} {
		if slices.Contains(names, name) {
			t.Errorf("all-only tool %s was registered in the flows tier", name)
		}
	}

	// rules.create in the flows tier exposes only map_local and map_remote.
	for _, tool := range listed.Tools {
		if tool.Name != "rules.create" {
			continue
		}
		props, _ := tool.InputSchema.(map[string]any)["properties"].(map[string]any)
		rule, _ := props["rule"].(map[string]any)
		fields, _ := rule["properties"].(map[string]any)
		kind, _ := fields["type"].(map[string]any)
		if got, _ := kind["enum"].([]any); !slices.Equal(got, []any{"map_local", "map_remote"}) {
			t.Fatalf("rules.create type enum = %v", kind["enum"])
		}
	}
	if !rejected(t, cs, "rules.create", map[string]any{"rule": map[string]any{"type": "block", "match": "example.com"}}) {
		t.Fatal("flows tier created a block rule")
	}
	result := call(t, cs, "rules.create", map[string]any{"rule": map[string]any{"type": "map_local", "match": "api.example.com", "target": "/tmp/mock.json"}})
	requireJSON(t, result, `{"action":"rules.create","params":{"rule":{"type":"map_local","match":"api.example.com","target":"/tmp/mock.json"},"confirm":true}}`)

	// Deleting a non-map rule is refused before the desktop call; a map rule
	// is forwarded with its fingerprint.
	if !rejected(t, cs, "rules.delete", map[string]any{"index": 0, "key": "block-key"}) {
		t.Fatal("flows tier deleted a block rule")
	}
	result = call(t, cs, "rules.delete", map[string]any{"index": 1, "key": "map-key"})
	requireJSON(t, result, `{"action":"rules.delete","params":{"index":1,"key":"map-key","confirm":true}}`)
}

func TestAllowWriteAllStillRegistersEveryTool(t *testing.T) {
	path := endpoint(t, func(context.Context, control.Command) (any, error) { return nil, nil })
	cs := connect(t, path, mcpserver.Options{AllowWrite: mcpserver.WriteAll}, "2025-11-25")
	listed, err := cs.ListTools(t.Context(), nil)
	if err != nil {
		t.Fatal(err)
	}
	names := make([]string, 0, len(listed.Tools))
	for _, tool := range listed.Tools {
		names = append(names, tool.Name)
	}
	want := append(slices.Clone(queries), writes...)
	slices.Sort(want)
	slices.Sort(names)
	if !slices.Equal(names, want) {
		t.Fatalf("all tier tools = %v\nwant %v", names, want)
	}
}
