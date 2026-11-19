package app

import (
	"slices"
	"strconv"
	"strings"
	"unicode/utf8"

	"github.com/baba983/vscroxy/internal/fuzzy"
	"github.com/baba983/vscroxy/internal/uikit"
	"github.com/egoist/mygo/ui"
)

// The name lists here follow two VS Code behaviors, both from commit
// 972b66fdc541e10689adfd90ade9549dea1b18bc:
//
//   - Tree and quick-pick find (src/vs/base/browser/ui/tree/abstractTree.ts's
//     FindFilter and src/vs/platform/quickinput/browser/quickInputList.ts)
//     scores labels in place and highlights the matched ranges. The rows keep
//     their source order; a stream stays a stream and a tree stays a tree.
//   - Quick open and IntelliSense (src/vs/base/common/fuzzyScorer.ts's
//     scoreItemFuzzy/compareItemsByFuzzyScore, used by both the palette in this
//     app and src/vs/editor/contrib/suggest/browser/suggestModel.ts) rank
//     candidates by the score. Only the query bar's value suggestions use that
//     path here.
//
// Matching goes through internal/fuzzy rather than a strings.Contains loop:
// "lsttm3" finds "List items 3" and highlights the letters it used. Scoring
// only reads a short head of each field, so a 64 KiB payload cannot turn one
// keystroke into a large scoring matrix. The query's result list is cached for
// repeated frames, as paletteAssetsFor does; a source generation is part of
// the key, so an appended event invalidates it.

const (
	fuzzyListCacheLimit = 24
	fuzzyListFieldRunes = 128 // VS Code's fuzzyScore also scores at most 128 runes
)

type fuzzyListKey struct {
	scope  string
	source string
	query  string
}

type fuzzyListHit struct {
	index   int
	score   int
	matches []fuzzy.Match // rune ranges of the displayed label
}

type fuzzyListCandidate struct {
	hit   fuzzyListHit
	item  fuzzyListItem
	score *fuzzy.IItemScore
}

// fuzzyListItem is the text one candidate scores. detail may hold a payload
// or a URL: it is searchable, but only label matches are drawn as highlights,
// matching quickInputList's separate label/description/detail highlights.
type fuzzyListItem struct {
	label  string
	detail string
}

type fuzzyListAccessor struct{}

func (fuzzyListAccessor) GetItemLabel(item fuzzyListItem) string { return item.label }

func (fuzzyListAccessor) GetItemDescription(item fuzzyListItem) (string, bool) {
	return item.detail, item.detail != ""
}

func (fuzzyListAccessor) GetItemPath(fuzzyListItem) (string, bool) { return "", false }

// fuzzySource is the generation of an append-only event list. Replacing the
// list with the same length and edge sequence numbers is rare; where it can
// happen the caller adds a session key or a counter of its own.
func fuzzySource(count, first, last int) string {
	return strconv.Itoa(count) + ":" + strconv.Itoa(first) + ":" + strconv.Itoa(last)
}

// fuzzyListHead returns the first limit runes without converting the rest of
// a possibly very large payload to a rune slice.
func fuzzyListHead(s string, limit int) string {
	runes := 0
	for i := range s {
		if runes == limit {
			return s[:i]
		}
		runes++
	}
	return s
}

func fuzzyListPossible(item fuzzyListItem, query fuzzy.IPreparedQuery) bool {
	words := item.label + " " + item.detail
	pieces := query.Values
	if len(pieces) == 0 {
		pieces = []fuzzy.IPreparedQueryPiece{query.IPreparedQueryPiece}
	}
	for _, piece := range pieces {
		if !fuzzyPatternInWord(piece.NormalizedLowercase, words) {
			return false
		}
	}
	return true
}

// fuzzyPatternInWord is the byte-level prefilter from filters.ts's
// isPatternInWord. An ASCII needle is scanned without lowercasing the whole
// haystack; a Unicode needle or a Unicode code unit takes the same ToLower
// path as fuzzy.PatternInWord.
func fuzzyPatternInWord(pattern, word string) bool {
	asciiPattern, asciiWord := true, true
	for i := range pattern {
		if pattern[i] >= utf8.RuneSelf {
			asciiPattern = false
			break
		}
	}
	if !asciiPattern {
		return fuzzy.PatternInWord(pattern, strings.ToLower(word))
	}
	at := 0
	for i := range word {
		if word[i] >= utf8.RuneSelf {
			asciiWord = false
			break
		}
		if at < len(pattern) && lowerASCII(word[i]) == pattern[at] {
			at++
		}
	}
	if asciiWord {
		return at == len(pattern)
	}
	return fuzzy.PatternInWord(pattern, strings.ToLower(word))
}

// fuzzyListLiteral keeps full-text searches working beyond the scored head.
// The old filters were substring searches, so a literal hit in a long payload
// must still narrow the list; its row just has no highlight to draw.
func fuzzyListLiteral(item fuzzyListItem, query fuzzy.IPreparedQuery) bool {
	needles := []string{query.OriginalLowercase, query.NormalizedLowercase}
	for _, needle := range needles {
		if needle == "" {
			continue
		}
		if fuzzyContainsFold(item.label, needle) || fuzzyContainsFold(item.detail, needle) {
			return true
		}
		// The old haystacks put label and detail next to each other, so a
		// phrase can straddle them. Only phrases need that extra scan.
		if strings.ContainsAny(needle, " \t") && fuzzyContainsFold(item.label+" "+item.detail, needle) {
			return true
		}
	}
	return false
}

// fuzzyContainsFold is strings.Contains(strings.ToLower(text), needle) without
// copying every retained payload to lower it on each keystroke. Unicode text
// takes the original ToLower path; ASCII text is scanned in place.
func fuzzyContainsFold(text, needle string) bool {
	ascii := true
	for i := range needle {
		if needle[i] >= utf8.RuneSelf {
			ascii = false
			break
		}
	}
	if !ascii {
		return strings.Contains(strings.ToLower(text), needle)
	}
	if strings.Contains(text, needle) {
		return true
	}
	nonASCII := false
	for start := range len(text) - len(needle) + 1 {
		match := true
		for j := range needle {
			c := text[start+j]
			if c >= utf8.RuneSelf {
				nonASCII = true
				match = false
				break
			}
			if lowerASCII(c) != needle[j] {
				match = false
				break
			}
		}
		if match {
			return true
		}
	}
	return nonASCII && strings.Contains(strings.ToLower(text), needle)
}

func lowerASCII(c byte) byte {
	if c >= 'A' && c <= 'Z' {
		return c + ('a' - 'A')
	}
	return c
}

func fuzzyListLabelMatches(matches []fuzzy.Match, label string) []fuzzy.Match {
	limit := len([]rune(label))
	out := make([]fuzzy.Match, 0, len(matches))
	for _, match := range matches {
		if match.Start < 0 || match.End > limit || match.End <= match.Start {
			continue
		}
		out = append(out, match)
	}
	return out
}

// fuzzyListScores scores one candidate for in-place list find. VS Code's
// FindFilter scores the keyboard navigation label; a payload hit from the
// app's older full-text search stays a literal hit with no label highlight.
func fuzzyListScores(item fuzzyListItem, query fuzzy.IPreparedQuery, cache fuzzy.FuzzyScorerCache) (int, []fuzzy.Match) {
	if score, matches := fuzzyListScoreText(item.label, query, cache); score != 0 {
		return score, matches
	}
	if fuzzyListLiteral(item, query) {
		return 1, nil
	}
	return 0, nil
}

// fuzzyListScoreText uses the palette's quick-open scorer (fuzzyScorer.ts's
// scoreFuzzy) and its per-query cache. The Go port's fuzzyScore2 needs fixed
// 129x129 scratch tables per call, which is far too much to allocate for
// every row of a thousand-row timeline; scoreFuzzy also returns VS Code's
// matched rune positions, and the cache reuses a repeated label's score.
func fuzzyListScoreText(text string, query fuzzy.IPreparedQuery, cache fuzzy.FuzzyScorerCache) (int, []fuzzy.Match) {
	score := fuzzy.ScoreItemFuzzy(fuzzyListItem{label: fuzzyListHead(text, fuzzyListFieldRunes)},
		query, true, fuzzyListAccessor{}, cache)
	if score.Score == 0 {
		return 0, nil
	}
	return score.Score, fuzzyListLabelMatches(score.LabelMatch, text)
}

func fuzzyShiftMatches(matches []fuzzy.Match, by int) []fuzzy.Match {
	out := make([]fuzzy.Match, len(matches))
	for i, match := range matches {
		out[i] = fuzzy.Match{Start: match.Start + by, End: match.End + by}
	}
	return out
}

// fuzzyTextMatches is the stateless scoring path for lists whose model owns
// the filter (a saved session's exchanges), where there is no app generation
// to key a result cache on. The prefilter and the scored head still bound the
// work per row to a short scan.
func fuzzyTextMatches(query, text string) bool {
	query = strings.TrimSpace(query)
	if query == "" {
		return true
	}
	prepared := fuzzy.PrepareQuery(query)
	item := fuzzyListItem{label: text}
	if !fuzzyListPossible(item, prepared) {
		return false
	}
	if score, _ := fuzzyListScoreText(text, prepared, fuzzy.NewFuzzyScorerCache()); score != 0 {
		return true
	}
	return fuzzyListLiteral(item, prepared)
}

// fuzzyList returns the matching source indices in display order, with the
// label ranges to highlight. ranked uses quick-open scoring and ordering;
// otherwise the source order is kept, as VS Code's find filters do.
func (a *app) fuzzyList(query, scope, source string, count int, item func(int) fuzzyListItem, ranked bool) []fuzzyListHit {
	if query == "" {
		hits := make([]fuzzyListHit, 0, count)
		for i := range count {
			hits = append(hits, fuzzyListHit{index: i})
		}
		return hits
	}
	key := fuzzyListKey{scope: scope, source: source, query: query}
	if cached, ok := a.fuzzyLists[key]; ok {
		return cached
	}
	if a.fuzzyLists == nil {
		a.fuzzyLists = make(map[fuzzyListKey][]fuzzyListHit)
	}
	if len(a.fuzzyLists) >= fuzzyListCacheLimit {
		clear(a.fuzzyLists)
	}

	prepared := fuzzy.PrepareQuery(query)
	cache := fuzzy.NewFuzzyScorerCache()
	if ranked {
		var candidates []fuzzyListCandidate
		for i := range count {
			entry := item(i)
			if !fuzzyListPossible(entry, prepared) && !fuzzyListLiteral(entry, prepared) {
				continue
			}
			score := fuzzy.ScoreItemFuzzy(entry, prepared, true, fuzzyListAccessor{}, cache)
			if score.Score == 0 && fuzzyListLiteral(entry, prepared) {
				score = &fuzzy.IItemScore{Score: 1}
			}
			if score.Score == 0 {
				continue
			}
			candidates = append(candidates, fuzzyListCandidate{
				hit: fuzzyListHit{index: i, score: score.Score,
					matches: fuzzyListLabelMatches(score.LabelMatch, entry.label)},
				item: entry, score: score,
			})
		}
		slices.SortStableFunc(candidates, func(x, y fuzzyListCandidate) int {
			return fuzzy.CompareItemScores(x.item, y.item, x.score, y.score, prepared, fuzzyListAccessor{})
		})
		hits := make([]fuzzyListHit, len(candidates))
		for i, candidate := range candidates {
			hits[i] = candidate.hit
		}
		a.fuzzyLists[key] = hits
		return hits
	}

	var hits []fuzzyListHit
	for i := range count {
		entry := item(i)
		if !fuzzyListPossible(entry, prepared) && !fuzzyListLiteral(entry, prepared) {
			continue
		}
		score, matches := fuzzyListScores(entry, prepared, cache)
		if score == 0 {
			continue
		}
		hits = append(hits, fuzzyListHit{index: i, score: score, matches: matches})
	}
	a.fuzzyLists[key] = hits
	return hits
}

// The row labels use the same highlight ink and the palette's span builder,
// so Light and Dark follow the theme and the text stays one readable label
// for assistive technology.
func fuzzyListBody(c *ui.Context, text string, matches []fuzzy.Match) ui.Element {
	if len(matches) == 0 {
		return uikit.Body(c, text)
	}
	spans := paletteSpans(text, matches, paletteHighlightInk(c, true), uikit.Scaled(uikit.TextBody))
	return ui.RichText(c, spans...).Label(text)
}
