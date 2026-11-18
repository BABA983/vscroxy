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

func unicodeMarkerLabel() string {
	return unicodeMarkerText + " " + locales.UnicodeHint()
}

func unicodeMarkerShown(tt *ui.Tester) bool {
	for _, text := range tt.Texts() {
		if text == unicodeMarkerLabel() {
			return true
		}
	}
	return false
}

func hoverUnicodeMarker(t *testing.T, tt *ui.Tester) {
	t.Helper()
	r, ok := tt.Find(unicodeMarkerLabel())
	if !ok {
		t.Fatalf("no marker in %q", tt.Texts())
	}
	tt.Move(r.X+r.W/2, r.Y+r.H/2)
	tt.Advance(200 * time.Millisecond)
	tt.Frame()
}

func assertNoSecretInTip(t *testing.T, tt *ui.Tester, secret string) {
	t.Helper()
	shown := strings.Join(tt.Texts(), "\n")
	for _, part := range []string{"Bearer", secret, "secret", "ab\u200bcd"} {
		if strings.Contains(shown, part) {
			t.Fatalf("tip leaked %q: %q", part, shown)
		}
	}
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
	hoverUnicodeMarker(t, tt)
	if !tt.HasText(locales.UnicodeInvisible()) || !tt.HasText("⟨U+200B⟩") {
		t.Fatalf("the tip did not localise the kind or show Reveal: %q", tt.Texts())
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

func TestUnicodeHintsFlagMaskedHeadersWithoutLeakingThem(t *testing.T) {
	secret := "Bearer ab\u200bcd-secret"
	cases := []struct {
		name  string
		setup func(*app)
	}{
		{"inspector", func(a *app) {
			a.flows[0].respHeader.Set("Authorization", secret)
			a.inspect.ready, a.inspect.tab[1] = true, resViewHeaders
		}},
		{"request editor", func(a *app) {
			openDraft(a, a.flows[0])
			d := a.requestDocument
			d.composeHeaders = "Authorization: " + secret
			d.headerFor = ""
		}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			a := workspaceApp()
			tc.setup(a)
			tt := ui.NewTester(a.view, 1280, 800)
			tt.Frame()
			if !unicodeMarkerShown(tt) {
				t.Fatalf("a masked secret with U+200B was not flagged: %q", tt.Texts())
			}
			hoverUnicodeMarker(t, tt)
			shown := strings.Join(tt.Texts(), "\n")
			for _, want := range []string{"U+200B", locales.UnicodeInvisible(), locales.UnicodeAtCharacter(10)} {
				if !strings.Contains(shown, want) {
					t.Fatalf("tip did not name %q: %q", want, shown)
				}
			}
			if strings.Contains(shown, "⟨U+") {
				t.Fatalf("a masked tip offered a revealed view: %q", shown)
			}
			assertNoSecretInTip(t, tt, secret)
		})
	}
}

func TestUnicodeHintsFlagRequestFieldAndURLWithoutChangingThem(t *testing.T) {
	a := workspaceApp()
	openDraft(a, a.flows[0])
	d := a.requestDocument
	d.composeHeaders = "X-Note: Bearer ab\u200bcd"
	d.composeURL = "https://аpple.com/projects"
	d.headerFor = ""

	tt := ui.NewTester(a.view, 1280, 800)
	tt.Frame()
	if !unicodeMarkerShown(tt) {
		t.Fatalf("the header field and URL were not flagged: %q", tt.Texts())
	}
	if d.composeHeaders != "X-Note: Bearer ab\u200bcd" || d.composeURL != "https://аpple.com/projects" {
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

// TestUnicodeHintScreenshots exports 2x captures with the same Tester/png
// pattern as the repository's snapshot tests. Set the env var to write them.
func TestUnicodeHintScreenshots(t *testing.T) {
	dir := os.Getenv("VSCROXY_UNICODE_SCREENSHOTS")
	if dir == "" {
		t.Skip("set VSCROXY_UNICODE_SCREENSHOTS to an output directory")
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}

	maskedHeader := func(a *app) {
		a.flows[0].respHeader = http.Header{"Authorization": {"Bearer ab\u200bcd-secret"}}
		a.inspect.ready, a.inspect.tab[1] = true, resViewHeaders
	}
	unmaskedHeader := func(a *app) {
		a.flows[0].respHeader = http.Header{"X-Note": {"Bearer ab\u200bcd"}}
		a.inspect.ready, a.inspect.tab[1] = true, resViewHeaders
	}
	flaggedHost := func(a *app) {
		openDraft(a, a.flows[0])
		a.requestDocument.composeURL = "https://аpple.com/projects"
	}
	queryKeys := func(a *app) {
		openDraft(a, a.flows[0])
		d := a.requestDocument
		d.composeURL = "https://api.example.com/projects?q=ab\u200bcd&tag=plain"
		d.querySource = ""
		d.composeTab = composeTabParams
	}
	scenes := []struct {
		name  string
		setup func(*app)
		act   func(*testing.T, *ui.Tester)
	}{
		{"unflagged-default", nil, nil},
		{"masked-header", maskedHeader, nil},
		{"masked-tip", maskedHeader, func(t *testing.T, tt *ui.Tester) {
			hoverUnicodeMarker(t, tt)
			if !tt.HasText("U+200B") || tt.HasText("Bearer") {
				t.Fatalf("the masked tip was not safe: %q", tt.Texts())
			}
		}},
		{"flagged-host", flaggedHost, func(t *testing.T, tt *ui.Tester) {
			hoverUnicodeMarker(t, tt)
			if !tt.HasText("U+0430") || !tt.HasText("U+0061") {
				t.Fatalf("the host tip did not name the confusable: %q", tt.Texts())
			}
		}},
		{"revealed-view", unmaskedHeader, func(t *testing.T, tt *ui.Tester) {
			hoverUnicodeMarker(t, tt)
			if !tt.HasText("U+200B") || !tt.HasText("⟨U+200B⟩") {
				t.Fatalf("the revealed tip did not name the code point: %q", tt.Texts())
			}
		}},
		{"query-keys", queryKeys, func(t *testing.T, tt *ui.Tester) {
			if !tt.HasText("parameter key 2") || !unicodeMarkerShown(tt) {
				t.Fatalf("the query tab did not show two keys and a flagged value: %q", tt.Texts())
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
			tt.SetScale(2)
			tt.Frame()
			if scene.act != nil {
				scene.act(t, tt)
			}
			file, err := os.Create(filepath.Join(dir, scene.name+".png"))
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
