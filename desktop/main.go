// Command desktop is the vscroxy desktop app: a native UI over the capture
// core. The UI reads flows from internal/capture and never imports the proxy
// engine itself.
package main

import (
	"cmp"
	"context"
	"fmt"
	"log"
	"maps"
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
	picked int
	tab    int

	// sort is the column the table is sorted by, and sortApplied what the
	// rows were sorted with, so a header click sorts on the next frame.
	sort        ui.SortOrder
	sortApplied ui.SortOrder

	sidebarWidth float32
	tablePercent float32

	trafficOpen bool
	domainsOpen bool
	clientsOpen bool

	// icons caches the app icon per process name; a nil entry means the
	// process has none.
	icons map[string]ui.ImageSource

	// detailFor is the flow the detail panes were built from, so the text and
	// the diff buffers are rebuilt only when the choice changes.
	detailFor int
	headers   string
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

	// exclusions are the hosts the proxy tunnels without decrypting, edited
	// as text and kept in a file.
	exclusionsPath string
	exclusionsText string
	exclusionsOpen bool

	// compose is the request being written: what Compose sends through the
	// proxy so the exchange is captured like any other.
	composeOpen    bool
	composeMethod  string
	composeURL     string
	composeHeaders string
	composeBody    string

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
		filter:       "all",
		icons:        make(map[string]ui.ImageSource),
		sidebarWidth: 220,
		tablePercent: 62,
		trafficOpen:  true,
		domainsOpen:  true,
		clientsOpen:  true,
		detailFor:    -1,
		frames:       make(map[int][]frame),
		throttle:     capture.Presets[0],
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
			a.exclusionsPath = filepath.Join(home, ".vscroxy", "exclusions.txt")
			a.scriptPath = filepath.Join(home, ".vscroxy", "script.js")
		}
		a.reloadConfig()
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
	if a.exclusionsPath != "" && a.srv != nil {
		patterns, err := capture.LoadExclusions(a.exclusionsPath)
		if err != nil {
			a.errText = err.Error()
		} else {
			a.srv.SetExclusions(patterns)
			a.exclusionsText = strings.Join(patterns, "\n")
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
			id:         f.ID,
			method:     f.Method,
			scheme:     f.Scheme,
			host:       f.Host,
			path:       f.Path,
			url:        f.URL,
			status:     f.Status,
			bytes:      f.Bytes,
			millis:     f.Millis,
			reqHeaders: f.RequestHeaders,
			resHeaders: f.ResponseHeaders,
			reqBody:    f.RequestBody,
			reqFile:    f.RequestBodyFile,
			reqSize:    f.RequestBodySize,
			resBody:    f.ResponseBody,
			resFile:    f.ResponseBodyFile,
			resSize:    f.ResponseBodySize,
			reqHeader:  f.RequestHeader,
			respHeader: f.ResponseHeader,
			at:         f.At,
			kind:       kind,
			process:    f.Process,
			wait:       f.Wait,
			download:   f.Download,
		})
	}
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
	if f.kind == "" && a.tab == 4 {
		a.tab = 0
	}
	a.headers = f.reqHeaders + "\n\n" + f.resHeaders
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
	a.breakpointModal(c, t)
	a.upstreamModal(c, t)
	a.exclusionsModal(c, t)
	a.composeModal(c, t)
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
	ui.Row(c).Height(46).Padding(0, 14).Gap(10).AlignItems(ui.Center).Children(func() {
		ui.Box(c).Size(8, 8).Radius(4).Background(dot)
		ui.Text(c, label).FontSize(12).TextColor(t.TextMuted).SingleLine()
		ui.Spacer(c)
		if ui.SearchField(c, &a.query).Width(240).Changed() {
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
			if m.Item("SSL exclusions…").Chosen() {
				a.exclusionsOpen = true
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
		})
		if ui.Button(c, "Reload config").Clicked() {
			a.reloadConfig()
		}
		ui.MenuButton(c, "Session", func(m *ui.Menu) {
			if m.Item("Export HAR…").Chosen() {
				a.exportHAR()
			}
			if m.Item("Save session…").Chosen() {
				a.saveSession()
			}
			if m.Item("Open session…").Chosen() {
				a.openSession()
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
			a.flows = nil
			a.visible = nil
			a.frames = make(map[int][]frame)
			a.picked = 0
			a.detailFor = -1
			a.query = ""
			a.filter = "all"
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
			if len(a.clients) > 0 {
				ui.SidebarSection(c, "Clients", &a.clientsOpen, func() {
					for _, d := range a.clients {
						ui.SidebarItem(c, "client:"+d.name, nil, d.name).Children(func() {
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
		{ID: "method", Title: "Method", Width: 84, Fixed: true, Sortable: true},
		{ID: "proto", Title: "Proto", Width: 66, Fixed: true, Sortable: true},
		{ID: "status", Title: "Status", Width: 72, Fixed: true, Align: ui.End, Sortable: true},
		{ID: "host", Title: "Host", Width: 190, Fixed: true, Sortable: true},
		{ID: "client", Title: "Client", Width: 120, Fixed: true, Sortable: true},
		{ID: "path", Title: "Path", Sortable: true},
		{ID: "size", Title: "Size", Width: 86, Align: ui.End, Sortable: true},
		{ID: "time", Title: "Time", Width: 74, Align: ui.End, Sortable: true},
		{Title: "", ID: "filler", Width: 14, Fixed: true},
	}
	state := &ui.ListState{
		Selected: &a.picked,
		Key:      func(row int) any { return a.flows[a.visible[row]].id },
		Sort:     &a.sort,
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
			text(f.method).TextColor(color)
		case 1:
			color := ui.Hex("#71717a")
			if f.scheme == "https" {
				color = ui.Hex("#0891b2")
			}
			if chosen {
				color = t.AccentText
			}
			text(f.scheme).TextColor(color)
		case 2:
			color := statusColor(f.status, t)
			if chosen {
				color = t.AccentText
			}
			text(fmt.Sprint(f.status)).TextColor(color)
		case 3:
			text(f.host).ContextMenu(a.rowMenu(c, f)).TextColor(rowColor(chosen, t, t.Text))
		case 4:
			if src, ok := a.clientIcon(f.process); ok {
				ui.Image(c, src).Size(16, 16)
			}
			text(f.process).Padding(0, 0, 0, 6).ContextMenu(a.rowMenu(c, f)).TextColor(rowColor(chosen, t, t.TextMuted))
		case 5:
			text(f.path).ContextMenu(a.rowMenu(c, f)).TextColor(rowColor(chosen, t, t.Text))
		case 6:
			text(humanBytes(f.bytes)).TextColor(rowColor(chosen, t, t.TextMuted))
		case 7:
			text(fmt.Sprintf("%d ms", f.millis)).TextColor(rowColor(chosen, t, t.TextMuted))
		}
	}).Grow(1)
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
			if ui.Button(c, "Replay").Disabled(f.url == "" || f.kind != "").Clicked() {
				go a.replay(f)
			}
			ui.Text(c, fmt.Sprintf("%d · %s · %d ms", f.status, humanBytes(f.bytes), f.millis)).
				FontSize(12).TextColor(statusColor(f.status, t))
		})
		labels := []string{"Headers", "Body", "Diff", "Timing"}
		if f.kind != "" {
			labels = append(labels, "Messages")
		}
		ui.Tabs(c, &a.tab, labels...).Padding(0, 8)
		if !ok {
			ui.Text(c, "Capture a request to inspect it.").FontSize(12).TextColor(t.TextMuted).Padding(16)
			return
		}
		switch a.tab {
		case 0:
			ui.Scroll(c).Grow(1).Padding(12).Children(func() {
				ui.Text(c, a.headers).FontSize(12).Font("monospace").TextColor(t.Text)
			})
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
		ui.Text(c, fmt.Sprintf("%d flows · %d shown", len(a.flows), len(a.visible))).
			FontSize(11).TextColor(t.TextMuted)
		if a.dropped > 0 {
			ui.Text(c, fmt.Sprintf("%d dropped", a.dropped)).FontSize(11).TextColor(ui.Hex("#d97706"))
		}
		if a.scriptErrs > 0 {
			ui.Text(c, fmt.Sprintf("%d script errors", a.scriptErrs)).FontSize(11).TextColor(ui.Hex("#d97706"))
		}
		ui.Spacer(c)
		if a.throttle.Name != "" && a.throttle.Name != "Off" {
			ui.Text(c, "Network: "+a.throttle.Name).FontSize(11).TextColor(t.TextMuted)
		}
		if a.upstream != "" {
			ui.Text(c, "Upstream: "+a.upstream).FontSize(11).TextColor(t.TextMuted)
		}
		if len(a.rules) > 0 {
			ui.Text(c, fmt.Sprintf("%d rules", len(a.rules))).FontSize(11).TextColor(t.TextMuted)
		}
		a.mu.Lock()
		message := a.message
		a.mu.Unlock()
		if message != "" {
			ui.Text(c, message).FontSize(11).TextColor(t.TextMuted)
		}
		if a.caPath != "" {
			ui.Text(c, "CA "+shortenHome(a.caPath)).FontSize(11).TextColor(t.TextMuted)
		}
		ui.Text(c, "HTTP/1.1 · HTTP/2 · TLS 1.3").FontSize(11).TextColor(t.TextMuted)
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
			Content:   ui.View(a.view),
		})
		go a.startCapture()
	})
	if err := mygo.App.Run(); err != nil {
		log.Fatal(err)
	}
}

// clientIcon returns the app's icon, loading it the first time it is asked
// for. A process with no icon gets none.
func (a *app) clientIcon(process string) (ui.ImageSource, bool) {
	if process == "" {
		return nil, false
	}
	if src, ok := a.icons[process]; ok {
		return src, src != nil
	}
	src := loadIcon(process)
	a.icons[process] = src
	return src, src != nil
}

// loadIcon reads the icon file of a process: an SVG as an SVG, anything else
// as a bitmap.
func loadIcon(process string) ui.ImageSource {
	path := appicon.Lookup(process)
	if path == "" {
		return nil
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return nil
	}
	if strings.HasSuffix(path, ".svg") {
		svg, err := ui.ParseSVG(data)
		if err != nil {
			return nil
		}
		return svg
	}
	bmp, err := ui.DecodeBitmap(data)
	if err != nil {
		return nil
	}
	return bmp
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
		if m.Item("Edit in Compose").Chosen() {
			a.composeFrom(f)
		}
		if m.Item("Replay").Chosen() {
			go a.replay(f)
		}
	}
}

// openCompose shows the request editor with an empty request.
func (a *app) openCompose() {
	a.composeMethod = "GET"
	a.composeURL = "https://"
	a.composeHeaders = ""
	a.composeBody = ""
	a.composeOpen = true
}

// composeFrom fills the request editor with a captured request, the step from
// traffic to a request worth keeping.
func (a *app) composeFrom(f flow) {
	a.composeMethod = cmp.Or(f.method, "GET")
	a.composeURL = f.url
	a.composeHeaders = headerBlock(f.reqHeader)
	a.composeBody = f.body(false)
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
		ui.Column(c).Width(640).Gap(10).Children(func() {
			ui.Text(c, "Compose").FontSize(14).Bold()
			ui.Row(c).Gap(8).Children(func() {
				ui.TextInput(c, &a.composeMethod).Width(90).Label("Method")
				ui.TextInput(c, &a.composeURL).Grow(1).Label("URL")
			})
			ui.Text(c, "Headers").FontSize(11).TextColor(t.TextMuted)
			ui.TextArea(c, &a.composeHeaders).Height(96).Label("Headers")
			ui.Text(c, "Body").FontSize(11).TextColor(t.TextMuted)
			ui.TextArea(c, &a.composeBody).Height(160).Label("Body")
			ui.Row(c).Gap(8).Justify(ui.End).Children(func() {
				if ui.Button(c, "Cancel").Clicked() {
					a.composeOpen = false
				}
				if ui.PrimaryButton(c, "Send").Clicked() {
					a.sendComposed()
				}
			})
		})
	})
}

// sendComposed sends the editor's request through the proxy, so the result
// lands in the table with the rest of the traffic.
func (a *app) sendComposed() {
	if a.srv == nil {
		a.setMessage("Compose: no proxy running")
		return
	}
	url := strings.TrimSpace(a.composeURL)
	if url == "" {
		a.setMessage("Compose: the URL is empty")
		return
	}
	f := capture.Flow{
		Method:        strings.ToUpper(strings.TrimSpace(cmp.Or(a.composeMethod, "GET"))),
		URL:           url,
		RequestHeader: parseHeaderBlock(a.composeHeaders),
		RequestBody:   a.composeBody,
	}
	a.composeOpen = false
	a.setMessage("Sent " + f.Method + " " + url)
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		if err := a.srv.Replay(ctx, f); err != nil {
			a.setMessage("Compose: " + err.Error())
		}
	}()
}

// sortVisible orders the shown rows by the chosen column, keeping the chosen
// flow chosen.
func (a *app) sortVisible() {
	if a.sort.Column == "" || len(a.visible) < 2 {
		return
	}
	chosen := -1
	if a.picked >= 0 && a.picked < len(a.visible) {
		chosen = a.flows[a.visible[a.picked]].id
	}
	less := a.sortOrder()
	slices.SortStableFunc(a.visible, less)
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
		case "host":
			c = cmp.Compare(fx.host, fy.host)
		case "client":
			c = cmp.Compare(fx.process, fy.process)
		case "path":
			c = cmp.Compare(fx.path, fy.path)
		case "size":
			c = cmp.Compare(fx.bytes, fy.bytes)
		case "time":
			c = cmp.Compare(fx.millis, fy.millis)
		}
		if desc {
			return -c
		}
		return c
	}
}

// exclusionsModal edits the hosts the proxy tunnels without decrypting, one
// per line, and keeps them in a file the next start reads.
func (a *app) exclusionsModal(c *ui.Context, t *ui.Theme) {
	if !a.exclusionsOpen {
		return
	}
	ui.Modal(c, &a.exclusionsOpen, func() {
		ui.Column(c).Width(520).Gap(10).Children(func() {
			ui.Text(c, "SSL exclusions").FontSize(14).Bold()
			ui.Text(c, "Hosts the proxy tunnels without decrypting, one per line. A host matches its subdomains; *.example.com matches one level.").
				FontSize(11).TextColor(t.TextMuted)
			ui.TextArea(c, &a.exclusionsText).Height(140).Label("Excluded hosts")
			ui.Row(c).Gap(8).Justify(ui.End).Children(func() {
				if ui.Button(c, "Cancel").Clicked() {
					a.exclusionsOpen = false
				}
				if ui.PrimaryButton(c, "Save").Clicked() {
					a.saveExclusions()
				}
			})
		})
	})
}

// saveExclusions writes the list and hands it to the proxy.
func (a *app) saveExclusions() {
	var patterns []string
	for _, line := range strings.Split(a.exclusionsText, "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		patterns = append(patterns, line)
	}
	if a.exclusionsPath != "" {
		if err := os.MkdirAll(filepath.Dir(a.exclusionsPath), 0o755); err != nil {
			a.setMessage("Exclusions: " + err.Error())
			return
		}
		text := strings.Join(patterns, "\n")
		if text != "" {
			text += "\n"
		}
		if err := os.WriteFile(a.exclusionsPath, []byte(text), 0o644); err != nil {
			a.setMessage("Exclusions: " + err.Error())
			return
		}
	}
	if a.srv != nil {
		a.srv.SetExclusions(patterns)
	}
	a.exclusionsOpen = false
	a.setMessage(fmt.Sprintf("SSL exclusions: %d hosts", len(patterns)))
}
