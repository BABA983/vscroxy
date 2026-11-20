package app

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/baba983/vscroxy/internal/update"
	"github.com/baba983/vscroxy/locales"
	"github.com/egoist/mygo/ui"
)

// progressTestApp runs published notice changes on the calling thread, as a
// Tester has no native main loop to dispatch them to.
func progressTestApp() *app {
	return &app{quitOnMain: func(fn func()) { fn() }}
}

// awaitProgressSignal waits for a worker's channel without sleeping or polling.
func awaitProgressSignal[T any](t *testing.T, done <-chan T) T {
	t.Helper()
	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
	defer cancel()
	select {
	case value := <-done:
		return value
	case <-ctx.Done():
		t.Fatal("the progress task did not finish")
		return *new(T)
	}
}

func TestProgressUpdatesOneNoticeInPlace(t *testing.T) {
	a := progressTestApp()
	started, release := make(chan struct{}), make(chan struct{})
	run := a.withProgress(progressOptions{title: "Import", location: progressNotification},
		func(ctx context.Context, report func(progressReport)) (string, error) {
			report(progressReport{message: "Reading file", percentage: 20})
			close(started)
			select {
			case <-release:
			case <-t.Context().Done():
				return "", t.Context().Err()
			}
			report(progressReport{message: "Merging requests", percentage: 80})
			return "Imported collection", nil
		})
	awaitProgressSignal(t, started)
	if len(a.notices) != 1 || len(a.noticeCenter().pending) != 1 {
		t.Fatalf("first report left %d notices and %d queued: %+v", len(a.notices), len(a.noticeCenter().pending), a.notices)
	}
	if got := a.notices[0].text; got != "Reading file" || a.notices[0].detail != locales.ProgressPercent(20) {
		t.Fatalf("first report = %q, %q", got, a.notices[0].detail)
	}
	tt := ui.NewTester(func(c *ui.Context) { a.showNotices(c) }, 640, 320)
	tt.Frame()

	close(release)
	awaitProgressSignal(t, run.done)
	if len(a.noticeCenter().pending) != 1 {
		t.Fatalf("reports queued %d toasts, want one update", len(a.noticeCenter().pending))
	}
	tt.Frame()
	if len(a.notices) != 1 {
		t.Fatalf("reports left %d notices: %+v", len(a.notices), a.notices)
	}
	n := a.notices[0]
	if n.progress.phase != progressDone || n.level != noticeSuccess || n.text != "Imported collection" {
		t.Fatalf("final notice = %+v", n)
	}
	if tt.HasText("Reading file") || !tt.HasText("Imported collection") {
		t.Fatalf("toast did not change in place: %v", tt.Texts())
	}
}

func TestProgressCancelCancelsTaskAndEndsCancelled(t *testing.T) {
	a := progressTestApp()
	a.inform("Earlier notice") // the Cancel action must use this task's id, not zero
	started := make(chan context.Context, 1)
	run := a.withProgress(progressOptions{title: "Import", location: progressNotification, cancellable: true},
		func(ctx context.Context, report func(progressReport)) (string, error) {
			report(progressReport{message: "Reading file", percentage: 40})
			started <- ctx
			select {
			case <-ctx.Done():
				return "", ctx.Err()
			case <-t.Context().Done():
				return "", t.Context().Err()
			}
		})
	taskCtx := awaitProgressSignal(t, started)
	tt := ui.NewTester(func(c *ui.Context) { a.showNotices(c) }, 640, 320)
	tt.Frame()
	if err := tt.Click(locales.Cancel()); err != nil {
		t.Fatalf("Cancel action is not on the progress toast: %v (%v)", err, tt.Texts())
	}
	awaitProgressSignal(t, run.done)
	if !errors.Is(taskCtx.Err(), context.Canceled) {
		t.Errorf("task context = %v, want cancelled", taskCtx.Err())
	}
	n := a.notices[len(a.notices)-1]
	if n.progress.phase != progressCancelled || n.level != noticeCancelled {
		t.Fatalf("final notice = %+v, want cancelled", n)
	}
	if n.text != locales.ProgressCancelled("Import") {
		t.Errorf("final text = %q, want %q", n.text, locales.ProgressCancelled("Import"))
	}
}

func TestNeverShowAgainSurvivesReloadAndReset(t *testing.T) {
	path := filepath.Join(t.TempDir(), "settings.json")
	a := &app{settingsPath: path, quitOnMain: func(fn func()) { fn() }}
	a.offerUpdate(update.Release{Tag: "v99.0.0", URL: "https://example.com/release"})
	if len(a.notices) != 1 || a.notices[0].neverShowAgain != updateNoticeID {
		t.Fatalf("update notice = %+v", a.notices)
	}
	a.suppressNotice(a.notices[0].id)
	if len(a.notices) != 0 {
		t.Fatalf("suppressed notice stayed in the center: %+v", a.notices)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(data), updateNoticeID) || strings.Contains(string(data), locales.UpdateAvailable("99.0.0")) {
		t.Fatalf("settings saved the text instead of the id: %s", data)
	}
	saved, err := loadSettings(path)
	if err != nil || !slices.Equal(saved.DismissedNotices, []string{updateNoticeID}) {
		t.Fatalf("saved dismissals = %v, %v", saved.DismissedNotices, err)
	}

	// A new app takes the saved ids at startup, as newApp does.
	reloaded := &app{settingsPath: path, quitOnMain: func(fn func()) { fn() }}
	reloaded.dismissedNotices = slices.Clone(saved.DismissedNotices)
	reloaded.offerUpdate(update.Release{Tag: "v99.0.0", URL: "https://example.com/release"})
	if len(reloaded.notices) != 0 {
		t.Fatalf("suppressed notice returned after reload: %+v", reloaded.notices)
	}
	if !reloaded.resetSetting("general.dismissedNotices") {
		t.Fatal("the Settings entry did not reset dismissed notices")
	}
	after, err := loadSettings(path)
	if err != nil || len(after.DismissedNotices) != 0 {
		t.Fatalf("dismissals after reset = %v, %v", after.DismissedNotices, err)
	}
	reloaded.clearNotifications()
	reloaded.offerUpdate(update.Release{Tag: "v99.0.0", URL: "https://example.com/release"})
	if len(reloaded.notices) != 1 || !strings.Contains(reloaded.notices[0].text, "99.0.0") {
		t.Fatalf("notice did not return after reset: %+v", reloaded.notices)
	}
}
