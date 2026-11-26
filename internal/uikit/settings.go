package uikit

import (
	"sync"

	"github.com/egoist/mygo/ui"
)

// SettingsPage is one category of a settings window, as in macOS's System
// Settings: its title, a sentence on what it holds, and a form of
// SettingsGroups, whose labels line up across the page. It keeps a reading
// width however wide the window is.
func SettingsPage(c *ui.Context, title, about string, groups func()) ui.Element {
	t := c.Theme()
	return ui.Column(c).FillWidth().MaxWidth(640).Gap(t.Space(SpaceXXL)).Children(func() {
		ui.Column(c).Gap(t.Space(SpaceXS)).Children(func() {
			Heading(c, title)
			if about != "" {
				Hint(c, about)
			}
		})
		ui.Form(c, groups).Gap(t.Space(SpaceXL))
	})
}

// SettingsCardPage is the looser Settings skeleton: a page 760 points wide
// and left-aligned, a title and one sentence, then groups of SettingsCards.
// It is for the settings, first-run and wizard surfaces; the workspace keeps
// its own density.
func SettingsCardPage(c *ui.Context, title, about string, groups func()) ui.Element {
	t := c.Theme()
	return ui.Column(c).FillWidth().MaxWidth(SettingsPageWidth).Gap(t.Space(SpaceXL)).Children(func() {
		ui.Column(c).Gap(t.Space(SpaceXS)).Children(func() {
			Display(c, title)
			if about != "" {
				Hint(c, about)
			}
		})
		ui.Form(c, groups).Gap(t.Space(SpaceXL))
	})
}

// settingsGroups and settingsWidths remember the current group and the widest
// label seen in it. The first frame uses the old 112-point column; measuring a
// wider label asks for one more frame, and the next frame uses the new width.
var settingsGroups sync.Map // *ui.Context -> string
var settingsWidths sync.Map // *ui.Context -> map[string]float32
func settingsGroupName(c *ui.Context) string {
	name, _ := settingsGroups.Load(c)
	group, _ := name.(string)
	return group
}
func settingsLabelWidth(c *ui.Context, label string) float32 {
	group := settingsGroupName(c)
	if group == "" {
		return Scaled(SettingsLabelWidth)
	}
	width := float32(0)
	if stored, ok := settingsWidths.Load(c); ok {
		if widths, ok := stored.(map[string]float32); ok {
			width = widths[group]
		}
	}
	if width == 0 {
		width = Scaled(SettingsLabelWidth)
	}
	measured, _ := c.MeasureText(0, ui.Span{Text: label, Size: Scaled(TextBody)})
	want := min(max(measured+Scaled(settingsLabelSlack), Scaled(SettingsLabelWidth)), Scaled(SettingsLabelMax))
	if want > width {
		settingsWidths.Store(c, map[string]float32{group: want})
		c.Invalidate()
	}
	return width
}

// SettingsGroup is a group of related fields under its legend: a heading and
// a rule, not a card, so the page reads as one canvas rather than a stack of
// boxes.
func SettingsGroup(c *ui.Context, legend string, fields func()) ui.Element {
	t := c.Theme()
	previous := settingsGroupName(c)
	settingsGroups.Store(c, legend)
	defer func() {
		if previous == "" {
			settingsGroups.Delete(c)
		} else {
			settingsGroups.Store(c, previous)
		}
	}()
	return ui.Fieldset(c, legend, fields).Margin(0).Padding(0, 0, t.Space(SpaceXL), 0).
		BorderWidth(0, 0, 1, 0).BorderColor(t.Border)
}

// first returns the first value, or fallback when there is none.
func first(values []string, fallback string) string {
	if len(values) > 0 && values[0] != "" {
		return values[0]
	}
	return fallback
}

// SettingsRow is one setting as a row: its label in a fixed left column, its
// control beside it, and a hint under the control when it needs one. The
// label column is Scaled so it follows the text size, the label is centered
// on a single-line control, and the hint stays a meta line within the
// control's width instead of running across the page.
func SettingsRow(c *ui.Context, label string, control func() ui.Element, hint string, tip ...string) ui.Element {
	return settingsRow(c, label, control, hint, first(tip, hint), false)
}

// SettingsRowMultiline is SettingsRow for a control taller than one line,
// such as a text area: the label lines up with the control's first line.
func SettingsRowMultiline(c *ui.Context, label string, control func() ui.Element, hint string) ui.Element {
	return settingsRow(c, label, control, hint, hint, true)
}
func settingsRow(c *ui.Context, label string, control func() ui.Element, hint, tip string, multiline bool) ui.Element {
	t := c.Theme()
	align := ui.Center
	if multiline {
		align = ui.Start
	}
	if hint == "" {
		return settingsRowBody(c, label, control, "", align)
	}
	return ui.Column(c).FillWidth().Gap(t.Space(SpaceXS)).Children(func() {
		settingsRowBody(c, label, control, hint, align)
		ui.Row(c).Children(func() {
			ui.Box(c).Width(Scaled(SettingsLabelWidth) + t.Space(SpaceXL)).Shrink(0)
			Tip(c, ui.Text(c, hint).FontSize(Scaled(TextMeta)).TextColor(t.TextMuted).
				MaxWidth(Scaled(SettingsHintWidth)).LineHeight(LineProse), tip)
		})
	})
}

// settingsRowBody lines the label up with the control. The hint is the
// control's description for assistive technology; the hint line is drawn
// under the row by settingsRow. The label is not bound to the control with
// LabelledBy: that would replace the control's own name, which the settings
// screens rely on.
func settingsRowBody(c *ui.Context, label string, control func() ui.Element, hint string, align ui.Align) ui.Element {
	t := c.Theme()
	labelWidth := settingsLabelWidth(c, label)
	row := ui.Row(c).FillWidth().Gap(t.Space(SpaceXL)).AlignItems(align)
	row.Children(func() {
		if label == "" {
			ui.Box(c).Width(labelWidth).Shrink(0)
		} else {
			ui.Text(c, label).FontSize(Scaled(TextBody)).TextColor(t.Text).Width(labelWidth).Shrink(0).MaxLines(2)
		}
		ui.Column(c).Grow(1).MinWidth(0).Children(func() {
			control().Description(hint)
		})
	})
	return row
}
