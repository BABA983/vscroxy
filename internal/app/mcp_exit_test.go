//go:build unix

package app

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/baba983/vscroxy/internal/request"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

func TestMCPStopDocumentsKillsStdioTreeOnExit(t *testing.T) {
	dir := t.TempDir()
	pidFile := filepath.Join(dir, "sleep.pid")
	script := filepath.Join(dir, "server.sh")
	source := "#!/bin/sh\nsleep 300 &\necho $! > \"$VSCROXY_SLEEP_PID\"\nexec \"$VSCROXY_TEST_BIN\" -test.run=TestMCPStdioAppHelper -test.v=false\n"
	if err := os.WriteFile(script, []byte(source), 0o700); err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command(os.Args[0], "-test.run=TestMCPStopDocumentsExitHelper", "-test.v=false")
	cmd.Env = append(os.Environ(),
		"VSCROXY_MCP_EXIT_HELPER=1",
		"VSCROXY_MCP_APP_HELPER=1",
		"VSCROXY_TEST_BIN="+os.Args[0],
		"VSCROXY_SLEEP_PID="+pidFile,
		"VSCROXY_MCP_SERVER="+script,
	)
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	pid := waitAppPIDFile(t, pidFile)
	if err := cmd.Wait(); err != nil {
		t.Fatalf("exit helper: %v", err)
	}
	deadline := time.Now().Add(1500 * time.Millisecond)
	for time.Now().Before(deadline) {
		if syscall.Kill(pid, 0) != nil {
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatalf("stdio grandchild %d survived app exit", pid)
}

func TestMCPStopDocumentsExitHelper(t *testing.T) {
	if os.Getenv("VSCROXY_MCP_EXIT_HELPER") != "1" {
		t.Skip("exit helper")
	}
	a := workspaceApp()
	a.addDocument(savedRequest{Protocol: "MCP", Name: "Local server", MCP: request.MCPConfig{
		Transport: request.MCPTransportStdio,
		Command:   os.Getenv("VSCROXY_MCP_SERVER"),
		Env:       []request.Field{{Name: "VSCROXY_TEST_BIN", Value: os.Getenv("VSCROXY_TEST_BIN")}, {Name: "VSCROXY_SLEEP_PID", Value: os.Getenv("VSCROXY_SLEEP_PID")}},
	}}, "")
	d := a.requestDocument
	r := d.value().Request
	in, vars := a.sendContext(r), a.activeVars()
	plan, err := r.PrepareMCP(in, vars)
	if err != nil {
		t.Fatal(err)
	}
	a.startMCP(d, plan)
	waitMCPSession(t, d.mcpUI.session, func(snapshot mcpSnapshot) bool { return snapshot.connected && !snapshot.busy })
	a.stopDocuments()
	os.Exit(0)
}

func TestMCPStdioAppHelper(t *testing.T) {
	if os.Getenv("VSCROXY_MCP_APP_HELPER") != "1" {
		t.Skip("stdio helper")
	}
	server := appMCPFixture()
	session, err := server.Connect(context.Background(), &mcp.StdioTransport{}, nil)
	if err != nil {
		os.Exit(1)
	}
	_ = session.Wait()
	os.Exit(0)
}

func waitAppPIDFile(t *testing.T, path string) int {
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
