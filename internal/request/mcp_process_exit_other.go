//go:build unix && !linux && !darwin && !dragonfly && !freebsd && !netbsd && !openbsd

package request

import "os/exec"

// mcpWatchProcessExit has no process watch on this platform: shutdown relies
// on stdout EOF and the grace timers.
func mcpWatchProcessExit(*exec.Cmd, *mcpExitSignal) {}
