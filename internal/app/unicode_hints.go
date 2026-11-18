package app

import (
	"fmt"
	"slices"
	"strings"

	"github.com/baba983/vscroxy/internal/uikit"
	"github.com/baba983/vscroxy/internal/unicodechars"
	"github.com/baba983/vscroxy/locales"
	"github.com/egoist/mygo/i18n"
	"github.com/egoist/mygo/ui"
)

// unicodeMarkerText is a data symbol, not prose: it marks a value whose raw
// bytes contain a hidden or look-alike code point. The tip names each one.
const unicodeMarkerText = "⟨!⟩"

const unicodeTipLimit = 4

// unicodeOptions picks the confusable table from the app's language. VS Code's
// tables use lower-case tags; an app language outside those tables falls back
// to their default, as VS Code does.
func (a *app) unicodeOptions() unicodechars.UnicodeHighlighterOptions {
	tag := a.language
	if tag == "" {
		tag = i18n.Locale()
	}
	tag = strings.ToLower(tag)
	switch {
	case strings.Contains(tag, "hant"):
		tag = "zh-hant"
	case strings.HasPrefix(tag, "zh"):
		tag = "zh-hans"
	case !slices.Contains(unicodechars.GetLocales(), tag):
		tag = ""
	}
	var allowed []string
	if tag != "" {
		allowed = []string{tag}
	}
	return unicodechars.UnicodeHighlighterOptions{
		AmbiguousCharacters: true,
		InvisibleCharacters: true,
		AllowedLocales:      allowed,
	}
}

// unicodeMarker flags raw wire data without changing it. Callers pass the
// model's string, never a tooltip or a wrapped display string: uikit.breakLong
// inserts U+200B for display wrapping, which is not part of the value.
func (a *app) unicodeMarker(c *ui.Context, key, raw string) bool {
	if raw == "" {
		return false
	}
	highlights := unicodechars.ComputeUnicodeHighlights(raw, a.unicodeOptions())
	if len(highlights) == 0 {
		return false
	}

	marker := ui.Icon(c.Key(key), warningIcon).
		Size(uikit.IconInline, uikit.IconInline).
		TextColor(c.Theme().Warning).
		Label(unicodeMarkerText + " " + locales.Reveal())
	uikit.Tip(c, marker, unicodeHintTip(raw, highlights))
	return true
}

// unicodeHintTip names the code points and their primary confusables, then
// offers the revealed view built by unicodechars.Reveal.
func unicodeHintTip(raw string, highlights []unicodechars.Highlight) string {
	detail := make([]byte, 0, 96)
	for i, highlight := range highlights[:min(len(highlights), unicodeTipLimit)] {
		if i > 0 {
			detail = append(detail, '\n')
		}
		detail = fmt.Appendf(detail, "%c+%04X %s", 'U', highlight.CodePoint, highlight.Kind)
		if highlight.Kind == unicodechars.HighlightAmbiguous {
			detail = fmt.Appendf(detail, " → %c+%04X (%q)", 'U', highlight.Confusable, highlight.Confusable)
		}
	}
	if len(highlights) > unicodeTipLimit {
		detail = append(detail, '\n')
		detail = append(detail, "…"...)
	}
	detail = append(detail, '\n')
	detail = append(detail, locales.Reveal()...)
	detail = append(detail, ' ')
	detail = append(detail, unicodechars.Reveal(raw)...)
	return string(detail)
}
