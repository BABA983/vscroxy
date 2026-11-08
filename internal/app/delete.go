package app

import (
	"github.com/baba983/vscroxy/internal/uikit"
	"slices"
	"strings"

	"github.com/baba983/vscroxy/internal/request"
	"github.com/baba983/vscroxy/locales"
	"github.com/egoist/mygo/ui"
)

// deletion is the request, or the collection or folder and everything in
// it, a confirmation is about to delete. key names a request; path a folder.
type deletion struct {
	open      bool
	key, path string
	session   string // a saved session's key
	name      string
	kind      string
	detail    string
}

// within says a request filed under collection lives at path or below it.
func within(collection, path string) bool {
	return collection == path || strings.HasPrefix(collection, path+"/")
}

// confirmDeleteRequest asks before deleting the saved request with key.
func (a *app) confirmDeleteRequest(key, name string) {
	a.deleting = deletion{open: true, key: key, name: locales.RequestQuoted(name), kind: locales.RequestNoun(),
		detail: "Its file and examples are removed from the library."}
}

// confirmDeleteFolder asks before deleting the collection or folder at path
// with the requests and folders in it.
func (a *app) confirmDeleteFolder(path string) {
	n := 0
	for _, r := range a.requests {
		if within(r.Collection, path) {
			n++
		}
	}
	name, kind := locales.CollectionQuoted(path), locales.CollectionNoun()
	if strings.Contains(path, "/") {
		name, kind = locales.FolderQuoted(path), locales.FolderNoun()
	}
	a.deleting = deletion{open: true, path: path, name: name, kind: kind, detail: locales.DeleteDetail(n)}
}

// deleteAssets removes what the confirmation names. Open tabs of deleted
// requests close when they hold nothing unsaved; edited ones stay open as
// new, unsaved requests.
func (a *app) deleteAssets(del deletion) error {
	gone := func(r savedRequest) bool {
		return del.key != "" && r.key() == del.key || del.path != "" && within(r.Collection, del.path)
	}
	var keys []string
	for _, r := range a.requests {
		if gone(r) {
			keys = append(keys, r.key())
		}
	}
	requests := slices.DeleteFunc(slices.Clone(a.requests), gone)
	folders := slices.Clone(a.folders)
	if del.path != "" {
		for i := range folders {
			if within(folders[i].Path, del.path) {
				folders[i].Path = ""
			}
		}
		folders = slices.DeleteFunc(folders, func(f request.Folder) bool { return f.Path == "" })
	}
	if err := a.writeLibrary(requests, folders); err != nil {
		return err
	}
	if del.path != "" {
		a.dropFolderCurrent(del.path)
	}
	for _, d := range slices.Clone(a.documents) {
		if d.savedKey == "" || !slices.Contains(keys, d.savedKey) {
			continue
		}
		if d.dirty() || d.composeWaiting {
			d.savedKey, d.assetID = "", ""
			continue
		}
		a.closeDocument(d)
	}
	return nil
}

// deleteModal confirms a deletion.
func (a *app) deleteModal(c *ui.Context, t *ui.Theme) {
	del := &a.deleting
	if !del.open {
		return
	}
	uikit.Modal(c, &del.open, func() {
		uikit.DialogColumn(c, uikit.DialogSmall).Children(func() {
			uikit.DialogTitle(c, locales.DeleteConfirm(del.name))
			uikit.Hint(c, locales.DeleteCannotBeUndone(del.detail))
			uikit.DialogFooter(c).Children(func() {
				if uikit.SecondaryButton(c, locales.Cancel()).Clicked() {
					del.open = false
				}
				if uikit.PrimaryButton(c, locales.DeleteKind(del.kind)).Clicked() {
					deleteAll := a.deleteAssets
					if del.session != "" {
						deleteAll = func(del deletion) error { return a.sessions.remove(del.session) }
					}
					if err := deleteAll(*del); err != nil {
						a.fail(locales.Delete(), err)
					} else {
						a.done(locales.DeletedQuoted(del.name))
					}
					del.open = false
				}
			})
		})
	})
}
