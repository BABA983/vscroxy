//go:build linux

package appicon

import (
	"os"
	"path/filepath"
	"testing"
)

func TestLookupFindsTheEntryIcon(t *testing.T) {
	apps, icons := t.TempDir(), t.TempDir()
	png := filepath.Join(icons, "hicolor", "64x64", "apps", "demo.png")
	write(t, png, "entry-icon")
	entry := "[Desktop Entry]\nType=Application\nName=Demo\nExec=/usr/bin/demo --flag %U\nIcon=demo\n"
	write(t, filepath.Join(apps, "demo.desktop"), entry)

	desktopDirs, iconRoots = []string{apps}, []string{icons}
	if got, ok := lookup("demo", "/usr/bin/demo"); !ok || string(got.Data) != "entry-icon" {
		t.Errorf("lookup(demo) = %q, %v; want the entry's icon", got.Data, ok)
	}
	if _, ok := lookup("other", "/usr/bin/other"); ok {
		t.Error("lookup(other) found an icon, want none")
	}
}

func TestLookupResolvesAnIconName(t *testing.T) {
	apps, icons := t.TempDir(), t.TempDir()
	write(t, filepath.Join(icons, "hicolor", "scalable", "apps", "demo.svg"), "<svg/>")
	write(t, filepath.Join(apps, "demo.desktop"), "[Desktop Entry]\nExec=demo\nIcon=demo\n")

	desktopDirs, iconRoots = []string{apps}, []string{icons}
	got, ok := lookup("demo", "/usr/bin/demo")
	if !ok || !got.SVG || string(got.Data) != "<svg/>" {
		t.Errorf("lookup(demo) = %+v, %v; want the SVG", got, ok)
	}
}

func TestLookupFallsBackToTheExecutableDirectory(t *testing.T) {
	// An app without a .desktop entry, as a bundled Chrome, keeps its logo
	// beside the executable.
	bin := t.TempDir()
	write(t, filepath.Join(bin, "product_logo_48.png"), "logo")
	desktopDirs, iconRoots = nil, nil

	got, ok := lookup("chrome", filepath.Join(bin, "chrome"))
	if !ok || string(got.Data) != "logo" {
		t.Errorf("lookup(chrome) = %q, %v; want the logo beside the executable", got.Data, ok)
	}
}

func TestLookupUsesAnAbsoluteIconPath(t *testing.T) {
	apps := t.TempDir()
	icon := filepath.Join(apps, "demo.png")
	write(t, icon, "absolute")
	write(t, filepath.Join(apps, "demo.desktop"), "[Desktop Entry]\nExec=demo\nIcon="+icon+"\n")

	desktopDirs, iconRoots = []string{apps}, nil
	if got, ok := lookup("demo", "/usr/bin/demo"); !ok || string(got.Data) != "absolute" {
		t.Errorf("lookup(demo) = %q, %v; want the absolute icon", got.Data, ok)
	}
}

func write(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatalf("make dir: %v", err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatalf("write %s: %v", path, err)
	}
}
