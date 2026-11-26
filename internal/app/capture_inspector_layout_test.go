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
	if !ok || hint.H < 2*uikit.TextSmall*uikit.LineSmall {
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
