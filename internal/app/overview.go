package app

import (
	"cmp"
	"fmt"
	"slices"
	"strings"
	"time"

	"github.com/baba983/vscroxy/internal/store"
	"github.com/baba983/vscroxy/internal/uikit"
	"github.com/baba983/vscroxy/locales"
	"github.com/egoist/mygo"
	"github.com/egoist/mygo/ui"
)

// overviewScope chooses what the overview summarizes.
type overviewScope int

const (
	overviewWholeSession overviewScope = iota
	overviewChosen
)

// overviewCount is one app, domain or status row.
type overviewCount struct {
	name  string
	count int
	bytes int64
}

// overviewStats is everything the dashboard and its report show. Status
// classes 1xx to 5xx take indexes 0 to 4; an exchange without a response is
// counted last, so a failed request is never hidden in a status class.
type overviewStats struct {
	captures   int
	reqBytes   int64
	resBytes   int64
	apps       []overviewCount
	domains    []overviewCount
	statuses   [6]int
	errors     int
	incomplete int
}

func buildOverview(flows []flow) overviewStats {
	apps := map[string]*overviewCount{}
	domains := map[string]*overviewCount{}
	var s overviewStats
	for _, f := range flows {
		s.captures++
		requestBytes := f.reqWire
		if requestBytes == 0 {
			requestBytes = len(f.reqBody)
		}
		s.reqBytes += int64(requestBytes)
		s.resBytes += int64(f.bytes)
		app := apps[f.process]
		if app == nil {
			app = &overviewCount{name: f.process}
			apps[f.process] = app
		}
		app.count++
		domain := domains[f.host]
		if domain == nil {
			domain = &overviewCount{name: f.host}
			domains[f.host] = domain
		}
		domain.count++
		domain.bytes += int64(f.bytes)
		switch {
		case f.status >= 100 && f.status < 600:
			s.statuses[f.status/100-1]++
		default:
			s.statuses[5]++
		}
		if f.status >= 400 || f.conn.error != "" || f.reqError != "" || f.resError != "" {
			s.errors++
		}
		if f.reqError != "" || f.resError != "" {
			s.incomplete++
		}
	}
	s.apps = overviewRows(apps, false)
	s.domains = overviewRows(domains, true)
	return s
}

func overviewRows(rows map[string]*overviewCount, byBytes bool) []overviewCount {
	out := make([]overviewCount, 0, len(rows))
	for _, row := range rows {
		out = append(out, *row)
	}
	slices.SortStableFunc(out, func(a, b overviewCount) int {
		if byBytes && a.bytes != b.bytes {
			return cmp.Compare(b.bytes, a.bytes)
		}
		if a.count != b.count {
			return b.count - a.count
		}
		if a.name == "" {
			return 1
		}
		if b.name == "" {
			return -1
		}
		return strings.Compare(a.name, b.name)
	})
	return out
}

// overviewFlows is the session, or the chosen rows when the reader asked for
// them and any are still retained.
func (a *app) overviewFlows() []flow {
	if a.overviewScope == overviewChosen {
		return a.selectedFlows()
	}
	return a.flows
}

// overviewCache is the last built dashboard. A repaint reuses it until the
// scope, the flow list, the selection size or the newest flow changes; while
// capture is running it is rebuilt at most every 250 ms, since a response can
// complete on any older row without changing the list's length.
type overviewCache struct {
	valid  bool
	key    string
	at     time.Time
	builds int
	stats  overviewStats
}

func (a *app) overviewKey() string {
	key := fmt.Sprintf("%d:%d", a.overviewScope, len(a.flows))
	if a.overviewScope == overviewChosen {
		// An order-independent sum of the chosen IDs catches a swap that
		// keeps the selection's size, without sorting it every frame.
		var selection uint64
		for id := range a.chosen.All() {
			selection += uint64(id) * 0x9E3779B97F4A7C15
		}
		key += fmt.Sprintf(":%d", selection)
	}
	if n := len(a.flows); n > 0 {
		last := a.flows[n-1]
		key += fmt.Sprintf(":%d:%d:%d", last.id, last.bytes, last.status)
	}
	return key
}

func (a *app) cachedOverview() overviewStats {
	c := &a.overviewCache
	key, now := a.overviewKey(), time.Now()
	capturing := a.srv != nil && a.srv.Running()
	if c.valid && c.key == key && (!capturing || now.Sub(c.at) < 250*time.Millisecond) {
		return c.stats
	}
	c.stats, c.key, c.at, c.valid = buildOverview(a.overviewFlows()), key, now, true
	c.builds++
	return c.stats
}

func (a *app) openOverview() {
	a.openPageTab(pageOverview, tabOpen{})
	a.sidebarChoice = "overview"
}

func (a *app) overviewView(c *ui.Context, t *ui.Theme) {
	flows := a.overviewFlows()
	stats := a.cachedOverview()
	p := uikit.Colors(t)
	ui.Column(c).Fill().Children(func() {
		ui.Row(c).Padding(t.Space(uikit.SpaceXS), t.Space(uikit.SpaceL)).Gap(t.Space(uikit.SpaceM)).MinHeight(uikit.HeightControl).AlignItems(ui.Center).Wrap().Children(func() {
			uikit.Heading(c, locales.Overview()).NoWrap()
			uikit.Segmented(c, (*int)(&a.overviewScope), locales.Session(), locales.Selection())
			ui.Spacer(c)
			disabled := len(flows) == 0
			if uikit.SecondaryButton(c, locales.CopyReport()).Disabled(disabled).Clicked() {
				c.Services().WriteClipboard(a.overviewReport(stats))
				a.done(locales.CopiedReport())
			}
			if uikit.SecondaryButton(c, locales.ExportMarkdownReport()).Disabled(disabled).Clicked() {
				a.exportOverviewReport(stats)
			}
		})
		ui.Divider(c)
		if len(flows) == 0 {
			uikit.Hint(c, locales.OverviewEmpty()).Padding(t.Space(uikit.SpaceXL))
			return
		}
		ui.Scroll(c).Grow(1).Padding(t.Space(uikit.SpaceL)).Gap(t.Space(uikit.SpaceXL)).Children(func() {
			uikit.Meta(c, a.overviewScopeLine(len(flows))).FontFeatures("tnum")
			ui.Row(c).Gap(t.Space(uikit.SpaceL)).Wrap().Children(func() {
				overviewStat(c, t, locales.Captures(), fmt.Sprint(stats.captures))
				overviewStat(c, t, locales.ResponseBytesLabel(), fmt.Sprint(stats.resBytes))
				overviewStat(c, t, locales.Errors(), fmt.Sprint(stats.errors))
				overviewStat(c, t, locales.IncompleteBodiesLabel(), fmt.Sprint(stats.incomplete))
			})
			overviewSection(c, t, locales.TopApps())
			overviewBars(c, t, stats.apps, 8, false, false, p.Primary)
			overviewSection(c, t, locales.TopDomains())
			overviewBars(c, t, stats.domains, 8, true, true, p.Info)
			overviewSection(c, t, locales.StatusCodes())
			overviewStatuses(c, t, stats)
			overviewSection(c, t, locales.Bandwidth())
			overviewBandwidth(c, t, stats)
		})
	})
}

func overviewStat(c *ui.Context, t *ui.Theme, label, value string) {
	ui.Column(c).Padding(t.Space(uikit.SpaceM), t.Space(uikit.SpaceL)).Gap(t.Space(uikit.SpaceHair)).MinWidth(uikit.WidthPicker).Radius(uikit.RadiusControl).Background(uikit.Colors(t).Window).Children(func() {
		uikit.Heading(c, value).FontFeatures("tnum")
		uikit.Meta(c, label).FontFeatures("tnum")
	})
}

func overviewSection(c *ui.Context, t *ui.Theme, title string) {
	uikit.BodyStrong(c, title).Margin(t.Space(uikit.SpaceXS), 0, 0, 0)
}

// overviewBars lists the busiest rows with a share bar; zero counts draw
// nothing, so a quiet session does not show empty tracks.
func overviewBars(c *ui.Context, t *ui.Theme, rows []overviewCount, limit int, withBytes, byBytes bool, color ui.Color) {
	if len(rows) == 0 {
		uikit.Hint(c, locales.None())
		return
	}
	var total int64
	for _, row := range rows {
		if byBytes {
			total += row.bytes
			continue
		}
		total += int64(row.count)
	}
	ui.Column(c).Gap(t.Space(uikit.SpaceS)).Children(func() {
		for _, row := range rows[:min(limit, len(rows))] {
			name := row.name
			if name == "" {
				name = locales.UnknownApp()
			}
			ui.Column(c).Gap(t.Space(uikit.SpaceHair)).Children(func() {
				ui.Row(c).Gap(t.Space(uikit.SpaceM)).AlignItems(ui.Center).Children(func() {
					uikit.Body(c, name).SingleLine().Grow(1)
					uikit.Meta(c, fmt.Sprint(row.count)).FontFeatures("tnum")
					if withBytes {
						uikit.Meta(c, locales.ResponseBytes(int(row.bytes))).FontFeatures("tnum").Width(150)
					}
					share := float64(row.count)
					if byBytes {
						share = float64(row.bytes)
					}
					uikit.Meta(c, fmt.Sprintf("%.0f%%", 100*share/float64(total))).FontFeatures("tnum").Width(45)
				})
				if total > 0 {
					share := float32(row.count)
					if byBytes {
						share = float32(row.bytes)
					}
					if share > 0 {
						ui.Row(c).Height(4).Radius(2).Background(uikit.Colors(t).Muted).Children(func() {
							ui.Box(c).Grow(share).Height(4).Radius(2).Background(color)
							ui.Box(c).Grow(float32(total) - share).Height(4)
						})
					}
				}
			})
		}
	})
}

func overviewStatuses(c *ui.Context, t *ui.Theme, stats overviewStats) {
	p := uikit.Colors(t)
	total := 0
	for _, count := range stats.statuses {
		total += count
	}
	ui.Column(c).Gap(t.Space(uikit.SpaceS)).Children(func() {
		for i, count := range stats.statuses {
			if count == 0 {
				continue
			}
			label, color := fmt.Sprintf("%dxx", i+1), p.MutedForeground
			switch i {
			case 1:
				color = p.Primary
			case 2:
				color = p.Info
			case 3:
				color = p.Warning
			case 4:
				color = p.Danger
			case 5:
				label, color = locales.NoResponse(), p.Danger
			}
			ui.Column(c).Gap(t.Space(uikit.SpaceHair)).Children(func() {
				ui.Row(c).Gap(t.Space(uikit.SpaceM)).AlignItems(ui.Center).Children(func() {
					uikit.Mono(c, label).Width(90)
					uikit.Meta(c, fmt.Sprint(count)).FontFeatures("tnum").Grow(1)
					uikit.Meta(c, fmt.Sprintf("%.0f%%", 100*float64(count)/float64(total))).FontFeatures("tnum").Width(45)
				})
				ui.Row(c).Height(4).Radius(2).Background(p.Muted).Children(func() {
					ui.Box(c).Grow(float32(count)).Height(4).Radius(2).Background(color)
					ui.Box(c).Grow(float32(total - count)).Height(4)
				})
			})
		}
	})
}

func overviewBandwidth(c *ui.Context, t *ui.Theme, stats overviewStats) {
	ui.Column(c).Gap(t.Space(uikit.SpaceXS)).Children(func() {
		uikit.Body(c, locales.RequestBytes(int(stats.reqBytes))).FontFeatures("tnum")
		uikit.Body(c, locales.ResponseBytes(int(stats.resBytes))).FontFeatures("tnum")
		uikit.Body(c, locales.TotalBytes(int(stats.reqBytes+stats.resBytes))).FontFeatures("tnum").FontWeight(500)
	})
	ui.Divider(c)
}

func (a *app) overviewScopeLine(captures int) string {
	if a.overviewScope == overviewChosen {
		return locales.OverviewScopeSelection(captures)
	}
	return locales.OverviewScopeSession(captures)
}

// overviewReport is the dashboard as a Markdown document.
func (a *app) overviewReport(stats overviewStats) string {
	var out strings.Builder
	fmt.Fprintf(&out, "# %s\n\n", locales.Overview())
	fmt.Fprintf(&out, "- %s\n", locales.OverviewReportScope(a.overviewScopeLine(stats.captures)))
	fmt.Fprintf(&out, "- %s\n\n", locales.OverviewReportGenerated(time.Now().UTC().Format("2006-01-02 15:04:05 UTC")))
	fmt.Fprintf(&out, "## %s\n\n| %s | %s |\n| --- | ---: |\n", locales.TopApps(), locales.ColumnApp(), locales.ColumnCaptures())
	for _, row := range stats.apps[:min(10, len(stats.apps))] {
		fmt.Fprintf(&out, "| %s | %d |\n", overviewCell(row.name, locales.UnknownApp()), row.count)
	}
	fmt.Fprintf(&out, "\n## %s\n\n| %s | %s | %s |\n| --- | ---: | ---: |\n", locales.TopDomains(), locales.ColumnDomain(), locales.ColumnCaptures(), locales.ColumnBytes())
	for _, row := range stats.domains[:min(10, len(stats.domains))] {
		fmt.Fprintf(&out, "| %s | %d | %d |\n", overviewCell(row.name, "—"), row.count, row.bytes)
	}
	fmt.Fprintf(&out, "\n## %s\n\n| %s | %s |\n| --- | ---: |\n", locales.StatusCodes(), locales.ColumnStatus(), locales.ColumnCaptures())
	for i, count := range stats.statuses {
		if count == 0 {
			continue
		}
		label := fmt.Sprintf("%dxx", i+1)
		if i == 5 {
			label = locales.NoResponse()
		}
		fmt.Fprintf(&out, "| %s | %d |\n", overviewCell(label, ""), count)
	}
	fmt.Fprintf(&out, "\n## %s\n\n| %s | %s |\n| --- | ---: |\n", locales.Bandwidth(), locales.ColumnDirection(), locales.ColumnBytes())
	fmt.Fprintf(&out, "| %s | %d |\n", locales.DirectionRequests(), stats.reqBytes)
	fmt.Fprintf(&out, "| %s | %d |\n", locales.DirectionResponses(), stats.resBytes)
	fmt.Fprintf(&out, "| %s | %d |\n", locales.DirectionTotal(), stats.reqBytes+stats.resBytes)
	return out.String()
}

// markdownCell keeps a table intact when a name contains a pipe or a newline.
func overviewCell(value, fallback string) string {
	if value == "" {
		value = fallback
	}
	value = strings.ReplaceAll(value, "|", "\\|")
	value = strings.ReplaceAll(value, "\n", " ")
	return value
}

func (a *app) exportOverviewReport(stats overviewStats) {
	path, err := mygo.Dialog.Save(mygo.SaveDialogOptions{
		Title:       locales.ExportMarkdownReport(),
		DefaultPath: "vscroxy-overview.md",
		Filters:     []mygo.FileFilter{{Name: "Markdown", Extensions: []string{"md"}}},
	})
	if err != nil || path == "" {
		return
	}
	if err := store.WriteFile(path, []byte(a.overviewReport(stats))); err != nil {
		a.fail(locales.ExportMarkdownReport(), err)
		return
	}
	a.done(locales.ExportedMarkdownReport())
}
