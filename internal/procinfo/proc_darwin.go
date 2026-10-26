//go:build darwin

package procinfo

import (
	"net/netip"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
)

// lookupSocket asks lsof for both endpoints and rejects non-unique owners.
func lookupSocket(client, server netip.AddrPort) Process {
	out, err := exec.Command("lsof", "-nP", "-a", "-iTCP:"+strconv.Itoa(int(client.Port())), "-sTCP:ESTABLISHED", "-Fpn").Output()
	if err != nil {
		return Process{}
	}
	var pid, owner string
	for line := range strings.SplitSeq(string(out), "\n") {
		if value, ok := strings.CutPrefix(line, "p"); ok {
			pid = value
			continue
		}
		if value, ok := strings.CutPrefix(line, "n"); ok {
			local, remote, ok := strings.Cut(value, "->")
			l, le := endpoint(local)
			r, re := endpoint(remote)
			if !ok || le != nil || re != nil || l != client || r != server || pid == "" {
				continue
			}
			if owner != "" && owner != pid {
				return Process{}
			}
			owner = pid
		}
	}
	if owner == "" {
		return Process{}
	}
	comm, err := exec.Command("ps", "-p", owner, "-o", "comm=").Output()
	path := strings.TrimSpace(string(comm))
	if err != nil || !filepath.IsAbs(path) {
		return Process{}
	}
	return Process{Name: filepath.Base(path), Path: path}
}
