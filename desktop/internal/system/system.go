// Package system changes the machine's proxy settings and trust store, which
// a debugging proxy needs before a client will use it.
package system

import (
	"errors"
	"fmt"
	"os/exec"
	"strings"
)

// ProxyAddr is the address a client should use.
const ProxyAddr = "127.0.0.1:7890"

// splitAddr splits "host:port" as the dialogs give it.
func splitAddr(addr string) (host, port string, ok bool) {
	host, port, ok = strings.Cut(addr, ":")
	if !ok || host == "" || port == "" {
		return "", "", false
	}
	return host, port, true
}

// run executes a platform helper and turns its output into an error message.
func run(name string, args ...string) error {
	bin, err := exec.LookPath(name)
	if err != nil {
		return fmt.Errorf("%s not found", name)
	}
	out, err := exec.Command(bin, args...).CombinedOutput()
	if err != nil {
		msg := trim(string(out))
		if msg == "" {
			msg = err.Error()
		}
		return errors.New(msg)
	}
	return nil
}

func trim(s string) string {
	s = strings.TrimSpace(s)
	if len(s) > 200 {
		s = s[:200]
	}
	return s
}
