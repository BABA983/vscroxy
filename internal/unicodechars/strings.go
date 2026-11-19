// Package unicodechars ports VS Code's Unicode data and highlight decisions
// so HTTP data can be checked for characters that look ordinary but are not.
// It has no dependency on MyGo. The tables in data_generated.go come from
// microsoft/vscode at the commit named in that file.
//
//go:generate go run ./gen
//go:generate python3 gen/names.py .
package unicodechars

import (
	"fmt"
	"maps"
	"slices"
	"strings"
	"sync"
	"unicode"
)

// InvisibleCharacters is the Go form of VS Code's strings.InvisibleCharacters.
// Its zero value is ready to use. The data is locale-independent in VS Code:
// getRawData's locale tables are flattened into one code point set.
type InvisibleCharacters struct{}

// IsInvisibleCharacter corresponds to strings.InvisibleCharacters.isInvisibleCharacter.
func (InvisibleCharacters) IsInvisibleCharacter(codePoint rune) bool {
	_, found := slices.BinarySearchFunc(invisibleRanges[:], codePoint, func(r runeRange, target rune) int {
		switch {
		case target < r.first:
			return 1
		case target > r.last:
			return -1
		default:
			return 0
		}
	})
	return found
}

// ContainsInvisibleCharacter corresponds to strings.InvisibleCharacters.containsInvisibleCharacter.
// Like VS Code, it also reports the ordinary space as invisible.
func (InvisibleCharacters) ContainsInvisibleCharacter(s string) bool {
	for _, codePoint := range s {
		if codePoint == ' ' || (InvisibleCharacters{}).IsInvisibleCharacter(codePoint) {
			return true
		}
	}
	return false
}

// CodePoints returns the VS Code strings.InvisibleCharacters.codePoints set as
// a sorted slice. The result is a copy; lookups use IsInvisibleCharacter.
func (InvisibleCharacters) CodePoints() []rune {
	count := 0
	for _, r := range invisibleRanges {
		count += int(r.last-r.first) + 1
	}
	points := make([]rune, 0, count)
	for _, r := range invisibleRanges {
		for codePoint := r.first; ; codePoint++ {
			points = append(points, codePoint)
			if codePoint == r.last {
				break
			}
		}
	}
	return points
}

// AmbiguousCharacters corresponds to a VS Code strings.AmbiguousCharacters
// instance. GetInstance returns the instance for a set of locales; the zero
// value is not a usable instance.
type AmbiguousCharacters struct {
	confusables map[rune]rune
}

var ambiguousInstances sync.Map

// GetInstance corresponds to strings.AmbiguousCharacters.getInstance. Locales
// not present in VS Code's data are ignored; when none remain, VS Code's
// "_default" table is used. Like VS Code's mergeMaps(commonMap,
// languageSpecificMap), a language-specific entry overrides a common one.
func GetInstance(locales []string) *AmbiguousCharacters {
	key := strings.Join(locales, ",")
	if cached, ok := ambiguousInstances.Load(key); ok {
		return cached.(*AmbiguousCharacters)
	}

	filtered := make([]string, 0, len(locales))
	for _, locale := range locales {
		if strings.HasPrefix(locale, "_") {
			continue
		}
		if _, ok := ambiguousData[locale]; ok {
			filtered = append(filtered, locale)
		}
	}
	if len(filtered) == 0 {
		filtered = []string{"_default"}
	}

	var confusables map[rune]rune
	for _, locale := range filtered {
		localeMap := make(map[rune]rune, len(ambiguousData[locale]))
		for _, pair := range ambiguousData[locale] {
			localeMap[pair.codePoint] = pair.primary
		}
		if confusables == nil {
			confusables = localeMap
			continue
		}
		for codePoint := range confusables {
			if _, ok := localeMap[codePoint]; !ok {
				delete(confusables, codePoint)
			}
		}
	}
	for _, pair := range ambiguousData["_common"] {
		if _, ok := confusables[pair.codePoint]; !ok {
			confusables[pair.codePoint] = pair.primary
		}
	}

	instance := &AmbiguousCharacters{confusables: confusables}
	actual, _ := ambiguousInstances.LoadOrStore(key, instance)
	return actual.(*AmbiguousCharacters)
}

var ambiguousLocales = sync.OnceValue(func() []string {
	locales := make([]string, 0, len(ambiguousData))
	for locale := range ambiguousData {
		if !strings.HasPrefix(locale, "_") {
			locales = append(locales, locale)
		}
	}
	slices.Sort(locales)
	return locales
})

// GetLocales corresponds to strings.AmbiguousCharacters.getLocales.
func GetLocales() []string {
	return ambiguousLocales()
}

// IsAmbiguous corresponds to strings.AmbiguousCharacters.isAmbiguous.
func (a *AmbiguousCharacters) IsAmbiguous(codePoint rune) bool {
	_, ok := a.confusables[codePoint]
	return ok
}

// ContainsAmbiguousCharacter corresponds to strings.AmbiguousCharacters.containsAmbiguousCharacter.
func (a *AmbiguousCharacters) ContainsAmbiguousCharacter(s string) bool {
	for _, codePoint := range s {
		if a.IsAmbiguous(codePoint) {
			return true
		}
	}
	return false
}

// GetPrimaryConfusable corresponds to strings.AmbiguousCharacters.getPrimaryConfusable.
// VS Code returns undefined when there is no confusable; Go reports that with
// the second result.
func (a *AmbiguousCharacters) GetPrimaryConfusable(codePoint rune) (rune, bool) {
	primary, ok := a.confusables[codePoint]
	return primary, ok
}

// GetConfusableCodePoints corresponds to strings.AmbiguousCharacters.getConfusableCodePoints.
func (a *AmbiguousCharacters) GetConfusableCodePoints() []rune {
	return slices.Sorted(maps.Keys(a.confusables))
}

// EscapeControlAndFormatCharacters corresponds to
// strings.escapeControlAndFormatCharacters: it writes control (Cc), format
// (Cf) and line/paragraph separator (Zl/Zp) code points as \u{...} escapes.
func EscapeControlAndFormatCharacters(s string) string {
	var result strings.Builder
	for _, codePoint := range s {
		if unicode.Is(unicode.Cc, codePoint) || unicode.Is(unicode.Cf, codePoint) ||
			unicode.Is(unicode.Zl, codePoint) || unicode.Is(unicode.Zp, codePoint) {
			fmt.Fprintf(&result, `\u{%x}`, codePoint)
			continue
		}
		result.WriteRune(codePoint)
	}
	return result.String()
}

// IsBasicASCII corresponds to strings.isBasicASCII.
func IsBasicASCII(s string) bool {
	for _, codePoint := range s {
		if !isBasicASCIIRune(codePoint) {
			return false
		}
	}
	return true
}

func isBasicASCIIRune(codePoint rune) bool {
	return codePoint == '\t' || codePoint == '\n' || codePoint == '\r' ||
		(codePoint >= 0x20 && codePoint <= 0x7e)
}
