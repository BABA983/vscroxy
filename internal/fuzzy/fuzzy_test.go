package fuzzy

import (
	"path"
	"reflect"
	"slices"
	"testing"
)

// The expectations in this file are ported from
// src/vs/base/test/common/fuzzyScorer.test.ts (VS Code commit
// 972b66fdc541e10689adfd90ade9549dea1b18bc). URI-based resources are
// represented by their path, label and description.

type item struct {
	label          string
	description    string
	path           string
	hasDescription bool
	hasPath        bool
}

type testResource = item

type resourceAccessor struct{}

func (resourceAccessor) GetItemLabel(item item) string {
	return item.label
}

func (resourceAccessor) GetItemDescription(item item) (string, bool) {
	return item.description, item.hasDescription
}

func (resourceAccessor) GetItemPath(item item) (string, bool) {
	return item.path, item.hasPath
}

func resource(fullPath string) item {
	return item{label: path.Base(fullPath), description: path.Dir(fullPath), path: fullPath, hasDescription: true, hasPath: true}
}

func resourceWithDescription(fullPath, description string, present bool) item {
	item := resource(fullPath)
	item.description, item.hasDescription = description, present
	return item
}

func scoreFuzzyQuery(target, query string, allowNonContiguousMatches ...bool) FuzzyScore {
	prepared := PrepareQuery(query)
	allow := !prepared.ExpectContiguousMatch
	if len(allowNonContiguousMatches) > 0 {
		allow = allowNonContiguousMatches[0]
	}
	return ScoreFuzzy(target, prepared.Normalized, prepared.NormalizedLowercase, allow)
}

func doScore2(target, query string, matchOffset int) FuzzyScore2 {
	return ScoreFuzzy2(target, PrepareQuery(query), 0, matchOffset)
}

func scoreItem(item item, query string, allow bool, cache FuzzyScorerCache) *IItemScore {
	return ScoreItemFuzzy(item, PrepareQuery(query), allow, resourceAccessor{}, cache)
}

func TestScoreFuzzyOrder(t *testing.T) {
	target := "HelLo-World"
	queries := []string{
		"HelLo-World", "hello-world", "HW", "hw", "H", "h", "W", "Ld", "ld", "w", "L", "l", "4",
	}
	scores := make([]FuzzyScore, 0, len(queries))
	for _, query := range queries {
		scores = append(scores, scoreFuzzyQuery(target, query, true))
	}
	sorted := slices.Clone(scores)
	slices.SortStableFunc(sorted, func(a, b FuzzyScore) int { return b.Score - a.Score })
	if !reflect.DeepEqual(scores, sorted) {
		t.Fatalf("scores are not in descending order: %+v", scores)
	}
}

func TestScoreFuzzyContiguous(t *testing.T) {
	target := "HelLo-World"
	if scoreFuzzyQuery(target, "HelLo-World", false).Score <= 0 {
		t.Fatal("full match has no score")
	}
	if got := len(scoreFuzzyQuery(target, "HelLo-World", false).Matches); got != len(target) {
		t.Fatalf("full match positions = %d", got)
	}
	if scoreFuzzyQuery(target, "hello-world", false).Score <= 0 {
		t.Fatal("mixed-case full match has no score")
	}
	for _, query := range []string{"h", "ello", "ld"} {
		if scoreFuzzyQuery(target, query, false).Score <= 0 {
			t.Fatalf("%q should match contiguously", query)
		}
	}
	for _, query := range []string{"HW", "eo"} {
		if scoreFuzzyQuery(target, query, false).Score != 0 {
			t.Fatalf("%q should not match contiguously", query)
		}
	}
}

func TestScoreItemMatchesAreProper(t *testing.T) {
	if scoreItem(item{}, "something", true, NewFuzzyScorerCache()).Score != 0 {
		t.Fatal("empty item matched")
	}
	item := resource("/xyz/some/path/someFile123.txt")
	if scoreItem(item, "something", true, NewFuzzyScorerCache()).Score != 0 {
		t.Fatal("NullAccessor equivalent should not match")
	}
	nullAccessorItem := ScoreItemFuzzy[*testResource](nil, PrepareQuery("something"), true, pointerAccessor{}, NewFuzzyScorerCache())
	if nullAccessorItem.Score != 0 {
		t.Fatal("nil item matched")
	}

	identity := scoreItem(item, item.path, true, NewFuzzyScorerCache())
	if identity.Score == 0 || len(identity.DescriptionMatch) != 1 || len(identity.LabelMatch) != 1 ||
		identity.DescriptionMatch[0] != (Match{0, runeLen(item.description)}) ||
		identity.LabelMatch[0] != (Match{0, runeLen(item.label)}) {
		t.Fatalf("identity result = %+v", identity)
	}

	basenamePrefix := scoreItem(item, "som", true, NewFuzzyScorerCache())
	if basenamePrefix.Score == 0 || basenamePrefix.DescriptionMatch != nil ||
		!slices.Equal(basenamePrefix.LabelMatch, []Match{{0, 3}}) {
		t.Fatalf("basename prefix = %+v", basenamePrefix)
	}

	camelcase := scoreItem(item, "sF", true, NewFuzzyScorerCache())
	if camelcase.Score == 0 || camelcase.DescriptionMatch != nil ||
		!slices.Equal(camelcase.LabelMatch, []Match{{0, 1}, {4, 5}}) {
		t.Fatalf("basename camelcase = %+v", camelcase)
	}

	basename := scoreItem(item, "of", true, NewFuzzyScorerCache())
	if basename.Score == 0 || basename.DescriptionMatch != nil ||
		!slices.Equal(basename.LabelMatch, []Match{{1, 2}, {4, 5}}) {
		t.Fatalf("basename match = %+v", basename)
	}

	pathResult := scoreItem(item, "xyz123", true, NewFuzzyScorerCache())
	if pathResult.Score == 0 || len(pathResult.DescriptionMatch) != 1 || len(pathResult.LabelMatch) != 1 ||
		pathResult.LabelMatch[0] != (Match{8, 11}) || pathResult.DescriptionMatch[0] != (Match{1, 4}) {
		t.Fatalf("path match = %+v", pathResult)
	}

	// The ellipsis is removed from the normalized query. VS Code only asserts
	// that the ellipsis query scores and then rechecks pathResult.
	if scoreItem(item, "…me/path/someFile123.txt", true, NewFuzzyScorerCache()).Score == 0 {
		t.Fatal("ellipsis query should match")
	}

	noMatch := scoreItem(item, "987", true, NewFuzzyScorerCache())
	if noMatch.Score != 0 || noMatch.LabelMatch != nil || noMatch.DescriptionMatch != nil {
		t.Fatalf("no match = %+v", noMatch)
	}
	noExact := scoreItem(item, `"sF"`, true, NewFuzzyScorerCache())
	if noExact.Score != noMatch.Score || noExact.LabelMatch != nil || noExact.DescriptionMatch != nil {
		t.Fatalf("no exact match = %+v", noExact)
	}

	if !(identity.Score > basenamePrefix.Score && basenamePrefix.Score > basename.Score &&
		basename.Score > pathResult.Score && pathResult.Score > noMatch.Score) {
		t.Fatalf("unexpected score ordering: identity=%d prefix=%d basename=%d path=%d none=%d",
			identity.Score, basenamePrefix.Score, basename.Score, pathResult.Score, noMatch.Score)
	}
}

type pointerAccessor struct{}

func (pointerAccessor) GetItemLabel(value *item) string {
	if value == nil {
		return ""
	}
	return value.label
}

func (pointerAccessor) GetItemDescription(value *item) (string, bool) {
	if value == nil {
		return "", false
	}
	return value.description, value.hasDescription
}

func (pointerAccessor) GetItemPath(value *item) (string, bool) {
	if value == nil {
		return "", false
	}
	return value.path, value.hasPath
}

func TestScoreItemMultiple(t *testing.T) {
	item := resource("/xyz/some/path/someFile123.txt")
	res1 := scoreItem(item, "xyz some", true, NewFuzzyScorerCache())
	if res1.Score == 0 || !slices.Equal(res1.LabelMatch, []Match{{0, 4}}) || !slices.Equal(res1.DescriptionMatch, []Match{{1, 4}}) {
		t.Fatalf("xyz some = %+v", res1)
	}
	res2 := scoreItem(item, "some xyz", true, NewFuzzyScorerCache())
	if res2.Score != res1.Score || !slices.Equal(res2.LabelMatch, []Match{{0, 4}}) || !slices.Equal(res2.DescriptionMatch, []Match{{1, 4}}) {
		t.Fatalf("some xyz = %+v", res2)
	}
	res3 := scoreItem(item, "some xyz file file123", true, NewFuzzyScorerCache())
	if res3.Score <= res2.Score || !slices.Equal(res3.LabelMatch, []Match{{0, 11}}) || !slices.Equal(res3.DescriptionMatch, []Match{{1, 4}}) {
		t.Fatalf("multiple pieces = %+v", res3)
	}
	res4 := scoreItem(item, "path z y", true, NewFuzzyScorerCache())
	if res4.Score == 0 || res4.Score >= res2.Score || len(res4.LabelMatch) != 0 ||
		!slices.Equal(res4.DescriptionMatch, []Match{{2, 4}, {10, 14}}) {
		t.Fatalf("path z y = %+v", res4)
	}
}

func TestScoreItemCache(t *testing.T) {
	item := resource("/xyz/some/path/someFile123.txt")
	cache := NewFuzzyScorerCache()
	if scoreItem(item, "xyz sm", true, cache).Score == 0 {
		t.Fatal("expected a match")
	}
	if scoreItem(item, `xyz "sm"`, true, cache).Score != 0 {
		t.Fatal("quoted piece should be a different, non-matching query")
	}

	cache = NewFuzzyScorerCache()
	scoreItem(resource("/xyz/some/path/someFile123.txt"), "some", true, cache)
	scoreItem(resource("/xyz/other/path/someFile123.txt"), "some", true, cache)
	scoreItem(item, "somefile", true, cache)
	scoreItem(item, "xyz sm", true, cache)
	scoreItem(item, `xyz "sm"`, true, cache)
	scoreItem(item, "sm xyz", true, cache)
	scoreItem(item, "somefile", false, cache)
	scoreItem(resourceWithDescription("/someFile123.txt", "", false), "some", true, cache)
	scoreItem(resourceWithDescription("/someFile123.txt", "", true), "some", true, cache)
	if len(cache) != 9 {
		t.Fatalf("cache entries = %d, want 9", len(cache))
	}

	for _, query := range []string{"somefile", "xyz sm", `xyz "sm"`, "ünïcödé"} {
		cache = NewFuzzyScorerCache()
		cold := scoreItem(item, query, true, cache)
		warm := scoreItem(item, query, true, cache)
		if len(cache) != 1 || warm != cold {
			t.Fatalf("%q: cache entries = %d, same pointer = %v", query, len(cache), warm == cold)
		}
	}

	cache = NewFuzzyScorerCache()
	query := PrepareQuery("xyz sm")
	before := ScoreItemFuzzy(item, query, true, resourceAccessor{}, cache)
	if before.Score == 0 {
		t.Fatal("expected a match")
	}
	query.Values[1].ExpectContiguousMatch = true
	after := ScoreItemFuzzy(item, query, true, resourceAccessor{}, cache)
	if len(cache) != 2 {
		t.Fatalf("mutated query produced %d entries, want 2", len(cache))
	}
	fresh := ScoreItemFuzzy(item, PrepareQuery(`xyz "sm"`), true, resourceAccessor{}, NewFuzzyScorerCache())
	if !reflect.DeepEqual(after, fresh) {
		t.Fatalf("mutated query result = %+v, want %+v", after, fresh)
	}
}

func TestScoreItemInvalidInput(t *testing.T) {
	res := scoreItem(item{}, "null", true, NewFuzzyScorerCache())
	if res.Score != 0 {
		t.Fatal("empty label matched")
	}
	res = ScoreItemFuzzy(resource("/abc"), PrepareQuery(""), true, resourceAccessor{}, NewFuzzyScorerCache())
	if res.Score != 0 {
		t.Fatal("empty query matched")
	}
}

func TestScoreItemOptimizeForFilePaths(t *testing.T) {
	item := resource("/xyz/others/spath/some/xsp/file123.txt")
	result := scoreItem(item, "xspfile123", true, NewFuzzyScorerCache())
	if result.Score == 0 || !slices.Equal(result.LabelMatch, []Match{{0, 7}}) || !slices.Equal(result.DescriptionMatch, []Match{{23, 26}}) {
		t.Fatalf("xspfile123 = %+v", result)
	}
}

func TestScoreItemAvoidMatchScattering(t *testing.T) {
	item := resource("projects/ui/cula/ats/target.mk")
	result := scoreItem(item, "tcltarget.mk", true, NewFuzzyScorerCache())
	if result.Score == 0 || !slices.Equal(result.LabelMatch, []Match{{0, 9}}) {
		t.Fatalf("tcltarget.mk = %+v", result)
	}
}

func TestScoreItemPrefersCompactMatches(t *testing.T) {
	item := resource("/1a111d1/11a1d1/something.txt")
	result := scoreItem(item, "ad", true, NewFuzzyScorerCache())
	if result.Score == 0 || len(result.LabelMatch) != 0 ||
		!slices.Equal(result.DescriptionMatch, []Match{{11, 12}, {13, 14}}) {
		t.Fatalf("ad = %+v", result)
	}
}

func TestScoreItemProperTargetOffset(t *testing.T) {
	if scoreItem(resource("etem"), "teem", true, NewFuzzyScorerCache()).Score != 0 {
		t.Fatal("teem should not match etem")
	}
	result := scoreItem(resource("ede"), "de", true, NewFuzzyScorerCache())
	if !slices.Equal(result.LabelMatch, []Match{{1, 3}}) {
		t.Fatalf("de on ede = %+v", result)
	}
	result = scoreItem(resource("/src/vs/editor/browser/viewParts/lineNumbers/flipped-cursor-2x.svg"), "debug", true, NewFuzzyScorerCache())
	if !slices.Equal(result.DescriptionMatch, []Match{{9, 10}, {36, 37}, {40, 41}}) ||
		!slices.Equal(result.LabelMatch, []Match{{9, 10}, {20, 21}}) {
		t.Fatalf("debug = %+v", result)
	}
	if scoreItem(resource("abcde"), "edcda", true, NewFuzzyScorerCache()).Score != 0 {
		t.Fatal("query must match in sequence")
	}
}

func TestScoreItemPathSeparators(t *testing.T) {
	local := resource("abcde/super/duper")
	remote := resource("abcde/super/duper") // URI scheme does not affect fsPath scoring.
	queries := []string{`abcde\super\duper`, "abcde/super/duper"}
	for _, item := range []item{local, remote} {
		for _, query := range queries {
			if scoreItem(item, query, true, NewFuzzyScorerCache()).Score == 0 {
				t.Fatalf("%q did not match %s", query, item.path)
			}
			// Also try slash/backslash-normalized accessors.
			slash := item
			slash.path = path.Clean(item.path)
			slash.description = path.Dir(item.path)
			if scoreItem(slash, query, true, NewFuzzyScorerCache()).Score == 0 {
				t.Fatalf("slash accessor: %q did not match %s", query, slash.path)
			}
			backslash := item
			backslash.path = string(pathSeparatorWildcard(item.path))
			if scoreItem(backslash, query, true, NewFuzzyScorerCache()).Score == 0 {
				t.Fatalf("backslash accessor: %q did not match %s", query, backslash.path)
			}
		}
	}
}

func pathSeparatorWildcard(value string) []rune {
	runes := []rune(value)
	for i, r := range runes {
		if r == '/' {
			runes[i] = '\\'
		}
	}
	return runes
}

func TestScoreItemUpperCaseBonusOnlyOnNonConsecutive(t *testing.T) {
	lower := scoreItem(resource("asdfasdfasdf"), "asdf", true, NewFuzzyScorerCache())
	upper := scoreItem(resource("ASDFasdfasdf"), "asdf", true, NewFuzzyScorerCache())
	if lower.Score <= upper.Score {
		t.Fatalf("lower = %d, upper = %d", lower.Score, upper.Score)
	}
}

func TestPrepareQuery(t *testing.T) {
	if PrepareQuery(" f*a ").Normalized != "fa" || PrepareQuery(" f…a ").Normalized != "fa" {
		t.Fatal("wildcards/ellipsis not normalized")
	}
	if PrepareQuery("main#").Normalized != "main" || PrepareQuery("main#").Original != "main#" {
		t.Fatal("trailing hash not normalized")
	}
	if PrepareQuery("foo*").Normalized != "foo" || PrepareQuery("foo*").Original != "foo*" {
		t.Fatal("wildcard not preserved in Original")
	}
	query := PrepareQuery("model Tester.ts")
	if query.Original != "model Tester.ts" || query.OriginalLowercase != "model tester.ts" ||
		query.Normalized != "modelTester.ts" || query.NormalizedLowercase != "modeltester.ts" ||
		query.ExpectContiguousMatch {
		t.Fatalf("model query = %+v", query)
	}
	if PrepareQuery("ModelTester.ts").ContainsPathSeparator {
		t.Fatal("unexpected path separator")
	}
	if !PrepareQuery("Model" + string(pathSeparator) + "Tester.ts").ContainsPathSeparator {
		t.Fatal("expected path separator")
	}
	if !PrepareQuery(`"hello"`).ExpectContiguousMatch || PrepareQuery(`"hello"`).Normalized != "hello" {
		t.Fatal("quotes not handled")
	}

	query = PrepareQuery("He*llo World")
	if query.Normalized != "HelloWorld" || len(query.Values) != 2 ||
		query.Values[0].Original != "He*llo" || query.Values[0].Normalized != "Hello" ||
		query.Values[1].Original != "World" || query.Values[1].Normalized != "World" {
		t.Fatalf("multi query = %+v", query)
	}
	restored := PiecesToQuery(query.Values)
	if restored.Original != query.Original || len(restored.Values) != len(query.Values) ||
		restored.ContainsPathSeparator != query.ContainsPathSeparator {
		t.Fatalf("restored query = %+v", restored)
	}

	query = PrepareQuery(" Hello   World  \t")
	if query.OriginalLowercase != " hello   world  \t" || query.Normalized != "HelloWorld" ||
		len(query.Values) != 2 || query.Values[0].OriginalLowercase != "hello" ||
		query.Values[1].OriginalLowercase != "world" {
		t.Fatalf("spaces query = %+v", query)
	}

	if pathSeparator == '\\' {
		if PrepareQuery("C:/some/path").PathNormalized != `C:\some\path` ||
			!PrepareQuery("C:/some/path").ContainsPathSeparator {
			t.Fatal("windows path normalization")
		}
	} else if PrepareQuery(`\some\path`).PathNormalized != "/some/path" ||
		!PrepareQuery(`\some\path`).ContainsPathSeparator {
		t.Fatal("posix path normalization")
	}
}

func TestScoreFuzzy2Matching(t *testing.T) {
	target := "HelLo-World"
	for _, offset := range []int{0, 3} {
		scoringTarget := target
		if offset != 0 {
			scoringTarget = "123" + target
		}
		result := doScore2(scoringTarget, "HelLo-World", offset)
		if !result.HasScore || !slices.Equal(result.Matches, []Match{{offset, runeLen(target) + offset}}) {
			t.Fatalf("full match at %d = %+v", offset, result)
		}
		result = doScore2(scoringTarget, "HW", offset)
		if !result.HasScore || !slices.Equal(result.Matches, []Match{{offset, 1 + offset}, {6 + offset, 7 + offset}}) {
			t.Fatalf("HW at %d = %+v", offset, result)
		}
	}
}

func TestScoreFuzzy2Multiple(t *testing.T) {
	target := "HelLo-World"
	first := doScore2(target, "HelLo", 0)
	second := doScore2(target, "World", 0)
	combined := append(slices.Clone(first.Matches), second.Matches...)

	checkScore := func(result FuzzyScore2) {
		if !result.HasScore || result.Score < first.Score+second.Score {
			t.Fatalf("multi result = %+v", result)
		}
		if len(result.Matches) != len(combined) {
			t.Fatalf("multi matches = %+v", result.Matches)
		}
		for i, match := range result.Matches {
			if match != combined[i] {
				t.Fatalf("multi matches = %+v, want %+v", result.Matches, combined)
			}
		}
	}
	for _, query := range []string{"HelLo World", "World HelLo", "World HelLo World"} {
		checkScore(doScore2(target, query, 0))
	}
	for _, query := range []string{"World HelLo Nothing", "More Nothing"} {
		result := doScore2(target, query, 0)
		if result.HasScore || len(result.Matches) != 0 {
			t.Fatalf("%q should not match: %+v", query, result)
		}
	}
}

func TestScoreFuzzy2Emoji(t *testing.T) {
	result := doScore2("# ❌ Wow", "❌", 0)
	if !result.HasScore || len(result.Matches) == 0 {
		t.Fatalf("emoji query = %+v", result)
	}
}

func TestQuotesAndSpecialCharacters(t *testing.T) {
	if scoreFuzzyQuery("contiguous", `"contguous"`).Score != 0 {
		t.Fatal("missing character should not match with quotes")
	}
	if scoreFuzzyQuery("contiguous", `"contiguous"`).Score <= 0 {
		t.Fatal("exact quoted query should match")
	}
	result := scoreFuzzyQuery("2021-7-26.md", `"26"`)
	if result.Score != 14 || !slices.Equal(result.Matches, []int{7, 8}) {
		t.Fatalf("quoted positions = %+v", result)
	}

	for _, tc := range []struct{ target, query string }{
		{"main", "main#"}, {"foo", "foo*"}, {"MyClass", "MyClass#*"}, {"MyClass", "MC#"},
		{"#SpecialFunction", "#SpecialFunction"}, {"#", "#"},
	} {
		result := doScore2(tc.target, tc.query, 0)
		if !result.HasScore || result.Score <= 0 || len(result.Matches) == 0 {
			t.Fatalf("%q on %q = %+v", tc.query, tc.target, result)
		}
	}
	if PrepareQuery("#").Normalized != "#" || PrepareQuery("#SpecialFunction").Normalized != "#SpecialFunction" {
		t.Fatal("leading # should be preserved")
	}
}

func TestChineseHighlightsAreRuneOffsets(t *testing.T) {
	target := "打开设置面板"
	prepared := PrepareQuery("设置")
	score := ScoreFuzzy(target, prepared.Normalized, prepared.NormalizedLowercase, true)
	if score.Score == 0 || !slices.Equal(score.Matches, []int{2, 3}) {
		t.Fatalf("ScoreFuzzy = %+v", score)
	}
	ranges := CreateMatches(score.Matches)
	if !slices.Equal(ranges, []Match{{2, 4}}) {
		t.Fatalf("CreateMatches = %+v", ranges)
	}
	// The same highlight in UTF-8 bytes would be 6..12, inside/beside runes.
	if got := runesSlice(target, ranges[0]); got != "设置" {
		t.Fatalf("highlight = %q", got)
	}

	result := ScoreFuzzy2(target, prepared, 0, 0)
	if !result.HasScore || !slices.Equal(result.Matches, []Match{{2, 4}}) {
		t.Fatalf("ScoreFuzzy2 = %+v", result)
	}

	item := resource("项目/打开设置面板")
	item.label = target
	item.description = "中文描述"
	item.path = "项目/" + target
	itemResult := scoreItem(item, "设置", true, NewFuzzyScorerCache())
	if itemResult.Score == 0 || !slices.Equal(itemResult.LabelMatch, []Match{{2, 4}}) {
		t.Fatalf("ScoreItemFuzzy = %+v", itemResult)
	}

	// MatchesWords anchors at word starts; a space makes 设置 a word here.
	wordResult := MatchesWords("设置", "打开 设置面板")
	if !slices.Equal(wordResult, []Match{{3, 5}}) {
		t.Fatalf("MatchesWords = %+v", wordResult)
	}
}

func runesSlice(value string, match Match) string {
	runes := []rune(value)
	return string(runes[match.Start:match.End])
}
