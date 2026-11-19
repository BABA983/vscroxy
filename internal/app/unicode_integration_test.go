package app

import (
	"fmt"
	"image/png"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/baba983/vscroxy/internal/unicodechars"
	"github.com/baba983/vscroxy/locales"
	"github.com/egoist/mygo/ui"
	"github.com/egoist/mygo/ui/editor"
)

func TestUnicodeHeaderValueGetsOneDiagnosticWithItsRange(t *testing.T) {
	value := "Bearer ab\u200bcd"
	diags := (&app{}).unicodeDiagnostics(value)
	if len(diags) != 1 {
		t.Fatalf("want one character diagnostic, got %+v", diags)
	}
	diag := diags[0]
	if diag.Start != 9 || diag.End != 12 {
		t.Fatalf("range = %d:%d, want byte range 9:12 for the zero-width space", diag.Start, diag.End)
	}
	if diag.Severity != editor.SeverityWarning || diag.Source != "unicode" {
		t.Fatalf("severity/source = %v/%q, want warning/unicode", diag.Severity, diag.Source)
	}
	if diag.Message != "U+200B ZERO WIDTH SPACE" {
		t.Fatalf("message = %q", diag.Message)
	}

	// The editor keeps variable problems and the character warning together.
	a := workspaceApp()
	state := &editor.State{Buffer: editor.NewBuffer("{\"token\":\"{{missing}}\",\"note\":\"ab\u200bcd\"}")}
	a.setVariableDiagnostics(state, state.Buffer.String(), "")
	kinds := make(map[string]int)
	for _, d := range state.Diagnostics() {
		kinds[d.Source]++
	}
	if kinds["variables"] != 1 || kinds["unicode"] != 1 {
		t.Fatalf("diagnostic sources = %v", kinds)
	}
}

func TestUnicodeMaskedValueDiagnosticNeverShowsItsContents(t *testing.T) {
	secret := "Bearer ab\u200bcd-secret"
	highlights := (&app{}).unicodeHighlights(secret)
	if len(highlights) != 1 {
		t.Fatalf("want one flagged rune, got %+v", highlights)
	}
	details := unicodeTipDetails(secret, highlights, true)
	shown := strings.Join(details, "\n")
	for _, want := range []string{"U+200B", unicodeKindName(highlights[0].Kind), locales.UnicodeAtCharacter(10)} {
		if !strings.Contains(shown, want) {
			t.Fatalf("masked detail lacks %q: %q", want, shown)
		}
	}
	for _, leaked := range []string{"Bearer", "ab", "cd", "secret"} {
		if strings.Contains(strings.ToLower(shown), strings.ToLower(leaked)) {
			t.Fatalf("masked detail leaked %q: %q", leaked, shown)
		}
	}
	// The same rule applies to input styling: no literal value, only the
	// code point and position, reaches the tooltip.
	a := workspaceApp()
	a.notify(notice{level: noticeWarning, text: locales.UnicodeSendWarning("Authorization", unicodeCodePointLabel(0x200B, unicodechars.HighlightInvisible))})
	warning := a.notices[len(a.notices)-1].text
	if strings.Contains(warning, secret) || strings.Contains(warning, "Bearer") {
		t.Fatalf("warning leaked the value: %q", warning)
	}
}

func TestUnicodeSendWarnsOnceAndStillSends(t *testing.T) {
	seen := make(chan string, 1)
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		select {
		case seen <- r.URL.EscapedPath():
		default:
		}
		fmt.Fprint(w, `{"ok":true}`)
	}))
	defer upstream.Close()

	a := workspaceApp()
	a.srv = requestProxy(t)
	r := savedRequest{Method: "GET", URL: upstream.URL + "/a\u200bb"}
	a.addDocument(r, "draft:unicode")
	before := len(a.notices)
	a.sendComposed()

	if len(a.notices) != before+1 {
		t.Fatalf("want exactly one warning notice, got %+v", a.notices)
	}
	warning := a.notices[before]
	if warning.level != noticeWarning || !strings.Contains(warning.text, "U+200B") || warning.action != locales.UnicodeOpenField() {
		t.Fatalf("warning = %+v", warning)
	}
	if warning.run == nil || warning.valid == nil {
		t.Fatal("warning has no action or target check")
	}
	warning.run()
	focus := ui.NewTester(a.view, 1280, 800)
	focus.Frame()
	if !focus.Focused(locales.Url()) {
		t.Fatalf("Open field did not focus the URL: %q", focus.Texts())
	}
	if a.requestDocument.sent.URL != r.URL {
		t.Fatalf("send changed the URL: sent=%q, want %q", a.requestDocument.sent.URL, r.URL)
	}
	select {
	case path := <-seen:
		if !strings.Contains(path, "a") || !strings.Contains(path, "b") {
			t.Fatalf("unexpected wire path %q", path)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("the warned request was not sent")
	}
}

func TestUnicodeCJKBodyDoesNotWarn(t *testing.T) {
	a := workspaceApp()
	for _, tc := range []struct {
		name        string
		body        string
		diagnostics int
	}{
		{"CJK JSON", `{"name":"中文测试","city":"北京"}`, 0},
		{"CJK prose", "今天天气很好。", 0},
		{"invisible beside CJK", `{"name":"中文\u200b测试"}`, 1},
	} {
		t.Run(tc.name, func(t *testing.T) {
			body := strings.ReplaceAll(tc.body, `\u200b`, "\u200b")
			if got := len(a.unicodeDiagnostics(body)); got != tc.diagnostics {
				t.Fatalf("editor diagnostics = %d, want %d", got, tc.diagnostics)
			}
			r := savedRequest{Method: "POST", URL: "https://api.example.com", Body: body}
			before := len(a.notices)
			a.warnUnicodeBeforeSend(r, a.requestDocument, nil)
			if len(a.notices) != before {
				t.Fatalf("body text raised a send warning: %q", a.notices[before].text)
			}
		})
	}
	// A flagged header still warns when the request happens to carry CJK.
	r := savedRequest{Method: "POST", URL: "https://api.example.com",
		Headers: "X-Note: ab\u200bcd", Body: `{"name":"中文测试"}`}
	if warning := a.firstUnicodeSendWarning(r, nil); warning == nil || !strings.Contains(strings.ToLower(warning.field.name), "header") {
		t.Fatalf("header beside a CJK body was not warned: %+v", warning)
	}
}

func TestUnicodeQuickFixReplacement(t *testing.T) {
	a := workspaceApp()
	if replacement, label, ok := a.unicodeFix('\u200b'); !ok || replacement != "" || label != locales.UnicodeRemoveCharacter() {
		t.Fatalf("invisible fix = %q/%q/%v", replacement, label, ok)
	}
	if replacement, label, ok := a.unicodeFix('\u0430'); !ok || replacement != "a" || label != locales.UnicodeReplaceWithAscii() {
		t.Fatalf("ambiguous fix = %q/%q/%v", replacement, label, ok)
	}
}

// TestUnicodeIntegrationScreenshots exports 2x captures of the diagnostic and
// the pre-send warning in both themes. Set the env var to an output directory.
func TestUnicodeIntegrationScreenshots(t *testing.T) {
	dir := os.Getenv("VSCROXY_UNICODE_INTEGRATION_SCREENSHOTS")
	if dir == "" {
		t.Skip("set VSCROXY_UNICODE_INTEGRATION_SCREENSHOTS to an output directory")
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	for _, dark := range []bool{false, true} {
		appearance := "light"
		if dark {
			appearance = "dark"
		}
		t.Run(appearance+"-diagnostic", func(t *testing.T) {
			a := workspaceApp()
			a.appearance = appearance
			r := savedRequest{Method: "POST", URL: "https://api.example.com/projects",
				Body: `{"name":"ab\u200bcd","note":"pаssword"}`}
			a.addDocument(r, "draft:unicode")
			d := a.requestDocument
			d.composeTab, d.composeBody = composeTabBody, strings.ReplaceAll(r.Body, `\u200b`, "\u200b")
			d.bodyFor = ""
			tt := ui.NewTester(a.view, 1280, 800)
			tt.SetLocale("en-US")
			tt.SetScale(2)
			tt.Frame()
			if d.bodyEditor == nil {
				t.Fatal("body editor was not built")
			}
			diags := d.bodyEditor.Diagnostics()
			if len(diags) < 2 {
				t.Fatalf("want invisible and ambiguous diagnostics, got %+v", diags)
			}
			var invisible editor.Diagnostic
			for _, diag := range diags {
				if diag.Message == "U+200B ZERO WIDTH SPACE" {
					invisible = diag
				}
			}
			if invisible.Message == "" {
				t.Fatalf("no U+200B diagnostic: %+v", diags)
			}
			d.bodyEditor.ShowHover(invisible.Start, invisible.End, "", nil)
			tt.Frame()
			if !tt.HasText("U+200B ZERO WIDTH SPACE") || !tt.HasText(locales.UnicodeRemoveCharacter()) {
				t.Fatalf("hover lacks the diagnostic and quick fix: %q", tt.Texts())
			}
			writeUnicodeScreenshot(t, filepath.Join(dir, "editor-diagnostic-"+appearance+".png"), tt)
		})
		t.Run(appearance+"-send-warning", func(t *testing.T) {
			a := workspaceApp()
			a.appearance = appearance
			r := savedRequest{Method: "GET", URL: "https://api.example.com/a\u200bb", Headers: "X-Note: plain"}
			a.addDocument(r, "draft:unicode")
			a.warnUnicodeBeforeSend(r, a.requestDocument, nil)
			tt := ui.NewTester(a.view, 1280, 800)
			tt.SetLocale("en-US")
			tt.SetScale(2)
			tt.Frame()
			tt.Advance(250 * time.Millisecond)
			tt.Frame()
			if !tt.HasText("U+200B") || !tt.HasText(locales.UnicodeOpenField()) {
				t.Fatalf("toast lacks the warning and action: %q", tt.Texts())
			}
			writeUnicodeScreenshot(t, filepath.Join(dir, "send-warning-"+appearance+".png"), tt)
		})
	}
}

func writeUnicodeScreenshot(t *testing.T, path string, tt *ui.Tester) {
	t.Helper()
	file, err := os.Create(path)
	if err != nil {
		t.Fatal(err)
	}
	defer file.Close()
	if err := png.Encode(file, tt.Image()); err != nil {
		t.Fatal(err)
	}
}
