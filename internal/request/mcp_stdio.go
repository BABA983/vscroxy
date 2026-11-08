package request

import (
	"context"
	"errors"
	"io"
	"os/exec"
	"sync"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// mcpStdioTransport starts one local MCP server and owns its process group.
// The SDK's CommandTransport only signals the process itself, which leaves
// children of npx/uvx/sh -c behind; this transport terminates the group.
type mcpStdioTransport struct {
	cmd  *exec.Cmd
	pgid int
	mu   sync.Mutex
	conn *mcpStdioConn
}

func (t *mcpStdioTransport) Connect(ctx context.Context) (mcp.Connection, error) {
	stdout, err := t.cmd.StdoutPipe()
	if err != nil {
		return nil, err
	}
	stdin, err := t.cmd.StdinPipe()
	if err != nil {
		return nil, err
	}
	mcpPrepareCommand(t.cmd)
	t.cmd.WaitDelay = 2 * time.Second
	if err := t.cmd.Start(); err != nil {
		return nil, err
	}
	t.pgid = mcpProcessGroupID(t.cmd)
	base := &mcp.IOTransport{Reader: stdout, Writer: stdin, MaxLineLength: MCPMaxFrame}
	conn, err := base.Connect(ctx)
	if err != nil {
		mcpKillProcessGroup(t.pgid, false)
		mcpKillProcessGroup(t.pgid, true)
		_ = t.cmd.Wait()
		return nil, err
	}
	c := &mcpStdioConn{Connection: conn, cmd: t.cmd, pgid: t.pgid}
	t.mu.Lock()
	t.conn = c
	t.mu.Unlock()
	return c, nil
}

// Close is the error path: DialMCP calls it when initialization fails, so a
// started command never survives a failed connection.
func (t *mcpStdioTransport) Close() error {
	t.mu.Lock()
	conn := t.conn
	t.mu.Unlock()
	if conn != nil {
		return conn.Close()
	}
	if t.cmd.Process != nil {
		mcpKillProcessGroup(t.pgid, false)
		mcpKillProcessGroup(t.pgid, true)
		_ = t.cmd.Wait()
	}
	return nil
}

type mcpStdioConn struct {
	mcp.Connection
	cmd  *exec.Cmd
	pgid int
	once sync.Once
	err  error
}

// Close closes stdin/stdout, terminates the process group, and waits. A wait
// error after our own termination is not a close failure.
func (c *mcpStdioConn) Close() error {
	c.once.Do(func() {
		if err := c.Connection.Close(); err != nil && !errors.Is(err, io.ErrClosedPipe) {
			c.err = err
		}
		mcpKillProcessGroup(c.pgid, false)
		wait := make(chan error, 1)
		go func() { wait <- c.cmd.Wait() }()
		select {
		case <-wait:
		case <-time.After(2 * time.Second):
			mcpKillProcessGroup(c.pgid, true)
			select {
			case <-wait:
			case <-time.After(time.Second):
				if c.cmd.Process != nil {
					_ = c.cmd.Process.Kill()
				}
				<-wait
			}
		}
		// A normally exited process may still have children in its group.
		// The group id was captured at start, so this still reaches a child
		// that outlived the leader and kept the group alive.
		mcpKillProcessGroup(c.pgid, true)
	})
	return c.err
}
