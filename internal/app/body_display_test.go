package app

import (
	"io"
	"net/http"
	"os"
	"path/filepath"
	"slices"
	"testing"

	"github.com/baba983/vscroxy/internal/capture"
	"github.com/egoist/mygo/ui"
	"golang.org/x/text/encoding/simplifiedchinese"
)

// Decoding for display must not travel with the body anywhere it leaves the
// viewer: copy, save, replay and the capture itself keep the captured bytes.
func TestDecodedBodyDisplayKeepsRawBytesOnCopySaveAndReplay(t *testing.T) {
	const requestText, responseText = "请求原文", "响应中文正文"
	encode := func(text string) string {
		b, err := simplifiedchinese.GBK.NewEncoder().Bytes([]byte(text))
		if err != nil {
			t.Fatal(err)
		}
		return string(b)
	}
	rawRequest, rawResponse := encode(requestText), encode(responseText)

	sent := make(chan string, 1)
	a, flows := replayTestApp(t, func(w http.ResponseWriter, r *http.Request) {
		body, err := io.ReadAll(r.Body)
		if err != nil {
			t.Errorf("read replay body: %v", err)
		}
		sent <- string(body)
		w.WriteHeader(http.StatusOK)
	})
	f := &flows[0] // the inspector's selected exchange
	f.reqBody, f.resBody = rawRequest, rawResponse
	f.reqHeader = http.Header{"Content-Type": {"text/plain; charset=gbk"}}
	f.respHeader = http.Header{"Content-Type": {"text/plain; charset=gbk"}}
	f.status = http.StatusOK
	a.pickedID = f.id
	a.applyFilter()
	a.picked = slices.Index(a.visible, 0)
	a.syncDetail()
	openResponseBody(a)

	tt := ui.NewTester(a.view, 960, 700)
	tt.Frame()
	if !tt.HasText("Encoding: GBK") {
		t.Fatalf("no encoding label: %v", tt.Texts())
	}
	if got := a.bodyText[0].Buffer.String(); got != responseText {
		t.Fatalf("displayed body = %q, want decoded %q", got, responseText)
	}
	if a.body != rawResponse || a.reqBody != rawRequest {
		t.Fatal("display decoding changed the captured bytes")
	}

	if err := tt.Click("Response pane options"); err != nil {
		t.Fatal(err)
	}
	if err := chooseMenu(tt, "Copy shown body"); err != nil {
		t.Fatal(err)
	}
	if got := tt.Clipboard(); got != rawResponse {
		t.Fatalf("copy wrote %q, want the captured GBK bytes", got)
	}

	path := filepath.Join(t.TempDir(), "response.bin")
	if err := writeResponseBody(path, capture.Flow{Status: f.status, ResponseBody: f.resBody, ResponseBodySize: len(f.resBody)}); err != nil {
		t.Fatal(err)
	}
	saved, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(saved) != rawResponse {
		t.Fatalf("saved response = %q, want the captured GBK bytes", saved)
	}

	if _, err := a.replayFlow(t.Context(), *f, false); err != nil {
		t.Fatalf("replay: %v", err)
	}
	if got := <-sent; got != rawRequest {
		t.Fatalf("replay sent %q, want the captured GBK request bytes", got)
	}
}
