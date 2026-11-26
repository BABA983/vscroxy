package app

import (
	"testing"
	"time"

	"github.com/egoist/mygo/ui"
)

// The recording lamp is a static dot. A pulse painted from Draw would ask the
// window for frames while the capture is idle, and AnimationFrameRate lowers
// the whole window's display rate on macOS, pacing scrolling with it.
func TestRecordingLampDoesNotAnimate(t *testing.T) {
	a := workspaceApp()
	a.srv = requestProxy(t)
	a.lastFlow.Store(time.Now().Unix())
	tt := ui.NewTester(a.view, 1280, 800)
	tt.Frame()
	tt.Frame()
	if !tt.HasText("Recording") {
		t.Fatalf("the recording lamp is not drawn: %v", tt.Texts())
	}
	if tt.FramePending() {
		t.Fatal("an idle capture screen with traffic asks for another frame; the lamp must not animate")
	}
}
