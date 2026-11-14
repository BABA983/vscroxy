package app

import (
	"cmp"
	"context"
	"fmt"
	"github.com/baba983/vscroxy/internal/uikit"
	"maps"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/baba983/vscroxy/internal/request"
	"github.com/baba983/vscroxy/internal/runner"
	"github.com/baba983/vscroxy/internal/store"
	"github.com/baba983/vscroxy/locales"
	"github.com/egoist/mygo"
	"github.com/egoist/mygo/ui"
	"github.com/oneo-me/onevra/source/icons/lucide"
)

// runSummary is one line for a run: its error, or status, size, time and checks.
func runSummary(r request.Execution) string {
	if r.Skipped {
		return "Skipped by its pre-request script"
	}
	if r.Err != nil {
		return ntlmErrorText(r.Err)
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
	request      request.Request
	attempt      runner.Result
}

// pastRun is a finished run kept for the session: its rows, its report for
// export, and a one-line label for the picker.
type pastRun struct {
	label  string
	rows   []runnerRow
	report runner.Report
}

// maxPastRuns bounds the session's history; each run keeps its full report.
const maxPastRuns = 10

// runOutcome is written by the run goroutine before it closes events, so
// drainRunner can read it once the channel is closed.
type runOutcome struct {
	summary runner.Summary
	err     error
}

type collectionRunner struct {
	open, running, cancelled                    bool
	collection, iterations, environment, status string
	// delay is the wait between requests in milliseconds. discardVars drops
	// the run's variable writes instead of saving them to the environment,
	// globals and collection.
	delay                       string
	discardVars                 bool
	steps                       []runnerStep
	rows                        []runnerRow
	picked, completed, failures int
	results                     ui.ListState
	stopOnFailure               bool
	// report is the shown run's attempts; history holds finished runs, and
	// viewKey is the index of the shown one, as a picker key.
	report  runner.Report
	history []pastRun
	viewKey string
	outcome *runOutcome
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
	r.delay = cmp.Or(r.delay, "0")
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
	if r.running || a.loadRunner.running || a.remoteRunActive() {
		return
	}
	n, err := strconv.Atoi(r.iterations)
	if err != nil || n < 1 || n > 1000 {
		r.status = "Iterations must be between 1 and 1000"
		return
	}
	delay, err := strconv.Atoi(cmp.Or(r.delay, "0"))
	if err != nil || delay < 0 || delay > 3600000 {
		r.status = locales.RunnerDelayRange()
		return
	}
	if a.srv == nil || !a.srv.Running() {
		r.status = "Start the local proxy first"
		return
	}
	var requests []savedRequest
	var steps []runner.Step
	for _, step := range r.steps {
		if step.enabled {
			requests = append(requests, step.request)
			steps = append(steps, runner.Step{Request: step.request.Request, Context: a.sendContext(step.request.Request)})
		}
	}
	if len(requests) == 0 {
		r.status = "Choose at least one saved request"
		return
	}
	ctx, cancel := context.WithCancel(context.Background())
	r.cancel, r.running, r.cancelled = cancel, true, false
	r.rows, r.completed, r.failures, r.picked = nil, 0, 0, 0
	r.report = runner.Report{Version: 1, StartedAt: time.Now().UTC()}
	r.viewKey = strconv.Itoa(len(r.history))
	outcome := &runOutcome{}
	r.outcome = outcome
	r.environment, r.status = a.activeEnv, "Running…"
	events := make(chan runnerEvent, 16)
	r.events = events
	vars, stop, srv, env, data := a.activeVars(), r.stopOnFailure, a.srv, r.environment, r.data
	jar := a.requestCookies()
	go func() {
		defer close(events)
		defer cancel()
		defer a.invalidate()
		opts := runner.Options{Iterations: n, Delay: time.Duration(delay) * time.Millisecond, StopOnFailure: stop, Data: data, Vars: vars, Cookies: jar}
		outcome.summary, outcome.err = runner.Run(ctx, srv, steps, opts, func(result runner.Result) error {
			req, got := requests[result.Index], result.Execution
			record := snapshotExperiment(0, req, env, got)
			record.RunKey = result.RunKey
			event := runnerEvent{row: runnerRow{name: req.Name, iteration: result.Iteration, summary: runSummary(got), failed: got.Failed(), tests: got.Tests, console: result.Console}, before: result.Before, vars: maps.Clone(got.Vars), writes: got.Writes, record: record, request: req.Request, attempt: result}
			select {
			case events <- event:
			default:
				// Prefer retaining an already completed/cancelled attempt. Only
				// abandon delivery on cancellation if the UI is not draining.
				select {
				case events <- event:
				case <-ctx.Done():
					return ctx.Err()
				}
			}
			a.invalidate()
			return nil
		})
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

// chooseRunnerData reads a data file the user picks; its rows set the
// iteration count.
func (a *app) chooseRunnerData() {
	go func() {
		paths, err := mygo.Dialog.Open(mygo.OpenDialogOptions{Title: locales.RunnerDataFile(),
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
				a.finishRunner()
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
			r.report.Append(event.request, event.attempt, false)
			r.rows = append(r.rows, event.row)
			if len(r.rows) > 1000 {
				r.rows = slices.Delete(r.rows, 0, 1)
				r.picked = max(0, r.picked-1)
			}
			if !r.discardVars {
				if !maps.Equal(event.before, event.vars) {
					a.applyVars(r.environment, event.before, event.vars, event.writes...)
				}
				a.applyWrites(event.writes)
			}
		default:
			return
		}
	}
}

// finishRunner keeps the finished run in the session history and shows it.
func (a *app) finishRunner() {
	r := &a.runner
	r.report.Finish(r.outcome.summary, r.outcome.err, time.Since(r.report.StartedAt))
	label := fmt.Sprintf("%s · %s · %s · %d completed · %d failed", r.report.StartedAt.Local().Format("15:04:05"), cmp.Or(r.collection, "/"), r.status, r.report.Completed, r.report.Failed)
	r.history = append(r.history, pastRun{label: label, rows: r.rows, report: r.report})
	r.history = r.history[max(0, len(r.history)-maxPastRuns):]
	r.showRun(len(r.history) - 1)
}

// showRun makes history entry i the run whose rows and report are shown.
func (r *collectionRunner) showRun(i int) {
	past := r.history[i]
	r.rows, r.report = past.rows, past.report
	r.completed, r.failures = past.report.Completed, past.report.Failed
	r.viewKey = strconv.Itoa(i)
}

// runLabel names a picker key: a history entry, or the live run after the last.
func (r *collectionRunner) runLabel(key string) string {
	i, _ := strconv.Atoi(key)
	if i >= len(r.history) {
		return locales.RunningCollection()
	}
	return r.history[i].label
}

// exportRunReport saves the shown run as a JSON or JUnit report. Assertion
// names and error text stay out, as with the CLI's default.
func (a *app) exportRunReport(junit bool) {
	report := a.runner.report
	title, ext, encode := locales.ExportRunJSON(), "json", report.JSON
	if junit {
		title, ext, encode = locales.ExportRunJUnit(), "xml", report.JUnit
	}
	go func() {
		path, err := mygo.Dialog.Save(mygo.SaveDialogOptions{Parent: a.win, Title: title, DefaultPath: "run." + ext, Filters: []mygo.FileFilter{{Name: ext, Extensions: []string{ext}}}})
		if err != nil || path == "" {
			if err != nil {
				a.fail(title, err)
			}
			return
		}
		data, err := encode()
		if err == nil {
			err = store.WriteFile(path, data)
		}
		if err != nil {
			a.fail(title, err)
			return
		}
		a.done(locales.ExportedTo(path))
	}()
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
	uikit.Modal(c, &r.open, func() {
		ui.Column(c).Width(min(840, width-80)).Height(min(560, height-100)).Gap(t.Space(2)).Children(func() {
			uikit.DialogTitle(c, locales.CollectionRunner())
			uikit.Meta(c, locales.RunsSavedRequestsInOrder())
			ui.Row(c).Gap(t.Space(2)).AlignItems(ui.Center).Children(func() {
				names := runnableCollections(a.requests)
				if len(names) == 0 {
					names = []string{""}
				}
				if uikit.Select(c, &r.collection, names).Label(locales.RunnerCollection()).Grow(1).Disabled(r.running).Changed() {
					r.load(a.requests)
				}
				uikit.Input(c, &r.iterations).Width(86).Label(locales.Iterations()).Disabled(r.running)
				uikit.Tip(c, uikit.Input(c, &r.delay).Width(110).Label(locales.RunnerDelay()).Disabled(r.running), locales.RunnerDelay())
				ui.Checkbox(c, &r.stopOnFailure, "Stop on failure").FontSize(uikit.Scaled(uikit.TextBody)).Disabled(r.running)
				keep := !r.discardVars
				ui.Checkbox(c, &keep, locales.KeepVariableValues()).FontSize(uikit.Scaled(uikit.TextBody)).Disabled(r.running)
				r.discardVars = !keep
			})
			ui.Row(c).Gap(t.Space(2)).AlignItems(ui.Center).Children(func() {
				text, color := "No data file: every iteration uses the same variables.", t.TextMuted
				switch {
				case r.dataProblem != "":
					text, color = r.dataProblem, t.Danger
				case len(r.data) > 0:
					text, color = fmt.Sprintf("%s · %d %s · %s", r.dataName, len(r.data), plural(len(r.data), "row", "rows"), strings.Join(request.DataColumns(r.data), ", ")), t.Text
				}
				uikit.Tip(c, ui.Text(c, text).FontSize(uikit.Scaled(uikit.TextMeta)).TextColor(color).Grow(1).MinWidth(0).SingleLine(), text)
				if len(r.data) > 0 && uikit.SecondaryButton(c, locales.Remove()).Label(locales.RemoveDataFile()).Disabled(r.running).Clicked() {
					r.data, r.dataName, r.iterations = nil, "", "1"
				}
				if uikit.Tip(c, uikit.SecondaryButton(c, locales.DataFile()), locales.CsvWithAHeaderRow()).Disabled(r.running).Clicked() {
					a.chooseRunnerData()
				}
			})
			ui.Row(c).Grow(1).MinHeight(0).Border(1, t.Border).Radius(uikit.RadiusControl).AlignItems(ui.Stretch).Children(func() {
				ui.Column(c).Width(300).Children(func() {
					uikit.Label(c, locales.ExecutionOrder()).Height(uikit.HeightTabs).Padding(t.Space(2)).
						Border(0, t.Border).BorderWidth(0, 0, 1, 0)
					move, delta := -1, 0
					ui.Scroll(c).Grow(1).Children(func() {
						if len(r.steps) == 0 {
							uikit.EmptyState(c, locales.NoSavedRequests(), locales.ChooseACollectionWithSaved(), nil)
						}
						for i := range r.steps {
							step := &r.steps[i]
							ui.Row(c.Key(i)).Height(uikit.HeightControl).Padding(0, t.Space(2)).Gap(t.Space(1)).AlignItems(ui.Center).
								Border(0, t.Border).BorderWidth(0, 0, 1, 0).Children(func() {
								ui.Checkbox(c, &step.enabled, step.request.Name).Label(locales.RunRequest(i + 1)).FontSize(uikit.Scaled(uikit.TextBody)).Grow(1).Disabled(r.running)
								if uikit.Tip(c, uikit.IconButton(c, locales.MoveRequestUp(i+1), locales.MoveRequestUp(i+1), lucide.ArrowUp()).Disabled(r.running || i == 0), locales.MoveRequestUp(i+1)).Clicked() {
									move, delta = i, -1
								}
								if uikit.Tip(c, uikit.IconButton(c, locales.MoveRequestDown(i+1), locales.MoveRequestDown(i+1), lucide.ArrowDown()).Disabled(r.running || i == len(r.steps)-1), locales.MoveRequestDown(i+1)).Clicked() {
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
					uikit.Label(c, locales.RunnerResults(r.completed, r.failures)).Height(uikit.HeightTabs).Padding(t.Space(2)).FontFeatures("tnum").Border(0, t.Border).BorderWidth(0, 0, 1, 0)
					if len(r.history) > 0 || r.running {
						keys := make([]string, 0, len(r.history)+1)
						for i := range r.history {
							keys = append(keys, strconv.Itoa(i))
						}
						if r.running {
							keys = append(keys, strconv.Itoa(len(r.history)))
						}
						if uikit.SelectLabels(c, &r.viewKey, keys, r.runLabel).Label(locales.PastRuns()).Disabled(r.running).FillWidth().Changed() {
							if i, err := strconv.Atoi(r.viewKey); err == nil && i < len(r.history) {
								r.showRun(i)
								r.picked = 0
							}
						}
					}
					r.results.Selected = &r.picked
					c.SetTheme(uikit.ListTheme(t, r.results.Handle.Focused(c)))
					ui.List(c, &r.results, len(r.rows)).Grow(1).Rows(func(item ui.ListRow) {
						c := item.Context
						if item.Selected() {
							uikit.RowMarker(c, item.ListFocused())
						}
						row := r.rows[item.Index]
						color := t.Success
						if row.failed {
							color = t.Danger
						}
						uikit.Tip(c, ui.Row(c).Height(uikit.HeightControl).Padding(0, t.Space(2)).Gap(t.Space(2)).AlignItems(ui.Center).
							Border(0, t.Border).BorderWidth(0, 0, 1, 0), row.summary).Children(func() {
							uikit.Body(c, fmt.Sprintf("%d · %s", row.iteration, row.name)).FontFeatures("tnum").Grow(1).MinWidth(0).SingleLine()
							summary := ui.Text(c, row.summary).FontSize(uikit.Scaled(uikit.TextMeta)).FontFeatures("tnum").SingleLine().MaxWidth(240)
							summary.TextColor(color)
						})
					}).Children(func() {
						if len(r.rows) == 0 {
							if r.running {
								uikit.EmptyState(c, locales.RunningCollection(), locales.CompletedRequestsWillAppearHere(), nil)
							} else {
								uikit.EmptyState(c, locales.NoResultsYet(), locales.RunTheSelectedRequestsTo(), nil)
							}
						}
					})
					c.SetTheme(t)
					ui.Divider(c)
					ui.Scroll(c).Height(125).Padding(t.Space(2)).Gap(t.Space(1)).Children(func() {
						ui.Row(c).Gap(t.Space(2)).Children(func() {
							disabled := r.running || r.report.StartedAt.IsZero()
							if uikit.SecondaryButton(c, locales.ExportRunJSON()).Disabled(disabled).Clicked() {
								a.exportRunReport(false)
							}
							if uikit.SecondaryButton(c, locales.ExportRunJUnit()).Disabled(disabled).Clicked() {
								a.exportRunReport(true)
							}
						})
						if r.picked >= 0 && r.picked < len(r.rows) {
							row := r.rows[r.picked]
							if uikit.SecondaryButton(c, locales.InspectRetainedRun()).Disabled(row.recordID == 0).Clicked() {
								a.experiments.scope, a.experiments.draftKey = nil, ""
								a.experiments.pick(row.recordID)
								a.experiments.open, r.open = true, false
							}
							for _, test := range r.rows[r.picked].tests {
								testResultRow(c, test)
							}
							for _, entry := range row.console {
								uikit.Body(c, entry.Phase+" · "+entry.Kind+" · "+entry.Text).Selectable()
							}
						}
					})
				})
			})
			footer := "Displaying latest 1000 results; closing keeps a run active"
			if r.status != "" {
				footer = r.status + " · displaying latest 1000 results; closing keeps a run active"
			}
			uikit.Meta(c, footer)
			ui.Row(c).Gap(t.Space(2)).Justify(ui.End).Children(func() {
				uikit.SecondaryButton(c, locales.LoadTest()).Disabled(r.running || a.remoteRunActive()).OnClick(a.openLoadRunner)
				if uikit.SecondaryButton(c, locales.Close()).Clicked() {
					r.open = false
				}
				if uikit.SecondaryButton(c, locales.CancelRun()).Disabled(!r.running).Clicked() {
					r.cancelled = true
					r.cancel()
					r.status = "Stopping…"
				}
				if uikit.PrimaryButton(c, locales.RunCollection()).Disabled(r.running || a.loadRunner.running || a.remoteRunActive() || len(r.steps) == 0).Clicked() {
					a.startRunner()
				}
			})
		})
	})
}
