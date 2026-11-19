package fuzzy

// ScoreScratch reuses the matrices and lowered runes that scoreFuzzy needs.
// It is useful when many labels are scored with the same query: the scorer is
// unchanged, but a list no longer allocates two matrices and a lowercase
// copy of every label on every keystroke. Like FuzzyScorerCache, it is not
// safe for concurrent use; keep one scratch per goroutine or per UI pass.
type ScoreScratch struct {
	scores  []int
	matches []int
	runes   map[string][]rune
	lowered map[string][]rune
}

func (s *ScoreScratch) runeSlice(target string) []rune {
	if s == nil {
		return []rune(target)
	}
	if hit, ok := s.runes[target]; ok {
		return hit
	}
	value := []rune(target)
	if s.runes == nil {
		s.runes = make(map[string][]rune)
	}
	s.runes[target] = value
	return value
}

func (s *ScoreScratch) loweredRunes(target string, runes []rune) []rune {
	if s == nil {
		return lowerRunes(runes)
	}
	if hit, ok := s.lowered[target]; ok {
		return hit
	}
	value := lowerRunes(runes)
	if s.lowered == nil {
		s.lowered = make(map[string][]rune)
	}
	s.lowered[target] = value
	return value
}

func (s *ScoreScratch) matrices(size int) ([]int, []int) {
	if s == nil {
		return make([]int, size), make([]int, size)
	}
	if cap(s.scores) < size {
		s.scores = make([]int, size)
		s.matches = make([]int, size)
	} else {
		s.scores, s.matches = s.scores[:size], s.matches[:size]
	}
	clear(s.scores)
	clear(s.matches)
	return s.scores, s.matches
}
