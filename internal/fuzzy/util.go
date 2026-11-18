package fuzzy

import "unicode"

func runeLen(s string) int {
	return len([]rune(s))
}

func lowerRunes(value []rune) []rune {
	lowered := make([]rune, len(value))
	for i, r := range value {
		lowered[i] = unicode.ToLower(r)
	}
	return lowered
}

func lowerString(s string) string {
	return string(lowerRunes([]rune(s)))
}

// utf16Len reports the length JavaScript would report for s. tryNormalizeToBase
// uses it to decide whether accent removal changed the string length.
func utf16Len(s string) int {
	length := 0
	for _, r := range s {
		if r > 0xFFFF {
			length += 2
		} else {
			length++
		}
	}
	return length
}
