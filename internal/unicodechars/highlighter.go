package unicodechars

import (
	"fmt"
	"strings"
	"unicode"
	"unicode/utf8"
)

// HighlightKind is VS Code's UnicodeHighlighterReasonKind.
type HighlightKind uint8

const (
	// HighlightAmbiguous corresponds to UnicodeHighlighterReasonKind.Ambiguous.
	HighlightAmbiguous HighlightKind = iota
	// HighlightInvisible corresponds to UnicodeHighlighterReasonKind.Invisible.
	HighlightInvisible
	// HighlightNonBasicASCII corresponds to UnicodeHighlighterReasonKind.NonBasicAscii.
	HighlightNonBasicASCII
)

func (kind HighlightKind) String() string {
	switch kind {
	case HighlightAmbiguous:
		return "Ambiguous"
	case HighlightInvisible:
		return "Invisible"
	case HighlightNonBasicASCII:
		return "NonBasicASCII"
	default:
		return fmt.Sprintf("HighlightKind(%d)", kind)
	}
}

// Highlight is one flagged code point. Start and End are byte offsets into the
// scanned string, with End exclusive. Confusable is set for ambiguous highlights.
type Highlight struct {
	Start      int
	End        int
	Kind       HighlightKind
	CodePoint  rune
	Confusable rune
}

// UnicodeHighlighterOptions is VS Code's UnicodeHighlighterOptions without
// includeComments and includeStrings: this package scans plain strings and
// has no text model, comments or quoted-string tokens to consider.
type UnicodeHighlighterOptions struct {
	NonBasicASCII       bool
	AmbiguousCharacters bool
	InvisibleCharacters bool
	AllowedCodePoints   []rune
	AllowedLocales      []string
}

// ComputeUnicodeHighlights is the Go form of
// UnicodeTextModelHighlighter.computeUnicodeHighlights. It scans the whole
// string and returns the highlight reason with each range, using the same
// decision order as VS Code's CodePointHighlighter.shouldHighlightNonBasicASCII.
// Offsets are bytes, not VS Code's UTF-16 code units or 1-based columns.
func ComputeUnicodeHighlights(s string, options UnicodeHighlighterOptions) []Highlight {
	h := codePointHighlighter{
		options:   options,
		allowed:   make(map[rune]bool, len(options.AllowedCodePoints)),
		ambiguous: GetInstance(options.AllowedLocales),
	}
	for _, codePoint := range options.AllowedCodePoints {
		h.allowed[codePoint] = true
	}

	var highlights []Highlight
	for index := 0; index < len(s); {
		codePoint, size := utf8.DecodeRuneInString(s[index:])
		reason := h.shouldHighlight(s, codePoint, index)
		if reason != reasonNone {
			highlight := Highlight{
				Start:     index,
				End:       index + size,
				Kind:      HighlightKind(reason - 1),
				CodePoint: codePoint,
			}
			if reason == reasonAmbiguous {
				highlight.Confusable, _ = h.ambiguous.GetPrimaryConfusable(codePoint)
			}
			highlights = append(highlights, highlight)
		}
		index += size
	}
	return highlights
}

var revealOptions = UnicodeHighlighterOptions{
	AmbiguousCharacters: true,
	InvisibleCharacters: true,
}

// Reveal renders flagged runes as ⟨U+200B⟩ for display. It uses the default
// invisible and ambiguous detection, leaving ordinary non-ASCII text alone.
// The input string is never changed; the escapes only exist in the result.
func Reveal(s string) string {
	highlights := ComputeUnicodeHighlights(s, revealOptions)
	if len(highlights) == 0 {
		return s
	}

	var result strings.Builder
	last := 0
	for _, highlight := range highlights {
		result.WriteString(s[last:highlight.Start])
		fmt.Fprintf(&result, "⟨U+%04X⟩", highlight.CodePoint)
		last = highlight.End
	}
	result.WriteString(s[last:])
	return result.String()
}

type simpleHighlightReason uint8

const (
	reasonNone simpleHighlightReason = iota
	reasonAmbiguous
	reasonInvisible
	reasonNonBasicASCII
)

type codePointHighlighter struct {
	options   UnicodeHighlighterOptions
	allowed   map[rune]bool
	ambiguous *AmbiguousCharacters
}

func (h codePointHighlighter) shouldHighlight(s string, codePoint rune, index int) simpleHighlightReason {
	if h.allowed[codePoint] {
		return reasonNone
	}
	if h.options.NonBasicASCII && !isBasicASCIIRune(codePoint) {
		return reasonNonBasicASCII
	}

	// VS Code suppresses a confusable when the surrounding word is entirely
	// made of non-ASCII, non-confusable characters (getWordAtText with
	// DEFAULT_WORD_REGEXP). The Go word helper below uses the same default word
	// separators over runes instead of JS UTF-16 code units.
	if start, end, ok := wordAt(s, index); ok {
		hasASCII, hasWeirdNonASCII := false, false
		for _, wordRune := range s[start:end] {
			if isBasicASCIIRune(wordRune) {
				hasASCII = true
				continue
			}
			if !h.ambiguous.IsAmbiguous(wordRune) && !(InvisibleCharacters{}).IsInvisibleCharacter(wordRune) {
				hasWeirdNonASCII = true
			}
		}
		if !hasASCII && hasWeirdNonASCII {
			return reasonNone
		}
	}

	if h.options.InvisibleCharacters && !isAllowedInvisibleCharacter(codePoint) &&
		(InvisibleCharacters{}).IsInvisibleCharacter(codePoint) {
		return reasonInvisible
	}
	if h.options.AmbiguousCharacters && h.ambiguous.IsAmbiguous(codePoint) {
		return reasonAmbiguous
	}
	return reasonNone
}

// isAllowedInvisibleCharacter mirrors the VS Code helper of the same name.
func isAllowedInvisibleCharacter(codePoint rune) bool {
	return codePoint == ' ' || codePoint == '\n' || codePoint == '\t'
}

// usualWordSeparators is VS Code's USUAL_WORD_SEPARATORS from wordHelper.ts.
const usualWordSeparators = "`~!@#$%^&*()-=+[{]}\\|;:'\",.<>/?"

func isWordRune(codePoint rune) bool {
	return !unicode.IsSpace(codePoint) && !strings.ContainsRune(usualWordSeparators, codePoint)
}

// wordAt is the Go equivalent of getWordAtText(s, index+1, DEFAULT_WORD_REGEXP,
// 0) for the enclosing word. VS Code discards a word that ends at the flagged
// position, which is the same as returning no word here.
func wordAt(s string, index int) (int, int, bool) {
	codePoint, size := utf8.DecodeRuneInString(s[index:])
	if !isWordRune(codePoint) {
		return 0, 0, false
	}

	start := index
	for start > 0 {
		previous, previousSize := utf8.DecodeLastRuneInString(s[:start])
		if !isWordRune(previous) {
			break
		}
		start -= previousSize
	}
	end := index + size
	for end < len(s) {
		next, nextSize := utf8.DecodeRuneInString(s[end:])
		if !isWordRune(next) {
			break
		}
		end += nextSize
	}
	return start, end, true
}
