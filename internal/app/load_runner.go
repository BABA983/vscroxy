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

	"github.com/baba983/vscroxy/internal/runner"
	"github.com/baba983/vscroxy/internal/uikit"
	"github.com/baba983/vscroxy/locales"
	"github.com/egoist/mygo/ui"
)

const loadMaxAttempts = 100000

type loadRunnerEvent struct {
	generation uint64
	report     runner.LoadReport
}

// Only the UI thread touches this state. The worker owns immutable inputs and
// a one-slot mailbox, never request results or references to editor state.
type collectionLoadRunner struct {
	open, running, stopping bool
	collection, environment string
	users, duration         string
	stopOnFailure           bool
	status                  string
	report                  runner.LoadReport
	generation              uint64
	cancel                  context.CancelFunc
	done                    <-chan struct{}
	events                  chan loadRunnerEvent
}

func (a *app) openLoadRunner() {
	if a.runner.running || a.remoteRunActive() {
		return
	}
	r := &a.loadRunner
	a.runner.open, r.open = false, true
	if r.running {
		return
	}
	r.users, r.duration = cmp.Or(r.users, "1"), cmp.Or(r.duration, "30s")
	r.environment = a.activeEnv
	names := runnableCollections(a.requests)
	if slices.Contains(names, a.runner.collection) {
		r.collection = a.runner.collection
	} else if !slices.Contains(names, r.collection) && len(names) > 0 {
		r.collection = names[0]
	}
}

// loadRunnerSteps snapshots the saved library, not the editor or ordinary
// Runner's reordered/enabled draft. Vault access never prompts for an unlock.
func (a *app) loadRunnerSteps(collection, environmentName string) []runner.Step {
	var steps []runner.Step
	for _, saved := range a.requests {
		r := saved.Request
		if r.Collection != collection && (collection == "" || !strings.HasPrefix(r.Collection, collection+"/")) {
			continue
		}
		r.Form, r.HeaderList = slices.Clone(r.Form), slices.Clone(r.HeaderList)
		r.TokenSources, r.Examples = slices.Clone(r.TokenSources), slices.Clone(r.Examples)
		in := a.inheritance(r.Collection)
		in.Globals = maps.Clone(in.Globals)
		in.Environment = environmentName
		if at := slices.IndexFunc(a.envs, func(e environment) bool { return e.Name == environmentName }); at >= 0 {
			in.EnvironmentInitial = maps.Clone(a.envs[at].Vars)
		}
		for i := range in.Folders {
			in.Folders[i].Variables = slices.Clone(in.Folders[i].Variables)
		}
		if a.vault.loaded {
			secrets := slices.Clone(a.vault.secrets)
			for i := range secrets {
				secrets[i].Domains = slices.Clone(secrets[i].Domains)
			}
			in.Vault = secrets.Lookup
		}
		steps = append(steps, runner.Step{Request: r, Context: in})
	}
	slices.SortStableFunc(steps, func(a, b runner.Step) int { return strings.Compare(a.Request.Collection, b.Request.Collection) })
	return steps
}

func (a *app) startLoadRunner() {
	r := &a.loadRunner
	if r.running || a.runner.running || a.remoteRunActive() {
		return
	}
	users, err := strconv.Atoi(strings.TrimSpace(r.users))
	if err != nil || users < 1 || users > 64 {
		r.status = "Users must be between 1 and 64"
		return
	}
	duration, err := time.ParseDuration(strings.TrimSpace(r.duration))
	if err != nil || duration < time.Second || duration > 10*time.Minute {
		r.status = "Duration must be between 1s and 10m (for example, 30s)"
		return
	}
	if a.srv == nil || !a.srv.Running() {
		r.status = "Start the local proxy first"
		return
	}
	var vars map[string]string
	if r.environment != "" {
		at := slices.IndexFunc(a.envs, func(e environment) bool { return e.Name == r.environment })
		if at < 0 {
			r.status = "Choose an available environment"
			return
		}
		vars = maps.Clone(a.envs[at].values())
	}
	steps := a.loadRunnerSteps(r.collection, r.environment)
	if len(steps) < 1 || len(steps) > 200 {
		r.status = "Choose a saved collection with 1–200 HTTP(S) requests"
		return
	}
	for _, step := range steps {
		method, url := strings.ToUpper(step.Request.Method), strings.ToLower(strings.TrimSpace(step.Request.URL))
		if method == "WS" || method == "WSS" || strings.HasPrefix(url, "ws:") || strings.HasPrefix(url, "wss:") {
			r.status = "Load tests support HTTP(S) requests only"
			return
		}
	}
	opts := runner.LoadOptions{Users: users, Duration: duration, Timeout: 30 * time.Second, MaxAttempts: loadMaxAttempts, StopOnFailure: r.stopOnFailure, Vars: vars}
	ctx, cancel := context.WithCancel(context.Background())
	r.generation++
	generation := r.generation
	events, done := make(chan loadRunnerEvent, 1), make(chan struct{})
	r.cancel, r.events, r.done = cancel, events, done
	r.running, r.stopping, r.status, r.report = true, false, "Running…", runner.LoadReport{}
	// Capture the window, never app/UI state, in the worker. Invalidate is
	// asynchronous; neither progress nor shutdown depends on UI draining.
	srv, win := a.srv, a.win
	go func() {
		defer func() {
			cancel()
			close(events)
			// The final publish can wake a frame before events closes. Wake
			// once more after closure so that frame releases the busy state.
			if win != nil {
				win.Invalidate()
			}
			close(done)
		}()
		publish := func(report runner.LoadReport) {
			event := loadRunnerEvent{generation: generation, report: report}
			select {
			case events <- event:
			default:
				select {
				case <-events:
				default:
				}
				select {
				case events <- event:
				default:
				}
			}
			if win != nil {
				win.Invalidate()
			}
		}
		report, _ := runner.RunLoad(ctx, srv, steps, opts, publish)
		if !report.Done {
			// Only possible for an engine validation error. Do not expose
			// arbitrary errors/request contents in this aggregate-only panel.
			publish(runner.LoadReport{Done: true, Reason: "execution-error"})
		}
	}()
}

func (a *app) stopLoadRunner() {
	r := &a.loadRunner
	if r.running && r.cancel != nil {
		r.stopping, r.status = true, "Stopping…"
		r.cancel()
	}
}

// closeLoadRunner is an App Quit hook, not the modal's Close action.
func (a *app) closeLoadRunner() {
	a.stopLoadRunner()
	if a.loadRunner.done != nil {
		<-a.loadRunner.done
	}
}

func (a *app) drainLoadRunner() {
	r := &a.loadRunner
	for r.events != nil {
		select {
		case event, ok := <-r.events:
			if !ok {
				r.events, r.cancel, r.running, r.stopping = nil, nil, false, false
				return
			}
			if event.generation != r.generation {
				continue
			}
			r.report = event.report
			if event.report.Done {
				r.status = loadRunnerReason(event.report.Reason)
			}
		default:
			return
		}
	}
}

func loadRunnerReason(reason string) string {
	switch reason {
	case "duration":
		return "Finished · duration reached"
	case "failure":
		return "Stopped · first failure"
	case "attempt-limit":
		return "Stopped · attempt budget reached"
	case "cancelled":
		return "Stopped · cancelled"
	case "timed-out":
		return "Stopped · timed out"
	case "finished":
		return "Finished · all users ended their sequence"
	default:
		return "Stopped · execution error"
	}
}

func (a *app) loadRunnerModal(c *ui.Context, t *ui.Theme) {
	r := &a.loadRunner
	if !r.open {
		return
	}
	width, height := c.Size()
	uikit.Modal(c, &r.open, func() {
		ui.Column(c).Width(min(820, width-100)).Height(min(570, height-100)).Gap(t.Space(2)).Children(func() {
			uikit.DialogTitle(c, locales.CollectionLoadTest())
			uikit.Meta(c, locales.FixedConcurrentUsersClosedModel())
			ui.Scroll(c).Grow(1).MinHeight(0).Gap(t.Space(3)).Padding(0, t.Space(1)).Children(func() {
				ui.Row(c).Gap(t.Space(3)).Children(func() {
					ui.Column(c).Grow(1).MinWidth(0).Gap(t.Space(1)).Children(func() {
						uikit.Label(c, locales.SavedCollection())
						names := runnableCollections(a.requests)
						if len(names) == 0 {
							names = []string{""}
						}
						uikit.Select(c, &r.collection, names).Label(locales.LoadCollection()).FillWidth().Disabled(r.running)
					})
					ui.Column(c).Width(220).Gap(t.Space(1)).Children(func() {
						uikit.Label(c, locales.Environment())
						names := append([]string{""}, a.envNames()...)
						uikit.Select(c, &r.environment, names).Label(locales.LoadEnvironment()).FillWidth().Disabled(r.running)
					})
				})
				ui.Row(c).Gap(t.Space(3)).AlignItems(ui.End).Children(func() {
					ui.Column(c).Width(110).Gap(t.Space(1)).Children(func() {
						uikit.Label(c, locales.Users164())
						uikit.Input(c, &r.users).Label(locales.LoadUsers()).FillWidth().Disabled(r.running)
					})
					ui.Column(c).Width(160).Gap(t.Space(1)).Children(func() {
						uikit.Label(c, locales.Duration1s10m())
						uikit.Input(c, &r.duration).Label(locales.LoadDuration()).FillWidth().Disabled(r.running)
					})
					ui.Checkbox(c, &r.stopOnFailure, "Stop on failure").FontSize(uikit.TextBody).Disabled(r.running)
				})
				uikit.Meta(c, locales.Budget30SPerExecution())
				ui.Column(c).Padding(t.Space(3)).Gap(t.Space(2)).Border(1, t.Border).Radius(uikit.RadiusControl).Children(func() {
					uikit.Label(c, locales.VolatileAggregatesExecutionTimeIn())
					m := r.report.Total
					loadMetricRow(c, t, [][2]string{{"Completed", fmt.Sprint(m.Completed)}, {"Failed", fmt.Sprint(m.Failed)}, {"Skipped", fmt.Sprint(m.Skipped)}, {"Interrupted", fmt.Sprint(m.Interrupted)}, {"Executions/s", fmt.Sprintf("%.1f", m.RequestsPerSec)}})
					loadMetricRow(c, t, [][2]string{{"Mean ms", fmt.Sprintf("%.2f", m.MeanMillis)}, {"P50 ms", fmt.Sprintf("%.2f", m.P50Millis)}, {"P95 ms", fmt.Sprintf("%.2f", m.P95Millis)}, {"P99 ms", fmt.Sprintf("%.2f", m.P99Millis)}, {"Max ms", fmt.Sprintf("%.2f", m.MaxMillis)}})
				})
				uikit.Prose(c, locales.RealRequestsMayHaveSide())
				uikit.Meta(c, locales.EachUserHasIsolatedVariables())
			})
			status := cmp.Or(r.status, "Ready · closing this panel keeps an active load running")
			uikit.Tip(c, ui.Text(c, status).FontSize(uikit.TextBody).TextColor(t.Text).SingleLine(), status)
			ui.Row(c).Gap(t.Space(2)).AlignItems(ui.Center).Children(func() {
				uikit.Meta(c, locales.CloseKeepsRunningQuitCancels()).Grow(1)
				uikit.SecondaryButton(c, locales.Close()).OnClick(func() { r.open = false })
				uikit.SecondaryButton(c, locales.Stop()).Disabled(!r.running || r.stopping).OnClick(a.stopLoadRunner)
				uikit.PrimaryButton(c, locales.StartLoadTest()).Disabled(r.running || a.runner.running || a.remoteRunActive() || len(a.requests) == 0).OnClick(a.startLoadRunner)
			})
		})
	})
}

func loadMetricRow(c *ui.Context, t *ui.Theme, metrics [][2]string) {
	ui.Row(c).Gap(t.Space(2)).Children(func() {
		for _, metric := range metrics {
			ui.Column(c).Grow(1).Basis(0).MinWidth(0).Gap(t.Space(1)).Children(func() {
				uikit.Meta(c, metric[0])
				ui.Text(c, metric[1]).FontSize(uikit.TextTitle).FontFeatures("tnum").SingleLine()
			})
		}
	})
}
