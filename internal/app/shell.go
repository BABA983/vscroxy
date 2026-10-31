package app

import (
	"fmt"
	"github.com/baba983/vscroxy/internal/uikit"
	"runtime"
	"strings"

	"github.com/baba983/vscroxy/internal/capture"
	"github.com/egoist/mygo/ui"
)

// shell draws the window's own title bar and the footer of the workspace
// below it. The bar carries only what is true everywhere — where capture is
// listening, the two workspaces, and the rare commands — so the screen under
// it belongs to traffic or to a request.
func (a *app) shell(c *ui.Context, t *ui.Theme) {
	bar := c.TitleBar()
	height := max(bar.Height, 44)
	ui.Grid(c).ColumnTracks(ui.Fr(1), ui.Fr(1), ui.Fr(1)).Height(height).AlignItems(ui.Center).
		Background(uikit.Colors(t).Panel).Border(0, t.Border).BorderWidth(0, 0, 1, 0).DragWindow().Children(func() {
		ui.Row(c).PaddingStart(max(bar.Left, t.Space(3))).Gap(t.Space(2)).AlignItems(ui.Center).Children(func() {
			ui.Row(c).Gap(t.Space(2)).AlignItems(ui.Center).Children(func() {
				ui.Icon(c, brandIcon).Size(uikit.IconLarge, uikit.IconLarge).TextColor(t.Text)
				ui.Text(c, "vscroxy").FontSize(uikit.TextBody).Bold().TextColor(t.Text)
			})
			a.workspaceSwitch(c, t)
		})
		ui.Row(c).Justify(ui.Center).MinWidth(0).Children(func() { a.proxyIndicator(c, t) })
		ui.Row(c).PaddingEnd(max(bar.Right, t.Space(3))).Gap(t.Space(2)).Justify(ui.End).AlignItems(ui.Center).Children(func() {
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
				a.settingsOpen = true
			}
			uikit.IconButton(c, "More", "More commands", moreIcon).PopoverMenuButton(func(m *ui.Menu) { a.moreMenu(c, m) })
		})
	})
}

// workspaceSwitch is a quiet segmented control: the chosen workspace is a
// lighter gray, not an accent-colored tab. There are two: traffic is one
// place, and requests and contracts are another.
func (a *app) workspaceSwitch(c *ui.Context, t *ui.Theme) {
	names := []string{"Capture", "Requests"}
	seg := ui.SegmentedBase(c, &a.workspace, len(names))
	seg.Track.Padding(t.Space(1)).Radius(uikit.RadiusPanel).Border(1, t.Border).Background(t.Background).Children(func() {
		for i, name := range names {
			s := seg.Segment(i).Label(name).Height(uikit.HeightControl).Padding(0, t.Space(3)).Radius(uikit.RadiusControl).AlignItems(ui.Center).TextColor(t.TextMuted)
			if i == a.workspace {
				s.TextColor(t.Text)
			}
			if s.Pressed() {
				s.Background(t.SurfacePressed)
			} else if s.Hovered() || i == a.workspace {
				s.Background(t.SurfaceHover)
			}
			s.Children(func() { ui.Text(c, name).FontSize(uikit.TextBody) })
		}
	})
	if seg.Track.Changed() {
		switch a.workspace {
		case 1:
			if a.requestsSection == 0 && a.requestDocument != nil {
				a.urlInput.Focus(c)
			}
		case 0:
			if a.captureSection == 0 {
				a.focusTraffic(c)
			}
		}
	}
}

// proxyIndicator reports the listener, never HTTPS inspection or list following.
// Recording pause is a separate feature; this pill does not imply it exists.
func (a *app) proxyIndicator(c *ui.Context, t *ui.Theme) {
	running := a.srv != nil && a.srv.Running()
	label, color := "Proxy stopped", t.TextMuted
	if running {
		label, color = "Proxy running · "+a.srv.Addr(), t.TextMuted
	}
	if a.deviceUI.gateway != nil {
		label += " · LAN"
	}
	b := uikit.ToolbarButton(c, "Proxy status", "Listening address · proxy settings; listening is not proof of HTTPS inspection", false).
		Background(t.Background).Border(1, t.Border).Radius(uikit.HeightControl / 2).MinWidth(0)
	b.Children(func() {
		dot := t.Danger
		if running {
			dot = t.Success
		}
		ui.Box(c).Size(7, 7).Radius(uikit.RadiusControl).Background(dot)
		ui.Text(c, label).FontSize(uikit.TextMeta).FontFeatures("tnum").TextColor(color).SingleLine()
	})
	if b.Clicked() {
		a.settingsTab, a.settingsOpen = 1, true
	}
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
		a.setMessage("Upstream: off")
	}
}

// activeTools remains visible in both workspaces. Each close button changes
// the same setting as its existing menu/editor, never just hides the chip.
func (a *app) activeTools(c *ui.Context, t *ui.Theme) {
	scenario := capture.ActiveScenario(a.rules)
	throttled := a.throttle.Name != "" && a.throttle.Name != "Off"
	noCaching := a.srv != nil && a.srv.NoCaching()
	if !a.breakOn && !throttled && !noCaching && scenario == "" && a.upstream == "" {
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
			if scenario != "" {
				toolChip(c, "scenario", "Scenario: "+scenario, "Manage scenario: "+scenario, layersIcon, a.openRules, func() {
					a.selectRuleScenario("")
					if a.ruleUI.problem != "" {
						a.setMessage("Could not disable scenario: " + a.ruleUI.problem)
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
		if m.Item("Capture diagnostics…").Chosen() {
			a.openDiagnostics()
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
			a.setMessage(fmt.Sprintf("%d chosen", a.chosen.Len()))
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
		a.desktopMenuItem(c, m, "investigations", "Historical sessions & evidence…")
		if m.Item("Import HAR…").Chosen() {
			a.importHAR()
		}
		if len(a.history) > 0 && m.Item("Clear history").Chosen() {
			a.history = nil
			a.saveHistory()
			a.setMessage("History cleared")
		}
		m.Separator()
		if m.Item("Export requests…").Chosen() {
			a.exportRequests()
		}
		if m.Item("Import…").Chosen() {
			a.importRequests()
		}
		if m.Item("Import regression…").Chosen() {
			a.importRegression()
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
			if action.id == "layout-bottom" || action.id == "layout-right" || action.id == "layout-auto" ||
				action.id == "sidebar" || action.id == "focus" {
				item := m.Item(action.label).Shortcut(action.mods, action.key).Disabled(!action.enabled)
				if action.id == "layout-bottom" || action.id == "layout-right" || action.id == "layout-auto" {
					layout := cmpOr(a.workbench.layout, "bottom")
					item.Checked(action.id == "layout-"+layout || action.id == "layout-auto" && layout == "adaptive")
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

// syncSections keeps each sidebar's choice and the section on screen in step
// when another part of the app changes them: a filter reset, a new
// investigation, a contract or a request opened from elsewhere.
func (a *app) syncSections() {
	if a.workspace == 1 {
		if a.requestsSection == 1 {
			a.requestSide = 3
			if a.apiChoice == "" && len(a.apis) > 0 {
				a.apiChoice = a.apis[0].spec.ID
			}
			if a.apiChoice == "" {
				a.requestsSection = 0
			} else {
				a.requestChoice = "contract:" + a.apiChoice
				return
			}
		}
		if strings.HasPrefix(a.requestChoice, "contract:") {
			a.requestChoice = ""
		}
		return
	}
	if a.captureSection == 1 {
		if a.board.active != "" {
			a.sidebarChoice = "investigation:" + a.board.active
			return
		}
		a.captureSection = 0
	}
	if !strings.HasPrefix(a.sidebarChoice, "investigation:") && a.sidebarChoice != cmpOr(a.filter, "all") {
		a.sidebarChoice = cmpOr(a.filter, "all")
	}
}

// footer is the one line under every workspace: what this screen counts on
// the left, and the workspace's own quiet state on the right.
func (a *app) footer(c *ui.Context, t *ui.Theme) {
	ui.Row(c).Height(27).Padding(0, t.Space(3)).Gap(t.Space(3)).AlignItems(ui.Center).
		Background(uikit.Colors(t).Panel).Border(0, t.Border).BorderWidth(1, 0, 0, 0).Children(func() {
		text := func(s string) ui.Element {
			return ui.Text(c, s).FontSize(uikit.TextMeta).FontFeatures("tnum").TextColor(t.TextMuted).SingleLine()
		}
		switch {
		case a.workspace == 0 && a.captureSection == 1:
			text(a.boardSummary())
			ui.Spacer(c)
			text("Read-only evidence · live traffic unchanged")
			return
		case a.workspace == 1 && a.requestsSection == 1:
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
		case a.workspace == 1:
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
		if a.workspace != 0 {
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
				ui.Icon(c, breakIcon).Size(uikit.IconInline, uikit.IconInline).TextColor(t.TextMuted)
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
