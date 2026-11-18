package fuzzy

import (
	"cmp"
	"slices"
)

// FuzzyScore corresponds to fuzzyScorer.ts's FuzzyScore tuple: an overall
// score and the matched rune positions in the target, in ascending order.
type FuzzyScore struct {
	Score   int
	Matches []int
}

func noFuzzyScore() FuzzyScore {
	return FuzzyScore{Matches: []int{}}
}

// ScoreFuzzy corresponds to scoreFuzzy in fuzzyScorer.ts. queryLower must be
// the rune-by-rune lowercase form of query. A non-match returns a zero score
// and no positions.
func ScoreFuzzy(target, query, queryLower string, allowNonContiguousMatches bool) FuzzyScore {
	if target == "" || query == "" {
		return noFuzzyScore()
	}

	targetRunes := []rune(target)
	queryRunes := []rune(query)
	targetLength := len(targetRunes)
	queryLength := len(queryRunes)
	if targetLength < queryLength {
		return noFuzzyScore()
	}

	targetLower := lowerRunes(targetRunes)
	queryLowerRunes := []rune(queryLower)
	return doScoreFuzzy(queryRunes, queryLowerRunes, queryLength, targetRunes, targetLower, targetLength, allowNonContiguousMatches)
}

func doScoreFuzzy(query, queryLower []rune, queryLength int, target, targetLower []rune, targetLength int, allowNonContiguousMatches bool) FuzzyScore {
	scores := make([]int, queryLength*targetLength)
	matches := make([]int, queryLength*targetLength)

	for queryIndex := 0; queryIndex < queryLength; queryIndex++ {
		queryOffset := queryIndex * targetLength
		previousOffset := queryOffset - targetLength

		for targetIndex := 0; targetIndex < targetLength; targetIndex++ {
			leftScore := 0
			if targetIndex > 0 {
				leftScore = scores[queryOffset+targetIndex-1]
			}
			diagScore := 0
			sequenceLength := 0
			if queryIndex > 0 && targetIndex > 0 {
				diagIndex := previousOffset + targetIndex - 1
				diagScore = scores[diagIndex]
				sequenceLength = matches[diagIndex]
			}

			score := 0
			if diagScore != 0 || queryIndex == 0 {
				score = computeCharScore(query[queryIndex], queryLower[queryIndex], target, targetLower, targetIndex, sequenceLength)
			}

			validScore := score != 0 && diagScore+score >= leftScore
			if validScore && (allowNonContiguousMatches || queryIndex > 0 || startsWithAt(targetLower, queryLower, targetIndex)) {
				current := queryOffset + targetIndex
				matches[current] = sequenceLength + 1
				scores[current] = diagScore + score
			} else {
				matches[queryOffset+targetIndex] = 0
				scores[queryOffset+targetIndex] = leftScore
			}
		}
	}

	positions := make([]int, 0, queryLength)
	queryIndex := queryLength - 1
	for targetIndex := targetLength - 1; queryIndex >= 0 && targetIndex >= 0; targetIndex-- {
		if matches[queryIndex*targetLength+targetIndex] == 0 {
			continue
		}
		positions = append(positions, targetIndex)
		queryIndex--
	}
	slices.Reverse(positions)

	return FuzzyScore{Score: scores[queryLength*targetLength-1], Matches: positions}
}

func computeCharScore(queryChar, queryLowerChar rune, target, targetLower []rune, targetIndex, sequenceLength int) int {
	if !considerAsEqual(queryLowerChar, targetLower[targetIndex]) {
		return 0
	}

	score := 1
	if sequenceLength > 0 {
		score += min(sequenceLength, 3)*6 + max(0, sequenceLength-3)*3
	}
	if queryChar == target[targetIndex] {
		score++
	}

	if targetIndex == 0 {
		score += 8
	} else if bonus := scoreSeparatorAtPos(target[targetIndex-1]); bonus != 0 {
		score += bonus
	} else if isUpper(target[targetIndex]) && sequenceLength == 0 {
		score += 2
	}
	return score
}

func considerAsEqual(a, b rune) bool {
	if a == b {
		return true
	}
	// Path separators are interchangeable across platforms.
	return (a == '/' || a == '\\') && (b == '/' || b == '\\')
}

func scoreSeparatorAtPos(r rune) int {
	switch r {
	case '/', '\\':
		return 5
	case '_', '-', '.', ' ', '\'', '"', ':':
		return 4
	default:
		return 0
	}
}

func startsWithAt(value, prefix []rune, start int) bool {
	if start+len(prefix) > len(value) {
		return false
	}
	for i, r := range prefix {
		if value[start+i] != r {
			return false
		}
	}
	return true
}

// FuzzyScore2 corresponds to fuzzyScorer.ts's FuzzyScore2 tuple. HasScore
// replaces the tuple's undefined first item; Matches holds rune ranges.
type FuzzyScore2 struct {
	Score    int
	HasScore bool
	Matches  []Match
}

func noFuzzyScore2() FuzzyScore2 {
	return FuzzyScore2{Matches: []Match{}}
}

// ScoreFuzzy2 corresponds to scoreFuzzy2 with an IPreparedQuery. Use
// ScoreFuzzy2Piece for VS Code's single-piece overload.
func ScoreFuzzy2(target string, query IPreparedQuery, patternStart, wordStart int) FuzzyScore2 {
	if len(query.Values) > 1 {
		return doScoreFuzzy2Multiple(target, query.Values, patternStart, wordStart)
	}
	return doScoreFuzzy2Single(target, query.IPreparedQueryPiece, patternStart, wordStart)
}

// ScoreFuzzy2Piece corresponds to scoreFuzzy2 when passed an
// IPreparedQueryPiece; Go has no union type for the two overloads.
func ScoreFuzzy2Piece(target string, query IPreparedQueryPiece, patternStart, wordStart int) FuzzyScore2 {
	return doScoreFuzzy2Single(target, query, patternStart, wordStart)
}

func doScoreFuzzy2Multiple(target string, query []IPreparedQueryPiece, patternStart, wordStart int) FuzzyScore2 {
	totalScore := 0
	totalMatches := make([]Match, 0)
	for _, piece := range query {
		result := doScoreFuzzy2Single(target, piece, patternStart, wordStart)
		if !result.HasScore {
			return noFuzzyScore2()
		}
		totalScore += result.Score
		totalMatches = append(totalMatches, result.Matches...)
	}
	// All pieces must match; normalize so the ranges are sorted and distinct.
	slices.SortFunc(totalMatches, func(a, b Match) int { return cmp.Compare(a.Start, b.Start) })
	return FuzzyScore2{Score: totalScore, HasScore: true, Matches: normalizeMatches(totalMatches)}
}

func doScoreFuzzy2Single(target string, query IPreparedQueryPiece, patternStart, wordStart int) FuzzyScore2 {
	score := fuzzyScore(query.Normalized, query.NormalizedLowercase, patternStart, target, lowerString(target), wordStart, filterScoreOptions{
		FirstMatchCanBeWeak: true,
		BoostFullMatch:      true,
	})
	if score == nil {
		return noFuzzyScore2()
	}
	return FuzzyScore2{Score: score.Score, HasScore: true, Matches: createFilterMatches(*score)}
}
