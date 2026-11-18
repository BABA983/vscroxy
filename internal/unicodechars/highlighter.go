package unicodechars

import (
	"fmt"
	"slices"
	"strings"
	"sync"
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
	h := getCodePointHighlighter(options)

	var highlights []Highlight
	var wordStart, wordEnd int
	var hasWord, wordHasASCII, wordHasWeirdNonASCII bool
	for index := 0; index < len(s); {
		codePoint, size := utf8.DecodeRuneInString(s[index:])
		// VS Code only runs the regex and word lookup on candidates. An
		// ordinary ASCII token is skipped without scanning its word.
		if !h.isCandidate(codePoint) {
			index += size
			continue
		}
		if !hasWord || index < wordStart || index >= wordEnd {
			wordStart, wordEnd, hasWord = wordAt(s, index)
			wordHasASCII, wordHasWeirdNonASCII = false, false
			if hasWord {
				wordHasASCII, wordHasWeirdNonASCII = wordContext(s[wordStart:wordEnd], h.ambiguous)
			}
		}
		reason := h.shouldHighlight(codePoint, wordHasASCII, wordHasWeirdNonASCII)
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
	// candidates is nil for VS Code's 'allNonBasicAscii' candidate set.
	candidates map[rune]bool
}

var codePointHighlighters sync.Map

// getCodePointHighlighter caches the ambiguous instance and candidate set per
// options, as VS Code builds them once per CodePointHighlighter.
func getCodePointHighlighter(options UnicodeHighlighterOptions) *codePointHighlighter {
	key := fmt.Sprintf("%t|%t|%t|%v|%s", options.NonBasicASCII,
		options.AmbiguousCharacters, options.InvisibleCharacters,
		options.AllowedCodePoints, options.AllowedLocales)
	if cached, ok := codePointHighlighters.Load(key); ok {
		return cached.(*codePointHighlighter)
	}

	options.AllowedCodePoints = slices.Clone(options.AllowedCodePoints)
	options.AllowedLocales = slices.Clone(options.AllowedLocales)
	h := &codePointHighlighter{
		options:   options,
		allowed:   make(map[rune]bool, len(options.AllowedCodePoints)),
		ambiguous: GetInstance(options.AllowedLocales),
	}
	for _, codePoint := range options.AllowedCodePoints {
		h.allowed[codePoint] = true
	}
	h.candidates = h.getCandidateCodePoints()

	actual, _ := codePointHighlighters.LoadOrStore(key, h)
	return actual.(*codePointHighlighter)
}

// getCandidateCodePoints mirrors CodePointHighlighter.getCandidateCodePoints:
// a nil set means the 'allNonBasicAscii' candidate expression.
func (h *codePointHighlighter) getCandidateCodePoints() map[rune]bool {
	if h.options.NonBasicASCII {
		return nil
	}

	candidates := make(map[rune]bool)
	if h.options.InvisibleCharacters {
		for _, codePoint := range (InvisibleCharacters{}).CodePoints() {
			if !isAllowedInvisibleCharacter(codePoint) {
				candidates[codePoint] = true
			}
		}
	}
	if h.options.AmbiguousCharacters {
		for _, codePoint := range h.ambiguous.GetConfusableCodePoints() {
			candidates[codePoint] = true
		}
	}
	for codePoint := range h.allowed {
		delete(candidates, codePoint)
	}
	return candidates
}

func (h *codePointHighlighter) isCandidate(codePoint rune) bool {
	if h.candidates == nil {
		return !isBasicASCIIRune(codePoint)
	}
	return h.candidates[codePoint]
}

func (h codePointHighlighter) shouldHighlight(codePoint rune, hasASCII, hasWeirdNonASCII bool) simpleHighlightReason {
	if h.allowed[codePoint] {
		return reasonNone
	}
	if h.options.NonBasicASCII && !isBasicASCIIRune(codePoint) {
		return reasonNonBasicASCII
	}

	// VS Code suppresses a confusable when the surrounding word is entirely
	// made of non-ASCII, non-confusable characters (getWordAtText with
	// DEFAULT_WORD_REGEXP). wordContext computes those two facts once per word.
	if !hasASCII && hasWeirdNonASCII {
		return reasonNone
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

// wordContext computes the two conditions used by
// CodePointHighlighter.shouldHighlightNonBasicASCII for one word.
func wordContext(word string, ambiguous *AmbiguousCharacters) (hasASCII, hasWeirdNonASCII bool) {
	for _, codePoint := range word {
		if isBasicASCIIRune(codePoint) {
			hasASCII = true
			continue
		}
		if !ambiguous.IsAmbiguous(codePoint) && !(InvisibleCharacters{}).IsInvisibleCharacter(codePoint) {
			hasWeirdNonASCII = true
		}
	}
	return hasASCII, hasWeirdNonASCII
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
