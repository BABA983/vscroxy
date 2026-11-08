package app

import (
	"fmt"
	"github.com/baba983/vscroxy/internal/uikit"
	"runtime"
	"time"

	"github.com/baba983/vscroxy/internal/capture"
	"github.com/baba983/vscroxy/locales"
	"github.com/egoist/mygo/ui"
)

// shell draws the window's own title bar and the footer of the workspace
// below it. The bar carries only what is true everywhere — where capture is
// listening, the layout, and the rare commands — so the screen under it
// belongs to traffic or to a request.
func (a *app) shell(c *ui.Context, t *ui.Theme) {
	bar := c.TitleBar()
	// Without a band or a rule of its own, the title bar is the top of the
	// column it heads; on macOS, beside the sidebar, the brand and the
	// window controls are at the sidebar's top. The sides share what the centered
	// status leaves; when the commands need more than half of it, the
	// status moves aside rather than under them.
	ui.Row(c).Height(max(bar.Height, uikit.HeightBar)).AlignItems(ui.Center).DragWindow().Children(func() {
		ui.Row(c).Grow(1).Basis(0).MinWidth(0).Children(func() {
			if !a.macChrome || !a.sidebarShown() {
				a.brandBar(c, t)
			}
		})
		ui.Row(c).Justify(ui.Center).MinWidth(0).Children(func() { a.proxyIndicator(c, t) })
		// Flex counts padding outside a zero basis: keep it inside, so the
		// two sides split the room evenly. Host profiles sit right beside
		// the pill, which stays centered.
		ui.Row(c).Grow(1).Basis(0).AlignItems(ui.Center).Children(func() {
			// It gives way first: its label shortens before the pill moves.
			ui.Row(c).PaddingStart(t.Space(2)).MinWidth(0).Shrink(1).Children(func() { a.hostsSwitch(c, t) })
			ui.Spacer(c)
			ui.Row(c).Shrink(0).PaddingEnd(max(bar.Right, t.Space(3))).Gap(t.Space(2)).AlignItems(ui.Center).Children(func() {
				ui.Row(c).Gap(t.Space(0.5)).AlignItems(ui.Center).Children(func() { a.layoutToggle(c) })
				if uikit.IconButton(c, locales.Commands2(), locales.SearchEveryActionCtrlCmd(), commandIcon).Clicked() {
					a.workbench.openPalette()
				}
				uikit.IconButton(c, locales.Tools(), locales.ManageDebuggingRulesAndNetwork(), toolsIcon).PopoverMenuButton(func(m *ui.Menu) {
					actionMenu(m, append([]action{do("tools.rules", locales.DebuggingRules2(), a.openRules).in("rules")}, a.networkTools()...))
				})
				if uikit.IconButton(c, locales.Settings(), locales.SettingsCtrlCmd(), settingsIcon).Clicked() {
					a.openSettings(-1)
				}
				uikit.IconButton(c, locales.More(), locales.MoreCommands(), moreIcon).PopoverMenuButton(func(m *ui.Menu) { a.moreMenu(c, m) })
			})
		})
	})
}

// brandBar is the window's name past any window controls on the left: at
// the top of the sidebar on macOS, or at the title bar's start.
func (a *app) brandBar(c *ui.Context, t *ui.Theme) {
	bar := c.TitleBar()
	// bar.Left ends at the last window control's edge; the brand keeps a
	// gap from it rather than touching the zoom button.
	start := t.Space(3)
	if bar.Left > 0 {
		start += bar.Left
	}
	ui.Row(c).Height(max(bar.Height, uikit.HeightBar)).PaddingStart(start).Gap(t.Space(2)).AlignItems(ui.Center).
		DragWindow().Children(func() {
		ui.Icon(c, brandIcon).Size(uikit.IconLarge, uikit.IconLarge).TextColor(t.Text)
		ui.Text(c, "vscroxy").FontSize(uikit.TextBody).FontWeight(600).TextColor(t.Text).SingleLine()
	})
}

// layoutToggle chooses where the inspector sits: beside the traffic list or
// under it. A window too narrow for side by side stacks them anyway.
func (a *app) layoutToggle(c *ui.Context) {
	w := &a.workbench
	for _, l := range []struct {
		id, label string
		icon      *ui.SVG
	}{{"right", locales.SideBySideLayout(), sideLayoutIcon}, {"bottom", locales.StackedLayout(), stackedLayoutIcon}} {
		b := uikit.ToolbarButton(c, l.label, l.label, w.layout == l.id).Checked(w.layout == l.id).
			Size(uikit.HeightControl, uikit.HeightControl).Padding(0).Justify(ui.Center).
			Children(func() { ui.Icon(c, l.icon).Size(uikit.IconInline, uikit.IconInline) })
		if b.Clicked() {
			w.layout, w.focused = l.id, false
		}
	}
}

// proxyIndicator is the recording pill: whether traffic is being kept,
// where the proxy listens, and how much is arriving, with pause beside it.
// Listening is never presented as proof of HTTPS inspection.
func (a *app) proxyIndicator(c *ui.Context, t *ui.Theme) {
	pal := uikit.Colors(t)
	running := a.srv != nil && a.srv.Running()
	state, dot := locales.ProxyStopped(), t.Danger
	switch {
	case running && a.recordingPaused:
		state, dot = locales.PausedState(), pal.TextFaint
	case running:
		state, dot = locales.RecordingState(), t.Success
	}
	tip := locales.ProxyTipOff()
	if running {
		tip = locales.ProxyTipOn()
	}
	ui.Row(c).Height(uikit.HeightControl).MinWidth(0).AlignItems(ui.Center).Gap(0).Padding(0, 1).
		Radius(uikit.RadiusControl).Border(1, t.Border).Background(t.Background).Children(func() {
		// Inset by the border, so the faces stay concentric with the pill.
		b := uikit.ToolbarButton(c, locales.ProxyStatus(), tip, false).Height(uikit.HeightControl-4).
			Radius(uikit.RadiusControl-2).MinWidth(0).Padding(0, t.Space(2)).FontSize(uikit.TextMeta)
		b.Children(func() {
			ui.Box(c).Size(7, 7).Radius(uikit.RadiusControl).Background(dot)
			ui.Text(c, state).TextColor(t.Text).SingleLine()
			if !running {
				ui.Icon(c, statusMoreIcon).Size(uikit.IconInline-2, uikit.IconInline-2)
				return
			}
			addr := a.srv.Addr()
			if a.deviceUI.gateway != nil {
				addr += " · LAN"
			}
			ui.Text(c, addr).Font(uikit.FontMono).FontFeatures("tnum").SingleLine().MinWidth(0)
			a.rate.advance(time.Now().Unix())
			if a.recordingPaused {
				// Nothing ambient is kept, so there is no rate to show.
				uikit.Sparkline(c, nil, pal.TextFaint).Label(locales.RecordingPaused())
				return
			}
			// Two-second sums: 30 points over 48px draw a line, not a saw.
			var curve [30]int
			for i, n := range a.rate.counts {
				curve[i/2] += n
			}
			uikit.Sparkline(c, curve[:], t.TextMuted).Label(locales.RequestsPerSecondLastMinute())
			ui.Text(c, rateLabel(a.rate.perSecond())).Font(uikit.FontMono).FontFeatures("tnum").SingleLine()
		})
		if b.Clicked() {
			a.openSettings(1)
		}
		if !running {
			return
		}
		ui.Box(c).Size(1, uikit.IconInline).Background(t.Border)
		label, tip, icon := "Pause recording", "Pause recording · the proxy keeps forwarding without keeping traffic", pauseIcon
		if a.recordingPaused {
			label, tip, icon = "Resume recording", "Resume recording", resumeIcon
		}
		if uikit.IconButton(c, label, tip, icon).Size(uikit.HeightCompact, uikit.HeightCompact).Radius(uikit.RadiusControl - 2).MarginStart(1).Clicked() {
			a.recordingPaused = !a.recordingPaused
		}
	})
}

// rateLabel is a request rate as the pill prints it: tenths while it is
// low enough for them to matter.
func rateLabel(perSecond float64) string {
	if perSecond == 0 || perSecond >= 10 {
		return fmt.Sprintf("%.0f/s", perSecond)
	}
	return fmt.Sprintf("%.1f/s", perSecond)
}

// networkTools is shared by Tools, the enabled chips and More → Network.
func (a *app) networkTools() []action {
	actions := []action{
		do("network.no-caching", locales.NoCaching(), func() { a.srv.SetNoCaching(!a.srv.NoCaching()) }).
			when(a.srv != nil, "Start capture first").checked(checkbox(a.srv != nil && a.srv.NoCaching())).in("caching"),
	}
	for _, preset := range a.networkPresets() {
		selected := a.throttle.Name == preset.Name || a.throttle.Name == "" && preset.Name == "Off"
		if preset.Name == "Off" {
			selected = selected && a.activeNetworkRules() == 0
		}
		actions = append(actions, do("network.throttle", preset.Name, func() {
			if preset.Name == "Off" {
				a.networkOff()
			} else {
				a.setThrottle(preset)
			}
		}).checked(radio(selected)).in("throttle"))
	}
	return append(actions,
		do("network.custom", locales.CustomPresets(), a.openNetwork).in("throttle"),
		do("network.rules", locales.NetworkRules(), a.openNetworkRules).in("throttle"),
		do("network.upstream", locales.Upstream(), func() { a.openSettings(1) }).in("upstream"),
		do("network.upstream-off", locales.UpstreamOff(), a.clearUpstream).when(a.upstream != "" || a.upstreamProblem != "" || a.upstreamCfg.Enabled(), "No upstream proxy is set").in("upstream"),
	)
}

// activeTools remains visible in both workspaces. Each close button changes
// the same setting as its existing menu/editor, never just hides the chip.
func (a *app) activeTools(c *ui.Context, t *ui.Theme) {
	scenario := capture.ActiveScenario(a.rules)
	throttled := a.throttle.Name != "" && a.throttle.Name != "Off"
	networkRules := a.activeNetworkRules()
	noCaching := a.srv != nil && a.srv.NoCaching()
	hostsOn := len(a.profiles.set.On()) > 0
	if !a.breakOn && !throttled && networkRules == 0 && !noCaching && !a.allowOn && scenario == "" && a.upstream == "" && a.upstreamProblem == "" && !a.upstreamCfg.Enabled() && !hostsOn && a.collectionRecording == nil {
		return
	}
	ui.ScrollHorizontal(c.Key("active-tools")).Height(uikit.HeightLarge).Background(uikit.Colors(t).Panel).Border(0, t.Border).BorderWidth(0, 0, 1, 0).Children(func() {
		ui.Row(c).Padding(0, t.Space(3)).Gap(t.Space(2)).Height(uikit.HeightLarge).AlignItems(ui.Center).Children(func() {
			a.collectionRecordingChip(c, t)
			if a.breakOn {
				toolChip(c, "breakpoints", "Breakpoints", "Edit breakpoint rules", breakIcon, a.openRules, func() {
					a.breakOn = false
					if a.srv != nil {
						a.srv.SetBreak(false)
					}
				})
			}
			if throttled {
				toolChip(c, "throttle", a.throttle.Name, "Change network conditions", networkIcon, nil, func() {
					a.setThrottle(capture.Presets[0])
				}).PopoverMenuButton(func(m *ui.Menu) { actionMenu(m, a.networkTools()) })
			}
			if networkRules > 0 {
				toolChip(c, "network-rules", fmt.Sprintf("Network rules: %d", networkRules), "Edit scoped network conditions", networkIcon, a.openNetworkRules, func() {
					a.disableNetworkRules()
				})
			}
			if noCaching {
				toolChip(c, "no-caching", "No Caching", "Change caching and network conditions", cacheIcon, nil, func() {
					a.srv.SetNoCaching(false)
				}).PopoverMenuButton(func(m *ui.Menu) { actionMenu(m, a.networkTools()) })
			}
			if a.allowOn {
				toolChip(c, "allow-list", "Allow list", "Edit the allow list", filterIcon, func() { a.openSettings(4) }, func() {
					a.allowOn = false
					a.applyAllow()
				})
			}
			if scenario != "" {
				toolChip(c, "scenario", locales.ScenarioLabel(scenario), "Manage scenario: "+scenario, layersIcon, a.openRules, func() {
					a.selectRuleScenario("")
					if a.ruleUI.problem != "" {
						a.warn(locales.CouldNotDisableScenario(a.ruleUI.problem))
					}
				})
			}
			a.hostsChip(c)
			if a.upstreamProblem != "" {
				label := "Upstream error"
				if a.upstreamLoading {
					label = "Upstream loading"
				}
				dangerToolChip(c, "upstream-error", label, a.upstreamProblem, upstreamIcon, func() {
					a.openSettings(1)
				}, a.clearUpstream)
			} else if a.upstream != "" {
				toolChip(c, "upstream", "Upstream", "Edit upstream proxy settings", upstreamIcon, func() {
					a.openSettings(1)
				}, a.clearUpstream)
			}
		})
	})
}

func toolChip(c *ui.Context, id, label, tip string, icon *ui.SVG, open, off func()) ui.Element {
	return toolChipState(c, id, label, tip, icon, open, off, false)
}

// dangerToolChip is the same chip in the theme's danger role, for a tool that
// is configured but not working. Its open action goes to the editor that can
// fix it, and its close action still turns the tool off.
func dangerToolChip(c *ui.Context, id, label, tip string, icon *ui.SVG, open, off func()) ui.Element {
	return toolChipState(c, id, label, tip, icon, open, off, true)
}

func toolChipState(c *ui.Context, id, label, tip string, icon *ui.SVG, open, off func(), danger bool) ui.Element {
	t := c.Theme()
	background, border := t.Background, t.Border
	if danger {
		background, border = t.Danger.Alpha(0.12), t.Danger
	}
	var body ui.Element
	ui.Row(c.Key(id)).Height(uikit.HeightCompact).AlignItems(ui.Center).Background(background).Border(1, border).Radius(uikit.RadiusControl).Children(func() {
		body = uikit.ToolbarButton(c, locales.ActiveTool(label), tip, false).Height(uikit.HeightCompact).Children(func() {
			ui.Icon(c, icon).Size(uikit.IconInline, uikit.IconInline)
			ui.Text(c, label).FontSize(uikit.TextMeta).MaxWidth(180).SingleLine()
		})
		if danger {
			body.TextColor(t.Danger)
		}
		if open != nil {
			body.OnClick(open)
		}
		closeButton := uikit.IconButton(c, locales.DisableTool(label), locales.TurnOffTool(label), closeIcon)
		if danger {
			closeButton.TextColor(t.Danger)
		}
		closeButton.OnClick(off)
	})
	return body
}

// moreMenu keeps the less frequent menus one step away without hiding them:
// the same items stay where they were, grouped as they were.
func (a *app) moreMenu(c *ui.Context, m *ui.Menu) { actionMenu(m, a.moreActions(c)) }

func (a *app) moreActions(c *ui.Context) []action {
	global := a.actions(c)
	oneClick := []action{
		pick(global, "network.terminal"),
		pick(global, "network.chrome"),
		pick(global, "network.firefox"),
		pick(global, "network.android"),
		pick(global, "network.ios"),
		pick(global, "network.java"),
		pick(global, "network.electron"),
	}
	network := []action{
		do("network.diagnostics", locales.ConnectionDiagnostics(), func() { a.openDiagnostics(nil, diagnoseMac) }).in("tools"),
		do("network.client", locales.SetUpALocalClient(), a.openClientSetup).in("tools"),
		submenu(locales.OneClickSetup(), oneClick...).in("tools"),
		pick(global, "network.websocket").in("tools"),
		pick(global, "network.sse").in("tools"),
	}
	for _, x := range a.networkTools() {
		if x.group == "caching" {
			x.group = "tools" // No Caching follows the client tools directly.
		}
		network = append(network, x)
	}
	network = append(network,
		do("network.devices", locales.ConnectADevice(), a.openDevices).checked(checkbox(a.deviceUI.gateway != nil)).in("devices"),
		do("https.rules", locales.SslProxying2(), func() { a.sslOpen = true }).in("lists"),
		do("capture.ignored-hosts", locales.IgnoredHosts(), func() { a.ignoreOpen = true }).in("lists"),
		do("capture.ignored-apps", locales.IgnoredApps(), func() { a.ignoreAppsOpen = true }).in("lists"),
		pick(a.hostsActions(), "hosts.manage").as("Host profiles…").in("lists"),
	)
	session := []action{
		pick(global, "collection.record"),
		pick(global, "flow.replay").as("Replay chosen…"),
		do("capture.export-har", locales.ExportHar(), a.exportHAR).in("files"),
		pick(global, "capture.export-chosen").as("Export chosen HAR…").in("files"),
		do("capture.export-csv", locales.ExportCsv(), a.exportCSV).in("files"),
		pick(global, "capture.export-chosen-csv").as("Export chosen CSV…").in("files"),
		do("capture.choose-shown", locales.ChooseAllShown(), func() {
			for _, at := range a.visible {
				a.chosen.Add(a.flows[at].id)
			}
			a.inform(locales.ChosenCount(a.chosen.Len()))
		}).in("files"),
		do("capture.clear-choice", locales.ClearChoice(), a.chosen.Clear).in("files"),
		do("session.save-as", locales.SaveSession(), a.saveSession).in("files"),
		do("session.open", locales.OpenSession(), a.openSession).in("files"),
		do("capture.import-har", locales.ImportHar(), a.importHAR).in("files"),
		do("capture.import-session", locales.ImportCaptureSession(), a.importCaptureSession).in("import"),
		do("capture.import-proxyman-folder", locales.ImportProxymanLogFolder(), a.importCaptureFolder).in("import"),
		do("collection.import", locales.ImportText(), a.importRequests).in("import"),
		do("collection.import-folder", locales.ImportCollectionFolder(), a.importCollectionFolder).in("import"),
		do("request.import-curl", locales.ImportCurl(), a.openCurlImport).in("import"),
		do("api.generate", locales.GenerateOpenapi(), a.openAPIGeneration).
			when(len(a.requests) > 0, "Save a request to a collection first").in("api"),
	}
	view := a.pickActions(c, "view.focus-inspector", "view.layout-right", "view.layout-bottom", "view.sidebar")
	view = append(view, do("capture.filter", locales.Filter(), a.openFilter).in("filter"))
	if a.filtersActive() {
		view = append(view, do("capture.clear-filters", locales.ClearFilters(), a.clearFilters).in("filter"))
	}
	view = append(view,
		do("capture.add-column", locales.AddColumn(), a.openColumn).in("columns"),
		do("protobuf.schema", locales.ProtobufSchema(), func() { a.protobuf.open = true }).in("protobuf"),
	)
	return []action{
		do("tools.rules", locales.DebuggingRules2(), a.openRules),
		submenu(locales.Network(), network...),
		submenu(locales.Session(), session...),
		submenu(locales.View(), view...),
	}
}

// syncSections keeps the sidebar's one choice and the content on screen in
// step when another part of the app changes them: a filter reset, a new
// investigation, a contract or a request opened from elsewhere.
func (a *app) syncSections() {
	if a.page == pageDiagnostics {
		a.sidebarChoice = "diagnostics"
		return
	}
	if !a.page.capture() {
		if a.page == pageAPI {
			if a.apiChoice == "" && len(a.apis) > 0 {
				a.apiChoice = a.apis[0].spec.ID
			}
			if a.apiChoice != "" {
				a.sidebarChoice = "contract:" + a.apiChoice
				return
			}
			a.page = pageRequest
		}
		// A request is a tree item with its own selection; no other item is
		// chosen meanwhile.
		a.sidebarChoice = ""
		return
	}
	if a.page == pageSession {
		a.sidebarChoice = "session:" + a.sessions.active
		return
	}
	if a.sidebarChoice != cmpOr(a.filter, "all") {
		a.sidebarChoice = cmpOr(a.filter, "all")
	}
}

// footer is the one line under every workspace: what this screen counts on
// the left, and the workspace's own quiet state on the right.
func (a *app) footer(c *ui.Context, t *ui.Theme) {
	ui.Row(c).Height(27).Padding(0, t.Space(3)).Gap(t.Space(3)).AlignItems(ui.Center).
		Background(uikit.Colors(t).Panel).Border(0, t.Border).BorderWidth(1, 0, 0, 0).Children(func() {
		text := func(s string) ui.Element {
			return uikit.Meta(c, s).FontFeatures("tnum").SingleLine()
		}
		switch {
		case a.page == pageDiagnostics:
			text("Connection diagnostics")
			ui.Spacer(c)
			text("Read-only until you press a fix")
			return
		case a.page == pageSession:
			text(fmt.Sprintf("%d saved sessions", len(a.sessions.items)))
			ui.Spacer(c)
			text("Read-only evidence · live traffic unchanged")
			return
		case a.page == pageAPI:
			counts := fmt.Sprintf("%d contracts", len(a.apis))
			if len(a.apis) == 1 {
				counts = "1 contract"
			}
			text(counts)
			ui.Spacer(c)
			for _, doc := range a.apis {
				if doc.spec.ID == a.apiChoice {
					text(doc.title())
				}
			}
			return
		case !a.page.capture():
			env := locales.NoEnvironment()
			if a.activeEnv != "" {
				env = locales.EnvironmentNamed(a.activeEnv)
			}
			counts := locales.RequestSummary(len(a.requests))
			text(counts)
			ui.Spacer(c)
			text(env)
			if key := sendShortcut(); key != "" {
				text(key + "  " + locales.Send())
			}
			return
		}
		counts := locales.CaptureCounts(len(a.flows), len(a.visible))
		if n := a.chosen.Len(); n > 0 {
			counts += locales.SelectedSuffix(n)
		}
		text(counts)
		if a.filtersActive() {
			ui.Text(c, locales.Filtered()).FontSize(uikit.TextMeta).TextColor(t.Text).SingleLine().Tooltip(locales.FilteredTooltip(a.filterSummary()))
		}
		if a.dropped > 0 {
			ui.Text(c, locales.DroppedCount(int(a.dropped))).FontSize(uikit.TextMeta).FontFeatures("tnum").TextColor(t.Warning).SingleLine()
		}
		if a.scriptErrs > 0 {
			ui.Text(c, locales.ScriptErrors(int(a.scriptErrs))).FontSize(uikit.TextMeta).FontFeatures("tnum").TextColor(t.Warning).SingleLine()
		}
		ui.Spacer(c)
		if !a.page.capture() {
			return
		}
		view := a.views.activeView()
		if view == nil || len(a.visible) == 0 {
			return
		}
		newRows := a.newRows
		label, tip := locales.AutoFollowOn(), locales.AutoFollowOnTip()
		if !view.list.AtEnd() {
			label = locales.AutoFollowOff()
			tip = locales.AutoFollowOffTip()
		}
		if newRows > 0 {
			label = locales.AutoFollowNew(newRows)
		}
		if uikit.ToolbarButton(c, locales.AutoFollow(), tip, false).Height(uikit.HeightCompact).Padding(0, t.Space(1)).TextColor(t.Text).Children(func() {
			if newRows > 0 {
				uikit.Glyph(c, breakIcon)
			}
			ui.Text(c, label).FontSize(uikit.TextMeta).FontFeatures("tnum").SingleLine()
		}).Clicked() {
			a.workbench.focused = false
			a.newRows = 0
			view.list.ScrollToEnd()
		}
	})
}

// sendShortcut spells the Send shortcut for the footer.
func sendShortcut() string {
	if runtime.GOOS == "darwin" {
		return "⌘ Enter"
	}
	return "Ctrl Enter"
}

func cmpOr(value, fallback string) string {
	if value == "" {
		return fallback
	}
	return value
}
