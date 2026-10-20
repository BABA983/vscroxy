//go:build darwin

package procinfo

import (
	"os/exec"
	"strings"
)

// lookupPort asks lsof which process holds the connection whose local port is
// port. The server side listens on the proxy port, so the match is the client.
func lookupPort(port string) string {
	out, err := exec.Command("lsof", "-nP", "-iTCP:"+port, "-sTCP:ESTABLISHED", "-Fc").Output()
	if err != nil {
		return ""
	}
	for _, line := range strings.Split(string(out), "\n") {
		if name, ok := strings.CutPrefix(line, "c"); ok && name != "" {
			return name
		}
	}
	return ""
}
