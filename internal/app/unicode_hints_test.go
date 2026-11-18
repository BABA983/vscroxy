package app

import (
	"image/png"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/baba983/vscroxy/locales"
	"github.com/egoist/mygo/ui"
)

func unicodeMarkerShown(tt *ui.Tester) bool {
	for _, text := range tt.Texts() {
		if text == unicodeMarkerText+" "+locales.Reveal() {
			return true
		}
	}
	return false
}

func TestUnicodeHintsFlagCapturedHeaderWithoutChangingIt(t *testing.T) {
	value := "Bearer ab\u200bcd"
	a := workspaceApp()
	a.flows[0].respHeader.Set("X-Note", value)
	a.inspect.ready, a.inspect.tab[1] = true, resViewHeaders
	tt := ui.NewTester(a.view, 1280, 800)
	tt.Frame()
	if !unicodeMarkerShown(tt) {
		t.Fatalf("a header with U+200B was not flagged: %q", tt.Texts())
	}
	if got := a.flows[0].respHeader.Get("X-Note"); got != value {
		t.Fatalf("showing the hint changed the captured header: %q", got)
	}

	plain := workspaceApp()
	plain.flows[0].respHeader.Set("X-Note", "Bearer abcd")
	plain.inspect.ready, plain.inspect.tab[1] = true, resViewHeaders
	plainTester := ui.NewTester(plain.view, 1280, 800)
	plainTester.Frame()
	if unicodeMarkerShown(plainTester) {
		t.Fatalf("an ordinary header was flagged: %q", plainTester.Texts())
	}
}

func TestUnicodeHintsFlagRequestFieldAndURLWithoutChangingThem(t *testing.T) {
	a := workspaceApp()
	openDraft(a, a.flows[0])
	d := a.requestDocument
	d.composeHeaders = "X-Token: Bearer ab\u200bcd"
	d.composeURL = "https://аpple.com/projects"
	d.headerFor = ""

	tt := ui.NewTester(a.view, 1280, 800)
	tt.Frame()
	if !unicodeMarkerShown(tt) {
		t.Fatalf("the header field and URL were not flagged: %q", tt.Texts())
	}
	if d.composeHeaders != "X-Token: Bearer ab\u200bcd" || d.composeURL != "https://аpple.com/projects" {
		t.Fatalf("showing hints changed the draft: headers %q, URL %q", d.composeHeaders, d.composeURL)
	}
}

func TestUnicodeHintsUseTheAppsChineseLocale(t *testing.T) {
	a := workspaceApp()
	a.language = "zh-Hans"
	a.flows[0].reqHeader.Set("X-Note", "中文测试")
	tt := ui.NewTester(a.view, 1280, 800)
	tt.SetLocale("zh-Hans")
	tt.Frame()
	if unicodeMarkerShown(tt) {
		t.Fatalf("Chinese text was flagged under zh-Hans: %q", tt.Texts())
	}
}

// TestUnicodeHintScreenshots reuses the repository's Tester/png export pattern
// with a smaller scene table. Set VSCROXY_UNICODE_SCREENSHOTS to write it.
func TestUnicodeHintScreenshots(t *testing.T) {
	dir := os.Getenv("VSCROXY_UNICODE_SCREENSHOTS")
	if dir == "" {
		t.Skip("set VSCROXY_UNICODE_SCREENSHOTS to an output directory")
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}

	flaggedHeader := func(a *app) {
		a.flows[0].respHeader = http.Header{"X-Note": {"Bearer ab\u200bcd"}}
		a.inspect.ready, a.inspect.tab[1] = true, resViewHeaders
	}
	flaggedHost := func(a *app) {
		openDraft(a, a.flows[0])
		a.requestDocument.composeURL = "https://аpple.com/projects"
	}
	scenes := []struct {
		name  string
		setup func(*app)
		act   func(*ui.Tester)
	}{
		{"unflagged-default", nil, nil},
		{"flagged-header", flaggedHeader, nil},
		{"flagged-host", flaggedHost, nil},
		{"revealed-view", flaggedHeader, func(tt *ui.Tester) {
			r, ok := tt.Find(unicodeMarkerText + " " + locales.Reveal())
			if !ok {
				t.Fatal("the flagged header has no marker")
			}
			tt.Move(r.X+r.W/2, r.Y+r.H/2)
			tt.Advance(200 * time.Millisecond)
			tt.Frame()
			if !tt.HasText("U+200B") || !tt.HasText("⟨U+200B⟩") {
				t.Fatalf("the revealed tip did not name the code point: %q", tt.Texts())
			}
		}},
	}
	for _, scene := range scenes {
		t.Run(scene.name, func(t *testing.T) {
			a := workspaceApp()
			if scene.setup != nil {
				scene.setup(a)
			}
			tt := ui.NewTester(a.view, 1280, 800)
			tt.SetLocale("en-US")
			tt.Frame()
			if scene.act != nil {
				scene.act(tt)
			}
			file, err := os.Create(filepath.Join(dir, strings.ReplaceAll(scene.name, "/", "-")+".png"))
			if err != nil {
				t.Fatal(err)
			}
			defer file.Close()
			if err := png.Encode(file, tt.Image()); err != nil {
				t.Fatal(err)
			}
		})
	}
}
