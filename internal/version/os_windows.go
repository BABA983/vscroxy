//go:build windows

package version

import (
	"fmt"

	"golang.org/x/sys/windows"
)

func osVersion() string {
	info := windows.RtlGetVersion()
	if info == nil {
		return ""
	}
	text := fmt.Sprintf("%d.%d.%d", info.MajorVersion, info.MinorVersion, info.BuildNumber)
	if len(text) > 64 {
		return ""
	}
	return text
}
