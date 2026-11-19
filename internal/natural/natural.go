// Package natural compares file and folder names the way a file explorer
// does: case-insensitively, with runs of digits compared as numbers.
package natural

import (
	"cmp"
	"unicode"
)

// Compare orders names case-insensitively, giving digit runs their numeric
// value, so req2 comes before req10. Runs of zeros and case differences do
// not break a tie; callers use a stable sort to keep the input order there,
// as VS Code's compareFileNames does for names its collator cannot tell
// apart. Non-digit characters are compared by code point after folding.
func Compare(one, other string) int {
	a, b := []rune(one), []rune(other)
	i, j := 0, 0
	for i < len(a) && j < len(b) {
		x, y := unicode.ToLower(a[i]), unicode.ToLower(b[j])
		if isDigit(x) && isDigit(y) {
			startA, startB := i, j
			for i < len(a) && isDigit(unicode.ToLower(a[i])) {
				i++
			}
			for j < len(b) && isDigit(unicode.ToLower(b[j])) {
				j++
			}
			na := trimZeros(a[startA:i])
			nb := trimZeros(b[startB:j])
			if compared := cmp.Compare(len(na), len(nb)); compared != 0 {
				return compared
			}
			if compared := cmp.Compare(na, nb); compared != 0 {
				return compared
			}
			continue
		}
		if compared := cmp.Compare(x, y); compared != 0 {
			return compared
		}
		i++
		j++
	}
	return cmp.Compare(len(a)-i, len(b)-j)
}

func isDigit(r rune) bool { return r >= '0' && r <= '9' }

func trimZeros(runes []rune) string {
	start := 0
	for start < len(runes) && runes[start] == '0' {
		start++
	}
	return string(runes[start:])
}
