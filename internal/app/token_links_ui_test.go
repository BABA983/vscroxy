package app

import (
	"net/http"
	"reflect"
	"testing"
	"time"

	"github.com/egoist/mygo/ui"
)

func tokenPreviewFlows() []flow {
	return []flow{
		{id: 1, live: true, method: "POST", url: "https://api.example.com/login", status: 200, at: time.Unix(100, 0), resBody: `{"data":{"access_token":"preview-secret-token-123"}}`, respHeader: http.Header{"Content-Type": {"application/json"}}},
		{id: 2, live: true, method: "GET", url: "https://api.example.com/profile", status: 200, at: time.Unix(102, 0), millis: 10, reqHeader: http.Header{"Authorization": {"Bearer preview-secret-token-123"}}},
	}
}

func TestTokenLinksPreviewIsReadOnlyAndMasked(t *testing.T) {
	var a *app
	var tt *ui.Tester
	for _, size := range [][2]float32{{1280, 800}, {960, 600}} {
		a = libraryApp(t)
		a.flows = tokenPreviewFlows()
		before := append([]savedRequest(nil), a.requests...)
		a.openTokenLinks(a.flows)
		tt = ui.NewTester(a.view, int(size[0]), int(size[1]))
		if !tt.HasText("Response #1 → 1 Bearer requests") || !tt.HasText(`["data","access_token"]`) || tt.HasText("preview-secret-token-123") {
			t.Fatalf("incorrect or unmasked preview: %v", tt.Texts())
		}
		r, ok := tt.Find("Close token links")
		if !ok || r.Y < 0 || r.Y+r.H > size[1] {
			t.Fatal("footer outside the window")
		}
		if !reflect.DeepEqual(a.requests, before) || len(libraryTree(t, a.library.root)) != 0 {
			t.Fatal("preview persisted changes")
		}
	}
	if err := tt.Click("Close token links"); err != nil {
		t.Fatal(err)
	}
	tt.Frame()
	if a.tokenLinks.open || tt.HasText("JSON path segments:") {
		t.Fatal("preview did not close")
	}
	a.openTokenLinks(a.flows[:1])
	tt.Frame()
	if !tt.HasText("No unambiguous token links found") {
		t.Fatal("empty analysis state missing")
	}
	tt.Key(0, ui.KeyEscape)
	tt.Frame()
	if a.tokenLinks.open {
		t.Fatal("escape did not close preview")
	}
}
