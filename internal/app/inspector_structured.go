package app

import (
	"context"
	"fmt"
	"net/http"

	"github.com/baba983/vscroxy/internal/inspect"
	"github.com/baba983/vscroxy/internal/request"
	"github.com/baba983/vscroxy/internal/uikit"
	"github.com/baba983/vscroxy/locales"
	"github.com/egoist/mygo/ui"
)

// Each visible side/document owns its cache and its cancelable work. Nothing
// here formats or reads files during a frame, or writes to the source flow.
type structuredInspector struct {
	ready       bool
	source      inspectionBody
	contentType string
	job         bodyInspection
	list        ui.ListState
}

func (s *structuredInspector) stop() {
	s.job.stop()
	s.ready = false
}

func (s *structuredInspector) view(c *ui.Context, source inspectionBody, contentType string) {
	if !s.ready || s.source != source || s.contentType != contentType {
		s.source, s.contentType, s.ready, s.list = source, contentType, true, ui.ListState{}
		s.job.start(c.Services(), func(ctx context.Context) inspectionResult {
			data, err := source.read(ctx)
			if err != nil {
				return inspectionResult{err: err}
			}
			body, err := inspect.InspectBody(contentType, []byte(data))
			if err == nil {
				err = ctx.Err()
			}
			return inspectionResult{text: body.Text, body: &body, err: err}
		})
	}
	s.job.poll(c)
	t := c.Theme()
	ui.Row(c).Padding(t.Space(2), t.Space(3)).Gap(t.Space(2)).AlignItems(ui.Center).Children(func() {
		label := inspect.BodyFormat(contentType) + " · read-only inspection"
		if s.job.body != nil {
			switch s.job.body.Format {
			case "Form":
				label = fmt.Sprintf("%d form fields · original order", len(s.job.body.Fields))
			case "Multipart":
				label = fmt.Sprintf("%d parts · files are not previewed", len(s.job.body.Parts))
			}
		}
		uikit.Tip(c, uikit.Meta(c, label).Grow(1).SingleLine(), label)
		ui.Row(c).Disabled(s.job.result == nil).Children(func() {
			if uikit.SecondaryButton(c, locales.CopyView()).Clicked() {
				c.Services().WriteClipboard(s.job.result.Buffer.String())
			}
		})
	})
	if s.job.problem != "" {
		ui.Text(c, s.job.problem).FontSize(uikit.TextBody).TextColor(t.Warning).Padding(t.Space(3)).FillWidth()
		uikit.Hint(c, locales.ChoosePrettyRawOrHex()).Padding(t.Space(3))
		return
	}
	if s.job.body == nil {
		uikit.Hint(c, locales.InspectingRetainedBody()).Padding(t.Space(3))
		return
	}
	body := s.job.body
	switch body.Format {
	case "Form":
		cols := []ui.TableColumn{{Title: "#", Width: 36, Fixed: true}, {Title: locales.Name()}, {Title: locales.Value()}}
		ui.Table(c, &s.list, cols, len(body.Fields), func(row, col int) {
			field := body.Fields[row]
			value := fmt.Sprint(row + 1)
			raw := "Original entry order; duplicates are preserved"
			if col == 1 {
				value, raw = field.Key, "Raw name: "+field.RawKey
			} else if col == 2 {
				value, raw = field.Value, "Raw value: "+field.RawValue
				if value == "" {
					value = "(empty)"
					if !field.HasEquals {
						value = "(no =)"
					}
				}
			}
			uikit.Tip(c, uikit.Mono(c, clamp(value, 256)).SingleLine(), clamp(raw, 4096)).Selectable()
		}).Label(locales.FormFields()).Grow(1).MinHeight(0)
		if len(body.Fields) == 0 {
			uikit.Hint(c, locales.NoFormFields())
		}
	case "Multipart":
		ui.List(c, &s.list, len(body.Parts), func(i int) {
			part := body.Parts[i]
			ui.Column(c).Padding(t.Space(2), t.Space(3)).Gap(t.Space(1)).FillWidth().Children(func() {
				uikit.Body(c, locales.PartInfo(i+1, fmt.Sprintf("%q", part.Name), part.Size)).FillWidth()
				if part.File {
					uikit.Meta(c, locales.FilePart(fmt.Sprintf("%q", part.Filename))).FillWidth()
				}
				uikit.Meta(c, clamp(request.HeaderBlock(http.Header(part.Headers)), 4096)).Selectable().FillWidth()
				if part.Preview != nil {
					uikit.Mono(c, *part.Preview).Selectable().FillWidth()
					if part.PreviewTruncated {
						uikit.Meta(c, locales.TextPreviewLimitedTo4())
					}
				} else if !part.File {
					uikit.Hint(c, locales.BinaryEncodedOrNonUtf()).FillWidth()
				}
			})
			ui.Divider(c)
		}).Label(locales.MultipartParts()).Grow(1).MinHeight(0).FillWidth()
	default:
		requestCodeEditor(c, s.job.result)
	}
}
