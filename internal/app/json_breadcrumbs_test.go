package app

import (
	"net/http"
	"strings"
	"testing"

	"github.com/baba983/vscroxy/internal/jsonc"
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

func TestBreadcrumbRecomputesOnlyOnChange(t *testing.T) {
	view := &editor.State{Buffer: editor.NewBuffer(`{"a":{"b":1}}`)}
	var crumbs jsonBreadcrumbs
	calls := 0
	crumbs.scan = func(text string, offset int) jsonc.BreadcrumbPath {
		calls++
		return jsonc.BreadcrumbsAt(text, offset)
	}
	for range 4 {
		crumbs.refresh("body", view, 8)
	}
	if calls != 1 {
		t.Fatalf("an unchanged caret scanned %d times", calls)
	}
	crumbs.refresh("body", view, 9)
	if calls != 2 {
		t.Fatalf("a moved caret scanned %d times", calls)
	}
	view.Select(len(`{"a":`), len(`{"a":`))
	view.Insert("x")
	crumbs.refresh("body", view, view.Cursor())
	if calls != 3 {
		t.Fatalf("an edited buffer scanned %d times", calls)
	}
	crumbs.refresh("other", view, view.Cursor())
	if calls != 4 {
		t.Fatalf("another body scanned %d times", calls)
	}
}

func TestBreadcrumbFollowsInPlaceBufferSet(t *testing.T) {
	view := &editor.State{Buffer: editor.NewBuffer(`{"data":{"value":1}}`)}
	var crumbs jsonBreadcrumbs
	calls := 0
	crumbs.scan = func(text string, offset int) jsonc.BreadcrumbPath {
		calls++
		return jsonc.BreadcrumbsAt(text, offset)
	}
	before := crumbs.refresh("body", view, 13)
	if before.JSONPath != "$.data.value" {
		t.Fatalf("before Set: %q", before.JSONPath)
	}
	// A fault-tolerant JSON answer now rewrites the existing buffer. Its
	// version changes, so the bar follows the new text at the same offset.
	view.Buffer.Set(`{"data":{"other":2}}`)
	after := crumbs.refresh("body", view, 13)
	if calls != 2 {
		t.Fatalf("Buffer.Set scanned %d times", calls)
	}
	if after.JSONPath != "$.data.other" {
		t.Fatalf("after Set: %q", after.JSONPath)
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
