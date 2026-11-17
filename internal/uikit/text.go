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

// BodyStrong is body text that must stand out from the lines around it: the
// lead of a group or the value a row is about. It is 13 at weight 500.
func BodyStrong(c *ui.Context, s string) ui.Element {
	return Body(c, s).FontWeight(500)
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

// DataBody is wire data at body size: a header value, a path, a field's
// monospace text. A sentence about the data is Body.
func DataBody(c *ui.Context, s string) ui.Element {
	return ui.Text(c, s).Font(FontMono).FontSize(Scaled(TextBody)).LineHeight(LineBody)
}

// DataMeta is wire data at the meta size: a count, a size or a cell in a dense
// table that is data rather than prose.
func DataMeta(c *ui.Context, s string) ui.Element {
	return ui.Text(c, s).Font(FontMono).FontSize(Scaled(TextMeta)).LineHeight(LineMeta)
}

// Hint explains: a sentence under a control or in an empty pane. It is the
// small prose role, so it carries the looser leading Chinese and long English
// sentences need without taking a body line's height.
func Hint(c *ui.Context, s string) ui.Element {
	return Small(c, s).LineHeight(LineProse).TextColor(c.Theme().TextMuted)
}

// Caption is a fact set in the body's color, at the meta size: a line that
// belongs with the text around it rather than reading as a note on it.
func Caption(c *ui.Context, s string) ui.Element {
	return ui.Text(c, s).FontSize(Scaled(TextMeta)).LineHeight(LineMeta)
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

// ErrorText is an error sentence no field can carry: the failure a page or a
// pane reports where there is no control to mark. A field's own error goes
// through FormRow, which binds it to the control.
func ErrorText(c *ui.Context, s string) ui.Element {
	return Small(c, s).TextColor(c.Theme().Danger)
}

// Code is wire data that runs over several lines: a header block, a raw
// message, a snippet a person copies. Monospace is for data only; a sentence
// about the data is Hint.
func Code(c *ui.Context, s string) ui.Element {
	return Mono(c, s).LineHeight(LineSmall)
}
