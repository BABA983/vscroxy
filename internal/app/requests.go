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
	protocol          string
	grpcConfig        request.GRPCConfig
	grpcUI            *grpcDocument
	graphqlUI         *graphqlDocument
	socketIOConfig    request.SocketIOConfig
	socketIOUI        *socketIODocument
	mqttConfig        request.MQTTConfig
	mqttUI            *mqttDocument
	mcpConfig         request.MCPConfig
	mcpUI             *mcpDocument
	composeHeaders    string
	composeBody       string
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
	examples        []request.Example
	example         int
	exampleView     int
	exampleEditor   *editor.State
	composeTab      int
	scriptTab       int
	composeResult   string
	composeWaiting  bool
	sendCancel      context.CancelFunc
	sendCancelling  bool
	sendOutcome     string
	sendDetails     bool
	sentRun         string
	variablesOpen   bool
	variablesReveal bool
	// bodyEditor is the raw request body's editor. bodyFor is the text it
	// last held, so an import or a restored draft resets it while typing
	// never does. The scripts use the same reader, each with its own state.
	bodyEditor *editor.State
	bodyFor    string
	// bodyRead is how the Body view reads the draft's bytes: 0 edits them,
	// the rest are the read-only representations draftBodyFormats lists.
	bodyRead       int
	preEditor      *editor.State
	preFor         string
	postEditor     *editor.State
	postFor        string
	composeTests   []request.TestResult
	composeReplies []string
	console        []request.ConsoleMessage
	response       *capture.Flow
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
	requestPercent  float32
	// splitSeen is the share the editor last laid out, so a divider the reader
	// moved is never moved back by the app.
	splitSeen float32
	// splitTouched is set once the reader drags the divider.
	splitTouched bool
	urlInput     ui.Handle
}

// requestResult is handed back to the UI thread; workers never mutate tabs,
// editor buffers or environments while a frame is reading them.
type requestResult struct {
	document    *requestDocument
	summary     string
	outcome     string
	response    *capture.Flow
	tests       []request.TestResult
	replies     []string
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
		Method: d.composeMethod, URL: d.composeURL, Headers: d.composeHeaders,
		Protocol: d.protocol, GRPC: d.grpcConfig, SocketIO: d.socketIOConfig, MQTT: d.mqttConfig, MCP: d.mcpConfig,
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
	return cmp.Or(d.composeName, "Untitled request")
}

func (a *app) addDocument(r savedRequest, key string) {
	d := a.newDocument(r, key)
	a.documents = append(a.documents, d)
	a.requestDocument = d
	a.requestChoice = key
	a.page = pageRequest
	d.urlInput.Focus()
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
	d.headerRows, d.headerFor = slices.Clone(r.HeaderList), ""
	d.composePre, d.composePost, d.checks = r.Pre, r.Post, r.Checks
	d.description, d.settings = r.Description, r.Settings.Clone()
	effective := a.inheritance(r.Collection).InheritSettings(r.Settings)
	d.timeout, d.maxRedirects = effective.Timeout().Seconds(), float64(effective.RedirectLimit())
	d.tokenSources = slices.Clone(r.TokenSources)
	d.examples, d.example, d.exampleEditor = slices.Clone(r.Examples), 0, nil
	d.auth, d.bodyMode, d.formFields = r.Auth, cmp.Or(r.BodyMode, "Raw"), slices.Clone(r.Form)
	d.bodyFile = r.BodyFile
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

// A request pane's views, in the order its row shows them. The first four are
// tabs; the rest open from More, so a pane keeps one row of them.
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
	if r.Body != "" || r.BodyMode != "" && r.BodyMode != "Raw" {
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
	go func() {
		s.closeNow(mcpCloseTimeout)
		a.mcpClosingMu.Lock()
		delete(a.mcpClosing, s)
		a.mcpClosingMu.Unlock()
	}()
}

func (a *app) closingMCPSessions() []*mcpSession {
	a.mcpClosingMu.Lock()
	defer a.mcpClosingMu.Unlock()
	return slices.Collect(maps.Keys(a.mcpClosing))
}

func (a *app) closeDocument(d *requestDocument) {
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
		a.mcpConsent = mcpConsentState{}
	}
	a.documents = slices.Delete(a.documents, at, at+1)
	if a.requestDocument == d {
		a.requestDocument = nil
		a.requestChoice = ""
		if len(a.documents) > 0 {
			a.requestDocument = a.documents[min(at, len(a.documents)-1)]
			a.requestChoice = a.savedKey
			a.urlInput.Focus()
		} else {
			a.workbench.content.Focus()
		}
	}
}

func (a *app) requestClose(d *requestDocument) {
	if d.composeWaiting {
		return
	}
	if d.dirty() {
		a.requestDocument, a.requestChoice, a.page = d, d.savedKey, pageRequest
		a.closingDocument = d
	} else {
		a.closeDocument(d)
	}
}

// Capture and the open drafts are one cycle: Ctrl+Tab steps through the
// request documents and back to the live traffic. Workspace shortcuts stay
// independent, so Cmd/Ctrl+1–4 never unexpectedly select a draft tab.
func (a *app) cycleDocument(step int) {
	index := 0
	if !a.page.capture() {
		index = slices.Index(a.documents, a.requestDocument) + 1
	}
	index = (index + step + len(a.documents) + 1) % (len(a.documents) + 1)
	if index == 0 {
		a.page = pageLive
		if view := a.views.activeView(); view != nil {
			view.list.Handle.Focus()
		}
		return
	}
	a.requestDocument = a.documents[index-1]
	a.requestChoice, a.page = a.savedKey, pageRequest
	a.urlInput.Focus()
}

func (a *app) closeRequestModal(c *ui.Context, t *ui.Theme) {
	d := a.closingDocument
	if d == nil {
		return
	}
	open := true
	uikit.Modal(c, &open, func() {
		uikit.DialogColumn(c, uikit.DialogSmall).Children(func() {
			uikit.DialogTitle(c, locales.SaveChanges())
			uikit.Hint(c, d.title()+" has unsaved changes. Closing it without saving will discard this draft.")
			uikit.DialogFooter(c).Children(func() {
				if uikit.SecondaryButton(c, locales.Cancel()).Clicked() {
					a.closingDocument = nil
				}
				if uikit.SecondaryButton(c, locales.Discard()).Clicked() {
					a.closeDocument(d)
					a.closingDocument = nil
				}
				if uikit.PrimaryButton(c, locales.SaveClose()).Clicked() {
					a.requestDocument = d
					if a.saveComposed() {
						a.closeDocument(d)
						a.closingDocument = nil
					}
				}
			})
		})
	})
	if !open {
		a.closingDocument = nil
	}
}

// documentHeader names the one request on screen as a breadcrumb — its
// collection, then its name, which renames or moves it — with the
// environment it sends in and its actions. Other open requests are in the
// sidebar: saved ones in their collection, new ones under Unsaved.
func (a *app) documentHeader(c *ui.Context, t *ui.Theme) {
	d := a.requestDocument
	// The editor's first row is the panel's, as the capture screen's filter
	// bar is: chrome only frames the window, never a line inside the panel.
	ui.Row(c).Height(uikit.HeightLarge).Padding(0, t.Space(2)).Gap(t.Space(1)).AlignItems(ui.Center).Background(t.Background).Children(func() {
		if d != nil {
			crumb := uikit.ToolbarButton(c, locales.RenameRequest(), locales.RenameOrMoveToAnother(), false).
				Padding(0, t.Space(1)).Gap(t.Space(1)).MinWidth(0)
			crumb.Children(func() {
				if d.composeCollection != "" {
					uikit.Meta(c, strings.ReplaceAll(d.composeCollection, "/", " / ")+" /").SingleLine().MinWidth(0)
				}
				uikit.Body(c, d.title()).FontWeight(500).SingleLine().MinWidth(0)
				if d.dirty() {
					unsavedDot(c)
				}
			})
			if crumb.Clicked() {
				a.renameOpen, a.saveAfterRename = true, false
			}
		}
		ui.Spacer(c)
		if names := a.envNames(); len(names) > 0 {
			env := ui.SelectBase(c.Key("active-environment"), &a.activeEnv)
			uikit.Tip(c, env.Trigger.Label(locales.ActiveEnvironment()), locales.ActiveEnvironment()).Width(132).Height(uikit.HeightControl).
				Padding(0, t.Space(2)).Gap(t.Space(2)).Radius(uikit.RadiusControl).Border(1, t.Border).AlignItems(ui.Center).Children(func() {
				ui.Box(c).Size(6, 6).Radius(uikit.RadiusControl).Background(t.Accent)
				uikit.Body(c, cmp.Or(a.activeEnv, "No environment")).SingleLine().Grow(1)
				uikit.Glyph(c, chevronIcon)
			})
			env.Popup(func(panel ui.Element) {
				uikit.PopoverPanel(c, panel).Padding(t.Space(1))
				for _, name := range names {
					item := env.Item(name).Label(name).Height(uikit.HeightControl).Padding(0, t.Space(2)).Gap(t.Space(2)).Radius(uikit.RadiusControl).AlignItems(ui.Center)
					if item.Highlighted() {
						item.Background(t.SurfaceHover)
					}
					item.Children(func() {
						color := t.TextMuted
						if name == a.activeEnv {
							color = t.Accent
						}
						ui.Box(c).Size(6, 6).Radius(uikit.RadiusControl).Background(color)
						uikit.Body(c, name)
					})
				}
			})
		} else {
			uikit.ToolbarButton(c, locales.ManageEnvironments2(), locales.AddAnEnvironmentForRequest(), false).
				Children(func() { ui.Text(c, locales.NoEnvironment()) }).OnClick(a.openVars)
		}
		if d != nil {
			a.requestDocumentActions(c, t, d)
			// A send in flight finishes before its request closes.
			ui.Row(c).Disabled(d.composeWaiting).Children(func() {
				if uikit.IconButton(c, locales.CloseRequest(), locales.CloseThisRequestCtrlCmd(), closeIcon).Clicked() {
					a.requestClose(d)
				}
			})
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
	ui.Column(c.Key(d.id)).Fill().Children(func() {
		a.requestBar(c, t, d, nil)
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
		response := func() {
			if r := a.experiments.record(d.historyPick); r != nil {
				a.historyResponse(c, t, d, r)
				return
			}
			a.requestResponse(c, t, d)
		}
		// The same rule as the inspector's panes: side by side while there is
		// room for both, stacked in a narrow column.
		if a.sidesStacked(c) {
			ui.SplitVerticalPercent(c.Key("request-editor"), &d.requestPercent, request, response).SplitReset(a.draftShare(c)).Grow(1)
			return
		}
		ui.SplitPercent(c.Key("request-editor"), &d.requestPercent, request, response).SplitReset(a.draftShare(c)).Grow(1)
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
	uikit.Modal(c, &d.headerWarn, func() {
		uikit.DialogColumn(c, uikit.DialogSmall).Children(func() {
			uikit.DialogTitle(c, locales.BulkEditShowsCredentials())
			uikit.Hint(c, locales.TheKeyValueBlockWrites())
			uikit.Hint(c, locales.SwitchingModesDoesNotSend())
			uikit.DialogFooter(c).Children(func() {
				if uikit.SecondaryButton(c, locales.Cancel()).Clicked() {
					d.headerWarn = false
				}
				if uikit.PrimaryButton(c, locales.ShowValues()).Clicked() {
					d.headerWarn = false
					toggleHeaderMode(d)
				}
			})
		})
	})
}

// requestRenameModal edits the document's name and collection, so neither
// needs a permanent input on the breadcrumb.
func (a *app) requestRenameModal(c *ui.Context, t *ui.Theme) {
	d := a.requestDocument
	if !a.renameOpen || d == nil {
		return
	}
	uikit.Modal(c, &a.renameOpen, func() {
		uikit.DialogColumn(c, uikit.DialogSmall).Children(func() {
			title := "Rename request"
			if a.saveAfterRename {
				title = "Save to collection"
			}
			uikit.DialogTitle(c, title)
			uikit.Input(c, &d.composeName).Label(locales.Name()).Placeholder(locales.UntitledRequest()).FillWidth().
				Height(uikit.HeightControl).Padding(0, t.Space(2)).Radius(uikit.RadiusControl).FontSize(uikit.Scaled(uikit.TextBody))
			uikit.Input(c, &d.composeCollection).Label(locales.Collection()).Placeholder(locales.Collection()).FillWidth().
				Height(uikit.HeightControl).Padding(0, t.Space(2)).Radius(uikit.RadiusControl).FontSize(uikit.Scaled(uikit.TextBody))
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
	ui.Row(c.Key(d.id)).Gap(t.Space(1)).AlignItems(ui.Center).Children(func() {
		if d.sourceAPI != "" {
			if uikit.ToolbarButton(c, locales.SourceApi(), locales.OpenTheApiThisRequest(), false).Children(func() {
				ui.Text(c, locales.SourceApi()).TextColor(t.Accent)
			}).Clicked() {
				a.apiChoice, a.page = d.sourceAPI, pageAPI
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
	r := d.value()
	r.ID, r.Name = "", uniqueRequestName(a.requests, r.Name+" copy")
	a.addDocument(r, "")
}

// documentActions are an open request's, beside Save.
func (a *app) documentActions(c *ui.Context, d *requestDocument) []action {
	return []action{
		do("request.duplicate", locales.DuplicateAsNewRequest(), func() { a.duplicateDocument(d) }).keys(ui.Cmd, ui.KeyD),
		do("request.copy-curl", locales.CopyAsCurl(), func() { a.copyRequest(c, flow{}, d, "cURL") }),
		copyAsMenu(a.requestCopyActions(c, flow{}, d)),
		do("request.code", locales.GenerateCode(), a.openCode),
		do("request.history", locales.History(), func() { a.openSendHistory(d) }),
		do("collection.settings", settingsLabel(d.composeCollection), func() { a.openFolder(d.composeCollection) }).
			when(d.composeCollection != "", "Save the request to a collection first").in("context"),
		do("environment.manage", locales.ManageEnvironments(), a.openVars).in("context"),
		do("request.variables", locales.RequestVariables(), func() { d.variablesOpen = !d.variablesOpen }).in("context"),
		do("vault.open", locales.Vault(), a.openVault).in("context"),
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
			rows[i].Source = "Environment: " + cmp.Or(a.activeEnv, "none")
		}
	}
	if len(rows) == 0 && !d.variablesOpen {
		return
	}
	target := "unresolved (review variables)"
	if r.Protocol == "MCP" && r.MCP.Transport == request.MCPTransportStdio {
		target = "stdio process"
	} else if u, err := url.Parse(request.ExpandVars(d.composeURL, in.Values(vars))); err == nil && u.Hostname() != "" {
		target = u.Scheme + "://" + u.Host // Do not reveal query tokens or userinfo.
	}
	targetLabel := "Target before scripts: "
	contextLabel := "Draft context · pre-script may generate or replace values"
	if r.Protocol != "" {
		targetLabel, contextLabel = "Target: ", "Draft context · resolved from variables and Vault"
	}
	ui.Row(c).Height(uikit.HeightControl).Padding(0, t.Space(3)).Gap(t.Space(2)).AlignItems(ui.Center).Children(func() {
		if uikit.CompactButton(c, locales.VariablesUnresolved(missing)).Label(locales.RequestVariables()).Clicked() {
			d.variablesOpen = !d.variablesOpen
		}
		uikit.Meta(c, targetLabel+target).SingleLine().Grow(1)
	})
	if !d.variablesOpen {
		return
	}
	ui.Column(c).Height(160).Padding(t.Space(2), t.Space(3)).Gap(t.Space(2)).Background(uikit.Colors(t).Chrome).Children(func() {
		ui.Row(c).Gap(t.Space(2)).Children(func() {
			uikit.Meta(c, contextLabel).Grow(1)
			if uikit.CompactButton(c, locales.EditEnvironments()).Clicked() {
				a.openVars()
			}
		})
		variableRows(c, rows, &d.variablesReveal)
	})
	ui.Divider(c)
}

func variableRows(c *ui.Context, rows []request.ResolvedVariable, reveal *bool) {
	t := c.Theme()
	ui.Checkbox(c, reveal, "Reveal values (plaintext)").FontSize(uikit.Scaled(uikit.TextMeta))
	ui.Scroll(c).Grow(1).FillWidth().Gap(t.Space(2)).Children(func() {
		if len(rows) == 0 {
			uikit.Meta(c, locales.NoReferencedVariablesInThis())
		}
		for _, row := range rows {
			ui.Row(c).Gap(t.Space(2)).Children(func() {
				ui.Text(c, row.Name).Width(140).Font(uikit.FontMono).FontSize(uikit.Scaled(uikit.TextMeta))
				value, color := "••••••", t.Text
				if row.Missing {
					value, color = "Unresolved", t.Warning
				} else if *reveal {
					value = row.Value
					if value == "" {
						value = "(empty value)"
					}
				}
				ui.Text(c, value).FontSize(uikit.Scaled(uikit.TextMeta)).TextColor(color).Grow(1).Selectable()
				uikit.Meta(c, row.Source).Width(180)
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
	width := a.contentWidth(c)
	if d := a.requestDocument; d != nil && d.assetID != "" && width >= 900 {
		width -= historyWidth
	}
	return width
}

// editorPaneWidthAt is how wide one pane of the editor was laid out, from the
// frame before, or the share it expects before the first layout.
func (a *app) editorPaneWidthAt(c *ui.Context, h *ui.Handle, share float32) float32 {
	if r := h.Bounds(c); r.W > 0 {
		return r.W
	}
	if a.sidesStacked(c) {
		return a.editorWidth(c)
	}
	return a.editorWidth(c) * share / 100
}

// draftShare is where the editor's divider starts in this layout.
func (a *app) draftShare(c *ui.Context) float32 {
	if a.sidesStacked(c) {
		return draftStackedPercent
	}
	return draftPercent
}

// requestMethods are the methods the editor offers; any other is typed.
var requestMethods = []string{"GET", "POST", "PUT", "PATCH", "DELETE", "HEAD", "OPTIONS"}

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

func (a *app) requestBar(c *ui.Context, t *ui.Theme, d *requestDocument, more func()) {
	ui.Row(c).Height(48).Padding(t.Space(2), t.Space(3)).Gap(t.Space(2)).AlignItems(ui.Center).Children(func() {
		field := ui.Row(c).Grow(1).Height(uikit.HeightLarge).AlignItems(ui.Center)
		field.Disabled(d.protocol != "" && d.composeWaiting)
		uikit.FieldFace(c, field, field.FocusWithin()).Children(func() {
			if !a.page.capture() {
				proto := uikit.ToolbarButton(c, locales.RequestProtocol(), locales.RequestProtocol(), false).
					Height(uikit.HeightControl).Padding(0, t.Space(1)).Gap(t.Space(0.5)).Shrink(0).
					Disabled(d.composeWaiting || d.grpcUI != nil && d.grpcUI.loading != nil)
				proto.Children(func() {
					ui.Text(c, cmp.Or(d.protocol, "HTTP")).FontSize(uikit.Scaled(uikit.TextBody)).TextColor(t.TextMuted).SingleLine()
					ui.Icon(c, chevronIcon).Size(uikit.IconInline, uikit.IconInline).TextColor(t.TextMuted)
				})
				proto.PopoverMenuButton(func(m *ui.Menu) {
					for _, protocol := range []string{"HTTP", "gRPC", "Socket.IO", "MQTT", "MCP"} {
						if !m.Item(protocol).Radio(cmp.Or(d.protocol, "HTTP") == protocol).Chosen() {
							continue
						}
						// A completed protocol session must not drain into the next
						// protocol's job. Config stays saved, runtime state does not.
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
						if protocol == "MQTT" {
							d.ensureMQTT()
						}
					}
				})
				ui.Box(c).Width(1).Height(18).Background(t.Border)
			}
			if d.protocol == "MCP" && d.mcpConfig.Transport == request.MCPTransportStdio {
				ui.Row(c).Grow(1).Height(uikit.HeightControl).AlignItems(ui.Center).Padding(0, t.Space(2)).Children(func() {
					uikit.Meta(c, "stdio · "+cmp.Or(d.mcpConfig.Command, "local command")).Font(uikit.FontMono).SingleLine()
				})
				return
			}
			// An editable dropdown: a click lists the common methods, and a method
			// the list lacks is typed and kept as it is. The chevron and the hover
			// background say it is a control, not a label.
			control := ui.Row(c).Gap(t.Space(0.5)).Padding(0, t.Space(1)).AlignItems(ui.Center).Hidden(d.protocol != "")
			if control.Hovered() {
				control.Background(t.SurfaceHover)
			}
			control.Children(func() {
				method := ui.ComboboxBase(c, &d.composeMethod)
				method.Input.Width(72).Height(uikit.HeightControl).Padding(0, t.Space(1)).Label(locales.Method()).FontSize(uikit.Scaled(uikit.TextBody)).FontWeight(500).
					TextColor(uikit.MethodColor(d.composeMethod, t)).Border(0, t.Border)
				if method.Input.Changed() {
					d.composeMethod = strings.ToUpper(strings.TrimSpace(d.composeMethod))
				}
				method.Popup(func(panel ui.Element) {
					uikit.PopoverPanel(c, panel).Padding(t.Space(1))
					query := strings.ToUpper(strings.TrimSpace(d.composeMethod))
					for _, m := range requestMethods {
						if method.Filtering() && query != "" && !strings.HasPrefix(m, query) {
							continue
						}
						item := method.Item(m).Height(uikit.HeightControl).Padding(0, t.Space(2)).Radius(uikit.RadiusControl).AlignItems(ui.Center)
						if item.Highlighted() {
							item.Background(t.SurfaceHover).TextColor(t.Text)
						}
						item.Children(func() { ui.Text(c, m).FontSize(uikit.Scaled(uikit.TextBody)).TextColor(uikit.MethodColor(m, t)) })
					}
				})
				if chosen, ok := method.Chosen(); ok {
					d.composeMethod = chosen
				}
				if uikit.IconButton(c, locales.MethodList(), locales.CommonMethods(), chevronIcon).Clicked() {
					method.SetOpen(!method.Open())
					method.Input.Focus()
				}
			})
			ui.Box(c).Width(1).Height(18).Background(t.Border)
			placeholder := "https://api.example.com/v1/resource"
			if d.protocol == "MQTT" {
				placeholder = "mqtts://broker.example.com:8883"
			}
			if d.protocol == "Socket.IO" {
				placeholder = "https://host/socket.io"
			}
			input := ui.TextInputBase(c, &d.composeURL).Bind(&d.urlInput).Grow(1).Height(uikit.HeightControl).Padding(0, t.Space(2)).Label(locales.Url()).
				Placeholder(placeholder).Font(uikit.FontMono).FontSize(uikit.Scaled(uikit.TextBody))
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
			a.variableTooltip(c, input, marks, false)
			a.syncVariableCompletion(input, "URL", &d.composeURL)
			a.variablePopover(c, input, "URL", &d.composeURL, set)
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
			a.primaryActionButton(c, "request.send", sendLabel).Width(sendWidth)
		}
		if d.composeWaiting {
			label := "Cancel"
			if d.protocol == "Socket.IO" || d.protocol == "MQTT" || d.protocol == "MCP" {
				label = "Disconnect"
			}
			if d.sendCancelling && !narrow {
				label = "Cancelling…"
			}
			a.actionButton(c, "request.cancel-send", label).Label(locales.CancelSend()).Width(cancelWidth).
				Tooltip(locales.StopWaitingCancellingDoesNot())
		}
		if more != nil {
			more()
		}
	})
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
	// The body tools share the header only when the pane has room for them;
	// side by side with the response it takes its own row.
	width := a.editorPaneWidthAt(c, &a.editorReqPane, draftPercent)
	wide := width >= 720
	bodyTools := func() {
		uikit.SelectLabels(c, &d.bodyMode, bodyTypes, optionLabel).Label(locales.RequestBodyType()).Width(132).FontSize(uikit.Scaled(uikit.TextMeta))
		// Reading the bytes is a view of the same draft: the formats a
		// capture's readers used to offer, without leaving the Body tab.
		formats := a.draftBodyFormats(d)
		shown := formats[min(max(d.bodyRead, 0), len(formats)-1)]
		ui.Row(c).Disabled(d.bodyMode != "Raw").Children(func() {
			uikit.Tip(c, uikit.MenuSelect(c, shown, func(m *ui.Menu) {
				for i, name := range formats {
					if m.Item(name).Checked(i == d.bodyRead).Chosen() {
						d.bodyRead = i
					}
				}
			}).Label(locales.ReadBodyAs()), locales.ReadBodyAsHelp()).FontSize(uikit.Scaled(uikit.TextMeta))
		})
		text, seen := &d.composeBody, &d.bodyFor
		formatLabel := locales.FormatBody()
		formatHelp := locales.FormatBodyHelp()
		canFormat := d.bodyMode == "Raw"
		if d.bodyMode == "GraphQL" && d.graphqlTab == 1 {
			text, seen = &d.graphql.Variables, &d.varsFor
			formatLabel, canFormat = locales.FormatGraphqlVariables(), true
		}
		formatted, isJSON := indentJSON(*text)
		query := d.bodyMode == "GraphQL" && d.graphqlTab == 0
		if query {
			formatLabel, formatHelp = "Format GraphQL query", "Format the GraphQL document without expanding variables or changing its operations"
		}
		ui.Row(c).Disabled(!query && (!isJSON || !canFormat)).Children(func() {
			if uikit.ToolbarButton(c, formatLabel, formatHelp, false).Children(func() {
				ui.Icon(c, formatIcon).Size(uikit.IconInline, uikit.IconInline)
				ui.Text(c, locales.Format()).FontSize(uikit.Scaled(uikit.TextMeta))
			}).Clicked() {
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
	ui.Column(c).Fill().Bind(&a.editorReqPane).Children(func() {
		a.requestPaneHeader(c, t, selected, d, f, paneTools{format: -1, width: width, draw: func(*paneRoom) {
			// Beside a capture's response the editor has half the width: its
			// body tools take their own row there.
			if wide && *selected == composeTabBody {
				bodyTools()
			}
		}})
		if !wide && *selected == composeTabBody {
			ui.Row(c).Height(uikit.HeightLarge).Padding(0, t.Space(3)).Gap(t.Space(2)).AlignItems(ui.Center).Children(func() {
				ui.Spacer(c)
				bodyTools()
			})
		}
		ui.Column(c).Grow(1).Padding(t.Space(2), t.Space(3)).Gap(t.Space(2)).Children(func() {
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
				ui.Row(c).Gap(t.Space(2)).AlignItems(ui.Center).Children(func() {
					uikit.Segmented(c, &d.scriptTab, locales.PreRequest(), locales.PostResponse())
					ui.Spacer(c)
					if st := map[bool]*editor.State{false: d.preEditor, true: d.postEditor}[d.scriptTab == 1]; st != nil {
						snippetMenu(c, st, "request")
					}
				})
				a.requestScriptText(c, d)
			case composeTabChecks:
				a.checksEditor(c, d, t)
			case composeTabExamples:
				a.examplesEditor(c, d, t)
			case composeTabDocs:
				a.requestDocsEditor(c, d, t)
			case composeTabSettings:
				a.requestSettingsEditor(c, d, t)
			case composeTabOriginal:
				a.headersSide(c, f, 1)
			case composeTabCookies:
				cookieView(c, f.reqHeader, false, &a.inspect.reveal[1])
			}
		})
		// A visible response already names the last attempt and draft changes.
		if d.composeWaiting || d.sentSet && d.response == nil {
			ui.Row(c).Height(uikit.HeightControl).Padding(0, t.Space(3)).AlignItems(ui.Center).Children(func() {
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
		uikit.Meta(c, "Draft matches last attempt · "+d.sentAt.Local().Format("15:04:05")).
			Tooltip(locales.TheDraftSnapshotMatchesVariables())
	default:
		ui.Text(c, locales.EditedSinceLastSendNot()).FontSize(uikit.Scaled(uikit.TextMeta)).TextColor(t.Warning).
			Tooltip(locales.TheResponseBelowBelongsTo())
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
		sum := responseSummary{dot: uikit.Colors(t).TextMuted}
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
			// Without a retained send there is nothing to read, so the entry
			// waits rather than opening an empty list.
			actions = append(actions, control{locales.OpenRequestHistory(), locales.HistoryInspectRetainedAttemptsOpening(), sendHistoryIcon, d.origin == nil && d.experimentID == 0, func() { a.openSendHistory(d) }})
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
				showFormat = room.fits(paneTextWidth(c, shown, uikit.TextBody, 400) + 2*t.Space(2) + 14)
			}
			actionsWidth := float32(len(actions))*uikit.HeightControl + float32(max(len(actions)-1, 0))*2
			showActions := room.fits(actionsWidth)
			showDecode := d.responseTab == resViewBody && d.response != nil &&
				room.fits(paneTextWidth(c, locales.DecodeResponseText(), uikit.TextBody, 400)+20)
			if d.responseTab == resViewBody && d.response != nil {
				if showFormat {
					uikit.Tip(c, uikit.MenuSelect(c, shown, func(m *ui.Menu) { actionMenu(m, a.responseFormatActions(d)) }).
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
			uikit.Meta(c, locales.RecordedRun(d.experimentID)).Padding(t.Space(1), t.Space(3)).
				Tooltip(locales.RetainedResultFromThisExperiment())
		}
		if !d.composeWaiting && outcome != "" && outcome != "Response received" {
			ui.Text(c, outcome).FontSize(uikit.Scaled(uikit.TextMeta)).TextColor(t.Warning).Padding(t.Space(1), t.Space(3))
		}
		if d.sentSet {
			ui.Row(c).Height(uikit.HeightControl).Padding(0, t.Space(3)).Gap(t.Space(2)).AlignItems(ui.Center).Children(func() {
				path := d.sent.URL
				if u, err := url.Parse(path); err == nil && u.Host != "" {
					path = cmp.Or(u.EscapedPath(), "/")
				}
				label := "Last attempt · " + d.sentAt.Local().Format("15:04:05") + " · " + d.sent.Method + " " + path
				if uikit.ToolbarButton(c, locales.LastSendDetails(), locales.ShowTheFullDraftUrl(), false).Grow(1).MinWidth(0).
					Justify(ui.Start).Children(func() {
					arrow := "›"
					if d.lastAttemptOpen {
						arrow = "⌄"
					}
					ui.Text(c, arrow).FontSize(uikit.Scaled(uikit.TextTitle))
					ui.Text(c, label).FontSize(uikit.Scaled(uikit.TextMeta)).SingleLine()
				}).Clicked() {
					d.lastAttemptOpen = !d.lastAttemptOpen
				}
				if !reflect.DeepEqual(d.draft(), d.sent) {
					ui.Text(c, locales.DraftChangedNotSent()).FontSize(uikit.Scaled(uikit.TextMeta)).TextColor(t.Warning)
				} else if !d.composeWaiting {
					uikit.Meta(c, locales.DraftMatchesLastAttempt()).
						Tooltip(locales.TheDraftSnapshotMatchesVariables())
				}
			})
			if d.lastAttemptOpen {
				ui.Column(c).Padding(t.Space(2), t.Space(3)).Gap(t.Space(2)).Background(uikit.Colors(t).Chrome).Children(func() {
					uikit.Body(c, d.sent.Method+" "+d.sent.URL).Label(locales.LastSentDraftUrl()).Selectable()
					uikit.Meta(c, locales.DraftSnapshotVariablesScriptsRedirects())
					if d.response != nil {
						uikit.Meta(c, "Response from "+d.response.At.Local().Format("15:04:05"))
					}
				})
			}
		}
		if outcome == "Cancelled" && (d.response != nil || len(d.composeReplies) > 0 || d.responseTab == resViewConsole) {
			ui.Column(c).Padding(t.Space(2), t.Space(3)).Gap(t.Space(2)).Children(func() { a.cancelledResult(c, t, d) })
		} else if outcome == "Network error" && d.response != nil || outcome == "Post-response script failed" || outcome == "Timed out" && d.response != nil {
			ui.Text(c, outcome+": "+detail).FontSize(uikit.Scaled(uikit.TextMeta)).TextColor(t.Danger).Padding(t.Space(2), t.Space(3)).Selectable()
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
	ui.Text(c, locales.WaitingCancelledTheServerMay()).FontSize(uikit.Scaled(uikit.TextBody)).TextColor(t.Text)
	ui.Collapsible(c, "Technical details", &d.sendDetails, func() {
		uikit.Meta(c, d.composeResult).Selectable().FillWidth()
	}).FontSize(uikit.Scaled(uikit.TextBody))
}

// responseProvenance says when the response arrived and whether the capture it
// came from was rewritten by sending it again.
func (a *app) responseProvenance(c *ui.Context, t *ui.Theme, d *requestDocument) {
	if d.response == nil || d.sentSet && !d.comparing {
		return
	}
	provenance := "Response from " + d.response.At.Local().Format("15:04:05")
	uikit.Meta(c, provenance).Padding(t.Space(2), t.Space(3)).
		Tooltip(locales.RedirectsCookiesAndProxyRules())
}

// responseBody shows the chosen view of the last response.
func (a *app) responseBody(c *ui.Context, t *ui.Theme, d *requestDocument) {
	if d.responseTab == resViewConsole {
		runConsole(c, d.console)
		return
	}
	if d.response == nil && len(d.composeReplies) == 0 {
		ui.Scroll(c).Grow(1).FillWidth().Children(func() {
			ui.Column(c).Fill().Center().Padding(t.Space(4)).Gap(t.Space(1)).Children(func() {
				if d.composeWaiting {
					ui.Spinner(c).Label(locales.WaitingForRequestResult())
					if budget := a.sendBudget(d.draft()); budget > 0 {
						uikit.Hint(c, locales.WaitingResponseTimeout(int(budget.Seconds())))
					} else {
						uikit.Hint(c, locales.WaitingForResponseNoRequest())
					}
				} else if d.sendOutcome == "Cancelled" {
					a.cancelledResult(c, t, d)
				} else if d.sendOutcome != "" {
					ui.Text(c, d.sendOutcome).FontSize(uikit.Scaled(uikit.TextBody)).TextColor(t.Warning)
					ui.Text(c, d.composeResult).FontSize(uikit.Scaled(uikit.TextBody)).TextColor(t.Text).Selectable().FillWidth()
					uikit.Meta(c, locales.EditTheDraftAndSend())
				} else {
					// What a response needs is the key that sends for it,
					// not a sentence about what a send would do.
					ui.Icon(c, sendIcon).Size(uikit.IconLarge, uikit.IconLarge).TextColor(uikit.Colors(t).TextFaint)
					uikit.Hint(c, sendShortcut()+"  "+locales.Send())
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
		body := strings.Join(d.composeReplies, "\n\n")
		if d.response != nil {
			body = d.response.ResponseBody
			if d.response.ResponseBodySize > len(d.response.ResponseBody) {
				uikit.Meta(c, locales.PreviewLimitedTo8Mib()).Padding(t.Space(1), t.Space(3))
			}
		}
		if r := d.response; r != nil && (d.responseView == 0 || d.responseView == 4) && inspect.BodyFormat(r.ResponseHeader.Get("Content-Type")) != "" {
			d.structured.view(c, inspectionBody{r.ResponseBody, r.ResponseBodyFile, r.ResponseBodyError, r.ResponseBodySize}, r.ResponseHeader.Get("Content-Type"))
			return
		}
		switch d.responseView {
		case 1:
			if d.responseEditor == nil {
				buf := editor.NewBuffer(body)
				d.responseEditor = &editor.State{Buffer: buf, ReadOnly: true, NoScrollPastEnd: true, SoftWrap: true, Label: locales.ResponseBody()}
			}
			a.decoderMenu(d.responseEditor)
			editor.View(c, d.responseEditor).Grow(1)
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
			a.decoderMenu(d.responseEditor)
			d.responseEditor.Theme = uikit.EditorTheme(t)
			editor.View(c, d.responseEditor).Grow(1)
		}
	case resViewHeaders:
		ui.Scroll(c).Grow(1).Padding(t.Space(2), t.Space(3)).Children(func() {
			if d.response != nil {
				uikit.Mono(c, request.HeaderBlock(d.response.ResponseHeader))
			}
		})
	case resViewCookies:
		a.responseCookies(c, t, d)
	case resViewTiming:
		ui.Column(c).Grow(1).FillWidth().Padding(t.Space(2), t.Space(3)).Gap(t.Space(2)).Children(func() {
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
		ui.Scroll(c).Grow(1).Padding(t.Space(2), t.Space(3)).Gap(t.Space(2)).Children(func() {
			if len(d.composeTests) == 0 {
				uikit.Hint(c, locales.NoAssertionsAddResponseChecks())
			}
			for _, result := range d.composeTests {
				testResultRow(c, result)
			}
		})
	}
}

// testResultRow is one assertion: its mark and name, and why a failed one
// failed beneath.
func testResultRow(c *ui.Context, result request.TestResult) {
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
		name += " · skipped"
	}
	ui.Column(c).Gap(t.Space(0.5)).Children(func() {
		ui.Text(c, name).FontSize(uikit.Scaled(uikit.TextBody)).TextColor(color)
		if result.Error != "" {
			uikit.Meta(c, result.Error).Padding(0, 0, 0, t.Space(3)).Selectable()
		}
	})
}

func runConsole(c *ui.Context, entries []request.ConsoleMessage) {
	t := c.Theme()
	ui.Scroll(c).Grow(1).FillWidth().Padding(t.Space(2), t.Space(3)).Gap(t.Space(2)).Children(func() {
		if len(entries) == 0 {
			uikit.Hint(c, locales.NoOutputCallConsoleLog())
		}
		for _, entry := range entries {
			color := t.Text
			if entry.Kind == "error" {
				color = t.Danger
			}
			ui.Text(c, entry.Phase+" · "+entry.Kind+" · "+entry.Text).Font(uikit.FontMono).FontSize(uikit.Scaled(uikit.TextBody)).TextColor(color).Selectable().FillWidth()
		}
	})
}

// responseCompare fills the response area with the last send beside the
// capture it came from, and puts the response tabs back when it closes.
func (a *app) responseCompare(c *ui.Context, t *ui.Theme, d *requestDocument) {
	ui.Row(c).Height(uikit.HeightControl).Padding(0, t.Space(3)).Gap(t.Space(2)).AlignItems(ui.Center).Children(func() {
		if uikit.ToolbarButton(c, locales.BackToResponse(), locales.BackToTheResponse(), false).Gap(t.Space(1)).Children(func() {
			ui.Icon(c, backIcon).Size(uikit.IconInline, uikit.IconInline)
			ui.Text(c, locales.BackToResponse()).FontSize(uikit.Scaled(uikit.TextMeta))
		}).Clicked() {
			d.comparing = false
		}
		uikit.Meta(c, locales.ComparingWithTheSourceCapture())
	})
	if d.source == nil || d.response == nil {
		uikit.Hint(c, locales.TheSourceCaptureIsNo()).Padding(t.Space(3))
		return
	}
	r := d.response
	right := flow{method: r.Method, path: r.URL, url: r.URL, status: r.Status, millis: r.Millis,
		reqBody: r.RequestBody, reqSize: r.RequestBodySize, reqHeaders: request.HeaderBlock(r.RequestHeader),
		resBody: r.ResponseBody, resSize: r.ResponseBodySize, resHeaders: request.HeaderBlock(r.ResponseHeader)}
	if r.RequestBodySize > len(r.RequestBody) {
		right.reqError = "preview limited to 8 MiB"
	}
	if r.ResponseBodySize > len(r.ResponseBody) {
		right.resError = "preview limited to 8 MiB"
	}
	uikit.Meta(c, locales.LastSendClientRequestAnd()).Padding(t.Space(1), t.Space(3))
	d.comparison.compare(c, *d.source, right, [2]string{fmt.Sprintf("Source #%d", d.sourceFlow), "Last send"}, &a.diffSplit)
}

// responseCookies lists what the response asked the client to store.
func (a *app) responseCookies(c *ui.Context, t *ui.Theme, d *requestDocument) {
	if d.response == nil {
		uikit.Hint(c, locales.NoResponseYet()).Padding(t.Space(3))
		return
	}
	cookieView(c, d.response.ResponseHeader, true, &d.cookieReveal)
}

// responseHex renders a response body as a hex dump.
func (a *app) responseHex(c *ui.Context, d *requestDocument, body string) {
	rows := hexRows(body, "")
	if len(rows) == 0 {
		uikit.Hint(c, locales.EmptyBody()).Padding(c.Theme().Space(3))
		return
	}
	ui.List(c, &d.responseList, len(rows), func(i int) {
		ui.Text(c, hexLine(body, rows[i])).FontSize(uikit.Scaled(uikit.TextMeta)).Font(uikit.FontMono).TextColor(c.Theme().Text).Padding(c.Theme().Space(1), c.Theme().Space(3)).SingleLine()
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
