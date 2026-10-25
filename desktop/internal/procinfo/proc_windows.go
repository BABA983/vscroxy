//go:build windows

package procinfo

import (
	"net/netip"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"

	"golang.org/x/sys/windows"
)

// lookupSocket requires both endpoints and a unique PID, for IPv4 and IPv6.
func lookupSocket(client, server netip.AddrPort) Process {
	out, err := exec.Command("netstat", "-ano").Output()
	if err != nil {
		return Process{}
	}
	var owner string
	for line := range strings.SplitSeq(string(out), "\n") {
		fields := strings.Fields(line)
		if len(fields) < 5 || fields[0] != "TCP" || fields[3] != "ESTABLISHED" {
			continue
		}
		local, le := endpoint(fields[1])
		remote, re := endpoint(fields[2])
		if le == nil && re == nil && local == client && remote == server {
			if owner != "" && owner != fields[4] {
				return Process{}
			}
			owner = fields[4]
		}
	}
	pid, err := strconv.ParseUint(owner, 10, 32)
	if err != nil || pid == 0 {
		return Process{}
	}
	handle, err := windows.OpenProcess(windows.PROCESS_QUERY_LIMITED_INFORMATION, false, uint32(pid))
	if err != nil {
		return Process{Name: taskName(owner)}
	}
	defer windows.CloseHandle(handle)
	buf := make([]uint16, 32768)
	size := uint32(len(buf))
	if err := windows.QueryFullProcessImageName(handle, 0, &buf[0], &size); err != nil {
		return Process{Name: taskName(owner)}
	}
	path := windows.UTF16ToString(buf[:size])
	return Process{Name: strings.TrimSuffix(filepath.Base(path), ".exe"), Path: path}
}

func taskName(pid string) string {
	if _, err := strconv.Atoi(pid); err != nil {
		return ""
	}
	out, err := exec.Command("tasklist", "/FI", "PID eq "+pid, "/FO", "CSV", "/NH").Output()
	if err != nil {
		return ""
	}
	name, _, _ := strings.Cut(strings.TrimSpace(string(out)), ",")
	return strings.TrimSuffix(strings.Trim(name, `"`), ".exe")
}
