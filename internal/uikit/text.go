package uikit

import "github.com/egoist/mygo/ui"

// Text roles. A screen says what a piece of text is and the role decides its
// size, weight and color; layout (Grow, SingleLine, Tooltip…) chains as
// usual. Data in a role's size is Font(FontMono) on top of it.

// Heading titles a dialog, a pane or the request an inspector shows.
func Heading(c *ui.Context, s string) ui.Element {
	return ui.Text(c, s).FontSize(TextTitle).FontWeight(600)
}

// Body is ordinary reading text. It keeps the color around it, so it reads
// right inside buttons and selected rows.
func Body(c *ui.Context, s string) ui.Element {
	return ui.Text(c, s).FontSize(TextBody)
}

// Mono is data at body size: a URL, a header, a path.
func Mono(c *ui.Context, s string) ui.Element {
	return Body(c, s).Font(FontMono)
}

// Hint explains: a sentence under a control or in an empty pane.
func Hint(c *ui.Context, s string) ui.Element {
	return Body(c, s).TextColor(c.Theme().TextMuted)
}

// Meta is a secondary fact: a count, a timestamp, a size.
func Meta(c *ui.Context, s string) ui.Element {
	return ui.Text(c, s).FontSize(TextMeta).TextColor(c.Theme().TextMuted)
}

// Label names a field or a group of them.
func Label(c *ui.Context, s string) ui.Element {
	return Meta(c, s).FontWeight(600)
}
