package app

import (
	"bytes"
	"errors"
	"io/fs"

	"github.com/baba983/vscroxy/internal/undo"
	"github.com/baba983/vscroxy/locales"
)

const (
	undoCaptureResource = "capture"
	undoDraftsResource  = "drafts"
)

// undoResource is one resource an undo element belongs to. A file resource
// is also the thing checked on disk before the element runs; capture rows and
// open drafts are in-memory resources with no file to check.
type undoResource struct {
	id   string
	path string
}

// undoRecord describes one operation after it succeeded. The two snapshots
// are the state before and after it; undo applies the first, redo the second.
type undoRecord struct {
	code, label string
	before      undoState
	after       undoState
	notice      func(redo bool)
	// inverse actions replace the state snapshots for operations that must
	// preserve later, unrelated work, such as moving or deleting rows.
	undoAction, redoAction func() error
}

func (a *app) undoHistory() *undo.Service {
	if a.undoService == nil {
		a.undoService = undo.NewService()
	}
	return a.undoService
}

// recordUndo pushes a reversible operation. Nothing is recorded for a failed
// or empty operation, and code inside withoutUndo updates state without
// adding another element (it is the undo of an earlier one, or part of an
// operation recorded by its caller).
func (a *app) recordUndo(r undoRecord) *undo.Handle {
	if a.undoSuppressed > 0 {
		return nil
	}
	resources := a.diffUndoResources(r.before, r.after)
	if len(resources) == 0 {
		return nil
	}
	// The expected bytes follow the applied state: after an undo the files
	// hold the old state, so that is what the paired redo checks.
	expected := make([][]byte, len(resources))
	refresh := func() {
		for i, resource := range resources {
			expected[i], _ = a.undoResourceBytes(resource)
		}
	}
	refresh()
	verify := func() error {
		for i, resource := range resources {
			onDisk, err := a.undoResourceBytes(resource)
			if resource.path != "" {
				if errors.Is(err, fs.ErrNotExist) {
					onDisk, err = nil, nil
				}
				if err != nil || !bytes.Equal(expected[i], onDisk) {
					return undoFileError(resource.path, err)
				}
			}
		}
		return nil
	}
	undoFn := func() error {
		var err error
		if r.undoAction != nil {
			err = r.undoAction()
		} else {
			err = r.before.apply(a, &r.after)
			a.undoApplyCount++
		}
		if err != nil {
			return err
		}
		refresh()
		if r.notice != nil {
			r.notice(false)
		}
		return nil
	}
	redoFn := func() error {
		var err error
		if r.redoAction != nil {
			err = r.redoAction()
		} else {
			err = r.after.apply(a, &r.before)
			a.undoApplyCount++
		}
		if err != nil {
			return err
		}
		refresh()
		if r.notice != nil {
			r.notice(true)
		}
		return nil
	}
	service := a.undoHistory()
	ids := make([]string, 0, len(resources))
	for _, resource := range resources {
		ids = append(ids, resource.id)
	}
	// One operation over several resources is one workspace element. A
	// single element means one write and one notice per undo or redo.
	if len(ids) > 1 {
		return service.Push(undo.NewWorkspace(ids, r.label, r.code, verify, undoFn, redoFn), nil)
	}
	return service.Push(undo.NewResource(ids[0], r.label, r.code, verify, undoFn, redoFn), nil)
}

func (a *app) withoutUndo(fn func()) {
	a.undoSuppressed++
	defer func() { a.undoSuppressed-- }()
	fn()
}

// undo undoes the newest operation, whatever resource it touched.
func (a *app) undo() {
	service := a.undoHistory()
	if !service.CanUndo() {
		return
	}
	if err := service.Undo(); err != nil {
		a.undoFailed(false, err)
		return
	}
	a.invalidate()
}

// redo redoes the newest undone operation.
func (a *app) redo() {
	service := a.undoHistory()
	if !service.CanRedo() {
		return
	}
	if err := service.Redo(); err != nil {
		a.undoFailed(true, err)
		return
	}
	a.invalidate()
}

func (a *app) undoFailed(redo bool, err error) {
	var refused *undo.VerificationError
	if errors.As(err, &refused) {
		text, detail := locales.Undo(), locales.UndoChangedOnDisk(refused.Label)
		if redo {
			text, detail = locales.Redo(), locales.RedoChangedOnDisk(refused.Label)
		}
		a.notify(notice{level: noticeWarning, text: text, detail: detail})
		return
	}
	text := locales.Undo()
	if redo {
		text = locales.Redo()
	}
	a.fail(text, err)
}

// undoNotice is the toast action of an operation: it undoes that operation
// through the same service as Cmd-Z, and hides once the element is gone.
func (a *app) undoNotice(text string, handle *undo.Handle, valid ...func() bool) {
	canUndo := func() bool {
		if !a.undoHistory().HasPast(handle) {
			return false
		}
		return len(valid) == 0 || valid[0] == nil || valid[0]()
	}
	a.notify(notice{level: noticeSuccess, text: text, action: locales.Undo(),
		valid: canUndo,
		run: func() {
			if !canUndo() {
				return
			}
			_ = a.undoHistory().UndoHandle(handle)
			a.invalidate()
		}})
}
