package app

// withProgress is the app's one long-task mechanism. Its notice keeps one id
// from start to finish: reports replace the text and percentage of that
// notice instead of adding another, Cancel cancels the task's context, and
// the same notice ends as done, failed or cancelled. The three locations
// mirror VS Code's IProgressLocation: Window in the status bar, Notification
// as a toast, Dialog in a modal.

import (
	"context"
	"errors"
	"slices"

	"github.com/baba983/vscroxy/internal/applog"
	"github.com/baba983/vscroxy/internal/uikit"
	"github.com/baba983/vscroxy/locales"
	"github.com/egoist/mygo/ui"
)

type progressLocation int

const (
	progressWindow progressLocation = iota
	progressNotification
	progressDialog
)

// progressReport is what a task tells the person watching it. A negative
// percentage is activity of unknown length.
type progressReport struct {
	message    string
	percentage int
}

type progressTask func(ctx context.Context, report func(progressReport)) (string, error)

type progressOptions struct {
	title       string
	location    progressLocation
	cancellable bool
	// finish runs on the UI thread once the worker succeeded. It applies
	// the result and returns the text of the final notice.
	finish func() (string, error)
	// settle runs on the UI thread either way, to put busy flags away.
	settle func()
}

// progressRun is the handle to a started task. done lets a test finish
// without watching a clock or waiting for a frame.
type progressRun struct {
	id   uint64
	done chan struct{}
}

type progressPhase int

const (
	progressRunning progressPhase = iota
	progressDone
	progressFailed
	progressCancelled
)

// progressState lives on a notice. The app's mu guards all of its fields;
// cancel is cleared when it has run so Cancel is one-shot.
type progressState struct {
	id            uint64
	title         string
	phase         progressPhase
	location      progressLocation
	cancellable   bool
	percentage    int
	cancel        func()
	cancelWanted  bool
}

type progressSnapshot struct {
	active      bool
	id          uint64
	text        string
	percentage  int
	cancellable bool
	phase       progressPhase
}

// withProgress starts task on a worker and returns at once. A returned text
// becomes the success notice; an error becomes the failure detail. Cancelling
// the notice cancels ctx, and a task that answers with context.Canceled ends
// as cancelled rather than failed.
func (a *app) withProgress(opts progressOptions, task progressTask) *progressRun {
	ctx, cancel := context.WithCancel(context.Background())
	p := &progressState{
		title:       opts.title,
		phase:       progressRunning,
		location:    opts.location,
		cancellable: opts.cancellable,
		percentage:  -1,
		cancel:      cancel,
	}
	n := notice{level: noticeInfo, sticky: true, text: opts.title, progress: p}
	if opts.cancellable {
		// notify assigns the id, so the action reads it from p later.
		n.action, n.run = locales.Cancel(), func() { a.cancelProgress(p.id) }
		n.valid = func() bool {
			a.mu.Lock()
			defer a.mu.Unlock()
			return a.progressPhase(p.id) == progressRunning
		}
	}
	a.notify(n)
	run := &progressRun{id: p.id, done: make(chan struct{})}
	if opts.location == progressDialog {
		a.mu.Lock()
		a.progressDialogID = p.id
		a.mu.Unlock()
		a.publishProgress(func() { a.progressDialogOpen = true })
	}
	go guarded(func() {
		defer close(run.done)
		text, err := task(ctx, func(update progressReport) {
			a.publishProgress(func() { a.reportProgress(run.id, update) })
		})
		if err == nil {
			err = ctx.Err()
		}
		a.publishProgress(func() {
			if opts.settle != nil {
				opts.settle()
			}
			if err == nil && opts.finish != nil {
				finished, finishErr := opts.finish()
				if finishErr != nil {
					err = finishErr
				} else {
					text = finished
				}
			}
			a.finishProgress(run.id, text, err)
			if opts.location == progressDialog {
				a.mu.Lock()
				current := a.progressDialogID
				a.mu.Unlock()
				if current == run.id {
					a.progressDialogOpen = false
				}
			}
		})
	})
	return run
}

// publishProgress hands a notice change to the UI thread. A Tester has no
// native main loop; there the change is already on the thread that draws it.
func (a *app) publishProgress(fn func()) {
	if a.quitOnMain != nil {
		a.quitOnMain(fn)
		return
	}
	if a.win != nil {
		a.win.Update(fn)
		return
	}
	fn()
}

// reportProgress edits the one notice in place. Old queued copies with the
// same id are dropped, so a rebuilding window shows the latest state once.
func (a *app) reportProgress(id uint64, update progressReport) {
	a.mu.Lock()
	i := a.noticeIndex(id)
	if i < 0 || a.notices[i].progress == nil || a.notices[i].progress.phase != progressRunning {
		a.mu.Unlock()
		return
	}
	n := &a.notices[i]
	if update.message != "" {
		n.text = update.message
	}
	percent := update.percentage
	if percent >= 0 {
		percent = min(percent, 100)
	}
	n.progress.percentage = percent
	if percent >= 0 {
		n.detail = locales.ProgressPercent(percent)
	} else {
		n.detail = ""
	}
	a.message = n.line()
	queueNoticeLocked(a.noticeCenter(), *n)
	a.mu.Unlock()
	a.invalidate()
}

// cancelProgress is the Cancel action on a progress notice, in any location.
// It cancels the task's context and marks the notice as cancelling until the
// task answers.
func (a *app) cancelProgress(id uint64) {
	a.mu.Lock()
	i := a.noticeIndex(id)
	if i < 0 || a.notices[i].progress == nil {
		a.mu.Unlock()
		return
	}
	p := a.notices[i].progress
	cancel := p.cancel
	if p.phase != progressRunning || cancel == nil {
		a.mu.Unlock()
		return
	}
	p.cancel, p.cancelWanted = nil, true
	n := &a.notices[i]
	n.text, n.detail = locales.ProgressCancelling(), ""
	a.message = n.line()
	queueNoticeLocked(a.noticeCenter(), *n)
	a.mu.Unlock()
	cancel()
	a.invalidate()
}

// finishProgress turns the progress notice into its result. It never adds a
// second notice: the final toast and the center use the same id.
func (a *app) finishProgress(id uint64, text string, err error) {
	a.mu.Lock()
	i := a.noticeIndex(id)
	if i < 0 || a.notices[i].progress == nil {
		a.mu.Unlock()
		return
	}
	n := &a.notices[i]
	p := n.progress
	cancelled := p.cancelWanted || errors.Is(err, context.Canceled)
	cancel := p.cancel
	p.cancel = nil
	switch {
	case cancelled:
		p.phase = progressCancelled
		n.level, n.text, n.detail = noticeCancelled, locales.ProgressCancelled(p.title), ""
	case err != nil:
		p.phase = progressFailed
		n.level, n.text, n.detail = noticeError, p.title, err.Error()
	default:
		p.phase = progressDone
		if text == "" {
			text = locales.ProgressDone(p.title)
		}
		n.level, n.text, n.detail = noticeSuccess, text, ""
	}
	n.action, n.run, n.valid, n.spent, n.sticky = "", nil, nil, false, false
	a.message = n.line()
	queueNoticeLocked(a.noticeCenter(), *n)
	a.mu.Unlock()
	if cancel != nil {
		cancel()
	}
	if err != nil && !cancelled {
		applog.For("app").Error(n.text, "detail", err.Error())
	}
	a.invalidate()
}

// progressPhase reads a task's phase under a.mu.
func (a *app) progressPhase(id uint64) progressPhase {
	i := a.noticeIndex(id)
	if i < 0 || a.notices[i].progress == nil {
		return progressDone
	}
	return a.notices[i].progress.phase
}

// queueNoticeLocked replaces any pending copy of n, keeping a rebuilding
// frame from stacking two versions of the same notice. The caller holds a.mu.
func queueNoticeLocked(center *noticeCenter, n notice) {
	for i := len(center.pending) - 1; i >= 0; i-- {
		if center.pending[i].id == n.id {
			center.pending = slices.Delete(center.pending, i, i+1)
		}
	}
	center.pending = append(center.pending, n)
}

// importProgress is the shared form of a collection import. load reads on a
// worker and hands back the UI-thread merge; the merge's own final notice
// becomes the progress notice's result.
func (a *app) importProgress(title string, load func(context.Context, func(progressReport)) (func(), error)) *progressRun {
	var apply func()
	return a.withProgress(progressOptions{
		title:       title,
		location:    progressNotification,
		cancellable: true,
		finish: func() (string, error) {
			if apply == nil {
				return "", nil
			}
			return a.resultNotice(apply)
		},
	}, func(ctx context.Context, report func(progressReport)) (string, error) {
		report(progressReport{message: title, percentage: 10})
		merge, err := load(ctx, report)
		if err != nil {
			return "", err
		}
		if err := ctx.Err(); err != nil {
			return "", err
		}
		report(progressReport{message: title, percentage: 90})
		apply = merge
		return "", nil
	})
}

// resultNotice runs a UI-thread import and, when it adds one final notice,
// moves that notice's text onto the progress result. The task then ends in a
// single notice instead of a generic Done beside an Imported one.
func (a *app) resultNotice(fn func()) (string, error) {
	before := len(a.notices)
	fn()
	if len(a.notices) <= before {
		return "", nil
	}
	n := a.notices[before]
	if n.progress != nil || n.action != "" {
		return "", nil
	}
	a.mu.Lock()
	a.notices = slices.Delete(a.notices, before, before+1)
	a.noticeCenter().pending = slices.DeleteFunc(a.noticeCenter().pending, func(p notice) bool { return p.id == n.id })
	a.mu.Unlock()
	if n.level == noticeError {
		return n.text, errors.New(n.detail)
	}
	return n.text, nil
}

// progressSnapshot is the latest running task in one location, read under
// a.mu so a worker cannot change it while the view builds.
func (a *app) progressSnapshot(location progressLocation) progressSnapshot {
	a.mu.Lock()
	defer a.mu.Unlock()
	var out progressSnapshot
	for i := len(a.notices) - 1; i >= 0; i-- {
		n := a.notices[i]
		if n.progress == nil || n.progress.phase != progressRunning || n.progress.location != location {
			continue
		}
		out = progressSnapshot{active: true, id: n.id, text: n.text, percentage: n.progress.percentage,
			cancellable: n.progress.cancellable, phase: n.progress.phase}
		break
	}
	return out
}

// progressStatus is the Window location: one live row in the status bar. It
// stays out of the way while no task is running there.
func (a *app) progressStatus(c *ui.Context, t *ui.Theme) {
	p := a.progressSnapshot(progressWindow)
	if !p.active {
		return
	}
	ui.Row(c.Key("progress-status")).Shrink(0).Gap(t.Space(uikit.SpaceXS)).AlignItems(ui.Center).Padding(0, t.Space(uikit.SpaceS)).Children(func() {
		fraction := 0.0
		if p.percentage >= 0 {
			fraction = float64(p.percentage) / 100
		} else {
			fraction = -1
		}
		uikit.ProgressBar(c, fraction, p.text).Width(120)
		uikit.Caption(c, p.text).SingleLine()
		if p.percentage >= 0 {
			uikit.Caption(c, locales.ProgressPercent(p.percentage)).FontFeatures("tnum").SingleLine()
		}
		if p.cancellable {
			if uikit.SecondaryButton(c, locales.Cancel()).Clicked() {
				a.cancelProgress(p.id)
			}
		}
	})
}

// progressDialog is the Dialog location: the same live state in a modal,
// including the one Cancel action.
func (a *app) progressDialog(c *ui.Context, t *ui.Theme) {
	if !a.progressDialogOpen {
		return
	}
	p := a.progressSnapshot(progressDialog)
	if !p.active {
		return
	}
	uikit.Modal(c, &a.progressDialogOpen, p.text, func() {
		uikit.DialogColumn(c, uikit.DialogSmall).Children(func() {
			uikit.DialogTitle(c, p.text)
			fraction := -1.0
			if p.percentage >= 0 {
				fraction = float64(p.percentage) / 100
			}
			uikit.ProgressBar(c, fraction, p.text)
			uikit.DialogFooter(c).Children(func() {
				if p.cancellable {
					if uikit.SecondaryButton(c, locales.Cancel()).Clicked() {
						a.cancelProgress(p.id)
					}
				}
			})
		})
	})
}
