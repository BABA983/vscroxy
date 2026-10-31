package app

import (
	"cmp"
	"context"
	"encoding/json/v2"
	"fmt"
	"github.com/baba983/vscroxy/internal/uikit"
	"hash/fnv"
	"log"
	"maps"
	"net"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"
	"uuid"

	"github.com/baba983/vscroxy/internal/appicon"
	"github.com/baba983/vscroxy/internal/capture"
	"github.com/baba983/vscroxy/internal/request"
	"github.com/baba983/vscroxy/internal/system"
	"github.com/egoist/mygo"
	"github.com/egoist/mygo/ui"
	"github.com/egoist/mygo/ui/editor"
	"github.com/egoist/mygo/ui/richtext"
	"github.com/oneo-me/onevra/source/icons/lucide"
	"golang.org/x/net/http/httpguts"
)

// maxFlows bounds what the UI keeps; older flows are dropped from the front.
const maxFlows = 5000

// app is the state the window shows; view reads it and the controls write it.
type app struct {
	flows   []flow
	visible []int
	domains []domain
	clients []domain

	query     string
	filter    string
	chip      int
	search    ui.Handle
	clearOpen bool

	// Capture filters and the request library have independent selections.
	// Each workspace has two sections, chosen in its sidebar: live traffic or
	// an investigation, and request documents or API contracts.
	workspace       int
	captureSection  int
	requestsSection int
	sidebarChoice   string
	requestChoice   string
	requestQuery    string
	renameOpen      bool
	settingsOpen    bool
	settingsTab     int
	curlOpen        bool
	curlInput       string
	curlProblem     string
	deviceUI        deviceSetup
	clientUI        clientSetup
	diagnostics     captureDiagnostics

	apis            []*apiDocument
	apisPath        string
	apiChoice       string
	apiGenerateOpen bool
	apiCollection   string

	// chosen are the rows chosen for a bulk action, by flow id.
	chosen ui.Selection[int]
	// picked bridges the table's row-index API; pickedID survives row rebuilds.
	picked   int
	pickedID int
	tab      int

	// sort is the column the table is sorted by, and sortApplied what the
	// rows were sorted with, so a header click sorts on the next frame.
	sort        ui.SortOrder
	sortApplied ui.SortOrder

	sidebarWidth float32
	tablePercent float32
	workbench    desktopWorkbench

	totalBytes int64

	trafficOpen bool
	domainsOpen bool
	clientsOpen bool
	// collectionsOpen keeps the sidebar's COLLECTIONS group open or shut.
	collectionsOpen bool
	// revealGroup scrolls the sidebar to a group in the next build: "live",
	// "collections" or "sessions".
	revealGroup string
	// collectionOpen keeps each saved collection's section open or shut,
	// by name, the empty name for the requests outside one.
	collectionOpen map[string]*bool

	// icons caches the app icon per process name; a nil entry means the
	// process has none.
	icons map[string]ui.ImageSource

	// detailFor is the flow the detail panes were built from, so the text and
	// the diff buffers are rebuilt only when the choice changes.
	detailFor    int
	body         string
	reqBody      string
	diff         *editor.DiffState
	diffBaseline bool
	analysis     captureAnalysis
	replayUI     replaySetup
	bulkSaveUI   collectionSave // saves the chosen rows into a collection
	frames       map[int][]frame
	messageUI    messagePanel
	frameLost    atomic.Int64

	// inspect is the inspector's own view, so the request and the response
	// keep independent tabs and body representations while both are on
	// screen, and the exchange-wide views stay one click away.
	inspect inspectorState

	// The body panes are indexed the way the rest of the inspector is: 0 is
	// the response, 1 the request. Readers survive leaving Capture to edit or
	// compare a request.
	bodyMode     [2]int
	bodyQuery    [2]string
	headerScroll [2]ui.ScrollState
	bodyText     [2]*editor.State
	bodyReader   [2]ui.Handle
	bodyNotice   [2]string
	jsonScroll   [2]ui.ListState
	hexScroll    [2]ui.ListState
	// The wire text and its reader are built once per exchange, not per frame.
	rawText   [2]string
	rawEditor [2]*editor.State

	// The parsed and decoded previews are cached per side, so two visible
	// panes do not rebuild each other's tree on every frame.
	htmlDoc      [2]*richtext.Document
	htmlFor      [2]string
	imageBmp     [2]*ui.Bitmap
	imageFor     [2]string
	imageErr     [2]string
	jsonRoots    [2][]*jsonNode
	jsonExpanded [2]map[string]bool
	jsonFor      [2]string
	jsonOK       [2]bool
	protoFor     [2]string
	protoOK      [2]bool
	protobuf     protoInspector

	// newRows counts traffic that arrived while the list was paused, so the
	// footer can offer to resume following without moving the reader.
	newRows int

	srv         *capture.Server
	win         *mygo.Window
	caPath      string
	certificate certificateSetup
	systemProxy proxySetup
	rulesPath   string
	scriptPath  string
	rules       []capture.Rule
	ruleUI      ruleEditor
	throttle    capture.Throttle
	upstream    string
	errText     string
	dropped     int64
	scriptErrs  int64

	upstreamOpen bool
	upstreamAddr string

	// ssl keeps the active domain/executable rules and an isolated editor
	// draft. Cancelling the editor never changes active inspection.
	sslPath       string
	sslAll        bool
	sslHosts      string
	sslApps       []capture.SSLApp
	sslOpen       bool
	sslDraft      *capture.SSLConfig
	sslDraftHosts string

	// dark draws the window in the dark theme.
	dark bool

	// settings are what the window keeps between launches, besides its
	// bounds, which MyGo saves with the window's StateKey.
	settingsPath     string
	saved            settings
	lastSave         time.Time
	columns          ui.TableLayout
	proxyPort        int
	proxyPortInput   string
	proxyPortProblem string

	// ignore keeps noise out of the table without stopping it at the proxy.
	ignorePath string
	ignoreText string
	ignoreOpen bool

	// dns maps names to addresses the proxy dials instead, so a device's
	// traffic reaches another machine as if the name pointed there.
	dnsPath string
	dnsText string
	dnsOpen bool

	// dnsRule is the dialog a flow opens to map its host: the app that sent
	// it is known there, so nobody has to spell an app's name.
	dnsRuleOpen  bool
	dnsRuleHost  string
	dnsRuleAddr  string
	dnsRuleScope string
	dnsRuleApps  []string

	// keyLogPath is the file the TLS secrets go to, which Wireshark reads.
	keyLogPath  string
	keyLogOpen  bool
	keyLogInput string

	// Connection overlays belong to the inspected request, not the list.
	connOpen    bool
	explainOpen bool

	// customColumns are the columns the user added to the table and
	// hiddenColumns the built-in ones they took out; the filter fields narrow
	// what it shows beyond the search box.
	customColumns []customColumn
	hiddenColumns []string
	filterStatus  string
	filterHeader  string
	filterBody    string
	filterTriage  captureTriage
	views         captureViews
	columnOpen    bool
	columnDraft   customColumn

	// mark edits what a flow is marked with: a color and a note.
	markOpen  bool
	markID    int
	markColor string
	markNote  string

	// ignoreApps does the same by app, for a browser's background traffic.
	ignoreAppsPath string
	ignoreAppsText string
	ignoreAppsOpen bool

	// requests are the saved requests, the Compose direction's kept half,
	// and history what Compose sent.
	requests        []savedRequest
	library         libraryState
	requestsProblem string
	// folders are the collections' and folders' own settings, by path.
	folders        []request.Folder
	folderUI       folderEditor
	deleting       deletion
	code           codeDialog
	vault          vaultState
	history        []historyEntry
	historyPath    string
	historyOpen    bool
	experiments    experimentWorkbench
	socket         websocketWorkbench
	investigations investigationWorkbench
	board          investigationBoard

	// The active document is embedded so the editor's fields always belong
	// to that tab. Workers return results to their originating document.
	*requestDocument
	documents       []*requestDocument
	nextDocument    int
	closingDocument *requestDocument
	requestResults  []requestResult
	importReport    importReport
	// tokens are the OAuth 2.0 access tokens of this session, never saved.
	tokens       request.Tokens
	oauth        oauthState
	runner       collectionRunner
	cookies      *cookieStore
	cookieUI     cookieEditor
	regressionUI regressionShare

	// envs are the variables a request uses as {{name}}, one set in use.
	envsPath    string
	envsProblem string
	envs        []environment
	activeEnv   string
	globals     map[string]string
	varsGlobals bool // the Variables dialog edits the globals
	varsOpen    bool
	varsName    string
	varsChoice  string
	varsRows    []request.Field
	varsProblem string

	breakOn   bool
	bp        *capture.Breakpoint
	bpOpen    bool
	bpMethod  string
	bpURL     string
	bpStatus  string
	bpBody    string
	bpHeaders string
	bpAbort   string

	// mu guards what the capture goroutine hands to the view: the flows not
	// yet drained, and a startup error.
	mu             sync.Mutex
	pending        []capture.Flow
	pendingFrames  []capture.FrameEvent
	bps            []capture.Breakpoint
	err            error
	message        string
	messagePending bool
	replayResults  []replayResult
}

type replayResult struct {
	source flow
	reply  capture.Flow
}

type domain struct {
	name string
	n    int
}

func newApp() *app {
	a := &app{
		filter:          "all",
		sidebarChoice:   "all",
		icons:           make(map[string]ui.ImageSource),
		sidebarWidth:    220,
		tablePercent:    44,
		trafficOpen:     true,
		domainsOpen:     true,
		clientsOpen:     true,
		collectionsOpen: true,
		historyOpen:     true,
		collectionOpen:  make(map[string]*bool),
		detailFor:       -1,
		frames:          make(map[int][]frame),
		throttle:        capture.Presets[0],
		workbench:       desktopWorkbench{layout: "right"},
	}
	// The engine reads SSLKEYLOGFILE on its first handshake, so a key log the
	// settings ask for has to be in the environment before it starts.
	if home, err := os.UserHomeDir(); err == nil {
		a.settingsPath = filepath.Join(home, ".vscroxy", "settings.json")
		a.caPath = filepath.Join(home, ".mitmproxy", "mitmproxy-ca-cert.pem")
		a.rulesPath = filepath.Join(home, ".vscroxy", "rules.json")
		a.sslPath = filepath.Join(home, ".vscroxy", "ssl.json")
		a.library.root = filepath.Join(home, ".vscroxy", "collections")
		a.historyPath = filepath.Join(home, ".vscroxy", "history.json")
		a.ignorePath = filepath.Join(home, ".vscroxy", "ignore.txt")
		a.ignoreAppsPath = filepath.Join(home, ".vscroxy", "ignore-apps.txt")
		a.dnsPath = filepath.Join(home, ".vscroxy", "dns.txt")
		a.scriptPath = filepath.Join(home, ".vscroxy", "script.js")
		a.envsPath = filepath.Join(home, ".vscroxy", "env.json")
		a.vault.path = filepath.Join(home, ".vscroxy", "vault.json")
		a.vault.keyPath = filepath.Join(home, ".vscroxy", "vault.key")
		if saved, err := loadSettings(a.settingsPath); err != nil {
			a.errText = err.Error()
		} else {
			a.saved, a.proxyPort, a.vault.scripts = saved, saved.ProxyPort, saved.VaultScripts
			if saved.ProxyPort != 0 {
				a.proxyPortInput = strconv.Itoa(saved.ProxyPort)
			}
			if saved.KeyLogPath != "" {
				_ = os.Setenv("SSLKEYLOGFILE", saved.KeyLogPath)
				a.keyLogPath = saved.KeyLogPath
			}
		}
	}
	addr, err := a.saved.proxyAddr()
	var srv *capture.Server
	if err == nil {
		srv, err = capture.New(addr, "")
	}
	if err != nil {
		// Keep local assets usable, but never present examples as captured data.
		a.errText = err.Error()
	} else {
		a.srv = srv
		a.reloadConfig()
	}
	a.loadRequestAssets()
	if a.historyPath != "" {
		if list, err := loadHistory(a.historyPath); err == nil {
			a.history = list
		}
	}
	if a.settingsPath != "" {
		saved := a.saved
		a.dark = saved.Dark
		if saved.SidebarWidth > 0 {
			a.sidebarWidth = saved.SidebarWidth
		}
		if saved.TablePercent > 0 {
			a.tablePercent = saved.TablePercent
		}
		a.workbench.layout = inspectorLayout(saved.InspectorLayout)
		a.workbench.sidebarHidden = saved.SidebarHidden
		if saved.SideTablePercent > 0 {
			a.workbench.sidePercent = saved.SideTablePercent
		}
		a.columns = saved.Columns.layout()
		a.customColumns, a.hiddenColumns = saved.customColumns(), saved.HiddenColumns
	}
	if home, err := os.UserHomeDir(); err == nil {
		a.apisPath = filepath.Join(home, ".vscroxy", "apis.json")
		if specs, err := loadAPIs(a.apisPath); err != nil {
			a.setMessage("APIs: " + err.Error())
		} else {
			for _, spec := range specs {
				a.addAPI(spec, true)
			}
		}
	}
	a.countDomains()
	a.countClients()
	a.applyFilter()
	if home, err := os.UserHomeDir(); err == nil {
		a.views.init(a, filepath.Join(home, ".vscroxy", "views.json"))
		a.protobuf.init(filepath.Join(home, ".vscroxy", "protobuf.pb"))
		a.loadExperiments(filepath.Join(home, ".vscroxy", "experiments.json"))
		if err := a.board.load(filepath.Join(home, ".vscroxy", "investigations.json")); err != nil {
			a.setMessage("Investigations: " + err.Error())
		}
	}
	return a
}

// A failed read protects that asset until the user repairs or moves the file
// aside and explicitly retries. A missing file is a valid empty asset.
func (a *app) loadRequestAssets() {
	a.loadRequestLibrary()
	a.refreshDocuments()
	if f, err := loadEnvs(a.envsPath); err != nil {
		a.envsProblem = fmt.Sprintf("Variables (%s): %v", a.envsPath, err)
	} else {
		a.envs, a.activeEnv, a.globals, a.envsProblem = f.Envs, f.Active, f.Globals, ""
		if a.activeEnv == "" && len(a.envs) > 0 {
			a.activeEnv = a.envs[0].Name
		}
	}
}

// startCapture listens and hands captured flows to the view.
// reloadConfig reads the rules and the script and hands them to the proxy.
// It runs on the main thread, from startup or the Reload button.
func (a *app) reloadConfig() {
	if a.dnsPath != "" && a.srv != nil {
		lines, err := capture.LoadIgnore(a.dnsPath)
		if err != nil {
			a.errText = err.Error()
		} else if err := a.srv.SetDNS(lines); err != nil {
			a.errText = err.Error()
		} else {
			a.dnsText = strings.Join(lines, "\n")
		}
	}
	if a.ignoreAppsPath != "" && a.srv != nil {
		apps, err := capture.LoadIgnore(a.ignoreAppsPath)
		if err != nil {
			a.errText = err.Error()
		} else {
			a.srv.SetIgnoreApps(apps)
			a.ignoreAppsText = strings.Join(apps, "\n")
		}
	}
	if a.ignorePath != "" && a.srv != nil {
		patterns, err := capture.LoadIgnore(a.ignorePath)
		if err != nil {
			a.errText = err.Error()
		} else {
			a.srv.SetIgnore(patterns)
			a.ignoreText = strings.Join(patterns, "\n")
		}
	}
	if a.sslPath != "" && a.srv != nil {
		cfg, err := capture.LoadSSL(a.sslPath)
		if err != nil {
			a.errText = err.Error()
		} else {
			a.srv.SetSSL(cfg)
			a.sslAll = cfg.All
			a.sslHosts = strings.Join(cfg.Hosts, "\n")
			a.sslApps = slices.Clone(cfg.Apps)
		}
	}
	if a.rulesPath != "" {
		rules, err := capture.LoadRules(a.rulesPath)
		if err != nil {
			a.errText = err.Error()
		} else {
			a.rules = rules
			if a.srv != nil {
				a.srv.SetRules(rules)
			}
		}
	}
	if a.scriptPath == "" {
		return
	}
	script, err := capture.LoadScript(a.scriptPath)
	if err != nil {
		a.errText = err.Error()
		return
	}
	if a.srv != nil {
		a.srv.SetScript(script)
	}
}

// setMessage queues feedback for the UI thread, from any goroutine.
func (a *app) setMessage(msg string) {
	a.mu.Lock()
	a.message = msg
	a.messagePending = true
	a.mu.Unlock()
	if a.win != nil {
		a.win.Invalidate()
	}
}

// invalidate asks for a frame, so what a goroutine changed shows.
func (a *app) invalidate() {
	if a.win != nil {
		a.win.Invalidate()
	}
}

// clamp shortens a label where its full text is available separately.
func clamp(msg string, max int) string {
	r := []rune(msg)
	if len(r) <= max {
		return msg
	}
	return string(r[:max-1]) + "…"
}

func (a *app) revealCA() {
	if a.caPath == "" {
		a.setMessage("Certificate: no path known")
		return
	}
	path := a.caPath
	go func() {
		if err := system.RevealCA(path); err != nil {
			a.setMessage("Reveal certificate: " + err.Error())
		}
	}()
}

// replay sends the flow's request again through the proxy, so the result is
// captured like any other exchange.
func (a *app) replay(ctx context.Context, f flow) error {
	if a.srv == nil || !a.srv.Running() || f.url == "" {
		return fmt.Errorf("capture proxy is not available")
	}
	reply, err := a.srv.Replay(ctx, capture.Flow{
		ID:              f.id,
		Method:          f.method,
		Scheme:          f.scheme,
		Host:            f.host,
		Path:            f.path,
		URL:             f.url,
		RequestHeader:   f.reqHeader,
		RequestBody:     f.reqBody,
		RequestBodyFile: f.reqFile,
	})
	if err != nil {
		return err
	}
	a.mu.Lock()
	a.replayResults = append(a.replayResults, replayResult{source: f, reply: reply})
	a.mu.Unlock()
	a.invalidate()
	return nil
}

func (a *app) startCapture() {
	if a.srv == nil {
		return
	}
	if err := a.srv.Start(); err != nil {
		a.mu.Lock()
		a.err = err
		a.mu.Unlock()
		a.invalidate()
		return
	}
	a.invalidate()
	go a.collectBreakpoints()
	go a.collectFrames()
	for f := range a.srv.Flows() {
		a.mu.Lock()
		a.pending = append(a.pending, f)
		a.mu.Unlock()
		a.win.Invalidate()
	}
}

// collectFrames queues the WebSocket messages and SSE events.
func (a *app) collectFrames() {
	for ev := range a.srv.Frames() {
		a.mu.Lock()
		if len(a.pendingFrames) < 1024 {
			a.pendingFrames = append(a.pendingFrames, ev)
		} else {
			a.frameLost.Add(1)
		}
		a.mu.Unlock()
		a.win.Invalidate()
	}
}

// collectBreakpoints queues the exchanges the proxy paused for the UI.
func (a *app) collectBreakpoints() {
	for bp := range a.srv.Breakpoints() {
		a.mu.Lock()
		a.bps = append(a.bps, bp)
		a.mu.Unlock()
		a.win.Invalidate()
	}
}

// resolveBreak answers the paused exchange and lets the proxy go on.
func (a *app) resolveBreak(abort, original bool) {
	bp := a.bp
	if bp == nil {
		return
	}
	if a.srv != nil {
		if !abort && !original && a.breakpointError() != "" {
			return
		}
		status := 0
		if bp.Phase == capture.PhaseResponse {
			status, _ = strconv.Atoi(strings.TrimSpace(a.bpStatus))
		}
		abortCode, _, _ := strings.Cut(a.bpAbort, " ")
		abortStatus, _ := strconv.Atoi(abortCode)
		resumed := a.srv.Resume(bp.ID, capture.Decision{
			Abort:       abort,
			AbortStatus: abortStatus,
			Original:    original,
			Method:      a.bpMethod,
			URL:         a.bpURL,
			Status:      status,
			Body:        a.bpBody,
			Header:      request.ParseHeaderBlock(a.bpHeaders),
			KeepBody:    bp.BodyReadOnly || a.bpBody == bp.Body,
		})
		if !resumed {
			a.bp, a.bpOpen = nil, false
			a.setMessage("Breakpoint expired; the exchange is no longer paused")
			return
		}
	}
	a.bp = nil
	a.bpOpen = false
	if abort {
		a.setMessage("Aborted " + bp.Method + " " + bp.URL)
		return
	}
	if original {
		a.setMessage("Continued original " + bp.Method + " " + bp.URL)
		return
	}
	a.setMessage("Continued " + bp.Method + " " + bp.URL)
}

// Validate before parsing so a malformed line cannot silently delete a header.
func (a *app) breakpointError() string {
	if a.bp == nil {
		return ""
	}
	if a.bp.Phase == capture.PhaseResponse {
		n, err := strconv.Atoi(strings.TrimSpace(a.bpStatus))
		if err != nil || n < 200 || n > 599 {
			return "Enter a final response status from 200 to 599."
		}
	} else {
		if !httpguts.ValidHeaderFieldName(a.bpMethod) {
			return "Enter a valid HTTP method."
		}
		u, err := url.Parse(a.bpURL)
		if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Hostname() == "" {
			return "Enter an absolute http:// or https:// URL."
		}
	}
	for i, line := range strings.Split(a.bpHeaders, "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		key, value, ok := strings.Cut(line, ":")
		if !ok || !httpguts.ValidHeaderFieldName(strings.TrimSpace(key)) || !httpguts.ValidHeaderFieldValue(value) {
			return fmt.Sprintf("Header line %d must be a valid Name: Value pair.", i+1)
		}
	}
	return ""
}

// drain moves the flows the capture goroutine collected into the table.
func (a *app) drain() {
	a.mu.Lock()
	pending := a.pending
	a.pending = nil
	results := a.requestResults
	a.requestResults = nil
	err := a.err
	a.mu.Unlock()
	for _, result := range results {
		d := result.document
		d.composeWaiting = false
		d.sendCancel, d.sendCancelling = nil, false
		d.sendOutcome = result.outcome
		d.composeResult = result.summary
		d.composeTests = result.tests
		d.composeReplies = result.replies
		d.console = result.console
		if result.outcome == "Cancelled" {
			d.responseTab = resViewBody
		} else if result.response == nil && len(result.console) > 0 {
			d.responseTab = resViewConsole
		}
		d.experimentID = 0
		if result.response != nil {
			first := d.response == nil
			d.response = result.response
			d.responseEditor = nil
			d.comparison.diffPart = "" // even an identical URL is a new send
			// The first response is what the reader came for; a divider they
			// set themselves stays where they put it.
			if first {
				d.responseArrived()
			}
		}
		if result.vars != nil && !maps.Equal(result.varsBefore, result.vars) {
			a.applyVars(result.environment, result.varsBefore, result.vars)
		}
		a.applyWrites(result.writes)
		if result.history != nil {
			a.history = pushHistory(a.history, *result.history)
			a.saveHistory()
		}
		if result.experiment != nil {
			for _, f := range a.flows {
				if f.live {
					result.experiment.keepWire(f)
				}
			}
			if err := a.experiments.log.retain(*result.experiment); err != nil {
				a.experiments.problem = "Experiment not retained: " + err.Error()
			} else {
				d.experimentID = a.experiments.log.NextID
				a.experiments.persist()
			}
		}
	}

	if err != nil {
		a.errText = err.Error()
		a.err = nil
	}
	a.mu.Lock()
	pendingFrames := a.pendingFrames
	a.pendingFrames = nil
	a.mu.Unlock()
	for _, ev := range pendingFrames {
		a.addFrame(ev)
	}
	if a.bp == nil {
		a.mu.Lock()
		var next *capture.Breakpoint
		if len(a.bps) > 0 {
			bp := a.bps[0]
			a.bps = a.bps[1:]
			next = &bp
		}
		a.mu.Unlock()
		if next != nil {
			a.bp = next
			a.bpOpen = true
			a.bpMethod = next.Method
			a.bpURL = next.URL
			a.bpStatus = ""
			if next.Status > 0 {
				a.bpStatus = strconv.Itoa(next.Status)
			}
			a.bpBody = next.Body
			a.bpHeaders = request.HeaderBlock(next.Header)
			a.bpAbort = "502 Bad Gateway"
		}
	}
	if len(pending) == 0 {
		return
	}
	// New traffic arriving while the reader is away from the end is counted,
	// not scrolled to: the footer offers to resume following instead.
	paused := false
	if view := a.views.activeView(); view != nil && len(a.visible) > 0 && !view.list.AtEnd() {
		paused = true
	}
	for _, f := range pending {
		kind := f.Kind
		if kind == "http" {
			kind = ""
		}
		a.flows = append(a.flows, flow{
			id:          f.ID,
			debugKey:    uuid.New().String(),
			debugRun:    f.DebugRun,
			live:        true,
			replayOf:    f.ReplayOf,
			actions:     f.Actions,
			method:      f.Method,
			scheme:      f.Scheme,
			host:        f.Host,
			path:        f.Path,
			url:         f.URL,
			status:      f.Status,
			bytes:       f.Bytes,
			millis:      f.Millis,
			reqHeaders:  f.RequestHeaders,
			resHeaders:  f.ResponseHeaders,
			reqBody:     f.RequestBody,
			reqFile:     f.RequestBodyFile,
			reqSize:     f.RequestBodySize,
			reqError:    f.RequestBodyError,
			resBody:     f.ResponseBody,
			resFile:     f.ResponseBodyFile,
			resSize:     f.ResponseBodySize,
			resError:    f.ResponseBodyError,
			reqHeader:   f.RequestHeader,
			respHeader:  f.ResponseHeader,
			reqTrailer:  f.RequestTrailer,
			resTrailer:  f.ResponseTrailer,
			resProto:    f.ResponseProto,
			at:          f.At,
			kind:        kind,
			process:     f.Process,
			processPath: f.ProcessPath,
			dns:         f.DNSMapped,
			conn: conn{
				proto:      f.Conn.Proto,
				client:     f.Conn.Client,
				proxy:      f.Conn.Proxy,
				server:     f.Conn.Server,
				resolved:   f.Conn.Resolved,
				lookupMs:   f.Conn.LookupMs,
				tls:        f.Conn.TLS,
				cipher:     f.Conn.Cipher,
				certCN:     f.Conn.CertCN,
				issuer:     f.Conn.Issuer,
				certUntil:  f.Conn.CertUntil,
				flows:      f.Conn.Flows,
				reused:     f.Conn.Reused,
				dnsFresh:   f.Conn.DNSFresh,
				log:        f.Conn.Log,
				error:      f.Conn.Error,
				errorStage: f.Conn.ErrorStage,
			},
			wait:     f.Wait,
			download: f.Download,
		})
	}
	checkpoint := a.experiments.log
	wireChanged := false
	for _, f := range a.flows[len(a.flows)-len(pending):] {
		a.totalBytes += int64(f.bytes)
		if f.debugRun == "" {
			continue
		}
		for i := range checkpoint.Records {
			record := checkpoint.Records[i]
			if record.keepWire(f) {
				if !wireChanged {
					checkpoint.Records = slices.Clone(checkpoint.Records)
				}
				checkpoint.Records[i] = record
				wireChanged = true
			}
		}
	}
	if wireChanged {
		if _, err := encodeExperiments(checkpoint); err != nil {
			a.experiments.problem = "Wire evidence not retained: " + err.Error()
		} else {
			a.experiments.log = checkpoint
			a.experiments.persist()
		}
	}
	if len(a.flows) > maxFlows {
		cut := len(a.flows) - maxFlows
		a.flows = a.flows[cut:]
		a.frames = pruneMessages(a.frames, a.flows)
	}
	a.countDomains()
	a.countClients()
	a.applyFilter()
	if paused {
		a.newRows += len(pending)
	} else {
		a.newRows = 0
	}
	if a.srv != nil {
		a.dropped = a.srv.Dropped()
		a.scriptErrs = a.srv.ScriptErrors()
	}
}

// countClients counts the flows per app, the Clients section of the sidebar.
func (a *app) countClients() {
	counts := make(map[string]int, len(a.flows))
	for _, f := range a.flows {
		if f.process != "" {
			counts[f.process]++
		}
	}
	a.clients = a.clients[:0]
	for name, n := range counts {
		a.clients = append(a.clients, domain{name: name, n: n})
	}
	slices.SortFunc(a.clients, func(x, y domain) int { return cmp.Compare(y.n, x.n) })
}

func (a *app) countDomains() {
	counts := make(map[string]int, len(a.flows))
	for _, f := range a.flows {
		counts[f.host]++
	}
	a.domains = a.domains[:0]
	for name, n := range counts {
		a.domains = append(a.domains, domain{name: name, n: n})
	}
	slices.SortFunc(a.domains, func(x, y domain) int { return cmp.Compare(y.n, x.n) })
}

// applyFilter rebuilds the visible rows from the search text and the sidebar
// choice, and keeps the choice on a row that still shows.
func (a *app) applyFilter() {
	q := strings.ToLower(strings.TrimSpace(a.query))
	a.visible = a.visible[:0]
	for i, f := range a.flows {
		if f.debugKey == "" {
			f.debugKey = uuid.New().String()
			a.flows[i].debugKey = f.debugKey
		}
		if !a.matchesChip(f) {
			continue
		}
		if a.filter == "pinned" && !f.pinned {
			continue
		}
		if a.filter == "errors" && !f.failed() {
			continue
		}
		if a.filter == "slow" && f.millis < 120 {
			continue
		}
		if host, ok := strings.CutPrefix(a.filter, "host:"); ok && f.host != host {
			continue
		}
		if client, ok := strings.CutPrefix(a.filter, "client:"); ok && f.process != client {
			continue
		}
		if q != "" && !strings.Contains(strings.ToLower(f.host+f.path), q) {
			continue
		}
		if a.filterStatus != "" && !statusMatches(a.filterStatus, f.status) {
			continue
		}
		if a.filterHeader != "" && !headerMatches(f, a.filterHeader) {
			continue
		}
		if !a.filterTriage.matches(f) {
			continue
		}
		if a.filterBody != "" && !strings.Contains(strings.ToLower(f.reqBody+f.resBody), strings.ToLower(a.filterBody)) {
			continue
		}
		a.visible = append(a.visible, i)
	}
	a.sortVisible()
	a.views.narrow(a)
	a.restoreSelection()
}

// restoreSelection maps the stable choice to the table's current row index.
func (a *app) restoreSelection() {
	// Resolve the identity only after all filters and sorting have finished.
	// If it disappeared, choose the nearest surviving row, or none.
	for row, at := range a.visible {
		if a.flows[at].id == a.pickedID {
			a.picked = row
			return
		}
	}
	a.pickedID = 0
	a.picked = min(max(a.picked, 0), len(a.visible)-1)
	if f, ok := a.current(); ok {
		a.pickedID = f.id
	}
}

// current is the flow the detail panes show, if any.
func (a *app) current() (flow, bool) {
	if len(a.visible) == 0 || a.picked < 0 || a.picked >= len(a.visible) {
		return flow{}, false
	}
	return a.flows[a.visible[a.picked]], true
}

// previousResponse is the last response for the same method and URL, the
// other side of the diff. An empty response is still a valid baseline.
func (a *app) previousResponse() (string, bool) {
	if len(a.visible) == 0 || a.picked < 0 || a.picked >= len(a.visible) {
		return "", false
	}
	at := a.visible[a.picked]
	f := a.flows[at]
	for i := at - 1; i >= 0; i-- {
		prev := a.flows[i]
		if prev.method == f.method && prev.scheme == f.scheme && prev.host == f.host && prev.path == f.path && prev.status > 0 {
			return prev.body(true), true
		}
	}
	return "", false
}

// syncDetail rebuilds the text and the diff when the choice moves.
func (a *app) syncDetail() {
	f, ok := a.current()
	if !ok {
		a.detailFor = -1
		a.connOpen, a.explainOpen = false, false
		a.protobuf.stop()
		return
	}
	if a.detailFor == f.id {
		return
	}
	a.detailFor = f.id
	a.connOpen, a.explainOpen = false, false
	if f.kind == "" && a.inspect.pane == inspectorMessages {
		a.inspect.pane = inspectorSplit
	}
	if f.kind == "connect" {
		if f.conn.error != "" {
			a.tab = 1 // open the failure evidence, not tunnel-only setup instructions
		}
	} else if a.tab != 0 {
		a.tab = 0
	}
	if (f.kind == "ws" || f.kind == "sse") && a.inspect.pane == inspectorSplit {
		a.inspect.pane = inspectorMessages
	}
	if f.kind != "ws" && f.kind != "sse" && a.inspect.pane == inspectorMessages {
		a.inspect.pane = inspectorSplit
	}
	a.body, a.bodyNotice[0] = f.bodyPreview(true)
	a.reqBody, a.bodyNotice[1] = f.bodyPreview(false)
	a.rawText[0], a.rawText[1] = rawMessage(f, 0), rawMessage(f, 1)
	a.rawEditor = [2]*editor.State{}
	a.inspect.reveal = [2]bool{}
	a.bodyMode, a.bodyQuery = [2]int{}, [2]string{}
	a.headerScroll, a.bodyText = [2]ui.ScrollState{}, [2]*editor.State{}
	a.jsonScroll, a.hexScroll = [2]ui.ListState{}, [2]ui.ListState{}
	a.jsonFor, a.protoFor, a.htmlFor, a.imageFor = [2]string{}, [2]string{}, [2]string{}, [2]string{}
	a.jsonOK, a.protoOK = [2]bool{}, [2]bool{}
	a.htmlDoc, a.imageBmp, a.imageErr = [2]*richtext.Document{}, [2]*ui.Bitmap{}, [2]string{}
	a.protobuf.stop()
	previous, baseline := a.previousResponse()
	a.diffBaseline = baseline
	a.diff = &editor.DiffState{
		Old:    editor.NewBuffer(previous),
		New:    editor.NewBuffer(a.body + a.bodyNotice[0]),
		Layout: editor.DiffSplit,
	}
	a.diff.Old.SetLanguage(editor.LanguageFor("response.json"))
	a.diff.New.SetLanguage(editor.LanguageFor("response.json"))
}

func (a *app) view(c *ui.Context) {
	c.SetTheme(uikit.Theme(a.dark))
	a.drain()
	a.drainCertificate()
	a.drainSystemProxy()
	a.drainRunner()
	a.mu.Lock()
	if a.messagePending {
		c.Toast(a.message)
		a.messagePending = false
	}
	replays := a.replayResults
	a.replayResults = nil
	a.mu.Unlock()
	for _, result := range replays {
		c.ToastAction(fmt.Sprintf("Replayed Capture #%d · %s %s → %d", result.source.id, result.source.method, result.source.path, result.reply.Status), "Inspect replay", func() {
			// The response belongs to this exact send, not the next capture of
			// the same URL. Never move the reader until they ask to inspect it.
			a.composeFrom(result.source)
			a.response = &result.reply
		})
	}
	a.desktopShortcuts(c)
	if a.sort != a.sortApplied {
		a.sortApplied = a.sort
		a.applyFilter()
	}
	a.views.drain(a)
	a.views.sync(a)
	// Bulk actions must never include requests hidden by this view's filters.
	if a.chosen.Len() > 0 {
		visibleIDs := make(map[int]bool, len(a.visible))
		for _, i := range a.visible {
			visibleIDs[a.flows[i].id] = true
		}
		for id := range a.chosen.All() {
			if !visibleIDs[id] {
				a.chosen.Remove(id)
			}
		}
	}
	a.desktopNativeMenu(c)
	t := c.Theme()
	a.syncSections()
	ui.Column(c).Bind(&a.workbench.content).Focusable().AutoFocus().Fill().Background(t.Background).Children(func() {
		a.shell(c, t)
		a.activeTools(c, t)
		if a.srv == nil && a.errText != "" {
			ui.Text(c, "Proxy unavailable · "+a.errText+" · Local requests remain available").Padding(t.Space(2), t.Space(3)).FontSize(uikit.TextBody).TextColor(t.Danger)
		}
		if a.requestsProblem != "" || a.envsProblem != "" {
			ui.Column(c).Padding(t.Space(2), t.Space(3)).Gap(t.Space(1)).Children(func() {
				ui.Row(c).Gap(t.Space(3)).AlignItems(ui.Center).Children(func() {
					ui.Text(c, "Local assets protected · saving disabled").FontSize(uikit.TextBody).Bold().TextColor(t.Danger).Grow(1)
					if uikit.SecondaryButton(c, "Reload local assets").Clicked() {
						a.loadRequestAssets()
					}
				})
				for _, problem := range []string{a.requestsProblem, a.envsProblem} {
					if problem != "" {
						ui.Text(c, problem).FontSize(uikit.TextMeta).TextColor(t.Danger)
					}
				}
				uikit.Meta(c, "Original files left untouched. Repair or move the affected file aside, then reload. Other local assets remain usable.")
			})
		}
		a.desktopContent(c.Key("workspace-content"))
		a.footer(c.Key("workspace-footer"), t)
	})
	a.persistSettings()
	a.board.persist()
	a.checkpointExperiments(c)
	// Dialogs keep their input and actions above background notifications.
	uikit.Toasts(c)
	a.commandPalette(c)
	uikit.Modal(c, &a.clearOpen, func() {
		uikit.DialogColumn(c, uikit.DialogSmall).Children(func() {
			uikit.DialogTitle(c, "Clear captured requests?")
			ui.Scroll(c).MinHeight(0).Children(func() {
				uikit.Body(c, "Unpinned requests will be removed from this session. Export any traffic you want to keep first.")
			})
			uikit.DialogFooter(c).Children(func() {
				if uikit.SecondaryButton(c, "Cancel").Clicked() {
					a.clearOpen = false
				}
				if uikit.PrimaryButton(c, "Clear unpinned requests").Background(t.Danger).Clicked() {
					a.clearCaptured()
					a.clearOpen = false
				}
			})
		})
	})
	a.settingsModal(c, t)
	a.breakpointModal(c, t)
	a.upstreamModal(c, t)
	a.sslModal(c, t)
	a.dnsModal(c, t)
	a.dnsRuleModal(c, t)
	a.hostListModal(c, t, &a.ignoreAppsOpen, "Ignored apps",
		"Apps whose flows stay out of the table, one name per line, as the Apps section spells them. They are still proxied.",
		a.ignoreAppsPath, &a.ignoreAppsText, func(apps []string) {
			if a.srv != nil {
				a.srv.SetIgnoreApps(apps)
			}
		})
	a.hostListModal(c, t, &a.ignoreOpen, "Ignored hosts",
		"Hosts whose flows stay out of the table, one per line. They are still proxied, so the client sees no difference.",
		a.ignorePath, &a.ignoreText, func(patterns []string) {
			if a.srv != nil {
				a.srv.SetIgnore(patterns)
			}
		})
	a.varsModal(c, t)
	a.importReportModal(c, t)
	a.folderModal(c, t)
	a.deleteModal(c, t)
	a.codeModal(c, t)
	a.vaultModal(c, t)
	a.markModal(c, t)
	a.views.modals(c, t, a)
	a.columnModal(c, t)
	a.keyLogModal(c, t)
	a.closeRequestModal(c, t)
	a.apiGenerationModal(c, t)
	a.curlImportModal(c, t)
	a.rulesModal(c, t)
	a.devicesModal(c, t)
	a.clientSetupModal(c, t)
	a.diagnosticsModal(c, t)
	a.experimentsModal(c, t)
	a.socketModal(c, t)
	a.investigations.modal(c, a)
	a.boardConfirmModal(c, t)
	a.requestRenameModal(c, t)
	a.headerWarnModal(c, t)
	a.runnerModal(c, t)
	a.cookiesModal(c, t)
	a.regressionModal(c, t)
	a.replayModal(c, t)
	a.collectionSaveModal(c, t)
	a.protobuf.modal(c, a)
	a.views.capture(a)
	if a.workspace != 0 || a.inspect.pane != inspectorMessages {
		a.messageUI.stopSearch()
	}
	f, ok := a.current()
	bodyShowing := a.workspace == 0 && ok && (a.inspect.tab[0] == reqTabBody || a.inspect.tab[1] == resTabBody)
	protobufShowing := false
	for side := range 2 {
		ct := f.contentType(side == 0)
		if a.bodyMode[side] == 5 || a.bodyMode[side] == 0 && (grpcContentType(ct) || strings.Contains(ct, "protobuf")) {
			protobufShowing = true
		}
	}
	if !bodyShowing || !protobufShowing {
		a.protobuf.stop()
	}
}

// upstreamModal edits the proxy every request goes through.
func (a *app) upstreamModal(c *ui.Context, t *ui.Theme) {
	if !a.upstreamOpen {
		return
	}
	uikit.Modal(c, &a.upstreamOpen, func() {
		uikit.DialogColumn(c, uikit.DialogSmall).Children(func() {
			uikit.DialogTitle(c, "Upstream proxy")
			ui.Scroll(c).MinHeight(0).Children(func() {
				uikit.Input(c, &a.upstreamAddr).Placeholder("http://127.0.0.1:8080").Label("Address")
			})
			uikit.DialogFooter(c).Children(func() {
				if uikit.SecondaryButton(c, "Cancel").Clicked() {
					a.upstreamOpen = false
				}
				if uikit.PrimaryButton(c, "Save").Clicked() {
					if a.srv != nil {
						if err := a.srv.SetUpstream(strings.TrimSpace(a.upstreamAddr)); err != nil {
							a.setMessage("Upstream: " + err.Error())
						} else {
							a.upstream = a.srv.Upstream()
							a.setMessage("Upstream: " + cmp.Or(a.upstream, "off"))
						}
					}
					a.upstreamOpen = false
				}
			})
		})
	})
}

// breakpointModal edits the request the proxy paused, and lets it continue.
func (a *app) breakpointModal(c *ui.Context, t *ui.Theme) {
	if a.bp == nil {
		return
	}
	// Feedback from a previous decision must not cover this pause's footer,
	// especially when queued breakpoints open in a small window.
	c.CloseToast("")
	_, height := c.Size()
	if uikit.Modal(c, &a.bpOpen, func() {
		uikit.DialogColumn(c, uikit.DialogLarge).Height(min(640, height-80)).Children(func() {
			title := "Breakpoint · request paused"
			if a.bp.Phase == capture.PhaseResponse {
				title = "Breakpoint · response paused"
			}
			uikit.DialogTitle(c, title)
			a.mu.Lock()
			waiting := len(a.bps) + 1
			a.mu.Unlock()
			uikit.Meta(c, fmt.Sprintf("%d paused · changes apply only to this exchange", waiting))
			ui.Scroll(c).Grow(1).MinHeight(0).FillWidth().Gap(t.Space(2)).Children(func() {
				if a.bp.Phase == capture.PhaseResponse {
					ui.Row(c).Gap(t.Space(2)).Children(func() {
						uikit.Input(c, &a.bpStatus).Width(90).Label("Status")
						uikit.Hint(c, a.bp.URL).SingleLine().Grow(1).MinWidth(0)
					})
				} else {
					ui.Row(c).Gap(t.Space(2)).Children(func() {
						uikit.Input(c, &a.bpMethod).Width(90).Label("Method")
						uikit.Input(c, &a.bpURL).Grow(1).MinWidth(0).Label("URL")
					})
				}
				uikit.Meta(c, "Headers · one Name: Value per line; repeat a name for multiple values")
				ui.TextArea(c, &a.bpHeaders).Height(130).Label("Breakpoint headers").FontSize(uikit.TextBody).Radius(uikit.RadiusControl)
				if a.bp.BodyReadOnly {
					uikit.Hint(c, "Binary, undecodable or large body: preserved unchanged; headers can still be edited.")
				} else {
					ui.TextArea(c, &a.bpBody).Height(160).Label("Body").FontSize(uikit.TextBody).Radius(uikit.RadiusControl)
					uikit.Meta(c, "Body edits use decoded bytes; encoding, digest and length headers are recalculated or removed.")
				}
			})
			validation := a.breakpointError()
			if validation != "" {
				ui.Text(c, validation).FontSize(uikit.TextBody).TextColor(t.Danger)
			}
			uikit.DialogFooter(c).Children(func() {
				uikit.Select(c, &a.bpAbort, []string{"502 Bad Gateway", "429 Too Many Requests", "503 Service Unavailable"}).Width(200).Label("Abort status")
				if uikit.SecondaryButton(c, "Abort").Clicked() {
					a.resolveBreak(true, false)
				}
				if uikit.SecondaryButton(c, "Continue original").Clicked() {
					a.resolveBreak(false, true)
				}
				if uikit.PrimaryButton(c, "Apply & continue").Disabled(validation != "").Clicked() {
					a.resolveBreak(false, false)
				}
			})
		})
	}).Dismissed() {
		// Escape or an accidental outside click must not abort a live request.
		a.bpOpen = true
	}
}

// clearCaptured runs only after confirmation. Pinned exchanges, including
// their streaming messages, remain available for inspection.
func (a *app) clearCaptured() {
	a.mu.Lock()
	a.pending = nil
	a.mu.Unlock()
	a.flows = slices.DeleteFunc(a.flows, func(f flow) bool { return !f.pinned })
	a.frames = pruneMessages(a.frames, a.flows)
	a.chosen.Clear()
	a.totalBytes = 0
	for _, f := range a.flows {
		a.totalBytes += int64(f.bytes)
	}
	a.countDomains()
	a.countClients()
	a.applyFilter()
	a.setMessage("Unpinned requests cleared")
}

// sidebar is the window's one source list, in three groups: LIVE is the
// traffic and what narrows it, COLLECTIONS the saved requests and API
// contracts, SESSIONS the evidence kept beyond live traffic. Its one choice
// is what the content area shows; syncSections keeps it in step.
func (a *app) sidebar(c *ui.Context) {
	t := c.Theme()
	ui.Column(c).Fill().Background(uikit.Colors(t).Panel).Children(func() {
		chosen := ui.Sidebar(c, &a.sidebarChoice, func() {
			reveal := func(group string, section ui.Element) {
				if a.revealGroup == group {
					section.ScrollIntoView()
				}
			}
			reveal("live", uikit.SidebarSection(c, "Live", &a.trafficOpen, func() {
				uikit.SidebarItem(c, "all", trafficIcon, "All requests", a.sidebarChoice == "all").Children(func() {
					uikit.Meta(c, fmt.Sprint(len(a.flows))).FontFeatures("tnum")
				})
				uikit.SidebarItem(c, "errors", errorIcon, "Errors", a.sidebarChoice == "errors").Children(func() {
					uikit.Meta(c, fmt.Sprint(a.count(flow.failed))).FontFeatures("tnum")
				})
				uikit.SidebarItem(c, "slow", captureIcon, "Slow", a.sidebarChoice == "slow").Children(func() {
					uikit.Meta(c, fmt.Sprint(a.count(func(f flow) bool { return f.millis >= 120 }))).FontFeatures("tnum")
				})
				uikit.SidebarItem(c, "pinned", pinIcon, "Pinned", a.sidebarChoice == "pinned").Children(func() {
					uikit.Meta(c, fmt.Sprint(a.count(func(f flow) bool { return f.pinned }))).FontFeatures("tnum")
				})
			}))
			if len(a.clients) > 0 {
				uikit.SidebarSection(c, "Apps", &a.clientsOpen, func() {
					for _, d := range a.clients {
						apps := a.capturedApps(d.name)
						uikit.SidebarItem(c, "client:"+d.name, appIcon, d.name, a.sidebarChoice == "client:"+d.name).PopoverMenu(
							func(m *ui.Menu) {
								if len(apps) == 1 {
									a.appInspectionMenu(m, apps[0])
								} else {
									m.Submenu("HTTPS inspection by executable", func(m *ui.Menu) {
										if len(apps) == 0 {
											m.Item("Local executable identity unavailable").Disabled(true)
										}
										for _, app := range apps {
											m.Submenu(app.Path, func(m *ui.Menu) { a.appInspectionMenu(m, app) })
										}
									})
								}
								if m.Item("View HTTPS rules…").Chosen() {
									a.sslOpen = true
								}
								m.Separator()
								if m.Item("Map a host for " + d.name + "…").Chosen() {
									a.openDNSRule("", d.name)
								}
							}).
							Children(func() {
								if a.filterTriage.Client == d.name {
									ui.Text(c, "✓").FontSize(uikit.TextMeta).TextColor(t.Accent).Tooltip("App filter is active; remove it in the condition bar")
								}
								if slices.ContainsFunc(apps, func(app capture.SSLApp) bool { return a.appInspectionEnabled(app.Path) }) {
									ui.Icon(c, lockIcon).Size(uikit.IconInline, uikit.IconInline).Tooltip("An exact executable rule is enabled; client CA trust is still required")
								}
								uikit.Meta(c, fmt.Sprint(d.n)).FontFeatures("tnum")
							})
					}
				})
			}
			uikit.SidebarSection(c, "Domains", &a.domainsOpen, func() {
				for _, d := range a.domains {
					uikit.SidebarItem(c, "host:"+d.name, domainIcon, d.name, a.sidebarChoice == "host:"+d.name).Children(func() {
						if a.filterTriage.Host == d.name {
							ui.Text(c, "✓").FontSize(uikit.TextMeta).TextColor(t.Accent).Tooltip("Host filter is active; remove it in the condition bar")
						}
						uikit.Meta(c, fmt.Sprint(d.n)).FontFeatures("tnum")
					})
				}
			})
			uikit.SidebarSection(c, "Views", nil, func() {
				a.views.sidebar(c, t, a)
			})
			reveal("collections", uikit.SidebarSection(c, "Collections", &a.collectionsOpen, func() {
				a.collectionsGroup(c, t)
			}))
			reveal("sessions", uikit.SidebarSection(c, "Sessions", nil, func() {
				for _, inv := range a.board.items {
					uikit.SidebarItem(c, "investigation:"+inv.ID, layersIcon, inv.Title, a.sidebarChoice == "investigation:"+inv.ID).Children(func() {
						uikit.Meta(c, fmt.Sprint(len(inv.Items))).FontFeatures("tnum")
					})
				}
				if uikit.ToolbarButton(c, "New investigation", "Create an investigation, then add captures from their inspectors to keep evidence", false).FillWidth().Justify(ui.Start).Children(func() {
					ui.Icon(c, plusIcon).Size(uikit.IconInline, uikit.IconInline)
					uikit.Body(c, "New investigation")
				}).Clicked() {
					a.board.create("")
					a.board.list = ui.ListState{}
					a.workspace, a.captureSection = 0, 1
				}
				if uikit.ToolbarButton(c, "Historical sessions…", "Read saved sessions and exported evidence, read-only", false).FillWidth().Justify(ui.Start).Children(func() {
					ui.Icon(c, clockIcon).Size(uikit.IconInline, uikit.IconInline)
					uikit.Body(c, "Historical sessions…")
				}).Clicked() {
					a.investigations.show()
				}
			}))
		}).Grow(1).Background(uikit.Colors(t).Panel)
		a.revealGroup = ""
		if chosen.Changed() {
			a.goTo(a.sidebarChoice)
		}
		ui.Divider(c)
		ui.Column(c).Padding(t.Space(2)).Gap(t.Space(0.5)).Children(func() {
			if uikit.ToolbarButton(c, "Client setup", "Copy a process-scoped proxy and CA example", false).FillWidth().Children(func() {
				ui.Icon(c, monitorIcon).Size(uikit.IconInline, uikit.IconInline)
				uikit.Body(c, "Client setup").Grow(1)
				uikit.Body(c, "›")
			}).Clicked() {
				a.openClientSetup()
			}
			if uikit.ToolbarButton(c, "Manage HTTPS rules", "HTTPS inspection rules", false).FillWidth().Children(func() {
				ui.Icon(c, lockIcon).Size(uikit.IconInline, uikit.IconInline)
				uikit.Body(c, "HTTPS rules").Grow(1)
				uikit.Body(c, "›")
			}).Clicked() {
				a.sslOpen = true
			}
		})
	})
}

// goTo shows what a sidebar choice names: an investigation, an API
// contract, or a LIVE scope of the traffic. Saved requests are tree items
// that open themselves.
func (a *app) goTo(choice string) {
	if id, ok := strings.CutPrefix(choice, "investigation:"); ok {
		a.workspace, a.captureSection = 0, 1
		a.board.active = id
		a.board.list = ui.ListState{}
		return
	}
	if id, ok := strings.CutPrefix(choice, "contract:"); ok {
		a.workspace, a.requestsSection, a.apiChoice = 1, 1, id
		return
	}
	a.workspace, a.captureSection = 0, 0
	if host, ok := strings.CutPrefix(choice, "host:"); ok {
		a.filterTriage.Host = host
	} else if client, ok := strings.CutPrefix(choice, "client:"); ok {
		a.filterTriage.Client = client
	} else {
		a.filter = cmp.Or(choice, "all")
	}
	a.applyFilter()
}

// collectionsGroup is the COLLECTIONS group: its search and tools, the
// collection tree, and the API contracts. Environments, run records and the
// rest of the library open from its menu and the command palette.
func (a *app) collectionsGroup(c *ui.Context, t *ui.Theme) {
	ui.Row(c).Gap(t.Space(1)).AlignItems(ui.Center).Padding(0, 0, t.Space(1)).Children(func() {
		uikit.Input(c, &a.requestQuery).Label("Search collections").Placeholder("Search…").Grow(1).MinWidth(0)
		if uikit.IconButton(c, "New request", "New request", plusIcon).Clicked() {
			a.openCompose()
		}
		uikit.IconButton(c, "Collection options", "Collections, APIs, environments and runs", moreIcon).PopoverMenuButton(a.collectionsMenu)
	})
	a.collectionTree(c)
	query := strings.ToLower(strings.TrimSpace(a.requestQuery))
	for _, d := range a.apis {
		if query != "" && !strings.Contains(strings.ToLower(d.title()), query) {
			continue
		}
		uikit.SidebarItem(c, "contract:"+d.spec.ID, apiIcon, d.title(), a.sidebarChoice == "contract:"+d.spec.ID).Label("API: " + d.title()).Children(func() {
			if d.dirty() {
				ui.Box(c).Size(5, 5).Radius(uikit.RadiusControl).Background(t.Accent).Label("Unsaved API")
			}
		})
	}
}

func (a *app) collectionsMenu(m *ui.Menu) {
	if m.Item("Import…").Chosen() {
		a.importRequests()
	}
	if m.Item("Import cURL…").Chosen() {
		a.openCurlImport()
	}
	m.Separator()
	if m.Item("Environments…").Chosen() {
		a.openVars()
	}
	if m.Item("Run records").Chosen() {
		a.showRunRecords()
	}
	if m.Item("Runner…").Chosen() {
		a.openRunner()
	}
	m.Separator()
	if m.Item("New API contract").Chosen() {
		a.newAPI()
		a.workspace, a.requestsSection = 1, 1
	}
	if m.Item("Import OpenAPI…").Chosen() {
		a.importOpenAPI()
	}
	if m.Item("Generate API from collection…").Disabled(len(a.requests) == 0).Chosen() {
		a.openAPIGeneration()
	}
	m.Separator()
	if m.Item("Vault…").Chosen() {
		a.openVault()
	}
	if m.Item("Cookies…").Chosen() {
		a.openCookies()
	}
}

// showRunRecords shows every run record in the content area.
func (a *app) showRunRecords() {
	a.experiments.scope, a.experiments.draftKey = nil, ""
	a.workspace, a.requestsSection = 1, 2
}

func (a *app) count(match func(flow) bool) int {
	n := 0
	for _, f := range a.flows {
		if match(f) {
			n++
		}
	}
	return n
}

func (a *app) table(c *ui.Context) {
	t := c.Theme()
	if len(a.visible) == 0 {
		if len(a.flows) > 0 {
			note, failure := a.views.status()
			title := "No matching requests"
			hint := fmt.Sprintf("%d captured requests are hidden by this view's search or filters.", len(a.flows))
			if note != "" {
				title, hint = "Searching captured bodies…", "Results are not final until the body search finishes."
				if failure {
					title, hint = "No matches in searchable traffic", "Some bodies could not be searched in full; matches may be missing."
				}
			}
			uikit.EmptyState(c, title, hint, func() {
				uikit.Hint(c, "All conditions must match. Remove a condition above or edit the filters.")
				if note != "" {
					color := t.TextMuted
					if failure {
						color = t.Warning
					}
					ui.Text(c, note).FontSize(uikit.TextBody).TextColor(color).Tooltip(a.views.statusDetail())
				}
				if uikit.SecondaryButton(c, "Edit filters…").Clicked() {
					a.openFilter()
				}
				if uikit.SecondaryButton(c, "Show all requests").Clicked() {
					a.views.resetFilters(a)
				}
			})
			return
		}
		running := a.srv != nil && a.srv.Running()
		title, hint := "Capture is stopped", "Check the listener before configuring a client."
		if running {
			title, hint = "Connect a client to start capturing", "Listening at "+a.srv.Addr()+" · no retained requests yet"
		}
		uikit.EmptyState(c, title, hint, func() {
			ui.Row(c).Gap(t.Space(2)).Wrap().Justify(ui.Center).Children(func() {
				if uikit.SecondaryButton(c, "Set up a local client…").Disabled(!running).Clicked() {
					a.openClientSetup()
				}
				if uikit.SecondaryButton(c, "Connect a device…").Disabled(!running).Clicked() {
					a.openDevices()
				}
				if uikit.SecondaryButton(c, "Check HTTPS…").Clicked() {
					a.settingsTab, a.settingsOpen = 2, true
				}
			})
			if uikit.SecondaryButton(c, "Diagnose capture…").Clicked() {
				a.openDiagnostics()
			}
		})
		return
	}
	cols := []ui.TableColumn{
		{ID: "id", Title: "#", Width: 48, Sortable: true},
		{ID: "status", Title: "Status", Width: 78, Sortable: true},
		{ID: "method", Title: "Method", Width: 76, Sortable: true},
		{ID: "host", Title: "Host", Width: 160, Sortable: true},
		// Keep the URL identity and sorting so saved column layouts survive.
		{ID: "url", Title: "Path", MinWidth: 160, Sortable: true},
		{ID: "client", Title: "Client", Width: 132, Sortable: true},
		{ID: "duration", Title: "Duration", Width: 84, Align: ui.End, Sortable: true},
		{ID: "size", Title: "Size", Width: 78, Align: ui.End, Sortable: true},
		{ID: "captured", Title: "Time", Width: 84, Sortable: true},
	}
	width, _ := c.Size()
	available := width
	if !a.workbench.sidebarHidden {
		available -= a.sidebarWidth + 6
	}
	if a.workbench.sideBySide {
		available = available * a.workbench.sidePercent / 100
	}
	// A narrow list keeps what identifies a request and what it returned;
	// the columns that only repeat the inspector's numbers give way.
	if available < 900 {
		cols[3].Width = 132
		cols[5].Width = 100
		cols = slices.DeleteFunc(cols, func(col ui.TableColumn) bool { return col.ID == "size" || col.ID == "captured" })
	}
	if available < 700 {
		cols = slices.DeleteFunc(cols, func(col ui.TableColumn) bool { return col.ID == "client" })
	}
	cols = slices.DeleteFunc(cols, func(col ui.TableColumn) bool { return slices.Contains(a.hiddenColumns, col.ID) })
	for _, cc := range a.customColumns {
		cols = append(cols, ui.TableColumn{ID: cc.id(), Title: cc.title(), Width: 150, Sortable: true})
	}
	cols = append(cols, ui.TableColumn{Title: "", ID: "filler", Width: 14, Fixed: true})
	state := &a.views.activeView().list
	state.FollowEnd = true
	state.Selected = &a.picked
	state.Selection = &a.chosen
	state.Key = func(row int) any { return a.flows[a.visible[row]].id }
	state.Sort = &a.sort
	state.Columns = a.columns
	state.HeaderMenu = func(m *ui.Menu, col int) {
		id := ""
		if col >= 0 {
			id = cols[col].ID
		}
		a.columnsMenu(m, id)
	}
	focused := state.Handle.Focused(c)
	selectionTheme := uikit.ListTheme(t, focused)
	// Table's built-in row minimum is eight spacing units. Scope the 3.5px
	// unit here only: 28px rows without shrinking the rest of the application.
	selectionTheme.Spacing = 3.5
	c.SetTheme(selectionTheme)
	table := ui.Table(c, state, cols, len(a.visible), func(row, col int) {
		f := a.flows[a.visible[row]]
		if col == 0 && a.chosen.Has(f.id) {
			// The marker sits on the row's first visible cell, whichever
			// column the user moved there.
			uikit.RowMarker(c, focused)
		}
		text := func(s string) ui.Element {
			return uikit.Body(c, s).Height(16).FontFeatures("tnum").SingleLine()
		}
		switch cols[col].ID {
		case "id":
			text(fmt.Sprint(f.id)).Font(uikit.FontMono).TextColor(t.TextMuted)
		case "method":
			uikit.MethodLabel(c, f.method).Height(16).TextColor(uikit.MethodColor(f.method, t)).Tooltip(f.method).PopoverMenu(a.rowMenu(c, f))
		case "host":
			text(f.host).TextColor(t.TextMuted).Tooltip(f.url).PopoverMenu(a.rowMenu(c, f))
		case "url":
			if f.pinned {
				ui.Icon(c, pinIcon).Size(uikit.IconInline, uikit.IconInline).MarginEnd(t.Space(1)).TextColor(t.TextMuted)
			}
			if f.color != "" {
				if dot, ok := highlightColor(f.color); ok {
					ui.Box(c).Size(8, 8).Radius(uikit.RadiusControl).Background(dot).MarginEnd(t.Space(1))
				}
			}
			rest := t.Text
			ui.Row(c).Gap(t.Space(0.5)).Children(func() {
				if f.kind == "connect" {
					ui.Icon(c, lockIcon).Size(uikit.IconInline, uikit.IconInline).TextColor(rest).MarginEnd(t.Space(1)).Tooltip("Encrypted tunnel; CONNECT success is not a decrypted response")
				}
				if f.note != "" {
					ui.Icon(c, noteIcon).Size(uikit.IconInline, uikit.IconInline).TextColor(t.TextMuted).MarginEnd(t.Space(1)).Tooltip(f.note)
				}
				if f.dns != "" {
					ui.Box(c).MinWidth(uikit.HeightControl).Height(16).Radius(uikit.RadiusControl).Background(t.Warning.Alpha(0.12)).Center().MarginEnd(t.Space(1)).
						Tooltip("DNS mapping " + f.dns).
						Children(func() {
							ui.Text(c, "DNS").FontSize(uikit.TextMeta).Bold().TextColor(t.Warning)
						})
				}
				text(cmp.Or(f.path, "/")).Label(f.host + f.path).TextColor(rest).Tooltip(f.url).PopoverMenu(a.rowMenu(c, f))
			})
		case "status":
			if f.conn.error != "" {
				text("Failed").TextColor(t.Danger).Tooltip(f.conn.errorStage + ": " + f.conn.error).PopoverMenu(a.rowMenu(c, f))
			} else if f.kind == "connect" {
				text("Tunnel").TextColor(t.TextMuted).Tooltip(fmt.Sprintf("CONNECT %d · encrypted tunnel, not an HTTP response", f.status)).PopoverMenu(a.rowMenu(c, f))
			} else {
				ui.Box(c).Size(5, 5).Radius(uikit.RadiusControl).Background(f.statusColor(t)).MarginEnd(t.Space(2))
				text(fmt.Sprint(f.status)).TextColor(f.statusColor(t)).PopoverMenu(a.rowMenu(c, f))
			}
		case "client":
			if src, ok := a.clientIcon(f.process, f.processPath); ok {
				ui.Image(c, src).Size(16, 16)
			} else {
				clientBadge(c, f.process)
			}
			text(f.process).Padding(0, 0, 0, t.Space(2)).PopoverMenu(a.rowMenu(c, f)).TextColor(t.TextMuted)
		case "duration":
			text(fmt.Sprintf("%d ms", f.millis)).TextColor(t.TextMuted)
		case "size":
			text(humanBytes(f.bytes)).TextColor(t.TextMuted)
		case "captured":
			at := "—"
			if !f.at.IsZero() {
				at = f.at.Local().Format("15:04:05")
			}
			text(at).Font(uikit.FontMono).TextColor(t.TextMuted)
		default:
			cc, ok := a.customColumnAt(cols[col].ID)
			if !ok {
				return
			}
			value := cc.value(f)
			if value == "" {
				text("—").TextColor(t.TextMuted)
				return
			}
			cell := text(value).TextColor(t.Text).Tooltip(value)
			if cc.header() {
				cell.PopoverMenu(a.headerFilterMenu(c, cc.Name, []string{value}))
			}
		}
	}).Label("Captured requests").Grow(1)
	c.SetTheme(t)
	if table.Submitted() {
		a.workbench.inspector.Focus(c)
	}
	a.columns = state.Columns
}

var noteIcon = lucide.MessageSquare()

// highlightColor is the color a flow is marked with.
func highlightColor(name string) (ui.Color, bool) {
	for _, c := range uikit.MarkColors {
		if c.Name == name {
			return c.Color, true
		}
	}
	return ui.Color{}, false
}

// openMark fills the mark dialog from a flow.
func (a *app) openMark(f flow) {
	a.markID = f.id
	a.markColor = f.color
	a.markNote = f.note
	a.markOpen = true
}

// markModal marks a flow with a color and a note, the two things a person adds
// to traffic while looking for the request that misbehaves.
func (a *app) markModal(c *ui.Context, t *ui.Theme) {
	if !a.markOpen {
		return
	}
	uikit.Modal(c, &a.markOpen, func() {
		uikit.DialogColumn(c, uikit.DialogSmall).Children(func() {
			uikit.DialogTitle(c, "Mark the request")
			ui.Scroll(c).MinHeight(0).Gap(t.Space(3)).Children(func() {
				uikit.Meta(c, "A color shows in the table beside the method, and the note in the URL cell and the detail header.")
				ui.Row(c).Gap(t.Space(2)).Wrap().AlignItems(ui.Center).Children(func() {
					for _, color := range uikit.MarkColors {
						selected := a.markColor == color.Name
						uikit.SwatchButton(c, "Mark "+color.Name, color.Color, selected).Tooltip(color.Name).
							OnClick(func() { a.markColor = color.Name })
					}
					if uikit.SecondaryButton(c, "None").Clicked() {
						a.markColor = ""
					}
				})
				ui.TextArea(c, &a.markNote).Height(90).Label("Note").FontSize(uikit.TextBody).Radius(uikit.RadiusControl)
			})
			uikit.DialogFooter(c).Children(func() {
				if uikit.SecondaryButton(c, "Cancel").Clicked() {
					a.markOpen = false
				}
				if uikit.PrimaryButton(c, "Save").Clicked() {
					a.saveMark()
				}
			})
		})
	})
}

// saveMark writes the dialog's color and note onto the flow.
func (a *app) saveMark() {
	for i := range a.flows {
		if a.flows[i].id == a.markID {
			a.flows[i].color = a.markColor
			a.flows[i].note = strings.TrimSpace(a.markNote)
			break
		}
	}
	a.markOpen = false
	a.applyFilter()
	a.invalidate()
	a.setMessage("Marked")
}

// infoIcon marks a header the Headers tab can explain, or a connection detail
// worth opening.
var infoIcon = lucide.Info()

func (a *app) explainConnection(c *ui.Context, f flow) {
	t := c.Theme()
	e := f.conn.explanation()
	width, height := c.Size()
	ui.Column(c).Width(min(480, width-40)).Children(func() {
		uikit.Heading(c, e.title).Padding(t.Space(4), t.Space(4), t.Space(3), t.Space(4))
		ui.Scroll(c).Height(min(270, height-220)).Padding(0, t.Space(4), t.Space(3), t.Space(4)).Gap(t.Space(4)).Selectable().Children(func() {
			uikit.Hint(c, e.summary)
			ui.Column(c).Padding(t.Space(3)).Gap(t.Space(2)).Radius(uikit.RadiusPanel).Background(t.Danger.Alpha(0.06)).Border(1, t.Danger.Alpha(0.2)).Children(func() {
				uikit.Label(c, "Observed failure · "+cmp.Or(f.conn.errorStage, "unknown stage"))
				ui.Text(c, f.conn.error).Font(uikit.FontMono).FontSize(uikit.TextMeta).TextColor(t.Danger)
			})
			ui.Column(c).Gap(t.Space(2)).Children(func() {
				ui.Text(c, "Possible causes · not confirmed").FontSize(uikit.TextBody).Bold()
				uikit.Hint(c, e.causes)
			})
			ui.Column(c).Gap(t.Space(2)).Children(func() {
				ui.Text(c, "What to try").FontSize(uikit.TextBody).Bold()
				uikit.Body(c, e.steps)
			})
		})
		ui.Divider(c)
		ui.Row(c).Padding(t.Space(3), t.Space(4)).Gap(t.Space(4)).Children(func() {
			uikit.Meta(c, "Review URLs, addresses and raw errors before sharing.").Grow(1)
			if uikit.SecondaryButton(c, "Copy diagnostics").Clicked() {
				c.Services().WriteClipboard(f.connectionDiagnostics())
				a.setMessage("Copied connection diagnostics")
			}
		})
	})
}

// connectionView makes transport failures inspectable and copyable, including
// failures after a successful CONNECT but before any HTTP request was received.
func (a *app) connectionView(c *ui.Context, f flow) {
	t := c.Theme()
	ui.Row(c).Padding(t.Space(2), t.Space(3)).Gap(t.Space(2)).Children(func() {
		if f.conn.error != "" {
			ui.Text(c, cmp.Or(f.conn.errorStage, "Connection")+" failed").FontSize(uikit.TextBody).TextColor(t.Danger).Grow(1)
		} else {
			uikit.Hint(c, "Connection log · snapshot at capture time").Grow(1)
		}
		if uikit.SecondaryButton(c, "Copy connection log").Height(uikit.HeightCompact).Disabled(f.conn.log == "").Clicked() {
			c.Services().WriteClipboard(f.conn.log)
			a.setMessage("Copied connection log")
		}
	})
	if f.conn.error != "" {
		ui.Text(c, f.conn.error).Selectable().MaxLines(3).Tooltip(f.conn.error).FontSize(uikit.TextBody).TextColor(t.Danger).Padding(0, t.Space(3), t.Space(2))
	}
	text := cmp.Or(f.conn.log, "No connection history was recorded for this exchange (for example, an older or imported session).")
	ui.TextArea(c, &text).ReadOnly(true).Label("Connection log").Font(uikit.FontMono).FontSize(uikit.TextBody).Grow(1).FillWidth()
}

// connPanel is the connection behind a flow: who talked to the proxy, where it
// dialled, the addresses the name resolved to, and what TLS agreed on.
func (a *app) connPanel(c *ui.Context, t *ui.Theme, f flow) {
	cn := f.conn
	ui.Column(c).Width(440).Gap(t.Space(0.5)).Padding(t.Space(1)).Children(func() {
		ui.Text(c, "Connection").FontSize(uikit.TextBody).Bold().TextColor(t.Text).Padding(0, 0, t.Space(2), 0)
		field(c, t, "HTTP version", cn.proto)
		field(c, t, "Client", cn.client)
		field(c, t, "Proxy", cn.proxy)
		field(c, t, "Upstream", cn.server)
		if cn.error != "" {
			field(c, t, "Failed at", cn.errorStage+": "+cn.error)
		}
		if cn.flows > 0 {
			field(c, t, "Connection use", fmt.Sprintf("request #%d · reused: %t", cn.flows, cn.reused))
		}
		resolved := cn.resolved
		if resolved != "" {
			resolved = fmt.Sprintf("%s  (%d ms)", resolved, cn.lookupMs)
		}
		field(c, t, "Resolved", resolved)
		tls := cn.tls
		if cn.cipher != "" {
			if tls != "" {
				tls += " · "
			}
			tls += cn.cipher
		}
		field(c, t, "TLS", tls)
		cert := cn.certCN
		if cn.issuer != "" {
			cert = fmt.Sprintf("%s, issued by %s", cert, cn.issuer)
		}
		if cn.certUntil != "" {
			cert += ", until " + cn.certUntil
		}
		field(c, t, "Certificate", strings.TrimPrefix(cert, ", "))
	})
}

// field is one label and value of the connection panel, left out when the flow
// has nothing to say about it.
func field(c *ui.Context, t *ui.Theme, label, value string) {
	if value == "" {
		return
	}
	ui.Row(c).Gap(t.Space(2)).Padding(t.Space(0.5), 0).Children(func() {
		uikit.Meta(c, label).Width(96)
		ui.Text(c, value).FontSize(uikit.TextBody).Font(uikit.FontMono).TextColor(t.Text).Grow(1)
	})
}

// headersView shows the request and the response headers row by row, each
// standard one with a note on what it is for, and the status line with what
// the code means.
// headerBlock is one side of the Headers tab: the line that starts the message
// and then its headers, sorted by name.
func (a *app) headerBlock(c *ui.Context, t *ui.Theme, title, line, note string, headers http.Header, raw string, trailers http.Header) {
	ui.Column(c).FillWidth().Padding(t.Space(2), t.Space(3)).Gap(t.Space(2)).Background(uikit.Colors(t).Panel).Children(func() {
		ui.Row(c).AlignItems(ui.Center).Children(func() {
			ui.Text(c, title).FontSize(uikit.TextBody).Bold()
			ui.Spacer(c)
			if len(headers) > 0 {
				uikit.Meta(c, fmt.Sprintf("%d headers", len(headers)))
			}
		})
		uikit.Meta(c, line).Font(uikit.FontMono).SingleLine().Selectable().Tooltip(strings.TrimSpace(line + "\n" + note))
	})
	ui.Divider(c)
	side := 0
	if title == "Response" {
		side = 1
	}
	ui.Scroll(c).TrackScroll(&a.headerScroll[side]).Grow(1).FillWidth().Children(func() {
		for i, block := range []http.Header{headers, trailers} {
			if i == 1 && len(block) > 0 {
				uikit.Label(c, "Trailers").Padding(t.Space(2), t.Space(3), t.Space(2)).FillWidth().Background(uikit.Colors(t).Panel)
			}
			if i == 0 && len(block) == 0 {
				if raw == "" {
					uikit.Hint(c, "No headers recorded").Padding(t.Space(3))
				} else {
					ui.Text(c, raw).FontSize(uikit.TextBody).Font(uikit.FontMono).TextColor(t.Text).Selectable().Padding(t.Space(3))
				}
			}
			for _, key := range slices.Sorted(maps.Keys(block)) {
				value := strings.Join(block[key], "\n")
				ui.Row(c).Gap(t.Space(3)).Padding(t.Space(2), t.Space(3)).AlignItems(ui.Start).FillWidth().Children(func() {
					uikit.Meta(c, key).Font(uikit.FontMono).Width(124).SingleLine().Tooltip(strings.TrimSpace(key + "\n" + headerDoc(key)))
					ui.Text(c, value).FontSize(uikit.TextBody).Font(uikit.FontMono).TextColor(t.Text).Grow(1).Selectable()
				})
				ui.Divider(c).Background(t.Border.Alpha(0.45))
			}
		}
	})
}

// messages lists the WebSocket messages or SSE events of a flow.
func (a *app) messages(c *ui.Context, f flow) {
	a.messageUI.view(c, a, f, a.frames[f.id])
}

func shortenHome(path string) string {
	if home, err := os.UserHomeDir(); err == nil {
		if rest, ok := strings.CutPrefix(path, home); ok {
			return "~" + rest
		}
	}
	return path
}

func (f flow) statusColor(t *ui.Theme) ui.Color {
	if f.status < 400 && f.failed() {
		return t.Danger
	}
	return uikit.StatusColor(f.status, t)
}

// Run starts the native desktop app and releases its resources on exit.
func Run() error {
	a := newApp()
	if a.srv != nil {
		defer a.srv.Close()
	}
	cleanupProxy := func() {
		if err := a.systemProxy.close(); err != nil {
			log.Printf("release system proxy: %v", err)
		}
	}
	defer cleanupProxy() // before the listener closes, even if Run returns an error
	mygo.App.OnBeforeQuit(a.beforeProxyQuit)
	mygo.App.OnQuit(cleanupProxy) // native termination may not return from Run
	mygo.App.OnQuit(a.experiments.close)
	defer a.experiments.close()
	cancelRequests := func() {
		for _, d := range a.documents {
			d.cancelSend()
		}
	}
	mygo.App.OnQuit(cancelRequests)
	defer cancelRequests()
	mygo.App.OnQuit(func() { a.socket.session.disconnect() })
	defer func() { a.socket.session.disconnect() }()
	mygo.App.OnQuit(a.investigations.close)
	mygo.App.OnQuit(a.board.flush)
	defer a.investigations.close()
	defer a.board.flush()
	defer a.discardRuleDraft()
	defer a.disableDevices()
	defer a.views.stopSearch()
	defer a.messageUI.stopSearch()
	defer a.protobuf.stop()
	defer func() {
		if a.runner.cancel != nil {
			a.runner.cancel()
		}
	}()
	mygo.App.WhenReady(func() {
		a.win = mygo.NewWindow(mygo.WindowOptions{
			Title:           "vscroxy",
			Width:           1280,
			Height:          800,
			MinWidth:        960,
			MinHeight:       600,
			AutoHideMenuBar: true,
			// The view draws the title bar under the window controls, so the
			// toolbar and the traffic share one continuous surface.
			TitleBarStyle:        mygo.TitleBarHidden,
			TrafficLightPosition: &mygo.Point{X: 16, Y: 17},
			// MyGo keeps the window's bounds under this key.
			StateKey: "vscroxy",
			Content:  ui.View(a.view),
		})
		a.win.OnClose(func(e *mygo.CloseEvent) {
			if !a.systemProxy.quitReady {
				e.PreventDefault()
				mygo.App.Quit()
			}
		})
		mygo.App.SetMenu(a.menu())
		a.checkSystemProxy(true) // retry only a recorded, interrupted proxy session
		go a.startCapture()
	})
	return mygo.App.Run()
}

// clientIcon returns the app's icon, loading it the first time it is asked
// for. A process with no icon gets none, and the table shows a badge.
func (a *app) clientIcon(process, path string) (ui.ImageSource, bool) {
	if process == "" && path == "" {
		return nil, false
	}
	key := cmp.Or(path, process)
	if src, ok := a.icons[key]; ok {
		return src, src != nil
	}
	src := loadIcon(process, path)
	a.icons[key] = src
	return src, src != nil
}

// loadIcon decodes an app's icon: an SVG as an SVG, anything else as a
// bitmap.
func loadIcon(process, path string) ui.ImageSource {
	icon, ok := appicon.Lookup(process, path)
	if !ok {
		return nil
	}
	if icon.SVG {
		svg, err := ui.ParseSVG(icon.Data)
		if err != nil {
			return nil
		}
		return svg
	}
	bmp, err := ui.DecodeBitmap(icon.Data)
	if err != nil {
		return nil
	}
	return bmp
}

// clientBadge draws the app's initial in its color, for an app whose icon the
// system does not have.
func clientBadge(c *ui.Context, name string) {
	letter := "?"
	if runes := []rune(name); len(runes) > 0 {
		letter = strings.ToUpper(string(runes[0]))
	}
	ui.Box(c).Size(16, 16).Radius(uikit.RadiusControl).Background(badgeColor(name)).Center().Children(func() {
		ui.Text(c, letter).FontSize(uikit.TextMeta).Bold().TextColor(uikit.SwatchText)
	})
}

// badgeColor is a stable color per app name.
func badgeColor(name string) ui.Color {
	h := fnv.New32a()
	_, _ = h.Write([]byte(name))
	return uikit.BadgeColors[int(h.Sum32())%len(uikit.BadgeColors)]
}

// rowMenu is the context menu of a flow's row: what a captured request is
// worth once it leaves the table.
func (a *app) rowMenu(c *ui.Context, f flow) func(*ui.Menu) {
	services := c.Services()
	return func(m *ui.Menu) {
		// The request's own next steps come first, then copying and finding
		// related traffic, then how this row is kept, then the tools that
		// change future traffic like it, as Proxyman's Tools submenu does.
		actions := a.requestActions(c, f, true)
		desktopMenuItem(m, actions, "replay", "Replay")
		desktopMenuItem(m, actions, "edit", "Edit & resend")
		if m.Item("Save as request").Disabled(f.kind == "connect" || f.method == "CONNECT").Chosen() {
			a.saveCapture(f)
		}
		if chosen := a.selectedFlows(); len(chosen) > 1 && m.Item(fmt.Sprintf("Save %d requests to collection…", len(chosen))).Chosen() {
			a.openCollectionSave(chosen)
		}
		m.Separator()
		m.Submenu("Copy", func(m *ui.Menu) {
			if m.Item("URL").Chosen() {
				services.WriteClipboard(f.url)
				a.setMessage("Copied URL")
			}
			desktopMenuItem(m, actions, "copy", "cURL")
			if m.Item("Response body").Disabled(f.resBody == "").Chosen() {
				services.WriteClipboard(f.body(true))
				a.setMessage("Copied response body")
			}
		})
		if f.kind == "" && f.url != "" && m.Item("Open in browser").Chosen() {
			services.OpenURL(f.url)
		}
		m.Submenu("Filter captures", func(m *ui.Menu) {
			// Sync the conditions on the next frame; changing visible rows
			// while the table is building would invalidate its row indices.
			if m.Item("Host: " + f.host).Disabled(f.host == "").Chosen() {
				a.filterTriage.Host = f.host
				c.AnimationFrame()
			}
			if m.Item("App: " + cmp.Or(f.process, "unidentified")).Disabled(f.process == "").Chosen() {
				a.filterTriage.Client = f.process
				c.AnimationFrame()
			}
			if m.Item("Method: " + f.method).Disabled(f.method == "").Chosen() {
				a.filterTriage.Method = f.method
				c.AnimationFrame()
			}
			// A CONNECT status acknowledges a tunnel; it is not the status
			// of an HTTP response from the destination.
			if f.kind != "connect" && f.conn.error == "" && f.status >= 100 && f.status <= 599 {
				if m.Item(fmt.Sprintf("Status: %d", f.status)).Chosen() {
					a.filterStatus = strconv.Itoa(f.status)
					c.AnimationFrame()
				}
			}
		})
		m.Separator()
		if m.Item(cmp.Or(pinLabel(f), "Pin")).Chosen() {
			a.togglePin(f)
		}
		if m.Item("Mark…").Chosen() {
			a.openMark(f)
		}
		m.Submenu("Tools", func(m *ui.Menu) {
			for _, kind := range ruleTypes {
				label := ruleLabel(kind) + "…"
				if kind == capture.RuleMapLocal {
					label = "Mock this response…"
				}
				if m.Item(label).Disabled(f.url == "" || f.method == "CONNECT").Chosen() {
					a.ruleFromFlow(f, kind)
				}
			}
			m.Separator()
			if m.Item("Map this host…").Chosen() {
				a.openDNSRule(hostOnly(f.host), f.process)
			}
			m.Submenu("HTTPS inspection", func(m *ui.Menu) {
				a.appInspectionMenu(m, capture.SSLApp{Name: f.process, Path: f.processPath})
				if m.Item("View HTTPS rules…").Chosen() {
					a.sslOpen = true
				}
			})
		})
		m.Separator()
		if m.Item("Add to investigation…").Chosen() {
			a.addToInvestigation(f)
		}
		if m.Item("Save as regression…").Disabled(f.status == 0 || f.method == "CONNECT" || f.kind == "ws").Chosen() {
			a.regressionFrom(f)
		}
	}
}

// openCompose shows the request editor with an empty request.
func (a *app) openCompose() {
	a.addDocument(savedRequest{Method: "GET", URL: "https://", Auth: request.Auth{Type: request.AuthInherit}}, "")
}

// composeFrom fills the request editor with a captured request, the step from
// traffic to a request worth keeping.
func (a *app) composeFrom(f flow) {
	r, err := f.requestDraft()
	if err != nil {
		a.setMessage("Edit & replay: " + err.Error())
		return
	}
	a.views.capture(a)
	a.addDocument(r, "")
	a.setDebugSource(newDebugSource(f))
	a.sourceView = a.views.activeView()
}

// envNames are the environments the editor offers, sorted.
func (a *app) envNames() []string {
	names := make([]string, 0, len(a.envs))
	for _, e := range a.envs {
		names = append(names, e.Name)
	}
	slices.Sort(names)
	return names
}

// activeVars are the variables of the environment in use, which a request
// fills its {{name}} from.
func (a *app) activeVars() map[string]string {
	for _, e := range a.envs {
		if e.Name == a.activeEnv {
			return maps.Clone(e.Vars)
		}
	}
	return map[string]string{}
}

// applyVars merges only the variables changed by this run. Another tab may
// have completed or the user may have edited the environment in the meantime.
func (a *app) applyVars(name string, before, vars map[string]string) {
	if a.envsProblem != "" {
		a.setMessage("Variables not saved: " + a.envsProblem)
		return
	}
	name = cmp.Or(name, "default")
	at := slices.IndexFunc(a.envs, func(e environment) bool { return e.Name == name })
	if at < 0 {
		a.envs = append(a.envs, environment{Name: name, Vars: map[string]string{}})
		at = len(a.envs) - 1
	}
	if a.envs[at].Vars == nil {
		a.envs[at].Vars = map[string]string{}
	}
	for key, value := range vars {
		if old, ok := before[key]; !ok || value != old {
			a.envs[at].Vars[key] = value
		}
	}
	for key := range before {
		if _, ok := vars[key]; !ok {
			delete(a.envs[at].Vars, key) // pm.environment.unset
		}
	}
	if a.activeEnv == "" {
		a.activeEnv = name
	}
	if err := saveEnvs(a.envsPath, envFile{Active: a.activeEnv, Envs: a.envs, Globals: a.globals}); err != nil {
		a.setMessage("Variables: " + err.Error())
	}
}

// openVars fills the variables editor with the environment in use.
func (a *app) openVars() {
	a.editEnvironment(cmp.Or(a.activeEnv, "default"))
	a.varsOpen = true
}

func (a *app) editEnvironment(name string) {
	a.varsGlobals = false
	a.varsName, a.varsChoice, a.varsProblem = name, name, ""
	a.varsRows = nil
	for _, env := range a.envs {
		if env.Name == name {
			a.varsRows = fieldsOf(env.Vars)
		}
	}
}

// editGlobals puts the global variables, beneath every environment, in the
// Variables dialog.
func (a *app) editGlobals() {
	a.varsGlobals, a.varsProblem, a.varsRows = true, "", fieldsOf(a.globals)
}

func fieldsOf(vars map[string]string) []request.Field {
	var rows []request.Field
	for _, key := range slices.Sorted(maps.Keys(vars)) {
		rows = append(rows, request.Field{Name: key, Value: vars[key]})
	}
	return rows
}

// varsModal edits the variables of one environment, the one a request fills its
// {{name}} from.
func (a *app) varsModal(c *ui.Context, t *ui.Theme) {
	if !a.varsOpen {
		return
	}
	_, height := c.Size()
	uikit.Modal(c, &a.varsOpen, func() {
		uikit.DialogColumn(c, uikit.DialogMedium).Height(min(560, height-80)).Children(func() {
			uikit.DialogTitle(c, "Variables")
			ui.Scroll(c).Grow(1).MinHeight(0).FillWidth().Gap(t.Space(3)).Children(func() {
				if a.envsProblem != "" {
					ui.Text(c, "Variables are read-only. Close this dialog, repair or move env.json aside, then reload local assets.").FontSize(uikit.TextMeta).TextColor(t.Danger)
				}
				scope := 0
				if a.varsGlobals {
					scope = 1
				}
				if uikit.Segmented(c, &scope, "Environments", "Globals").Changed() {
					if scope == 1 {
						a.editGlobals()
					} else {
						a.editEnvironment(cmp.Or(a.activeEnv, "default"))
					}
				}
				if a.varsGlobals {
					uikit.Meta(c, "Every request reads these, whatever the environment. Collection, folder and environment values override them. Stored locally in plaintext; keep secrets in the Vault.")
				} else {
					uikit.Meta(c, "Use {{name}} in requests. Environment values override collection values and globals. Stored locally in plaintext; keep secrets in the Vault.")
					ui.Row(c).Gap(t.Space(2)).Children(func() {
						if names := a.envNames(); len(names) > 0 {
							if uikit.Select(c, &a.varsChoice, names).Label("Edit environment").Grow(1).MinWidth(0).Changed() {
								a.editEnvironment(a.varsChoice)
							}
						}
						if uikit.SecondaryButton(c, "New environment").Clicked() {
							a.editEnvironment("")
						}
					})
					uikit.Input(c, &a.varsName).Label("Environment")
				}
				ui.Column(c).Height(224).Border(1, t.Border).Radius(uikit.RadiusControl).Children(func() {
					ui.Row(c).Height(uikit.HeightControl).AlignItems(ui.Center).BorderWidth(0, 0, 1, 0).BorderColor(t.Border).Children(func() {
						uikit.Meta(c, "NAME").Basis(0).Grow(1).Padding(0, t.Space(2))
						uikit.Meta(c, "VALUE").Basis(0).Grow(2).Padding(0, t.Space(2))
						ui.Box(c).Width(56)
					})
					ui.Scroll(c).Grow(1).MinHeight(0).FillWidth().Children(func() {
						remove := -1
						for i := range a.varsRows {
							r := &a.varsRows[i]
							row := ui.Row(c).Key(i).Height(uikit.HeightControl).AlignItems(ui.Center).BorderWidth(0, 0, 1, 0).BorderColor(t.Border)
							row.Children(func() {
								ui.Box(c).Basis(0).Grow(1).MinWidth(0).FillHeight().BorderWidth(0, 1, 0, 0).BorderColor(t.Border).Children(func() {
									field := requestGridInput(c, &r.Name, "base_url", fmt.Sprintf("Variable name %d", i+1)).ReadOnly(a.envsProblem != "")
									cellBorder(t, field, false, false)
								})
								ui.Box(c).Basis(0).Grow(2).MinWidth(0).FillHeight().Children(func() {
									value := requestGridInput(c, &r.Value, "Value", fmt.Sprintf("Variable value %d", i+1)).ReadOnly(a.envsProblem != "")
									if !r.Revealed {
										value.Password()
									}
									cellBorder(t, value, !r.Revealed, r.Revealed)
								})
								if uikit.IconButton(c, fmt.Sprintf("Reveal variable %d", i+1), "Reveal or hide variable value", eyeIcon).Clicked() {
									r.Revealed = !r.Revealed
								}
								ui.Row(c).Disabled(a.envsProblem != "").Children(func() {
									button := uikit.IconButton(c, fmt.Sprintf("Remove variable %d", i+1), "Remove variable", closeIcon)
									if !row.Hovered() && !row.FocusWithin() {
										button.Opacity(0)
									}
									if button.Clicked() {
										remove = i
									}
								})
							})
						}
						if remove >= 0 {
							a.varsRows = slices.Delete(a.varsRows, remove, remove+1)
						}
						ui.Row(c).Disabled(a.envsProblem != "").Children(func() {
							if uikit.ToolbarButton(c, "Add variable", "Add variable", false).Children(func() {
								ui.Icon(c, plusIcon).Size(uikit.IconInline, uikit.IconInline)
								uikit.Body(c, "Add variable")
							}).Clicked() {
								a.varsRows = append(a.varsRows, request.Field{})
							}
						})
					})
				})
				if a.varsProblem != "" {
					ui.Text(c, a.varsProblem).FontSize(uikit.TextMeta).TextColor(t.Danger)
				}
			})
			uikit.DialogFooter(c).Children(func() {
				if uikit.SecondaryButton(c, "Export as Postman…").Tooltip("Writes the rows shown here, saved or not, in Postman's environment format").Clicked() {
					a.exportPostmanVariables()
				}
				ui.Spacer(c)
				if uikit.SecondaryButton(c, "Cancel").Clicked() {
					a.varsOpen = false
				}
				if uikit.PrimaryButton(c, "Save").Disabled(a.envsProblem != "").Clicked() {
					a.saveVars()
				}
			})
		})
	})
}

// saveVars keeps the edited environment and puts it in use.
func (a *app) saveVars() {
	if a.envsProblem != "" {
		a.setMessage("Variables not saved: " + a.envsProblem)
		return
	}
	name := strings.TrimSpace(a.varsName)
	if name == "" && !a.varsGlobals {
		a.varsProblem = "The environment needs a name."
		return
	}
	vars := map[string]string{}
	for _, row := range a.varsRows {
		key := strings.TrimSpace(row.Name)
		if key == "" && row.Value == "" {
			continue
		}
		if _, duplicate := vars[key]; key == "" || duplicate || strings.ContainsAny(key, "{}") {
			a.varsProblem = "Variable names must be nonempty, unique and contain no braces."
			return
		}
		vars[key] = row.Value // Empty and whitespace values are significant.
	}
	if a.varsGlobals {
		if err := saveEnvs(a.envsPath, envFile{Active: a.activeEnv, Envs: a.envs, Globals: vars}); err != nil {
			a.varsProblem = "Save failed: " + err.Error()
			return
		}
		a.globals, a.varsOpen = vars, false
		a.setMessage(fmt.Sprintf("Globals: %d of them", len(vars)))
		return
	}
	envs := slices.Clone(a.envs)
	found := false
	for i, e := range envs {
		if e.Name == name {
			envs[i].Vars = vars
			found = true
			break
		}
	}
	if !found {
		envs = append(envs, environment{Name: name, Vars: vars})
	}
	if err := saveEnvs(a.envsPath, envFile{Active: name, Envs: envs, Globals: a.globals}); err != nil {
		a.varsProblem = "Save failed: " + err.Error()
		return
	}
	a.envs, a.activeEnv = envs, name
	a.varsOpen = false
	a.setMessage(fmt.Sprintf("Variables: %s, %d of them", name, len(vars)))
}

// sendComposed sends the editor's request through the proxy, so the result
// lands in the table with the rest of the traffic. Variables fill it in, the
// pre-request script may change it, and the post-response script sees what came
// back and asserts on it.
func (a *app) sendComposed() {
	d := a.requestDocument
	if d == nil || d.composeWaiting {
		return
	}
	if a.srv == nil || !a.srv.Running() {
		a.setMessage("No proxy running. Start capture before sending; previous result kept.")
		return
	}
	d.composeTests = nil
	d.composeReplies = nil
	d.console = nil
	d.historyPick = 0 // The new response replaces a retained run on screen.
	d.response = nil
	d.responseEditor = nil
	// A send replaces the response, so the comparison of the previous one
	// has nothing left to stand on.
	d.comparing = false
	d.sendOutcome, d.sendCancelling, d.sendDetails = "", false, false
	d.composeWaiting, d.composeResult = true, "Sending…"
	r, env, vars, srv, jar := d.draft(), a.activeEnv, a.activeVars(), a.srv, a.requestCookies()
	in := a.sendContext(r.Request)
	d.sent, d.sentAt, d.sentSet = r, time.Now(), true
	runKey, draftKey, origin := uuid.New().String(), d.debugKey, d.origin
	d.sentRun = runKey
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	d.sendCancel = cancel
	ctx = request.WithRunKey(ctx, runKey)
	if r.Checks != (request.Checks{}) {
		d.responseTab = resViewTests
	}
	go func() {
		defer cancel()
		got := r.Execute(ctx, srv, in, vars, jar)
		result := requestResult{document: d, environment: env, summary: runSummary(got), outcome: got.Outcome(), response: got.Response, tests: got.Tests, replies: got.Replies, console: got.Console, varsBefore: vars, vars: got.Vars, writes: got.Writes}
		record := snapshotExperiment(0, r, env, got)
		record.RunKey, record.DraftKey, record.Source = runKey, draftKey, origin
		result.experiment = &record
		if got.Request != nil {
			result.history = &historyEntry{Method: got.Request.Method, URL: got.Request.URL, Headers: r.Headers, Body: r.Body, Draft: &r, At: time.Now()}
		}
		a.mu.Lock()
		a.requestResults = append(a.requestResults, result)
		a.mu.Unlock()
		a.invalidate()
	}()
}

// plural picks the word for a count.
func plural(n int, one, many string) string {
	if n == 1 {
		return one
	}
	return many
}

// openKeyLog fills the key log dialog with what is set now.
func (a *app) openKeyLog() {
	a.keyLogInput = a.keyLogPath
	a.keyLogOpen = true
}

// keyLogModal turns the TLS key log on or off: the secrets go to a file
// Wireshark reads, which is how it decrypts what this app captured.
func (a *app) keyLogModal(c *ui.Context, t *ui.Theme) {
	if !a.keyLogOpen {
		return
	}
	uikit.Modal(c, &a.keyLogOpen, func() {
		uikit.DialogColumn(c, uikit.DialogMedium).Children(func() {
			uikit.DialogTitle(c, "TLS key log")
			ui.Scroll(c).MinHeight(0).Gap(t.Space(3)).Children(func() {
				uikit.Meta(c, "The secrets go to a file Wireshark reads (SSLKEYLOGFILE), so it can decrypt this capture. The engine reads the variable once, when it first handshakes, so a change here takes effect from the next launch.")
				uikit.Input(c, &a.keyLogInput).Placeholder("~/.vscroxy/keys.log").Label("File")
			})
			uikit.DialogFooter(c).Children(func() {
				if a.keyLogPath != "" && uikit.SecondaryButton(c, "Turn off").Clicked() {
					a.setKeyLog("")
					a.keyLogOpen = false
				}
				if uikit.SecondaryButton(c, "Cancel").Clicked() {
					a.keyLogOpen = false
				}
				if uikit.PrimaryButton(c, "Save").Clicked() {
					a.setKeyLog(strings.TrimSpace(a.keyLogInput))
					a.keyLogOpen = false
				}
			})
		})
	})
}

// setKeyLog keeps the file the secrets go to, and sets the variable the engine
// reads, which is too late for this run.
func (a *app) setKeyLog(path string) {
	if path != "" {
		if home, err := os.UserHomeDir(); err == nil {
			if rest, ok := strings.CutPrefix(path, "~/"); ok {
				path = filepath.Join(home, rest)
			}
		}
	}
	a.keyLogPath = path
	if path == "" {
		_ = os.Unsetenv("SSLKEYLOGFILE")
		a.setMessage("TLS key log off")
	} else {
		_ = os.Setenv("SSLKEYLOGFILE", path)
		a.setMessage("TLS keys go to " + path + " from the next launch")
	}
	a.persistSettings()
	a.invalidate()
}

// filtersActive includes every condition that can hide captured requests.
func (a *app) filtersActive() bool {
	return a.query != "" || a.filter != "" && a.filter != "all" || a.chip != 0 ||
		a.filterStatus != "" || a.filterHeader != "" || a.views.bodyFilter() != "" || a.filterTriage != (captureTriage{})
}

// filterSummary is what the status bar says about the filters in use.
func (a *app) filterSummary() string {
	var parts []string
	if a.query != "" {
		parts = append(parts, "URL "+a.query)
	}
	if a.filter != "" && a.filter != "all" {
		parts = append(parts, a.filter)
	}
	if a.chip != 0 {
		parts = append(parts, captureKinds[a.chip])
	}
	if a.filterStatus != "" {
		parts = append(parts, "status "+a.filterStatus)
	}
	if a.filterHeader != "" {
		parts = append(parts, "header "+a.filterHeader)
	}
	if body := a.views.bodyFilter(); body != "" {
		parts = append(parts, "body "+body)
	}
	parts = append(parts, a.filterTriage.summary()...)
	return strings.Join(parts, " · ")
}

// openFilter shows the content filter with what it already holds.
func (a *app) openFilter() {
	a.views.openFilter(a)
}

// clearFilters drops the content filter.
func (a *app) clearFilters() {
	a.views.clearFilters(a)
}

// statusMatches accepts comma-separated alternatives; invalid input never
// silently turns into a partial filter.
func statusMatches(expr string, status int) bool {
	if strings.TrimSpace(expr) == "" {
		return true
	}
	matched := false
	for part := range strings.SplitSeq(expr, ",") {
		lo, hi, ok := statusBounds(part)
		if !ok {
			return false
		}
		matched = matched || status >= lo && status <= hi
	}
	return matched
}

// statusBounds is shared by the filter validator and matcher.
func statusBounds(expr string) (lo, hi int, valid bool) {
	expr = strings.ToLower(strings.TrimSpace(expr))
	code := func(s string) (int, bool) {
		n, err := strconv.Atoi(strings.TrimSpace(s))
		return n, err == nil && n >= 100 && n <= 599
	}
	switch {
	case strings.HasPrefix(expr, ">="):
		n, ok := code(expr[2:])
		return n, 599, ok
	case strings.HasPrefix(expr, "<="):
		n, ok := code(expr[2:])
		return 100, n, ok
	case strings.HasPrefix(expr, ">"):
		n, ok := code(expr[1:])
		return n + 1, 599, ok
	case strings.HasPrefix(expr, "<"):
		n, ok := code(expr[1:])
		return 100, n - 1, ok
	case strings.HasSuffix(expr, "xx"):
		n, err := strconv.Atoi(strings.TrimSuffix(expr, "xx"))
		return n * 100, n*100 + 99, err == nil && n >= 1 && n <= 5
	case strings.Contains(expr, "-"):
		low, high, _ := strings.Cut(expr, "-")
		lo, a := code(low)
		hi, b := code(high)
		return lo, hi, a && b && lo <= hi
	default:
		n, ok := code(expr)
		return n, n, ok
	}
}

// headerMatches reports whether a flow carries the header a filter names, as
// "name" alone or "name: value" to match the value too.
func headerMatches(f flow, expr string) bool {
	name, value, hasValue := strings.Cut(expr, ":")
	name = strings.TrimSpace(name)
	if name == "" {
		return true
	}
	values := append(f.reqHeader.Values(name), f.respHeader.Values(name)...)
	if len(values) == 0 {
		return false
	}
	if !hasValue {
		return true
	}
	value = strings.ToLower(strings.TrimSpace(value))
	for _, have := range values {
		if strings.Contains(strings.ToLower(have), value) {
			return true
		}
	}
	return false
}

// sortVisible orders the shown rows; applyFilter restores selection by ID.
func (a *app) sortVisible() {
	if len(a.visible) < 2 {
		return
	}
	less := a.sortOrder()
	// Pinned flows stay at the top, whatever the column says.
	slices.SortStableFunc(a.visible, func(x, y int) int {
		px, py := a.flows[x].pinned, a.flows[y].pinned
		if px != py {
			if px {
				return -1
			}
			return 1
		}
		return less(x, y)
	})
}

// sortOrder compares two flows by the chosen column.
func (a *app) sortOrder() func(x, y int) int {
	desc := a.sort.Descending
	return func(x, y int) int {
		fx, fy := a.flows[x], a.flows[y]
		var c int
		switch a.sort.Column {
		case "id":
			c = cmp.Compare(fx.id, fy.id)
		case "captured":
			c = fx.at.Compare(fy.at)
		case "method":
			c = cmp.Compare(fx.method, fy.method)
		case "proto":
			c = cmp.Compare(fx.scheme, fy.scheme)
		case "status":
			c = cmp.Compare(fx.status, fy.status)
		case "host":
			c = cmp.Compare(fx.host, fy.host)
		case "url":
			c = cmp.Compare(fx.url, fy.url)
		case "client":
			c = cmp.Compare(fx.process, fy.process)
		case "path":
			c = cmp.Compare(fx.path, fy.path)
		case "size":
			c = cmp.Compare(fx.bytes, fy.bytes)
		case "time", "duration":
			c = cmp.Compare(fx.millis, fy.millis)
		default:
			if cc, ok := a.customColumnAt(a.sort.Column); ok {
				c = cmp.Compare(cc.value(fx), cc.value(fy))
			}
		}
		if desc {
			return -c
		}
		return c
	}
}

// hostListModal edits a list of hosts, one per line, and keeps it in a file
// the next start reads.
func (a *app) hostListModal(c *ui.Context, t *ui.Theme, open *bool, title, hint, path string, text *string, apply func([]string)) {
	if !*open {
		return
	}
	uikit.Modal(c, open, func() {
		uikit.DialogColumn(c, uikit.DialogMedium).Children(func() {
			uikit.DialogTitle(c, title)
			ui.Scroll(c).MinHeight(0).Gap(t.Space(3)).Children(func() {
				uikit.Meta(c, hint)
				ui.TextArea(c, text).Height(140).Label(title).FontSize(uikit.TextBody).Radius(uikit.RadiusControl)
			})
			uikit.DialogFooter(c).Children(func() {
				if uikit.SecondaryButton(c, "Cancel").Clicked() {
					*open = false
				}
				if uikit.PrimaryButton(c, "Save").Clicked() {
					patterns := splitHosts(*text)
					if path != "" {
						if err := writeHosts(path, patterns); err != nil {
							a.setMessage(title + ": " + err.Error())
							return
						}
					}
					apply(patterns)
					*open = false
					a.setMessage(fmt.Sprintf("%s: %d", title, len(patterns)))
				}
			})
		})
	})
}

// splitHosts reads a list of hosts, one per line, where a line starting with #
// is a comment.
func splitHosts(text string) []string {
	var patterns []string
	for _, line := range strings.Split(text, "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		patterns = append(patterns, line)
	}
	return patterns
}

// writeHosts writes the list where the next start reads it.
func writeHosts(path string, patterns []string) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	text := strings.Join(patterns, "\n")
	if text != "" {
		text += "\n"
	}
	return os.WriteFile(path, []byte(text), 0o644)
}

// saveComposed keeps the editor's request under its name, replacing one of
// the same name.
func (a *app) saveComposed() bool {
	name := strings.TrimSpace(a.composeName)
	if name == "" {
		name = strings.TrimSpace(a.composeMethod + " " + a.composeURL)
	}
	if name == "" {
		a.setMessage("Save: the request has no name")
		return false
	}
	r := a.requestDocument.value()
	if _, _, _, err := r.Checks.Parse(); err != nil {
		a.setMessage("Save: " + err.Error())
		return false
	}
	r.Name, r.Collection = name, cleanPath(r.Collection)
	r.Method, r.URL = strings.ToUpper(strings.TrimSpace(cmp.Or(r.Method, "GET"))), strings.TrimSpace(r.URL)
	for _, existing := range a.requests {
		if existing.Name == r.Name && existing.Collection == r.Collection && !(r.ID != "" && existing.ID == r.ID) && existing.key() != a.savedKey {
			a.setMessage("Save: another request has this name in this collection; rename it first. No asset replaced.")
			return false
		}
	}
	list := upsertRequest(slices.Clone(a.requests), r)
	for _, saved := range list {
		if saved.Name == r.Name && saved.Collection == r.Collection {
			r.ID = saved.ID
			break
		}
	}
	if a.historyPath != "" {
		if list, err := loadHistory(a.historyPath); err == nil {
			a.history = list
		}
	}
	if a.settingsPath != "" {
		saved, err := loadSettings(a.settingsPath)
		if err != nil {
			a.errText = err.Error()
		} else {
			a.saved = saved
			a.dark = saved.Dark
			if saved.SidebarWidth > 0 {
				a.sidebarWidth = saved.SidebarWidth
			}
			if saved.TablePercent > 0 {
				a.tablePercent = saved.TablePercent
			}
			a.columns = saved.Columns.layout()
			a.customColumns, a.hiddenColumns = saved.customColumns(), saved.HiddenColumns
		}
	}
	if err := a.persistRequests(list); err != nil {
		a.setMessage("Save: " + err.Error())
		return false
	}
	a.requests = list
	a.composeName = name
	a.composeCollection, a.composeMethod, a.composeURL = r.Collection, r.Method, r.URL
	a.assetID = r.ID
	a.baseline = a.requestDocument.value()
	a.savedKey = r.key()
	a.requestChoice = a.savedKey
	*a.collectionShown(r.Collection) = true
	a.setMessage("Saved " + name)
	return true
}

// openSaved fills the request editor from a saved request.
func (a *app) openSaved(key string) {
	for _, d := range a.documents {
		if d.savedKey == key {
			a.requestDocument = d
			d.revealTab = true
			a.requestChoice = key
			a.workspace, a.requestsSection = 1, 0
			return
		}
	}
	for i, r := range a.requests {
		if r.key() != key {
			continue
		}
		if r.ID == "" {
			r.ID = uuid.New().String()
			a.requests[i] = r
		}
		a.addDocument(r, r.key())
		return
	}
}

// matchesChip reports whether the flow belongs to the chip chosen above the
// table: a protocol, or a kind of body.
func (a *app) matchesChip(f flow) bool {
	json := strings.Contains(f.contentType(true), "json")
	switch a.chip {
	case 1: // HTTP
		return f.kind == "" && f.scheme == "http"
	case 2: // HTTPS
		return f.kind == "connect" || (f.kind == "" && f.scheme == "https")
	case 3: // WebSocket
		return f.kind == "ws"
	case 4: // SSE
		return f.kind == "sse"
	case 5: // JSON
		return json
	case 6: // Images
		return strings.HasPrefix(f.contentType(true), "image/")
	case 7: // HTML
		return strings.Contains(f.contentType(true), "html")
	}
	return true
}

// sslModal edits what the proxy decrypts.
func (a *app) sslModal(c *ui.Context, t *ui.Theme) {
	if !a.sslOpen {
		a.sslDraft = nil
		return
	}
	if a.sslDraft == nil {
		// The enable-rule toast must not cover this editor's Save/Cancel
		// buttons in a small window. Later save errors still remain visible.
		c.CloseToast("")
		cfg := a.inspectionConfig()
		a.sslDraft, a.sslDraftHosts = &cfg, a.sslHosts
	}
	draft := a.sslDraft
	_, height := c.Size()
	uikit.Modal(c, &a.sslOpen, func() {
		uikit.DialogColumn(c, uikit.DialogMedium).Height(min(510, height-80)).Children(func() {
			uikit.DialogTitle(c, "SSL proxying")
			ui.Scroll(c).Grow(1).MinHeight(0).FillWidth().Gap(t.Space(3)).Children(func() {
				uikit.Meta(c, "Enable a domain or a local executable. New connections only; clients must trust the CA. Enabling a rule does not prove decryption succeeded.")
				ui.Row(c).Gap(t.Space(2)).MinHeight(uikit.HeightControl).AlignItems(ui.Center).Children(func() {
					ui.Switch(c, &draft.All).Label("Decrypt every domain")
					ui.Text(c, "Decrypt every domain").FontSize(uikit.TextBody).TextColor(t.Text)
				})
				ui.Text(c, "Domains and subdomains").FontSize(uikit.TextBody).Bold()
				ui.TextArea(c, &a.sslDraftHosts).Height(100).Label("Decryption domains").FontSize(uikit.TextBody).Radius(uikit.RadiusControl)
				ui.Divider(c)
				ui.Text(c, "Local apps").FontSize(uikit.TextBody).Bold()
				uikit.Meta(c, "Exact executable paths, not app names or parent/Helper groups. Add from a captured request or an App's context menu.")
				if len(draft.Apps) == 0 {
					uikit.Hint(c, "No app rules enabled.")
				}
				remove := -1
				for i, app := range draft.Apps {
					ui.Row(c).Gap(t.Space(2)).AlignItems(ui.Center).Children(func() {
						ui.Column(c).Grow(1).MinWidth(0).Gap(t.Space(1)).Children(func() {
							uikit.Body(c, app.Name)
							uikit.Meta(c, app.Path).SingleLine().Tooltip(app.Path)
						})
						if uikit.SecondaryButton(c, "Remove").Label("Remove HTTPS app: " + app.Path).Clicked() {
							remove = i
						}
					})
				}
				if remove >= 0 {
					draft.Apps = slices.Delete(draft.Apps, remove, remove+1)
				}
			})
			uikit.DialogFooter(c).Children(func() {
				if uikit.SecondaryButton(c, "Cancel").Clicked() {
					a.sslOpen = false
				}
				if uikit.PrimaryButton(c, "Save").Clicked() {
					draft.Hosts = splitHosts(a.sslDraftHosts)
					if a.saveSSL(*draft) {
						a.sslOpen = false
					}
				}
			})
		})
	})
}

// saveSSL keeps the SSL proxying settings and hands them to the proxy. A
// connection already open keeps the choice it was made with.
func (a *app) saveSSL(cfg capture.SSLConfig) bool {
	if a.sslPath != "" {
		if err := os.MkdirAll(filepath.Dir(a.sslPath), 0o755); err != nil {
			a.setMessage("SSL proxying: " + err.Error())
			return false
		}
		data, err := json.Marshal(cfg)
		if err != nil {
			a.setMessage("SSL proxying: " + err.Error())
			return false
		}
		if err := os.WriteFile(a.sslPath, append(data, '\n'), 0o644); err != nil {
			a.setMessage("SSL proxying: " + err.Error())
			return false
		}
	}
	if a.srv != nil {
		a.srv.SetSSL(cfg)
	}
	a.sslAll, a.sslHosts = cfg.All, strings.Join(cfg.Hosts, "\n")
	a.sslApps = slices.Clone(cfg.Apps)
	if cfg.All {
		a.setMessage("SSL proxying: every domain")
	} else {
		a.setMessage(fmt.Sprintf("SSL proxying: %d domains, %d local apps · new connections only", len(cfg.Hosts), len(cfg.Apps)))
	}
	return true
}

// decryptHost adds a domain to what the proxy decrypts, the button under a
// tunnel.
func (a *app) decryptHost(host string) {
	host = strings.ToLower(strings.TrimSuffix(strings.Trim(hostOnly(host), "[]"), "."))
	cfg := a.inspectionConfig()
	if host != "" && !slices.Contains(cfg.Hosts, host) {
		cfg.Hosts = append(cfg.Hosts, host)
	}
	if a.saveSSL(cfg) {
		a.setMessage("Inspection enabled for " + host + " on all ports; reconnect the client and retry")
	}
}

// persistSettings writes what changed since the last write, at most every
// 400ms, so dragging a divider does not write a file a frame.
func (a *app) persistSettings() error {
	now := settings{
		Dark:             a.dark,
		SidebarWidth:     a.sidebarWidth,
		TablePercent:     a.tablePercent,
		InspectorLayout:  a.workbench.layout,
		SideTablePercent: a.workbench.sidePercent,
		SidebarHidden:    a.workbench.sidebarHidden,
		Columns:          savedLayout(a.columns),
		CustomColumns:    a.customColumns,
		HiddenColumns:    a.hiddenColumns,
		KeyLogPath:       a.keyLogPath,
		ProxyPort:        a.proxyPort,
		VaultScripts:     a.vault.scripts,
	}
	if sameSettings(now, a.saved) || time.Since(a.lastSave) < 400*time.Millisecond {
		return nil
	}
	a.lastSave = time.Now()
	if a.settingsPath == "" {
		a.saved = now
		return nil
	}
	// Serialize these small, debounced writes on the UI thread so an older
	// layout snapshot cannot overwrite a port explicitly saved after it.
	err := os.MkdirAll(filepath.Dir(a.settingsPath), 0o755)
	if err == nil {
		err = writeSettings(a.settingsPath, now)
	}
	if err != nil {
		a.setMessage("Settings: " + err.Error())
		return err
	}
	a.saved = now
	return nil
}

// pinLabel is what the row menu says about pinning a flow.
func pinLabel(f flow) string {
	if f.pinned {
		return "Unpin"
	}
	return "Pin to the top"
}

// togglePin pins a flow to the top of the table, or unpins it. A pinned flow
// stays when the table is cleared.
func (a *app) togglePin(f flow) {
	for i := range a.flows {
		if a.flows[i].id != f.id {
			continue
		}
		a.flows[i].pinned = !a.flows[i].pinned
		if a.flows[i].pinned {
			a.setMessage("Pinned " + f.method + " " + f.path)
		} else {
			a.setMessage("Unpinned " + f.method + " " + f.path)
		}
		a.applyFilter()
		return
	}
}

// collectionShown returns the open state of a collection's section, making it
// open the first time it is drawn.
func (a *app) collectionShown(name string) *bool {
	if open, ok := a.collectionOpen[name]; ok {
		return open
	}
	open := true
	a.collectionOpen[name] = &open
	return &open
}

// exportRequests writes the saved requests to a file the user picks.
func (a *app) exportRequests() {
	list := slices.Clone(a.requests)
	go func() {
		path, err := mygo.Dialog.Save(mygo.SaveDialogOptions{
			Title:       "Export requests",
			DefaultPath: "vscroxy-requests.json",
			Filters:     []mygo.FileFilter{{Name: "Requests", Extensions: []string{"json"}}},
		})
		if err != nil || path == "" {
			return
		}
		if err := saveRequests(path, list); err != nil {
			a.setMessage("Export requests: " + err.Error())
			return
		}
		a.setMessage(fmt.Sprintf("Exported %d requests to %s", len(list), path))
	}()
}

// openHistory fills the request editor from a sent request.
func (a *app) openHistory(i int) {
	if i < 0 || i >= len(a.history) {
		return
	}
	e := a.history[i]
	if e.Draft != nil {
		r := *e.Draft
		r.ID = "" // History opens a copy, never an update to a saved asset.
		a.addDocument(r, "")
		return
	}
	a.addDocument(savedRequest{
		Method: cmp.Or(e.Method, "GET"), URL: e.URL, Headers: e.Headers, Body: e.Body,
	}, "")
}

// saveHistory keeps the sent requests for the next launch.
func (a *app) saveHistory() {
	if a.historyPath == "" {
		return
	}
	list := slices.Clone(a.history)
	go func() {
		if err := os.MkdirAll(filepath.Dir(a.historyPath), 0o755); err != nil {
			a.setMessage("History: " + err.Error())
			return
		}
		if err := saveHistory(a.historyPath, list); err != nil {
			a.setMessage("History: " + err.Error())
		}
	}()
}

// selectedFlows are the flows chosen in the table, in the order captured.
func (a *app) selectedFlows() []flow {
	if a.chosen.Len() == 0 {
		return nil
	}
	var out []flow
	for _, f := range a.flows {
		if a.chosen.Has(f.id) {
			out = append(out, f)
		}
	}
	return out
}

// exportChosen writes the chosen flows to a HAR file the user picks.
func (a *app) exportChosen() {
	flows := a.selectedFlows()
	if len(flows) == 0 {
		a.setMessage("Export chosen: no flow chosen")
		return
	}
	go func() {
		path, err := mygo.Dialog.Save(mygo.SaveDialogOptions{
			Title:       "Export chosen flows",
			DefaultPath: "vscroxy-chosen.har",
			Filters:     []mygo.FileFilter{{Name: "HAR", Extensions: []string{"har"}}},
		})
		if err != nil || path == "" {
			return
		}
		if err := writeHAR(path, flows); err != nil {
			a.setMessage("HAR: " + err.Error())
			return
		}
		a.setMessage(fmt.Sprintf("Exported %d flows to %s", len(flows), path))
	}()
}

// dnsModal edits the names the proxy resolves itself.
func (a *app) dnsModal(c *ui.Context, t *ui.Theme) {
	if !a.dnsOpen {
		return
	}
	uikit.Modal(c, &a.dnsOpen, func() {
		uikit.DialogColumn(c, uikit.DialogMedium).Children(func() {
			uikit.DialogTitle(c, "DNS mapping")
			ui.Scroll(c).MinHeight(0).Gap(t.Space(3)).Children(func() {
				uikit.Meta(c, "Names the proxy resolves itself, one \"name = address\" a line. The request keeps its name and Host header, so a device pointed at this proxy reaches the machine you map: example.com = 192.168.1.50. Add \"@ Chrome, com.example.app\" to map the name for those apps only, or right-click a request and pick \"Map this host…\" to choose the app there. A name matches its subdomains; *.example.com matches one level. Flows that hit a mapping carry a DNS tag in the table.")
				ui.TextArea(c, &a.dnsText).Height(140).Label("DNS mappings").FontSize(uikit.TextBody).Radius(uikit.RadiusControl)
			})
			uikit.DialogFooter(c).Children(func() {
				if uikit.SecondaryButton(c, "Cancel").Clicked() {
					a.dnsOpen = false
				}
				if uikit.PrimaryButton(c, "Save").Clicked() {
					a.saveDNS()
				}
			})
		})
	})
}

// dnsRuleModal maps one host, opened from a flow: the host and the app that
// sent it come from the row, so the scope is a picker over the apps seen
// rather than a name to spell.
func (a *app) dnsRuleModal(c *ui.Context, t *ui.Theme) {
	if !a.dnsRuleOpen {
		return
	}
	uikit.Modal(c, &a.dnsRuleOpen, func() {
		uikit.DialogColumn(c, uikit.DialogSmall).Children(func() {
			uikit.DialogTitle(c, "Map a host")
			ui.Scroll(c).MinHeight(0).Gap(t.Space(3)).Children(func() {
				uikit.Meta(c, "The request keeps its name, URL, Host header and SNI; the proxy dials the address instead.")
				uikit.Input(c, &a.dnsRuleHost).Label("Host")
				uikit.Input(c, &a.dnsRuleAddr).Label("Address")
				uikit.Meta(c, "Applies to")
				uikit.Select(c, &a.dnsRuleScope, a.dnsRuleApps)
			})
			uikit.DialogFooter(c).Children(func() {
				if uikit.SecondaryButton(c, "Cancel").Clicked() {
					a.dnsRuleOpen = false
				}
				if uikit.PrimaryButton(c, "Save").Clicked() {
					a.saveDNSRule()
				}
			})
		})
	})
}

// openDNSRule fills the dialog from a flow's host and app, or from an app in
// the sidebar that has no host yet, so the app is picked rather than spelled.
func (a *app) openDNSRule(host, app string) {
	a.dnsRuleHost = host
	a.dnsRuleAddr = ""
	a.dnsRuleScope = "Any app"
	a.dnsRuleApps = []string{"Any app"}
	for _, d := range a.clients {
		if d.name != "" && !slices.Contains(a.dnsRuleApps, d.name) {
			a.dnsRuleApps = append(a.dnsRuleApps, d.name)
		}
	}
	if app != "" {
		if !slices.Contains(a.dnsRuleApps, app) {
			a.dnsRuleApps = append(a.dnsRuleApps, app)
		}
		a.dnsRuleScope = app
	}
	a.dnsRuleOpen = true
}

// saveDNSRule appends the dialog's rule to the mappings and hands them to the
// proxy; a rule for the same host and scope replaces the one before it.
func (a *app) saveDNSRule() {
	host := strings.TrimSpace(a.dnsRuleHost)
	addr := strings.TrimSpace(a.dnsRuleAddr)
	if host == "" || addr == "" {
		a.setMessage("DNS mapping: a host and an address")
		return
	}
	line := host + " = " + addr
	if a.dnsRuleScope != "" && a.dnsRuleScope != "Any app" {
		line += " @ " + a.dnsRuleScope
	}
	var lines []string
	for _, old := range splitHosts(a.dnsText) {
		if old != line {
			lines = append(lines, old)
		}
	}
	lines = append(lines, line)
	a.dnsText = strings.Join(lines, "\n")
	if a.srv != nil {
		if err := a.srv.SetDNS(lines); err != nil {
			a.setMessage("DNS mapping: " + err.Error())
			return
		}
	}
	if a.dnsPath != "" {
		if err := writeHosts(a.dnsPath, lines); err != nil {
			a.setMessage("DNS mapping: " + err.Error())
			return
		}
	}
	a.dnsRuleOpen = false
	a.setMessage("DNS mapping: " + line)
}

// hostOnly drops the port from a host the table shows, for a rule's name.
func hostOnly(host string) string {
	if name, _, err := net.SplitHostPort(host); err == nil {
		return name
	}
	return host
}

// saveDNS keeps the mappings and hands them to the proxy.
func (a *app) saveDNS() {
	lines := splitHosts(a.dnsText)
	if a.srv != nil {
		if err := a.srv.SetDNS(lines); err != nil {
			a.setMessage("DNS mapping: " + err.Error())
			return
		}
	}
	if a.dnsPath != "" {
		if err := writeHosts(a.dnsPath, lines); err != nil {
			a.setMessage("DNS mapping: " + err.Error())
			return
		}
	}
	a.dnsOpen = false
	a.setMessage(fmt.Sprintf("DNS mapping: %d names", len(lines)))
}
