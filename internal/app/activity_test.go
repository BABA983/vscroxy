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

// The plot draws the last five minutes whatever the traffic: a bucket keeps
// its place, so bars do not jump as each request arrives and the times a brush
// picks stay the ones it was drawn on.
func TestActivityAxisKeepsItsBucketsInPlace(t *testing.T) {
	a := workspaceApp()
	now := time.Now().Truncate(time.Second)
	end := now.Add(10 * time.Second).Truncate(activityStep).Add(activityStep)
	a.flows = nil
	for i := range 6 {
		a.flows = append(a.flows, flow{id: i + 1, at: now.Add(time.Duration(i) * time.Second), host: "example.test", method: "GET", status: 200, live: true})
	}
	a.applyFilter()
	start, used := a.activityAxis(end)
	if span := end.Sub(start); span != activitySpan || used != activityBuckets {
		t.Fatalf("axis spans %v in %d buckets, want the whole %v", span, used, activitySpan)
	}
	first := int(a.flows[0].at.Sub(start) / activityStep)
	// Older traffic arriving later must not move the bars already drawn.
	a.flows = append([]flow{{id: 7, at: now.Add(-4 * time.Minute), host: "example.test", method: "GET", status: 200, live: true}}, a.flows...)
	a.applyFilter()
	start2, used2 := a.activityAxis(end)
	if !start2.Equal(start) || used2 != used {
		t.Fatalf("the axis moved to %v (%d buckets) as traffic arrived", start2, used2)
	}
	if got := int(a.flows[1].at.Sub(start2) / activityStep); got != first {
		t.Fatalf("the request moved from bucket %d to %d", first, got)
	}
	// A brush keeps the axis it was drawn on: the selected times come from it.
	brush := activityRange{end: end, from: start.Add(time.Duration(first) * activityStep), to: start.Add(time.Duration(first+1) * activityStep), frozen: true}
	if from, _ := brush.bounds(); !from.Equal(start.Add(time.Duration(first) * activityStep)) {
		t.Fatalf("brush starts at %v, want the bucket it was drawn on", from)
	}
	if band := brush.band(start); band != (uikit.ActivityRange{From: first, To: first + 1}) {
		t.Fatalf("the brush draws as %+v, want its own bucket", band)
	}
}

// A brush is one window: the chip, the rows it keeps and the band the strip
// draws all name the same traffic. When the strip's clock moves on, the band
// goes with the bars instead of sliding off them.
func TestActivityBrushKeepsItsWindowWhenTheAxisMoves(t *testing.T) {
	a := workspaceApp()
	now := time.Now().Truncate(activityStep)
	a.flows = nil
	for i := range 6 {
		at := now.Add(-time.Duration(6-i) * activityStep)
		a.flows = append(a.flows, flow{id: i + 1, at: at, host: "example.test", method: "GET", status: 200, live: true})
	}
	a.applyFilter()
	tt := ui.NewTester(a.view, 1280, 800)
	tt.Frame()
	plot, ok := tt.Find("Activity time range")
	if !ok {
		t.Fatal("no activity strip")
	}
	// Brush the buckets the six requests sit in: the right sixth of the axis.
	tt.Press(plot.X+plot.W*0.9, plot.Y+plot.H/2)
	tt.Move(plot.X+plot.W, plot.Y+plot.H/2)
	tt.Release(plot.X+plot.W, plot.Y+plot.H/2)
	tt.Frame()
	if len(a.visible) != 6 {
		t.Fatalf("%d rows in the brushed window, want all 6", len(a.visible))
	}
	from, to := a.activity.bounds()
	// The strip's clock moves on, as a live capture's does.
	a.activity.end = a.activity.end.Add(40 * time.Second)
	tt.Frame()
	if got, _ := a.activity.bounds(); !got.Equal(from) {
		t.Fatalf("the window moved from %v to %v", from, got)
	}
	if _, got := a.activity.bounds(); !got.Equal(to) {
		t.Fatalf("the window ends at %v, want %v", got, to)
	}
	if len(a.visible) != 6 {
		t.Fatalf("%d rows after the axis moved, want the same 6", len(a.visible))
	}
	if !tt.HasText("Time: " + a.activity.label()) {
		t.Fatalf("the chip no longer names the window: %q", tt.Texts())
	}
	// The band is drawn from the window on the axis the bars are drawn on,
	// so every row the chip keeps sits inside it.
	start, _ := a.activityAxis(a.activity.end)
	band := a.activity.band(start)
	for _, at := range a.visible {
		bucket := int(a.flows[at].at.Sub(start) / activityStep)
		if bucket < band.From || bucket >= band.To {
			t.Fatalf("the row at %v sits in bucket %d, outside the band %+v", a.flows[at].at.Format("15:04:05"), bucket, band)
		}
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
