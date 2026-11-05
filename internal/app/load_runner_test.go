package app

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/baba983/vscroxy/internal/request"
	"github.com/baba983/vscroxy/internal/runner"
	"github.com/baba983/vscroxy/internal/vault"
	"github.com/egoist/mygo/ui"
)

func awaitLoadRunner(t *testing.T, a *app) {
	t.Helper()
	select {
	case <-a.loadRunner.done:
	case <-time.After(time.Minute):
		a.stopLoadRunner()
		t.Fatal("load worker did not finish")
	}
	a.drainLoadRunner()
	if a.loadRunner.running || a.loadRunner.events != nil || !a.loadRunner.report.Done {
		t.Fatal("final aggregate was not drained")
	}
}

func TestLoadRunnerEntryDefaultsAndValidation(t *testing.T) {
	a := workspaceApp()
	a.openRunner()
	tt := ui.NewTester(a.view, 960, 600)
	if err := tt.Click("Load test…"); err != nil {
		t.Fatal(err)
	}
	if a.runner.open || !a.loadRunner.open || a.loadRunner.users != "1" || a.loadRunner.duration != "30s" || a.loadRunner.stopOnFailure {
		t.Fatalf("unexpected load defaults: %+v", a.loadRunner)
	}
	for _, tc := range []struct{ users, duration, want string }{
		{"0", "30s", "Users must be between 1 and 64"},
		{"65", "30s", "Users must be between 1 and 64"},
		{"x", "30s", "Users must be between 1 and 64"},
		{"1", "0s", "Duration must be between 1s and 10m (for example, 30s)"},
		{"1", "11m", "Duration must be between 1s and 10m (for example, 30s)"},
		{"1", "30s", "Start the local proxy first"},
	} {
		a.loadRunner.users, a.loadRunner.duration = tc.users, tc.duration
		a.startLoadRunner()
		if a.loadRunner.running || a.loadRunner.status != tc.want {
			t.Fatalf("%+v: status %q", tc, a.loadRunner.status)
		}
	}
	// Editing the bound input survives extra builds, rather than rebinding a
	// temporary string; validation is visible in the panel, not a log only.
	if err := tt.Click("Load users"); err != nil {
		t.Fatal(err)
	}
	tt.Key(ui.Cmd, ui.KeyA)
	tt.Type("3")
	tt.Frame()
	if a.loadRunner.users != "3" {
		t.Fatalf("users edit lost: %q", a.loadRunner.users)
	}
	if err := tt.Click("Start load test"); err != nil || !tt.HasText("Start the local proxy first") {
		t.Fatalf("validation not rendered: %v", err)
	}
	tt.Key(0, ui.KeyEscape)
	if a.loadRunner.open {
		t.Fatal("Escape did not dismiss load modal")
	}
	a.openRunner()
	tt.Frame()
	if err := tt.Click("Load test…"); err != nil {
		t.Fatal(err)
	}
	tt.Frame()
	if !tt.HasText("Collection load test") {
		t.Fatal("load modal did not return after extra frames")
	}
}

func TestLoadRunnerLayouts(t *testing.T) {
	for _, size := range [][2]int{{960, 600}, {1280, 800}} {
		for _, state := range []string{"ready", "running", "stopping", "finished", "invalid"} {
			for _, dark := range []bool{false, true} {
				t.Run(fmt.Sprintf("%dx%d/%s/dark=%t", size[0], size[1], state, dark), func(t *testing.T) {
					a := workspaceApp()
					if dark {
						a.appearance = "dark"
					}
					a.openLoadRunner()
					switch state {
					case "running", "stopping", "finished":
						a.loadRunner.running = state != "finished"
						a.loadRunner.stopping = state == "stopping"
						a.loadRunner.status = map[string]string{"running": "Running…", "stopping": "Stopping…", "finished": "Finished · duration reached"}[state]
						a.loadRunner.report = runner.LoadReport{Done: state == "finished", Reason: "duration", Total: runner.LoadMetrics{Completed: 15832, Failed: 12, Skipped: 3, Interrupted: 4, RequestsPerSec: 527.7, MeanMillis: 12.35, P50Millis: 8.11, P95Millis: 34.54, P99Millis: 61.12, MaxMillis: 105.91}}
					case "invalid":
						a.loadRunner.status = "Duration must be between 1s and 10m (for example, 30s)"
					}
					tt := ui.NewTester(a.view, size[0], size[1])
					for _, label := range []string{"Collection load test", "Load collection", "Load environment", "Load users", "Load duration", "Completed", "P99 ms", "Max ms", "Close", "Stop", "Start load test"} {
						r, ok := tt.Find(label)
						if !ok || r.W <= 0 || r.H <= 0 || r.X < 0 || r.Y < 0 || r.X+r.W > float32(size[0]) || r.Y+r.H > float32(size[1]) {
							t.Fatalf("%q does not fit: %+v, found=%t", label, r, ok)
						}
					}
					if !tt.HasText("Budget: 30 s per execution · 100,000 attempts maximum · 1–200 saved requests") {
						t.Fatal("fixed execution budget missing")
					}
					before := a.loadRunner.users
					if a.loadRunner.running {
						tt.Click("Load users")
						tt.Type("9")
						if a.loadRunner.users != before {
							t.Fatal("active inputs remained editable")
						}
					}
					tt.Click("Close")
					tt.Frame()
					if a.loadRunner.open || a.loadRunner.running != (state == "running" || state == "stopping") {
						t.Fatal("Close cancelled a load or failed to dismiss")
					}
				})
			}
		}
	}
}

func TestLoadRunnerSnapshotsSavedInputs(t *testing.T) {
	a := workspaceApp()
	a.globals = map[string]string{"base": "original"}
	a.folders = []request.Folder{{Path: "Shop", Variables: []request.Variable{{Name: "folder", Initial: "original"}}}}
	a.requests = []savedRequest{{ID: "saved", Name: "Saved", Collection: "Shop", Form: []request.Field{{Name: "body", Value: "original"}}, HeaderList: []request.Field{{Name: "X-Test", Value: "original"}}}}
	a.vault.loaded = true
	a.vault.secrets = vault.Secrets{{Name: "token", Value: "original", Domains: []string{"allowed.test"}}}
	steps := a.loadRunnerSteps("Shop", "chosen")
	a.requests[0].Form[0].Value, a.requests[0].HeaderList[0].Value = "edited", "edited"
	a.folders[0].Variables[0].Initial, a.globals["base"] = "edited", "edited"
	a.vault.secrets[0].Value, a.vault.secrets[0].Domains[0] = "edited", "other.test"
	if len(steps) != 1 {
		t.Fatal("saved request missing")
	}
	s := steps[0]
	secret, err := s.Context.Vault("token", "allowed.test")
	if err != nil || secret != "original" || s.Request.Form[0].Value != "original" || s.Request.HeaderList[0].Value != "original" || s.Context.Folders[0].Variables[0].Value() != "original" || s.Context.Globals["base"] != "original" || s.Context.Environment != "chosen" || s.Context.ScriptVault || s.Context.Tokens != nil {
		t.Fatalf("snapshot retained live state: %+v; vault error %v", s.Context, err)
	}
	a.vault.loaded = false
	if got := a.loadRunnerSteps("Shop", "chosen"); got[0].Context.Vault != nil {
		t.Fatal("a locked Vault was inherited/unlocked")
	}
}

func TestLoadRunnerLocalStackIsolatedAndVolatile(t *testing.T) {
	const users = 8
	seen := make(chan string, users)
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		seen <- r.Header.Get("X-Snapshot") + "|" + r.Header.Get("X-User")
		fmt.Fprint(w, "ok")
	}))
	defer upstream.Close()
	a := workspaceApp()
	a.srv = requestProxy(t)
	a.activeEnv = "other"
	a.envs = []environment{{Name: "chosen", Vars: map[string]string{"value": "shared"}, Current: map[string]string{"value": "private"}}, {Name: "other", Vars: map[string]string{"value": "wrong"}}}
	a.globals = map[string]string{"global": "kept"}
	a.requests = []savedRequest{{ID: "one", Name: "One", Collection: "Load", Method: "GET", URL: upstream.URL, Headers: "X-Snapshot: {{value}}\nX-User: {{counter}}", Pre: `pm.environment.set("counter", Number(pm.environment.get("counter") || 0) + 1); pm.globals.set("global", "worker");`, Post: `pm.test("ok", () => pm.response.to.have.status(200)); pm.execution.setNextRequest(null);`}}
	a.openLoadRunner()
	a.loadRunner.collection, a.loadRunner.environment, a.loadRunner.users = "Load", "chosen", fmt.Sprint(users)
	a.startLoadRunner()
	if !a.loadRunner.running || cap(a.loadRunner.events) != 1 {
		t.Fatal("load did not start with bounded mailbox")
	}
	t.Cleanup(a.closeLoadRunner)
	// No UI draining until all workers finish: final delivery cannot block.
	awaitLoadRunner(t, a)
	for range users {
		select {
		case got := <-seen:
			if got != "private|1" {
				t.Fatalf("environment selection or user isolation lost: %q", got)
			}
		default:
			t.Fatalf("worker finished without all expected HTTP requests: %+v", a.loadRunner.report)
		}
	}
	m := a.loadRunner.report.Total
	if a.loadRunner.report.Reason != "finished" || m.Completed != users || m.Failed != 0 || m.HTTPResponses != users || m.LatencySamples != users {
		t.Fatalf("wrong final aggregate: %+v", a.loadRunner.report)
	}
	if a.envs[0].Current["value"] != "private" || a.envs[0].Vars["counter"] != "" || a.globals["global"] != "kept" || len(a.experiments.log.Records) != 0 || len(a.flows) != 2 || len(a.pending) != 0 {
		t.Fatal("load persisted state, experiments or capture rows")
	}
	select {
	case <-a.srv.Flows():
		t.Fatal("load published a capture record")
	default:
	}
}

func TestLoadRunnerStopAndQuitJoinWithoutDraining(t *testing.T) {
	for _, quit := range []bool{false, true} {
		t.Run(fmt.Sprintf("quit=%t", quit), func(t *testing.T) {
			started, release := make(chan struct{}), make(chan struct{})
			var once sync.Once
			upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				once.Do(func() { close(started) })
				select {
				case <-release:
				case <-r.Context().Done():
				}
			}))
			defer upstream.Close()
			defer close(release)
			a := workspaceApp()
			a.srv = requestProxy(t)
			a.requests = []savedRequest{{ID: "slow", Name: "Slow", Method: "GET", URL: upstream.URL}}
			a.openLoadRunner()
			tt := ui.NewTester(a.view, 960, 600)
			if err := tt.Click("Start load test"); err != nil {
				t.Fatal(err)
			}
			t.Cleanup(a.closeLoadRunner)
			select {
			case <-started:
			case <-time.After(time.Minute):
				t.Fatal("load request did not start")
			}
			if quit {
				tt.Click("Close")
				if !a.loadRunner.running || a.loadRunner.open {
					t.Fatal("Close did not leave worker active")
				}
				// The Quit hook joins while the UI is not pumping frames.
				joined := make(chan struct{})
				go func() { a.closeLoadRunner(); close(joined) }()
				select {
				case <-joined:
				case <-time.After(time.Minute):
					t.Fatal("Quit depended on UI draining")
				}
			} else if err := tt.Click("Stop"); err != nil {
				t.Fatal(err)
			}
			awaitLoadRunner(t, a)
			if a.loadRunner.report.Reason != "cancelled" || a.loadRunner.report.Total.Interrupted != 1 || a.loadRunner.report.Total.Failed != 0 {
				t.Fatalf("cancelled attempt was not accounted for: %+v", a.loadRunner.report)
			}
		})
	}
}

func TestLoadRunnerMutualExclusionAndHTTPOnly(t *testing.T) {
	a := workspaceApp()
	a.runner.running = true
	a.openLoadRunner()
	if a.loadRunner.open {
		t.Fatal("load opened during ordinary Runner")
	}
	a.runner.running = false
	a.loadRunner.running = true
	a.openRunner()
	a.startRunner()
	if a.runner.running {
		t.Fatal("ordinary Runner started during load")
	}
	a.loadRunner.running = false
	a.srv = requestProxy(t)
	a.requests = []savedRequest{{Method: "WS", URL: "ws://invalid.test"}}
	a.openLoadRunner()
	a.startLoadRunner()
	if a.loadRunner.running || !strings.Contains(a.loadRunner.status, "HTTP(S)") {
		t.Fatal("WebSocket load was not rejected")
	}
	a.loadRunner.open = false
	a.control = &desktopControl{jobs: map[string]*controlJob{"active": {}}}
	a.openLoadRunner()
	a.startLoadRunner()
	a.startRunner()
	if a.loadRunner.open || a.loadRunner.running || a.runner.running {
		t.Fatal("native Runner or load bypassed remote run exclusion")
	}
	a.openRunner()
	tt := ui.NewTester(a.view, 960, 600)
	if err := tt.Click("Load test…"); err != nil {
		t.Fatal(err)
	}
	if a.loadRunner.open {
		t.Fatal("load entry stayed enabled during remote run")
	}
}

func TestLoadRunnerDurationKeepsLatestFinalWithoutUIDrain(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { fmt.Fprint(w, "ok") }))
	defer upstream.Close()
	a := workspaceApp()
	a.srv = requestProxy(t)
	a.requests = []savedRequest{{ID: "repeat", Method: "GET", URL: upstream.URL}}
	a.openLoadRunner()
	a.loadRunner.duration = "1s"
	a.startLoadRunner()
	t.Cleanup(a.closeLoadRunner)
	// Real duration causes multiple aggregate snapshots; leave the one-slot
	// mailbox undrained until return. Progress must not displace the final.
	awaitLoadRunner(t, a)
	m := a.loadRunner.report.Total
	if a.loadRunner.report.Reason != "duration" || m.Attempts < 1 || m.Completed+m.Skipped+m.Interrupted != m.Attempts || m.Failed > m.Completed {
		t.Fatalf("final duration aggregate lost: %+v", a.loadRunner.report)
	}
}

func TestLoadRunnerStopOnFailedCheckAndStaleGeneration(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { fmt.Fprint(w, "ok") }))
	defer upstream.Close()
	a := workspaceApp()
	a.srv = requestProxy(t)
	a.requests = []savedRequest{{Method: "GET", URL: upstream.URL, Checks: request.Checks{Status: "201"}}}
	a.openLoadRunner()
	a.loadRunner.stopOnFailure = true
	a.startLoadRunner()
	t.Cleanup(a.closeLoadRunner)
	awaitLoadRunner(t, a)
	if a.loadRunner.report.Reason != "failure" || a.loadRunner.report.Total.Failed != 1 || a.loadRunner.report.Total.Completed != 1 {
		t.Fatalf("failed check did not stop load: %+v", a.loadRunner.report)
	}
	final := a.loadRunner.report
	a.loadRunner.events = make(chan loadRunnerEvent, 1)
	a.loadRunner.events <- loadRunnerEvent{generation: a.loadRunner.generation - 1, report: runner.LoadReport{Done: true, Reason: "cancelled"}}
	a.drainLoadRunner()
	if a.loadRunner.report.Reason != final.Reason || a.loadRunner.report.Total != final.Total {
		t.Fatal("stale generation overwrote final aggregates")
	}
}
