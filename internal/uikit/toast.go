package uikit

import (
	"github.com/baba983/vscroxy/locales"
	"github.com/egoist/mygo/ui"
)

// Only the look is ours: MyGo owns announcements, timeouts, pause-on-hover,
// the three-toast limit, action dispatch and keyboard dismissal.
func Toasts(c *ui.Context) {
	t, p := c.Theme(), Colors(c.Theme())
	width, _ := c.Size()
	ui.ToastViewportBase(c, func(viewport ui.Element, toasts []ui.Toast) {
		// Toasts keep clear of the footer: its buttons and tool chips are
		// at the window's bottom edge and must stay reachable.
		viewport.Padding(t.Space(SpaceXL), t.Space(SpaceXL), t.Space(SpaceXL)+HeightFooter, t.Space(SpaceXL)).AlignItems(ui.End).Gap(t.Space(SpaceM))
		for _, toast := range toasts {
			icon, color := infoIcon, t.TextMuted
			switch toast.Type {
			case "success":
				icon, color = successIcon, t.Success
			case "warning":
				icon, color = warningIcon, t.Warning
			case "error":
				icon, color = errorIcon, t.Danger
			}
			parts := ui.ToastBase(c, toast)
			ToastPanel(c, parts.Root).Row().Width(min(ToastWidth, width-2*WindowMargin)).Gap(t.Space(SpaceM)).Padding(ToastPaddingY, ToastPaddingX).
				AlignItems(ui.Center).
				Children(func() {
					ui.Icon(c, icon).Size(IconLarge, IconLarge).TextColor(color)
					ui.Column(c).Grow(1).MinWidth(0).Gap(t.Space(SpaceM)).Children(func() {
						ui.Text(c, toast.Title).FontSize(Scaled(TextBody)).TextColor(t.Text)
						if toast.Description != "" {
							ui.Text(c, toast.Description).FontSize(Scaled(TextMeta)).TextColor(t.TextMuted)
						}
					})
					if toast.Action != "" {
						// The action sits on the toast's one line, as a link beside
						// the message rather than a second full-width button. On the
						// canvas panel it is body ink, not the inverse text a
						// tooltip uses; at rest it must read as an action, not as a
						// disabled control.
						b := parts.ActionButton().Label(toast.Action).Height(HeightControlSmall).
							Padding(0, Scaled(ControlPaddingXSmall)).Radius(RadiusInner).AlignItems(ui.Center).
							FontSize(Scaled(TextBody)).FontWeight(WeightMedium).TextColor(t.Text)
						b.Children(func() {
							ui.Text(c, toast.Action).FontSize(Scaled(TextBody)).FontWeight(WeightMedium).TextColor(t.Text).Underline()
						})
						if b.Hovered() {
							b.Background(p.MutedHover).TextColor(t.Text)
						}
					}
					close := Tip(c, parts.CloseButton().Label(locales.DismissNotification()), locales.DismissNotification()).
						Size(HeightControlSmall, HeightControlSmall).Radius(RadiusControl).Center().TextColor(t.TextMuted)
					if close.Pressed() {
						close.Background(p.MutedPressed)
					} else if close.Hovered() {
						close.Background(p.MutedHover)
					}
					close.Children(func() { ui.Icon(c, closeIcon).Size(IconInline, IconInline) })
				})
		}
	})
}
