package app

import (
	"path/filepath"
	"testing"

	"github.com/baba983/vscroxy/internal/applog"
	"github.com/baba983/vscroxy/locales"
	"github.com/egoist/mygo/ui"
)

func restoreLogLevel(t *testing.T) {
	t.Helper()
	level := applog.EffectiveLevel()
	t.Cleanup(func() { _ = applog.SetLevel(level) })
}

func TestLoadSettingsReadsTheSavedLogLevel(t *testing.T) {
	path := filepath.Join(t.TempDir(), "settings.json")
	if err := writeSettings(path, settings{LogLevel: applog.LevelWarning}, ""); err != nil {
		t.Fatal(err)
	}
	loaded, err := loadSettings(path)
	if err != nil {
		t.Fatal(err)
	}
	if loaded.LogLevel != applog.LevelWarning {
		t.Fatalf("saved log level = %q, want %q", loaded.LogLevel, applog.LevelWarning)
	}
}

func TestSetLogLevelAppliesAtOnceAndSavesTheChoice(t *testing.T) {
	restoreLogLevel(t)
	path := filepath.Join(t.TempDir(), "settings.json")
	a := &app{settingsPath: path}

	a.setLogLevel(applog.LevelDebug)

	if got := applog.EffectiveLevel(); got != applog.LevelDebug {
		t.Fatalf("effective level = %q, want %q", got, applog.LevelDebug)
	}
	if a.saved.LogLevel != applog.LevelDebug {
		t.Fatalf("saved level = %q, want %q", a.saved.LogLevel, applog.LevelDebug)
	}
	saved, err := loadSettings(path)
	if err != nil {
		t.Fatal(err)
	}
	if saved.LogLevel != applog.LevelDebug {
		t.Fatalf("settings.json level = %q, want %q", saved.LogLevel, applog.LevelDebug)
	}
}

func TestSetLogLevelReportsErrorsAndKeepsTheEffectiveLevel(t *testing.T) {
	restoreLogLevel(t)
	before := applog.EffectiveLevel()
	a := workspaceApp()
	a.settingsPath = t.TempDir() // a directory cannot be replaced by settings.json

	a.setLogLevel("loud")
	if len(a.notices) == 0 || a.notices[len(a.notices)-1].level != noticeError {
		t.Fatal("an unknown level was not reported")
	}
	if got := applog.EffectiveLevel(); got != before {
		t.Fatalf("unknown level changed the log to %q", got)
	}

	a.setLogLevel(applog.LevelDebug)
	if len(a.notices) != 2 || a.notices[len(a.notices)-1].level != noticeError {
		t.Fatal("a failed settings write was not reported")
	}
	if got := applog.EffectiveLevel(); got != before {
		t.Fatalf("failed write left the log at %q, want %q", got, before)
	}
	if a.saved.LogLevel != "" {
		t.Fatalf("failed write kept unsaved level %q", a.saved.LogLevel)
	}
}

func TestSetLogLevelCommandOffersThePicker(t *testing.T) {
	restoreLogLevel(t)
	a := workspaceApp()
	current := applog.EffectiveLevel()

	command := pick(a.actions(nil), "workbench.action.setLogLevel")
	if command.id == "" || !command.enabled {
		t.Fatal("the command registry has no enabled workbench.action.setLogLevel")
	}
	command.run()
	if !a.logLevelPickerOpen || a.logLevelPicker != current {
		t.Fatalf("picker open=%t level=%q, want open with %q", a.logLevelPickerOpen, a.logLevelPicker, current)
	}

	tt := ui.NewTester(a.view, 1280, 800)
	tt.Frame()
	if !tt.HasText(locales.SetLogLevel()) || !tt.HasText(locales.LogLevelHint()) {
		t.Fatal("the command's level picker did not render")
	}
}
