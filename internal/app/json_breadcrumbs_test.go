package app

import (
	"net/http"
	"strings"
	"testing"

	"github.com/baba983/vscroxy/internal/jsonc"
	"github.com/baba983/vscroxy/internal/request"
	"github.com/baba983/vscroxy/locales"
	"github.com/egoist/mygo/ui"
	"github.com/egoist/mygo/ui/editor"
)

// breadcrumbScene shows one response body in the Pretty reader, with the
// caret inside the value of the last nested key, and returns the shown text.
func breadcrumbScene(t *testing.T, body string) (*app, *ui.Tester, string) {
	t.Helper()
	a := workspaceApp()
	a.flows[0].resBody, a.flows[0].bytes = body, len(body)
	a.flows[0].respHeader = http.Header{"Content-Type": {"application/json"}}
	a.syncDetail()
	a.bodyModes(a.flows[0], 0)
	a.bodyMode[0] = 1
	tt := ui.NewTester(func(c *ui.Context) {
		ui.Column(c).Fill().Padding(16).Children(func() {
			a.bodySideView(c, a.flows[0], 0)
		})
	}, 1100, 640)
	tt.Frame()
	text := a.bodyText[0].Buffer.String()
	pos := strings.Index(text, `"id"`)
	if pos < 0 {
		t.Fatalf("the reader did not show the key: %q", text)
	}
	a.bodyText[0].Select(pos+3, pos+3)
	tt.Frame()
	return a, tt, text
}

func TestBreadcrumbBarSegmentsAndClipboard(t *testing.T) {
	const body = `{"data":{"items":[{"id":3}]},"ok":true}`
	a, tt, _ := breadcrumbScene(t, body)

	path := a.jsonCrumbs[0].path
	var labels []string
	for _, s := range path.Segments {
		labels = append(labels, s.Label)
	}
	if got := strings.Join(labels, " › "); got != `$ › data › items › [0] › id` {
		t.Fatalf("bar = %q", got)
	}
	if path.JSONPath != "$.data.items[0].id" {
		t.Fatalf("JSONPath = %q", path.JSONPath)
	}
	if !tt.HasText("Copy path") || !tt.HasText("data") || !tt.HasText("[0]") {
		t.Fatalf("bar did not render: %v", tt.Texts())
	}

	if err := tt.Click("Copy path"); err != nil {
		t.Fatal(err)
	}
	if got := tt.Clipboard(); got != path.JSONPath {
		t.Fatalf("clipboard = %q, want %q", got, path.JSONPath)
	}

	found := false
	for _, cmd := range globalCommands().entries {
		if cmd.id == "json.copy-path" && cmd.label == locales.JsonBreadcrumbCopyPath() {
			found = true
		}
	}
	if !found {
		t.Fatal("Copy path is not in the command registry")
	}
}

func TestBreadcrumbSegmentsMoveTheCaret(t *testing.T) {
	a, tt, text := breadcrumbScene(t, `{"data":{"items":[{"id":3}]},"ok":true}`)
	data := a.jsonCrumbs[0].path.Segments[1]
	if err := tt.Click("data"); err != nil {
		t.Fatal(err)
	}
	caret := a.bodyText[0].Cursor()
	if caret < data.Start || caret > data.End || caret >= len(text) {
		t.Fatalf("caret = %d, wanted the data node %d:%d", caret, data.Start, data.End)
	}
}

func TestBreadcrumbSiblingMenuJumps(t *testing.T) {
	const body = `{"data":{"items":[{"id":3},{"id":4},{"id":5}]}}`
	a, tt, _ := breadcrumbScene(t, body)
	index := a.jsonCrumbs[0].path.Segments[len(a.jsonCrumbs[0].path.Segments)-2]
	if index.Label != "[0]" {
		t.Fatalf("wanted the array segment, got %q", index.Label)
	}
	if err := tt.Click(locales.JsonBreadcrumbSiblingFor("[0]")); err != nil {
		t.Fatal(err)
	}
	got := tt.Menu()
	if len(got) != 3 || got[0] != "[0]" || got[2] != "[2]" {
		t.Fatalf("sibling menu = %v", got)
	}
	if err := tt.ChooseMenuItem("[1]"); err != nil {
		t.Fatal(err)
	}
	caret := a.bodyText[0].Cursor()
	want := a.jsonCrumbs[0].path.Segments[len(a.jsonCrumbs[0].path.Segments)-2]
	if caret < want.Start || caret > want.End {
		t.Fatalf("caret = %d, wanted a later array element near %d:%d", caret, want.Start, want.End)
	}
	if index.Siblings[1].Start == want.Start {
		t.Fatal("the sibling menu did not move to another element")
	}
}

func TestCopyPathRunsFromThePalette(t *testing.T) {
	a := snapshotApp()
	tt := ui.NewTester(a.view, 1280, 800)
	tt.Frame()
	want := a.activeJSONPath.path
	if want == "" {
		t.Fatal("no JSON path was recorded for the palette to copy")
	}
	tt.Key(ui.Cmd, ui.KeyK)
	tt.Type(locales.JsonBreadcrumbCopyPath())
	tt.Key(0, ui.KeyEnter)
	if got := tt.Clipboard(); got != want {
		t.Fatalf("palette copied %q, want %q", got, want)
	}
}

// countedBreadcrumbs wraps both seams so a test can tell parsing from walking
// an already parsed tree. Nothing in the app itself uses these counters.
func countedBreadcrumbs() (*jsonBreadcrumbs, *int, *int) {
	var crumbs jsonBreadcrumbs
	parses, scans := 0, 0
	crumbs.parse = func(text string) (*jsonc.Node, []jsonc.ParseError) {
		parses++
		return jsonc.ParseTree(text)
	}
	crumbs.scan = func(root *jsonc.Node, text string, offset int) jsonc.BreadcrumbPath {
		scans++
		return jsonc.BreadcrumbsIn(root, text, offset)
	}
	return &crumbs, &parses, &scans
}

func TestBreadcrumbRecomputesOnlyOnChange(t *testing.T) {
	view := &editor.State{Buffer: editor.NewBuffer(`{"a":{"b":1}}`)}
	crumbs, parses, scans := countedBreadcrumbs()
	for range 4 {
		crumbs.refreshWith("body", view, 8, jsonCrumbSource{})
	}
	if *parses != 1 || *scans != 1 {
		t.Fatalf("unchanged caret: %d parses, %d scans", *parses, *scans)
	}
	crumbs.refreshWith("body", view, 9, jsonCrumbSource{})
	if *parses != 1 || *scans != 2 {
		t.Fatalf("moved caret: %d parses, %d scans", *parses, *scans)
	}
	view.Select(len(`{"a":`), len(`{"a":`))
	view.Insert("x")
	crumbs.refreshWith("body", view, view.Cursor(), jsonCrumbSource{})
	if *parses != 2 || *scans != 3 {
		t.Fatalf("edited buffer: %d parses, %d scans", *parses, *scans)
	}
	crumbs.refreshWith("other", view, view.Cursor(), jsonCrumbSource{})
	if *parses != 3 || *scans != 4 {
		t.Fatalf("another body: %d parses, %d scans", *parses, *scans)
	}
}

func TestBreadcrumbCaretMovesDoNotReparse(t *testing.T) {
	view := &editor.State{Buffer: editor.NewBuffer(`{"items":[{"id":1},{"id":2},{"id":3}]}`)}
	crumbs, parses, scans := countedBreadcrumbs()
	for offset := 3; offset < 13; offset++ {
		crumbs.refreshWith("body", view, offset, jsonCrumbSource{})
	}
	if *parses != 1 {
		t.Fatalf("10 caret moves parsed %d times", *parses)
	}
	if *scans != 10 {
		t.Fatalf("10 caret moves built %d paths", *scans)
	}
	view.Buffer.Set(`{"items":[{"id":4}]}`)
	crumbs.refreshWith("body", view, 3, jsonCrumbSource{})
	if *parses != 2 {
		t.Fatalf("a buffer edit parsed %d times", *parses)
	}
}

func TestBreadcrumbFollowsInPlaceBufferSet(t *testing.T) {
	view := &editor.State{Buffer: editor.NewBuffer(`{"data":{"value":1}}`)}
	crumbs, parses, _ := countedBreadcrumbs()
	before := crumbs.refreshWith("body", view, 13, jsonCrumbSource{})
	if before.JSONPath != "$.data.value" {
		t.Fatalf("before Set: %q", before.JSONPath)
	}
	// A fault-tolerant JSON answer now rewrites the existing buffer. Its
	// version changes, so the bar follows the new text at the same offset.
	view.Buffer.Set(`{"data":{"other":2}}`)
	after := crumbs.refreshWith("body", view, 13, jsonCrumbSource{})
	if *parses != 2 {
		t.Fatalf("Buffer.Set parsed %d times", *parses)
	}
	if after.JSONPath != "$.data.other" {
		t.Fatalf("after Set: %q", after.JSONPath)
	}
}

func TestBreadcrumbReusesReaderTreeForShownText(t *testing.T) {
	const body = "{\"data\":1,}" // fault-tolerant: the formatter rewrites it
	result := computeFaultTolerantBody(body)
	if result.crumbRoot == nil || result.crumbText == "" {
		t.Fatal("the worker did not supply a tree for the shown text")
	}
	if !strings.Contains(result.crumbText, `"data"`) {
		t.Fatalf("crumb text %q is not the shown text", result.crumbText)
	}

	view := &editor.State{Buffer: editor.NewBuffer(result.crumbText)}
	crumbs, parses, _ := countedBreadcrumbs()
	path := crumbs.refreshWith("body", view, strings.Index(result.crumbText, `"data"`)+2,
		jsonCrumbSource{root: result.crumbRoot, text: result.crumbText})
	if *parses != 0 {
		t.Fatalf("the UI parsed the worker's tree %d times", *parses)
	}
	if len(path.Segments) < 2 || path.Segments[1].Label != "data" {
		t.Fatalf("path from the worker tree = %+v", path)
	}

	// A tree for different bytes must not be used, even when supplied.
	other := &editor.State{Buffer: editor.NewBuffer(`{"other":1}`)}
	crumbs.refreshWith("body", other, 3, jsonCrumbSource{root: result.crumbRoot, text: result.crumbText})
	if *parses != 1 {
		t.Fatalf("a stale tree avoided a needed parse: %d", *parses)
	}
}

// The request body keeps no path bar: its tool row and text are all that sits
// above the editor. The response body still has its bar, and Copy path still
// knows the request body's node under the caret.
func TestRequestBodyHasNoJSONPathBar(t *testing.T) {
	a := snapshotApp()
	a.addDocument(savedRequest{Request: request.Request{Name: "Create project", Method: "POST",
		URL: "https://api.example.com/projects", Body: `{"data":{"id":3}}`}}, "")
	a.composeTab = composeTabBody
	tt := ui.NewTester(a.view, 1280, 800)
	tt.Frame()
	if tt.HasText(locales.JsonBreadcrumbCopyPath()) {
		t.Fatal("the request body drew a JSON path bar")
	}
	if a.activeJSONPath.path == "" {
		t.Fatal("the request body stopped tracking the path under its caret")
	}
	a.activateDocument(nil)
	tt.Frame()
	if !tt.HasText(locales.JsonBreadcrumbCopyPath()) {
		t.Fatal("the response body lost its JSON path bar")
	}
}

func TestBreadcrumbLargeBodyHasNoBar(t *testing.T) {
	a := workspaceApp()
	body := `{"big":"` + strings.Repeat("x", jsonBreadcrumbScanLimit+1) + `"}`
	a.flows[0].resBody, a.flows[0].bytes = body, len(body)
	a.flows[0].respHeader = http.Header{"Content-Type": {"application/json"}}
	a.syncDetail()
	a.bodyModes(a.flows[0], 0)
	a.bodyMode[0] = 1
	tt := ui.NewTester(func(c *ui.Context) {
		ui.Column(c).Fill().Children(func() { a.bodySideView(c, a.flows[0], 0) })
	}, 900, 520)
	tt.Frame()
	if len(a.jsonCrumbs[0].path.Segments) != 0 || tt.HasText("Copy path") {
		t.Fatal("a body beyond the scan limit drew a breadcrumb bar")
	}
	if a.bodyText[0] == nil {
		t.Fatal("a large body stopped being readable")
	}
}
