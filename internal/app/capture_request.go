package app

import (
	"fmt"
	"net/url"
	"slices"
	"strings"
	"unicode/utf8"
	"uuid"

	"github.com/egoist/mygo/ui"
	"github.com/egoist/mygo/ui/editor"
)

// A promotion freezes one conversion, so preview and confirmation cannot read
// different spill bytes. The bounded source snapshot is evidence, not payload.
type capturePromotion struct {
	open, save, parameterize bool
	request                  savedRequest
	view                     *captureView
	problem, origin          string
	changes                  []string
	reader                   *editor.State
}

func (a *app) previewCapture(f flow, save bool) {
	p := capturePromotion{open: true, save: save, view: a.views.activeView()}
	r, err := f.requestDraft()
	if err != nil {
		p.problem = err.Error()
	} else {
		r.Source = newDebugSource(f)
		if save {
			r.Name = uniqueRequestName(a.requests, strings.TrimSpace(f.method+" "+f.host+f.path))
		}
		p.request = r
		h := f.reqHeader
		if h == nil {
			h = parseHeaderBlock(f.reqHeaders)
		}
		for _, name := range []string{"Content-Encoding", "Content-Length", "Transfer-Encoding"} {
			if len(h.Values(name)) > 0 {
				p.changes = append(p.changes, "Remove "+name+"; body is retained decoded text, framing is regenerated at send.")
			}
		}
		if u, err := url.Parse(r.URL); err == nil && u.Host != "" && u.User == nil {
			p.origin = u.Scheme + "://" + u.Host
		}
	}
	a.promotion = p
}

func (p *capturePromotion) value() savedRequest {
	r := p.request
	if p.parameterize && p.origin != "" {
		r.URL = "{{base_url}}" + strings.TrimPrefix(r.URL, p.origin)
	}
	return r
}

func (a *app) confirmCaptureRequest() {
	p := &a.promotion
	if !p.open || p.problem != "" {
		return
	}
	r := p.value()
	if p.save {
		r.ID = uuid.New().String()
		list := upsertRequest(slices.Clone(a.requests), r)
		if err := a.persistRequests(list); err != nil {
			p.problem = "Save failed: " + err.Error()
			return
		}
		a.requests = list
		a.setMessage("Saved " + r.Name + "; not sent")
	} else {
		a.views.capture(a)
		a.addDocument(r, "")
		a.sourceView = p.view
	}
	p.open = false
}

func (a *app) captureRequestModal(c *ui.Context, t *ui.Theme) {
	p := &a.promotion
	if !p.open {
		return
	}
	width, height := c.Size()
	appModal(c, &p.open, func() {
		ui.Column(c).Width(min(780, width-64)).Height(min(610, height-80)).Gap(10).Children(func() {
			dialogTitle(c, "Capture → Request")
			ui.Text(c, "Review before creating an editable asset. Original capture stays unchanged; confirmation never sends. This is not raw wire replay or automatic redaction.").FontSize(11).TextColor(t.TextMuted)
			if p.problem != "" {
				ui.Text(c, "Conversion blocked: "+p.problem).FontSize(12).TextColor(t.Danger).Selectable()
				ui.Spacer(c)
			} else {
				ui.Text(c, p.request.Method+" "+p.value().URL).FontSize(12).Bold().Selectable()
				ui.Text(c, fmt.Sprintf("%d complete UTF-8 body bytes · duplicate headers and raw query preserved", len(p.request.Body))).FontSize(11).TextColor(t.TextMuted)
				for _, change := range p.changes {
					ui.Text(c, change).FontSize(11).TextColor(t.Warning)
				}
				if p.origin != "" {
					ui.Checkbox(c, &p.parameterize, "Replace origin with {{base_url}}")
					ui.Text(c, "Optional: set base_url to "+p.origin+" in the intended environment. No environment is changed automatically.").FontSize(11).TextColor(t.TextMuted)
				}
				ui.Text(c, "Review tokens, cookies and IDs manually. Preview may show credentials in plaintext; no secrets are detected or removed automatically.").FontSize(11).TextColor(t.TextMuted)
				if p.reader == nil {
					body := p.request.Body[:min(len(p.request.Body), 16<<10)]
					for !utf8.ValidString(body) {
						body = body[:len(body)-1]
					}
					text := p.request.Headers + "\n\n" + body
					p.reader = &editor.State{Buffer: editor.NewBuffer(text), ReadOnly: true, NoScrollPastEnd: true, SoftWrap: true, Label: "Converted request preview"}
				}
				if len(p.request.Body) > 16<<10 {
					ui.Text(c, "Preview shows the first 16 KiB only; the draft keeps the complete body.").FontSize(11).TextColor(t.Warning)
				}
				editor.View(c, p.reader).Grow(1)
			}
			ui.Row(c).Gap(8).Justify(ui.End).Children(func() {
				if ui.Button(c, "Cancel").Clicked() {
					p.open = false
				}
				label := "Create draft"
				if p.save {
					label = "Save request"
				}
				if ui.PrimaryButton(c, label).Disabled(p.problem != "").Clicked() {
					a.confirmCaptureRequest()
				}
			})
		})
	})
}
