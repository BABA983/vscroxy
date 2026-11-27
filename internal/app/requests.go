package app

import (
	"cmp"
	"context"
	"encoding/json/jsontext"
	"fmt"
	"maps"
	"net/url"
	"reflect"
	"slices"
	"strings"
	"sync"
	"time"
	"uuid"

	"github.com/baba983/vscroxy/internal/capture"
	"github.com/baba983/vscroxy/internal/graphql"
	"github.com/baba983/vscroxy/internal/inspect"
	"github.com/baba983/vscroxy/internal/jsonc"
	"github.com/baba983/vscroxy/internal/request"
	"github.com/baba983/vscroxy/internal/uikit"
	"github.com/baba983/vscroxy/locales"
	"github.com/egoist/mygo/ui"
	"github.com/egoist/mygo/ui/editor"
)

// requestDocument owns both the editable draft and the last send. Neither is
// borrowed from the selected capture row or another request tab.
type requestDocument struct {
	id       int
	debugKey string
	// The History / Examples column of a saved request: which list shows,
	// the retained run shown read-only in place of the response (0 for the
	// document's own response), and its reader.
	historyTab        int
	historyHidden     bool // the reader put the runs column away
	historyPick       int
	historyReader     *editor.State
	historyStatus     string
	historySource     string
	origin            *debugSource
	assetID           string
	savedKey          string
	baseline          savedRequest
	sourceFlow        int
	experimentID      int
	source            *flow
	comparison        captureAnalysis
	sourceAPI         string
	composeName       string
	composeCollection string
	composeMethod     string
	composeURL        string
	// methodCustom shows the method as its own short field, after Custom… in
	// its menu; methodFocus hands it the focus once, on the frame it appears.
	methodCustom   bool
	methodFocus    bool
	protocol       string
	wsMessages     []request.WebSocketMessage
	wsWait         float64 // milliseconds between a message and its replies
	grpcConfig     request.GRPCConfig
	grpcUI         *grpcDocument
	graphqlUI      *graphqlDocument
	socketIOConfig request.SocketIOConfig
	socketIOUI     *socketIODocument
	mqttConfig     request.MQTTConfig
	mqttUI         *mqttDocument
	mcpConfig      request.MCPConfig
	mcpUI          *mcpDocument
	composeHeaders string
	composeBody    string
	// jsonCrumbs holds the authored body's breadcrumb path, recalculated
	// only when its caret or editor version changes.
	jsonCrumbs jsonBreadcrumbs
	// problemBodyGen changes when code replaces composeBody outside the body
	// editor, so the Problems panel can skip an unchanged scan of a large body.
	problemBodyGen uint64
	// headerRows is the header table. composeHeaders stays the Key: Value
	// block for scripts, cURL and the bulk editor, written from the enabled
	// rows; headerFor is the block the rows were last built from.
	headerRows []request.Field
	headerFor  string
	headerBulk bool
	// headerWarn is the confirmation shown before the block displays
	// credentials in plain text.
	headerWarn bool
	// headerOff are the switched-off rows while the bulk editor shows the
	// block, which has no way to say a row is disabled.
	headerOff   []request.Field
	bulkEditor  *editor.State
	bulkFor     string
	auth        request.Auth
	bodyMode    string
	bodyFile    string
	graphql     request.GraphQLBody
	graphqlTab  int
	queryEditor *editor.State
	queryFor    string
	varsEditor  *editor.State
	varsFor     string
	formFields  []request.Field
	queryRows   []request.Field
	querySource string
	// fieldTablePane is the row table's last laid-out width. The Description
	// column hides from this measured width, so moving the request/response
	// divider changes it without waiting for the window to resize.
	fieldTablePane ui.Handle
	// fieldTableWidth remembers that width while no table is laid out, as on
	// the Binary body mode: the first frame after switching back uses the
	// same budget instead of the window's width, so a column cannot appear
	// for one frame and then stay until the pointer moves.
	fieldTableWidth float32
	// pathRows are the values of the URL's :name segments, in URL order.
	pathRows    []request.Field
	queryError  string
	composePre  string
	composePost string
	checks      request.Checks
	// description is the request's Markdown documentation; settings are its
	// transport choices. timeout and maxRedirects are the settings tab's
	// inputs, seeded from the effective values.
	description  string
	settings     request.Settings
	timeout      float64
	maxRedirects float64
	tokenSources []request.TokenSource
	// examples are the draft's kept responses; example is the one shown,
	// exampleView its Body, Headers or Request view.
	examples      []request.Example
	example       int
	exampleView   int
	exampleEditor *editor.State
	// exampleShown is the text exampleEditor was built from, so an edit is
	// the difference from it, not the display's formatting.
	exampleShown string
	// exampleStatusText and exampleDelayText are the Examples tab's status
	// and mock delay fields as typed.
	exampleStatusText string
	exampleDelayText  string
	composeTab        int
	scriptTab         int
	composeResult     string
	composeWaiting    bool
	sendCancel        context.CancelFunc
	sendCancelling    bool
	sendOutcome       string
	sendDetails       bool
	sentRun           string
	variablesOpen     bool
	variablesReveal   bool
	// bodyEditor is the raw request body's editor. bodyFor is the text it
	// last held, so an import or a restored draft resets it while typing
	// never does. The scripts use the same reader, each with its own state.
	bodyEditor *editor.State
	bodyFor    string
	// bodyRead is how the Body view reads the draft's bytes: 0 edits them,
	// the rest are the read-only representations draftBodyFormats lists.
	bodyRead         int
	preEditor        *editor.State
	preFor           string
	postEditor       *editor.State
	postFor          string
	composeTests     []request.TestResult
	composeReplies   []string
	composeExchanges []request.WebSocketExchange
	console          []request.ConsoleMessage
	response         *capture.Flow
	// responseTab is the response tab the reader chose. Comparing with the
	// source capture is not a tab: it replaces the whole response area and
	// returns to the tab kept here.
	responseTab    int
	comparing      bool
	responseView   int
	responseEditor *editor.State
	responseList   ui.ListState
	cookieReveal   bool
	structured     structuredInspector
	// sent is exactly what the last send carried, so the screen can say
	// whether the draft on it is the one that produced the response.
	sent            savedRequest
	sentAt          time.Time
	sentSet         bool
	lastAttemptOpen bool
	// split pins the debugging screen's two panes side by side or stacked.
	// Nil follows the width, as it always has; the title bar's layout button
	// sets it for this request tab.
	split          *bool
	requestPercent float32
	// splitSeen is the share the editor last laid out, so a divider the reader
	// moved is never moved back by the app.
	splitSeen float32
	// splitTouched is set once the reader drags the divider.
	splitTouched bool
	urlInput     ui.Handle
	// unicodeFocus holds the handles of fields a pre-send character warning
	// can open. They are created on demand and bound on each frame.
	unicodeFocus map[string]*ui.Handle
}

// requestResult is handed back to the UI thread; workers never mutate tabs,
// editor buffers or environments while a frame is reading them.
type requestResult struct {
	document    *requestDocument
	summary     string
	outcome     string
	err         error
	response    *capture.Flow
	tests       []request.TestResult
	replies     []string
	exchanges   []request.WebSocketExchange
	grpc        *grpcSession
	console     []request.ConsoleMessage
	environment string
	varsBefore  map[string]string
	vars        map[string]string
	writes      []request.Write
	experiment  *experimentRecord
}

func (d *requestDocument) value() savedRequest {
	r := savedRequest{
		ID: d.assetID, Source: d.origin,
		Name: d.composeName, Collection: d.composeCollection,
		Method: d.composeMethod, URL: d.composeURL, PathVariables: pathRowsFor(d.composeURL, d.pathRows),
		QueryDescriptions: queryDescriptionsFor(d.composeURL, d.queryRows), Headers: d.composeHeaders,
		Protocol: d.protocol, GRPC: d.grpcConfig, WebSocket: d.webSocketConfig(), SocketIO: d.socketIOConfig, MQTT: d.mqttConfig, MCP: d.mcpConfig,
		Body: d.composeBody, Pre: d.composePre, Post: d.composePost,
		Auth: d.auth, BodyMode: d.bodyMode, BodyFile: d.bodyFile, Form: slices.Clone(d.formFields),
		GraphQL: d.graphql,
		Checks:  d.checks, Settings: d.settings.Clone(), HeaderList: headerRowsKept(d.headerRows),
		Description: d.description,
		Examples:    slices.Clone(d.examples), TokenSources: slices.Clone(d.tokenSources),
	}
	if len(d.headerRows) > 0 {
		r.Headers = headerRowsText(d.headerRows)
	}
	if r.Auth.Type == "None" {
		r.Auth = request.Auth{}
	}
	if r.BodyMode == "Raw" {
		r.BodyMode = ""
	}
	return r
}

// draft is what a send carries: the request without its examples, which
// stay with the request rather than with every attempt.
func (d *requestDocument) draft() savedRequest {
	r := d.value()
	r.Examples = nil
	return r
}

func (d *requestDocument) dirty() bool { return !reflect.DeepEqual(d.value(), d.baseline) }

func (d *requestDocument) cancelSend() {
	if d.composeWaiting && d.sendCancel != nil && !d.sendCancelling {
		d.sendCancelling = true
		d.sendCancel()
	}
}

func (d *requestDocument) title() string {
	return cmp.Or(d.composeName, locales.UntitledRequest())
}

func (a *app) addDocument(r savedRequest, key string) {
	d := a.newDocument(r, key)
	a.openRequestTab(d, tabOpen{})
}

// newDocument is a document for r that no tab or capture holds yet.
func (a *app) newDocument(r savedRequest, key string) *requestDocument {
	a.nextDocument++
	d := &requestDocument{
		id: a.nextDocument, debugKey: uuid.New().String(),
		composeTab: composeTabFor(r), baseline: savedRequest{Method: "GET", URL: "https://"},
	}
	d.load(a, r, key)
	return d
}

// load puts r in the editor; a key says it is that saved asset, which is
// then the baseline its unsaved changes are measured against.
func (d *requestDocument) load(a *app, r savedRequest, key string) {
	d.structured.stop()
	if d.grpcUI != nil {
		d.grpcUI.stop()
		d.grpcUI = nil
	}
	d.stopGraphQL()
	d.graphqlUI = nil
	if d.socketIOUI != nil {
		d.socketIOUI.stop()
		d.socketIOUI = nil
	}
	if d.mqttUI != nil {
		d.mqttUI.stop()
		d.mqttUI = nil
	}
	if d.mcpUI != nil {
		a.stopMCPDocument(d.mcpUI)
		d.mcpUI = nil
	}
	d.savedKey, d.assetID = key, r.ID
	d.composeName, d.composeCollection = r.Name, r.Collection
	d.composeMethod, d.composeURL = cmp.Or(r.Method, "GET"), r.URL
	d.protocol, d.grpcConfig = r.Protocol, r.GRPC
	d.socketIOConfig = r.SocketIO
	d.mqttConfig = r.MQTT
	d.mcpConfig = r.MCP
	d.composeHeaders, d.composeBody = r.Headers, r.Body
	d.problemBodyGen++
	d.wsMessages, d.wsWait = slices.Clone(r.WebSocket.Messages), float64(r.WebSocket.Wait().Milliseconds())
	if len(d.wsMessages) == 0 && r.Body != "" && request.IsWebSocket(r.URL) {
		// A request saved before the message list existed keeps its one message.
		d.wsMessages = []request.WebSocketMessage{{Payload: r.Body}}
	}
	d.headerRows, d.headerFor = slices.Clone(r.HeaderList), ""
	d.composePre, d.composePost, d.checks = r.Pre, r.Post, r.Checks
	d.description, d.settings = r.Description, r.Settings.Clone()
	effective := a.inheritance(r.Collection).InheritSettings(r.Settings)
	d.timeout, d.maxRedirects = effective.Timeout().Seconds(), float64(effective.RedirectLimit())
	d.tokenSources = slices.Clone(r.TokenSources)
	d.examples, d.example, d.exampleEditor = slices.Clone(r.Examples), 0, nil
	d.auth, d.bodyMode, d.formFields = r.Auth, cmp.Or(r.BodyMode, "Raw"), slices.Clone(r.Form)
	d.bodyFile = r.BodyFile
	d.pathRows = slices.Clone(r.PathVariables)
	if rows, err := queryRowsFor(d.composeURL, nil); err == nil {
		for i := range rows {
			if i < len(r.QueryDescriptions) {
				rows[i].Description = r.QueryDescriptions[i]
			}
		}
		d.queryRows, d.querySource = rows, d.composeURL
	}
	d.graphql = r.GraphQL
	if d.headerRows == nil && d.composeHeaders != "" {
		d.headerRows, d.headerFor = parseHeaderRows(d.composeHeaders), d.composeHeaders
	}
	d.auth.Type = cmp.Or(d.auth.Type, "None")
	d.auth.Location = cmp.Or(d.auth.Location, "Header")
	d.setDebugSource(r.Source)
	if key != "" {
		d.baseline = d.value()
	}
}

// A request pane's views. The row fills from requestPaneViews' order, keeping
// as many as fit; the rest open from More, so a pane keeps one row of them.
const (
	composeTabHeaders = iota
	composeTabParams
	composeTabBody
	composeTabAuth
	composeTabRaw
	composeTabScripts
	composeTabChecks
	composeTabExamples
	composeTabDocs
	composeTabSettings
	// composeTabOriginal reads the captured request a draft came from, in
	// the capture's readers. Only a capture that was edited has it.
	composeTabOriginal
	composeTabCookies
)

// composeTabFor opens the tab a request is usually edited in: its body when
// it has one, its headers otherwise.
func composeTabFor(r savedRequest) int {
	if r.Body != "" || r.BodyMode != "" && r.BodyMode != "Raw" && r.BodyMode != "None" {
		return composeTabBody
	}
	return composeTabHeaders
}

// stopDocuments stops every protocol session a request tab owns. Quit calls
// it before the window goes away, so a stdio server or HTTP stream does not
// outlive the app.
func (a *app) stopDocuments() {
	var mqttSessions []*mqttSession
	var mcpSessions []*mcpSession
	seen := map[*mcpSession]bool{}
	addMCP := func(s *mcpSession) {
		if s != nil && !seen[s] {
			seen[s] = true
			mcpSessions = append(mcpSessions, s)
		}
	}
	for _, d := range a.documents {
		d.cancelSend()
		d.structured.stop()
		if d.grpcUI != nil {
			d.grpcUI.stop()
		}
		if d.socketIOUI != nil {
			d.socketIOUI.stop()
		}
		if d.mcpUI != nil {
			addMCP(d.mcpUI.session)
		}
		if d.mqttUI != nil {
			d.mqttUI.stop()
			if d.mqttUI.session != nil {
				mqttSessions = append(mqttSessions, d.mqttUI.session)
			}
		}
	}
	for _, s := range a.closingMCPSessions() {
		addMCP(s)
	}
	var closing sync.WaitGroup
	for _, s := range mcpSessions {
		closing.Go(func() {
			s.closeNow(mcpCloseTimeout)
		})
	}
	waitMQTTSessions(mqttSessions, time.Second)
	closing.Wait()
}

// stopMCPDocument starts a detached document's close off the UI thread and
// keeps the session visible to stopDocuments until it finishes.
func (a *app) stopMCPDocument(u *mcpDocument) {
	if u == nil || u.session == nil {
		return
	}
	s := u.session
	a.mcpClosingMu.Lock()
	if a.mcpClosing == nil {
		a.mcpClosing = map[*mcpSession]struct{}{}
	}
	a.mcpClosing[s] = struct{}{}
	a.mcpClosingMu.Unlock()
	go guarded(func() {
		s.closeNow(mcpCloseTimeout)
		a.mcpClosingMu.Lock()
		delete(a.mcpClosing, s)
		a.mcpClosingMu.Unlock()
	})
}

func (a *app) closingMCPSessions() []*mcpSession {
	a.mcpClosingMu.Lock()
	defer a.mcpClosingMu.Unlock()
	return slices.Collect(maps.Keys(a.mcpClosing))
}

func (a *app) closeDocument(d *requestDocument) {
	if d == nil {
		return
	}
	g := a.tabsGroup()
	id := requestTabID(d)
	if g.indexOf(id) < 0 {
		a.tabDocuments[id] = d
		if !slices.Contains(a.documents, d) {
			a.documents = append(a.documents, d)
		}
		g.add(requestTabDescriptor(d))
	}
	a.closeTab(id, true)
}

// forgetDocument stops a document's sessions and takes it out of the open
// registry. The tab model has already closed its tab; remembering the copy
// for Reopen is the caller's job.
func (a *app) forgetDocument(d *requestDocument) {
	at := slices.Index(a.documents, d)
	if at < 0 {
		return
	}
	d.structured.stop()
	if d.grpcUI != nil {
		d.grpcUI.stop()
	}
	d.stopGraphQL()
	if d.socketIOUI != nil {
		d.socketIOUI.stop()
	}
	if d.mqttUI != nil {
		d.mqttUI.stop()
	}
	if d.mcpUI != nil {
		a.stopMCPDocument(d.mcpUI)
	}
	if a.mcpConsent.d == d {
		if a.attention != nil {
			a.attention.clear(consentAttentionID(a.mcpConsent.shape))
		}
		a.mcpConsent = mcpConsentState{}
	}
	a.documents = slices.Delete(a.documents, at, at+1)
	id := requestTabID(d)
	delete(a.tabDocuments, id)
	if d.sourceFlow != 0 {
		delete(a.flowDrafts, d.sourceFlow)
	}
	if a.live == d {
		a.live, a.liveFor = nil, 0
	}
	a.clearDocumentProblems(d)
	if a.requestDocument == d {
		a.requestDocument = nil
		a.requestChoice = ""
	}
}

// Capture and the open drafts are one cycle: the positional step commands
// step through the request documents and back to the live traffic. Workspace
// shortcuts stay independent, so Cmd/Ctrl+1–4 never unexpectedly select a
// draft tab.
func (a *app) cycleDocument(step int) {
	index := 0
	if !a.page.capture() {
		index = slices.Index(a.documents, a.requestDocument) + 1
	}
	index = (index + step + len(a.documents) + 1) % (len(a.documents) + 1)
	if index == 0 {
		a.activateDocument(nil)
		return
	}
	a.activateDocument(a.documents[index-1])
}

// requestTitleEdit is an open request's name being edited on its breadcrumb.
// The draft stays here until Enter or leaving the field, so Escape can leave
// the saved name untouched.
type requestTitleEdit struct {
	id             int
	name           string
	focus, focused bool
}

// startRequestRename turns the breadcrumb's request name into an input in
// place, as Postman does, rather than opening the rename dialog. Another open
// request's title is committed first, so a click does not discard its draft.
func (a *app) startRequestRename(d *requestDocument) {
	if d == nil {
		return
	}
	if a.titleEdit.id != 0 && a.titleEdit.id != d.id {
		a.commitRequestRename()
	}
	a.titleEdit = requestTitleEdit{id: d.id, name: d.composeName, focus: true}
	a.invalidate()
}

// cancelRequestRename closes the inline title editor without changing the name.
func (a *app) cancelRequestRename(id int) {
	if a.titleEdit.id != id {
		return
	}
	a.titleEdit = requestTitleEdit{}
	a.invalidate()
}

// commitRequestRename writes the name typed on the breadcrumb into the draft.
// An empty or unchanged name just closes; the file is written by Save, as any
// other draft edit is.
func (a *app) commitRequestRename() {
	st := &a.titleEdit
	if st.id == 0 {
		return
	}
	id, name := st.id, strings.TrimSpace(st.name)
	*st = requestTitleEdit{}
	var d *requestDocument
	for _, candidate := range a.documents {
		if candidate.id == id {
			d = candidate
			break
		}
	}
	if d != nil && name != "" && name != d.composeName {
		d.composeName = name
	}
	a.invalidate()
}

// requestTitle draws the breadcrumb's request name: read-only until clicked,
// then the inline editor. The collection prefix is not part of the name and
// stays put while the name changes.
func (a *app) requestTitle(c *ui.Context, t *ui.Theme, d *requestDocument) {
	if a.titleEdit.id == d.id {
		// Bare: the inline rename is the breadcrumb itself, at row height and with the accent edge, not the kit's field face.
		input := ui.TextInput(c, &a.titleEdit.name).
			Label(locales.RenameRequest()).
			Height(uikit.HeightSecondary).Grow(1).MinWidth(uikit.WidthField).
			FontSize(uikit.Scaled(uikit.TextBody)).
			Padding(0, t.Space(uikit.SpaceS)).
			Radius(uikit.RadiusControl).FocusRing(false).
			Background(t.Background).Border(uikit.HairlineWidth, t.Accent)
		if a.titleEdit.focus {
			input.AutoFocus()
			a.titleEdit.focus = false
		}
		input.HandleInput(func(ev ui.InputEvent) bool {
			if ev.Kind == ui.InputKeyDown && ev.Key == ui.KeyEscape {
				a.cancelRequestRename(d.id)
				return true
			}
			return false
		})
		switch {
		case input.Submitted():
			a.commitRequestRename()
		case input.Focused() || input.FocusWithin():
			a.titleEdit.focused = true
		case a.titleEdit.focused:
			a.commitRequestRename()
		}
		return
	}
	button := uikit.ToolbarButton(c, locales.RenameRequest(), locales.RenameRequest(), false).
		Height(uikit.HeightSecondary).Padding(0, t.Space(uikit.SpaceXS)).MinWidth(0)
	button.Children(func() {
		uikit.Body(c, d.title()).FontWeight(500).SingleLine().MinWidth(0)
		if d.dirty() {
			unsavedDot(c)
		}
	})
	if button.Clicked() {
		a.startRequestRename(d)
	}
}

// documentHeader names the one request on screen as a breadcrumb — its
// collection, then its name, which edits in place — with its actions. The
// environment it sends in is at the document strip's end. Other open requests
// are in the sidebar: saved ones in their collection, new ones under Unsaved.
func (a *app) documentHeader(c *ui.Context, t *ui.Theme) {
	d := a.requestDocument
	// The editor's first row is the panel's, as the capture screen's filter
	// bar is: chrome only frames the window, never a line inside the panel.
	// It is the tab strip's height, so the crumb sits close to the URL bar
	// below, and every control in it is the Save button's height, so the
	// crumb and the actions share one centre line.
	ui.Row(c).Height(uikit.HeightTabs).Padding(0, t.Space(uikit.SpaceM)).Gap(t.Space(uikit.SpaceXS)).AlignItems(ui.Center).Background(t.Background).Children(func() {
		if d != nil {
			if d.composeCollection != "" {
				uikit.Meta(c, strings.ReplaceAll(d.composeCollection, "/", " / ")+" /").SingleLine().MinWidth(0)
			}
			a.requestTitle(c, t, d)
		}
		ui.Spacer(c)
		if d != nil {
			// The tab's own × closes the request; the header does not repeat it.
			a.requestDocumentActions(c, t, d)
		}
	})
}

// composeView is the debugging screen: the request as it will be sent on top,
// what came back below, and the provenance of both always visible.
func (a *app) composeView(c *ui.Context, t *ui.Theme) {
	d := a.requestDocument
	if d == nil {
		uikit.EmptyState(c, locales.OpenASavedRequestOr(), locales.KeepDraftsVariablesAndResponses(), func() {
			if uikit.PrimaryButton(c, locales.CreateRequest()).Clicked() {
				a.openCompose()
			}
		})
		return
	}
	a.protoOwner = -1
	if a.draftShowsProtobuf(d) {
		a.protoOwner = 1
	}
	ui.Column(c.Key(d.id)).Grow(1).FillWidth().MinHeight(0).Children(func() {
		a.requestBar(c, t, d, nil, true)
		a.requestVariablesPanel(c, t, d)
		if d.comparing {
			ui.Column(c).Grow(1).Children(func() { a.requestResponse(c, t, d) })
			return
		}
		if d.requestPercent == 0 {
			d.requestPercent = a.draftShare(c)
		}
		// A share the reader set is theirs; the app only moves the divider it
		// still owns.
		if d.splitSeen != 0 && d.requestPercent != d.splitSeen {
			d.splitTouched = true
		}
		d.splitSeen = d.requestPercent
		request := func() { a.requestEditor(c, t, d, flow{}, &d.composeTab) }
		// A saved request's runs and examples are what came back before, so
		// they sit at the response's side, under the rule that ends the URL
		// bar, rather than beside the whole editor.
		response := func() {
			ui.Row(c).Fill().AlignItems(ui.Stretch).Bind(&a.editorRespArea).Children(func() {
				ui.Column(c).Grow(1).MinWidth(0).Children(func() {
					if r := a.experiments.record(d.historyPick); r != nil {
						a.historyResponse(c, t, d, r)
						return
					}
					a.requestResponse(c, t, d)
				})
				if a.historyColumnShown(c, d) {
					a.historyColumn(c, t, d)
				}
			})
		}
		// The same rule as the inspector's panes: side by side while there is
		// room for both, stacked in a narrow column.
		if a.requestSidesStacked(c) {
			uikit.SplitVerticalPercent(c.Key("request-editor"), &d.requestPercent, request, response).SplitReset(a.draftShare(c)).Grow(1)
			return
		}
		// Side by side, the divider runs up to a rule under the URL bar, so
		// it meets a line instead of stopping in the air.
		uikit.SplitPercent(c.Key("request-editor"), &d.requestPercent, request, response).SplitReset(a.draftShare(c)).Grow(1).
			Border(0, t.Border).BorderWidth(1, 0, 0, 0)
	})
}

// responseFormatsOf is what the response body can be read as, and the format
// its Content-Type names, which the first entry resolves to.
func responseFormatsOf(d *requestDocument) ([]string, string) {
	formats := append([]string(nil), responseFormats...)
	detected := inspect.BodyFormat(d.response.ResponseHeader.Get("Content-Type"))
	if detected != "" {
		formats = append(formats, detected)
	}
	if d.responseView >= len(formats) {
		d.responseView, d.responseEditor = 0, nil
	}
	return formats, detected
}

// responseFormatActions are the response body's representations and the query,
// for a menu the pane's row already has.
func (a *app) responseFormatActions(d *requestDocument) []action {
	formats, _ := responseFormatsOf(d)
	items := make([]action, 0, len(formats)+1)
	for i, name := range formats {
		items = append(items, do("response.format", name, func() {
			d.responseView, d.responseEditor, d.responseList = i, nil, ui.ListState{}
		}).checked(checkbox(i == d.responseView)))
	}
	return append(items, do("response.query", locales.QueryJson(), func() {
		r := d.response
		a.openJSONQuery(inspectionBody{r.ResponseBody, r.ResponseBodyFile, r.ResponseBodyError, r.ResponseBodySize})
	}))
}

// responseArrived gives the first response the room it needs, unless the reader
// has set the divider themselves.
func (d *requestDocument) responseArrived() {
	d.cookieReveal = false
	if d.splitTouched {
		return
	}
	d.requestPercent, d.splitSeen = responsePercent, responsePercent
}

// headerWarnModal asks before the Key: Value block puts every credential on
// the screen: the table masks them one by one, the block cannot.
func (a *app) headerWarnModal(c *ui.Context, t *ui.Theme) {
	d := a.requestDocument
	if d == nil || !d.headerWarn {
		return
	}
	if result := uikit.ConfirmDialog(c, &d.headerWarn, uikit.ConfirmOptions{
		Title:    locales.BulkEditShowsCredentials(),
		Message:  locales.TheKeyValueBlockWrites(),
		Detail:   locales.SwitchingModesDoesNotSend(),
		Severity: uikit.SeverityWarning,
		Primary:  locales.ShowValues(),
		Cancel:   locales.Cancel(),
	}); result != uikit.ConfirmNone {
		d.headerWarn = false
		if result == uikit.ConfirmPrimary {
			toggleHeaderMode(d)
		}
	}
}

// requestRenameModal edits the document's name and collection, so neither
// needs a permanent input on the breadcrumb.
func (a *app) requestRenameModal(c *ui.Context, t *ui.Theme) {
	d := a.requestDocument
	if !a.renameOpen || d == nil {
		return
	}
	title := locales.RenameRequest()
	if a.saveAfterRename {
		title = locales.SaveToCollection()
	}
	uikit.Modal(c, &a.renameOpen, title, func() {
		uikit.DialogColumn(c, uikit.DialogSmall).Children(func() {
			uikit.DialogTitle(c, title)
			uikit.Input(c, &d.composeName).Label(locales.Name()).Placeholder(locales.UntitledRequest()).FillWidth().
				Height(uikit.HeightRow).Padding(0, t.Space(uikit.SpaceM)).Radius(uikit.RadiusControl)
			uikit.Input(c, &d.composeCollection).Label(locales.Collection()).Placeholder(locales.Collection()).FillWidth().
				Height(uikit.HeightRow).Padding(0, t.Space(uikit.SpaceM)).Radius(uikit.RadiusControl)
			uikit.Meta(c, locales.TheCollectionGroupsSavedRequests())
			uikit.DialogFooter(c).Children(func() {
				if uikit.SecondaryButton(c, locales.Cancel()).Clicked() {
					a.renameOpen = false
				}
				if a.saveAfterRename {
					if uikit.PrimaryButton(c, locales.Save()).Clicked() && a.saveComposed() {
						a.renameOpen = false
					}
					return
				}
				if uikit.PrimaryButton(c, locales.Done()).Clicked() {
					a.renameOpen = false
				}
			})
		})
	})
}

// requestDocumentActions never scrolls away with a long document tab strip.
func (a *app) requestDocumentActions(c *ui.Context, t *ui.Theme, d *requestDocument) {
	ui.Row(c.Key(d.id)).Gap(t.Space(uikit.SpaceXS)).AlignItems(ui.Center).Children(func() {
		if d.sourceAPI != "" {
			if uikit.ToolbarButton(c, locales.SourceApi(), locales.OpenTheApiThisRequest(), false).Children(func() {
				uikit.Body(c, locales.SourceApi()).TextColor(t.Accent)
			}).Clicked() {
				a.openAPITab(d.sourceAPI)
			}
		}
		a.actionButton(c, "request.save", locales.Save())
		uikit.MoreMenu(c, locales.RequestOptions(), func(m *ui.Menu) { actionMenu(m, a.documentActions(c, d)) })
	})
}

// duplicateDocument opens a copy of a request as a new unsaved one.
func (a *app) duplicateDocument(d *requestDocument) {
	if d == nil {
		return
	}
	before := a.snapshotDraftsUndo()
	r := d.value()
	r.ID, r.Name = "", uniqueRequestName(a.requests, r.Name+" copy")
	a.addDocument(r, "")
	after := a.snapshotDraftsUndo()
	a.recordUndo(undoRecord{code: "draft.duplicate", label: locales.DuplicateAsNewRequest(),
		before: before, after: after})
}

// documentActions are an open request's, beside Save. Shared commands come
// from the registry, built for this tab rather than the active document, so
// their shortcut and enabled state cannot drift from the menu item's.
func (a *app) documentActions(c *ui.Context, d *requestDocument) []action {
	global := a.actionsFor(c, d)
	return []action{
		pick(global, "request.duplicate"),
		do("request.copy-curl", locales.CopyAsCurl(), func() { a.copyRequest(c, flow{}, d, "cURL") }),
		pick(global, "request.export-http"),
		copyAsMenu(a.requestCopyActions(c, flow{}, d)),
		pick(global, "request.code"),
		do("request.history", locales.History(), func() { a.openSendHistory(d) }),
		pick(global, "collection.settings").as(settingsLabel(d.composeCollection)).in("context"),
		pick(global, "environment.manage").in("context"),
		do("request.variables", locales.RequestVariables(), func() { d.variablesOpen = !d.variablesOpen }).in("context"),
		pick(global, "editor.action.rename").in("context"),
		pick(global, "editor.action.goToReferences").in("context"),
		pick(global, "vault.open").in("context"),
		pick(global, "request.close-others").in("tabs"),
		pick(global, "request.close-saved").in("tabs"),
		pick(global, "request.reopen-closed").in("tabs"),
	}
}

func (a *app) requestVariablesPanel(c *ui.Context, t *ui.Theme, d *requestDocument) {
	vars := a.activeVars()
	r := d.value()
	in := a.inheritance(r.Collection)
	rows := r.ResolveVariables(in, vars, nil)
	missing := 0
	for i := range rows {
		switch rows[i].Source {
		case "Missing":
			missing++
		case "Run input":
			rows[i].Source = locales.EnvironmentSourceNamed(cmp.Or(a.activeEnv, locales.NoneLower()))
		}
	}
	if len(rows) == 0 && !d.variablesOpen {
		return
	}
	target := locales.UnresolvedReviewVariables()
	if r.Protocol == "MCP" && r.MCP.Transport == request.MCPTransportStdio {
		target = locales.StdioProcess()
	} else if u, err := url.Parse(request.ExpandVars(d.composeURL, in.Values(vars))); err == nil && u.Hostname() != "" {
		target = u.Scheme + "://" + u.Host // Do not reveal query tokens or userinfo.
	}
	targetLabel := locales.TargetBeforeScripts
	contextLabel := locales.DraftContextPreScript()
	if r.Protocol != "" {
		targetLabel, contextLabel = locales.TargetAfterVariables, locales.DraftContextResolved()
	}
	ui.Row(c).Height(uikit.HeightRow).Padding(0, t.Space(uikit.SpaceL)).Gap(t.Space(uikit.SpaceM)).AlignItems(ui.Center).Children(func() {
		if uikit.CompactButton(c, locales.VariablesUnresolved(missing)).Label(locales.RequestVariables()).Clicked() {
			d.variablesOpen = !d.variablesOpen
		}
		uikit.Meta(c, targetLabel(target)).SingleLine().Grow(1)
	})
	if !d.variablesOpen {
		return
	}
	ui.Column(c).Height(160).Padding(t.Space(uikit.SpaceM), t.Space(uikit.SpaceL)).Gap(t.Space(uikit.SpaceM)).Background(uikit.Colors(t).Window).Children(func() {
		ui.Row(c).Gap(t.Space(uikit.SpaceM)).Children(func() {
			uikit.Meta(c, contextLabel).Grow(1)
			if uikit.CompactButton(c, locales.EditEnvironments()).Clicked() {
				a.openVars()
			}
		})
		a.variableRows(c, rows, &d.variablesReveal, func(row request.ResolvedVariable) *variableTarget {
			t := variablePanelTarget(row, a.activeEnv)
			return &t
		})
	})
	ui.Divider(c)
}

func (a *app) variableRows(c *ui.Context, rows []request.ResolvedVariable, reveal *bool, target func(request.ResolvedVariable) *variableTarget) {
	t := c.Theme()
	uikit.Checkbox(c, reveal, locales.RevealPlaintextValues())
	ui.Scroll(c).Grow(1).MinHeight(0).FillWidth().Gap(t.Space(uikit.SpaceM)).Children(func() {
		if len(rows) == 0 {
			uikit.Meta(c, locales.NoReferencedVariablesInThis())
		}
		for _, row := range rows {
			ui.Row(c).Gap(t.Space(uikit.SpaceM)).Children(func() {
				uikit.DataMeta(c, row.Name).Width(uikit.WidthPicker)
				value, color := "••••••", t.Text
				if row.Missing {
					value, color = "Unresolved", t.Warning
				} else if *reveal {
					value = row.Value
					if value == "" {
						value = locales.EmptyValue()
					}
				}
				uikit.Caption(c, value).TextColor(color).Grow(1).Selectable()
				uikit.Meta(c, row.Source).Width(180)
				if target != nil {
					a.variableRowActions(c, *target(row), row.Name)
				}
			})
		}
	})
}

// draftPercent is the share of the debugging screen the request editor gets
// while nothing has come back: the design's 5:6. A column split needs more of
// the height for the editor than a row split needs of the width, so stacked
// panes start at draftStackedPercent. A response pulls either to
// responsePercent, which leaves the reader the wider half.
const (
	draftPercent        = splitShare
	draftStackedPercent = 55
	responsePercent     = 35
)

// editorWidth is the room the editor has: the content less the history
// column a saved request shows beside it.
func (a *app) editorWidth(c *ui.Context) float32 {
	return a.contentWidth(c)
}

// requestSidesStacked reports whether the debugging screen stacks its two
// panes. A request tab may pin either layout; otherwise it follows the room,
// as the capture inspector does.
func (a *app) requestSidesStacked(c *ui.Context) bool {
	if d := a.requestDocument; d != nil && d.split != nil {
		return !*d.split
	}
	return a.sidesStacked(c)
}

// editorPaneWidthAt is how wide one pane of the editor was laid out, from the
// frame before, or the share it expects before the first layout.
func (a *app) editorPaneWidthAt(c *ui.Context, h *ui.Handle, share float32) float32 {
	if r := h.Bounds(c); r.W > 0 {
		return r.W
	}
	if a.requestSidesStacked(c) {
		return a.editorWidth(c)
	}
	return a.editorWidth(c) * share / 100
}

// draftShare is where the editor's divider starts in this layout.
func (a *app) draftShare(c *ui.Context) float32 {
	if a.requestSidesStacked(c) {
		return draftStackedPercent
	}
	return draftPercent
}

// requestMethods are the methods the editor offers; any other is typed.
var requestMethods = []string{"GET", "POST", "PUT", "PATCH", "DELETE", "HEAD", "OPTIONS"}

// requestMethodWidth is the room the method dropdown's field takes: its
// longest method and its own padding, so the chevron sits right after the word
// as Postman's does. A typed method wider than this scrolls inside the field.
func requestMethodWidth(c *ui.Context) float32 {
	return paneTextWidth(c, "OPTIONS", uikit.TextBody, 500) + 2*c.Theme().Space(uikit.SpaceS)
}

// requestBar is the line a person edits and sends: method, URL, and the one
// prominent action.
// requestBar is the method, URL and Send; more adds what follows Send.
// protocolDefaultURL gives a new protocol a URL that its editor can use,
// but only while the URL is still the untouched https:// a new request
// starts with.
func protocolDefaultURL(protocol, current string) string {
	if current != "https://" {
		return current
	}
	switch protocol {
	case "MQTT":
		return "mqtts://"
	case "WebSocket":
		return "wss://"
	default:
		return current
	}
}

// requestBar is the method, URL and Send; more adds what follows Send. compact
// draws it at the pane header's height for the request editor, whose crumb sits
// directly above it; the capture inspector keeps its taller bar.
func (a *app) requestBar(c *ui.Context, t *ui.Theme, d *requestDocument, more func(), compact bool) {
	height := float32(48)
	if compact {
		height = uikit.HeightBar
	}
	ui.Row(c).Height(height).Padding(t.Space(uikit.SpaceM), t.Space(uikit.SpaceL)).Gap(t.Space(uikit.SpaceM)).AlignItems(ui.Center).Children(func() {
		field := ui.Row(c).Grow(1).Height(uikit.HeightControl).AlignItems(ui.Center).MinWidth(0)
		field.Disabled(d.protocol != "" && d.composeWaiting)
		uikit.FieldFace(c, field, field.FocusWithin()).Children(func() {
			if !a.page.capture() {
				a.requestTargetControl(c, t, d)
				// The method and the URL are one field, told apart by a rule
				// that reads on the well: the panel hairline disappears there.
				ui.Box(c).Width(1).Height(18).Background(t.TextMuted.Alpha(0.35))
			}
			if d.protocol == "MCP" && d.mcpConfig.Transport == request.MCPTransportStdio {
				ui.Row(c).Grow(1).Height(uikit.HeightRow).AlignItems(ui.Center).Padding(0, t.Space(uikit.SpaceM)).Children(func() {
					uikit.Meta(c, locales.StdioCommand(cmp.Or(d.mcpConfig.Command, locales.LocalCommand()))).Font(uikit.FontMono).SingleLine()
				})
				return
			}
			placeholder := "https://api.example.com/v1/resource"
			if d.protocol == "MQTT" {
				placeholder = "mqtts://broker.example.com:8883"
			}
			if d.protocol == "Socket.IO" {
				placeholder = "https://host/socket.io"
			}
			a.takeURLPaste(d)
			// Bare: the URL bar is one face with the method and Send beside it, which the kit's field face does not cover.
			input := uikit.TextInset(ui.TextInputBase(c, &d.composeURL).Bind(&d.urlInput).Grow(1).MinWidth(0).Height(uikit.HeightRow).Label(locales.Url()).
				Placeholder(placeholder).Font(uikit.FontMono).OnPaste(func(text string) bool {
				return a.pasteURLImport(d, text)
			}), t.Space(uikit.SpaceM))
			start, end := input.TextSelection()
			a.urlPasteSelection = [2]int{start, end}
			caret := [2]int{start, end}
			var set variableSet
			var marks []variableMark
			if strings.Contains(d.composeURL, "{{") {
				set = a.variableHintSet(d.composeCollection)
				marks = variableMarksWith(set, d.composeURL)
			}
			for _, mark := range marks {
				color := t.Accent
				if !mark.Defined {
					color = t.Danger
				}
				input.TextRanges(ui.TextRange{Start: mark.Start, End: mark.End, Color: color, Weight: 500})
			}
			a.variableTooltip(c, input, marks, false, set, d.composeCollection)
			a.syncVariableCompletion(input, "URL", &d.composeURL)
			a.variablePopover(c, input, "URL", &d.composeURL, set, func(k ui.TextKey) bool {
				return a.urlHistory.textKey(k, caret)
			})
			spans := urlSpans(d.composeURL, t, marks)
			hasVariables := len(marks) > 0
			if !input.Focused() && hasVariables {
				// Paint chips only at rest. The same native input always owns
				// selection, undo, clipboard and IME; editing shows styled text.
				input.DrawOver(func(p *ui.Painter, r ui.Rect) {
					p.Fill(r, t.Background, 0)
					r.X, r.W = r.X+8, max(0, r.W-16)
					p.Clip(r, 0, func() {
						_, h := p.MeasureText(0, spans...)
						p.RichText(r.X, r.Y+(r.H-h)/2, 0, spans...)
					})
				})
			}
			a.unicodeMarker(c, "request-url", d.composeURL, false)
			a.unicodeInputMarks(c, input, d.composeURL, false)
			a.bindQuickFixShortcut(c, &d.urlInput)
			a.trackInputQuickFix(d, problemFieldURL, &d.composeURL, input)
			a.quickFixContextMenu(c, input, &d.composeURL)
		})
		sendLabel := locales.Send()
		if d.composeWaiting {
			sendLabel = locales.Sending()
		}
		if d.protocol == "Socket.IO" || d.protocol == "MQTT" || d.protocol == "MCP" {
			sendLabel = locales.Connect()
			if d.composeWaiting {
				sendLabel = locales.Active()
			}
		}
		// A capture's inspector beside the list keeps its width for the URL:
		// a narrower Send, and while sending only Cancel in its place (the
		// response says it is sending).
		narrow := a.page.capture() && a.inspectorWidth(c) < 600
		sendWidth, cancelWidth := float32(80), float32(96)
		if narrow {
			sendWidth, cancelWidth = 64, 76
		}
		if !narrow || !d.composeWaiting {
			a.primaryActionButton(c, "request.send", sendLabel).Width(sendWidth).Shrink(0)
		}
		if d.composeWaiting {
			label := locales.Cancel()
			if d.protocol == "Socket.IO" || d.protocol == "MQTT" || d.protocol == "MCP" {
				label = locales.Disconnect()
			}
			if d.sendCancelling && !narrow {
				label = locales.Cancelling()
			}
			cancel := a.actionButton(c, "request.cancel-send", label).Label(locales.CancelSend()).Width(cancelWidth).Shrink(0)
			uikit.Tip(c, cancel, locales.StopWaitingCancellingDoesNot())
		}
		if more != nil {
			more()
		}
	})
}

// requestProtocols are the protocols a request document can speak, in the
// order the control before the URL lists them. HTTP is the empty protocol.
var requestProtocols = []string{"HTTP", "gRPC", "Socket.IO", "MQTT", "MCP"}

// switchProtocol makes the document speak another protocol. The session the
// old one owned stops, so a completed protocol job never drains into the next
// one: config stays saved, runtime state does not.
func (a *app) switchProtocol(d *requestDocument, protocol string) {
	if d.grpcUI != nil {
		d.grpcUI.stop()
		d.grpcUI = nil
	}
	if d.socketIOUI != nil {
		d.socketIOUI.stop()
		d.socketIOUI = nil
	}
	if d.mqttUI != nil {
		d.mqttUI.stop()
		d.mqttUI = nil
	}
	if d.mcpUI != nil {
		a.stopMCPDocument(d.mcpUI)
		d.mcpUI = nil
	}
	d.protocol = protocol
	if protocol == "HTTP" {
		d.protocol = ""
	}
	d.composeURL = protocolDefaultURL(protocol, d.composeURL)
}

// requestTargetControl is the one dropdown before the URL. An HTTP request
// shows its method there; a document that speaks another protocol shows that
// protocol; both are the same button, so the bar reads the same either way.
// The protocols sit in the method's menu, so no protocol is out of reach.
func (a *app) requestTargetControl(c *ui.Context, t *ui.Theme, d *requestDocument) {
	switch {
	case d.methodCustom:
		a.requestMethodInput(c, t, d)
	case d.protocol != "":
		a.requestProtocolButton(c, t, d)
	default:
		a.requestMethodButton(c, t, d)
	}
}

// requestTargetButton is the button both request targets share: a word, its
// chevron, and the menu that changes it. It is as wide as its word, so the URL
// starts right after it, as Postman's method does.
func requestTargetButton(c *ui.Context, t *ui.Theme, name, tip, word string, ink ui.Color, menu func(*ui.Menu)) ui.Element {
	var b ui.Element
	uikit.MenuFace(c, func() {
		b = uikit.ToolbarButton(c, name, tip, false).Height(uikit.HeightRow).
			Padding(0, t.Space(uikit.SpaceXS)).Gap(t.Space(uikit.SpaceHair)).Shrink(0)
		b.Children(func() {
			uikit.BodyStrong(c, word).TextColor(ink).SingleLine()
			ui.Icon(c, chevronIcon).Size(uikit.IconInline, uikit.IconInline).TextColor(t.TextMuted)
		})
		b.PopoverMenuButton(menu, uikit.MenuButtonOptions(c))
	})
	return b
}

// requestMethodButton is the method's dropdown: the methods, the protocols
// under them, and Custom… for a method the list lacks.
func (a *app) requestMethodButton(c *ui.Context, t *ui.Theme, d *requestDocument) {
	requestTargetButton(c, t, locales.Method(), locales.CommonMethods(), d.composeMethod,
		uikit.MethodColor(d.composeMethod, t), func(m *ui.Menu) {
			for _, method := range requestMethods {
				if m.Item(method).Radio(d.composeMethod == method).Chosen() {
					d.composeMethod = method
				}
			}
			m.Separator()
			m.Submenu(locales.Protocol(), func(m *ui.Menu) {
				for _, protocol := range requestProtocols {
					if m.Item(protocol).Radio(cmp.Or(d.protocol, "HTTP") == protocol).Chosen() {
						a.switchProtocol(d, protocol)
					}
				}
			})
			m.Separator()
			if m.Item(locales.CustomMethod()).Chosen() {
				d.methodCustom, d.methodFocus = true, true
			}
		})
}

// requestMethodInput is the method's own short field, shown only after
// Custom… is chosen: a method the menu lacks is typed, and Enter or leaving
// the field puts the button back.
func (a *app) requestMethodInput(c *ui.Context, t *ui.Theme, d *requestDocument) {
	// Bare: the method's width follows its text, which the kit's fixed-width fields do not do.
	in := ui.TextInputBase(c, &d.composeMethod).Width(requestMethodWidth(c)).Height(uikit.HeightRow).
		Padding(0, t.Space(uikit.SpaceXS)).Label(locales.Method()).FontWeight(500).
		TextColor(uikit.MethodColor(d.composeMethod, t)).Border(0, t.Border)
	if d.methodFocus {
		in.Focus()
		d.methodFocus = false
	} else if !in.Focused() {
		d.methodCustom = false
	}
	if in.Changed() {
		d.composeMethod = strings.ToUpper(strings.TrimSpace(d.composeMethod))
	}
	if in.Submitted() {
		d.methodCustom = false
	}
}

// requestProtocolButton is the same button for a document that is not HTTP:
// it names the protocol it speaks, and its menu switches it, HTTP included.
func (a *app) requestProtocolButton(c *ui.Context, t *ui.Theme, d *requestDocument) {
	b := requestTargetButton(c, t, locales.RequestProtocol(), locales.RequestProtocol(), d.protocol, t.Text, func(m *ui.Menu) {
		for _, protocol := range requestProtocols {
			if m.Item(protocol).Radio(d.protocol == protocol).Chosen() {
				a.switchProtocol(d, protocol)
			}
		}
	})
	b.Disabled(d.composeWaiting || d.grpcUI != nil && d.grpcUI.loading != nil)
}

// urlSpans styles references, never expands them or reads their secret values.
func urlSpans(raw string, t *ui.Theme, marks []variableMark) []ui.Span {
	plain := func(text string) ui.Span {
		return ui.Span{Text: text, Font: uikit.FontMono, Size: uikit.TextBody, Color: t.Text}
	}
	var spans []ui.Span
	at := 0
	for _, mark := range marks {
		if mark.Ref.Start > at {
			spans = append(spans, plain(raw[at:mark.Ref.Start]))
		}
		color := t.Accent
		if !mark.Defined {
			color = t.Danger
		}
		spans = append(spans, ui.Span{Text: raw[mark.Ref.Start:mark.Ref.End], Font: uikit.FontMono, Size: uikit.TextBody,
			Color: color, Weight: 500, Background: color.Alpha(0.12), Radius: uikit.RadiusControl})
		at = mark.Ref.End
	}
	if at < len(raw) {
		spans = append(spans, plain(raw[at:]))
	}
	return spans
}

// formatRequestJSON indents an authored JSONC body with VS Code's formatter.
// ParseTree gates the Format tool: unlike the old strict JSON check, comments
// and trailing commas stay in the editor document, and the target's EOL is
// preserved.
func formatRequestJSON(text string) (string, bool) {
	if _, errs := jsonc.ParseTree(text); len(errs) != 0 {
		return "", false
	}
	options := jsonc.FormattingOptions{TabSize: 2, InsertSpaces: true, EOL: jsonc.GetEOL(jsonc.FormattingOptions{}, text)}
	formatted, err := jsonc.ApplyEdits(text, jsonc.Format(text, nil, options))
	if err != nil {
		return "", false
	}
	return formatted, true
}

// requestEditor is the draft's own body: parameters, auth, headers, body and
// scripts, each a tab of the same document.
// requestEditor edits the request's parts; original, when set, reads the
// captured request it came from in its own tab.
func (a *app) requestEditor(c *ui.Context, t *ui.Theme, d *requestDocument, f flow, selected *int) {
	if d.protocol == "Socket.IO" {
		a.socketIOEditor(c, t, d)
		return
	}
	if d.protocol == "MQTT" {
		a.mqttEditor(c, t, d)
		return
	}
	if d.protocol == "gRPC" {
		a.grpcEditor(c, t, d)
		return
	}
	if d.protocol == "MCP" {
		a.mcpEditor(c, t, d)
		return
	}
	if *selected == composeTabOriginal && !a.edited(f.id) {
		*selected = composeTabHeaders
	}
	// The body type is a row of radio buttons, as Postman has it. The reader
	// and Format follow it on the same row when the pane has room; in a narrow
	// pane the type keeps its own row above them.
	width := a.editorPaneWidthAt(c, &a.editorReqPane, draftPercent)
	bodyTools := func() {
		typeRow := func() {
			before := d.bodyMode
			group := uikit.RadioGroup(c, func() {
				for _, kind := range bodyTypes {
					uikit.CompactRadio(c, &d.bodyMode, kind, optionLabel(kind))
				}
			}).Row().Gap(t.Space(uikit.SpaceL)).Label(locales.RequestBodyType())
			if d.bodyMode != before {
				// The table below changes with the mode, and its column
				// budget is read from the previous frame. Ask for that frame
				// now instead of waiting for the pointer to move.
				c.Invalidate()
			}
			if width < 560 {
				group.FillWidth().Wrap()
			} else {
				group.Shrink(0)
			}
		}
		// Reading the bytes is a view of the same draft: the formats a
		// capture's readers used to offer, without leaving the Body tab.
		formats := a.draftBodyFormats(d)
		shown := formats[min(max(d.bodyRead, 0), len(formats)-1)]
		reader := func() {
			ui.Row(c).Disabled(d.bodyMode != "Raw").Children(func() {
				uikit.Tip(c, uikit.ToolbarSelect(c, locales.ReadBodyAs()+": "+shown, func(m *ui.Menu) {
					for i, name := range formats {
						if m.Item(name).Checked(i == d.bodyRead).Chosen() {
							d.bodyRead = i
						}
					}
				}).Label(locales.ReadBodyAs()), locales.ReadBodyAsHelp())
			})
		}
		text, seen := &d.composeBody, &d.bodyFor
		formatLabel := locales.FormatBody()
		formatHelp := locales.FormatBodyHelp()
		canFormat := d.bodyMode == "Raw"
		if d.bodyMode == "GraphQL" && d.graphqlTab == 1 {
			text, seen = &d.graphql.Variables, &d.varsFor
			formatLabel, canFormat = locales.FormatGraphqlVariables(), true
		}
		formatted, isJSON := formatRequestJSON(*text)
		query := d.bodyMode == "GraphQL" && d.graphqlTab == 0
		if query {
			formatLabel, formatHelp = locales.FormatGraphQLQuery(), locales.FormatTheGraphqlDocumentWithout()
		}
		format := func() {
			ui.Row(c).Disabled(!query && (!isJSON || !canFormat)).Children(func() {
				clicked := uikit.ToolbarButton(c, formatLabel, formatHelp, false).Children(func() {
					ui.Icon(c, formatIcon).Size(uikit.IconInline, uikit.IconInline)
					uikit.Caption(c, locales.Format())
				}).Clicked()
				if clicked {
					if query {
						value, err := graphql.Format(d.graphql.Query)
						if err != nil {
							a.fail(locales.FormatGraphqlQuery(), err)
							return
						}
						d.graphql.Query, d.queryFor = value, ""
						return
					}
					// Rebuild the editor rather than writing its old buffer back.
					*text, *seen = formatted, ""
				}
			})
		}
		if width >= 700 {
			ui.Row(c).FillWidth().Height(uikit.HeightControl).Gap(t.Space(uikit.SpaceM)).AlignItems(ui.Center).Children(func() {
				typeRow()
				if d.bodyMode != "None" {
					reader()
					ui.Spacer(c)
					format()
				}
			})
			return
		}
		ui.Column(c).FillWidth().Gap(t.Space(uikit.SpaceM)).Children(func() {
			typeRow()
			if d.bodyMode != "None" {
				ui.Row(c).FillWidth().Height(uikit.HeightControl).Gap(t.Space(uikit.SpaceM)).AlignItems(ui.Center).Children(func() {
					reader()
					ui.Spacer(c)
					format()
				})
			}
		})
	}
	ui.Column(c).Fill().MinHeight(0).Bind(&a.editorReqPane).Children(func() {
		a.requestPaneHeader(c, t, selected, d, f, paneTools{format: -1, width: width})
		if *selected == composeTabBody && !request.IsWebSocket(d.composeURL) {
			ui.Column(c).FillWidth().Padding(t.Space(uikit.SpaceXS), t.Space(uikit.SpaceL)).Gap(t.Space(uikit.SpaceM)).Children(bodyTools)
		}
		// Settings is one Scroll of its own: it keeps the pane's padding
		// inside, so its scroll bar sits at the pane's edge as VS Code's
		// does, rather than a side padding away from the divider.
		if *selected == composeTabSettings {
			ui.Column(c).Grow(1).MinHeight(0).FillWidth().Children(func() {
				a.requestSettingsEditor(c, d, t)
			})
		} else {
			ui.Column(c).Grow(1).MinHeight(0).FillWidth().Padding(t.Space(uikit.SpaceM), t.Space(uikit.SpaceL)).Gap(t.Space(uikit.SpaceM)).Children(func() {
				switch *selected {
				case composeTabParams:
					a.paramsEditor(c, d, t)
				case composeTabHeaders:
					a.headerEditor(c, d, t)
				case composeTabAuth:
					a.authEditor(c, d, t)
				case composeTabBody:
					a.requestBodyEditor(c, d, t)
				case composeTabScripts:
					a.requestScriptsEditor(c, d, t)
				case composeTabChecks:
					a.checksEditor(c, d, t)
				case composeTabExamples:
					a.examplesEditor(c, d, t)
				case composeTabDocs:
					a.requestDocsEditor(c, d, t)
				case composeTabOriginal:
					a.headersSide(c, f, 1)
				case composeTabCookies:
					cookieView(c, f.reqHeader, false, &a.inspect.reveal[1], func() { a.saveFlowCookies(f) })
				}
			})
		}
		// A visible response already names the last attempt and draft changes.
		if d.composeWaiting || d.sentSet && d.response == nil {
			ui.Row(c).Height(uikit.HeightRow).Padding(0, t.Space(uikit.SpaceL)).AlignItems(ui.Center).Children(func() {
				a.requestSentState(c, t, d)
			})
		}
	})
}

// requestSentState says whether the draft on screen is the one that was
// actually sent, and when.
func (a *app) requestSentState(c *ui.Context, t *ui.Theme, d *requestDocument) {
	switch {
	case d.composeWaiting:
		uikit.Meta(c, locales.Sending())
	case !d.sentSet:
		uikit.Meta(c, locales.NotSentYet())
	case reflect.DeepEqual(d.draft(), d.sent):
		uikit.Tip(c, uikit.Meta(c, locales.DraftMatchesAttemptAt(d.sentAt.Local().Format("15:04:05"))),
			locales.TheDraftSnapshotMatchesVariables())
	default:
		uikit.Tip(c, uikit.Caption(c, locales.EditedSinceLastSendNot()).TextColor(t.Warning),
			locales.TheResponseBelowBelongsTo())
	}
}

// requestResponse is what came back: the result first, then the body with
// its own format, headers, cookies and timing, tied to the send that produced
// it.
func (a *app) requestResponse(c *ui.Context, t *ui.Theme, d *requestDocument) {
	if d.protocol == "Socket.IO" {
		a.socketIOResponse(c, t, d)
		return
	}
	if d.protocol == "MQTT" {
		a.mqttResponse(c, t, d)
		return
	}
	if d.protocol == "gRPC" {
		a.grpcResponse(c, t, d)
		return
	}
	if d.protocol == "MCP" {
		a.mcpResponse(c, t, d)
		return
	}
	outcome, detail := d.sendOutcome, d.composeResult
	if r := a.experiments.record(d.experimentID); r != nil && r.RunKey == d.sentRun && r.Outcome == "Network error" {
		outcome, detail = r.Outcome, r.Summary
	}
	width := a.editorPaneWidthAt(c, &a.editorRespPane, 100-draftPercent)
	ui.Column(c).Fill().Bind(&a.editorRespPane).Children(func() {
		// Nothing came back yet, so the row says nothing about a response:
		// the pane's tabs already name what it would show.
		sum := responseSummary{dot: uikit.Colors(t).MutedForeground}
		if r := d.response; r != nil {
			sum = sentSummaryOf(t, r, outcome)
		}
		if d.composeWaiting {
			sum.title = locales.Sending()
		}
		// The pane's own controls take their room in the order the row gives
		// it up: the actions first, then the format menu, and Decode last.
		// What does not fit is in the pane's menu instead, so a tab is never
		// cut.
		var leftOut []action
		tools := func(room *paneRoom) {
			type control struct {
				label, tip string
				icon       *ui.SVG
				disabled   bool
				run        func()
			}
			var actions []control
			if d.source != nil && d.response != nil && !d.comparing {
				actions = append(actions, control{locales.CompareWithSource(), locales.CompareThisResponseWithThe(), compareIcon, false, func() { d.comparing = true }})
			}
			if d.response != nil && d.response.Status != 0 && !d.composeWaiting {
				actions = append(actions, control{locales.SaveResponseBody(), locales.SaveBodyCompleteDecodedBytes(), saveIcon, false, func() { a.saveResponseBody(*d.response) }})
			}
			if d.response != nil && !d.composeWaiting && outcome != "Network error" && d.sentSet {
				actions = append(actions, control{locales.SaveAsExample(), locales.KeepThisResponseWithThe(), exampleIcon, false, func() { a.saveExample(d) }})
			}
			// A saved request's runs are a column of the response while it
			// has room, which the icon shows and hides; otherwise the icon
			// opens them on their own page. Without a retained send there is
			// nothing to read there, so the entry waits rather than opening
			// an empty list.
			if a.historyColumnFits(c, d) {
				label := locales.HideRequestRuns()
				if d.historyHidden {
					label = locales.ShowRequestRuns()
				}
				actions = append(actions, control{label, label, sendHistoryIcon, false, func() { d.historyHidden = !d.historyHidden }})
			} else {
				actions = append(actions, control{locales.OpenRequestHistory(), locales.HistoryInspectRetainedAttemptsOpening(), sendHistoryIcon, d.origin == nil && d.experimentID == 0, func() { a.openSendHistory(d) }})
			}
			// The body's format menu says how the tab you are reading is
			// read, so it comes before the pane's actions; the actions are
			// one group, two points apart, so a row of them costs what it
			// looks like; Decode is the one-off tool and yields first.
			var shown string
			showFormat := false
			if d.responseTab == resViewBody && d.response != nil {
				formats, detected := responseFormatsOf(d)
				shown = formats[d.responseView]
				if d.responseView == 0 {
					shown = cmp.Or(detected, "Pretty")
				}
				showFormat = room.fits(paneTextWidth(c, shown, uikit.TextBody, 400) + 2*t.Space(uikit.SpaceM) + 14)
			}
			actionsWidth := float32(len(actions))*uikit.HeightRow + float32(max(len(actions)-1, 0))*2
			showActions := room.fits(actionsWidth)
			showDecode := d.responseTab == resViewBody && d.response != nil &&
				room.fits(paneTextWidth(c, locales.DecodeResponseText(), uikit.TextBody, 400)+20)
			if d.responseTab == resViewBody && d.response != nil {
				if showFormat {
					uikit.Tip(c, uikit.ToolbarSelect(c, shown, func(m *ui.Menu) { actionMenu(m, a.responseFormatActions(d)) }).
						Label(locales.ResponseFormat()), locales.ResponseBodyFormat())
				} else {
					leftOut = append(leftOut, submenu(locales.ResponseFormat(), a.responseFormatActions(d)...))
				}
				if showDecode {
					if uikit.SecondaryButton(c, locales.DecodeDialog()).Label(locales.DecodeResponseText()).Clicked() {
						a.openDecoder(d.responseEditor, d.response.ResponseBody)
					}
				} else {
					leftOut = append(leftOut, do("response.decode", locales.DecodeDialog(), func() {
						a.openDecoder(d.responseEditor, d.response.ResponseBody)
					}))
				}
			}
			if d.composeWaiting {
				uikit.Hint(c, locales.Sending())
			}
			if !showActions {
				for _, action := range actions {
					leftOut = append(leftOut, do("response."+action.label, action.label, action.run).when(!action.disabled, locales.NotSentYet()))
				}
				return
			}
			ui.Row(c).Gap(2).AlignItems(ui.Center).Children(func() {
				for _, action := range actions {
					ui.Row(c).Disabled(action.disabled).Children(func() {
						if uikit.IconButton(c, action.label, action.tip, action.icon).Clicked() {
							action.run()
						}
					})
				}
			})
		}
		tab := d.responseTab
		a.responsePaneHeader(c, t, &d.responseTab, sum,
			a.responseViews(&d.responseTab, d.response != nil, checksLabel(d),
				d.response != nil || len(d.composeReplies) > 0 || len(d.console) > 0),
			paneTools{format: -1, width: width, draw: tools, overflow: func(items []action) []action {
				return append(leftOut, items...)
			}})
		// Choosing a view leaves the comparison: it belongs to the response,
		// not to a tab.
		if d.comparing && d.responseTab != tab {
			d.comparing = false
		}
		if d.experimentID != 0 && !d.composeWaiting {
			uikit.Tip(c, uikit.Meta(c, locales.RecordedRun(d.experimentID)).Padding(t.Space(uikit.SpaceXS), t.Space(uikit.SpaceL)),
				locales.RetainedResultFromThisExperiment())
		}
		if !d.composeWaiting && outcome != "" && outcome != "Response received" {
			uikit.Caption(c, outcome).TextColor(t.Warning).Padding(t.Space(uikit.SpaceXS), t.Space(uikit.SpaceL))
		}
		if d.sentSet {
			ui.Row(c).Height(uikit.HeightRow).Padding(0, t.Space(uikit.SpaceL)).Gap(t.Space(uikit.SpaceM)).AlignItems(ui.Center).Children(func() {
				path := d.sent.URL
				if u, err := url.Parse(path); err == nil && u.Host != "" {
					path = cmp.Or(u.EscapedPath(), "/")
				}
				label := locales.LastAttempt(d.sentAt.Local().Format("15:04:05"), d.sent.Method+" "+path)
				if uikit.ToolbarButton(c, locales.LastSendDetails(), locales.ShowTheFullDraftUrl(), false).Grow(1).MinWidth(0).
					Justify(ui.Start).Children(func() {
					arrow := "›"
					if d.lastAttemptOpen {
						arrow = "⌄"
					}
					uikit.Heading(c, arrow)
					uikit.Caption(c, label).SingleLine()
				}).Clicked() {
					d.lastAttemptOpen = !d.lastAttemptOpen
				}
				if !reflect.DeepEqual(d.draft(), d.sent) {
					uikit.Caption(c, locales.DraftChangedNotSent()).TextColor(t.Warning)
				} else if !d.composeWaiting {
					uikit.Tip(c, uikit.Meta(c, locales.DraftMatchesLastAttempt()),
						locales.TheDraftSnapshotMatchesVariables())
				}
			})
			if d.lastAttemptOpen {
				ui.Column(c).Padding(t.Space(uikit.SpaceM), t.Space(uikit.SpaceL)).Gap(t.Space(uikit.SpaceM)).Background(uikit.Colors(t).Window).Children(func() {
					ui.Row(c).Gap(t.Space(uikit.SpaceXS)).AlignItems(ui.Start).Children(func() {
						uikit.Body(c, d.sent.Method+" "+d.sent.URL).Label(locales.LastSentDraftUrl()).Selectable().Shrink(1)
						a.unicodeMarker(c, "last-sent-url", d.sent.URL, false)
					})
					uikit.Meta(c, locales.DraftSnapshotVariablesScriptsRedirects())
					if d.response != nil {
						uikit.Meta(c, locales.ResponseFromTime(d.response.At.Local().Format("15:04:05")))
					}
				})
			}
		}
		if outcome == "Cancelled" && (d.response != nil || len(d.composeReplies) > 0 || d.responseTab == resViewConsole) {
			ui.Column(c).Padding(t.Space(uikit.SpaceM), t.Space(uikit.SpaceL)).Gap(t.Space(uikit.SpaceM)).Children(func() { a.cancelledResult(c, t, d) })
		} else if outcome == "Network error" && d.response != nil || outcome == request.StagePostResponseScript || outcome == "Timed out" && d.response != nil {
			uikit.ErrorText(c, outcome+": "+detail).Padding(t.Space(uikit.SpaceM), t.Space(uikit.SpaceL)).Selectable()
		}
		if d.response == nil && len(d.composeReplies) == 0 && len(d.console) == 0 {
			// Nothing has come back yet: the empty state says what to do, and
			// the tabs wait for something to read.
			a.responseBody(c, t, d)
			return
		}
		if d.comparing {
			a.responseBody(c, t, d)
			a.responseProvenance(c, t, d)
			return
		}
		a.responseBody(c, t, d)
		a.responseProvenance(c, t, d)
	})
}

func (a *app) cancelledResult(c *ui.Context, t *ui.Theme, d *requestDocument) {
	uikit.Body(c, locales.WaitingCancelledTheServerMay()).TextColor(t.Text)
	ui.Collapsible(c, "Technical details", &d.sendDetails, func() {
		uikit.Meta(c, d.composeResult).Selectable().FillWidth()
	})
}

// responseProvenance says when the response arrived and whether the capture it
// came from was rewritten by sending it again.
func (a *app) responseProvenance(c *ui.Context, t *ui.Theme, d *requestDocument) {
	if d.response == nil || d.sentSet && !d.comparing {
		return
	}
	provenance := locales.ResponseFromTime(d.response.At.Local().Format("15:04:05"))
	uikit.Tip(c, uikit.Meta(c, provenance).Padding(t.Space(uikit.SpaceM), t.Space(uikit.SpaceL)),
		locales.RedirectsCookiesAndProxyRules())
}

// responseBody shows the chosen view of the last response.
func (a *app) responseBody(c *ui.Context, t *ui.Theme, d *requestDocument) {
	if d.responseTab == resViewConsole {
		runConsole(c, d.console)
		return
	}
	if d.response == nil && len(d.composeReplies) == 0 {
		ui.Scroll(c).Grow(1).MinHeight(0).FillWidth().Children(func() {
			ui.Column(c).Fill().Center().Padding(t.Space(uikit.SpaceXL)).Gap(t.Space(uikit.SpaceXS)).Children(func() {
				if d.composeWaiting {
					uikit.Spinner(c, locales.WaitingForRequestResult())
					if budget := a.sendBudget(d.draft()); budget > 0 {
						uikit.Hint(c, locales.WaitingResponseTimeout(int(budget.Seconds())))
					} else {
						uikit.Hint(c, locales.WaitingForResponseNoRequest())
					}
				} else if d.sendOutcome == "Cancelled" {
					a.cancelledResult(c, t, d)
				} else if d.sendOutcome != "" {
					uikit.Body(c, d.sendOutcome).TextColor(t.Warning)
					uikit.Body(c, d.composeResult).TextColor(t.Text).Selectable().FillWidth()
					uikit.Meta(c, locales.EditTheDraftAndSend())
				} else {
					// The response is empty because no send has run: offer
					// the next steps a person actually takes from here.
					ui.Icon(c, sendIcon).Size(uikit.IconLarge, uikit.IconLarge).TextColor(uikit.Colors(t).Faint)
					uikit.Small(c, locales.NoResponseYet()).TextColor(t.Text)
					uikit.Hint(c, locales.ResponseEmptyHint())
					nextStep := func(label string, icon *ui.SVG, run func()) {
						step := uikit.ToolbarButton(c, label, label, false).Height(uikit.HeightControlSmall).TextColor(t.TextMuted)
						step.Children(func() {
							ui.Icon(c, icon).Size(uikit.IconInline, uikit.IconInline)
							uikit.Small(c, label)
						})
						if step.Clicked() {
							run()
							c.Invalidate()
						}
					}
					ui.Column(c).Gap(t.Space(uikit.SpaceXS)).Padding(t.Space(uikit.SpaceM), 0, 0, 0).Children(func() {
						nextStep(locales.ResponseEmptySend(), sendIcon, a.sendComposed)
						nextStep(locales.ResponseEmptySendCheck(), checkIcon, func() {
							a.sendComposed()
							if doc := a.requestDocument; doc != nil {
								doc.composeTab = composeTabChecks
							}
						})
						nextStep(locales.ResponseEmptyCapture(), captureIcon, func() {
							a.openPageTab(pageLive, tabOpen{})
						})
					})
				}
				if d.composeWaiting || d.sendOutcome == "Timed out" {
					uikit.Meta(c, locales.CancellingDoesNotUndoServer())
				}
			})
		})
		return
	}
	if d.comparing {
		a.responseCompare(c, t, d)
		return
	}
	switch d.responseTab {
	case resViewBody:
		var responseFocus ui.Handle
		outline := outlineTarget{key: "draft:response", kind: outlineJSON,
			crumbs: &a.responseCrumbs, focus: func() { responseFocus.Focus(c) }}
		body := strings.Join(d.composeReplies, "\n\n")
		if len(d.composeExchanges) > 0 {
			body = webSocketTranscript(d.composeExchanges)
		}
		if d.response != nil {
			body = d.response.ResponseBody
			if d.response.ResponseBodySize > len(d.response.ResponseBody) {
				uikit.Meta(c, locales.PreviewLimitedTo8Mib()).Padding(t.Space(uikit.SpaceXS), t.Space(uikit.SpaceL))
			}
		}
		if r := d.response; r != nil && (d.responseView == 0 || d.responseView == 4) && inspect.BodyFormat(r.ResponseHeader.Get("Content-Type")) != "" {
			d.structured.view(c, a, inspectionBody{r.ResponseBody, r.ResponseBodyFile, r.ResponseBodyError, r.ResponseBodySize}, r.ResponseHeader.Get("Content-Type"))
			return
		}
		switch d.responseView {
		case 1:
			if d.responseEditor == nil {
				buf := editor.NewBuffer(body)
				d.responseEditor = &editor.State{Buffer: buf, ReadOnly: true, NoScrollPastEnd: true, SoftWrap: true, Label: locales.ResponseBody()}
			}
			a.bodyMenu(c, d.responseEditor, outline.crumbs, outline.key, jsonCrumbSource{})
			a.outlineEditor(c, d.responseEditor, outline, func() {
				editor.View(c, d.responseEditor).Bind(&responseFocus).FillWidth().Grow(1)
			})
		case 2:
			a.responseHex(c, d, body)
		default:
			if d.responseEditor == nil {
				value := jsontext.Value(body)
				isJSON := value.Indent(jsontext.WithIndent("  ")) == nil
				if isJSON {
					body = string(value)
				}
				buf := editor.NewBuffer(body)
				if isJSON {
					buf.SetLanguage(editor.LanguageNamed("json"))
				}
				d.responseEditor = &editor.State{Buffer: buf, ReadOnly: true, NoScrollPastEnd: true, Label: locales.ResponseBody()}
			}
			a.bodyMenu(c, d.responseEditor, outline.crumbs, outline.key, jsonCrumbSource{})
			d.responseEditor.Theme = uikit.EditorTheme(t)
			a.outlineEditor(c, d.responseEditor, outline, func() {
				editor.View(c, d.responseEditor).Bind(&responseFocus).FillWidth().Grow(1)
			})
		}
	case resViewContract:
		if d.response == nil {
			uikit.Hint(c, locales.NoResponseYet()).Padding(t.Space(uikit.SpaceL))
			return
		}
		a.contractView(c, t, fmt.Sprintf("send:%d:%s:%d", d.response.ID, d.response.URL, d.response.Status), a.captureExchange(d.response))
	case resViewHeaders:
		ui.Scroll(c).Grow(1).MinHeight(0).Padding(t.Space(uikit.SpaceM), t.Space(uikit.SpaceL)).Children(func() {
			if d.response != nil {
				block := request.HeaderBlock(d.response.ResponseHeader)
				ui.Row(c).Gap(t.Space(uikit.SpaceXS)).AlignItems(ui.Start).Children(func() {
					uikit.Mono(c, block).Shrink(1)
					a.unicodeMarker(c, "response-header-block", block, false)
				})
			}
		})
	case resViewCookies:
		a.responseCookies(c, t, d)
	case resViewTiming:
		ui.Column(c).Grow(1).FillWidth().Padding(t.Space(uikit.SpaceM), t.Space(uikit.SpaceL)).Gap(t.Space(uikit.SpaceM)).Children(func() {
			if d.response == nil {
				uikit.Hint(c, locales.NoResponseYet())
				return
			}
			uikit.Body(c, locales.TotalMsBytes(d.response.Millis, humanBytes(d.response.Bytes)))
			index := a.sentFlowIndex(d)
			if uikit.CompactButton(c, locales.InspectTimingInCapture()).Disabled(index < 0).Clicked() {
				if index >= 0 {
					a.inspectCapture(a.flows[index].id)
					a.syncDetail()
					a.inspect.pane, a.inspect.ready = inspectorSplit, true
					a.inspect.tab[1] = resViewTiming
				}
			}
			if index < 0 {
				uikit.Meta(c, locales.ThisSendSWireCapture())
			} else {
				a.timingView(c, a.flows[index])
			}
		})
	case resViewTests:
		if slices.ContainsFunc(d.composeTests, func(test request.TestResult) bool { return !test.Pass && !test.Skipped }) {
			ui.Row(c).Padding(t.Space(uikit.SpaceM), t.Space(uikit.SpaceM), 0).Justify(ui.End).Children(func() {
				if uikit.Tip(c, uikit.CompactButton(c, locales.RerunFailed()), locales.RerunFailedChecksTip()).Clicked() {
					a.rerunFailedChecks()
				}
			})
		}
		ui.Scroll(c).Grow(1).MinHeight(0).Padding(t.Space(uikit.SpaceM), t.Space(uikit.SpaceL)).Gap(t.Space(uikit.SpaceM)).Children(func() {
			if len(d.composeTests) == 0 {
				uikit.Hint(c, locales.NoAssertionsAddResponseChecks())
			}
			for _, result := range d.composeTests {
				testResultRow(c, result, a.openTestDiff)
			}
		})
	}
}

// rerunFailedChecks sends the request again after a failed assertion. Every
// assertion of that send is shown: one that passed before can fail now, and
// a results panel must never hide it.
func (a *app) rerunFailedChecks() {
	d := a.requestDocument
	if d == nil || d.composeWaiting {
		return
	}
	if slices.ContainsFunc(d.composeTests, func(test request.TestResult) bool { return !test.Pass && !test.Skipped }) {
		a.sendComposed()
	}
}

// testResultRow is one assertion: its mark and name, and why a failed one
// failed beneath. A failed assertion with a diff offers the comparison.
func testResultRow(c *ui.Context, result request.TestResult, showDiff func(request.TestResult)) {
	t := c.Theme()
	mark, color := "✓", t.Success
	switch {
	case result.Skipped:
		mark, color = "–", t.TextMuted
	case !result.Pass:
		mark, color = "✗", t.Danger
	}
	name := mark + " " + result.Name
	if result.Skipped {
		name = locales.SkippedNamed(name)
	}
	ui.Column(c).Gap(t.Space(uikit.SpaceHair)).Children(func() {
		ui.Row(c).Gap(t.Space(uikit.SpaceS)).AlignItems(ui.Center).Children(func() {
			uikit.Body(c, name).TextColor(color).Grow(1).MinWidth(0)
			if !result.Pass && result.HasDiff && showDiff != nil {
				if uikit.LinkButton(c, locales.ShowDiff()).Clicked() {
					showDiff(result)
				}
			}
		})
		if result.Error != "" {
			uikit.Meta(c, result.Error).Padding(0, 0, 0, t.Space(uikit.SpaceL)).Selectable()
		}
	})
}

func runConsole(c *ui.Context, entries []request.ConsoleMessage) {
	t := c.Theme()
	ui.Scroll(c).Grow(1).MinHeight(0).FillWidth().Padding(t.Space(uikit.SpaceM), t.Space(uikit.SpaceL)).Gap(t.Space(uikit.SpaceM)).Children(func() {
		if len(entries) == 0 {
			uikit.Hint(c, locales.NoOutputCallConsoleLog())
		}
		for _, entry := range entries {
			color := t.Text
			if entry.Kind == "error" {
				color = t.Danger
			}
			uikit.DataBody(c, entry.Phase+" · "+entry.Kind+" · "+entry.Text).TextColor(color).Selectable().FillWidth()
		}
	})
}

// responseCompare fills the response area with the last send beside the
// capture it came from, and puts the response tabs back when it closes.
func (a *app) responseCompare(c *ui.Context, t *ui.Theme, d *requestDocument) {
	ui.Row(c).Height(uikit.HeightRow).Padding(0, t.Space(uikit.SpaceL)).Gap(t.Space(uikit.SpaceM)).AlignItems(ui.Center).Children(func() {
		if uikit.ToolbarButton(c, locales.BackToResponse(), locales.BackToTheResponse(), false).Gap(t.Space(uikit.SpaceXS)).Children(func() {
			ui.Icon(c, backIcon).Size(uikit.IconInline, uikit.IconInline)
			uikit.Caption(c, locales.BackToResponse())
		}).Clicked() {
			d.comparing = false
		}
		uikit.Meta(c, locales.ComparingWithTheSourceCapture())
	})
	if d.source == nil || d.response == nil {
		uikit.Hint(c, locales.TheSourceCaptureIsNo()).Padding(t.Space(uikit.SpaceL))
		return
	}
	r := d.response
	right := flow{method: r.Method, path: r.URL, url: r.URL, status: r.Status, millis: r.Millis,
		reqBody: r.RequestBody, reqSize: r.RequestBodySize, reqHeaders: request.HeaderBlock(r.RequestHeader),
		resBody: r.ResponseBody, resSize: r.ResponseBodySize, resHeaders: request.HeaderBlock(r.ResponseHeader)}
	if r.RequestBodySize > len(r.RequestBody) {
		right.reqError = locales.PreviewLimitedTo8Mib2()
	}
	if r.ResponseBodySize > len(r.ResponseBody) {
		right.resError = locales.PreviewLimitedTo8Mib2()
	}
	uikit.Meta(c, locales.LastSendClientRequestAnd()).Padding(t.Space(uikit.SpaceXS), t.Space(uikit.SpaceL))
	d.comparison.compare(c, *d.source, right, [2]string{fmt.Sprintf("Source #%d", d.sourceFlow), "Last send"}, &a.diffSplit, nil)
}

// responseCookies lists what the response asked the client to store.
func (a *app) responseCookies(c *ui.Context, t *ui.Theme, d *requestDocument) {
	if d.response == nil {
		uikit.Hint(c, locales.NoResponseYet()).Padding(t.Space(uikit.SpaceL))
		return
	}
	cookieView(c, d.response.ResponseHeader, true, &d.cookieReveal, nil)
}

// responseHex renders a response body as a hex dump.
func (a *app) responseHex(c *ui.Context, d *requestDocument, body string) {
	rows := hexRows(body, "")
	if len(rows) == 0 {
		uikit.Hint(c, locales.EmptyBody()).Padding(c.Theme().Space(uikit.SpaceL))
		return
	}
	ui.List(c, &d.responseList, len(rows), func(i int) {
		uikit.DataMeta(c, hexLine(body, rows[i])).TextColor(c.Theme().Text).Padding(c.Theme().Space(uikit.SpaceXS), c.Theme().Space(uikit.SpaceL)).SingleLine()
	}).Grow(1)
}

// Only a run key links this send's final wire hop, never its URL or source ID.
func (a *app) sentFlowIndex(d *requestDocument) int {
	if d.sentRun != "" {
		for i := len(a.flows) - 1; i >= 0; i-- {
			if a.flows[i].debugRun == d.sentRun && a.flows[i].kind != "connect" {
				return i
			}
		}
	}
	return -1
}

// webSocketConfig is the saved message list of a ws/wss request; an HTTP
// request keeps none.
func (d *requestDocument) webSocketConfig() request.WebSocketConfig {
	if !request.IsWebSocket(d.composeURL) {
		return request.WebSocketConfig{}
	}
	return request.WebSocketConfig{Messages: slices.Clone(d.wsMessages), WaitMS: int(d.wsWait)}
}

// webSocketTranscript shows a ws send as what was sent and heard, one block per
// message. Replies keep their order, and a message with none shows alone.
func webSocketTranscript(exchanges []request.WebSocketExchange) string {
	var b strings.Builder
	for i, exchange := range exchanges {
		if i > 0 {
			b.WriteString("\n\n")
		}
		b.WriteString("→ ")
		if encoding := exchange.Message.Encoding; encoding != "" && !strings.EqualFold(encoding, "Text") {
			b.WriteString("[" + encoding + "] ")
		}
		b.WriteString(exchange.Message.Payload)
		for _, reply := range exchange.Replies {
			b.WriteString("\n← " + reply)
		}
	}
	return b.String()
}
