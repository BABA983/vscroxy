package uikit

import "github.com/egoist/mygo/ui"

// Text roles. A screen says what a piece of text is and the role decides its
// size, weight and color; layout (Grow, SingleLine, Tooltip…) chains as
// usual. Data in a role's size is Font(FontMono) on top of it.

// Heading titles a dialog, a pane or the request an inspector shows.
func Heading(c *ui.Context, s string) ui.Element {
	return ui.Text(c, s).FontSize(Scaled(TextTitle)).FontWeight(500).LineHeight(LineTitle)
}

// Display titles a first-run guide or a command palette: the one line a
// person reads before anything else on the surface.
func Display(c *ui.Context, s string) ui.Element {
	return ui.Text(c, s).FontSize(Scaled(TextDisplay)).FontWeight(500).LineHeight(LineDisplay)
}

// Body is ordinary reading text. It keeps the color around it, so it reads
// right inside buttons and selected rows. Its 13/20 leading is the desktop
// scale's; controls and table cells still fit their fixed heights.
func Body(c *ui.Context, s string) ui.Element {
	return ui.Text(c, s).FontSize(Scaled(TextBody)).LineHeight(LineBody)
}

// Small is secondary interface text: a popover description, a field hint or
// a second line in a menu. It is smaller than a body but still prose.
func Small(c *ui.Context, s string) ui.Element {
	return ui.Text(c, s).FontSize(Scaled(TextSmall)).LineHeight(LineSmall)
}

// Prose is body text that reads as a paragraph, with the leading long
// sentences and CJK need. Body is already 13/20; prose adds a little more.
func Prose(c *ui.Context, s string) ui.Element {
	return Body(c, s).LineHeight(LineProse)
}

// Mono is wire data: a URL, a header value, a path, a number. Monospace is
// never used for a label a person reads as interface.
func Mono(c *ui.Context, s string) ui.Element {
	return ui.Text(c, s).Font(FontMono).FontSize(Scaled(TextData))
}

// Hint explains: a sentence under a control or in an empty pane. It is the
// small prose role, so it carries the looser leading Chinese and long English
// sentences need without taking a body line's height.
func Hint(c *ui.Context, s string) ui.Element {
	return Small(c, s).LineHeight(LineProse).TextColor(c.Theme().TextMuted)
}

// Meta is a secondary fact: a count, a timestamp, a size. Tabular figures
// keep changing numbers from shifting the text around them.
func Meta(c *ui.Context, s string) ui.Element {
	return ui.Text(c, s).FontSize(Scaled(TextMeta)).LineHeight(LineMeta).FontFeatures("tnum").TextColor(c.Theme().TextMuted)
}

// Label names a field or a group of them.
func Label(c *ui.Context, s string) ui.Element {
	return Meta(c, s).FontWeight(500)
}
