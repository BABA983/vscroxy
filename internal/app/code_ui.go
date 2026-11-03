package app

import (
	"github.com/baba983/vscroxy/internal/uikit"
	"slices"

	"github.com/baba983/vscroxy/internal/request"
	"github.com/egoist/mygo/ui"
	"github.com/egoist/mygo/ui/editor"
)

// codeDialog shows the draft as code in one language at a time.
type codeDialog struct {
	open     bool
	language string
	problem  string
	view     *editor.State
}

// codeLanguages are cURL, which curl.go writes, then the snippet languages.
var codeLanguages = append([]string{"cURL", "cURL (CMD)"}, request.SnippetLanguages...)

// draftPrepared is the draft as cURL and code show it: its own or inherited
// auth and encoded body, with {{variables}} left as they are and no script
// run, so no secret is expanded into what is copied.
func (a *app) draftPrepared(d *requestDocument) (*request.Prepared, error) {
	r := d.draft()
	r.Auth, _ = a.inheritance(r.Collection).InheritAuth(r.Auth)
	if r.Auth.Type == request.AuthOAuth2 {
		// The token this session holds, as a send would use it.
		if in, cfg, err := a.oauthConfig(r.Auth, r.Collection); err == nil {
			tok, _ := in.Tokens.Peek(cfg)
			r.Auth.Token, r.Auth.OAuth2.HeaderPrefix = tok.Access, cfg.HeaderPrefix
		}
	}
	return r.Prepare(nil)
}

func (a *app) openCode() {
	a.code = codeDialog{open: true, language: a.code.language}
	if !slices.Contains(codeLanguages, a.code.language) {
		a.code.language = codeLanguages[0]
	}
}

// codeText is the draft in the dialog's language.
func (a *app) codeText(d *requestDocument) (string, error) {
	req, err := a.draftPrepared(d)
	if err != nil {
		return "", err
	}
	return requestCopyText(flow{method: req.Method, url: req.URL, reqHeader: req.Header, reqBody: req.Body}, a.code.language)
}

func (a *app) codeModal(c *ui.Context, t *ui.Theme) {
	s, d := &a.code, a.requestDocument
	if !s.open || d == nil {
		s.open = false
		return
	}
	if s.view == nil {
		text, err := a.codeText(d)
		s.problem = ""
		if err != nil {
			s.problem = err.Error()
		}
		buf := editor.NewBuffer(text)
		if language := codeLanguage(s.language); language != "" {
			buf.SetLanguage(editor.LanguageNamed(language))
		}
		s.view = &editor.State{Buffer: buf, ReadOnly: true, NoScrollPastEnd: true, SoftWrap: true, Label: "Code: " + s.language}
	}
	_, h := c.Size()
	uikit.Modal(c, &s.open, func() {
		uikit.DialogColumn(c, uikit.DialogLarge).Height(min(520, h-80)).Children(func() {
			ui.Row(c).Gap(t.Space(2)).Wrap().AlignItems(ui.Center).Children(func() {
				uikit.DialogTitle(c, "Code").Grow(1)
				if uikit.Select(c, &s.language, codeLanguages).Label("Code language").Width(200).Changed() {
					s.view = nil
				}
			})
			ui.Scroll(c).MinHeight(0).Shrink(1).MaxHeight(100).Gap(t.Space(2)).Children(func() {
				uikit.Hint(c, "{{variables}} stay as written and scripts do not run; replace them before running the code.")
				if s.language == "JavaScript (fetch)" {
					uikit.Hint(c, "Browser fetch follows browser cookie, forbidden-header and CORS policies. Use Node.js or cURL outside the browser.")
				}
				if s.language == "cURL (CMD)" {
					uikit.Hint(c, "Paste into interactive Windows CMD, not PowerShell or a .bat file. ASCII only; use Bash or PowerShell for Unicode.")
				}
				if s.problem != "" {
					ui.Text(c, s.problem).FontSize(uikit.TextBody).TextColor(t.Danger)
				}
			})
			if s.view != nil {
				requestCodeEditor(c, s.view)
			}
			uikit.DialogFooter(c).Children(func() {
				if uikit.SecondaryButton(c, "Close").Clicked() {
					s.open = false
				}
				if uikit.PrimaryButton(c, "Copy").Disabled(s.problem != "").Clicked() {
					c.Services().WriteClipboard(s.view.Buffer.String())
					a.done("Copied " + s.language + " code; review credentials before sharing")
				}
			})
		})
	})
}

// codeLanguage is the highlighter for a code language, if the editor has one.
func codeLanguage(language string) string {
	switch language {
	case "JavaScript (fetch)", "Node.js (fetch)", "Node.js (axios)":
		return "JavaScript"
	case "Python (requests)":
		return "Python"
	case "Go (net/http)":
		return "Go"
	case "cURL", "cURL (CMD)", "HTTPie":
		return "Shell"
	}
	return ""
}
