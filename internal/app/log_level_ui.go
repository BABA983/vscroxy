package app

import (
	"github.com/baba983/vscroxy/internal/applog"
	"github.com/baba983/vscroxy/internal/uikit"
	"github.com/baba983/vscroxy/locales"
	"github.com/egoist/mygo/ui"
)

func logLevelHint() string {
	if applog.FlagLevelOverridden() {
		return locales.LogLevelFlagOverride()
	}
	return locales.LogLevelHint()
}

// logLevelModal is the palette's way to pick a level. Settings shows the same
// select inline; this dialog is for workbench.action.setLogLevel, which is
// reachable without opening Settings.
func (a *app) logLevelModal(c *ui.Context) {
	if !a.logLevelPickerOpen {
		return
	}
	title := locales.SetLogLevel()
	uikit.Modal(c, &a.logLevelPickerOpen, title, func() {
		uikit.DialogColumn(c, uikit.DialogSmall).Children(func() {
			uikit.DialogTitle(c, title)
			uikit.Hint(c, logLevelHint())
			selected := a.logLevelPicker
			control := uikit.SelectLabels(c, &selected, applog.LevelNames(), logLevelLabel).
				Label(locales.LogLevel()).FillWidth().Disabled(applog.FlagLevelOverridden())
			if control.Changed() {
				a.logLevelPicker = selected
			}
			uikit.DialogFooter(c).Children(func() {
				if uikit.SecondaryButton(c, locales.Cancel()).Clicked() {
					a.logLevelPickerOpen = false
				}
				if uikit.PrimaryButton(c, locales.Done()).Clicked() && !applog.FlagLevelOverridden() {
					a.setLogLevel(a.logLevelPicker)
					a.logLevelPickerOpen = false
				}
			})
		})
	})
}
