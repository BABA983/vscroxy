package main

import (
	"path/filepath"
	"testing"
)

func TestSettingsRoundTrip(t *testing.T) {
	path := filepath.Join(t.TempDir(), "settings.json")
	want := settings{
		Dark:         true,
		SidebarWidth: 264,
		TablePercent: 55,
		Columns:      columnLayout{Order: []string{"status", "url"}, Widths: map[string]float32{"url": 320}},
	}
	if err := writeSettings(path, want); err != nil {
		t.Fatalf("write: %v", err)
	}
	got, err := loadSettings(path)
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if !sameSettings(got, want) {
		t.Fatalf("loaded = %+v, want %+v", got, want)
	}
	if layout := got.Columns.layout(); layout.Widths["url"] != 320 || layout.Order[1] != "url" {
		t.Fatalf("layout = %+v", layout)
	}
}

func TestLoadSettingsWithoutAFile(t *testing.T) {
	got, err := loadSettings(filepath.Join(t.TempDir(), "missing.json"))
	if err != nil || !sameSettings(got, settings{}) {
		t.Fatalf("load = %+v, %v; want the defaults", got, err)
	}
}

func TestSameSettingsSeesColumnChanges(t *testing.T) {
	base := settings{Columns: columnLayout{Order: []string{"url"}, Widths: map[string]float32{"url": 300}}}
	moved := settings{Columns: columnLayout{Order: []string{"client", "url"}, Widths: map[string]float32{"url": 300}}}
	wider := settings{Columns: columnLayout{Order: []string{"url"}, Widths: map[string]float32{"url": 380}}}
	if sameSettings(base, moved) {
		t.Error("a moved column compared equal")
	}
	if sameSettings(base, wider) {
		t.Error("a wider column compared equal")
	}
	if !sameSettings(base, base) {
		t.Error("the same settings compared different")
	}
}
