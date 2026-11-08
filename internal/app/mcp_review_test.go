package app

import (
	"bytes"
	"context"
	"fmt"
	"image"
	"image/color"
	"image/png"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/baba983/vscroxy/internal/request"
	"github.com/egoist/mygo/ui"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

func TestMCPConsentKeysTrackShapeAndValues(t *testing.T) {
	d := &requestDocument{debugKey: "doc-1"}
	key := []byte("0123456789abcdef0123456789abcdef")
	plan := request.MCPPlan{Command: "npx", Args: []string{"-y", "server"}, WorkingDir: "/tmp", Env: []string{"TOKEN=one", "DEBUG=1"}}
	shape := mcpConsentKey(d, plan)
	full := mcpConsentFullKey(shape, plan, key)
	same := plan
	same.Env = []string{"TOKEN=two", "DEBUG=2"}
	if got := mcpConsentKey(d, same); got != shape {
		t.Fatal("an environment value change changed the shape key")
	}
	if got := mcpConsentFullKey(shape, same, key); got == full {
		t.Fatal("an environment value change reused the full approval")
	}
	changed := plan
	changed.Args = []string{"-y", "other-server"}
	if got := mcpConsentKey(d, changed); got == shape {
		t.Fatal("an argument change reused the shape approval")
	}
	other := &requestDocument{debugKey: "doc-2"}
	if got := mcpConsentKey(other, plan); got == shape {
		t.Fatal("another document reused the shape approval")
	}
}

func TestMCPConsentFileRoundTrip(t *testing.T) {
	path := filepath.Join(t.TempDir(), "mcp-consents.json")
	want := map[string]mcpConsentApproval{
		"shape": {LastUsed: 10, Persist: true, Env: map[string]string{"TOKEN": "hash"}},
		"full":  {LastUsed: 20, Persist: true},
	}
	if err := saveMCPConsents(path, want); err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0o600 {
		t.Fatalf("consent file mode = %o", info.Mode().Perm())
	}
	got, err := loadMCPConsents(path)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 || got["shape"].Env["TOKEN"] != "hash" || got["full"].LastUsed != 20 {
		t.Fatalf("consents = %+v", got)
	}
}

func TestMCPConsentKeyFileIsPrivateAndStable(t *testing.T) {
	path := filepath.Join(t.TempDir(), "mcp-consents.key")
	first, err := loadOrCreateMCPConsentKey(path)
	if err != nil {
		t.Fatal(err)
	}
	if len(first) != mcpConsentKeySize {
		t.Fatalf("key length = %d", len(first))
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0o600 {
		t.Fatalf("key file mode = %o", info.Mode().Perm())
	}
	second, err := loadOrCreateMCPConsentKey(path)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(first, second) {
		t.Fatal("key changed between loads")
	}
}

func TestMCPConsentDraftIsNotPersisted(t *testing.T) {
	path := filepath.Join(t.TempDir(), "mcp-consents.json")
	if err := saveMCPConsents(path, map[string]mcpConsentApproval{
		"draft": {LastUsed: 1, Persist: false},
		"saved": {LastUsed: 2, Persist: true},
	}); err != nil {
		t.Fatal(err)
	}
	got, err := loadMCPConsents(path)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || got["draft"].Persist || !got["saved"].Persist {
		t.Fatalf("persisted consents = %+v", got)
	}
}

func TestMCPConsentPrunesByLastUse(t *testing.T) {
	path := filepath.Join(t.TempDir(), "mcp-consents.json")
	consents := make(map[string]mcpConsentApproval, mcpConsentLimit+1)
	for i := 0; i <= mcpConsentLimit; i++ {
		consents[fmt.Sprintf("k%04d", i)] = mcpConsentApproval{LastUsed: int64(i), Persist: true}
	}
	if err := saveMCPConsents(path, consents); err != nil {
		t.Fatal(err)
	}
	got, err := loadMCPConsents(path)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != mcpConsentLimit {
		t.Fatalf("kept %d approvals", len(got))
	}
	if _, ok := got["k0000"]; ok {
		t.Fatal("oldest approval survived")
	}
	if _, ok := got[fmt.Sprintf("k%04d", mcpConsentLimit)]; !ok {
		t.Fatal("newest approval was pruned")
	}
}

func TestMCPConsentValueChangeAsksAgain(t *testing.T) {
	a := workspaceApp()
	a.mcpConsentsPath = filepath.Join(t.TempDir(), "mcp-consents.json")
	a.mcpConsentsKey = []byte("0123456789abcdef0123456789abcdef")
	a.addDocument(savedRequest{ID: "asset-1", Protocol: "MCP", Name: "Local server",
		MCP: request.MCPConfig{Transport: request.MCPTransportStdio, Command: "/bin/sh", Args: "-c\nsleep 1",
			Env: []request.Field{{Name: "NODE_OPTIONS", Value: "--max-old-space-size=512"}}}}, "")
	d := a.requestDocument
	r := d.value().Request
	in, vars := a.sendContext(r), a.activeVars()
	plan, err := r.PrepareMCP(in, vars)
	if err != nil {
		t.Fatal(err)
	}
	shape := mcpConsentKey(d, plan)
	full := mcpConsentFullKey(shape, plan, a.mcpConsentsKey)
	a.mcpConsents = map[string]mcpConsentApproval{
		shape: {LastUsed: 1, Persist: true, Env: mcpConsentEnvHashes(plan, a.mcpConsentsKey)},
		full:  {LastUsed: 1, Persist: true},
	}
	d.mcpConfig.Env[0].Value = "--require /tmp/evil.js"
	a.connectMCP(d)
	if !a.mcpConsent.open || !a.mcpConsent.valuesChanged || !slices.Contains(a.mcpConsent.changed, "NODE_OPTIONS") {
		t.Fatalf("value change did not ask again: %+v", a.mcpConsent)
	}
	if d.mcpUI.session != nil {
		t.Fatal("changed environment started a process")
	}
	tt := ui.NewTester(a.view, 960, 600)
	if !tt.HasText("environment values changed since you approved this command.") || !tt.HasText("Changed values: NODE_OPTIONS") {
		t.Fatalf("consent dialog did not explain the change: %v", tt.Texts())
	}
}

func TestMCPStdioConnectWaitsForConsent(t *testing.T) {
	a := workspaceApp()
	a.mcpConsentsPath = filepath.Join(t.TempDir(), "mcp-consents.json")
	a.addDocument(savedRequest{Protocol: "MCP", Name: "Local server", URL: "https://hidden.example.com",
		MCP: request.MCPConfig{Transport: request.MCPTransportStdio, Command: "/bin/sh", Args: "-c\nsleep 1"}}, "")
	d := a.requestDocument
	a.connectMCP(d)
	if !a.mcpConsent.open || a.mcpConsent.d != d {
		t.Fatal("stdio connection did not ask for confirmation")
	}
	if d.mcpUI == nil || d.mcpUI.session != nil {
		t.Fatal("stdio command started before confirmation")
	}
	if a.mcpConsent.plan.URL != "" || len(a.mcpConsent.plan.Header) != 0 {
		t.Fatalf("confirmation kept hidden HTTP fields: %+v", a.mcpConsent.plan)
	}
}

func TestMCPDisconnectEndsSessionAndSendsDelete(t *testing.T) {
	var deletes atomic.Int32
	server := appMCPFixture()
	handler := mcp.NewStreamableHTTPHandler(func(*http.Request) *mcp.Server { return server }, nil)
	httpServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodDelete {
			deletes.Add(1)
		}
		handler.ServeHTTP(w, r)
	}))
	defer httpServer.Close()
	a := workspaceApp()
	a.addDocument(savedRequest{Protocol: "MCP", Name: "Echo", URL: httpServer.URL,
		MCP: request.MCPConfig{Transport: request.MCPTransportStreamableHTTP, ProtocolVersion: "2025-11-25", Tool: "echo", ToolArgs: `{"text":"hello"}`}}, "")
	d := a.requestDocument
	a.connectMCP(d)
	waitMCPSession(t, d.mcpUI.session, func(s mcpSnapshot) bool { return s.connected && !s.busy })
	d.cancelSend()
	waitMCPSession(t, d.mcpUI.session, func(s mcpSnapshot) bool { return s.finished })
	if state := d.mcpUI.session.snapshot().state; state != "Disconnected" {
		t.Fatalf("state after disconnect = %q", state)
	}
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) && deletes.Load() == 0 {
		time.Sleep(10 * time.Millisecond)
	}
	if deletes.Load() == 0 {
		t.Fatal("disconnect did not send DELETE")
	}
}

func TestMCPCloseDocumentStopsSession(t *testing.T) {
	server := appMCPFixture()
	httpServer := httptest.NewServer(mcp.NewStreamableHTTPHandler(func(*http.Request) *mcp.Server { return server }, nil))
	defer httpServer.Close()
	a := workspaceApp()
	a.addDocument(savedRequest{Protocol: "MCP", Name: "Echo", URL: httpServer.URL,
		MCP: request.MCPConfig{Transport: request.MCPTransportStreamableHTTP, ProtocolVersion: "2025-11-25"}}, "")
	d := a.requestDocument
	a.connectMCP(d)
	waitMCPSession(t, d.mcpUI.session, func(s mcpSnapshot) bool { return s.connected && !s.busy })
	a.closeDocument(d)
	waitMCPSession(t, d.mcpUI.session, func(s mcpSnapshot) bool { return s.finished })
}

func TestMCPStopDocumentsStopsSession(t *testing.T) {
	server := appMCPFixture()
	httpServer := httptest.NewServer(mcp.NewStreamableHTTPHandler(func(*http.Request) *mcp.Server { return server }, nil))
	defer httpServer.Close()
	a := workspaceApp()
	a.addDocument(savedRequest{Protocol: "MCP", Name: "Echo", URL: httpServer.URL,
		MCP: request.MCPConfig{Transport: request.MCPTransportStreamableHTTP, ProtocolVersion: "2025-11-25"}}, "")
	d := a.requestDocument
	a.connectMCP(d)
	waitMCPSession(t, d.mcpUI.session, func(s mcpSnapshot) bool { return s.connected && !s.busy })
	a.stopDocuments()
	waitMCPSession(t, d.mcpUI.session, func(s mcpSnapshot) bool { return s.finished })
}

func TestMCPEndpointDisplayHidesSecrets(t *testing.T) {
	got := mcpEndpointText("https://user:pass@mcp.example.com/rpc?key=secret#fragment")
	if got != "https://mcp.example.com/rpc" {
		t.Fatalf("endpoint display = %q", got)
	}
}

func TestMCPImageDecodeRejectsLargeHeader(t *testing.T) {
	var b bytes.Buffer
	b.WriteString("\x89PNG\r\n\x1a\n")
	ihdr := make([]byte, 13)
	ihdr[0], ihdr[1], ihdr[2], ihdr[3] = 0, 0, 0x4e, 0x20
	ihdr[4], ihdr[5], ihdr[6], ihdr[7] = 0, 0, 0x4e, 0x20
	ihdr[8], ihdr[9], ihdr[10], ihdr[11], ihdr[12] = 8, 0, 0, 0, 0
	writePNGChunk(&b, "IHDR", ihdr)
	u := &mcpDocument{}
	a := &app{}
	result := &request.MCPResult{Content: []request.MCPContent{{Kind: "image", MIMEType: "image/png", Data: b.Bytes()}}}
	a.startMCPImageDecode(u, result, "bomb")
	job := u.imageJob
	if job == nil {
		t.Fatal("no decode job")
	}
	select {
	case answer := <-job.answers:
		if len(answer.err) == 0 || !strings.Contains(answer.err[0], "limit") {
			t.Fatalf("decode error = %+v", answer.err)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("decode job did not finish")
	}
}

func TestMCPImageDecodeCachesResult(t *testing.T) {
	img := image.NewRGBA(image.Rect(0, 0, 4, 3))
	img.Set(0, 0, color.RGBA{R: 255, A: 255})
	var b bytes.Buffer
	if err := png.Encode(&b, img); err != nil {
		t.Fatal(err)
	}
	u := &mcpDocument{}
	a := &app{}
	result := &request.MCPResult{Content: []request.MCPContent{{Kind: "image", MIMEType: "image/png", Data: b.Bytes()}}}
	a.startMCPImageDecode(u, result, "1")
	job := u.imageJob
	if job == nil {
		t.Fatal("no decode job")
	}
	select {
	case answer := <-job.answers:
		u.resultBmp, u.resultErr, u.imageJob = answer.bmp, answer.err, nil
	case <-time.After(3 * time.Second):
		t.Fatal("decode job did not finish")
	}
	if u.resultBmp[0] == nil {
		t.Fatalf("image did not decode: %+v", u.resultErr)
	}
	a.startMCPImageDecode(u, result, "1")
	if u.imageJob != nil {
		t.Fatal("cached image decoded again")
	}
}

func writePNGChunk(w *bytes.Buffer, typ string, data []byte) {
	length := []byte{byte(len(data) >> 24), byte(len(data) >> 16), byte(len(data) >> 8), byte(len(data))}
	w.Write(length)
	w.WriteString(typ)
	w.Write(data)
	crc := pngCRC(append([]byte(typ), data...))
	w.Write([]byte{byte(crc >> 24), byte(crc >> 16), byte(crc >> 8), byte(crc)})
}

func pngCRC(data []byte) uint32 {
	var crc uint32 = 0xffffffff
	for _, b := range data {
		crc ^= uint32(b)
		for range 8 {
			if crc&1 != 0 {
				crc = crc>>1 ^ 0xedb88320
			} else {
				crc >>= 1
			}
		}
	}
	return ^crc
}

func TestMCPRefreshMarksCachedCatalog(t *testing.T) {
	server := mcp.NewServer(&mcp.Implementation{Name: "cached", Version: "1"}, &mcp.ServerOptions{
		SupportedProtocolVersions: []string{"2026-07-28"},
		SetCacheable: func(_ context.Context, _ mcp.Request, c *mcp.Cacheable) {
			// A non-zero TTL is what makes the SDK serve a repeated list from
			// its client-side cache.
			c.TTLMs = 10000
		},
	})
	mcp.AddTool(server, &mcp.Tool{Name: "echo"}, func(context.Context, *mcp.CallToolRequest, struct{}) (*mcp.CallToolResult, struct{}, error) {
		return &mcp.CallToolResult{}, struct{}{}, nil
	})
	httpServer := httptest.NewServer(mcp.NewStreamableHTTPHandler(func(*http.Request) *mcp.Server { return server }, &mcp.StreamableHTTPOptions{Stateless: true}))
	defer httpServer.Close()
	plan := request.MCPPlan{Transport: request.MCPTransportStreamableHTTP, URL: httpServer.URL, Timeout: 10 * time.Second}
	s := openMCPSession(plan, request.Request{Protocol: "MCP"}, request.Context{}, nil, nil)
	defer s.disconnect()
	waitMCPSession(t, s, func(snapshot mcpSnapshot) bool {
		return snapshot.connected && !snapshot.busy && len(snapshot.tools) == 1
	})
	if !s.refreshCatalog() {
		t.Fatal("second refresh did not start")
	}
	snapshot := waitMCPSession(t, s, func(snapshot mcpSnapshot) bool { return !snapshot.busy })
	var cached bool
	for _, event := range snapshot.events {
		if event.kind == "Cache" && strings.Contains(event.name, "tools/list") {
			cached = true
		}
	}
	if !cached {
		t.Fatalf("no cache marker in timeline: %+v", snapshot.events)
	}
}

func TestMCPProgressAppearsInSnapshotAndTimeline(t *testing.T) {
	server := mcp.NewServer(&mcp.Implementation{Name: "progress", Version: "1"}, nil)
	mcp.AddTool(server, &mcp.Tool{Name: "work"}, func(ctx context.Context, req *mcp.CallToolRequest, _ struct{}) (*mcp.CallToolResult, struct{}, error) {
		if token := req.Params.GetProgressToken(); token != nil {
			_ = req.Session.NotifyProgress(ctx, &mcp.ProgressNotificationParams{ProgressToken: token, Progress: 1, Total: 2, Message: "half"})
		}
		return &mcp.CallToolResult{Content: []mcp.Content{&mcp.TextContent{Text: "done"}}}, struct{}{}, nil
	})
	httpServer := httptest.NewServer(mcp.NewStreamableHTTPHandler(func(*http.Request) *mcp.Server { return server }, nil))
	defer httpServer.Close()
	r := request.Request{Protocol: "MCP", URL: httpServer.URL, MCP: request.MCPConfig{
		Transport: request.MCPTransportStreamableHTTP, ProtocolVersion: "2025-11-25", Tool: "work", TimeoutSeconds: 10,
	}}
	plan, err := r.PrepareMCP(request.Context{}, nil)
	if err != nil {
		t.Fatal(err)
	}
	s := openMCPSession(plan, r, request.Context{}, nil, nil)
	defer s.disconnect()
	waitMCPSession(t, s, func(snapshot mcpSnapshot) bool { return snapshot.connected && !snapshot.busy })
	if _, err := s.callTool(r.MCP); err != nil {
		t.Fatal(err)
	}
	snapshot := waitMCPSession(t, s, func(snapshot mcpSnapshot) bool { return !snapshot.busy && snapshot.result != nil })
	if !strings.Contains(snapshot.progress, "1 / 2") {
		t.Fatalf("progress = %q", snapshot.progress)
	}
	var event bool
	for _, e := range snapshot.events {
		if e.kind == "Progress" && strings.Contains(e.text, "half") {
			event = true
		}
	}
	if !event {
		t.Fatalf("progress missing from timeline: %+v", snapshot.events)
	}
}

func TestMCPStdioConsentDoesNotStartHiddenHTTP(t *testing.T) {
	a := workspaceApp()
	a.mcpConsentsPath = filepath.Join(t.TempDir(), "mcp-consents.json")
	a.addDocument(savedRequest{Protocol: "MCP", Name: "Hidden", URL: "https://api.github.com",
		Headers: "Authorization: Bearer {{missing}}", Auth: request.Auth{Type: "Bearer", Token: "{{missing}}"},
		MCP: request.MCPConfig{Transport: request.MCPTransportStdio, Command: "/bin/sh", Args: "-c\ntrue"}}, "")
	d := a.requestDocument
	a.connectMCP(d)
	if !a.mcpConsent.open || a.mcpConsent.d != d {
		t.Fatal("stdio did not ask for confirmation")
	}
	if a.mcpConsent.plan.URL != "" || len(a.mcpConsent.plan.Header) != 0 || len(a.mcpConsent.plan.Env) != 0 {
		t.Fatalf("hidden HTTP fields reached the stdio plan: %+v", a.mcpConsent.plan)
	}
	if strings.Contains(fmt.Sprint(a.mcpConsent.plan), "missing") {
		t.Fatal("hidden undefined variable blocked stdio")
	}
}

func TestMCPServerInitiatedRequestAppearsInTimeline(t *testing.T) {
	server := mcp.NewServer(&mcp.Implementation{Name: "roots", Version: "1"}, nil)
	mcp.AddTool(server, &mcp.Tool{Name: "roots"}, func(ctx context.Context, req *mcp.CallToolRequest, _ struct{}) (*mcp.CallToolResult, struct{}, error) {
		if _, err := req.Session.ListRoots(ctx, &mcp.ListRootsParams{}); err != nil {
			return nil, struct{}{}, err
		}
		return &mcp.CallToolResult{Content: []mcp.Content{&mcp.TextContent{Text: "roots"}}}, struct{}{}, nil
	})
	httpServer := httptest.NewServer(mcp.NewStreamableHTTPHandler(func(*http.Request) *mcp.Server { return server }, nil))
	defer httpServer.Close()
	r := request.Request{Protocol: "MCP", URL: httpServer.URL, MCP: request.MCPConfig{
		Transport: request.MCPTransportStreamableHTTP, ProtocolVersion: "2025-11-25", Tool: "roots", TimeoutSeconds: 10,
	}}
	plan, err := r.PrepareMCP(request.Context{}, nil)
	if err != nil {
		t.Fatal(err)
	}
	s := openMCPSession(plan, r, request.Context{}, nil, nil)
	defer s.disconnect()
	waitMCPSession(t, s, func(snapshot mcpSnapshot) bool { return snapshot.connected && !snapshot.busy })
	if _, err := s.callTool(r.MCP); err != nil {
		t.Fatal(err)
	}
	snapshot := waitMCPSession(t, s, func(snapshot mcpSnapshot) bool { return !snapshot.busy && snapshot.result != nil })
	for _, event := range snapshot.events {
		if event.kind == "RPC In" && event.name == "roots/list" {
			return
		}
	}
	t.Fatalf("roots/list missing from timeline: %+v", snapshot.events)
}

func TestMCPCachedReadIsMarked(t *testing.T) {
	server := mcp.NewServer(&mcp.Implementation{Name: "read-cache", Version: "1"}, &mcp.ServerOptions{
		SupportedProtocolVersions: []string{"2026-07-28"},
		SetCacheable: func(_ context.Context, _ mcp.Request, c *mcp.Cacheable) {
			c.TTLMs = 10000
		},
	})
	server.AddResource(&mcp.Resource{URI: "memo://cached", Name: "cached", MIMEType: "text/plain"},
		func(context.Context, *mcp.ReadResourceRequest) (*mcp.ReadResourceResult, error) {
			return &mcp.ReadResourceResult{Contents: []*mcp.ResourceContents{{URI: "memo://cached", MIMEType: "text/plain", Text: "cached text"}}}, nil
		})
	httpServer := httptest.NewServer(mcp.NewStreamableHTTPHandler(func(*http.Request) *mcp.Server { return server }, &mcp.StreamableHTTPOptions{Stateless: true}))
	defer httpServer.Close()
	r := request.Request{Protocol: "MCP", URL: httpServer.URL, MCP: request.MCPConfig{
		Transport: request.MCPTransportStreamableHTTP, ResourceURI: "memo://cached", TimeoutSeconds: 10,
	}}
	plan, err := r.PrepareMCP(request.Context{}, nil)
	if err != nil {
		t.Fatal(err)
	}
	s := openMCPSession(plan, r, request.Context{}, nil, nil)
	defer s.disconnect()
	waitMCPSession(t, s, func(snapshot mcpSnapshot) bool { return snapshot.connected && !snapshot.busy })
	if _, err := s.readResource(r.MCP); err != nil {
		t.Fatal(err)
	}
	waitMCPSession(t, s, func(snapshot mcpSnapshot) bool { return !snapshot.busy && snapshot.result != nil })
	if _, err := s.readResource(r.MCP); err != nil {
		t.Fatal(err)
	}
	snapshot := waitMCPSession(t, s, func(snapshot mcpSnapshot) bool { return !snapshot.busy })
	for _, event := range snapshot.events {
		if event.kind == "Cache" && strings.Contains(event.name, "resources/read") {
			return
		}
	}
	t.Fatalf("cached read missing marker: %+v", snapshot.events)
}

func TestMCPCatalogTruncationShowsNotice(t *testing.T) {
	server := mcp.NewServer(&mcp.Implementation{Name: "many", Version: "1"}, nil)
	for i := range request.MCPMaxCatalogItems + 1 {
		mcp.AddTool(server, &mcp.Tool{Name: fmt.Sprintf("tool-%04d", i)}, func(context.Context, *mcp.CallToolRequest, struct{}) (*mcp.CallToolResult, struct{}, error) {
			return &mcp.CallToolResult{}, struct{}{}, nil
		})
	}
	httpServer := httptest.NewServer(mcp.NewStreamableHTTPHandler(func(*http.Request) *mcp.Server { return server }, nil))
	defer httpServer.Close()
	plan := request.MCPPlan{Transport: request.MCPTransportStreamableHTTP, URL: httpServer.URL, Timeout: 20 * time.Second}
	s := openMCPSession(plan, request.Request{Protocol: "MCP"}, request.Context{}, nil, nil)
	defer s.disconnect()
	snapshot := waitMCPSession(t, s, func(snapshot mcpSnapshot) bool {
		return snapshot.connected && !snapshot.busy && len(snapshot.tools) > 0
	})
	if len(snapshot.tools) != request.MCPMaxCatalogItems {
		t.Fatalf("tools = %d", len(snapshot.tools))
	}
	if !strings.Contains(snapshot.catalogNotice, "Catalog truncated") {
		t.Fatalf("catalog notice = %q", snapshot.catalogNotice)
	}
}

func TestMCPReconnectAppearsInTimeline(t *testing.T) {
	server := appMCPFixture()
	var gets atomic.Int32
	handler := mcp.NewStreamableHTTPHandler(func(*http.Request) *mcp.Server { return server }, nil)
	httpServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodGet && gets.Add(1) == 1 {
			ctx, cancel := context.WithTimeout(r.Context(), 200*time.Millisecond)
			defer cancel()
			r = r.WithContext(ctx)
		}
		handler.ServeHTTP(w, r)
	}))
	defer httpServer.Close()
	plan := request.MCPPlan{Transport: request.MCPTransportStreamableHTTP, URL: httpServer.URL, Timeout: 15 * time.Second}
	s := openMCPSession(plan, request.Request{Protocol: "MCP"}, request.Context{}, nil, nil)
	defer s.disconnect()
	snapshot := waitMCPSession(t, s, func(snapshot mcpSnapshot) bool {
		for _, event := range snapshot.events {
			if event.kind == "System" && event.name == "Stream reconnect" {
				return true
			}
		}
		return false
	})
	if !snapshot.connected {
		t.Fatalf("session ended after reconnect: %+v", snapshot)
	}
}

func TestMCPRPCEventKeepsRealPayloadSize(t *testing.T) {
	s := &mcpSession{}
	payload := strings.Repeat("x", 2<<20)
	s.rpc("In", "sampling/createMessage", payload)
	snapshot := s.snapshot()
	if len(snapshot.events) != 1 {
		t.Fatalf("events = %d", len(snapshot.events))
	}
	event := snapshot.events[0]
	if event.size != len(payload) {
		t.Fatalf("event size = %d, want %d", event.size, len(payload))
	}
	if !strings.Contains(event.text, "Retained") || !strings.Contains(event.text, fmt.Sprint(len(payload))) {
		t.Fatalf("preview did not report the retained size: %q", event.text[len(event.text)-120:])
	}
}

func TestMCPBodyLimitExplainsSessionClose(t *testing.T) {
	body := bytes.Repeat([]byte("x"), request.MCPMaxFrame+1024)
	httpServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write(body)
	}))
	defer httpServer.Close()
	plan := request.MCPPlan{Transport: request.MCPTransportStreamableHTTP, URL: httpServer.URL, Timeout: 5 * time.Second}
	s := openMCPSession(plan, request.Request{Protocol: "MCP"}, request.Context{}, nil, nil)
	defer s.disconnect()
	snapshot := waitMCPSession(t, s, func(snapshot mcpSnapshot) bool { return snapshot.finished })
	for _, event := range snapshot.events {
		if event.kind == "System" && event.name == "Response limit" && strings.Contains(event.text, "exceeded") {
			return
		}
	}
	t.Fatalf("missing body-limit explanation: %+v", snapshot.events)
}

func TestMCPHeaderDropAppearsInTimeline(t *testing.T) {
	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if got := r.Header.Get("Authorization"); got != "" {
			t.Errorf("cross-origin SSE endpoint received Authorization %q", got)
		}
		http.Error(w, "not an MCP endpoint", http.StatusBadRequest)
	}))
	defer target.Close()
	primary := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(http.StatusOK)
		fmt.Fprintf(w, "event: endpoint\ndata: %s/message\n\n", target.URL)
		if f, ok := w.(http.Flusher); ok {
			f.Flush()
		}
		<-r.Context().Done()
	}))
	defer primary.Close()
	plan := request.MCPPlan{Transport: request.MCPTransportSSE, URL: primary.URL,
		Header: http.Header{"Authorization": {"Bearer secret"}}, Timeout: 3 * time.Second}
	s := openMCPSession(plan, request.Request{Protocol: "MCP"}, request.Context{}, nil, nil)
	defer s.disconnect()
	snapshot := waitMCPSession(t, s, func(snapshot mcpSnapshot) bool { return snapshot.finished })
	for _, event := range snapshot.events {
		if event.kind == "System" && event.name == "Headers withheld" && strings.Contains(event.text, target.URL) {
			return
		}
	}
	t.Fatalf("header-drop explanation missing: %+v", snapshot.events)
}
