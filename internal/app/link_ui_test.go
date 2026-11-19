package app

import (
	"net/http"
	"slices"
	"testing"

	"github.com/baba983/vscroxy/internal/request"
	"github.com/baba983/vscroxy/locales"
	"github.com/egoist/mygo/ui"
)

// capturedLinkTester opens a response body in the mode a captured content
// type selects, so tests click the same richtext links a person would.
func capturedLinkTester(t *testing.T, contentType, body, mode string) (*app, *ui.Tester, *ui.Context) {
	t.Helper()
	a := snapshotApp()
	a.flows[0].respHeader = http.Header{"Content-Type": {contentType}}
	a.flows[0].resBody = body
	modes := a.bodyModes(a.flows[0], 0)
	index := slices.Index(modes, mode)
	if index < 0 {
		t.Fatalf("%s has no %s mode: %v", contentType, mode, modes)
	}
	a.bodyMode[0] = index
	a.bodyFormatByType = map[string]string{contentType: mode}
	openResponseBody(a)
	var c *ui.Context
	tt := ui.NewTester(func(ctx *ui.Context) { c = ctx; a.view(ctx) }, 1440, 900)
	tt.SetLocale("en-US")
	tt.Frame()
	return a, tt, c
}

// clickBodyRichtextLink finds a link by the pointer cursor the rich text
// editor sets over it. Its document is one accessible text node, so there is
// no per-link label for a tester to click by name.
func clickBodyRichtextLink(t *testing.T, tt *ui.Tester) {
	t.Helper()
	for _, node := range tt.AccessAll() {
		if node.Role != ui.RoleTextField || node.Bounds.X < 900 {
			continue
		}
		r := node.Bounds
		for y := r.Y + 3; y < min(r.Y+48, r.Y+r.H-3); y += 4 {
			for x := r.X + 3; x < min(r.X+360, r.X+r.W-3); x += 5 {
				tt.Move(x, y)
				if tt.Cursor() == ui.CursorPointer {
					tt.ClickAt(x, y)
					return
				}
			}
		}
	}
	t.Fatalf("no link in the body's rich text: %q", tt.Texts())
}

func TestCapturedMarkdownLinkAsksBeforeOpening(t *testing.T) {
	target := "https://example.com/report"
	a, tt, _ := capturedLinkTester(t, "text/markdown", "[the report]("+target+")", "Markdown")
	clickBodyRichtextLink(t, tt)
	tt.Frame()
	if !a.linkPrompt.open || a.linkPrompt.raw != target {
		t.Fatalf("no confirmation for %q: %+v", target, a.linkPrompt)
	}
	if urls := tt.OpenedURLs(); len(urls) != 0 {
		t.Fatalf("opened %q before the person confirmed", urls)
	}
	if !tt.HasText(target) {
		t.Errorf("the dialog does not show the whole target: %q", tt.Texts())
	}
	if !tt.HasText(locales.ExternalLinkFromContent()) {
		t.Error("the dialog has no explanation of where the link came from")
	}
	if err := tt.Click(locales.CopyLink()); err != nil {
		t.Fatalf("click Copy link: %v; texts: %q", err, tt.Texts())
	}
	if got := tt.Clipboard(); got != target {
		t.Errorf("clipboard = %q, want the target %q", got, target)
	}
	if !a.linkPrompt.open || len(tt.OpenedURLs()) != 0 {
		t.Fatal("copying opened the link or closed the question")
	}
	if err := tt.Click(locales.Cancel()); err != nil {
		t.Fatalf("click Cancel: %v; texts: %q", err, tt.Texts())
	}
	if a.linkPrompt.open || len(tt.OpenedURLs()) != 0 {
		t.Fatal("cancel did not leave the link closed")
	}
}

func TestCapturedHTMLLinkCanOpenAfterConfirmation(t *testing.T) {
	target := "https://example.com/page?item=42"
	a, tt, _ := capturedLinkTester(t, "text/html", `<p>Read <a href="`+target+`">the page</a></p>`, "Preview")
	clickBodyRichtextLink(t, tt)
	tt.Frame()
	if !a.linkPrompt.open || a.linkPrompt.raw != target {
		t.Fatalf("no confirmation for %q: %+v", target, a.linkPrompt)
	}
	if err := tt.Click(locales.OpenExternalLink()); err != nil {
		t.Fatalf("click Open: %v; texts: %q", err, tt.Texts())
	}
	tt.Frame()
	if a.linkPrompt.open {
		t.Fatal("the dialog stayed open after Open")
	}
	if urls := tt.OpenedURLs(); !slices.Contains(urls, target) {
		t.Fatalf("opened URLs = %q, want %q", urls, target)
	}
}

func TestBlockedCapturedLinkShowsWhy(t *testing.T) {
	a, tt, c := capturedLinkTester(t, "text/markdown", "[the action](javascript:alert(1))", "Markdown")
	// MyGo does not make a dangerous Markdown href clickable at all. The
	// guard also refuses one handed to it from any other content surface.
	a.openCapturedLink(c, "javascript:alert(1)")
	tt.Frame()
	if a.linkPrompt.target.block == linkBlockNone || !a.linkPrompt.open {
		t.Fatalf("javascript: link was not blocked: %+v", a.linkPrompt)
	}
	if !tt.HasText(locales.BlockedScriptLink()) {
		t.Errorf("the blocked link has no reason: %q", tt.Texts())
	}
	if err := tt.Click(locales.Cancel()); err != nil {
		t.Fatalf("click Cancel: %v", err)
	}
	if urls := tt.OpenedURLs(); len(urls) != 0 {
		t.Fatalf("a blocked link opened %q", urls)
	}
}

func TestTrustedHostIsSavedAndClearable(t *testing.T) {
	target := "https://example.com/second-page"
	trusted := classifyLink(target).trustKey
	a, tt, _ := capturedLinkTester(t, "text/markdown", "[first]("+target+")", "Markdown")
	clickBodyRichtextLink(t, tt)
	tt.Frame()
	if err := tt.Click(locales.TrustThisHost()); err != nil {
		t.Fatalf("click Trust this host: %v; texts: %q", err, tt.Texts())
	}
	if err := tt.Click(locales.OpenExternalLink()); err != nil {
		t.Fatalf("click Open: %v; texts: %q", err, tt.Texts())
	}
	tt.Frame()
	if urls := tt.OpenedURLs(); !slices.Contains(urls, target) {
		t.Fatalf("Open did not pass the full target to the system: %q", urls)
	}
	if got := a.currentSettings().TrustedLinkHosts; !slices.Contains(got, trusted) {
		t.Fatalf("trust was not saved with the settings: %q", got)
	}

	clickBodyRichtextLink(t, tt)
	tt.Frame()
	if a.linkPrompt.open {
		t.Fatal("the trusted host opened the confirmation again")
	}

	a.settingsOpen, a.settingsTab, a.settingsChoice = true, 0, "0"
	tt.Frame()
	tt.Scroll(800, 500, 0, 600)
	tt.Frame()
	if err := tt.Click(locales.ClearTrustedLinkHosts()); err != nil {
		t.Fatalf("click Clear trusted hosts: %v; texts: %q", err, tt.Texts())
	}
	if len(a.trustedLinkHosts) != 0 {
		t.Fatalf("trusted hosts were not cleared: %q", a.trustedLinkHosts)
	}
}

func TestImportedDescriptionLinkAsksBeforeOpening(t *testing.T) {
	raw := "https://docs.example.com/guide"
	a := snapshotApp()
	a.folders = []request.Folder{{Path: "API", Description: "[the guide](" + raw + ")"}}
	a.openDocs("API")
	tt := ui.NewTester(a.view, 1100, 760)
	tt.SetLocale("en-US")
	tt.Frame()
	link, ok := tt.AccessByRole(ui.RoleLink, "the guide")
	if !ok {
		t.Fatalf("the description's link is not in the dialog: %q", tt.Texts())
	}
	tt.ClickAt(link.Bounds.X+link.Bounds.W/2, link.Bounds.Y+link.Bounds.H/2)
	tt.Frame()
	if !a.linkPrompt.open || a.linkPrompt.raw != raw {
		t.Fatalf("an imported description's link was not confirmed: %+v", a.linkPrompt)
	}
	if urls := tt.OpenedURLs(); len(urls) != 0 {
		t.Fatalf("the description's link opened %q before confirmation", urls)
	}
}

func TestMalformedNumericHostShowsWhy(t *testing.T) {
	a, tt, c := capturedLinkTester(t, "text/markdown", "[report](https://example.com/report)", "Markdown")
	a.openCapturedLink(c, "http://1.2.3.4.5/x")
	tt.Frame()
	if !tt.HasText(locales.ExternalLinkNumericHostWarning()) {
		t.Errorf("a numeric host that cannot parse has no warning: %q", tt.Texts())
	}
	if a.linkPrompt.target.trustKey != "" {
		t.Fatal("a malformed numeric host has a trust key")
	}
	if _, ok := tt.Find(locales.TrustThisHost()); ok {
		t.Fatal("a malformed numeric host can be trusted")
	}
}

func TestUnusualLinkCannotBeTrusted(t *testing.T) {
	a, tt, c := capturedLinkTester(t, "text/markdown", "[report](https://example.com/report)", "Markdown")
	a.openCapturedLink(c, "http://user@evil.com@good.com/report")
	tt.Frame()
	if !tt.HasText(locales.ExternalLinkUnusual()) || !tt.HasText(locales.ExternalLinkUserinfoWarning()) {
		t.Errorf("the disguised host has no warning: %q", tt.Texts())
	}
	if _, ok := tt.Find(locales.TrustThisHost()); ok {
		t.Error("a disguised host can be trusted without asking again")
	}
	if urls := tt.OpenedURLs(); len(urls) != 0 {
		t.Fatalf("the unconfirmed link opened %q", urls)
	}
}
