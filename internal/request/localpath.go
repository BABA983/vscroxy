package request

import (
	"fmt"
	"strings"
)

// localRequestPath rejects paths that would make Windows open a file through
// SMB or a device namespace. Imported collections can supply these paths; a
// local path is the only kind a request may read. The check is the same on
// every platform, so a collection cannot behave differently on a developer's
// machine and on a user's Windows desktop.
func localRequestPath(path, what string) (string, error) {
	// Windows treats both separators alike, including when they are mixed.
	normalized := strings.ReplaceAll(strings.TrimSpace(path), "/", `\`)
	// Win32 also accepts the NT namespace prefix \??\, which can name the
	// same file and network share as \\?\.
	if strings.HasPrefix(normalized, `\\`) || strings.HasPrefix(normalized, `\??\`) {
		return "", fmt.Errorf("%s: UNC and device paths are not allowed", what)
	}
	return path, nil
}
