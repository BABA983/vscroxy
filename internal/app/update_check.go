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
	go guarded(func() { a.checkForUpdate(true) })
}

// offerUpdate shows the notice when release is newer than this build.
func (a *app) offerUpdate(release update.Release) {
	if !update.Newer(version.Number, release.Tag) {
		return
	}
	a.notify(notice{
		level:  noticeInfo,
		sticky: true,
		text:   locales.UpdateAvailable(strings.TrimPrefix(release.Tag, "v")),
		detail: locales.UpdateAvailableHint(),
	})
}
