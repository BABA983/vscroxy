//go:build darwin

package procinfo

import (
	"context"
	"encoding/binary"
	"golang.org/x/sys/unix"
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

// pcb entries are the private xinpcb_n/xsocket_n pair from
// net.inet.tcp.pcblist_n. The offsets are the stable part of that layout:
// xinpcb_n is followed by xsocket_n at 104, and the socket's last pid is at
// +68. The item size follows kern.osrelease, as in sing-box's Darwin finder.
const (
	pcbHeaderSize   = 24
	pcbSocketAt     = 104
	pcbLocalPort    = 18
	pcbVFlag        = 44
	pcbLocalAddr    = 64
	pcbRemoteAddr   = 48
	pcbIPv4Offset   = 12
	pcbLastPID      = 68
	pcbEffectivePID = 72
	pcbTCPExtra     = 208
)

var pcbItemSize = func() int {
	value, _ := unix.Sysctl("kern.osrelease")
	major, _, _ := strings.Cut(value, ".")
	n, _ := strconv.Atoi(major)
	if n >= 22 {
		return 408 + pcbTCPExtra
	}
	return 384 + pcbTCPExtra
}()

type pcbEntry struct {
	client, server netip.AddrPort
	pid            uint32
}

func lookupPCBBatch(endpoints []Endpoint) []Process {
	out := make([]Process, len(endpoints))
	if len(endpoints) == 0 {
		return out
	}
	data, err := unix.SysctlRaw("net.inet.tcp.pcblist_n")
	if err != nil {
		return out
	}
	entries := parsePCBs(data)
	for i, endpoint := range endpoints {
		client, ok := loopbackEndpoint(endpoint.Client)
		server, ok2 := loopbackEndpoint(endpoint.Server)
		if !ok || !ok2 {
			continue
		}
		for _, entry := range entries {
			if entry.client == client && entry.server == server && entry.pid != 0 {
				out[i] = processForPID(strconv.FormatUint(uint64(entry.pid), 10))
				break
			}
		}
	}
	return out
}

func lookupPCB(client, server netip.AddrPort) Process {
	data, err := unix.SysctlRaw("net.inet.tcp.pcblist_n")
	if err != nil {
		return Process{}
	}
	for _, entry := range parsePCBs(data) {
		if entry.client == client && entry.server == server && entry.pid != 0 {
			return processForPID(strconv.FormatUint(uint64(entry.pid), 10))
		}
	}
	return Process{}
}

func parsePCBs(data []byte) []pcbEntry {
	entries := make([]pcbEntry, 0, (len(data)-pcbHeaderSize)/pcbItemSize)
	for i := pcbHeaderSize; i+pcbItemSize <= len(data); i += pcbItemSize {
		inp := i
		so := i + pcbSocketAt
		if so+pcbEffectivePID+4 > i+pcbItemSize {
			continue
		}
		pid := binary.NativeEndian.Uint32(data[so+pcbEffectivePID : so+pcbEffectivePID+4])
		if pid == 0 {
			pid = binary.NativeEndian.Uint32(data[so+pcbLastPID : so+pcbLastPID+4])
		}
		if pid == 0 {
			continue
		}
		localPort := binary.BigEndian.Uint16(data[inp+pcbLocalPort : inp+pcbLocalPort+2])
		remotePort := binary.BigEndian.Uint16(data[inp+pcbLocalPort-2 : inp+pcbLocalPort])
		var local, remote netip.Addr
		switch flag := data[inp+pcbVFlag]; {
		case flag&0x1 != 0:
			local = netip.AddrFrom4([4]byte(data[inp+pcbLocalAddr+pcbIPv4Offset : inp+pcbLocalAddr+pcbIPv4Offset+4]))
			remote = netip.AddrFrom4([4]byte(data[inp+pcbRemoteAddr+pcbIPv4Offset : inp+pcbRemoteAddr+pcbIPv4Offset+4]))
		case flag&0x2 != 0:
			local = netip.AddrFrom16([16]byte(data[inp+pcbLocalAddr : inp+pcbLocalAddr+16]))
			remote = netip.AddrFrom16([16]byte(data[inp+pcbRemoteAddr : inp+pcbRemoteAddr+16]))
		default:
			continue
		}
		entries = append(entries, pcbEntry{
			client: netip.AddrPortFrom(local.Unmap(), localPort),
			server: netip.AddrPortFrom(remote.Unmap(), remotePort),
			pid:    pid,
		})
	}
	return entries
}

// lookupSocket asks lsof for one exact connection, for a single retry after
// the batch missed it.
func lookupSocket(client, server netip.AddrPort) Process {
	if process := lookupPCB(client, server); process.Name != "" {
		return process
	}
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
	out := lookupPCBBatch(endpoints)
	missing := false
	for _, process := range out {
		if process.Name == "" {
			missing = true
			break
		}
	}
	if !missing {
		return out
	}
	// A socket can disappear between accept and this read. Fall back to lsof
	// for the entries the PCB table did not answer.
	lsof := lookupLsofBatch(endpoints)
	for i := range out {
		if out[i].Name == "" {
			out[i] = lsof[i]
		}
	}
	return out
}

func lookupLsofBatch(endpoints []Endpoint) []Process {
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
