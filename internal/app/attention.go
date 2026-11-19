package app

import (
	"net/url"
	"runtime"
	"slices"
	"strconv"
	"strings"
	"sync"

	"github.com/baba983/vscroxy/internal/capture"
	"github.com/baba983/vscroxy/locales"
	"github.com/egoist/mygo"
)

// AccessibilitySignal names the events that ask for the user's attention.
// The names follow VS Code's AccessibilitySignal (onDebugBreak,
// chatUserActionRequired, taskCompleted, taskFailed).
type AccessibilitySignal uint8

const (
	AccessibilitySignalOnDebugBreak AccessibilitySignal = iota
	AccessibilitySignalChatUserActionRequired
	AccessibilitySignalTaskCompleted
	AccessibilitySignalTaskFailed
)

// FocusMode is VS Code's FocusMode. A notification click uses Notify, which
// focuses the window in response to the user rather than stealing focus
// when a signal is raised.
type FocusMode uint8

const (
	FocusModeTransfer FocusMode = iota
	FocusModeNotify
	FocusModeForce
)

// attentionMode is the saved "Attention signals" choice. Empty is auto,
// like the other settings' zero values.
const (
	attentionAuto = "auto"
	attentionOn   = "on"
	attentionOff  = "off"

	attentionNotificationPrefix = "attention/"
	attentionBreakpointKind     = "breakpoint:"
	attentionConsentKind        = "mcp-consent:"
)

// signalChannels is the fixed signals table: one entry per event, saying
// which channels it uses. Announcement is always on while signals are on;
// it is the one channel that does not depend on the window's focus. The
// badge counts only things the user still has to handle, so a finished
// run raises no pending item.
type signalChannels struct {
	signal       AccessibilitySignal
	flash        bool
	badge        bool
	notification bool
	announcement bool
}

var accessibilitySignals = [...]signalChannels{
	{AccessibilitySignalOnDebugBreak, true, true, true, true},
	{AccessibilitySignalChatUserActionRequired, true, true, true, true},
	{AccessibilitySignalTaskCompleted, true, false, true, true},
	{AccessibilitySignalTaskFailed, true, false, true, true},
}

func channelsFor(signal AccessibilitySignal) signalChannels {
	for _, channels := range accessibilitySignals {
		if channels.signal == signal {
			return channels
		}
	}
	return signalChannels{}
}

// attentionItem is one event. Its ID identifies the thing a notification
// click opens; title and body hold no request contents, only host and
// method for a breakpoint.
type attentionItem struct {
	signal       AccessibilitySignal
	id           string
	title        string
	body         string
	announcement string
}

// attentionChannels are the OS calls, kept behind an interface so tests
// can assert exactly what the app asked the system to do.
type attentionChannels interface {
	flashWindow(on bool)
	setBadge(count int)
	showNotice(id, title, body string, onClick func())
	focus(mode FocusMode)
}

// attentionService plays VS Code's accessibility signals: an announcement
// for screen readers and, when the window is not in front, a flash or
// bounce, a pending badge and a notification that opens the exact item.
type attentionService struct {
	mu            sync.Mutex
	mode          string
	channels      attentionChannels
	focused       func() bool
	pending       map[string]struct{}
	announcements []string
	open          func(string)
}

func newAttentionService(a *app, channels attentionChannels) *attentionService {
	s := &attentionService{
		mode:     attentionAuto,
		channels: channels,
		focused:  a.windowFocused,
		open:     a.openAttentionItem,
	}
	return s
}

func (s *attentionService) setMode(mode string) {
	switch mode {
	case attentionOn, attentionOff:
	default:
		mode = attentionAuto
	}
	s.mu.Lock()
	s.mode = mode
	s.mu.Unlock()
}

// syncBadge writes the badge the current mode and pending items ask for.
// It is separate from setMode because settings are read before MyGo starts.
func (s *attentionService) syncBadge() {
	if s == nil || s.channels == nil {
		return
	}
	s.mu.Lock()
	count := countForMode(s.mode, len(s.pending))
	s.mu.Unlock()
	s.channels.setBadge(count)
}

func countForMode(mode string, pending int) int {
	if mode == attentionOff {
		return 0
	}
	return pending
}

func (a *app) attentionMode() string {
	if a.attention == nil {
		return attentionAuto
	}
	a.attention.mu.Lock()
	defer a.attention.mu.Unlock()
	return a.attention.mode
}

func (a *app) setAttentionMode(mode string) {
	if a.attention == nil {
		return
	}
	a.attention.setMode(mode)
	a.attention.syncBadge()
	a.persistSettings()
}

// play runs the signals table for one event. "on" signals even while the
// window is in front; "auto" only when it is not. "off" fires nothing at
// all, including no screen-reader announcement.
func (s *attentionService) play(item attentionItem) {
	if s == nil || s.channels == nil {
		return
	}
	channels := channelsFor(item.signal)
	s.mu.Lock()
	mode, silent := s.mode, !channels.announcement
	s.mu.Unlock()
	if mode == attentionOff || silent {
		return
	}
	// Read the window's focus before taking the lock: MyGo may need the
	// main thread, which can be drawing a frame that waits for this lock.
	// Announce even when the window is in front: a screen reader user is
	// already there and still needs the news, without a visual signal.
	awake := mode == attentionOn || s.focused == nil || !s.focused()
	s.mu.Lock()
	if s.mode == attentionOff {
		s.mu.Unlock()
		return
	}
	s.announcements = append(s.announcements, item.announcement)
	if !awake {
		s.mu.Unlock()
		return
	}
	if channels.badge && item.id != "" {
		if s.pending == nil {
			s.pending = make(map[string]struct{})
		}
		s.pending[item.id] = struct{}{}
	}
	count := countForMode(s.mode, len(s.pending))
	s.mu.Unlock()

	if channels.flash {
		s.channels.flashWindow(true)
	}
	if channels.badge {
		s.channels.setBadge(count)
	}
	if channels.notification {
		id := item.id
		if id == "" {
			id = item.title
		}
		s.channels.showNotice(id, item.title, item.body, func() { s.click(id) })
	}
}

// clear removes a handled breakpoint or consent from the pending badge.
func (s *attentionService) clear(id string) {
	if s == nil || s.channels == nil || id == "" {
		return
	}
	s.mu.Lock()
	if _, ok := s.pending[id]; !ok {
		s.mu.Unlock()
		return
	}
	delete(s.pending, id)
	count := countForMode(s.mode, len(s.pending))
	s.mu.Unlock()
	s.channels.setBadge(count)
}

// click follows a notification: handle the pending item before opening it,
// so an already answered breakpoint does not reopen.
func (s *attentionService) click(id string) {
	if s == nil {
		return
	}
	s.clear(id)
	if s.open != nil {
		s.open(id)
	}
}

// focusGained stops the window's flash or bounce once the user is back.
func (s *attentionService) focusGained() {
	if s != nil && s.channels != nil {
		s.channels.flashWindow(false)
	}
}

func (s *attentionService) drainAnnouncements() []string {
	if s == nil {
		return nil
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	out := s.announcements
	s.announcements = nil
	return out
}

// windowFocused is the default source for auto: the app's own window in
// front, not another window or another app.
func (a *app) windowFocused() bool {
	if a.win != nil && a.win.IsFocused() {
		return true
	}
	if a.settingsWin != nil && a.settingsWin.IsFocused() {
		return true
	}
	return false
}

// liveAttentionChannels uses MyGo's platform APIs. Each backend quietly
// ignores what its platform cannot show.
type liveAttentionChannels struct {
	win func() *mygo.Window
}

func (c liveAttentionChannels) flashWindow(on bool) {
	if runtime.GOOS == "darwin" {
		if on {
			mygo.App.Dock.Bounce(false)
		}
		return
	}
	if w := c.win(); w != nil {
		w.FlashFrame(on)
	}
}

func (c liveAttentionChannels) setBadge(count int) {
	mygo.App.SetBadgeCount(count)
}

// focus takes the focus only for a user's click on a notification, never
// when a signal is raised.
func (c liveAttentionChannels) focus(mode FocusMode) {
	if mode == FocusModeTransfer {
		return
	}
	if w := c.win(); w != nil {
		w.Focus()
	}
}

func (c liveAttentionChannels) showNotice(id, title, body string, onClick func()) {
	if !mygo.NotificationsSupported() {
		return
	}
	n := mygo.NewNotification(mygo.NotificationOptions{
		ID:    attentionNotificationPrefix + id,
		Group: "attention",
		Title: title,
		Body:  body,
	})
	n.OnClick(onClick)
	// Show waits for the system's answer (and for macOS's first-use
	// permission); an event must never hold up the view or the capture.
	go func() { _ = n.Show() }()
}

func breakpointAttentionID(id int) string {
	return attentionBreakpointKind + strconv.Itoa(id)
}

func consentAttentionID(shape string) string {
	return attentionConsentKind + shape
}

// breakpointAttention reports host + method only, never a URL path, query,
// header or body. An unparsable URL leaves the host unnamed.
func breakpointAttention(bpMethod, bpURL string) attentionItem {
	host := ""
	if u, err := url.Parse(bpURL); err == nil {
		host = u.Hostname()
	}
	return attentionItem{
		signal:       AccessibilitySignalOnDebugBreak,
		id:           "",
		title:        locales.AttentionBreakpointTitle(),
		body:         locales.AttentionBreakpoint(bpMethod, host),
		announcement: locales.AttentionBreakpointAnnouncement(bpMethod, host),
	}
}

func consentAttention() attentionItem {
	return attentionItem{
		signal:       AccessibilitySignalChatUserActionRequired,
		id:           "",
		title:        locales.AttentionMcpConsentTitle(),
		body:         locales.AttentionMcpConsentBody(),
		announcement: locales.AttentionMcpConsentTitle(),
	}
}

func runAttention(failed bool, completed, failures int) attentionItem {
	if failed {
		return attentionItem{
			signal:       AccessibilitySignalTaskFailed,
			title:        locales.AttentionRunFailedTitle(),
			body:         locales.AttentionRunResult(completed, failures),
			announcement: locales.AttentionRunFailedAnnouncement(completed, failures),
		}
	}
	return attentionItem{
		signal:       AccessibilitySignalTaskCompleted,
		title:        locales.AttentionRunFinishedTitle(),
		body:         locales.AttentionRunResult(completed, failures),
		announcement: locales.AttentionRunFinishedAnnouncement(completed, failures),
	}
}

// openAttentionItem is the only path that takes focus, and it runs only
// after the user clicks a notification (possibly in an earlier run).
func (a *app) openAttentionItem(id string) {
	switch {
	case strings.HasPrefix(id, attentionBreakpointKind):
		a.focusAttentionWindow(FocusModeNotify)
		a.openQueuedBreakpoint(strings.TrimPrefix(id, attentionBreakpointKind))
	case strings.HasPrefix(id, attentionConsentKind):
		a.focusAttentionWindow(FocusModeNotify)
		a.openPendingConsent(strings.TrimPrefix(id, attentionConsentKind))
	case id != "":
		a.focusAttentionWindow(FocusModeNotify)
	}
	a.invalidate()
}

func (a *app) focusAttentionWindow(mode FocusMode) {
	if mode == FocusModeTransfer || a.attention == nil || a.attention.channels == nil {
		return
	}
	a.attention.channels.focus(mode)
}

// openQueuedBreakpoint shows the exact paused exchange the notification
// named. A pause already in the editor goes back in front of the queue so
// it is not lost; its edits are not carried over when it opens again.
func (a *app) openQueuedBreakpoint(id string) {
	if id == "" {
		return
	}
	a.mu.Lock()
	var next *capture.Breakpoint
	if a.bp != nil && strconv.Itoa(a.bp.ID) == id {
		next = a.bp
	} else if i := slices.IndexFunc(a.bps, func(bp capture.Breakpoint) bool {
		return strconv.Itoa(bp.ID) == id
	}); i >= 0 {
		bp := a.bps[i]
		a.bps = slices.Delete(a.bps, i, i+1)
		if a.bp != nil && a.bp.ID != bp.ID {
			a.bps = append([]capture.Breakpoint{*a.bp}, a.bps...)
		}
		next = &bp
	}
	a.mu.Unlock()
	if next != nil {
		a.showBreakpoint(next)
	}
}

// openPendingConsent brings the document with this exact prompt back to the
// front. A prompt that was handled already has no state to reopen.
func (a *app) openPendingConsent(shape string) {
	s := &a.mcpConsent
	if !s.open || s.shape != shape || s.d == nil {
		return
	}
	a.requestDocument = s.d
	a.page = pageRequest
	s.open = true
}
