package app

import (
	"cmp"
	"fmt"
	"maps"
	"net/http"
	"net/url"
	"slices"
	"strconv"
	"strings"
	"unicode/utf8"

	"github.com/baba983/vscroxy/internal/request"
	"github.com/egoist/mygo/ui"
	"github.com/egoist/mygo/ui/editor"
)

// inspectorState is what the inspector itself remembers: the tab of each
// side, and which of the exchange-wide views is showing instead of them.
type inspectorState struct {
	tab     [2]int // request, response
	pane    int
	percent float32 // width share of the request side
	ready   bool    // the response tab has taken its first default
	focus   int     // side that owns shared readers: 0 response, 1 request
	reveal  [2]bool // credentials are masked until the reader asks
}

const (
	inspectorSplit = iota
	inspectorDiff
	inspectorConnection
	inspectorMessages
)

// Request and response tabs are separate lists; the constants keep each
// side's index named where it is used.
const (
	reqTabHeaders = iota
	reqTabParams
	reqTabBody
	reqTabRaw
)

const (
	resTabHeaders = iota
	resTabBody
	resTabRaw
	resTabTiming
	resTabMessages
)

func requestTabLabels() []string {
	return []string{"Headers", "Params", "Body", "Raw"}
}

func responseTabLabels(f flow) []string {
	labels := []string{"Headers", "Body", "Raw", "Timing"}
	if f.kind == "ws" || f.kind == "sse" {
		labels = append(labels, "Messages")
	}
	return labels
}

// detail is the inspector under the traffic list: one compact summary, then
// the request and the response beside each other, each with its own tabs.
func (a *app) detail(c *ui.Context) {
	// The table may change the choice during this frame. Sync after it draws,
	// so all detail widgets see the same selected request immediately.
	a.syncDetail()
	if !a.inspect.ready {
		// A captured exchange is read by its response body first; the request
		// side opens its body when it has one, its headers otherwise.
		a.inspect.tab[1] = resTabBody
		if f, ok := a.current(); ok && (f.reqBody != "" || f.reqFile != "" || f.reqSize > 0) {
			a.inspect.tab[0] = reqTabBody
		}
		a.inspect.ready = true
	}
	t := c.Theme()
	ui.Column(c).Bind(&a.workbench.inspector).Focusable().Label("Request inspector").
		OnShortcut(0, ui.KeyEscape, func() { a.focusTraffic(c) }).
		Fill().Background(t.Background).Children(func() {
		if selected := a.selectedFlows(); len(selected) > 1 {
			a.analysisDetail(c, selected)
			return
		}
		f, ok := a.current()
		if ok && f.kind == "connect" {
			a.connectDetail(c, t, f)
			return
		}
		a.inspectorSummary(c, t, f, ok)
		if ok {
			a.replayLinks(c, f)
			if len(f.actions) > 0 {
				ui.Text(c, "Applied: "+strings.Join(f.actions, " · ")).FontSize(11).TextColor(t.Accent).
					Padding(4, 14).SingleLine().Tooltip(strings.Join(f.actions, "\n"))
			}
		}
		if !ok {
			ui.Text(c, "Capture a request to inspect it.").FontSize(12).TextColor(t.TextMuted).Padding(16)
			return
		}
		switch a.inspect.pane {
		case inspectorDiff:
			a.inspectorPaneBar(c, t, "Diff", "Previous and current response for this method and URL")
			a.diffPane(c, f)
		case inspectorConnection:
			a.inspectorPaneBar(c, t, "Connection", "Recorded connection evidence at capture time")
			a.connectionView(c, f)
		case inspectorMessages:
			a.inspectorPaneBar(c, t, "Messages", "WebSocket messages or SSE events")
			a.messages(c, f)
		default:
			a.splitInspector(c, f)
		}
	})
}

// connectDetail keeps the tunnel's own two views: what the client must do to
// decrypt it, and what the proxy recorded while opening it.
func (a *app) connectDetail(c *ui.Context, t *ui.Theme, f flow) {
	if a.tab > 1 {
		a.tab = 0
	}
	ui.Row(c).Height(40).Padding(0, 14).Gap(10).AlignItems(ui.Center).Children(func() {
		ui.Text(c, cmp.Or(f.process, "Unidentified client")).FontSize(13).Bold().SingleLine().Tooltip(f.processPath)
		ui.Text(c, f.host).FontSize(12).Font("monospace").TextColor(t.TextMuted).SingleLine().Tooltip(f.url)
		if f.conn.error != "" {
			ui.Text(c, cmp.Or(f.conn.errorStage, "Connection")+" failed").FontSize(12).TextColor(t.Danger)
		}
		ui.Spacer(c)
		explain := ui.Button(c, "Explain").Height(26).FontSize(11).Tooltip("Explain the recorded connection failure")
		if explain.Clicked() {
			a.explainOpen = !a.explainOpen
		}
		ui.PopoverBase(c, explain, &a.explainOpen, func(panel ui.Element) {
			panel.Margin(8, 0, 0, 0).Padding(0).Radius(10).Background(t.Background).
				Border(1, t.Border).Shadow(0, 8, 24, 0, ui.RGBA(0, 0, 0, 0.22)).
				Children(func() { a.explainConnection(c, f) })
		})
		a.desktopButton(c, "focus", "Focus")
	})
	ui.Divider(c)
	ui.Tabs(c, &a.tab, "HTTPS setup", "Connection").Padding(0, 10)
	if a.tab == 1 {
		a.connectionView(c, f)
		return
	}
	a.connectPanel(c, f)
}

// inspectorSummary is the one line that names what is inspected and what the
// exchange produced, with the actions that start from it.
func (a *app) inspectorSummary(c *ui.Context, t *ui.Theme, f flow, ok bool) {
	// A narrow inspector keeps the name, the result and the one action that
	// starts the next step; the rest stays in the overflow.
	compact := a.inspectorWidth(c) < 1100
	ui.Row(c).Height(36).Padding(0, 12).Gap(8).AlignItems(ui.Center).Children(func() {
		if !ok {
			ui.Text(c, "No request chosen").FontSize(12).TextColor(t.TextMuted).Grow(1)
			return
		}
		title := fmt.Sprintf("%s %s", f.method, cmp.Or(f.url, f.scheme+"://"+f.host+f.path))
		ui.Text(c, title).FontSize(12).Font("monospace").TextColor(t.Text).SingleLine().MinWidth(80).Grow(1).Tooltip(title)
		a.exchangeResult(c, t, f, compact)
		// One action stands out: starting the edit-and-replay loop. Copying
		// and history are quiet beside it.
		a.desktopButton(c, "edit", "Edit & replay").Height(30).FontSize(12).Background(t.Accent).TextColor(t.AccentText)
		if !compact {
			copy := ui.ButtonBase(c).Label("Copy as cURL").Height(26).Padding(0, 8).Gap(6).Radius(5).AlignItems(ui.Center).TextColor(t.TextMuted)
			if copy.Hovered() {
				copy.TextColor(t.Text)
			}
			if copy.Clicked() {
				a.copyCurl(c, f)
			}
			copy.Children(func() {
				ui.Icon(c, copyIcon).Size(13, 13)
				ui.Text(c, "Copy as cURL").FontSize(11)
			})
		}
		info := ui.ButtonBase(c).Size(26, 26).Radius(5).Label("Connection details").Tooltip("Connection: addresses, DNS and TLS").TextColor(t.TextMuted).Children(func() {
			ui.Icon(c, infoIcon).Size(14, 14)
		})
		info.OnClick(func() { a.connOpen = !a.connOpen })
		ui.Popover(c, info, &a.connOpen, func() { a.connPanel(c, t, f) })
		if !compact {
			if ui.ButtonBase(c).Label("Debug history").Height(26).Padding(0, 8).Gap(6).Radius(5).AlignItems(ui.Center).
				Tooltip("Retained runs and linked drafts for this capture").TextColor(t.TextMuted).Children(func() {
				ui.Icon(c, sendHistoryIcon).Size(14, 14)
				ui.Text(c, "Debug history").FontSize(12)
			}).Clicked() {
				a.openDebugHistory(newDebugSource(f))
			}
		}
		a.inspectorMenu(c, t, f)
	})
	ui.Divider(c)
}

// exchangeResult spells the status as a person reads it, then the two numbers
// that say how much work the exchange took and carried.
func (a *app) exchangeResult(c *ui.Context, t *ui.Theme, f flow, compact bool) {
	status := fmt.Sprintf("%d %s", f.status, http.StatusText(f.status))
	if compact && f.conn.error == "" && f.kind == "" {
		status = strconv.Itoa(f.status)
	}
	color := f.statusColor(t)
	if f.kind == "connect" {
		status, color = "Tunnel", t.TextMuted
	}
	if f.conn.error != "" {
		status, color = "Failed", t.Danger
		if f.kind == "connect" && f.status == 200 {
			status = "CONNECT 200 · failed"
		}
	}
	chip := ui.Text(c, status).FontSize(12).TextColor(color).SingleLine()
	if f.conn.error != "" {
		chip = chip.Tooltip(cmp.Or(f.conn.errorStage, "Connection") + ": " + f.conn.error)
	} else if doc := statusDoc(f.status); doc != "" {
		chip = chip.Tooltip(doc)
	}
	if !compact {
		ui.Text(c, "·").FontSize(12).TextColor(t.Border)
		ui.Text(c, fmt.Sprintf("%d ms", f.millis)).FontSize(12).TextColor(t.TextMuted).SingleLine()
		ui.Text(c, "·").FontSize(12).TextColor(t.Border)
		ui.Text(c, humanBytes(f.bytes)).FontSize(12).TextColor(t.TextMuted).SingleLine()
	}
	if f.conn.error != "" {
		explain := ui.Button(c, "Explain").Height(26).FontSize(11).Tooltip("Explain the recorded connection failure")
		if explain.Clicked() {
			a.explainOpen = !a.explainOpen
		}
		ui.PopoverBase(c, explain, &a.explainOpen, func(panel ui.Element) {
			panel.Margin(8, 0, 0, 0).Padding(0).Radius(10).Background(t.Background).
				Border(1, t.Border).Shadow(0, 8, 24, 0, ui.RGBA(0, 0, 0, 0.22)).
				Children(func() { a.explainConnection(c, f) })
		})
	}
}

// inspectorMenu holds the exchange-wide views and the layout choices, so the
// default screen stays request beside response.
func (a *app) inspectorMenu(c *ui.Context, t *ui.Theme, f flow) {
	ui.MenuButton(c, "⋯", func(m *ui.Menu) {
		split := m.Item("Request & response").Checked(a.inspect.pane == inspectorSplit)
		if split.Chosen() {
			a.inspect.pane = inspectorSplit
		}
		if m.Item("Diff responses").Checked(a.inspect.pane == inspectorDiff).Disabled(!a.diffBaseline).Chosen() {
			a.inspect.pane = inspectorDiff
		}
		if m.Item("Connection").Checked(a.inspect.pane == inspectorConnection).Chosen() {
			a.inspect.pane = inspectorConnection
		}
		if f.kind == "ws" || f.kind == "sse" {
			if m.Item("Messages").Checked(a.inspect.pane == inspectorMessages).Chosen() {
				a.inspect.pane = inspectorMessages
			}
		}
		m.Separator()
		a.desktopMenuItem(c, m, "replay", "Replay…")
		if m.Item("Add to investigation…").Chosen() {
			a.addToInvestigation(f)
		}
		if m.Item("Connection details").Chosen() {
			a.connOpen = !a.connOpen
		}
		m.Separator()
		for _, action := range a.desktopActions(c) {
			if action.id == "focus" || action.id == "layout-bottom" || action.id == "layout-right" || action.id == "layout-auto" {
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
	}).Label("Inspector options").Height(26).Padding(0, 6).FontSize(14).TextColor(t.TextMuted).Border(0, t.Border)
}

// inspectorWidth is the room the inspector has, which is the window's when
// it sits under the list and its share of the split when it sits beside it.
func (a *app) inspectorWidth(c *ui.Context) float32 {
	width, _ := c.Size()
	if a.workbench.sideBySide {
		share := a.workbench.sidePercent
		if share == 0 {
			share = 64
		}
		width = width * (100 - share) / 100
	}
	return width
}

// splitInspector puts the request beside the response, or above it when the
// inspector is too narrow for two readable panes.
func (a *app) splitInspector(c *ui.Context, f flow) {
	if a.inspect.percent == 0 {
		a.inspect.percent = 50
	}
	request := func() { a.requestPane(c, f) }
	response := func() { a.responsePane(c, f) }
	if a.inspectorWidth(c) < 760 {
		ui.SplitVerticalPercent(c.Key("inspector-sides"), &a.inspect.percent, request, response).Grow(1)
		return
	}
	ui.SplitPercent(c.Key("inspector-sides"), &a.inspect.percent, request, response).Grow(1)
}

func (a *app) requestPane(c *ui.Context, f flow) {
	t := c.Theme()
	ui.Column(c).Fill().Background(t.Background).Children(func() {
		a.paneTabs(c, t, "Request", &a.inspect.tab[0], requestTabLabels(), 1)
		switch a.inspect.tab[0] {
		case reqTabHeaders:
			a.headersSide(c, f, 1)
		case reqTabParams:
			a.paramsSide(c, f)
		case reqTabBody:
			a.bodySideView(c, f, 1)
		case reqTabRaw:
			a.rawSide(c, f, 1)
		}
	})
}

func (a *app) responsePane(c *ui.Context, f flow) {
	t := c.Theme()
	labels := responseTabLabels(f)
	if a.inspect.tab[1] >= len(labels) {
		a.inspect.tab[1] = resTabHeaders
	}
	ui.Column(c).Fill().Background(t.Background).Children(func() {
		a.paneTabs(c, t, "Response", &a.inspect.tab[1], labels, 0)
		switch a.inspect.tab[1] {
		case resTabHeaders:
			a.headersSide(c, f, 0)
		case resTabBody:
			a.bodySideView(c, f, 0)
		case resTabRaw:
			a.rawSide(c, f, 0)
		case resTabTiming:
			a.timingView(c, f)
		case resTabMessages:
			a.messages(c, f)
		}
	})
}

// paneTabs names the side, its tabs and — while Body is chosen — the format
// the body is read in. The format is a menu rather than a row of segments so
// two visible panes never show the same control twice.
func (a *app) paneTabs(c *ui.Context, t *ui.Theme, side string, selected *int, labels []string, bodySide int) {
	bodyTab := reqTabBody
	if bodySide == 0 {
		bodyTab = resTabBody
	}
	ui.Row(c).Height(32).Padding(0, 8).Gap(4).AlignItems(ui.Center).Children(func() {
		title := ui.ButtonBase(c).Label(side+" pane").Height(24).Padding(0, 4).Radius(4).AlignItems(ui.Center).
			TextColor(t.TextMuted).Tooltip("Read this side in shared readers")
		if a.inspect.focus == bodySide {
			title.TextColor(t.Text)
		}
		if title.Clicked() {
			a.inspect.focus = bodySide
		}
		title.Children(func() { ui.Text(c, side).FontSize(13).FontWeight(600) })
		ui.Spacer(c)
		if *selected == bodyTab {
			a.bodyFormatMenu(c, t, bodySide)
			ui.MenuButton(c, "⋯", func(m *ui.Menu) {
				if m.Item("Find in text…").Chosen() {
					if reader := a.bodyText[bodySide]; reader != nil {
						reader.Run(editor.Find)
					}
				}
				if m.Item("Copy shown body").Chosen() {
					a.copyPaneBody(c, bodySide)
				}
			}).Label(side+" pane options").Height(22).Padding(0, 5).FontSize(13).TextColor(t.TextMuted).Border(0, t.Border)
		}
	})
	ui.ScrollHorizontal(c).Height(36).Padding(0, 8).
		Border(0, t.Border).BorderWidth(0, 0, 1, 0).Children(func() {
		flatTabs(c, selected, side+" pane", labels...)
	})
}

// bodyFormatMenu shows and chooses the representation of one side's body.
func (a *app) bodyFormatMenu(c *ui.Context, t *ui.Theme, side int) {
	f, ok := a.current()
	if !ok {
		return
	}
	modes := a.bodyModes(f, side)
	mode := a.bodyMode[side]
	if mode >= len(modes) {
		mode = 0
	}
	// The menu names what the pane shows, which is the resolved mode while
	// the choice is Auto.
	shown := modes[mode]
	if mode == 0 {
		body := a.body
		if side == 1 {
			body = a.reqBody
		}
		if resolved := a.autoBodyMode(f, side, body); resolved < len(modes) {
			shown = modes[resolved]
		}
	}
	ui.MenuButton(c, shown, func(m *ui.Menu) {
		for i, name := range modes {
			if m.Item(name).Checked(i == mode).Chosen() {
				a.bodyMode[side] = i
				a.bodyQuery[side] = ""
			}
		}
	}).Label(bodyFormatLabel(side)).Height(28).Padding(0, 8).FontSize(12).TextColor(t.TextMuted).Border(1, t.Border)
}

// copyPaneBody copies exactly the bytes the pane shows, without formatting
// or display warnings.
func (a *app) copyPaneBody(c *ui.Context, side int) {
	body := a.body
	if side == 1 {
		body = a.reqBody
	}
	if !utf8.ValidString(body) || strings.ContainsRune(body, 0) {
		a.setMessage("Binary bytes cannot be copied to a text clipboard; use HAR or session export")
		return
	}
	c.Services().WriteClipboard(body)
	a.setMessage("Copied shown body; review credentials before sharing")
}

func bodyFormatLabel(side int) string {
	if side == 1 {
		return "Request body format"
	}
	return "Response body format"
}

// inspectorPaneBar names an exchange-wide view and gives the way back.
func (a *app) inspectorPaneBar(c *ui.Context, t *ui.Theme, title, note string) {
	ui.Row(c).Height(34).Padding(0, 8).Gap(8).AlignItems(ui.Center).Children(func() {
		if ui.ButtonBase(c).Size(24, 24).Radius(5).Label("Back to request and response").Tooltip("Request and response").
			TextColor(t.TextMuted).Children(func() {
			ui.Icon(c, backIcon).Size(13, 13)
		}).Clicked() {
			a.inspect.pane = inspectorSplit
		}
		ui.Text(c, title).FontSize(12).Bold().TextColor(t.Text)
		ui.Text(c, note).FontSize(11).TextColor(t.TextMuted).SingleLine().Tooltip(note)
		ui.Spacer(c)
	})
	ui.Divider(c)
}

// headersSide lists one side's start line and headers.
func (a *app) headersSide(c *ui.Context, f flow, side int) {
	t := c.Theme()
	headers, raw, trailers := f.reqHeader, f.reqHeaders, f.reqTrailer
	line := strings.TrimSpace(f.conn.proto + " " + f.method + " " + f.path)
	note := ""
	if side == 0 {
		headers, raw, trailers = f.respHeader, f.resHeaders, f.resTrailer
		line = "No response"
		if f.status != 0 {
			line = fmt.Sprintf("%d %s", f.status, http.StatusText(f.status))
			if f.resProto != "" {
				line = f.resProto + " " + line
			}
		}
		note = statusDoc(f.status)
	}
	ui.Column(c).Fill().Children(func() {
		ui.Row(c).Padding(6, 12).Gap(8).AlignItems(ui.Center).Children(func() {
			ui.Text(c, line).FontSize(11).Font("monospace").TextColor(t.TextMuted).SingleLine().Selectable().Grow(1).
				Tooltip(strings.TrimSpace(line + "\n" + note))
			if len(headers) > 0 {
				ui.Text(c, fmt.Sprintf("%d headers", len(headers))).FontSize(10).TextColor(t.TextMuted)
			}
			if hasSensitiveHeader(headers) {
				label := "Reveal"
				if a.inspect.reveal[side] {
					label = "Hide"
				}
				if ui.ButtonBase(c).Label("Reveal credentials").Height(20).Padding(0, 5).Radius(4).AlignItems(ui.Center).
					TextColor(t.TextMuted).Tooltip("Credentials are masked so a shared screen or recording does not leak them").
					Children(func() { ui.Text(c, label).FontSize(10) }).Clicked() {
					a.inspect.reveal[side] = !a.inspect.reveal[side]
				}
			}
		})
		ui.Scroll(c).TrackScroll(&a.headerScroll[side]).Grow(1).FillWidth().Children(func() {
			if len(headers) == 0 {
				if raw == "" {
					ui.Text(c, "No headers recorded").FontSize(12).TextColor(t.TextMuted).Padding(12)
				} else {
					ui.Text(c, raw).FontSize(12).Font("monospace").TextColor(t.Text).Selectable().Padding(12)
				}
			}
			for _, key := range slices.Sorted(maps.Keys(headers)) {
				value := strings.Join(headers[key], "\n")
				if sensitiveHeader(key) && !a.inspect.reveal[side] {
					value = "••••••••  masked"
				}
				ui.Row(c).Gap(12).Padding(6, 12).AlignItems(ui.Start).FillWidth().Children(func() {
					ui.Text(c, key).FontSize(11).Font("monospace").TextColor(t.TextMuted).Width(124).SingleLine().
						Tooltip(strings.TrimSpace(key + "\n" + headerDoc(key))).ContextMenu(a.headerFilterMenu(c, key, headers[key]))
					ui.Text(c, value).FontSize(12).Font("monospace").TextColor(t.Text).Grow(1).Selectable().ContextMenu(a.headerFilterMenu(c, key, headers[key]))
				})
				ui.Divider(c).Background(t.Border.Alpha(0.45))
			}
			if len(trailers) > 0 {
				ui.Text(c, "Trailers").FontSize(11).Bold().TextColor(t.TextMuted).Padding(8, 12, 4).FillWidth()
				for _, key := range slices.Sorted(maps.Keys(trailers)) {
					ui.Row(c).Gap(12).Padding(6, 12).AlignItems(ui.Start).FillWidth().Children(func() {
						ui.Text(c, key).FontSize(11).Font("monospace").TextColor(t.TextMuted).Width(124).SingleLine()
						ui.Text(c, strings.Join(trailers[key], "\n")).FontSize(12).Font("monospace").TextColor(t.Text).Grow(1).Selectable()
					})
				}
			}
		})
	})
}

// paramsSide shows the query the request carried, which the URL keeps
// verbatim, so nobody has to read percent escapes by eye.
func (a *app) paramsSide(c *ui.Context, f flow) {
	t := c.Theme()
	parsed, err := url.Parse(cmp.Or(f.url, f.scheme+"://"+f.host+f.path))
	if err != nil || len(parsed.Query()) == 0 {
		ui.Text(c, "No query parameters in this request.").FontSize(12).TextColor(t.TextMuted).Padding(14)
		return
	}
	ui.Scroll(c).Grow(1).FillWidth().Children(func() {
		for _, key := range slices.Sorted(maps.Keys(parsed.Query())) {
			for _, value := range parsed.Query()[key] {
				ui.Row(c).Gap(12).Padding(6, 12).AlignItems(ui.Start).FillWidth().Children(func() {
					ui.Text(c, key).FontSize(11).Font("monospace").TextColor(t.TextMuted).Width(140).SingleLine()
					ui.Text(c, value).FontSize(12).Font("monospace").TextColor(t.Text).Grow(1).Selectable()
				})
				ui.Divider(c).Background(t.Border.Alpha(0.45))
			}
		}
	})
}

// rawSide shows the message as it went over the wire, without the tabs'
// interpretation.
func (a *app) rawSide(c *ui.Context, f flow, side int) {
	t := c.Theme()
	if a.rawEditor[side] == nil {
		buf := editor.NewBuffer(a.rawText[side])
		buf.SetLanguage(editor.LanguageNamed("http"))
		a.rawEditor[side] = &editor.State{Buffer: buf, ReadOnly: true, NoScrollPastEnd: true, SoftWrap: true, Label: "Raw message"}
	}
	ui.Row(c).Padding(6, 12).Gap(8).AlignItems(ui.Center).Children(func() {
		ui.Text(c, "As sent and received · capture bytes").FontSize(11).TextColor(t.TextMuted).Grow(1)
		if ui.Button(c, "Copy raw").Height(22).FontSize(11).Disabled(a.rawText[side] == "").Clicked() {
			c.Services().WriteClipboard(a.rawText[side])
			a.setMessage("Copied raw message; review credentials before sharing")
		}
	})
	editor.View(c, a.rawEditor[side]).Grow(1)
}

// diffPane compares the previous response for the same method and URL with
// the one captured now.
func (a *app) diffPane(c *ui.Context, f flow) {
	t := c.Theme()
	if !a.diffBaseline {
		ui.Text(c, "No previous response for this method and URL. Replay the request to compare responses.").
			FontSize(12).TextColor(t.TextMuted).Padding(16)
		return
	}
	ui.Row(c).Padding(6, 14).Children(func() {
		ui.Text(c, "Previous response (left)").Grow(1).FontSize(11).TextColor(t.TextMuted)
		ui.Text(c, "Current response (right)").Grow(1).FontSize(11).TextColor(t.TextMuted)
	})
	if a.diff != nil {
		editor.DiffView(c, a.diff).Grow(1).FillWidth()
	}
}

// sensitiveHeader marks the headers whose values are credentials. They are
// masked until the reader reveals them, so a shared screen does not leak a
// session or a token by default.
func sensitiveHeader(name string) bool {
	n := strings.ToLower(name)
	switch n {
	case "authorization", "proxy-authorization", "cookie", "set-cookie", "x-api-key", "api-key":
		return true
	}
	return strings.Contains(n, "token") || strings.Contains(n, "secret") ||
		strings.Contains(n, "password") || strings.Contains(n, "api-key")
}

func hasSensitiveHeader(headers http.Header) bool {
	for name := range headers {
		if sensitiveHeader(name) {
			return true
		}
	}
	return false
}

// rawMessage writes one side of the exchange as it went over the wire.
func rawMessage(f flow, side int) string {
	var b strings.Builder
	if side == 1 {
		fmt.Fprintf(&b, "%s %s HTTP/1.1\r\nHost: %s\r\n", cmp.Or(f.method, "GET"), cmp.Or(f.path, "/"), f.host)
		b.WriteString(request.HeaderBlock(f.reqHeader))
		b.WriteString("\r\n\r\n")
		b.WriteString(f.body(false))
		return b.String()
	}
	proto := cmp.Or(f.resProto, "HTTP/1.1")
	status := "no response"
	if f.status != 0 {
		status = fmt.Sprintf("%d %s", f.status, http.StatusText(f.status))
	}
	fmt.Fprintf(&b, "%s %s\r\n", proto, status)
	b.WriteString(request.HeaderBlock(f.respHeader))
	b.WriteString("\r\n\r\n")
	b.WriteString(f.body(true))
	return b.String()
}
