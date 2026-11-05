package app

import (
	"context"
	"fmt"
	"slices"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/baba983/vscroxy/internal/uikit"
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
	total       int
	started     time.Time
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
	r.done, r.failed, r.problem, r.open = 0, 0, "", true
	r.total, r.elapsed, r.started, r.finished, r.stopped = 0, 0, time.Time{}, false, false
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
	if !intervalOK {
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
	r.done, r.failed, r.problem = 0, 0, ""
	r.started, r.elapsed, r.stopped, r.finished = time.Now(), 0, false, false
	r.statuses, r.latencySum, r.latencyN = map[int]int{}, 0, 0
	updates := make(chan replayOutcome, r.total)
	r.updates = updates
	go a.runReplay(ctx, flows, settings, seconds, updates)
}

// runReplay dispatches in capture order and repeats. Workers overlap sends;
// interval spaces successive starts, and cancellation stops new work while
// in-flight requests receive the same context they would on their own.
func (a *app) runReplay(ctx context.Context, flows []flow, settings replaySettings, seconds int, updates chan<- replayOutcome) {
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
				reply, err := a.replayFlow(requestCtx, f, false)
				finish()
				updates <- replayOutcome{err: err, status: reply.Status, millis: reply.Millis, canceled: ctx.Err() != nil}
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
	close(updates)
}

func (r *replaySetup) summary() string {
	parts := []string{
		fmt.Sprintf("%d/%d sent", r.done, r.total),
		fmt.Sprintf("%d succeeded", r.done-r.failed),
		fmt.Sprintf("%d failed", r.failed),
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
				if !r.started.IsZero() {
					r.elapsed = time.Since(r.started)
				}
				state := "finished"
				if r.stopped {
					state = "stopped"
				}
				a.inform("Replay " + state + " · " + r.summary())
				break
			}
			if outcome.canceled {
				continue
			}
			r.done++
			if outcome.err != nil {
				r.failed++
				r.problem = outcome.err.Error()
				continue
			}
			if outcome.status != 0 {
				if r.statuses == nil {
					r.statuses = map[int]int{}
				}
				r.statuses[outcome.status]++
			}
			r.latencySum += int64(outcome.millis)
			r.latencyN++
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
	valid := timeoutValid && inputProblem == ""
	total := len(r.flows) * settings.repeat
	if r.total > 0 {
		total = r.total
	}
	uikit.Modal(c, &r.open, func() {
		uikit.DialogColumn(c, uikit.DialogMedium).Children(func() {
			uikit.DialogTitle(c, fmt.Sprintf("Replay %d requests", len(r.flows)))
			ui.Scroll(c).MinHeight(0).Shrink(1).Gap(t.Space(4)).Children(func() {
				uikit.Hint(c, "Sends in capture order through the current rules. POST, PUT, PATCH and DELETE can change server data. WebSocket, SSE and tunnels are excluded.")
				ui.Field(c, "Timeout per request (seconds; 0 = no timeout)", func() {
					uikit.Input(c, &r.timeout).Label("Replay timeout").Disabled(r.updates != nil)
				})
				if !timeoutValid {
					ui.Text(c, "Enter 0–86400 seconds.").FontSize(uikit.TextBody).TextColor(t.Danger)
				}
				ui.Field(c, "Repeat each request", func() {
					uikit.Input(c, &r.repeat).Label("Replay repeats").Disabled(r.updates != nil)
				}).Description(fmt.Sprintf("1–%d times. The whole selection is repeated in capture order.", replayMaxRepeat))
				ui.Row(c).Gap(t.Space(3)).Children(func() {
					ui.Field(c, "Concurrency", func() {
						uikit.Input(c, &r.concurrency).Label("Replay concurrency").Disabled(r.updates != nil)
					}).Description(fmt.Sprintf("1–%d requests in flight.", replayMaxConcurrency)).Grow(1)
					ui.Field(c, "Interval between starts (ms)", func() {
						uikit.Input(c, &r.interval).Label("Replay interval").Disabled(r.updates != nil)
					}).Description(fmt.Sprintf("0–%d ms; 0 starts as a worker frees up.", replayMaxIntervalMS)).Grow(1)
				})
				if inputProblem != "" {
					ui.Text(c, inputProblem).FontSize(uikit.TextBody).TextColor(t.Danger)
				} else {
					uikit.Meta(c, fmt.Sprintf("Total: %d %s.", total, plural(total, "send", "sends")))
				}
				if a.srv != nil && a.srv.NoCaching() {
					ui.Text(c, "No Caching is on: conditional cache headers will be removed.").FontSize(uikit.TextBody).TextColor(t.Accent)
				}
				if r.updates != nil || r.finished {
					fraction := 0.0
					if r.total > 0 {
						fraction = float64(r.done) / float64(r.total)
					}
					ui.Progress(c, fraction).Label("Replay progress")
					if r.updates != nil {
						uikit.Body(c, fmt.Sprintf("%d/%d sent · %d failed", r.done, r.total, r.failed))
						if r.stopped {
							uikit.Hint(c, "Stopping after in-flight requests finish.")
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
				if r.cancel != nil && uikit.SecondaryButton(c, "Stop replay").Clicked() {
					r.stopped = true
					r.cancel()
				}
				if uikit.SecondaryButton(c, "Close").Clicked() {
					r.open = false
				}
				if uikit.PrimaryButton(c, "Send replays").Disabled(!valid || len(r.flows) == 0 || r.updates != nil || r.finished || r.done > 0 || a.srv == nil).Clicked() {
					a.startReplay(seconds)
				}
			})
		})
	})
}
