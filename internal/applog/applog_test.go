package applog

import (
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

func openTestLog(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	previous := slog.Default()
	logs, err := Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_ = logs.Close()
		slog.SetDefault(previous)
		levels.Set(slog.LevelInfo)
		flagLevel.Store(false)
	})
	return filepath.Join(dir, fileName)
}

func TestLevelChangeFiltersTheNextRecordInTheOpenFile(t *testing.T) {
	path := openTestLog(t)
	For("test").Info("before")
	if err := SetLevel(LevelError); err != nil {
		t.Fatal(err)
	}
	For("test").Info("filtered")
	For("test").Error("after")

	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	text := string(data)
	if !strings.Contains(text, "msg=before") || !strings.Contains(text, "msg=after") {
		t.Fatalf("records around the level change missing: %q", text)
	}
	if strings.Contains(text, "filtered") {
		t.Fatalf("info record written after the change: %q", text)
	}
	if !strings.Contains(text, "component=test") {
		t.Fatalf("component attribute missing: %q", text)
	}
}

func TestOffWritesNothing(t *testing.T) {
	path := openTestLog(t)
	if err := SetLevel(LevelOff); err != nil {
		t.Fatal(err)
	}
	if got := EffectiveLevel(); got != LevelOff {
		t.Fatalf("effective level = %q, want %q", got, LevelOff)
	}
	For("test").Debug("hidden debug")
	For("test").Info("hidden info")
	For("test").Error("hidden error")

	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if len(data) != 0 {
		t.Fatalf("off still wrote %q", data)
	}
}

func TestFlagLevelWinsOverSavedLevelForThisRun(t *testing.T) {
	path := openTestLog(t)
	if err := SetFlagLevel("DEBUG"); err != nil {
		t.Fatal(err)
	}
	if err := SetLevel(LevelError); err != nil { // the saved setting
		t.Fatal(err)
	}
	if got := EffectiveLevel(); got != LevelDebug {
		t.Fatalf("effective level = %q, want %q", got, LevelDebug)
	}
	if !FlagLevelOverridden() {
		t.Fatal("the flag is not marked as this run's override")
	}
	For("test").Debug("from the flag")

	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(data), "msg=\"from the flag\"") {
		t.Fatalf("debug record suppressed by the flag: %q", data)
	}
}

func TestEveryLevelNameRoundTrips(t *testing.T) {
	for _, name := range LevelNames() {
		if err := SetLevel(name); err != nil {
			t.Errorf("SetLevel(%q): %v", name, err)
			continue
		}
		if got := EffectiveLevel(); got != name {
			t.Errorf("SetLevel(%q) then EffectiveLevel() = %q", name, got)
		}
	}
	if err := SetLevel("loud"); err == nil {
		t.Error("an unknown level was accepted")
	}
}

func TestRotationKeepsTheNewestLinesInFiveFiles(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, fileName)
	r := &rotating{path: path, limit: 100}
	if err := r.open(); err != nil {
		t.Fatal(err)
	}
	// 30-byte lines: three fit under the limit, the fourth rotates, so the
	// files hold 39, 36–38, 33–35, 30–32 and 27–29.
	for i := range 40 {
		if _, err := fmt.Fprintf(r, "line %02d %s\n", i, strings.Repeat("x", 21)); err != nil {
			t.Fatal(err)
		}
	}
	r.Close()
	entries, _ := os.ReadDir(dir)
	var names []string
	for _, e := range entries {
		names = append(names, e.Name())
	}
	if want := []string{"vscroxy.log", "vscroxy.log.1", "vscroxy.log.2", "vscroxy.log.3", "vscroxy.log.4"}; !slices.Equal(names, want) {
		t.Fatalf("files = %q, want %q", names, want)
	}
	current, _ := os.ReadFile(path)
	oldest, _ := os.ReadFile(path + ".4")
	if !strings.HasPrefix(string(current), "line 39") || !strings.HasPrefix(string(oldest), "line 27") {
		t.Fatalf("current %q, oldest %q", current, oldest)
	}
	for _, name := range names {
		if info, _ := os.Stat(filepath.Join(dir, name)); info.Size() > 100 {
			t.Errorf("%s has %d bytes, over the limit", name, info.Size())
		}
	}
}
