//go:build windows

package request

// The process-group tests skip on Windows; this keeps their helper compiling.
func mcpProcessAlive(int) bool { return false }
