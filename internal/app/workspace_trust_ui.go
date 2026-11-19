package app

import (
	"github.com/baba983/vscroxy/internal/uikit"
	"github.com/baba983/vscroxy/locales"
	"github.com/egoist/mygo/ui"
)

// trustListCard is one side of the Settings trust list: a path and the action
// that forgets its answer. Paths are data, so they keep the mono data role.
func (a *app) trustListCard(c *ui.Context, title string, paths []string, empty string) ui.Element {
	return uikit.SettingsCard(c, title, func() {
		uikit.SettingsCardBlock(c, "", "", func() {
			if len(paths) == 0 {
				uikit.Hint(c, empty)
				return
			}
			for _, dir := range paths {
				// One row per decision; a long path wraps instead of pushing
				// the revoke action out of the panel.
				ui.Row(c).FillWidth().Gap(uikit.Scaled(uikit.SpaceM)).Wrap().AlignItems(ui.Center).Children(func() {
					uikit.Mono(c, dir).Selectable().Grow(1).MinWidth(0)
					if uikit.CompactButton(c, locales.RevokeTrust()).Clicked() {
						a.revokeTrust(dir)
					}
				})
			}
		})
	})
}

// workspaceTrustModal is the list the Settings card and the palette command
// open. Revoking an answer leaves the project undecided; its next script run
// asks again.
func (a *app) workspaceTrustModal(c *ui.Context, t *ui.Theme) {
	if !a.trustManageOpen {
		return
	}
	trusted, untrusted := a.trustLists()
	_, height := c.Size()
	// The list is short until many projects have been decided; give the
	// dialog the room its rows need instead of a tall empty panel.
	panel := float32(250 + 24*(len(trusted)+len(untrusted)))
	uikit.Modal(c, &a.trustManageOpen, locales.WorkspaceTrust(), func() {
		uikit.DialogColumn(c, uikit.SettingsWidth).Height(min(panel, height-80)).Children(func() {
			uikit.DialogTitle(c, locales.WorkspaceTrust())
			uikit.Hint(c, locales.WorkspaceTrustHint())
			ui.Scroll(c).Grow(1).MinHeight(0).FillWidth().Gap(t.Space(uikit.SpaceM)).Children(func() {
				a.trustListCard(c, locales.TrustedProjects(), trusted, locales.NoTrustedProjects())
				a.trustListCard(c, locales.UntrustedProjects(), untrusted, locales.NoUntrustedProjects())
			})
			uikit.DialogFooter(c).Children(func() {
				if uikit.PrimaryButton(c, locales.Done()).Clicked() {
					a.trustManageOpen = false
				}
			})
		})
	})
}
