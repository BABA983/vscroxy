package main

import (
	"cmp"
	"context"
	"fmt"
	"maps"
	"net/http"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/baba983/vscroxy/desktop/internal/capture"
	"github.com/egoist/mygo/ui"
)

type requestExecution struct {
	request  *jsRequest
	response *capture.Flow
	replies  []string
	tests    []testResult
	vars     map[string]string
	err      error
}

// A single execution is also a runner step. Both use the same encoder,
// scripts, proxy and session cookie jar; no request tab is borrowed by Runner.
func executeRequest(ctx context.Context, srv *capture.Server, r savedRequest, vars map[string]string, jar http.CookieJar) requestExecution {
	run := &scriptRun{vars: maps.Clone(vars)}
	out := requestExecution{vars: run.vars}
	req, err := prepareRequest(r, vars)
	if err != nil {
		out.err = err
		return out
	}
	out.request = req
	if err := runScriptContext(ctx, r.Pre, run, map[string]any{"req": req}); err != nil {
		out.err = fmt.Errorf("Pre-request script: %w", err)
		out.tests, out.vars = run.tests, run.vars
		return out
	}
	if isWebSocket(req.URL) {
		out.replies, out.err = wsSend(ctx, srv.Addr(), req, srv.CACertificate(), jar, 2*time.Second)
		if r.Post != "" && out.err == nil {
			out.err = fmt.Errorf("post-response scripts require an HTTP response")
		}
	} else {
		got, err := srv.ReplayWithCookies(ctx, capture.Flow{Method: req.Method, URL: req.URL, RequestHeader: req.header, RequestBody: req.Body}, jar)
		out.err = err
		if err == nil {
			out.response = &got
			res := &jsResponse{Status: got.Status, Body: got.ResponseBody, header: got.ResponseHeader}
			if err := runScriptContext(ctx, r.Post, run, map[string]any{"res": res, "req": req}); err != nil {
				out.err = fmt.Errorf("Post-response script: %w", err)
			}
		}
	}
	out.tests, out.vars = run.tests, run.vars
	return out
}

func (r requestExecution) summary() string {
	if r.err != nil {
		return r.err.Error()
	}
	if r.response == nil {
		return fmt.Sprintf("WebSocket: %d replies", len(r.replies))
	}
	passed := 0
	for _, t := range r.tests {
		if t.Pass {
			passed++
		}
	}
	return fmt.Sprintf("%d · %s · %d ms · %d/%d tests", r.response.Status, humanBytes(r.response.Bytes), r.response.Millis, passed, len(r.tests))
}

func (r requestExecution) failed() bool {
	if r.err != nil || r.response != nil && r.response.Status >= 400 {
		return true
	}
	return slices.ContainsFunc(r.tests, func(t testResult) bool { return !t.Pass })
}

type runnerStep struct {
	request savedRequest
	enabled bool
}
type runnerRow struct {
	name      string
	iteration int
	summary   string
	failed    bool
	tests     []testResult
}
type runnerEvent struct {
	row          runnerRow
	before, vars map[string]string
}
type collectionRunner struct {
	open, running, cancelled                    bool
	collection, iterations, environment, status string
	steps                                       []runnerStep
	rows                                        []runnerRow
	picked, completed, failures                 int
	results                                     ui.ListState
	stopOnFailure                               bool
	cancel                                      context.CancelFunc
	events                                      chan runnerEvent
}

func (a *app) openRunner() {
	r := &a.runner
	r.open = true
	if r.running {
		return
	}
	r.iterations, r.stopOnFailure = cmp.Or(r.iterations, "1"), true
	if a.requestDocument != nil {
		r.collection = a.composeCollection
	}
	r.load(a.requests)
}

func (r *collectionRunner) load(requests []savedRequest) {
	r.steps = nil
	for _, req := range requests {
		if req.Collection == r.collection || r.collection != "" && strings.HasPrefix(req.Collection, r.collection+"/") {
			req.Form = slices.Clone(req.Form)
			r.steps = append(r.steps, runnerStep{request: req, enabled: true})
		}
	}
	slices.SortFunc(r.steps, func(a, b runnerStep) int {
		return strings.Compare(a.request.Collection+"/"+a.request.Name, b.request.Collection+"/"+b.request.Name)
	})
}

func (a *app) startRunner() {
	r := &a.runner
	if r.running {
		return
	}
	n, err := strconv.Atoi(r.iterations)
	if err != nil || n < 1 || n > 1000 {
		r.status = "Iterations must be between 1 and 1000"
		return
	}
	if a.srv == nil {
		r.status = "Start the local proxy first"
		return
	}
	var requests []savedRequest
	for _, step := range r.steps {
		if step.enabled {
			requests = append(requests, step.request)
		}
	}
	if len(requests) == 0 {
		r.status = "Choose at least one saved request"
		return
	}
	ctx, cancel := context.WithCancel(context.Background())
	r.cancel, r.running, r.cancelled = cancel, true, false
	r.rows, r.completed, r.failures, r.picked = nil, 0, 0, 0
	r.environment, r.status = a.activeEnv, "Running…"
	events := make(chan runnerEvent, 16)
	r.events = events
	vars, stop, srv := a.activeVars(), r.stopOnFailure, a.srv
	jar := a.requestCookies()
	go func() {
		defer close(events)
		defer cancel()
		defer a.invalidate()
		for iteration := 1; iteration <= n; iteration++ {
			for _, req := range requests {
				if ctx.Err() != nil {
					return
				}
				before := maps.Clone(vars)
				stepCtx, stepCancel := context.WithTimeout(ctx, 30*time.Second)
				got := executeRequest(stepCtx, srv, req, vars, jar)
				stepCancel()
				vars = got.vars
				event := runnerEvent{row: runnerRow{name: req.Name, iteration: iteration, summary: got.summary(), failed: got.failed(), tests: got.tests}, before: before, vars: maps.Clone(vars)}
				select {
				case events <- event:
					a.invalidate()
				case <-ctx.Done():
					return
				}
				if stop && got.failed() {
					return
				}
			}
		}
	}()
}

func (a *app) drainRunner() {
	r := &a.runner
	for r.events != nil {
		select {
		case event, ok := <-r.events:
			if !ok {
				r.events, r.cancel, r.running = nil, nil, false
				r.status = "Finished"
				if r.cancelled {
					r.status = "Cancelled"
				}
				return
			}
			r.completed++
			if event.row.failed {
				r.failures++
			}
			r.rows = append(r.rows, event.row)
			if len(r.rows) > 1000 {
				r.rows = slices.Delete(r.rows, 0, 1)
				r.picked = max(0, r.picked-1)
			}
			if !maps.Equal(event.before, event.vars) {
				a.applyVars(r.environment, event.before, event.vars)
			}
		default:
			return
		}
	}
}

func (a *app) runnerModal(c *ui.Context, t *ui.Theme) {
	r := &a.runner
	if r.running {
		c.After(100 * time.Millisecond)
	}
	if !r.open {
		return
	}
	ui.Modal(c, &r.open, func() {
		ui.Column(c).Width(820).Height(510).Gap(12).Children(func() {
			ui.Text(c, "Collection Runner").FontSize(18).Bold()
			ui.Text(c, "Saved requests only · sequential, including subfolders · environment snapshot at start · shared session cookies · 30 s per request").FontSize(11).TextColor(t.TextMuted)
			ui.Row(c).Gap(10).AlignItems(ui.Center).Children(func() {
				names := slices.Sorted(maps.Keys(groupRequests(a.requests)))
				if len(names) == 0 {
					names = []string{""}
				}
				if ui.Select(c, &r.collection, names).Label("Runner collection").Grow(1).Disabled(r.running).Changed() {
					r.load(a.requests)
				}
				ui.TextInput(c, &r.iterations).Width(86).Label("Iterations").Disabled(r.running)
				ui.Checkbox(c, &r.stopOnFailure, "Stop on failure").Disabled(r.running)
			})
			ui.Row(c).Grow(1).Gap(16).AlignItems(ui.Stretch).Children(func() {
				ui.Column(c).Width(310).Gap(8).Children(func() {
					ui.Text(c, "EXECUTION ORDER").FontSize(10).Bold().TextColor(t.TextMuted)
					move, delta := -1, 0
					ui.Scroll(c).Grow(1).Gap(6).Children(func() {
						for i := range r.steps {
							step := &r.steps[i]
							ui.Row(c).Key(i).Gap(6).AlignItems(ui.Center).Children(func() {
								ui.Checkbox(c, &step.enabled, step.request.Name).Label(fmt.Sprintf("Run request %d", i+1)).Grow(1).Disabled(r.running)
								if ui.Button(c, "↑").Label(fmt.Sprintf("Move request %d up", i+1)).Disabled(r.running || i == 0).Clicked() {
									move, delta = i, -1
								}
								if ui.Button(c, "↓").Label(fmt.Sprintf("Move request %d down", i+1)).Disabled(r.running || i == len(r.steps)-1).Clicked() {
									move, delta = i, 1
								}
							})
						}
					})
					if move >= 0 {
						r.steps[move], r.steps[move+delta] = r.steps[move+delta], r.steps[move]
					}
				})
				ui.Column(c).Grow(1).Gap(8).Children(func() {
					ui.Text(c, fmt.Sprintf("RESULTS · %d completed · %d failed", r.completed, r.failures)).FontSize(10).Bold().TextColor(t.TextMuted)
					r.results.Selected = &r.picked
					ui.List(c, &r.results, len(r.rows), func(i int) {
						row := r.rows[i]
						color := t.Success
						if row.failed {
							color = t.Danger
						}
						ui.Column(c).Padding(6).Gap(3).Children(func() {
							ui.Text(c, fmt.Sprintf("%d · %s", row.iteration, row.name)).FontSize(12).Bold()
							ui.Text(c, row.summary).FontSize(11).TextColor(color).SingleLine()
						})
					}).Grow(1)
					ui.Scroll(c).Height(85).Gap(3).Children(func() {
						if r.picked >= 0 && r.picked < len(r.rows) {
							for _, test := range r.rows[r.picked].tests {
								mark, color := "✓", t.Success
								if !test.Pass {
									mark, color = "✗", t.Danger
								}
								ui.Text(c, mark+" "+test.Name).FontSize(11).TextColor(color)
							}
						}
					})
				})
			})
			ui.Text(c, r.status+" · displaying latest 1000 results; closing keeps a run active").FontSize(11).TextColor(t.TextMuted)
			ui.Row(c).Gap(8).Justify(ui.End).Children(func() {
				if ui.Button(c, "Close").Clicked() {
					r.open = false
				}
				if ui.Button(c, "Cancel run").Disabled(!r.running).Clicked() {
					r.cancelled = true
					r.cancel()
					r.status = "Stopping…"
				}
				if ui.PrimaryButton(c, "Run collection").Disabled(r.running || len(r.steps) == 0).Clicked() {
					a.startRunner()
				}
			})
		})
	})
}
