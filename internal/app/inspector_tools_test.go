package app

import (
	"context"
	"net/http"
	"net/url"
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

func TestInspectorStructuredBodiesAutoCopyAndOriginal(t *testing.T) {
	for _, tc := range []struct{ name, contentType, body, want string }{
		{"request form", "application/x-www-form-urlencoded", "tag=one&tag=a+b&plus=%2B&flag", "4 form fields · original order"},
		{"response multipart", "multipart/form-data; boundary=fixture", "--fixture\r\nContent-Disposition: form-data; name=\"tag\"\r\n\r\nhello\r\n--fixture--\r\n", "1 parts · files are not previewed"},
		{"response XML", "application/xml", "<r><n>73</n><n>19</n></r>", "XML · read-only inspection"},
		{"sent MessagePack", "application/msgpack", "\xcf\x00\x20\x00\x00\x00\x00\x00\x01", "MessagePack · read-only inspection"},
		{"sent form", "application/x-www-form-urlencoded", "name=hello+world&name=second", "2 form fields · original order"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			a := workspaceApp()
			side := 0
			if strings.HasPrefix(tc.name, "request") {
				side = 1
				a.flows[0].reqBody, a.flows[0].reqHeader = tc.body, http.Header{"Content-Type": {tc.contentType}}
			} else {
				a.flows[0].resBody, a.flows[0].respHeader = tc.body, http.Header{"Content-Type": {tc.contentType}}
			}
			if strings.HasPrefix(tc.name, "sent") {
				openDraft(a, a.flows[0])
				a.sentSet = true
				a.response = &capture.Flow{Status: 200, ResponseHeader: http.Header{"Content-Type": {tc.contentType}}, ResponseBody: tc.body}
			}
			t.Cleanup(func() {
				a.structured[0].stop()
				a.structured[1].stop()
				if a.requestDocument != nil {
					a.requestDocument.structured.stop()
				}
			})
			tt := ui.NewTester(a.view, 1280, 800)
			reader := &a.structured[side]
			if strings.HasPrefix(tc.name, "sent") {
				reader = &a.requestDocument.structured
			}
			waitInspection(t, tt, func() bool { return reader.job.answers == nil })
			if !tt.HasText(tc.want) {
				t.Fatal("Auto did not select structured viewer: ", tt.Texts())
			}
			if err := tt.Click("Copy view"); err != nil {
				t.Fatal(err)
			}
			switch {
			case strings.HasSuffix(tc.name, "XML"):
				if tt.Clipboard() != "<r>\n  <n>73</n>\n  <n>19</n>\n</r>" {
					t.Fatal("XML not formatted: ", tt.Clipboard())
				}
			case strings.HasSuffix(tc.name, "MessagePack"):
				if tt.Clipboard() != "9007199254740993" {
					t.Fatal("MessagePack integer rounded")
				}
			case strings.HasSuffix(tc.name, "form"):
				if !strings.Contains(tt.Clipboard(), `"rawValue"`) || !strings.Contains(tt.Clipboard(), `"key"`) {
					t.Fatal("copy lost form original/decoded spellings")
				}
			}
			if strings.HasPrefix(tc.name, "sent") {
				if a.response.ResponseBody != tc.body {
					t.Fatal("inspection changed sent response")
				}
			} else if side == 1 && a.flows[0].reqBody != tc.body || side == 0 && a.flows[0].resBody != tc.body {
				t.Fatal("inspection changed captured bytes")
			}
		})
	}
}

func TestInspectorStructuredErrorsRemainVisibleAndResetOnNewFlow(t *testing.T) {
	a := workspaceApp()
	// These bytes are also valid protobuf. Auto must still use the declared
	// MessagePack decoder, not confuse its optional format index with protobuf.
	a.flows[0].resBody, a.flows[0].respHeader = "\x08\x01", http.Header{"Content-Type": {"application/msgpack"}}
	t.Cleanup(a.structured[0].stop)
	tt := ui.NewTester(a.view, 960, 600)
	waitInspection(t, tt, func() bool { return a.structured[0].job.answers == nil })
	if !tt.HasText("MessagePack requires exactly one value") {
		t.Fatal(tt.Texts())
	}
	if err := tt.Click("Response body format"); err != nil {
		t.Fatal(err)
	}
	if err := chooseMenu(tt, "Hex"); err != nil {
		t.Fatal(err)
	}
	if !tt.HasText("08 01") {
		t.Fatal("Hex no longer reaches original bytes: ", tt.Texts())
	}
	a.flows[1].resBody, a.flows[1].respHeader = "x=next", http.Header{"Content-Type": {"application/x-www-form-urlencoded"}}
	a.picked = 1
	tt.Frame()
	waitInspection(t, tt, func() bool { return a.structured[0].job.answers == nil })
	if !tt.HasText("1 form fields · original order") || tt.HasText("MessagePack requires exactly one value") {
		t.Fatal("new flow retained the old decoder/error: ", tt.Texts())
	}
}

func TestInspectorJSONColumnsReadCompleteBodiesSortAndReplace(t *testing.T) {
	a := workspaceApp()
	t.Cleanup(a.columnRead.stop)
	file := filepath.Join(t.TempDir(), "body.json")
	if err := os.WriteFile(file, []byte(`{"ID":9007199254740993,"id":7}`), 0600); err != nil {
		t.Fatal(err)
	}
	a.flows[0].resBody, a.flows[0].resFile = "preview is not JSON", file
	a.flows[1].resBody = `{"ID":100,"id":31}`
	a.flows[0].reqBody, a.flows[1].reqBody = `{"id":"request-first"}`, `{"id":"request-second"}`
	large := customColumn{Source: sourceResponseJSON, Name: "/ID"}
	small := customColumn{Source: sourceResponseJSON, Name: "/id"}
	req := customColumn{Source: sourceRequestJSON, Name: "/id"}
	a.setCustomColumns([]customColumn{large, small, req})
	a.sort = ui.SortOrder{Column: large.id()}
	tt := ui.NewTester(a.view, 1280, 800)
	settle := func() {
		waitInspection(t, tt, func() bool {
			return a.columnRead.answers == nil && len(a.columnRead.queue) == 0 && !a.columnRead.dirty
		})
	}
	settle()
	if !tt.HasText("9007199254740993") || !tt.HasText("request-first") || a.columnValue(small, a.flows[0]) != "7" || a.visible[0] != 1 {
		t.Fatal("column sides/case, complete retained body, integer precision or lexical sort wrong: ", tt.Texts(), a.visible)
	}
	// A changed body cannot keep a prior answer; malformed data is visible.
	a.flows[1].resBody = `{"ID":1,"ID":2}`
	a.applyFilter()
	tt.Frame()
	settle()
	if !tt.HasText("Unavailable") || a.columnValue(large, a.flows[1]) != "" {
		t.Fatal("malformed source kept old value", tt.Texts())
	}
	a.setCustomColumns([]customColumn{{Source: sourceQuery, Name: "page"}})
	tt.Frame()
	if tt.HasText("Unavailable") || tt.HasText("9007199254740993") || a.flows[0].resBody != "preview is not JSON" {
		t.Fatal("removed column persisted, or capture was changed")
	}
}

func TestInspectorGraphQLOperationsSelectFilterAndInvalidate(t *testing.T) {
	a := workspaceApp()
	t.Cleanup(a.columnRead.stop)
	file := filepath.Join(t.TempDir(), "request.json")
	body := `{"query":"query Other { a } mutation Save { b }", "operationName":"Save"}`
	if err := os.WriteFile(file, []byte(body), 0600); err != nil {
		t.Fatal(err)
	}
	a.flows[0].method, a.flows[0].reqBody, a.flows[0].reqFile = "POST", "incomplete preview", file
	a.flows[0].reqHeader.Set("Content-Type", "application/json")
	a.flows[1].method, a.flows[1].url = "GET", "https://api.example.com/graphql?query="+url.QueryEscape(`query Search { items { id } }`)
	a.setCustomColumns([]customColumn{graphqlColumn})
	a.sort = ui.SortOrder{Column: graphqlColumn.id()}
	tt := ui.NewTester(a.view, 1280, 800)
	settle := func() {
		waitInspection(t, tt, func() bool {
			return a.columnRead.answers == nil && len(a.columnRead.queue) == 0 && !a.columnRead.dirty
		})
	}
	settle()
	if !tt.HasText("mutation Save") || !tt.HasText("query Search") || a.visible[0] != 0 {
		t.Fatal("GET, retained POST selection or operation sorting incorrect: ", tt.Texts(), a.visible)
	}
	// The filter must work without the display column and preserve exact case.
	a.setCustomColumns(nil)
	a.filterTriage.Operation = "Save"
	a.applyFilter()
	tt.Frame()
	settle()
	if got := visibleIDs(a); len(got) != 1 || got[0] != a.flows[0].id {
		t.Fatal("operation filter depends on visible column: ", got)
	}
	a.filterTriage.Operation = "save"
	a.applyFilter()
	if len(a.visible) != 0 {
		t.Fatal("operation matching ignored case")
	}
	a.filterTriage.Operation = "Other"
	a.applyFilter()
	if len(a.visible) != 0 {
		t.Fatal("unselected operation matched")
	}
	// A changed URL alone must invalidate a cached GET operation.
	a.filterTriage.Operation = "Search"
	a.flows[1].url = "https://api.example.com/graphql?query=" + url.QueryEscape(`query Renamed { items { id } }`)
	a.applyFilter()
	if len(a.visible) != 0 {
		t.Fatal("stale operation matched changed metadata")
	}
	tt.Frame()
	settle()
	a.filterTriage.Operation = "Renamed"
	a.applyFilter()
	if got := visibleIDs(a); len(got) != 1 || got[0] != a.flows[1].id {
		t.Fatal("changed GET URL not parsed", got)
	}
	// A batch matches a selected member; one malformed entry invalidates all.
	a.flows[1].method = "POST"
	a.flows[1].reqHeader = http.Header{"Content-Type": {"application/json"}}
	a.flows[1].reqBody = `[{"query":"query Renamed { a }"},{"query":"{ b }"}]`
	a.applyFilter()
	tt.Frame()
	settle()
	if len(a.visible) != 1 {
		t.Fatal("selected batch member did not match")
	}
	a.flows[1].reqBody = `[{"query":"query Renamed { a }"},{"query":"not GraphQL"}]`
	a.setCustomColumns([]customColumn{graphqlColumn})
	tt.Frame()
	settle()
	if len(a.visible) != 0 {
		t.Fatal("partial batch result matched")
	}
	a.filterTriage.Operation = ""
	a.applyFilter()
	tt.Frame()
	if !tt.HasText("Unavailable") || a.flows[0].reqBody != "incomplete preview" {
		t.Fatal("parse failure not visible, or inspection changed the capture")
	}
}
