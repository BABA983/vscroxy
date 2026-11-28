package app

import (
	"net/http"
	"testing"
	"time"

	"github.com/baba983/vscroxy/internal/capture"
	"github.com/baba983/vscroxy/internal/request"
	"github.com/baba983/vscroxy/locales"
	"github.com/egoist/mygo/ui"
)

func consoleFixture(t *testing.T) (*app, *ui.Tester, *ui.Context) {
	t.Helper()
	a := workspaceApp()
	openDraft(a, a.flows[0])
	d := a.requestDocument
	d.sent = savedRequest{Method: "GET", URL: "https://api.example.com/bytes?type=json"}
	d.sentAt = time.Date(2026, 11, 27, 9, 52, 53, 0, time.Local)
	d.sentSet = true
	d.response = &capture.Flow{Status: 200, Millis: 1628, Bytes: 746000,
		RequestBodySize: 1200, ResponseBodySize: 746000,
		RequestHeader:  http.Header{"Accept": {"application/json"}},
		ResponseHeader: http.Header{"Content-Type": {"application/json"}}}
	a.consoleSend(d, requestResult{document: d, outcome: "Response received", response: d.response,
		console: []request.ConsoleMessage{
			{Phase: "pre-request", Kind: "log", Text: `"before"`},
			{Phase: "pre-request", Kind: "warning", Text: "Deprecated parameter type=json"},
			{Phase: "post-response", Kind: "log", Text: `"after"`},
			{Phase: "post-response", Kind: "error", Text: "TypeError: cannot read properties of undefined"},
		}})
	a.bottom.open, a.bottom.tab = true, bottomConsole
	var c *ui.Context
	tt := ui.NewTester(func(ctx *ui.Context) { c = ctx; a.view(ctx) }, 1400, 900)
	tt.Frame()
	return a, tt, c
}

func TestConsoleKeepsScriptLinesAroundTheirRequest(t *testing.T) {
	a, _, _ := consoleFixture(t)
	entries := a.requestConsole.entries
	if len(entries) != 5 {
		t.Fatalf("console entries = %d, want the four script lines and the request: %+v", len(entries), entries)
	}
	want := []struct {
		request bool
		kind    string
		text    string
	}{
		{false, "log", `"before"`},
		{false, "warning", "Deprecated parameter type=json"},
		{true, "", ""},
		{false, "log", `"after"`},
		{false, "error", "TypeError: cannot read properties of undefined"},
	}
	for i, w := range want {
		if entries[i].request != w.request || entries[i].kind != w.kind || entries[i].text != w.text {
			t.Errorf("entry %d = %+v, want request %t %s %q", i, entries[i], w.request, w.kind, w.text)
		}
	}
	req := entries[2]
	if req.method != "GET" || req.status != 200 || req.millis != 1628 {
		t.Errorf("request row = %+v", req)
	}
	if req.requestHeaders == "" || req.responseHeaders == "" || req.requestSize != 1200 || req.responseSize != 746000 {
		t.Errorf("request row details = %+v", req)
	}
	if !entries[0].at.Equal(req.at) || !entries[1].at.Equal(req.at) {
		t.Errorf("pre-request lines at %v and %v, want the send's start %v", entries[0].at, entries[1].at, req.at)
	}
	if want := req.at.Add(1628 * time.Millisecond); !entries[3].at.Equal(want) || !entries[4].at.Equal(want) {
		t.Errorf("post-response lines at %v and %v, want the response's end %v", entries[3].at, entries[4].at, want)
	}
}

func TestConsoleFiltersLevelsAndShowsRequestDetails(t *testing.T) {
	a, tt, _ := consoleFixture(t)
	var id int
	for _, e := range a.requestConsole.entries {
		if e.request {
			id = e.id
		}
	}
	a.requestConsole.expanded = map[int]bool{id: true}
	tt.Frame()
	for _, text := range []string{"Console", "before", "Deprecated parameter type=json", "after", "TypeError: cannot read properties of undefined", "Request headers", "Response headers"} {
		if !tt.HasText(text) {
			t.Errorf("console is missing %q: %v", text, tt.Texts())
		}
	}
	if err := tt.Click("All logs"); err != nil {
		t.Fatal(err)
	}
	if err := chooseMenu(tt, "Warning"); err != nil {
		t.Fatal(err)
	}
	tt.Frame()
	if !tt.HasText("Deprecated parameter type=json") || tt.HasText(`"before"`) || tt.HasText("TypeError: cannot read properties of undefined") {
		t.Fatalf("warning filter kept the wrong rows: %v", tt.Texts())
	}
}

func TestConsoleClearAndBound(t *testing.T) {
	a := workspaceApp()
	openDraft(a, a.flows[0])
	d := a.requestDocument
	d.sent = savedRequest{Method: "GET", URL: "https://api.example.com/"}
	for range consoleEntryLimit + 5 {
		a.consoleSend(d, requestResult{document: d, response: &capture.Flow{Status: 200}})
	}
	if len(a.requestConsole.entries) != consoleEntryLimit {
		t.Fatalf("console kept %d entries, want %d", len(a.requestConsole.entries), consoleEntryLimit)
	}
	if a.requestConsole.entries[0].id <= 5 {
		t.Fatalf("the oldest entries were not dropped: first id %d", a.requestConsole.entries[0].id)
	}
	a.bottom.open, a.bottom.tab = true, bottomConsole
	tt := ui.NewTester(a.view, 1200, 800)
	tt.Frame()
	if err := tt.Click(locales.Clear()); err != nil {
		t.Fatal(err)
	}
	tt.Frame()
	if len(a.requestConsole.entries) != 0 || !tt.HasText(locales.ConsoleEmpty()) {
		t.Fatalf("clear left %d entries: %v", len(a.requestConsole.entries), tt.Texts())
	}
}
