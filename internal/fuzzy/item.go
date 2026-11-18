package fuzzy

import (
	"cmp"
	"math"
	"reflect"
	"strconv"
	"strings"
)

// IItemAccessor corresponds to IItemAccessor in fuzzyScorer.ts. VS Code's
// `string | undefined` returns are represented by (string, bool); the bool is
// false where VS Code returns undefined.
type IItemAccessor[T any] interface {
	GetItemLabel(item T) string
	GetItemDescription(item T) (string, bool)
	GetItemPath(item T) (string, bool)
}

// IItemScore corresponds to IItemScore in fuzzyScorer.ts. A nil match slice
// means undefined in TS; a non-nil empty slice means an empty match array.
type IItemScore struct {
	Score            int
	LabelMatch       []Match
	DescriptionMatch []Match
}

func noItemScore() *IItemScore {
	return &IItemScore{}
}

// FuzzyScorerCache corresponds to FuzzyScorerCache. VS Code hashes its inputs
// into a string key; here a comparable struct is the key, so different inputs
// cannot collide. Create one with NewFuzzyScorerCache. Like VS Code's object,
// a cache is not synchronized: keep it on one goroutine or guard it yourself.
type FuzzyScorerCache map[scorerCacheKey]*IItemScore

type scorerCacheKey struct {
	label                     string
	description               string
	hasDescription            bool
	allowNonContiguousMatches bool
	normalized                string
	values                    string
}

// NewFuzzyScorerCache corresponds to an empty FuzzyScorerCache object.
func NewFuzzyScorerCache() FuzzyScorerCache {
	return make(FuzzyScorerCache)
}

// ScoreItemFuzzy corresponds to scoreItemFuzzy. It returns a pointer so a
// cache hit has the same identity as VS Code's returned cached object.
func ScoreItemFuzzy[T any](item T, query IPreparedQuery, allowNonContiguousMatches bool, accessor IItemAccessor[T], cache FuzzyScorerCache) *IItemScore {
	if isNil(item) || query.Normalized == "" {
		return noItemScore()
	}
	label := accessor.GetItemLabel(item)
	if label == "" {
		return noItemScore()
	}
	description, hasDescription := accessor.GetItemDescription(item)
	path, hasPath := accessor.GetItemPath(item)

	if cache == nil {
		cache = make(FuzzyScorerCache)
	}
	key := cacheKey(label, description, hasDescription, allowNonContiguousMatches, query)
	if cached, ok := cache[key]; ok {
		return cached
	}
	score := doScoreItemFuzzy(label, description, hasDescription, path, hasPath, query, allowNonContiguousMatches)
	cache[key] = score
	return score
}

// CompareItemScores is the comparison half of VS Code's
// compareItemsByFuzzyScore for two items whose scores are already known.
// It lets a caller reuse one score per item while sorting a large list.
func CompareItemScores[T any](itemA, itemB T, scoreA, scoreB *IItemScore, query IPreparedQuery, accessor IItemAccessor[T]) int {
	a, b := scoreA.Score, scoreB.Score

	// 1.) Identity matches have the highest score.
	if a == pathIdentityScore || b == pathIdentityScore {
		if a != b {
			if a == pathIdentityScore {
				return -1
			}
			return 1
		}
	}

	// 2.) Label matches rank above label+description matches.
	if a > labelScoreThreshold || b > labelScoreThreshold {
		if a != b {
			if a > b {
				return -1
			}
			return 1
		}
		if a < labelPrefixScoreThreshold && b < labelPrefixScoreThreshold {
			if compared := compareByMatchLength(scoreA.LabelMatch, scoreB.LabelMatch); compared != 0 {
				return compared
			}
		}
		labelA := accessor.GetItemLabel(itemA)
		labelB := accessor.GetItemLabel(itemB)
		if runeLen(labelA) != runeLen(labelB) {
			return runeLen(labelA) - runeLen(labelB)
		}
	}

	// 3.) Compare by label+description score.
	if a != b {
		if a > b {
			return -1
		}
		return 1
	}

	// 4.) Prefer matches in the label.
	hasLabelA := len(scoreA.LabelMatch) > 0
	hasLabelB := len(scoreB.LabelMatch) > 0
	if hasLabelA && !hasLabelB {
		return -1
	} else if hasLabelB && !hasLabelA {
		return 1
	}

	// 5.) Prefer more compact matches.
	distanceA := matchDistance(itemA, scoreA, accessor)
	distanceB := matchDistance(itemB, scoreB, accessor)
	if distanceA != 0 && distanceB != 0 && distanceA != distanceB {
		if distanceB > distanceA {
			return -1
		}
		return 1
	}

	// 6.) Fall back to label, description and path comparisons.
	return fallbackCompare(itemA, itemB, query, accessor)
}

// CompareItemsByFuzzyScore corresponds to compareItemsByFuzzyScore. It can be
// passed directly to a stable sorting function as a less-like comparison.
func CompareItemsByFuzzyScore[T any](itemA, itemB T, query IPreparedQuery, allowNonContiguousMatches bool, accessor IItemAccessor[T], cache FuzzyScorerCache) int {
	return CompareItemScores(itemA, itemB,
		ScoreItemFuzzy(itemA, query, allowNonContiguousMatches, accessor, cache),
		ScoreItemFuzzy(itemB, query, allowNonContiguousMatches, accessor, cache),
		query, accessor)
}

const (
	pathIdentityScore         = 1 << 18
	labelPrefixScoreThreshold = 1 << 17
	labelScoreThreshold       = 1 << 16
)

func cacheKey(label, description string, hasDescription, allow bool, query IPreparedQuery) scorerCacheKey {
	values := query.Values
	if len(values) == 0 {
		values = []IPreparedQueryPiece{query.IPreparedQueryPiece}
	}
	var builder strings.Builder
	for _, value := range values {
		builder.WriteString(strconv.Itoa(len(value.Normalized)))
		builder.WriteByte(':')
		builder.WriteString(value.Normalized)
		builder.WriteByte(':')
		builder.WriteString(strconv.FormatBool(value.ExpectContiguousMatch))
		builder.WriteByte(';')
	}
	return scorerCacheKey{
		label:                     label,
		description:               description,
		hasDescription:            hasDescription,
		allowNonContiguousMatches: allow,
		normalized:                query.Normalized,
		values:                    builder.String(),
	}
}

func isNil[T any](item T) bool {
	value := reflect.ValueOf(item)
	switch value.Kind() {
	case reflect.Chan, reflect.Func, reflect.Interface, reflect.Map, reflect.Pointer, reflect.Slice, reflect.UnsafePointer:
		return value.IsNil()
	default:
		return false
	}
}

func doScoreItemFuzzy(label, description string, hasDescription bool, path string, hasPath bool, query IPreparedQuery, allow bool) *IItemScore {
	preferLabelMatches := !hasPath || !query.ContainsPathSeparator

	// Identity matches on the full path rank highest.
	if hasPath && (pathSeparator == '/' && query.PathNormalized == path ||
		pathSeparator == '\\' && strings.EqualFold(query.PathNormalized, path)) {
		score := &IItemScore{
			Score:      pathIdentityScore,
			LabelMatch: []Match{{Start: 0, End: runeLen(label)}},
		}
		if hasDescription && description != "" {
			score.DescriptionMatch = []Match{{Start: 0, End: runeLen(description)}}
		}
		return score
	}

	if len(query.Values) > 1 {
		return doScoreItemFuzzyMultiple(label, description, hasDescription, path, hasPath, query.Values, preferLabelMatches, allow)
	}
	return doScoreItemFuzzySingle(label, description, hasDescription, path, hasPath, query.IPreparedQueryPiece, preferLabelMatches, allow)
}

func doScoreItemFuzzyMultiple(label, description string, hasDescription bool, path string, hasPath bool, query []IPreparedQueryPiece, preferLabelMatches, allow bool) *IItemScore {
	totalScore := 0
	totalLabelMatches := make([]Match, 0)
	totalDescriptionMatches := make([]Match, 0)
	for _, piece := range query {
		result := doScoreItemFuzzySingle(label, description, hasDescription, path, hasPath, piece, preferLabelMatches, allow)
		if result.Score == 0 {
			return noItemScore()
		}
		totalScore += result.Score
		totalLabelMatches = append(totalLabelMatches, result.LabelMatch...)
		totalDescriptionMatches = append(totalDescriptionMatches, result.DescriptionMatch...)
	}
	return &IItemScore{
		Score:            totalScore,
		LabelMatch:       normalizeMatches(totalLabelMatches),
		DescriptionMatch: normalizeMatches(totalDescriptionMatches),
	}
}

func doScoreItemFuzzySingle(label, description string, hasDescription bool, path string, hasPath bool, query IPreparedQueryPiece, preferLabelMatches, allow bool) *IItemScore {
	if preferLabelMatches || !hasDescription {
		labelResult := ScoreFuzzy(label, query.Normalized, query.NormalizedLowercase, allow && !query.ExpectContiguousMatch)
		if labelResult.Score != 0 {
			baseScore := labelScoreThreshold
			labelMatches := CreateMatches(labelResult.Matches)
			if prefixMatch := MatchesPrefix(query.Normalized, label); prefixMatch != nil {
				baseScore = labelPrefixScoreThreshold
				// A short label wins: "window.ts" over "windowActions.ts".
				baseScore += int(math.Round(float64(runeLen(query.Normalized)) / float64(runeLen(label)) * 100))
				labelMatches = prefixMatch
			}
			return &IItemScore{Score: baseScore + labelResult.Score, LabelMatch: labelMatches}
		}
	}

	if hasDescription && description != "" {
		descriptionPrefix := description
		if hasPath {
			descriptionPrefix += string(pathSeparator)
		}
		descriptionPrefixLength := runeLen(descriptionPrefix)
		descriptionAndLabel := descriptionPrefix + label
		result := ScoreFuzzy(descriptionAndLabel, query.Normalized, query.NormalizedLowercase, allow && !query.ExpectContiguousMatch)
		if result.Score != 0 {
			labelMatches := make([]Match, 0)
			descriptionMatches := make([]Match, 0)
			for _, match := range CreateMatches(result.Matches) {
				switch {
				case match.Start < descriptionPrefixLength && match.End > descriptionPrefixLength:
					labelMatches = append(labelMatches, Match{Start: 0, End: match.End - descriptionPrefixLength})
					descriptionMatches = append(descriptionMatches, Match{Start: match.Start, End: descriptionPrefixLength})
				case match.Start >= descriptionPrefixLength:
					labelMatches = append(labelMatches, Match{
						Start: match.Start - descriptionPrefixLength,
						End:   match.End - descriptionPrefixLength,
					})
				default:
					descriptionMatches = append(descriptionMatches, match)
				}
			}
			return &IItemScore{Score: result.Score, LabelMatch: labelMatches, DescriptionMatch: descriptionMatches}
		}
	}
	return noItemScore()
}

func compareByMatchLength(matchesA, matchesB []Match) int {
	if len(matchesA) == 0 && len(matchesB) == 0 {
		return 0
	}
	if len(matchesB) == 0 {
		return -1
	}
	if len(matchesA) == 0 {
		return 1
	}
	lengthA := matchesA[len(matchesA)-1].End - matchesA[0].Start
	lengthB := matchesB[len(matchesB)-1].End - matchesB[0].Start
	if lengthA == lengthB {
		return 0
	}
	if lengthB < lengthA {
		return 1
	}
	return -1
}

func matchDistance[T any](item T, score *IItemScore, accessor IItemAccessor[T]) int {
	matchStart, matchEnd := -1, -1
	if len(score.DescriptionMatch) > 0 {
		matchStart = score.DescriptionMatch[0].Start
	} else if len(score.LabelMatch) > 0 {
		matchStart = score.LabelMatch[0].Start
	}
	if len(score.LabelMatch) > 0 {
		matchEnd = score.LabelMatch[len(score.LabelMatch)-1].End
		if len(score.DescriptionMatch) > 0 {
			if description, ok := accessor.GetItemDescription(item); ok {
				matchEnd += runeLen(description)
			}
		}
	} else if len(score.DescriptionMatch) > 0 {
		matchEnd = score.DescriptionMatch[len(score.DescriptionMatch)-1].End
	}
	return matchEnd - matchStart
}

func fallbackCompare[T any](itemA, itemB T, query IPreparedQuery, accessor IItemAccessor[T]) int {
	labelA := accessor.GetItemLabel(itemA)
	labelB := accessor.GetItemLabel(itemB)
	descriptionA, _ := accessor.GetItemDescription(itemA)
	descriptionB, _ := accessor.GetItemDescription(itemB)
	lengthA := runeLen(labelA) + runeLen(descriptionA)
	lengthB := runeLen(labelB) + runeLen(descriptionB)
	if lengthA != lengthB {
		return lengthA - lengthB
	}
	pathA, _ := accessor.GetItemPath(itemA)
	pathB, _ := accessor.GetItemPath(itemB)
	if pathA != "" && pathB != "" && runeLen(pathA) != runeLen(pathB) {
		return runeLen(pathA) - runeLen(pathB)
	}
	if labelA != labelB {
		return compareAnything(labelA, labelB, query.Normalized)
	}
	if descriptionA != "" && descriptionB != "" && descriptionA != descriptionB {
		return compareAnything(descriptionA, descriptionB, query.Normalized)
	}
	if pathA != "" && pathB != "" && pathA != pathB {
		return compareAnything(pathA, pathB, query.Normalized)
	}
	return 0
}

// compareAnything corresponds to comparers.ts's compareAnything. VS Code uses
// Intl.Collator with numeric sorting and base sensitivity; Go has no locale
// collator here, so lowercase natural comparison with a length tie-break is
// used. It only affects final ties after every score has been compared.
func compareAnything(one, other, lookFor string) int {
	a, b := lowerString(one), lowerString(other)
	if compared := compareByPrefix(a, b, lookFor); compared != 0 {
		return compared
	}
	aSuffix, bSuffix := strings.HasSuffix(a, lookFor), strings.HasSuffix(b, lookFor)
	if aSuffix != bSuffix {
		if aSuffix {
			return -1
		}
		return 1
	}
	if compared := compareFileNames(a, b); compared != 0 {
		return compared
	}
	return strings.Compare(a, b)
}

func compareByPrefix(a, b, lookFor string) int {
	aPrefix, bPrefix := strings.HasPrefix(a, lookFor), strings.HasPrefix(b, lookFor)
	if aPrefix != bPrefix {
		if aPrefix {
			return -1
		}
		return 1
	}
	if aPrefix && bPrefix {
		if len(a) < len(b) {
			return -1
		}
		if len(a) > len(b) {
			return 1
		}
	}
	return 0
}

func compareFileNames(a, b string) int {
	if compared := naturalCompare(a, b); compared != 0 {
		return compared
	}
	return cmp.Compare(runeLen(a), runeLen(b))
}

func naturalCompare(a, b string) int {
	ar, br := []rune(a), []rune(b)
	i, j := 0, 0
	for i < len(ar) && j < len(br) {
		if ar[i] >= '0' && ar[i] <= '9' && br[j] >= '0' && br[j] <= '9' {
			startA, startB := i, j
			for i < len(ar) && ar[i] >= '0' && ar[i] <= '9' {
				i++
			}
			for j < len(br) && br[j] >= '0' && br[j] <= '9' {
				j++
			}
			numberA := strings.TrimLeft(string(ar[startA:i]), "0")
			numberB := strings.TrimLeft(string(br[startB:j]), "0")
			if compared := cmp.Compare(len(numberA), len(numberB)); compared != 0 {
				return compared
			}
			if compared := strings.Compare(numberA, numberB); compared != 0 {
				return compared
			}
			continue
		}
		if compared := cmp.Compare(ar[i], br[j]); compared != 0 {
			return compared
		}
		i++
		j++
	}
	return cmp.Compare(len(ar)-i, len(br)-j)
}
