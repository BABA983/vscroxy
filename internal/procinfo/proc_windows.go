//go:build windows

package procinfo

import (
	"context"
	"net/netip"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"

	"golang.org/x/sys/windows"
)

func output(name string, args ...string) ([]byte, error) {
	ctx, cancel := context.WithTimeout(context.Background(), commandTimeout)
	defer cancel()
	return exec.CommandContext(ctx, name, args...).Output()
}

// lookupSocket requires both endpoints and a unique PID, for IPv4 and IPv6.
func lookupSocket(client, server netip.AddrPort) Process {
	out, err := output("netstat", "-ano")
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
	out, err := output("tasklist", "/FI", "PID eq "+pid, "/FO", "CSV", "/NH")
	if err != nil {
		return ""
	}
	name, _, _ := strings.Cut(strings.TrimSpace(string(out)), ",")
	return strings.TrimSuffix(strings.Trim(name, `"`), ".exe")
}

func lookupBatch(endpoints []Endpoint) []Process {
	out := make([]Process, len(endpoints))
	for i, endpoint := range endpoints {
		out[i] = Lookup(endpoint.Client, endpoint.Server)
	}
	return out
}
