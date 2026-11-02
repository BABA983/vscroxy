package app

import (
	"errors"
	"log/slog"
	"time"

	"github.com/baba983/vscroxy/internal/store"

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
	level  noticeLevel
	text   string
	detail string
	action string
	run    func() // runs on the UI thread
}

// line is the notice as one line, as it was shown before levels existed.
func (n notice) line() string {
	if n.detail == "" {
		return n.text
	}
	return n.text + ": " + n.detail
}

// notify queues n for the window in front, from any goroutine. Errors also go
// to the log, which never holds request contents: only the text and detail.
func (a *app) notify(n notice) {
	if n.level == noticeError {
		slog.Error(n.text, "detail", n.detail)
	}
	a.mu.Lock()
	a.notices = append(a.notices, n)
	a.message = n.line()
	a.mu.Unlock()
	a.invalidate()
	if a.settingsWin != nil {
		a.settingsWin.Invalidate()
	}
	a.invalidateTools()
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
		n.action, n.run = "Reload", a.reloadFromDisk
	}
	a.notify(n)
}

// showNotices hands queued notices to c as toasts. The Settings window shows
// them while it has the focus; otherwise the main window does.
func (a *app) showNotices(c *ui.Context) {
	a.mu.Lock()
	queued := a.notices
	a.notices = nil
	a.mu.Unlock()
	for _, n := range queued {
		t := ui.Toast{ID: n.line(), Title: n.text, Description: n.detail, Action: n.action, OnAction: n.run}
		if n.action != "" {
			t.Timeout = 10 * time.Second // time to read it and decide
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
