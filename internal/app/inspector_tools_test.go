package app

import (
	"context"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/baba983/vscroxy/internal/capture"
	"github.com/egoist/mygo/ui"
)

func TestInspectorCookiesPreserveScopeAndMaskValues(t *testing.T) {
	a := workspaceApp()
	a.flows[0].respHeader["Set-Cookie"] = []string{"sid=top-secret; Path=/; Secure; HttpOnly; SameSite=None; Partitioned; Expires=Wed, 21 Oct 2037 07:28:00 GMT", "sid=other-secret; Path=/admin; Max-Age=0", "invalid"}
	a.flows[0].reqHeader["Cookie"] = []string{"sid=request-one; sid=request-two"}
	a.inspect.ready, a.inspect.tab[1] = true, resTabCookies
	tt := ui.NewTester(a.view, 1280, 800)
	if tt.HasText("top-secret") || !tt.HasText("2 cookies · captured headers") || !tt.HasText("1 invalid cookie headers; inspect Raw for original bytes") {
		t.Fatal(tt.Texts())
	}
	if err := tt.Click("Reveal cookie values"); err != nil {
		t.Fatal(err)
	}
	if !tt.HasText("sid = top-secret") || !tt.HasText("sid = other-secret") || !tt.HasText("SameSite None") || !tt.HasText("Domain \"\" · Path \"/admin\"") || !tt.HasText("Secure true · HttpOnly true · Partitioned true") {
		t.Fatal("Set-Cookie attributes/duplicates were lost: ", tt.Texts())
	}
	if err := tt.Click("Hide cookie values"); err != nil {
		t.Fatal(err)
	}
	a.inspect.tab[0], a.inspect.tab[1] = reqTabCookies, resTabBody
	tt.Frame()
	if err := tt.Click("Reveal cookie values"); err != nil {
		t.Fatal(err)
	}
	if !tt.HasText("sid = request-one") || !tt.HasText("sid = request-two") || tt.HasText("top-secret") {
		t.Fatal("request cookies borrowed response or jar state: ", tt.Texts())
	}
	a.picked = 1
	tt.Frame()
	if a.inspect.reveal != [2]bool{} || tt.HasText("request-one") {
		t.Fatal("cookie reveal survived changing exchange")
	}
	if len(a.flows[0].respHeader.Values("Set-Cookie")) != 3 {
		t.Fatal("inspection changed captured headers")
	}
}

func TestInspectorDecodeSelectionDoesNotChangeBody(t *testing.T) {
	a := workspaceApp()
	a.flows[0].resBody = "prefix aGVsbG8 suffix"
	a.flows[0].respHeader = http.Header{"Content-Type": {"text/plain"}}
	openResponseBody(a)
	tt := ui.NewTester(a.view, 1280, 800)
	reader := a.bodyText[0]
	start := strings.Index(reader.Buffer.String(), "aGVsbG8")
	reader.Select(start, start+7)
	if err := tt.Click("Response pane options"); err != nil {
		t.Fatal(err)
	}
	if err := chooseMenu(tt, "Decode text…"); err != nil {
		t.Fatal(err)
	}
	if a.decoder.input != "aGVsbG8" {
		t.Fatalf("did not capture selection: %q", a.decoder.input)
	}
	if err := tt.Click("Decode"); err != nil {
		t.Fatal(err)
	}
	if err := tt.Click("Copy result"); err != nil {
		t.Fatal(err)
	}
	if tt.Clipboard() != "hello" || reader.Buffer.String() != "prefix aGVsbG8 suffix" || a.flows[0].resBody != "prefix aGVsbG8 suffix" {
		t.Fatal("decoder changed source or copied a different result")
	}
	if err := tt.Click("Decode input"); err != nil {
		t.Fatal(err)
	}
	tt.Key(ui.Cmd, ui.KeyA)
	tt.Type("not base64!")
	if err := tt.Click("Decode"); err != nil {
		t.Fatal(err)
	}
	if a.decoder.result != nil || !tt.HasText("invalid Base64; use standard or URL-safe encoding") {
		t.Fatal("invalid decode retained stale success")
	}
}

func waitInspection(t *testing.T, tt *ui.Tester, ready func() bool) {
	t.Helper()
	guard, cancel := context.WithTimeout(t.Context(), time.Minute)
	defer cancel()
	for !ready() {
		if guard.Err() != nil {
			t.Fatal("inspection did not settle: ", tt.Texts())
		}
		runtime.Gosched()
		tt.Frame()
	}
	tt.Frame()
}

func TestInspectorJSONQueryReadsCompleteSideAndKeepsSource(t *testing.T) {
	for _, side := range []string{"Request", "Response", "Sent"} {
		t.Run(side, func(t *testing.T) {
			const body = `{"rows":[{"n":3},{"n":19},{"n":7}],"exact":9007199254740993}`
			path := filepath.Join(t.TempDir(), "body.json")
			if err := os.WriteFile(path, []byte(body), 0600); err != nil {
				t.Fatal(err)
			}
			a := workspaceApp()
			a.flows[0].reqBody = `{"wrong":"request preview"}`
			a.flows[0].resBody = `{"wrong":"response preview"}`
			if side == "Request" {
				a.flows[0].reqFile, a.flows[0].reqSize = path, len(body)
			} else {
				a.flows[0].resFile, a.flows[0].resSize = path, len(body)
			}
			if side == "Sent" {
				openDraft(a, a.flows[0])
				a.sentSet = true
				a.response = &capture.Flow{Status: 200, ResponseBody: "{", ResponseBodyFile: path, ResponseBodySize: len(body)}
			}
			t.Cleanup(a.jsonQuery.job.stop)
			tt := ui.NewTester(a.view, 1280, 800)
			menu := side + " pane options"
			if side == "Sent" {
				menu = "Response format"
			}
			if err := tt.Click(menu); err != nil {
				t.Fatal(err)
			}
			if err := chooseMenu(tt, "Query JSON…"); err != nil {
				t.Fatal(err)
			}
			if err := tt.Click("JSON query expression"); err != nil {
				t.Fatal(err)
			}
			tt.Key(ui.Cmd, ui.KeyA)
			tt.Type("$.exact")
			if err := tt.Click("Run query"); err != nil {
				t.Fatal(err)
			}
			waitInspection(t, tt, func() bool { return a.jsonQuery.job.answers == nil })
			if err := tt.Click("Copy results"); err != nil {
				t.Fatal(err)
			}
			if got := tt.Clipboard(); got != "[\n  9007199254740993\n]" {
				t.Fatalf("query read a preview/wrong side or rounded: %q", got)
			}
			if err := tt.Click("Query dialect"); err != nil {
				t.Fatal(err)
			}
			if err := tt.Click("jq"); err != nil {
				t.Fatal(err)
			}
			if a.jsonQuery.job.result != nil {
				t.Fatal("dialect change retained stale results")
			}
			if err := tt.Click("JSON query expression"); err != nil {
				t.Fatal(err)
			}
			tt.Key(ui.Cmd, ui.KeyA)
			tt.Type(".rows[] | select(.n > 7) | .n")
			if err := tt.Click("Run query"); err != nil {
				t.Fatal(err)
			}
			waitInspection(t, tt, func() bool { return a.jsonQuery.job.answers == nil })
			if err := tt.Click("Copy results"); err != nil {
				t.Fatal(err)
			}
			if tt.Clipboard() != "19" || a.flows[0].resBody != `{"wrong":"response preview"}` {
				t.Fatal("filter result or original evidence changed")
			}
		})
	}
}

func TestInspectorJSONQueryErrorsCancellationAndNewSource(t *testing.T) {
	a := workspaceApp()
	a.openJSONQuery(inspectionBody{text: `{"n":1}`, size: 200})
	t.Cleanup(a.jsonQuery.job.stop)
	tt := ui.NewTester(a.view, 960, 600)
	if err := tt.Click("Run query"); err != nil {
		t.Fatal(err)
	}
	waitInspection(t, tt, func() bool { return a.jsonQuery.job.answers == nil })
	if !tt.HasText("body is incomplete") || a.jsonQuery.job.result != nil {
		t.Fatal("queried an incomplete preview: ", tt.Texts())
	}
	a.openJSONQuery(inspectionBody{text: `null`})
	a.jsonQuery.dialect, a.jsonQuery.expression = "jq", "last(repeat(empty))"
	tt.Frame()
	if err := tt.Click("Run query"); err != nil {
		t.Fatal(err)
	}
	if err := tt.Click("Cancel query"); err != nil {
		t.Fatal(err)
	}
	if !tt.HasText("Query cancelled") || a.jsonQuery.job.answers != nil {
		t.Fatal("cancel did not release the reader")
	}
	if err := tt.Click("Run query"); err != nil {
		t.Fatal(err)
	}
	if err := tt.Click("Close"); err != nil {
		t.Fatal(err)
	}
	if a.jsonQuery.open || a.jsonQuery.job.answers != nil {
		t.Fatal("closing query left work attached")
	}
	a.openJSONQuery(inspectionBody{text: `{"new":73}`})
	a.jsonQuery.dialect, a.jsonQuery.expression = "jq", ".new"
	tt.Frame()
	if err := tt.Click("Run query"); err != nil {
		t.Fatal(err)
	}
	waitInspection(t, tt, func() bool { return a.jsonQuery.job.answers == nil })
	if err := tt.Click("Copy results"); err != nil {
		t.Fatal(err)
	}
	if tt.Clipboard() != "73" {
		t.Fatal("old job replaced new source")
	}
}
