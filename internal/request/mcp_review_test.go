package request

import (
	"bytes"
	"context"
	"encoding/json/jsontext"
	"fmt"
	"image"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

type mcpRoundTripFunc func(*http.Request) (*http.Response, error)

func (f mcpRoundTripFunc) RoundTrip(req *http.Request) (*http.Response, error) { return f(req) }

func TestMCPHeadersOnlyReachTheDocumentOrigin(t *testing.T) {
	for _, test := range []struct {
		origin, target string
		send           bool
	}{
		{"https://mcp.example.com/rpc", "https://other.example.net/rpc", false},
		{"https://mcp.example.com/rpc", "https://mcp.example.com/rpc", true},
		{"https://h:443/rpc", "https://h/rpc", true},
		{"https://h./rpc", "https://h/rpc", true},
		{"https://127.0.0.1/rpc", "https://localhost/rpc", false},
	} {
		origin, _ := url.Parse(test.origin)
		var seen http.Header
		var dropped atomic.Int32
		transport := &mcpHeaderTransport{
			base: mcpRoundTripFunc(func(req *http.Request) (*http.Response, error) {
				seen = req.Header.Clone()
				return &http.Response{StatusCode: http.StatusOK, Header: http.Header{"Content-Type": {"application/json"}}, Body: io.NopCloser(strings.NewReader(`{}`))}, nil
			}),
			header:       http.Header{"Authorization": {"Bearer secret"}, "X-Test": {"one"}},
			origin:       origin,
			onHeaderDrop: func(string) { dropped.Add(1) },
		}
		req := httptest.NewRequest(http.MethodPost, test.target, nil)
		resp, err := transport.RoundTrip(req)
		if err != nil {
			t.Fatal(err)
		}
		resp.Body.Close()
		got := seen.Get("Authorization")
		if test.send && got != "Bearer secret" {
			t.Errorf("%s -> %s Authorization = %q", test.origin, test.target, got)
		}
		if !test.send && (got != "" || dropped.Load() != 1) {
			t.Errorf("%s -> %s withheld=%d Authorization=%q", test.origin, test.target, dropped.Load(), got)
		}
	}
}

func TestMCPRejectsCrossOriginRedirects(t *testing.T) {
	var targetCalls atomic.Int32
	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		targetCalls.Add(1)
		if got := r.Header.Get("Authorization"); got != "" {
			t.Errorf("redirect target received Authorization %q", got)
		}
		http.Error(w, "unexpected", http.StatusBadRequest)
	}))
	defer target.Close()
	redirect := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, target.URL+"/mcp", http.StatusTemporaryRedirect)
	}))
	defer redirect.Close()
	plan := MCPPlan{URL: redirect.URL, Header: http.Header{"Authorization": {"Bearer secret"}}}
	client := mcpHTTPClient(plan, MCPHandlers{})
	req, _ := http.NewRequest(http.MethodPost, redirect.URL, strings.NewReader(`{}`))
	resp, err := client.Do(req)
	if err == nil {
		resp.Body.Close()
		t.Fatal("cross-origin redirect was followed")
	}
	if !strings.Contains(err.Error(), "cross-origin") {
		t.Fatalf("redirect error = %v", err)
	}
	if targetCalls.Load() != 0 {
		t.Fatalf("redirect target was called %d times", targetCalls.Load())
	}
}

func TestMCPSSEEndpointOnAnotherOriginGetsNoAuth(t *testing.T) {
	var targetAuth atomic.Value
	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		targetAuth.Store(r.Header.Get("Authorization"))
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
	plan := MCPPlan{Transport: MCPTransportSSE, URL: primary.URL, Header: http.Header{"Authorization": {"Bearer secret"}}}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	if client, err := DialMCP(ctx, plan, MCPHandlers{}); err == nil {
		client.Close()
		t.Fatal("cross-origin SSE endpoint connected")
	}
	if got, _ := targetAuth.Load().(string); got != "" {
		t.Fatalf("SSE endpoint received Authorization %q", got)
	}
}

func TestMCPJSONResponseIsBounded(t *testing.T) {
	for _, contentLength := range []int64{-1, MCPMaxFrame + 1} {
		body := bytes.Repeat([]byte("x"), MCPMaxFrame+1)
		resp := &http.Response{StatusCode: http.StatusOK, Header: http.Header{"Content-Type": {"application/json"}},
			Body: io.NopCloser(bytes.NewReader(body)), ContentLength: contentLength}
		var limited atomic.Int32
		bounded, err := mcpBoundResponse(resp, func(int64) { limited.Add(1) })
		if err != nil {
			t.Fatal(err)
		}
		if _, err := io.ReadAll(bounded.Body); err == nil {
			t.Fatal("oversized JSON response was read without an error")
		}
		if limited.Load() != 1 {
			t.Fatalf("ContentLength=%d limit callback = %d", contentLength, limited.Load())
		}
	}
}

func mcpStreamableServer(t *testing.T, server *mcp.Server, opts *mcp.StreamableHTTPOptions) *httptest.Server {
	t.Helper()
	return httptest.NewServer(mcp.NewStreamableHTTPHandler(func(*http.Request) *mcp.Server { return server }, opts))
}

func TestMCP2026LoggingProgressAndServerRequests(t *testing.T) {
	server := mcp.NewServer(&mcp.Implementation{Name: "protocol-fixture", Version: "1"}, &mcp.ServerOptions{
		SupportedProtocolVersions: []string{"2026-07-28"},
	})
	mcp.AddTool(server, &mcp.Tool{Name: "log"}, func(ctx context.Context, req *mcp.CallToolRequest, _ struct{}) (*mcp.CallToolResult, struct{}, error) {
		if err := req.Session.Log(ctx, &mcp.LoggingMessageParams{Level: mcp.LoggingLevel("info"), Logger: "fixture", Data: "hello 2026"}); err != nil {
			return nil, struct{}{}, err
		}
		return &mcp.CallToolResult{Content: []mcp.Content{&mcp.TextContent{Text: "logged"}}}, struct{}{}, nil
	})
	mcp.AddTool(server, &mcp.Tool{Name: "progress"}, func(ctx context.Context, req *mcp.CallToolRequest, _ struct{}) (*mcp.CallToolResult, struct{}, error) {
		if token := req.Params.GetProgressToken(); token != nil {
			_ = req.Session.NotifyProgress(ctx, &mcp.ProgressNotificationParams{ProgressToken: token, Progress: 1, Total: 2, Message: "half"})
		}
		return &mcp.CallToolResult{Content: []mcp.Content{&mcp.TextContent{Text: "done"}}}, struct{}{}, nil
	})
	httpServer := mcpStreamableServer(t, server, &mcp.StreamableHTTPOptions{Stateless: true})
	defer httpServer.Close()

	var mu sync.Mutex
	var logs, rpc []string
	progress := make(chan string, 4)
	plan := MCPPlan{Transport: MCPTransportStreamableHTTP, URL: httpServer.URL, Timeout: 10 * time.Second}
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	client, err := DialMCP(ctx, plan, MCPHandlers{
		Log: func(level, logger, text string) {
			mu.Lock()
			logs = append(logs, level+" "+logger+" "+text)
			mu.Unlock()
		},
		Progress: func(token string, p, total float64, message string) {
			select {
			case progress <- fmt.Sprintf("%s %g/%g %s", token, p, total, message):
			default:
			}
		},
		RPC: func(direction, method, text string) {
			mu.Lock()
			rpc = append(rpc, direction+" "+method)
			mu.Unlock()
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	defer client.Close()
	if got := client.Server().ProtocolVersion; got != "2026-07-28" {
		t.Fatalf("negotiated version = %q", got)
	}
	if _, err := client.CallTool(ctx, "log", jsontext.Value(`{}`)); err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		mu.Lock()
		got := len(logs) > 0
		mu.Unlock()
		if got {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	mu.Lock()
	gotLogs := append([]string(nil), logs...)
	mu.Unlock()
	if len(gotLogs) == 0 || !strings.Contains(gotLogs[0], "hello 2026") {
		t.Fatalf("2026 logs = %+v", gotLogs)
	}
	if _, err := client.CallTool(ctx, "progress", jsontext.Value(`{}`)); err != nil {
		t.Fatal(err)
	}
	select {
	case got := <-progress:
		if !strings.Contains(got, "1/2") || !strings.Contains(got, "half") {
			t.Fatalf("progress = %q", got)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("no progress notification")
	}
}

func TestMCPRecordsServerInitiatedRequests(t *testing.T) {
	server := mcp.NewServer(&mcp.Implementation{Name: "rpc-fixture", Version: "1"}, nil)
	mcp.AddTool(server, &mcp.Tool{Name: "roots"}, func(ctx context.Context, req *mcp.CallToolRequest, _ struct{}) (*mcp.CallToolResult, struct{}, error) {
		if _, err := req.Session.ListRoots(ctx, &mcp.ListRootsParams{}); err != nil {
			return nil, struct{}{}, err
		}
		return &mcp.CallToolResult{Content: []mcp.Content{&mcp.TextContent{Text: "roots"}}}, struct{}{}, nil
	})
	httpServer := mcpStreamableServer(t, server, nil)
	defer httpServer.Close()
	var mu sync.Mutex
	var rpc []string
	plan := MCPPlan{Transport: MCPTransportStreamableHTTP, URL: httpServer.URL, ProtocolVersion: "2025-11-25", Timeout: 10 * time.Second}
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	client, err := DialMCP(ctx, plan, MCPHandlers{RPC: func(direction, method, text string) {
		mu.Lock()
		rpc = append(rpc, direction+" "+method)
		mu.Unlock()
	}})
	if err != nil {
		t.Fatal(err)
	}
	defer client.Close()
	if _, err := client.CallTool(ctx, "roots", jsontext.Value(`{}`)); err != nil {
		t.Fatal(err)
	}
	mu.Lock()
	got := append([]string(nil), rpc...)
	mu.Unlock()
	if !containsString(got, "In roots/list") || !containsString(got, "Out roots/list response") {
		t.Fatalf("server-initiated request missing from RPC trace: %+v", got)
	}
}

func containsString(values []string, want string) bool {
	for _, value := range values {
		if value == want {
			return true
		}
	}
	return false
}

func TestMCPDoesNotAdvertiseOptionalCapabilities(t *testing.T) {
	server := mcp.NewServer(&mcp.Implementation{Name: "caps", Version: "1"}, nil)
	mcp.AddTool(server, &mcp.Tool{Name: "caps"}, func(_ context.Context, req *mcp.CallToolRequest, _ struct{}) (*mcp.CallToolResult, struct{}, error) {
		caps := "none"
		if init := req.Session.InitializeParams(); init != nil && init.Capabilities != nil {
			caps = fmt.Sprintf("roots=%v sampling=%v elicitation=%v", init.Capabilities.RootsV2, init.Capabilities.Sampling, init.Capabilities.Elicitation)
		}
		return &mcp.CallToolResult{Content: []mcp.Content{&mcp.TextContent{Text: caps}}}, struct{}{}, nil
	})
	httpServer := mcpStreamableServer(t, server, nil)
	defer httpServer.Close()
	plan := MCPPlan{Transport: MCPTransportStreamableHTTP, URL: httpServer.URL, ProtocolVersion: "2025-11-25", Timeout: 10 * time.Second}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	client, err := DialMCP(ctx, plan, MCPHandlers{})
	if err != nil {
		t.Fatal(err)
	}
	defer client.Close()
	result, err := client.CallTool(ctx, "caps", jsontext.Value(`{}`))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(result.Text, "roots=<nil>") || !strings.Contains(result.Text, "sampling=<nil>") || !strings.Contains(result.Text, "elicitation=<nil>") {
		t.Fatalf("advertised optional capabilities: %s", result.Text)
	}
}

func TestMCPFixedVersionDoesNotSilentlyFallBack(t *testing.T) {
	server := mcp.NewServer(&mcp.Implementation{Name: "old", Version: "1"}, &mcp.ServerOptions{
		SupportedProtocolVersions: []string{"2025-06-18"},
	})
	httpServer := mcpStreamableServer(t, server, nil)
	defer httpServer.Close()
	plan := MCPPlan{Transport: MCPTransportStreamableHTTP, URL: httpServer.URL, ProtocolVersion: "2025-11-25", Timeout: 5 * time.Second}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	client, err := DialMCP(ctx, plan, MCPHandlers{})
	if err == nil {
		client.Close()
		t.Fatal("fixed version silently fell back")
	}
	if !strings.Contains(err.Error(), "requires 2025-11-25") {
		t.Fatalf("version error = %v", err)
	}
}

func TestMCPOversizedJSONResponseFailsTheConnection(t *testing.T) {
	for _, declared := range []bool{false, true} {
		t.Run(fmt.Sprintf("declared=%v", declared), func(t *testing.T) {
			body := bytes.Repeat([]byte("x"), MCPMaxFrame+1024)
			httpServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Content-Type", "application/json")
				if declared {
					w.Header().Set("Content-Length", strconv.Itoa(len(body)))
				}
				w.WriteHeader(http.StatusOK)
				_, _ = w.Write(body)
			}))
			defer httpServer.Close()
			plan := MCPPlan{Transport: MCPTransportStreamableHTTP, URL: httpServer.URL, Timeout: 5 * time.Second}
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			var limited atomic.Int32
			client, err := DialMCP(ctx, plan, MCPHandlers{BodyLimit: func(int64) { limited.Add(1) }})
			if err == nil {
				client.Close()
				t.Fatal("oversized JSON response was accepted")
			}
			if !strings.Contains(err.Error(), "exceeds") {
				t.Fatalf("oversized response error = %v", err)
			}
			if limited.Load() != 1 {
				t.Fatalf("limit callback = %d", limited.Load())
			}
		})
	}
}

func TestMCPCatalogBounds(t *testing.T) {
	server := mcp.NewServer(&mcp.Implementation{Name: "catalog", Version: "1"}, nil)
	long := strings.Repeat("n", 3*MCPMaxCatalogString)
	for i := range 3 {
		mcp.AddTool(server, &mcp.Tool{Name: fmt.Sprintf("%s-%d", long, i), Description: long}, func(context.Context, *mcp.CallToolRequest, struct{}) (*mcp.CallToolResult, struct{}, error) {
			return &mcp.CallToolResult{}, struct{}{}, nil
		})
	}
	for i := range MCPMaxCatalogItems + 1 {
		mcp.AddTool(server, &mcp.Tool{Name: fmt.Sprintf("t%04d", i)}, func(context.Context, *mcp.CallToolRequest, struct{}) (*mcp.CallToolResult, struct{}, error) {
			return &mcp.CallToolResult{}, struct{}{}, nil
		})
	}
	httpServer := mcpStreamableServer(t, server, nil)
	defer httpServer.Close()
	plan := MCPPlan{Transport: MCPTransportStreamableHTTP, URL: httpServer.URL, Timeout: 10 * time.Second}
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	client, err := DialMCP(ctx, plan, MCPHandlers{})
	if err != nil {
		t.Fatal(err)
	}
	defer client.Close()
	client.BeginCatalog()
	tools, err := client.ListTools(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if !client.CatalogTruncated() || len(tools) > MCPMaxCatalogItems {
		t.Fatalf("catalog = %d tools, truncated=%v", len(tools), client.CatalogTruncated())
	}
	for _, tool := range tools {
		if len(tool.Name) > MCPMaxCatalogString || len(tool.Description) > MCPMaxCatalogString {
			t.Fatalf("catalog string not truncated: name=%d description=%d", len(tool.Name), len(tool.Description))
		}
	}
}

func TestMCPCatalogStringMarksTruncation(t *testing.T) {
	server := mcp.NewServer(&mcp.Implementation{Name: "long-name", Version: "1"}, nil)
	mcp.AddTool(server, &mcp.Tool{Name: strings.Repeat("n", 2*MCPMaxCatalogString), Description: "ok"},
		func(context.Context, *mcp.CallToolRequest, struct{}) (*mcp.CallToolResult, struct{}, error) {
			return &mcp.CallToolResult{}, struct{}{}, nil
		})
	httpServer := mcpStreamableServer(t, server, nil)
	defer httpServer.Close()
	plan := MCPPlan{Transport: MCPTransportStreamableHTTP, URL: httpServer.URL, Timeout: 10 * time.Second}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	client, err := DialMCP(ctx, plan, MCPHandlers{})
	if err != nil {
		t.Fatal(err)
	}
	defer client.Close()
	client.BeginCatalog()
	tools, err := client.ListTools(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(tools) != 1 || len(tools[0].Name) != MCPMaxCatalogString || !client.CatalogTruncated() {
		t.Fatalf("long name = %d bytes, truncated=%v", len(tools[0].Name), client.CatalogTruncated())
	}
}

func TestMCPImageHeaderBounds(t *testing.T) {
	// A PNG header is enough for DecodeConfig; no pixel data is needed.
	var b bytes.Buffer
	b.WriteString("\x89PNG\r\n\x1a\n")
	ihdr := make([]byte, 13)
	ihdr[0], ihdr[1], ihdr[2], ihdr[3] = 0, 0, 0x4e, 0x20 // 20000
	ihdr[4], ihdr[5], ihdr[6], ihdr[7] = 0, 0, 0x4e, 0x20 // 20000
	ihdr[8], ihdr[9], ihdr[10], ihdr[11], ihdr[12] = 8, 0, 0, 0, 0
	writeChunk(&b, "IHDR", ihdr)
	config, _, err := image.DecodeConfig(bytes.NewReader(b.Bytes()))
	if err != nil {
		t.Fatalf("header fixture: %v", err)
	}
	if config.Width != 20000 || config.Height != 20000 {
		t.Fatalf("dimensions = %dx%d", config.Width, config.Height)
	}
	if config.Width*config.Height <= 16<<20 {
		t.Fatalf("fixture is not a bomb: %dx%d", config.Width, config.Height)
	}
}

func writeChunk(w io.Writer, typ string, data []byte) {
	length := []byte{byte(len(data) >> 24), byte(len(data) >> 16), byte(len(data) >> 8), byte(len(data))}
	w.Write(length)
	w.Write([]byte(typ))
	w.Write(data)
	crc := crc32IEEE(append([]byte(typ), data...))
	w.Write([]byte{byte(crc >> 24), byte(crc >> 16), byte(crc >> 8), byte(crc)})
}

func crc32IEEE(data []byte) uint32 {
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

func TestMCPStdioProcessGroupIsTerminated(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("process-group test uses a POSIX shell")
	}
	dir := t.TempDir()
	pidFile := filepath.Join(dir, "sleep.pid")
	script := filepath.Join(dir, "server.sh")
	source := "#!/bin/sh\nsleep 300 &\necho $! > \"$VSCROXY_SLEEP_PID\"\nexec \"$VSCROXY_TEST_BIN\" -test.run=TestMCPStdioHelper -test.v=false\n"
	if err := os.WriteFile(script, []byte(source), 0o700); err != nil {
		t.Fatal(err)
	}
	plan := MCPPlan{Transport: MCPTransportStdio, Command: script, Timeout: 15 * time.Second,
		Env: []string{"VSCROXY_MCP_STDIO_HELPER=1", "VSCROXY_SLEEP_PID=" + pidFile, "VSCROXY_TEST_BIN=" + os.Args[0]}}
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	client, err := DialMCP(ctx, plan, MCPHandlers{})
	if err != nil {
		t.Fatal(err)
	}
	pid := waitPIDFile(t, pidFile)
	if err := client.Close(); err != nil && !strings.Contains(err.Error(), "already finished") {
		t.Errorf("close: %v", err)
	}
	waitProcessGone(t, pid)
}

func TestMCPCancellingDuringStdioDialKillsProcess(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("process-group test uses a POSIX shell")
	}
	dir := t.TempDir()
	pidFile := filepath.Join(dir, "sleep.pid")
	script := filepath.Join(dir, "server.sh")
	source := "#!/bin/sh\nsleep 300 &\necho $! > \"$VSCROXY_SLEEP_PID\"\nsleep 300\n"
	if err := os.WriteFile(script, []byte(source), 0o700); err != nil {
		t.Fatal(err)
	}
	plan := MCPPlan{Transport: MCPTransportStdio, Command: script, Timeout: 30 * time.Second,
		Env: []string{"VSCROXY_SLEEP_PID=" + pidFile}}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() {
		client, err := DialMCP(ctx, plan, MCPHandlers{})
		if client != nil {
			client.Close()
		}
		done <- err
	}()
	pid := waitPIDFile(t, pidFile)
	cancel()
	select {
	case err := <-done:
		if err == nil {
			t.Fatal("cancelled dial succeeded")
		}
	case <-time.After(5 * time.Second):
		t.Fatal("cancelled dial did not return")
	}
	waitProcessGone(t, pid)
}

func TestMCPFailedDialTerminatesStdio(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("process-group test uses a POSIX shell")
	}
	dir := t.TempDir()
	pidFile := filepath.Join(dir, "sleep.pid")
	script := filepath.Join(dir, "server.sh")
	source := "#!/bin/sh\nsleep 300 &\necho $! > \"$VSCROXY_SLEEP_PID\"\nsleep 300\n"
	if err := os.WriteFile(script, []byte(source), 0o700); err != nil {
		t.Fatal(err)
	}
	plan := MCPPlan{Transport: MCPTransportStdio, Command: script, Timeout: 2 * time.Second,
		Env: []string{"VSCROXY_SLEEP_PID=" + pidFile}}
	ctx, cancel := context.WithTimeout(context.Background(), 400*time.Millisecond)
	defer cancel()
	if client, err := DialMCP(ctx, plan, MCPHandlers{}); err == nil {
		client.Close()
		t.Fatal("non-MCP command connected")
	}
	pid := waitPIDFile(t, pidFile)
	waitProcessGone(t, pid)
}

func waitPIDFile(t *testing.T, path string) int {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		data, err := os.ReadFile(path)
		if err == nil {
			pid, err := strconv.Atoi(strings.TrimSpace(string(data)))
			if err == nil && pid > 0 {
				return pid
			}
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("no pid in %s", path)
	return 0
}

func waitProcessGone(t *testing.T, pid int) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if !mcpProcessAlive(pid) {
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatalf("process %d survived Close", pid)
}

func TestMCPStandaloneGETReconnectsWithoutReplayingPOST(t *testing.T) {
	server := mcpFixture(t)
	var gets atomic.Int32
	handler := mcp.NewStreamableHTTPHandler(func(*http.Request) *mcp.Server { return server }, nil)
	wrapped := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodGet && gets.Add(1) <= 4 {
			ctx, cancel := context.WithTimeout(r.Context(), 100*time.Millisecond)
			defer cancel()
			r = r.WithContext(ctx)
		}
		handler.ServeHTTP(w, r)
	})
	httpServer := httptest.NewServer(wrapped)
	defer httpServer.Close()
	var reconnects atomic.Int32
	plan := mcpTestPlan(t, MCPTransportStreamableHTTP, httpServer.URL, "2025-11-25")
	ctx, cancel := context.WithTimeout(context.Background(), 40*time.Second)
	defer cancel()
	client, err := DialMCP(ctx, plan, MCPHandlers{Reconnect: func() { reconnects.Add(1) }})
	if err != nil {
		t.Fatal(err)
	}
	defer client.Close()
	deadline := time.Now().Add(30 * time.Second)
	for time.Now().Before(deadline) && reconnects.Load() < 4 {
		time.Sleep(50 * time.Millisecond)
	}
	if reconnects.Load() < 4 {
		t.Fatalf("standalone GET stopped reconnecting: GETs=%d reconnects=%d", gets.Load(), reconnects.Load())
	}
	if _, err := client.CallTool(ctx, "echo", jsontext.Value(`{"text":"after"}`)); err != nil {
		t.Fatalf("call after reconnect: %v", err)
	}
}

func TestMCPStreamableJSONBodyLimitThroughSDK(t *testing.T) {
	// A JSON body larger than MCPMaxFrame must fail at the transport, not be
	// read whole by the SDK. This exercises the same wrapper the SDK uses.
	body := bytes.Repeat([]byte("x"), MCPMaxFrame+1)
	resp := &http.Response{StatusCode: http.StatusOK, Header: http.Header{"Content-Type": {"application/json"}},
		Body: io.NopCloser(bytes.NewReader(body)), ContentLength: -1}
	bounded, err := mcpBoundResponse(resp, nil)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := io.ReadAll(bounded.Body); err == nil {
		t.Fatal("body limit did not fail")
	}
}
