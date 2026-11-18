package fuzzy

import (
	"strings"

	"golang.org/x/text/unicode/norm"
)

// tryNormalizeToBase corresponds to normalization.ts's tryNormalizeToBase:
// NFD, strip combining accents, then lowercase, but only when stripping did
// not change the JavaScript string length. VS Code's bounded memoization is
// omitted; the result is the same.
func tryNormalizeToBase(s string) string {
	var builder strings.Builder
	for _, r := range norm.NFD.String(s) {
		if r >= 0x0300 && r <= 0x036F {
			continue
		}
		builder.WriteRune(r)
	}
	noAccents := builder.String()
	if utf16Len(noAccents) == utf16Len(s) {
		return lowerString(noAccents)
	}
	return lowerString(s)
}
