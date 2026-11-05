//go:build !windows

package clientsetup

import "os/exec"

func configureStart(*exec.Cmd, bool) {}
