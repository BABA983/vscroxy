//go:build windows

package appicon

import (
	"bytes"
	"image/png"
	"os"
	"path/filepath"
	"testing"
)

func TestLookupReadsAnExecutableIcon(t *testing.T) {
	path := filepath.Join(os.Getenv("SystemRoot"), "System32", "notepad.exe")
	if _, err := os.Stat(path); err != nil {
		t.Skip("notepad.exe is not present")
	}
	icon, ok := Lookup("notepad", path)
	if !ok || len(icon.Data) == 0 {
		t.Fatal("notepad.exe has no readable icon")
	}
	if _, err := png.Decode(bytes.NewReader(icon.Data)); err != nil {
		t.Fatalf("the icon is not a PNG: %v", err)
	}
	if _, ok := Lookup("missing", filepath.Join(os.TempDir(), "vscroxy-no-such-app.exe")); ok {
		t.Fatal("a missing executable returned an icon")
	}
}
