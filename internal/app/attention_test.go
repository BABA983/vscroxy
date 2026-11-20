package app

import (
	"errors"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"testing"

	"github.com/baba983/vscroxy/internal/capture"
	"github.com/baba983/vscroxy/internal/request"
	"github.com/baba983/vscroxy/locales"
	"github.com/egoist/mygo/ui"
)

// fakeAttentionChannels records the OS channel calls. It is the fake behind
// every contract test: the app goes through its normal event paths, only
// MyGo's platform calls are replaced.
type fakeAttentionChannels struct {
	mu      sync.Mutex
	flashes []bool
	badges  []int
	notices []fakeAttentionNotice
	focuses []FocusMode
}

type fakeAttentionNotice struct {
	id, title, body string
	click           func()
}

func (f *fakeAttentionChannels) flashWindow(on bool) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.flashes = append(f.flashes, on)
}

func (f *fakeAttentionChannels) setBadge(count int) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.badges = append(f.badges, count)
}

func (f *fakeAttentionChannels) showNotice(id, title, body string, onClick func()) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.notices = append(f.notices, fakeAttentionNotice{id: id, title: title, body: body, click: onClick})
}

func (f *fakeAttentionChannels) focus(mode FocusMode) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.focuses = append(f.focuses, mode)
}

func (f *fakeAttentionChannels) snapshot() (flashes []bool, badges []int, notices []fakeAttentionNotice, focuses []FocusMode) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return slices.Clone(f.flashes), slices.Clone(f.badges), slices.Clone(f.notices), slices.Clone(f.focuses)
}

func (f *fakeAttentionChannels) snapshotFocusAndBadges() (focuses []FocusMode, badges []int) {
	_, badges, _, focuses = f.snapshot()
	return focuses, badges
}

func attentionTestApp(t *testing.T, mode string, focused bool) (*app, *fakeAttentionChannels) {
	t.Helper()
	a := workspaceApp()
	channels := &fakeAttentionChannels{}
	a.attention = newAttentionService(a, channels)
	a.attention.setMode(mode)
	a.attention.focused = func() bool { return focused }
	return a, channels
}

func attentionFrame(t *testing.T, a *app) *ui.Tester {
	t.Helper()
	tt := ui.NewTester(a.view, 900, 640)
	tt.UseScreenReader()
	tt.Frame()
	return tt
}

func noticeText(n fakeAttentionNotice) string { return n.title + "\n" + n.body }

// TestAccessibilitySignalsTable pins the fixed table. Catches an
// implementation that adds an event without a row, or quietly drops a
// channel from one (for example announcing only when it also notifies).
func TestAccessibilitySignalsTable(t *testing.T) {
	want := []struct {
		signal                               AccessibilitySignal
		flash, badge, notification, announce bool
	}{
		{AccessibilitySignalOnDebugBreak, true, true, true, true},
		{AccessibilitySignalChatUserActionRequired, true, true, true, true},
		{AccessibilitySignalTaskCompleted, true, false, true, true},
		{AccessibilitySignalTaskFailed, true, false, true, true},
	}
	if len(accessibilitySignals) != len(want) {
		t.Fatalf("signals table has %d entries, want %d", len(accessibilitySignals), len(want))
	}
	for i, entry := range accessibilitySignals {
		w := want[i]
		if entry.signal != w.signal || entry.flash != w.flash || entry.badge != w.badge ||
			entry.notification != w.notification || entry.announcement != w.announce {
			t.Errorf("signals[%d] = %+v, want %+v", i, entry, w)
		}
	}
	if got := channelsFor(AccessibilitySignal(99)); got != (signalChannels{}) {
		t.Errorf("unknown signal matched %+v", got)
	}
}

// TestAttentionBackgroundBreakpointFlashBadgeAndNotify catches the reported
// bug: a pause in the background only redrew the window. It also catches a
// notification that leaks the URL path, query or body.
func TestAttentionBackgroundBreakpointFlashBadgeAndNotify(t *testing.T) {
	a, channels := attentionTestApp(t, attentionAuto, false)
	bp := capture.Breakpoint{ID: 7, Method: "POST", URL: "https://api.example.com/orders/42?token=secret-token", Body: "secret-body"}
	tt := attentionFrame(t, a)

	a.queueBreakpoint(bp)

	flashes, badges, notices, focuses := channels.snapshot()
	if !slices.Equal(flashes, []bool{true}) || !slices.Equal(badges, []int{1}) {
		t.Fatalf("flash=%v badge=%v, want a flash and a badge of one", flashes, badges)
	}
	if len(notices) != 1 || notices[0].id != breakpointAttentionID(bp.ID) {
		t.Fatalf("notices = %+v", notices)
	}
	if got, want := notices[0].body, locales.AttentionBreakpoint("POST", "api.example.com"); got != want {
		t.Fatalf("notification body = %q, want %q", got, want)
	}
	if text := noticeText(notices[0]); strings.Contains(text, "secret") || strings.Contains(text, "/orders/42") {
		t.Fatalf("notification shows request contents: %q", text)
	}
	if len(focuses) != 0 {
		t.Fatalf("raising a signal stole the focus: %v", focuses)
	}
	tt.Frame()
	if !slices.Contains(tt.Announcements(), locales.AttentionBreakpointAnnouncement("POST", "api.example.com")) {
		t.Fatalf("screen reader heard %v", tt.Announcements())
	}
}

// TestAttentionForegroundBreakpointOnlyAnnounces catches an implementation
// that treats auto as always-on and flashes or takes focus while the user
// is looking at the window.
func TestAttentionForegroundBreakpointOnlyAnnounces(t *testing.T) {
	a, channels := attentionTestApp(t, attentionAuto, true)
	tt := attentionFrame(t, a)

	a.queueBreakpoint(capture.Breakpoint{ID: 8, Method: "GET", URL: "https://api.example.com/health"})

	flashes, badges, notices, focuses := channels.snapshot()
	if len(flashes) != 0 || len(badges) != 0 || len(notices) != 0 || len(focuses) != 0 {
		t.Fatalf("foreground signals flash=%v badge=%v notice=%v focus=%v", flashes, badges, notices, focuses)
	}
	tt.Frame()
	if !slices.Contains(tt.Announcements(), locales.AttentionBreakpointAnnouncement("GET", "api.example.com")) {
		t.Fatalf("screen reader heard %v", tt.Announcements())
	}
}

// TestAttentionOnModeSignalsInForeground catches a setting that only
// changes the label but leaves auto's focus check in place.
func TestAttentionOnModeSignalsInForeground(t *testing.T) {
	a, channels := attentionTestApp(t, attentionOn, true)
	tt := attentionFrame(t, a)

	a.queueBreakpoint(capture.Breakpoint{ID: 9, Method: "GET", URL: "https://api.example.com/on"})

	flashes, badges, notices, _ := channels.snapshot()
	if !slices.Equal(flashes, []bool{true}) || !slices.Equal(badges, []int{1}) || len(notices) != 1 {
		t.Fatalf("flash=%v badge=%v notices=%d", flashes, badges, len(notices))
	}
	tt.Frame()
	if !slices.Contains(tt.Announcements(), locales.AttentionBreakpointAnnouncement("GET", "api.example.com")) {
		t.Fatalf("screen reader heard %v", tt.Announcements())
	}
}

// TestAttentionBreakpointHandlingClearsBadge catches a badge that counts
// every pause forever. The badge must follow the items still waiting.
func TestAttentionBreakpointHandlingClearsBadge(t *testing.T) {
	a, channels := attentionTestApp(t, attentionAuto, false)
	first := capture.Breakpoint{ID: 1, Method: "GET", URL: "https://api.example.com/one"}
	second := capture.Breakpoint{ID: 2, Method: "POST", URL: "https://api.example.com/two"}
	a.queueBreakpoint(first)
	a.queueBreakpoint(second)
	if _, badges, _, _ := channels.snapshot(); !slices.Equal(badges, []int{1, 2}) {
		t.Fatalf("badges = %v, want one then two pending", badges)
	}

	a.bp = &first
	a.resolveBreak(true, false, false)
	_, badges, _, _ := channels.snapshot()
	if !slices.Equal(badges, []int{1, 2, 1}) {
		t.Fatalf("badges = %v, want the handled breakpoint removed", badges)
	}
	if _, pending := a.attention.pending[breakpointAttentionID(first.ID)]; pending {
		t.Fatal("handled breakpoint is still pending")
	}
}

// TestAttentionOffFiresNothing catches an off switch that still flashes,
// badges or notifies. Screen-reader announcements are a separate channel and
// must survive, as VS Code keeps them separate from the visual signals.
func TestAttentionOffFiresNothing(t *testing.T) {
	a, channels := attentionTestApp(t, attentionOff, false)
	tt := attentionFrame(t, a)

	a.queueBreakpoint(capture.Breakpoint{ID: 3, Method: "GET", URL: "https://api.example.com/off"})
	a.mcpConsentsPath = filepath.Join(t.TempDir(), "mcp-consents.json")
	a.addDocument(savedRequest{Name: "Local", Protocol: "MCP", MCP: request.MCPConfig{
		Transport: request.MCPTransportStdio, Command: "/bin/sh", Args: "-c\ntrue",
	}}, "")
	a.connectMCP(a.requestDocument)
	if flashes, badges, notices, focuses := channels.snapshot(); len(flashes)+len(badges)+len(notices)+len(focuses) != 0 {
		t.Fatalf("off fired flash=%v badge=%v notice=%v focus=%v", flashes, badges, notices, focuses)
	}
	tt.Frame()
	want := []string{
		locales.AttentionBreakpointAnnouncement("GET", "api.example.com"),
		locales.AttentionMcpConsentTitle(),
	}
	if got := tt.Announcements(); !slices.Contains(got, want[0]) || !slices.Contains(got, want[1]) {
		t.Fatalf("off announced %v, want %v", got, want)
	}
}

// TestAttentionNotificationClickOpensExactBreakpoint catches a click
// handler that opens the first pending breakpoint, or that leaves the
// window in the background.
func TestAttentionNotificationClickOpensExactBreakpoint(t *testing.T) {
	a, channels := attentionTestApp(t, attentionAuto, false)
	a.queueBreakpoint(capture.Breakpoint{ID: 11, Method: "GET", URL: "https://api.example.com/first"})
	second := capture.Breakpoint{ID: 12, Method: "POST", URL: "https://api.example.com/second"}
	a.queueBreakpoint(second)
	_, _, notices, _ := channels.snapshot()
	if len(notices) != 2 {
		t.Fatalf("got %d notices", len(notices))
	}
	notices[1].click()

	if !a.bpOpen || a.bp == nil || a.bp.ID != second.ID || a.bpURL != second.URL {
		t.Fatalf("notification opened %+v, want breakpoint %d", a.bp, second.ID)
	}
	focuses, badges := channels.snapshotFocusAndBadges()
	if !slices.Equal(focuses, []FocusMode{FocusModeNotify}) || !slices.Equal(badges, []int{1, 2, 1}) {
		t.Fatalf("focus=%v badge=%v, want a click focus and one pending item", focuses, badges)
	}
}

// TestAttentionNotificationClickKeepsOpenBreakpointEdits catches a click
// handler that swaps in the queued breakpoint and silently discards the
// request body, method and headers the user is halfway through editing.
func TestAttentionNotificationClickKeepsOpenBreakpointEdits(t *testing.T) {
	a, channels := attentionTestApp(t, attentionAuto, false)
	a.queueBreakpoint(capture.Breakpoint{ID: 21, Method: "GET", URL: "https://api.example.com/a"})
	second := capture.Breakpoint{ID: 22, Method: "POST", URL: "https://api.example.com/b"}
	a.queueBreakpoint(second)

	first := a.bps[0]
	a.bps = a.bps[1:]
	a.showBreakpoint(&first)
	a.bpBody, a.bpMethod, a.bpHeaders = "edited-body", "PATCH", "X-Draft: keep"

	_, _, notices, _ := channels.snapshot()
	if len(notices) != 2 {
		t.Fatalf("got %d notices", len(notices))
	}
	notices[1].click()

	if a.bp == nil || a.bp.ID != first.ID || !a.bpOpen || a.bpBody != "edited-body" ||
		a.bpMethod != "PATCH" || a.bpHeaders != "X-Draft: keep" {
		t.Fatalf("click replaced the open breakpoint's edits: %+v (%q, %q)", a.bp, a.bpBody, a.bpHeaders)
	}
	if len(a.bps) != 1 || a.bps[0].ID != second.ID {
		t.Fatalf("queued breakpoint = %+v, want %d still waiting", a.bps, second.ID)
	}
	if got, want := a.message, locales.AttentionBreakpoint("POST", "api.example.com"); got != want {
		t.Fatalf("notice says %q, want %q", got, want)
	}
	focuses, badges := channels.snapshotFocusAndBadges()
	if !slices.Equal(focuses, []FocusMode{FocusModeNotify}) || !slices.Equal(badges, []int{1, 2, 1}) {
		t.Fatalf("focus=%v badge=%v", focuses, badges)
	}
}

// TestAttentionNotificationClickOpensExactConsent catches a click that
// focuses the window but drops the user back on a different tab, and a
// prompt that puts the command or its secrets into the notification.
func TestAttentionNotificationClickOpensExactConsent(t *testing.T) {
	a, channels := attentionTestApp(t, attentionAuto, false)
	a.mcpConsentsPath = filepath.Join(t.TempDir(), "mcp-consents.json")
	a.addDocument(savedRequest{Name: "Local", Protocol: "MCP", MCP: request.MCPConfig{
		Transport: request.MCPTransportStdio, Command: "/bin/sh", Args: "-c\nsecret-arg",
		Env: []request.Field{{Name: "TOKEN", Value: "secret-value"}},
	}}, "")
	d := a.requestDocument
	a.connectMCP(d)
	if !a.mcpConsent.open {
		t.Fatal("test setup did not open a consent")
	}
	_, _, notices, _ := channels.snapshot()
	if len(notices) != 1 {
		t.Fatalf("got %d notices", len(notices))
	}
	if text := noticeText(notices[0]); strings.Contains(text, "secret-arg") || strings.Contains(text, "secret-value") || strings.Contains(text, "/bin/sh") {
		t.Fatalf("consent notification shows the command: %q", text)
	}
	a.addDocument(savedRequest{Name: "Other", Method: "GET", URL: "https://api.example.com/other"}, "")
	notices[0].click()

	if !a.mcpConsent.open || a.mcpConsent.d != d || a.requestDocument != d || a.page != pageRequest {
		t.Fatalf("notification did not reopen the exact prompt: doc=%v open=%v page=%v", a.requestDocument, a.mcpConsent.open, a.page)
	}
	focuses, badges := channels.snapshotFocusAndBadges()
	if !slices.Equal(focuses, []FocusMode{FocusModeNotify}) || !slices.Equal(badges, []int{1, 0}) {
		t.Fatalf("focus=%v badge=%v, want the only pending item cleared", focuses, badges)
	}
}

// TestAttentionRunFinishedAndFailed catches a run completion that never
// signals, one that badges an item nobody has to handle, and one that
// reports a failed run as finished.
func TestAttentionRunFinishedAndFailed(t *testing.T) {
	for _, failed := range []bool{false, true} {
		a, channels := attentionTestApp(t, attentionAuto, false)
		events := make(chan runnerEvent)
		close(events)
		a.runner.events = events
		a.runner.running = true
		a.runner.outcome = &runOutcome{}
		if failed {
			a.runner.outcome.err = errors.New("a step failed")
		}
		a.drainRunner()

		_, badges, notices, _ := channels.snapshot()
		if len(badges) != 0 {
			t.Fatalf("failed=%v: a finished run badged %v", failed, badges)
		}
		if len(notices) != 1 {
			t.Fatalf("failed=%v: got %d notices", failed, len(notices))
		}
		wantTitle := locales.AttentionRunFinishedTitle()
		signal := AccessibilitySignalTaskCompleted
		wantAnnouncement := locales.AttentionRunFinishedAnnouncement(0, 0)
		if failed {
			wantTitle, signal = locales.AttentionRunFailedTitle(), AccessibilitySignalTaskFailed
			wantAnnouncement = locales.AttentionRunFailedAnnouncement(0, 0)
		}
		wantChannels := signalChannels{signal, true, false, true, true}
		if notices[0].title != wantTitle || channelsFor(signal) != wantChannels {
			t.Fatalf("failed=%v: title %q, signal %v channels %+v", failed, notices[0].title, signal, channelsFor(signal))
		}
		if got := a.attention.drainAnnouncements(); !slices.Equal(got, []string{wantAnnouncement}) {
			t.Fatalf("failed=%v: announced %v", failed, got)
		}
	}
}

// TestAttentionSettingPersists catches a setting that changes the current
// service but is not read back from settings.json on the next launch.
func TestAttentionSettingPersists(t *testing.T) {
	a := workspaceApp()
	a.settingsPath = filepath.Join(t.TempDir(), "settings.json")
	a.attention = newAttentionService(a, &fakeAttentionChannels{})
	a.setAttentionMode(attentionOff)
	if err := a.persistSettings(); err != nil {
		t.Fatal(err)
	}
	saved, err := loadSettings(a.settingsPath)
	if err != nil {
		t.Fatal(err)
	}
	if saved.AttentionSignals != attentionOff {
		t.Fatalf("saved attention signals = %q, want off", saved.AttentionSignals)
	}
	reloaded := newApp()
	t.Cleanup(func() { _ = reloaded.srv.Close() })
	reloaded.attention.setMode(saved.AttentionSignals)
	if got := reloaded.attentionMode(); got != attentionOff {
		t.Fatalf("reloaded attention signals = %q, want off", got)
	}
}
