package app

import (
	"time"

	"github.com/baba983/vscroxy/internal/uikit"
	"github.com/egoist/mygo/ui"
)

const activitySpan = 5 * time.Minute
const activityStep = 5 * time.Second
const activityBuckets = int(activitySpan / activityStep)

// activityRange belongs to a capture view, not a saved filter. It is a window
// in absolute time, chosen once from the buckets that were under the pointer:
// the rows it keeps and the band the strip draws are then the same window,
// whatever the axis does afterwards. Bucketing and filtering take their
// boundaries from here, never from two copies that can drift apart.
type activityRange struct {
	end    time.Time // the axis' end the window was drawn on
	from   time.Time
	to     time.Time
	frozen bool
}

func (r activityRange) active() bool { return r.to.After(r.from) }

func (r activityRange) bounds() (time.Time, time.Time) { return r.from, r.to }

func (r activityRange) label() string {
	return r.from.Format("15:04:05") + "–" + r.to.Format("15:04:05")
}

// band is the window as buckets of the axis starting at start, for the strip
// to draw. A window the axis has scrolled past clamps to an empty band.
func (r activityRange) band(start time.Time) uikit.ActivityRange {
	if !r.active() {
		return uikit.ActivityRange{}
	}
	from := int(r.from.Sub(start) / activityStep)
	to := int(r.to.Sub(start) / activityStep)
	return uikit.ActivityRange{From: max(0, min(activityBuckets, from)), To: max(0, min(activityBuckets, to))}
}

// activityEnd follows the live clock, or the latest timestamp of an imported
// or stopped capture. End is exclusive, so the current five-second bucket is
// included even when the newest request falls exactly on its boundary.
func (a *app) activityEnd(now time.Time) time.Time {
	if a.activity.active() && a.activity.frozen {
		return a.activity.end
	}
	latest := a.tally.latest
	if latest.IsZero() || a.tally.live > 0 && a.srv != nil && a.srv.Running() {
		latest = now
	}
	return latest.Truncate(activityStep).Add(activityStep)
}

// activityAxis is the span the strip draws: the last five minutes, whatever
// the traffic in them. An axis cut to the traffic moved every bar as each
// request arrived, so a bucket keeps its place now and new traffic walks in
// from the right.
func (a *app) activityAxis(end time.Time) (time.Time, int) {
	return end.Add(-activitySpan), activityBuckets
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
	band := a.activity.band(start)
	cache := &a.activityCounts
	if !cache.valid || !cache.end.Equal(end) || cache.selection != band {
		*cache = activityCache{valid: true, end: end, start: start, used: used, selection: band}
		cache.buckets, cache.total, cache.selected = a.countActivity(start, end, band)
	}
	buckets, total, selected := cache.buckets, cache.total, cache.selected
	a.drawActivity(c, end, start, used, band, buckets, total, selected)
}

func (a *app) countActivity(start, end time.Time, band uikit.ActivityRange) (buckets [activityBuckets]uikit.ActivityBucket, total, selected int) {
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
		if i >= band.From && i < band.To {
			selected++
		}
	}
	return buckets, total, selected
}

func (a *app) drawActivity(c *ui.Context, end, start time.Time, used int, band uikit.ActivityRange, buckets [activityBuckets]uikit.ActivityBucket, total, selected int) {
	key := 0
	if view := a.views.activeView(); view != nil {
		key = view.id
	}
	// The strip is the plot and the time it ends at: the count is the
	// sidebar's, and a chosen range is a chip in the search field. The gesture
	// works on this axis' buckets; the window it leaves is absolute time.
	selection := band
	uikit.ActivityStrip(c.Key(key), buckets[:used], &selection, end.Format("15:04:05"), func() {
		if selection.To > selection.From {
			a.activity = activityRange{
				end:    end,
				from:   start.Add(time.Duration(selection.From) * activityStep),
				to:     start.Add(time.Duration(selection.To) * activityStep),
				frozen: true,
			}
		} else {
			a.activity = activityRange{}
		}
		a.views.capture(a)
		a.applyFilter()
	}).Disabled(total == 0 && !a.activity.active())
}
