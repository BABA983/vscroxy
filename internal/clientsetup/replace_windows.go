//go:build windows

package clientsetup

import "golang.org/x/sys/windows"

// replaceFile uses MoveFileEx with MOVEFILE_REPLACE_EXISTING; os.Rename on
// Windows fails when the destination already exists.
func replaceFile(source, destination string) error {
	return windows.Rename(source, destination)
}
