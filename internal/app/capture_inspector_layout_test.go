package app

import (
	"testing"

	"github.com/baba983/vscroxy/internal/uikit"
	"github.com/baba983/vscroxy/locales"
	"github.com/egoist/mygo/ui"
)

// A scrolling inspector pane ends where its pane ends: the Settings form's
// last row is reachable at the bottom of a focused inspector, not laid out
// below the window and clipped by it.
func TestSettingsScrollReachesItsLastRow(t *testing.T) {
	a := workspaceApp()
	tt := ui.NewTester(a.view, 1280, 700)
	tt.SetLocale("en-US")
	tt.Frame()
	if err := tt.Click("api.example.com/projects"); err != nil {
		t.Fatalf("select the capture: %v", err)
	}
	tt.Frame()
	a.workbench.focused = true
	tt.Frame()
	a.inspect.tab[0] = composeTabSettings
	tt.Frame()
	tt.Frame()
	tt.Scroll(300, 500, 0, 1e6)
	tt.Frame()

	title, ok := tt.Find(locales.SocketPath())
	if !ok || title.W <= 0 {
		t.Fatalf("the last Settings row is not drawn: %+v %v", title, ok)
	}
	hint, ok := tt.Find(locales.SocketPathHint())
	if !ok || hint.H < uikit.TextSmall*uikit.LineSmall {
		t.Fatalf("the last Settings row's description is clipped: %+v %v", hint, ok)
	}
	var scroll ui.Rect
	for _, info := range tt.AccessAll() {
		r := info.Bounds
		if info.Role == ui.RoleScroll && r.H > 100 && r.Y <= title.Y && title.Y < r.Y+r.H {
			scroll = r
		}
	}
	if scroll.H == 0 {
		t.Fatal("the Settings scroll is not drawn")
	}
	if bottom := max(title.Y+title.H, hint.Y+hint.H); bottom > scroll.Y+scroll.H+1 {
		t.Fatalf("the last Settings row ends at %.1f, below its scroll at %.1f: row %+v, hint %+v, scroll %+v",
			bottom, scroll.Y+scroll.H, title, hint, scroll)
	}
	// The pane itself must end above the window's footer: a scroll taller
	// than its pane runs under it and cuts its own last row.
	if footer, ok := tt.Find(locales.ProxyStatus()); ok && scroll.Y+scroll.H > footer.Y+1 {
		t.Fatalf("the Settings scroll ends at %.1f, past the footer at %.1f: %+v", scroll.Y+scroll.H, footer.Y, scroll)
	}
}

// A captured header's value wraps inside its row: the next row starts below
// the wrapped text instead of drawing over it, as a fixed row height did.
func TestCapturedHeaderRowsGrowAroundWrappedValues(t *testing.T) {
	a := workspaceApp()
	long := "authorization, content-type, x-request-id, x-trace-id, x-correlation-id, x-session-id, x-client-version, x-feature-flags, x-rate-limit"
	a.flows[0].respHeader.Set("Access-Control-Allow-Headers", long)
	a.flows[0].respHeader.Set("Access-Control-Expose-Headers", "x-request-id, x-trace-id, x-correlation-id, x-session-id, x-client-version, x-feature-flags, x-rate-limit")
	a.flows[0].respHeader.Set("Cache-Control", "no-store")

	tt := ui.NewTester(a.view, 1280, 800)
	tt.SetLocale("en-US")
	tt.Frame()
	if err := tt.Click("api.example.com/projects"); err != nil {
		t.Fatalf("select the capture: %v", err)
	}
	tt.Frame()
	a.inspect.tab[0], a.inspect.tab[1] = composeTabRaw, resViewHeaders
	tt.Frame()
	tt.Frame()

	keyText, ok := tt.Find("Access-Control-Allow-Headers")
	if !ok {
		t.Fatalf("the first header key is not drawn: %v", tt.Texts())
	}
	value, ok := tt.Find(long)
	if !ok {
		t.Fatalf("the first header value is not drawn: %v", tt.Texts())
	}
	// The grid's cell keeps the inset from the divider, so the column
	// title's box and the row's text start at the same edge. Take the title
	// nearest above each cell: the request pane can hold a second table.
	titleAbove := func(name string, y float32) (ui.Rect, bool) {
		var found ui.Rect
		for _, info := range tt.AccessAll() {
			if info.Name == name && info.Bounds.Y < y && (found.W == 0 || info.Bounds.Y > found.Y) {
				found = info.Bounds
			}
		}
		return found, found.W > 0
	}
	if keyTitle, ok := titleAbove("Key", keyText.Y); !ok {
		t.Fatalf("the Key column title is not drawn: %v", tt.Texts())
	} else if d := keyText.X - keyTitle.X; d < -1 || d > 1 {
		t.Fatalf("the header key starts %.1f from the column title: title %+v, key %+v", d, keyTitle, keyText)
	}
	if valueTitle, ok := titleAbove("Value", value.Y); !ok {
		t.Fatalf("the Value column title is not drawn: %v", tt.Texts())
	} else if d := value.X - valueTitle.X; d < -1 || d > 1 {
		t.Fatalf("the header value starts %.1f from the column title: title %+v, value %+v", d, valueTitle, value)
	}
	if value.H <= uikit.HeightControl {
		t.Fatalf("the long header value did not wrap: box %+v", value)
	}
	next, ok := tt.Find("Access-Control-Expose-Headers")
	if !ok {
		t.Fatalf("the next header row is not drawn: %v", tt.Texts())
	}
	if next.Y < value.Y+value.H {
		t.Fatalf("the next header row starts at %.1f, above the wrapped value ending at %.1f: value %+v, next %+v",
			next.Y, value.Y+value.H, value, next)
	}
}
