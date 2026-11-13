package app

import (
	"fmt"
	"github.com/baba983/vscroxy/internal/uikit"
	"runtime"
	"strings"

	"github.com/baba983/vscroxy/internal/capture"
	"github.com/baba983/vscroxy/locales"
	"github.com/egoist/mygo/ui"
)

// shell draws the window's own title bar: the switch that starts or pauses
// capture, the brand, where the proxy is listening, and the window's own
// controls. The tools that are on and the Host profiles live in the footer
// with the counts, so the bar itself stays quiet.
func (a *app) shell(c *ui.Context, t *ui.Theme) {
	bar := c.TitleBar()
	ui.Row(c).Height(max(bar.Height, uikit.HeightBar)).AlignItems(ui.Center).DragWindow().Children(func() {
		if !a.macChrome || !a.sidebarShown() {
			a.barLeading(c, t)
		}
		ui.Spacer(c)
		// The switch sits with the state it reports: on macOS the brand is in
		// the sidebar, so the bar would otherwise start with a lone button.
		ui.Row(c).Shrink(0).Gap(t.Space(1.5)).AlignItems(ui.Center).Children(func() {
			if a.macChrome && a.sidebarShown() {
				a.recordButton(c, t)
			}
			a.proxyIndicator(c, t)
		})
		ui.Spacer(c)
		ui.Row(c).Shrink(0).Gap(t.Space(0.5)).AlignItems(ui.Center).PaddingEnd(max(bar.Right, t.Space(3))).Children(func() {
			a.commandButton(c, t)
			a.sidebarToggle(c)
			a.layoutToggle(c)
			uikit.IconButton(c, locales.Tools(), locales.ManageDebuggingRulesAndNetwork(), toolsIcon).PopoverMenuButton(func(m *ui.Menu) {
				actionMenu(m, append([]action{do("tools.rules", locales.DebuggingRules2(), a.openRules).in("rules")}, a.networkTools()...))
			})
			if uikit.IconButton(c, locales.Settings(), locales.SettingsCtrlCmd(), settingsIcon).Clicked() {
				a.openSettings(-1)
			}
			uikit.IconButton(c, locales.More(), locales.MoreCommands(), moreIcon).PopoverMenuButton(func(m *ui.Menu) { a.moreMenu(c, m) })
		})
	})
}

// barLeading is the title bar's start: the record switch and the brand, past
// any window controls on the left.
func (a *app) barLeading(c *ui.Context, t *ui.Theme) {
	bar := c.TitleBar()
	start := t.Space(2)
	if bar.Left > 0 {
		start += bar.Left
	}
	ui.Row(c).Height(max(bar.Height, uikit.HeightBar)).PaddingStart(start).Gap(t.Space(1)).AlignItems(ui.Center).
		DragWindow().Children(func() {
		a.recordButton(c, t)
		a.brand(c, t)
	})
}

// recordButton starts capture, or pauses what it keeps: the one switch the
// title bar always carries.
func (a *app) recordButton(c *ui.Context, t *ui.Theme) {
	running := a.srv != nil && a.srv.Running()
	label, icon := locales.StartCapture(), recordIcon
	switch {
	case running && a.recordingPaused:
		label, icon = locales.ResumeRecording(), resumeIcon
	case running:
		label, icon = locales.PauseRecording(), pauseIcon
	}
	if uikit.IconButton(c, label, label, icon).Clicked() {
		switch {
		case !running:
			// The listener runs until it is closed, as it does from the
			// launch: never on the thread that draws.
			go a.startCapture()
		case a.recordingPaused:
			a.recordingPaused = false
		default:
			a.recordingPaused = true
		}
	}
}

// commandButton is the title bar's way to every command: the magnifier and
// the shortcut that opens the palette, small enough to leave the bar's room to
// the tools that are on.
func (a *app) commandButton(c *ui.Context, t *ui.Theme) {
	b := uikit.ChipButton(c, locales.Commands2(), locales.SearchEveryActionCtrlCmd()).Shrink(0)
	b.Children(func() {
		ui.Icon(c, searchIcon).Size(uikit.IconInline, uikit.IconInline).TextColor(t.TextMuted)
		ui.Text(c, commandShortcut()).FontSize(uikit.TextMeta).TextColor(t.TextMuted).NoWrap()
	})
	if b.Clicked() {
		a.workbench.openPalette()
	}
}

// brandBar is the window's name past any window controls on the left: at
// the top of the sidebar on macOS.
func (a *app) brandBar(c *ui.Context, t *ui.Theme) {
	bar := c.TitleBar()
	// bar.Left ends at the last window control's edge; the brand keeps a
	// gap from it rather than touching the zoom button.
	start := t.Space(3)
	if bar.Left > 0 {
		start += bar.Left
	}
	ui.Row(c).Height(max(bar.Height, uikit.HeightBar)).PaddingStart(start).Gap(t.Space(2)).AlignItems(ui.Center).
		DragWindow().Children(func() { a.brand(c, t) })
}

// brand is the app's icon and name.
func (a *app) brand(c *ui.Context, t *ui.Theme) {
	ui.Icon(c, brandIcon).Size(uikit.IconLarge, uikit.IconLarge).TextColor(t.Text)
	ui.Text(c, "vscroxy").FontSize(uikit.TextBody).FontWeight(500).TextColor(t.Text).SingleLine()
}

// sidebarToggle hides the sidebar or brings it back: the same choice Cmd+B
// makes and the command palette offers, with its own room in the bar. It names
// the choice, not what the window has room for: a window too narrow for the
// sidebar puts it aside by itself, and says so.
func (a *app) sidebarToggle(c *ui.Context) {
	label, icon := locales.HideSidebar(), sidebarCloseIcon
	if a.workbench.sidebarHidden {
		label, icon = locales.ShowSidebar(), sidebarOpenIcon
	}
	tip := label + " · " + sidebarShortcut()
	b := uikit.IconButton(c, label, tip, icon)
	if a.windowWidth < sidebarMinWidth {
		uikit.Tip(c, b.Disabled(true), locales.WindowTooNarrowForTheSidebar(int(sidebarMinWidth)))
		return
	}
	if b.Clicked() {
		a.workbench.sidebarHidden = !a.workbench.sidebarHidden
	}
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

// proxyIndicator is the listening pill: whether traffic is being kept and
// where the proxy listens, centred in the title bar. Listening is never
// presented as proof of HTTPS inspection, and a stopped proxy has no green
// dot to mistake for one.
func (a *app) proxyIndicator(c *ui.Context, t *ui.Theme) {
	pal := uikit.Colors(t)
	running := a.srv != nil && a.srv.Running()
	state, dot := locales.ProxyStopped(), t.Danger
	switch {
	case running && a.recordingPaused:
		state, dot = locales.PausedState(), pal.TextFaint
	case running:
		state, dot = locales.ListeningOn(), t.Success
	}
	tip := locales.ProxyTipOff()
	if running {
		tip = locales.ProxyTipOn()
	}
	b := uikit.ToolbarButton(c, locales.ProxyStatus(), tip, false).Height(uikit.HeightControl).
		Padding(0, t.Space(2)).Gap(t.Space(1.5)).Radius(uikit.RadiusControl).Background(pal.Fill)
	b.Children(func() {
		mark := ui.Box(c).Size(7, 7).Shrink(0)
		// Recording with traffic arriving: the dot breathes. Nothing
		// arriving, or paused: it rests, so the pill does not twitch all day.
		// The pulse is painted rather than built: its frames repeat the last
		// layout, so the table under the pill is not run again at 30 a second
		// while capture is on.
		if running && !a.recordingPaused && a.rate.perSecond() > 0 {
			mark.Draw(func(p *ui.Painter, r ui.Rect) {
				p.AnimationFrameRate(30)
				phase := float32(p.Now().UnixMilli()%1200) / 1200
				if phase > 0.5 {
					phase = 1 - phase
				}
				p.Fill(r, dot.Alpha(0.55+0.45*ui.EaseInOut(phase*2)), r.W/2)
			})
		} else {
			mark.Radius(uikit.RadiusControl).Background(dot)
		}
		ui.Text(c, state).FontSize(uikit.TextMeta).TextColor(t.Text).SingleLine()
		if !running {
			return
		}
		addr := a.srv.Addr()
		if a.deviceUI.gateway != nil {
			addr += " · LAN"
		}
		ui.Text(c, addr).Font(uikit.FontMono).FontSize(uikit.TextMeta).FontFeatures("tnum").SingleLine()
	})
	if b.Clicked() {
		a.openSettings(1)
	}
}

// hostsShortcut spells the Host profiles shortcut for its tooltip.
func hostsShortcut() string {
	if runtime.GOOS == "darwin" {
		return "⌘⇧H"
	}
	return "Ctrl Shift H"
}

// sidebarShortcut spells the sidebar toggle's shortcut for its tooltip.
func sidebarShortcut() string {
	if runtime.GOOS == "darwin" {
		return "⌘B"
	}
	return "Ctrl B"
}

// commandShortcut spells the palette's shortcut as the title bar's field
// shows it.
func commandShortcut() string {
	if runtime.GOOS == "darwin" {
		return "⌘K"
	}
	return "Ctrl K"
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

// footer is the bar under everything: what can be done to the list, what is
// chosen, and the tools that are on. It is the same chrome as the title bar
// and the sidebar, so the window reads as one frame around the panel.
func (a *app) footer(c *ui.Context, t *ui.Theme) {
	p := uikit.Colors(t)
	ui.Row(c).Height(uikit.HeightFooter).Padding(0, t.Space(3)).Gap(t.Space(2)).AlignItems(ui.Center).
		Background(p.Chrome).Children(func() {
		if a.page.capture() {
			ui.Row(c).Shrink(0).Gap(t.Space(1)).AlignItems(ui.Center).Children(func() {
				if uikit.FooterButton(c, locales.Clear(), locales.ClearRecords(), false).Disabled(len(a.flows) == 0).Clicked() {
					a.clearOpen = true
				}
				if uikit.FooterButton(c, locales.Filter2(), locales.EditFilters(), a.filtersActive()).Clicked() {
					a.openFilter()
				}
				on := a.following()
				tip := locales.AutoFollowOnTip()
				if !on {
					tip = locales.AutoFollowOffTip()
				}
				follow := uikit.FooterButton(c, locales.AutoFollow(), tip, on)
				// New traffic that is not being followed says so on the
				// button, so the count is not hidden behind a tint.
				if n := a.newRows; n > 0 {
					follow.Children(func() {
						ui.Text(c, fmt.Sprintf("%d new", n)).FontSize(uikit.TextMeta).TextColor(t.Text)
					})
				}
				if follow.Clicked() {
					a.follow()
				}
			})
		}
		ui.Spacer(c)
		ui.Row(c).Shrink(1).MinWidth(0).Gap(t.Space(2)).AlignItems(ui.Center).Children(func() {
			uikit.Meta(c, a.footerCountsText()).FontFeatures("tnum").SingleLine().
				ContentTransition(ui.ContentTransition{Numeric: true})
			if a.dropped > 0 {
				ui.Text(c, locales.DroppedCount(int(a.dropped))).FontSize(uikit.TextMeta).FontFeatures("tnum").TextColor(t.Warning).SingleLine()
			}
			if a.scriptErrs > 0 {
				ui.Text(c, locales.ScriptErrors(int(a.scriptErrs))).FontSize(uikit.TextMeta).FontFeatures("tnum").TextColor(t.Warning).SingleLine()
			}
		})
		ui.Spacer(c)
		// The tools that are on follow the window, not one screen: a
		// breakpoint or a throttle is on everywhere.
		a.footerTools(c, t)
	})
}

// footerCountsText is what the footer says about the screen it is under.
func (a *app) footerCountsText() string {
	switch a.page {
	case pageDiagnostics:
		return "Connection diagnostics · read-only until you press a fix"
	case pageSession:
		return fmt.Sprintf("%d saved sessions · read-only evidence", len(a.sessions.items))
	case pageAPI:
		if len(a.apis) == 1 {
			return "1 contract"
		}
		return fmt.Sprintf("%d contracts", len(a.apis))
	}
	if !a.page.capture() {
		env := locales.NoEnvironment()
		if a.activeEnv != "" {
			env = locales.EnvironmentNamed(a.activeEnv)
		}
		counts := locales.RequestSummary(len(a.requests)) + " · " + env
		if key := sendShortcut(); key != "" {
			counts += " · " + key + "  " + locales.Send()
		}
		return counts
	}
	if n := a.chosen.Len(); n > 0 {
		return locales.SelectedOfTotal(n, len(a.visible))
	}
	return locales.CaptureCounts(len(a.flows), len(a.visible))
}

// following is whether the list is following new traffic.
func (a *app) following() bool {
	view := a.views.activeView()
	return view != nil && view.list.AtEnd()
}

// follow puts the list back on the newest request and follows again.
func (a *app) follow() {
	a.workbench.focused = false
	a.newRows = 0
	if view := a.views.activeView(); view != nil {
		view.list.ScrollToEnd()
	}
}

// footerChip is one tool that is on, as the footer shows it: what it says,
// what it opens, and how it turns off.
type footerChip struct {
	id, label, tip string
	icon           *ui.SVG
	open, off      func()
	// openLabel names the button that opens the chip, when the chip's own
	// words are a status rather than a tool; offLabel and offTip name the
	// button that turns it off, when "disable" is not what it does. A menu
	// opens under the label instead of an editor, as a switch does.
	openLabel, offLabel, offTip string
	// panel opens a popover under the label instead of an editor or a menu,
	// as the Host switch's list of profiles does.
	panel  func(anchor ui.Element)
	danger bool
}

// footerChips are the tools that are on and the Host profiles, in the order
// the footer shows them. A tool is in this list exactly when it is doing
// something, so the bar says what is on rather than what exists.
func (a *app) footerChips(c *ui.Context) []footerChip {
	var chips []footerChip
	scenario := capture.ActiveScenario(a.rules)
	throttled := a.throttle.Name != "" && a.throttle.Name != "Off"
	networkRules := a.activeNetworkRules()
	noCaching := a.srv != nil && a.srv.NoCaching()
	if s := a.collectionRecording; s != nil {
		state := "Stopped"
		if s.Active {
			state = "Recording"
		} else if s.Busy {
			state = "Stopping"
		}
		chip := footerChip{id: "collection-recording", label: state + " → " + s.Collection,
			openLabel: locales.CollectionRecordingStatus(),
			tip:       locales.ViewRecordingScopeCountersAnd(), icon: captureIcon,
			open: func() { a.openCollectionRecording("", "", "") }}
		if s.Active {
			chip.off = func() { a.stopCollectionRecording("") }
			chip.offLabel, chip.offTip = locales.StopCollectionRecorder(), locales.StopAndDiscardQueuedWork()
		} else {
			chip.off = func() { a.collectionRecording = nil }
			chip.offLabel, chip.offTip = locales.DismissRecordingStatus(), locales.DismissStatusSavedRequestsStay()
		}
		chips = append(chips, chip)
	}
	if a.breakOn {
		chips = append(chips, footerChip{id: "breakpoints", label: "Breakpoints", tip: "Edit breakpoint rules", icon: breakIcon,
			open: a.openRules, off: func() {
				a.breakOn = false
				if a.srv != nil {
					a.srv.SetBreak(false)
				}
			}})
	}
	if throttled {
		chips = append(chips, footerChip{id: "throttle", label: a.throttle.Name, tip: "Change network conditions", icon: networkIcon,
			open: func() { a.openNetwork() }, off: func() { a.setThrottle(capture.Presets[0]) }})
	}
	if networkRules > 0 {
		chips = append(chips, footerChip{id: "network-rules", label: fmt.Sprintf("Network rules: %d", networkRules),
			tip: "Edit scoped network conditions", icon: networkIcon, open: a.openNetworkRules, off: func() { a.disableNetworkRules() }})
	}
	if noCaching {
		chips = append(chips, footerChip{id: "no-caching", label: "No Caching", tip: "Change caching and network conditions", icon: cacheIcon,
			open: func() { a.openNetwork() }, off: func() { a.srv.SetNoCaching(false) }})
	}
	if a.allowOn {
		chips = append(chips, footerChip{id: "allow-list", label: "Allow list", tip: "Edit the allow list", icon: filterIcon,
			open: func() { a.openSettings(4) }, off: func() {
				a.allowOn = false
				a.applyAllow()
			}})
	}
	if scenario != "" {
		chips = append(chips, footerChip{id: "scenario", label: locales.ScenarioLabel(scenario), tip: "Manage scenario: " + scenario, icon: layersIcon,
			open: a.openRules, off: func() {
				a.selectRuleScenario("")
				if a.ruleUI.problem != "" {
					a.warn(locales.CouldNotDisableScenario(a.ruleUI.problem))
				}
			}})
	}
	// The Host switch is always here: it is how a profile is turned on in
	// the first place, and its label says which are.
	hostsLabel := a.hostsLabel()
	if on := a.profiles.set.On(); len(on) > 0 {
		hostsLabel = "Hosts: " + strings.Join(on, " + ")
	}
	hosts := footerChip{id: "hosts", label: hostsLabel, openLabel: locales.HostProfiles(),
		tip: "Switch Host profiles · " + hostsShortcut(), icon: hostsIcon}
	hosts.open = func() { a.profiles.menuOpen = !a.profiles.menuOpen }
	hosts.panel = func(anchor ui.Element) {
		ui.PopoverBase(c, anchor, &a.profiles.menuOpen, func(panel ui.Element) {
			uikit.PopoverPanel(c, panel).Width(320).Padding(c.Theme().Space(2)).Gap(c.Theme().Space(1)).
				Margin(c.Theme().Space(1.5), 0, 0, 0)
			a.hostsMenu(c, c.Theme())
		})
	}
	if len(a.profiles.set.On()) > 0 {
		hosts.off = a.hostsOff
	}
	chips = append(chips, hosts)
	if a.upstreamProblem != "" {
		label := "Upstream error"
		if a.upstreamLoading {
			label = "Upstream loading"
		}
		chips = append(chips, footerChip{id: "upstream-error", label: label, tip: a.upstreamProblem, icon: upstreamIcon, danger: true,
			open: func() { a.openSettings(1) }, off: a.clearUpstream})
	} else if a.upstream != "" {
		chips = append(chips, footerChip{id: "upstream", label: "Upstream", tip: "Edit upstream proxy settings", icon: upstreamIcon,
			open: func() { a.openSettings(1) }, off: a.clearUpstream})
	}
	return chips
}

// footerTools draws the tools that are on at the footer's end. A chip that
// does not fit folds into a count, which opens the same chips as a menu: the
// bar never scrolls sideways or hides what is on behind a fade.
func (a *app) footerTools(c *ui.Context, t *ui.Theme) {
	chips := a.footerChips(c)
	if len(chips) == 0 {
		return
	}
	gap := t.Space(1)
	room := a.footerRoom(c, t)
	used, show := float32(0), len(chips)
	for i, chip := range chips {
		used += footerChipWidth(c, chip)
		if i > 0 {
			used += gap
		}
		// A chip that fits only by pushing the count of the rest past the
		// room folds too, so the bar never runs under the window's edge.
		rest := len(chips) - i - 1
		extra := float32(0)
		if rest > 0 {
			extra = gap + footerMoreWidth(c, rest)
		}
		if used+extra > room {
			show = i
			break
		}
	}
	ui.Row(c).Shrink(0).Gap(gap).AlignItems(ui.Center).Children(func() {
		for _, chip := range chips[:show] {
			footerChipRow(c, t, chip)
		}
		if rest := chips[show:]; len(rest) > 0 {
			b := uikit.FooterButton(c, footerMoreLabel(len(rest)), locales.HiddenTools(), false)
			b.PopoverMenuButton(func(m *ui.Menu) {
				var items []action
				for _, chip := range rest {
					if chip.open != nil {
						items = append(items, do("footer.tool."+chip.id, chip.label, chip.open))
					}
				}
				actionMenu(m, items)
			})
		}
	})
}

// footerRoom is how much of the footer's end the tools may take: the window
// less its padding, the actions at the start and the counts in the middle.
func (a *app) footerRoom(c *ui.Context, t *ui.Theme) float32 {
	padding, gap := 2*t.Space(3), t.Space(2)
	room := a.windowWidth - padding - 4*gap
	if a.page.capture() {
		for _, label := range []string{locales.Clear(), locales.Filter(), locales.AutoFollow()} {
			room -= paneTextWidth(c, label, uikit.TextMeta, 400) + 2*t.Space(2) + gap
		}
	}
	room -= paneTextWidth(c, a.footerCountsText(), uikit.TextMeta, 400)
	return max(room, 0)
}

// footerChipWidth is how wide one tool chip is: its icon, its label, the
// button that turns it off and the chip's own padding.
func footerChipWidth(c *ui.Context, chip footerChip) float32 {
	return 2*c.Theme().Space(1.5) + uikit.IconInline + c.Theme().Space(1) + paneTextWidth(c, chip.label, uikit.TextMeta, 400) + 20
}

// footerMoreWidth is how wide the count of folded tools is.
func footerMoreWidth(c *ui.Context, n int) float32 {
	return paneTextWidth(c, footerMoreLabel(n), uikit.TextMeta, 400) + 2*c.Theme().Space(2)
}

func footerMoreLabel(n int) string { return fmt.Sprintf("+%d", n) }

// footerChipRow draws one tool that is on: its label opens the editor that
// changes it, and the close button turns it off.
func footerChipRow(c *ui.Context, t *ui.Theme, chip footerChip) {
	p := uikit.Colors(t)
	background, ink := p.Fill, t.Text
	if chip.danger {
		background, ink = t.Danger.Alpha(0.12), t.Danger
	}
	ui.Row(c.Key(chip.id)).Height(uikit.HeightFooterControl).AlignItems(ui.Center).Background(background).
		Radius(uikit.RadiusInner).Children(func() {
		open := chip.openLabel
		if open == "" {
			open = locales.ActiveTool(chip.label)
		}
		label := uikit.ToolbarButton(c, open, chip.tip, false).Height(uikit.HeightFooterControl).
			PaddingStart(t.Space(1.5)).PaddingEnd(t.Space(0.5)).Children(func() {
			ui.Icon(c, chip.icon).Size(uikit.IconInline, uikit.IconInline)
			ui.Text(c, chip.label).FontSize(uikit.TextMeta).MaxWidth(180).SingleLine()
			if chip.panel != nil {
				ui.Icon(c, chevronIcon).Size(uikit.IconInline-4, uikit.IconInline-4)
			}
		})
		label.TextColor(ink)
		if chip.panel != nil {
			if chip.open != nil {
				label.OnClick(chip.open)
			}
			chip.panel(label)
		} else if chip.open != nil {
			label.OnClick(chip.open)
		}
		if chip.off == nil {
			return
		}
		offLabel, offTip := chip.offLabel, chip.offTip
		if offLabel == "" {
			offLabel, offTip = locales.DisableTool(chip.label), locales.TurnOffTool(chip.label)
		}
		close := uikit.IconButton(c, offLabel, offTip, closeIcon).
			Size(uikit.HeightFooterControl-4, uikit.HeightFooterControl-4).Radius(uikit.RadiusInner)
		if chip.danger {
			close.TextColor(ink)
		}
		close.OnClick(chip.off)
	})
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
