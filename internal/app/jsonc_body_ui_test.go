package app

import (
	"strings"
	"testing"

	"github.com/egoist/mygo/ui"
)

// TestRequestBodyFormatKeepsJSONC renders the request editor and uses its
// Format tool on a body with a comment and a trailing comma.
func TestRequestBodyFormatKeepsJSONC(t *testing.T) {
	a := workspaceApp()
	tt := ui.NewTester(a.view, 1200, 700)
	click := func(label string) {
		t.Helper()
		if err := tt.Click(label); err != nil {
			t.Fatal(err)
		}
	}
	read := func() string {
		t.Helper()
		click("Request body")
		tt.Key(ui.Cmd, ui.KeyA)
		tt.Key(ui.Cmd, ui.KeyC)
		return tt.Clipboard()
	}

	tt.Key(ui.Cmd, ui.Key2)
	click("List projects")
	click("Request pane: Body")
	click("Request body")
	tt.Key(ui.Cmd, ui.KeyA)
	tt.Type(`{"a":1,/* note */"url":"http://x",}`)

	click("Format body")
	formatted := read()
	if !strings.Contains(formatted, "/* note */") || !strings.Contains(formatted, `"url": "http://x"`) ||
		!strings.Contains(formatted, "\n  \"a\": 1") {
		t.Fatalf("JSONC was not preserved and formatted: %q", formatted)
	}
	if a.requestDocument.composeBody != formatted {
		t.Fatalf("the editor and the saved draft disagree: %q vs %q", a.requestDocument.composeBody, formatted)
	}
	if tt.Image() == nil {
		t.Fatal("the request editor did not render")
	}
}
