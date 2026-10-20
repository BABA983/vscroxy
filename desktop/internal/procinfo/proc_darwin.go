//go:build darwin

package procinfo

import (
	"os/exec"
	"path/filepath"
	"strings"
)

// lookupPort asks lsof which process holds the connection whose local port is
// port, then ps for the path of its executable. The server side listens on the
// proxy port, so the match is the client.
func lookupPort(port string) Process {
	out, err := exec.Command("lsof", "-nP", "-iTCP:"+port, "-sTCP:ESTABLISHED", "-Fp").Output()
	if err != nil {
		return Process{}
	}
	for _, line := range strings.Split(string(out), "\n") {
		pid, ok := strings.CutPrefix(line, "p")
		if !ok || pid == "" {
			continue
		}
		comm, err := exec.Command("ps", "-p", pid, "-o", "comm=").Output()
		if err != nil {
			return Process{}
		}
		path := strings.TrimSpace(string(comm))
		if path == "" {
			return Process{}
		}
		return Process{Name: filepath.Base(path), Path: path}
	}
	return Process{}
}
