package app

import (
	"strings"

	"github.com/baba983/vscroxy/internal/applog"
	"github.com/baba983/vscroxy/locales"
)

// reportPreviousCrash warns once about a fatal panic the last run wrote. The
// report is taken and cleared here, so a following launch is quiet; the copy
// beside the live crash file stays for a bug report. It runs after the launch
// owns the data root, so a second instance never consumes the report.
func (a *app) reportPreviousCrash(root string) {
	report, err := applog.TakeCrash(root)
	if err != nil {
		applog.For("app").Warn("previous crash report unavailable", "err", err)
		return
	}
	if strings.TrimSpace(report) == "" {
		return
	}
	a.notify(notice{
		level:  noticeWarning,
		text:   locales.QuitUnexpectedly(),
		detail: locales.CrashReportSavedTo(shortenHome(applog.CrashReportPath(root))),
		action: locales.Diagnostics(),
		run:    func() { a.openDiagnostics(nil, diagnoseMac) },
	})
}
