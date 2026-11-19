package app

import (
	"errors"
	"log/slog"
	"slices"
	"sync"
	"time"

	"github.com/baba983/vscroxy/internal/store"
	"github.com/baba983/vscroxy/locales"

	"github.com/egoist/mygo/ui"
)

// noticeLevel is how much a notice matters. Info and success fade after a
// few seconds; warnings and errors stay until closed (UX guidelines §8).
type noticeLevel int

const (
	noticeInfo noticeLevel = iota
	noticeSuccess
	noticeWarning
	noticeError
)

// notice is feedback on what just happened: one sentence, the raw error or
// other detail under it, and at most one action such as Undo or Inspect.
type notice struct {
	id     uint64
	at     time.Time
	level  noticeLevel
	text   string
	detail string
	action string
	read   bool
	// sticky keeps an info notice until it is closed, for something worth
	// acting on that did not just happen, such as an available update.
	sticky bool
	run    func() // runs on the UI thread
	// spent is set once the action has been used, in a toast or in the
	// center: Undo and Reload are one-shot and must not run a second time.
	spent bool
}

// line is the notice as one line, as it was shown before levels existed.
func (n notice) line() string {
	if n.detail == "" {
		return n.text
	}
	return n.text + ": " + n.detail
}

// notify queues n for the window in front, from any goroutine, and keeps it
// in the in-memory notification history. Errors also go to the log, which
// never holds request contents: only the text and detail.
func (a *app) notify(n notice) {
	if n.level == noticeError {
		slog.Error(n.text, "detail", n.detail)
	}
	center := a.noticeCenter()
	a.mu.Lock()
	n.at = time.Now()
	n.id = center.nextID
	center.nextID++
	a.notices = append(a.notices, n)
	if len(a.notices) > noticeHistoryLimit {
		a.notices = slices.Clone(a.notices[len(a.notices)-noticeHistoryLimit:])
	}
	a.message = n.line()
	center.pending = append(center.pending, n)
	a.mu.Unlock()
	a.invalidate()
	if a.settingsWin != nil {
		a.settingsWin.Invalidate()
	}
	a.invalidateTools()
}

// Run installs fail as the error handler, so a recovered panic reaches the
// same notice as any other error in the running app.
var unexpectedError struct {
	sync.RWMutex
	handler func(string, error)
}

func setUnexpectedErrorHandler(handler func(string, error)) {
	unexpectedError.Lock()
	unexpectedError.handler = handler
	unexpectedError.Unlock()
}

func onUnexpectedError(err error) {
	unexpectedError.RLock()
	handler := unexpectedError.handler
	unexpectedError.RUnlock()
	if handler != nil {
		handler(locales.UnexpectedBackgroundError(), err)
	}
}

// inform, done and warn are notify at one level, for the common case.
func (a *app) inform(text string) { a.notify(notice{level: noticeInfo, text: text}) }
func (a *app) done(text string)   { a.notify(notice{level: noticeSuccess, text: text}) }
func (a *app) warn(text string)   { a.notify(notice{level: noticeWarning, text: text}) }

// fail says what could not be done; the error itself is the detail.
// A file changed on disk offers to read it again.
func (a *app) fail(what string, err error) {
	n := notice{level: noticeError, text: what, detail: err.Error()}
	if errors.Is(err, store.ErrChanged) {
		n.action, n.run = locales.ReloadAction(), a.reloadFromDisk
	}
	a.notify(n)
}

// showNotices hands queued notices to c as toasts. The notices stay in the
// in-memory history: a toast goes away, the notification center does not.
// The Settings window shows them while it has the focus; otherwise the main
// window does.
func (a *app) showNotices(c *ui.Context) {
	center := a.noticeCenter()
	a.mu.Lock()
	queued := center.pending
	center.pending = nil
	a.mu.Unlock()
	for _, n := range queued {
		id := n.id
		t := ui.Toast{ID: n.line(), Title: n.text, Description: n.detail, Action: n.action,
			OnAction: func() {
				a.markNoticeSpent(id)
				if n.run != nil {
					n.run()
				}
			}}
		if n.action != "" {
			t.Timeout = 10 * time.Second // time to read it and decide
		}
		if n.sticky {
			t.Timeout = -1
		}
		switch n.level {
		case noticeSuccess:
			t.Type = "success"
		case noticeWarning:
			t.Type, t.Timeout = "warning", -1
		case noticeError:
			t.Type, t.Timeout = "error", -1
		}
		c.AddToast(t)
	}
}
