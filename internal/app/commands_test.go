package app

import (
	"testing"

	"github.com/baba983/vscroxy/internal/capture"
	"github.com/baba983/vscroxy/locales"
	"github.com/egoist/mygo/ui"
)

// commandTestState is one representative context for the command table. The
// snapshot must answer the same question for every menu, button and shortcut
// that asks for an id in that state.
type commandTestState struct {
	name  string
	setup func(t *testing.T, a *app)
}

func startedTestCapture(t *testing.T) *capture.Server {
	t.Helper()
	srv, err := capture.New("127.0.0.1:0", t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { srv.Close() })
	if err := srv.Start(); err != nil {
		t.Fatal(err)
	}
	return srv
}

func captureTestState(t *testing.T, a *app, srv *capture.Server) {
	t.Helper()
	a.srv = srv
	a.page = pageLive
	a.applyFilter()
	a.picked = 0
	a.chosen.Add(a.flows[0].id)
	a.syncDocument()
}

func openTestDraft(a *app) *requestDocument {
	openDraft(a, a.flows[0])
	a.page = pageRequest
	return a.requestDocument
}

func commandTestStates() []commandTestState {
	return []commandTestState{
		{"capture stopped", func(t *testing.T, a *app) {}},
		{"capturing", func(t *testing.T, a *app) {
			captureTestState(t, a, startedTestCapture(t))
		}},
		{"no document", func(t *testing.T, a *app) {
			a.page, a.requestDocument = pageRequest, nil
		}},
		{"editing", func(t *testing.T, a *app) { openTestDraft(a) }},
		{"sending", func(t *testing.T, a *app) {
			d := openTestDraft(a)
			d.composeWaiting = true
			d.sendCancel = func() {}
		}},
		{"detached inspector", func(t *testing.T, a *app) {
			captureTestState(t, a, startedTestCapture(t))
			a.inspectorDetached = true
		}},
		// Undo and redo share the Z/Y keys with nothing else. After one
		// undo both commands are enabled, so the conflict check sees their
		// keys in the same pass.
		{"undo and redo", func(t *testing.T, a *app) {
			a.deleteFlows([]flow{a.flows[0]})
			a.deleteFlows([]flow{a.flows[0]})
			a.undo()
		}},
	}
}

// commandTestApp frames once to get a real window context, then applies the
// state and starts a new pass so the commands are built from it.
func commandTestApp(t *testing.T, s commandTestState) (*app, *ui.Context) {
	t.Helper()
	a := workspaceApp()
	var c *ui.Context
	tt := ui.NewTester(func(ctx *ui.Context) {
		c = ctx
		a.view(ctx)
	}, 1280, 800)
	tt.Frame()
	s.setup(t, a)
	a.builds++
	return a, c
}

// TestKeybindingOwnersAreUnique is the registry's answer to VS Code's
// KeybindingResolver check: Start/Stop and Dock/Detach share a key, and their
// preconditions must leave at most one owner in every context.
func TestKeybindingOwnersAreUnique(t *testing.T) {
	for _, s := range commandTestStates() {
		t.Run(s.name, func(t *testing.T) {
			a, c := commandTestApp(t, s)
			actions := a.actions(c)
			if s.name == "undo and redo" {
				for _, id := range []string{"edit.undo", "edit.redo"} {
					if !pick(actions, id).enabled {
						t.Errorf("%s is disabled with an undo and a redo available", id)
					}
				}
			}
			owners := make(map[shortcut][]string)
			for _, x := range actions {
				if !x.enabled {
					continue
				}
				for _, key := range []shortcut{x.shortcut, x.altShortcut} {
					if key.key != 0 {
						owners[key] = append(owners[key], x.id)
					}
				}
			}
			for key, ids := range owners {
				if len(ids) > 1 {
					t.Errorf("%s has %d enabled owners: %v", key, len(ids), ids)
				}
			}
		})
	}
}

func flatActions(items []action) []action {
	var out []action
	for _, x := range items {
		if len(x.items) > 0 {
			out = append(out, flatActions(x.items)...)
			continue
		}
		out = append(out, x)
	}
	return out
}

// checkShared compares only the ids a menu takes from the registry. Row and
// column menus own their targets, so their conditions are a different matter.
func checkShared(t *testing.T, state string, global, items []action, ids ...string) {
	t.Helper()
	for _, id := range ids {
		want := pick(global, id)
		for _, x := range items {
			if x.id != id {
				continue
			}
			if x.enabled != want.enabled {
				t.Errorf("%s: menu %q enabled=%v, registry enabled=%v", state, id, x.enabled, want.enabled)
			}
		}
	}
}

// TestSharedMenusFollowRegistry checks menus that ask for global ids: a menu
// item must never stay enabled when the same command's shortcut and palette
// entry are disabled.
func TestSharedMenusFollowRegistry(t *testing.T) {
	for _, s := range commandTestStates() {
		t.Run(s.name, func(t *testing.T) {
			a, c := commandTestApp(t, s)
			global := a.actions(c)
			checkShared(t, s.name, global, flatActions(a.moreActions(c)),
				"network.client", "network.devices", "capture.export-csv", "capture.filter", "app.settings")
			checkShared(t, s.name, global, a.captureActions(c),
				"capture.stop", "capture.start", "tools.rules", "collection.record", "capture.filter", "edit.undo")
			if d := a.requestDocument; d != nil {
				checkShared(t, s.name, global, a.documentActions(c, d),
					"request.duplicate", "request.export-http", "request.code", "collection.settings")
			}
			if f, ok := a.current(); ok {
				checkShared(t, s.name, global, a.inspectorActions(c, f), "inspector.detach", "inspector.dock")
			}
		})
	}
}

// TestDisabledSharedCommandsExplainWhy keeps the explanations of the ids that
// moved from local menus into the registry: a disabled menu item still has to
// say what the reader can do to enable it.
func TestDisabledSharedCommandsExplainWhy(t *testing.T) {
	t.Run("no server", func(t *testing.T) {
		a, c := commandTestApp(t, commandTestState{
			name: "capture stopped", setup: func(t *testing.T, a *app) {},
		})
		for _, id := range []string{"network.client", "network.devices"} {
			x := pick(a.actions(c), id)
			if x.enabled || x.reason != locales.StartCaptureFirst() {
				t.Errorf("%s reason = %q, want %q", id, x.reason, locales.StartCaptureFirst())
			}
		}
	})
	t.Run("unsaved request", func(t *testing.T) {
		a, c := commandTestApp(t, commandTestState{
			name: "editing", setup: func(t *testing.T, a *app) { openTestDraft(a) },
		})
		d := a.requestDocument
		if x := pick(a.documentActions(c, d), "collection.settings"); x.enabled || x.reason != locales.SaveRequestToCollectionFirst() {
			t.Errorf("collection.settings enabled=%v reason = %q, want %q", x.enabled, x.reason, locales.SaveRequestToCollectionFirst())
		}
	})
}

// Send keeps the primary button's face while capture is stopped: a click
// explains why nothing was sent and offers to start the capture, rather than
// leaving a disabled button that looks broken. A send already waiting is the
// one state that disables it.
func TestSendOffersToStartCaptureWhenStopped(t *testing.T) {
	a := workspaceApp()
	d := openTestDraft(a)
	var c *ui.Context
	tt := ui.NewTester(func(ctx *ui.Context) { c = ctx; a.view(ctx) }, 1280, 800)
	tt.SetLocale("en-US")
	tt.Frame()
	send := pick(a.actions(c), "request.send")
	if !send.enabled {
		t.Fatal("Send is disabled while a draft is editable and the capture is stopped")
	}
	send.run()
	if d.composeWaiting || d.sendOutcome != "" {
		t.Fatal("Send sent the request without a capture")
	}
	notices := a.notificationItems()
	if len(notices) != 1 || notices[0].action != locales.StartCapture() || notices[0].run == nil {
		t.Fatalf("Send's notice does not offer Start capture: %+v", notices)
	}
	d.composeWaiting = true
	a.builds++
	if pick(a.actions(c), "request.send").enabled {
		t.Fatal("Send stays enabled while a send is already waiting")
	}
}
