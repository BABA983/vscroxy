package mcpserver_test

import (
	"context"
	"errors"
	"io"
	"net"
	"sync"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// joinedTransport builds one end of an in-memory MCP connection. The SDK's
// IO transport starts a frame reader inside Connect and returns before that
// reader exits, so Close alone can leave it running when the tests end. The
// read end tracks that reader through a WaitGroup; closed stops any new Read
// from registering after Close has begun waiting.
type joinedTransport struct {
	reader *joinedReader
	writer io.WriteCloser
}

func joinedTransports() (*joinedTransport, *joinedTransport) {
	server, client := net.Pipe()
	return &joinedTransport{reader: &joinedReader{ReadCloser: server}, writer: server},
		&joinedTransport{reader: &joinedReader{ReadCloser: client}, writer: client}
}

func (t *joinedTransport) Connect(ctx context.Context) (mcp.Connection, error) {
	return (&mcp.IOTransport{Reader: t.reader, Writer: t.writer}).Connect(ctx)
}

func (t *joinedTransport) Close() error { return errors.Join(t.reader.Close(), t.writer.Close()) }

func (t *joinedTransport) wait() { t.reader.frames.Wait() }

type joinedReader struct {
	io.ReadCloser
	mu     sync.Mutex
	closed bool
	frames sync.WaitGroup
}

func (r *joinedReader) Read(p []byte) (int, error) {
	r.mu.Lock()
	if r.closed {
		r.mu.Unlock()
		return 0, io.ErrClosedPipe
	}
	r.frames.Add(1)
	r.mu.Unlock()
	defer r.frames.Done()
	return r.ReadCloser.Read(p)
}

func (r *joinedReader) Close() error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.closed {
		return nil
	}
	r.closed = true
	return r.ReadCloser.Close()
}
