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
