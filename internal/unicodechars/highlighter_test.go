package unicodechars

import (
	"strings"
	"testing"
)

var detectDefaults = UnicodeHighlighterOptions{
	AmbiguousCharacters: true,
	InvisibleCharacters: true,
}

// TestComputeUnicodeHighlights is ported from
// src/vs/editor/test/common/services/unicodeTextModelHighlighter.test.ts at the
// generated data's commit. The VS Code ranges ([2,5 -> 2,6], [2,7 -> 2,8],
// [2,10 -> 2,11], [2,13 -> 2,14]) are byte offsets here: 5, 9, 13 and 17.
func TestComputeUnicodeHighlights(t *testing.T) {
	text := "\n\tFor\u00a0\u00e5\u00a0gi\u00a0et\u00a0eksempel\n"
	want := []Highlight{
		{Start: 5, End: 7, Kind: HighlightInvisible, CodePoint: '\u00a0'},
		{Start: 9, End: 11, Kind: HighlightInvisible, CodePoint: '\u00a0'},
		{Start: 13, End: 15, Kind: HighlightInvisible, CodePoint: '\u00a0'},
		{Start: 17, End: 19, Kind: HighlightInvisible, CodePoint: '\u00a0'},
	}
	got := ComputeUnicodeHighlights(text, detectDefaults)
	assertHighlights(t, got, want)
}

func TestComputeUnicodeHighlightsLargeASCIIToken(t *testing.T) {
	text := "Bearer " + strings.Repeat("a", 1<<20)
	assertHighlights(t, ComputeUnicodeHighlights(text, revealOptions), nil)
	if got := Reveal(text); got != text {
		t.Error("Reveal changed an ordinary ASCII token")
	}
}

func TestComputeUnicodeHighlightsHTTPData(t *testing.T) {
	tests := []struct {
		name    string
		text    string
		options UnicodeHighlighterOptions
		want    []Highlight
		reveal  string
	}{
		{
			name:    "zero width space in Bearer token",
			text:    "Authorization: Bearer ab\u200bcd",
			options: detectDefaults,
			want: []Highlight{
				{Start: 24, End: 27, Kind: HighlightInvisible, CodePoint: '\u200b'},
			},
			reveal: "Authorization: Bearer ab⟨U+200B⟩cd",
		},
		{
			name:    "trailing NBSP in header value",
			text:    "X-Token: abc\u00a0",
			options: detectDefaults,
			want: []Highlight{
				{Start: 12, End: 14, Kind: HighlightInvisible, CodePoint: '\u00a0'},
			},
			reveal: "X-Token: abc⟨U+00A0⟩",
		},
		{
			name:    "Cyrillic a in host",
			text:    "аpple.com",
			options: detectDefaults,
			want: []Highlight{
				{Start: 0, End: 2, Kind: HighlightAmbiguous, CodePoint: 'а', Confusable: 'a'},
			},
			reveal: "⟨U+0430⟩pple.com",
		},
		{
			name:    "plain Chinese under zh-hans",
			text:    "中文测试",
			options: UnicodeHighlighterOptions{AmbiguousCharacters: true, InvisibleCharacters: true, AllowedLocales: []string{"zh-hans"}},
			reveal:  "中文测试",
		},
		{
			name:    "plain Chinese under unknown zh locale",
			text:    "中文测试",
			options: UnicodeHighlighterOptions{AmbiguousCharacters: true, InvisibleCharacters: true, AllowedLocales: []string{"zh"}},
			reveal:  "中文测试",
		},
		{
			name: "allowed code point",
			text: "X-Token: abc\u00a0",
			options: UnicodeHighlighterOptions{
				AmbiguousCharacters: true,
				InvisibleCharacters: true,
				AllowedCodePoints:   []rune{'\u00a0'},
			},
			// Reveal always uses the default options, so it still shows NBSP.
			reveal: "X-Token: abc⟨U+00A0⟩",
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			assertHighlights(t, ComputeUnicodeHighlights(test.text, test.options), test.want)
			if got := Reveal(test.text); got != test.reveal {
				t.Errorf("Reveal(%q) = %q, want %q", test.text, got, test.reveal)
			}
		})
	}
}

func TestNonBasicASCIIOption(t *testing.T) {
	text := "中文测试"
	got := ComputeUnicodeHighlights(text, UnicodeHighlighterOptions{NonBasicASCII: true})
	want := []Highlight{
		{Start: 0, End: 3, Kind: HighlightNonBasicASCII, CodePoint: '中'},
		{Start: 3, End: 6, Kind: HighlightNonBasicASCII, CodePoint: '文'},
		{Start: 6, End: 9, Kind: HighlightNonBasicASCII, CodePoint: '测'},
		{Start: 9, End: 12, Kind: HighlightNonBasicASCII, CodePoint: '试'},
	}
	assertHighlights(t, got, want)
}

func assertHighlights(t *testing.T, got, want []Highlight) {
	t.Helper()
	if len(got) != len(want) {
		t.Fatalf("got %d highlights %v, want %d %v", len(got), got, len(want), want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("highlight %d = %+v, want %+v", i, got[i], want[i])
		}
	}
}
