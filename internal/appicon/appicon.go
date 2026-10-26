// Package appicon finds the icon of the app behind a process, so the traffic
// table can show it next to the app's name.
package appicon

import (
	"os"
	"strings"
)

// Icon is an app's icon: a PNG bitmap, or an SVG in its own colors.
type Icon struct {
	Data []byte
	SVG  bool
}

// Lookup returns the icon of the process named name, whose executable is at
// path, or false when it has none.
func Lookup(name, path string) (Icon, bool) {
	if name == "" && path == "" {
		return Icon{}, false
	}
	return lookup(name, path)
}

// read returns the icon a file holds, an SVG when its name says so.
func read(path string) (Icon, bool) {
	data, err := os.ReadFile(path)
	if err != nil || len(data) == 0 {
		return Icon{}, false
	}
	return Icon{Data: data, SVG: strings.HasSuffix(strings.ToLower(path), ".svg")}, true
}
