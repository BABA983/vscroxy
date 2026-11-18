package fuzzy

import (
	"cmp"
	"slices"
)

// Match is a highlight range in rune offsets, covering [Start, End).
type Match struct {
	Start int
	End   int
}

// CreateMatches corresponds to createMatches in fuzzyScorer.ts: it turns a
// sorted list of matched rune positions into contiguous ranges.
func CreateMatches(positions []int) []Match {
	matches := make([]Match, 0, len(positions))
	for _, position := range positions {
		last := len(matches) - 1
		if last >= 0 && matches[last].End == position {
			matches[last].End = position + 1
		} else {
			matches = append(matches, Match{Start: position, End: position + 1})
		}
	}
	return matches
}

func createFilterMatches(score filterFuzzyScore) []Match {
	positions := make([]int, len(score.Matches))
	for i, position := range score.Matches {
		positions[i] = position + score.WordStart
	}
	slices.Reverse(positions)
	return CreateMatches(positions)
}

// normalizeMatches corresponds to fuzzyScorer.ts's normalizeMatches: it sorts
// by start and merges overlapping ranges.
func normalizeMatches(matches []Match) []Match {
	slices.SortFunc(matches, func(a, b Match) int {
		return cmp.Compare(a.Start, b.Start)
	})
	normalized := make([]Match, 0, len(matches))
	for _, match := range matches {
		if len(normalized) == 0 || !matchOverlaps(normalized[len(normalized)-1], match) {
			normalized = append(normalized, match)
			continue
		}
		last := &normalized[len(normalized)-1]
		last.Start = min(last.Start, match.Start)
		last.End = max(last.End, match.End)
	}
	return normalized
}

func matchOverlaps(a, b Match) bool {
	return a.End >= b.Start && b.End >= a.Start
}
