package app

import (
	"cmp"
	"encoding/json/jsontext"
	"fmt"
	"github.com/baba983/vscroxy/internal/uikit"
	"net/http"
	"os"
	"slices"
	"strconv"
	"unicode/utf8"
	"uuid"

	"github.com/baba983/vscroxy/internal/capture"
	"github.com/baba983/vscroxy/internal/request"
	"github.com/egoist/mygo/ui"
	"github.com/egoist/mygo/ui/editor"
)

// newExample keeps the response on screen with the request that got it, as
// authored: the draft that was sent, not its expanded values.
func newExample(sent savedRequest, res *capture.Flow, names []request.Example) (request.Example, error) {
	if res.ResponseBodyError != "" {
		return request.Example{}, fmt.Errorf("the response body is incomplete: %s", res.ResponseBodyError)
	}
	body := res.ResponseBody
	if res.ResponseBodySize > len(body) {
		if res.ResponseBodySize > request.MaxExampleBody || res.ResponseBodyFile == "" {
			return request.Example{}, fmt.Errorf("examples keep bodies up to %s", humanBytes(request.MaxExampleBody))
		}
		data, err := os.ReadFile(res.ResponseBodyFile)
		if err != nil {
			return request.Example{}, fmt.Errorf("the response body is unavailable: %w", err)
		}
		body = string(data)
	}
	if len(body) > request.MaxExampleBody {
		return request.Example{}, fmt.Errorf("examples keep bodies up to %s", humanBytes(request.MaxExampleBody))
	}
	if !utf8.ValidString(body) {
		return request.Example{}, fmt.Errorf("examples keep text bodies; this one is binary")
	}
	// Capture keeps the decoded body under the wire headers.
	header := res.ResponseHeader.Clone()
	header.Del("Content-Encoding")
	header.Del("Content-Length")
	capture.StripHopHeaders(header)
	name := strconv.Itoa(res.Status) + " " + http.StatusText(res.Status)
	for n, base := 2, name; slices.ContainsFunc(names, func(e request.Example) bool { return e.Name == name }); n++ {
		name = fmt.Sprintf("%s %d", base, n)
	}
	sentBody := ""
	if sent.BodyMode == "" || sent.BodyMode == "Raw" {
		sentBody = sent.Body
	}
	return request.Example{
		ID: uuid.New().String(), Name: name, Status: res.Status,
		Headers: request.HeaderBlock(header), Body: body,
		Request: request.ExampleRequest{Method: cmp.Or(sent.Method, "GET"), URL: sent.URL, Headers: sent.Headers, Body: sentBody},
	}, nil
}

// saveExample adds the response on screen to the draft's examples. A saved
// request with no other changes is saved with it at once, as Postman does;
// otherwise it is kept by the next Save, with the rest of the draft.
func (a *app) saveExample(d *requestDocument) {
	if d.response == nil {
		return
	}
	clean := d.savedKey != "" && !d.dirty()
	example, err := newExample(d.sent, d.response, d.examples)
	if err != nil {
		a.setMessage("Save as example: " + err.Error())
		return
	}
	d.examples = append(d.examples, example)
	d.example, d.exampleEditor = len(d.examples)-1, nil
	if clean && d == a.requestDocument && a.saveComposed() {
		a.setMessage("Saved example “" + example.Name + "”")
		return
	}
	a.setMessage("Added example “" + example.Name + "”; save the request to keep it")
}

// examplesEditor lists the request's examples and shows the chosen one.
func (a *app) examplesEditor(c *ui.Context, d *requestDocument, t *ui.Theme) {
	if len(d.examples) == 0 {
		uikit.EmptyState(c, "No examples yet", "Send the request, then choose Save as example beside the response.", nil)
		return
	}
	d.example = min(max(d.example, 0), len(d.examples)-1)
	ui.Row(c).Grow(1).MinHeight(0).Gap(t.Space(3)).AlignItems(ui.Stretch).Children(func() {
		ui.Scroll(c).Width(168).BorderWidth(0, 1, 0, 0).BorderColor(t.Border).Padding(0, t.Space(2), 0, 0).Children(func() {
			for i, e := range d.examples {
				item := uikit.ToolbarButton(c.Key(e.ID), "Example: "+e.Name, e.Name, false).FillWidth()
				if i == d.example {
					item.Background(t.SurfaceHover)
				}
				if item.Children(func() {
					ui.Text(c, strconv.Itoa(e.Status)).Font(uikit.FontMono).FontSize(uikit.TextMeta).Bold().TextColor(uikit.StatusColor(e.Status, t)).Width(28)
					ui.Text(c, e.Name).FontSize(uikit.TextBody).SingleLine().Grow(1).MinWidth(0).TextColor(t.Text)
				}).Clicked() {
					d.example, d.exampleEditor = i, nil
				}
			}
		})
		e := &d.examples[d.example]
		ui.Column(c).Grow(1).MinWidth(0).MinHeight(0).Gap(t.Space(2)).Children(func() {
			ui.Form(c, func() {
				ui.Field(c, "Example name", func() { uikit.Input(c, &e.Name).Label("Example name") })
			})
			ui.Row(c).Gap(t.Space(2)).AlignItems(ui.Center).Children(func() {
				ui.Text(c, fmt.Sprintf("%d %s", e.Status, http.StatusText(e.Status))).FontSize(uikit.TextBody).Bold().TextColor(uikit.StatusColor(e.Status, t))
				ui.Text(c, humanBytes(len(e.Body))).FontSize(uikit.TextMeta).FontFeatures("tnum").TextColor(t.TextMuted)
				ui.Spacer(c)
				if uikit.Segmented(c, &d.exampleView, "Body", "Headers", "Request").Changed() {
					d.exampleEditor = nil
				}
				if uikit.SecondaryButton(c, "Delete").Label("Delete example").Clicked() {
					d.examples = slices.Delete(d.examples, d.example, d.example+1)
					d.exampleEditor = nil
				}
			})
			if d.example >= len(d.examples) {
				return
			}
			ui.Text(c, e.Request.Method+" "+e.Request.URL).FontSize(uikit.TextMeta).Font(uikit.FontMono).TextColor(t.TextMuted).SingleLine().Selectable()
			if d.exampleEditor == nil {
				text, language := e.Body, ""
				switch d.exampleView {
				case 1:
					text = e.Headers
				case 2:
					text = e.Request.Method + " " + e.Request.URL + "\n" + e.Request.Headers
					if e.Request.Body != "" {
						text += "\n" + e.Request.Body
					}
				default:
					if value := jsontext.Value(text); value.Indent(jsontext.WithIndent("  ")) == nil {
						text, language = string(value), "json"
					}
				}
				buf := editor.NewBuffer(text)
				if language != "" {
					buf.SetLanguage(editor.LanguageNamed(language))
				}
				d.exampleEditor = &editor.State{Buffer: buf, ReadOnly: true, NoScrollPastEnd: true, SoftWrap: true, Label: "Example " + e.Name}
			}
			requestCodeEditor(c, d.exampleEditor)
		})
	})
}
