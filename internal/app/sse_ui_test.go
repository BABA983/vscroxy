package app

import (
	"testing"

	"github.com/egoist/mygo/ui"
)

// The SSE dialog names itself once: the state is a pill beside the title,
// not part of the title. A live session offers Reconnect and Stop, never a
// disabled Connect, and copying an event's ID is an icon on its meta line.
func TestSSESessionTitleStateAndActions(t *testing.T) {
	a := workspaceApp()
	a.sse.open = true
	a.sse.session = &sseSession{state: "Connected"}
	tt := ui.NewTester(a.view, 1280, 800)
	tt.Frame()
	tt.Frame()
	if !tt.HasText("SSE session") || !tt.HasText("Connected") {
		t.Fatalf("title or state missing: %v", tt.Texts())
	}
	if tt.HasText("SSE session · Connected") {
		t.Fatalf("the state is still part of the title: %v", tt.Texts())
	}
	if _, ok := tt.Find("Connect SSE"); ok {
		t.Fatalf("a connected session still offers Connect SSE: %v", tt.Texts())
	}
	for _, label := range []string{"Reconnect", "Stop SSE"} {
		if _, ok := tt.Find(label); !ok {
			t.Fatalf("a connected session does not offer %q: %v", label, tt.Texts())
		}
	}
}
