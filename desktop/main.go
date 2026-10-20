// Command desktop is the vscroxy desktop app: a native UI over the capture
// core. This first cut draws the inspector shell with seeded flows, so the
// layout, the table and the diff viewer can be judged before the proxy engine
// is wired in.
package main

import (
	"cmp"
	"fmt"
	"log"
	"slices"
	"strings"

	"github.com/egoist/mygo"
	"github.com/egoist/mygo/ui"
	"github.com/egoist/mygo/ui/editor"
)

// app is the state the window shows; view reads it and the controls write it.
type app struct {
	flows   []flow
	visible []int
	domains []domain

	query  string
	filter string
	picked int
	tab    int

	sidebarWidth float32
	tablePercent float32

	trafficOpen bool
	domainsOpen bool

	// detailFor is the flow the detail panes were built from, so the text and
	// the diff buffers are rebuilt only when the choice changes.
	detailFor int
	headers   string
	body      string
	diff      *editor.DiffState
}

type domain struct {
	name string
	n    int
}

func newApp() *app {
	a := &app{
		flows:        seedFlows(2000),
		filter:       "all",
		picked:       0,
		sidebarWidth: 220,
		tablePercent: 62,
		trafficOpen:  true,
		domainsOpen:  true,
		detailFor:    -1,
	}
	a.countDomains()
	a.applyFilter()
	return a
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
		if q != "" && !strings.Contains(strings.ToLower(f.host+f.path), q) {
			continue
		}
		a.visible = append(a.visible, i)
	}
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

// syncDetail rebuilds the text and the diff when the choice moves.
func (a *app) syncDetail() {
	f, ok := a.current()
	if !ok || a.detailFor == f.id {
		return
	}
	a.detailFor = f.id
	a.headers = requestHeaders(f) + "\n\n" + responseHeaders(f)
	a.body = f.resBody
	a.diff = &editor.DiffState{
		Old:    editor.NewBuffer(previousBody(f)),
		New:    editor.NewBuffer(f.resBody),
		Layout: editor.DiffSplit,
	}
	a.diff.Old.SetLanguage(editor.LanguageFor("response.json"))
	a.diff.New.SetLanguage(editor.LanguageFor("response.json"))
}

func (a *app) view(c *ui.Context) {
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
}

func (a *app) toolbar(c *ui.Context, t *ui.Theme) {
	ui.Row(c).Height(46).Padding(0, 14).Gap(10).AlignItems(ui.Center).Children(func() {
		ui.Box(c).Size(8, 8).Radius(4).Background(ui.Hex("#22c55e"))
		ui.Text(c, "Listening on 127.0.0.1:7890").FontSize(12).TextColor(t.TextMuted)
		ui.Spacer(c)
		if ui.SearchField(c, &a.query).Width(320).Changed() {
			a.applyFilter()
		}
		if ui.Button(c, "Clear").Clicked() {
			a.query = ""
			a.filter = "all"
			a.applyFilter()
		}
	})
	ui.Divider(c)
}

func (a *app) sidebar(c *ui.Context) {
	t := c.Theme()
	ui.Column(c).Fill().Background(t.Surface).Children(func() {
		ui.Sidebar(c, &a.filter, func() {
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
		}).Fill().Changed()
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
	cols := []ui.TableColumn{
		{Title: "Method", Width: 88, Fixed: true},
		{Title: "Status", Width: 76, Fixed: true, Align: ui.End},
		{Title: "Host", Width: 196, Fixed: true},
		{Title: "Path"},
		{Title: "Size", Width: 86, Align: ui.End},
		{Title: "Time", Width: 74, Align: ui.End},
		{Title: "", ID: "filler", Width: 14, Fixed: true},
	}
	state := &ui.ListState{
		Selected: &a.picked,
		Key:      func(row int) any { return a.visible[row] },
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
			color := statusColor(f.status, t)
			if chosen {
				color = t.AccentText
			}
			text(fmt.Sprint(f.status)).TextColor(color)
		case 2:
			text(f.host).TextColor(rowColor(chosen, t, t.Text))
		case 3:
			text(f.path).TextColor(rowColor(chosen, t, t.Text))
		case 4:
			text(humanBytes(f.bytes)).TextColor(rowColor(chosen, t, t.TextMuted))
		case 5:
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
			ui.Text(c, fmt.Sprintf("%s https://%s%s", f.method, f.host, f.path)).
				FontSize(12).TextColor(t.Text).SingleLine().Grow(1)
			ui.Text(c, fmt.Sprintf("%d · %s · %d ms", f.status, humanBytes(f.bytes), f.millis)).
				FontSize(12).TextColor(statusColor(f.status, t))
		})
		ui.Tabs(c, &a.tab, "Headers", "Body", "Diff").Padding(0, 8)
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
			ui.Scroll(c).Grow(1).Padding(12).Children(func() {
				ui.Text(c, a.body).FontSize(12).Font("monospace").TextColor(t.Text)
			})
		case 2:
			if a.diff != nil {
				editor.DiffView(c, a.diff).Grow(1).FillWidth()
			}
		}
	})
}

func (a *app) statusBar(c *ui.Context, t *ui.Theme) {
	ui.Divider(c)
	ui.Row(c).Height(28).Padding(0, 14).Gap(16).AlignItems(ui.Center).Children(func() {
		ui.Text(c, fmt.Sprintf("%d flows · %d shown", len(a.flows), len(a.visible))).
			FontSize(11).TextColor(t.TextMuted)
		ui.Spacer(c)
		ui.Text(c, "HTTP/1.1 · TLS 1.3 · MITM off").FontSize(11).TextColor(t.TextMuted)
	})
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
	}
	return ui.Hex("#71717a")
}

func statusColor(status int, t *ui.Theme) ui.Color {
	switch {
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
		mygo.NewWindow(mygo.WindowOptions{
			Title:     "vscroxy",
			Width:     1280,
			Height:    800,
			MinWidth:  960,
			MinHeight: 600,
			Content:   ui.View(a.view),
		})
	})
	if err := mygo.App.Run(); err != nil {
		log.Fatal(err)
	}
}
