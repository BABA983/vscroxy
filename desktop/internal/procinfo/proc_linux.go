//go:build linux

package procinfo

import (
	"encoding/binary"
	"encoding/hex"
	"net/netip"
	"os"
	"path/filepath"
	"strconv"
	"strings"
)

// lookupSocket finds the exact client socket, not another connection that
// happens to reuse the same ephemeral port.
func lookupSocket(client, server netip.AddrPort) Process {
	inode := socketInode(client, server)
	if inode == "" {
		return Process{}
	}
	return processOf(inode)
}

// socketInode requires a unique established socket with both endpoints.
func socketInode(client, server netip.AddrPort) string {
	var inode string
	for _, path := range []string{"/proc/net/tcp", "/proc/net/tcp6"} {
		data, err := os.ReadFile(path)
		if err != nil {
			continue
		}
		for line := range strings.SplitSeq(string(data), "\n") {
			fields := strings.Fields(line)
			if len(fields) < 10 || fields[3] != "01" {
				continue // not ESTABLISHED, or the header
			}
			local, ok := procEndpoint(fields[1])
			remote, remoteOK := procEndpoint(fields[2])
			if ok && remoteOK && local == client && remote == server {
				if inode != "" && inode != fields[9] {
					return ""
				}
				inode = fields[9]
			}
		}
	}
	return inode
}

// /proc stores each 32-bit address word in native byte order, for IPv6 too.
func procEndpoint(raw string) (netip.AddrPort, bool) {
	host, port, ok := strings.Cut(raw, ":")
	b, err := hex.DecodeString(host)
	if !ok || err != nil || (len(b) != 4 && len(b) != 16) {
		return netip.AddrPort{}, false
	}
	for i := 0; i < len(b); i += 4 {
		binary.BigEndian.PutUint32(b[i:i+4], binary.NativeEndian.Uint32(b[i:i+4]))
	}
	ip, ok := netip.AddrFromSlice(b)
	n, err := strconv.ParseUint(port, 16, 16)
	return netip.AddrPortFrom(ip.Unmap(), uint16(n)), ok && err == nil
}

// processOf walks the open file descriptors of every process for the socket
// inode and returns the process behind it.
func processOf(inode string) Process {
	target := "socket:[" + inode + "]"
	procs, err := os.ReadDir("/proc")
	if err != nil {
		return Process{}
	}
	var owner Process
	found := false
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
			if found {
				return Process{} // inherited/shared socket: no unique process
			}
			found = true
			if exe, err := os.Readlink(filepath.Join("/proc", pid, "exe")); err == nil {
				owner = Process{Name: filepath.Base(exe), Path: exe}
			} else if comm, err := os.ReadFile(filepath.Join("/proc", pid, "comm")); err == nil {
				owner = Process{Name: strings.TrimSpace(string(comm))}
			}
			break
		}
	}
	return owner
}
