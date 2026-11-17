package app

import (
	"cmp"
	"context"
	"errors"
	"io"
	"os"
	"strings"
	"time"

	"github.com/baba983/vscroxy/internal/inspect"
	"github.com/baba983/vscroxy/internal/uikit"
	"github.com/baba983/vscroxy/locales"
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
			j.result = &editor.State{Buffer: editor.NewBuffer(result.text), ReadOnly: true, SoftWrap: true, NoScrollPastEnd: true, Label: locales.InspectionResult()}
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
	saved, name               string
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
	uikit.Modal(c, &u.open, locales.QueryJson2(), func() {
		uikit.DialogColumn(c, uikit.DialogLarge).Height(min(570, height-70)).Children(func() {
			uikit.DialogTitle(c, locales.QueryJson2())
			ui.Row(c).Gap(t.Space(uikit.SpaceM)).AlignItems(ui.Center).Children(func() {
				before := u.dialect
				uikit.Select(c, &u.dialect, []string{"JSONPath", "jq"}).Label(locales.QueryDialect()).Width(uikit.WidthPicker)
				if before != u.dialect {
					u.job.stop()
					u.job.result, u.job.problem = nil, ""
				}
				uikit.Meta(c, locales.ReadOnlySnapshot1Mib()).Grow(1)
			})
			uikit.Meta(c, locales.JsonpathItemsPrice10Jq()).FillWidth()
			ui.Column(c).Height(76).Children(func() {
				before := u.expression
				authoredText(c, &u.expression, &u.expressionEditor, &u.seen, "JSON query expression", "")
				if before != u.expression {
					u.job.stop()
					u.job.result, u.job.problem = nil, ""
				}
			})
			if u.job.problem != "" {
				uikit.ErrorText(c, u.job.problem).FillWidth()
			}
			if u.job.result != nil {
				if u.job.result.Buffer.Len() == 0 {
					uikit.Hint(c, locales.NoResultsEmptyJqStream())
				}
				requestCodeEditor(c, u.job.result)
			} else {
				hint := locales.QueryPreviewHint()
				if u.job.answers != nil {
					hint = locales.RunningQuery()
				}
				uikit.Hint(c, hint).Grow(1).FillWidth()
			}
			uikit.Meta(c, locales.SafeSubsetNoModulesExternal()).FillWidth()
			uikit.DialogFooter(c).Children(func() {
				ui.Row(c).Disabled(u.job.result == nil).Children(func() {
					if uikit.SecondaryButton(c, locales.CopyResults()).Clicked() {
						c.Services().WriteClipboard(u.job.result.Buffer.String())
					}
				})
				ui.Spacer(c)
				if u.job.answers != nil && uikit.SecondaryButton(c, locales.CancelQuery()).Clicked() {
					u.job.stop()
					u.job.problem = "Query cancelled"
				}
				if uikit.SecondaryButton(c, locales.Close()).Clicked() {
					u.open = false
				}
				if uikit.PrimaryButton(c, locales.RunQuery()).Clicked() {
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

// jsonQueryPanel is the query bar beside the JSON tree: the expression, its
// results and saved queries, without leaving the tree.
func (a *app) jsonQueryPanel(c *ui.Context, side int) {
	f, ok := a.current()
	if !ok {
		return
	}
	u := &a.treeQuery
	source := capturedInspectionBody(f, side)
	if u.source != source {
		u.job.stop()
		u.source = source
		u.job.result, u.job.problem = nil, ""
	}
	u.job.poll(c)
	t := c.Theme()
	panel := ui.Column(c).MinHeight(0).Gap(t.Space(uikit.SpaceXS)).Padding(t.Space(uikit.SpaceXS), t.Space(uikit.SpaceM)).Background(uikit.Colors(t).Chrome)
	if a.paneWidth(c) >= 620 {
		panel.Width(300)
	} else {
		panel.FillWidth()
	}
	panel.Children(func() {
		ui.Row(c).Gap(t.Space(uikit.SpaceXS)).AlignItems(ui.Center).Children(func() {
			before := u.dialect
			uikit.Select(c, &u.dialect, []string{"JSONPath", "jq"}).Label(locales.QueryDialect()).Width(uikit.WidthNarrow)
			if before != u.dialect {
				u.job.stop()
				u.job.result, u.job.problem = nil, ""
			}
			ui.Spacer(c)
			if uikit.PrimaryButton(c, locales.RunQuery()).Clicked() {
				a.runTreeQuery(c)
			}
		})
		before := u.expression
		uikit.Input(c, &u.expression).Label(locales.Query()).Placeholder(locales.QueryPlaceholder()).Font(uikit.FontMono).FillWidth()
		if before != u.expression {
			u.job.stop()
			u.job.result, u.job.problem = nil, ""
		}
		ui.Row(c).Gap(t.Space(uikit.SpaceXS)).AlignItems(ui.Center).Children(func() {
			uikit.MenuSelect(c, cmp.Or(u.saved, locales.SavedQuery()), func(m *ui.Menu) {
				for _, q := range a.queries {
					if m.Item(q.Name).Radio(q.Name == u.saved).Chosen() {
						u.saved, u.dialect, u.expression = q.Name, q.Dialect, q.Expression
						u.job.stop()
						u.job.result, u.job.problem = nil, ""
					}
				}
			}).Grow(1)
			if uikit.SecondaryButton(c, locales.Delete()).Disabled(u.saved == "").Clicked() {
				a.deleteQuery(u.saved)
				u.saved = ""
			}
		})
		ui.Row(c).Gap(t.Space(uikit.SpaceXS)).AlignItems(ui.Center).Children(func() {
			uikit.Input(c, &u.name).Label(locales.QueryName()).Placeholder(locales.Name()).Width(uikit.WidthPicker)
			if uikit.SecondaryButton(c, locales.Save()).Disabled(u.name == "" || u.expression == "").Clicked() {
				if a.saveQuery(u.name, u.dialect, u.expression) {
					u.saved, u.name = strings.TrimSpace(u.name), ""
				}
			}
		})
		if a.queriesProblem != "" {
			uikit.ErrorText(c, a.queriesProblem).MaxLines(3)
		}
		if u.job.problem != "" {
			uikit.ErrorText(c, u.job.problem).MaxLines(3)
		}
		if u.job.result != nil {
			ui.Box(c).Grow(1).MinHeight(80).FillWidth().Children(func() { requestCodeEditor(c, u.job.result) })
		} else if u.job.answers != nil {
			uikit.Hint(c, locales.RunningQuery())
		} else {
			uikit.Hint(c, locales.RunToPreviewResults())
		}
	})
}

// runTreeQuery runs the inline panel's expression on its immutable body.
func (a *app) runTreeQuery(c *ui.Context) {
	u := &a.treeQuery
	if u.source.text == "" && u.source.file == "" && u.source.size == 0 {
		return
	}
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
