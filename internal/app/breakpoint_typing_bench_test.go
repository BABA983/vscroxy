package app

import (
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/baba983/vscroxy/internal/capture"
	"github.com/egoist/mygo/ui"
)

// benchmarkBreakpointBody renders the paused dialog with a body of size MiB
// and types into the body editor. MyGo rebuilds the view on input, so the
// reported time per typed byte is what a person waits for between keystrokes.
// The 1/4/8/16 MiB runs are the measurements behind MaxBreakpointBody.
func benchmarkBreakpointBody(b *testing.B, sizeMiB int) {
	b.Helper()
	line := strings.Repeat("abcdefghij", 10) + "\n"
	body := strings.Repeat(line, sizeMiB*(1<<20)/len(line))
	a := workspaceApp()
	a.bp = &capture.Breakpoint{
		ID: 1, Phase: capture.PhaseRequest, Method: "POST",
		URL: "https://api.example.com/items", Header: http.Header{"Content-Type": {"application/json"}},
		Body: body,
	}
	a.showBreakpoint(a.bp)
	tt := ui.NewTester(a.view, 1280, 800)
	tt.SetLocale("en-US")
	tt.Frame()
	if err := tt.Click("Body"); err != nil {
		b.Fatal(err)
	}
	const typed = 8
	var total time.Duration
	for b.Loop() {
		start := time.Now()
		for range typed {
			tt.Type("x")
		}
		total += time.Since(start)
	}
	b.ReportMetric(float64(total.Nanoseconds())/float64(b.N*typed), "ns/typed")
}

func BenchmarkBreakpointBodyTyping1MiB(b *testing.B)  { benchmarkBreakpointBody(b, 1) }
func BenchmarkBreakpointBodyTyping4MiB(b *testing.B)  { benchmarkBreakpointBody(b, 4) }
func BenchmarkBreakpointBodyTyping8MiB(b *testing.B)  { benchmarkBreakpointBody(b, 8) }
func BenchmarkBreakpointBodyTyping16MiB(b *testing.B) { benchmarkBreakpointBody(b, 16) }
