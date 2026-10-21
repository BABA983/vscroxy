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
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/baba983/vscroxy/desktop/internal/appicon"
	"github.com/baba983/vscroxy/desktop/internal/capture"
	"github.com/baba983/vscroxy/desktop/internal/system"
	"github.com/egoist/mygo"
	"github.com/egoist/mygo/ui"
	"github.com/egoist/mygo/ui/editor"
	"github.com/egoist/mygo/ui/richtext"
)

// maxFlows bounds what the UI keeps; older flows are dropped from the front.
const maxFlows = 5000

// app is the state the window shows; view reads it and the controls write it.
type app struct {
	flows   []flow
	visible []int
	domains []domain
	clients []domain

	query  string
	filter string
	chip   int

	// chosen are the rows chosen for a bulk action, by flow id.
	chosen ui.Selection[int]
	picked int
	tab    int

	// sort is the column the table is sorted by, and sortApplied what the
	// rows were sorted with, so a header click sorts on the next frame.
	sort        ui.SortOrder
	sortApplied ui.SortOrder

	sidebarWidth float32
	tablePercent float32

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
	detailFor int
	body      string
	reqBody   string
	diff      *editor.DiffState
	frames    map[int][]frame

	bodySide int
	bodyMode int

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
	protoFields  []pbField
	jsonOK       bool

	srv        *capture.Server
	win        *mygo.Window
	caPath     string
	rulesPath  string
	scriptPath string
	rules      []capture.Rule
	throttle   capture.Throttle
	upstream   string
	errText    string
	dropped    int64
	scriptErrs int64

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
	settingsPath string
	saved        settings
	lastSave     time.Time
	columns      ui.TableLayout

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

	// connOpen shows the connection panel of the chosen flow.
	connOpen bool

	// headerColumn is the header the table shows as a column of its own, and
	// the filter fields narrow what it shows beyond the search box.
	headerColumn string
	filterOpen   bool
	filterStatus string
	filterHeader string
	filterBody   string
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

	// compose is the request being written: what Compose sends through the
	// proxy so the exchange is captured like any other.
	composeOpen       bool
	composeName       string
	composeCollection string
	// composeTitle says what the editor is for, and the send's result shows
	// under its buttons so a request can be sent again after a change.
	composeTitle   string
	composeResult  string
	composeWaiting bool
	composeBefore  int
	composeMethod  string
	composeURL     string
	composeHeaders string
	composeBody    string

	// composeTab picks which script the editor shows: the body, the script
	// that runs before the send, or the one that runs after the response.
	composeTab     int
	composePre     string
	composePost    string
	composeTests   []testResult
	composeReplies []string
	// composeSent is what a send resolved to once variables and the
	// pre-request script had their say, so the table's row is found.
	composeSentMethod string
	composeSentURL    string

	// envs are the variables a request uses as {{name}}, one set in use.
	envsPath  string
	envs      []environment
	activeEnv string
	varsOpen  bool
	varsName  string
	varsText  string

	breakOn  bool
	bp       *capture.Breakpoint
	bpOpen   bool
	bpMethod string
	bpURL    string
	bpStatus string
	bpBody   string

	// mu guards what the capture goroutine hands to the view: the flows not
	// yet drained, and a startup error.
	mu            sync.Mutex
	pending       []capture.Flow
	pendingFrames []capture.FrameEvent
	bps           []capture.Breakpoint
	err           error
	message       string
}

type domain struct {
	name string
	n    int
}

// frame is one WebSocket message or SSE event the detail pane lists.
type frame struct {
	direction string
	kind      string
	data      string
}

func newApp() *app {
	a := &app{
		filter:         "all",
		icons:          make(map[string]ui.ImageSource),
		sidebarWidth:   220,
		tablePercent:   62,
		trafficOpen:    true,
		domainsOpen:    true,
		clientsOpen:    true,
		historyOpen:    true,
		collectionOpen: make(map[string]*bool),
		detailFor:      -1,
		frames:         make(map[int][]frame),
		throttle:       capture.Presets[0],
	}
	srv, err := capture.New("127.0.0.1:7890", "")
	if err != nil {
		// Keep the window usable when the port is taken: show seeded flows
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
		if list, err := loadRequests(a.requestsPath); err == nil {
			a.requests = list
		}
	}
	a.countDomains()
	a.countClients()
	a.applyFilter()
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

// setMessage records a short status line, from any goroutine.
func (a *app) setMessage(msg string) {
	a.mu.Lock()
	a.message = clamp(msg, 72)
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

// clamp keeps a status message on one line.
func clamp(msg string, max int) string {
	r := []rune(msg)
	if len(r) <= max {
		return msg
	}
	return string(r[:max-1]) + "…"
}

// setSystemProxy turns the machine's proxy on or off. The platform helpers
// may block on a password prompt, so they run off the UI thread.
func (a *app) setSystemProxy(on bool) {
	go func() {
		var err error
		if on {
			err = system.SetProxy(system.ProxyAddr)
		} else {
			err = system.ClearProxy()
		}
		if err != nil {
			a.setMessage("System proxy: " + err.Error())
			return
		}
		if on {
			a.setMessage("System proxy: " + system.ProxyAddr)
		} else {
			a.setMessage("System proxy: off")
		}
	}()
}

// installCA trusts the CA that the proxy uses to sign intercepted TLS.
func (a *app) installCA() {
	if a.caPath == "" {
		a.setMessage("Certificate: no path known")
		return
	}
	path := a.caPath
	go func() {
		if _, err := os.Stat(path); err != nil {
			a.setMessage("Certificate not found; capture one request first")
			return
		}
		if err := system.InstallCA(path); err != nil {
			a.setMessage("Install certificate: " + err.Error())
			return
		}
		a.setMessage("Certificate trusted")
	}()
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
func (a *app) replay(f flow) {
	if a.srv == nil || f.url == "" {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	err := a.srv.Replay(ctx, capture.Flow{
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
		a.setMessage("Replay failed: " + err.Error())
		return
	}
	a.setMessage("Replayed " + f.method + " " + f.path)
}

func (a *app) startCapture() {
	if a.srv == nil {
		return
	}
	if err := a.srv.Start(); err != nil {
		a.mu.Lock()
		a.err = err
		a.mu.Unlock()
		a.win.Invalidate()
		return
	}
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
		a.pendingFrames = append(a.pendingFrames, ev)
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
func (a *app) resolveBreak(abort bool) {
	bp := a.bp
	if bp == nil {
		return
	}
	if a.srv != nil {
		status := 0
		if bp.Phase == capture.PhaseResponse {
			status, _ = strconv.Atoi(strings.TrimSpace(a.bpStatus))
		}
		a.srv.Resume(bp.ID, capture.Decision{
			Abort:  abort,
			Method: a.bpMethod,
			URL:    a.bpURL,
			Status: status,
			Body:   a.bpBody,
		})
	}
	a.bp = nil
	a.bpOpen = false
	if abort {
		a.setMessage("Aborted " + bp.Method + " " + bp.URL)
		return
	}
	a.setMessage("Continued " + bp.Method + " " + bp.URL)
}

// drain moves the flows the capture goroutine collected into the table.
func (a *app) drain() {
	a.mu.Lock()
	pending := a.pending
	a.pending = nil
	err := a.err
	a.mu.Unlock()

	if err != nil {
		a.errText = err.Error()
		a.err = nil
	}
	a.mu.Lock()
	pendingFrames := a.pendingFrames
	a.pendingFrames = nil
	a.mu.Unlock()
	for _, ev := range pendingFrames {
		list := append(a.frames[ev.FlowID], frame{
			direction: ev.Frame.Direction,
			kind:      ev.Frame.Kind,
			data:      ev.Frame.Data,
		})
		if len(list) > 200 {
			list = list[len(list)-200:]
		}
		a.frames[ev.FlowID] = list
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
			resBody:     f.ResponseBody,
			resFile:     f.ResponseBodyFile,
			resSize:     f.ResponseBodySize,
			reqHeader:   f.RequestHeader,
			respHeader:  f.ResponseHeader,
			at:          f.At,
			kind:        kind,
			process:     f.Process,
			processPath: f.ProcessPath,
			dns:         f.DNSMapped,
			conn: conn{
				proto:     f.Conn.Proto,
				client:    f.Conn.Client,
				proxy:     f.Conn.Proxy,
				server:    f.Conn.Server,
				resolved:  f.Conn.Resolved,
				lookupMs:  f.Conn.LookupMs,
				tls:       f.Conn.TLS,
				cipher:    f.Conn.Cipher,
				certCN:    f.Conn.CertCN,
				issuer:    f.Conn.Issuer,
				certUntil: f.Conn.CertUntil,
				flows:     f.Conn.Flows,
				reused:    f.Conn.Reused,
				dnsFresh:  f.Conn.DNSFresh,
			},
			wait:     f.Wait,
			download: f.Download,
		})
	}
	for _, f := range a.flows[len(a.flows)-len(pending):] {
		a.totalBytes += int64(f.bytes)
	}
	a.noteComposed()
	if len(a.flows) > maxFlows {
		cut := len(a.flows) - maxFlows
		a.flows = a.flows[cut:]
		a.detailFor = -1
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
		if a.filter == "errors" && f.status < 400 {
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
		if a.filterBody != "" && !strings.Contains(strings.ToLower(f.reqBody+f.resBody), strings.ToLower(a.filterBody)) {
			continue
		}
		a.visible = append(a.visible, i)
	}
	a.sortVisible()
	if a.picked >= len(a.visible) {
		a.picked = len(a.visible) - 1
	}
	if a.picked < 0 {
		a.picked = 0
	}
	a.detailFor = -1
}

// current is the flow the detail panes show, if any.
func (a *app) current() (flow, bool) {
	if len(a.visible) == 0 || a.picked < 0 || a.picked >= len(a.visible) {
		return flow{}, false
	}
	return a.flows[a.visible[a.picked]], true
}

// previousResponse is the last response for the same method and URL, the
// other side of the diff. Empty when this is the first one.
func (a *app) previousResponse() string {
	if len(a.visible) == 0 || a.picked < 0 || a.picked >= len(a.visible) {
		return ""
	}
	at := a.visible[a.picked]
	f := a.flows[at]
	for i := at - 1; i >= 0; i-- {
		prev := a.flows[i]
		if prev.method == f.method && prev.scheme == f.scheme && prev.host == f.host && prev.path == f.path && prev.resBody != "" {
			return prev.body(true)
		}
	}
	return ""
}

// syncDetail rebuilds the text and the diff when the choice moves.
func (a *app) syncDetail() {
	f, ok := a.current()
	if !ok || a.detailFor == f.id {
		return
	}
	a.detailFor = f.id
	a.connOpen = false
	if f.kind == "" && a.tab == 4 {
		a.tab = 0
	}
	a.body = f.body(true)
	a.reqBody = f.body(false)
	a.jsonFor = ""
	a.diff = &editor.DiffState{
		Old:    editor.NewBuffer(a.previousResponse()),
		New:    editor.NewBuffer(a.body),
		Layout: editor.DiffSplit,
	}
	a.diff.Old.SetLanguage(editor.LanguageFor("response.json"))
	a.diff.New.SetLanguage(editor.LanguageFor("response.json"))
}

func (a *app) view(c *ui.Context) {
	if a.dark {
		c.SetTheme(ui.DarkTheme())
	}
	a.drain()
	if a.sort != a.sortApplied {
		a.sortApplied = a.sort
		a.applyFilter()
	}
	a.syncDetail()
	t := c.Theme()
	ui.Column(c).Fill().Background(t.Background).Children(func() {
		a.toolbar(c, t)
		ui.Split(c, &a.sidebarWidth, func() {
			a.sidebar(c)
		}, func() {
			ui.SplitVerticalPercent(c, &a.tablePercent, func() {
				a.table(c)
			}, func() {
				a.detail(c)
			}).Fill()
		}).Grow(1)
		a.statusBar(c, t)
	})
	a.persistSettings()
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
	a.composeModal(c, t)
	// The variables editor opens from the request editor, so it is drawn
	// after it and over it.
	a.varsModal(c, t)
	a.markModal(c, t)
	a.filterModal(c, t)
	a.columnModal(c, t)
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
	if ui.Modal(c, &a.bpOpen, func() {
		ui.Column(c).Width(640).Gap(10).Children(func() {
			title := "Breakpoint · request paused"
			if a.bp.Phase == capture.PhaseResponse {
				title = "Breakpoint · response paused"
			}
			ui.Text(c, title).FontSize(14).Bold()
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
			ui.TextArea(c, &a.bpBody).Height(200).Label("Body")
			ui.Row(c).Gap(8).Justify(ui.End).Children(func() {
				if ui.Button(c, "Abort").Clicked() {
					a.resolveBreak(true)
				}
				if ui.PrimaryButton(c, "Continue").Clicked() {
					a.resolveBreak(false)
				}
			})
		})
	}).Dismissed() {
		a.resolveBreak(true)
	}
}

func (a *app) toolbar(c *ui.Context, t *ui.Theme) {
	live := a.srv != nil && a.errText == ""
	dot := ui.Hex("#dc2626")
	label := "Capture stopped"
	if live {
		dot = ui.Hex("#22c55e")
		label = "Live · " + a.srv.Addr()
	}
	// A narrow window keeps the controls and drops what explains them.
	width := 0
	if a.win != nil {
		width, _ = a.win.Size()
	}
	narrow := width > 0 && width < 1100
	searchWidth := float32(240)
	switch {
	case width > 0 && width < 1200:
		searchWidth = 150
	case width > 0 && width < 1360:
		searchWidth = 180
	}
	ui.Row(c).Height(46).Padding(0, 14).Gap(10).AlignItems(ui.Center).Children(func() {
		ui.Box(c).Size(8, 8).Radius(4).Background(dot)
		if !narrow {
			ui.Text(c, label).FontSize(12).TextColor(t.TextMuted).SingleLine().Shrink(0)
		}
		ui.Spacer(c)
		if ui.SearchField(c, &a.query).Width(searchWidth).Shrink(1).Changed() {
			a.applyFilter()
		}
		ui.MenuButton(c, "Network", func(m *ui.Menu) {
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
		})
		ui.MenuButton(c, "System", func(m *ui.Menu) {
			if m.Item("Enable system proxy").Chosen() {
				a.setSystemProxy(true)
			}
			if m.Item("Disable system proxy").Chosen() {
				a.setSystemProxy(false)
			}
			m.Separator()
			if m.Item("Install certificate…").Chosen() {
				a.installCA()
			}
			if m.Item("Reveal certificate").Chosen() {
				a.revealCA()
			}
			m.Separator()
			if m.Item("Dark mode").Checked(a.dark).Chosen() {
				a.dark = !a.dark
			}
		})
		if ui.Button(c, "Reload").Clicked() {
			a.reloadConfig()
		}
		ui.MenuButton(c, "Session", func(m *ui.Menu) {
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
		})
		ui.MenuButton(c, "View", func(m *ui.Menu) {
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
		})
		if ui.Button(c, "Compose").Clicked() {
			a.openCompose()
		}
		if ui.Button(c, "Latest").Clicked() && len(a.visible) > 0 {
			a.picked = len(a.visible) - 1
		}
		ui.Text(c, "Break").FontSize(11).TextColor(t.TextMuted)
		if ui.Switch(c, &a.breakOn).Changed() && a.srv != nil {
			a.srv.SetBreak(a.breakOn)
		}
		if ui.Button(c, "Clear").Clicked() {
			a.mu.Lock()
			a.pending = nil
			a.mu.Unlock()
			// A pinned flow stays, the reason it was pinned.
			a.flows = slices.DeleteFunc(a.flows, func(f flow) bool { return !f.pinned })
			a.frames = make(map[int][]frame)
			a.chosen.Clear()
			a.picked = 0
			a.detailFor = -1
			a.query = ""
			a.filter = "all"
			a.chip = 0
			a.totalBytes = 0
			for _, f := range a.flows {
				a.totalBytes += int64(f.bytes)
			}
			a.countDomains()
			a.countClients()
			a.applyFilter()
		}
	})
	ui.Divider(c)
	ui.Row(c).Height(36).Padding(0, 14).AlignItems(ui.Center).Children(func() {
		if ui.Segmented(c, &a.chip, "All", "HTTP", "HTTPS", "WebSocket", "SSE", "JSON", "Images", "HTML").Changed() {
			a.applyFilter()
		}
	})
	ui.Divider(c)
}

func (a *app) sidebar(c *ui.Context) {
	t := c.Theme()
	ui.Column(c).Fill().Background(t.Surface).Children(func() {
		chosen := ui.Sidebar(c, &a.filter, func() {
			ui.SidebarSection(c, "Traffic", &a.trafficOpen, func() {
				ui.SidebarItem(c, "all", nil, "All requests")
				ui.SidebarItem(c, "errors", nil, "Errors").Children(func() {
					ui.Badge(c, fmt.Sprint(a.count(func(f flow) bool { return f.status >= 400 })))
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
			if len(a.history) > 0 {
				ui.SidebarSection(c, "History", &a.historyOpen, func() {
					for i, e := range a.history {
						ui.SidebarItem(c, "history:"+strconv.Itoa(i), nil, e.label())
					}
				})
			}
			if len(a.requests) > 0 {
				groups := groupRequests(a.requests)
				for _, name := range slices.Sorted(maps.Keys(groups)) {
					label := name
					if label == "" {
						label = "Saved"
					}
					ui.SidebarSection(c, label, a.collectionShown(name), func() {
						for _, r := range groups[name] {
							ui.SidebarItem(c, "saved:"+r.Collection+"/"+r.Name, nil, r.Name)
						}
					})
				}
			}
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
			if at, ok := strings.CutPrefix(a.filter, "history:"); ok {
				if i, err := strconv.Atoi(at); err == nil {
					a.openHistory(i)
				}
				a.filter = "all"
			} else if key, ok := strings.CutPrefix(a.filter, "saved:"); ok {
				collection, name, _ := strings.Cut(key, "/")
				a.openSaved(collection, name)
				a.filter = "all"
			} else {
				a.applyFilter()
			}
		}
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
			ui.Text(c, "Waiting for traffic").FontSize(14).TextColor(t.Text)
			ui.Text(c, "Set your HTTP proxy to 127.0.0.1:7890").FontSize(12).TextColor(t.TextMuted)
		})
		return
	}
	cols := []ui.TableColumn{
		{ID: "method", Title: "Method", Width: 80, Sortable: true},
		{ID: "status", Title: "Status", Width: 78, Align: ui.End, Sortable: true},
		{ID: "url", Title: "URL", Sortable: true},
	}
	headerCol := -1
	if a.headerColumn != "" {
		headerCol = len(cols)
		cols = append(cols, ui.TableColumn{ID: "header", Title: a.headerColumn, Width: 150, Sortable: true})
	}
	clientCol := len(cols)
	cols = append(cols,
		ui.TableColumn{ID: "client", Title: "Client", Width: 132, Sortable: true},
		ui.TableColumn{ID: "duration", Title: "Duration", Width: 96, Align: ui.End, Sortable: true},
		ui.TableColumn{ID: "size", Title: "Size", Width: 86, Align: ui.End, Sortable: true},
		ui.TableColumn{Title: "", ID: "filler", Width: 14, Fixed: true},
	)
	durationCol, sizeCol := clientCol+1, clientCol+2
	state := &ui.ListState{
		Selected:  &a.picked,
		Selection: &a.chosen,
		Key:       func(row int) any { return a.flows[a.visible[row]].id },
		Sort:      &a.sort,
		Columns:   a.columns,
	}
	ui.Table(c, state, cols, len(a.visible), func(row, col int) {
		f := a.flows[a.visible[row]]
		chosen := row == a.picked
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
			if f.kind == "connect" && f.status == 0 {
				color := ui.Hex("#d97706")
				if chosen {
					color = t.AccentText
				}
				text("Tunnel").TextColor(color)
				return
			}
			color := statusColor(f.status, t)
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
				text(f.host + f.path).TextColor(rest).ContextMenu(a.rowMenu(c, f))
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
	t := c.Theme()
	ui.Column(c).Fill().Background(t.Background).Children(func() {
		f, ok := a.current()
		ui.Row(c).Height(38).Padding(0, 12).Gap(10).AlignItems(ui.Center).Children(func() {
			if !ok {
				ui.Text(c, "No request chosen").FontSize(12).TextColor(t.TextMuted)
				return
			}
			ui.Text(c, fmt.Sprintf("%s %s://%s%s", f.method, f.scheme, f.host, f.path)).
				FontSize(12).TextColor(t.Text).SingleLine().Grow(1)
			if f.process != "" {
				ui.Text(c, f.process).FontSize(12).TextColor(t.TextMuted)
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
				ui.Text(c, f.note).FontSize(12).TextColor(t.TextMuted).SingleLine().Width(220).Tooltip(f.note)
			}
			if ui.Button(c, "Replay").Disabled(f.url == "" || f.kind != "").Clicked() {
				go a.replay(f)
			}
			chip := ui.Text(c, fmt.Sprintf("%d · %s · %d ms", f.status, humanBytes(f.bytes), f.millis)).
				FontSize(12).TextColor(statusColor(f.status, t))
			if doc := statusDoc(f.status); doc != "" {
				chip = chip.Tooltip(doc)
			}
			info := ui.Icon(c, infoIcon).Size(13, 13).TextColor(t.TextMuted).
				Tooltip("Connection: addresses, DNS and TLS").
				OnClick(func() { a.connOpen = !a.connOpen })
			ui.Popover(c, info, &a.connOpen, func() { a.connPanel(c, t, f) })
		})
		if f.kind == "connect" {
			a.connectPanel(c, f)
			return
		}
		labels := []string{"Headers", "Body", "Diff", "Timing"}
		if f.kind == "ws" || f.kind == "sse" {
			labels = append(labels, "Messages")
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
			if a.diff != nil {
				editor.DiffView(c, a.diff).Grow(1).FillWidth()
			}
		case 3:
			a.timingView(c, f)
		case 4:
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
	a.detailFor = -1
	a.invalidate()
	a.setMessage("Marked")
}

// infoIcon marks a header the Headers tab can explain, or a connection detail
// worth opening.
var infoIcon = ui.MustParseSVG([]byte(`<svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 16 16"><path d="M8 1.3a6.7 6.7 0 1 0 0 13.4A6.7 6.7 0 0 0 8 1.3Zm0 1.5a5.2 5.2 0 1 1 0 10.4A5.2 5.2 0 0 1 8 2.8Zm0 1.6a.95.95 0 1 0 0 1.9.95.95 0 0 0 0-1.9Zm-.85 3.2v4h1.7v-4Z"/></svg>`))

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
	ui.Scroll(c).Grow(1).Padding(12).Children(func() {
		a.headerBlock(c, t, "Request", f.method+" "+f.path, "", f.reqHeader, f.reqHeaders)
		line := "No response"
		if f.status != 0 {
			line = fmt.Sprintf("%d %s", f.status, http.StatusText(f.status))
		}
		a.headerBlock(c, t, "Response", line, statusDoc(f.status), f.respHeader, f.resHeaders)
	})
}

// headerBlock is one side of the Headers tab: the line that starts the message
// and then its headers, sorted by name.
func (a *app) headerBlock(c *ui.Context, t *ui.Theme, title, line, note string, headers http.Header, raw string) {
	ui.Text(c, title).FontSize(11).Bold().TextColor(t.TextMuted).Padding(0, 0, 2, 0)
	start := ui.Text(c, line).FontSize(12).Font("monospace").TextColor(t.Text).SingleLine()
	if note != "" {
		start = start.Tooltip(note)
	}
	start.Padding(0, 0, 6, 0)
	if len(headers) == 0 {
		ui.Text(c, raw).FontSize(12).Font("monospace").TextColor(t.Text).Padding(0, 0, 14, 0)
		return
	}
	for _, key := range slices.Sorted(maps.Keys(headers)) {
		value := strings.Join(headers[key], ", ")
		doc := headerDoc(key)
		ui.Row(c).Gap(6).Padding(2, 0).AlignItems(ui.Start).Children(func() {
			ui.Text(c, key).FontSize(12).Font("monospace").TextColor(ui.Hex("#a1a1aa")).Width(210).SingleLine()
			ui.Box(c).Size(11, 11).Children(func() {
				if doc != "" {
					ui.Icon(c, infoIcon).Size(11, 11).TextColor(t.TextMuted).Tooltip(doc)
				}
			})
			ui.Text(c, value).FontSize(12).Font("monospace").TextColor(t.Text).Grow(1)
		})
	}
	ui.Box(c).Height(14)
}

// messages lists the WebSocket messages or SSE events of a flow.
func (a *app) messages(c *ui.Context, f flow) {
	t := c.Theme()
	list := a.frames[f.id]
	if len(list) == 0 {
		ui.Text(c, "No messages yet").FontSize(12).TextColor(t.TextMuted).Padding(16)
		return
	}
	ui.List(c, nil, len(list), func(i int) {
		m := list[i]
		arrow, color := "→", ui.Hex("#3b82f6")
		if m.direction == "in" {
			arrow, color = "←", ui.Hex("#16a34a")
		}
		ui.Row(c).Gap(10).Padding(4, 12).Children(func() {
			ui.Text(c, arrow).FontSize(12).TextColor(color).Width(16)
			ui.Text(c, m.kind).FontSize(11).TextColor(t.TextMuted).Width(52)
			ui.Text(c, m.data).FontSize(12).Font("monospace").TextColor(t.Text).SingleLine().Grow(1)
		})
	}).Grow(1)
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
			ui.Text(c, summary).FontSize(11).TextColor(t.Text).Width(240).SingleLine().Tooltip(summary)
		}
		if a.dropped > 0 {
			label(fmt.Sprintf("%d dropped", a.dropped), ui.Hex("#d97706"))
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
		a.mu.Lock()
		message := a.message
		a.mu.Unlock()
		if message != "" {
			ui.Text(c, message).FontSize(11).TextColor(t.TextMuted).Width(320).SingleLine().Tooltip(message)
		}
		if a.caPath != "" {
			label("CA "+shortenHome(a.caPath), t.TextMuted)
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
	a := newApp()
	mygo.App.WhenReady(func() {
		a.win = mygo.NewWindow(mygo.WindowOptions{
			Title:     "vscroxy",
			Width:     1280,
			Height:    800,
			MinWidth:  960,
			MinHeight: 600,
			// MyGo keeps the window's bounds under this key.
			StateKey: "vscroxy",
			Content:  ui.View(a.view),
		})
		go a.startCapture()
	})
	if err := mygo.App.Run(); err != nil {
		log.Fatal(err)
	}
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
			services.WriteClipboard(curlCommand(f))
			a.setMessage("Copied cURL command")
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
		if m.Item("Replay").Chosen() {
			go a.replay(f)
		}
	}
}

// openCompose shows the request editor with an empty request.
func (a *app) openCompose() {
	a.composeTitle = "Compose"
	a.composeResult = ""
	a.composeName = ""
	a.composeCollection = ""
	a.composeMethod = "GET"
	a.composeURL = "https://"
	a.composeHeaders = ""
	a.composeBody = ""
	a.composePre = ""
	a.composePost = ""
	a.composeTab = 0
	a.composeTests = nil
	a.composeReplies = nil
	a.composeSentMethod, a.composeSentURL = "", ""
	a.composeOpen = true
}

// composeFrom fills the request editor with a captured request, the step from
// traffic to a request worth keeping.
func (a *app) composeFrom(f flow) {
	a.composeTitle = "Edit & resend"
	a.composeResult = ""
	a.composeName = ""
	a.composeCollection = ""
	a.composeMethod = cmp.Or(f.method, "GET")
	a.composeURL = f.url
	a.composeHeaders = headerBlock(f.reqHeader)
	a.composeBody = f.body(false)
	a.composePre = ""
	a.composePost = ""
	a.composeTab = 0
	a.composeTests = nil
	a.composeReplies = nil
	a.composeSentMethod, a.composeSentURL = "", ""
	a.composeOpen = true
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

// composeModal draws the request editor and sends what it holds through the
// proxy.
func (a *app) composeModal(c *ui.Context, t *ui.Theme) {
	if !a.composeOpen {
		return
	}
	ui.Modal(c, &a.composeOpen, func() {
		ui.Column(c).Width(680).Gap(10).Children(func() {
			ui.Row(c).Gap(8).AlignItems(ui.Center).Children(func() {
				ui.Text(c, cmp.Or(a.composeTitle, "Compose")).FontSize(14).Bold().Grow(1)
				if names := a.envNames(); len(names) > 0 {
					ui.Text(c, "Variables").FontSize(11).TextColor(t.TextMuted)
					ui.Select(c, &a.activeEnv, names).Width(140)
				}
				if ui.Button(c, "Edit…").Clicked() {
					a.openVars()
				}
			})
			ui.Row(c).Gap(8).Children(func() {
				ui.TextInput(c, &a.composeName).Grow(1).Placeholder("Name").Label("Name")
				ui.TextInput(c, &a.composeCollection).Width(200).Placeholder("Collection").Label("Collection")
			})
			ui.Row(c).Gap(8).Children(func() {
				ui.TextInput(c, &a.composeMethod).Width(90).Label("Method")
				ui.TextInput(c, &a.composeURL).Grow(1).Label("URL")
			})
			ui.Text(c, "Headers").FontSize(11).TextColor(t.TextMuted)
			ui.TextArea(c, &a.composeHeaders).Height(88).Label("Headers")
			ui.Tabs(c, &a.composeTab, "Body", "Pre-request", "Tests")
			switch a.composeTab {
			case 0:
				ui.TextArea(c, &a.composeBody).Height(140).Label("Body")
				ui.Text(c, "A ws:// or wss:// URL sends this body as one text message, through the proxy, and shows what comes back.").
					FontSize(11).TextColor(t.TextMuted)
			case 1:
				ui.TextArea(c, &a.composePre).Height(140).Label("Pre-request script")
				ui.Text(c, "req.method, req.url, req.body; req.get/set/remove(\"Header\", value); vars.get/set(name, value); test(name, pass); log(…)").
					FontSize(11).TextColor(t.TextMuted)
			case 2:
				ui.TextArea(c, &a.composePost).Height(120).Label("Post-response script")
				ui.Text(c, "res.status, res.body, res.get(\"Header\"), res.json(); test(name, pass); vars.set(name, value)").
					FontSize(11).TextColor(t.TextMuted)
				// The assertions belong beside the script that made them.
				for _, result := range a.composeTests {
					mark, color := "✓", t.Success
					if !result.Pass {
						mark, color = "✗", t.Danger
					}
					ui.Text(c, mark+" "+result.Name).FontSize(11).TextColor(color)
				}
			}
			ui.Row(c).Gap(8).Justify(ui.End).Children(func() {
				if ui.Button(c, "Cancel").Clicked() {
					a.composeOpen = false
				}
				if ui.Button(c, "Save").Clicked() {
					a.saveComposed()
				}
				if ui.PrimaryButton(c, "Send").Clicked() {
					a.sendComposed()
				}
			})
			if a.composeResult != "" {
				ui.Text(c, a.composeResult).FontSize(11).TextColor(t.TextMuted)
			}
			for _, reply := range a.composeReplies {
				ui.Text(c, "← "+reply).FontSize(11).Font("monospace").TextColor(t.Text).SingleLine().Tooltip(reply)
			}
		})
	})
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

// applyVars keeps what a script set, in the environment in use.
func (a *app) applyVars(vars map[string]string) {
	name := cmp.Or(a.activeEnv, "default")
	found := false
	for i, e := range a.envs {
		if e.Name == name {
			a.envs[i].Vars = maps.Clone(vars)
			found = true
			break
		}
	}
	if !found {
		a.envs = append(a.envs, environment{Name: name, Vars: maps.Clone(vars)})
	}
	a.activeEnv = name
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
			ui.TextArea(c, &a.varsText).Height(160).Label("Variables")
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
	if a.srv == nil {
		a.setMessage("Compose: no proxy running")
		return
	}
	vars := a.activeVars()
	req := &jsRequest{
		Method: strings.ToUpper(strings.TrimSpace(cmp.Or(expandVars(a.composeMethod, vars), "GET"))),
		URL:    strings.TrimSpace(expandVars(a.composeURL, vars)),
		Body:   expandVars(a.composeBody, vars),
		header: parseHeaderBlock(expandVars(a.composeHeaders, vars)),
	}
	if req.URL == "" {
		a.setMessage("Compose: the URL is empty")
		return
	}
	run := &scriptRun{vars: maps.Clone(vars)}
	if err := runScript(a.composePre, run, map[string]any{"req": req}); err != nil {
		a.mu.Lock()
		a.composeResult = "Pre-request script: " + err.Error()
		a.composeTests = nil
		a.mu.Unlock()
		return
	}
	if !maps.Equal(run.vars, vars) {
		a.applyVars(run.vars)
	}
	if isWebSocket(req.URL) {
		a.sendWebSocket(req)
		return
	}
	f := capture.Flow{
		Method:        req.Method,
		URL:           req.URL,
		RequestHeader: req.header,
		RequestBody:   req.Body,
	}
	before := 0
	if n := len(a.flows); n > 0 {
		before = a.flows[n-1].id
	}
	a.mu.Lock()
	a.composeBefore = before
	a.composeWaiting = true
	a.composeResult = "Sending…"
	a.composeTests = nil
	a.composeSentMethod, a.composeSentURL = req.Method, req.URL
	a.mu.Unlock()
	a.history = pushHistory(a.history, historyEntry{
		Method:  f.Method,
		URL:     req.URL,
		Headers: a.composeHeaders,
		Body:    a.composeBody,
		At:      time.Now(),
	})
	a.saveHistory()
	a.setMessage("Sent " + f.Method + " " + req.URL)
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		if err := a.srv.Replay(ctx, f); err != nil {
			a.mu.Lock()
			a.composeWaiting = false
			a.composeResult = "Failed: " + err.Error()
			a.mu.Unlock()
			a.setMessage("Compose: " + err.Error())
			return
		}
		got, ok := a.waitComposed(before, req.Method, req.URL)
		if !ok {
			a.mu.Lock()
			a.composeWaiting = false
			a.composeResult = "Sent, but the exchange is not in the table"
			a.mu.Unlock()
			a.invalidate()
			return
		}
		res := &jsResponse{Status: got.status, Body: got.body(true), header: got.respHeader}
		if err := runScript(a.composePost, run, map[string]any{"res": res}); err != nil {
			a.mu.Lock()
			a.composeWaiting = false
			a.composeResult = "Post-response script: " + err.Error()
			a.composeTests = nil
			a.mu.Unlock()
			a.invalidate()
			return
		}
		if !maps.Equal(run.vars, vars) {
			a.applyVars(run.vars)
		}
		summary := fmt.Sprintf("%d · %s · %d ms", got.status, humanBytes(got.bytes), got.millis)
		if len(run.tests) > 0 {
			summary += fmt.Sprintf(" · %d/%d tests", run.passed(), len(run.tests))
		}
		a.mu.Lock()
		a.composeWaiting = false
		a.composeResult = summary
		a.composeTests = run.tests
		a.mu.Unlock()
		a.invalidate()
	}()
}

// sendWebSocket sends the editor's body as one WebSocket message, through the
// proxy so the frames are captured, and reports what came back.
func (a *app) sendWebSocket(req *jsRequest) {
	message := req.Body
	a.mu.Lock()
	a.composeWaiting = true
	a.composeResult = "Connecting…"
	a.composeTests = nil
	a.composeReplies = nil
	a.composeSentMethod, a.composeSentURL = "WS", req.URL
	a.mu.Unlock()
	a.setMessage("WebSocket " + req.URL)
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		replies, err := wsSend(ctx, a.srv.Addr(), req.URL, message, 2*time.Second)
		a.mu.Lock()
		a.composeWaiting = false
		if err != nil {
			a.composeResult = "WebSocket failed: " + err.Error()
		} else {
			a.composeResult = fmt.Sprintf("WebSocket: sent %s, %d %s",
				humanBytes(len(message)), len(replies), plural(len(replies), "reply", "replies"))
			if len(replies) == 0 {
				a.composeResult += " (nothing came back in 2 s)"
			}
			a.composeReplies = replies
		}
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

// waitComposed is the flow a send produced, once the table has it.
func (a *app) waitComposed(before int, method, url string) (flow, bool) {
	deadline := time.Now().Add(3 * time.Second)
	for {
		a.mu.Lock()
		for i := len(a.flows) - 1; i >= 0; i-- {
			f := a.flows[i]
			if f.id <= before {
				break
			}
			if f.method == method && f.url == url {
				a.mu.Unlock()
				return f, true
			}
		}
		a.mu.Unlock()
		if time.Now().After(deadline) {
			return flow{}, false
		}
		time.Sleep(20 * time.Millisecond)
	}
}

// filtersActive says whether a content filter narrows the table.
func (a *app) filtersActive() bool {
	return a.filterStatus != "" || a.filterHeader != "" || a.filterBody != ""
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
	if a.filterBody != "" {
		parts = append(parts, "body "+a.filterBody)
	}
	return strings.Join(parts, " · ")
}

// openFilter shows the content filter with what it already holds.
func (a *app) openFilter() {
	a.filterOpen = true
}

// clearFilters drops the content filter.
func (a *app) clearFilters() {
	a.filterStatus, a.filterHeader, a.filterBody = "", "", ""
	a.applyFilter()
	a.setMessage("Filters cleared")
}

// filterModal narrows the table by status, header and body, beside the search
// box which matches the URL.
func (a *app) filterModal(c *ui.Context, t *ui.Theme) {
	if !a.filterOpen {
		return
	}
	ui.Modal(c, &a.filterOpen, func() {
		ui.Column(c).Width(480).Gap(10).Children(func() {
			ui.Text(c, "Filter").FontSize(14).Bold()
			ui.Text(c, "Status takes 404, 4xx, 200-299 or >=400. A header is a name, or \"name: value\" to match the value too. The body matches either side.").
				FontSize(11).TextColor(t.TextMuted)
			ui.TextInput(c, &a.filterStatus).Label("Status")
			ui.TextInput(c, &a.filterHeader).Placeholder("content-type: json").Label("Header")
			ui.TextInput(c, &a.filterBody).Placeholder("\"error\":").Label("Body")
			ui.Row(c).Gap(8).Justify(ui.End).Children(func() {
				if ui.Button(c, "Clear").Clicked() {
					a.filterStatus, a.filterHeader, a.filterBody = "", "", ""
					a.applyFilter()
					a.filterOpen = false
				}
				if ui.Button(c, "Cancel").Clicked() {
					a.filterOpen = false
				}
				if ui.PrimaryButton(c, "Apply").Clicked() {
					a.applyFilter()
					a.filterOpen = false
				}
			})
		})
	})
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

// statusMatches reports whether a status code answers a filter such as "404",
// "4xx", ">=400" or "200-299".
func statusMatches(expr string, status int) bool {
	expr = strings.TrimSpace(strings.ToLower(expr))
	switch {
	case expr == "":
		return true
	case strings.HasPrefix(expr, ">="):
		n, err := strconv.Atoi(strings.TrimSpace(expr[2:]))
		return err == nil && status >= n
	case strings.HasPrefix(expr, "<="):
		n, err := strconv.Atoi(strings.TrimSpace(expr[2:]))
		return err == nil && status <= n
	case strings.HasPrefix(expr, ">"):
		n, err := strconv.Atoi(strings.TrimSpace(expr[1:]))
		return err == nil && status > n
	case strings.HasPrefix(expr, "<"):
		n, err := strconv.Atoi(strings.TrimSpace(expr[1:]))
		return err == nil && status < n
	case strings.HasSuffix(expr, "xx"):
		n, err := strconv.Atoi(strings.TrimSuffix(expr, "xx"))
		return err == nil && status/100 == n
	case strings.Contains(expr, "-"):
		low, high, ok := strings.Cut(expr, "-")
		if !ok {
			return false
		}
		lo, errLow := strconv.Atoi(strings.TrimSpace(low))
		hi, errHigh := strconv.Atoi(strings.TrimSpace(high))
		return errLow == nil && errHigh == nil && status >= lo && status <= hi
	default:
		n, err := strconv.Atoi(expr)
		return err == nil && status == n
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

// sortVisible orders the shown rows by the chosen column, keeping the chosen
// flow chosen.
func (a *app) sortVisible() {
	if len(a.visible) < 2 {
		return
	}
	chosen := -1
	if a.picked >= 0 && a.picked < len(a.visible) {
		chosen = a.flows[a.visible[a.picked]].id
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
	if chosen < 0 {
		return
	}
	for i, at := range a.visible {
		if a.flows[at].id == chosen {
			a.picked = i
			return
		}
	}
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
func (a *app) saveComposed() {
	name := strings.TrimSpace(a.composeName)
	if name == "" {
		name = strings.TrimSpace(a.composeMethod + " " + a.composeURL)
	}
	if name == "" {
		a.setMessage("Save: the request has no name")
		return
	}
	r := savedRequest{
		Name:       name,
		Collection: strings.TrimSpace(a.composeCollection),
		Method:     strings.ToUpper(strings.TrimSpace(cmp.Or(a.composeMethod, "GET"))),
		URL:        strings.TrimSpace(a.composeURL),
		Headers:    a.composeHeaders,
		Body:       a.composeBody,
		Pre:        a.composePre,
		Post:       a.composePost,
	}
	a.requests = upsertRequest(a.requests, r)
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
			return
		}
		if err := saveRequests(a.requestsPath, a.requests); err != nil {
			a.setMessage("Save: " + err.Error())
			return
		}
	}
	a.composeName = name
	a.setMessage("Saved " + name)
}

// openSaved fills the request editor from a saved request.
func (a *app) openSaved(collection, name string) {
	for _, r := range a.requests {
		if r.Name != name || r.Collection != collection {
			continue
		}
		a.composeName = r.Name
		a.composeCollection = r.Collection
		a.composeMethod = cmp.Or(r.Method, "GET")
		a.composeURL = r.URL
		a.composeHeaders = r.Headers
		a.composeBody = r.Body
		a.composePre = r.Pre
		a.composePost = r.Post
		a.composeTab = 0
		a.composeTests = nil
		a.composeSentMethod, a.composeSentURL = "", ""
		a.composeOpen = true
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
			ui.Text(c, "HTTPS is tunnelled untouched until a domain is listed. Its CONNECT shows in the table, and Decrypt this domain adds it; a domain matches its subdomains.").
				FontSize(11).TextColor(t.TextMuted)
			ui.Row(c).Gap(8).AlignItems(ui.Center).Children(func() {
				ui.Switch(c, &a.sslAll)
				ui.Text(c, "Decrypt every domain").FontSize(12).TextColor(t.Text)
			})
			ui.TextArea(c, &a.sslHosts).Height(120).Label("Domains")
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
func (a *app) saveSSL(cfg capture.SSLConfig) {
	if a.srv != nil {
		a.srv.SetSSL(cfg)
	}
	a.sslAll, a.sslHosts = cfg.All, strings.Join(cfg.Hosts, "\n")
	if a.sslPath != "" {
		if err := os.MkdirAll(filepath.Dir(a.sslPath), 0o755); err != nil {
			a.setMessage("SSL proxying: " + err.Error())
			return
		}
		data, err := json.Marshal(cfg)
		if err != nil {
			a.setMessage("SSL proxying: " + err.Error())
			return
		}
		if err := os.WriteFile(a.sslPath, append(data, '\n'), 0o644); err != nil {
			a.setMessage("SSL proxying: " + err.Error())
			return
		}
	}
	if cfg.All {
		a.setMessage("SSL proxying: every domain")
	} else {
		a.setMessage(fmt.Sprintf("SSL proxying: %d domains", len(cfg.Hosts)))
	}
}

// decryptHost adds a domain to what the proxy decrypts, the button under a
// tunnel.
func (a *app) decryptHost(host string) {
	cfg := capture.SSLConfig{All: a.sslAll, Hosts: splitHosts(a.sslHosts)}
	if host != "" && !slices.Contains(cfg.Hosts, host) {
		cfg.Hosts = append(cfg.Hosts, host)
	}
	a.saveSSL(cfg)
	a.setMessage("Decrypting " + host + "; reload the page")
}

// connectPanel explains a tunnel and offers to decrypt it, as a proxy that
// leaves HTTPS alone must.
func (a *app) connectPanel(c *ui.Context, f flow) {
	t := c.Theme()
	host := f.host
	ui.Column(c).Fill().Center().Gap(10).Children(func() {
		ui.Text(c, "HTTPS is not decrypted").FontSize(15).TextColor(t.Text)
		ui.Text(c, host+" goes through untouched, so its content stays encrypted.").
			FontSize(12).TextColor(t.TextMuted)
		if ui.PrimaryButton(c, "Decrypt this domain").Clicked() {
			a.decryptHost(host)
		}
		if ui.Button(c, "Decrypt every domain").Clicked() {
			a.saveSSL(capture.SSLConfig{All: true})
		}
	})
}

// persistSettings writes what changed since the last write, at most every
// 400ms, so dragging a divider does not write a file a frame.
func (a *app) persistSettings() {
	now := settings{
		Dark:         a.dark,
		SidebarWidth: a.sidebarWidth,
		TablePercent: a.tablePercent,
		Columns:      savedLayout(a.columns),
		HeaderColumn: a.headerColumn,
	}
	if sameSettings(now, a.saved) || time.Since(a.lastSave) < 400*time.Millisecond {
		return
	}
	a.saved = now
	a.lastSave = time.Now()
	if a.settingsPath == "" {
		return
	}
	path := a.settingsPath
	go func() {
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			a.setMessage("Settings: " + err.Error())
			return
		}
		if err := writeSettings(path, now); err != nil {
			a.setMessage("Settings: " + err.Error())
		}
	}()
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
	a.composeTitle = "Compose"
	a.composeResult = ""
	a.composeName = ""
	a.composeCollection = ""
	a.composeMethod = cmp.Or(e.Method, "GET")
	a.composeURL = e.URL
	a.composeHeaders = e.Headers
	a.composeBody = e.Body
	a.composePre = ""
	a.composePost = ""
	a.composeTab = 0
	a.composeTests = nil
	a.composeReplies = nil
	a.composeSentMethod, a.composeSentURL = "", ""
	a.composeOpen = true
}

// saveHistory keeps the sent requests for the next launch.
func (a *app) saveHistory() {
	if a.historyPath == "" {
		return
	}
	list := a.history
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

// noteComposed reports the flow a send from the editor produced, so the editor
// says what came back before the next change.
func (a *app) noteComposed() {
	a.mu.Lock()
	waiting, before := a.composeWaiting, a.composeBefore
	a.mu.Unlock()
	if !waiting {
		return
	}
	url := a.composeSentURL
	method := a.composeSentMethod
	if url == "" {
		url = strings.TrimSpace(a.composeURL)
		method = strings.ToUpper(strings.TrimSpace(cmp.Or(a.composeMethod, "GET")))
	}
	for i := len(a.flows) - 1; i >= 0; i-- {
		f := a.flows[i]
		if f.id <= before {
			break
		}
		if f.method != method || f.url != url {
			continue
		}
		a.mu.Lock()
		a.composeWaiting = false
		a.composeResult = fmt.Sprintf("%d · %s · %d ms", f.status, humanBytes(f.bytes), f.millis)
		a.mu.Unlock()
		return
	}
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
