//go:build windows

package request

import (
	"bufio"
	"context"
	"crypto/rand"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"sync"
	"testing"
	"time"

	"golang.org/x/sys/windows"
)

// namedPipeListener accepts HTTP connections on one Windows named pipe. It
// creates a fresh pipe instance for each Accept, as a server must to serve
// more than one connection.
type namedPipeListener struct {
	name string

	mu     sync.Mutex
	handle windows.Handle
	closed bool
}

func listenNamedPipe(t *testing.T) *namedPipeListener {
	t.Helper()
	var suffix [8]byte
	if _, err := rand.Read(suffix[:]); err != nil {
		t.Fatal(err)
	}
	return &namedPipeListener{name: fmt.Sprintf(`\\.\pipe\vscroxy-test-%x`, suffix)}
}

func (l *namedPipeListener) Accept() (net.Conn, error) {
	name, err := windows.UTF16PtrFromString(l.name)
	if err != nil {
		return nil, err
	}
	handle, err := windows.CreateNamedPipe(name, windows.PIPE_ACCESS_DUPLEX,
		windows.PIPE_TYPE_BYTE|windows.PIPE_READMODE_BYTE|windows.PIPE_WAIT,
		windows.PIPE_UNLIMITED_INSTANCES, 4096, 4096, 0, nil)
	if err != nil {
		return nil, err
	}
	l.mu.Lock()
	if l.closed {
		l.mu.Unlock()
		_ = windows.CloseHandle(handle)
		return nil, net.ErrClosed
	}
	l.handle = handle
	l.mu.Unlock()
	if err := windows.ConnectNamedPipe(handle, nil); err != nil && err != windows.ERROR_PIPE_CONNECTED {
		_ = windows.CloseHandle(handle)
		l.mu.Lock()
		if l.handle == handle {
			l.handle = 0
		}
		closed := l.closed
		l.mu.Unlock()
		if closed {
			return nil, net.ErrClosed
		}
		return nil, err
	}
	file := os.NewFile(uintptr(handle), l.name)
	if file == nil {
		_ = windows.CloseHandle(handle)
		return nil, errors.New("open named pipe")
	}
	return &pipeConn{File: file}, nil
}

func (l *namedPipeListener) Close() error {
	l.mu.Lock()
	if l.closed {
		l.mu.Unlock()
		return nil
	}
	l.closed = true
	handle := l.handle
	l.handle = 0
	l.mu.Unlock()
	if handle != 0 {
		return windows.CloseHandle(handle)
	}
	return nil
}

func (l *namedPipeListener) Addr() net.Addr { return pipeAddr(l.name) }

func TestNamedPipeSend(t *testing.T) {
	type seenRequest struct {
		host, path, body string
	}
	seen := make(chan seenRequest, 1)
	listener := listenNamedPipe(t)
	t.Cleanup(func() { _ = listener.Close() })
	go func() {
		conn, err := listener.Accept()
		if err != nil {
			return
		}
		defer conn.Close()
		req, err := http.ReadRequest(bufio.NewReader(conn))
		if err != nil {
			return
		}
		body, _ := io.ReadAll(req.Body)
		seen <- seenRequest{host: req.Host, path: req.URL.RequestURI(), body: string(body)}
		reply := "pong over a pipe"
		fmt.Fprintf(conn, "HTTP/1.1 200 OK\r\nContent-Length: %d\r\nConnection: close\r\n\r\n%s", len(reply), reply)
	}()

	ctx, cancel := context.WithTimeout(t.Context(), 15*time.Second)
	defer cancel()
	r := Request{Method: "POST", URL: "http://pipe.test/echo?x=1", Headers: "X-Pipe: yes",
		BodyMode: "Raw", Body: "through the pipe", Settings: Settings{SocketPath: listener.name}}
	out := r.Execute(ctx, testProxy(t), Context{}, nil, nil)
	if out.Err != nil {
		t.Fatalf("%s: %v", out.Stage, out.Err)
	}
	if out.Response == nil || out.Response.Status != http.StatusOK || out.Response.ResponseBody != "pong over a pipe" {
		t.Fatalf("response = %+v", out.Response)
	}
	got := <-seen
	if got.host != "pipe.test" || got.path != "/echo?x=1" || got.body != "through the pipe" {
		t.Fatalf("server saw %+v", got)
	}
}
