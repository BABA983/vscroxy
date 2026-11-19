package app

import (
	"runtime"
	"slices"
	"sync"

	"github.com/baba983/vscroxy/locales"
	"github.com/egoist/mygo/ui"
	"github.com/egoist/mygo/ui/editor"
)

// commandContext is one build's view of the app: the current document and
// flow, focus targets bound to the current ui.Context, and the flags a
// command's precondition reads. VS Code uses context keys for this; here the
// snapshot is an ordinary value, made once per pass and shared by every
// command built in that pass. It holds no build-time UI value: only closures
// used within the pass.
type commandContext struct {
	a          *app
	d          *requestDocument
	f          flow
	current    bool
	capturing  bool
	editing    bool
	tab        bool
	proxyReady bool
	// Derived labels and targets are read from the snapshot, not recomputed
	// by each menu, button and shortcut subscription in the same frame.
	searchLabel string
	search      func()
	// find is the focused find bar: the body's or the Search view's. Both
	// expose the same three options, so Alt+C/W/R has one owner.
	find           findTarget
	focusSearch    func()
	focusTraffic   func()
	focusInspector func()
	focusURL       func()
	focusLabel     string
	primaryLabel   string
	recordingLabel string
	breakLabel     string
	collectionRoot string
}

func commandSnapshot(a *app, c *ui.Context, target *requestDocument) *commandContext {
	w := &a.workbench
	f, current := a.current()
	d := a.requestDocument
	if target != nil {
		d = target
	}
	capturing := a.page.capture()
	proxyReady := a.srv != nil && a.srv.Running()
	// A captured request in LIVE is a document too; tabs are COLLECTIONS'.
	editing := d != nil && (a.page == pageLive || a.page == pageRequest)
	x := &commandContext{
		a: a, d: d, f: f, current: current, capturing: capturing,
		editing: editing, tab: editing && !capturing, proxyReady: proxyReady,
		focusSearch:    func() { a.focusSearch(c) },
		focusTraffic:   func() { a.focusTraffic(c) },
		focusInspector: func() { a.workbench.inspector.Focus(c) },
		focusURL:       func() { d.urlInput.Focus(c) },
		searchLabel:    locales.SearchHostOrPath(),
		focusLabel:     locales.FocusInspector(),
		primaryLabel:   locales.FocusRequestList(),
		recordingLabel: locales.PauseCapture(),
		breakLabel:     a.breakActionLabel(),
	}
	if w.focused {
		x.focusLabel = locales.BackToList()
	}
	if a.recordingPaused {
		x.recordingLabel = locales.ResumeCapture()
	}
	if x.tab {
		x.primaryLabel = locales.FocusRequestURL()
	}
	x.search = func() { a.search.Focus(c) }
	for side := range 2 {
		if c != nil && capturing && a.bodyText[side] != nil && a.bodyReader[side].FocusWithin(c) {
			x.searchLabel = locales.FindInBodyLabel()
			x.search = func() { a.bodyText[side].Run(editor.Find) }
		}
		if c != nil {
			state := &a.bodyFind[side]
			if state.find != nil && state.handle.FocusWithin(c) {
				x.find = state
			}
		}
	}
	if x.find == nil && c != nil && a.page == pageSearch && a.searchAll.handle.FocusWithin(c) {
		x.find = &a.searchAll
	}
	if d != nil {
		x.collectionRoot = collectionRoot(d.composeCollection)
	}
	return x
}

// command is a command definition. The registry is built once; a command's
// preconditions and title read the context snapshot supplied at build time,
// never a cached element or a captured widget. source contributes the actions
// of a group whose members are already built by a target-specific builder.
type command struct {
	id, label, group, reason string
	title                    func(*commandContext) string
	enabled                  func(*commandContext) bool
	checked                  func(*commandContext) check
	key                      shortcut
	run                      func(*commandContext)
	source                   func(*commandContext, *ui.Context) []action
}

type commandRegistry struct {
	entries []*command
	ids     map[string]bool
}

// globalCommands is VS Code's CommandsRegistry, KeybindingsRegistry and
// MenuRegistry for the app's global commands in one table: an id is defined
// once here, and every menu, button and shortcut asks for that id. It is
// built on first use: the commands themselves reference views that read it.
var (
	commandsOnce  sync.Once
	commandsTable *commandRegistry
)

func globalCommands() *commandRegistry {
	commandsOnce.Do(func() { commandsTable = registerGlobalCommands() })
	return commandsTable
}

func newCommandRegistry() *commandRegistry {
	return &commandRegistry{ids: make(map[string]bool)}
}

func (r *commandRegistry) add(id, label string, run func(*commandContext)) *command {
	if r.ids[id] {
		panic("app: command registered twice: " + id)
	}
	r.ids[id] = true
	c := &command{id: id, label: label, run: run}
	r.entries = append(r.entries, c)
	return c
}

// source contributes a group whose ids belong to one target-specific
// builder, such as the open protocol's commands. They are still global
// commands: ids are checked for duplicates when the list is built.
func (r *commandRegistry) source(name string, build func(*commandContext, *ui.Context) []action) {
	r.add("@"+name, "", nil).source = build
}

func (c *command) in(group string) *command { c.group = group; return c }

func (c *command) keys(mods ui.Modifiers, key ui.Key) *command {
	c.key = shortcut{mods, key}
	return c
}

func (c *command) when(ok func(*commandContext) bool, reason string) *command {
	c.enabled, c.reason = ok, reason
	return c
}

func fixed(ok bool) func(*commandContext) bool { return func(*commandContext) bool { return ok } }

func (c *command) named(title func(*commandContext) string) *command {
	c.title = title
	return c
}

func (c *command) mark(mark func(*commandContext) check) *command {
	c.checked = mark
	return c
}

// keybindingOf is what the native menu bar needs: an accelerator belongs to
// an id, not to whichever action list the menu happens to be showing.
func keybindingOf(id string) shortcut {
	for _, c := range globalCommands().entries {
		if c.id == id {
			return c.key
		}
	}
	return shortcut{}
}

// build creates this pass's actions in registry order. Definitions occur once
// in the table; grouped builders may contribute several actions for one id.
func (x *commandContext) build(c *ui.Context) []action {
	entries := globalCommands().entries
	actions := make([]action, 0, len(entries))
	for _, def := range entries {
		if def.source != nil {
			// A grouped builder may repeat an id for different targets,
			// such as one per host profile. The ids themselves are each
			// defined in exactly one place.
			actions = append(actions, def.source(x, c)...)
			continue
		}
		ok := true
		if def.enabled != nil {
			ok = def.enabled(x)
		}
		label := def.label
		if def.title != nil {
			label = def.title(x)
		}
		x0 := action{id: def.id, label: label, group: def.group, shortcut: def.key,
			enabled: ok, run: func() {
				// A quick-switch pick is provisional until the reader does
				// something else; every other command settles it first.
				if def.id != "request.switch-mru-next" && def.id != "request.switch-mru-previous" {
					x.a.commitMRUPick()
				}
				def.run(x)
			}}
		// Like action.when, the reason is only carried by a disabled
		// command; an enabled one has nothing to explain.
		if !ok {
			x0.reason = def.reason
		}
		if def.checked != nil {
			x0.check = def.checked(x)
		}
		actions = append(actions, x0)
	}
	return actions
}

// actions returns the active global commands. They are built once per view
// pass: the menu bar, ⋯ menus, buttons, palette and shortcut dispatcher all
// read the same list, rather than each rebuilding it from app state.
func (a *app) actions(c *ui.Context) []action {
	w := &a.workbench
	if w.commandList != nil && w.commandFrame == a.builds && w.commandFor == c.Services() {
		return w.commandList
	}
	list := commandSnapshot(a, c, nil).build(c)
	w.commandList, w.commandFrame, w.commandFor = list, a.builds, c.Services()
	return list
}

// actionsFor builds commands for a particular document. A request tab's ⋯
// menu can be open for a document other than the active one, so its target is
// the tab, not the window's current draft.
func (a *app) actionsFor(c *ui.Context, d *requestDocument) []action {
	return commandSnapshot(a, c, d).build(c)
}

func registerGlobalCommands() *commandRegistry {
	r := newCommandRegistry()
	// The inspected capture's row actions precede the app-wide ones.
	r.source("selected flows", func(x *commandContext, c *ui.Context) []action {
		return x.a.requestActions(c, x.f, x.capturing && x.current)
	})

	r.add("app.palette", locales.Commands(), func(x *commandContext) { x.a.workbench.openPalette() }).
		keys(ui.Cmd, ui.KeyK)
	r.add("notifications.show", locales.ShowNotifications(), func(x *commandContext) { x.a.openNotifications() })
	r.add("notifications.clear", locales.ClearNotifications(), func(x *commandContext) { x.a.clearNotifications() })
	r.add("problems.focus", locales.FocusProblems(), func(x *commandContext) { x.a.focusProblems() }).
		keys(ui.Cmd|ui.Shift, ui.KeyM)
	r.add("problems.next", locales.NextProblem(), func(x *commandContext) { x.a.navigateProblems(1) }).
		when(func(x *commandContext) bool { return len(x.a.visibleProblems()) > 0 }, locales.NoProblems()).
		keys(0, ui.KeyF8)
	r.add("problems.previous", locales.PreviousProblem(), func(x *commandContext) { x.a.navigateProblems(-1) }).
		when(func(x *commandContext) bool { return len(x.a.visibleProblems()) > 0 }, locales.NoProblems()).
		keys(ui.Shift, ui.KeyF8)
	r.add("view.search", "", func(x *commandContext) { x.search() }).
		named(func(x *commandContext) string { return x.searchLabel }).
		when(func(x *commandContext) bool { return x.capturing }, "").keys(ui.Cmd, ui.KeyF)
	r.add("search.focus", locales.SearchEverywhere(), func(x *commandContext) { x.focusSearch() }).
		keys(ui.Cmd|ui.Shift, ui.KeyF)
	r.add("search.next-match", locales.NextSearchMatch(), func(x *commandContext) { x.a.searchAll.step(1) })
	r.add("search.previous-match", locales.PreviousSearchMatch(), func(x *commandContext) { x.a.searchAll.step(-1) })
	// The find bar's options take Alt+C/W/R only while that bar has the
	// focus; the commands own the keys so menus and the palette see one
	// owner, and the precondition keeps them off everywhere else.
	r.add("find.match-case", "", func(x *commandContext) { toggleFindCase(x.find) }).
		when(func(x *commandContext) bool { return x.find != nil }, "").keys(ui.Alt, ui.KeyC)
	r.add("find.match-whole-word", "", func(x *commandContext) { toggleFindWord(x.find) }).
		when(func(x *commandContext) bool { return x.find != nil }, "").keys(ui.Alt, ui.KeyW)
	r.add("find.use-regexp", "", func(x *commandContext) { toggleFindRegexp(x.find) }).
		when(func(x *commandContext) bool { return x.find != nil }, "").keys(ui.Alt, ui.KeyR)
	r.add("view.primary", "", func(x *commandContext) {
		if x.tab {
			x.focusURL()
		} else {
			x.focusTraffic()
		}
	}).named(func(x *commandContext) string { return x.primaryLabel }).
		when(func(x *commandContext) bool {
			return x.capturing && len(x.a.visible) > 0 || x.editing
		}, "").keys(ui.Cmd, ui.KeyL)
	r.add("request.send", locales.SendRequest(), func(x *commandContext) { x.a.sendComposed() }).
		when(func(x *commandContext) bool {
			d := x.d
			return x.editing && !d.composeWaiting && (d.protocol == "gRPC" || d.protocol == "MQTT" || d.protocol == "MCP" || x.proxyReady)
		}, locales.OpenDraftNeedsCapture()).keys(ui.Cmd, ui.KeyEnter)
	r.add("request.cancel-send", locales.CancelSend(), func(x *commandContext) { x.d.cancelSend() }).
		when(func(x *commandContext) bool {
			d := x.d
			return x.editing && d.composeWaiting && d.sendCancel != nil && !d.sendCancelling
		}, "")
	r.add("request.duplicate", locales.DuplicateAsNewRequest(), func(x *commandContext) {
		x.a.duplicateDocument(x.d)
	}).when(func(x *commandContext) bool {
		return x.editing && !x.d.composeWaiting
	}, locales.OpenARequestDraftFirst()).keys(ui.Cmd, ui.KeyD)
	r.add("request.save", locales.SaveRequest(), func(x *commandContext) {
		if x.a.page.capture() && x.d.savedKey == "" {
			// A capture has no name or collection yet: ask for both.
			x.a.renameOpen, x.a.saveAfterRename = true, true
			return
		}
		x.a.saveComposed()
	}).when(func(x *commandContext) bool { return x.editing }, locales.OpenARequestDraftFirst()).keys(ui.Cmd, ui.KeyS)
	r.add("request.compare-source", locales.CompareWithSource(), func(x *commandContext) {
		x.d.comparing = true
		if !x.tab {
			// The comparison replaces the editor that had the focus; keep
			// it in the inspector, where Escape returns to the list.
			x.focusInspector()
		}
	}).when(func(x *commandContext) bool {
		d := x.d
		return x.editing && d.source != nil && d.response != nil && !d.composeWaiting
	}, locales.SendDraftBeforeCompare()).keys(ui.Cmd|ui.Shift, ui.KeyD)
	r.add("request.close", locales.CloseRequest(), func(x *commandContext) { x.a.requestClose(x.d) }).
		when(func(x *commandContext) bool { return x.tab && !x.d.composeWaiting }, locales.OpenARequestDraftWait()).keys(ui.Cmd, ui.KeyW)
	r.add("request.close-others", locales.CloseOtherRequests(), func(x *commandContext) { x.a.closeOtherDocuments(x.d) }).
		when(func(x *commandContext) bool { return x.tab && len(x.a.documents) > 1 }, "")
	r.add("request.close-saved", locales.CloseSavedRequests(), func(x *commandContext) { x.a.closeSavedDocuments() }).
		when(func(x *commandContext) bool {
			return slices.ContainsFunc(x.a.documents, func(d *requestDocument) bool { return d.savedKey != "" && !d.dirty() })
		}, "")
	r.add("request.reopen-closed", locales.ReopenClosedRequest(), func(x *commandContext) { x.a.reopenClosedDocument() }).
		when(func(x *commandContext) bool { return x.a.closedDocuments().Size() > 0 }, "").
		keys(ui.Cmd|ui.Shift, ui.KeyT)
	// The positional commands keep their order in menus and the palette;
	// Ctrl/Cmd+Tab belongs to VS Code's MRU quick switch instead.
	r.add("request.next-tab", locales.NextOpenRequest(), func(x *commandContext) { x.a.cycleDocument(1) }).
		when(func(x *commandContext) bool { return len(x.a.documents) > 0 }, locales.OpenARequestFirst())
	r.add("request.previous-tab", locales.PreviousOpenRequest(), func(x *commandContext) { x.a.cycleDocument(-1) }).
		when(func(x *commandContext) bool { return len(x.a.documents) > 0 }, locales.OpenARequestFirst())
	r.add("request.switch-mru-next", locales.NextRecentlyUsedRequest(), func(x *commandContext) { x.a.cycleDocumentMRU(1) }).
		when(func(x *commandContext) bool { return len(x.a.documents) > 0 }, locales.OpenARequestFirst()).keys(ui.Ctrl, ui.KeyTab)
	r.add("request.switch-mru-previous", locales.PreviousRecentlyUsedRequest(), func(x *commandContext) { x.a.cycleDocumentMRU(-1) }).
		when(func(x *commandContext) bool { return len(x.a.documents) > 0 }, locales.OpenARequestFirst()).keys(ui.Ctrl|ui.Shift, ui.KeyTab)
	r.add("capture.export-chosen", locales.ExportSelectedHar(), func(x *commandContext) { x.a.exportChosen() }).
		when(func(x *commandContext) bool { return x.capturing && x.a.chosen.Len() > 0 }, "")
	r.add("capture.export-csv", locales.ExportCsv(), func(x *commandContext) { x.a.exportCSV() }).
		when(func(x *commandContext) bool { return x.capturing && len(x.a.visible) > 0 }, locales.NoVisibleRequestsToExport())
	r.add("capture.export-chosen-csv", locales.ExportChosenCsv(), func(x *commandContext) { x.a.exportChosenCSV() }).
		when(func(x *commandContext) bool { return x.capturing && x.a.chosen.Len() > 0 }, locales.ChooseRequestsFirst())
	r.add("view.focus-inspector", "", func(x *commandContext) {
		w := &x.a.workbench
		w.focused = !w.focused
		if w.focused {
			x.focusInspector()
		} else {
			x.focusTraffic()
		}
	}).named(func(x *commandContext) string { return x.focusLabel }).
		when(func(x *commandContext) bool { return x.capturing && x.current && !x.a.inspectorDetached }, "").
		keys(ui.Cmd|ui.Shift, ui.KeyEnter)
	r.add("inspector.detach", locales.DetachInspector(), func(x *commandContext) { x.a.openInspectorWindow() }).
		when(func(x *commandContext) bool { return x.capturing && x.current && !x.a.inspectorDetached }, "").
		keys(ui.Cmd|ui.Shift, ui.KeyI)
	r.add("inspector.dock", locales.DockInspector(), func(x *commandContext) { x.a.closeInspectorWindow() }).
		when(func(x *commandContext) bool { return x.a.inspectorDetached }, "").keys(ui.Cmd|ui.Shift, ui.KeyI)
	r.add("view.follow-end", locales.FollowListEnd(), func(x *commandContext) {
		x.a.workbench.focused = false
		x.a.views.activeView().list.ScrollToEnd()
	}).when(func(x *commandContext) bool { return x.capturing && len(x.a.visible) > 0 }, "")
	r.add("capture.filter", locales.EditCaptureFilters(), func(x *commandContext) { x.a.openFilter() }).
		when(func(x *commandContext) bool { return x.capturing }, "")
	r.add("capture.saved-filters", locales.ManageSavedFilters(), func(x *commandContext) { x.a.views.openSaved(x.a) }).
		when(func(x *commandContext) bool { return x.capturing }, "")
	r.add("capture.new-view", locales.NewCaptureView(), func(x *commandContext) {
		x.a.views.capture(x.a)
		x.a.views.newView(x.a)
	}).when(func(x *commandContext) bool { return x.capturing }, "")
	r.add("capture.reset-filters", locales.ResetAllCaptureConditions(), func(x *commandContext) {
		x.a.views.resetFilters(x.a)
	}).when(func(x *commandContext) bool { return x.capturing }, "")
	r.add("capture.break", "", func(x *commandContext) {
		x.a.breakOn = !x.a.breakOn
		x.a.srv.SetBreak(x.a.breakOn)
	}).named(func(x *commandContext) string { return x.breakLabel }).
		when(func(x *commandContext) bool { return x.a.srv != nil }, "")
	r.add("capture.stop", locales.StopCapture(), func(x *commandContext) { x.a.stopCapture() }).
		when(func(x *commandContext) bool { return x.proxyReady }, locales.CaptureIsNotRunning()).keys(ui.Cmd, ui.KeyPeriod)
	r.add("capture.start", locales.StartCapture(), func(x *commandContext) {
		go guarded(func() { x.a.startCapture() })
	}).when(func(x *commandContext) bool { return x.a.srv != nil && !x.proxyReady }, locales.CaptureIsAlreadyRunning()).
		keys(ui.Cmd, ui.KeyPeriod)
	r.add("capture.recording", "", func(x *commandContext) {
		x.a.recordingPaused = !x.a.recordingPaused
	}).named(func(x *commandContext) string { return x.recordingLabel }).
		when(func(x *commandContext) bool { return x.proxyReady }, "")
	r.add("collection.record", locales.RecordIntoCollection(), func(x *commandContext) {
		x.a.openCollectionRecording("", "", "")
	})
	r.add("view.layout-right", locales.LayoutSideBySide(), func(x *commandContext) {
		w := &x.a.workbench
		w.layout, w.focused = "right", false
	}).mark(func(x *commandContext) check { return checkbox(x.a.workbench.layout == "right") })
	r.add("view.layout-bottom", locales.LayoutStacked(), func(x *commandContext) {
		w := &x.a.workbench
		w.layout, w.focused = "bottom", false
	}).mark(func(x *commandContext) check { return checkbox(x.a.workbench.layout == "bottom") })
	r.add("view.sidebar", locales.ToggleSidebar(), func(x *commandContext) {
		x.a.workbench.sidebarHidden = !x.a.workbench.sidebarHidden
	}).keys(ui.Cmd, ui.KeyB)
	r.add("flow.undo-delete", locales.Undo(), func(x *commandContext) { x.a.undoDelete() }).
		when(func(x *commandContext) bool { return len(x.a.deletions) > 0 }, locales.NothingToUndo()).keys(ui.Cmd, ui.KeyZ)
	r.add("view.layout-reset", locales.ResetDesktopLayout(), func(x *commandContext) {
		a, w := x.a, &x.a.workbench
		w.layout, w.sidebarHidden, w.focused, w.sidePercent = "right", false, false, 64
		a.sidebarWidth, a.tablePercent = 220, 44
		a.setColumns(ui.TableLayout{})
	})
	r.add("settings.https", locales.HttpsCertificateDecryptionSetup(), func(x *commandContext) { x.a.openSettings(2) })
	r.add("settings.certificates", locales.ClientCertificatesRoots(), func(x *commandContext) { x.a.openSettings(6) })
	r.add("network.diagnostics", locales.ConnectionDiagnostics(), func(x *commandContext) {
		x.a.openDiagnostics(nil, diagnoseMac)
	})
	r.add("app.setup-guide", locales.ShowSetupGuide(), func(x *commandContext) { x.a.openGuide() }).
		when(func(x *commandContext) bool { return !x.a.guide }, "")
	r.add("history.open", locales.History2(), func(x *commandContext) {
		a := x.a
		a.experiments.scope, a.experiments.draftKey, a.experiments.open = nil, "", true
		a.experiments.assetID, a.experiments.status, a.experiments.sourceFilter = "", "", ""
	})
	r.add("network.websocket", locales.WebsocketSession(), func(x *commandContext) { x.a.openSocket() })
	r.add("network.sse", locales.SseSession(), func(x *commandContext) { x.a.openSSE() })
	r.add("session.open", locales.OpenSession(), func(x *commandContext) { x.a.openSession() })
	r.add("session.save", locales.SaveCaptureToSessions(), func(x *commandContext) { x.a.saveSession() }).
		when(func(x *commandContext) bool { return x.capturing }, "")
	r.add("network.client", locales.SetUpALocalClient(), func(x *commandContext) { x.a.openClientSetup() }).
		when(func(x *commandContext) bool { return x.a.srv != nil }, locales.StartCaptureFirst())
	r.add("network.terminal", locales.OpenAProxiedTerminal(), func(x *commandContext) { x.a.openProxiedTerminal() }).
		when(func(x *commandContext) bool { return x.proxyReady }, locales.StartCaptureFirst())
	r.add("network.chrome", locales.OpenProxiedChrome(), func(x *commandContext) { x.a.openProxiedChrome() }).
		when(func(x *commandContext) bool { return x.proxyReady }, locales.StartCaptureFirst())
	r.add("network.firefox", locales.OpenProxiedFirefox(), func(x *commandContext) { x.a.openProxiedFirefox() }).
		when(func(x *commandContext) bool { return x.proxyReady }, locales.StartCaptureFirst())
	r.add("network.android", locales.SetUpAnAndroidEmulator(), func(x *commandContext) {
		x.a.openClientSetupTarget(clientTargetAndroid)
	})
	r.add("network.ios", locales.InstallCaInIosSimulator(), func(x *commandContext) {
		x.a.openClientSetupTarget(clientTargetIOS)
	}).when(fixed(runtime.GOOS == "darwin"), locales.IosSimulatorsOnlyOnMacOS())
	r.add("network.java", locales.ImportCaIntoJavaTruststore(), func(x *commandContext) {
		x.a.openClientSetupTarget(clientTargetJava)
	})
	r.add("network.electron", locales.LaunchAnElectronApp(), func(x *commandContext) {
		x.a.openClientSetupTarget(clientTargetElectron)
	})
	r.add("network.devices", locales.ConnectADevice(), func(x *commandContext) { x.a.openDevices() }).
		when(func(x *commandContext) bool { return x.a.srv != nil }, locales.StartCaptureFirst())
	r.add("tools.rules", locales.ManageDebuggingRules(), func(x *commandContext) { x.a.openRules() })

	r.source("hosts", func(x *commandContext, c *ui.Context) []action { return x.a.hostsActions() })

	r.add("vault.open", locales.Vault(), func(x *commandContext) { x.a.openVault() })
	r.add("environment.manage", locales.ManageEnvironments(), func(x *commandContext) { x.a.openVars() })
	r.add("request.code", locales.GenerateCode(), func(x *commandContext) { x.a.openCode() }).
		when(func(x *commandContext) bool { return x.editing }, locales.OpenARequestDraftFirst())
	r.add("request.export-http", locales.ExportAsHttp(), func(x *commandContext) {
		x.a.exportHTTPDocument(x.d)
	}).when(func(x *commandContext) bool { return x.editing }, locales.OpenARequestDraftFirst())
	r.add("collection.settings", locales.CollectionSettings(), func(x *commandContext) {
		x.a.openFolder(x.d.composeCollection)
	}).when(func(x *commandContext) bool { return x.editing && x.d.composeCollection != "" }, locales.SaveRequestToCollectionFirst())
	r.add("request.new", locales.NewRequest(), func(x *commandContext) { x.a.openCompose() }).keys(ui.Cmd, ui.KeyN)
	r.add("request.import-curl", locales.ImportCurl(), func(x *commandContext) { x.a.openCurlImport() })
	r.add("collection.reload", locales.ReloadCollectionsFromDisk(), func(x *commandContext) {
		x.a.loadRequestAssets()
		x.a.done(locales.ReloadedRequests(len(x.a.requests), x.a.library.root))
	})
	r.add("capture.import-session", locales.ImportCaptureSession(), func(x *commandContext) { x.a.importCaptureSession() })
	r.add("capture.import-proxyman-folder", locales.ImportProxymanLogFolder(), func(x *commandContext) { x.a.importCaptureFolder() })
	r.add("rules.import-charles", locales.ImportCharlesSettings(), func(x *commandContext) { x.a.importCharlesSettings() })
	r.add("collection.import", locales.ImportText(), func(x *commandContext) { x.a.importRequests() })
	r.add("collection.import-url", locales.ImportFromUrl(), func(x *commandContext) { x.a.openImportURL() })
	r.add("collection.import-folder", locales.ImportCollectionFolder(), func(x *commandContext) { x.a.importCollectionFolder() })
	r.add("collection.export-postman", locales.ExportCollectionAsPostman(), func(x *commandContext) {
		x.a.exportPostman(x.collectionRoot)
	}).when(func(x *commandContext) bool { return x.editing && x.d.composeCollection != "" }, "")
	r.add("collection.export-http", "", func(x *commandContext) {
		x.a.exportHTTPCollection(x.collectionRoot)
	}).named(func(x *commandContext) string { return locales.ExportCollectionAsHttp(x.collectionRoot) }).
		when(func(x *commandContext) bool { return x.editing && x.d.composeCollection != "" }, "")
	r.add("app.settings", locales.OpenSettings(), func(x *commandContext) { x.a.openSettings(-1) }).keys(ui.Cmd, ui.KeyComma)
	r.add("go.live", locales.GoToLiveTraffic(), func(x *commandContext) {
		a := x.a
		a.page = pageLive
		*a.regionOpen("traffic") = true
		x.focusTraffic()
	}).keys(ui.Cmd, ui.Key1)
	r.add("go.collections", locales.GoToCollections(), func(x *commandContext) {
		a := x.a
		a.page = pageRequest
		*a.regionOpen("collections") = true
		if a.requestDocument != nil {
			x.focusURL()
		}
	}).keys(ui.Cmd, ui.Key2)
	r.add("go.sessions", locales.GoToSessions(), func(x *commandContext) {
		a := x.a
		a.page = pageSession
		*a.regionOpen("sessions") = true
	}).keys(ui.Cmd, ui.Key3)
	r.add("go.apis", locales.ApiContracts(), func(x *commandContext) { x.a.page = pageAPI }).
		when(func(x *commandContext) bool { return len(x.a.apis) > 0 }, locales.CreateOrImportAnApi())
	r.add("navigation.back", locales.GoBack(), func(x *commandContext) {
		x.a.workbench.navigation.step(x.a, -1)
	}).when(func(x *commandContext) bool { return x.a.workbench.navigation.canGo(x.a, -1) }, "").
		keys(navigationBackKey.mods, navigationBackKey.key)
	r.add("navigation.forward", locales.GoForward(), func(x *commandContext) {
		x.a.workbench.navigation.step(x.a, 1)
	}).when(func(x *commandContext) bool { return x.a.workbench.navigation.canGo(x.a, 1) }, "").
		keys(navigationForwardKey.mods, navigationForwardKey.key)
	r.add("api.new", locales.NewApiContract(), func(x *commandContext) {
		x.a.newAPI()
		x.a.page = pageAPI
	})
	r.add("api.import-openapi", locales.ImportOpenapi(), func(x *commandContext) { x.a.importOpenAPI() })
	r.add("history.show", locales.ShowRuns(), func(x *commandContext) { x.a.showRunRecords() })
	r.add("flow.save-evidence", locales.SaveEvidenceToSessions(), func(x *commandContext) { x.a.saveEvidence(x.f) }).
		when(func(x *commandContext) bool { return x.capturing && x.current }, "")

	r.source("gRPC", func(x *commandContext, c *ui.Context) []action { return x.a.grpcActions(x.d, x.editing) })
	r.source("Socket.IO", func(x *commandContext, c *ui.Context) []action { return x.a.socketIOActions(x.d, x.editing) })
	r.source("MQTT", func(x *commandContext, c *ui.Context) []action { return x.a.mqttActions(x.d, x.editing) })
	r.source("MCP", func(x *commandContext, c *ui.Context) []action { return x.a.mcpActions(x.d, x.editing) })
	return r
}
