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
)

// checkForUpdate runs off the UI thread once per launch, when the setting is
// on. A failure is silent: being offline is not a problem to report.
func (a *app) checkForUpdate() {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	release, err := update.Latest(ctx, nil)
	if err != nil {
		return
	}
	applog.For("update").Debug("update check finished", "found", release.Tag != "")
	a.offerUpdate(release)
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
