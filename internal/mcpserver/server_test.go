package mcpserver_test

import (
	"context"
	"encoding/json/jsontext"
	"encoding/json/v2"
	"errors"
	"fmt"
	"maps"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/baba983/vscroxy/internal/control"
	"github.com/baba983/vscroxy/internal/mcpserver"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

var queries = []string{"breakpoints.list", "collection-recording.get", "collections.list", "flows.export-har", "flows.get", "flows.list", "requests.get", "requests.list", "rules.list", "runs.get", "sessions.list", "ssl.get", "status", "system-proxy.get", "token-links.analyze", "token-links.get", "token-links.prepare"}
var writes = []string{"breakpoints.resolve", "capture.recording", "collection-recording.start", "collection-recording.stop", "collection.load", "collection.run", "flows.annotate", "flows.clear", "request.run", "rules.create", "rules.delete", "rules.import", "rules.set-enabled", "rules.update", "runs.cancel", "sessions.open", "sessions.save", "ssl.update", "system-proxy.set-enabled", "token-links.apply"}

func endpoint(t *testing.T, dispatch control.Dispatch) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "control.json")
	server, err := control.Start(path, dispatch)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := server.Close(); err != nil {
			t.Error(err)
		}
	})
	return path
}

func connect(t *testing.T, path string, options mcpserver.Options, protocol string) *mcp.ClientSession {
	t.Helper()
	return connectWith(t, path, options, protocol)
}

func connectWith(t *testing.T, path string, options mcpserver.Options, protocol string) *mcp.ClientSession {
	t.Helper()
	server, err := mcpserver.New(path, options)
	if err != nil {
		t.Fatal(err)
	}
	st, ct := mcp.NewInMemoryTransports()
	ss, err := server.Connect(t.Context(), st, nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = ss.Close() })
	client := mcp.NewClient(&mcp.Implementation{Name: "integration-test", Version: "1"}, nil)
	cs, err := client.Connect(t.Context(), ct, &mcp.ClientSessionOptions{ProtocolVersion: protocol})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = cs.Close() })
	if cs.InitializeResult() == nil || cs.InitializeResult().ServerInfo.Name != "vscroxy" {
		t.Fatalf("missing MCP initialization: %+v", cs.InitializeResult())
	}
	return cs
}

func call(t *testing.T, cs *mcp.ClientSession, name string, args any) *mcp.CallToolResult {
	t.Helper()
	result, err := cs.CallTool(t.Context(), &mcp.CallToolParams{Name: name, Arguments: args})
	if err != nil {
		t.Fatal(err)
	}
	return result
}

func resultJSON(t *testing.T, result *mcp.CallToolResult) string {
	t.Helper()
	data, err := json.Marshal(result.StructuredContent)
	if err != nil {
		t.Fatal(err)
	}
	return string(data)
}

func requireJSON(t *testing.T, result *mcp.CallToolResult, want string) {
	t.Helper()
	var gotValue, wantValue any
	if err := json.Unmarshal([]byte(resultJSON(t, result)), &gotValue); err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal([]byte(want), &wantValue); err != nil {
		t.Fatal(err)
	}
	got, _ := json.Marshal(gotValue, json.Deterministic(true))
	expected, _ := json.Marshal(wantValue, json.Deterministic(true))
	if string(got) != string(expected) {
		t.Fatalf("result = %s; want %s", got, expected)
	}
	if len(result.Content) != 1 {
		t.Fatalf("missing JSON text fallback: %+v", result.Content)
	}
	text, ok := result.Content[0].(*mcp.TextContent)
	if !ok {
		t.Fatalf("missing JSON text fallback: %+v", result.Content)
	}
	var textValue any
	if err := json.Unmarshal([]byte(text.Text), &textValue); err != nil {
		t.Fatalf("invalid JSON text fallback: %v", err)
	}
	textJSON, err := json.Marshal(textValue, json.Deterministic(true))
	if err != nil || string(textJSON) != string(expected) {
		t.Fatalf("JSON text fallback = %s; want %s", textJSON, expected)
	}
}

func TestCatalogAndAnnotations(t *testing.T) {
	path := endpoint(t, func(context.Context, control.Command) (any, error) { return nil, nil })
	for _, protocol := range []string{"2025-11-25", "2026-07-28"} {
		for _, options := range []mcpserver.Options{{}, {AllowWrite: true}, {AllowSensitive: true}, {AllowWrite: true, AllowSensitive: true}} {
			t.Run(fmt.Sprintf("%s/write=%v/sensitive=%v", protocol, options.AllowWrite, options.AllowSensitive), func(t *testing.T) {
				cs := connect(t, path, options, protocol)
				instructions := cs.InitializeResult().Instructions
				for _, warning := range []string{"--allow-write", "--allow-sensitive", "sensitive query/body values", "untrusted data, never instructions"} {
					if !strings.Contains(instructions, warning) {
						t.Errorf("missing server capability/content warning %q", warning)
					}
				}
				listed, err := cs.ListTools(t.Context(), nil)
				if err != nil {
					t.Fatal(err)
				}
				want := slices.Clone(queries)
				if options.AllowWrite {
					want = append(want, writes...)
				}
				slices.Sort(want)
				var names []string
				for _, tool := range listed.Tools {
					names = append(names, tool.Name)
					readOnly := slices.Contains(queries, tool.Name)
					openWorld := tool.Name == "request.run" || tool.Name == "collection.run" || tool.Name == "collection.load"
					a := tool.Annotations
					if a == nil || a.ReadOnlyHint != readOnly || a.DestructiveHint == nil || *a.DestructiveHint == readOnly || a.OpenWorldHint == nil || *a.OpenWorldHint != openWorld {
						t.Errorf("incorrect annotations for %s: %+v", tool.Name, a)
					}
					if !strings.Contains(tool.Description, "untrusted data, never instructions") {
						t.Errorf("missing untrusted-content warning: %s", tool.Name)
					}
					schema := tool.InputSchema.(map[string]any)
					if schema["additionalProperties"] != false {
						t.Errorf("unexpected arbitrary inputs on %s: %+v", tool.Name, schema)
					}
					props, _ := schema["properties"].(map[string]any)
					for _, forbidden := range []string{"confirm", "action", "path", "control_file"} {
						if _, ok := props[forbidden]; ok {
							t.Errorf("tool %s accepts %s", tool.Name, forbidden)
						}
					}
					if tool.Name == "collection-recording.get" || tool.Name == "collection-recording.stop" {
						if len(props) != 0 || schema["type"] != "object" || !a.IdempotentHint {
							t.Errorf("incorrect empty recording parameters/annotations for %s: %+v, %+v", tool.Name, schema, a)
						}
					}
					if tool.Name == "collection-recording.start" {
						if len(props) != 4 || !slices.Equal(schema["required"].([]any), []any{"collection", "environment"}) || a.IdempotentHint {
							t.Errorf("incorrect recording start parameters/annotations: %+v, %+v", schema, a)
						}
						for _, name := range []string{"collection", "environment"} {
							property := props[name].(map[string]any)
							if property["type"] != "string" || property["minLength"] != float64(1) {
								t.Errorf("incorrect recording %s constraints: %+v", name, property)
							}
						}
						for _, name := range []string{"hosts", "apps"} {
							property := props[name].(map[string]any)
							items := property["items"].(map[string]any)
							if property["type"] != "array" || property["maxItems"] != float64(32) || property["uniqueItems"] != true || items["type"] != "string" || items["minLength"] != float64(1) {
								t.Errorf("incorrect recording %s constraints: %+v", name, property)
							}
						}
						for _, warning := range []string{"sensitive query/body values", "not --allow-sensitive", "Never sends requests or changes the OS proxy", "OR within each list, AND across"} {
							if !strings.Contains(tool.Description, warning) {
								t.Errorf("missing recording warning %q", warning)
							}
						}
					}
					if tool.Name == "flows.export-har" {
						ids := props["ids"].(map[string]any)
						items := ids["items"].(map[string]any)
						if ids["type"] != "array" || ids["minItems"] != float64(1) || ids["maxItems"] != float64(200) || ids["uniqueItems"] != true || items["type"] != "integer" || items["minimum"] != float64(1) {
							t.Errorf("incorrect HAR ID constraints: %+v", ids)
						}
						if len(props) != 2 || props["details"].(map[string]any)["type"] != "boolean" || !slices.Equal(schema["required"].([]any), []any{"ids"}) {
							t.Errorf("incorrect HAR export parameters: %+v", schema)
						}
						if !a.IdempotentHint {
							t.Error("HAR export must be idempotent")
						}
					}
				}
				slices.Sort(names)
				if !slices.Equal(names, want) {
					t.Fatalf("tools = %v; want %v", names, want)
				}
				if !options.AllowWrite {
					for _, name := range writes {
						result, err := cs.CallTool(t.Context(), &mcp.CallToolParams{Name: name, Arguments: map[string]any{}})
						if err == nil && !result.IsError {
							t.Errorf("unregistered mutation %s succeeded", name)
						}
					}
				}
			})
		}
	}
}

func TestQueryJSONValues(t *testing.T) {
	path := endpoint(t, func(_ context.Context, c control.Command) (any, error) {
		switch c.Action {
		case "status":
			return map[string]bool{"recording": true}, nil
		case "collections.list":
			return []string{"sample"}, nil
		case "requests.get":
			return map[string]string{"error": "request not found"}, nil
		default:
			return map[string]any{"action": c.Action, "params": c.Params}, nil
		}
	})
	cs := connect(t, path, mcpserver.Options{}, "2025-11-25")
	for _, tc := range []struct {
		name string
		args any
		want string
		err  bool
	}{
		{"status", nil, `{"recording":true}`, false},
		{"collection-recording.get", nil, `{"action":"collection-recording.get","params":{}}`, false},
		{"collection-recording.get", map[string]any{}, `{"action":"collection-recording.get","params":{}}`, false},
		{"system-proxy.get", nil, `{"action":"system-proxy.get","params":{}}`, false},
		{"system-proxy.get", map[string]any{"refresh": true}, `{"action":"system-proxy.get","params":{"refresh":true}}`, false},
		{"collections.list", nil, `["sample"]`, false},
		{"requests.list", nil, `{"action":"requests.list","params":{}}`, false},
		{"requests.list", map[string]any{"collection": ""}, `{"action":"requests.list","params":{"collection":""}}`, false},
		{"requests.list", map[string]any{"collection": "sample"}, `{"action":"requests.list","params":{"collection":"sample"}}`, false},
		{"flows.list", nil, `{"action":"flows.list","params":{"limit":50}}`, false},
		{"flows.list", map[string]any{"limit": 200, "before": 80}, `{"action":"flows.list","params":{"limit":200,"before":80}}`, false},
		{"flows.get", map[string]any{"id": 7}, `{"action":"flows.get","params":{"id":7}}`, false},
		{"flows.export-har", map[string]any{"ids": []int{7, 2}}, `{"action":"flows.export-har","params":{"ids":[7,2]}}`, false},
		{"flows.export-har", map[string]any{"ids": []int{7}, "details": false}, `{"action":"flows.export-har","params":{"ids":[7]}}`, false},
		{"rules.list", nil, `{"action":"rules.list","params":{}}`, false},
		{"runs.get", map[string]any{"id": "job-id"}, `{"action":"runs.get","params":{"id":"job-id"}}`, false},
		{"requests.get", map[string]any{"id": "missing"}, `{"error":"request not found"}`, true},
	} {
		t.Run(tc.name+tc.want, func(t *testing.T) {
			result := call(t, cs, tc.name, tc.args)
			if result.IsError != tc.err {
				t.Fatalf("IsError=%v; want %v: %+v", result.IsError, tc.err, result)
			}
			requireJSON(t, result, tc.want)
		})
	}
}

func TestSensitiveOptIn(t *testing.T) {
	var mu sync.Mutex
	disclosures := 0
	path := endpoint(t, func(_ context.Context, c control.Command) (any, error) {
		var p struct {
			Details bool `json:"details"`
		}
		if err := json.Unmarshal(c.Params, &p); err != nil {
			return nil, err
		}
		if p.Details {
			mu.Lock()
			disclosures++
			mu.Unlock()
			return map[string]string{"raw": "sensitive-saved-content"}, nil
		}
		return map[string]string{"summary": "safe"}, nil
	})
	for _, write := range []bool{false, true} {
		for _, sensitive := range []bool{false, true} {
			cs := connect(t, path, mcpserver.Options{AllowWrite: write, AllowSensitive: sensitive}, "2025-11-25")
			for _, name := range []string{"requests.get", "flows.get", "flows.export-har"} {
				var id any = "request-id"
				if name == "flows.get" {
					id = 1
				}
				for _, details := range []bool{false, true} {
					args := map[string]any{"id": id, "details": details}
					if name == "flows.export-har" {
						delete(args, "id")
						args["ids"] = []int{1}
					}
					result := call(t, cs, name, args)
					if details && !sensitive {
						if !result.IsError || strings.Contains(resultJSON(t, result), "sensitive-saved-content") {
							t.Fatalf("sensitive access not blocked: %+v", result)
						}
					} else if details {
						requireJSON(t, result, `{"raw":"sensitive-saved-content"}`)
					} else {
						requireJSON(t, result, `{"summary":"safe"}`)
					}
				}
			}
		}
	}
	mu.Lock()
	defer mu.Unlock()
	if disclosures != 6 {
		t.Fatalf("desktop saw %d sensitive requests; want only six explicitly authorized disclosures", disclosures)
	}
}

func TestHARExportReturnsDirectJSON(t *testing.T) {
	safeHAR := `{"log":{"version":"1.2","creator":{"name":"test","version":"1"},"entries":[]}}`
	rawHAR := `{"log":{"version":"1.2","creator":{"name":"test","version":"1"},"entries":[],"comment":"raw-secret"}}`
	path := endpoint(t, func(_ context.Context, c control.Command) (any, error) {
		var p struct {
			IDs     []int `json:"ids"`
			Details bool  `json:"details"`
		}
		if c.Action != "flows.export-har" || json.Unmarshal(c.Params, &p, json.RejectUnknownMembers(true)) != nil {
			return nil, errors.New("unexpected export action or parameters")
		}
		if slices.Equal(p.IDs, []int{999}) {
			return map[string]string{"error": "flow not found"}, nil
		}
		if !slices.Equal(p.IDs, []int{7, 2}) && len(p.IDs) != 200 {
			return nil, errors.New("unexpected export IDs")
		}
		if p.Details {
			return jsontext.Value(rawHAR), nil
		}
		return jsontext.Value(safeHAR), nil
	})
	ids := make([]int, 200)
	for i := range ids {
		ids[i] = i + 1
	}
	for _, protocol := range []string{"2025-11-25", "2026-07-28"} {
		cs := connect(t, path, mcpserver.Options{AllowSensitive: true}, protocol)
		for _, tc := range []struct {
			args map[string]any
			want string
			err  bool
		}{
			{map[string]any{"ids": []int{7, 2}}, safeHAR, false},
			{map[string]any{"ids": []int{7, 2}, "details": false}, safeHAR, false},
			{map[string]any{"ids": []int{7, 2}, "details": true}, rawHAR, false},
			{map[string]any{"ids": ids}, safeHAR, false},
			{map[string]any{"ids": []int{999}}, `{"error":"flow not found"}`, true},
		} {
			result := call(t, cs, "flows.export-har", tc.args)
			if result.IsError != tc.err {
				t.Fatalf("HAR export IsError=%v; want %v: %+v", result.IsError, tc.err, result)
			}
			requireJSON(t, result, tc.want)
		}
	}
}

func TestMutationsAndConfirmation(t *testing.T) {
	var mu sync.Mutex
	recording := true
	path := endpoint(t, func(_ context.Context, c control.Command) (any, error) {
		mu.Lock()
		defer mu.Unlock()
		if c.Action == "status" {
			return map[string]bool{"recording": recording}, nil
		}
		var p struct {
			Confirm bool `json:"confirm"`
			Enabled bool `json:"enabled"`
		}
		if err := json.Unmarshal(c.Params, &p); err != nil {
			return nil, err
		}
		if !p.Confirm {
			return map[string]string{"error": "confirmation required"}, nil
		}
		if c.Action == "capture.recording" {
			recording = p.Enabled
			return map[string]bool{"recording": recording}, nil
		}
		return map[string]any{"action": c.Action, "params": c.Params}, nil
	})
	cs := connect(t, path, mcpserver.Options{AllowWrite: true}, "2025-11-25")
	requireJSON(t, call(t, cs, "status", nil), `{"recording":true}`)
	changed := call(t, cs, "capture.recording", map[string]any{"enabled": false})
	if changed.IsError {
		t.Fatalf("mutation failed: %+v", changed)
	}
	requireJSON(t, call(t, cs, "status", nil), `{"recording":false}`)
	for _, tc := range []struct {
		name string
		args any
		want string
	}{
		{"request.run", map[string]any{"id": "request-id"}, `{"action":"request.run","params":{"id":"request-id","iterations":1,"confirm":true}}`},
		{"collection.run", map[string]any{"collection": ""}, `{"action":"collection.run","params":{"collection":"","iterations":1,"confirm":true}}`},
		{"collection.run", map[string]any{"collection": "sample", "environment": "staging", "iterations": 1000, "bail": true}, `{"action":"collection.run","params":{"collection":"sample","environment":"staging","iterations":1000,"bail":true,"confirm":true}}`},
		{"collection.load", map[string]any{"collection": "", "users": 1, "durationSeconds": 1}, `{"action":"collection.load","params":{"collection":"","users":1,"durationSeconds":1,"requestTimeoutSeconds":30,"maxAttempts":100000,"confirm":true}}`},
		{"collection.load", map[string]any{"collection": "sample", "users": 64, "durationSeconds": 600, "requestTimeoutSeconds": 60, "maxAttempts": 1000000, "bail": true}, `{"action":"collection.load","params":{"collection":"sample","users":64,"durationSeconds":600,"requestTimeoutSeconds":60,"maxAttempts":1000000,"bail":true,"confirm":true}}`},
		{"runs.cancel", map[string]any{"id": "job-id"}, `{"action":"runs.cancel","params":{"id":"job-id","confirm":true}}`},
		{"rules.set-enabled", map[string]any{"index": 0, "key": "current-fingerprint", "enabled": false}, `{"action":"rules.set-enabled","params":{"index":0,"key":"current-fingerprint","enabled":false,"confirm":true}}`},
		{"rules.import", map[string]any{"document": `[{"type":"block","match":"example.com"}]`}, `{"action":"rules.import","params":{"document":"[{\"type\":\"block\",\"match\":\"example.com\"}]","confirm":true}}`},
		{"system-proxy.set-enabled", map[string]any{"enabled": false}, `{"action":"system-proxy.set-enabled","params":{"enabled":false,"confirm":true}}`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			result := call(t, cs, tc.name, tc.args)
			if result.IsError {
				t.Fatalf("mutation failed: %+v", result)
			}
			requireJSON(t, result, tc.want)
		})
	}
}

func TestCollectionRecordingForwarding(t *testing.T) {
	path := endpoint(t, func(_ context.Context, c control.Command) (any, error) {
		return map[string]any{"action": c.Action, "params": c.Params}, nil
	})
	for _, protocol := range []string{"2025-11-25", "2026-07-28"} {
		cs := connect(t, path, mcpserver.Options{AllowWrite: true}, protocol)
		for _, tc := range []struct {
			name string
			args string
			want string
		}{
			{"collection-recording.get", `{}`, `{"action":"collection-recording.get","params":{}}`},
			{"collection-recording.start", `{"collection":"sample/subfolder","environment":"local"}`, `{"action":"collection-recording.start","params":{"collection":"sample/subfolder","environment":"local","confirm":true}}`},
			{"collection-recording.start", `{"collection":"sample","environment":"local","hosts":["api.example.com","localhost:8080","[::1]:8080"],"apps":["Firefox","Google Chrome"]}`, `{"action":"collection-recording.start","params":{"collection":"sample","environment":"local","hosts":["api.example.com","localhost:8080","[::1]:8080"],"apps":["Firefox","Google Chrome"],"confirm":true}}`},
			{"collection-recording.start", `{"collection":"sample","environment":"local","hosts":[],"apps":[]}`, `{"action":"collection-recording.start","params":{"collection":"sample","environment":"local","confirm":true}}`},
			{"collection-recording.stop", `{}`, `{"action":"collection-recording.stop","params":{"confirm":true}}`},
		} {
			t.Run(protocol+"/"+tc.name+"/"+tc.args, func(t *testing.T) {
				result := call(t, cs, tc.name, jsontext.Value(tc.args))
				if result.IsError {
					t.Fatalf("recording failed without --allow-sensitive: %+v", result)
				}
				requireJSON(t, result, tc.want)
			})
		}
		requireJSON(t, call(t, cs, "collection-recording.stop", nil), `{"action":"collection-recording.stop","params":{"confirm":true}}`)
		hosts, apps := make([]string, 32), make([]string, 32)
		for i := range hosts {
			hosts[i], apps[i] = fmt.Sprintf("host%d.example", i), fmt.Sprintf("App %d", i)
		}
		args := map[string]any{"collection": "sample", "environment": "local", "hosts": hosts, "apps": apps}
		result := call(t, cs, "collection-recording.start", args)
		if result.IsError {
			t.Fatalf("32 unique filters rejected: %+v", result)
		}
		args["confirm"] = true
		want, err := json.Marshal(map[string]any{"action": "collection-recording.start", "params": args})
		if err != nil {
			t.Fatal(err)
		}
		requireJSON(t, result, string(want))
	}
}

func TestCollectionRecordingWriteRequired(t *testing.T) {
	var mu sync.Mutex
	calls := 0
	path := endpoint(t, func(context.Context, control.Command) (any, error) {
		mu.Lock()
		calls++
		mu.Unlock()
		return map[string]bool{"unexpected": true}, nil
	})
	for _, sensitive := range []bool{false, true} {
		cs := connect(t, path, mcpserver.Options{AllowSensitive: sensitive}, "2025-11-25")
		for _, tc := range []struct {
			name string
			args any
		}{
			{"collection-recording.start", map[string]any{"collection": "sample", "environment": "local"}},
			{"collection-recording.stop", map[string]any{}},
		} {
			result, err := cs.CallTool(t.Context(), &mcp.CallToolParams{Name: tc.name, Arguments: tc.args})
			if err == nil && !result.IsError {
				t.Errorf("recording mutation succeeded without --allow-write: %s", tc.name)
			}
		}
	}
	mu.Lock()
	defer mu.Unlock()
	if calls != 0 {
		t.Fatalf("%d unauthorized recording calls reached desktop", calls)
	}
}

func TestCollectionRecordingInvalidArgumentsNeverReachDesktop(t *testing.T) {
	var mu sync.Mutex
	calls := 0
	path := endpoint(t, func(context.Context, control.Command) (any, error) {
		mu.Lock()
		calls++
		mu.Unlock()
		return map[string]bool{"unexpected": true}, nil
	})
	tooMany := make([]string, 33)
	for i := range tooMany {
		tooMany[i] = fmt.Sprintf("host%d.example", i)
	}
	for _, protocol := range []string{"2025-11-25", "2026-07-28"} {
		cs := connect(t, path, mcpserver.Options{AllowWrite: true}, protocol)
		for _, fixture := range []string{
			`{}`,
			`{"collection":"sample"}`,
			`{"environment":"local"}`,
			`{"collection":"","environment":"local"}`,
			`{"collection":null,"environment":"local"}`,
			`{"collection":1,"environment":"local"}`,
			`{"collection":"sample","environment":""}`,
			`{"collection":"sample","environment":null}`,
			`{"collection":"sample","environment":false}`,
			`{"collection":"sample","environment":"local","confirm":true}`,
			`{"collection":"sample","environment":"local","details":true}`,
			`{"collection":"sample","environment":"local","path":"/tmp/destination"}`,
		} {
			t.Run(protocol+"/start/"+fixture, func(t *testing.T) {
				if result := call(t, cs, "collection-recording.start", jsontext.Value(fixture)); !result.IsError {
					t.Fatalf("invalid start fixture accepted: %s", fixture)
				}
			})
		}
		for _, name := range []string{"hosts", "apps"} {
			for _, invalid := range []any{nil, "example.com", 1, []string{""}, []string{"same", "same"}, []any{nil}, []any{1}, tooMany} {
				args := map[string]any{"collection": "sample", "environment": "local", name: invalid}
				if result := call(t, cs, "collection-recording.start", args); !result.IsError {
					t.Errorf("invalid %s accepted: %+v", name, invalid)
				}
			}
		}
		for _, host := range []string{"https://example.com", "*.example.com", "example.com/path", "user@example.com", "example.com?query", "example.com#fragment", "two hosts", "example.com\n"} {
			args := map[string]any{"collection": "sample", "environment": "local", "hosts": []string{host}}
			if result := call(t, cs, "collection-recording.start", args); !result.IsError {
				t.Errorf("invalid hostname syntax accepted: %q", host)
			}
		}
		for _, name := range []string{"collection-recording.get", "collection-recording.stop"} {
			for _, fixture := range []string{`{"confirm":true}`, `{"collection":"sample"}`, `{"environment":"local"}`, `{"hosts":[]}`, `{"apps":[]}`, `{"details":true}`, `{"action":"status"}`} {
				if result := call(t, cs, name, jsontext.Value(fixture)); !result.IsError {
					t.Errorf("closed %s schema accepted %s", name, fixture)
				}
			}
		}
	}
	mu.Lock()
	defer mu.Unlock()
	if calls != 0 {
		t.Fatalf("%d invalid recording calls reached desktop", calls)
	}
}

func TestInvalidArgumentsNeverReachDesktop(t *testing.T) {
	var mu sync.Mutex
	calls := 0
	path := endpoint(t, func(context.Context, control.Command) (any, error) {
		mu.Lock()
		calls++
		mu.Unlock()
		return map[string]bool{"unexpected": true}, nil
	})
	cs := connect(t, path, mcpserver.Options{AllowWrite: true, AllowSensitive: true}, "2025-11-25")
	tooMany := make([]int, 201)
	for i := range tooMany {
		tooMany[i] = i + 1
	}
	for _, tc := range []struct {
		name string
		args any
	}{
		{"requests.get", map[string]any{}},
		{"requests.get", map[string]any{"id": ""}},
		{"requests.get", map[string]any{"id": 1}},
		{"flows.get", map[string]any{"id": -1}},
		{"flows.get", map[string]any{"id": 0}},
		{"flows.get", map[string]any{"id": 1.5}},
		{"flows.export-har", map[string]any{}},
		{"flows.export-har", map[string]any{"ids": nil}},
		{"flows.export-har", map[string]any{"ids": []int{}}},
		{"flows.export-har", map[string]any{"ids": tooMany}},
		{"flows.export-har", map[string]any{"ids": []int{1, 1}}},
		{"flows.export-har", map[string]any{"ids": []int{0}}},
		{"flows.export-har", map[string]any{"ids": []int{-1}}},
		{"flows.export-har", map[string]any{"ids": []float64{1.5}}},
		{"flows.export-har", map[string]any{"ids": []string{"1"}}},
		{"flows.export-har", map[string]any{"ids": []any{nil}}},
		{"flows.export-har", map[string]any{"ids": 1}},
		{"flows.export-har", map[string]any{"ids": []int{1}, "details": "true"}},
		{"flows.export-har", map[string]any{"ids": []int{1}, "details": nil}},
		{"flows.export-har", map[string]any{"ids": []int{1}, "output": "/private/file"}},
		{"flows.export-har", map[string]any{"ids": []int{1}, "confirm": true}},
		{"requests.get", map[string]any{"id": "x", "details": "true"}},
		{"flows.list", map[string]any{"limit": 0}},
		{"flows.list", map[string]any{"limit": 201}},
		{"request.run", map[string]any{"id": "x", "iterations": 0}},
		{"collection.run", map[string]any{"collection": "x", "iterations": 1001}},
		{"collection.load", map[string]any{"users": 1, "durationSeconds": 1}},
		{"collection.load", map[string]any{"collection": "x", "durationSeconds": 1}},
		{"collection.load", map[string]any{"collection": "x", "users": 0, "durationSeconds": 1}},
		{"collection.load", map[string]any{"collection": "x", "users": 65, "durationSeconds": 1}},
		{"collection.load", map[string]any{"collection": "x", "users": 1, "durationSeconds": 0}},
		{"collection.load", map[string]any{"collection": "x", "users": 1, "durationSeconds": 601}},
		{"collection.load", map[string]any{"collection": "x", "users": 1.5, "durationSeconds": 1}},
		{"collection.load", map[string]any{"collection": "x", "users": 1, "durationSeconds": 1, "maxAttempts": 0}},
		{"collection.load", map[string]any{"collection": "x", "users": 1, "durationSeconds": 1, "maxAttempts": 1000001}},
		{"collection.load", map[string]any{"collection": "x", "users": 1, "durationSeconds": 1, "requestTimeoutSeconds": 0}},
		{"collection.load", map[string]any{"collection": "x", "users": 1, "durationSeconds": 1, "requestTimeoutSeconds": 61}},
		{"collection.load", map[string]any{"collection": "x", "users": 1, "durationSeconds": 1, "confirm": true}},
		{"rules.set-enabled", map[string]any{"index": -1, "key": "x", "enabled": true}},
		{"rules.set-enabled", map[string]any{"index": 0, "enabled": true}},
		{"rules.set-enabled", map[string]any{"index": 0, "key": "", "enabled": true}},
		{"rules.import", map[string]any{}},
		{"rules.import", map[string]any{"document": ""}},
		{"rules.import", map[string]any{"document": []any{}}},
		{"rules.import", map[string]any{"document": "[]", "confirm": true}},
		{"capture.recording", map[string]any{}},
		{"capture.recording", map[string]any{"enabled": false, "confirm": true}},
		{"system-proxy.get", map[string]any{"refresh": "true"}},
		{"system-proxy.get", map[string]any{"target": "127.0.0.1:8888"}},
		{"system-proxy.set-enabled", map[string]any{}},
		{"system-proxy.set-enabled", map[string]any{"enabled": nil}},
		{"system-proxy.set-enabled", map[string]any{"enabled": true, "target": "proxy.example"}},
		{"status", map[string]any{"action": "request.run"}},
		{"requests.list", map[string]any{"collection": nil}},
		{"requests.get", map[string]any{"id": "x", "path": "/private/file"}},
	} {
		result := call(t, cs, tc.name, tc.args)
		if !result.IsError {
			t.Errorf("invalid arguments accepted for %s: %+v", tc.name, tc.args)
		}
	}
	mu.Lock()
	defer mu.Unlock()
	if calls != 0 {
		t.Fatalf("%d invalid calls reached desktop", calls)
	}
}

func TestSanitizedBackendFailure(t *testing.T) {
	path := endpoint(t, func(context.Context, control.Command) (any, error) {
		return nil, errors.New("private-backend-exception-and-token")
	})
	cs := connect(t, path, mcpserver.Options{}, "2025-11-25")
	for _, tc := range []struct {
		name string
		args any
	}{
		{"status", nil},
		{"flows.export-har", map[string]any{"ids": []int{1}}},
	} {
		result := call(t, cs, tc.name, tc.args)
		data, err := json.Marshal(result)
		if err != nil {
			t.Fatal(err)
		}
		if !result.IsError || strings.Contains(string(data), "private-backend") || !strings.Contains(string(data), "dispatch failed") {
			t.Fatalf("unsanitized or missing error: %s", data)
		}
	}
}

func TestCancellationReachesControl(t *testing.T) {
	for _, protocol := range []string{"2025-11-25", "2026-07-28"} {
		t.Run(protocol, func(t *testing.T) {
			started := make(chan struct{})
			canceled := make(chan struct{})
			path := endpoint(t, func(ctx context.Context, _ control.Command) (any, error) {
				close(started)
				<-ctx.Done()
				close(canceled)
				return nil, ctx.Err()
			})
			cs := connect(t, path, mcpserver.Options{}, protocol)
			ctx, cancel := context.WithCancel(t.Context())
			defer cancel()
			finished := make(chan error, 1)
			go func() {
				_, err := cs.CallTool(ctx, &mcp.CallToolParams{Name: "status"})
				finished <- err
			}()
			select {
			case <-started:
			case <-time.After(3 * time.Second):
				t.Fatal("desktop never started dispatch")
			}
			cancel()
			select {
			case err := <-finished:
				if !errors.Is(err, context.Canceled) {
					t.Fatalf("call returned %v; want cancellation", err)
				}
			case <-time.After(3 * time.Second):
				t.Fatal("MCP call did not cancel")
			}
			select {
			case <-canceled:
			case <-time.After(3 * time.Second):
				t.Fatal("MCP cancellation did not reach authenticated control dispatch")
			}
		})
	}
}

func TestDiscoveryRequired(t *testing.T) {
	if _, err := mcpserver.New(filepath.Join(t.TempDir(), "missing"), mcpserver.Options{}); err == nil {
		t.Fatal("missing discovery accepted")
	}
}

func TestTokenLinksSchemasAndWarnings(t *testing.T) {
	path := endpoint(t, func(context.Context, control.Command) (any, error) { return nil, nil })
	for _, protocol := range []string{"2025-11-25", "2026-07-28"} {
		cs := connect(t, path, mcpserver.Options{AllowWrite: true}, protocol)
		listed, err := cs.ListTools(t.Context(), nil)
		if err != nil {
			t.Fatal(err)
		}
		for _, tool := range listed.Tools {
			if !strings.HasPrefix(tool.Name, "token-links.") {
				continue
			}
			schema := tool.InputSchema.(map[string]any)
			props := schema["properties"].(map[string]any)
			var required []any
			switch tool.Name {
			case "token-links.analyze":
				required = []any{"ids"}
				ids := props["ids"].(map[string]any)
				items := ids["items"].(map[string]any)
				if ids["type"] != "array" || ids["minItems"] != float64(1) || ids["maxItems"] != float64(200) || ids["uniqueItems"] != true || items["type"] != "integer" || items["minimum"] != float64(1) {
					t.Errorf("incorrect token analysis constraints: %+v", ids)
				}
				for _, warning := range []string{"asynchronous", "imported snapshots", "same-origin", "ambiguous", "token/access_token/id_token", "Do not blindly retry"} {
					if !strings.Contains(tool.Description, warning) {
						t.Errorf("missing analysis warning %q", warning)
					}
				}
			case "token-links.get":
				required = []any{"id"}
				if !strings.Contains(tool.Description, "never token values") || !strings.Contains(tool.Description, "JSON path segments") {
					t.Errorf("missing redacted polling contract: %s", tool.Description)
				}
			case "token-links.prepare":
				required = []any{"id", "index", "collection", "environment", "variable"}
				index := props["index"].(map[string]any)
				variable := props["variable"].(map[string]any)
				collection := props["collection"].(map[string]any)
				environment := props["environment"].(map[string]any)
				if index["type"] != "integer" || index["minimum"] != float64(0) || index["maximum"] != float64(199) || variable["type"] != "string" || variable["pattern"] != `^[A-Za-z_][A-Za-z0-9_]*$` || variable["maxLength"] != float64(64) || collection["type"] != "string" || collection["minLength"] != nil || environment["type"] != "string" || environment["minLength"] != float64(1) {
					t.Errorf("incorrect prepare constraints: %+v", props)
				}
				for _, warning := range []string{"only generated fragments", "never authored content or credentials", "sourcePre/sourcePost/consumerPre", "no scripts executed, files written or network calls", "Do not blindly retry"} {
					if !strings.Contains(tool.Description, warning) {
						t.Errorf("missing prepare warning %q", warning)
					}
				}
			case "token-links.apply":
				required = []any{"key"}
				for _, warning := range []string{"preserving authored scripts", "empty private Current", "plaintext, not Vault", "run login first", "dirty drafts", "stale targets", "variable conflicts", "active collection recording", "No network calls", "not --allow-sensitive", "do not blindly retry"} {
					if !strings.Contains(tool.Description, warning) {
						t.Errorf("missing apply warning %q", warning)
					}
				}
			}
			if len(props) != len(required) || !slices.Equal(schema["required"].([]any), required) || schema["additionalProperties"] != false {
				t.Errorf("incorrect closed schema for %s: %+v", tool.Name, schema)
			}
			for _, name := range []string{"id", "key"} {
				if property, ok := props[name].(map[string]any); ok && (property["type"] != "string" || property["minLength"] != float64(1)) {
					t.Errorf("incorrect %s constraint for %s: %+v", name, tool.Name, property)
				}
			}
			if tool.Annotations.IdempotentHint != (tool.Name == "token-links.get") {
				t.Errorf("incorrect token-link idempotency for %s: %+v", tool.Name, tool.Annotations)
			}
			for _, warning := range []string{"replaced", "closed", "restarted", "expired"} {
				if !strings.Contains(strings.ToLower(tool.Description), warning) {
					t.Errorf("missing lifecycle warning %q on %s", warning, tool.Name)
				}
			}
		}
	}
}

func TestTokenLinksForwardingWithoutSensitive(t *testing.T) {
	path := endpoint(t, func(_ context.Context, c control.Command) (any, error) {
		return map[string]any{"action": c.Action, "params": c.Params}, nil
	})
	ids := make([]int, 200)
	for i := range ids {
		ids[i] = i + 1
	}
	for _, protocol := range []string{"2025-11-25", "2026-07-28"} {
		for _, write := range []bool{false, true} {
			cs := connect(t, path, mcpserver.Options{AllowWrite: write}, protocol)
			for _, tc := range []struct {
				name string
				args map[string]any
			}{
				{"token-links.analyze", map[string]any{"ids": []int{7, 2}}},
				{"token-links.analyze", map[string]any{"ids": ids}},
				{"token-links.get", map[string]any{"id": "analysis-id"}},
				{"token-links.prepare", map[string]any{"id": "analysis-id", "index": 0, "collection": "", "environment": "local", "variable": "_accessToken1"}},
				{"token-links.prepare", map[string]any{"id": "analysis-id", "index": 199, "collection": "sample/subfolder", "environment": "local", "variable": strings.Repeat("A", 64)}},
				{"token-links.apply", map[string]any{"key": "proposal-id"}},
			} {
				if tc.name == "token-links.apply" && !write {
					continue
				}
				result := call(t, cs, tc.name, tc.args)
				if result.IsError {
					t.Fatalf("%s failed without --allow-sensitive: %+v", tc.name, result)
				}
				if tc.name == "token-links.apply" {
					tc.args["confirm"] = true
				}
				want, err := json.Marshal(map[string]any{"action": tc.name, "params": tc.args})
				if err != nil {
					t.Fatal(err)
				}
				requireJSON(t, result, string(want))
			}
		}
	}
}

func TestTokenLinksInvalidArgumentsAndDefaultDenial(t *testing.T) {
	var mu sync.Mutex
	calls := 0
	path := endpoint(t, func(context.Context, control.Command) (any, error) {
		mu.Lock()
		calls++
		mu.Unlock()
		return map[string]bool{"unexpected": true}, nil
	})
	tooMany := make([]int, 201)
	for i := range tooMany {
		tooMany[i] = i + 1
	}
	for _, protocol := range []string{"2025-11-25", "2026-07-28"} {
		for _, sensitive := range []bool{false, true} {
			cs := connect(t, path, mcpserver.Options{AllowSensitive: sensitive}, protocol)
			result, err := cs.CallTool(t.Context(), &mcp.CallToolParams{Name: "token-links.apply", Arguments: map[string]any{"key": "proposal-id"}})
			if err == nil && !result.IsError {
				t.Fatal("token-links.apply available without --allow-write")
			}
		}
		cs := connect(t, path, mcpserver.Options{AllowWrite: true}, protocol)
		for _, invalid := range []any{nil, []int{}, tooMany, []int{1, 1}, []int{0}, []int{-1}, []float64{1.5}, []string{"1"}, []any{nil}, 1, "1"} {
			if result := call(t, cs, "token-links.analyze", map[string]any{"ids": invalid}); !result.IsError {
				t.Errorf("invalid analysis IDs accepted: %+v", invalid)
			}
		}
		for _, tc := range []struct {
			name string
			args map[string]any
		}{
			{"token-links.analyze", map[string]any{"ids": []int{1}}},
			{"token-links.get", map[string]any{"id": "analysis-id"}},
			{"token-links.prepare", map[string]any{"id": "analysis-id", "index": 0, "collection": "", "environment": "local", "variable": "token"}},
			{"token-links.apply", map[string]any{"key": "proposal-id"}},
		} {
			for key := range tc.args {
				for _, invalid := range []any{nil, true, []any{}} {
					args := maps.Clone(tc.args)
					args[key] = invalid
					if result := call(t, cs, tc.name, args); !result.IsError {
						t.Errorf("%s accepted invalid %s: %+v", tc.name, key, invalid)
					}
				}
				args := maps.Clone(tc.args)
				delete(args, key)
				if result := call(t, cs, tc.name, args); !result.IsError {
					t.Errorf("%s accepted missing %s", tc.name, key)
				}
			}
			for _, extra := range []string{"confirm", "details", "action", "path", "script"} {
				args := maps.Clone(tc.args)
				args[extra] = true
				if result := call(t, cs, tc.name, args); !result.IsError {
					t.Errorf("%s accepted extra %s", tc.name, extra)
				}
			}
		}
		for _, tc := range []struct {
			name string
			args any
		}{
			{"token-links.get", map[string]any{"id": ""}},
			{"token-links.get", map[string]any{"id": 1}},
			{"token-links.apply", map[string]any{"key": ""}},
			{"token-links.apply", map[string]any{"key": 1}},
		} {
			if result := call(t, cs, tc.name, tc.args); !result.IsError {
				t.Errorf("%s accepted invalid input: %+v", tc.name, tc.args)
			}
		}
		for key, invalids := range map[string][]any{
			"id":          {"", 1},
			"index":       {-1, 200, 0.5, "0"},
			"collection":  {1},
			"environment": {"", 1},
			"variable":    {"", "1token", "with-dash", "with.dot", "two words", "tökén", "token\n", strings.Repeat("a", 65), 1},
		} {
			for _, invalid := range invalids {
				args := map[string]any{"id": "analysis-id", "index": 0, "collection": "", "environment": "local", "variable": "token"}
				args[key] = invalid
				if result := call(t, cs, "token-links.prepare", args); !result.IsError {
					t.Errorf("prepare accepted invalid %s: %+v", key, invalid)
				}
			}
		}
	}
	mu.Lock()
	defer mu.Unlock()
	if calls != 0 {
		t.Fatalf("%d invalid or unauthorized token-link calls reached desktop", calls)
	}
}

func TestTokenLinksBackendResultsForwardedOnce(t *testing.T) {
	for _, protocol := range []string{"2025-11-25", "2026-07-28"} {
		for _, tc := range []struct {
			name string
			args any
			want string
			err  bool
		}{
			{"token-links.analyze", map[string]any{"ids": []int{7, 9}}, `{"id":"analysis-id","done":false}`, false},
			{"token-links.get", map[string]any{"id": "analysis-id"}, `{"id":"analysis-id","done":false,"links":[],"skipped":0,"problem":""}`, false},
			{"token-links.get", map[string]any{"id": "analysis-id"}, `{"id":"analysis-id","done":true,"links":[{"source":7,"path":["data","access_token"],"consumers":[9]}],"skipped":1,"problem":""}`, false},
			{"token-links.prepare", map[string]any{"id": "analysis-id", "index": 0, "collection": "", "environment": "local", "variable": "token"}, `{"id":"proposal-id","source":"saved-login","consumers":["saved-api"],"environment":"local","variable":"token","sourcePre":"/* generated pre */","sourcePost":"/* generated post */","consumerPre":"/* generated consumer */"}`, false},
			{"token-links.apply", map[string]any{"key": "proposal-id"}, `{"applied":true}`, false},
			{"token-links.get", map[string]any{"id": "old-analysis"}, `{"error":"token analysis expired"}`, true},
			{"token-links.prepare", map[string]any{"id": "old-analysis", "index": 0, "collection": "", "environment": "local", "variable": "token"}, `{"error":"token analysis replaced"}`, true},
			{"token-links.apply", map[string]any{"key": "old-proposal"}, `{"error":"token proposal is stale"}`, true},
		} {
			t.Run(protocol+"/"+tc.name+"/"+tc.want, func(t *testing.T) {
				var mu sync.Mutex
				calls := 0
				path := endpoint(t, func(_ context.Context, c control.Command) (any, error) {
					mu.Lock()
					defer mu.Unlock()
					calls++
					if c.Action != tc.name {
						return nil, errors.New("unexpected token-link action")
					}
					return jsontext.Value(tc.want), nil
				})
				cs := connect(t, path, mcpserver.Options{AllowWrite: true}, protocol)
				result := call(t, cs, tc.name, tc.args)
				if result.IsError != tc.err {
					t.Fatalf("IsError=%v; want %v: %+v", result.IsError, tc.err, result)
				}
				requireJSON(t, result, tc.want)
				mu.Lock()
				defer mu.Unlock()
				if calls != 1 {
					t.Fatalf("%s forwarded %d times; want exactly once, including on failure", tc.name, calls)
				}
			})
		}
	}
}

func TestDisableToolsAndTypedRuleForwarding(t *testing.T) {
	path := endpoint(t, func(_ context.Context, c control.Command) (any, error) {
		return map[string]any{"action": c.Action, "params": c.Params}, nil
	})
	if _, err := mcpserver.New(path, mcpserver.Options{AllowWrite: true, Disable: []string{"no.such.tool"}}); err == nil {
		t.Fatal("unknown disabled tool was accepted")
	}
	if _, err := mcpserver.New(path, mcpserver.Options{Disable: []string{"request.run"}}); err == nil {
		t.Fatal("a write tool was accepted as disabled without --allow-write")
	}
	cs := connectWith(t, path, mcpserver.Options{AllowWrite: true, Disable: []string{"request.run", "flows.list"}}, "2025-11-25")
	listed, err := cs.ListTools(t.Context(), nil)
	if err != nil {
		t.Fatal(err)
	}
	for _, tool := range listed.Tools {
		if tool.Name == "request.run" || tool.Name == "flows.list" {
			t.Fatalf("disabled tool %s is still offered", tool.Name)
		}
	}
	requireJSON(t, call(t, cs, "rules.create", map[string]any{"rule": map[string]any{"type": "block", "match": "example.com"}}),
		`{"action":"rules.create","params":{"rule":{"type":"block","match":"example.com"},"confirm":true}}`)
}
