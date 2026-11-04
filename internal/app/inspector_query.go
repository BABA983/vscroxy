package app

import (
	"context"
	"errors"
	"io"
	"os"
	"time"

	"github.com/baba983/vscroxy/internal/inspect"
	"github.com/baba983/vscroxy/internal/uikit"
	"github.com/egoist/mygo/ui"
	"github.com/egoist/mygo/ui/editor"
)

// An immutable source: workers never read app state, and never interpret a
// retained preview as a complete document. File access is also off-thread.
type inspectionBody struct {
	text, file, problem string
	size                int
}

func capturedInspectionBody(f flow, side int) inspectionBody {
	if side == 1 {
		return inspectionBody{f.reqBody, f.reqFile, f.reqError, f.reqSize}
	}
	return inspectionBody{f.resBody, f.resFile, f.resError, f.resSize}
}

func (b inspectionBody) read(ctx context.Context) (string, error) {
	if err := ctx.Err(); err != nil {
		return "", err
	}
	if b.problem != "" {
		return "", errors.New("body is incomplete; inspect Raw for the capture error")
	}
	if b.size > 1<<20 || len(b.text) > 1<<20 {
		return "", errors.New("body inspection is limited to 1 MiB")
	}
	text := b.text
	if b.file != "" {
		f, err := os.Open(b.file)
		if err != nil {
			return "", errors.New("retained body is unavailable")
		}
		defer f.Close()
		data, err := io.ReadAll(io.LimitReader(f, 1<<20+1))
		if err != nil {
			return "", errors.New("cannot read retained body")
		}
		text = string(data)
	}
	if len(text) > 1<<20 {
		return "", errors.New("body inspection is limited to 1 MiB")
	}
	if len(text) < b.size {
		return "", errors.New("body is incomplete; a preview cannot be queried as a full document")
	}
	return text, ctx.Err()
}

type inspectionResult struct {
	text string
	err  error
	body *inspect.BodyView
}

// Each run owns its answer channel. Replacing/closing a reader drops the old
// channel as well as canceling its work, so even an already queued answer
// cannot overwrite a later source or expression.
type bodyInspection struct {
	cancel  context.CancelFunc
	answers chan inspectionResult
	result  *editor.State
	problem string
	body    *inspect.BodyView
}

func (j *bodyInspection) stop() {
	if j.cancel != nil {
		j.cancel()
	}
	j.cancel, j.answers = nil, nil
}

func (j *bodyInspection) start(services ui.Services, read func(context.Context) inspectionResult) {
	j.stop()
	j.result, j.problem, j.body = nil, "", nil
	ctx, cancel := context.WithCancel(context.Background())
	answers := make(chan inspectionResult, 1)
	j.cancel, j.answers = cancel, answers
	go func() {
		result := read(ctx)
		select {
		case answers <- result:
			services.Invalidate()
		case <-ctx.Done():
		}
	}()
}

func (j *bodyInspection) poll(c *ui.Context) {
	if j.answers == nil {
		return
	}
	select {
	case result := <-j.answers:
		j.stop()
		if result.err != nil {
			j.problem = result.err.Error()
		} else {
			j.body = result.body
			j.result = &editor.State{Buffer: editor.NewBuffer(result.text), ReadOnly: true, SoftWrap: true, NoScrollPastEnd: true, Label: "Inspection result"}
		}
	default:
		c.After(50 * time.Millisecond)
	}
}

type jsonQueryInspector struct {
	open                      bool
	dialect, expression, seen string
	expressionEditor          *editor.State
	source                    inspectionBody
	job                       bodyInspection
}

func (a *app) openJSONQuery(source inspectionBody) {
	a.jsonQuery.job.stop()
	a.jsonQuery = jsonQueryInspector{open: true, dialect: "JSONPath", expression: "$", source: source}
}

func (a *app) jsonQueryModal(c *ui.Context) {
	u := &a.jsonQuery
	if !u.open {
		u.job.stop()
		return
	}
	u.job.poll(c)
	t := c.Theme()
	_, height := c.Size()
	uikit.Modal(c, &u.open, func() {
		uikit.DialogColumn(c, uikit.DialogLarge).Height(min(570, height-70)).Children(func() {
			uikit.DialogTitle(c, "Query JSON")
			ui.Row(c).Gap(t.Space(2)).AlignItems(ui.Center).Children(func() {
				before := u.dialect
				uikit.Select(c, &u.dialect, []string{"JSONPath", "jq"}).Label("Query dialect").Width(140)
				if before != u.dialect {
					u.job.stop()
					u.job.result, u.job.problem = nil, ""
				}
				uikit.Meta(c, "Read-only snapshot · 1 MiB input · bounded local query").Grow(1)
			})
			uikit.Meta(c, "JSONPath: $.items[?(@.price > 10)]  ·  jq: .items[] | select(.price > 10)").FillWidth()
			ui.Column(c).Height(76).Children(func() {
				before := u.expression
				authoredText(c, &u.expression, &u.expressionEditor, &u.seen, "JSON query expression", "")
				if before != u.expression {
					u.job.stop()
					u.job.result, u.job.problem = nil, ""
				}
			})
			if u.job.problem != "" {
				ui.Text(c, u.job.problem).FontSize(uikit.TextMeta).TextColor(t.Danger).FillWidth()
			}
			if u.job.result != nil {
				if u.job.result.Buffer.Len() == 0 {
					uikit.Hint(c, "No results (empty jq stream)")
				}
				requestCodeEditor(c, u.job.result)
			} else {
				hint := "Run to preview results. JSONPath returns an array; jq returns a stream of JSON values."
				if u.job.answers != nil {
					hint = "Running query…"
				}
				uikit.Hint(c, hint).Grow(1).FillWidth()
			}
			uikit.Meta(c, "Safe subset: no modules, external input, updates or definitions. See the guide for dialect limits.").FillWidth()
			uikit.DialogFooter(c).Children(func() {
				ui.Row(c).Disabled(u.job.result == nil).Children(func() {
					if uikit.SecondaryButton(c, "Copy results").Clicked() {
						c.Services().WriteClipboard(u.job.result.Buffer.String())
					}
				})
				ui.Spacer(c)
				if u.job.answers != nil && uikit.SecondaryButton(c, "Cancel query").Clicked() {
					u.job.stop()
					u.job.problem = "Query cancelled"
				}
				if uikit.SecondaryButton(c, "Close").Clicked() {
					u.open = false
				}
				if uikit.PrimaryButton(c, "Run query").Clicked() {
					source, dialect, expression := u.source, u.dialect, u.expression
					u.job.start(c.Services(), func(ctx context.Context) inspectionResult {
						input, err := source.read(ctx)
						if err != nil {
							return inspectionResult{err: err}
						}
						text, err := inspect.Query(ctx, dialect, expression, input)
						return inspectionResult{text: text, err: err}
					})
				}
			})
		})
	})
	if !u.open {
		u.job.stop()
	}
}
