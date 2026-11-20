// Package version says which vscroxy this is. It depends only on the standard
// library, so the desktop app, the CLI and the MCP server share it.
package version

import (
	"runtime"
	"runtime/debug"
	"strings"
)

// Number is the release number. It is the version mygo.json gives the app; a
// test keeps the two equal.
const Number = "0.1.0"

// Commit is the commit the build came from. A release build sets it with
// -ldflags "-X github.com/baba983/vscroxy/internal/version.Commit=<sha>";
// otherwise it comes from the VCS information Go embeds.
var Commit string

// commit returns the short commit and whether the tree had uncommitted
// changes, or "" when the build carries no VCS information.
func commit() (string, bool) {
	if Commit != "" {
		return Commit, strings.HasSuffix(Commit, "-dirty")
	}
	info, ok := debug.ReadBuildInfo()
	if !ok {
		return "", false
	}
	var revision, modified string
	for _, setting := range info.Settings {
		switch setting.Key {
		case "vcs.revision":
			revision = setting.Value
		case "vcs.modified":
			modified = setting.Value
		}
	}
	if revision == "" {
		return "", false
	}
	if len(revision) > 7 {
		revision = revision[:7]
	}
	return revision, modified == "true"
}

// CommitString is the short commit, with "+dirty" for a build from a modified
// tree, or "" when the build carries no VCS information.
func CommitString() string {
	revision, dirty := commit()
	if revision == "" {
		return ""
	}
	if dirty && !strings.HasSuffix(revision, "-dirty") {
		revision += "+dirty"
	}
	return revision
}

// String is the version and, when the build differs from it, the commit or
// build number: "0.1.0 (abc1234)". A build stamped with the release number
// is not shown twice.
func String() string {
	if revision := CommitString(); revision != "" && revision != Number {
		return Number + " (" + revision + ")"
	}
	return Number
}

// Platform is "darwin/arm64", the OS and processor the binary runs on.
func Platform() string {
	return runtime.GOOS + "/" + runtime.GOARCH
}
