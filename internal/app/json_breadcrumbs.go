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

// jsonCrumbSource is a tree a JSON reader already parsed for the text it
// shows. The cache still checks the text before using it: a stale answer must
// never describe bytes the editor no longer holds.
type jsonCrumbSource struct {
	root *jsonc.Node
	text string
}

// jsonBreadcrumbs caches one parsed tree and path per editor. Parsing belongs
// to a changed buffer, not to a caret: moving through a large document walks
// the same tree and only rebuilds the path and sibling lists.
type jsonBreadcrumbs struct {
	key     string
	buf     *editor.Buffer
	version uint64
	cursor  int
	ready   bool
	text    string
	root    *jsonc.Node
	path    jsonc.BreadcrumbPath
	parse   func(string) (*jsonc.Node, []jsonc.ParseError)      // seam for parse counting
	scan    func(*jsonc.Node, string, int) jsonc.BreadcrumbPath // seam for path counting
}

// treeFor is the one place a JSON editor's tree is built. The breadcrumb bar
// and the outline both read it, so showing both never parses the same text
// twice. A fault-tolerant reader can hand over the tree it made for the bytes
// it shows; only then is no other parse needed.
func (b *jsonBreadcrumbs) treeFor(key string, view *editor.State, provided jsonCrumbSource) (*jsonc.Node, string) {
	version := view.Buffer.Version()
	if b.ready && b.key == key && b.buf == view.Buffer && b.version == version {
		return b.root, b.text
	}
	b.key, b.buf, b.version, b.ready = key, view.Buffer, version, true
	b.root, b.text, b.path, b.cursor = nil, "", jsonc.BreadcrumbPath{}, 0
	text := view.Buffer.String()
	if len(text) > 0 && len(text) <= jsonBreadcrumbScanLimit {
		if provided.root != nil && provided.text == text {
			b.root, b.text = provided.root, text
		} else {
			parse := b.parse
			if parse == nil {
				parse = func(text string) (*jsonc.Node, []jsonc.ParseError) { return jsonc.ParseTree(text) }
			}
			b.root, _ = parse(text)
			b.text = text
		}
	}
	return b.root, b.text
}

func (b *jsonBreadcrumbs) refreshWith(key string, view *editor.State, cursor int, provided jsonCrumbSource) jsonc.BreadcrumbPath {
	version := view.Buffer.Version()
	sameBuffer := b.ready && b.key == key && b.buf == view.Buffer && b.version == version
	root, text := b.treeFor(key, view, provided)
	if sameBuffer && b.cursor == cursor {
		return b.path
	}
	b.key, b.buf, b.version, b.cursor, b.ready = key, view.Buffer, version, cursor, true
	b.path = jsonc.BreadcrumbPath{}
	if root == nil {
		return b.path
	}
	scan := b.scan
	if scan == nil {
		scan = jsonc.BreadcrumbsIn
	}
	b.path = scan(root, text, cursor)
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
func (a *app) jsonPathBar(c *ui.Context, crumbs *jsonBreadcrumbs, key string, view *editor.State, focus func(), provided jsonCrumbSource) {
	path := crumbs.refreshWith(key, view, view.Cursor(), provided)
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
