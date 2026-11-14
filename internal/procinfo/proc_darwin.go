//go:build darwin

package procinfo

import (
	"context"
	"net/netip"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

const commandTimeout = 500 * time.Millisecond

func output(name string, args ...string) ([]byte, error) {
	ctx, cancel := context.WithTimeout(context.Background(), commandTimeout)
	defer cancel()
	return exec.CommandContext(ctx, name, args...).Output()
}

// lookupSocket asks lsof for both endpoints and rejects non-unique owners.
func lookupSocket(client, server netip.AddrPort) Process {
	out, err := output("lsof", "-nP", "-a", "-iTCP:"+strconv.Itoa(int(client.Port())), "-Fpn")
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
	comm, err := output("ps", "-p", owner, "-o", "comm=")
	if err != nil {
		return Process{}
	}
	return executable(owner, strings.TrimSpace(string(comm)))
}

// executable turns ps's command column into a process. ps prints a full path
// for a process it can name that way, and the bare command name for one it
// cannot — curl is "curl", not "/usr/bin/curl" — so a relative name is
// resolved through the text file lsof lists for the process.
func executable(pid, comm string) Process {
	if filepath.IsAbs(comm) {
		return Process{Name: filepath.Base(comm), Path: comm}
	}
	name := filepath.Base(comm)
	out, err := output("lsof", "-nP", "-p", pid, "-a", "-d", "txt", "-Fn")
	if err == nil {
		for line := range strings.SplitSeq(string(out), "\n") {
			if path, ok := strings.CutPrefix(line, "n"); ok && filepath.IsAbs(path) {
				return Process{Name: filepath.Base(path), Path: path}
			}
		}
	}
	// A name without a path still names the app; only its icon is missing.
	return Process{Name: name}
}
