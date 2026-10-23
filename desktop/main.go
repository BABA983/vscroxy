// Command desktop is the vscroxy desktop app: a native UI over the capture
// core. The UI reads flows from internal/capture and never imports the proxy
// engine itself.
package main

import (
	"cmp"
	"context"
	"encoding/json/v2"
	"fmt"
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

	"github.com/baba983/vscroxy/desktop/internal/appicon"
	"github.com/baba983/vscroxy/desktop/internal/capture"
	"github.com/baba983/vscroxy/desktop/internal/system"
	"github.com/egoist/mygo"
	"github.com/egoist/mygo/ui"
	"github.com/egoist/mygo/ui/editor"
	"github.com/egoist/mygo/ui/richtext"
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
	workspace     int
	requestChoice string
	settingsOpen  bool
	settingsTab   int
	curlOpen      bool
	curlInput     string
	curlProblem   string
	deviceUI      deviceSetup
	clientUI      clientSetup

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
	frames       map[int][]frame
	messageUI    messagePanel
	frameLost    atomic.Int64

	bodySide int
	bodyMode int
	// Readers survive leaving Capture to edit or compare a request.
	headerScroll [2]ui.ScrollState
	bodyScroll   [2]ui.ScrollState
	jsonScroll   [2]ui.ListState
	hexScroll    [2]ui.ListState

	// htmlDoc and imageBmp cache the preview of the body shown, so a frame
	// does not parse or decode it again.
	htmlDoc      *richtext.Document
	htmlFor      string
	imageBmp     *ui.Bitmap
	imageFor     string
	imageErr     string
	bodyQuery    string
	jsonRoots    []*jsonNode
	jsonExpanded map[string]bool
	jsonFor      string
	protoFor     string
	protoOK      bool
	protobuf     protoInspector
	jsonOK       bool

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

	// ssl is what the proxy decrypts: every domain, or those listed. It is
	// edited as text and kept in a file.
	sslPath  string
	sslAll   bool
	sslHosts string
	sslOpen  bool

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

	// headerColumn is the header the table shows as a column of its own, and
	// the filter fields narrow what it shows beyond the search box.
	headerColumn string
	filterStatus string
	filterHeader string
	filterBody   string
	filterTriage captureTriage
	views        captureViews
	columnOpen   bool
	columnName   string

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
	requests     []savedRequest
	requestsPath string
	history      []historyEntry
	historyPath  string
	historyOpen  bool

	// The active document is embedded so the editor's fields always belong
	// to that tab. Workers return results to their originating document.
	*requestDocument
	documents       []*requestDocument
	nextDocument    int
	closingDocument *requestDocument
	requestResults  []requestResult
	runner          collectionRunner
	cookies         *cookieStore
	cookieUI        cookieEditor
	regressionUI    regressionShare

	// envs are the variables a request uses as {{name}}, one set in use.
	envsPath  string
	envs      []environment
	activeEnv string
	varsOpen  bool
	varsName  string
	varsText  string

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
		filter:         "all",
		icons:          make(map[string]ui.ImageSource),
		sidebarWidth:   220,
		tablePercent:   44,
		trafficOpen:    true,
		domainsOpen:    true,
		clientsOpen:    true,
		historyOpen:    true,
		collectionOpen: make(map[string]*bool),
		detailFor:      -1,
		frames:         make(map[int][]frame),
		throttle:       capture.Presets[0],
	}
	// The engine reads SSLKEYLOGFILE on its first handshake, so a key log the
	// settings ask for has to be in the environment before it starts.
	if home, err := os.UserHomeDir(); err == nil {
		a.settingsPath = filepath.Join(home, ".vscroxy", "settings.json")
		if saved, err := loadSettings(a.settingsPath); err != nil {
			a.errText = err.Error()
		} else {
			a.saved, a.proxyPort = saved, saved.ProxyPort
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
		// Keep the window usable when initialization fails: show seeded flows
		// and the error, rather than an empty app.
		a.errText = err.Error()
		a.flows = seedFlows(2000)
	} else {
		a.srv = srv
		if home, err := os.UserHomeDir(); err == nil {
			a.caPath = filepath.Join(home, ".mitmproxy", "mitmproxy-ca-cert.pem")
			a.rulesPath = filepath.Join(home, ".vscroxy", "rules.json")
			a.sslPath = filepath.Join(home, ".vscroxy", "ssl.json")
			a.requestsPath = filepath.Join(home, ".vscroxy", "requests.json")
			a.historyPath = filepath.Join(home, ".vscroxy", "history.json")
			a.settingsPath = filepath.Join(home, ".vscroxy", "settings.json")
			a.ignorePath = filepath.Join(home, ".vscroxy", "ignore.txt")
			a.ignoreAppsPath = filepath.Join(home, ".vscroxy", "ignore-apps.txt")
			a.dnsPath = filepath.Join(home, ".vscroxy", "dns.txt")
			a.scriptPath = filepath.Join(home, ".vscroxy", "script.js")
			a.envsPath = filepath.Join(home, ".vscroxy", "env.json")
		}
		a.reloadConfig()
	}
	if a.envsPath != "" {
		if envs, active, err := loadEnvs(a.envsPath); err == nil {
			a.envs, a.activeEnv = envs, active
			if a.activeEnv == "" && len(envs) > 0 {
				a.activeEnv = envs[0].Name
			}
		}
	}
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
		a.workbench.layout = saved.InspectorLayout
		a.workbench.sidebarHidden = saved.SidebarHidden
		if saved.SideTablePercent > 0 {
			a.workbench.sidePercent = saved.SideTablePercent
		}
		a.columns = saved.Columns.layout()
		a.headerColumn = saved.HeaderColumn
	}
	if a.requestsPath != "" {
		if list, err := loadRequests(a.requestsPath); err == nil {
			a.requests = list
		}
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
	}
	return a
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
			Header:      parseHeaderBlock(a.bpHeaders),
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
		d.composeResult = result.summary
		d.composeTests = result.tests
		d.composeReplies = result.replies
		if result.response != nil {
			d.response = result.response
			d.responseEditor = nil
			d.comparison.diffPart = "" // even an identical URL is a new send
		}
		if result.vars != nil && !maps.Equal(result.varsBefore, result.vars) {
			a.applyVars(result.environment, result.varsBefore, result.vars)
		}
		if result.history != nil {
			a.history = pushHistory(a.history, *result.history)
			a.saveHistory()
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
			a.bpHeaders = headerBlock(next.Header)
			a.bpAbort = "502 Bad Gateway"
		}
	}
	if len(pending) == 0 {
		return
	}
	for _, f := range pending {
		kind := f.Kind
		if kind == "http" {
			kind = ""
		}
		a.flows = append(a.flows, flow{
			id:          f.ID,
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
	for _, f := range a.flows[len(a.flows)-len(pending):] {
		a.totalBytes += int64(f.bytes)
	}
	if len(a.flows) > maxFlows {
		cut := len(a.flows) - maxFlows
		a.flows = a.flows[cut:]
		a.frames = pruneMessages(a.frames, a.flows)
	}
	a.countDomains()
	a.countClients()
	a.applyFilter()
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
		if !a.matchesChip(f) {
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
	if f.kind == "" && a.tab == 5 {
		a.tab = 0
	}
	if f.kind == "connect" && f.conn.error != "" {
		a.tab = 1 // open the failure evidence, not tunnel-only setup instructions
	}
	a.body, a.reqBody = f.body(true), f.body(false)
	a.bodyMode, a.bodyQuery = 0, ""
	a.headerScroll, a.bodyScroll = [2]ui.ScrollState{}, [2]ui.ScrollState{}
	a.jsonScroll, a.hexScroll = [2]ui.ListState{}, [2]ui.ListState{}
	a.jsonFor, a.protoFor, a.htmlFor, a.imageFor = "", "", "", ""
	a.htmlDoc, a.imageBmp, a.imageErr = nil, nil, ""
	a.protobuf.stop()
	previous, baseline := a.previousResponse()
	a.diffBaseline = baseline
	a.diff = &editor.DiffState{
		Old:    editor.NewBuffer(previous),
		New:    editor.NewBuffer(a.body),
		Layout: editor.DiffSplit,
	}
	a.diff.Old.SetLanguage(editor.LanguageFor("response.json"))
	a.diff.New.SetLanguage(editor.LanguageFor("response.json"))
}

func (a *app) view(c *ui.Context) {
	c.SetTheme(appTheme(a.dark))
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
	t := c.Theme()
	ui.Column(c).Fill().Background(t.Background).Children(func() {
		a.toolbar(c, t)
		if a.workspace == 1 || a.workspace == 0 && len(a.documents) > 0 {
			a.documentTabs(c, t)
		}
		if a.workspace == 0 {
			a.captureToolbar(c, t)
		}
		a.desktopContent(c)
		a.statusBar(c, t)
	})
	a.persistSettings()
	a.commandPalette(c)
	ui.Modal(c, &a.clearOpen, func() {
		ui.Column(c).Width(360).Gap(12).Children(func() {
			ui.Text(c, "Clear captured requests?").Bold()
			ui.Text(c, "Unpinned requests will be removed from this session. Export any traffic you want to keep first.")
			ui.Row(c).Gap(8).Justify(ui.End).Children(func() {
				if ui.Button(c, "Cancel").Clicked() {
					a.clearOpen = false
				}
				if ui.PrimaryButton(c, "Clear unpinned requests").Clicked() {
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
	a.runnerModal(c, t)
	a.cookiesModal(c, t)
	a.regressionModal(c, t)
	a.replayModal(c, t)
	a.protobuf.modal(c, a)
	a.views.capture(a)
	if a.workspace != 0 || a.tab != 5 {
		a.messageUI.stopSearch()
	}
	f, ok := a.current()
	ct := f.contentType(a.bodySide == 0)
	if a.workspace != 0 || a.tab != 1 || !ok || !(a.bodyMode == 5 || a.bodyMode == 0 && (grpcContentType(ct) || strings.Contains(ct, "protobuf"))) {
		a.protobuf.stop()
	}
}

// upstreamModal edits the proxy every request goes through.
func (a *app) upstreamModal(c *ui.Context, t *ui.Theme) {
	if !a.upstreamOpen {
		return
	}
	ui.Modal(c, &a.upstreamOpen, func() {
		ui.Column(c).Width(480).Gap(10).Children(func() {
			ui.Text(c, "Upstream proxy").FontSize(14).Bold()
			ui.TextInput(c, &a.upstreamAddr).Placeholder("http://127.0.0.1:8080").Label("Address")
			ui.Row(c).Gap(8).Justify(ui.End).Children(func() {
				if ui.Button(c, "Cancel").Clicked() {
					a.upstreamOpen = false
				}
				if ui.PrimaryButton(c, "Save").Clicked() {
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
	width, height := c.Size()
	if ui.Modal(c, &a.bpOpen, func() {
		ui.Column(c).Width(min(720, width-64)).Height(min(640, height-64)).Gap(10).Children(func() {
			title := "Breakpoint · request paused"
			if a.bp.Phase == capture.PhaseResponse {
				title = "Breakpoint · response paused"
			}
			ui.Text(c, title).FontSize(14).Bold()
			a.mu.Lock()
			waiting := len(a.bps) + 1
			a.mu.Unlock()
			ui.Text(c, fmt.Sprintf("%d paused · changes apply only to this exchange", waiting)).FontSize(11).TextColor(t.TextMuted)
			ui.Scroll(c).Grow(1).FillWidth().Gap(8).Children(func() {
				if a.bp.Phase == capture.PhaseResponse {
					ui.Row(c).Gap(8).Children(func() {
						ui.TextInput(c, &a.bpStatus).Width(90).Label("Status")
						ui.Text(c, a.bp.URL).FontSize(12).TextColor(t.TextMuted).SingleLine().Grow(1)
					})
				} else {
					ui.Row(c).Gap(8).Children(func() {
						ui.TextInput(c, &a.bpMethod).Width(90).Label("Method")
						ui.TextInput(c, &a.bpURL).Grow(1).Label("URL")
					})
				}
				ui.Text(c, "Headers · one Name: Value per line; repeat a name for multiple values").FontSize(11).TextColor(t.TextMuted)
				ui.TextArea(c, &a.bpHeaders).Height(130).Label("Breakpoint headers")
				if a.bp.BodyReadOnly {
					ui.Text(c, "Binary, undecodable or large body: preserved unchanged; headers can still be edited.").FontSize(12).TextColor(t.TextMuted)
				} else {
					ui.TextArea(c, &a.bpBody).Height(160).Label("Body")
					ui.Text(c, "Body edits use decoded bytes; encoding, digest and length headers are recalculated or removed.").FontSize(11).TextColor(t.TextMuted)
				}
			})
			validation := a.breakpointError()
			if validation != "" {
				ui.Text(c, validation).FontSize(12).TextColor(t.Danger)
			}
			ui.Row(c).Gap(8).Justify(ui.End).Children(func() {
				ui.Select(c, &a.bpAbort, []string{"502 Bad Gateway", "429 Too Many Requests", "503 Service Unavailable"}).Width(200).Label("Abort status")
				if ui.Button(c, "Abort").Clicked() {
					a.resolveBreak(true, false)
				}
				if ui.Button(c, "Continue original").Clicked() {
					a.resolveBreak(false, true)
				}
				if ui.PrimaryButton(c, "Apply & continue").Disabled(validation != "").Clicked() {
					a.resolveBreak(false, false)
				}
			})
		})
	}).Dismissed() {
		// Escape or an accidental outside click must not abort a live request.
		a.bpOpen = true
	}
}

func (a *app) toolbar(c *ui.Context, t *ui.Theme) {
	ui.Row(c).Height(44).Padding(0, 12).Gap(6).AlignItems(ui.Center).Background(t.Surface).Children(func() {
		ui.Text(c, "vscroxy").FontSize(14).Bold().Width(80)
		nav := ui.TabsBase(c, &a.workspace, 3)
		nav.List.Gap(4).Children(func() {
			for i, label := range []string{"Capture", "Requests", "APIs"} {
				tab := nav.Tab(i).Height(32).Padding(0, 12).Gap(7).AlignItems(ui.Center).Radius(6).TextColor(t.TextMuted)
				if a.workspace == i {
					tab.Background(t.Accent.Alpha(0.12)).TextColor(t.Accent)
				}
				tab.Children(func() {
					ui.Icon(c, []*ui.SVG{captureIcon, requestIcon, apiIcon}[i]).Size(15, 15)
					ui.Text(c, label).FontSize(12).Bold()
				})
			}
		})
		if nav.List.Changed() && a.workspace == 1 && a.requestDocument == nil {
			a.openCompose()
		}
		ui.Spacer(c)
		if ui.Button(c, "Rules").FontSize(11).Height(28).Clicked() {
			a.openRules()
		}
		networkLabel := "Network"
		if a.deviceUI.gateway != nil {
			networkLabel = "Network · LAN"
		}
		ui.MenuButton(c, networkLabel, func(m *ui.Menu) {
			if m.Item("Set up a local client…").Chosen() {
				a.openClientSetup()
			}
			if m.Item("No Caching").Checked(a.srv != nil && a.srv.NoCaching()).Disabled(a.srv == nil).Chosen() {
				a.srv.SetNoCaching(!a.srv.NoCaching())
			}
			m.Separator()
			if m.Item("Connect a device…").Checked(a.deviceUI.gateway != nil).Chosen() {
				a.openDevices()
			}
			m.Separator()
			for _, preset := range capture.Presets {
				if m.Item(preset.Name).Checked(a.throttle.Name == preset.Name).Chosen() {
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
			if m.Item("Upstream off").Chosen() {
				if a.srv != nil {
					_ = a.srv.SetUpstream("")
				}
				a.upstream = ""
				a.setMessage("Upstream: off")
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
		}).FontSize(11).Height(28).TextColor(t.TextMuted).Border(0, t.Border)
		ui.MenuButton(c, "Session", func(m *ui.Menu) {
			if m.Item("Replay chosen…").Disabled(a.chosen.Len() == 0).Chosen() {
				var selected []flow
				for _, f := range a.flows {
					if a.chosen.Has(f.id) {
						selected = append(selected, f)
					}
				}
				a.openReplay(selected)
			}
			m.Separator()
			if m.Item("Export HAR…").Chosen() {
				a.exportHAR()
			}
			if m.Item("Export chosen HAR…").Chosen() {
				a.exportChosen()
			}
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
			if m.Item("Import requests…").Chosen() {
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
		}).FontSize(11).Height(28).TextColor(t.TextMuted).Border(0, t.Border)
		ui.MenuButton(c, "View", func(m *ui.Menu) {
			for _, action := range a.desktopActions(c) {
				if strings.HasPrefix(action.id, "layout-") || action.id == "sidebar" || action.id == "focus" {
					if m.Item(action.label).Disabled(!action.enabled).Chosen() {
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
			if m.Item("Column from header…").Chosen() {
				a.openColumn()
			}
			if a.headerColumn != "" && m.Item("Hide the "+a.headerColumn+" column").Chosen() {
				a.setHeaderColumn("")
			}
			m.Separator()
			if m.Item("Protobuf schema…").Chosen() {
				a.protobuf.open = true
			}
		}).FontSize(11).Height(28).TextColor(t.TextMuted).Border(0, t.Border)
		if ui.Button(c, "Commands").Height(28).FontSize(11).Tooltip("Search actions · Ctrl/Cmd+K").Clicked() {
			a.workbench.openPalette()
		}
		if ui.ButtonBase(c).Size(30, 30).Radius(6).Label("Settings").Tooltip("Settings · Ctrl/Cmd+,").Children(func() {
			ui.Icon(c, settingsIcon).Size(17, 17).TextColor(t.TextMuted)
		}).Clicked() {
			a.settingsOpen = true
		}
		if ui.ButtonBase(c).Size(30, 30).Radius(6).Label("New request").Tooltip("New request · Ctrl/Cmd+N").Children(func() {
			ui.Icon(c, plusIcon).Size(17, 17).TextColor(t.TextMuted)
		}).Clicked() {
			a.openCompose()
		}
	})
	ui.Divider(c)
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

func (a *app) sidebar(c *ui.Context) {
	t := c.Theme()
	if a.workspace == 1 {
		a.requestSidebar(c, t)
		return
	}
	if a.workspace == 2 {
		a.apiSidebar(c, t)
		return
	}
	ui.Column(c).Fill().Background(t.Surface).Children(func() {
		chosen := ui.Sidebar(c, &a.filter, func() {
			a.views.controls(c, t, a)
			ui.SidebarSection(c, "Traffic", &a.trafficOpen, func() {
				ui.SidebarItem(c, "all", nil, "All requests")
				ui.SidebarItem(c, "errors", nil, "Errors").Children(func() {
					ui.Badge(c, fmt.Sprint(a.count(flow.failed)))
				})
				ui.SidebarItem(c, "slow", nil, "Slow").Children(func() {
					ui.Badge(c, fmt.Sprint(a.count(func(f flow) bool { return f.millis >= 120 })))
				})
			})
			ui.SidebarSection(c, "Domains", &a.domainsOpen, func() {
				for _, d := range a.domains {
					ui.SidebarItem(c, "host:"+d.name, nil, d.name).Children(func() {
						ui.Badge(c, fmt.Sprint(d.n))
					})
				}
			})
			if len(a.clients) > 0 {
				ui.SidebarSection(c, "Apps", &a.clientsOpen, func() {
					for _, d := range a.clients {
						ui.SidebarItem(c, "client:"+d.name, nil, d.name).
							ContextMenu(func(m *ui.Menu) {
								if m.Item("Map a host for " + d.name + "…").Chosen() {
									a.openDNSRule("", d.name)
								}
							}).
							Children(func() {
								ui.Badge(c, fmt.Sprint(d.n))
							})
					}
				})
			}
		}).Fill()
		if chosen.Changed() {
			a.applyFilter()
		}
	})
}

func (a *app) requestSidebar(c *ui.Context, t *ui.Theme) {
	ui.Column(c).Fill().Background(t.Surface).Children(func() {
		ui.Text(c, "REQUEST LIBRARY").FontSize(10).Bold().TextColor(t.TextMuted).Padding(18, 16, 8, 16)
		if len(a.requests) == 0 && len(a.history) == 0 {
			ui.Text(c, "Save a request here, or edit a captured flow to start.").FontSize(12).TextColor(t.TextMuted).Padding(8, 16)
		}
		chosen := ui.Sidebar(c, &a.requestChoice, func() {
			groups := groupRequests(a.requests)
			for _, name := range slices.Sorted(maps.Keys(groups)) {
				ui.SidebarSection(c, cmp.Or(name, "Saved"), a.collectionShown(name), func() {
					for _, r := range groups[name] {
						ui.SidebarItem(c, "saved:"+r.Collection+"\x00"+r.Name, requestIcon, r.Name).Children(func() {
							ui.Text(c, r.Method).FontSize(9).Bold().TextColor(methodColor(r.Method)).Width(38)
						})
					}
				})
			}
			if len(a.history) > 0 {
				ui.SidebarSection(c, "History", &a.historyOpen, func() {
					for i, e := range a.history {
						ui.SidebarItem(c, "history:"+strconv.Itoa(i), nil, e.label())
					}
				})
			}
		}).Grow(1)
		if chosen.Changed() {
			if value, ok := strings.CutPrefix(a.requestChoice, "history:"); ok {
				if i, err := strconv.Atoi(value); err == nil {
					a.openHistory(i)
				}
			} else {
				for _, r := range a.requests {
					if a.requestChoice == "saved:"+r.Collection+"\x00"+r.Name {
						a.openSaved(r.Collection, r.Name)
						break
					}
				}
			}
		}
		ui.Column(c).Padding(12).Gap(6).Children(func() {
			if ui.Button(c, "Import cURL…").FillWidth().Clicked() {
				a.openCurlImport()
			}
			ui.Row(c).Gap(6).Children(func() {
				if ui.Button(c, "Runner…").Grow(1).Clicked() {
					a.openRunner()
				}
				if ui.Button(c, "Cookies…").Grow(1).Clicked() {
					a.openCookies()
				}
			})
		})
	})
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
		ui.Column(c).Fill().Center().Gap(8).Children(func() {
			if len(a.flows) > 0 {
				ui.Text(c, "No matching requests").FontSize(14).TextColor(t.Text)
				ui.Text(c, fmt.Sprintf("%d captured requests are hidden by this view's search or filters.", len(a.flows))).FontSize(12).TextColor(t.TextMuted)
				if ui.Button(c, "Show all requests").Clicked() {
					a.query, a.filter, a.chip = "", "all", 0
					a.views.capture(a)
					a.clearFilters()
				}
				return
			}
			ui.Text(c, "Waiting for traffic").FontSize(14).TextColor(t.Text)
			hint := "Start capture successfully before configuring a client"
			if a.srv != nil && a.srv.Running() {
				hint = "Set your HTTP proxy to " + a.srv.Addr()
			}
			ui.Text(c, hint).FontSize(12).TextColor(t.TextMuted)
		})
		return
	}
	cols := []ui.TableColumn{
		{ID: "method", Title: "Method", Width: 80, Sortable: true},
		{ID: "status", Title: "Status", Width: 78, Align: ui.End, Sortable: true},
		{ID: "url", Title: "URL", MinWidth: 180, Sortable: true},
	}
	headerCol := -1
	if a.headerColumn != "" {
		headerCol = len(cols)
		cols = append(cols, ui.TableColumn{ID: "header", Title: a.headerColumn, Width: 150, Sortable: true})
	}
	clientCol, sizeCol := -2, -3
	if !a.workbench.sideBySide {
		clientCol = len(cols)
		cols = append(cols, ui.TableColumn{ID: "client", Title: "Client", Width: 132, Sortable: true})
	}
	durationCol := len(cols)
	cols = append(cols, ui.TableColumn{ID: "duration", Title: "Duration", Width: 96, Align: ui.End, Sortable: true})
	if !a.workbench.sideBySide {
		sizeCol = len(cols)
		cols = append(cols, ui.TableColumn{ID: "size", Title: "Size", Width: 86, Align: ui.End, Sortable: true})
	}
	cols = append(cols, ui.TableColumn{Title: "", ID: "filler", Width: 14, Fixed: true})
	state := &a.views.activeView().list
	state.FollowEnd = true
	state.Selected = &a.picked
	state.Selection = &a.chosen
	state.Key = func(row int) any { return a.flows[a.visible[row]].id }
	state.Sort = &a.sort
	state.Columns = a.columns
	ui.Table(c, state, cols, len(a.visible), func(row, col int) {
		f := a.flows[a.visible[row]]
		chosen := a.chosen.Has(f.id)
		text := func(s string) ui.Element { return ui.Text(c, s).FontSize(12).SingleLine() }
		switch col {
		case 0:
			color := methodColor(f.method)
			if chosen {
				color = t.AccentText
			}
			if f.pinned {
				ui.Icon(c, pinIcon).Size(11, 11).Padding(0, 4, 0, 0).TextColor(color)
			}
			if f.color != "" {
				if dot, ok := highlightColor(f.color); ok {
					ui.Box(c).Size(8, 8).Radius(4).Background(dot).MarginEnd(5)
				}
			}
			text(f.method).TextColor(color)
		case 1:
			if f.conn.error != "" {
				text("Failed").TextColor(rowColor(chosen, t, t.Danger)).Tooltip(f.conn.errorStage + ": " + f.conn.error)
				return
			}
			if f.kind == "connect" && f.status == 0 {
				color := ui.Hex("#d97706")
				if chosen {
					color = t.AccentText
				}
				text("Tunnel").TextColor(color)
				return
			}
			color := f.statusColor(t)
			if chosen {
				color = t.AccentText
			}
			text(fmt.Sprint(f.status)).TextColor(color)
		case 2:
			scheme := ui.Hex("#71717a")
			if f.scheme == "https" {
				scheme = ui.Hex("#0891b2")
			}
			rest := rowColor(chosen, t, t.Text)
			if chosen {
				scheme = t.AccentText
			}
			ui.Row(c).Gap(1).Children(func() {
				if f.note != "" {
					ui.Icon(c, noteIcon).Size(11, 11).TextColor(ui.Hex("#a1a1aa")).MarginEnd(4).Tooltip(f.note)
				}
				if f.dns != "" {
					ui.Box(c).Size(28, 14).Radius(3).Background(ui.Hex("#d97706")).Center().MarginEnd(4).
						Tooltip("DNS mapping " + f.dns).
						Children(func() {
							ui.Text(c, "DNS").FontSize(9).Bold().TextColor(ui.Hex("#ffffff"))
						})
				}
				text(f.scheme + "://").Shrink(0).TextColor(scheme).ContextMenu(a.rowMenu(c, f))
				text(f.host + f.path).TextColor(rest).Tooltip(f.url).ContextMenu(a.rowMenu(c, f))
			})
		case headerCol:
			value := f.respHeader.Get(a.headerColumn)
			if value == "" {
				value = f.reqHeader.Get(a.headerColumn)
			}
			if value == "" {
				text("—").TextColor(rowColor(chosen, t, t.TextMuted))
				return
			}
			text(value).TextColor(rowColor(chosen, t, t.Text)).Tooltip(value)
		case clientCol:
			if src, ok := a.clientIcon(f.process, f.processPath); ok {
				ui.Image(c, src).Size(16, 16)
			} else {
				clientBadge(c, f.process)
			}
			text(f.process).Padding(0, 0, 0, 6).ContextMenu(a.rowMenu(c, f)).TextColor(rowColor(chosen, t, t.TextMuted))
		case durationCol:
			text(fmt.Sprintf("%d ms", f.millis)).TextColor(rowColor(chosen, t, t.TextMuted))
		case sizeCol:
			text(humanBytes(f.bytes)).TextColor(rowColor(chosen, t, t.TextMuted))
		}
	}).Grow(1)
	a.columns = state.Columns
}

func (a *app) detail(c *ui.Context) {
	// The table may change the choice during this frame. Sync after it draws,
	// so all detail widgets see the same selected request immediately.
	a.syncDetail()
	if selected := a.selectedFlows(); len(selected) > 1 {
		a.analysisDetail(c, selected)
		return
	}
	t := c.Theme()
	ui.Column(c).Fill().Background(t.Background).Children(func() {
		f, ok := a.current()
		ui.Row(c).Height(34).Padding(0, 12).Gap(8).AlignItems(ui.Center).Children(func() {
			if ok {
				ui.Text(c, fmt.Sprintf("%s %s://%s%s", f.method, f.scheme, f.host, f.path)).
					FontSize(12).Bold().TextColor(t.Text).SingleLine().Grow(1).Tooltip(f.url)
			} else {
				ui.Text(c, "No request chosen").FontSize(12).TextColor(t.TextMuted).Grow(1)
			}
			if !a.workbench.focused {
				a.desktopButton(c, "focus", "Focus").Tooltip("Focus inspector · Ctrl/Cmd+Shift+Enter")
			}
		})
		ui.Row(c).Padding(2, 12).Gap(8).Wrap().AlignItems(ui.Center).Children(func() {
			if !ok {
				return
			}
			a.desktopButton(c, "edit", "Edit & replay")
			a.desktopButton(c, "replay", "Replay")
			a.desktopButton(c, "copy", "cURL")
			if f.process != "" {
				ui.Text(c, f.process).FontSize(11).TextColor(t.TextMuted).SingleLine().MaxWidth(100).Tooltip(f.process)
			}
			if f.dns != "" {
				ui.Box(c).Size(28, 14).Radius(3).Background(ui.Hex("#d97706")).Center().Children(func() {
					ui.Text(c, "DNS").FontSize(9).Bold().TextColor(ui.Hex("#ffffff"))
				})
				ui.Text(c, f.dns).FontSize(12).TextColor(t.TextMuted).
					Tooltip("The proxy dialled this address; the request kept its name, Host header and SNI")
			}
			if f.note != "" {
				ui.Icon(c, noteIcon).Size(12, 12).TextColor(t.TextMuted)
				ui.Text(c, f.note).FontSize(11).TextColor(t.TextMuted).SingleLine().MaxWidth(100).Tooltip(f.note)
			}
			status := strconv.Itoa(f.status)
			color := f.statusColor(t)
			if f.conn.error != "" {
				status = "Failed"
				color = t.Danger
				if f.kind == "connect" && f.status == 200 {
					status = "CONNECT 200 · failed"
				}
			}
			chip := ui.Text(c, fmt.Sprintf("%s · %s · %d ms", status, humanBytes(f.bytes), f.millis)).
				FontSize(12).TextColor(color)
			if f.conn.error != "" {
				chip = chip.Tooltip(cmp.Or(f.conn.errorStage, "Connection") + ": " + f.conn.error)
				explain := ui.Button(c, "Explain").FontSize(11).Tooltip("Explain the recorded connection failure")
				if explain.Clicked() {
					a.explainOpen = !a.explainOpen
				}
				ui.PopoverBase(c, explain, &a.explainOpen, func(panel ui.Element) {
					panel.Margin(8, 0, 0, 0).Padding(0).Radius(10).Background(t.Background).
						Border(1, t.Border).Shadow(0, 8, 24, 0, ui.RGBA(0, 0, 0, 0.22)).
						Children(func() { a.explainConnection(c, f) })
				})
			} else if doc := statusDoc(f.status); doc != "" {
				chip = chip.Tooltip(doc)
			}
			info := ui.Icon(c, infoIcon).Size(13, 13).TextColor(t.TextMuted).
				Label("Connection details").
				Tooltip("Connection: addresses, DNS and TLS").
				OnClick(func() { a.connOpen = !a.connOpen })
			ui.Popover(c, info, &a.connOpen, func() { a.connPanel(c, t, f) })
		})
		if ok {
			a.replayLinks(c, f)
			if len(f.actions) > 0 {
				ui.Text(c, "Applied: "+strings.Join(f.actions, " · ")).FontSize(11).TextColor(t.Accent).
					Padding(4, 12).SingleLine().Tooltip(strings.Join(f.actions, "\n"))
			}
		}
		if f.kind == "connect" {
			if a.tab > 1 {
				a.tab = 0
			}
			ui.Tabs(c, &a.tab, "HTTPS setup", "Connection").Padding(0, 8)
			if a.tab == 1 {
				a.connectionView(c, f)
			} else {
				if f.conn.error != "" {
					ui.Text(c, f.conn.errorStage+" failed: "+f.conn.error).FontSize(12).TextColor(t.Danger).Padding(8, 12)
				}
				a.connectPanel(c, f)
			}
			return
		}
		labels := []string{"Headers", "Body", "Diff", "Timing", "Connection"}
		if f.kind == "ws" || f.kind == "sse" {
			labels = append(labels, "Messages")
		}
		if a.tab >= len(labels) {
			a.tab = 0
		}
		ui.Tabs(c, &a.tab, labels...).Padding(0, 8)
		if !ok {
			ui.Text(c, "Capture a request to inspect it.").FontSize(12).TextColor(t.TextMuted).Padding(16)
			return
		}
		switch a.tab {
		case 0:
			a.headersView(c, f)
		case 1:
			a.bodyView(c, f)
		case 2:
			if !a.diffBaseline {
				ui.Text(c, "No previous response for this method and URL. Replay the request to compare responses.").FontSize(12).TextColor(t.TextMuted).Padding(16)
				return
			}
			ui.Row(c).Padding(8, 12).Children(func() {
				ui.Text(c, "Previous response (left)").Grow(1).FontSize(11).TextColor(t.TextMuted)
				ui.Text(c, "Current response (right)").Grow(1).FontSize(11).TextColor(t.TextMuted)
			})
			if a.diff != nil {
				editor.DiffView(c, a.diff).Grow(1).FillWidth()
			}
		case 3:
			a.timingView(c, f)
		case 4:
			a.connectionView(c, f)
		case 5:
			a.messages(c, f)
		}
	})
}

// noteIcon marks a flow a person wrote a note on.
var noteIcon = ui.MustParseSVG([]byte(`<svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 16 16"><path d="M8 1.5c-3.9 0-7 2.7-7 6 0 1.9 1 3.5 2.6 4.6L3 15.2l3-1.7c.6.1 1.3.2 2 .2 3.9 0 7-2.7 7-6s-3.1-6.2-7-6.2Z"/></svg>`))

// highlightColors are the colors a flow can be marked with, in menu order.
var highlightColors = []struct{ name, hex string }{
	{"Red", "#dc2626"},
	{"Orange", "#ea580c"},
	{"Yellow", "#ca8a04"},
	{"Green", "#16a34a"},
	{"Blue", "#2563eb"},
	{"Purple", "#7c3aed"},
}

// highlightColor is the color a flow is marked with.
func highlightColor(name string) (ui.Color, bool) {
	for _, c := range highlightColors {
		if c.name == name {
			return ui.Hex(c.hex), true
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
	ui.Modal(c, &a.markOpen, func() {
		ui.Column(c).Width(460).Gap(10).Children(func() {
			ui.Text(c, "Mark the request").FontSize(14).Bold()
			ui.Text(c, "A color shows in the table beside the method, and the note in the URL cell and the detail header.").
				FontSize(11).TextColor(t.TextMuted)
			ui.Row(c).Gap(6).AlignItems(ui.Center).Children(func() {
				for _, color := range highlightColors {
					selected := a.markColor == color.name
					ui.Box(c).Size(24, 24).Radius(5).Background(ui.Hex(color.hex)).Center().
						Tooltip(color.name).
						OnClick(func() { a.markColor = color.name }).
						Children(func() {
							if selected {
								ui.Text(c, "✓").FontSize(12).Bold().TextColor(ui.Hex("#ffffff"))
							}
						})
				}
				if ui.Button(c, "None").Clicked() {
					a.markColor = ""
				}
			})
			ui.TextArea(c, &a.markNote).Height(90).Label("Note")
			ui.Row(c).Gap(8).Justify(ui.End).Children(func() {
				if ui.Button(c, "Cancel").Clicked() {
					a.markOpen = false
				}
				if ui.PrimaryButton(c, "Save").Clicked() {
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
var infoIcon = ui.MustParseSVG([]byte(`<svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 16 16"><path d="M8 1.3a6.7 6.7 0 1 0 0 13.4A6.7 6.7 0 0 0 8 1.3Zm0 1.5a5.2 5.2 0 1 1 0 10.4A5.2 5.2 0 0 1 8 2.8Zm0 1.6a.95.95 0 1 0 0 1.9.95.95 0 0 0 0-1.9Zm-.85 3.2v4h1.7v-4Z"/></svg>`))

func (a *app) explainConnection(c *ui.Context, f flow) {
	t := c.Theme()
	e := f.conn.explanation()
	width, height := c.Size()
	ui.Column(c).Width(min(480, width-40)).Children(func() {
		ui.Text(c, e.title).FontSize(16).Bold().Padding(16, 16, 12, 16)
		ui.Scroll(c).Height(min(270, height-220)).Padding(0, 16, 14, 16).Gap(16).Selectable().Children(func() {
			ui.Text(c, e.summary).FontSize(12).TextColor(t.TextMuted)
			ui.Column(c).Padding(12).Gap(6).Radius(7).Background(t.Danger.Alpha(0.06)).Border(1, t.Danger.Alpha(0.2)).Children(func() {
				ui.Text(c, "Observed failure · "+cmp.Or(f.conn.errorStage, "unknown stage")).FontSize(11).Bold().TextColor(t.TextMuted)
				ui.Text(c, f.conn.error).Font("monospace").FontSize(11).TextColor(t.Danger)
			})
			ui.Column(c).Gap(6).Children(func() {
				ui.Text(c, "Possible causes · not confirmed").FontSize(12).Bold()
				ui.Text(c, e.causes).FontSize(12).TextColor(t.TextMuted)
			})
			ui.Column(c).Gap(6).Children(func() {
				ui.Text(c, "What to try").FontSize(12).Bold()
				ui.Text(c, e.steps).FontSize(12)
			})
		})
		ui.Divider(c)
		ui.Row(c).Padding(12, 16).Gap(16).Children(func() {
			ui.Text(c, "Review URLs, addresses and raw errors before sharing.").FontSize(11).TextColor(t.TextMuted).Grow(1)
			if ui.Button(c, "Copy diagnostics").FontSize(12).Height(30).Clicked() {
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
	ui.Row(c).Padding(8, 12).Gap(8).Children(func() {
		if f.conn.error != "" {
			ui.Text(c, cmp.Or(f.conn.errorStage, "Connection")+" failed").FontSize(12).TextColor(t.Danger).Grow(1)
		} else {
			ui.Text(c, "Connection log · snapshot at capture time").FontSize(12).TextColor(t.TextMuted).Grow(1)
		}
		if ui.Button(c, "Copy connection log").Disabled(f.conn.log == "").Clicked() {
			c.Services().WriteClipboard(f.conn.log)
			a.setMessage("Copied connection log")
		}
	})
	if f.conn.error != "" {
		ui.Text(c, f.conn.error).Selectable().MaxLines(3).Tooltip(f.conn.error).FontSize(12).TextColor(t.Danger).Padding(0, 12, 8)
	}
	text := cmp.Or(f.conn.log, "No connection history was recorded for this exchange (for example, an older or imported session).")
	ui.TextArea(c, &text).ReadOnly(true).Label("Connection log").Font("monospace").FontSize(12).Grow(1).FillWidth()
}

// connPanel is the connection behind a flow: who talked to the proxy, where it
// dialled, the addresses the name resolved to, and what TLS agreed on.
func (a *app) connPanel(c *ui.Context, t *ui.Theme, f flow) {
	cn := f.conn
	ui.Column(c).Width(440).Gap(2).Padding(4).Children(func() {
		ui.Text(c, "Connection").FontSize(12).Bold().TextColor(t.Text).Padding(0, 0, 6, 0)
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
	ui.Row(c).Gap(10).Padding(2, 0).Children(func() {
		ui.Text(c, label).FontSize(11).TextColor(t.TextMuted).Width(96)
		ui.Text(c, value).FontSize(12).Font("monospace").TextColor(t.Text).Grow(1)
	})
}

// headersView shows the request and the response headers row by row, each
// standard one with a note on what it is for, and the status line with what
// the code means.
func (a *app) headersView(c *ui.Context, f flow) {
	t := c.Theme()
	if grpcContentType(f.contentType(true)) {
		ui.Text(c, grpcStatus(f)).FontSize(12).TextColor(t.Text).Padding(8, 16, 0, 16)
	}
	stacked := a.workbench.sideBySide && !a.workbench.focused
	var panes ui.Element
	if stacked {
		panes = ui.Column(c)
	} else {
		panes = ui.Row(c)
	}
	panes.Grow(1).Children(func() {
		request := ui.Column(c).Padding(16)
		if stacked {
			request.FillWidth().Grow(1)
		} else {
			request.WidthPercent(50).FillHeight()
		}
		request.Children(func() {
			a.headerBlock(c, t, "Request", strings.TrimSpace(f.conn.proto+" "+f.method+" "+f.path), "", f.reqHeader, f.reqHeaders, f.reqTrailer)
		})
		if stacked {
			ui.Divider(c)
		} else {
			ui.Box(c).Width(1).FillHeight().Background(t.Border)
		}
		line := "No response"
		if f.status != 0 {
			line = fmt.Sprintf("%d %s", f.status, http.StatusText(f.status))
			if f.resProto != "" {
				line = f.resProto + " " + line
			}
		}
		response := ui.Column(c).Grow(1).Padding(16)
		if stacked {
			response.FillWidth()
		} else {
			response.FillHeight()
		}
		response.Children(func() {
			a.headerBlock(c, t, "Response", line, statusDoc(f.status), f.respHeader, f.resHeaders, f.resTrailer)
		})
	})
}

// headerBlock is one side of the Headers tab: the line that starts the message
// and then its headers, sorted by name.
func (a *app) headerBlock(c *ui.Context, t *ui.Theme, title, line, note string, headers http.Header, raw string, trailers http.Header) {
	ui.Text(c, title).FontSize(11).Bold().TextColor(t.TextMuted).Padding(0, 0, 2, 0)
	start := ui.Text(c, line).FontSize(12).Font("monospace").TextColor(t.Text).SingleLine()
	if note != "" {
		start = start.Tooltip(note)
	}
	start.Padding(0, 0, 6, 0)
	side := 0
	if title == "Response" {
		side = 1
	}
	ui.Scroll(c).TrackScroll(&a.headerScroll[side]).Grow(1).Children(func() {
		for i, block := range []http.Header{headers, trailers} {
			if i == 1 && len(block) > 0 {
				ui.Text(c, "Trailers").FontSize(11).Bold().TextColor(t.TextMuted).Padding(10, 0, 4, 0)
			}
			if i == 0 && len(block) == 0 {
				ui.Text(c, raw).FontSize(12).Font("monospace").TextColor(t.Text)
			}
			for _, key := range slices.Sorted(maps.Keys(block)) {
				value := strings.Join(block[key], ", ")
				doc := headerDoc(key)
				ui.Row(c).Gap(6).Padding(4, 0).AlignItems(ui.Start).Children(func() {
					ui.Text(c, key).FontSize(11).Font("monospace").TextColor(t.TextMuted).Width(140).SingleLine().Tooltip(key)
					ui.Box(c).Size(11, 11).Children(func() {
						if doc != "" {
							ui.Icon(c, infoIcon).Size(11, 11).TextColor(t.TextMuted).Tooltip(doc)
						}
					})
					ui.Text(c, value).FontSize(12).Font("monospace").TextColor(t.Text).Grow(1)
				})
			}
		}
	})
}

// messages lists the WebSocket messages or SSE events of a flow.
func (a *app) messages(c *ui.Context, f flow) {
	a.messageUI.view(c, a, f, a.frames[f.id])
}

func (a *app) statusBar(c *ui.Context, t *ui.Theme) {
	ui.Divider(c)
	ui.Row(c).Height(28).Padding(0, 14).Gap(16).AlignItems(ui.Center).Children(func() {
		// Each item stays on one line: the bar narrows as things are turned
		// on, and a wrapped bar reads worse than a short one.
		label := func(s string, color ui.Color) ui.Element {
			return ui.Text(c, s).FontSize(11).TextColor(color).SingleLine()
		}
		label(fmt.Sprintf("%d flows · %d shown", len(a.flows), len(a.visible)), t.TextMuted)
		if a.srv != nil && a.srv.NoCaching() {
			if ui.Button(c, "No Caching · on").FontSize(11).Height(22).Tooltip("Click to restore normal caching for new requests").Clicked() {
				a.srv.SetNoCaching(false)
			}
		}
		captured := humanBytes(int(a.totalBytes))
		if a.totalBytes == 0 {
			captured = "0 B"
		}
		label(captured+" captured", t.TextMuted)
		if n := a.chosen.Len(); n > 0 {
			label(fmt.Sprintf("%d chosen", n), t.Text)
		}
		if a.filtersActive() {
			summary := "filtered: " + a.filterSummary()
			ui.Text(c, "Filtered").FontSize(11).TextColor(t.Accent).SingleLine().Tooltip(summary)
		}
		if a.dropped > 0 {
			label(fmt.Sprintf("%d dropped", a.dropped), ui.Hex("#d97706"))
		}
		lost := a.frameLost.Load()
		if a.srv != nil {
			lost += a.srv.FrameDropped()
		}
		if lost > 0 {
			label(fmt.Sprintf("%d messages dropped", lost), ui.Hex("#d97706"))
		}
		if a.scriptErrs > 0 {
			label(fmt.Sprintf("%d script errors", a.scriptErrs), ui.Hex("#d97706"))
		}
		ui.Spacer(c)
		if a.throttle.Name != "" && a.throttle.Name != "Off" {
			label("Network: "+a.throttle.Name, t.TextMuted)
		}
		if a.upstream != "" {
			label("Upstream: "+a.upstream, t.TextMuted)
		}
		if a.srv != nil {
			if hit, ok := a.srv.LastDNSMatch(); ok {
				label(fmt.Sprintf("DNS %s → %s", hit.Name, hit.IP), t.TextMuted)
			}
		}
		if len(a.rules) > 0 {
			label(fmt.Sprintf("%d rules", len(a.rules)), t.TextMuted)
		}
		if a.caPath != "" {
			if ui.Button(c, "HTTPS setup").Height(22).FontSize(11).Tooltip("Certificate and decryption settings · " + shortenHome(a.caPath)).Clicked() {
				a.settingsTab, a.settingsOpen = 2, true
			}
		}
	})
}

func shortenHome(path string) string {
	if home, err := os.UserHomeDir(); err == nil {
		if rest, ok := strings.CutPrefix(path, home); ok {
			return "~" + rest
		}
	}
	return path
}

func rowColor(chosen bool, t *ui.Theme, normal ui.Color) ui.Color {
	if chosen {
		return t.AccentText
	}
	return normal
}

func methodColor(method string) ui.Color {
	switch method {
	case "GET":
		return ui.Hex("#3b82f6")
	case "POST":
		return ui.Hex("#16a34a")
	case "PUT":
		return ui.Hex("#d97706")
	case "DELETE":
		return ui.Hex("#dc2626")
	case "PATCH":
		return ui.Hex("#7c3aed")
	case "WS":
		return ui.Hex("#0891b2")
	}
	return ui.Hex("#71717a")
}

func (f flow) statusColor(t *ui.Theme) ui.Color {
	if f.status < 400 && f.failed() {
		return t.Danger
	}
	return statusColor(f.status, t)
}

func statusColor(status int, t *ui.Theme) ui.Color {
	switch {
	case status == 0:
		return t.TextMuted
	case status == 304:
		return t.TextMuted
	case status < 300:
		return ui.Hex("#16a34a")
	case status < 400:
		return ui.Hex("#0891b2")
	case status < 500:
		return ui.Hex("#d97706")
	default:
		return ui.Hex("#dc2626")
	}
}

func main() {
	if len(os.Args) == 4 && os.Args[1] == "--proxy-guard" {
		if err := system.GuardProxy(os.Args[2], os.Args[3], os.Stdin, os.Stdout); err != nil {
			log.Fatal(err)
		}
		return
	}
	if err := runDesktop(); err != nil {
		log.Fatal(err)
	}
}

func runDesktop() error {
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
		a.checkSystemProxy() // retry only a recorded, interrupted proxy session
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
	ui.Box(c).Size(16, 16).Radius(4).Background(badgeColor(name)).Center().Children(func() {
		ui.Text(c, letter).FontSize(9).Bold().TextColor(ui.Hex("#ffffff"))
	})
}

// badgeColor is a stable color per app name.
func badgeColor(name string) ui.Color {
	h := fnv.New32a()
	_, _ = h.Write([]byte(name))
	palette := []string{"#2563eb", "#7c3aed", "#db2777", "#ea580c", "#0891b2", "#16a34a", "#ca8a04"}
	return ui.Hex(palette[int(h.Sum32())%len(palette)])
}

// rowMenu is the context menu of a flow's row: what a captured request is
// worth once it leaves the table.
func (a *app) rowMenu(c *ui.Context, f flow) func(*ui.Menu) {
	services := c.Services()
	return func(m *ui.Menu) {
		if m.Item("Copy URL").Chosen() {
			services.WriteClipboard(f.url)
			a.setMessage("Copied URL")
		}
		if m.Item("Copy as cURL").Chosen() {
			a.copyCurl(c, f)
		}
		if f.kind == "" && f.url != "" && m.Item("Open in browser").Chosen() {
			services.OpenURL(f.url)
		}
		if f.resBody != "" && m.Item("Copy response body").Chosen() {
			services.WriteClipboard(f.body(true))
			a.setMessage("Copied response body")
		}
		m.Separator()
		if m.Item(cmp.Or(pinLabel(f), "Pin")).Chosen() {
			a.togglePin(f)
		}
		if m.Item("Mark…").Chosen() {
			a.openMark(f)
		}
		if m.Item("Map this host…").Chosen() {
			a.openDNSRule(hostOnly(f.host), f.process)
		}
		if m.Item("Edit & resend").Chosen() {
			a.composeFrom(f)
		}
		if m.Item("Save as request").Chosen() {
			a.saveFlowAsRequest(f)
		}
		if m.Item("Save as regression…").Disabled(f.status == 0 || f.method == "CONNECT" || f.kind == "ws").Chosen() {
			a.regressionFrom(f)
		}
		m.Submenu("Create rule", func(m *ui.Menu) {
			for _, kind := range ruleTypes {
				label := ruleLabel(kind) + "…"
				if kind == capture.RuleMapLocal {
					label = "Mock this response…"
				}
				if m.Item(label).Disabled(f.url == "" || f.method == "CONNECT").Chosen() {
					a.ruleFromFlow(f, kind)
				}
			}
		})
		if m.Item("Replay").Chosen() {
			a.openReplay([]flow{f})
		}
	}
}

// openCompose shows the request editor with an empty request.
func (a *app) openCompose() {
	a.addDocument(savedRequest{Method: "GET", URL: "https://"}, "")
}

// composeFrom fills the request editor with a captured request, the step from
// traffic to a request worth keeping.
func (a *app) composeFrom(f flow) {
	a.views.capture(a)
	a.addDocument(savedRequest{
		Name:   cmp.Or(f.path, f.url),
		Method: cmp.Or(f.method, "GET"), URL: f.url,
		Headers: headerBlock(f.reqHeader), Body: f.body(false),
	}, "")
	a.sourceFlow, a.source, a.sourceView = f.id, &f, a.views.activeView()
}

// headerBlock writes headers the way the editor takes them back.
func headerBlock(h http.Header) string {
	var b strings.Builder
	for _, key := range slices.Sorted(maps.Keys(h)) {
		for _, value := range h[key] {
			b.WriteString(key + ": " + value + "\n")
		}
	}
	return strings.TrimSuffix(b.String(), "\n")
}

// parseHeaderBlock reads the editor's headers, one "Key: Value" per line.
func parseHeaderBlock(text string) http.Header {
	h := http.Header{}
	for _, line := range strings.Split(text, "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		key, value, ok := strings.Cut(line, ":")
		if !ok {
			continue
		}
		key = strings.TrimSpace(key)
		if key == "" {
			continue
		}
		h.Add(key, strings.TrimSpace(value))
	}
	return h
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
	if a.activeEnv == "" {
		a.activeEnv = name
	}
	if err := saveEnvs(a.envsPath, a.envs, a.activeEnv); err != nil {
		a.setMessage("Variables: " + err.Error())
	}
}

// openVars fills the variables editor with the environment in use.
func (a *app) openVars() {
	a.varsName = cmp.Or(a.activeEnv, "default")
	a.varsText = formatVars(a.activeVars())
	a.varsOpen = true
}

// varsModal edits the variables of one environment, the one a request fills its
// {{name}} from.
func (a *app) varsModal(c *ui.Context, t *ui.Theme) {
	if !a.varsOpen {
		return
	}
	ui.Modal(c, &a.varsOpen, func() {
		ui.Column(c).Width(520).Gap(10).Children(func() {
			ui.Text(c, "Variables").FontSize(14).Bold()
			ui.Text(c, "One \"name = value\" a line. A request uses them as {{name}}, in the URL, the headers, the body and the scripts.").
				FontSize(11).TextColor(t.TextMuted)
			ui.TextInput(c, &a.varsName).Label("Environment")
			ui.TextArea(c, &a.varsText).Height(160).Label("Environment variables")
			ui.Row(c).Gap(8).Justify(ui.End).Children(func() {
				if ui.Button(c, "Cancel").Clicked() {
					a.varsOpen = false
				}
				if ui.PrimaryButton(c, "Save").Clicked() {
					a.saveVars()
				}
			})
		})
	})
}

// saveVars keeps the edited environment and puts it in use.
func (a *app) saveVars() {
	name := strings.TrimSpace(a.varsName)
	if name == "" {
		a.setMessage("Variables: the environment has no name")
		return
	}
	vars := varLines(a.varsText)
	found := false
	for i, e := range a.envs {
		if e.Name == name {
			a.envs[i].Vars = vars
			found = true
			break
		}
	}
	if !found {
		a.envs = append(a.envs, environment{Name: name, Vars: vars})
	}
	a.activeEnv = name
	if err := saveEnvs(a.envsPath, a.envs, a.activeEnv); err != nil {
		a.setMessage("Variables: " + err.Error())
		return
	}
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
	d.composeTests = nil
	d.composeReplies = nil
	d.response = nil
	d.responseEditor = nil
	if a.srv == nil || !a.srv.Running() {
		d.composeResult = "No proxy running. Start capture before sending."
		a.setMessage("Compose: no proxy running")
		return
	}
	d.composeWaiting, d.composeResult = true, "Sending…"
	r, env, vars, srv, jar := d.value(), a.activeEnv, a.activeVars(), a.srv, a.requestCookies()
	if r.Checks != (responseChecks{}) {
		d.responseTab = 2
	}
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		got := executeRequest(ctx, srv, r, vars, jar)
		result := requestResult{document: d, environment: env, summary: got.summary(), response: got.response, tests: got.tests, replies: got.replies, varsBefore: vars, vars: got.vars}
		if got.request != nil {
			result.history = &historyEntry{Method: got.request.Method, URL: got.request.URL, Headers: r.Headers, Body: r.Body, Draft: &r, At: time.Now()}
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
	ui.Modal(c, &a.keyLogOpen, func() {
		ui.Column(c).Width(520).Gap(10).Children(func() {
			ui.Text(c, "TLS key log").FontSize(14).Bold()
			ui.Text(c, "The secrets go to a file Wireshark reads (SSLKEYLOGFILE), so it can decrypt this capture. The engine reads the variable once, when it first handshakes, so a change here takes effect from the next launch.").
				FontSize(11).TextColor(t.TextMuted)
			ui.TextInput(c, &a.keyLogInput).Placeholder("~/.vscroxy/keys.log").Label("File")
			ui.Row(c).Gap(8).Justify(ui.End).Children(func() {
				if a.keyLogPath != "" && ui.Button(c, "Turn off").Clicked() {
					a.setKeyLog("")
					a.keyLogOpen = false
				}
				if ui.Button(c, "Cancel").Clicked() {
					a.keyLogOpen = false
				}
				if ui.PrimaryButton(c, "Save").Clicked() {
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

// filtersActive says whether a content filter narrows the table.
func (a *app) filtersActive() bool {
	return a.filterStatus != "" || a.filterHeader != "" || a.views.bodyFilter() != "" || a.filterTriage != (captureTriage{})
}

// filterSummary is what the status bar says about the filters in use.
func (a *app) filterSummary() string {
	var parts []string
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

// openColumn asks which header the table should show as a column.
func (a *app) openColumn() {
	a.columnName = a.headerColumn
	a.columnOpen = true
}

// columnModal adds a column from a request or response header.
func (a *app) columnModal(c *ui.Context, t *ui.Theme) {
	if !a.columnOpen {
		return
	}
	ui.Modal(c, &a.columnOpen, func() {
		ui.Column(c).Width(440).Gap(10).Children(func() {
			ui.Text(c, "Column from a header").FontSize(14).Bold()
			ui.Text(c, "The table shows the response header's value, or the request's when the response has none. Keep it to one that says something about the exchange, such as x-request-id or content-type.").
				FontSize(11).TextColor(t.TextMuted)
			ui.TextInput(c, &a.columnName).Placeholder("x-request-id").Label("Header")
			ui.Row(c).Gap(8).Justify(ui.End).Children(func() {
				if ui.Button(c, "Cancel").Clicked() {
					a.columnOpen = false
				}
				if ui.PrimaryButton(c, "Show").Clicked() {
					a.setHeaderColumn(strings.TrimSpace(a.columnName))
					a.columnOpen = false
				}
			})
		})
	})
}

// setHeaderColumn puts a header in the table, or takes it out with an empty
// name, and keeps the choice.
func (a *app) setHeaderColumn(name string) {
	a.headerColumn = name
	a.applyFilter()
	if name == "" {
		a.setMessage("Column removed")
	} else {
		a.setMessage("Column: " + name)
	}
	a.persistSettings()
	a.invalidate()
}

// headerValue is what the header column shows for a flow: the response's
// header, or the request's when the response has none.
func (a *app) headerValue(f flow) string {
	if a.headerColumn == "" {
		return ""
	}
	if value := f.respHeader.Get(a.headerColumn); value != "" {
		return value
	}
	return f.reqHeader.Get(a.headerColumn)
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
		case "method":
			c = cmp.Compare(fx.method, fy.method)
		case "proto":
			c = cmp.Compare(fx.scheme, fy.scheme)
		case "status":
			c = cmp.Compare(fx.status, fy.status)
		case "host", "url":
			c = cmp.Compare(fx.url, fy.url)
		case "client":
			c = cmp.Compare(fx.process, fy.process)
		case "path":
			c = cmp.Compare(fx.path, fy.path)
		case "size":
			c = cmp.Compare(fx.bytes, fy.bytes)
		case "time", "duration":
			c = cmp.Compare(fx.millis, fy.millis)
		case "header":
			c = cmp.Compare(a.headerValue(fx), a.headerValue(fy))
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
	ui.Modal(c, open, func() {
		ui.Column(c).Width(520).Gap(10).Children(func() {
			ui.Text(c, title).FontSize(14).Bold()
			ui.Text(c, hint).FontSize(11).TextColor(t.TextMuted)
			ui.TextArea(c, text).Height(140).Label(title)
			ui.Row(c).Gap(8).Justify(ui.End).Children(func() {
				if ui.Button(c, "Cancel").Clicked() {
					*open = false
				}
				if ui.PrimaryButton(c, "Save").Clicked() {
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
	if _, _, _, err := r.Checks.parse(); err != nil {
		a.setMessage("Save: " + err.Error())
		return false
	}
	r.Name, r.Collection = name, strings.TrimSpace(r.Collection)
	r.Method, r.URL = strings.ToUpper(strings.TrimSpace(cmp.Or(r.Method, "GET"))), strings.TrimSpace(r.URL)
	list := upsertRequest(slices.Clone(a.requests), r)
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
			a.headerColumn = saved.HeaderColumn
		}
	}
	if a.requestsPath != "" {
		if err := os.MkdirAll(filepath.Dir(a.requestsPath), 0o755); err != nil {
			a.setMessage("Save: " + err.Error())
			return false
		}
		if err := saveRequests(a.requestsPath, list); err != nil {
			a.setMessage("Save: " + err.Error())
			return false
		}
	}
	a.requests = list
	a.composeName = name
	a.composeCollection, a.composeMethod, a.composeURL = r.Collection, r.Method, r.URL
	a.baseline = a.requestDocument.value()
	a.savedKey = "saved:" + r.Collection + "\x00" + r.Name
	a.requestChoice = a.savedKey
	*a.collectionShown(r.Collection) = true
	a.setMessage("Saved " + name)
	return true
}

// openSaved fills the request editor from a saved request.
func (a *app) openSaved(collection, name string) {
	key := "saved:" + collection + "\x00" + name
	for _, d := range a.documents {
		if d.savedKey == key {
			a.requestDocument = d
			d.revealTab = true
			a.requestChoice = key
			a.workspace = 1
			return
		}
	}
	for _, r := range a.requests {
		if r.Name != name || r.Collection != collection {
			continue
		}
		a.addDocument(r, key)
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
		return
	}
	ui.Modal(c, &a.sslOpen, func() {
		ui.Column(c).Width(520).Gap(10).Children(func() {
			ui.Text(c, "SSL proxying").FontSize(14).Bold()
			ui.Text(c, "HTTPS stays encrypted until a domain is enabled and the client trusts the CA. A domain also matches its subdomains. Changes apply to new connections; reconnect the client and retry.").
				FontSize(11).TextColor(t.TextMuted)
			ui.Row(c).Gap(8).AlignItems(ui.Center).Children(func() {
				ui.Switch(c, &a.sslAll)
				ui.Text(c, "Decrypt every domain").FontSize(12).TextColor(t.Text)
			})
			ui.TextArea(c, &a.sslHosts).Height(120).Label("Decryption domains")
			ui.Row(c).Gap(8).Justify(ui.End).Children(func() {
				if ui.Button(c, "Cancel").Clicked() {
					a.sslOpen = false
				}
				if ui.PrimaryButton(c, "Save").Clicked() {
					a.saveSSL(capture.SSLConfig{All: a.sslAll, Hosts: splitHosts(a.sslHosts)})
					a.sslOpen = false
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
	if cfg.All {
		a.setMessage("SSL proxying: every domain")
	} else {
		a.setMessage(fmt.Sprintf("SSL proxying: %d domains", len(cfg.Hosts)))
	}
	return true
}

// decryptHost adds a domain to what the proxy decrypts, the button under a
// tunnel.
func (a *app) decryptHost(host string) {
	host = strings.ToLower(strings.TrimSuffix(strings.Trim(hostOnly(host), "[]"), "."))
	cfg := capture.SSLConfig{All: a.sslAll, Hosts: splitHosts(a.sslHosts)}
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
		HeaderColumn:     a.headerColumn,
		KeyLogPath:       a.keyLogPath,
		ProxyPort:        a.proxyPort,
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

// pinIcon marks a pinned flow in the table.
var pinIcon = ui.MustParseSVG([]byte(`<svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 16 16"><path d="M10.3 1.2 14.8 5.7 13.2 7.3 11.4 7.1 8.8 9.7 9 11.5 7.6 12.9 3.1 8.4 4.5 7 6.3 7.2 8.9 4.6 8.7 2.8z"/></svg>`))

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

// importRequests adds the requests of a file the user picks, keeping the ones
// already saved.
func (a *app) importRequests() {
	go func() {
		paths, err := mygo.Dialog.Open(mygo.OpenDialogOptions{
			Title:   "Import requests",
			Filters: []mygo.FileFilter{{Name: "Requests", Extensions: []string{"json"}}},
		})
		if err != nil || len(paths) == 0 {
			return
		}
		path := paths[0]
		list, err := loadRequests(path)
		if err != nil {
			a.setMessage("Import requests: " + err.Error())
			return
		}
		a.win.Update(func() {
			for _, r := range list {
				if r.Name != "" {
					a.requests = upsertRequest(a.requests, r)
				}
			}
			if a.requestsPath != "" {
				if err := os.MkdirAll(filepath.Dir(a.requestsPath), 0o755); err == nil {
					if err := saveRequests(a.requestsPath, a.requests); err != nil {
						a.setMessage("Import requests: " + err.Error())
						return
					}
				}
			}
			a.setMessage(fmt.Sprintf("Imported %d requests", len(list)))
		})
	}()
}

// openHistory fills the request editor from a sent request.
func (a *app) openHistory(i int) {
	if i < 0 || i >= len(a.history) {
		return
	}
	e := a.history[i]
	if e.Draft != nil {
		a.addDocument(*e.Draft, "")
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

// saveFlowAsRequest keeps a captured request in the saved ones, under a name
// that says what it is.
func (a *app) saveFlowAsRequest(f flow) {
	name := strings.TrimSpace(f.method + " " + f.host + f.path)
	if f.path == "" {
		name = strings.TrimSpace(f.method + " " + f.host)
	}
	// A second save of the same request takes a name of its own, rather
	// than replacing the first.
	name = uniqueRequestName(a.requests, name)
	r := savedRequest{
		Name:    name,
		Method:  cmp.Or(f.method, "GET"),
		URL:     f.url,
		Headers: headerBlock(f.reqHeader),
		Body:    f.body(false),
	}
	a.requests = upsertRequest(a.requests, r)
	if a.requestsPath != "" {
		if err := os.MkdirAll(filepath.Dir(a.requestsPath), 0o755); err != nil {
			a.setMessage("Save request: " + err.Error())
			return
		}
		if err := saveRequests(a.requestsPath, a.requests); err != nil {
			a.setMessage("Save request: " + err.Error())
			return
		}
	}
	a.setMessage("Saved " + name)
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
	ui.Modal(c, &a.dnsOpen, func() {
		ui.Column(c).Width(560).Gap(10).Children(func() {
			ui.Text(c, "DNS mapping").FontSize(14).Bold()
			ui.Text(c, "Names the proxy resolves itself, one \"name = address\" a line. The request keeps its name and Host header, so a device pointed at this proxy reaches the machine you map: example.com = 192.168.1.50. Add \"@ Chrome, com.example.app\" to map the name for those apps only, or right-click a request and pick \"Map this host…\" to choose the app there. A name matches its subdomains; *.example.com matches one level. Flows that hit a mapping carry a DNS tag in the table.").
				FontSize(11).TextColor(t.TextMuted)
			ui.TextArea(c, &a.dnsText).Height(140).Label("DNS mappings")
			ui.Row(c).Gap(8).Justify(ui.End).Children(func() {
				if ui.Button(c, "Cancel").Clicked() {
					a.dnsOpen = false
				}
				if ui.PrimaryButton(c, "Save").Clicked() {
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
	ui.Modal(c, &a.dnsRuleOpen, func() {
		ui.Column(c).Width(460).Gap(10).Children(func() {
			ui.Text(c, "Map a host").FontSize(14).Bold()
			ui.Text(c, "The request keeps its name, URL, Host header and SNI; the proxy dials the address instead.").
				FontSize(11).TextColor(t.TextMuted)
			ui.TextInput(c, &a.dnsRuleHost).Label("Host")
			ui.TextInput(c, &a.dnsRuleAddr).Label("Address")
			ui.Text(c, "Applies to").FontSize(11).TextColor(t.TextMuted)
			ui.Select(c, &a.dnsRuleScope, a.dnsRuleApps)
			ui.Row(c).Gap(8).Justify(ui.End).Children(func() {
				if ui.Button(c, "Cancel").Clicked() {
					a.dnsRuleOpen = false
				}
				if ui.PrimaryButton(c, "Save").Clicked() {
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
