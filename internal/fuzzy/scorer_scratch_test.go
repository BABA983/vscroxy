package fuzzy

import (
	"math/rand"
	"slices"
	"testing"
)

type scratchTestItem struct{ label string }

func (scratchTestItem) GetItemLabel(item scratchTestItem) string { return item.label }

func (scratchTestItem) GetItemDescription(scratchTestItem) (string, bool) { return "", false }

func (scratchTestItem) GetItemPath(scratchTestItem) (string, bool) { return "", false }

// TestScoreScratchIsEquivalent pins the reusable work buffers to the direct
// scorer, including accents, separators, mixed case and multi-piece queries.
func TestScoreScratchIsEquivalent(t *testing.T) {
	rng := rand.New(rand.NewSource(97266))
	letters := []rune("abcKLMxyzéÉ中/-_ .:012")
	scratch := &ScoreScratch{}
	queryScratch := &ScoreScratch{}

	for range 20000 {
		targetRunes := make([]rune, 1+rng.Intn(48))
		queryRunes := make([]rune, 1+rng.Intn(9))
		for i := range targetRunes {
			targetRunes[i] = letters[rng.Intn(len(letters))]
		}
		for i := range queryRunes {
			queryRunes[i] = letters[rng.Intn(len(letters))]
		}
		if rng.Intn(4) == 0 {
			queryRunes = append(queryRunes, ' ', letters[rng.Intn(len(letters))])
		}
		target, query := string(targetRunes), string(queryRunes)

		prepared := PrepareQuery(query)
		wantScore := ScoreFuzzy(target, query, query, true)
		gotScore := ScoreFuzzyWithScratch(target, query, query, true, scratch)
		if wantScore.Score != gotScore.Score || !slices.Equal(wantScore.Matches, gotScore.Matches) {
			t.Fatalf("ScoreFuzzy(%q, %q) = %d/%v, scratch = %d/%v",
				target, query, wantScore.Score, wantScore.Matches, gotScore.Score, gotScore.Matches)
		}

		item := scratchTestItem{label: target}
		wantItem := ScoreItemFuzzy(item, prepared, true, scratchTestItem{}, nil)
		gotItem := ScoreItemFuzzy(item, prepared, true, scratchTestItem{}, NewFuzzyScorerCache(), queryScratch)
		if wantItem.Score != gotItem.Score ||
			!slices.Equal(wantItem.LabelMatch, gotItem.LabelMatch) ||
			!slices.Equal(wantItem.DescriptionMatch, gotItem.DescriptionMatch) {
			t.Fatalf("ScoreItemFuzzy(%q, %q) = %+v, scratch = %+v", target, query, wantItem, gotItem)
		}
	}
}
