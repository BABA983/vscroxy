//go:build !(linux || android || darwin || freebsd || openbsd || netbsd || dragonfly)

package system

import "os/exec"

// Other platforms (notably Windows) never resolve a login shell here.
func startedFromTerminal() bool { return false }

func setShellProcessGroup(*exec.Cmd) {}
