package app

import (
	"encoding/json/jsontext"
	"strings"
	"testing"

	"github.com/baba983/vscroxy/internal/jsonc"
	"github.com/baba983/vscroxy/locales"
	"github.com/egoist/mygo/ui"
)

func TestBodyJSONPartialTreeKeepsReadNodesAndStop(t *testing.T) {
	body := "{\n  \"id\": 1,\n  \"name\": \"st"
	parsed := parseJSON(body)
	if len(parsed.roots) != 1 || parsed.roots[0].kind != "object" || len(parsed.roots[0].children) != 2 {
		t.Fatalf("partial object was lost: %+v", parsed)
	}
	if got := parsed.roots[0].children[1]; got.key != "name" || got.value != `"st"` {
		t.Fatalf("node before the truncation = %+v", got)
	}
	if !strings.Contains(parsed.stop, "line 3, column 11") {
		t.Fatalf("stop line = %q, want a line and column", parsed.stop)
	}
	if want := locales.JsonParsingStopped(3, 11, 23); parsed.stop != want {
		t.Fatalf("stop line = %q, want %q", parsed.stop, want)
	}
}

func TestBodyJSONStopLineCountsUnicodeAndClamps(t *testing.T) {
	// Two bytes are one Chinese character in the column, while the byte
	// offset in the message stays the parser's byte offset.
	body := "ab\n名前x"
	if got, want := jsonStopLine(body, []jsonc.ParseError{{Offset: 6}}),
		locales.JsonParsingStopped(2, 2, 6); got != want {
		t.Fatalf("stop = %q, want %q", got, want)
	}
	if got, want := jsonStopLine(body, []jsonc.ParseError{{Offset: -1}}),
		locales.JsonParsingStopped(1, 1, 0); got != want {
		t.Fatalf("negative stop = %q, want %q", got, want)
	}
	if got, want := jsonStopLine(body, []jsonc.ParseError{{Offset: 99}}),
		locales.JsonParsingStopped(2, 4, len(body)); got != want {
		t.Fatalf("past-end stop = %q, want %q", got, want)
	}
}

func TestBodyJSONNDJSONHasOneRootPerLine(t *testing.T) {
	body := "{\"id\":1}\r\n{\"id\":2,\"nested\":{\"ok\":true}}\r\n[3]\r\n"
	parsed := parseJSON(body)
	if !parsed.ndjson || parsed.stop != "" || len(parsed.roots) != 3 {
		t.Fatalf("NDJSON result = %+v", parsed)
	}
	if got := parsed.roots[1].path; got != "/1" {
		t.Fatalf("second line path = %q", got)
	}
	if got := parsed.roots[1].children[1].children[0].path; got != "/1/nested/ok" {
		t.Fatalf("nested path = %q", got)
	}
	if parsed.roots[1].id == parsed.roots[0].id || parsed.roots[1].children[0].id == parsed.roots[0].children[0].id {
		t.Fatal("NDJSON lines share expansion identities")
	}
	// A pretty document that spans lines is one value, not a stream.
	if got := parseJSON("[\n  1,\n  2\n]"); got.ndjson || len(got.roots) != 1 {
		t.Fatalf("pretty array was split into lines: %+v", got)
	}
}

func TestBodyJSONPathsAreUniquePointers(t *testing.T) {
	parsed := parseJSON(`{"a.b":1,"a":{"b":2},"a/b":3}`)
	if len(parsed.roots) != 1 || len(parsed.roots[0].children) != 3 {
		t.Fatalf("parse = %+v", parsed)
	}
	first, nested, slash := parsed.roots[0].children[0], parsed.roots[0].children[1], parsed.roots[0].children[2]
	if first.path != "/a.b" || nested.children[0].path != "/a/b" || slash.path != "/a~1b" {
		t.Fatalf("paths = %q, %q, %q", first.path, nested.children[0].path, slash.path)
	}
	if first.path == nested.children[0].path || first.path == slash.path {
		t.Fatal("visible paths collide")
	}
	ids := map[nodeID]bool{}
	var walk func([]*jsonNode)
	walk = func(nodes []*jsonNode) {
		for _, n := range nodes {
			if ids[n.id] {
				t.Fatalf("duplicate expansion id %+v", n.id)
			}
			ids[n.id] = true
			walk(n.children)
		}
	}
	walk(parsed.roots)

	// Expansion follows identity, not the visible pointer: expanding the
	// nested /a/b node cannot expand a different /a/b from a dotted key.
	dup := parseJSON(`{"a.b":{"x":1},"a":{"b":2}}`)
	a := &app{jsonTrees: [2]jsonTreeResult{dup}}
	nested = dup.roots[0].children[1]
	a.jsonExpanded[0] = map[nodeID]bool{dup.roots[0].id: true, nested.id: true}
	rows := a.visibleJSON(0)
	if len(rows) != 4 || rows[3].node.key != "b" {
		t.Fatalf("expanded rows = %+v", rows)
	}
	if got := matchingJSON(dup.roots, "/a/b"); len(got) != 1 || got[0].node != nested.children[0] {
		t.Fatalf("pointer filter = %+v", got)
	}
}

func TestBodyJSONCTreeKeepsCommentsAndNumberSpelling(t *testing.T) {
	body := "{\n  \"n\": 12.3400, // keep me\n  \"big\": 9007199254740993,\n  \"list\": [true,],\n}"
	parsed := parseJSON(body)
	if parsed.stop != "" || len(parsed.roots) != 1 || len(parsed.roots[0].children) != 3 {
		t.Fatalf("JSONC tree = %+v", parsed)
	}
	if got := parsed.roots[0].children[0]; got.value != "12.3400" {
		t.Fatalf("number spelling = %q", got.value)
	}
	if got := parsed.roots[0].children[1]; got.value != "9007199254740993" {
		t.Fatalf("large integer = %q", got.value)
	}
}

func TestBodyPrettyJSONFormatting(t *testing.T) {
	valid := `{"a":[1,2],"b":{"c":"x"}}`
	value := jsontext.Value(valid)
	if err := value.Indent(jsontext.WithIndent("  ")); err != nil {
		t.Fatal(err)
	}
	got, isJSON := prettyBodyText(valid, nil)
	if !isJSON || got != string(value) {
		t.Fatalf("valid Pretty text = %q, want %q", got, value)
	}
	// A worker result never overrides the strict formatter's exact output.
	stale := &jsonReader{ready: true, result: jsonBodyResult{pretty: "ignored", prettyOK: true}}
	got, _ = prettyBodyText(valid, stale)
	if got != string(value) {
		t.Fatalf("valid body was reformatted by the fallback: %q", got)
	}

	for _, tc := range []struct {
		name string
		body string
		want []string
	}{
		{"truncated", `{"a":{"b":1},"c":[2`, []string{"\n  \"a\": {\n    \"b\": 1", "\"c\": ["}},
		{"jsonc", "{\"a\":1,// note\n\"b\":2,}", []string{"\n  \"a\": 1, // note", "\n  \"b\": 2,"}},
		{"ndjson", "{\"a\":1}\n{\"b\":2}\n", []string{"{\n  \"a\": 1\n}\n{\n  \"b\": 2\n}\n"}},
		{"garbage", `{"a":1} trailing`, []string{"\n  \"a\": 1", "} trailing"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			result := computeFaultTolerantBody(tc.body)
			formatted, ok := result.pretty, result.prettyOK
			if !ok || formatted == tc.body {
				t.Fatalf("Pretty did not re-indent %q: %q", tc.body, formatted)
			}
			for _, want := range tc.want {
				if !strings.Contains(formatted, want) {
					t.Fatalf("Pretty text %q does not contain %q", formatted, want)
				}
			}
		})
	}
}

func TestBodyJSONTreeShowsStopLineInTheViewer(t *testing.T) {
	body := `{"id":1,"name":"st`
	a := snapshotApp()
	a.flows[0].resBody, a.flows[0].bytes = body, len(body)
	a.syncDetail()
	tt := ui.NewTester(a.view, 1280, 800)
	tt.SetLocale("en-US")
	tt.Frame()
	a.bodyMode[0] = 2 // choose JSON after the first frame syncs the selection
	waitInspection(t, tt, func() bool { return a.jsonReader[0].ready })
	want := parseJSON(body).stop
	if !tt.HasText(want) {
		t.Fatalf("stop line %q missing from Body tab: %q", want, tt.Texts())
	}
	if !tt.HasText("name") || !tt.HasText(`"st"`) {
		t.Fatalf("partial tree is not visible: %q", tt.Texts())
	}
	if a.flows[0].resBody != body {
		t.Fatalf("reading the tree changed the retained body: %q", a.flows[0].resBody)
	}
}

func TestJSONReaderDropsStaleAnswer(t *testing.T) {
	a := &app{}
	a.jsonReader[0] = jsonReader{key: "1:0", body: "old body", pending: true}
	a.finishJSONRead(0, "2:0", "new body", jsonBodyResult{
		tree:     jsonTreeResult{roots: []*jsonNode{{key: "stale"}}},
		prettyOK: true,
	})
	if a.jsonReader[0].ready || len(a.jsonTrees[0].roots) != 0 {
		t.Fatalf("a late answer replaced the current body: %+v", a.jsonReader[0])
	}
}

func TestBodyJSONPlainTextKeepsOldNotice(t *testing.T) {
	for _, body := range []string{"not json", "<html><body>plain page</body></html>", "   "} {
		a := snapshotApp()
		a.flows[0].resBody, a.flows[0].bytes = body, len(body)
		a.syncDetail()
		tt := ui.NewTester(a.view, 1280, 800)
		tt.SetLocale("en-US")
		tt.Frame()
		a.bodyMode[0] = 2
		waitInspection(t, tt, func() bool { return a.jsonReader[0].ready })
		if !tt.HasText(locales.BodyIsNotJsonShowing()) {
			t.Fatalf("%q lost the old notice: %q", body, tt.Texts())
		}
		if tt.HasText("Parsing stopped at") {
			t.Fatalf("%q gained a stop line without a tree: %q", body, tt.Texts())
		}
	}
}

func TestBodyPrettyShowsStopLineForPartialTree(t *testing.T) {
	body := `{"a":1,"b":[2`
	a := snapshotApp()
	a.flows[0].resBody, a.flows[0].bytes = body, len(body)
	a.syncDetail()
	tt := ui.NewTester(a.view, 1280, 800)
	tt.SetLocale("en-US")
	tt.Frame()
	a.bodyMode[0] = 1
	waitInspection(t, tt, func() bool { return a.jsonReader[0].ready })
	want := parseJSON(body).stop
	if !tt.HasText(want) {
		t.Fatalf("Pretty lost the stop line %q: %q", want, tt.Texts())
	}
	if text := a.bodyText[0].Buffer.String(); !strings.Contains(text, "\n  \"a\": 1,") {
		t.Fatalf("Pretty did not use the worker's formatted text: %q", text)
	}
	if a.flows[0].resBody != body {
		t.Fatal("Pretty changed the retained body")
	}
}

// The new readers are display-only: retained bytes and exports must stay the
// captured bytes even when the tree or Pretty text differs.
func TestBodyJSONReadersKeepCapturedBytes(t *testing.T) {
	bodies := []string{"{\"a\":1,\"b\":2", "{\"a\":1}\n{\"b\":2}\n", "{\"a\":1,/*c*/\"b\":2,}"}
	for _, body := range bodies {
		f := flow{resBody: body, resSize: len(body)}
		if got := f.body(true); got != body {
			t.Fatalf("retained response body changed: %q", got)
		}
		if result := computeFaultTolerantBody(body); !result.prettyOK {
			t.Fatalf("no tree for %q", body)
		}
		if got := f.body(true); got != body {
			t.Fatalf("reading Pretty changed the retained bytes: %q", got)
		}
	}
}
