package app

import (
	"slices"
	"time"

	"github.com/baba983/vscroxy/internal/async"
	"github.com/baba983/vscroxy/internal/capture"
)

// timerClock is the clock used by the app's timers. A nil clock means real time.
func (a *app) timerClock() async.Clock {
	if a.clock != nil {
		return a.clock
	}
	return async.RealClock()
}

// record keeps what arrived unless recording is paused. A paused proxy still
// forwards everything; it only stops keeping ambient traffic. What the app
// sent itself (editor sends, replays) is kept, as the reader asked for it.
func (a *app) record(pending []capture.Flow) []capture.Flow {
	if a.recordingPaused {
		pending = slices.DeleteFunc(pending, func(f capture.Flow) bool {
			if f.DebugRun != "" || f.ReplayOf != 0 {
				return false
			}
			if a.unrecorded == nil {
				a.unrecorded = map[int]bool{}
			}
			a.unrecorded[f.ID] = true
			return true
		})
	}
	return pending
}

// tickActivity redraws the activity strip as its five-second axis moves. The
// strip is the only thing on the capture screen that follows the clock without
// traffic, and it steps once per activity bucket: asking for a frame every
// second built the whole capture view four times to show one step.
func (a *app) tickActivity(done <-chan struct{}) {
	clock := a.timerClock()
	var drawn time.Time
	tick := async.NewIntervalTimer(func() error {
		now := clock.Now()
		if now.Unix()-a.lastFlow.Load() > int64(activitySpan/time.Second)+1 {
			return nil
		}
		if bucket := now.Truncate(activityStep); !bucket.Equal(drawn) {
			drawn = bucket
			if a.win != nil {
				a.win.Invalidate()
			}
		}
		return nil
	}, time.Second, async.WithClock(clock))
	defer tick.Dispose()
	<-done
}
