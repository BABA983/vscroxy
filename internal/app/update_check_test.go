package app

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/baba983/vscroxy/internal/update"
	"github.com/baba983/vscroxy/internal/version"
	"github.com/baba983/vscroxy/locales"
	"github.com/egoist/mygo/ui"
)

type testConnectionMeter bool

func (m testConnectionMeter) metered() bool { return bool(m) }

type testLatestRelease struct {
	calls   int
	release update.Release
	err     error
}

func (l *testLatestRelease) get(context.Context) (update.Release, error) {
	l.calls++
	return l.release, l.err
}

func TestOfferUpdateOnlyForANewerRelease(t *testing.T) {
	a := workspaceApp()
	a.offerUpdate(update.Release{Tag: "v" + version.Number, URL: "https://example.com/old"})
	if len(a.notices) != 0 {
		t.Fatalf("the running version was offered: %+v", a.notices)
	}
	a.offerUpdate(update.Release{Tag: "v0.2.0", URL: "https://example.com/new"})
	if len(a.notices) != 1 {
		t.Fatalf("notices = %+v", a.notices)
	}
	n := a.notices[0]
	if !n.sticky || !strings.Contains(n.text, "0.2.0") || n.action != "" {
		t.Fatalf("notice = %+v", n)
	}
}

func TestUpdateCheckSettingPersists(t *testing.T) {
	path := filepath.Join(t.TempDir(), "settings.json")
	if err := writeSettings(path, settings{DisableUpdateCheck: true}, ""); err != nil {
		t.Fatal(err)
	}
	got, err := loadSettings(path)
	if err != nil || !got.DisableUpdateCheck {
		t.Fatalf("settings = %+v, %v", got, err)
	}
}

func TestBackgroundUpdateCheckSkipsMeteredConnections(t *testing.T) {
	a := workspaceApp()
	latest := &testLatestRelease{release: update.Release{Tag: "v999.0.0", URL: "https://example.com/new"}}
	a.checkForUpdateWith(false, testConnectionMeter(true), latest.get)
	if latest.calls != 0 {
		t.Fatalf("the background check ran %d times on a metered connection", latest.calls)
	}
	if len(a.notices) != 0 || a.message != "" {
		t.Fatalf("a skipped check showed feedback: %+v, %q", a.notices, a.message)
	}
}

func TestBackgroundUpdateCheckIsSilentWithoutAnUpdate(t *testing.T) {
	for _, tt := range []struct {
		name    string
		release update.Release
		err     error
	}{
		{"up to date", update.Release{Tag: "v" + version.Number}, nil},
		{"failure", update.Release{}, errors.New("network down")},
	} {
		t.Run(tt.name, func(t *testing.T) {
			a := workspaceApp()
			latest := &testLatestRelease{release: tt.release, err: tt.err}
			a.checkForUpdateWith(false, testConnectionMeter(false), latest.get)
			if latest.calls != 1 {
				t.Fatalf("latest calls = %d, want 1", latest.calls)
			}
			if len(a.notices) != 0 || a.message != "" {
				t.Fatalf("background feedback = %+v, %q", a.notices, a.message)
			}
		})
	}
}

func TestBackgroundUpdateCheckShowsOnlyANewerRelease(t *testing.T) {
	a := workspaceApp()
	latest := &testLatestRelease{release: update.Release{Tag: "v999.0.0", URL: "https://example.com/new"}}
	a.checkForUpdateWith(false, testConnectionMeter(false), latest.get)
	if latest.calls != 1 || len(a.notices) != 1 {
		t.Fatalf("calls = %d, notices = %+v", latest.calls, a.notices)
	}
	if n := a.notices[0]; n.level != noticeInfo || !n.sticky || !strings.Contains(n.text, "999.0.0") {
		t.Fatalf("notice = %+v", n)
	}
}

func TestManualUpdateCheckRunsWhenMeteredAndReportsTheOutcome(t *testing.T) {
	for _, tt := range []struct {
		name       string
		release    update.Release
		err        error
		wantLevel  noticeLevel
		wantText   string
		wantDetail string
	}{
		{"up to date", update.Release{Tag: "v" + version.Number}, nil,
			noticeSuccess, locales.UpToDate(), ""},
		{"available", update.Release{Tag: "v999.0.0", URL: "https://example.com/new"}, nil,
			noticeInfo, locales.UpdateAvailable("999.0.0"), locales.UpdateAvailableHint()},
		{"failure", update.Release{}, errors.New("network down"),
			noticeError, locales.UpdateCheckFailed(), "network down"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			a := workspaceApp()
			a.updateCheck = false // the setting only turns off background checks
			latest := &testLatestRelease{release: tt.release, err: tt.err}
			a.checkForUpdateWith(true, testConnectionMeter(true), latest.get)
			if latest.calls != 1 {
				t.Fatalf("latest calls = %d, want 1", latest.calls)
			}
			if len(a.notices) != 1 {
				t.Fatalf("notices = %+v", a.notices)
			}
			n := a.notices[0]
			if n.level != tt.wantLevel || n.text != tt.wantText || n.detail != tt.wantDetail {
				t.Fatalf("notice = %+v, want level %d, text %q, detail %q", n, tt.wantLevel, tt.wantText, tt.wantDetail)
			}
			if a.message != n.line() {
				t.Fatalf("message = %q, notice = %q", a.message, n.line())
			}
		})
	}
}

func TestManualUpdateCheckHasAStableCommand(t *testing.T) {
	a := workspaceApp()
	x := pick(a.actions(nil), "app.checkForUpdates")
	if x.id != "app.checkForUpdates" || !x.enabled || x.label != locales.CheckForUpdatesNow() {
		t.Fatalf("command = %+v", x)
	}
	if a.menu().ItemByID("app.checkForUpdates") == nil {
		t.Fatal("the native menu has no update check entry")
	}
}

func whatsNewApp(t *testing.T, previous string) (*app, string) {
	t.Helper()
	path := filepath.Join(t.TempDir(), "settings.json")
	if previous != "" {
		if err := writeSettings(path, settings{LastRunVersion: previous}, ""); err != nil {
			t.Fatal(err)
		}
	}
	saved, err := loadSettings(path)
	if err != nil {
		t.Fatal(err)
	}
	return &app{settingsPath: path, saved: saved, lastRunVersion: saved.LastRunVersion, settingsReadable: true}, path
}

func TestWhatsNewShowsOnceAndRecordsTheRunningVersion(t *testing.T) {
	a, path := whatsNewApp(t, "0.0.9")
	a.offerWhatsNew()
	if len(a.notices) != 1 {
		t.Fatalf("notices = %+v, want one", a.notices)
	}
	n := a.notices[0]
	wantURL := "https://github.com/BABA983/vscroxy/releases/tag/v" + version.Number
	if n.level != noticeInfo || !n.sticky || n.text != locales.WhatsNew(version.Number) ||
		n.action != locales.ViewReleaseNotes() || n.openURL != wantURL {
		t.Fatalf("notice = %+v", n)
	}
	saved, err := loadSettings(path)
	if err != nil {
		t.Fatal(err)
	}
	if saved.LastRunVersion != version.Number {
		t.Fatalf("saved version = %q, want %q", saved.LastRunVersion, version.Number)
	}

	// The next launch has this version as its last known one: the notice
	// does not come back, and the file keeps the same value.
	next := &app{settingsPath: path, saved: saved, lastRunVersion: saved.LastRunVersion, settingsReadable: true}
	next.offerWhatsNew()
	if len(next.notices) != 0 {
		t.Fatalf("the same version showed a notice: %+v", next.notices)
	}
	again, err := loadSettings(path)
	if err != nil || again.LastRunVersion != version.Number {
		t.Fatalf("saved after the second launch = %+v, %v", again, err)
	}
}

func TestWhatsNewStaysSilentForFirstInstallSameVersionAndDowngrade(t *testing.T) {
	for _, tc := range []struct {
		name     string
		previous string
	}{
		{"first install", ""},
		{"same version", version.Number},
		{"downgrade", "999.0.0"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			a, path := whatsNewApp(t, tc.previous)
			a.offerWhatsNew()
			if len(a.notices) != 0 {
				t.Fatalf("notice for %s: %+v", tc.name, a.notices)
			}
			saved, err := loadSettings(path)
			if err != nil || saved.LastRunVersion != version.Number {
				t.Fatalf("saved version = %q, %v; want %q", saved.LastRunVersion, err, version.Number)
			}
		})
	}
}

func TestWhatsNewDoesNotRecordAVersionBeforeItsNoticeIsPosted(t *testing.T) {
	a, path := whatsNewApp(t, "0.0.9")
	a.offerWhatsNewWith(func(notice) bool { return false })
	if len(a.notices) != 0 || a.lastRunVersion != "0.0.9" {
		t.Fatalf("notices = %+v, last run = %q", a.notices, a.lastRunVersion)
	}
	saved, err := loadSettings(path)
	if err != nil || saved.LastRunVersion != "0.0.9" {
		t.Fatalf("saved version = %q, %v; want the old one", saved.LastRunVersion, err)
	}
}

func TestWhatsNewLeavesUnreadableSettingsUntouched(t *testing.T) {
	path := filepath.Join(t.TempDir(), "settings.json")
	damaged := "{damaged"
	if err := os.WriteFile(path, []byte(damaged), 0o600); err != nil {
		t.Fatal(err)
	}
	a := &app{settingsPath: path}
	a.offerWhatsNew()
	data, err := os.ReadFile(path)
	if err != nil || string(data) != damaged {
		t.Fatalf("settings = %q, %v; want the file left as it was", data, err)
	}
	if len(a.notices) != 0 {
		t.Fatalf("notice from unreadable settings: %+v", a.notices)
	}
}

func TestWhatsNewActionOpensTheReleasePageThroughTheWindow(t *testing.T) {
	a, _ := whatsNewApp(t, "0.0.9")
	a.offerWhatsNew()
	label := locales.In("en-US").ViewReleaseNotes()
	tt := ui.NewTester(func(c *ui.Context) { a.showNotices(c) }, 800, 600)
	tt.SetLocale("en-US")
	tt.Frame()
	if err := tt.Click(label); err != nil {
		t.Fatalf("click %q: %v (texts %v)", label, err, tt.Texts())
	}
	if urls := tt.OpenedURLs(); !slices.Contains(urls, releasePage(version.Number)) {
		t.Fatalf("opened URLs = %v, want %q", urls, releasePage(version.Number))
	}
}

func TestLastRunVersionIsNotSharedThroughConfigurationBundles(t *testing.T) {
	a := workspaceApp()
	a.lastRunVersion, a.saved.LastRunVersion = "0.0.9", "0.0.9"
	a.updateCheck = false // the other local choice stripped on export
	bundle, _ := a.exportConfig()
	if bundle.Settings == nil || bundle.Settings.LastRunVersion != "" || bundle.Settings.DisableUpdateCheck {
		t.Fatalf("export carried local settings: %+v", bundle.Settings)
	}

	// Even a bundle that does carry a version must not change this computer's
	// last known version when its settings are imported.
	bundle.Settings.LastRunVersion = "999.0.0"
	if err := a.applySettings(*bundle.Settings); err != nil {
		t.Fatal(err)
	}
	if a.lastRunVersion != "0.0.9" || a.saved.LastRunVersion != "0.0.9" {
		t.Fatalf("import changed the last run: live %q, saved %q", a.lastRunVersion, a.saved.LastRunVersion)
	}
}

func TestManualCheckIgnoresARequestWhileOneIsInFlight(t *testing.T) {
	a := workspaceApp()
	started, release := make(chan struct{}), make(chan struct{})
	finished := make(chan struct{}, 2)
	var calls atomic.Int32
	latest := func(context.Context) (update.Release, error) {
		if calls.Add(1) == 1 {
			close(started)
			<-release // hold the first lookup in flight
		}
		return update.Release{Tag: "v" + version.Number}, nil
	}
	done := func() { finished <- struct{}{} }

	waitSignal := func(ch <-chan struct{}, what string) {
		t.Helper()
		select {
		case <-ch:
		case <-time.After(5 * time.Second):
			t.Fatal(what)
		}
	}

	a.manualUpdateCheckWith(latest, done)
	waitSignal(started, "the first lookup never started")
	a.manualUpdateCheckWith(latest, done)
	if got := calls.Load(); got != 1 {
		t.Fatalf("overlapping request called the lookup %d times, want 1", got)
	}
	close(release)
	waitSignal(finished, "the first check did not finish and clear the guard")

	a.manualUpdateCheckWith(latest, done)
	waitSignal(finished, "the next check did not finish")
	if got := calls.Load(); got != 2 {
		t.Fatalf("lookup calls = %d after a completed check, want 2", got)
	}
}
