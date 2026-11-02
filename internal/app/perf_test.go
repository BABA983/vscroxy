package app

// Benchmarks for a long capture, the regression measures of the performance
// plan: a table of 100,000 requests, scrolled, filtered, searched, and taking
// new traffic. Run them with
//
//	go test -run '^$' -bench . ./internal/app

import (
	"fmt"
	"testing"
	"time"

	"github.com/baba983/vscroxy/internal/capture"
	"github.com/egoist/mygo/ui"
)

const benchFlows = 100_000

// benchApp is a workspace holding n captured requests across 200 hosts and
// 8 apps, each with a small JSON response, as a day of browsing leaves.
func benchApp(n int) *app {
	a := workspaceApp()
	a.flowLimit = n + 1000 // past the largest choice in Settings, on purpose
	a.memoryLimit = 1 << 20
	start := time.Now().Add(-time.Duration(n) * time.Second)
	a.flows = make([]flow, n)
	for i := range a.flows {
		host := fmt.Sprintf("api%d.example.com", i%200)
		path := fmt.Sprintf("/v1/items/%d", i)
		a.flows[i] = flow{
			id: i + 1, method: []string{"GET", "POST", "PUT", "DELETE"}[i%4], scheme: "https",
			host: host, path: path, url: "https://" + host + path,
			status: []int{200, 200, 201, 304, 404, 500}[i%6], millis: i % 1500, bytes: 400 + i%4000,
			process: fmt.Sprintf("App %d", i%8), at: start.Add(time.Duration(i) * time.Second),
			resBody: fmt.Sprintf(`{"id":%d,"name":"item %d","tags":["a","b"]}`, i, i),
		}
	}
	a.countDomains()
	a.countClients()
	a.applyFilter()
	return a
}

// newFlows are batch requests the capture core publishes in one frame.
func newFlows(from, batch int) []capture.Flow {
	out := make([]capture.Flow, batch)
	for i := range out {
		id := from + i
		out[i] = capture.Flow{ID: id, Method: "GET", Scheme: "https", Host: "live.example.com", Path: fmt.Sprintf("/%d", id), Status: 200, Kind: "http", At: time.Now()}
	}
	return out
}

// BenchmarkFilter100k is the table narrowed by a sidebar choice and a status
// condition, as one click or keystroke does.
func BenchmarkFilter100k(b *testing.B) {
	a := benchApp(benchFlows)
	a.filter, a.filterStatus = "host:api7.example.com", "2xx"
	for b.Loop() {
		a.applyFilter()
	}
	b.ReportMetric(float64(len(a.visible)), "rows")
}

// BenchmarkSearch100k is the search field matching host and path, as each
// typed character does.
func BenchmarkSearch100k(b *testing.B) {
	a := benchApp(benchFlows)
	a.query = "items/99"
	for b.Loop() {
		a.applyFilter()
	}
	b.ReportMetric(float64(len(a.visible)), "rows")
}

// BenchmarkScroll100k is one frame of the table scrolled by a wheel step.
func BenchmarkScroll100k(b *testing.B) {
	a := benchApp(benchFlows)
	tt := ui.NewTester(a.view, 1280, 800)
	tt.Frame()
	step := float32(240)
	for b.Loop() {
		tt.Scroll(400, 400, 0, step)
		step = -step // back and forth, never pinned to an end
		tt.Frame()
	}
}

// BenchmarkArrival100k is one frame in which 50 new requests reach a table
// of 100,000 and the oldest leave it: the frame a live capture repeats.
func BenchmarkArrival100k(b *testing.B) {
	a := benchApp(benchFlows)
	a.flowLimit = benchFlows
	tt := ui.NewTester(a.view, 1280, 800)
	tt.Frame()
	next := benchFlows + 1
	for b.Loop() {
		a.pending = newFlows(next, 50)
		next += 50
		tt.Frame()
	}
	if len(a.flows) != benchFlows {
		b.Fatalf("table holds %d requests, want %d", len(a.flows), benchFlows)
	}
}
