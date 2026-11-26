package app

import (
	"encoding/json"
	"slices"
	"strconv"
	"strings"

	"github.com/baba983/vscroxy/internal/capture"
	"github.com/baba983/vscroxy/internal/store"
	"github.com/baba983/vscroxy/internal/uikit"
	"github.com/baba983/vscroxy/locales"
	"github.com/egoist/mygo/ui"
)

// bpTemplate is a message saved at a breakpoint to apply at a later pause in
// the same phase. It never holds the URL: a request template replaces the
// paused request's method, headers and body, a response template the status,
// headers and body.
type bpTemplate struct {
	Name    string  `json:"name"`
	Phase   string  `json:"phase"` // "request" or "response"
	Method  string  `json:"method,omitempty"`
	Status  int     `json:"status,omitempty"`
	Headers string  `json:"headers"`        // as the dialog edits them, one Name: Value per line
	Body    *string `json:"body,omitempty"` // nil leaves the paused body as it is
}

func phaseName(p capture.Phase) string {
	if p == capture.PhaseResponse {
		return "response"
	}
	return "request"
}

// phaseLabel is the phase as the screen says it.
func phaseLabel(p capture.Phase) string {
	if p == capture.PhaseResponse {
		return locales.SideResponse()
	}
	return locales.SideRequest()
}

// readBreakTemplates reads the templates file. One that does not parse stays
// read-only.
func (a *app) readBreakTemplates() ([]bpTemplate, error) {
	f := a.file(a.bpTemplatesPath)
	data, err := f.Read()
	if err != nil || len(data) == 0 {
		return nil, err
	}
	var doc struct {
		Templates []bpTemplate `json:"templates"`
	}
	if err := json.Unmarshal(data, &doc); err != nil {
		f.Fail(err)
		return nil, err
	}
	return doc.Templates, nil
}

// writeBreakTemplates saves list and, once it is on disk, keeps it.
func (a *app) writeBreakTemplates(list []bpTemplate) error {
	data, err := json.MarshalIndent(struct {
		Version   int          `json:"version"`
		Templates []bpTemplate `json:"templates"`
	}{store.Version, list}, "", "  ")
	if err != nil {
		return err
	}
	if err := a.file(a.bpTemplatesPath).Write(append(data, '\n')); err != nil {
		return err
	}
	a.bpTemplates = list
	return nil
}

// saveBreakTemplate keeps the paused message as edited, replacing a template
// of the same name and phase.
func (a *app) saveBreakTemplate(name string) {
	t := bpTemplate{Name: name, Phase: phaseName(a.bp.Phase), Headers: a.bpHeaders}
	if a.bp.Phase == capture.PhaseResponse {
		t.Status, _ = strconv.Atoi(strings.TrimSpace(a.bpStatus))
	} else {
		t.Method = a.bpMethod
	}
	if !a.bp.BodyReadOnly {
		body := a.bpBody
		t.Body = &body
	}
	list := slices.Clone(a.bpTemplates)
	if i := slices.IndexFunc(list, func(o bpTemplate) bool { return o.Name == name && o.Phase == t.Phase }); i >= 0 {
		list[i] = t
	} else {
		list = append(list, t)
	}
	if err := a.writeBreakTemplates(list); err != nil {
		a.bpNote = locales.CouldNotSaveTemplateWith(err.Error())
		a.fail(locales.BreakpointTemplate(), err)
		return
	}
	a.bpSaving, a.bpTemplate = false, name
	a.bpNote = ""
}

func (a *app) deleteBreakTemplate(t bpTemplate) {
	list := slices.DeleteFunc(slices.Clone(a.bpTemplates), func(o bpTemplate) bool { return o.Name == t.Name && o.Phase == t.Phase })
	if err := a.writeBreakTemplates(list); err != nil {
		a.bpNote = locales.CouldNotDeleteTemplateWith(err.Error())
		a.fail(locales.BreakpointTemplate(), err)
		return
	}
	if a.bpTemplate == t.Name {
		a.bpTemplate = ""
	}
}

// applyBreakTemplate fills the dialog with t; the pause continues only when
// the person chooses to.
func (a *app) applyBreakTemplate(t bpTemplate) {
	if a.bp.Phase == capture.PhaseResponse {
		a.bpStatus = strconv.Itoa(t.Status)
	} else {
		a.bpMethod = t.Method
	}
	a.bpHeaders = t.Headers
	a.bpNote = ""
	if t.Body != nil {
		if a.bp.BodyReadOnly {
			a.bpNote = locales.ThisBodyCannotBeEdited()
		} else {
			a.bpBody = *t.Body
		}
	}
	a.bpTemplate = t.Name
}

// breakTemplates is the dialog's line of template controls. With nothing
// saved for this phase it is one quiet button.
func (a *app) breakTemplates(c *ui.Context, t *ui.Theme, valid bool) {
	phase := phaseLabel(a.bp.Phase)
	var mine []bpTemplate
	for _, o := range a.bpTemplates {
		if o.Phase == phase {
			mine = append(mine, o)
		}
	}
	ui.Row(c).Gap(t.Space(uikit.SpaceM)).AlignItems(ui.Center).Children(func() {
		if a.bpSaving {
			input := uikit.Input(c, &a.bpName).Width(uikit.WidthFieldWide).Label(locales.TemplateName()).Placeholder(locales.TemplateName())
			name := strings.TrimSpace(a.bpName)
			save := uikit.CompactButton(c, locales.Save()).Disabled(name == "" || !valid)
			if save.Clicked() || (input.Submitted() && name != "" && valid) {
				a.saveBreakTemplate(name)
			}
			if uikit.CompactButton(c, locales.Cancel()).Clicked() {
				a.bpSaving = false
			}
			return
		}
		if len(mine) > 0 {
			current := locales.NoTemplate()
			if a.bpTemplate != "" {
				current = locales.TemplateNamed(a.bpTemplate)
			}
			uikit.Tip(c, uikit.MenuSelect(c, current, func(m *ui.Menu) {
				for _, o := range mine {
					if m.Item(o.Name).Radio(a.bpTemplate == o.Name).Chosen() {
						a.applyBreakTemplate(o)
					}
				}
				m.Separator()
				m.Submenu(locales.Delete(), func(m *ui.Menu) {
					for _, o := range mine {
						if m.Item(o.Name).Chosen() {
							a.deleteBreakTemplate(o)
						}
					}
				})
			}).Height(uikit.HeightRow).Label(locales.BreakpointTemplate()), locales.FillThisWithSaved(phase))
		}
		save := uikit.CompactButton(c, locales.SaveAsTemplate()).Disabled(!valid)
		uikit.Tip(c, save, locales.KeepThisAsEdited(phase))
		if save.Clicked() {
			a.bpSaving, a.bpName = true, a.bpTemplate
		}
	})
}
