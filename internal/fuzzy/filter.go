package fuzzy

import (
	"strings"
	"unicode"
)

// Filter corresponds to IFilter in filters.ts. A nil result means no match; a
// non-nil empty slice is a match with no highlighted runes, just as in VS Code.
type Filter func(word, wordToMatchAgainst string) []Match

// MatchesStrictPrefix corresponds to matchesStrictPrefix in filters.ts.
func MatchesStrictPrefix(word, wordToMatchAgainst string) []Match {
	return matchesPrefix(false, word, wordToMatchAgainst)
}

// MatchesPrefix corresponds to matchesPrefix in filters.ts and ignores case.
func MatchesPrefix(word, wordToMatchAgainst string) []Match {
	return matchesPrefix(true, word, wordToMatchAgainst)
}

func matchesPrefix(ignoreCase bool, word, wordToMatchAgainst string) []Match {
	if wordToMatchAgainst == "" || runeLen(wordToMatchAgainst) < runeLen(word) {
		return nil
	}
	matched := false
	if ignoreCase {
		matched = strings.HasPrefix(lowerString(wordToMatchAgainst), lowerString(word))
	} else {
		matched = strings.HasPrefix(wordToMatchAgainst, word)
	}
	if !matched {
		return nil
	}
	if word == "" {
		return []Match{}
	}
	return []Match{{Start: 0, End: runeLen(word)}}
}

// MatchesCamelCase corresponds to matchesCamelCase in filters.ts.
func MatchesCamelCase(word, camelCaseWord string) []Match {
	if camelCaseWord == "" {
		return nil
	}
	target := []rune(strings.TrimSpace(camelCaseWord))
	if len(target) == 0 {
		return nil
	}

	pattern := []rune(word)
	if !isCamelCasePattern(pattern) {
		return nil
	}
	if len(target) > 60 {
		target = target[:60]
	}
	analysis := analyzeCamelCaseWord(target)
	if !isCamelCaseWord(analysis) {
		if !isUpperCaseWord(analysis) {
			return nil
		}
		target = lowerRunes(target)
	}

	pattern = lowerRunes(pattern)
	var result []Match
	for i := 0; i < len(target) && result == nil; {
		result = matchesCamelCaseRec(pattern, target, 0, i)
		i = nextAnchor(target, i+1)
	}
	return result
}

// MatchesWords corresponds to matchesWords in filters.ts. It matches the
// beginnings of words, supporting non-ASCII text and Korean input codes.
func MatchesWords(word, target string) []Match {
	return MatchesWordsContiguous(word, target, false)
}

// MatchesWordsContiguous corresponds to matchesWords with an explicit
// `contiguous` argument; Go has no optional parameters.
func MatchesWordsContiguous(word, target string, contiguous bool) []Match {
	if target == "" {
		return nil
	}
	wordRunes := []rune(tryNormalizeToBase(word))
	targetRunes := []rune(tryNormalizeToBase(target))
	memo := make(map[int]*[]Match)
	var result []Match
	for targetIndex := 0; targetIndex < len(targetRunes); {
		result = matchesWordsRec(wordRunes, targetRunes, 0, targetIndex, contiguous, memo)
		if result != nil {
			break
		}
		targetIndex = nextWord(targetRunes, targetIndex+1)
	}
	return result
}

func isLower(r rune) bool {
	return r >= 'a' && r <= 'z'
}

func isUpper(r rune) bool {
	return r >= 'A' && r <= 'Z'
}

func isNumber(r rune) bool {
	return r >= '0' && r <= '9'
}

func isWhitespace(r rune) bool {
	return r == ' ' || r == '\t' || r == '\n' || r == '\r'
}

const wordSeparators = "()[]{}<>`\\'\"-/;:,.?!"

func isWordSeparator(r rune) bool {
	return isWhitespace(r) || strings.ContainsRune(wordSeparators, r)
}

func charactersMatch(a, b rune) bool {
	return a == b || (isWordSeparator(a) && isWordSeparator(b))
}

func isAlphanumeric(r rune) bool {
	return isLower(r) || isUpper(r) || isNumber(r)
}

// join corresponds to join in filters.ts.
func join(head Match, tail []Match) []Match {
	if len(tail) == 0 {
		return []Match{head}
	}
	if head.End == tail[0].Start {
		tail[0].Start = head.Start
	} else {
		tail = append([]Match{head}, tail...)
	}
	return tail
}

func nextAnchor(camelCaseWord []rune, start int) int {
	for i := start; i < len(camelCaseWord); i++ {
		r := camelCaseWord[i]
		if isUpper(r) || isNumber(r) || (i > 0 && !isAlphanumeric(camelCaseWord[i-1])) {
			return i
		}
	}
	return len(camelCaseWord)
}

func matchesCamelCaseRec(word, camelCaseWord []rune, i, j int) []Match {
	if i == len(word) {
		return []Match{}
	} else if j == len(camelCaseWord) {
		return nil
	} else if word[i] != unicode.ToLower(camelCaseWord[j]) {
		return nil
	}

	result := matchesCamelCaseRec(word, camelCaseWord, i+1, j+1)
	for nextUpperIndex := j + 1; result == nil; {
		anchor := nextAnchor(camelCaseWord, nextUpperIndex)
		if anchor >= len(camelCaseWord) {
			break
		}
		result = matchesCamelCaseRec(word, camelCaseWord, i+1, anchor)
		nextUpperIndex = anchor + 1
	}
	if result == nil {
		return nil
	}
	return join(Match{Start: j, End: j + 1}, result)
}

type camelCaseAnalysis struct {
	upperPercent   float64
	lowerPercent   float64
	alphaPercent   float64
	numericPercent float64
}

func analyzeCamelCaseWord(word []rune) camelCaseAnalysis {
	var upper, lowerCount, alpha, numeric int
	for _, r := range word {
		if isUpper(r) {
			upper++
		}
		if isLower(r) {
			lowerCount++
		}
		if isAlphanumeric(r) {
			alpha++
		}
		if isNumber(r) {
			numeric++
		}
	}
	length := float64(len(word))
	return camelCaseAnalysis{
		upperPercent:   float64(upper) / length,
		lowerPercent:   float64(lowerCount) / length,
		alphaPercent:   float64(alpha) / length,
		numericPercent: float64(numeric) / length,
	}
}

func isUpperCaseWord(a camelCaseAnalysis) bool {
	return a.lowerPercent == 0 && a.upperPercent > 0.6
}

func isCamelCaseWord(a camelCaseAnalysis) bool {
	return a.lowerPercent > 0.2 && a.upperPercent < 0.8 && a.alphaPercent > 0.6 && a.numericPercent < 0.2
}

func isCamelCasePattern(word []rune) bool {
	var upper, lowerCount, whitespace int
	for _, r := range word {
		if isUpper(r) {
			upper++
		}
		if isLower(r) {
			lowerCount++
		}
		if isWhitespace(r) {
			whitespace++
		}
	}
	if (upper == 0 || lowerCount == 0) && whitespace == 0 {
		return len(word) <= 30
	}
	return upper <= 5
}

func cloneMatches(matches []Match) []Match {
	if matches == nil {
		return nil
	}
	clone := make([]Match, len(matches))
	copy(clone, matches)
	return clone
}

func matchesWordsRec(word, target []rune, wordIndex, targetIndex int, contiguous bool, memo map[int]*[]Match) []Match {
	if wordIndex == len(word) {
		return []Match{}
	} else if targetIndex == len(target) {
		return nil
	}

	key := wordIndex*(len(target)+1) + targetIndex
	if cached, ok := memo[key]; ok {
		// join mutates the returned slice, so hand out a clone as VS Code does.
		return cloneMatches(*cached)
	}
	computed := matchesWordsCompute(word, target, wordIndex, targetIndex, contiguous, memo)
	stored := new([]Match)
	*stored = cloneMatches(computed)
	memo[key] = stored
	return computed
}

func matchesWordsCompute(word, target []rune, wordIndex, targetIndex int, contiguous bool, memo map[int]*[]Match) []Match {
	wordCode := word[wordIndex]
	targetCode := target[targetIndex]
	alternates := getAlternateCodes(wordCode)
	targetIndexOffset := 0

	if !charactersMatch(wordCode, targetCode) {
		if alternates == nil {
			return nil
		}
		for k, code := range alternates {
			if targetIndex+k >= len(target) || !charactersMatch(code, target[targetIndex+k]) {
				return nil
			}
		}
		targetIndexOffset = len(alternates) - 1
	}

	nextIndex := targetIndex + targetIndexOffset + 1
	result := matchesWordsRec(word, target, wordIndex+1, nextIndex, contiguous, memo)
	if !contiguous {
		for result == nil && nextIndex < len(target) {
			nextIndex = nextWord(target, nextIndex)
			if nextIndex >= len(target) {
				break
			}
			result = matchesWordsRec(word, target, wordIndex+1, nextIndex, contiguous, memo)
			nextIndex++
		}
	}
	if result == nil {
		return nil
	}

	// Word separators match each other but must not be highlighted.
	if wordCode != targetCode {
		if alternates == nil {
			return result
		}
		for k, code := range alternates {
			if targetIndex+k >= len(target) || code != target[targetIndex+k] {
				return result
			}
		}
	}
	return join(Match{Start: targetIndex, End: targetIndex + targetIndexOffset + 1}, result)
}

func nextWord(word []rune, start int) int {
	for i := start; i < len(word); i++ {
		if isWordSeparator(word[i]) || (i > 0 && isWordSeparator(word[i-1])) {
			return i
		}
	}
	return len(word)
}
