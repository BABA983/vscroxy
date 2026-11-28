package app

import (
	"path/filepath"
	"strings"

	"github.com/baba983/vscroxy/internal/uikit"
	"github.com/baba983/vscroxy/locales"
	"github.com/egoist/mygo/ui"
	"github.com/egoist/mygo/ui/editor"
)

// saveConflict is a library save that would replace a file someone else
// changed. The disk version is read-only; the draft stays in an editable
// buffer, so comparing versions never takes the draft away. Nothing is
// written until the reader confirms an overwrite.
type saveConflict struct {
	open    bool
	confirm bool
	source  *libraryConflictError
	files   []libraryConflict
	drafts  map[string]*editor.Buffer
	shown   string
	diff    *editor.DiffState
	diffFor string // the file the current diff was built for
	apply   func(edits map[string][]byte) error
}

// reviewSaveConflict opens the comparison for err, keeping the draft edits
// already made in an earlier visit.
func (a *app) reviewSaveConflict(err *libraryConflictError) {
	if err == nil || err.resolve == nil {
		return
	}
	if a.conflict == nil || a.conflict.source != err {
		s := &saveConflict{source: err, files: err.Files, drafts: make(map[string]*editor.Buffer, len(err.Files)), shown: err.Files[0].Name, apply: err.resolve}
		for _, file := range err.Files {
			s.drafts[file.Name] = editor.NewBuffer(string(file.Draft))
		}
		a.conflict = s
	}
	a.conflict.open, a.conflict.confirm = true, false
	a.dialogs.arrive(dialogSaveConflict)
	a.closeNotifications() // the notice's popover must not cover the dialog
	a.invalidate()
}

// offerSaveOverwrite is the notice's direct path to the same confirmation
// the comparison view offers. It never writes on the first click.
func (a *app) offerSaveOverwrite(err *libraryConflictError) {
	a.reviewSaveConflict(err)
	if a.conflict != nil {
		a.conflict.confirm = true
		a.invalidate()
	}
}

// editedDrafts reads what the draft side currently holds, including edits
// made while comparing, and pairs it with every file in the conflict.
func (s *saveConflict) editedDrafts() map[string][]byte {
	edits := make(map[string][]byte, len(s.files))
	for _, file := range s.files {
		edits[file.Name] = []byte(s.drafts[file.Name].String())
	}
	return edits
}

// overwriteSaveConflict writes the draft over the newer disk version only
// after the confirmation button. The versioned write backs up what was on
// disk first, so the outside change is still in local history.
func (a *app) overwriteSaveConflict() {
	s := a.conflict
	if s == nil || s.apply == nil {
		return
	}
	if err := s.apply(s.editedDrafts()); err != nil {
		s.open = false
		a.fail(locales.Save(), err)
		return
	}
	source := s.source
	a.conflict = nil
	a.spendConflictNotices(source)
	a.done(locales.SaveConflictOverwritten())
}

// saveConflictModal is the compare and overwrite view of the notice's
// choices. The draft side uses the app's shared diff view and stays editable.
func (a *app) saveConflictModal(c *ui.Context, t *ui.Theme) {
	s := a.conflict
	if s == nil || !s.open || len(s.files) == 0 {
		return
	}
	_, height := c.Size()
	uikit.Modal(c, &s.open, locales.SaveConflictTitle(), func() {
		uikit.DialogColumn(c, uikit.DialogLarge).Height(min(620, height-80)).Children(func() {
			uikit.DialogTitle(c, locales.SaveConflictTitle())
			uikit.Meta(c, locales.SaveConflictDetail(conflictNames(s.files)))
			if s.confirm {
				uikit.Hint(c, locales.OverwriteConfirmDetail())
				uikit.DialogFooter(c).Children(func() {
					if uikit.SecondaryButton(c, locales.Cancel()).Clicked() {
						s.confirm = false
					}
					if uikit.DestructiveButton(c, locales.OverwriteFile()).Clicked() {
						a.overwriteSaveConflict()
					}
				})
				return
			}
			a.saveConflictDiff(c, t, s)
			uikit.DialogFooter(c).Children(func() {
				if uikit.SecondaryButton(c, locales.Cancel()).Clicked() {
					s.open = false
				}
				if uikit.PrimaryButton(c, locales.OverwriteWithDraft()).Clicked() {
					s.confirm = true
				}
			})
		})
	})
}

func conflictNames(files []libraryConflict) string {
	names := make([]string, len(files))
	for i, file := range files {
		names[i] = file.Name
	}
	return strings.Join(names, ", ")
}

// saveConflictDiff builds the diff for the chosen file. The buffers live in
// the conflict state, so switching files or rebuilding the view keeps both
// the scroll position and what the reader typed into the draft.
func (a *app) saveConflictDiff(c *ui.Context, t *ui.Theme, s *saveConflict) {
	if len(s.files) > 1 {
		names := make([]string, len(s.files))
		for i, file := range s.files {
			names[i] = file.Name
		}
		uikit.SelectLabels(c, &s.shown, names, func(name string) string { return name }).Label(locales.SaveConflictFile())
	}
	var file libraryConflict
	for _, candidate := range s.files {
		if candidate.Name == s.shown {
			file = candidate
			break
		}
	}
	if s.diff == nil || s.diffFor != file.Name {
		language := editor.LanguageFor(filepath.Base(file.Name))
		old := editor.NewBuffer(string(file.Disk))
		old.SetLanguage(language)
		draft := s.drafts[file.Name]
		draft.SetLanguage(language)
		s.diff = &editor.DiffState{Old: old, New: draft, Editable: true, Layout: editor.DiffSplit, SplitPercent: a.diffSplit}
		s.diffFor = file.Name
	}
	ui.Row(c).Gap(t.Space(uikit.SpaceL)).Padding(0, 0, t.Space(uikit.SpaceXS), 0).Children(func() {
		uikit.Caption(c, locales.DiskVersion()).Grow(1)
		uikit.Caption(c, locales.YourDraft()).Grow(1)
	})
	s.diff.Font = ui.Font{Family: uikit.FontMono, Size: uikit.TextBody}
	// MyGo's DiffState has no focus query, so even this editable merge diff
	// keeps its caret marks quiet rather than guessing that it has the keys.
	s.diff.Theme = uikit.EditorThemeFor(t, false)
	editor.DiffView(c, s.diff).Grow(1).MinHeight(0).FillWidth()
	a.diffSplit = s.diff.SplitPercent
}
