package app

import (
	"context"
	"errors"
	"path/filepath"
	"strings"
	"testing"

	"github.com/baba983/vscroxy/internal/update"
	"github.com/baba983/vscroxy/internal/version"
	"github.com/baba983/vscroxy/locales"
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
