package app

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/baba983/vscroxy/internal/uikit"
	"github.com/baba983/vscroxy/locales"
	"github.com/egoist/mygo/ui"
)

// Advanced replay is deliberately bounded: a mistaken repeat count or a
// whole-session selection must not send thousands of requests.
const (
	replayMaxSends       = 500
	replayMaxRepeat      = 100
	replayMaxConcurrency = 8
	replayMaxIntervalMS  = 60_000
)

type replaySettings struct {
	repeat      int
	concurrency int
	interval    time.Duration
}

// problem says why these settings cannot be sent. It is pure, so the limits
// are tested without a window, a listener or a real request.
func (s replaySettings) problem(flows int) string {
	switch {
	case flows <= 0:
		return "Select at least one HTTP request."
	case s.repeat < 1 || s.repeat > replayMaxRepeat:
		return fmt.Sprintf("Enter 1–%d repeats.", replayMaxRepeat)
	case s.concurrency < 1 || s.concurrency > replayMaxConcurrency:
		return fmt.Sprintf("Enter 1–%d concurrent requests.", replayMaxConcurrency)
	case s.interval < 0 || s.interval > replayMaxIntervalMS*time.Millisecond:
		return fmt.Sprintf("Enter 0–%d ms between starts.", replayMaxIntervalMS)
	case flows*s.repeat > replayMaxSends:
		return fmt.Sprintf("This would send %d requests; the batch limit is %d. Lower the repeat count or select fewer requests.", flows*s.repeat, replayMaxSends)
	}
	return ""
}

type replayOutcome struct {
	err      error
	status   int
	millis   int
	canceled bool
	final    bool
	elapsed  time.Duration
}

type replaySetup struct {
	open        bool
	flows       []flow
	timeout     string
	repeat      string
	concurrency string
	interval    string
	done        int
	failed      int
	canceled    int
	total       int
	elapsed     time.Duration
	statuses    map[int]int
	latencySum  int64
	latencyN    int
	stopped     bool
	finished    bool
	problem     string
	cancel      context.CancelFunc
	updates     chan replayOutcome
}

func (a *app) openReplay(flows []flow) {
	r := &a.replayUI
	if r.updates != nil {
		r.open = true
		return
	}
	r.flows = slices.DeleteFunc(slices.Clone(flows), func(f flow) bool { return f.url == "" || f.kind != "" })
	if r.timeout == "" {
		r.timeout = "30"
	}
	if r.repeat == "" {
		r.repeat = "1"
	}
	if r.concurrency == "" {
		r.concurrency = "1"
	}
	if r.interval == "" {
		r.interval = "0"
	}
	r.done, r.failed, r.canceled, r.problem, r.open = 0, 0, 0, "", true
	r.total, r.elapsed, r.finished, r.stopped = 0, 0, false, false
	r.statuses, r.latencySum, r.latencyN = nil, 0, 0
}

func (r *replaySetup) settings() (replaySettings, string) {
	number := func(text string) (int, bool) {
		value, err := strconv.Atoi(strings.TrimSpace(text))
		return value, err == nil
	}
	repeat, repeatOK := number(r.repeat)
	concurrency, concurrencyOK := number(r.concurrency)
	interval, intervalOK := number(r.interval)
	if !repeatOK {
		repeat = 0
	}
	if !concurrencyOK {
		concurrency = 0
	}
	// Validate the integer before converting to a Duration: a huge value must
	// not wrap around into a valid-looking interval.
	if !intervalOK || interval < 0 || interval > replayMaxIntervalMS {
		interval = -1
	}
	s := replaySettings{repeat: repeat, concurrency: concurrency, interval: time.Duration(interval) * time.Millisecond}
	return s, s.problem(len(r.flows))
}

func (a *app) startReplay(seconds int) {
	r := &a.replayUI
	settings, problem := r.settings()
	if problem != "" {
		r.problem = problem
		return
	}
	ctx, cancel := context.WithCancel(context.Background())
	r.cancel = cancel
	flows := slices.Clone(r.flows)
	r.total = len(flows) * settings.repeat
	r.done, r.failed, r.canceled, r.problem = 0, 0, 0, ""
	r.elapsed, r.stopped, r.finished = 0, false, false
	r.statuses, r.latencySum, r.latencyN = map[int]int{}, 0, 0
	// One extra slot carries the final elapsed time, so it is measured when
	// the last send finishes rather than when the UI next drains the channel.
	updates := make(chan replayOutcome, r.total+1)
	r.updates = updates
	go a.runReplay(ctx, flows, settings, seconds, updates)
}

// runReplay dispatches in capture order and repeats. Workers overlap sends;
// interval spaces successive starts. A one-request, one-repeat batch records
// its result like a single Replay. Cancellation stops new work and cancels
// in-flight requests; only errors.Is(context.Canceled) is counted as canceled.
func (a *app) runReplay(ctx context.Context, flows []flow, settings replaySettings, seconds int, updates chan<- replayOutcome) {
	started := time.Now()
	record := settings.repeat == 1 && len(flows) == 1
	jobs := make(chan flow)
	var workers sync.WaitGroup
	for range settings.concurrency {
		workers.Add(1)
		go func() {
			defer workers.Done()
			for f := range jobs {
				if ctx.Err() != nil {
					continue
				}
				var requestCtx context.Context
				var finish context.CancelFunc
				if seconds > 0 {
					requestCtx, finish = context.WithTimeout(ctx, time.Duration(seconds)*time.Second)
				} else {
					requestCtx, finish = context.WithCancel(ctx)
				}
				reply, err := a.replayFlow(requestCtx, f, record)
				finish()
				updates <- replayOutcome{
					err:      err,
					status:   reply.Status,
					millis:   reply.Millis,
					canceled: errors.Is(err, context.Canceled),
				}
				a.invalidate()
			}
		}()
	}
	next := time.Now()
scheduler:
	for range settings.repeat {
		for _, f := range flows {
			if ctx.Err() != nil {
				break scheduler
			}
			if wait := time.Until(next); wait > 0 {
				timer := time.NewTimer(wait)
				select {
				case <-timer.C:
				case <-ctx.Done():
					timer.Stop()
					break scheduler
				}
			}
			select {
			case jobs <- f:
				next = time.Now().Add(settings.interval)
			case <-ctx.Done():
				break scheduler
			}
		}
	}
	close(jobs)
	workers.Wait()
	updates <- replayOutcome{final: true, elapsed: time.Since(started)}
	a.invalidate()
	close(updates)
	a.invalidate()
}

func (r *replaySetup) summary() string {
	succeeded := r.done - r.failed - r.canceled
	parts := []string{
		fmt.Sprintf("%d/%d started", r.done, r.total),
		fmt.Sprintf("%d succeeded", succeeded),
		fmt.Sprintf("%d failed", r.failed),
	}
	if r.canceled > 0 {
		parts = append(parts, fmt.Sprintf("%d canceled", r.canceled))
	}
	if len(r.statuses) > 0 {
		codes := make([]int, 0, len(r.statuses))
		for code := range r.statuses {
			codes = append(codes, code)
		}
		slices.Sort(codes)
		distribution := make([]string, 0, len(codes))
		for _, code := range codes {
			distribution = append(distribution, fmt.Sprintf("%d × %d", code, r.statuses[code]))
		}
		parts = append(parts, strings.Join(distribution, ", "))
	}
	if r.elapsed > 0 {
		parts = append(parts, fmt.Sprintf("%.1f s total", r.elapsed.Seconds()))
	}
	if r.latencyN > 0 {
		parts = append(parts, fmt.Sprintf("%d ms average", r.latencySum/int64(r.latencyN)))
	}
	return strings.Join(parts, " · ")
}

func (a *app) replayModal(c *ui.Context, t *ui.Theme) {
	r := &a.replayUI
	for r.updates != nil {
		select {
		case outcome, ok := <-r.updates:
			if !ok {
				r.updates, r.cancel = nil, nil
				r.finished = true
				state := "finished"
				if r.stopped {
					state = "stopped"
				}
				a.inform(locales.ReplayState(state, r.summary()))
				break
			}
			if outcome.final {
				r.elapsed = outcome.elapsed
				continue
			}
			r.done++
			switch {
			case outcome.canceled:
				r.canceled++
			case outcome.err != nil:
				r.failed++
				r.problem = outcome.err.Error()
			default:
				if outcome.status != 0 {
					if r.statuses == nil {
						r.statuses = map[int]int{}
					}
					r.statuses[outcome.status]++
				}
				r.latencySum += int64(outcome.millis)
				r.latencyN++
			}
		default:
			goto drained
		}
	}
drained:
	if r.updates != nil {
		c.After(100 * time.Millisecond)
	}
	if !r.open {
		return
	}
	settings, inputProblem := r.settings()
	seconds, err := strconv.Atoi(strings.TrimSpace(r.timeout))
	timeoutValid := err == nil && seconds >= 0 && seconds <= 86400
	timeoutProblem := ""
	if !timeoutValid {
		timeoutProblem = "Enter 0–86400 seconds."
	}
	valid := timeoutValid && inputProblem == ""
	total := len(r.flows) * settings.repeat
	if r.total > 0 {
		total = r.total
	}
	uikit.Modal(c, &r.open, func() {
		uikit.DialogColumn(c, uikit.DialogMedium).Children(func() {
			uikit.DialogTitle(c, locales.ReplayRequests(len(r.flows)))
			// Leave room for the input's focus halo: a scroll clips what
			// reaches past its content edge.
			ui.Scroll(c).MinHeight(0).Shrink(1).Gap(t.Space(4)).Padding(0, t.Space(1)).Children(func() {
				uikit.Hint(c, locales.SendsInCaptureOrderThrough())
				ui.Field(c, locales.TimeoutPerRequestSeconds0(), func() {
					uikit.Input(c, &r.timeout).Label(locales.ReplayTimeout()).Disabled(r.updates != nil)
				}).Error(timeoutProblem)
				ui.Field(c, locales.RepeatEachRequest(), func() {
					uikit.Input(c, &r.repeat).Label(locales.ReplayRepeats()).Disabled(r.updates != nil)
				}).Description(locales.RepeatDescription(replayMaxRepeat))
				ui.Row(c).Gap(t.Space(3)).Children(func() {
					ui.Field(c, locales.Concurrency(), func() {
						uikit.Input(c, &r.concurrency).Label(locales.ReplayConcurrency()).Disabled(r.updates != nil)
					}).Description(locales.ConcurrencyDescription(replayMaxConcurrency)).Grow(1)
					ui.Field(c, locales.IntervalBetweenStartsMs(), func() {
						uikit.Input(c, &r.interval).Label(locales.ReplayInterval()).Disabled(r.updates != nil)
					}).Description(locales.IntervalDescription(replayMaxIntervalMS)).Grow(1)
				})
				if inputProblem != "" {
					ui.Text(c, inputProblem).FontSize(uikit.TextBody).TextColor(t.Danger)
				} else {
					uikit.Meta(c, locales.TotalSends(total))
				}
				if a.srv != nil && a.srv.NoCaching() {
					ui.Text(c, locales.NoCachingIsOnConditional()).FontSize(uikit.TextBody).TextColor(t.Accent)
				}
				if r.updates != nil || r.finished {
					fraction := 0.0
					if r.total > 0 {
						fraction = float64(r.done) / float64(r.total)
					}
					ui.Progress(c, fraction).Label(locales.ReplayProgress())
					if r.updates != nil {
						line := fmt.Sprintf("%d/%d started · %d failed", r.done, r.total, r.failed)
						if r.canceled > 0 {
							line += fmt.Sprintf(" · %d canceled", r.canceled)
						}
						uikit.Body(c, line)
						if r.stopped {
							uikit.Hint(c, locales.StoppingNoNewSendsIn())
						}
					}
				}
				if r.finished {
					uikit.Body(c, r.summary())
				}
				if r.problem != "" {
					ui.Text(c, r.problem).FontSize(uikit.TextBody).TextColor(t.Danger).MaxLines(3).Tooltip(r.problem)
				}
			})
			uikit.DialogFooter(c).Children(func() {
				if r.cancel != nil && uikit.SecondaryButton(c, locales.StopReplay()).Clicked() {
					r.stopped = true
					r.cancel()
				}
				if uikit.SecondaryButton(c, locales.Close()).Clicked() {
					r.open = false
				}
				if uikit.PrimaryButton(c, locales.SendReplays()).Disabled(!valid || len(r.flows) == 0 || r.updates != nil || r.finished || r.done > 0 || a.srv == nil).Clicked() {
					a.startReplay(seconds)
				}
			})
		})
	})
}
