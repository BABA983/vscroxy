package unicodechars

import (
	"strings"
	"testing"
)

func BenchmarkComputeUnicodeHighlights(b *testing.B) {
	const size = 1 << 20
	ascii := "Bearer " + strings.Repeat("a", size-len("Bearer "))

	// Exactly 1 MiB: repeated ZWSP and Chinese mixed into ASCII, then padded.
	unit := "a\u200b中"
	mixed := strings.Repeat(unit, size/len(unit)) + strings.Repeat("a", size%len(unit))

	for _, test := range []struct {
		name string
		text string
	}{
		{"ascii token", ascii},
		{"mixed text", mixed},
	} {
		b.Run(test.name, func(b *testing.B) {
			b.SetBytes(int64(len(test.text)))
			b.ReportAllocs()
			for b.Loop() {
				_ = ComputeUnicodeHighlights(test.text, revealOptions)
			}
		})
	}
}
