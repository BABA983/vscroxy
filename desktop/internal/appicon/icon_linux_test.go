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
	if err := os.MkdirAll(filepath.Dir(png), 0o755); err != nil {
		t.Fatalf("make icon dir: %v", err)
	}
	if err := os.WriteFile(png, []byte("png"), 0o644); err != nil {
		t.Fatalf("write icon: %v", err)
	}
	entry := "[Desktop Entry]\nType=Application\nName=Demo\nExec=/usr/bin/demo --flag %U\nIcon=demo\n"
	if err := os.WriteFile(filepath.Join(apps, "demo.desktop"), []byte(entry), 0o644); err != nil {
		t.Fatalf("write entry: %v", err)
	}

	desktopDirs, iconRoots = []string{apps}, []string{icons}
	if got := lookup("demo"); got != png {
		t.Errorf("lookup(demo) = %q, want %q", got, png)
	}
	if got := lookup("other"); got != "" {
		t.Errorf("lookup(other) = %q, want none", got)
	}
}

func TestLookupResolvesAnIconName(t *testing.T) {
	apps, icons := t.TempDir(), t.TempDir()
	svg := filepath.Join(icons, "hicolor", "scalable", "apps", "demo.svg")
	if err := os.MkdirAll(filepath.Dir(svg), 0o755); err != nil {
		t.Fatalf("make icon dir: %v", err)
	}
	if err := os.WriteFile(svg, []byte("<svg/>"), 0o644); err != nil {
		t.Fatalf("write icon: %v", err)
	}
	entry := "[Desktop Entry]\nExec=demo\nIcon=demo\n"
	if err := os.WriteFile(filepath.Join(apps, "demo.desktop"), []byte(entry), 0o644); err != nil {
		t.Fatalf("write entry: %v", err)
	}

	desktopDirs, iconRoots = []string{apps}, []string{icons}
	if got := lookup("demo"); got != svg {
		t.Errorf("lookup(demo) = %q, want %q", got, svg)
	}
}

func TestLookupUsesAnAbsoluteIconPath(t *testing.T) {
	apps := t.TempDir()
	icon := filepath.Join(apps, "demo.png")
	if err := os.WriteFile(icon, []byte("png"), 0o644); err != nil {
		t.Fatalf("write icon: %v", err)
	}
	entry := "[Desktop Entry]\nExec=demo\nIcon=" + icon + "\n"
	if err := os.WriteFile(filepath.Join(apps, "demo.desktop"), []byte(entry), 0o644); err != nil {
		t.Fatalf("write entry: %v", err)
	}

	desktopDirs, iconRoots = []string{apps}, nil
	if got := lookup("demo"); got != icon {
		t.Errorf("lookup(demo) = %q, want %q", got, icon)
	}
}
