package app

import (
	"strconv"

	"github.com/baba983/vscroxy/internal/jsonc"
	"github.com/baba983/vscroxy/internal/uikit"
	"github.com/baba983/vscroxy/locales"
	"github.com/egoist/mygo/ui"
	"github.com/egoist/mygo/ui/editor"
)

// jsonBreadcrumbScanLimit bounds the text a path scan reads. Bodies beyond it
// have no bar; the reader and the rest of the body view keep working with the
// bytes the app already holds.
const jsonBreadcrumbScanLimit = 1 << 20

// jsonBreadcrumbs caches one path per editor. Scanning a document is work for
// a changed caret or buffer version only: frames that redraw the bar at the
// same position reuse the answer, including the sibling menus.
type jsonBreadcrumbs struct {
	key     string
	buf     *editor.Buffer
	version uint64
	cursor  int
	ready   bool
	path    jsonc.BreadcrumbPath
	scan    func(string, int) jsonc.BreadcrumbPath // seam for the recompute test
}

func (b *jsonBreadcrumbs) refresh(key string, view *editor.State, cursor int) jsonc.BreadcrumbPath {
	version := view.Buffer.Version()
	if b.ready && b.key == key && b.buf == view.Buffer && b.version == version && b.cursor == cursor {
		return b.path
	}
	b.key, b.buf, b.version, b.cursor, b.ready = key, view.Buffer, version, cursor, true
	b.path = jsonc.BreadcrumbPath{}
	size := view.Buffer.Len()
	if size == 0 || size > jsonBreadcrumbScanLimit {
		return b.path
	}
	scan := b.scan
	if scan == nil {
		scan = jsonc.BreadcrumbsAt
	}
	b.path = scan(view.Buffer.String(), cursor)
	return b.path
}

// activeJSONPath is what the Copy path command copies. The bar records the
// focused editor, or the first one shown for a new body, so the command stays
// available while the command palette owns the keyboard focus.
type activeJSONPath struct {
	key, path string
}

func (a *app) copyJSONPath(c *ui.Context, path string) {
	if c == nil || path == "" {
		return
	}
	c.Services().WriteClipboard(path)
	a.notify(notice{level: noticeSuccess, text: locales.JsonPathCopied()})
}

// jsonPathBar draws a VS Code style breadcrumb above a JSON text viewer. Each
// segment jumps to its node when clicked; its arrow opens the sibling keys or
// indices at that level.
func (a *app) jsonPathBar(c *ui.Context, crumbs *jsonBreadcrumbs, key string, view *editor.State, focus func()) {
	path := crumbs.refresh(key, view, view.Cursor())
	// The slot stays even when there is no path: adding it on the frame a
	// body first parses as JSON must not move the editor among its siblings
	// and take the keyboard focus away from it.
	bar := ui.Box(c).Key("json-path-bar")
	if len(path.Segments) == 0 {
		return
	}
	if view.Focused() || a.activeJSONPath.key != key {
		a.activeJSONPath = activeJSONPath{key: key, path: path.JSONPath}
	}
	t := c.Theme()
	bar.Children(func() {
		ui.Row(c).Label(locales.JsonBreadcrumbBar()).Role(ui.RoleNavigation).AlignItems(ui.Center).
			Gap(t.Space(uikit.SpaceS)).FillWidth().MinHeight(uikit.HeightControlSmall).
			Padding(0, t.Space(uikit.SpaceL), t.Space(uikit.SpaceS)).Children(func() {
			for i, crumb := range path.Segments {
				if i > 0 {
					ui.Text(c, "›").TextColor(t.TextMuted).Shrink(0)
				}
				jump := func() {
					view.Select(crumb.Start, crumb.End)
					if focus != nil {
						focus()
					}
				}
				last := i == len(path.Segments)-1
				if last {
					uikit.DataBody(c, crumb.Label).Current(ui.CurrentPage).Shrink(1)
				} else if link := uikit.LinkButton(c, crumb.Label); link.Clicked() {
					jump()
				}
				menu := func(m *ui.Menu) {
					if len(crumb.Siblings) == 0 {
						m.Item(locales.JsonBreadcrumbNoSiblings()).Disabled(true)
					}
					for _, sibling := range crumb.Siblings {
						item := m.Item(sibling.Label).ID(strconv.Itoa(sibling.Start)).
							Radio(sibling.Segment == crumb.Segment)
						if item.Chosen() {
							view.Select(sibling.Start, sibling.End)
							if focus != nil {
								focus()
							}
						}
					}
					if crumb.More {
						m.Separator()
						m.Item(locales.JsonBreadcrumbMore(jsonc.BreadcrumbSiblingLimit)).Disabled(true)
					}
				}
				uikit.LinkButton(c, "▾").Label(locales.JsonBreadcrumbSiblingFor(crumb.Label)).
					PopoverMenuButton(menu, uikit.MenuButtonOptions(c))
			}
			ui.Box(c).Grow(1)
			if copy := uikit.CompactButton(c, locales.JsonBreadcrumbCopyPath()); copy.Clicked() {
				a.copyJSONPath(c, path.JSONPath)
			}
		})
	})
}
