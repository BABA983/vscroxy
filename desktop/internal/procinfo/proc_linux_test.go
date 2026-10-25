//go:build linux

package procinfo

import (
	"bufio"
	"fmt"
	"net"
	"os"
	"os/exec"
	"testing"
)

func TestInheritedSocketHelper(t *testing.T) {
	if os.Getenv("VSCROXY_SHARED_SOCKET_HELPER") != "1" {
		return
	}
	// ExtraFiles keeps the inherited client socket open on fd 3 while the
	// parent looks it up. The handshake avoids timing-based assertions.
	fmt.Println("ready")
	bufio.NewReader(os.Stdin).ReadString('\n')
}

func TestLookupRejectsSharedSocket(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()
	client, err := net.Dial("tcp", ln.Addr().String())
	if err != nil {
		t.Fatal(err)
	}
	defer client.Close()
	server, err := ln.Accept()
	if err != nil {
		t.Fatal(err)
	}
	defer server.Close()
	file, err := client.(*net.TCPConn).File()
	if err != nil {
		t.Fatal(err)
	}
	defer file.Close()
	exe, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	cmd := exec.CommandContext(t.Context(), exe, "-test.run=^TestInheritedSocketHelper$", "-test.count=1")
	cmd.Env = append(os.Environ(), "VSCROXY_SHARED_SOCKET_HELPER=1")
	cmd.ExtraFiles = []*os.File{file}
	stdin, err := cmd.StdinPipe()
	if err != nil {
		t.Fatal(err)
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		t.Fatal(err)
	}
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	defer func() {
		stdin.Close()
		if err := cmd.Wait(); err != nil {
			t.Errorf("shared-socket helper: %v", err)
		}
	}()
	if ready, err := bufio.NewReader(stdout).ReadString('\n'); err != nil || ready != "ready\n" {
		t.Fatalf("helper readiness = %q, %v", ready, err)
	}
	if got := Lookup(server.RemoteAddr().String(), server.LocalAddr().String()); got != (Process{}) {
		t.Fatalf("shared socket attributed to %+v", got)
	}
}
