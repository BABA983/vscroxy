//go:build linux

package procinfo

import (
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
)

// lookupPort finds the socket whose local port is port, then the process that
// holds it. The server side of the connection listens on the proxy port, so
// the matching socket belongs to the client.
func lookupPort(port string) Process {
	inode := socketInode(port)
	if inode == "" {
		return Process{}
	}
	return processOf(inode)
}

// socketInode reads /proc/net/tcp and tcp6 for the established socket whose
// local port is port and returns its inode.
func socketInode(port string) string {
	n, err := strconv.ParseUint(port, 10, 16)
	if err != nil {
		return ""
	}
	want := fmt.Sprintf(":%04X", n)
	for _, path := range []string{"/proc/net/tcp", "/proc/net/tcp6"} {
		data, err := os.ReadFile(path)
		if err != nil {
			continue
		}
		for _, line := range strings.Split(string(data), "\n") {
			fields := strings.Fields(line)
			if len(fields) < 10 || fields[3] != "01" {
				continue // not ESTABLISHED, or the header
			}
			if strings.HasSuffix(fields[1], want) {
				return fields[9]
			}
		}
	}
	return ""
}

// processOf walks the open file descriptors of every process for the socket
// inode and returns the process behind it.
func processOf(inode string) Process {
	target := "socket:[" + inode + "]"
	procs, err := os.ReadDir("/proc")
	if err != nil {
		return Process{}
	}
	for _, proc := range procs {
		pid := proc.Name()
		if _, err := strconv.Atoi(pid); err != nil {
			continue
		}
		fds, err := os.ReadDir(filepath.Join("/proc", pid, "fd"))
		if err != nil {
			continue
		}
		for _, fd := range fds {
			link, err := os.Readlink(filepath.Join("/proc", pid, "fd", fd.Name()))
			if err != nil || link != target {
				continue
			}
			if exe, err := os.Readlink(filepath.Join("/proc", pid, "exe")); err == nil {
				return Process{Name: filepath.Base(exe), Path: exe}
			}
			if comm, err := os.ReadFile(filepath.Join("/proc", pid, "comm")); err == nil {
				return Process{Name: strings.TrimSpace(string(comm))}
			}
			return Process{}
		}
	}
	return Process{}
}
