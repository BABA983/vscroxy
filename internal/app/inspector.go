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

// splitShare is where the request side ends when the two panes sit side by
// side: the design's 5:6, so the response gets the wider half.
const splitShare = 45

const (
	inspectorSplit = iota
	inspectorDiff
	inspectorConnection
	inspectorMessages
)

// A pane shows one view at a time. A request's views live with its document
// (composeTab*); a response's are here.
const (
	resViewBody = iota
	resViewHeaders
	resViewCookies
	resViewTiming
	resViewRaw
	resViewTests
	resViewConsole
	resViewMessages
)

// detail is the inspector under the traffic list: one compact summary, then
// the request and the response beside each other, each with its own tabs.
func (a *app) detail(c *ui.Context) {
	// The table may change the choice during this frame. Sync after it draws,
	// so all detail widgets see the same selected request immediately.
	a.syncDetail()
	if !a.inspect.ready {
		// A captured exchange is read by its response body first; the request
		// side opens its body when it has one, its headers otherwise.
		a.inspect.tab[1] = resViewBody
		if f, ok := a.current(); ok && (f.reqBody != "" || f.reqFile != "" || f.reqSize > 0) {
			a.inspect.tab[0] = composeTabBody
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
			// A note about what rules did, not a state: the accent is ink
			// now, so it would read as ordinary body text.
			uikit.Tip(c, ui.Text(c, locales.AppliedActions(strings.Join(f.actions, " · "))).FontSize(uikit.TextMeta).TextColor(t.TextMuted).
				Padding(4, 14).SingleLine(), strings.Join(f.actions, "\n"))
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
	// One row, as every other pane header: the tunnel's name, its views, and
	// what the exchange says on the right. Nothing here is the page's main
	// action, so nothing wears the primary button.
	ui.Row(c).Height(uikit.HeightTabs).Padding(0, t.Space(3)).Gap(t.Space(2)).AlignItems(ui.Center).Children(func() {
		uikit.Tip(c, ui.Text(c, cmp.Or(f.process, "Unidentified client")).FontSize(uikit.TextBody).FontWeight(500).SingleLine().Shrink(0), f.processPath)
		uikit.Tabs(c, &a.tab, "", locales.HttpsSetup(), locales.Connection())
		ui.Spacer(c)
		if f.conn.error != "" {
			uikit.Tip(c, ui.Text(c, cmp.Or(f.conn.errorStage, "Connection")+" failed").FontSize(uikit.TextMeta).TextColor(t.Danger).SingleLine().Shrink(0), f.conn.error)
		}
		uikit.Tip(c, uikit.Meta(c, f.host).Font(uikit.FontMono).SingleLine().MinWidth(0).Shrink(1), f.url)
		if uikit.Tip(c, uikit.SecondaryButton(c, locales.Explain()), locales.DiagnoseThisConnection()).Clicked() {
			a.openDiagnostics(&f, diagnoseMac)
		}
		a.actionButton(c, "view.focus-inspector", "Focus")
	})
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
		chip = uikit.Tip(c, chip, cmp.Or(f.conn.errorStage, "Connection")+": "+f.conn.error)
	} else if doc := statusDoc(f.status); doc != "" {
		chip = uikit.Tip(c, chip, doc)
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

// paneWidth is how wide one pane of the inspector is: the whole inspector
// while the two sides stack, its request share beside the response.
func (a *app) paneWidth(c *ui.Context) float32 {
	width := a.inspectorWidth(c)
	if a.sidesStacked(c) {
		return width
	}
	return width * splitShare / 100
}

// paneWidthAt is how wide a pane was laid out, from the frame before, or the
// share it expects before the first layout.
func (a *app) paneWidthAt(c *ui.Context, h *ui.Handle) float32 {
	if r := h.Bounds(c); r.W > 0 {
		return r.W
	}
	return a.paneWidth(c)
}

// inspectorWidth is the main pane's width beneath the list, its share beside
// the list, or the full window while the inspector is focused.
func (a *app) inspectorWidth(c *ui.Context) float32 {
	width := a.contentWidth(c)
	switch {
	case a.workbench.focused:
		width, _ = c.Size()
	case a.page.capture() && a.workbench.sideBySide:
		// The share belongs to the traffic list, which only Capture shows
		// beside the inspector.
		share := a.workbench.sidePercent
		if share == 0 {
			share = 64
		}
		width = width * (100 - share) / 100
	}
	return width
}

// sidesStacked reports whether the request goes above the response. Beside
// the list the inspector is a narrow column, and two panes side by side would
// be too narrow to read a URL in, so they stack; below the list or filling
// the window it is wide enough for the design's 5:6 split.
func (a *app) sidesStacked(c *ui.Context) bool {
	return a.inspectorWidth(c) < 760
}

// splitInspector puts the request beside the response, or above it in a
// column beside the list.
func (a *app) splitInspector(c *ui.Context, f flow) {
	if a.inspect.percent == 0 {
		a.inspect.percent = splitShare
	}
	request := func() { a.requestPane(c, f) }
	response := func() { a.responsePane(c, f) }
	if a.sidesStacked(c) {
		ui.SplitVerticalPercent(c.Key("inspector-sides"), &a.inspect.percent, request, response).SplitReset(splitShare).Grow(1)
		return
	}
	ui.SplitPercent(c.Key("inspector-sides"), &a.inspect.percent, request, response).SplitReset(splitShare).Grow(1)
}

// requestPane is the request of a capture the editor cannot take: its parts
// as they were recorded, in the same row the editor uses.
func (a *app) requestPane(c *ui.Context, f flow) {
	t := c.Theme()
	width := a.paneWidthAt(c, &a.inspectorReqPane)
	ui.Column(c).Fill().Background(t.Background).Bind(&a.inspectorReqPane).Children(func() {
		tools := paneTools{format: -1, width: width}
		if a.inspect.tab[0] == composeTabBody {
			tools.format = 1
		}
		a.requestPaneHeader(c, t, &a.inspect.tab[0], nil, f, tools)
		switch a.inspect.tab[0] {
		case composeTabParams:
			a.paramsSide(c, f)
		case composeTabBody:
			a.bodySideView(c, f, 1)
		case composeTabRaw:
			a.rawSide(c, f, 1)
		case composeTabCookies:
			cookieView(c, f.reqHeader, false, &a.inspect.reveal[1])
		default:
			a.headersSide(c, f, 1)
		}
	})
}

func (a *app) responsePane(c *ui.Context, f flow) {
	t := c.Theme()
	width := a.paneWidthAt(c, &a.inspectorRespPane)
	ui.Column(c).Fill().Background(t.Background).Bind(&a.inspectorRespPane).Children(func() {
		more := a.responseViews(&a.inspect.tab[1], true, "", false)
		tools := paneTools{format: -1, width: width}
		if a.inspect.tab[1] == resViewBody {
			more = append(more, a.bodyPaneActions(c, 0)...)
			tools.format = 0
		}
		a.responsePaneHeader(c, t, &a.inspect.tab[1], responseSummaryOf(t, f), more, tools)
		switch a.inspect.tab[1] {
		case resViewBody:
			a.bodySideView(c, f, 0)
		case resViewRaw:
			a.rawSide(c, f, 0)
		case resViewTiming:
			a.timingView(c, f)
		case resViewCookies:
			cookieView(c, f.respHeader, true, &a.inspect.reveal[0])
		case resViewMessages:
			a.messages(c, f)
		default:
			a.headersSide(c, f, 0)
		}
	})
}

// paneView is one view a pane can show: its constant, the label the row or
// the More menu prints, and a quiet count after it.
type paneView struct {
	view  int
	label string
	count string
}

// paneTools are what a pane's row offers past its tabs: the controls the
// caller draws at the row's end, the body side whose format menu the row
// holds, and what the row leaves out when it runs out of room. The tabs and
// the status always keep their room; everything else yields, numbers first.
type paneTools struct {
	// width is how wide the pane was laid out, from the last frame. Zero
	// falls back to the pane's share of the window.
	width float32
	// draw paints the caller's own controls in the room the row has left
	// for them. What it does not draw it adds through overflow.
	draw func(room *paneRoom)
	// format is the body side whose format menu the row holds.
	format int
	// overflow adds to the pane's menu what the row left out.
	overflow func([]action) []action
}

// paneRoom is the width a row has left for what it reports past its tabs.
// Pieces take it in the order of the row, and one that does not fit goes to
// the pane's menu instead of pushing a tab off the row.
type paneRoom struct{ left float32 }

// fits takes width, and the gap after it, when the room has them.
func (r *paneRoom) fits(width float32) bool {
	if r.left < width {
		return false
	}
	r.left -= width + 8
	return true
}

// paneTextWidth measures text as a pane's row prints it.
func paneTextWidth(c *ui.Context, text string, size float32, weight int) float32 {
	w, _ := c.MeasureText(0, ui.Span{Text: text, Size: size, Weight: weight})
	return w
}

// paneTitleWidth is the room a pane's title takes: its status dot when it has
// one, the gap after it and the name.
func paneTitleWidth(c *ui.Context, title string, dot bool) float32 {
	if !dot {
		return paneTextWidth(c, title, uikit.TextBody, 500)
	}
	return 6 + c.Theme().Space(1.5) + paneTextWidth(c, title, uikit.TextBody, 500)
}

// paneTabsWidth is the room the tabs take, as uikit lays them out: each chip's
// padding, its label, its count and the gaps between them.
func paneTabsWidth(c *ui.Context, labels, counts []string) float32 {
	t := c.Theme()
	var w float32
	for i, label := range labels {
		if i > 0 {
			w += t.Space(0.5)
		}
		w += 2*t.Space(1.5) + paneTextWidth(c, label, uikit.TextBody, 400)
		if i < len(counts) && counts[i] != "" && counts[i] != "0" {
			w += t.Space(1) + paneTextWidth(c, counts[i], uikit.TextMeta, 400)
		}
	}
	return w
}

// paneRoomFor is what a row has left once its title, its tabs and its menu
// have theirs.
func paneRoomFor(c *ui.Context, width float32, title string, dot bool, labels, counts []string, menu float32) paneRoom {
	t := c.Theme()
	return paneRoom{left: width - 2*t.Space(3) - paneTitleWidth(c, title, dot) - paneTabsWidth(c, labels, counts) - 2*t.Space(2) - menu}
}

// paneMoreWidth is the room the request pane's More tab takes: it is a word
// with a chevron, where the response pane's menu is a single glyph.
func paneMoreWidth(c *ui.Context) float32 {
	t := c.Theme()
	return paneTextWidth(c, locales.More(), uikit.TextBody, 400) + 2*t.Space(1.5) + t.Space(1) + 10
}

// paneMenuWidth is the room the pane's own ⋯ takes at the row's end.
const paneMenuWidth = 45

// paneMonoWidth measures the numbers a row prints in the data font.
func paneMonoWidth(c *ui.Context, text string) float32 {
	w, _ := c.MeasureText(0, ui.Span{Text: text, Size: uikit.TextMeta, Font: uikit.FontMono})
	return w
}

// paneMetaWidth is the room a response's duration and size take.
func paneMetaWidth(c *ui.Context, sum responseSummary) float32 {
	w := paneMonoWidth(c, sum.duration)
	if sum.detail != "" {
		w += paneMonoWidth(c, " · "+sum.detail)
	}
	return w
}

// paneFormatWidth is the room a body's format menu takes: its label, the
// control's padding and its chevron.
func (a *app) paneFormatWidth(c *ui.Context, side int) float32 {
	label, ok := a.bodyFormatShown(side)
	if !ok {
		return 0
	}
	return paneTextWidth(c, label, uikit.TextBody, 400) + 2*c.Theme().Space(2) + 14
}

// requestPaneTabs are the views the request pane's row starts with.
func requestPaneTabs(d *requestDocument, f flow) []paneView {
	views := []paneView{
		{composeTabHeaders, locales.RequestHeadersLabel(), requestHeaderCount(d, f)},
		{composeTabParams, locales.Params(), requestParamCount(d, f)},
		{composeTabBody, locales.Body(), ""},
	}
	if d != nil {
		views = append(views, paneView{composeTabAuth, locales.Auth(), ""})
	}
	return views
}

// requestTabsFit reports whether a request pane of this width shows every tab
// as a whole word, with no room saved for the history column beside it. The
// width is measured, not taken from a window size: a pane that fits at 1200
// with a short header count and not at 1300 with a long one is decided the
// same way.
func (a *app) requestTabsFit(c *ui.Context, d *requestDocument, width float32) bool {
	t := c.Theme()
	if !a.sidesStacked(c) {
		// The share the divider is at now: a response pulls it to the
		// response's favour, and the tabs have to fit where it leaves them.
		share := float32(draftPercent)
		if d != nil && d.requestPercent > 0 {
			share = d.requestPercent
		}
		width = width * share / 100
	}
	views := requestPaneTabs(d, flow{})
	labels, counts := make([]string, len(views)), make([]string, len(views))
	for i, v := range views {
		labels[i], counts[i] = v.label, v.count
	}
	need := 2*t.Space(3) + paneTitleWidth(c, locales.Request(), false) + paneTabsWidth(c, labels, counts) +
		2*t.Space(2) + paneMoreWidth(c)
	return width >= need
}

// requestPaneHeader draws the request pane's one row and settles the view it
// shows. The first tabs are always in the row; the rest open from More, and
// the one chosen there takes the last place in the row so the pane says what
// it is showing without a second row.
func (a *app) requestPaneHeader(c *ui.Context, t *ui.Theme, selected *int, d *requestDocument, f flow, tools paneTools) {
	views := []paneView{
		{composeTabHeaders, locales.RequestHeadersLabel(), requestHeaderCount(d, f)},
		{composeTabParams, locales.Params(), requestParamCount(d, f)},
		{composeTabBody, locales.Body(), ""},
	}
	if d != nil {
		views = append(views, paneView{composeTabAuth, locales.Auth(), ""})
	}
	more := a.requestMoreViews(d, f)
	if i := slices.IndexFunc(more, func(v paneView) bool { return v.view == *selected }); i >= 0 {
		views = append(views, more[i])
	}
	before := *selected
	labels, counts := make([]string, len(views)), make([]string, len(views))
	for i, v := range views {
		labels[i], counts[i] = v.label, v.count
	}
	var extra []action
	if before == composeTabBody {
		extra = a.bodyPaneActions(c, 1)
	}
	width := tools.width
	if width <= 0 {
		width = a.paneWidth(c)
	}
	// A tab is always a whole word: a pane too narrow for all of them moves
	// the last ones into More, from the right, and never scrolls or fades
	// the row. The view on screen stays where the reader put it.
	menu := paneMoreWidth(c)
	room := paneRoomFor(c, width, locales.Request(), false, labels, counts, menu)
	for len(views) > 1 && room.left < 0 {
		at := -1
		for i := len(views) - 1; i >= 0; i-- {
			if views[i].view != before {
				at = i
				break
			}
		}
		if at < 0 {
			break
		}
		dropped := views[at]
		views = slices.Delete(views, at, at+1)
		labels = slices.Delete(labels, at, at+1)
		counts = slices.Delete(counts, at, at+1)
		more = append([]paneView{dropped}, more...)
		room = paneRoomFor(c, width, locales.Request(), false, labels, counts, menu)
	}
	shown := max(0, slices.IndexFunc(views, func(v paneView) bool { return v.view == before }))
	showFormat := tools.format >= 0 && room.fits(a.paneFormatWidth(c, tools.format))
	if !showFormat && tools.format >= 0 {
		extra = append([]action{submenu(bodyFormatLabel(tools.format), a.bodyFormatActions(tools.format)...)}, extra...)
	}
	uikit.PaneHeader(c, func() {
		uikit.PaneTitle(c, locales.Request(), locales.RequestPaneName(), ui.Color{}, t.Text, func() { a.inspect.focus = 1 })
		uikit.TabStrip(c, func() {
			uikit.CountedTabs(c, &shown, locales.RequestPaneName(), labels, counts)
		}).Grow(1).FillHeight()
	}, func() {
		a.requestMoreMenu(c, selected, more, d, extra)
		if tools.draw != nil {
			tools.draw(&room)
		}
		if showFormat {
			a.bodyFormatMenu(c, t, tools.format)
		}
	})
	settleView(selected, views, shown, before)
}

// settleView maps the tab strip's index back to a view. A view chosen from
// More during the same build wins: the menu moved the selection, so the row's
// old index says nothing about it.
func settleView(selected *int, views []paneView, shown, before int) {
	switch {
	case *selected != before:
	case shown >= 0 && shown < len(views):
		*selected = views[shown].view
	}
}

// requestMoreViews are the request views the row keeps out: the parts of a
// document read or edited one at a time, and the capture a draft came from.
func (a *app) requestMoreViews(d *requestDocument, f flow) []paneView {
	if d == nil {
		return []paneView{{composeTabRaw, locales.Raw(), ""}, {composeTabCookies, locales.Cookies2(), ""}}
	}
	examples := ""
	if n := len(d.examples); n > 0 {
		examples = strconv.Itoa(n)
	}
	more := []paneView{
		{composeTabScripts, locales.Scripts(), ""},
		{composeTabChecks, locales.Checks(), checkCount(d)},
		{composeTabExamples, locales.Examples(), examples},
		{composeTabDocs, locales.Docs(), ""},
		{composeTabSettings, locales.Settings(), ""},
	}
	// The capture is only somewhere else to go once the draft differs from
	// it: until then the tabs are the capture.
	more = append(more, paneView{composeTabCookies, locales.Cookies2(), ""})
	if a.edited(f.id) {
		more = append(more, paneView{composeTabOriginal, locales.OriginalTab(), ""})
	}
	return more
}

// requestHeaderCount is how many headers the request sends, as its tab says.
func requestHeaderCount(d *requestDocument, f flow) string {
	if d == nil {
		if len(f.reqHeader) == 0 {
			return ""
		}
		return fmt.Sprint(len(f.reqHeader))
	}
	headers := d.headerRows
	if d.headerBulk || headers == nil {
		headers = parseHeaderRows(d.composeHeaders)
	}
	n := 0
	for _, row := range headers {
		if !row.Disabled && row.Name != "" {
			n++
		}
	}
	if n == 0 {
		return ""
	}
	return fmt.Sprint(n)
}

// requestParamCount is how many query values the URL carries.
func requestParamCount(d *requestDocument, f flow) string {
	raw := f.url
	if d != nil {
		raw = d.composeURL
	}
	u, err := url.Parse(raw)
	if err != nil {
		return ""
	}
	n := 0
	for _, values := range u.Query() {
		n += len(values)
	}
	if n == 0 {
		return ""
	}
	return fmt.Sprint(n)
}

// checkCount is how many checks the request's scripts assert.
func checkCount(d *requestDocument) string {
	n := 0
	if strings.TrimSpace(d.checks.Status) != "" {
		n++
	}
	for _, block := range []string{d.checks.Headers, d.checks.JSON} {
		for line := range strings.SplitSeq(block, "\n") {
			if strings.TrimSpace(line) != "" {
				n++
			}
		}
	}
	if n == 0 {
		return ""
	}
	return fmt.Sprint(n)
}

func (a *app) requestMoreMenu(c *ui.Context, selected *int, more []paneView, d *requestDocument, extra []action) {
	uikit.TabMenu(c, locales.More(), locales.RequestPaneOptions(), func(m *ui.Menu) {
		var items []action
		for _, v := range more {
			label := v.label
			if v.count != "" {
				label += " " + v.count
			}
			items = append(items, do("request.view", label, func() { *selected = v.view }).
				checked(checkbox(*selected == v.view)).in("views"))
		}
		if d != nil {
			items = append(items, do("request.bulk-edit", locales.BulkEdit(), func() {
				if d.headerBulk || !hasSecretHeader(d.headerRows) {
					toggleHeaderMode(d)
					return
				}
				d.headerWarn = true
			}).checked(checkbox(d.headerBulk)).in("edit"))
		}
		actionMenu(m, append(items, extra...))
	})
}

// responseSummary is what a response pane's header says about what it shows.
type responseSummary struct {
	title, duration, detail string
	dot, ink                ui.Color // the status light, and the title's ink when it carries a signal
	timing                  uikit.Timing
	stream                  bool // the messages are a view of their own
}

// responseSummaryOf reads a capture's result. A normal response is quiet: its
// title stays in the body color and only the light carries the status.
func responseSummaryOf(t *ui.Theme, f flow) responseSummary {
	p := uikit.Colors(t)
	title := "No response"
	if f.status != 0 {
		title = strings.TrimSpace(fmt.Sprintf("%d %s", f.status, http.StatusText(f.status)))
	}
	if f.conn.error != "" {
		title = "Failed"
	}
	sum := responseSummary{title: title, dot: p.TextMuted, duration: fmt.Sprintf("%d ms", f.millis), detail: humanBytes(f.bytes),
		timing: headlineTiming(t, f), stream: f.kind == "ws" || f.kind == "sse"}
	if f.conn.error != "" || f.status >= 300 {
		sum.dot, sum.ink = f.statusColor(t), f.statusColor(t)
	}
	return sum
}

// sentSummaryOf reads what the last send got back.
func sentSummaryOf(t *ui.Theme, r *capture.Flow, outcome string) responseSummary {
	p := uikit.Colors(t)
	sum := responseSummary{title: "No response", dot: p.TextMuted, duration: fmt.Sprintf("%d ms", r.Millis), detail: humanBytes(r.Bytes)}
	if r.Status != 0 {
		sum.title = strings.TrimSpace(fmt.Sprintf("%d %s", r.Status, http.StatusText(r.Status)))
	}
	switch {
	case outcome == "Network error":
		sum.title = fmt.Sprintf("%d · proxy response", r.Status)
		sum.dot, sum.ink = t.Danger, t.Danger
	case r.Status >= 300:
		sum.dot, sum.ink = uikit.StatusColor(r.Status, t), uikit.StatusColor(r.Status, t)
	}
	sum.timing = headlineTiming(t, flow{wait: r.Wait, download: r.Download, conn: conn{
		resolved: r.Conn.Resolved, lookupMs: r.Conn.LookupMs, dnsFresh: r.Conn.DNSFresh, flows: r.Conn.Flows, tls: r.Conn.TLS,
	}})
	return sum
}

// responsePaneHeader draws the response pane's one row: what came back, the
// views of it, and how long it took and how much it carried.
func (a *app) responsePaneHeader(c *ui.Context, t *ui.Theme, selected *int, sum responseSummary, more []action, tools paneTools) {
	views := []paneView{
		{resViewBody, locales.Body(), ""},
		{resViewHeaders, locales.ResponseHeadersLabel(), ""},
		{resViewCookies, locales.Cookies2(), ""},
		{resViewTiming, locales.TimingTab(), ""},
	}
	if sum.stream {
		views = append(views, paneView{resViewMessages, locales.MessagesTab(), ""})
	}
	extras := []paneView{
		{resViewRaw, locales.Raw(), ""},
		{resViewTests, locales.Checks(), ""},
		{resViewConsole, locales.Console(), ""},
	}
	if i := slices.IndexFunc(extras, func(v paneView) bool { return v.view == *selected }); i >= 0 {
		views = append(views, extras[i])
	}
	before := *selected
	shown := max(0, slices.IndexFunc(views, func(v paneView) bool { return v.view == before }))
	labels := make([]string, len(views))
	for i, v := range views {
		labels[i] = v.label
	}
	// The tabs and the status keep the row; what else it reports is measured
	// against the room that is left, in the order the row gives it up: the
	// numbers first, then the caller's own controls (which give up Decode
	// before the format menu and the actions), then the body's format menu.
	// Whatever does not fit goes to the pane's menu.
	width := tools.width
	if width <= 0 {
		width = a.paneWidth(c)
	}
	room := paneRoomFor(c, width, sum.title, true, labels, nil, paneMenuWidth)
	showNumbers := sum.duration != "" && room.fits(paneMetaWidth(c, sum))
	// The caller's controls take their room as the row builds them; the
	// format menu is the inspector's own, and the editor never has both.
	afterNumbers := room
	showFormat := tools.format >= 0 && afterNumbers.fits(a.paneFormatWidth(c, tools.format))
	uikit.PaneHeader(c, func() {
		title := uikit.PaneTitle(c, sum.title, locales.ResponsePaneName(), sum.dot, sum.ink, func() { a.inspect.focus = 0 })
		if !showNumbers && sum.duration != "" {
			// What the row has no room for, the status says on hover.
			uikit.Tip(c, title, sum.title+" · "+strings.TrimSpace(sum.duration+" · "+sum.detail))
		}
		uikit.TabStrip(c, func() {
			uikit.CountedTabs(c, &shown, locales.ResponsePaneName(), labels, nil)
		}).Grow(1).FillHeight()
	}, func() {
		if showNumbers {
			uikit.ResponseMeta(c, sum.duration, sum.detail, sum.timing)
		}
		if tools.draw != nil {
			tools.draw(&room)
		}
		if showFormat {
			a.bodyFormatMenu(c, t, tools.format)
		}
		items := more
		var head []action
		if !showNumbers && sum.duration != "" {
			head = append(head, note(strings.TrimSpace(sum.duration+" · "+sum.detail)))
		}
		if tools.format >= 0 && !showFormat {
			head = append(head, submenu(bodyFormatLabel(tools.format), a.bodyFormatActions(tools.format)...))
		}
		if tools.overflow != nil {
			head = tools.overflow(head)
		}
		items = append(head, items...)
		if len(items) > 0 {
			uikit.MoreMenu(c, locales.ResponsePaneOptions(), func(m *ui.Menu) { actionMenu(m, items) })
		}
	})
	settleView(selected, views, shown, before)
}

// checksLabel is how many of a send's checks passed, as the More menu says it.
func checksLabel(d *requestDocument) string {
	if len(d.composeTests) == 0 {
		return ""
	}
	passed := 0
	for _, check := range d.composeTests {
		if check.Pass {
			passed++
		}
	}
	return fmt.Sprintf("%d/%d", passed, len(d.composeTests))
}

// responseViews are the response views the row keeps out, as menu actions.
func (a *app) responseViews(selected *int, raw bool, checks string, console bool) []action {
	var items []action
	if raw {
		items = append(items, do("response.view", locales.Raw(), func() { *selected = resViewRaw }).
			checked(checkbox(*selected == resViewRaw)).in("views"))
	}
	if checks != "" {
		items = append(items, do("response.view", locales.Checks(), func() { *selected = resViewTests }).
			checked(checkbox(*selected == resViewTests)).in("views"))
	}
	if console {
		items = append(items, do("response.view", locales.Console(), func() { *selected = resViewConsole }).
			checked(checkbox(*selected == resViewConsole)).in("views"))
	}
	return items
}

// bodyPaneActions are what a pane offers about the body it is showing: find,
// decode, query, copy, and — for a response — save. They read the bytes the
// pane shows, so they live in its header.
func (a *app) bodyPaneActions(c *ui.Context, side int) []action {
	items := []action{
		do("body.find", locales.FindInText(), func() {
			if reader := a.bodyText[side]; reader != nil {
				reader.Run(editor.Find)
			}
		}),
		do("body.decode", locales.DecodeText(), func() {
			body := a.body
			if side == 1 {
				body = a.reqBody
			}
			a.openDecoder(a.bodyText[side], body)
		}),
		do("body.query", locales.QueryJson(), func() {
			if f, ok := a.current(); ok {
				a.openJSONQuery(capturedInspectionBody(f, side))
			}
		}),
		do("body.copy", locales.CopyShownBody(), func() { a.copyPaneBody(c, side) }),
	}
	if side == 0 {
		f, ok := a.current()
		items = append(items, do("body.save", locales.SaveBody(), func() {
			a.saveResponseBody(capture.Flow{Status: f.status, ResponseBody: f.resBody,
				ResponseBodyFile: f.resFile, ResponseBodySize: f.resSize, ResponseBodyError: f.resError})
		}).when(ok && f.status != 0, "No response was received"))
	}
	return items
}

// bodyFormatShown is what the format menu names for one side's body: the
// resolved mode while the choice is Auto.
func (a *app) bodyFormatShown(side int) (string, bool) {
	f, ok := a.current()
	if !ok {
		return "", false
	}
	modes := a.bodyModes(f, side)
	mode := a.bodyMode[side]
	if mode >= len(modes) {
		mode = 0
	}
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
	return shown, true
}

// bodyFormatMenu shows and chooses the representation of one side's body.
func (a *app) bodyFormatMenu(c *ui.Context, t *ui.Theme, side int) {
	shown, ok := a.bodyFormatShown(side)
	if !ok {
		return
	}
	uikit.MenuSelect(c, shown, func(m *ui.Menu) { actionMenu(m, a.bodyFormatActions(side)) }).
		Label(bodyFormatLabel(side))
}

// bodyFormatActions are the representations one side's body can take, with the
// chosen one checked, for a menu the pane's row already has: a narrow pane
// moves the format menu there rather than lose a tab to it.
func (a *app) bodyFormatActions(side int) []action {
	f, ok := a.current()
	if !ok {
		return nil
	}
	modes := a.bodyModes(f, side)
	mode := a.bodyMode[side]
	if mode >= len(modes) {
		mode = 0
	}
	items := make([]action, 0, len(modes))
	for i, name := range modes {
		items = append(items, do("body.format", name, func() {
			a.bodyMode[side] = i
			a.bodyQuery[side] = ""
		}).checked(checkbox(i == mode)))
	}
	return items
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
		ui.Text(c, title).FontSize(uikit.TextBody).FontWeight(500).TextColor(t.Text)
		uikit.Tip(c, uikit.Meta(c, note).SingleLine(), note)
		ui.Spacer(c)
	})
}

// capturedHeaders are the headers the inspected capture arrived with: what a
// header row's context menu can filter the traffic by.
func (a *app) capturedHeaders() map[string][]string {
	if f, ok := a.current(); ok {
		return f.reqHeader
	}
	return nil
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
			// The response pane names its status already; the request's start
			// line is the one thing the row adds.
			if side == 1 {
				uikit.Mono(c, line).TextColor(t.TextMuted).SingleLine().Selectable().Grow(1).
					Tooltip(strings.TrimSpace(line + "\n" + note))
			}
			if len(headers) > 0 {
				count := locales.RequestHeadersCount(len(headers))
				if side == 0 {
					count = locales.ResponseHeadersCount(len(headers))
				}
				uikit.Meta(c, count).FontFeatures("tnum")
			}
			if hasSensitiveHeader(headers) {
				label := "Reveal"
				if a.inspect.reveal[side] {
					label = "Hide"
				}
				if uikit.ToolbarButton(c, locales.RevealCredentials(), locales.CredentialsAreMaskedSoA(), false).Height(uikit.HeightControl).
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
		if uikit.SecondaryButton(c, locales.CopyRaw()).Height(uikit.HeightControl).Disabled(a.rawText[side] == "").Clicked() {
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
