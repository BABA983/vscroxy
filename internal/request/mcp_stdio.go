package request

import (
	"context"
	"errors"
	"io"
	"os"
	"os/exec"
	"sync"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// mcpStdioTransport starts one local MCP server and owns its process tree.
// The SDK's CommandTransport only signals the process itself, which leaves
// children of npx/uvx/sh -c behind; this transport terminates the whole tree.
type mcpStdioTransport struct {
	cmd    *exec.Cmd
	tree   mcpProcessTree
	mu     sync.Mutex
	conn   *mcpStdioConn
	closed bool
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
	t.tree = mcpAttachProcess(t.cmd)
	// Keep stdout out of the connection's Close path: the server gets stdin
	// EOF first, then the process tree is terminated, then stdout is closed.
	exited := make(chan struct{})
	reader := &mcpExitReader{ReadCloser: stdout, done: exited}
	base := &mcp.IOTransport{Reader: reader, Writer: stdin, MaxLineLength: MCPMaxFrame}
	conn, err := base.Connect(ctx)
	if err != nil {
		mcpCloseProcess(t.cmd, t.tree, stdin, stdout, nil, false)
		t.mu.Lock()
		t.closed = true
		t.mu.Unlock()
		return nil, err
	}
	c := &mcpStdioConn{Connection: conn, cmd: t.cmd, tree: t.tree, stdin: stdin, stdout: stdout, exited: exited}
	t.mu.Lock()
	t.conn = c
	t.mu.Unlock()
	return c, nil
}

// Close is the error path: DialMCP calls it when initialization fails, so a
// started command never survives a failed connection.
func (t *mcpStdioTransport) Close() error {
	t.mu.Lock()
	if t.closed {
		t.mu.Unlock()
		return nil
	}
	t.closed = true
	conn := t.conn
	t.mu.Unlock()
	if conn != nil {
		return conn.Close()
	}
	if t.cmd.Process != nil {
		mcpCloseProcess(t.cmd, t.tree, nil, nil, nil, false)
	}
	return nil
}

// mcpExitReader closes done when the server's stdout reaches EOF. Unix uses
// it to skip the stdin-EOF grace period when the process already exited.
type mcpExitReader struct {
	io.ReadCloser
	once sync.Once
	done chan struct{}
}

func (r *mcpExitReader) Read(p []byte) (int, error) {
	n, err := r.ReadCloser.Read(p)
	if err != nil {
		r.once.Do(func() { close(r.done) })
	}
	return n, err
}

type mcpStdioConn struct {
	mcp.Connection
	cmd    *exec.Cmd
	tree   mcpProcessTree
	stdin  io.WriteCloser
	stdout io.ReadCloser
	exited chan struct{}
	once   sync.Once
	err    error
}

// Close follows the MCP stdio shutdown order: close stdin, let the server
// exit, then terminate the process tree. stdout stays open until the server
// has had its chance to drain, so a server writing its final message is not
// cut off by a closed pipe.
func (c *mcpStdioConn) Close() error {
	c.once.Do(func() {
		c.err = mcpCloseProcess(c.cmd, c.tree, c.stdin, c.stdout, c.exited, true)
		if err := c.Connection.Close(); err != nil && !errors.Is(err, io.ErrClosedPipe) && !errors.Is(err, os.ErrClosed) && c.err == nil {
			c.err = err
		}
	})
	return c.err
}
