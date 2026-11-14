//go:build darwin

package procinfo

import (
	"context"
	"net/netip"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"
)

const (
	commandTimeout = 2 * time.Second
	processTTL     = 5 * time.Second
)

func output(name string, args ...string) ([]byte, error) {
	ctx, cancel := context.WithTimeout(context.Background(), commandTimeout)
	defer cancel()
	return exec.CommandContext(ctx, name, args...).Output()
}

// lookupSocket asks lsof for one exact connection, for a single retry after
// the batch missed it.
func lookupSocket(client, server netip.AddrPort) Process {
	out, err := output("lsof", "-nP", "-a", "-iTCP:"+strconv.Itoa(int(client.Port())), "-Fpn")
	if err != nil {
		return Process{}
	}
	pid, owner := "", ""
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
	return processForPID(owner)
}

// lookupBatch reads every loopback TCP socket in one lsof pass, then divides
// the owners by exact four-tuple. One scan serves the burst Chrome opens at
// startup instead of one lsof process per connection.
func lookupBatch(endpoints []Endpoint) []Process {
	out := make([]Process, len(endpoints))
	if len(endpoints) == 0 {
		return out
	}
	data, err := output("lsof", "-nP", "-iTCP", "-sTCP:ESTABLISHED,CLOSE_WAIT", "-a", "-i@127.0.0.1", "-Fpn")
	if err != nil {
		return out
	}
	owners := map[string]string{}
	pid := ""
	for line := range strings.SplitSeq(string(data), "\n") {
		if value, ok := strings.CutPrefix(line, "p"); ok {
			pid = value
			continue
		}
		value, ok := strings.CutPrefix(line, "n")
		if !ok {
			continue
		}
		local, remote, ok := strings.Cut(value, "->")
		if !ok || pid == "" {
			continue
		}
		l, le := endpoint(local)
		r, re := endpoint(remote)
		if le != nil || re != nil || !l.Addr().IsLoopback() || !r.Addr().IsLoopback() {
			continue
		}
		key := l.String() + "->" + r.String()
		if previous, seen := owners[key]; seen && previous != pid {
			owners[key] = "" // ambiguous: do not borrow either owner
			continue
		}
		owners[key] = pid
	}
	for i, endpoint := range endpoints {
		client, ok := loopbackEndpoint(endpoint.Client)
		server, ok2 := loopbackEndpoint(endpoint.Server)
		if !ok || !ok2 {
			continue
		}
		if pid := owners[client.String()+"->"+server.String()]; pid != "" {
			out[i] = processForPID(pid)
		}
	}
	return out
}

type cachedProcess struct {
	process Process
	at      time.Time
}

var processCache struct {
	sync.Mutex
	entries map[string]cachedProcess
}

// processForPID keeps ps and lsof -p off the burst: a helper process usually
// owns many sockets, and its name and executable do not change between them.
func processForPID(pid string) Process {
	processCache.Lock()
	if cached, ok := processCache.entries[pid]; ok && time.Since(cached.at) < processTTL {
		processCache.Unlock()
		return cached.process
	}
	processCache.Unlock()

	process := lookupProcess(pid)

	processCache.Lock()
	if processCache.entries == nil {
		processCache.entries = map[string]cachedProcess{}
	}
	processCache.entries[pid] = cachedProcess{process: process, at: time.Now()}
	processCache.Unlock()
	return process
}

func lookupProcess(pid string) Process {
	comm, err := output("ps", "-p", pid, "-o", "comm=")
	if err != nil {
		return Process{}
	}
	return executable(pid, strings.TrimSpace(string(comm)))
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
