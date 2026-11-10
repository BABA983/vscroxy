package app

import (
	"cmp"
	"fmt"
	"github.com/baba983/vscroxy/internal/uikit"
	"maps"
	"net/http"
	"net/url"
	"slices"
	"strconv"
	"strings"
	"unicode/utf8"

	"github.com/baba983/vscroxy/internal/capture"
	"github.com/baba983/vscroxy/internal/request"
	"github.com/baba983/vscroxy/locales"
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
	reqTabCookies
)

const (
	resTabHeaders = iota
	resTabBody
	resTabRaw
	resTabTiming
	resTabCookies
	resTabMessages
)

func requestTabLabels() []string {
	return []string{locales.RequestHeadersLabel(), locales.Params(), locales.Body(), locales.Raw(), locales.Cookies2()}
}

func responseTabLabels(f flow) []string {
	labels := []string{locales.ResponseHeadersLabel(), locales.Body(), locales.Raw(), locales.TimingTab(), locales.Cookies2()}
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
	ui.Column(c).Bind(&a.workbench.inspector).Focusable().Label(locales.RequestInspector()).
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
		if !ok {
			ui.Row(c).Height(uikit.HeightLarge).Padding(0, t.Space(3)).AlignItems(ui.Center).Children(func() {
				uikit.Hint(c, locales.NoRequestChosen())
			})
			ui.Divider(c)
			uikit.Hint(c, locales.CaptureARequestToInspect()).Padding(t.Space(4))
			return
		}
		d, problem := a.liveDocument(f)
		a.capturedHeader(c, t, f, d)
		a.replayLinks(c, f)
		if len(f.actions) > 0 {
			ui.Text(c, locales.AppliedActions(strings.Join(f.actions, " · "))).FontSize(uikit.TextMeta).TextColor(t.Accent).
				Padding(4, 14).SingleLine().Tooltip(strings.Join(f.actions, "\n"))
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
			if d != nil {
				a.capturedDocument(c, t, f, d)
				return
			}
			a.capturedReadOnly(c, t, f, problem)
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
	ui.Row(c).Height(uikit.HeightLarge).Padding(0, t.Space(3)).Gap(t.Space(2)).AlignItems(ui.Center).Children(func() {
		ui.Text(c, cmp.Or(f.process, "Unidentified client")).FontSize(uikit.TextBody).FontWeight(600).SingleLine().Tooltip(f.processPath)
		uikit.Hint(c, f.host).Font(uikit.FontMono).SingleLine().Tooltip(f.url)
		if f.conn.error != "" {
			ui.Text(c, cmp.Or(f.conn.errorStage, "Connection")+" failed").FontSize(uikit.TextBody).TextColor(t.Danger)
		}
		ui.ScrollHorizontal(c).Grow(1).FillHeight().Children(func() {
			uikit.Tabs(c, &a.tab, "", locales.HttpsSetup(), locales.Connection())
		})
		if uikit.SecondaryButton(c, locales.Explain()).Tooltip(locales.DiagnoseThisConnection()).Clicked() {
			a.openDiagnostics(&f, diagnoseMac)
		}
		a.actionButton(c, "view.focus-inspector", "Focus")
	})
	ui.Divider(c)
	if a.tab == 1 {
		a.connectionView(c, f)
		return
	}
	a.connectPanel(c, f)
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
	chip := uikit.StatusPill(c, status, color).Shrink(0)
	if f.conn.error != "" {
		chip = chip.Tooltip(cmp.Or(f.conn.errorStage, "Connection") + ": " + f.conn.error)
	} else if doc := statusDoc(f.status); doc != "" {
		chip = chip.Tooltip(doc)
	}
	if !compact {
		ui.Text(c, "·").FontSize(uikit.TextBody).TextColor(t.Border)
		uikit.Hint(c, fmt.Sprintf("%d ms", f.millis)).FontFeatures("tnum").SingleLine()
		ui.Text(c, "·").FontSize(uikit.TextBody).TextColor(t.Border)
		uikit.Hint(c, humanBytes(f.bytes)).FontFeatures("tnum").SingleLine()
	}
}

// inspectorMenu holds the exchange-wide views and the layout choices, so the
// default screen stays request beside response.
func (a *app) inspectorMenu(c *ui.Context, t *ui.Theme, f flow) {
	uikit.MoreMenu(c, locales.InspectorOptions(), func(m *ui.Menu) { actionMenu(m, a.inspectorActions(c, f)) })
}

// inspectorWidth is the main pane's width beneath the list, its share beside
// the list, or the full window while the inspector is focused.
func (a *app) inspectorWidth(c *ui.Context) float32 {
	width := a.contentWidth(c)
	if a.workbench.focused {
		width, _ = c.Size()
	} else if a.workbench.sideBySide {
		share := a.workbench.sidePercent
		if share == 0 {
			share = 64
		}
		width = width * (100 - share) / 100
	}
	return width
}

// sidesStacked reports whether the request goes above the response: when the
// inspector is a column beside the list. Below the list, or filling the
// window, it is wide and short, so the two sides sit next to each other.
func (a *app) sidesStacked() bool {
	return a.workbench.sideBySide && !a.workbench.focused
}

// splitInspector puts the request beside the response, or above it in a
// column beside the list.
func (a *app) splitInspector(c *ui.Context, f flow) {
	if a.inspect.percent == 0 {
		a.inspect.percent = 50
	}
	request := func() { a.requestPane(c, f) }
	response := func() { a.responsePane(c, f) }
	if a.sidesStacked() {
		ui.SplitVerticalPercent(c.Key("inspector-sides"), &a.inspect.percent, request, response).SplitReset(50).Grow(1)
		return
	}
	ui.SplitPercent(c.Key("inspector-sides"), &a.inspect.percent, request, response).SplitReset(50).Grow(1)
}

func (a *app) requestPane(c *ui.Context, f flow) {
	t := c.Theme()
	ui.Column(c).Fill().Background(t.Background).Children(func() {
		a.paneTabs(c, t, locales.Request(), locales.RequestPaneName(), &a.inspect.tab[0], requestTabLabels(), 1)
		switch a.inspect.tab[0] {
		case reqTabHeaders:
			a.headersSide(c, f, 1)
		case reqTabParams:
			a.paramsSide(c, f)
		case reqTabBody:
			a.bodySideView(c, f, 1)
		case reqTabRaw:
			a.rawSide(c, f, 1)
		case reqTabCookies:
			cookieView(c, f.reqHeader, false, &a.inspect.reveal[1])
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
		a.paneTabs(c, t, locales.Response(), locales.ResponsePaneName(), &a.inspect.tab[1], labels, 0)
		status := "No response"
		if f.status != 0 {
			status = strings.TrimSpace(fmt.Sprintf("%d %s", f.status, http.StatusText(f.status)))
		}
		if f.conn.error != "" {
			status = "Failed"
		}
		detail := humanBytes(f.bytes)
		if ct := f.contentType(true); ct != "" {
			detail += " · " + ct
		}
		uikit.ResponseHeadline(c, status, f.statusColor(t), fmt.Sprintf("%d ms", f.millis), detail, headlineTiming(t, f))
		switch a.inspect.tab[1] {
		case resTabHeaders:
			a.headersSide(c, f, 0)
		case resTabBody:
			a.bodySideView(c, f, 0)
		case resTabRaw:
			a.rawSide(c, f, 0)
		case resTabTiming:
			a.timingView(c, f)
		case resTabCookies:
			cookieView(c, f.respHeader, true, &a.inspect.reveal[0])
		case resTabMessages:
			a.messages(c, f)
		}
	})
}

// paneTabs names the side, its tabs and — while Body is chosen — the format
// the body is read in. The format is a menu rather than a row of segments so
// two visible panes never show the same control twice.
func (a *app) paneTabs(c *ui.Context, t *ui.Theme, side, pane string, selected *int, labels []string, bodySide int) {
	bodyTab := reqTabBody
	if bodySide == 0 {
		bodyTab = resTabBody
	}
	ui.Row(c).Height(uikit.HeightLarge).Padding(0, t.Space(2)).Gap(t.Space(1)).AlignItems(ui.Center).Children(func() {
		title := uikit.ToolbarButton(c, pane, locales.ReadThisSideInShared(), false).Height(uikit.HeightCompact).Padding(0, t.Space(1))
		if a.inspect.focus == bodySide {
			title.TextColor(t.Text)
		}
		if title.Clicked() {
			a.inspect.focus = bodySide
		}
		title.Children(func() { ui.Text(c, side).FontSize(uikit.TextBody).FontWeight(600) })
		uikit.TabStrip(c, func() {
			uikit.SubTabs(c, selected, pane, labels...)
		}).Grow(1).FillHeight()
		if *selected == bodyTab {
			a.bodyFormatMenu(c, t, bodySide)
			paneOptions := locales.ResponsePaneOptions()
			if bodySide == 1 {
				paneOptions = locales.RequestPaneOptions()
			}
			uikit.MoreMenu(c, paneOptions, func(m *ui.Menu) {
				actionMenu(m, []action{
					do("body.find", locales.FindInText(), func() {
						if reader := a.bodyText[bodySide]; reader != nil {
							reader.Run(editor.Find)
						}
					}),
					do("body.decode", locales.DecodeText(), func() {
						body := a.body
						if bodySide == 1 {
							body = a.reqBody
						}
						a.openDecoder(a.bodyText[bodySide], body)
					}),
					do("body.query", locales.QueryJson(), func() {
						if f, ok := a.current(); ok {
							a.openJSONQuery(capturedInspectionBody(f, bodySide))
						}
					}),
					do("body.copy", locales.CopyShownBody(), func() { a.copyPaneBody(c, bodySide) }),
				})
				if bodySide == 0 {
					f, ok := a.current()
					actionMenu(m, []action{do("body.save", locales.SaveBody(), func() {
						a.saveResponseBody(capture.Flow{Status: f.status, ResponseBody: f.resBody,
							ResponseBodyFile: f.resFile, ResponseBodySize: f.resSize, ResponseBodyError: f.resError})
					}).when(ok && f.status != 0, "No response was received")})
				}
			}).Padding(0, t.Space(1))
		}
	})
	ui.Divider(c)
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
	uikit.MenuSelect(c, shown, func(m *ui.Menu) {
		for i, name := range modes {
			if m.Item(name).Checked(i == mode).Chosen() {
				a.bodyMode[side] = i
				a.bodyQuery[side] = ""
			}
		}
	}).Label(bodyFormatLabel(side))
}

// copyPaneBody copies exactly the bytes the pane shows, without formatting
// or display warnings.
func (a *app) copyPaneBody(c *ui.Context, side int) {
	body := a.body
	if side == 1 {
		body = a.reqBody
	}
	if !utf8.ValidString(body) || strings.ContainsRune(body, 0) {
		a.warn(locales.BinaryBytesCannotBeCopied())
		return
	}
	c.Services().WriteClipboard(body)
	a.done(locales.CopiedShownBodyReviewCredentials())
}

func bodyFormatLabel(side int) string {
	if side == 1 {
		return locales.RequestBodyFormatLabel()
	}
	return locales.ResponseBodyFormatLabel()
}

// inspectorPaneBar names an exchange-wide view and gives the way back.
func (a *app) inspectorPaneBar(c *ui.Context, t *ui.Theme, title, note string) {
	ui.Row(c).Height(uikit.HeightLarge).Padding(0, t.Space(3)).Gap(t.Space(2)).AlignItems(ui.Center).Children(func() {
		if uikit.IconButton(c, locales.BackToRequestAndResponse(), locales.RequestAndResponse(), backIcon).Clicked() {
			a.inspect.pane = inspectorSplit
		}
		ui.Text(c, title).FontSize(uikit.TextBody).FontWeight(600).TextColor(t.Text)
		uikit.Meta(c, note).SingleLine().Tooltip(note)
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
		ui.Row(c).Padding(t.Space(2), t.Space(3)).Gap(t.Space(2)).AlignItems(ui.Center).Children(func() {
			uikit.Meta(c, line).Font(uikit.FontMono).SingleLine().Selectable().Grow(1).
				Tooltip(strings.TrimSpace(line + "\n" + note))
			if len(headers) > 0 {
				uikit.Meta(c, locales.HeadersCount(len(headers))).FontFeatures("tnum")
			}
			if hasSensitiveHeader(headers) {
				label := "Reveal"
				if a.inspect.reveal[side] {
					label = "Hide"
				}
				if uikit.ToolbarButton(c, locales.RevealCredentials(), locales.CredentialsAreMaskedSoA(), false).Height(uikit.HeightCompact).
					Children(func() { ui.Text(c, label).FontSize(uikit.TextMeta) }).Clicked() {
					a.inspect.reveal[side] = !a.inspect.reveal[side]
				}
			}
		})
		ui.Scroll(c).TrackScroll(&a.headerScroll[side]).Grow(1).FillWidth().Children(func() {
			if len(headers) == 0 {
				if raw == "" {
					uikit.Hint(c, locales.NoHeadersRecorded()).Padding(t.Space(3))
				} else {
					ui.Text(c, raw).FontSize(uikit.TextBody).Font(uikit.FontMono).TextColor(t.Text).Selectable().Padding(t.Space(3))
				}
			}
			for _, key := range slices.Sorted(maps.Keys(headers)) {
				value := strings.Join(headers[key], "\n")
				if sensitiveHeader(key) && !a.inspect.reveal[side] {
					value = "••••••••  masked"
				}
				uikit.KVRow(c, func() {
					uikit.Hint(c, key).Font(uikit.FontMono).Width(124).SingleLine().
						Tooltip(strings.TrimSpace(key + "\n" + headerDoc(key))).PopoverMenu(
						a.headerFilterMenu(c, key, headers[key]))

					ui.Text(c, value).FontSize(uikit.TextBody).Font(uikit.FontMono).TextColor(t.Text).Grow(1).Selectable().PopoverMenu(a.headerFilterMenu(c, key, headers[key]))
				})
				ui.Divider(c).Background(t.Border.Alpha(0.45))
			}
			if len(trailers) > 0 {
				uikit.Label(c, locales.Trailers()).Padding(t.Space(2), t.Space(3), t.Space(1)).FillWidth()
				for _, key := range slices.Sorted(maps.Keys(trailers)) {
					uikit.KVRow(c, func() {
						uikit.Hint(c, key).Font(uikit.FontMono).Width(124).SingleLine()
						ui.Text(c, strings.Join(trailers[key], "\n")).FontSize(uikit.TextBody).Font(uikit.FontMono).TextColor(t.Text).Grow(1).Selectable()
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
		uikit.Hint(c, locales.NoQueryParametersInThis()).Padding(t.Space(4))
		return
	}
	ui.Scroll(c).Grow(1).FillWidth().Children(func() {
		for _, key := range slices.Sorted(maps.Keys(parsed.Query())) {
			for _, value := range parsed.Query()[key] {
				uikit.KVRow(c, func() {
					uikit.Hint(c, key).Font(uikit.FontMono).Width(140).SingleLine()
					ui.Text(c, value).FontSize(uikit.TextBody).Font(uikit.FontMono).TextColor(t.Text).Grow(1).Selectable()
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
		a.rawEditor[side] = &editor.State{Buffer: buf, ReadOnly: true, NoScrollPastEnd: true, SoftWrap: true, Label: locales.RawMessage()}
	}
	ui.Row(c).Padding(t.Space(2), t.Space(3)).Gap(t.Space(2)).AlignItems(ui.Center).Children(func() {
		uikit.Meta(c, locales.AsSentAndReceivedCapture()).Grow(1)
		if uikit.SecondaryButton(c, locales.CopyRaw()).Height(uikit.HeightCompact).Disabled(a.rawText[side] == "").Clicked() {
			c.Services().WriteClipboard(a.rawText[side])
			a.done(locales.CopiedRawMessageReviewCredentials())
		}
	})
	a.rawEditor[side].Font = ui.Font{Family: uikit.FontMono, Size: uikit.TextBody}
	a.rawEditor[side].Theme = uikit.EditorTheme(t)
	editor.View(c, a.rawEditor[side]).Grow(1)
}

// diffPane compares the previous response for the same method and URL with
// the one captured now.
func (a *app) diffPane(c *ui.Context, f flow) {
	t := c.Theme()
	if !a.diffBaseline {
		uikit.Hint(c, locales.NoPreviousResponseForThis()).Padding(t.Space(4))
		return
	}
	ui.Row(c).Padding(t.Space(2), t.Space(3)).Children(func() {
		uikit.Meta(c, locales.PreviousResponseLeft()).Grow(1)
		uikit.Meta(c, locales.CurrentResponseRight()).Grow(1)
	})
	if a.diff != nil {
		editor.DiffView(c, a.diff).Grow(1).FillWidth()
		a.diffSplit = a.diff.SplitPercent
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

func (a *app) inspectorActions(c *ui.Context, f flow) []action {
	pane := func(id, label string, p int) action {
		return do(id, label, func() { a.inspect.pane = p }).checked(checkbox(a.inspect.pane == p))
	}
	actions := []action{
		pane("inspector.split", "Request & response", inspectorSplit),
		pane("inspector.diff", "Diff responses", inspectorDiff).when(a.diffBaseline, "Choose a baseline response first"),
		pane("inspector.connection", "Connection", inspectorConnection),
	}
	if f.kind == "ws" || f.kind == "sse" {
		actions = append(actions, pane("inspector.messages", "Messages", inspectorMessages))
	}
	actions = append(actions,
		do("flow.discard-edits", locales.DiscardEdits(), func() {
			delete(a.flowDrafts, f.id)
			a.liveFor = 0 // The capture opens unchanged again.
		}).when(a.flowDrafts[f.id] != nil, "This request has no edits").in("flow"),
		pick(a.actions(c), "flow.replay").as("Replay…").in("flow"),
		do("flow.save-evidence", locales.SaveEvidenceToSessions(), func() { a.saveEvidence(f) }).in("flow"),
		do("flow.connection-details", locales.ConnectionDetails(), func() { a.connOpen = !a.connOpen }).in("flow"),
		do("flow.history", locales.History(), func() { a.openDebugHistory(newDebugSource(f)) }).in("flow"),
		copyAsMenu(append(a.requestCopyActions(c, f, nil), a.captureCopyActions(c, []flow{f})...)).in("flow"),
	)
	for _, x := range a.pickActions(c, "view.focus-inspector", "view.layout-right", "view.layout-bottom") {
		actions = append(actions, x.in("view"))
	}
	return actions
}
