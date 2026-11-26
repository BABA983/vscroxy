package uikit

import (
	"github.com/egoist/mygo/ui"
)

// Dialog content uses three width tiers inside Modal's shared padding.
// Callers choose the height; long content scrolls without moving the footer.
func DialogColumn(c *ui.Context, width float32) ui.Element {
	w, h := c.Size()
	// The panel keeps 16 points at each edge, then its own 24-point inset.
	innerW := max(0, w-2*DialogPadding-2*WindowMargin)
	innerH := max(0, h-2*DialogPadding-2*WindowMargin)
	return ui.Column(c).Width(min(width, innerW)).MaxHeight(innerH).MinHeight(0).Gap(DialogGap)
}

// DialogFooter holds a dialog's actions at its end. It has no rule above it:
// the dialog's own gap and the actions' inset carry the separation, as they do
// between every other pair of groups on the surface.
func DialogFooter(c *ui.Context) ui.Element {
	t := c.Theme()
	return ui.Row(c).FillWidth().Gap(t.Space(SpaceM)).Wrap().AlignItems(ui.Center).Justify(ui.End).
		Padding(t.Space(SpaceL), 0, 0, 0)
}

// ModalFlush is a modal with no inset: its content owns the whole panel.
// It is for a command palette, whose input and rows run to the panel's edge;
// the title still names the dialog for assistive technology.
func ModalFlush(c *ui.Context, open *bool, title string, content func()) ui.Element {
	t, p := c.Theme(), Colors(c.Theme())
	width, height := c.Size()
	dialog := ui.DialogBase(c, open, func(backdrop, panel ui.Element) {
		backdrop.Background(p.Scrim)
		panel.Label(title).Padding(0).Radius(RadiusCard).Background(p.Background).Clip().
			InsetShadow(0, 0, 0, HairlineWidth, p.Border).
			MaxWidth(max(0, width-2*WindowMargin)).MaxHeight(max(0, height-2*WindowMargin))
		ShadowPopover.Apply(panel, t.Dark)
		seen := ui.Local(panel, "modal-in", func() bool { return false })
		if !*seen {
			panel.Jump("modal-in", 0)
			*seen = true
		}

		content()
	})
	if dialog.Dismissed() {
		c.Invalidate()
	}
	return dialog
}

// DialogBase retains the library's modal focus and dismissal rules; only
// the shared desktop chrome is ours.
func Modal(c *ui.Context, open *bool, title string, content func()) ui.Element {
	t, p := c.Theme(), Colors(c.Theme())
	width, height := c.Size()
	dialog := ui.DialogBase(c, open, func(backdrop, panel ui.Element) {
		backdrop.Background(p.Scrim)
		panel.Label(title).Padding(DialogPadding).Gap(DialogGap).Radius(RadiusDialog).Background(p.Background).
			InsetShadow(0, 0, 0, HairlineWidth, p.Border).
			MaxWidth(max(0, width-2*WindowMargin)).MaxHeight(max(0, height-2*WindowMargin))
		ShadowDialog.Apply(panel, t.Dark)
		content()
	})
	if dialog.Dismissed() {
		// MyGo v0.7.3 restores the opener after painting. Render its focus
		// ring even when no hover or animation would request another frame.
		c.Invalidate()
	}
	return dialog
}

// DialogTitle names a dialog: the heading role in the body text color.
func DialogTitle(c *ui.Context, title string) ui.Element {
	return Heading(c, title).TextColor(c.Theme().Text)
}

// ConfirmSeverity is what a confirmation's icon, if any, says about the
// question: nothing (Info), a choice that needs care (Warning), or one that
// destroys something (Danger).
type ConfirmSeverity int

const (
	SeverityInfo ConfirmSeverity = iota
	SeverityWarning
	SeverityDanger
)

// ConfirmResult is the button a confirmation chose. ConfirmNone is the
// frame the dialog is still open and nothing was chosen.
type ConfirmResult int

const (
	ConfirmNone ConfirmResult = iota
	ConfirmPrimary
	ConfirmSecondary
	ConfirmCancel
)

// ConfirmOptions is one question. Title is the question; Message explains it
// in body text and Detail adds a quiet line under that. Severity chooses the
// icon beside the title, and PrimaryDanger paints the primary action with the
// danger role. Primary is required; Secondary and Cancel are left out when
// empty. Cancel is also what Escape returns.
type ConfirmOptions struct {
	Title         string
	Message       string
	Detail        string
	Severity      ConfirmSeverity
	Primary       string
	Secondary     string
	Cancel        string
	PrimaryDanger bool
}

// ConfirmDialog asks the question in o over the window while *open is true,
// as macOS's and VS Code's message dialogs do. It is role alertdialog, named
// by the title and described by the message and detail, and its focus is
// trapped inside it. The buttons sit at its end in the order Secondary,
// Cancel, Primary: Primary is filled, Secondary outlined and Cancel a quiet
// text button. A destructive primary leaves the focus on Cancel, as macOS
// does, so Enter cancels; otherwise the focus starts on Primary and Enter
// chooses it. Escape cancels and a press outside does nothing. A choice is
// returned in the frame it is made; the caller closes the dialog by setting
// *open to false, so one whose action fails can leave the question up.
func ConfirmDialog(c *ui.Context, open *bool, o ConfirmOptions) ConfirmResult {
	if open == nil {
		return ConfirmNone
	}
	// The root ages this question's frames so the focus moves to its default
	// button as it opens, after MyGo's modal focus has settled too, and never
	// again while the reader Tabs or clicks elsewhere in it.
	age := ui.Local(c.Root(), "confirm-age:"+o.Title+"\x00"+o.Primary+"\x00"+o.Secondary+"\x00"+o.Cancel, func() int { return 0 })
	if !*open {
		*age = 0
		return ConfirmNone
	}
	*age++
	focusDefault := *age <= 2
	t, p := c.Theme(), Colors(c.Theme())
	width, height := c.Size()
	chosen := ConfirmNone
	var target ui.Element
	ui.Overlay(c, func() {
		backdrop := ui.Box(c).Absolute().Left(0).Top(0).Right(0).Bottom(0).Center().
			Background(p.Scrim).Modal()
		backdrop.Children(func() {
			panel := ui.Column(c).Role(ui.RoleAlertDialog).Label(o.Title).Description(confirmDescription(o)).
				Width(min(DialogConfirm, max(0, width-2*WindowMargin))).
				MaxHeight(max(0, height-2*WindowMargin)).Clip().
				Padding(t.Space(SpaceL), t.Space(SpaceXL)).Gap(t.Space(SpaceM)).
				Radius(RadiusCard).Background(p.Background).
				InsetShadow(0, 0, 0, HairlineWidth, p.Border)
			ShadowDialog.Apply(panel, t.Dark)
			panel.Children(func() {
				head := ui.Row(c).FillWidth().Gap(t.Space(SpaceM)).AlignItems(ui.Start)
				head.Children(func() {
					if icon, ink, ok := confirmIcon(c, o.Severity); ok {
						ui.Icon(c, icon).Size(IconLarge, IconLarge).Shrink(0).TextColor(ink).
							Margin(t.Space(SpaceHair), 0, 0, 0)
					}
					BodyStrong(c, o.Title).Grow(1).MinWidth(0)
				})
				if o.Message != "" {
					Body(c, o.Message).TextColor(t.Text)
				}
				if o.Detail != "" {
					Hint(c, o.Detail)
				}
				footer := ui.Row(c).FillWidth().Gap(t.Space(SpaceM)).Wrap().
					AlignItems(ui.Center).Justify(ui.End).Padding(t.Space(SpaceS), 0, 0, 0)
				footer.Children(func() {
					if o.Secondary != "" && SecondaryButton(c, o.Secondary).Clicked() {
						chosen = ConfirmSecondary
					}
					if o.Cancel != "" {
						cancel := confirmCancelButton(c, o.Cancel)
						if o.PrimaryDanger {
							target = cancel
						}
						if cancel.Clicked() {
							chosen = ConfirmCancel
						}
					}
					if o.Primary != "" {
						// Build one button: a MyGo constructor already adds the
						// element to the footer, so making a second to replace
						// it would leave both in the dialog.
						var primary ui.Element
						if o.PrimaryDanger {
							primary = DestructiveButton(c, o.Primary)
						} else {
							primary = PrimaryButton(c, o.Primary)
							target = primary
						}
						if primary.Clicked() {
							chosen = ConfirmPrimary
						}
					}
				})
			})
		})
		if backdrop.OverlayShortcut(0, ui.KeyEscape) {
			chosen = ConfirmCancel
		}
	})
	if focusDefault && chosen == ConfirmNone {
		// Moving the focus after the overlay is built leaves MyGo's note of
		// the element that opened it intact; an AutoFocus inside would be
		// noted instead, and the opener would never get the focus back.
		target.Focus()
	}
	if chosen != ConfirmNone {
		// The next opening starts on its default button again.
		*age = 0
		// MyGo gives the opener its focus back after the painting that takes
		// this overlay away; ask for that frame so its ring is drawn then.
		c.Invalidate()
	}
	return chosen
}

// confirmDescription is what assistive technology reads after the title.
func confirmDescription(o ConfirmOptions) string {
	switch {
	case o.Message != "" && o.Detail != "":
		return o.Message + " " + o.Detail
	case o.Message != "":
		return o.Message
	default:
		return o.Detail
	}
}

// confirmIcon is the signal an icon carries, if the severity has one: info is
// the dialog's own question and needs no mark.
func confirmIcon(c *ui.Context, severity ConfirmSeverity) (*ui.SVG, ui.Color, bool) {
	t := c.Theme()
	switch severity {
	case SeverityWarning:
		return warningIcon, t.Warning, true
	case SeverityDanger:
		return errorIcon, t.Danger, true
	}
	return nil, ui.Color{}, false
}

// confirmCancelButton is a confirmation's quiet third choice: ink text with a
// wash under the pointer, lighter than the outlined secondary action. It is
// how Cancel stays distinct from a secondary such as "Don't Save".
func confirmCancelButton(c *ui.Context, label string) ui.Element {
	t, p := c.Theme(), Colors(c.Theme())
	b := ui.ButtonBase(c).Label(label).Height(HeightControl).Padding(0, Scaled(ControlPaddingX)).
		Radius(RadiusControl).AlignItems(ui.Center).TextColor(t.Text)
	b.Children(func() { ui.Text(c, label).FontSize(Scaled(TextBody)).FontWeight(WeightMedium) })
	switch {
	case b.IsDisabled():
	case b.Pressed():
		b.Background(p.MutedPressed)
	case b.Hovered():
		b.Background(p.MutedHover)
	}
	return b
}
