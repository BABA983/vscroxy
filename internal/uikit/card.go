package uikit

import (
	"github.com/baba983/vscroxy/locales"
	"github.com/egoist/mygo/ui"
)

// PropertyRow is a workspace setting: a bold title with one muted description
// under it on the left, and the control on the right. A footnote under the
// control, such as "Default: Collection", is muted and right-aligned. Rows are
// separated by their parent's gap rather than a rule.
func PropertyRow(c *ui.Context, title, description string, control func(), footnote ...string) ui.Element {
	t := c.Theme()
	row := ui.Row(c).FillWidth().Gap(t.Space(SpaceXL)).AlignItems(ui.Start)
	row.Children(func() {
		head := ui.Column(c).Basis(0).Grow(1).MinWidth(0).Gap(t.Space(SpaceXS))
		head.Children(func() {
			BodyStrong(c, title)
			if description != "" {
				Hint(c, description)
			}
		})
		ui.Column(c).Shrink(0).AlignItems(ui.End).Gap(t.Space(SpaceXS)).Children(func() {
			control()
			if len(footnote) > 0 && footnote[0] != "" {
				Hint(c, footnote[0])
			}
		})
	})
	return row
}

// SettingsCard groups related rows in one canvas card, its legend above it.
// A row is at least 44 points high, with its title and one line of
// description on the left and its control on the right.
func SettingsCard(c *ui.Context, legend string, rows func()) ui.Element {
	t, p := c.Theme(), Colors(c.Theme())
	return ui.Column(c).FillWidth().Gap(t.Space(SpaceM)).Children(func() {
		if legend != "" {
			Label(c, legend)
		}
		ui.Column(c).FillWidth().Clip().Radius(RadiusCard).Background(p.Background).
			Border(HairlineWidth, p.Border).Children(rows)
	})
}

// SettingsCardRowWithReset is a SettingsCardRow that can show VS Code's
// modified marker and reset menu. Both slots keep their width whether the
// setting is modified or not, so toggling a control cannot move it under the
// pointer between a press and a release.
func SettingsCardRowWithReset(c *ui.Context, title, description string, modified bool, control func(), menu func(*ui.Menu), tip ...string) ui.Element {
	t := c.Theme()
	row := ui.Row(c).FillWidth().MinHeight(SettingsRowHeight).Padding(CardRowPaddingY, CardPaddingX).
		Gap(t.Space(SpaceM)).AlignItems(ui.Center).BorderWidth(0, 0, 1, 0).BorderColor(t.Border)
	// The marker sits on the card's own edge, out of the row's layout: every
	// row keeps the same inset whether or not its setting is modified, so
	// titles in one card line up.
	if modified {
		ui.Box(c).Absolute().Left(0).Top(t.Space(SpaceS)).Bottom(t.Space(SpaceS)).Width(3).Radius(2).
			Background(Colors(t).Info)
	}
	row.Children(func() {
		head := ui.Column(c).Grow(1).MinWidth(0).Gap(0)
		head.Children(func() {
			Body(c, title)
			if description != "" {
				Small(c, description).TextColor(t.TextMuted)
			}
		})
		if len(tip) > 0 && tip[0] != "" {
			Tip(c, head, tip[0])
		}
		if modified {
			// The reset menu sits before the control, so the control keeps the
			// card's inner edge whether or not the row is modified. An empty
			// slot after it left every other row short of that edge.
			MoreMenu(c, locales.SettingsSettingActions(title), menu)
		}
		if control != nil {
			control()
		}
	})
	return row
}

// SettingsCardBlockWithReset is SettingsCardRowWithReset for an editor that
// takes the card's full width; its marker and menu reserve their slots too.
func SettingsCardBlockWithReset(c *ui.Context, title, description string, modified bool, content func(), menu func(*ui.Menu), tip ...string) ui.Element {
	t := c.Theme()
	block := ui.Column(c).FillWidth().Padding(CardRowPaddingY, CardPaddingX).Gap(t.Space(SpaceM)).
		BorderWidth(0, 0, 1, 0).BorderColor(t.Border)
	if modified {
		ui.Box(c).Absolute().Left(0).Top(t.Space(SpaceS)).Bottom(t.Space(SpaceS)).Width(3).Radius(2).
			Background(Colors(t).Info)
	}
	block.Children(func() {
		ui.Row(c).FillWidth().Gap(t.Space(SpaceM)).Children(func() {
			head := ui.Column(c).Grow(1).MinWidth(0).Gap(0)
			head.Children(func() {
				if title != "" {
					Body(c, title)
				}
				if description != "" {
					Small(c, description).TextColor(t.TextMuted)
				}
			})
			if len(tip) > 0 && tip[0] != "" {
				Tip(c, head, tip[0])
			}
			if modified {
				MoreMenu(c, locales.SettingsSettingActions(title), menu)
			} else {
				ui.Box(c).Size(HeightRowAction, HeightRowAction).Shrink(0)
			}
		})
		if content != nil {
			content()
		}
	})
	return block
}

// SettingsCardRow is one row of a SettingsCard. The title is body text, the
// description one small muted line, and the control stays at the trailing
// edge, vertically centered. A longer tip can explain the description.
func SettingsCardRow(c *ui.Context, title, description string, control func(), tip ...string) ui.Element {
	t := c.Theme()
	row := ui.Row(c).FillWidth().MinHeight(SettingsRowHeight).Padding(CardRowPaddingY, CardPaddingX).
		Gap(t.Space(SpaceXL)).AlignItems(ui.Center).BorderWidth(0, 0, 1, 0).BorderColor(t.Border)
	row.Children(func() {
		head := ui.Column(c).Grow(1).MinWidth(0).Gap(0)
		head.Children(func() {
			Body(c, title)
			if description != "" {
				Small(c, description).TextColor(t.TextMuted)
			}
		})
		if len(tip) > 0 && tip[0] != "" {
			Tip(c, head, tip[0])
		}
		if control != nil {
			control()
		}
	})
	return row
}

// SettingsCardBlock is a row of a SettingsCard for content that does not fit
// beside its title: a multiline field, a list of editors or certificate
// details. The title and one description line sit above the content, and the
// block keeps the card's full width.
func SettingsCardBlock(c *ui.Context, title, description string, content func(), tip ...string) ui.Element {
	t := c.Theme()
	block := ui.Column(c).FillWidth().Padding(CardRowPaddingY, CardPaddingX).Gap(t.Space(SpaceM)).
		BorderWidth(0, 0, 1, 0).BorderColor(t.Border)
	block.Children(func() {
		if title != "" || description != "" {
			head := ui.Column(c).Gap(0)
			head.Children(func() {
				if title != "" {
					Body(c, title)
				}
				if description != "" {
					Small(c, description).TextColor(t.TextMuted)
				}
			})
			if len(tip) > 0 && tip[0] != "" {
				Tip(c, head, tip[0])
			}
		}
		if content != nil {
			content()
		}
	})
	return block
}
