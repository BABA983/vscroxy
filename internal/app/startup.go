package app

import (
	"fmt"
	"strings"
	"sync"

	"github.com/baba983/vscroxy/internal/applog"
	"github.com/baba983/vscroxy/internal/perf"
	"github.com/baba983/vscroxy/internal/watchdog"
	"github.com/egoist/mygo"
)

// The startup marks mirror VS Code's code/* performance marks: each key point
// records one time, and the report shows both its offset from process start and
// the phase that led to it.
const (
	markProcessStart        = "process_start"
	markDataRootReady       = "data_root_ready"
	markLogsOpened          = "logs_opened"
	markSettingsLoaded      = "settings_loaded"
	markSecretsLoaded       = "secrets_loaded"
	markCaptureServerReady  = "capture_server_created"
	markRequestAssetsLoaded = "request_assets_loaded"
	markCookiesLoaded       = "cookies_loaded"
	markExperimentsLoaded   = "experiments_loaded"
	markSessionsLoaded      = "sessions_loaded"
	markAppConstructed      = "app_constructed"
	markRunSetupDone        = "run_setup_done"
	markAppReady            = "app_ready"
	markDockIconDone        = "dock_icon_done"
	markUpdateStarted       = "update_check_started"
	markSystemProxyChecked  = "system_proxy_checked"
	markMenuReady           = "menu_ready"
	markAppearanceSynced    = "appearance_synced"
	markCaptureReady        = "capture_server_ready"
	markWindowCreated       = "window_created"
	markProjectWatch        = "project_watch_started"
	markControlStarted      = "control_started"
	markFirstFrame          = "first_frame_drawn"
)

var startupPhases = []string{
	markProcessStart, markSettingsLoaded, markCaptureReady,
	markWindowCreated, markFirstFrame, markProjectWatch, markControlStarted,
	markDockIconDone,
}

var startupSummaryOnce sync.Once

// firstFrame marks the first build of the window's view and starts the UI
// watchdog from there. Tester views in tests have no real window or app clock
// to watch, so they neither mark nor sample.
func (a *app) firstFrame() {
	a.firstFrameOnce.Do(func() {
		if !a.watchdogArmed {
			return
		}
		markStartup(markFirstFrame)
		// The callback is posted, not run here: the first frame must be
		// free to draw while the after-restored contributions do their work.
		if a.startup != nil {
			a.onMain(func() { a.startup.run(startupAfterFirstFrame) })
		}
		if a.startupLifetime == nil {
			return
		}
		w := watchdog.New(func(fn func()) {
			// RunOnMain waits for the main thread, so a stuck thread must not
			// hold the timer that needs to notice the stuck thread.
			go guarded(func() { mygo.RunOnMain(fn) })
		}, watchdog.WithClock(a.timerClock()))
		a.uiWatchdog = w
		a.startupLifetime.Add(w)
		w.Start()
	})
}

// markStartup records one startup mark and logs the summary once every phase
// has been reached. The marks can arrive on different threads: the window is
// created on the UI thread while the capture server starts on a worker.
func markStartup(name string) {
	perf.Mark(name)
	maybeLogStartupSummary()
}

func maybeLogStartupSummary() {
	seen := make(map[string]bool, len(startupPhases))
	for _, event := range perf.Marks() {
		seen[event.Name] = true
	}
	for _, name := range startupPhases {
		if !seen[name] {
			return
		}
	}
	startupSummaryOnce.Do(func() {
		applog.For("app").Info("startup phases", "detail", strings.TrimSpace(string(startupMarksReport())))
	})
}

// startupMarksReport formats the marks for the diagnostics bundle and the
// startup log line. It contains no request contents: only our own mark names
// and durations.
func startupMarksReport() []byte {
	marks := perf.Marks()
	var b strings.Builder
	b.WriteString("startup marks (ms from process start; phase time in parentheses):\n")
	if len(marks) == 0 {
		b.WriteString("(none)\n")
		return []byte(b.String())
	}
	base, previous := marks[0].At, marks[0].At
	for _, event := range marks {
		fmt.Fprintf(&b, "%-22s +%5dms (%4dms)\n", event.Name,
			event.At.Sub(base).Milliseconds(), event.At.Sub(previous).Milliseconds())
		previous = event.At
	}
	return []byte(b.String())
}
