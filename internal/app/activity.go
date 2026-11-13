package app

import (
	"math"
	"time"

	"github.com/baba983/vscroxy/internal/uikit"
	"github.com/egoist/mygo/ui"
)

const activitySpan = 5 * time.Minute
const activityStep = 5 * time.Second
const activityBuckets = int(activitySpan / activityStep)

// activityMinBuckets is the shortest axis the strip draws, in buckets: with
// traffic spread over a few seconds, a shorter one would put a handful of
// requests in a single bucket at the axis' edge.
const activityMinBuckets = 6

// activityRange belongs to a capture view, not a saved filter. The overview's
// end is frozen while brushing; new traffic cannot move the selected times.
type activityRange struct {
	end       time.Time
	start     time.Time
	selection uikit.ActivityRange
}

func (r activityRange) active() bool { return r.selection.To > r.selection.From }

func (r activityRange) bounds() (time.Time, time.Time) {
	start := r.start
	if start.IsZero() {
		start = r.end.Add(-activitySpan)
	}
	return start.Add(time.Duration(r.selection.From) * activityStep), start.Add(time.Duration(r.selection.To) * activityStep)
}

func (r activityRange) label() string {
	from, to := r.bounds()
	return from.Format("15:04:05") + "–" + to.Format("15:04:05")
}

// activityEnd follows the live clock, or the latest timestamp of an imported
// or stopped capture. End is exclusive, so the current five-second bucket is
// included even when the newest request falls exactly on its boundary.
func (a *app) activityEnd(now time.Time) time.Time {
	if a.activity.active() && !a.activity.end.IsZero() {
		return a.activity.end
	}
	latest := a.tally.latest
	if latest.IsZero() || a.tally.live > 0 && a.srv != nil && a.srv.Running() {
		latest = now
	}
	return latest.Truncate(activityStep).Add(activityStep)
}

// activityAxis is the span the strip draws, and how many of its buckets that
// takes. It is the last five minutes, unless the traffic in them is shorter than
// that: then the axis starts a little before the first request, so a handful of
// requests spreads across the plot instead of gathering in its last corner.
func (a *app) activityAxis(end time.Time) (time.Time, int) {
	start := end.Add(-activitySpan)
	earliest := time.Time{}
	for _, at := range a.activityRows {
		t := a.flows[at].at
		if !t.Before(end) {
			continue
		}
		if earliest.IsZero() || t.Before(earliest) {
			earliest = t
		}
	}
	if earliest.IsZero() {
		return start, activityBuckets
	}
	// A bucket of air before the first request, and never less than the
	// shortest axis.
	used := int(math.Ceil(float64(end.Sub(earliest))/float64(activityStep))) + 1
	used = min(activityBuckets, max(activityMinBuckets, used))
	return end.Add(-time.Duration(used) * activityStep), used
}

// activityCache is the strip's counts for the rows, end and brush it was
// counted for.
type activityCache struct {
	valid           bool
	end             time.Time
	start           time.Time
	used            int
	selection       uikit.ActivityRange
	buckets         [activityBuckets]uikit.ActivityBucket
	total, selected int
}

func (a *app) activityStrip(c *ui.Context) {
	end := a.activityEnd(time.Now())
	start, used := a.activityAxis(end)
	cache := &a.activityCounts
	if !cache.valid || !cache.end.Equal(end) || cache.selection != a.activity.selection {
		*cache = activityCache{valid: true, end: end, start: start, used: used, selection: a.activity.selection}
		cache.buckets, cache.total, cache.selected = a.countActivity(start, end)
	}
	buckets, total, selected := cache.buckets, cache.total, cache.selected
	a.drawActivity(c, end, start, used, buckets, total, selected)
}

func (a *app) countActivity(start, end time.Time) (buckets [activityBuckets]uikit.ActivityBucket, total, selected int) {
	for _, at := range a.activityRows {
		f := a.flows[at]
		if f.at.Before(start) || !f.at.Before(end) {
			continue
		}
		i := int(f.at.Sub(start) / activityStep)
		status := 0
		switch {
		case f.conn.error != "", f.status >= 500:
			status = 3
		case f.status >= 400:
			status = 2
		case f.status >= 300 && f.status != 304:
			status = 1
		case f.millis >= slowMillis:
			// A slow exchange is a signal color of its own, above the
			// neutral part it counts in.
			status = 4
		}
		buckets[i][status]++
		total++
		if i >= a.activity.selection.From && i < a.activity.selection.To {
			selected++
		}
	}
	return buckets, total, selected
}

func (a *app) drawActivity(c *ui.Context, end, start time.Time, used int, buckets [activityBuckets]uikit.ActivityBucket, total, selected int) {
	key := 0
	if view := a.views.activeView(); view != nil {
		key = view.id
	}
	// The strip is the plot and the time it ends at: the count is the
	// sidebar's, and a chosen range is a chip in the search field.
	uikit.ActivityStrip(c.Key(key), buckets[:used], &a.activity.selection, end.Format("15:04:05"), func() {
		if a.activity.active() {
			if a.activity.start.IsZero() {
				a.activity.start = start
			}
			a.activity.end = end
		} else {
			a.activity = activityRange{}
		}
		a.views.capture(a)
		a.applyFilter()
	}).Disabled(total == 0 && !a.activity.active())
}
