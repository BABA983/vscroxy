package app

import (
	"archive/zip"
	"bytes"
	"io"
	"net/url"
	"strings"
	"testing"

	"github.com/baba983/vscroxy/internal/async"
	"github.com/baba983/vscroxy/internal/lifecycle"
	"github.com/baba983/vscroxy/internal/perf"
	"github.com/baba983/vscroxy/internal/uikit"
	"github.com/egoist/mygo/ui"
)

func TestDiagnosticsBundleIncludesStartupMarks(t *testing.T) {
	perf.Mark("bundle/first")
	perf.Mark("bundle/second")
	a := workspaceApp()
	target, err := url.Parse("https://api.example.com/projects")
	if err != nil {
		t.Fatal(err)
	}
	data, err := a.buildDiagnosticsBundle(uikit.Theme(false), target)
	if err != nil {
		t.Fatal(err)
	}
	zr, err := zip.NewReader(bytes.NewReader(data), int64(len(data)))
	if err != nil {
		t.Fatal(err)
	}
	for _, file := range zr.File {
		if file.Name != "startup-marks.txt" {
			continue
		}
		rc, err := file.Open()
		if err != nil {
			t.Fatal(err)
		}
		defer rc.Close()
		body, err := io.ReadAll(rc)
		if err != nil {
			t.Fatal(err)
		}
		text := string(body)
		first, second := strings.Index(text, "bundle/first"), strings.Index(text, "bundle/second")
		if first < 0 || second < first {
			t.Fatalf("marks out of order in bundle: %q", text)
		}
		if !strings.Contains(text, "ms from process start") {
			t.Fatalf("no durations in %q", text)
		}
		return
	}
	t.Fatal("bundle has no startup-marks.txt")
}

func TestFirstFrameStartsWatchdogUnderStartupLifetime(t *testing.T) {
	a := workspaceApp()
	a.lifecycle = &lifecycle.Lifecycle{}
	a.startupLifetime = lifecycle.NewDisposableStore()
	a.clock = async.NewFakeClock()
	a.watchdogArmed = true
	ui.NewTester(a.view, 1280, 800)
	if a.uiWatchdog == nil {
		t.Fatal("first frame did not start the watchdog")
	}
	// Quit releases it through the store; disposing twice is safe.
	a.startupLifetime.Dispose()
	a.startupLifetime.Dispose()
}

// A deferred contribution runs after the frame that schedules it, and the
// frame is complete without it. The queue stands in for MyGo's main loop, so
// the test does not wait on a scheduler.
func TestAfterFirstFramePhaseRunsAfterTheFrame(t *testing.T) {
	a := workspaceApp()
	a.lifecycle = &lifecycle.Lifecycle{}
	a.startupLifetime = lifecycle.NewDisposableStore()
	a.clock = async.NewFakeClock()
	a.watchdogArmed = true
	a.startup = newStartupRunner()
	runs := 0
	a.startup.add(startupAfterFirstFrame, "test/after_first_frame", func() { runs++ })
	var queued []func()
	a.quitOnMain = func(fn func()) { queued = append(queued, fn) }

	a.firstFrame()
	if runs != 0 || len(queued) != 1 {
		t.Fatalf("first frame ran the deferred step or queued no work: runs=%d queued=%d", runs, len(queued))
	}
	marks := perf.Marks()
	frame, deferred := -1, -1
	for i, event := range marks {
		switch event.Name {
		case markFirstFrame:
			frame = i
		case "test/after_first_frame":
			deferred = i
		}
	}
	if frame < 0 || deferred >= 0 {
		t.Fatalf("first frame did not finish on its own: frame=%d deferred=%d", frame, deferred)
	}

	queued[0]()
	a.startup.run(startupAfterFirstFrame)
	if runs != 1 {
		t.Fatalf("deferred step ran %d times, want once", runs)
	}
	marks = perf.Marks()
	frame, deferred = -1, -1
	for i, event := range marks {
		switch event.Name {
		case markFirstFrame:
			frame = i
		case "test/after_first_frame":
			deferred = i
		}
	}
	if frame < 0 || deferred < frame {
		t.Fatalf("deferred step did not follow the frame: frame=%d deferred=%d", frame, deferred)
	}
}
