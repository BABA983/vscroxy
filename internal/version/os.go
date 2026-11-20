package version

// The OS version is a short product or kernel version. Reading it is bounded
// and safe to call from a worker; it carries no machine name, user name or
// path. An empty result means the system could not answer.

import (
	"context"
	"os/exec"
	"strings"
	"time"
)

// osCommandTimeout is the same bound the local process lookups use for their
// platform commands, so a hung helper cannot hold up the caller.
const osCommandTimeout = 2 * time.Second

// OSVersion reports the installed OS version, or "" when unavailable.
func OSVersion() string { return osVersion() }

func helper(name string, args ...string) string {
	ctx, cancel := context.WithTimeout(context.Background(), osCommandTimeout)
	defer cancel()
	bin, err := exec.LookPath(name)
	if err != nil {
		return ""
	}
	data, err := exec.CommandContext(ctx, bin, args...).Output()
	if err != nil {
		return ""
	}
	value := strings.TrimSpace(string(data))
	if len(value) > 64 || strings.ContainsAny(value, "\n\r\\") {
		return ""
	}
	return value
}
