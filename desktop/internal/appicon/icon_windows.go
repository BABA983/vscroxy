//go:build windows

package appicon

// lookup has no Windows implementation yet: the icon of an app lives in its
// executable's resources, which the UI shows as a badge for now.
func lookup(string) string { return "" }
