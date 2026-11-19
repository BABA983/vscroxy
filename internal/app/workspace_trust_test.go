package app

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"slices"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/baba983/vscroxy/internal/request"
	"github.com/baba983/vscroxy/internal/runner"
	"github.com/baba983/vscroxy/locales"
	"github.com/egoist/mygo/ui"
)

// trustTestApp is a workspace whose library can come from a project folder.
// The settings path is real so an answer can be reloaded from disk.
func trustTestApp(t *testing.T, project string) *app {
	t.Helper()
	a := workspaceApp()
	a.settingsPath = filepath.Join(t.TempDir(), "settings.json")
	a.projectDir = project
	a.srv = requestProxy(t)
	return a
}

func trustUpstream(t *testing.T, calls *atomic.Int32) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		fmt.Fprint(w, "ok")
	}))
}

func trustScriptRequest(url string) request.Request {
	return request.Request{Method: "GET", Collection: "Shop", URL: url,
		Pre:  `pm.environment.set("pre", "ran")`,
		Post: `pm.environment.set("post", "ran")`,
	}
}

// countedPermission is the execution seam: only an allowed decision lets a
// script run, so the count says which sends actually executed their code.
func countedPermission(a *app, allowed *atomic.Int32) request.ProjectScriptPermission {
	return func(ctx context.Context, dir string) bool {
		ok := a.projectScriptPermission(ctx, dir)
		if ok {
			allowed.Add(1)
		}
		return ok
	}
}

func awaitTrustPrompt(t *testing.T, tt *ui.Tester) {
	t.Helper()
	for range 30 {
		tt.Frame()
		if tt.HasText(locales.WorkspaceTrustPrompt()) {
			return
		}
	}
	t.Fatalf("trust prompt never drew: %v", tt.Texts())
}

// runWhilePrompting answers the queued prompt while work waits on it. The
// frames run on this goroutine and the send on another: a wait can never hold
// the UI thread.
func runWhilePrompting(t *testing.T, tt *ui.Tester, answer string, work func()) {
	t.Helper()
	done := make(chan struct{})
	go func() {
		defer close(done)
		work()
	}()
	awaitTrustPrompt(t, tt)
	if err := tt.Click(answer); err != nil {
		t.Fatal(err)
	}
	<-done
}

func TestProjectScriptPromptOnceAndRememberedAfterReload(t *testing.T) {
	var served, scripts atomic.Int32
	upstream := trustUpstream(t, &served)
	defer upstream.Close()
	project := t.TempDir()
	a := trustTestApp(t, project)
	tt := ui.NewTester(a.view, 1180, 760)
	r := trustScriptRequest(upstream.URL)
	in := a.sendContext(r)
	in.ScriptPermission = countedPermission(a, &scripts)
	var out request.Execution
	runWhilePrompting(t, tt, locales.Trust(), func() {
		out = r.Execute(t.Context(), a.srv, in, map[string]string{}, nil)
	})
	if out.Err != nil || out.Response == nil || served.Load() != 1 {
		t.Fatalf("send = %+v, served %d", out, served.Load())
	}
	if out.Vars["pre"] != "ran" || out.Vars["post"] != "ran" || scripts.Load() != 1 {
		t.Fatalf("trusted scripts did not run: vars %v, count %d", out.Vars, scripts.Load())
	}
	if a.trustPrompts.Load() != 1 {
		t.Fatalf("prompted %d times", a.trustPrompts.Load())
	}
	// The answer is in the same settings file the app reloads at launch.
	reloaded, err := loadSettings(a.settingsPath)
	if err != nil {
		t.Fatal(err)
	}
	if !containsTrust(reloaded.TrustedProjects, project) {
		t.Fatalf("trusted paths = %v", reloaded.TrustedProjects)
	}
	fromFile := workspaceApp()
	fromFile.trustedProjects = reloaded.TrustedProjects
	if allow, decided := fromFile.trustDecision(project); !decided || !allow {
		t.Fatalf("reloaded decision = %v, %v", allow, decided)
	}
	// A second send uses the remembered answer without drawing again.
	second := r.Execute(t.Context(), a.srv, in, map[string]string{}, nil)
	if second.Err != nil || second.Vars["pre"] != "ran" || second.Vars["post"] != "ran" {
		t.Fatalf("second send = %+v", second)
	}
	if a.trustPrompts.Load() != 1 || scripts.Load() != 2 || served.Load() != 2 {
		t.Fatalf("prompts %d, scripts %d, served %d", a.trustPrompts.Load(), scripts.Load(), served.Load())
	}
}

func containsTrust(paths []string, dir string) bool {
	return slices.Contains(paths, trustPath(dir))
}

func TestUntrustedProjectSendsWithoutScriptsAndShowsNotice(t *testing.T) {
	var served, scripts atomic.Int32
	upstream := trustUpstream(t, &served)
	defer upstream.Close()
	project := t.TempDir()
	a := trustTestApp(t, project)
	a.untrustedProjects = []string{trustPath(project)}
	r := trustScriptRequest(upstream.URL)
	in := a.sendContext(r)
	in.ScriptPermission = countedPermission(a, &scripts)
	out := r.Execute(t.Context(), a.srv, in, map[string]string{}, nil)
	if out.Err != nil || out.Response == nil || served.Load() != 1 {
		t.Fatalf("untrusted send did not reach the server: %+v, served %d", out, served.Load())
	}
	if !out.ScriptsSkipped || out.Vars["pre"] != "" || out.Vars["post"] != "" || scripts.Load() != 0 {
		t.Fatalf("scripts ran for an untrusted project: skipped=%v vars=%v count=%d", out.ScriptsSkipped, out.Vars, scripts.Load())
	}
	if summary := runSummary(out); !strings.Contains(summary, locales.ScriptsSkippedRestrictedMode()) {
		t.Fatalf("visible summary = %q", summary)
	}
	if a.trustPrompts.Load() != 0 {
		t.Fatalf("a known untrusted project prompted %d times", a.trustPrompts.Load())
	}
}

func TestLibraryCollectionAlwaysRunsItsScripts(t *testing.T) {
	var served, prompts atomic.Int32
	upstream := trustUpstream(t, &served)
	defer upstream.Close()
	a := trustTestApp(t, "")
	r := trustScriptRequest(upstream.URL)
	in := a.sendContext(r)
	in.ScriptPermission = func(ctx context.Context, dir string) bool {
		prompts.Add(1)
		return a.projectScriptPermission(ctx, dir)
	}
	out := r.Execute(t.Context(), a.srv, in, map[string]string{}, nil)
	if out.Err != nil || out.Vars["pre"] != "ran" || out.Vars["post"] != "ran" || out.ScriptsSkipped {
		t.Fatalf("library send = %+v", out)
	}
	if prompts.Load() != 0 || a.trustPrompts.Load() != 0 || served.Load() != 1 {
		t.Fatalf("prompts %d/%d, served %d", prompts.Load(), a.trustPrompts.Load(), served.Load())
	}
}

func TestTwoProjectsKeepSeparateDecisions(t *testing.T) {
	var served atomic.Int32
	upstream := trustUpstream(t, &served)
	defer upstream.Close()
	trusted, restricted := t.TempDir(), t.TempDir()
	a := trustTestApp(t, trusted)
	a.trustedProjects, a.untrustedProjects = []string{trustPath(trusted)}, []string{trustPath(restricted)}
	for _, tc := range []struct {
		project string
		wantRan bool
	}{
		{trusted, true},
		{restricted, false},
	} {
		a.projectDir = tc.project
		r := trustScriptRequest(upstream.URL)
		in := a.sendContext(r)
		out := r.Execute(t.Context(), a.srv, in, map[string]string{}, nil)
		if out.Err != nil || out.Response == nil {
			t.Fatalf("%s: %+v", tc.project, out)
		}
		if ran := out.Vars["pre"] == "ran"; ran != tc.wantRan || out.ScriptsSkipped == tc.wantRan {
			t.Errorf("%s: scripts ran %v, skipped %v", tc.project, ran, out.ScriptsSkipped)
		}
	}
	if served.Load() != 2 || a.trustPrompts.Load() != 0 {
		t.Fatalf("served %d, prompted %d", served.Load(), a.trustPrompts.Load())
	}
}

func TestRevokingTrustPromptsAgain(t *testing.T) {
	var served atomic.Int32
	upstream := trustUpstream(t, &served)
	defer upstream.Close()
	project := t.TempDir()
	a := trustTestApp(t, project)
	key := trustPath(project)
	a.trustPending, a.trustAskDir = map[string]chan struct{}{key: make(chan struct{})}, key
	a.answerTrust(true)
	if allow, decided := a.trustDecision(project); !decided || !allow {
		t.Fatal("the first answer was not remembered")
	}
	a.revokeTrust(project)
	if _, decided := a.trustDecision(project); decided {
		t.Fatal("revocation left a decision behind")
	}
	tt := ui.NewTester(a.view, 1180, 760)
	r := trustScriptRequest(upstream.URL)
	in := a.sendContext(r)
	var out request.Execution
	runWhilePrompting(t, tt, locales.DontTrust(), func() {
		out = r.Execute(t.Context(), a.srv, in, map[string]string{}, nil)
	})
	if !out.ScriptsSkipped || out.Response == nil || out.Vars["pre"] != "" || served.Load() != 1 {
		t.Fatalf("revoked project send = %+v, served %d", out, served.Load())
	}
	if a.trustPrompts.Load() != 1 || !containsTrust(a.untrustedProjects, project) {
		t.Fatalf("prompts %d, untrusted %v", a.trustPrompts.Load(), a.untrustedProjects)
	}
}

func TestCollectionRunAsksOnlyOnce(t *testing.T) {
	var served, scripts atomic.Int32
	upstream := trustUpstream(t, &served)
	defer upstream.Close()
	project := t.TempDir()
	a := trustTestApp(t, project)
	tt := ui.NewTester(a.view, 1180, 760)
	steps := make([]runner.Step, 4)
	for i := range steps {
		r := trustScriptRequest(fmt.Sprintf("%s/%d", upstream.URL, i))
		in := a.sendContext(r)
		in.ScriptPermission = countedPermission(a, &scripts)
		steps[i] = runner.Step{Request: r, Context: in}
	}
	type runResult struct {
		summary runner.Summary
		err     error
	}
	done := make(chan runResult, 1)
	go func() {
		summary, err := runner.Run(t.Context(), a.srv, steps, runner.Options{Iterations: 2, MaxSteps: 100}, func(result runner.Result) error { return nil })
		done <- runResult{summary, err}
	}()
	awaitTrustPrompt(t, tt)
	if err := tt.Click(locales.Trust()); err != nil {
		t.Fatal(err)
	}
	got := <-done
	if got.err != nil || got.summary.Completed != 8 || got.summary.Failed != 0 {
		t.Fatalf("run = %+v, %v", got.summary, got.err)
	}
	if served.Load() != 8 || scripts.Load() != 8 {
		t.Fatalf("served %d, executed scripts %d", served.Load(), scripts.Load())
	}
	if a.trustPrompts.Load() != 1 {
		t.Fatalf("four requests and two iterations prompted %d times", a.trustPrompts.Load())
	}
}
