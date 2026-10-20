//go:build windows

package procinfo

import (
	"os/exec"
	"strconv"
	"strings"
)

// lookupPort reads the process id netstat reports for the connection whose
// local port is port, then asks tasklist for that process's name.
func lookupPort(port string) Process {
	out, err := exec.Command("netstat", "-ano", "-p", "TCP").Output()
	if err != nil {
		return Process{}
	}
	suffix := ":" + port
	for _, line := range strings.Split(string(out), "\n") {
		fields := strings.Fields(line)
		if len(fields) < 5 || fields[3] != "ESTABLISHED" {
			continue
		}
		if strings.HasSuffix(fields[1], suffix) {
			return Process{Name: taskName(fields[4])}
		}
	}
	return Process{}
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
