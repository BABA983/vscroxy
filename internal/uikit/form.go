package uikit

import (
	"github.com/egoist/mygo/ui"
)

// FormRow is one field: its label, the control, an optional description and an
// optional error. It uses MyGo's Field, so the label names the control and the
// description and error are read with it; the error also marks the control
// invalid. A text field should use InputState(c, &value, err != "") for the
// visible danger edge, since the face is drawn by this kit rather than MyGo.
func FormRow(c *ui.Context, label, description, errText string, control func()) ui.Element {
	field := ui.Field(c, label, control)
	if description != "" {
		field.Description(description)
	}
	if errText != "" {
		field.Error(errText)
	}
	return field
}

// FormRowTip is FormRow with its explanation as a tip on the label rather
// than a line under the control: for a tall field whose description would
// otherwise sit below it, away from the name it explains. The label still
// names the control for assistive technology.
func FormRowTip(c *ui.Context, label, tip string, control func() ui.Element) ui.Element {
	t := c.Theme()
	return ui.Column(c).FillWidth().Gap(t.Space(SpaceXS)).Children(func() {
		name := Tip(c, ui.Text(c, label).FontSize(Scaled(TextBody)).TextColor(t.Text), tip)
		field := control()
		field.LabelledBy(name)
	})
}

// FormRowTall is FormRow for a control taller than one line, such as a code
// editor: its label sits on the control's top line instead of being centered
// on the whole box. It names the control, and binds the description and error
// directly, since MyGo's Field aligns a form row's first text line and a tall
// editor has none. Use it inside a ui.Form, beside FormRow fields.
func FormRowTall(c *ui.Context, label, description, errText string, control func() ui.Element) ui.Element {
	t := c.Theme()
	row := ui.Row(c).FillWidth().Gap(t.Space(SpaceL)).AlignItems(ui.Start)
	row.Children(func() {
		lab := ui.Text(c, label).Width(Scaled(formTallLabelWidth)).TextAlign(ui.End).TextColor(t.Text)
		col := ui.Column(c).Grow(1).MinWidth(0).Gap(t.Space(SpaceXS))
		col.Children(func() {
			ctrl := control()
			ctrl.LabelledBy(lab).Invalid(errText != "")
			if description != "" {
				ctrl.Description(description)
			}
			if errText != "" {
				// The error is drawn once, and named as the control's error for
				// assistive technology.
				ctrl.ErrorMessage(ui.Text(c, errText).FontSize(Scaled(TextMeta)).TextColor(t.Danger))
			} else if description != "" {
				Meta(c, description)
			}
		})
	})
	return row
}
