package app

import (
	"testing"

	"github.com/baba983/vscroxy/internal/capture"
)

// A helper process asks the desktop for its host app's icon, not the helper
// bundle's generic tile.
func TestHelperIconUsesTheOuterBundle(t *testing.T) {
	path := "/Applications/Spotify.app/Contents/Frameworks/Spotify Helper.app/Contents/MacOS/Spotify Helper"
	target, ok := appBundle(path)
	if !ok || target != "/Applications/Spotify.app" {
		t.Fatalf("icon target = %q, %v; want the host app", target, ok)
	}
	if _, ok := appBundle("/usr/libexec/nsurlsessiond"); ok {
		t.Fatal("a daemon outside every bundle was given an icon target")
	}
}

// A helper row with no path of its own still uses the next app's path, so
// the host app's icon is not skipped.
func TestFirstAppPathSkipsAnEmptyHelper(t *testing.T) {
	apps := []capture.SSLApp{{Name: "Spotify Helper"}, {Name: "Spotify", Path: "/Applications/Spotify.app"}}
	if got := firstAppPath(apps); got != "/Applications/Spotify.app" {
		t.Fatalf("firstAppPath = %q, want the host app path", got)
	}
}
