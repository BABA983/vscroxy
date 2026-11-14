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
	"github.com/baba983/vscroxy/locales"
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
	name := uniqueExampleName(names, strconv.Itoa(res.Status)+" "+http.StatusText(res.Status))
	return request.Example{
		ID: uuid.New().String(), Name: name, Status: res.Status,
		Headers: request.HeaderBlock(header), Body: body,
		Request: exampleRequest(sent),
	}, nil
}

// uniqueExampleName is base, or base with the first free number after it.
func uniqueExampleName(examples []request.Example, base string) string {
	name := base
	for n := 2; slices.ContainsFunc(examples, func(e request.Example) bool { return e.Name == name }); n++ {
		name = fmt.Sprintf("%s %d", base, n)
	}
	return name
}

// exampleRequest is the request an example keeps: as authored, and with its
// body only when the body is raw text.
func exampleRequest(sent savedRequest) request.ExampleRequest {
	sentBody := ""
	if sent.BodyMode == "" || sent.BodyMode == "Raw" {
		sentBody = sent.Body
	}
	return request.ExampleRequest{Method: cmp.Or(sent.Method, "GET"), URL: sent.URL, Headers: sent.Headers, Body: sentBody}
}

// blankExample is a new example with no response: status 200 and an empty
// body, under the request's Content-Type.
func (d *requestDocument) blankExample() request.Example {
	sent := d.value()
	header := http.Header{}
	if ct := request.ParseHeaderBlock(sent.Headers).Get("Content-Type"); ct != "" {
		header.Set("Content-Type", ct)
	}
	return request.Example{
		ID: uuid.New().String(), Name: uniqueExampleName(d.examples, "200 OK"), Status: 200,
		Headers: request.HeaderBlock(header), Request: exampleRequest(sent),
	}
}

// addExample puts e after the chosen example, or last, and chooses it.
func (d *requestDocument) addExample(e request.Example) {
	at := len(d.examples)
	if len(d.examples) > 0 {
		at = d.example + 1
	}
	d.examples = slices.Insert(d.examples, at, e)
	d.example, d.exampleEditor = at, nil
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
		a.fail(locales.SaveAsExample(), err)
		return
	}
	d.examples = append(d.examples, example)
	d.example, d.exampleEditor = len(d.examples)-1, nil
	if clean && d == a.requestDocument && a.saveComposed() {
		a.done(locales.SavedExample(example.Name))
		return
	}
	a.done(locales.AddedExample(example.Name))
}

// examplesEditor lists the request's examples and shows the chosen one.
func (a *app) examplesEditor(c *ui.Context, d *requestDocument, t *ui.Theme) {
	if len(d.examples) == 0 {
		uikit.EmptyState(c, locales.NoExamplesYet(), locales.SendTheRequestThenChoose(), func() {
			if uikit.SecondaryButton(c, locales.NewExample()).Clicked() {
				d.addExample(d.blankExample())
			}
		})
		return
	}
	d.example = min(max(d.example, 0), len(d.examples)-1)
	ui.Row(c).Grow(1).MinHeight(0).Gap(t.Space(3)).AlignItems(ui.Stretch).Children(func() {
		ui.Column(c).Width(168).BorderWidth(0, 1, 0, 0).BorderColor(t.Border).Children(func() {
			ui.Scroll(c).Grow(1).MinHeight(0).Padding(0, t.Space(2), 0, 0).Children(func() {
				for i, e := range d.examples {
					item := uikit.ToolbarButton(c.Key(e.ID), "Example: "+e.Name, e.Name, false).FillWidth()
					if i == d.example {
						item.Background(t.SurfaceHover)
					}
					if item.Children(func() {
						ui.Text(c, strconv.Itoa(e.Status)).Font(uikit.FontMono).FontSize(uikit.Scaled(uikit.TextMeta)).FontWeight(500).TextColor(uikit.StatusColor(e.Status, t)).Width(28)
						ui.Text(c, e.Name).FontSize(uikit.Scaled(uikit.TextBody)).SingleLine().Grow(1).MinWidth(0).TextColor(t.Text)
					}).Clicked() {
						d.example, d.exampleEditor = i, nil
					}
				}
			})
			ui.Column(c).Padding(t.Space(2)).Children(func() {
				if uikit.SecondaryButton(c, locales.NewExample()).FillWidth().Clicked() {
					d.addExample(d.blankExample())
				}
			})
		})
		e := &d.examples[d.example]
		ui.Column(c).Grow(1).MinWidth(0).MinHeight(0).Gap(t.Space(2)).Children(func() {
			ui.Form(c, func() {
				ui.Field(c, locales.ExampleName(), func() { uikit.Input(c, &e.Name).Label(locales.ExampleName()) })
			})
			// Beside the response the editor has half the width: the actions
			// take their own line rather than run off the pane.
			ui.Row(c).Gap(t.Space(2)).AlignItems(ui.Center).Wrap().Children(func() {
				// The status is typed; NumberField keeps values outside 100 to 599 out.
				status := float64(e.Status)
				uikit.NumberField(c, &d.exampleStatusText, &status, 100, 599, "", locales.Status(), "", nil)
				e.Status = int(status)
				uikit.Meta(c, http.StatusText(e.Status))
				uikit.Meta(c, humanBytes(len(e.Body))).FontFeatures("tnum")
				ui.Spacer(c)
				if uikit.Segmented(c, &d.exampleView, locales.Body(), locales.RequestHeadersLabel(), locales.Request()).Changed() {
					d.exampleEditor = nil
				}
				if uikit.Tip(c, uikit.SecondaryButton(c, locales.Mock()).Label(locales.MockThisExample()), locales.AnswerMatchingRequestsWithThis()).Clicked() {
					a.mockExample(d.value().Request, *e)
				}
				if uikit.SecondaryButton(c, locales.Duplicate()).Label(locales.DuplicateExample()).Clicked() {
					dup := *e
					dup.ID, dup.Name = uuid.New().String(), uniqueExampleName(d.examples, e.Name)
					d.addExample(dup)
				}
				if uikit.SecondaryButton(c, locales.Delete()).Label(locales.DeleteExample()).Clicked() {
					d.examples = slices.Delete(d.examples, d.example, d.example+1)
					d.exampleEditor = nil
				}
			})
			// An action above may have chosen another example; the next frame
			// shows it, and this one must not edit the old pointer.
			if d.example >= len(d.examples) || d.examples[d.example].ID != e.ID {
				return
			}
			uikit.Meta(c, e.Request.Method+" "+e.Request.URL).Font(uikit.FontMono).SingleLine().Selectable()
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
				d.exampleEditor = &editor.State{Buffer: buf, NoScrollPastEnd: true, SoftWrap: true, Label: "Example " + e.Name}
				d.exampleShown = text
			}
			// The body and headers are edited in place, and an edit is written
			// back to the example, so the draft is dirty and Save keeps it. The
			// request view is what was sent, so it stays read-only.
			d.exampleEditor.ReadOnly = d.exampleView == 2
			requestCodeEditor(c, d.exampleEditor)
			if current := d.exampleEditor.Buffer.String(); d.exampleView != 2 && current != d.exampleShown {
				d.exampleShown = current
				if d.exampleView == 1 {
					e.Headers = current
				} else {
					e.Body = current
				}
			}
		})
	})
}
