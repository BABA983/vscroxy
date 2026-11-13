package app

import (
	"slices"
	"testing"
	"time"

	"github.com/baba983/vscroxy/internal/capture"
	"github.com/baba983/vscroxy/internal/uikit"
	"github.com/egoist/mygo/ui"
)

func TestActivityBrushFiltersWithoutLosingContext(t *testing.T) {
	a := workspaceApp()
	start := time.Date(2026, 1, 15, 10, 30, 0, 0, time.UTC)
	a.flows = nil
	for i, offset := range []time.Duration{-1, 50*time.Second - 1, 50 * time.Second, 150*time.Second - 1, 150 * time.Second, 299 * time.Second} {
		a.flows = append(a.flows, flow{id: i + 1, at: start.Add(offset), host: "example.test", method: "GET", status: 200})
	}
	a.flows = append(a.flows, flow{id: 7, at: start.Add(100 * time.Second), host: "other.test"})
	a.applyFilter()
	tt := ui.NewTester(a.view, 1440, 800)
	if err := tt.Click("Search host or path"); err != nil {
		t.Fatal(err)
	}
	tt.Type("example")
	check := func(ids ...int) {
		t.Helper()
		if got := visibleIDs(a); !slices.Equal(got, ids) {
			t.Fatalf("visible %v, want %v", got, ids)
		}
	}
	plot, ok := tt.Find("Activity time range")
	strip, _ := tt.Find("Activity strip")
	if !ok || strip.H != uikit.HeightStatus {
		t.Fatalf("missing the 24px overview: %v, %v", strip, tt.Texts())
	}
	x := func(bucket float32) float32 { return plot.X + plot.W*bucket/60 }
	y := plot.Y + plot.H/2
	// Start in an empty bucket: the inspector must not collapse or change
	// the plot's coordinates before the drag reaches a populated interval.
	tt.Press(x(0.2), y)
	check()
	if moved, _ := tt.Find("Activity time range"); moved != plot {
		t.Fatalf("empty selection moved the plot from %v to %v", plot, moved)
	}
	tt.Move(x(29.2), y)
	tt.Release(x(29.2), y)
	check(2, 3, 4)
	if err := tt.Click("Clear activity range"); err != nil {
		t.Fatal(err)
	}
	// Reverse selection has the same half-open bucket boundaries: exactly
	// 10:30:50 is included, exactly 10:32:30 is not.
	tt.Press(x(29.2), y)
	tt.Move(x(10.2), y)
	tt.Release(x(10.2), y)
	check(3, 4)
	if !tt.HasText("Time: 10:30:50–10:32:30") || !a.filtersActive() {
		t.Fatal("range summary or active filter was lost")
	}
	// Both handles resize rather than starting a new selection.
	tt.Press(x(10), y)
	tt.Move(x(20), y)
	tt.Release(x(20), y)
	check(4)
	tt.Press(x(30), y)
	tt.Move(x(40), y)
	tt.Release(x(40), y)
	check(4, 5)
	// The deep body condition and the time brush intersect. Body results
	// outside the selected interval still contribute to the overview.
	a.flows[4].resBody, a.flows[5].resBody = "needle", "needle"
	a.views.activeView().body = "needle"
	a.applyFilter()
	settle(t, a, &a.views)
	tt.Frame()
	check(5)
	if !tt.HasText("Time: 10:31:40–10:33:20") {
		t.Fatal("body results did not narrow the selected interval")
	}
	if err := tt.Click("Remove condition: body:needle"); err != nil {
		t.Fatal(err)
	}
	check(4, 5)
	// New data cannot slide the selected window, or hide its context.
	a.flows = append(a.flows, flow{id: 8, host: "example.test", at: start.Add(10 * time.Minute)})
	a.applyFilter()
	tt.Frame()
	check(4, 5)
	if !tt.HasText("Time: 10:31:40–10:33:20") || !tt.HasText("10:35:00") {
		t.Fatal("new traffic moved the selected timeline")
	}
	tt.SetSize(960, 600)
	if strip, _ := tt.Find("Activity strip"); strip.H != uikit.HeightStatus {
		t.Fatalf("narrow activity strip grew: %v", strip)
	}
	tt.Click("Activity time range")
	tt.Key(0, ui.KeyEscape)
	check(1, 2, 3, 4, 5, 6, 8)
	if a.query != "example" || a.activity.active() {
		t.Fatal("clearing time removed another filter or failed to resume the window")
	}
}

func TestActivityRangeIsViewLocalAndTransient(t *testing.T) {
	a := snapshotApp()
	tt := ui.NewTester(a.view, 1280, 800)
	if err := tt.Click("Activity time range"); err != nil {
		t.Fatal(err)
	}
	tt.Key(0, ui.KeyLeft)
	if !a.activity.active() {
		t.Fatal("keyboard did not select an interval")
	}
	selection := a.activity
	a.views.newView(a)
	tt.Frame()
	if a.activity.active() {
		t.Fatal("new view inherited the time range")
	}
	a.views.activate(a, 0)
	tt.Frame()
	if a.activity != selection {
		t.Fatal("view lost its time range")
	}
	if err := tt.Click("Reset all"); err != nil {
		t.Fatal(err)
	}
	if a.activity.active() || len(a.visible) != 2 {
		t.Fatal("Reset all kept the time filter")
	}
	tt.Click("Activity time range")
	a.views.newView(a)
	tt.Frame()
	a.replaceSession(a.flows, nil)
	a.views.activate(a, 0)
	tt.Frame()
	if a.activity.active() {
		t.Fatal("replacement session inherited the previous session's time range")
	}
}

// Six requests a few seconds apart spread across the plot: the axis starts just
// before the first of them instead of holding the whole five minutes, which left
// them in one bucket at its right edge.
func TestActivityAxisFollowsSparseTraffic(t *testing.T) {
	a := workspaceApp()
	now := time.Now().Truncate(time.Second)
	a.flows = nil
	for i := range 6 {
		a.flows = append(a.flows, flow{id: i + 1, at: now.Add(time.Duration(i) * time.Second), host: "example.test", method: "GET", status: 200, live: true})
	}
	a.tally = tally{}
	a.applyFilter()
	end := a.activityEnd(now.Add(10 * time.Second))
	start, used := a.activityAxis(end)
	if span := end.Sub(start); span > time.Minute || used > activityBuckets {
		t.Fatalf("axis spans %v in %d buckets, want a short one for six requests a few seconds apart", span, used)
	}
	first := int(a.flows[0].at.Sub(start) / activityStep)
	last := int(a.flows[5].at.Sub(start) / activityStep)
	if last >= used {
		t.Fatalf("the newest request is in bucket %d of %d, outside the axis", last, used)
	}
	if first >= used-1 {
		t.Fatalf("the first request is in bucket %d of %d, want the axis to start before it", first, used)
	}
	// The plot draws the axis it used across its whole width, so six requests
	// a second apart fill a third of it rather than one sixtieth.
	if spread := last - first + 1; spread < 2 {
		t.Fatalf("six requests fall in %d bucket(s), want them spread across the plot", spread)
	}
	// A brush keeps the axis it was drawn on: the selected times come from it.
	brush := activityRange{end: end, start: start, selection: uikit.ActivityRange{From: first, To: last + 1}}
	if from, _ := brush.bounds(); !from.Equal(start.Add(time.Duration(first) * activityStep)) {
		t.Fatalf("brush starts at %v, want the bucket it was drawn on", from)
	}
}

func TestActivityPausedCaptureShowsOnlyRecentTraffic(t *testing.T) {
	a := workspaceApp()
	var err error
	a.srv, err = capture.New("127.0.0.1:0", t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if err := a.srv.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { a.srv.Close() })
	a.recordingPaused = true
	now := time.Now().Truncate(5 * time.Second)
	a.flows[0].at, a.flows[0].live = now.Add(-6*time.Minute), true
	a.flows[1].at, a.flows[1].live = now.Add(-time.Second), true
	a.applyFilter()
	tt := ui.NewTester(a.view, 1280, 800)
	tt.Frame()
	if a.activityCounts.total != 1 {
		t.Fatalf("paused capture counted %d requests in the window, want only the recent one", a.activityCounts.total)
	}
}
