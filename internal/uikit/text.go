package uikit

import "github.com/egoist/mygo/ui"

// Text roles. A screen says what a piece of text is and the role decides its
// size, weight and color; layout (Grow, SingleLine, Tooltip…) chains as
// usual. Data in a role's size is Font(FontMono) on top of it.

// Heading titles a dialog, a pane or the request an inspector shows.
func Heading(c *ui.Context, s string) ui.Element {
	return ui.Text(c, s).FontSize(TextTitle).FontWeight(600).LineHeight(LineTitle)
}

// Body is ordinary reading text. It keeps the color around it, so it reads
// right inside buttons and selected rows. Its leading stays compact because
// controls and table cells use it too.
func Body(c *ui.Context, s string) ui.Element {
	return ui.Text(c, s).FontSize(TextBody)
}

// Prose is body text that reads as a paragraph, with the leading long
// sentences and CJK need. Body stays compact for controls and cells.
func Prose(c *ui.Context, s string) ui.Element {
	return Body(c, s).LineHeight(LineProse)
}

// Mono is data at body size: a URL, a header, a path.
func Mono(c *ui.Context, s string) ui.Element {
	return Body(c, s).Font(FontMono)
}

// Hint explains: a sentence under a control or in an empty pane. It is the
// prose role, so it carries the looser leading Chinese and long English
// sentences need.
func Hint(c *ui.Context, s string) ui.Element {
	return Prose(c, s).TextColor(c.Theme().TextMuted)
}

// Meta is a secondary fact: a count, a timestamp, a size. Tabular figures
// keep changing numbers from shifting the text around them.
func Meta(c *ui.Context, s string) ui.Element {
	return ui.Text(c, s).FontSize(TextMeta).LineHeight(LineMeta).FontFeatures("tnum").TextColor(c.Theme().TextMuted)
}

// Label names a field or a group of them.
func Label(c *ui.Context, s string) ui.Element {
	return Meta(c, s).FontWeight(600)
}
