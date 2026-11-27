package app

import (
	"fmt"
	"net"
	"runtime"
	"strconv"
	"strings"
	"time"

	"github.com/baba983/vscroxy/internal/capture"
	"github.com/baba983/vscroxy/internal/uikit"
	"github.com/baba983/vscroxy/locales"
	"github.com/egoist/mygo/ui"
)

// shell draws the window's own title bar, across the whole window as VS Code
// and Postman draw theirs: the sidebar switch at its start, the search in the
// middle, and at its end the screen's layout and the gear. Live state
// (the proxy, the tools that are on, notifications) is the footer's.
func (a *app) shell(c *ui.Context, t *ui.Theme) {
	bar := c.TitleBar()
	// The bar is the shell's own surface. On macOS the window's root is clear
	// so the sidebar's material can show, and without this the bar would show
	// the desktop behind it instead of chrome.
	ui.Row(c).Height(max(bar.Height, uikit.HeightBar)).Background(uikit.Colors(t).Window).
		AlignItems(ui.Center).DragWindow().Children(func() {
		// The two ends share the room left by the search equally, so the
		// search sits on the window's center line, not between the groups.
		a.barLeading(c, t)
		a.commandButton(c, t)
		ui.Row(c).Grow(1).Basis(0).Justify(ui.End).Gap(t.Space(uikit.SpaceHair)).AlignItems(ui.Center).PaddingEnd(max(bar.Right, t.Space(uikit.SpaceL))).Children(func() {
			// The layout switches pair across the bar: the sidebar's at the
			// start, the request workspace's third column and the screen's
			// split here, as VS Code keeps them.
			a.rightSidebarToggle(c)
			a.layoutToggle(c)
			// One gear, as VS Code's Manage: Settings and the debugging
			// tools share a window, and everything else is one menu.
			uikit.MenuFace(c, func() {
				uikit.IconButton(c, locales.Manage(), locales.ManageTip(), settingsIcon).PopoverMenuButton(func(m *ui.Menu) {
					actionMenu(m, a.moreActions(c))
				}, uikit.MenuButtonOptions(c))
			})
		})
	})
}

// barLeading is the title bar's start: the sidebar switch, past any window
// controls on the left. It sits beside the window's controls, where macOS apps
// keep it, rather than at the far end of the bar with the tools.
func (a *app) barLeading(c *ui.Context, t *ui.Theme) {
	bar := c.TitleBar()
	start := t.Space(uikit.SpaceM)
	if bar.Left > 0 {
		start += bar.Left
	}
	ui.Row(c).Grow(1).Basis(0).Height(max(bar.Height, uikit.HeightBar)).PaddingStart(start).Gap(t.Space(uikit.SpaceXS)).AlignItems(ui.Center).
		DragWindow().Children(func() {
		a.sidebarToggle(c)
	})
}

// recordButton starts capture, or pauses what it keeps: the switch that sits
// beside the proxy state in the footer.
func (a *app) recordButton(c *ui.Context, t *ui.Theme) {
	running := a.srv != nil && a.srv.Running()
	if !running {
		return
	}
	label, icon := locales.PauseCapture(), pauseIcon
	switch {
	case a.recordingPaused:
		label, icon = locales.ResumeCapture(), resumeIcon
	}
	if uikit.IconButton(c, label, label, icon).Clicked() {
		switch {
		case !running:
			// The listener runs until it is closed, as it does from the
			// launch: never on the thread that draws.
			go guarded(func() { a.startCapture() })
		case a.recordingPaused:
			a.recordingPaused = false
		default:
			a.recordingPaused = true
		}
	}
}

// commandButton is the title bar's way to every command, request and flow:
// a field-shaped button in the middle of the bar, as Postman's search is,
// that opens the palette. It gives way before the tools do.
func (a *app) commandButton(c *ui.Context, t *ui.Theme) {
	p := uikit.Colors(t)
	b := uikit.ChipButton(c, locales.Commands2(), locales.SearchEveryActionCtrlCmd()).
		Width(320).Shrink(1).MinWidth(t.Space(uikit.SpaceXXL)*3).Gap(t.Space(uikit.SpaceS)).
		Background(p.Background).InsetShadow(0, 0, 0, uikit.HairlineWidth, p.Border)
	if b.Hovered() {
		b.Background(p.MutedHover)
	}
	b.Children(func() {
		ui.Icon(c, searchIcon).Size(uikit.IconInline, uikit.IconInline).TextColor(t.TextMuted).Shrink(0)
		uikit.Meta(c, locales.SearchRequestsTrafficAndCommands()).SingleLine().MinWidth(0).Grow(1)
		uikit.Meta(c, commandShortcut()).NoWrap().Shrink(0)
	})
	if b.Clicked() {
		a.workbench.openPalette()
	}
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
	b := uikit.IconButton(c, label, label, icon, sidebarKeys()...)
	if a.windowWidth < sidebarMinWidth {
		uikit.Tip(c, b.Disabled(true), locales.WindowTooNarrowForTheSidebar(int(sidebarMinWidth)))
		return
	}
	if b.Clicked() {
		a.workbench.sidebarHidden = !a.workbench.sidebarHidden
	}
}

// rightSidebarToggle opens or closes the request workspace's third column.
// It keeps its place on every screen; the column itself belongs to requests.
func (a *app) rightSidebarToggle(c *ui.Context) {
	label, icon := locales.ShowRightSidebar(), rightOpenIcon
	if a.rightSidebar.open {
		label, icon = locales.HideRightSidebar(), rightCloseIcon
	}
	b := uikit.IconButton(c, label, label, icon)
	if b.Clicked() {
		a.toggleRightSidebar()
	}
}

func (a *app) toggleRightSidebar() {
	a.rightSidebar.open = !a.rightSidebar.open
	a.rightSidebar.splitReady = false
}

// layoutToggle switches the current screen's split: the inspector beside the
// traffic list or under it, and on a request the editor beside its response or
// above it. One button shows the layout it switches to, so the footer carries
// one control rather than a pair that has to be read to tell which is on.
func (a *app) layoutToggle(c *ui.Context) {
	w := &a.workbench
	if a.page == pageRequest {
		d := a.requestDocument
		if d == nil {
			return
		}
		side := !a.requestSidesStacked(c)
		label, icon := locales.StackedLayout(), stackedLayoutIcon
		if !side {
			label, icon = locales.SideBySideLayout(), sideLayoutIcon
		}
		if uikit.IconButton(c, label, label, icon).Clicked() {
			next := !side
			d.split = &next
			// The split is a layout change, not a repaint of the old tree:
			// ask for the next frame here so the panes move on the click,
			// not on the next pointer move.
			c.Invalidate()
		}
		return
	}
	next, label, icon := "bottom", locales.StackedLayout(), stackedLayoutIcon
	if w.layout != "right" {
		next, label, icon = "right", locales.SideBySideLayout(), sideLayoutIcon
	}
	b := uikit.IconButton(c, label, label, icon)
	if b.Clicked() {
		w.layout, w.focused = next, false
		c.Invalidate()
	}
}

// proxyIndicator is the footer's lamp: a dot and one word for whether traffic
// is being kept. Where the proxy listens, this computer's addresses and
// device access are details a reader asks for, so they wait in a hover card
// instead of taking the footer's width. Listening is never presented as proof
// of HTTPS inspection, and a stopped proxy has no green dot to mistake for one.
func (a *app) proxyIndicator(c *ui.Context, t *ui.Theme) {
	pal := uikit.Colors(t)
	running := a.srv != nil && a.srv.Running() || a.diagnostics.demoRunning
	// Red is a signal, not a resting state: a stopped capture keeps a muted
	// dot, and only a capture that is recording takes a live one.
	state, dot := locales.Stopped(), pal.Faint
	switch {
	case running && a.recordingPaused:
		state, dot = locales.PausedState(), pal.Faint
	case running:
		state, dot = locales.RecordingState(), t.Success
	}
	b := uikit.ToolbarButton(c, locales.ProxyStatus(), "", false).
		Height(uikit.HeightControlSmall).Padding(0, uikit.Scaled(uikit.ControlPaddingXSmall)).
		Gap(t.Space(uikit.SpaceS)).Radius(uikit.RadiusInner).FontSize(uikit.Scaled(uikit.TextSmall)).
		TextColor(t.TextMuted)
	card := uikit.HoverCard{Title: state, Meta: locales.ProxyTipOff()}
	if running {
		card.Meta = locales.ProxyTipOn()
	}
	// The addresses are read only while the card can show: listing the
	// network interfaces every frame would cost the footer for nothing.
	if running && (b.Hovered() || b.Focused()) {
		card.Rows = a.proxyCardRows()
	}
	uikit.CardTip(c, b, card)
	b.Children(func() {
		// The lamp is a static dot: its state is the word beside it, and a
		// pulse would ask the window for frames while it records. A drawing
		// that animates with p.AnimationFrameRate also lowers the display's
		// own refresh rate on macOS, which paced every scroll at 30 fps.
		ui.Box(c).Size(7, 7).Radius(uikit.RadiusControl).Background(dot).Shrink(0)
		uikit.Small(c, state).TextColor(t.TextMuted).SingleLine()
	})
	uikit.MenuFace(c, func() {
		b.PopoverMenuButton(func(m *ui.Menu) {
			global := a.actions(c)
			start := pick(global, "capture.start")
			stop := pick(global, "capture.stop")
			settings := pick(global, "app.settings")
			if running {
				actionMenu(m, []action{stop, settings})
			} else {
				actionMenu(m, []action{start, settings})
			}
		}, uikit.MenuButtonOptions(c))
	})
}

// proxyCardRows are the proxy lamp's details: the address a client on this
// computer uses, its port, this computer's addresses on the network, and
// where other devices connect, or Off. The keys are single words: the card's
// key column is narrow.
func (a *app) proxyCardRows() []uikit.HoverCardRow {
	addr := a.diagnostics.demoAddr
	if addr == "" && a.srv != nil {
		addr = a.srv.Addr()
	}
	rows := []uikit.HoverCardRow{{Key: locales.Address(), Value: addr}}
	if _, port, err := net.SplitHostPort(addr); err == nil {
		rows = append(rows, uikit.HoverCardRow{Key: locales.Port(), Value: port})
	}
	if ips := deviceIPs(); len(ips) > 0 {
		rows = append(rows, uikit.HoverCardRow{Key: locales.Network(), Value: strings.Join(ips[:min(len(ips), 3)], ", ")})
	}
	access := locales.Off()
	if g := a.deviceUI.gateway; g != nil {
		access = g.Addr()
	}
	return append(rows, uikit.HoverCardRow{Key: locales.Devices(), Value: access})
}

// hostsKeys spells the Host profiles shortcut as keycaps for its tip.
func hostsKeys() []string {
	if runtime.GOOS == "darwin" {
		return []string{"⌘", "⇧", "H"}
	}
	return []string{"Ctrl", "Shift", "H"}
}

// sidebarKeys spells the sidebar toggle's shortcut as keycaps for its tip.
func sidebarKeys() []string {
	if runtime.GOOS == "darwin" {
		return []string{"⌘", "B"}
	}
	return []string{"Ctrl", "B"}
}

// settingsKeys spells the Settings shortcut as keycaps for its tip.
func settingsKeys() []string {
	if runtime.GOOS == "darwin" {
		return []string{"⌘", ","}
	}
	return []string{"Ctrl", ","}
}

// saveKeys spells the Save shortcut as keycaps for its tip.
func saveKeys() []string {
	if runtime.GOOS == "darwin" {
		return []string{"⌘", "S"}
	}
	return []string{"Ctrl", "S"}
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
			when(a.srv != nil, locales.StartCaptureFirst()).checked(checkbox(a.srv != nil && a.srv.NoCaching())).in("caching"),
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
		do("network.upstream-off", locales.UpstreamOff(), a.clearUpstream).when(a.upstream != "" || a.upstreamProblem != "" || a.upstreamCfg.Enabled(), locales.NoUpstreamProxy()).in("upstream"),
	)
}

// footer is the window's status bar, as VS Code's: at its start the state
// that holds everywhere -- the proxy, its pause switch and the tools that
// are on -- and at its end what the current screen counts, with the
// notifications. Commands on one screen's list live in that screen's
// toolbar, not here.
func (a *app) footer(c *ui.Context, t *ui.Theme) {
	p := uikit.Colors(t)
	ui.Row(c).Height(uikit.HeightFooter).Padding(0, t.Space(uikit.SpaceL)).Gap(t.Space(uikit.SpaceM)).AlignItems(ui.Center).
		Background(p.Window).Children(func() {
		ui.Row(c).Shrink(0).Gap(t.Space(uikit.SpaceXS)).AlignItems(ui.Center).Children(func() {
			a.proxyIndicator(c, t)
			a.recordButton(c, t)
		})
		// The tools that are on follow the window, not one screen: a
		// breakpoint or a throttle is on everywhere.
		a.footerTools(c, t)
		if uikit.FooterButton(c, locales.Console(), locales.ToggleConsoleTip(), a.requestConsole.open).Clicked() {
			a.toggleConsole()
		}
		ui.Spacer(c)
		// A Window progress task takes its place in the status bar, before
		// the counts, so its Cancel stays reachable.
		a.progressStatus(c, t)
		ui.Row(c).Shrink(1).MinWidth(0).Gap(t.Space(uikit.SpaceM)).AlignItems(ui.Center).Children(func() {
			if text := a.footerCountsText(); text != "" {
				counts := uikit.Small(c, text).TextColor(t.TextMuted).FontFeatures("tnum").SingleLine()
				if tip := a.footerCountsTip(); tip != "" {
					uikit.Tip(c, counts, tip)
				}
			}
			if a.dropped > 0 {
				uikit.Small(c, locales.DroppedCount(int(a.dropped))).FontFeatures("tnum").TextColor(t.Warning).SingleLine()
			}
			if a.scriptErrs > 0 {
				uikit.Small(c, locales.ScriptErrors(int(a.scriptErrs))).FontFeatures("tnum").TextColor(t.Warning).SingleLine()
			}
		})
		a.notificationButton(c, t)
	})
}

// footerCountsTip is what the counts' hover says beyond their number: the
// evidence a diagnostics session holds, or the capture's clock. The capture
// clock is the timeline's end, so it needs no row of its own above the table.
func (a *app) footerCountsTip() string {
	switch {
	case a.page == pageDiagnostics:
		return locales.EvidenceFromThisLiveSession()
	case a.page.capture():
		return locales.CapturedThrough(a.activityEnd(time.Now()).Local().Format("15:04:05"))
	}
	return ""
}

// footerCountsText is what the footer says about the screen it is under.
func (a *app) footerCountsText() string {
	switch a.page {
	case pageDiagnostics:
		return locales.ConnectionDiagnosticsReadOnly()
	case pageOverview:
		return locales.CapturesOverview(len(a.flows))
	case pageSession:
		return locales.SavedSessionsEvidence(len(a.sessions.items))
	case pageRequest:
		if d := a.requestDocument; d != nil && d.protocol == "Socket.IO" {
			state := "Not connected"
			if d.socketIOState().session != nil {
				state = stateLabel(d.socketIOState().session.snapshot().state)
			}
			return locales.SocketIOState(state)
		}
	case pageRuns:
		n := 0
		for _, r := range a.experiments.log.Records {
			if a.experiments.includes(r) {
				n++
			}
		}
		word := locales.Runs()
		if n == 1 {
			word = locales.Run()
		}
		count := fmt.Sprintf("%d %s", n, word)
		if id := a.experiments.log.Baseline; id != 0 {
			count += " · " + locales.BaselineNumber(strconv.Itoa(id))
		}
		return count
	case pageAPI:
		if len(a.apis) == 1 {
			return locales.OneContract()
		}
		return locales.ContractsCount(len(a.apis))
	}
	if !a.page.capture() {
		// The environment is the tab strip's select; the footer only counts.
		return locales.RequestSummary(len(a.requests))
	}
	if n := a.chosen.Len(); n > 1 {
		return locales.SelectedOfTotal(n, len(a.visible))
	}
	// The sidebar already counts the capture: the footer says how much of it
	// the view hides, and nothing while it hides nothing.
	if shown, captured := len(a.visible), len(a.flows); shown != captured {
		return locales.ShownOfCaptured(shown, captured)
	}
	return ""
}

// selectNewest scrolls to the last row and chooses it, as Auto-select does
// when traffic arrives.
func (a *app) selectNewest() {
	view := a.views.activeView()
	if view == nil || len(a.visible) == 0 {
		return
	}
	a.autoSelecting = true
	a.picked = len(a.visible) - 1
	a.pickedID = a.flows[a.visible[a.picked]].id
	a.choosePicked()
	view.list.ScrollToEnd()
	a.lastPicked = a.picked
	a.newRows = 0
	a.autoSelecting = false
}

// following is whether the list is following new traffic.
func (a *app) following() bool {
	return a.autoSelect
}

// follow puts the list back on the newest request and follows again.
func (a *app) follow() {
	a.workbench.focused = false
	a.autoSelect = true
	a.selectNewest()
}

// footerChip is one tool that is on, as the footer shows it: what it says,
// what it opens, and how it turns off.
type footerChip struct {
	id, label, tip string
	// keys draws a shortcut as Kbd caps in the chip's tip.
	keys      []string
	icon      *ui.SVG
	open, off func()
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
		state := locales.Stopped()
		if s.Active {
			state = locales.RecordingState()
		} else if s.Busy {
			state = locales.StoppingState()
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
		chips = append(chips, footerChip{id: "breakpoints", label: locales.BreakpointsChip(), tip: locales.EditBreakpointRules(), icon: breakIcon,
			open: a.openRules, off: func() {
				a.breakOn = false
				if a.srv != nil {
					a.srv.SetBreak(false)
				}
			}})
	}
	if throttled {
		chips = append(chips, footerChip{id: "throttle", label: a.throttle.Name, tip: locales.ChangeNetworkConditions(), icon: networkIcon,
			open: func() { a.openNetwork() }, off: func() { a.setThrottle(capture.Presets[0]) }})
	}
	if networkRules > 0 {
		chips = append(chips, footerChip{id: "network-rules", label: locales.NetworkRuleCount(networkRules),
			tip: locales.EditScopedNetworkConditions(), icon: networkIcon, open: a.openNetworkRules, off: func() { a.disableNetworkRules() }})
	}
	if noCaching {
		chips = append(chips, footerChip{id: "no-caching", label: locales.NoCaching(), tip: locales.ChangeCachingAndNetworkConditions(), icon: cacheIcon,
			open: func() { a.openNetwork() }, off: func() { a.srv.SetNoCaching(false) }})
	}
	if a.allowOn {
		chips = append(chips, footerChip{id: "allow-list", label: locales.AllowListChip(), tip: locales.EditTheAllowList(), icon: filterIcon,
			open: func() { a.openSettings(4) }, off: func() {
				a.allowOn = false
				a.applyAllow()
			}})
	}
	if scenario != "" {
		chips = append(chips, footerChip{id: "scenario", label: locales.ScenarioLabel(scenario), tip: locales.ManageScenario(scenario), icon: layersIcon,
			open: a.openRules, off: func() {
				a.selectRuleScenario("")
				if a.ruleUI.problem != "" {
					a.warn(locales.CouldNotDisableScenario(a.ruleUI.problem))
				}
			}})
	}
	// A Host profile that is on says which one, and nothing is said while none
	// is: a chip that reads "Off" is a switch for a feature nobody turned on.
	if hostsLabel := a.hostsLabel(); hostsLabel != "" {
		hosts := footerChip{id: "hosts", label: hostsLabel, openLabel: locales.HostProfiles(),
			tip: locales.SwitchHostProfiles(), keys: hostsKeys(), icon: hostsIcon, off: a.hostsOff}
		hosts.open = func() { a.profiles.menuOpen = !a.profiles.menuOpen }
		hosts.panel = func(anchor ui.Element) {
			ui.PopoverBase(c, anchor, &a.profiles.menuOpen, func(panel ui.Element) {
				uikit.PopoverPanel(c, panel).Width(320).Padding(c.Theme().Space(uikit.SpaceM)).Gap(c.Theme().Space(uikit.SpaceXS)).
					Margin(c.Theme().Space(uikit.SpaceS), 0, 0, 0)
				a.hostsMenu(c, c.Theme())
			})
		}
		chips = append(chips, hosts)
	}
	if a.upstreamProblem != "" {
		label := locales.UpstreamError()
		if a.upstreamLoading {
			label = locales.UpstreamLoading()
		}
		chips = append(chips, footerChip{id: "upstream-error", label: label, tip: a.upstreamProblem, icon: upstreamIcon, danger: !a.upstreamLoading,
			open: func() { a.openSettings(1) }, off: a.clearUpstream})
	} else if a.upstream != "" {
		chips = append(chips, footerChip{id: "upstream", label: locales.ShellUpstream(), tip: locales.EditUpstreamProxySettings(), icon: upstreamIcon,
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
	gap := t.Space(uikit.SpaceXS)
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
			uikit.MenuFace(c, func() {
				b.PopoverMenuButton(func(m *ui.Menu) {
					var items []action
					for _, chip := range rest {
						if chip.open != nil {
							items = append(items, do("footer.tool."+chip.id, chip.label, chip.open))
						}
					}
					actionMenu(m, items)
				}, uikit.MenuButtonOptions(c))
			})
		}
	})
}

// footerRoom is how much of the footer's end the tools may take: the window
// less its padding, the actions at the start and the counts in the middle.
func (a *app) footerRoom(c *ui.Context, t *ui.Theme) float32 {
	padding, gap := 2*t.Space(uikit.SpaceL), t.Space(uikit.SpaceM)
	room := a.windowWidth - padding - 4*gap
	room -= paneTextWidth(c, a.footerCountsText(), uikit.TextSmall, 400)
	// The proxy state and its pause switch at the start, the notifications
	// at the end.
	room -= uikit.WidthPicker + uikit.HeightRowAction + uikit.HeightControl + 2*gap
	return max(room, 0)
}

// footerChipWidth is how wide one tool chip is: its icon, its label, the
// button that turns it off and the chip's own padding.
func footerChipWidth(c *ui.Context, chip footerChip) float32 {
	return 2*c.Theme().Space(uikit.SpaceS) + uikit.IconInline + c.Theme().Space(uikit.SpaceXS) + paneTextWidth(c, chip.label, uikit.TextSmall, 400) + 20
}

// footerMoreWidth is how wide the count of folded tools is.
func footerMoreWidth(c *ui.Context, n int) float32 {
	return paneTextWidth(c, footerMoreLabel(n), uikit.TextSmall, 400) + 2*c.Theme().Space(uikit.SpaceM)
}

func footerMoreLabel(n int) string { return fmt.Sprintf("+%d", n) }

// footerChipRow draws one tool that is on: its label opens the editor that
// changes it, and the close button turns it off. The bar stays quiet until the
// pointer reaches the chip.
func footerChipRow(c *ui.Context, t *ui.Theme, chip footerChip) {
	p := uikit.Colors(t)
	ink := t.TextMuted
	if chip.danger {
		ink = t.Danger
	}
	row := ui.Row(c.Key(chip.id)).Height(uikit.HeightControlSmall).AlignItems(ui.Center).
		Radius(uikit.RadiusInner)
	if row.Hovered() {
		row.Background(p.MutedHover)
	}
	row.Children(func() {
		open := chip.openLabel
		if open == "" {
			open = locales.ActiveTool(chip.label)
		}
		label := uikit.ToolbarButton(c, open, chip.tip, false, chip.keys...).Height(uikit.HeightControlSmall).
			PaddingStart(t.Space(uikit.SpaceS)).PaddingEnd(t.Space(uikit.SpaceHair)).FontSize(uikit.Scaled(uikit.TextSmall)).
			Children(func() {
				ui.Icon(c, chip.icon).Size(uikit.IconInline, uikit.IconInline)
				uikit.Small(c, chip.label).MaxWidth(180).SingleLine()
				if chip.panel != nil {
					ui.Icon(c, chevronIcon).Size(uikit.IconInline, uikit.IconInline)
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
			Size(uikit.HeightControlSmall-4, uikit.HeightControlSmall-4).Radius(uikit.RadiusInner)
		if chip.danger {
			close.TextColor(ink)
		}
		close.OnClick(chip.off)
	})
}

// moreActions is the title bar's gear menu, ordered as VS Code's Manage:
// the command palette, Settings and the debugging tools that share its
// window, the stores and setup, then the less frequent menus one step away.
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
	// Network holds the throttles, caching and upstream with the client tools;
	// client setup, diagnostics and HTTPS decryption are the menu's top.
	network := []action{
		submenu(locales.OneClickSetup(), oneClick...).in("tools"),
		pick(global, "network.websocket").in("tools"),
		pick(global, "network.sse").in("tools"),
	}
	network = append(network, a.networkTools()...)
	network = append(network,
		pick(global, "network.devices").checked(checkbox(a.deviceUI.gateway != nil)).in("devices"),
		do("capture.ignored-hosts", locales.IgnoredHosts(), func() { a.ignoreOpen = true }).in("lists"),
		do("capture.ignored-apps", locales.IgnoredApps(), func() { a.ignoreAppsOpen = true }).in("lists"),
		pick(a.hostsActions(), "hosts.manage").as(locales.HostProfiles2()).in("lists"),
	)
	session := []action{
		pick(global, "collection.record"),
		pick(global, "flow.replay").as(locales.ReplayChosen()),
		do("capture.export-har", locales.ExportHar(), a.exportHAR).in("files"),
		pick(global, "capture.export-chosen").as(locales.ExportChosenHar()).in("files"),
		pick(global, "capture.export-csv").in("files"),
		pick(global, "capture.export-chosen-csv").as(locales.ExportChosenCsv()).in("files"),
		do("capture.choose-shown", locales.ChooseAllShown(), func() {
			for _, at := range a.visible {
				a.chosen.Add(a.flows[at].id)
			}
			a.inform(locales.ChosenCount(a.chosen.Len()))
		}).in("files"),
		do("capture.clear-choice", locales.ClearChoice(), a.chosen.Clear).in("files"),
		do("session.save-as", locales.SaveSession(), a.saveSession).in("files"),
		pick(global, "session.open").in("files"),
		do("capture.import-har", locales.ImportHar(), a.importHAR).in("files"),
		pick(global, "capture.import-session").in("import"),
		pick(global, "capture.import-proxyman-folder").in("import"),
		pick(global, "rules.import-charles").in("import"),
		pick(global, "collection.import"),
		pick(global, "collection.import-url").in("import"),
		pick(global, "collection.import-folder").in("import"),
		pick(global, "request.import-curl").in("import"),
		do("api.generate", locales.GenerateOpenapi(), a.openAPIGeneration).
			when(len(a.requests) > 0, locales.SaveARequestToA()).in("api"),
	}
	view := a.pickActions(c, "view.focus-inspector", "view.layout-right", "view.layout-bottom", "view.sidebar", "view.json-path")
	view = append(view, pick(global, "capture.filter").as(locales.Filter()).in("filter"))
	if a.filtersActive() {
		view = append(view, do("capture.clear-filters", locales.ClearFilters(), a.clearFilters).in("filter"))
	}
	view = append(view,
		do("capture.add-column", locales.AddColumn(), a.openColumn).in("columns"),
		do("protobuf.schema", locales.ProtobufSchema(), func() { a.protobuf.open = true }).in("protobuf"),
	)
	tools := []action{
		pick(global, "app.palette").as(locales.CommandPalette()).in("palette"),
		pick(global, "app.settings").in("settings"),
		pick(global, "tools.rules").as(locales.DebuggingRules2()).in("settings"),
		do("tools.cookies", locales.Cookies(), a.openCookies).in("stores"),
		do("tools.vault", locales.Vault(), a.openVault).in("stores"),
		do("setup.client", locales.ClientSetup(), a.openClientSetup).in("setup"),
		do("setup.https", locales.HttpsDecryption(), func() { a.sslOpen = true }).in("setup"),
		do("setup.diagnostics", locales.Diagnostics(), func() { a.goTo("diagnostics") }).in("setup"),
	}
	return append(tools,
		submenu(locales.Network(), network...).in("more"),
		submenu(locales.Session(), session...).in("more"),
		submenu(locales.View(), view...).in("more"),
	)
}

// syncSections keeps the sidebar's one choice and the content on screen in
// step when another part of the app changes them: a filter reset, a new
// investigation, a contract or a request opened from elsewhere.
func (a *app) syncSections() {
	if a.page == pageDiagnostics {
		a.sidebarChoice = "diagnostics"
		return
	}
	if a.page == pageOverview {
		a.sidebarChoice = "overview"
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
			a.showRequestWorkspace()
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

func cmpOr(value, fallback string) string {
	if value == "" {
		return fallback
	}
	return value
}
