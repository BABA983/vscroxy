package app

import (
	"reflect"
	"strings"
	"testing"

	"github.com/baba983/vscroxy/internal/request"
	"github.com/baba983/vscroxy/locales"
	"github.com/egoist/mygo/ui"
)

// pasteURL pastes text into the focused URL field. A frame before the key is
// needed so the field's selection is available to the paste hook.
func pasteURL(t *testing.T, tt *ui.Tester, text string) {
	t.Helper()
	if err := tt.Click("URL"); err != nil {
		t.Fatalf("click URL: %v", err)
	}
	tt.SetClipboard(text)
	tt.Key(ui.Cmd, ui.KeyV)
	tt.Frame()
}

func pastedURLApp(t *testing.T) (*app, *requestDocument, *ui.Tester) {
	t.Helper()
	a := workspaceApp()
	d := openTestDraft(a)
	tt := ui.NewTester(a.view, 1280, 800)
	tt.Frame()
	return a, d, tt
}

func pastedHeaderValue(rows []request.Field, name string) string {
	for _, row := range rows {
		if strings.EqualFold(row.Name, name) {
			return row.Value
		}
	}
	return ""
}

func TestPasteCurlIntoURLFillsTheWholeDraft(t *testing.T) {
	a, d, tt := pastedURLApp(t)
	command := `curl --request PUT 'https://api.test/v2/item?id=7' ` +
		`-H 'X-One: 1' -H 'X-Two: 2' -H 'Content-Type: application/json' ` +
		`-u 'user:secret' --data-raw '{"ok":true}'`
	pasteURL(t, tt, command)

	if d.composeMethod != "PUT" || d.composeURL != "https://api.test/v2/item?id=7" {
		t.Errorf("method/URL = %q %q", d.composeMethod, d.composeURL)
	}
	if pastedHeaderValue(d.headerRows, "X-One") != "1" || pastedHeaderValue(d.headerRows, "X-Two") != "2" {
		t.Errorf("multi-header paste lost headers: %+v", d.headerRows)
	}
	if !strings.Contains(d.composeHeaders, "X-Two: 2") || d.composeBody != `{"ok":true}` {
		t.Errorf("headers/body = %q / %q", d.composeHeaders, d.composeBody)
	}
	if auth := pastedHeaderValue(d.headerRows, "Authorization"); !strings.HasPrefix(auth, "Basic ") {
		t.Errorf("credentials were not carried into the draft: %q", auth)
	}
	if d.sentSet {
		t.Error("filling the draft sent the request")
	}
	if !tt.HasText(locales.CurlPasteFilledRequest()) {
		t.Errorf("no notice for the filled draft: %v", tt.Texts())
	}
	if len(a.notices) != 1 || a.notices[0].action != locales.CurlPasteAsTextAction() {
		t.Errorf("notice = %+v, want one Paste as text action", a.notices)
	}
}

func TestPasteAsTextRestoresTheOldDraftAndInsertsTheCommand(t *testing.T) {
	a, d, tt := pastedURLApp(t)
	d.load(a, savedRequest{Request: request.Request{
		Method: "POST", URL: "https://old.test/start", Headers: "X-Old: 1",
		Body: "old body", Auth: request.Auth{Type: "Bearer", Token: "old token"},
	}}, "")
	tt.Frame()
	before := d.value()
	command := `curl -X PATCH 'https://new.test/v2' -H 'X-New: 2'`

	pasteURL(t, tt, command)
	if d.composeURL == before.URL || d.composeMethod != "PATCH" {
		t.Fatalf("the cURL command did not fill the draft: %q %q", d.composeMethod, d.composeURL)
	}
	if !tt.HasText(locales.CurlPasteAsTextAction()) {
		t.Fatalf("no paste-as-text action: %v", tt.Texts())
	}
	if err := tt.Click(locales.CurlPasteAsTextAction()); err != nil {
		t.Fatalf("click Paste as text: %v", err)
	}
	tt.Frame()

	want := before
	want.URL += command
	if !reflect.DeepEqual(d.value(), want) {
		t.Errorf("restored draft =\n%+v\nwant\n%+v", d.value(), want)
	}
}

func TestPasteCurlShowsParserWarningsOnTheNotice(t *testing.T) {
	a, d, tt := pastedURLApp(t)
	command := "$session = New-Object Microsoft.PowerShell.Commands.WebRequestSession\n" +
		"$session.Cookies.Add((New-Object System.Net.Cookie(\"token\", \"a b\", \"/app\", \"api.example.com\")))\n" +
		"Invoke-WebRequest -UseBasicParsing -Uri \"https://api.example.com/echo\" -Method \"PATCH\" -WebSession $session"
	pasteURL(t, tt, command)

	if d.composeMethod != "PATCH" || d.composeURL != "https://api.example.com/echo" {
		t.Errorf("Chrome PowerShell command was not imported: %q %q", d.composeMethod, d.composeURL)
	}
	if len(a.notices) != 1 || !strings.Contains(a.notices[0].detail, "cookie domain/path were dropped") {
		t.Fatalf("notice = %+v, want the parser's cookie warning", a.notices)
	}
	if !tt.HasText(a.notices[0].detail) {
		t.Errorf("warning was not shown in the notice: %v", tt.Texts())
	}
}

func TestPastePlainTextIntoURLChangesOnlyTheURL(t *testing.T) {
	a, d, tt := pastedURLApp(t)
	if err := tt.Click("URL"); err != nil {
		t.Fatal(err)
	}
	tt.Key(ui.Cmd, ui.KeyA)
	before := d.value()
	tt.SetClipboard("https://plain.test/x")
	tt.Key(ui.Cmd, ui.KeyV)
	tt.Frame()

	want := before
	want.URL = "https://plain.test/x"
	if !reflect.DeepEqual(d.value(), want) {
		t.Errorf("plain paste changed more than the URL:\n%+v\nwant\n%+v", d.value(), want)
	}
	if len(a.notices) != 0 || a.urlPaste != nil {
		t.Errorf("plain paste raised a notice or a pending import: %+v / %+v", a.notices, a.urlPaste)
	}
}

func TestPasteBrokenCurlFallsBackToPlainText(t *testing.T) {
	a, d, tt := pastedURLApp(t)
	if err := tt.Click("URL"); err != nil {
		t.Fatal(err)
	}
	tt.Key(ui.Cmd, ui.KeyA)
	before := d.value()
	command := "curl https://example.test --data @private.txt"
	tt.SetClipboard(command)
	tt.Key(ui.Cmd, ui.KeyV)
	tt.Frame()

	want := before
	want.URL = command
	if !reflect.DeepEqual(d.value(), want) {
		t.Errorf("broken cURL paste changed more than the URL:\n%+v\nwant\n%+v", d.value(), want)
	}
	if len(a.notices) != 0 || a.urlPaste != nil {
		t.Errorf("broken cURL paste raised a notice or a pending import: %+v / %+v", a.notices, a.urlPaste)
	}
}
