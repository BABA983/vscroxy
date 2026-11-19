package app

import (
	"slices"

	"github.com/baba983/vscroxy/internal/uikit"
	"github.com/egoist/mygo/ui"
)

type treeDropVisual uint8

const (
	dropNoVisual treeDropVisual = iota
	dropIntoFolder
	dropBeforeRow
	dropAfterRow
)

// libraryDropOnRow turns a drag over a tree row into the move it would make.
// A folder's middle is its interior, its top and bottom are the gaps next to
// it; other rows are entirely gaps around their name. The indicator is drawn
// only for a move the app can actually make, so an impossible drop gives no
// promise that it will work.
func (a *app) libraryDropOnRow(row ui.Element, source, target libraryDrag, branch bool, parent string, paths map[string]bool) (libraryDrop, treeDropVisual, bool) {
	if !source.valid() || source.key() == target.key() {
		return libraryDrop{}, dropNoVisual, false
	}
	_, y, over := row.PointerPosition()
	h := row.Bounds().H
	if h <= 0 {
		h = uikit.HeightRow
	}
	var visual treeDropVisual
	switch {
	case branch && y >= h/4 && y <= h*3/4:
		visual = dropIntoFolder
	case y < h/2:
		visual = dropBeforeRow
	default:
		visual = dropAfterRow
	}
	if !over && visual != dropIntoFolder {
		// The pointer is still near this row at a drop, but an over row
		// is the one MyGo will deliver the value to.
		return libraryDrop{}, dropNoVisual, false
	}
	destination := parent
	if visual == dropIntoFolder {
		destination = target.id
	}
	if source.kind == folderItem && !canMoveFolderInto(source.id, destination) {
		return libraryDrop{}, dropNoVisual, false
	}
	drop := libraryDrop{parent: destination}
	if visual != dropIntoFolder {
		siblings := a.librarySiblings(parent, paths)
		at := slices.IndexFunc(siblings, func(x libraryDrag) bool { return x.key() == target.key() })
		if at < 0 {
			return libraryDrop{}, dropNoVisual, false
		}
		if visual == dropAfterRow {
			at++
		}
		if at < len(siblings) {
			drop.before = siblings[at]
		}
	}
	return drop, visual, true
}

// drawTreeDrop shows a line in the gap where the item would go, or washes
// the folder that would receive it. The colors are theme roles, as the tree's
// own rows are.
func drawTreeDrop(row ui.Element, visual treeDropVisual, t *ui.Theme) {
	switch visual {
	case dropIntoFolder:
		row.DrawOver(func(p *ui.Painter, r ui.Rect) {
			p.Fill(r, uikit.Colors(t).Accent, uikit.RadiusControl)
		})
	case dropBeforeRow, dropAfterRow:
		row.DrawOver(func(p *ui.Painter, r ui.Rect) {
			y := r.Y + r.H - 2
			if visual == dropBeforeRow {
				y = r.Y + 2
			}
			p.Fill(ui.Rect{X: r.X + 4, Y: y, W: r.W - 8, H: 2}, uikit.Colors(t).Ring, 1)
			p.Fill(ui.Rect{X: r.X + 1, Y: y - 3, W: 8, H: 8}, uikit.Colors(t).Ring, 4)
		})
	}
}

// queueTreeDrop registers both the hover and the release on one tree row.
// MyGo starts the drag and cancels it with Escape; the app only decides
// where it may land.
func (a *app) queueTreeDrop(c *ui.Context, row ui.Element, target libraryDrag, branch bool, parent string, paths map[string]bool) {
	if dragged, over := ui.DragOver[libraryDrag](row); over {
		if _, visual, ok := a.libraryDropOnRow(row, dragged, target, branch, parent, paths); ok {
			drawTreeDrop(row, visual, c.Theme())
		}
	}
	if dragged, ok := ui.Drop[libraryDrag](row); ok {
		if drop, _, valid := a.libraryDropOnRow(row, dragged, target, branch, parent, paths); valid {
			a.pendingMove = &libraryMove{item: dragged, to: drop}
		}
	}
}
