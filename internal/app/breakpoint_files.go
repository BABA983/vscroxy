package app

// The file alternatives for a paused body a text editor must not show. The
// dialog owns the choice of file; capture owns reading the original body and
// sending the replacement's bytes.

import (
	"os"
	"path/filepath"
	"strconv"

	"github.com/baba983/vscroxy/internal/uikit"
	"github.com/baba983/vscroxy/locales"
	"github.com/egoist/mygo"
	"github.com/egoist/mygo/ui"
)

// syncBreakpointBodyFile drops a chosen replacement when the body text has
// changed since the choice. A typed edit must never be shadowed by a file.
func (a *app) syncBreakpointBodyFile() {
	if a.bpReplaceFile != "" && a.bpBody != a.bpBodySeen {
		a.bpReplaceFile, a.bpReplaceSize = "", 0
	}
	a.bpBodySeen = a.bpBody
}

// breakpointFileActions offers a way forward for a binary, undecodable or
// oversized body: load exact bytes from a file, or save the decoded body
// (raw bytes when it could not be decoded) for an outside editor.
func (a *app) breakpointFileActions(c *ui.Context) {
	ui.Row(c).Gap(c.Theme().Space(uikit.SpaceS)).Wrap().Children(func() {
		if uikit.SecondaryButton(c, locales.ReplaceBodyFromFile()).Clicked() {
			a.chooseBreakpointBodyFile()
		}
		if uikit.SecondaryButton(c, locales.SaveBodyToFile()).Clicked() {
			a.saveBreakpointBodyToFile()
		}
	})
	if a.bpReplaceFile != "" {
		size := strconv.FormatInt(a.bpReplaceSize, 10)
		uikit.Hint(c, locales.BodyFromFile(filepath.Base(a.bpReplaceFile), size))
	}
}

func (a *app) chooseBreakpointBodyFile() {
	if a.win == nil || a.bp == nil || a.srv == nil {
		return
	}
	win, id := a.win, a.bp.ID
	go guarded(func() {
		paths, err := mygo.Dialog.Open(mygo.OpenDialogOptions{Title: locales.ReplaceBodyFromFile()})
		if err != nil || len(paths) == 0 {
			if err != nil {
				a.fail(locales.ReplaceBodyFromFile(), err)
			}
			return
		}
		info, err := os.Stat(paths[0])
		if err != nil {
			a.fail(locales.ReplaceBodyFromFile(), err)
			return
		}
		win.Update(func() {
			if a.bp == nil || a.bp.ID != id {
				return
			}
			a.bpReplaceFile, a.bpReplaceSize = paths[0], info.Size()
			a.syncBreakpointBodyFile()
		})
	})
}

func (a *app) saveBreakpointBodyToFile() {
	if a.win == nil || a.bp == nil || a.srv == nil {
		return
	}
	win, bp, srv := a.win, a.bp, a.srv
	go guarded(func() {
		path, err := mygo.Dialog.Save(mygo.SaveDialogOptions{Parent: win, Title: locales.SaveBodyDialog(), DefaultPath: "body.bin"})
		if err != nil || path == "" {
			if err != nil {
				a.fail(locales.SaveBodyToFile(), err)
			}
			return
		}
		n, err := srv.SaveBreakpointBody(bp, path)
		if err != nil {
			a.fail(locales.SaveBodyToFile(), err)
			return
		}
		win.Update(func() { a.done(locales.BodySaved(strconv.FormatInt(n, 10))) })
	})
}
