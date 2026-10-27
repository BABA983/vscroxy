package app

import (
	"cmp"
	"context"
	"errors"
	"fmt"
	"maps"
	"net/http"
	"net/url"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/baba983/vscroxy/internal/capture"
	"github.com/egoist/mygo/ui"
)

type requestExecution struct {
	request        *jsRequest
	response       *capture.Flow
	replies        []string
	tests          []testResult
	vars           map[string]string
	err            error
	stage          string
	expectedStatus int
}

type debugRunKey struct{}

// A single execution is also a runner step. Both use the same encoder,
// scripts, proxy and session cookie jar; no request tab is borrowed by Runner.
func executeRequest(ctx context.Context, srv *capture.Server, r savedRequest, vars map[string]string, jar http.CookieJar) requestExecution {
	run := &scriptRun{vars: maps.Clone(vars)}
	out := requestExecution{vars: run.vars, stage: "Request setup failed"}
	status, _, _, err := r.Checks.parse()
	if err != nil {
		out.err = err
		return out
	}
	out.expectedStatus = status
	// Pre-scripts edit authored templates, not an already encoded request.
	// Generate variables first; expand and encode exactly once afterwards.
	template := &jsRequest{Method: r.Method, URL: r.URL, Body: r.Body,
		header: parseHeaderBlock(r.Headers), editedHeaders: map[string]bool{}}
	out.stage = "Pre-request script failed"
	if err := runScriptContext(ctx, r.Pre, run, map[string]any{"req": template}); err != nil {
		out.err = fmt.Errorf("Pre-request script: %w", err)
		out.tests, out.vars = run.tests, run.vars
		return out
	}
	r.Method, r.URL, r.Headers = template.Method, template.URL, headerBlock(template.header)
	if template.editedBody {
		r.BodyMode = "Raw" // Explicit script bodies override structured forms.
	}
	r.Body = template.Body
	authHeader := "Authorization"
	if r.Auth.Type == "API key" {
		authHeader = expandVars(r.Auth.Key, run.vars)
	}
	if template.editedHeaders[http.CanonicalHeaderKey(authHeader)] && (r.Auth.Type != "API key" || r.Auth.Location != "Query") {
		r.Auth = requestAuth{}
	}
	out.stage = "Request setup failed"
	out.tests, out.vars = run.tests, run.vars
	if r.Checks != (responseChecks{}) {
		var missing []string
		for _, name := range requestVariables(r) {
			if _, ok := run.vars[name]; !ok {
				missing = append(missing, name)
			}
		}
		if len(missing) > 0 {
			out.err = fmt.Errorf("Set environment variables before running: %s", strings.Join(missing, ", "))
			return out
		}
	}
	req, err := prepareRequest(r, run.vars)
	if err != nil {
		out.err = err
		return out
	}
	// Generated Content-Type/auth must not undo explicit set/remove calls.
	for name := range template.editedHeaders {
		req.Remove(name)
		for _, value := range template.header.Values(name) {
			req.header.Add(name, expandVars(value, run.vars))
		}
	}
	out.request = req
	if isWebSocket(req.URL) && r.Checks != (responseChecks{}) {
		out.err = fmt.Errorf("response checks require an HTTP(S) request, not a WebSocket exchange")
		return out
	}
	u, err := url.Parse(req.URL)
	if err != nil || u.Hostname() == "" || u.Scheme != "http" && u.Scheme != "https" && u.Scheme != "ws" && u.Scheme != "wss" {
		out.stage, out.err = "Request setup failed", fmt.Errorf("use an absolute HTTP(S) or WebSocket URL with a host")
		out.tests, out.vars = run.tests, run.vars
		return out
	}
	out.stage = "Network error"
	if isWebSocket(req.URL) {
		out.replies, out.err = wsSend(ctx, srv.Addr(), req, srv.CACertificate(), jar, 2*time.Second)
		if r.Post != "" && out.err == nil {
			out.stage = "Post-response script failed"
			out.err = fmt.Errorf("post-response scripts require an HTTP response")
		}
	} else {
		runKey, _ := ctx.Value(debugRunKey{}).(string)
		got, err := srv.ReplayWithCookies(ctx, capture.Flow{DebugRun: runKey, Method: req.Method, URL: req.URL, RequestHeader: req.header, RequestBody: req.Body}, jar, r.Checks == (responseChecks{}))
		out.err = err
		if err == nil {
			out.response = &got
			run.tests = append(run.tests, r.Checks.evaluate(got)...)
			res := &jsResponse{Status: got.Status, Body: got.ResponseBody, header: got.ResponseHeader}
			out.stage = "Post-response script failed"
			if err := runScriptContext(ctx, r.Post, run, map[string]any{"res": res, "req": req}); err != nil {
				out.err = fmt.Errorf("Post-response script: %w", err)
			}
		}
	}
	out.tests, out.vars = run.tests, run.vars
	return out
}

// Outcome separates transport/setup failures from HTTP responses and checks.
// A failed assertion never turns a received response into a network failure.
func (r requestExecution) outcome() string {
	switch {
	case errors.Is(r.err, context.Canceled):
		return "Cancelled"
	case errors.Is(r.err, context.DeadlineExceeded):
		return "Timed out"
	case r.err != nil:
		return r.stage
	case r.response != nil && r.response.Status >= 400:
		return "HTTP error response"
	case slices.ContainsFunc(r.tests, func(t testResult) bool { return !t.Pass }):
		return "Checks failed"
	case r.response == nil:
		return "WebSocket completed"
	default:
		return "Response received"
	}
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
	if r.err != nil || r.response != nil && r.response.Status >= 400 && r.response.Status != r.expectedStatus {
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
	if a.srv == nil || !a.srv.Running() {
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
	appModal(c, &r.open, func() {
		ui.Column(c).Width(820).Height(510).Gap(12).Children(func() {
			dialogTitle(c, "Collection Runner")
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
