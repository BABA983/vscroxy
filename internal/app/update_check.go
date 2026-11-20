package app

// The launch-time update check asks GitHub for the latest release number and
// offers nothing but a notice: vscroxy never downloads or installs an update.

import (
	"context"
	"strings"
	"time"

	"github.com/baba983/vscroxy/internal/applog"
	"github.com/baba983/vscroxy/internal/update"
	"github.com/baba983/vscroxy/internal/version"
	"github.com/baba983/vscroxy/locales"
	"github.com/egoist/mygo"
)

// updateNoticeID is the stable key of the update notice. It is saved instead
// of the text when someone chooses not to see the notice again.
const updateNoticeID = "notices.update.available"

// connectionMeter is the platform's own answer to whether the current
// network should be spared optional traffic. Tests inject it.
type connectionMeter interface {
	metered() bool
}

// systemConnectionMeter uses MyGo's Network.Status: NWPathMonitor's
// isExpensive/isConstrained on macOS, the network cost hints on Windows and
// GLib's network-metered flag (NetworkManager) on Linux. Where no cheap
// reliable signal exists, MyGo reports false and the network counts as
// unmetered.
type systemConnectionMeter struct{}

func (systemConnectionMeter) metered() bool {
	return mygo.Network.Status().Metered
}

type latestReleaseFunc func(context.Context) (update.Release, error)

// checkForUpdate runs off the UI thread. An explicit check always looks for a
// release and reports every outcome, even on a metered network or with
// background checks turned off. A background check stays silent unless a
// newer release turns up, and never spends a metered connection's data.
func (a *app) checkForUpdate(manual bool) {
	a.checkForUpdateWith(manual, systemConnectionMeter{}, func(ctx context.Context) (update.Release, error) {
		return update.Latest(ctx, nil)
	})
}

func (a *app) checkForUpdateWith(manual bool, connection connectionMeter, latest latestReleaseFunc) {
	if !manual && connection.metered() {
		applog.For("update").Info("background check skipped", "reason", "metered network")
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	release, err := latest(ctx)
	if err != nil {
		if manual {
			a.fail(locales.UpdateCheckFailed(), err)
		} else {
			applog.For("update").Warn("background check failed", "err", err)
		}
		return
	}
	found := update.Newer(version.Number, release.Tag)
	applog.For("update").Debug("check finished", "found", found)
	if found {
		a.offerUpdate(release)
	} else if manual {
		a.done(locales.UpToDate())
	}
}

// manualUpdateCheck starts the explicit check from a menu, command or button,
// so the release lookup never holds the UI thread.
func (a *app) manualUpdateCheck() {
	a.manualUpdateCheckWith(func(ctx context.Context) (update.Release, error) {
		return update.Latest(ctx, nil)
	}, nil)
}

// manualUpdateCheckWith is the injectable form: latest stands in for GitHub,
// and finished, when set, runs after the in-flight guard has cleared. Repeat
// requests while a check is running do nothing.
func (a *app) manualUpdateCheckWith(latest latestReleaseFunc, finished func()) {
	if !a.manualCheckInFlight.CompareAndSwap(false, true) {
		return
	}
	go guarded(func() {
		defer func() {
			a.manualCheckInFlight.Store(false)
			if finished != nil {
				finished()
			}
		}()
		// A manual check ignores the meter; the argument keeps one release
		// lookup path for both the background and explicit checks.
		a.checkForUpdateWith(true, systemConnectionMeter{}, latest)
	})
}

// releasePage is the release notes for number in this project's GitHub repo;
// version numbers in the file are plain numbers, without the tag's v.
func releasePage(number string) string {
	return "https://github.com/" + update.Repository + "/releases/tag/v" + strings.TrimPrefix(number, "v")
}

// noticePoster posts a notice and says whether it got out. Tests pass a poster
// that refuses, as a crash before the post would, to check that the version
// is not recorded early.
type noticePoster func(notice) bool

// offerWhatsNew compares the version this run started with against the last
// one the settings file saw, as VS Code compares its last known version. A
// first install, the same version and a downgrade show nothing.
func (a *app) offerWhatsNew() {
	a.offerWhatsNewWith(func(n notice) bool {
		a.notify(n)
		return true
	})
}

func (a *app) offerWhatsNewWith(post noticePoster) {
	if a.settingsPath != "" && !a.settingsReadable {
		return // never replace a settings file we could not read
	}
	newer := a.lastRunVersion != "" && update.Newer(a.lastRunVersion, version.Number)
	if newer && !post(notice{
		level:   noticeInfo,
		sticky:  true,
		text:    locales.WhatsNew(version.Number),
		action:  locales.ViewReleaseNotes(),
		openURL: releasePage(version.Number),
	}) {
		return
	}
	if !newer && a.lastRunVersion == version.Number {
		return
	}
	previous := a.lastRunVersion
	a.lastRunVersion = version.Number
	// An explicit write, not the debounced one: the version must survive an
	// exit right after the notice, and no older layout write may overwrite it.
	a.bypassSettingsSave()
	if err := a.persistSettings(); err != nil {
		a.lastRunVersion = previous // persistSettings has already reported it
	}
}

// offerUpdate shows the notice when release is newer than this build.
func (a *app) offerUpdate(release update.Release) {
	if !update.Newer(version.Number, release.Tag) {
		return
	}
	a.notify(notice{
		level:          noticeInfo,
		sticky:         true,
		neverShowAgain: updateNoticeID,
		text:           locales.UpdateAvailable(strings.TrimPrefix(release.Tag, "v")),
		detail:         locales.UpdateAvailableHint(),
	})
}
