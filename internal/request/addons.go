package request

import (
	"fmt"
	"io/fs"
	"path/filepath"
	"slices"
	"strings"

	"github.com/baba983/vscroxy/internal/capture"
)

// An addon is the user's module, a name.js file at any depth of the addons
// folder. Its name is the path without .js, with / separators: @team/pkg is
// @team/pkg.js, and scripts require it as require("@team/pkg").

// AddonNames lists the valid addon names under dir. A missing dir is an error
// wrapping fs.ErrNotExist; files whose names CheckAddonName refuses are left out.
func AddonNames(dir string) ([]string, error) {
	var names []string
	err := filepath.WalkDir(dir, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() || !strings.HasSuffix(path, ".js") {
			return nil
		}
		rel, err := filepath.Rel(dir, path)
		if err != nil {
			return err
		}
		if name := strings.TrimSuffix(filepath.ToSlash(rel), ".js"); CheckAddonName(name) == nil {
			names = append(names, name)
		}
		return nil
	})
	return names, err
}

// CheckAddonName refuses a name a library require already loads, and a name
// with a part that breaks the script name rules, such as an empty folder.
func CheckAddonName(name string) error {
	if slices.Contains(Libraries, name) {
		return fmt.Errorf("require(%q) loads the built-in library", name)
	}
	for _, part := range strings.Split(name, "/") {
		if err := capture.CheckScriptName(part); err != nil {
			return err
		}
	}
	return nil
}
