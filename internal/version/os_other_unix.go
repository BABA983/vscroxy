//go:build !darwin && !windows

package version

func osVersion() string { return helper("uname", "-r") }
