package app

import "testing"

// A helper process asks the desktop for its host app's icon, not the helper
// bundle's generic tile.
func TestHelperIconUsesTheOuterBundle(t *testing.T) {
	path := "/Applications/Spotify.app/Contents/Frameworks/Spotify Helper.app/Contents/MacOS/Spotify Helper"
	target, ok := iconTarget(path)
	if !ok || target != "/Applications/Spotify.app" {
		t.Fatalf("icon target = %q, %v; want the host app", target, ok)
	}
	if _, ok := iconTarget("/usr/libexec/nsurlsessiond"); ok {
		t.Fatal("a daemon outside every bundle was given an icon target")
	}
}
