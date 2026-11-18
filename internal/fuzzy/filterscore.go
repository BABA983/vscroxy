package fuzzy

import "math"

const maxFuzzyLen = 128

const (
	arrowDiag uint8 = iota + 1
	arrowLeft
	arrowLeftLeft
)

const noCharScore = math.MinInt

// filterFuzzyScore corresponds to the FuzzyScore tuple in filters.ts: the
// score, the offset at which matching started, and match positions relative
// to that start in reverse order. It is the input to createMatches.
type filterFuzzyScore struct {
	Score     int
	WordStart int
	Matches   []int
}

type filterScoreOptions struct {
	FirstMatchCanBeWeak bool
	BoostFullMatch      bool
}

// fuzzyScore corresponds to fuzzyScore in filters.ts. It is the IntelliSense
// scorer used by ScoreFuzzy2; nil means no match.
func fuzzyScore(pattern, patternLow string, patternStart int, word, lowWord string, wordStart int, options filterScoreOptions) *filterFuzzyScore {
	patternRunes := []rune(pattern)
	patternLowRunes := []rune(patternLow)
	wordRunes := []rune(word)
	wordLowRunes := []rune(lowWord)

	patternLen := min(len(patternRunes), maxFuzzyLen)
	wordLen := min(len(wordRunes), maxFuzzyLen)
	if patternStart >= patternLen || wordStart >= wordLen || patternLen-patternStart > wordLen-wordStart {
		return nil
	}

	// VS Code uses module-level scratch arrays here; local state keeps calls
	// independent and safe to run concurrently.
	var minWordMatchPos [2 * maxFuzzyLen]int
	var maxWordMatchPos [2 * maxFuzzyLen]int
	var diag [maxFuzzyLen + 1][maxFuzzyLen + 1]int
	var table [maxFuzzyLen + 1][maxFuzzyLen + 1]int
	var arrows [maxFuzzyLen + 1][maxFuzzyLen + 1]uint8

	if !isPatternInWord(patternLowRunes, patternStart, patternLen, wordLowRunes, wordStart, wordLen, &minWordMatchPos) {
		return nil
	}
	fillMaxWordMatchPos(patternLen, wordLen, patternStart, wordStart, patternLowRunes, wordLowRunes, &maxWordMatchPos)

	hasStrongFirstMatch := false
	row, column := 1, 1
	for patternPos := patternStart; patternPos < patternLen; row, patternPos = row+1, patternPos+1 {
		minPos := minWordMatchPos[patternPos]
		maxPos := maxWordMatchPos[patternPos]
		nextMaxPos := wordLen
		if patternPos+1 < patternLen {
			nextMaxPos = maxWordMatchPos[patternPos+1]
		}

		column = minPos - wordStart + 1
		for wordPos := minPos; wordPos < nextMaxPos; column, wordPos = column+1, wordPos+1 {
			score := noCharScore
			canComeDiag := false
			diagScore := 0
			if wordPos <= maxPos {
				var strong bool
				score, strong = doScore(patternRunes, patternLowRunes, patternPos, patternStart, wordRunes, wordLowRunes, wordPos, wordLen, wordStart, diag[row-1][column-1] == 0)
				if strong {
					hasStrongFirstMatch = true
				}
			}
			if score != noCharScore {
				canComeDiag = true
				diagScore = score + table[row-1][column-1]
			}

			canComeLeft := wordPos > minPos
			leftScore := 0
			if canComeLeft {
				leftScore = table[row][column-1]
				if diag[row][column-1] > 0 {
					leftScore -= 5 // penalty for a gap start
				}
			}

			canComeLeftLeft := wordPos > minPos+1 && diag[row][column-1] > 0
			leftLeftScore := 0
			if canComeLeftLeft {
				leftLeftScore = table[row][column-2]
				if diag[row][column-2] > 0 {
					leftLeftScore -= 5
				}
			}

			switch {
			case canComeLeftLeft && (!canComeLeft || leftLeftScore >= leftScore) && (!canComeDiag || leftLeftScore >= diagScore):
				// Prefer left-left: it jumps over a diagonal and matches earlier in the word.
				table[row][column] = leftLeftScore
				arrows[row][column] = arrowLeftLeft
			case canComeLeft && (!canComeDiag || leftScore >= diagScore):
				table[row][column] = leftScore
				arrows[row][column] = arrowLeft
			case canComeDiag:
				table[row][column] = diagScore
				arrows[row][column] = arrowDiag
				diag[row][column] = diag[row-1][column-1] + 1
			default:
				panic("fuzzyScore: not possible")
			}
		}
	}

	if !hasStrongFirstMatch && !options.FirstMatchCanBeWeak {
		return nil
	}

	row--
	column--
	result := &filterFuzzyScore{Score: table[row][column], WordStart: wordStart}
	backwardsDiagLength := 0
	maxMatchColumn := 0

	for row >= 1 {
		diagColumn := column
		for diagColumn >= 0 {
			switch arrows[row][diagColumn] {
			case arrowLeftLeft:
				diagColumn -= 2
				continue
			case arrowLeft:
				diagColumn--
				continue
			default:
				// Found the diagonal.
			}
			break
		}

		if backwardsDiagLength > 1 &&
			diagColumn >= 0 &&
			patternLowRunes[patternStart+row-1] == wordLowRunes[wordStart+column-1] &&
			!isUpperCaseAtPos(diagColumn+wordStart-1, wordRunes, wordLowRunes) &&
			backwardsDiagLength+1 > diag[row][diagColumn] {
			diagColumn = column
		}

		if diagColumn == column {
			backwardsDiagLength++
		} else {
			backwardsDiagLength = 1
		}
		if maxMatchColumn == 0 {
			maxMatchColumn = diagColumn
		}

		row--
		column = diagColumn - 1
		result.Matches = append(result.Matches, column)
	}

	if wordLen-wordStart == patternLen && options.BoostFullMatch {
		result.Score += 2
	}
	result.Score -= maxMatchColumn - patternLen
	return result
}

// PatternInWord corresponds to the cheap subsequence test in filters.ts's
// isPatternInWord. A false result means fuzzyScore cannot match; callers can
// use it to skip building the scoring matrix. Both strings must be lowercase.
// The scan is over UTF-8 bytes: for any rune that really occurs in the word,
// its bytes occur there too, so this may only over-approximate Unicode
// matches, never reject a match that the rune-based scorer would find.
func PatternInWord(patternLower, wordLower string) bool {
	patternPos, wordPos := 0, 0
	for patternPos < len(patternLower) && wordPos < len(wordLower) {
		if patternLower[patternPos] == wordLower[wordPos] {
			patternPos++
		}
		wordPos++
	}
	return patternPos == len(patternLower)
}

// isPatternInWord corresponds to isPatternInWord in filters.ts. It also fills
// minWordMatchPos, like the fillMinWordPosArr argument does in VS Code.
func isPatternInWord(patternLow []rune, patternPos, patternLen int, wordLow []rune, wordPos, wordLen int, minWordMatchPos *[2 * maxFuzzyLen]int) bool {
	for patternPos < patternLen && wordPos < wordLen {
		if patternLow[patternPos] == wordLow[wordPos] {
			if minWordMatchPos != nil {
				minWordMatchPos[patternPos] = wordPos
			}
			patternPos++
		}
		wordPos++
	}
	return patternPos == patternLen
}

func fillMaxWordMatchPos(patternLen, wordLen, patternStart, wordStart int, patternLow, wordLow []rune, maxWordMatchPos *[2 * maxFuzzyLen]int) {
	patternPos := patternLen - 1
	for wordPos := wordLen - 1; patternPos >= patternStart && wordPos >= wordStart; wordPos-- {
		if patternLow[patternPos] == wordLow[wordPos] {
			maxWordMatchPos[patternPos] = wordPos
			patternPos--
		}
	}
}

func doScore(pattern, patternLow []rune, patternPos, patternStart int, word, wordLow []rune, wordPos, wordLen, wordStart int, newMatchStart bool) (int, bool) {
	if patternLow[patternPos] != wordLow[wordPos] {
		return noCharScore, false
	}

	score := 1
	isGapLocation := false
	switch {
	case wordPos == patternPos-patternStart:
		// Common prefix: foobar <-> foobaz.
		if pattern[patternPos] == word[wordPos] {
			score = 7
		} else {
			score = 5
		}
	case isUpperCaseAtPos(wordPos, word, wordLow) && (wordPos == 0 || !isUpperCaseAtPos(wordPos-1, word, wordLow)):
		if pattern[patternPos] == word[wordPos] {
			score = 7
		} else {
			score = 5
		}
		isGapLocation = true
	case isSeparatorAtPos(wordLow, wordPos) && (wordPos == 0 || !isSeparatorAtPos(wordLow, wordPos-1)):
		score = 5
	case isSeparatorAtPos(wordLow, wordPos-1) || isWhitespaceAtPos(wordLow, wordPos-1):
		score = 5
		isGapLocation = true
	}
	strong := score > 1 && patternPos == patternStart

	if !isGapLocation {
		isGapLocation = isUpperCaseAtPos(wordPos, word, wordLow) || isSeparatorAtPos(wordLow, wordPos-1) || isWhitespaceAtPos(wordLow, wordPos-1)
	}

	if patternPos == patternStart {
		if wordPos > wordStart {
			score -= 3
			if !isGapLocation {
				score -= 2 // total penalty 5
			}
		}
	} else if newMatchStart {
		if isGapLocation {
			score += 2
		}
	} else if !isGapLocation {
		score++
	}

	if wordPos+1 == wordLen {
		score -= 3
		if !isGapLocation {
			score -= 2
		}
	}
	return score, strong
}

func isUpperCaseAtPos(pos int, word, wordLow []rune) bool {
	return pos >= 0 && pos < len(word) && word[pos] != wordLow[pos]
}

func isSeparatorAtPos(value []rune, index int) bool {
	if index < 0 || index >= len(value) {
		return false
	}
	switch r := value[index]; {
	case r == '_', r == '-', r == '.', r == ' ', r == '/', r == '\\', r == '\'', r == '"',
		r == ':', r == '$', r == '<', r == '>', r == '(', r == ')', r == '[', r == ']', r == '{', r == '}':
		return true
	default:
		return isEmojiImprecise(r)
	}
}

func isWhitespaceAtPos(value []rune, index int) bool {
	if index < 0 || index >= len(value) {
		return false
	}
	return value[index] == ' ' || value[index] == '\t'
}

// isEmojiImprecise corresponds to strings.isEmojiImprecise, used by
// isSeparatorAtPos in filters.ts.
func isEmojiImprecise(r rune) bool {
	return (r >= 0x1F1E6 && r <= 0x1F1FF) || r == 8986 || r == 8987 || r == 9200 ||
		r == 9203 || (r >= 9728 && r <= 10175) || r == 11088 || r == 11093 ||
		(r >= 127744 && r <= 128591) || (r >= 128640 && r <= 128764) ||
		(r >= 128992 && r <= 129008) || (r >= 129280 && r <= 129535) ||
		(r >= 129648 && r <= 129782)
}
