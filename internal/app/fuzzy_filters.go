package app

import (
	"fmt"
	"slices"
	"strconv"
	"strings"
	"unicode/utf8"

	"github.com/baba983/vscroxy/internal/fuzzy"
	"github.com/baba983/vscroxy/internal/linkedmap"
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
	scope string
	owner string
	query string
}

// fuzzyListRange is the sequence-number window a stream returned. Its edges
// let an appended event be scored on its own while the earlier matching rows
// are reused; a reset stream has a key of its own.
type fuzzyListRange struct {
	first int
	last  int
	count int
}

type fuzzyListCacheValue struct {
	window fuzzyListRange
	hits   []fuzzyListHit
}

type fuzzyListHit struct {
	index   int
	seq     int
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

// fuzzyListOwner identifies the document and the current session generation.
// A reconnect resets sequence numbers, so it must not share cached hits with
// the earlier session even when the two windows look identical.
func fuzzyListOwner(document any, generation uint64) string {
	return fmt.Sprintf("%p:%d", document, generation)
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
	pieces := query.Values
	if len(pieces) == 0 {
		pieces = []fuzzy.IPreparedQueryPiece{query.IPreparedQueryPiece}
	}
	for _, piece := range pieces {
		pattern := piece.NormalizedLowercase
		if fuzzyPatternInWord(pattern, item.label) || fuzzyPatternInWord(pattern, item.detail) {
			continue
		}
		// The old haystack put label and detail next to each other, so keep
		// that last resort but avoid the concatenation when the label alone
		// already qualifies.
		if !fuzzyPatternInWord(pattern, item.label+" "+item.detail) {
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
			continue
		}
		if at < len(pattern) && lowerASCII(word[i]) == pattern[at] {
			at++
		}
	}
	if at == len(pattern) {
		return true
	}
	if asciiWord {
		return false
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
func fuzzyListScores(item fuzzyListItem, query fuzzy.IPreparedQuery, state *fuzzyScoreState) (int, []fuzzy.Match) {
	if score, matches := fuzzyListScoreText(item.label, query, state); score != 0 {
		return score, matches
	}
	if fuzzyListLiteral(item, query) {
		return 1, nil
	}
	return 0, nil
}

// fuzzyListScoreText applies fuzzyScorer.ts's scoreFuzzy through the inline
// scorer, so a large list does not allocate a score matrix per row. A prefix
// hit uses VS Code's contiguous match ranges, as scoreItemFuzzy does.
func fuzzyListScoreText(text string, query fuzzy.IPreparedQuery, state *fuzzyScoreState) (int, []fuzzy.Match) {
	if state == nil {
		state = &fuzzyScoreState{}
	}
	head := fuzzyListHead(text, fuzzyListFieldRunes)
	target := state.target(head)
	pieces := query.Values
	if len(pieces) == 0 {
		pieces = []fuzzy.IPreparedQueryPiece{query.IPreparedQueryPiece}
	}
	total := 0
	var positions []int
	for _, piece := range pieces {
		score := state.scoreRunes(target, state.original(piece.Normalized), state.pattern(piece.Normalized))
		if score.score == 0 {
			return 0, nil
		}
		total += score.score
		positions = append(positions, score.positions...)
	}
	matches := inlineLabelMatches(positions, text)
	if len(pieces) == 1 {
		if prefix := inlinePrefix(query.Normalized, head); prefix != nil {
			matches = prefix
		}
	}
	return total, matches
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
	if score, _ := fuzzyListScoreText(text, prepared, nil); score != 0 {
		return true
	}
	return fuzzyListLiteral(item, prepared)
}

func (a *app) fuzzyListCache() *linkedmap.LRUCache[fuzzyListKey, fuzzyListCacheValue] {
	if a.fuzzyLists == nil {
		a.fuzzyLists = linkedmap.NewLRUCache[fuzzyListKey, fuzzyListCacheValue](fuzzyListCacheLimit)
	}
	return a.fuzzyLists
}

func fuzzyAllHits(count int, seq func(int) int) []fuzzyListHit {
	hits := make([]fuzzyListHit, 0, count)
	for i := range count {
		hits = append(hits, fuzzyListHit{index: i, seq: seq(i)})
	}
	return hits
}

// fuzzyList returns the matching source indices in display order, with the
// label ranges to highlight. ranked uses quick-open scoring and ordering;
// otherwise the source order is kept, as VS Code's find filters do.
func (a *app) fuzzyList(query, scope, source string, count int, item func(int) fuzzyListItem, ranked bool) []fuzzyListHit {
	if query == "" {
		return fuzzyAllHits(count, func(i int) int { return i })
	}
	key := fuzzyListKey{scope: scope, owner: source, query: query}
	if cached, ok := a.fuzzyListCache().Get(key); ok {
		return cached.hits
	}
	hits := fuzzyScoreHits(query, count, item, func(i int) int { return i }, ranked)
	a.fuzzyListCache().Set(key, fuzzyListCacheValue{
		window: fuzzyListRange{first: 0, last: count - 1, count: count}, hits: hits,
	})
	return hits
}

// fuzzyListStream caches by document identity and session generation, never
// by the stream's end sequence alone. When the only change since the last
// frame is an appended tail (and optional eviction from the front), it reuses
// the cached hits and scores just the new events.
func (a *app) fuzzyListStream(query, scope, owner string, window fuzzyListRange, seq func(int) int, item func(int) fuzzyListItem) []fuzzyListHit {
	if query == "" {
		return fuzzyAllHits(window.count, seq)
	}
	key := fuzzyListKey{scope: scope, owner: owner, query: query}
	cache := a.fuzzyListCache()
	if old, ok := cache.Get(key); ok && old.window == window {
		return old.hits
	}
	if old, ok := cache.Get(key); ok &&
		window.first >= old.window.first && window.last > old.window.last &&
		window.count >= old.window.count-(window.first-old.window.first) {
		hits := make([]fuzzyListHit, 0, len(old.hits)+window.count-old.window.count+1)
		for _, hit := range old.hits {
			if hit.seq >= window.first {
				hits = append(hits, hit)
			}
		}
		prepared := fuzzy.PrepareQuery(query)
		state := &fuzzyScoreState{}
		for i := range window.count {
			eventSeq := seq(i)
			if eventSeq <= old.window.last {
				continue
			}
			entry := item(i)
			if !fuzzyListPossible(entry, prepared) {
				continue
			}
			if score, matches := fuzzyListScores(entry, prepared, state); score != 0 {
				hits = append(hits, fuzzyListHit{
					index: i, seq: eventSeq, score: score, matches: matches,
				})
			}
		}
		value := fuzzyListCacheValue{window: window, hits: hits}
		cache.Set(key, value)
		return value.hits
	}

	hits := fuzzyScoreHits(query, window.count, item, seq, false)
	cache.Set(key, fuzzyListCacheValue{window: window, hits: hits})
	return hits
}

// fuzzyScoreHits is the full cold pass, shared by static and stream lists.
func fuzzyScoreHits(query string, count int, item func(int) fuzzyListItem, seq func(int) int, ranked bool) []fuzzyListHit {
	prepared := fuzzy.PrepareQuery(query)
	cache := fuzzy.NewFuzzyScorerCache()
	if ranked {
		var candidates []fuzzyListCandidate
		for i := range count {
			entry := item(i)
			if !fuzzyListPossible(entry, prepared) {
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
				hit: fuzzyListHit{index: i, seq: seq(i), score: score.Score,
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
		return hits
	}

	var hits []fuzzyListHit
	state := &fuzzyScoreState{}
	for i := range count {
		entry := item(i)
		if !fuzzyListPossible(entry, prepared) {
			continue
		}
		if score, matches := fuzzyListScores(entry, prepared, state); score != 0 {
			hits = append(hits, fuzzyListHit{
				index: i, seq: seq(i), score: score, matches: matches,
			})
		}
	}
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
