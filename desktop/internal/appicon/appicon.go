// Package appicon finds the icon of the app behind a process, so the traffic
// table can show it next to the client's name.
package appicon

// Lookup returns the path of the icon for the process named process, or "" when
// there is none.
func Lookup(process string) string {
	if process == "" {
		return ""
	}
	return lookup(process)
}
