package app

import (
	"fmt"
	"github.com/baba983/vscroxy/internal/uikit"
	"runtime"
	"time"

	"github.com/baba983/vscroxy/internal/capture"
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
		// two sides split the room evenly.
		ui.Row(c).Grow(1).Basis(0).Justify(ui.End).Children(func() {
			ui.Row(c).PaddingEnd(max(bar.Right, t.Space(3))).Gap(t.Space(2)).AlignItems(ui.Center).Children(func() {
				ui.Row(c).Gap(t.Space(0.5)).AlignItems(ui.Center).Children(func() { a.layoutToggle(c) })
				if uikit.IconButton(c, "Commands", "Search every action · Ctrl/Cmd+K", commandIcon).Clicked() {
					a.workbench.openPalette()
				}
				uikit.IconButton(c, "Tools", "Manage debugging rules and network conditions", toolsIcon).PopoverMenuButton(func(m *ui.Menu) {
					if m.Item("Debugging rules…").Chosen() {
						a.openRules()
					}
					m.Separator()
					a.networkTools(m)
				})
				if uikit.IconButton(c, "Settings", "Settings · Ctrl/Cmd+,", settingsIcon).Clicked() {
					a.openSettings(-1)
				}
				uikit.IconButton(c, "More", "More commands", moreIcon).PopoverMenuButton(func(m *ui.Menu) { a.moreMenu(c, m) })
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
	}{{"right", "Side by side layout", sideLayoutIcon}, {"bottom", "Stacked layout", stackedLayoutIcon}} {
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
	state, dot := "Proxy stopped", t.Danger
	switch {
	case running && a.recordingPaused:
		state, dot = "Paused", pal.TextFaint
	case running:
		state, dot = "Recording", t.Success
	}
	tip := "Proxy settings · the listener failed or is off"
	if running {
		tip = "Listening address · proxy settings; listening is not proof of HTTPS inspection"
	}
	ui.Row(c).Height(uikit.HeightControl).MinWidth(0).AlignItems(ui.Center).Gap(0).Padding(0, 1).
		Radius(uikit.RadiusControl).Border(1, t.Border).Background(t.Background).Children(func() {
		// Inset by the border, so the faces stay concentric with the pill.
		b := uikit.ToolbarButton(c, "Proxy status", tip, false).Height(uikit.HeightControl-4).
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
				uikit.Sparkline(c, nil, pal.TextFaint).Label("Recording paused")
				return
			}
			// Two-second sums: 30 points over 48px draw a line, not a saw.
			var curve [30]int
			for i, n := range a.rate.counts {
				curve[i/2] += n
			}
			uikit.Sparkline(c, curve[:], t.TextMuted).Label("Requests per second, last minute")
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
func (a *app) networkTools(m *ui.Menu) {
	if m.Item("No Caching").Checked(a.srv != nil && a.srv.NoCaching()).Disabled(a.srv == nil).Chosen() {
		a.srv.SetNoCaching(!a.srv.NoCaching())
	}
	m.Separator()
	for _, preset := range capture.Presets {
		if m.Item(preset.Name).Radio(a.throttle.Name == preset.Name || a.throttle.Name == "" && preset.Name == "Off").Chosen() {
			a.throttle = preset
			if a.srv != nil {
				a.srv.SetThrottle(preset)
			}
		}
	}
	m.Separator()
	if m.Item("Upstream…").Chosen() {
		a.upstreamAddr = a.upstream
		a.upstreamOpen = true
	}
	if m.Item("Upstream off").Disabled(a.upstream == "").Chosen() {
		if a.srv != nil {
			_ = a.srv.SetUpstream("")
		}
		a.upstream = ""
		a.inform("Upstream: off")
	}
}

// activeTools remains visible in both workspaces. Each close button changes
// the same setting as its existing menu/editor, never just hides the chip.
func (a *app) activeTools(c *ui.Context, t *ui.Theme) {
	scenario := capture.ActiveScenario(a.rules)
	throttled := a.throttle.Name != "" && a.throttle.Name != "Off"
	noCaching := a.srv != nil && a.srv.NoCaching()
	if !a.breakOn && !throttled && !noCaching && !a.allowOn && scenario == "" && a.upstream == "" {
		return
	}
	ui.ScrollHorizontal(c.Key("active-tools")).Height(uikit.HeightLarge).Background(uikit.Colors(t).Panel).Border(0, t.Border).BorderWidth(0, 0, 1, 0).Children(func() {
		ui.Row(c).Padding(0, t.Space(3)).Gap(t.Space(2)).Height(uikit.HeightLarge).AlignItems(ui.Center).Children(func() {
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
					a.throttle = capture.Presets[0]
					if a.srv != nil {
						a.srv.SetThrottle(a.throttle)
					}
				}).PopoverMenuButton(a.networkTools)
			}
			if noCaching {
				toolChip(c, "no-caching", "No Caching", "Change caching and network conditions", cacheIcon, nil, func() {
					a.srv.SetNoCaching(false)
				}).PopoverMenuButton(a.networkTools)
			}
			if a.allowOn {
				toolChip(c, "allow-list", "Allow list", "Edit the allow list", filterIcon, func() { a.openSettings(4) }, func() {
					a.allowOn = false
					a.applyAllow()
				})
			}
			if scenario != "" {
				toolChip(c, "scenario", "Scenario: "+scenario, "Manage scenario: "+scenario, layersIcon, a.openRules, func() {
					a.selectRuleScenario("")
					if a.ruleUI.problem != "" {
						a.warn("Could not disable scenario: " + a.ruleUI.problem)
					}
				})
			}
			if a.upstream != "" {
				toolChip(c, "upstream", "Upstream", "Edit upstream proxy settings", upstreamIcon, func() {
					a.upstreamAddr, a.upstreamOpen = a.upstream, true
				}, func() {
					if a.srv != nil {
						_ = a.srv.SetUpstream("")
					}
					a.upstream = ""
				})
			}
		})
	})
}

func toolChip(c *ui.Context, id, label, tip string, icon *ui.SVG, open, off func()) ui.Element {
	t := c.Theme()
	var body ui.Element
	ui.Row(c.Key(id)).Height(uikit.HeightCompact).AlignItems(ui.Center).Background(t.Background).Border(1, t.Border).Radius(uikit.RadiusControl).Children(func() {
		body = uikit.ToolbarButton(c, "Active tool: "+label, tip, false).Height(uikit.HeightCompact).Children(func() {
			ui.Icon(c, icon).Size(uikit.IconInline, uikit.IconInline)
			ui.Text(c, label).FontSize(uikit.TextMeta).MaxWidth(180).SingleLine()
		})
		if open != nil {
			body.OnClick(open)
		}
		uikit.IconButton(c, "Disable "+label, "Turn off "+label, closeIcon).OnClick(off)
	})
	return body
}

// moreMenu keeps the less frequent menus one step away without hiding them:
// the same items stay where they were, grouped as they were.
func (a *app) moreMenu(c *ui.Context, m *ui.Menu) {
	if m.Item("Debugging rules…").Chosen() {
		a.openRules()
	}
	m.Submenu("Network", func(m *ui.Menu) {
		if m.Item("Connection diagnostics").Chosen() {
			a.openDiagnostics(nil, diagnoseMac)
		}
		if m.Item("Set up a local client…").Chosen() {
			a.openClientSetup()
		}
		a.desktopMenuItem(c, m, "websocket", "WebSocket session…")
		a.networkTools(m)
		m.Separator()
		if m.Item("Connect a device…").Checked(a.deviceUI.gateway != nil).Chosen() {
			a.openDevices()
		}
		m.Separator()
		if m.Item("SSL proxying…").Chosen() {
			a.sslOpen = true
		}
		if m.Item("Ignored hosts…").Chosen() {
			a.ignoreOpen = true
		}
		if m.Item("Ignored apps…").Chosen() {
			a.ignoreAppsOpen = true
		}
		if m.Item("DNS mapping…").Chosen() {
			a.dnsOpen = true
		}
	})
	m.Submenu("Session", func(m *ui.Menu) {
		a.desktopMenuItem(c, m, "replay", "Replay chosen…")
		m.Separator()
		if m.Item("Export HAR…").Chosen() {
			a.exportHAR()
		}
		a.desktopMenuItem(c, m, "export-selection", "Export chosen HAR…")
		if m.Item("Choose all shown").Chosen() {
			for _, at := range a.visible {
				a.chosen.Add(a.flows[at].id)
			}
			a.inform(fmt.Sprintf("%d chosen", a.chosen.Len()))
		}
		if m.Item("Clear choice").Chosen() {
			a.chosen.Clear()
		}
		if m.Item("Save session…").Chosen() {
			a.saveSession()
		}
		if m.Item("Open session…").Chosen() {
			a.openSession()
		}
		if m.Item("Import HAR…").Chosen() {
			a.importHAR()
		}
		m.Separator()
		if m.Item("Import…").Chosen() {
			a.importRequests()
		}
		if m.Item("Import cURL…").Chosen() {
			a.openCurlImport()
		}
		m.Separator()
		if m.Item("Generate OpenAPI…").Disabled(len(a.requests) == 0).Chosen() {
			a.openAPIGeneration()
		}
	})
	m.Submenu("View", func(m *ui.Menu) {
		for _, action := range a.desktopActions(c) {
			if action.id == "layout-bottom" || action.id == "layout-right" || action.id == "sidebar" || action.id == "focus" {
				item := m.Item(action.label).Shortcut(action.mods, action.key).Disabled(!action.enabled)
				if action.id == "layout-bottom" || action.id == "layout-right" {
					item.Checked(action.id == "layout-"+a.workbench.layout)
				}
				if item.Chosen() {
					action.run()
				}
			}
		}
		m.Separator()
		if m.Item("Filter…").Chosen() {
			a.openFilter()
		}
		if a.filtersActive() && m.Item("Clear filters").Chosen() {
			a.clearFilters()
		}
		m.Separator()
		if m.Item("Add column…").Chosen() {
			a.openColumn()
		}
		m.Separator()
		if m.Item("Protobuf schema…").Chosen() {
			a.protobuf.open = true
		}
	})
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
			env := "No environment"
			if a.activeEnv != "" {
				env = a.activeEnv + " environment"
			}
			counts := fmt.Sprintf("%d requests", len(a.requests))
			if len(a.requests) == 1 {
				counts = "1 request"
			}
			text(counts)
			ui.Spacer(c)
			text(env)
			if key := sendShortcut(); key != "" {
				text(key + "  Send")
			}
			return
		}
		counts := fmt.Sprintf("%d captured · %d shown", len(a.flows), len(a.visible))
		if n := a.chosen.Len(); n > 0 {
			counts += fmt.Sprintf(" · %d selected", n)
		}
		text(counts)
		if a.filtersActive() {
			ui.Text(c, "Filtered").FontSize(uikit.TextMeta).TextColor(t.Text).SingleLine().Tooltip("filtered: " + a.filterSummary())
		}
		if a.dropped > 0 {
			ui.Text(c, fmt.Sprintf("%d dropped", a.dropped)).FontSize(uikit.TextMeta).FontFeatures("tnum").TextColor(t.Warning).SingleLine()
		}
		if a.scriptErrs > 0 {
			ui.Text(c, fmt.Sprintf("%d script errors", a.scriptErrs)).FontSize(uikit.TextMeta).FontFeatures("tnum").TextColor(t.Warning).SingleLine()
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
		label, tip := "Auto-follow on · latest ↓", "The list follows new traffic. Click to jump to the newest request."
		if !view.list.AtEnd() {
			label = "Auto-follow off · latest ↓"
			tip = "The list stays where you are reading. Click to jump to the newest request and follow again."
		}
		if newRows > 0 {
			label = fmt.Sprintf("Auto-follow off · %d new ↓", newRows)
		}
		if uikit.ToolbarButton(c, "Auto-follow", tip, false).Height(uikit.HeightCompact).Padding(0, t.Space(1)).TextColor(t.Text).Children(func() {
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
