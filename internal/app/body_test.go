package app

import (
	"net/http"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/baba983/vscroxy/internal/uikit"
	"github.com/egoist/mygo/ui"
	"github.com/egoist/mygo/ui/editor"
)

func TestContentType(t *testing.T) {
	structured := flow{respHeader: http.Header{"Content-Type": {"Image/PNG; charset=binary"}}}
	if got := structured.contentType(true); got != "image/png" {
		t.Errorf("structured = %q, want image/png", got)
	}
	formatted := flow{resHeaders: "HTTP/1.1 200 OK\nContent-Type: text/html; charset=utf-8\n"}
	if got := formatted.contentType(true); got != "text/html" {
		t.Errorf("formatted = %q, want text/html", got)
	}
	request := flow{reqHeaders: "GET / HTTP/1.1\nContent-Type: application/json\n"}
	if got := request.contentType(false); got != "application/json" {
		t.Errorf("request = %q, want application/json", got)
	}
	if got := (flow{}).contentType(true); got != "" {
		t.Errorf("empty = %q, want empty", got)
	}
}

func TestBodyReadsSpillFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "body.bin")
	if err := os.WriteFile(path, []byte("whole body"), 0o600); err != nil {
		t.Fatalf("write spill file: %v", err)
	}
	f := flow{resBody: "head", resFile: path, resSize: 10}
	if got := f.body(true); got != "whole body" {
		t.Errorf("spilled body = %q, want the file's", got)
	}
	f.resFile = filepath.Join(t.TempDir(), "gone.bin")
	if got := f.body(true); !strings.HasPrefix(got, "head") || !strings.Contains(got, "gone") {
		t.Errorf("missing file = %q, want the head and a note", got)
	}
	if got := (flow{resBody: "inline"}).body(true); got != "inline" {
		t.Errorf("inline body = %q, want inline", got)
	}
}

func TestParseJSON(t *testing.T) {
	roots, ok := parseJSON(`{"z":[9007199254740993,1.2300e+09],"a":true}`)
	if !ok || len(roots) != 1 {
		t.Fatalf("parse = %v, %v", roots, ok)
	}
	root := roots[0]
	if root.kind != "object" || len(root.children) != 2 {
		t.Fatalf("root = %+v", root)
	}
	if root.children[0].key != "z" || root.children[0].kind != "array" || len(root.children[0].children) != 2 {
		t.Fatalf("first child = %+v", root.children[0])
	}
	if root.children[0].children[0].value != "9007199254740993" || root.children[0].children[1].value != "1.2300e+09" {
		t.Fatal("JSON tree rounded or reformatted the original numbers")
	}
	if root.children[1].key != "a" || root.children[1].value != "true" {
		t.Fatalf("second child = %+v", root.children[1])
	}
	for _, invalid := range []string{"not json", `{"a":1`, `[1,]`, `{} {}`, `{"a":1,"a":2}`} {
		if _, ok := parseJSON(invalid); ok {
			t.Fatalf("invalid body %q parsed as JSON", invalid)
		}
	}
}

// The JSON tree is a custom list, but its values must use the same token ink
// Pretty gives them: strings in the string color, everything else in the
// constant color. Body text on both values was invisible as syntax.
func TestJSONTreeUsesEditorTokenColors(t *testing.T) {
	roots, ok := parseJSON(`{"name":"Demo project","id":73,"active":true,"nothing":null}`)
	if !ok || len(roots) == 0 {
		t.Fatalf("fixture did not parse: %v, %v", roots, ok)
	}
	a := &app{}
	rows := make([]jsonRow, 0, len(roots[0].children))
	for _, n := range roots[0].children {
		rows = append(rows, jsonRow{node: n, depth: n.depth})
	}
	for _, dark := range []bool{false, true} {
		th := uikit.Theme(dark)
		editorTheme := uikit.EditorTheme(th)
		tt := ui.NewTester(func(c *ui.Context) {
			c.SetTheme(th)
			ui.Column(c).Fill().Background(th.Background).Padding(20).Children(func() {
				a.jsonTree(c, 0, rows, editorTheme, th)
			})
		}, 600, 200)
		tt.SetLocale("en-US")
		tt.Frame()
		img := tt.Image()
		for _, tc := range []struct {
			label string
			want  ui.Color
		}{
			{`"Demo project"`, editorTheme.Tokens[editor.TokenString].Color},
			{"73", editorTheme.Tokens[editor.TokenConstant].Color},
			{"true", editorTheme.Tokens[editor.TokenConstant].Color},
			{"null", editorTheme.Tokens[editor.TokenConstant].Color},
		} {
			r, ok := tt.Find(tc.label)
			if !ok {
				t.Fatalf("dark=%t: %q missing from JSON tree: %q", dark, tc.label, tt.Texts())
			}
			found := 0
			for y := max(img.Bounds().Min.Y, int(r.Y)); y < min(img.Bounds().Max.Y, int(r.Y+r.H)); y++ {
				for x := max(img.Bounds().Min.X, int(r.X)); x < min(img.Bounds().Max.X, int(r.X+r.W)); x++ {
					got := img.RGBAAt(x, y)
					if got.R == tc.want.R && got.G == tc.want.G && got.B == tc.want.B {
						found++
					}
				}
			}
			if found == 0 {
				t.Errorf("dark=%t: %q has no %v ink", dark, tc.label, tc.want)
			}
		}
	}
}

func TestHexLine(t *testing.T) {
	body := "Hello, world!\x00\x01\x02"
	got := hexLine(body, 0)
	want := "00000000  48 65 6c 6c 6f 2c 20 77  6f 72 6c 64 21 00 01 02  |Hello, world!...|"
	if got != want {
		t.Fatalf("hex line =\n%q\nwant\n%q", got, want)
	}
	if got := hexLine(body, 1); got != "" {
		t.Fatalf("second line = %q, want empty", got)
	}
}

// Display limits are independent of capture errors and must not inject
// warning text into payload bytes used by JSON/image/hex readers.
func TestBodyPreviewLimitAndWarnings(t *testing.T) {
	for _, size := range []int{maxBodyView, maxBodyView + 1} {
		f := flow{resBody: strings.Repeat("x", size), resSize: size}
		body, notice := f.bodyPreview(true)
		if len(body) != maxBodyView || strings.Contains(body, "truncated") {
			t.Fatal("display limit changed the retained payload bytes")
		}
		if (notice != "") != (size > maxBodyView) {
			t.Fatalf("size %d has incorrect limit notice: %q", size, notice)
		}
	}
	f := flow{resBody: `{"ok":true}`, resError: "transfer stopped"}
	body, notice := f.bodyPreview(true)
	if body != `{"ok":true}` || !strings.Contains(notice, "transfer stopped") || f.body(true) != body+notice {
		t.Fatal("capture warning was lost or mixed into the payload")
	}
}

func TestBodyFormatIsRememberedPerContentType(t *testing.T) {
	a := workspaceApp()
	a.body = "body{color:red}"
	f := flow{id: 1, resBody: "body{color:red}", respHeader: http.Header{"Content-Type": {"text/css"}}}
	a.bodyFormatByType = map[string]string{"text/css": "CSS"}
	modes := a.bodyModes(f, 0)
	if a.bodyMode[0] >= len(modes) || modes[a.bodyMode[0]] != "CSS" {
		t.Fatalf("remembered CSS mode = %v, %v", a.bodyMode[0], modes)
	}
	for _, tc := range []struct {
		contentType, mode string
	}{
		{"text/javascript", "JavaScript"},
		{"text/markdown", "Markdown"},
		{"text/event-stream", "Merged stream"},
	} {
		f.respHeader = http.Header{"Content-Type": {tc.contentType}}
		modes := a.bodyModes(f, 0)
		if !slices.Contains(modes, tc.mode) {
			t.Fatalf("%s modes = %v, want %s", tc.contentType, modes, tc.mode)
		}
	}
	f.respHeader = http.Header{"Content-Type": {"text/event-stream"}}
	if mode := a.autoBodyMode(f, 0, "data: {\"choices\":[{\"delta\":{\"content\":\"hi\"}}]}\n\n"); mode >= len(a.bodyModes(f, 0)) || a.bodyModes(f, 0)[mode] != "Merged stream" {
		t.Fatalf("SSE auto mode = %v", mode)
	}
}
