package app

import (
	"bytes"
	"errors"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"slices"
	"strings"

	"github.com/baba983/vscroxy/internal/store"
	"github.com/baba983/vscroxy/internal/uikit"
	"github.com/baba983/vscroxy/locales"
	"github.com/egoist/mygo/ui"
	"github.com/egoist/mygo/ui/editor"
)

// historyView is the local history of one collection file. The backup list
// comes from store, the chosen version is compared with the file as it is now,
// and a restore goes through the same versioned write the app uses for saves.
type historyView struct {
	open    bool
	title   string
	target  string
	entries []store.BackupEntry
	picked  int
	list    ui.ListState
	diff    *editor.DiffState
	shown   string
	confirm bool
}

// savedRequestPath finds the library file of r. The marshalled bytes identify
// it exactly, including the name escape and any same-name request's numbered
// suffix; the name path is only a fallback for a file edited outside the app.
func (a *app) savedRequestPath(r savedRequest) (string, error) {
	if a.library.root == "" {
		return "", errors.New("no library folder is open")
	}
	files, err := libraryFiles(a.requests, a.folders)
	if err != nil {
		return "", err
	}
	fileRequest := r.Request
	fileRequest.Collection = "" // the directory, not the JSON, holds it
	want, err := marshalFile(savedRequest{Request: fileRequest, Source: r.Source})
	if err != nil {
		return "", err
	}
	for name, data := range files {
		if strings.HasSuffix(name, requestFileSuffix) && bytes.Equal(data, want) {
			return filepath.Join(a.library.root, filepath.FromSlash(name)), nil
		}
	}
	name := path.Join(dirOf(r.Collection), escapeName(r.Name)+requestFileSuffix)
	return filepath.Join(a.library.root, filepath.FromSlash(name)), nil
}

func (a *app) folderFilePath(collection string) string {
	name := path.Join(dirOf(collection), folderFileName)
	return filepath.Join(a.library.root, filepath.FromSlash(name))
}

// openRequestHistory shows the versions of one saved request's file.
func (a *app) openRequestHistory(key string) {
	i := slices.IndexFunc(a.requests, func(r savedRequest) bool { return r.key() == key })
	if i < 0 {
		a.warn(locales.LocalHistoryMissing())
		return
	}
	target, err := a.savedRequestPath(a.requests[i])
	if err != nil {
		a.fail(locales.LocalHistory(), err)
		return
	}
	a.openLocalHistory(target, locales.LocalHistoryFor(a.requests[i].Name))
}

// openFolderHistory shows the versions of one collection's or folder's
// folder.json. Its path names the level in the collection tree.
func (a *app) openFolderHistory(collection string) {
	title := locales.LocalHistoryFor(collection)
	if collection == "" {
		title = locales.LocalHistoryFor(locales.SavedRequests())
	}
	a.openLocalHistory(a.folderFilePath(collection), title)
}

func (a *app) libraryBackupDir() string {
	if a.library.backupDir != "" {
		return a.library.backupDir
	}
	return a.backupDir
}

func (a *app) openLocalHistory(target, title string) {
	entries, err := store.ListBackups(a.libraryBackupDir(), target)
	if err != nil {
		a.fail(locales.LocalHistory(), err)
		return
	}
	a.history = historyView{open: true, title: title, target: target, entries: entries}
	a.history.list.Selected = &a.history.picked
	a.history.list.Key = func(i int) any { return a.history.entries[i].Path }
	a.history.list.Label = func(i int) string { return historyEntryLabel(a.history.entries[i]) }
	a.invalidate()
}

// historyEntryLabel gives a backup's time in the reader's own locale, with
// the same shape wherever the time appears.
func historyEntryLabel(entry store.BackupEntry) string {
	return locales.SavedAt(entry.At.Local().Format("2006-01-02 15:04:05"))
}

// restoreHistory replaces the current file with the chosen version. It
// backs up the current file first, so the restore is a new history entry and
// can be undone by restoring the entry it just made.
func (a *app) restoreHistory() {
	v := &a.history
	if v.picked < 0 || v.picked >= len(v.entries) {
		return
	}
	entry := v.entries[v.picked]
	data, err := os.ReadFile(entry.Path)
	if err != nil {
		a.fail(locales.LocalHistory(), err)
		return
	}
	if err := store.WriteFileVersioned(v.target, store.Collection, data, a.libraryBackupDir()); err != nil {
		a.fail(locales.RestoreVersion(), err)
		return
	}
	v.open = false
	a.loadRequestLibrary()
	a.refreshDocuments()
	a.done(locales.RestoredVersion(historyEntryLabel(entry)))
}

// historyModal lists the backups newest first, with a diff of the selected
// version against the file as it is now. Restoring takes one confirmation.
func (a *app) historyModal(c *ui.Context, t *ui.Theme) {
	v := &a.history
	if !v.open {
		return
	}
	_, height := c.Size()
	uikit.Modal(c, &v.open, v.title, func() {
		uikit.DialogColumn(c, uikit.DialogLarge).Height(min(620, height-80)).Children(func() {
			uikit.DialogTitle(c, v.title)
			if v.confirm {
				if v.picked < 0 || v.picked >= len(v.entries) {
					v.confirm = false
					return
				}
				uikit.Hint(c, locales.RestoreVersionConfirm(historyEntryLabel(v.entries[v.picked])))
				uikit.Meta(c, locales.RestoreVersionDetail())
				uikit.DialogFooter(c).Children(func() {
					if uikit.SecondaryButton(c, locales.Cancel()).Clicked() {
						v.confirm = false
					}
					if uikit.PrimaryButton(c, locales.RestoreVersion()).Clicked() {
						a.restoreHistory()
					}
				})
				return
			}
			uikit.Meta(c, locales.LocalHistoryHint())
			ui.Row(c).Grow(1).FillHeight().MinHeight(0).Gap(t.Space(uikit.SpaceL)).Children(func() {
				a.historyList(c, t, v)
				ui.Column(c).Grow(1).FillHeight().MinWidth(0).MinHeight(0).Gap(t.Space(uikit.SpaceXS)).Children(func() {
					if v.picked >= 0 && v.picked < len(v.entries) {
						a.historyDiff(c, t, v)
					} else {
						uikit.Meta(c, locales.NoSavedVersions())
					}
				})
			})
			uikit.DialogFooter(c).Children(func() {
				if uikit.SecondaryButton(c, locales.Cancel()).Clicked() {
					v.open = false
				}
				if uikit.PrimaryButton(c, locales.RestoreVersionEllipsis()).Disabled(len(v.entries) == 0).Clicked() {
					v.confirm = true
				}
			})
		})
	})
}

func (a *app) historyList(c *ui.Context, t *ui.Theme, v *historyView) {
	pick := *t
	pick.Accent, pick.AccentText = ui.Color{}, t.Text
	pick.SurfaceHover = ui.Color{}
	c.SetTheme(&pick)
	ui.List(c, &v.list, len(v.entries)).Label(locales.SavedVersions()).Width(260).Shrink(0).
		FillHeight().MinHeight(0).Gap(t.Space(uikit.SpaceHair)).Rows(func(row ui.ListRow) {
		c := row.Context
		c.SetTheme(t)
		entry := v.entries[row.Index]
		line := ui.Row(c).Height(uikit.HeightControl).Padding(0, t.Space(uikit.SpaceS)).
			Gap(t.Space(uikit.SpaceS)).AlignItems(ui.Center).Radius(uikit.RadiusControl)
		if row.Selected() {
			uikit.Selected(c, line, true)
		}
		line.Children(func() {
			uikit.Body(c, historyEntryLabel(entry)).Grow(1).MinWidth(0).SingleLine().FontFeatures("tnum")
		})
	})
	c.SetTheme(t)
}

// historyDiff compares the chosen backup with the file on disk. It is
// read-only: restoring is the only way to replace the current version.
func (a *app) historyDiff(c *ui.Context, t *ui.Theme, v *historyView) {
	entry := v.entries[v.picked]
	if v.diff == nil || v.shown != entry.Path {
		oldData, err := os.ReadFile(entry.Path)
		if err != nil {
			uikit.ErrorText(c, err.Error())
			return
		}
		current, err := os.ReadFile(v.target)
		if err != nil && !errors.Is(err, fs.ErrNotExist) {
			uikit.ErrorText(c, err.Error())
			return
		}
		language := editor.LanguageFor(filepath.Base(v.target))
		old := editor.NewBuffer(string(oldData))
		currentBuffer := editor.NewBuffer(string(current))
		old.SetLanguage(language)
		currentBuffer.SetLanguage(language)
		v.diff = &editor.DiffState{Old: old, New: currentBuffer, Layout: editor.DiffSplit, SplitPercent: a.diffSplit, Wrap: true}
		v.shown = entry.Path
	}
	ui.Row(c).Gap(t.Space(uikit.SpaceL)).Padding(0, 0, t.Space(uikit.SpaceXS), 0).Children(func() {
		uikit.Caption(c, locales.SelectedVersion()).Grow(1)
		uikit.Caption(c, locales.CurrentVersion()).Grow(1)
	})
	v.diff.Font = ui.Font{Family: uikit.FontMono, Size: uikit.TextBody}
	v.diff.Theme = uikit.EditorTheme(t)
	editor.DiffView(c, v.diff).Grow(1).MinHeight(0).FillWidth()
	a.diffSplit = v.diff.SplitPercent
}
