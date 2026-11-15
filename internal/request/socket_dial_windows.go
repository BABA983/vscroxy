//go:build windows

package request

import (
	"context"
	"errors"
	"net"
	"os"
	"strings"

	"golang.org/x/sys/windows"
)

// dialSocket opens a Windows named pipe. A path that is not a pipe name is
// tried as an AF_UNIX socket, which Windows 10 and later also have.
func dialSocket(ctx context.Context, path string) (net.Conn, error) {
	if !strings.HasPrefix(path, `\\.\pipe\`) {
		var dialer net.Dialer
		return dialer.DialContext(ctx, "unix", path)
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	name, err := windows.UTF16PtrFromString(path)
	if err != nil {
		return nil, err
	}
	handle, err := windows.CreateFile(name, windows.GENERIC_READ|windows.GENERIC_WRITE, 0, nil,
		windows.OPEN_EXISTING, windows.FILE_FLAG_OVERLAPPED, 0)
	if err != nil {
		return nil, err
	}
	file := os.NewFile(uintptr(handle), path)
	if file == nil {
		_ = windows.CloseHandle(handle)
		return nil, errors.New("open named pipe")
	}
	return &pipeConn{File: file}, nil
}

// pipeConn gives an *os.File the address methods of net.Conn. Reads, writes,
// closing and deadlines stay the file's, including its overlapped I/O.
type pipeConn struct {
	*os.File
}

func (c *pipeConn) LocalAddr() net.Addr  { return pipeAddr(c.Name()) }
func (c *pipeConn) RemoteAddr() net.Addr { return pipeAddr(c.Name()) }

type pipeAddr string

func (a pipeAddr) Network() string { return "pipe" }
func (a pipeAddr) String() string  { return string(a) }
