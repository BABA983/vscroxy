package app

import (
	"cmp"
	"context"
	"fmt"
	"maps"
	"slices"
	"strconv"
	"strings"
	"time"
	"uuid"

	"github.com/baba983/vscroxy/internal/request"
	"github.com/egoist/mygo/ui"
)

// runSummary is one line for a run: its error, or status, size, time and checks.
func runSummary(r request.Execution) string {
	if r.Err != nil {
		return r.Err.Error()
	}
	if r.Response == nil {
		return fmt.Sprintf("WebSocket: %d replies", len(r.Replies))
	}
	passed := 0
	for _, t := range r.Tests {
		if t.Pass {
			passed++
		}
	}
	return fmt.Sprintf("%d · %s · %d ms · %d/%d tests", r.Response.Status, humanBytes(r.Response.Bytes), r.Response.Millis, passed, len(r.Tests))
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
	tests     []request.TestResult
	console   []request.ConsoleMessage
	recordID  int
}
type runnerEvent struct {
	row          runnerRow
	before, vars map[string]string
	record       experimentRecord
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
	if a.srv == nil || !a.srv.Running() {
		r.status = "Start the local proxy first"
		return
	}
	var requests []savedRequest
	var contexts []request.Context
	for _, step := range r.steps {
		if step.enabled {
			requests = append(requests, step.request)
			contexts = append(contexts, a.sendContext(step.request.Request))
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
	vars, stop, srv, env := a.activeVars(), r.stopOnFailure, a.srv, r.environment
	jar := a.requestCookies()
	go func() {
		defer close(events)
		defer cancel()
		defer a.invalidate()
		for iteration := 1; iteration <= n; iteration++ {
			for i, req := range requests {
				if ctx.Err() != nil {
					return
				}
				before := maps.Clone(vars)
				stepCtx, stepCancel := context.WithTimeout(ctx, 30*time.Second)
				runKey := uuid.New().String()
				stepCtx = request.WithRunKey(stepCtx, runKey)
				got := req.Execute(stepCtx, srv, contexts[i], vars, jar)
				stepCancel()
				vars = got.Vars
				record := snapshotExperiment(0, req, env, got)
				record.RunKey = runKey
				event := runnerEvent{row: runnerRow{name: req.Name, iteration: iteration, summary: runSummary(got), failed: got.Failed(), tests: got.Tests, console: got.Console}, before: before, vars: maps.Clone(vars), record: record}
				select {
				case events <- event:
				default:
					// Prefer retaining an already completed/cancelled attempt. Only
					// abandon delivery on cancellation if the UI is not draining.
					select {
					case events <- event:
					case <-ctx.Done():
						return
					}
				}
				a.invalidate()
				if stop && got.Failed() {
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
			for _, f := range a.flows {
				if f.live {
					event.record.keepWire(f)
				}
			}
			if err := a.experiments.log.retain(event.record); err != nil {
				a.experiments.problem = "Runner record not retained: " + err.Error()
			} else {
				event.row.recordID = a.experiments.log.NextID
				a.experiments.persist()
			}
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
	appModal(c, &r.open, func() {
		ui.Column(c).Width(820).Height(510).Gap(12).Children(func() {
			dialogTitle(c, "Collection Runner")
			ui.Text(c, "Runs saved requests in order, 30 s per request").FontSize(11).TextColor(t.TextMuted)
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
					ui.Scroll(c).Height(125).Gap(3).Children(func() {
						if r.picked >= 0 && r.picked < len(r.rows) {
							row := r.rows[r.picked]
							if ui.Button(c, "Inspect retained run").Disabled(row.recordID == 0).Clicked() {
								a.experiments.scope, a.experiments.draftKey = nil, ""
								a.experiments.pick(row.recordID)
								a.experiments.open, r.open = true, false
							}
							for _, test := range r.rows[r.picked].tests {
								mark, color := "✓", t.Success
								if !test.Pass {
									mark, color = "✗", t.Danger
								}
								ui.Text(c, mark+" "+test.Name).FontSize(11).TextColor(color)
							}
							for _, entry := range row.console {
								ui.Text(c, entry.Phase+" · "+entry.Kind+" · "+entry.Text).FontSize(11).Selectable()
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
