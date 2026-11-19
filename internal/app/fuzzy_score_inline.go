package app

import (
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/baba983/vscroxy/internal/fuzzy"
)

// This is the quick-open scorer from internal/fuzzy/fuzzy.go with fixed-size
// workspaces and per-pass text caches. The Go port allocates two query x
// target matrices and lowercases the same label again for every keystroke;
// on a thousand-row timeline that dominates the frame. The algorithm and rune
// offsets are the same, so highlights remain VS Code's.
const (
	fuzzyInlineQueryRunes  = 24
	fuzzyInlineTargetRunes = 128
)

type fuzzyInlineScore struct {
	score     int
	positions []int
}

type fuzzyScoreTarget struct {
	text      string
	runes     []rune
	lower     []rune
	lowerText string
}

// fuzzyScoreState keeps lowered labels and query pieces for one scoring pass;
// repeated event names then cost a map lookup instead of two conversions.
type fuzzyScoreState struct {
	targets   map[string]fuzzyScoreTarget
	patterns  map[string][]rune
	originals map[string][]rune
	previous  [fuzzyInlineTargetRunes]int
	current   [fuzzyInlineTargetRunes]int
	minPos    [fuzzyInlineQueryRunes]int
	maxPos    [fuzzyInlineQueryRunes]int
	sequence  [fuzzyInlineQueryRunes][fuzzyInlineTargetRunes]uint8
}

func (s *fuzzyScoreState) target(text string) fuzzyScoreTarget {
	if s == nil {
		runes := []rune(text)
		lower := lowerRunesInline(runes)
		return fuzzyScoreTarget{text: text, runes: runes, lower: lower, lowerText: string(lower)}
	}
	if hit, ok := s.targets[text]; ok {
		return hit
	}
	runes := []rune(text)
	lower := lowerRunesInline(runes)
	target := fuzzyScoreTarget{text: text, runes: runes, lower: lower, lowerText: string(lower)}
	if s.targets == nil {
		s.targets = make(map[string]fuzzyScoreTarget, 1024)
	}
	s.targets[text] = target
	return target
}

func (s *fuzzyScoreState) pattern(text string) []rune {
	if s == nil {
		return lowerRunesInline([]rune(text))
	}
	if hit, ok := s.patterns[text]; ok {
		return hit
	}
	runes := lowerRunesInline([]rune(text))
	if s.patterns == nil {
		s.patterns = make(map[string][]rune, 4)
	}
	s.patterns[text] = runes
	return runes
}

func (s *fuzzyScoreState) original(text string) []rune {
	if s == nil {
		return []rune(text)
	}
	if hit, ok := s.originals[text]; ok {
		return hit
	}
	runes := []rune(text)
	if s.originals == nil {
		s.originals = make(map[string][]rune, 4)
	}
	s.originals[text] = runes
	return runes
}

func fuzzyScoreInline(target, query string) fuzzyInlineScore {
	state := &fuzzyScoreState{}
	t := state.target(target)
	return state.scoreRunes(t, []rune(query), state.pattern(query))
}

// scoreRunes corresponds to fuzzy.ScoreFuzzy with
// allowNonContiguousMatches=true. Longer queries use the general scorer.
func (s *fuzzyScoreState) scoreRunes(target fuzzyScoreTarget, query, queryLower []rune) fuzzyInlineScore {
	if len(query) == 0 || len(target.runes) == 0 || len(query) > len(target.runes) ||
		len(query) > fuzzyInlineQueryRunes || len(target.runes) > fuzzyInlineTargetRunes {
		score := fuzzy.ScoreFuzzy(target.text, string(query), string(queryLower), true)
		return fuzzyInlineScore{score: score.Score, positions: score.Matches}
	}
	targetRunes, targetLower := target.runes, target.lower

	// A contiguous hit is common in a literal query. Its match runs are the
	// same one range VS Code would return, and it needs no score matrix.
	if asciiRunes(queryLower) {
		needle := string(queryLower)
		if at := strings.Index(target.lowerText, needle); at >= 0 && !strings.ContainsAny(needle, " \t") {
			start := utf8.RuneCountInString(target.lowerText[:at])
			score := 0
			for i := range query {
				score += inlineCharScore(query[i], queryLower[i], targetRunes, targetLower, start+i, i)
			}
			return fuzzyInlineScore{score: score, positions: positionsRange(start, len(query))}
		}
	}

	// filterScore bounds each pattern rune to the span in which it can occur;
	// the full matrix is unnecessary for a match decision and its highlights.
	minPos, maxPos := s.minPos[:len(query)], s.maxPos[:len(query)]
	inlineBounds(queryLower, targetLower, minPos, maxPos)
	for q := range query {
		if minPos[q] > maxPos[q] || minPos[q] >= len(targetRunes) {
			return fuzzyInlineScore{}
		}
	}
	previous, current := &s.previous, &s.current
	for q := range query {
		stop := len(targetRunes)
		if q+1 < len(query) {
			stop = maxPos[q+1]
		}
		clear(current[minPos[q]:stop])
		clear(s.sequence[q][minPos[q]:stop])
		for t := minPos[q]; t < stop; t++ {
			leftScore := 0
			if t > minPos[q] {
				leftScore = current[t-1]
			}
			diagScore, runLength := 0, 0
			if q > 0 && t > 0 && previous[t-1] != 0 {
				diagScore = previous[t-1]
				runLength = int(s.sequence[q-1][t-1])
			}
			positionScore := 0
			if (diagScore != 0 || q == 0) && t <= maxPos[q] {
				positionScore = inlineCharScore(query[q], queryLower[q], targetRunes, targetLower, t, runLength)
			}
			if positionScore != 0 && diagScore+positionScore >= leftScore {
				current[t] = diagScore + positionScore
				s.sequence[q][t] = uint8(runLength + 1)
			} else {
				s.sequence[q][t] = 0
				current[t] = leftScore
			}
		}
		previous, current = current, previous
	}
	best := previous[len(targetRunes)-1]
	if best == 0 {
		return fuzzyInlineScore{}
	}
	positions := make([]int, 0, len(query))
	t := len(targetRunes) - 1
	for q := len(query) - 1; q >= 0 && t >= 0; t-- {
		if s.sequence[q][t] == 0 {
			continue
		}
		positions = append(positions, t)
		q--
	}
	for i, j := 0, len(positions)-1; i < j; i, j = i+1, j-1 {
		positions[i], positions[j] = positions[j], positions[i]
	}
	return fuzzyInlineScore{score: best, positions: positions}
}

// inlineLabelMatches turns the scorer's ascending rune positions into
// contiguous ranges fitted to the drawn label, in one allocation.
func inlineLabelMatches(positions []int, label string) []fuzzy.Match {
	limit := utf8.RuneCountInString(label)
	out := make([]fuzzy.Match, 0, len(positions))
	for _, position := range positions {
		if position < 0 || position >= limit {
			continue
		}
		if last := len(out) - 1; last >= 0 && out[last].End == position {
			out[last].End++
			continue
		}
		out = append(out, fuzzy.Match{Start: position, End: position + 1})
	}
	return out
}

func asciiRunes(runes []rune) bool {
	for _, r := range runes {
		if r >= utf8.RuneSelf {
			return false
		}
	}
	return true
}

func positionsRange(start, count int) []int {
	out := make([]int, count)
	for i := range out {
		out[i] = start + i
	}
	return out
}

// inlineBounds is filters.ts's forward/backward subsequence scan: minPos[q]
// is the earliest place pattern[q] can match and maxPos[q] the latest.
func inlineBounds(pattern, word []rune, minPos, maxPos []int) {
	at := 0
	for i, r := range pattern {
		for at < len(word) && word[at] != r {
			at++
		}
		minPos[i] = at
		if at < len(word) {
			at++
		}
	}
	at = len(word) - 1
	for i := len(pattern) - 1; i >= 0; i-- {
		for at >= 0 && word[at] != pattern[i] {
			at--
		}
		maxPos[i] = at
		at--
	}
}

func lowerRunesInline(runes []rune) []rune {
	lower := make([]rune, len(runes))
	for i, r := range runes {
		lower[i] = unicode.ToLower(r)
	}
	return lower
}

func inlineCharScore(query, queryLower rune, target, targetLower []rune, index, runLength int) int {
	pathMatch := (queryLower == '/' || queryLower == '\\') && (targetLower[index] == '/' || targetLower[index] == '\\')
	if queryLower != targetLower[index] && !pathMatch {
		return 0
	}
	score := 1
	if runLength > 0 {
		score += min(runLength, 3)*6 + max(runLength-3, 0)*3
	}
	if query == target[index] {
		score++
	}
	if index == 0 {
		score += 8
	} else if bonus := inlineSeparatorBonus(target[index-1]); bonus != 0 {
		score += bonus
	} else if target[index] >= 'A' && target[index] <= 'Z' && runLength == 0 {
		score += 2
	}
	return score
}

func inlineSeparatorBonus(r rune) int {
	switch r {
	case '/', '\\':
		return 5
	case '_', '-', '.', ' ', '\'', '"', ':':
		return 4
	}
	return 0
}

// inlinePrefix is fuzzy.MatchesPrefix without converting ASCII labels and
// queries to runes; VS Code gives a contiguous prefix hit its own match runs.
func inlinePrefix(query, label string) []fuzzy.Match {
	if query == "" {
		return []fuzzy.Match{}
	}
	ascii := true
	for i := range query {
		if query[i] >= 0x80 {
			ascii = false
			break
		}
	}
	if !ascii {
		return fuzzy.MatchesPrefix(query, label)
	}
	if len(label) < len(query) {
		return nil
	}
	prefix := label[:len(query)]
	for i := range prefix {
		if prefix[i] >= utf8.RuneSelf {
			return nil
		}
	}
	if strings.EqualFold(prefix, query) {
		return []fuzzy.Match{{Start: 0, End: len(query)}}
	}
	return nil
}
