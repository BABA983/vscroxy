package app

import (
	"fmt"
	"time"

	"github.com/baba983/vscroxy/internal/uikit"
	"github.com/egoist/mygo/ui"
)

const activitySpan = 5 * time.Minute
const activityStep = 5 * time.Second
const activityBuckets = int(activitySpan / activityStep)

// activityRange belongs to a capture view, not a saved filter. The overview's
// end is frozen while brushing; new traffic cannot move the selected times.
type activityRange struct {
	end       time.Time
	selection uikit.ActivityRange
}

func (r activityRange) active() bool { return r.selection.To > r.selection.From }

func (r activityRange) bounds() (time.Time, time.Time) {
	start := r.end.Add(-activitySpan)
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

// activityCache is the strip's counts for the rows, end and brush it was
// counted for.
type activityCache struct {
	valid           bool
	end             time.Time
	selection       uikit.ActivityRange
	buckets         [activityBuckets]uikit.ActivityBucket
	total, selected int
}

func (a *app) activityStrip(c *ui.Context) {
	end := a.activityEnd(time.Now())
	start := end.Add(-activitySpan)
	cache := &a.activityCounts
	if !cache.valid || !cache.end.Equal(end) || cache.selection != a.activity.selection {
		*cache = activityCache{valid: true, end: end, selection: a.activity.selection}
		cache.buckets, cache.total, cache.selected = a.countActivity(start, end)
	}
	buckets, total, selected := cache.buckets, cache.total, cache.selected
	a.drawActivity(c, end, start, buckets, total, selected)
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
		}
		buckets[i][status]++
		total++
		if i >= a.activity.selection.From && i < a.activity.selection.To {
			selected++
		}
	}
	return buckets, total, selected
}

func (a *app) drawActivity(c *ui.Context, end, start time.Time, buckets [activityBuckets]uikit.ActivityBucket, total, selected int) {
	summary := fmt.Sprintf("Last 5 min · %d %s", total, plural(total, "request", "requests"))
	if a.activity.active() {
		summary = fmt.Sprintf("%s · %d %s", a.activity.label(), selected, plural(selected, "request", "requests"))
	}
	ticks := [3]string{start.Format("15:04:05"), start.Add(activitySpan / 2).Format("15:04:05"), end.Format("15:04:05")}
	key := 0
	if view := a.views.activeView(); view != nil {
		key = view.id
	}
	uikit.ActivityStrip(c.Key(key), buckets[:], &a.activity.selection, summary, ticks, func() {
		if a.activity.active() {
			a.activity.end = end
		} else {
			a.activity = activityRange{}
		}
		a.views.capture(a)
		a.applyFilter()
	}).Disabled(total == 0 && !a.activity.active())
}
