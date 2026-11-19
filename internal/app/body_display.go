package app

import (
	"github.com/baba983/vscroxy/internal/inspect"
	"github.com/baba983/vscroxy/internal/uikit"
	"github.com/baba983/vscroxy/locales"
	"github.com/egoist/mygo/ui"
)

// bodyDisplayCache keeps one decoded form per pane. A build pass may draw the
// same body many times, and decoding 8 MiB is far more expensive than
// comparing the captured string it came from.
type bodyDisplayCache struct {
	flow        int
	body        string
	contentType string
	text        string
	encoding    string
	decoded     bool
}

// displayText decodes a body once for the view. The result is never written
// back into the capture: copy, save and replay keep reading captured bytes.
func (a *app) displayText(f flow, side int, body string) (string, bool) {
	cached := &a.bodyDisplayCache[side]
	contentType := f.bodyContentType(side == 0)
	if cached.flow != f.id || cached.body != body || cached.contentType != contentType {
		text, encoding, ok := inspect.DisplayText(contentType, []byte(body))
		*cached = bodyDisplayCache{flow: f.id, body: body, contentType: contentType,
			text: text, encoding: encoding, decoded: ok}
	}
	return cached.text, cached.decoded
}

// encodingLabel names the character set only when the viewer decoded the
// body; a plain UTF-8 or hex body needs no badge.
func (a *app) encodingLabel(c *ui.Context, side int) {
	if !a.bodyDisplayCache[side].decoded {
		return
	}
	t := c.Theme()
	uikit.Meta(c, locales.EncodingLabel(a.bodyDisplayCache[side].encoding)).
		Padding(0, t.Space(uikit.SpaceL), t.Space(uikit.SpaceM))
}
