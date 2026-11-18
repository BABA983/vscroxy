package app

import (
	"fmt"
	"strings"
	"sync"
	"unicode/utf8"

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

var (
	unicodeLocales  = sync.OnceValue(unicodeLocaleSet)
	unicodeOptCache sync.Map
)

func unicodeLocaleSet() map[string]bool {
	set := make(map[string]bool)
	for _, locale := range unicodechars.GetLocales() {
		set[locale] = true
	}
	return set
}

// unicodeOptions picks the confusable table from the app's language. VS Code's
// tables use lower-case tags; an app language outside those tables falls back
// to their default, as VS Code does. The result is cached by language: frames
// build many markers, and none of them write to the options.
func (a *app) unicodeOptions() unicodechars.UnicodeHighlighterOptions {
	var key, tag string
	if a.language == "" {
		tag = i18n.Locale()
		key = "locale:" + tag
	} else {
		tag = a.language
		key = "tag:" + tag
	}
	if cached, ok := unicodeOptCache.Load(key); ok {
		return cached.(unicodechars.UnicodeHighlighterOptions)
	}

	tag = strings.ToLower(tag)
	switch {
	case strings.Contains(tag, "hant"):
		tag = "zh-hant"
	case strings.HasPrefix(tag, "zh"):
		tag = "zh-hans"
	case !unicodeLocales()[tag]:
		tag = ""
	}
	var allowed []string
	if tag != "" {
		allowed = []string{tag}
	}
	options := unicodechars.UnicodeHighlighterOptions{
		AmbiguousCharacters: true,
		InvisibleCharacters: true,
		AllowedLocales:      allowed,
	}
	actual, _ := unicodeOptCache.LoadOrStore(key, options)
	return actual.(unicodechars.UnicodeHighlighterOptions)
}

// unicodeMarker flags raw wire data without changing it. Callers pass the
// model's string, never a tooltip or a wrapped display string: uikit.breakLong
// inserts U+200B for display wrapping, which is not part of the value. A
// masked value is still scanned, but its tip never repeats any of its text.
func (a *app) unicodeMarker(c *ui.Context, key, raw string, masked bool) bool {
	if raw == "" || unicodechars.IsBasicASCII(raw) {
		return false
	}
	highlights := unicodechars.ComputeUnicodeHighlights(raw, a.unicodeOptions())
	if len(highlights) == 0 {
		return false
	}

	marker := ui.Icon(c.Key(key), warningIcon).
		Size(uikit.IconInline, uikit.IconInline).
		TextColor(c.Theme().Warning).
		Label(unicodeMarkerText + " " + locales.UnicodeHint())
	a.unicodeTip(c, marker, raw, highlights, masked)
	return true
}

// unicodeTip uses the kit's tooltip face, with the Reveal view in a distinct
// warning-coloured mono role so its code point escapes cannot be mistaken for
// part of the value. A masked value gets only code points, kinds and positions.
func (a *app) unicodeTip(c *ui.Context, marker ui.Element, raw string, highlights []unicodechars.Highlight, masked bool) {
	t := c.Theme()
	details := unicodeTipDetails(raw, highlights, masked)
	ui.TooltipBase(c, marker, func(tip ui.Element) {
		uikit.TipSurface(c, tip)
		tip.Children(func() {
			ui.Column(c).Gap(t.Space(uikit.SpaceXS)).Children(func() {
				for _, line := range details {
					uikit.Small(c, line).TextColor(t.Text)
				}
				if !masked {
					ui.Row(c).Gap(t.Space(uikit.SpaceS)).AlignItems(ui.Center).Children(func() {
						uikit.Small(c, locales.Reveal())
						uikit.Mono(c, unicodechars.Reveal(raw)).TextColor(t.Warning).Shrink(1)
					})
				}
			})
		})
	})
	description := strings.Join(details, ". ")
	if !masked {
		description += ". " + locales.Reveal() + " " + unicodechars.Reveal(raw)
	}
	marker.Description(description)
}

func unicodeTipDetails(raw string, highlights []unicodechars.Highlight, masked bool) []string {
	lines := make([]string, 0, min(len(highlights), unicodeTipLimit)+1)
	for _, highlight := range highlights[:min(len(highlights), unicodeTipLimit)] {
		detail := make([]byte, 0, 64)
		detail = fmt.Appendf(detail, "%c+%04X %s", 'U', highlight.CodePoint, unicodeKindName(highlight.Kind))
		if highlight.Kind == unicodechars.HighlightAmbiguous && !masked {
			detail = fmt.Appendf(detail, " → %c+%04X", 'U', highlight.Confusable)
		}
		if masked {
			position := utf8.RuneCountInString(raw[:highlight.Start]) + 1
			detail = append(detail, ' ')
			detail = append(detail, locales.UnicodeAtCharacter(position)...)
		}
		lines = append(lines, string(detail))
	}
	if len(highlights) > unicodeTipLimit {
		lines = append(lines, "…")
	}
	return lines
}

func unicodeKindName(kind unicodechars.HighlightKind) string {
	switch kind {
	case unicodechars.HighlightInvisible:
		return locales.UnicodeInvisible()
	case unicodechars.HighlightAmbiguous:
		return locales.UnicodeAmbiguous()
	case unicodechars.HighlightNonBasicASCII:
		return locales.UnicodeNonBasicASCII()
	default:
		return ""
	}
}
