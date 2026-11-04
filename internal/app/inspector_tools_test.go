package app

import (
	"net/http"
	"strings"
	"testing"

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
