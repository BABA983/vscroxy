package app

import (
	"strings"
	"testing"

	"github.com/egoist/mygo/ui"
)

func TestBuildOverviewCountsAppsDomainsStatusesAndBytes(t *testing.T) {
	flows := []flow{
		{id: 1, process: "Browser", host: "a.example.com", status: 200, bytes: 100, reqWire: 10},
		{id: 2, process: "Browser", host: "a.example.com", status: 404, bytes: 50, reqWire: 5},
		{id: 3, process: "curl", host: "b.example.com", status: 0, reqBody: "abcd", conn: conn{error: "refused"}},
	}
	stats := buildOverview(flows)
	if stats.captures != 3 || stats.reqBytes != 19 || stats.resBytes != 150 || stats.errors != 2 {
		t.Fatalf("totals: %+v", stats)
	}
	if len(stats.apps) != 2 || stats.apps[0].name != "Browser" || stats.apps[0].count != 2 || stats.apps[1].name != "curl" {
		t.Fatalf("apps: %+v", stats.apps)
	}
	if len(stats.domains) != 2 || stats.domains[0].name != "a.example.com" || stats.domains[0].bytes != 150 || stats.domains[1].name != "b.example.com" {
		t.Fatalf("domains: %+v", stats.domains)
	}
	if stats.statuses[1] != 1 || stats.statuses[3] != 1 || stats.statuses[5] != 1 || stats.statuses[0] != 0 {
		t.Fatalf("statuses: %v", stats.statuses)
	}
}

func TestOverviewReportIsMarkdownWithEscapedCells(t *testing.T) {
	a := workspaceApp()
	a.flows[0].process = "Odd | App"
	stats := buildOverview(a.flows)
	report := a.overviewReport(stats)
	for _, want := range []string{"# Overview", "## Top apps", "## Top domains", "## Status codes", "## Bandwidth", "Odd \\| App", "| Requests |"} {
		if !strings.Contains(report, want) {
			t.Fatalf("report lacks %q:\n%s", want, report)
		}
	}
}

func TestOverviewPageShowsSessionAndSelection(t *testing.T) {
	a := workspaceApp()
	tt := ui.NewTester(a.view, 1200, 800)
	if err := tt.Click("Overview"); err != nil {
		t.Fatal(err)
	}
	if a.page != pageOverview || a.sidebarChoice != "overview" || !tt.HasText("Top apps") || !tt.HasText("api.example.com") {
		t.Fatalf("overview page: page=%v texts=%q", a.page, tt.Texts())
	}
	a.chosen.Clear()
	a.chosen.Add(2)
	if err := tt.Click("Selection"); err != nil {
		t.Fatal(err)
	}
	if a.overviewScope != overviewChosen || !tt.HasText("Selection · 1 captures") {
		t.Fatalf("selection scope: %v %q", a.overviewScope, tt.Texts())
	}
}

func TestOverviewRepaintReusesCachedStats(t *testing.T) {
	a := workspaceApp()
	a.cachedOverview()
	if a.overviewCache.builds != 1 {
		t.Fatalf("first build: %d", a.overviewCache.builds)
	}
	a.cachedOverview()
	if a.overviewCache.builds != 1 {
		t.Fatalf("unchanged inputs rebuilt: %d", a.overviewCache.builds)
	}
	a.openOverview()
	tt := ui.NewTester(a.view, 1200, 800)
	tt.Frame()
	tt.Frame()
	builds := a.overviewCache.builds
	tt.Frame()
	tt.Frame()
	if a.overviewCache.builds != builds {
		t.Fatalf("a repaint rebuilt the stats: %d -> %d", builds, a.overviewCache.builds)
	}
	a.flows = append(a.flows, flow{id: 99, host: "new.example.com", status: 200})
	a.cachedOverview()
	if a.overviewCache.builds != builds+1 {
		t.Fatalf("a new flow did not rebuild: %d", a.overviewCache.builds)
	}
	a.overviewScope = overviewChosen
	a.cachedOverview()
	if a.overviewCache.builds != builds+2 {
		t.Fatalf("the chosen scope did not rebuild: %d", a.overviewCache.builds)
	}
	a.chosen.Add(7)
	a.cachedOverview()
	if a.overviewCache.builds != builds+3 {
		t.Fatalf("a selection change did not rebuild: %d", a.overviewCache.builds)
	}
}

func BenchmarkOverviewRepaint(b *testing.B) {
	a := workspaceApp()
	for i := range 100000 {
		a.flows = append(a.flows, flow{id: 1000 + i, process: "Browser", host: "api.example.com", status: 200, bytes: 2048})
	}
	a.cachedOverview()
	b.ReportAllocs()
	for b.Loop() {
		a.cachedOverview()
	}
}
