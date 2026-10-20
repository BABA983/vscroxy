//go:build darwin

package appicon

// lookup has no macOS implementation yet: the icon of an app lives in its
// bundle, which the UI shows as a badge for now.
func lookup(string) string { return "" }
