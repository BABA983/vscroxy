//go:build darwin

package procinfo

import (
	"net"
	"os/exec"
	"path/filepath"
	"testing"
	"time"
)

// ps names a process it cannot name by path with its command name alone —
// curl is "curl", not "/usr/bin/curl" — and the lookup used to drop that as
// if the process were unknown.
func TestLookupNamesAProcessWithABareCommandName(t *testing.T) {
	if _, err := exec.LookPath("curl"); err != nil {
		t.Skip("curl is not installed")
	}
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()
	accepted := make(chan net.Conn, 1)
	go func() {
		conn, err := ln.Accept()
		if err == nil {
			accepted <- conn
		}
	}()
	cmd := exec.Command("curl", "-s", "-o", "/dev/null", "http://"+ln.Addr().String())
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	defer func() { _ = cmd.Process.Kill(); _, _ = cmd.Process.Wait() }()
	var conn net.Conn
	select {
	case conn = <-accepted:
	case <-time.After(5 * time.Second):
		t.Fatal("curl did not connect")
	}
	defer conn.Close()
	process := Lookup(conn.RemoteAddr().String(), conn.LocalAddr().String())
	if process.Name != "curl" {
		t.Fatalf("process = %+v, want curl", process)
	}
	if !filepath.IsAbs(process.Path) {
		t.Fatalf("path = %q, want the executable's absolute path", process.Path)
	}
}
