package app

import (
	"strconv"
	"strings"

	"github.com/baba983/vscroxy/internal/fuzzy"
	"github.com/baba983/vscroxy/internal/jsonc"
	"github.com/baba983/vscroxy/internal/uikit"
	"github.com/baba983/vscroxy/locales"
	"github.com/egoist/mygo/ui"
	"github.com/egoist/mygo/ui/editor"
	"github.com/oneo-me/onevra/source/icons/lucide"
)

// outlineUI is shared by the body editors; each editor keeps its own panel.
// The panel keys its symbols by buffer and version, as the JSON breadcrumbs
// do. Symbol work is bounded by the breadcrumbs' scan limit and runs in the
// build pass that sees the new version.
type outlineUI struct {
	open   bool
	panels map[*editor.State]*outlinePanel
	active *outlinePanel
	// parse is a seam for tests: a parse of an unchanged buffer is a bug the
	// contract tests count here.
	parse func(outlineParseInput) []*outlineSymbol
}

type outlineTarget struct {
	key    string
	kind   outlineKind
	crumbs *jsonBreadcrumbs
	crumb  jsonCrumbSource
	focus  func()
}

type outlinePanel struct {
	key, query string
	kind       outlineKind
	focus      func()
	crumbs     *jsonBreadcrumbs
	crumb      jsonCrumbSource
	split      float32
	input      ui.Handle
	tree       ui.OutlineState[string]
	followRow  int
	followKey  string

	buf      *editor.Buffer
	version  uint64
	ready    bool
	tooLarge bool
	roots    []*outlineSymbol
	all      []*outlineSymbol
	byKey    map[string]*outlineSymbol

	filterQuery string
	filterFor   uint64
	filterRoots []string
	filterKids  map[string][]string
	matches     map[string][]fuzzy.Match
}

func (a *app) outlinePanelFor(view *editor.State) *outlinePanel {
	if a.outline.panels == nil {
		a.outline.panels = make(map[*editor.State]*outlinePanel)
	}
	p := a.outline.panels[view]
	if p == nil {
		p = &outlinePanel{split: 73}
		a.outline.panels[view] = p
	}
	return p
}

// outlineLoad returns the symbols for one editor. Only a changed key, buffer
// or buffer version recomputes them. JSON uses the tree the breadcrumb cache
// already parsed for the same text; it never parses the body a second time.
func (a *app) outlineLoad(view *editor.State, target outlineTarget) *outlinePanel {
	p := a.outlinePanelFor(view)
	p.key, p.kind, p.focus, p.crumbs, p.crumb = target.key, target.kind, target.focus, target.crumbs, target.crumb
	version := view.Buffer.Version()
	if p.ready && p.buf == view.Buffer && p.version == version && p.key == target.key && p.kind == target.kind {
		return p
	}
	p.buf, p.version, p.ready = view.Buffer, version, false
	text := view.Buffer.String()
	// The outline applies to every body kind, so it uses the same scan cap as
	// the JSON breadcrumbs. Above it no symbols are computed at all: a big
	// body would either parse twice or need a worker whose answer could race
	// an edit. The section says why instead of showing a stale tree.
	if len(text) > jsonBreadcrumbScanLimit {
		p.tooLarge, p.roots = true, nil
		p.setSymbols(nil)
		return p
	}
	p.tooLarge = false
	var tree *jsonc.Node
	if target.kind == outlineJSON {
		crumbs := target.crumbs
		if crumbs == nil {
			crumbs = &jsonBreadcrumbs{}
		}
		tree, text = crumbs.treeFor(target.key, view, target.crumb)
		if tree == nil {
			p.setSymbols(nil)
			return p
		}
	}
	p.setSymbols(a.computeOutline(outlineParseInput{kind: target.kind, text: text, tree: tree}))
	return p
}

func (a *app) computeOutline(in outlineParseInput) []*outlineSymbol {
	if a.outline.parse != nil {
		return a.outline.parse(in)
	}
	return parseOutlineSymbols(in)
}

func (p *outlinePanel) setSymbols(symbols []*outlineSymbol) {
	p.roots, p.byKey = symbols, make(map[string]*outlineSymbol)
	p.all = nil
	p.filterQuery, p.filterFor, p.filterRoots, p.filterKids, p.matches = "", 0, nil, nil, nil
	var walk func([]*outlineSymbol)
	walk = func(nodes []*outlineSymbol) {
		for _, sym := range nodes {
			p.all = append(p.all, sym)
			p.byKey[sym.key] = sym
			walk(sym.children)
			if len(sym.children) > outlineChildLimit {
				more := &outlineSymbol{key: sym.key + "/more", parent: sym.key,
					label: locales.OutlineMore(len(sym.children) - outlineChildLimit)}
				p.byKey[more.key] = more
			}
		}
	}
	walk(p.roots)
	if len(p.roots) > outlineChildLimit {
		p.byKey["@more"] = &outlineSymbol{key: "@more", label: locales.OutlineMore(len(p.roots) - outlineChildLimit)}
	}
	p.ready = true
}

// outlineView places the outline beside the editor rather than in a separate
// panel: the body already has one pane per side and its own header row, so a
// split keeps the caret, breadcrumbs and find bar with the text they describe.
func (a *app) outlineView(c *ui.Context, view *editor.State, target outlineTarget, draw func()) {
	if !a.outline.open {
		draw()
		return
	}
	p := a.outlineLoad(view, target)
	a.outline.active = p
	ui.Row(c).Grow(1).FillWidth().MinHeight(0).Children(func() {
		ui.SplitPercent(c.Key("outline-"+target.key), &p.split, draw, func() {
			a.outlineSection(c, p, view)
		}).FillWidth().StartPercent(73).SplitReset(73)
	})
}

func (a *app) outlineSection(c *ui.Context, p *outlinePanel, view *editor.State) {
	t := c.Theme()
	ui.Column(c).MinWidth(0).FillWidth().FillHeight().Background(uikit.Colors(t).Window).Children(func() {
		ui.Row(c).Height(uikit.HeightControl).Padding(0, t.Space(uikit.SpaceM)).
			Gap(t.Space(uikit.SpaceXS)).AlignItems(ui.Center).Children(func() {
			ui.Icon(c, lucide.List()).Size(uikit.IconInline, uikit.IconInline).TextColor(t.TextMuted).Shrink(0)
			uikit.BodyStrong(c, locales.Outline()).Shrink(0)
			ui.Box(c).Grow(1)
			if uikit.IconButton(c, locales.HideOutline(), locales.ToggleOutline(), lucide.PanelRightClose()).Clicked() {
				a.outline.open = false
			}
		})
		ui.Row(c).Padding(0, t.Space(uikit.SpaceM), t.Space(uikit.SpaceS)).Children(func() {
			uikit.Input(c, &p.query).Bind(&p.input).Label(locales.FilterOutline()).
				Placeholder(locales.FilterOutline()).Height(uikit.HeightRow).Grow(1).MinWidth(0)
		})
		ui.Divider(c)
		if p.tooLarge {
			uikit.Hint(c, locales.OutlineTooLarge()).Padding(t.Space(uikit.SpaceM))
			return
		}
		if len(p.roots) == 0 {
			uikit.Hint(c, locales.NoOutlineSymbols()).Padding(t.Space(uikit.SpaceM))
			return
		}
		p.refreshFilter(a, view)
		p.followKey = p.symbolAtCursor(view.Cursor())
		p.openAncestors(p.followKey)
		p.followRow = p.rowOf(p.followKey)
		beforeKey := p.followKey
		p.tree.List.Selected = &p.followRow
		p.tree.List.Label = func(row int) string {
			if sym := p.symbol(p.tree.Item(row)); sym != nil {
				return sym.label
			}
			return ""
		}
		palette := uikit.Colors(t)
		ui.Outline(c, &p.tree, p.rootKeys(), p.childKeys, func(key string) {
			sym := p.symbol(key)
			if sym == nil {
				return
			}
			selected := key == p.followKey
			fg, muted := t.Text, t.TextMuted
			if selected {
				fg, muted = palette.PrimaryForeground, palette.PrimaryForeground
			}
			row := ui.Row(c).Label(sym.label).Gap(t.Space(uikit.SpaceXS)).AlignItems(ui.Center).FillWidth().
				Padding(0, t.Space(uikit.SpaceS)).MinHeight(uikit.HeightRow)
			if selected {
				row.Background(palette.Primary)
			}
			row.Children(func() {
				ui.Icon(c, outlineIconSVG(p.kind, sym)).Size(uikit.IconInline, uikit.IconInline).
					TextColor(muted).Shrink(0)
				if sym.key != "" && strings.HasSuffix(sym.key, "/more") {
					uikit.Hint(c, sym.label).TextColor(fg)
				} else if selected {
					// The completion rows' rule: no muted ink on Primary.
					uikit.Body(c, sym.label).TextColor(fg).SingleLine().Shrink(1)
				} else {
					fuzzyListBody(c, sym.label, p.matches[key]).SingleLine().Shrink(1)
				}
				if sym.hint != "" {
					ui.Box(c).Grow(1)
					uikit.DataMeta(c, sym.hint).TextColor(muted).SingleLine().Shrink(0)
				}
			})
		}).Grow(1).MinHeight(0)
		// A click writes the row it chose into List.Selected; follow-cursor
		// sets the same row from the caret. Only a different row is a click.
		if p.followRow >= 0 && p.followRow < p.tree.Rows() {
			if key := p.tree.Item(p.followRow); key != "" && key != beforeKey {
				p.reveal(view, key)
			}
		}
	})
}

func (p *outlinePanel) symbol(key string) *outlineSymbol {
	if sym := p.byKey[key]; sym != nil {
		return sym
	}
	return nil
}

func (p *outlinePanel) rootKeys() []string {
	roots := p.roots
	if p.query != "" {
		roots = nil
		all := make([]*outlineSymbol, 0, len(p.filterRoots))
		for _, key := range p.filterRoots {
			all = append(all, p.symbol(key))
		}
		roots = all
	}
	keys := make([]string, 0, min(len(roots)+1, outlineChildLimit+1))
	for _, sym := range roots[:min(len(roots), outlineChildLimit)] {
		keys = append(keys, sym.key)
	}
	if len(roots) > outlineChildLimit {
		keys = append(keys, "@more")
	}
	return keys
}

func (p *outlinePanel) childKeys(key string) []string {
	var keys []string
	if p.query == "" {
		sym := p.symbol(key)
		if sym == nil {
			return nil
		}
		for _, child := range sym.children[:min(len(sym.children), outlineChildLimit)] {
			keys = append(keys, child.key)
		}
		if len(sym.children) > outlineChildLimit {
			keys = append(keys, key+"/more")
		}
		return keys
	}
	all := p.filterKids[key]
	for _, child := range all[:min(len(all), outlineChildLimit)] {
		keys = append(keys, child)
	}
	if len(all) > outlineChildLimit {
		keys = append(keys, key+"/more")
		p.byKey[key+"/more"] = &outlineSymbol{key: key + "/more", parent: key,
			label: locales.OutlineMore(len(all) - outlineChildLimit)}
	}
	return keys
}

// refreshFilter uses the app's fuzzy list filter, keeping source order and
// highlighting the runes of each label that matched. An ancestor stays in the
// tree when a descendant matches, as it does in VS Code's outline find.
func (p *outlinePanel) refreshFilter(a *app, view *editor.State) {
	query := strings.TrimSpace(p.query)
	version := view.Buffer.Version()
	if p.filterQuery == query && p.filterFor == version {
		return
	}
	p.filterQuery, p.filterFor = query, version
	p.filterRoots, p.filterKids, p.matches = nil, make(map[string][]string), make(map[string][]fuzzy.Match)
	if query == "" {
		return
	}
	hits := a.fuzzyList(query, "outline", p.key+":"+strconv.FormatUint(version, 10), len(p.all),
		func(i int) fuzzyListItem {
			sym := p.all[i]
			return fuzzyListItem{label: sym.label, detail: sym.hint}
		}, false)
	included := make(map[string]bool)
	for _, hit := range hits {
		sym := p.all[hit.index]
		included[sym.key] = true
		for _, match := range hit.matches {
			p.matches[sym.key] = append(p.matches[sym.key], fuzzy.Match{Start: match.Start, End: match.End})
		}
		// A hit deep in the tree brings its path with it.
		for parent := sym.parent; parent != ""; {
			included[parent] = true
			ancestor := p.symbol(parent)
			if ancestor == nil {
				break
			}
			parent = ancestor.parent
		}
	}
	for _, sym := range p.all {
		if !included[sym.key] {
			continue
		}
		if sym.parent != "" && included[sym.parent] {
			p.filterKids[sym.parent] = append(p.filterKids[sym.parent], sym.key)
		} else {
			p.filterRoots = append(p.filterRoots, sym.key)
		}
		if sym.parent != "" {
			p.tree.Open.Add(sym.parent)
		}
	}
}

// symbolAtCursor follows the caret with the innermost symbol containing it,
// so a caret in an array's object highlights that object, not the array.
func (p *outlinePanel) symbolAtCursor(cursor int) string {
	var best *outlineSymbol
	var walk func([]*outlineSymbol)
	walk = func(nodes []*outlineSymbol) {
		for _, sym := range nodes {
			if sym.key == "" || sym.start > cursor || cursor > sym.end {
				continue
			}
			if best == nil || sym.end-sym.start < best.end-best.start {
				best = sym
			}
			walk(sym.children)
		}
	}
	walk(p.roots)
	if best == nil {
		return ""
	}
	return best.key
}

func (p *outlinePanel) openAncestors(key string) {
	for parent := p.symbol(key); parent != nil && parent.parent != ""; {
		p.tree.Open.Add(parent.parent)
		parent = p.symbol(parent.parent)
	}
}

func (p *outlinePanel) rowOf(key string) int {
	for row := range p.tree.Rows() {
		if p.tree.Item(row) == key {
			return row
		}
	}
	return -1
}

func (p *outlinePanel) reveal(view *editor.State, key string) {
	sym := p.symbol(key)
	if sym == nil || sym.key == "" {
		return
	}
	// More rows are counts, not places in the text.
	if strings.HasSuffix(sym.key, "/more") || sym.start < 0 || sym.end > view.Buffer.Len() || sym.end <= sym.start {
		return
	}
	at := min(max(sym.start, 0), view.Buffer.Len())
	view.Select(at, at)
	if p.focus != nil {
		p.focus()
	}
}

// outlineEditor wraps the editor a view already draws. The outline's split
// only appears when the user toggles it, so the normal body view keeps its
// space and its committed focus.
func (a *app) outlineEditor(c *ui.Context, view *editor.State, target outlineTarget, draw func()) {
	a.outlineView(c, view, target, draw)
}
