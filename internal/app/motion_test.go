package app

import (
	"testing"
	"time"

	"github.com/egoist/mygo/ui"
)

// The recording dot's pulse is painted, not built: a second of it runs frames
// that repeat the last layout, so the view that holds the traffic table runs
// about once, not once a frame.
func TestRecordingPulsePaintsWithoutRebuildingTheView(t *testing.T) {
	a := workspaceApp()
	a.srv = requestProxy(t)
	now := time.Now().Unix()
	a.rate.add(now, 2)
	a.rate.add(now+1, 2)
	if a.rate.perSecond() <= 0 {
		t.Fatalf("the fixture has no traffic to pulse for: %v", a.rate.perSecond())
	}
	builds := 0
	tt := ui.NewTester(func(c *ui.Context) { builds++; a.view(c) }, 1280, 800)
	tt.Frame()
	builds = 0
	for range 62 {
		tt.Advance(16 * time.Millisecond)
	}
	if builds > 20 {
		t.Fatalf("the view ran %d times in a second of the recording pulse, near the 62 frames painted", builds)
	}
}
