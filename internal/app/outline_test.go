package app

import (
	"net/http"
	"slices"
	"strings"
	"testing"

	"github.com/baba983/vscroxy/internal/jsonc"
	"github.com/baba983/vscroxy/locales"
	"github.com/egoist/mygo/ui"
	"github.com/egoist/mygo/ui/editor"
)

func outlineTree(t *testing.T, text string) []*outlineSymbol {
	t.Helper()
	root, _ := jsonc.ParseTree(text)
	if root == nil {
		t.Fatal("no JSON tree")
	}
	return outlineJSONSymbols(root, text)
}

func outlineLabels(roots []*outlineSymbol) []string {
	var out []string
	var walk func([]*outlineSymbol)
	walk = func(nodes []*outlineSymbol) {
		for _, sym := range nodes {
			out = append(out, sym.label)
			walk(sym.children)
		}
	}
	walk(roots)
	return out
}

func outlineByLabel(roots []*outlineSymbol, label string) *outlineSymbol {
	var found *outlineSymbol
	var walk func([]*outlineSymbol)
	walk = func(nodes []*outlineSymbol) {
		for _, sym := range nodes {
			if sym.label == label && found == nil {
				found = sym
			}
			walk(sym.children)
		}
	}
	walk(roots)
	return found
}

func TestOutlineJSONObjectsArraysAndTruncatedTail(t *testing.T) {
	text := `{"data":{"items":[{"id":3,"sku":"A-1"},{"id":4,"sku":"B-2"}],"total":2},"ok":true}`
	roots := outlineTree(t, text)
	if got, want := outlineLabels(roots), []string{"data", "items", "[0]", "id", "sku", "[1]", "id", "sku", "total", "ok"}; !slices.Equal(got, want) {
		t.Fatalf("labels = %v, want %v", got, want)
	}
	items := outlineByLabel(roots, "items")
	if items == nil || items.hint != "[2]" || !items.branch() || len(items.children) != 2 {
		t.Fatalf("items symbol = %+v", items)
	}
	if first := outlineByLabel(roots, "[0]"); first == nil || first.hint != "{2}" ||
		!strings.Contains(text[first.start:first.end], `"sku":"A-1"`) {
		t.Fatalf("first array object = %+v", first)
	}
	if ok := outlineByLabel(roots, "ok"); ok == nil || ok.hint != "true" {
		t.Fatalf("ok hint = %+v", ok)
	}
	for _, sym := range flattenOutline(roots) {
		if sym.start < 0 || sym.end <= sym.start || sym.end > len(text) {
			t.Fatalf("symbol %q has span %d..%d", sym.label, sym.start, sym.end)
		}
	}

	broken := `{"head":{"items":[{"id":1},`
	partial := outlineTree(t, broken)
	if got := outlineLabels(partial); !slices.Contains(got, "head") || !slices.Contains(got, "items") ||
		!slices.Contains(got, "[0]") || !slices.Contains(got, "id") {
		t.Fatalf("truncated body lost its open nodes: %v", got)
	}
	for _, sym := range flattenOutline(partial) {
		if sym.start < 0 || sym.end > len(broken) || sym.end <= sym.start {
			t.Fatalf("partial symbol %q has span %d..%d", sym.label, sym.start, sym.end)
		}
	}
}

func flattenOutline(roots []*outlineSymbol) []*outlineSymbol {
	var out []*outlineSymbol
	var walk func([]*outlineSymbol)
	walk = func(nodes []*outlineSymbol) {
		for _, sym := range nodes {
			out = append(out, sym)
			walk(sym.children)
		}
	}
	walk(roots)
	return out
}

func TestOutlineJSONChildLimitHasMoreRow(t *testing.T) {
	text := "{"
	for i := range 65 {
		if i > 0 {
			text += ","
		}
		text += `"k` + string(rune('a'+i%26)) + `":` + strconvItoa(i)
	}
	text += "}"
	roots := outlineTree(t, text)
	if len(roots) != 65 {
		t.Fatalf("got %d keys, want 65", len(roots))
	}
	p := &outlinePanel{}
	p.setSymbols(roots)
	if roots[0].parent != "" {
		t.Fatalf("root's parent = %q", roots[0].parent)
	}
	if got := len(p.rootKeys()); got != outlineChildLimit+1 {
		t.Fatalf("root rows = %d, want %d plus more", got, outlineChildLimit)
	}
	if got := len(p.childKeys(roots[0].key)); got != 0 {
		t.Fatalf("a scalar's children = %v", got)
	}
	more := p.byKey["@more"]
	if more == nil || more.label != locales.OutlineMore(5) {
		t.Fatalf("more row = %+v", more)
	}
}

func strconvItoa(i int) string {
	if i == 0 {
		return "0"
	}
	var digits []byte
	for i > 0 {
		digits = append([]byte{byte('0' + i%10)}, digits...)
		i /= 10
	}
	return string(digits)
}

func TestOutlineGraphQLOperationsFragmentsAndFields(t *testing.T) {
	text := "query A { user { id } }\nquery B { node { ...F } }\nfragment F on User { name }"
	roots := parseOutlineSymbols(outlineParseInput{kind: outlineGraphQL, text: text})
	if got, want := outlineLabels(roots), []string{
		"query A", "user", "id", "query B", "node", "...F", "fragment F on User", "name",
	}; !slices.Equal(got, want) {
		t.Fatalf("labels = %v, want %v", got, want)
	}
	all := flattenOutline(roots)
	if all[0].hint != "query" || all[6].hint != "User" {
		t.Fatalf("hints = %q, %q", all[0].hint, all[6].hint)
	}
	for _, sym := range flattenOutline(roots) {
		if sym.start < 0 || sym.end <= sym.start || sym.end > len(text) {
			t.Fatalf("symbol %q has span %d..%d", sym.label, sym.start, sym.end)
		}
	}
}

func TestOutlineScriptBlocksTestsAndBrokenText(t *testing.T) {
	text := `function outer() {
  const inner = 1;
}
describe("suite", function () {
  it("works", function () {
    pm.test("status is 200", function () {});
  });
});
pm.test("body ok", () => {});
const helper = () => 42;`
	symbols := parseOutlineSymbols(outlineParseInput{kind: outlineScript, text: text})
	labels := outlineLabels(symbols)
	for _, want := range []string{"outer", "suite", "works", "test: status is 200", "test: body ok", "helper"} {
		if !slices.Contains(labels, want) {
			t.Fatalf("missing %q in %v", want, labels)
		}
	}
	suite, works, test := outlineByLabel(symbols, "suite"), outlineByLabel(symbols, "works"), outlineByLabel(symbols, "test: status is 200")
	if suite == nil || works == nil || test == nil || works.parent != suite.key || test.parent != works.key {
		t.Fatalf("nesting: suite=%+v works=%+v test=%+v", suite, works, test)
	}
	for _, sym := range flattenOutline(symbols) {
		if sym.start < 0 || sym.end <= sym.start || sym.end > len(text) {
			t.Fatalf("symbol %q has span %d..%d", sym.label, sym.start, sym.end)
		}
	}
	for _, broken := range []string{
		"",
		"function (",
		"describe('unfinished', () => { pm.test( ",
		"pm.test('x', function () { if ( {",
		"const x = () =>",
		"{{{ not javascript",
		strings.Repeat("function f() { ", 128),
	} {
		got := parseOutlineSymbols(outlineParseInput{kind: outlineScript, text: broken})
		for _, sym := range flattenOutline(got) {
			if sym.start < 0 || sym.end <= sym.start || sym.end > len(broken) {
				t.Fatalf("broken source %q produced span %d..%d", broken[:min(24, len(broken))], sym.start, sym.end)
			}
		}
	}
}

func outlineScene(t *testing.T, body string, setup ...func(*app)) (*app, *ui.Tester, string) {
	t.Helper()
	a := workspaceApp()
	a.flows[0].resBody, a.flows[0].bytes = body, len(body)
	a.flows[0].respHeader = http.Header{"Content-Type": {"application/json"}}
	a.syncDetail()
	a.bodyModes(a.flows[0], 0)
	a.bodyMode[0] = 1
	a.outline.open = true
	for _, fn := range setup {
		fn(a)
	}
	tt := ui.NewTester(func(c *ui.Context) {
		ui.Box(c).Fill().Padding(8).Children(func() {
			ui.Row(c).Width(1180).Children(func() { a.bodySideView(c, a.flows[0], 0) })
		})
	}, 2400, 1280)
	tt.SetScale(1)
	tt.Frame()
	return a, tt, a.bodyText[0].Buffer.String()
}

func TestOutlineClickRevealAndFollowCursor(t *testing.T) {
	const body = `{"omegaOnly":{"sku":"A-1"},"other":2}`
	a, tt, text := outlineScene(t, body)
	p := a.outline.active
	if p == nil || len(p.roots) == 0 {
		t.Fatalf("no outline: %+v", p)
	}
	if !tt.HasText(locales.Outline()) || !tt.HasText(locales.FilterOutline()) {
		t.Fatalf("outline section did not render: %v", tt.Texts())
	}

	// Follow the caret: an open child is chosen while its parent stays in
	// the path. Clicking another row moves the caret and reveals its text.
	at := strings.Index(text, `"sku"`) + 4
	a.bodyText[0].Select(at, at)
	tt.Frame()
	if got := p.symbol(p.followKey); got == nil || got.label != "sku" {
		t.Fatalf("follow key %q named %+v, want sku", p.followKey, got)
	}
	a.bodyText[0].Select(0, 0)
	tt.Frame()
	view := a.bodyText[0]
	if _, found := tt.AccessByRole(ui.RoleTreeItem, "omegaOnly"); !found {
		t.Fatal("no outline tree item for omegaOnly")
	}
	if err := tt.Click("omegaOnly"); err != nil {
		t.Fatal(err)
	}
	tt.Frame()
	omega := outlineByLabel(p.roots, "omegaOnly")
	if omega == nil || view.Cursor() != omega.start {
		t.Fatalf("click moved the caret to %d, want symbol start %v", view.Cursor(), omega)
	}
	tt.Frame()
	if got := p.symbol(p.followKey); got == nil || got.label != "omegaOnly" {
		t.Fatalf("follow cursor after click = %q", p.followKey)
	}
}

func TestOutlineFilterKeepsAncestorsAndMatches(t *testing.T) {
	const body = `{"data":{"items":[{"id":3,"sku":"A-1"},{"id":4,"sku":"B-2"}],"total":2},"ok":true}`
	a, tt, _ := outlineScene(t, body)
	p := a.outline.active
	p.query = "sku"
	tt.Frame()
	labels := make([]string, 0, len(p.filterRoots))
	for _, key := range p.filterRoots {
		labels = append(labels, p.symbol(key).label)
	}
	if len(p.filterRoots) != 1 || labels[0] != "data" {
		t.Fatalf("filter roots = %v", labels)
	}
	if len(p.filterKids["0"]) != 1 || p.symbol(p.filterKids["0"][0]).label != "items" {
		t.Fatalf("filter hid an ancestor: %+v", p.filterKids)
	}
	if len(p.filterKids["0/0"]) != 2 {
		t.Fatalf("both sku rows should survive: %+v", p.filterKids)
	}
	if len(p.matches) == 0 {
		t.Fatal("filter kept rows without recording matches")
	}
	if !p.tree.Open.Has("0") || !p.tree.Open.Has("0/0") {
		t.Fatalf("matching ancestors are not open: %v", p.tree.Open)
	}
	p.query = ""
	tt.Frame()
	if len(p.filterRoots) != 0 || len(p.rootKeys()) != 2 {
		t.Fatalf("clearing the filter did not restore the roots: %v", p.rootKeys())
	}
}

// TestOutlineSharesBreadcrumbTree shows the breadcrumb bar and the outline on
// one JSON editor. The parse seam counts the one tree both readers use; the
// outline's parse seam checks that it received that exact tree. A second
// jsonc.ParseTree call in outlineLoad would fail the identity check.
func TestOutlineSharesBreadcrumbTree(t *testing.T) {
	parses, wrongTree := 0, 0
	var crumbs *jsonBreadcrumbs
	body := `{"data":{"items":[{"id":1},{"id":2}],"total":2},"ok":true}`
	a, tt, _ := outlineScene(t, body, func(app *app) {
		crumbs = &app.jsonCrumbs[0]
		crumbs.parse = func(text string) (*jsonc.Node, []jsonc.ParseError) {
			parses++
			return jsonc.ParseTree(text)
		}
		app.outline.parse = func(in outlineParseInput) []*outlineSymbol {
			if in.kind == outlineJSON && in.tree != crumbs.root {
				wrongTree++
			}
			return parseOutlineSymbols(in)
		}
	})
	if parses != 1 || wrongTree != 0 || len(crumbs.path.Segments) == 0 || len(a.outline.active.roots) == 0 {
		t.Fatalf("first frame: %d parses, %d foreign trees, %d crumbs, %d outline roots",
			parses, wrongTree, len(crumbs.path.Segments), len(a.outline.active.roots))
	}
	text := a.bodyText[0].Buffer.String()
	for _, offset := range []int{2, strings.Index(text, `"items"`), strings.Index(text, `"ok"`), 1} {
		if offset < 0 {
			t.Fatalf("no caret target in %q", text)
		}
		a.bodyText[0].Select(offset, offset)
		tt.Frame()
	}
	if parses != 1 || wrongTree != 0 {
		t.Fatalf("four caret moves with a bar and an outline: %d parses, %d foreign trees", parses, wrongTree)
	}
	a.bodyText[0].Buffer.Set(`{"data":{"items":[{"id":3}],"total":1},"ok":false}`)
	tt.Frame()
	tt.Frame()
	if parses != 2 || wrongTree != 0 {
		t.Fatalf("new buffer version: %d parses, %d foreign trees", parses, wrongTree)
	}
}

func TestOutlineScanCap(t *testing.T) {
	const kind = outlineScript
	small := "// " + strings.Repeat("a", jsonBreadcrumbScanLimit-3)
	view := &editor.State{Buffer: editor.NewBuffer(small)}
	target := outlineTarget{key: "cap", kind: kind}
	parses := 0
	a := workspaceApp()
	a.outline.parse = func(in outlineParseInput) []*outlineSymbol {
		parses++
		return parseOutlineSymbols(in)
	}
	p := a.outlineLoad(view, target)
	if p.tooLarge || parses != 1 || len(p.roots) != 0 {
		t.Fatalf("body at the cap: tooLarge=%v parses=%d roots=%d", p.tooLarge, parses, len(p.roots))
	}
	view.Buffer.Set(small + "b")
	p = a.outlineLoad(view, target)
	if !p.tooLarge || parses != 1 {
		t.Fatalf("body over the cap: tooLarge=%v parses=%d", p.tooLarge, parses)
	}
	tt := ui.NewTester(func(c *ui.Context) {
		a.outlineSection(c, p, view)
	}, 900, 400)
	tt.Frame()
	if !tt.HasText(locales.OutlineTooLarge()) {
		t.Fatalf("no size hint in the outline section: %v", tt.Texts())
	}
}

func TestOutlineCommandsRegistered(t *testing.T) {
	ids := map[string]bool{}
	for _, cmd := range globalCommands().entries {
		ids[cmd.id] = true
	}
	for _, id := range []string{"outline.focus", "outline.toggle"} {
		if !ids[id] {
			t.Errorf("%s is not in the command registry", id)
		}
	}
	for _, cmd := range globalCommands().entries {
		if cmd.id == "outline.focus" && cmd.key.key == 0 {
			t.Error("outline.focus has no shortcut")
		}
	}
}
