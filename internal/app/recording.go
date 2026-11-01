package app

import (
	"slices"
	"time"

	"github.com/baba983/vscroxy/internal/capture"
)

// rateWindow counts the recorded flows of each second in the last minute,
// the recording pill's curve. The newest second is the last bucket.
type rateWindow struct {
	end    int64 // the Unix second of the last bucket
	counts [60]int
}

// advance moves the window to now, emptying the seconds that passed.
func (w *rateWindow) advance(now int64) {
	shift := int(min(now-w.end, int64(len(w.counts))))
	if shift <= 0 {
		return
	}
	copy(w.counts[:], w.counts[shift:])
	clear(w.counts[len(w.counts)-shift:])
	w.end = now
}

func (w *rateWindow) add(now int64, n int) {
	w.advance(now)
	w.counts[len(w.counts)-1] += n
}

// perSecond is the mean of the last ten whole seconds: steadier than the
// second still filling, and still current.
func (w *rateWindow) perSecond() float64 {
	sum := 0
	for _, n := range w.counts[len(w.counts)-11 : len(w.counts)-1] {
		sum += n
	}
	return float64(sum) / 10
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
	if len(pending) > 0 {
		a.rate.add(time.Now().Unix(), len(pending))
	}
	return pending
}

// tickRate redraws once a second while the pill or five-minute activity
// overview has something in it to move, until capture stops.
func (a *app) tickRate(done <-chan struct{}) {
	tick := time.NewTicker(time.Second)
	defer tick.Stop()
	for {
		select {
		case <-done:
			return
		case now := <-tick.C:
			if now.Unix()-a.lastFlow.Load() <= int64(activitySpan/time.Second)+1 {
				a.win.Invalidate()
			}
		}
	}
}
