package uikit

import (
	"github.com/egoist/mygo/ui"
	"github.com/egoist/mygo/ui/markdown"
)

// MarkdownTheme is the prose scale inside a Markdown view. Amp Web's
// --prose-* variables set body 13/20, paragraph gap .75em, list gap .375em,
// 2.5/2/1.5em above H1/H2/H3 and .5em below each heading. MyGo measures
// these in ems, so the values cross over unchanged.
func MarkdownTheme(t *ui.Theme) *markdown.Theme {
	th := markdown.DefaultTheme(t)
	th.FontSize = Scaled(TextBody)
	th.LineHeight = LineBody
	th.CodeSize = 0.85
	th.BlockSpacing = 0.75
	th.ItemSpacing = 0.375
	th.Headings[0] = markdown.HeadingStyle{Size: 1.5, Weight: 700, Color: t.Text, Top: 2.5, Bottom: 0.5}
	th.Headings[1] = markdown.HeadingStyle{Size: 1.25, Weight: 600, Color: t.Text, Top: 2, Bottom: 0.5}
	th.Headings[2] = markdown.HeadingStyle{Size: 1, Weight: 600, Color: t.Text, Top: 1.5, Bottom: 0.5}
	return th
}
