package app

import (
	"github.com/baba983/vscroxy/internal/request"
	"github.com/baba983/vscroxy/internal/uikit"
	"github.com/baba983/vscroxy/locales"
	"github.com/egoist/mygo/ui"
	"github.com/egoist/mygo/ui/editor"
)

// openTestDiff shows a Chai assertion's sides side by side. The buffers belong
// to this dialog, so another failure replaces them without touching the diff
// of a captured response.
func (a *app) openTestDiff(result request.TestResult) {
	if !result.HasDiff {
		return
	}
	a.testDiffName = result.Name
	a.testDiff = &editor.DiffState{
		Old:          editor.NewBuffer(result.Expected),
		New:          editor.NewBuffer(result.Actual),
		Layout:       editor.DiffSplit,
		SplitPercent: a.testDiffSplit,
	}
	a.testDiffOpen = true
}

// testDiffModal is a read-only comparison: expected on the left, actual on
// the right, as the script reported them.
func (a *app) testDiffModal(c *ui.Context, t *ui.Theme) {
	if !a.testDiffOpen {
		return
	}
	title := locales.TestDiffNamed(a.testDiffName)
	_, height := c.Size()
	uikit.Modal(c, &a.testDiffOpen, title, func() {
		uikit.DialogColumn(c, uikit.DialogLarge).Height(min(620, height-80)).Children(func() {
			uikit.DialogTitle(c, title)
			ui.Row(c).Padding(0, t.Space(uikit.SpaceL)).Gap(t.Space(uikit.SpaceL)).Children(func() {
				uikit.Meta(c, locales.ExpectedValue()).Grow(1)
				uikit.Meta(c, locales.ActualValue()).Grow(1)
			})
			if a.testDiff != nil {
				a.testDiff.Font = ui.Font{Family: uikit.FontMono, Size: uikit.TextBody}
				a.testDiff.Theme = uikit.EditorThemeFor(t, false)
				editor.DiffView(c, a.testDiff).Grow(1).MinHeight(0).FillWidth()
				a.testDiffSplit = a.testDiff.SplitPercent
			}
			uikit.DialogFooter(c).Children(func() {
				if uikit.PrimaryButton(c, locales.Close()).Clicked() {
					a.testDiffOpen = false
				}
			})
		})
	})
}
