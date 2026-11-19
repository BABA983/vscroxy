package app

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/baba983/vscroxy/internal/capture"
	"github.com/baba983/vscroxy/internal/request"
	"github.com/baba983/vscroxy/locales"
	"github.com/egoist/mygo/ui"
)

func TestCompareHeadersIgnoreCaseAndOrder(t *testing.T) {
	left := compareHeaders("Z-Trace: one\nAccept: application/json")
	right := compareHeaders("accept: application/json\nz-trace: one")
	if left.normalized != right.normalized {
		t.Fatalf("normalized headers differ: %q / %q", left.normalized, right.normalized)
	}
	if left.normalized != "Accept: application/json\nZ-Trace: one" {
		t.Fatalf("normalized headers = %q", left.normalized)
	}
	if left.raw == right.raw {
		t.Fatal("raw headers must keep their recorded order for the toggle")
	}
	repeated := compareHeaders("Cookie: a=1\nX-Tag: b\nCOOKIE: a=2")
	if got, want := repeated.normalized, "Cookie: a=1\nCookie: a=2\nX-Tag: b"; got != want {
		t.Fatalf("repeated headers = %q, want %q", got, want)
	}
}

func TestCompareJSONSortsObjectKeysButKeepsArrayOrder(t *testing.T) {
	left := prepareCompareBody(compareBodySource{inline: `{"z":1,"items":[3,1,2],"nest":{"b":2,"a":1}}`,
		contentType: "application/json"})
	right := prepareCompareBody(compareBodySource{inline: `{"nest":{"a":1,"b":2},"items":[3,1,2],"z":1}`,
		contentType: "application/json"})
	if left.normalized != right.normalized {
		t.Fatalf("normalized JSON differs:\n%s\n%s", left.normalized, right.normalized)
	}
	if !strings.Contains(left.normalized, "\"items\": [\n    3,\n    1,\n    2\n  ]") {
		t.Fatalf("array order was changed: %s", left.normalized)
	}
	if left.normalized == left.raw {
		t.Fatal("normalized JSON should be pretty-printed")
	}
	plain := prepareCompareBody(compareBodySource{inline: "not json at all", contentType: "text/plain"})
	if plain.normalized != plain.raw {
		t.Fatalf("plain text was rewritten: %q -> %q", plain.raw, plain.normalized)
	}
	// IDs beyond float64 precision must stay literal: normalizing must not
	// hide a real one-digit difference between two responses.
	big1 := prepareCompareBody(compareBodySource{inline: `{"id":12345678901234567891}`, contentType: "application/json"})
	big2 := prepareCompareBody(compareBodySource{inline: `{"id":12345678901234567892}`, contentType: "application/json"})
	if big1.normalized == big2.normalized || !strings.Contains(big1.normalized, "12345678901234567891") ||
		!strings.Contains(big2.normalized, "12345678901234567892") {
		t.Fatalf("large ids collapsed: %q / %q", big1.normalized, big2.normalized)
	}
	decimal := prepareCompareBody(compareBodySource{inline: `{"v":1.0}`, contentType: "application/json"})
	if !strings.Contains(decimal.normalized, `"v": 1.0`) {
		t.Fatalf("number spelling was changed: %q", decimal.normalized)
	}
}

func TestCompareBodyReadsTheWholeSpillFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "response.bin")
	if err := os.WriteFile(path, []byte(`{"b":2,"a":1}`), 0o600); err != nil {
		t.Fatal(err)
	}
	part := prepareCompareBody(compareBodySource{inline: `{"partial":true}`, file: path,
		size: 13, contentType: "application/json"})
	if !strings.Contains(part.raw, `"b":2`) || strings.Contains(part.raw, "partial") {
		t.Fatalf("body = %q, want the spill file rather than its preview", part.raw)
	}
	if !strings.Contains(part.normalized, `"a": 1`) {
		t.Fatalf("normalized body = %q", part.normalized)
	}
}

func TestCompareBinaryBodyShowsSizeAndHashSummary(t *testing.T) {
	part := prepareCompareBody(compareBodySource{inline: "\x89PNG\r\n\x1a\n", size: 16, contentType: "image/png"})
	if !strings.Contains(part.summary, "Binary body") || !strings.Contains(part.summary, "sha256 ") ||
		!strings.Contains(part.summary, "16 B") {
		t.Fatalf("binary summary = %q", part.summary)
	}
	if part.raw != part.summary || part.normalized != part.summary {
		t.Fatal("a binary body must not produce a text diff")
	}
	large := prepareCompareBody(compareBodySource{inline: strings.Repeat("x", 64), size: maxBodyView + 1,
		contentType: "text/plain"})
	if !strings.Contains(large.summary, "display limit") || !strings.Contains(large.summary, "sha256 ") {
		t.Fatalf("oversized summary = %q", large.summary)
	}
}

func TestCompareRawToggleSwitchesTheDiffBuffers(t *testing.T) {
	a := workspaceApp()
	a.compare = compareState{
		open: true, ready: true, part: comparePartResponseHeaders,
		sides: [2]compareExchange{
			{resHeaders: compareHeaders("Z-Trace: one\nAccept: application/json")},
			{resHeaders: compareHeaders("Accept: text/plain\nZ-Trace: two")},
		},
	}
	tt := ui.NewTester(a.view, 1200, 780)
	tt.Frame()
	if a.compare.diff == nil || !strings.Contains(a.compare.diff.Old.String(), "Accept: application/json\nZ-Trace: one") {
		t.Fatalf("normalized diff = %v", a.compare.diff)
	}
	if !tt.HasText(locales.CompareNormalized()) {
		t.Fatalf("toggle missing: %v", tt.Texts())
	}
	if err := tt.Click(locales.CompareNormalized()); err != nil {
		t.Fatal(err)
	}
	tt.Frame()
	if !a.compare.raw {
		t.Fatal("raw toggle did not take")
	}
	if got := a.compare.diff.Old.String(); got != "Z-Trace: one\nAccept: application/json" {
		t.Fatalf("raw diff = %q", got)
	}
	if !tt.HasText(locales.CompareRaw()) {
		t.Fatalf("raw label missing: %v", tt.Texts())
	}
}

func TestCompareSelectionPersistsAcrossTheTwoCommands(t *testing.T) {
	a := workspaceApp()
	a.srv = startedTestCapture(t)
	a.page = pageLive
	a.applyFilter()
	a.picked = 0
	a.chosen.Add(a.flows[0].id)
	a.syncDocument()
	a.builds++
	var c *ui.Context
	tt := ui.NewTester(func(ctx *ui.Context) { c = ctx; a.view(ctx) }, 1200, 780)
	tt.Frame()

	selectAction := pick(a.actions(c), "compare.select")
	if !selectAction.enabled {
		t.Fatal("Select for Compare is unavailable for the focused capture")
	}
	selectAction.run()
	if !a.compare.selected.same(flowTarget(a.flows[0])) {
		t.Fatalf("held side = %+v", a.compare.selected)
	}
	a.picked, a.pickedID = 1, a.flows[1].id
	a.syncDocument()
	a.builds++
	withAction := pick(a.actions(c), "compare.with-selected")
	if !withAction.enabled {
		t.Fatal("Compare with Selected is unavailable for a second capture")
	}
	withAction.run()
	if !a.compare.open || a.compare.generation != 1 {
		t.Fatalf("comparison did not start: open=%v generation=%d", a.compare.open, a.compare.generation)
	}
	if !a.compare.sources[0].target.same(flowTarget(a.flows[0])) ||
		!a.compare.sources[1].target.same(flowTarget(a.flows[1])) {
		t.Fatalf("pair = %+v / %+v", a.compare.sources[0].target, a.compare.sources[1].target)
	}
	if !a.compare.selected.same(flowTarget(a.flows[0])) {
		t.Fatal("Compare with Selected must keep the held side for the next comparison")
	}
}

func TestCompareSelectionClearedWhenCaptureIsDeleted(t *testing.T) {
	a := workspaceApp()
	target := flowTarget(a.flows[0])
	a.selectForCompare(target)
	if !a.compare.selected.same(target) {
		t.Fatal("setup: selection not held")
	}
	a.deleteFlows([]flow{a.flows[0]})
	if a.compare.selected != (compareTarget{}) {
		t.Fatalf("selection survived deletion: %+v", a.compare.selected)
	}
}

func TestCompareSelectionClearedByClearCaptured(t *testing.T) {
	for _, tc := range []struct {
		name        string
		selectIndex int
		wantCleared bool
	}{
		{"pinned row stays selected", 1, false},
		{"unpinned row loses selection", 0, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			a := workspaceApp()
			a.flows[1].pinned = true
			target := flowTarget(a.flows[tc.selectIndex])
			a.selectForCompare(target)
			a.clearCaptured()
			if len(a.flows) != 1 || a.flows[0].id != 2 {
				t.Fatalf("clear kept %+v, want only the pinned row", a.flows)
			}
			if tc.wantCleared {
				if a.compare.selected != (compareTarget{}) {
					t.Fatalf("unpinned selection survived: %+v", a.compare.selected)
				}
			} else if !a.compare.selected.same(target) {
				t.Fatalf("pinned selection was lost: %+v", a.compare.selected)
			}
		})
	}
}

func TestRunHistoryCompareBuildsBothSides(t *testing.T) {
	a := workspaceApp()
	exchange := &capture.Flow{
		Method: "POST", URL: "https://api.example.com/login", Status: 200, Millis: 21,
		RequestHeader:  request.ParseHeaderBlock("Content-Type: application/json"),
		ResponseHeader: request.ParseHeaderBlock("X-Tag: b\nContent-Type: application/json"),
	}
	first := experimentRecord{ID: 10, DraftKey: "draft-key", Draft: savedRequest{Method: "POST", URL: exchange.URL},
		Exchange: exchange, ResponseBody: []byte(`{"b":2,"a":1}`)}
	second := first
	second.ID, second.ResponseBody, second.Exchange = 11, []byte(`{"a":1,"b":3}`), &capture.Flow{
		Method: "POST", URL: exchange.URL, Status: 500, Millis: 44,
		RequestHeader:  request.ParseHeaderBlock("Content-Type: application/json"),
		ResponseHeader: request.ParseHeaderBlock("Content-Type: application/json\nX-Tag: b"),
	}
	a.experiments.log.Records = []experimentRecord{second, first}

	firstActions := a.runCompareActions(runTarget(first))
	if len(firstActions) == 0 || !firstActions[0].enabled {
		t.Fatal("run history has no Select for Compare action")
	}
	firstActions[0].run()
	secondActions := a.runCompareActions(runTarget(second))
	if !secondActions[1].enabled {
		t.Fatal("Compare with Selected should be available for the other run")
	}
	secondActions[1].run()
	if !a.compare.open || a.compare.generation != 1 {
		t.Fatalf("run comparison did not start: open=%v generation=%d", a.compare.open, a.compare.generation)
	}
	if a.compare.sources[0].label != locales.RunNumberLabel(10) ||
		a.compare.sources[1].label != locales.RunNumberLabel(11) {
		t.Fatalf("labels = %q / %q", a.compare.sources[0].label, a.compare.sources[1].label)
	}
	left, right := prepareCompareExchange(a.compare.sources[0]), prepareCompareExchange(a.compare.sources[1])
	if left.status != 200 || right.status != 500 || left.method != "POST" || right.method != "POST" {
		t.Fatalf("statuses/methods = %d/%d %q/%q", left.status, right.status, left.method, right.method)
	}
	if left.resHeaders.normalized != right.resHeaders.normalized {
		t.Fatalf("header order leaked into the diff: %q / %q", left.resHeaders.normalized, right.resHeaders.normalized)
	}
	if left.resBody.normalized == right.resBody.normalized {
		t.Fatal("two different JSON responses must differ after normalization")
	}
}
