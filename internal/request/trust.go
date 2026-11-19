package request

import (
	"context"
	"strings"
)

// ProjectScriptPermission decides whether scripts from dir may run. A nil
// permission allows them: callers that keep their own library have nothing to
// ask. Sends hold the decision before any script runs, so a project asked once
// keeps that answer for pre-request, post-response and nested scripts alike.
type ProjectScriptPermission func(ctx context.Context, dir string) bool

// hasScripts reports whether this send would run any code. An untrusted
// project must be asked only when a script would actually execute, so a plain
// request never opens a trust dialog.
func hasScripts(r Request, folders []Folder) bool {
	for _, src := range []string{r.Pre, r.Post} {
		if strings.TrimSpace(src) != "" {
			return true
		}
	}
	for _, f := range folders {
		if strings.TrimSpace(f.Pre) != "" || strings.TrimSpace(f.Post) != "" {
			return true
		}
	}
	return false
}
