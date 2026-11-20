//go:build darwin

package version

func osVersion() string { return helper("sw_vers", "-productVersion") }
