package app

import (
	"cmp"
	"context"
	"fmt"
	"maps"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"time"
	"uuid"

	"github.com/baba983/vscroxy/internal/request"
	"github.com/egoist/mygo"
	"github.com/egoist/mygo/ui"
)

// runSummary is one line for a run: its error, or status, size, time and checks.
func runSummary(r request.Execution) string {
	if r.Skipped {
		return "Skipped by its pre-request script"
	}
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
	writes       []request.Write
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
	// data are the data file's rows, one per iteration; dataName names it.
	data                  []map[string]string
	dataName, dataProblem string
	cancel                context.CancelFunc
	events                chan runnerEvent
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
	slices.SortStableFunc(r.steps, func(a, b runnerStep) int { return strings.Compare(a.request.Collection, b.request.Collection) })
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
	vars, stop, srv, env, data := a.activeVars(), r.stopOnFailure, a.srv, r.environment, r.data
	jar := a.requestCookies()
	go func() {
		defer close(events)
		defer cancel()
		defer a.invalidate()
		var locals map[string]string
		for iteration := 1; iteration <= n; iteration++ {
			for i := 0; i < len(requests); {
				if ctx.Err() != nil {
					return
				}
				req, in := requests[i], contexts[i]
				in.Iteration, in.Iterations, in.Locals = iteration-1, n, locals
				if len(data) > 0 {
					// Postman repeats the last row when there are more iterations.
					in.Data = data[min(iteration, len(data))-1]
				}
				before := maps.Clone(vars)
				stepCtx, stepCancel := context.WithTimeout(ctx, 30*time.Second)
				runKey := uuid.New().String()
				stepCtx = request.WithRunKey(stepCtx, runKey)
				got := req.Execute(stepCtx, srv, in, vars, jar)
				stepCancel()
				vars, locals = got.Vars, got.Locals
				// Later requests see the globals and collection variables
				// this one's scripts changed, as the app is about to save them.
				for j := range contexts {
					contexts[j] = contexts[j].Apply(got.Writes, requests[j].Collection)
				}
				next, console := nextStep(requests, i, got.Next), got.Console
				if got.Next != nil && next == len(requests) {
					text := "pm.execution.setNextRequest(null) stopped the run"
					if *got.Next != "" {
						text = fmt.Sprintf("setNextRequest(%q): no such request in this run, so it stopped", *got.Next)
					}
					console = append(slices.Clone(console), request.ConsoleMessage{Phase: "runner", Kind: "warning", Text: text})
				}
				record := snapshotExperiment(0, req, env, got)
				record.RunKey = runKey
				event := runnerEvent{row: runnerRow{name: req.Name, iteration: iteration, summary: runSummary(got), failed: got.Failed(), tests: got.Tests, console: console}, before: before, vars: maps.Clone(vars), writes: got.Writes, record: record}
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
				if stop && got.Failed() || got.Next != nil && next == len(requests) {
					return
				}
				i = next
			}
		}
	}()
}

// runnableCollections are the paths a run can start from: every collection
// and folder that holds requests, directly or below it.
func runnableCollections(requests []savedRequest) []string {
	seen := map[string]bool{}
	for _, r := range requests {
		path := ""
		for part := range strings.SplitSeq(r.Collection, "/") {
			path = strings.TrimPrefix(path+"/"+part, "/")
			seen[path] = true
		}
	}
	return slices.Sorted(maps.Keys(seen))
}

// nextStep is the index of the request after step i: the following one, or
// the one pm.execution.setNextRequest named. len(requests) stops the run.
func nextStep(requests []savedRequest, i int, next *string) int {
	if next == nil {
		return i + 1
	}
	if at := slices.IndexFunc(requests, func(r savedRequest) bool { return r.Name == *next || r.ID == *next }); at >= 0 && *next != "" {
		return at
	}
	return len(requests)
}

// chooseRunnerData reads a data file the user picks; its rows set the
// iteration count.
func (a *app) chooseRunnerData() {
	go func() {
		paths, err := mygo.Dialog.Open(mygo.OpenDialogOptions{Title: "Runner data file",
			Filters: []mygo.FileFilter{{Name: "CSV or JSON", Extensions: []string{"csv", "json"}}}})
		if err != nil || len(paths) == 0 {
			return
		}
		data, err := os.ReadFile(paths[0])
		var rows []map[string]string
		if err == nil {
			rows, err = request.ReadIterationData(paths[0], data)
		}
		a.win.Update(func() { a.runner.useData(filepath.Base(paths[0]), rows, err) })
	}()
}

func (r *collectionRunner) useData(name string, rows []map[string]string, err error) {
	if err != nil {
		r.dataProblem = name + ": " + err.Error()
		return
	}
	r.data, r.dataName, r.dataProblem = rows, name, ""
	r.iterations = strconv.Itoa(len(rows))
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
			a.applyWrites(event.writes)
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
	width, height := c.Size()
	appModal(c, &r.open, func() {
		ui.Column(c).Width(min(840, width-80)).Height(min(560, height-100)).Gap(t.Space(2)).Children(func() {
			dialogTitle(c, "Collection Runner")
			ui.Text(c, "Runs saved requests in order, 30 s per request").FontSize(textMeta).TextColor(t.TextMuted)
			ui.Row(c).Gap(t.Space(2)).AlignItems(ui.Center).Children(func() {
				names := runnableCollections(a.requests)
				if len(names) == 0 {
					names = []string{""}
				}
				if ui.Select(c, &r.collection, names).Label("Runner collection").Grow(1).Height(heightControl).FontSize(textBody).Radius(radiusControl).Disabled(r.running).Changed() {
					r.load(a.requests)
				}
				ui.TextInput(c, &r.iterations).Width(86).Height(heightControl).FontSize(textBody).Radius(radiusControl).Label("Iterations").Disabled(r.running)
				ui.Checkbox(c, &r.stopOnFailure, "Stop on failure").FontSize(textBody).Disabled(r.running)
			})
			ui.Row(c).Gap(t.Space(2)).AlignItems(ui.Center).Children(func() {
				text, color := "No data file: every iteration uses the same variables.", t.TextMuted
				switch {
				case r.dataProblem != "":
					text, color = r.dataProblem, t.Danger
				case len(r.data) > 0:
					text, color = fmt.Sprintf("%s · %d %s · %s", r.dataName, len(r.data), plural(len(r.data), "row", "rows"), strings.Join(request.DataColumns(r.data), ", ")), t.Text
				}
				ui.Text(c, text).FontSize(textMeta).TextColor(color).Grow(1).MinWidth(0).SingleLine().Tooltip(text)
				if len(r.data) > 0 && secondaryButton(c, "Remove").Label("Remove data file").Disabled(r.running).Clicked() {
					r.data, r.dataName, r.iterations = nil, "", "1"
				}
				if secondaryButton(c, "Data file…").Tooltip("CSV with a header row, or a JSON array of objects; each row is one iteration's pm.iterationData").Disabled(r.running).Clicked() {
					a.chooseRunnerData()
				}
			})
			ui.Row(c).Grow(1).MinHeight(0).Border(1, t.Border).Radius(radiusControl).AlignItems(ui.Stretch).Children(func() {
				ui.Column(c).Width(300).Children(func() {
					ui.Text(c, "EXECUTION ORDER").Height(heightTabs).Padding(t.Space(2)).FontSize(textMeta).Bold().TextColor(t.TextMuted).
						Border(0, t.Border).BorderWidth(0, 0, 1, 0)
					move, delta := -1, 0
					ui.Scroll(c).Grow(1).Children(func() {
						if len(r.steps) == 0 {
							emptyState(c, "No saved requests", "Choose a collection with saved requests to run.", nil)
						}
						for i := range r.steps {
							step := &r.steps[i]
							ui.Row(c).Key(i).Height(heightControl).Padding(0, t.Space(2)).Gap(t.Space(1)).AlignItems(ui.Center).
								Border(0, t.Border).BorderWidth(0, 0, 1, 0).Children(func() {
								ui.Checkbox(c, &step.enabled, step.request.Name).Label(fmt.Sprintf("Run request %d", i+1)).FontSize(textBody).Grow(1).Disabled(r.running)
								if secondaryButton(c, "↑").Label(fmt.Sprintf("Move request %d up", i+1)).Tooltip(fmt.Sprintf("Move request %d up", i+1)).Width(heightCompact).Padding(0).Disabled(r.running || i == 0).Clicked() {
									move, delta = i, -1
								}
								if secondaryButton(c, "↓").Label(fmt.Sprintf("Move request %d down", i+1)).Tooltip(fmt.Sprintf("Move request %d down", i+1)).Width(heightCompact).Padding(0).Disabled(r.running || i == len(r.steps)-1).Clicked() {
									move, delta = i, 1
								}
							})
						}
					})
					if move >= 0 {
						r.steps[move], r.steps[move+delta] = r.steps[move+delta], r.steps[move]
					}
				})
				ui.Divider(c)
				ui.Column(c).Grow(1).MinWidth(0).Children(func() {
					ui.Text(c, fmt.Sprintf("RESULTS · %d completed · %d failed", r.completed, r.failures)).Height(heightTabs).Padding(t.Space(2)).
						FontSize(textMeta).FontFeatures("tnum").Bold().TextColor(t.TextMuted).Border(0, t.Border).BorderWidth(0, 0, 1, 0)
					r.results.Selected = &r.picked
					ui.List(c, &r.results, len(r.rows)).Grow(1).Rows(func(item ui.ListRow) {
						c := item.Context
						row := r.rows[item.Index]
						color := t.Success
						if row.failed {
							color = t.Danger
						}
						ui.Row(c).Height(heightControl).Padding(0, t.Space(2)).Gap(t.Space(2)).AlignItems(ui.Center).
							Border(0, t.Border).BorderWidth(0, 0, 1, 0).Tooltip(row.summary).Children(func() {
							ui.Text(c, fmt.Sprintf("%d · %s", row.iteration, row.name)).FontSize(textBody).FontFeatures("tnum").Grow(1).MinWidth(0).SingleLine()
							summary := ui.Text(c, row.summary).FontSize(textMeta).FontFeatures("tnum").SingleLine().MaxWidth(240)
							if !item.Selected() {
								summary.TextColor(color)
							}
						})
					}).Children(func() {
						if len(r.rows) == 0 {
							if r.running {
								emptyState(c, "Running collection", "Completed requests will appear here.", nil)
							} else {
								emptyState(c, "No results yet", "Run the selected requests to inspect checks and output.", nil)
							}
						}
					})
					ui.Divider(c)
					ui.Scroll(c).Height(125).Padding(t.Space(2)).Gap(t.Space(1)).Children(func() {
						if r.picked >= 0 && r.picked < len(r.rows) {
							row := r.rows[r.picked]
							if secondaryButton(c, "Inspect retained run").Disabled(row.recordID == 0).Clicked() {
								a.experiments.scope, a.experiments.draftKey = nil, ""
								a.experiments.pick(row.recordID)
								a.experiments.open, r.open = true, false
							}
							for _, test := range r.rows[r.picked].tests {
								testResultRow(c, test)
							}
							for _, entry := range row.console {
								ui.Text(c, entry.Phase+" · "+entry.Kind+" · "+entry.Text).FontSize(textBody).Selectable()
							}
						}
					})
				})
			})
			footer := "Displaying latest 1000 results; closing keeps a run active"
			if r.status != "" {
				footer = r.status + " · displaying latest 1000 results; closing keeps a run active"
			}
			ui.Text(c, footer).FontSize(textMeta).TextColor(t.TextMuted)
			ui.Row(c).Gap(t.Space(2)).Justify(ui.End).Children(func() {
				if secondaryButton(c, "Close").Clicked() {
					r.open = false
				}
				if secondaryButton(c, "Cancel run").Disabled(!r.running).Clicked() {
					r.cancelled = true
					r.cancel()
					r.status = "Stopping…"
				}
				if primaryButton(c, "Run collection").Disabled(r.running || len(r.steps) == 0).Clicked() {
					a.startRunner()
				}
			})
		})
	})
}
