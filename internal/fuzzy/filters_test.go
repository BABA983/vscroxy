package fuzzy

import (
	"slices"
	"strings"
	"testing"
)

// The expectations in this file are ported from
// src/vs/base/test/common/filters.test.ts (VS Code commit
// 972b66fdc541e10689adfd90ade9549dea1b18bc), for the filters and the
// IntelliSense fuzzyScore that the port depends on.

func filterOk(t *testing.T, filter Filter, word, target string, want []Match) {
	t.Helper()
	got := filter(word, target)
	if got == nil {
		t.Fatalf("%q didn't match %q", word, target)
	}
	if want != nil && !slices.Equal(got, want) {
		t.Fatalf("%q on %q = %v, want %v", word, target, got, want)
	}
}

func filterNotOk(t *testing.T, filter Filter, word, target string) {
	t.Helper()
	if got := filter(word, target); got != nil {
		t.Fatalf("%q matched %q as %v", word, target, got)
	}
}

func TestPrefixFilterCaseSensitive(t *testing.T) {
	filterNotOk(t, MatchesStrictPrefix, "", "")
	filterOk(t, MatchesStrictPrefix, "", "anything", []Match{})
	filterOk(t, MatchesStrictPrefix, "alpha", "alpha", []Match{{0, 5}})
	filterOk(t, MatchesStrictPrefix, "alpha", "alphasomething", []Match{{0, 5}})
	filterNotOk(t, MatchesStrictPrefix, "alpha", "alp")
	filterOk(t, MatchesStrictPrefix, "a", "alpha", []Match{{0, 1}})
	filterNotOk(t, MatchesStrictPrefix, "x", "alpha")
	filterNotOk(t, MatchesStrictPrefix, "A", "alpha")
	filterNotOk(t, MatchesStrictPrefix, "AlPh", "alPHA")
}

func TestPrefixFilterIgnoreCase(t *testing.T) {
	filterOk(t, MatchesPrefix, "alpha", "alpha", []Match{{0, 5}})
	filterOk(t, MatchesPrefix, "alpha", "alphasomething", []Match{{0, 5}})
	filterNotOk(t, MatchesPrefix, "alpha", "alp")
	filterOk(t, MatchesPrefix, "a", "alpha", []Match{{0, 1}})
	filterOk(t, MatchesPrefix, "ä", "Älpha", []Match{{0, 1}})
	filterNotOk(t, MatchesPrefix, "x", "alpha")
	filterOk(t, MatchesPrefix, "A", "alpha", []Match{{0, 1}})
	filterOk(t, MatchesPrefix, "AlPh", "alPHA", []Match{{0, 4}})
	filterNotOk(t, MatchesPrefix, "T", "4") // VS Code issue #22401
}

func TestCamelCaseFilter(t *testing.T) {
	filterNotOk(t, MatchesCamelCase, "", "")
	filterOk(t, MatchesCamelCase, "", "anything", []Match{})
	filterOk(t, MatchesCamelCase, "alpha", "alpha", []Match{{0, 5}})
	filterOk(t, MatchesCamelCase, "AlPhA", "alpha", []Match{{0, 5}})
	filterOk(t, MatchesCamelCase, "alpha", "alphasomething", []Match{{0, 5}})
	filterNotOk(t, MatchesCamelCase, "alpha", "alp")

	cases := []struct {
		word, target string
		want         []Match
	}{
		{"c", "CamelCaseRocks", []Match{{0, 1}}},
		{"cc", "CamelCaseRocks", []Match{{0, 1}, {5, 6}}},
		{"ccr", "CamelCaseRocks", []Match{{0, 1}, {5, 6}, {9, 10}}},
		{"cacr", "CamelCaseRocks", []Match{{0, 2}, {5, 6}, {9, 10}}},
		{"cacar", "CamelCaseRocks", []Match{{0, 2}, {5, 7}, {9, 10}}},
		{"ccarocks", "CamelCaseRocks", []Match{{0, 1}, {5, 7}, {9, 14}}},
		{"cr", "CamelCaseRocks", []Match{{0, 1}, {9, 10}}},
		{"fba", "FooBarAbe", []Match{{0, 1}, {3, 5}}},
		{"fbar", "FooBarAbe", []Match{{0, 1}, {3, 6}}},
		{"fbara", "FooBarAbe", []Match{{0, 1}, {3, 7}}},
		{"fbaa", "FooBarAbe", []Match{{0, 1}, {3, 5}, {6, 7}}},
		{"fbaab", "FooBarAbe", []Match{{0, 1}, {3, 5}, {6, 8}}},
		{"c2d", "canvasCreation2D", []Match{{0, 1}, {14, 16}}},
		{"cce", "_canvasCreationEvent", []Match{{1, 2}, {7, 8}, {15, 16}}},
	}
	for _, tc := range cases {
		filterOk(t, MatchesCamelCase, tc.word, tc.target, tc.want)
	}
}

func TestCamelCaseFilterIssue19256(t *testing.T) {
	if MatchesCamelCase("Debug Console", "Open: Debug Console") == nil ||
		MatchesCamelCase("Debug console", "Open: Debug Console") == nil ||
		MatchesCamelCase("debug console", "Open: Debug Console") == nil {
		t.Fatal("expected Debug Console variants to match")
	}
}

func TestWordFilter(t *testing.T) {
	filterOk(t, MatchesWords, "alpha", "alpha", []Match{{0, 5}})
	filterOk(t, MatchesWords, "alpha", "alphasomething", []Match{{0, 5}})
	filterNotOk(t, MatchesWords, "alpha", "alp")
	filterOk(t, MatchesWords, "a", "alpha", []Match{{0, 1}})
	filterNotOk(t, MatchesWords, "x", "alpha")
	filterOk(t, MatchesWords, "A", "alpha", []Match{{0, 1}})
	filterOk(t, MatchesWords, "AlPh", "alPHA", []Match{{0, 4}})
	if MatchesWords("Debug Console", "Open: Debug Console") == nil {
		t.Fatal("expected Debug Console to match")
	}

	filterOk(t, MatchesWords, "gp", "Git: Pull", []Match{{0, 1}, {5, 6}})
	filterOk(t, MatchesWords, "g p", "Git: Pull", []Match{{0, 1}, {5, 6}})
	filterOk(t, MatchesWords, "gipu", "Git: Pull", []Match{{0, 2}, {5, 7}})

	filterOk(t, MatchesWords, "gp", "Category: Git: Pull", []Match{{10, 11}, {15, 16}})
	filterOk(t, MatchesWords, "g p", "Category: Git: Pull", []Match{{10, 11}, {15, 16}})
	filterOk(t, MatchesWords, "gipu", "Category: Git: Pull", []Match{{10, 12}, {15, 17}})

	filterNotOk(t, MatchesWords, "it", "Git: Pull")
	filterNotOk(t, MatchesWords, "ll", "Git: Pull")

	filterOk(t, MatchesWords, "git: プル", "git: プル", []Match{{0, 7}})
	filterOk(t, MatchesWords, "git プル", "git: プル", []Match{{0, 3}, {5, 7}})
	filterOk(t, MatchesWords, "öäk", "Öhm: Älles Klar", []Match{{0, 1}, {5, 6}, {11, 12}})

	filterOk(t, MatchesWords, "C++", "C/C++: command", []Match{{2, 5}}) // issue #123915
	filterOk(t, MatchesWords, ".", ":", []Match{})                      // issue #154533
	filterOk(t, MatchesWords, ".", ".", []Match{{0, 1}})

	filterOk(t, MatchesWords, "bar", "foo-bar", nil)
	filterOk(t, MatchesWords, "bar test", "foo-bar test", nil)
	filterOk(t, MatchesWords, "fbt", "foo-bar test", nil)
	filterOk(t, MatchesWords, "bar test", "foo-bar (test)", nil)
	filterOk(t, MatchesWords, "foo bar", "foo (bar)", nil)

	filterNotOk(t, MatchesWords, "bar est", "foo-bar test")
	filterNotOk(t, MatchesWords, "fo ar", "foo-bar test")
	filterNotOk(t, MatchesWords, "for", "foo-bar test")

	filterOk(t, MatchesWords, "foo bar", "foo-bar", nil)
	filterOk(t, MatchesWords, "foo bar", "123 foo-bar 456", nil)
	filterOk(t, MatchesWords, "foo-bar", "foo bar", nil)
	filterOk(t, MatchesWords, "foo:bar", "foo:bar", nil)
}

func TestPatternInWord(t *testing.T) {
	if !PatternInWord("getusr", "get user") {
		t.Fatal("subsequence should match")
	}
	if PatternInWord("zzzz", "get user") {
		t.Fatal("absent rune must not match")
	}
	if !PatternInWord("she", "say 你好 hello") {
		t.Fatal("subsequence across non-ASCII text should match")
	}
}

// Expectations from VS Code's korean.test.ts, which supplies
// MatchesWords' alternate character codes.
func TestKoreanAlternateCodes(t *testing.T) {
	if got := string(getAlternateCodes('ㄱ')); got != "r" {
		t.Fatalf("ㄱ = %q, want r", got)
	}
	if got := string(getAlternateCodes('ᄀ')); got != "r" {
		t.Fatalf("ᄀ = %q, want r", got)
	}
	if got := string(getAlternateCodes('ㄳ')); got != "rt" {
		t.Fatalf("ㄳ = %q, want rt", got)
	}
}

func TestMatchesWordsPerformanceIssue309582(t *testing.T) {
	targets := []string{
		"workbench.action.terminal.focusNextLine",
		"editor.action.clipboardCopyAction",
		"workbench.action.editor.changeLanguageMode",
		"editor.action.smartSelect.expand",
		"workbench.action.files.saveAll",
	}
	for range 1000 {
		for _, target := range targets {
			MatchesWords("editor.action", target)
		}
	}
}

func decorateWord(word string, matches []Match) string {
	runes := []rune(word)
	var builder strings.Builder
	position := 0
	for _, match := range matches {
		builder.WriteString(string(runes[position:match.Start]))
		builder.WriteByte('^')
		highlighted := make([]string, 0, match.End-match.Start)
		for _, r := range runes[match.Start:match.End] {
			highlighted = append(highlighted, string(r))
		}
		builder.WriteString(strings.Join(highlighted, "^"))
		position = match.End
	}
	builder.WriteString(string(runes[position:]))
	return builder.String()
}

func assertMatches(t *testing.T, pattern, word, decoratedWord string, opts filterScoreOptions) {
	t.Helper()
	patternLow := lowerString(pattern)
	wordLow := lowerString(word)
	result := fuzzyScore(pattern, patternLow, 0, word, wordLow, 0, opts)
	if decoratedWord == "" {
		if result != nil {
			t.Fatalf("%q on %q unexpectedly matched %v", pattern, word, createFilterMatches(*result))
		}
		return
	}
	if result == nil {
		t.Fatalf("%q didn't match %q", pattern, word)
	}
	if got := decorateWord(word, createFilterMatches(*result)); got != decoratedWord {
		t.Fatalf("%q on %q = %q, want %q", pattern, word, got, decoratedWord)
	}
}

func assertTopScore(t *testing.T, pattern string, expected int, words ...string) {
	t.Helper()
	topScore := -(100 * 10)
	topIndex := 0
	for i, word := range words {
		result := fuzzyScore(pattern, lowerString(pattern), 0, word, lowerString(word), 0, filterScoreOptions{BoostFullMatch: true})
		if result != nil && result.Score > topScore {
			topScore = result.Score
			topIndex = i
		}
	}
	if topIndex != expected {
		t.Fatalf("%q -> top %q (index %d), want index %d", pattern, words[topIndex], topIndex, expected)
	}
}

func TestFuzzyScoreFilterIssues(t *testing.T) {
	opts := filterScoreOptions{BoostFullMatch: true}
	tests := []struct{ pattern, word, decorated string }{
		{"tit", "win.tit", "win.^t^i^t"},
		{"title", "win.title", "win.^t^i^t^l^e"},
		{"WordCla", "WordCharacterClassifier", "^W^o^r^dCharacter^C^l^assifier"},
		{"WordCCla", "WordCharacterClassifier", "^W^o^r^d^Character^C^l^assifier"},
		{"dete", `"editor.quickSuggestionsDelay"`, ""},
		{`c:\do`, `& 'C:\Documents and Settings'`, `& '^C^:^\^D^ocuments and Settings'`},
		{`c:\do`, `& 'c:\Documents and Settings'`, `& '^c^:^\^D^ocuments and Settings'`},
		{"close", "css.lint.importStatement", "^css.^lint.imp^ort^Stat^ement"},
		{"close", "css.colorDecorators.enable", "^css.co^l^orDecorator^s.^enable"},
		{"close", "workbench.quickOpen.closeOnFocusOut", "workbench.quickOpen.^c^l^o^s^eOnFocusOut"},
		{"highlight", "editorHoverHighlight", "editorHover^H^i^g^h^l^i^g^h^t"},
		{"hhighlight", "editorHoverHighlight", "editor^Hover^H^i^g^h^l^i^g^h^t"},
		{"dhhighlight", "editorHoverHighlight", ""},
		{"-moz", "-moz-foo", "^-^m^o^z-foo"},
		{"moz", "-moz-foo", "-^m^o^z-foo"},
		{"moz", "-moz-animation", "-^m^o^z-animation"},
		{"moza", "-moz-animation", "-^m^o^z-^animation"},
	}
	for _, tc := range tests {
		assertMatches(t, tc.pattern, tc.word, tc.decorated, opts)
	}

	assertTopScore(t, "close", 2, "css.lint.importStatement", "css.colorDecorators.enable", "workbench.quickOpen.closeOnFocusOut")
}

func TestFuzzyScoreFilter(t *testing.T) {
	opts := filterScoreOptions{BoostFullMatch: true}
	cases := [][3]string{
		{"ab", "abA", "^a^bA"},
		{"ccm", "cacmelCase", "^ca^c^melCase"},
		{"bti", "the_black_knight", ""},
		{"ccm", "camelCase", ""},
		{"cmcm", "camelCase", ""},
		{"BK", "the_black_knight", "the_^black_^knight"},
		{"KeyboardLayout=", "KeyboardLayout", ""},
		{"LLL", "SVisualLoggerLogsList", "SVisual^Logger^Logs^List"},
		{"LLLL", "SVilLoLosLi", ""},
		{"LLLL", "SVisualLoggerLogsList", ""},
		{"TEdit", "TextEdit", "^Text^E^d^i^t"},
		{"TEdit", "TextEditor", "^Text^E^d^i^tor"},
		{"TEdit", "Textedit", "^Text^e^d^i^t"},
		{"TEdit", "text_edit", "^text_^e^d^i^t"},
		{"TEditDit", "TextEditorDecorationType", "^Text^E^d^i^tor^Decorat^ion^Type"},
		{"TEdit", "TextEditorDecorationType", "^Text^E^d^i^torDecorationType"},
		{"Tedit", "TextEdit", "^Text^E^d^i^t"},
		{"ba", "?AB?", ""},
		{"bkn", "the_black_knight", "the_^black_^k^night"},
		{"bt", "the_black_knight", "the_^black_knigh^t"},
		{"ccm", "camelCasecm", "^camel^Casec^m"},
		{"fdm", "findModel", "^fin^d^Model"},
		{"fob", "foobar", "^f^oo^bar"},
		{"fobz", "foobar", ""},
		{"foobar", "foobar", "^f^o^o^b^a^r"},
		{"form", "editor.formatOnSave", "editor.^f^o^r^matOnSave"},
		{"g p", "Git: Pull", "^Git:^ ^Pull"},
		{"gip", "Git: Pull", "^G^it: ^Pull"},
		{"gp", "Git: Pull", "^Git: ^Pull"},
		{"gp", "Git_Git_Pull", "^Git_Git_^Pull"},
		{"is", "ImportStatement", "^Import^Statement"},
		{"is", "isValid", "^i^sValid"},
		{"lowrd", "lowWord", "^l^o^wWo^r^d"},
		{"myvable", "myvariable", "^m^y^v^aria^b^l^e"},
		{"no", "", ""},
		{"no", "match", ""},
		{"ob", "foobar", ""},
		{"sl", "SVisualLoggerLogsList", "^SVisual^LoggerLogsList"},
		{"sllll", "SVisualLoggerLogsList", "^SVisua^l^Logger^Logs^List"},
		{"Three", "HTMLHRElement", ""},
		{"Three", "Three", "^T^h^r^e^e"},
		{"fo", "barfoo", ""},
		{"fo", "bar_foo", "bar_^f^oo"},
		{"fo", "bar_Foo", "bar_^F^oo"},
		{"fo", "bar foo", "bar ^f^oo"},
		{"fo", "bar.foo", "bar.^f^oo"},
		{"fo", "bar/foo", "bar/^f^oo"},
		{`fo`, `bar\foo`, `bar\^f^oo`},
	}
	for _, tc := range cases {
		assertMatches(t, tc[0], tc[1], tc[2], opts)
	}
}

func TestFuzzyScoreFilterFirstMatchCanBeWeak(t *testing.T) {
	opts := filterScoreOptions{BoostFullMatch: true, FirstMatchCanBeWeak: true}
	assertMatches(t, "Three", "HTMLHRElement", "H^TML^H^R^El^ement", opts)
	assertMatches(t, "tor", "constructor", "construc^t^o^r", opts)
	assertMatches(t, "ur", "constructor", "constr^ucto^r", opts)
	assertTopScore(t, "tor", 2, "constructor", "Thor", "cTor")
}

func TestFuzzyScoreFilterManyMatches(t *testing.T) {
	longWord := strings.Repeat("a", 205)
	pattern := "aaaaaa"
	result := fuzzyScore(pattern, pattern, 0, longWord, longWord, 0, filterScoreOptions{BoostFullMatch: true})
	if result == nil {
		t.Fatal("expected long match")
	}
	if matches := createFilterMatches(*result); len(matches) != 1 || matches[0] != (Match{Start: 0, End: 6}) {
		t.Fatalf("matches = %v", matches)
	}
}

func TestFuzzyScoreFilterFreezeIssues(t *testing.T) {
	cases := []struct {
		pattern, word, decorated string
		weak                     bool
	}{
		{`jfjfj`, `fjfjfjfjfjfjfjfjfjfjfj`, ``, false},
		{`jfjfjfjfjfjfjfjfjfj`, `fjfjfjfjfjfjfjfjfjfjfjfjfjfjfjfjfjfjfjfjfjfjfjfjfjfjfjfjfjfj`, ``, false},
		{`jfjfjfjfjfjfjfjfjfjjfjfjfjfjfjfjfjfjfjjfjfjfjfjfjfjfjfjfjjfjfjfjfjfjfjfjfjfjjfjfjfjfjfjfjfjfjfjjfjfjfjfjfjfjfjfjfj`, `fjfjfjfjfjfjfjfjfjfjfjfjfjfjfjfjfjfjfjfjfjfjfjfjfjfjfjfjfjfjfjfjfjfjfjfjfjfjfjfjfjfjfjfjfjfjfjfjfjfjfjfjfjfjfjfjfjfjfjfj`, ``, false},
		{`jfjfjfjfjfjfjfjfjfj`, `fJfjfjfjfjfjfjfjfjfjfjfjfjfjfjfjfjfjfjfjfjfjfjfjfjfjfjfjfjfj`, `f^J^f^j^f^j^f^j^f^j^f^j^f^j^f^j^f^j^f^jfjfjfjfjfjfjfjfjfjfjfjfjfjfjfjfjfjfjfjfj`, false},
		{`jfjfjfjfjfjfjfjfjfj`, `fjfjfjfjfjfjfjfjfjfjfjfjfjfjfjfjfjfjfjfjfjfjfjfjfjfjfjfjfjfj`, `f^j^f^j^f^j^f^j^f^j^f^j^f^j^f^j^f^j^f^jfjfjfjfjfjfjfjfjfjfjfjfjfjfjfjfjfjfjfjfj`, true},
	}
	for _, tc := range cases {
		options := filterScoreOptions{BoostFullMatch: true, FirstMatchCanBeWeak: tc.weak}
		result := fuzzyScore(tc.pattern, lowerString(tc.pattern), 0, tc.word, lowerString(tc.word), 0, options)
		if tc.decorated == "" {
			if result != nil {
				t.Fatalf("%q on %q unexpectedly matched", tc.pattern, tc.word)
			}
			continue
		}
		if result == nil {
			t.Fatalf("%q didn't match %q", tc.pattern, tc.word)
		}
		if got := decorateWord(tc.word, createFilterMatches(*result)); got != tc.decorated {
			t.Fatalf("%q on %q = %q, want %q", tc.pattern, tc.word, got, tc.decorated)
		}
	}
}

func TestFuzzyScoreFilterIssue26423(t *testing.T) {
	opts := filterScoreOptions{BoostFullMatch: true}
	assertMatches(t, "baba", "abababab", "", opts)
	assertMatches(t, "fsfsfs", "dsafdsafdsafdsafdsafdsafdsafasdfdsa", "", opts)
	assertMatches(t, "fsfsfsfsfsfsfsf", "dsafdsafdsafdsafdsafdsafdsafasdfdsafdsafdsafdsfdsafdsfdfdfasdnfdsajfndsjnafjndsajlknfdsa", "", opts)
}

func TestFuzzyScoreFilterMiscIssues(t *testing.T) {
	opts := filterScoreOptions{BoostFullMatch: true}
	assertMatches(t, "f", ":Foo", ":^Foo", opts)
	assertMatches(t, "f", ":foo", ":^foo", opts)
	assertMatches(t, ".", "foo.bar", "foo^.bar", opts) // #79558

	// #26511: must not explode.
	fuzzyScore(strings.Repeat("a", 119), strings.Repeat("a", 119), 0, strings.Repeat("a", 122), strings.Repeat("a", 122), 0, filterScoreOptions{BoostFullMatch: true})

	assertMatches(t, "g", "  group", "  ^group", opts)
	assertMatches(t, "g g", "  groupGroup", "", opts)
	assertMatches(t, "g g", "  group Group", "  ^group^ ^Group", opts)
	assertMatches(t, "zz", "zzGroup", "^z^zGroup", opts)
	assertMatches(t, "zzg", "zzGroup", "^z^z^Group", opts)
	assertMatches(t, "g", "zzGroup", "zz^Group", opts)

	assertMatches(t, "\t\t<", "\t\t</body>", "^\t^\t^</body>", opts)
	assertMatches(t, "di", "✨div classname=\"\"></div>", "✨^d^iv classname=\"\"></div>", opts) // #113404
	assertMatches(t, "di", "adiv classname=\"\"></div>", "adiv classname=\"\"></^d^iv>", opts)
	assertMatches(t, "SemanticTokens", "SemanticTokensEdits", "^S^e^m^a^n^t^i^c^T^o^k^e^n^sEdits", opts)

	assertMatches(t, "i", "machine/{id}", "machine/{^id}", opts)           // #147423
	assertMatches(t, "ok", "obobobf{ok}/user", "^obobobf{o^k}/user", opts) // #147423
}

func TestFuzzyScoreFilterPatternAndWordPositions(t *testing.T) {
	opts := filterScoreOptions{BoostFullMatch: true}
	cases := []struct {
		pattern, word, decorated string
		patternPos, wordPos      int
		weak                     bool
	}{
		{"  g", "  group", "  ^group", 2, 0, false},
		{" g g", "  group Group", "  ^group^ ^Group", 1, 0, false},
		{"p", "prop", "^prop", 0, 0, false},
		{":p", "prop", "^prop", 1, 0, false},
		{":p", "prop", "", 2, 0, false},
		{":p", "proP", "pro^P", 1, 1, false},
		{":p", "aprop", "a^prop", 1, 0, true},
		{":p", "aprop", "", 1, 0, false},
		{"\t\t<", "\t\t</body>", "\t\t^</body>", 2, 0, false},
		{"\t<", "\t</body>", "\t^</body>", 1, 0, false},
	}
	for _, tc := range cases {
		options := opts
		options.FirstMatchCanBeWeak = tc.weak
		result := fuzzyScore(tc.pattern, lowerString(tc.pattern), tc.patternPos, tc.word, lowerString(tc.word), tc.wordPos, options)
		if tc.decorated == "" {
			if result != nil {
				t.Fatalf("%q/%d on %q/%d unexpectedly matched", tc.pattern, tc.patternPos, tc.word, tc.wordPos)
			}
			continue
		}
		if result == nil {
			t.Fatalf("%q/%d didn't match %q/%d", tc.pattern, tc.patternPos, tc.word, tc.wordPos)
		}
		if got := decorateWord(tc.word, createFilterMatches(*result)); got != tc.decorated {
			t.Fatalf("%q/%d on %q/%d = %q, want %q", tc.pattern, tc.patternPos, tc.word, tc.wordPos, got, tc.decorated)
		}
	}
}

func TestFuzzyScoreFilterTopScores(t *testing.T) {
	topScoreTests := []struct {
		pattern  string
		expected int
		words    []string
	}{
		{"cons", 2, []string{"ArrayBufferConstructor", "Console", "console"}},
		{"Foo", 1, []string{"foo", "Foo", "foo"}},
		{"onMess", 1, []string{"onmessage", "onMessage", "onThisMegaEscape"}},
		{"CC", 1, []string{"camelCase", "CamelCase"}},
		{"cC", 0, []string{"camelCase", "CamelCase"}},
		{"p", 4, []string{"parse", "posix", "pafdsa", "path", "p"}},
		{"pa", 0, []string{"parse", "pafdsa", "path"}},
		{"log", 3, []string{"HTMLOptGroupElement", "ScrollLogicalPosition", "SVGFEMorphologyElement", "log", "logger"}},
		{"e", 2, []string{"AbstractWorker", "ActiveXObject", "else"}},
		{"workbench.sideb", 1, []string{"workbench.editor.defaultSideBySideLayout", "workbench.sideBar.location"}},
		{"editor.r", 2, []string{"diffEditor.renderSideBySide", "editor.overviewRulerlanes", "editor.renderControlCharacter", "editor.renderWhitespace"}},
		{"-mo", 1, []string{"-ms-ime-mode", "-moz-columns"}},
		{"convertModelPosition", 0, []string{"convertModelPositionToViewPosition", "convertViewToModelPosition"}},
		{"is", 0, []string{"isValidViewletId", "import statement"}},
		{"title", 1, []string{"files.trimTrailingWhitespace", "window.title"}},
		{"const", 1, []string{"constructor", "const", "cuOnstrul"}},
	}
	for _, tc := range topScoreTests {
		assertTopScore(t, tc.pattern, tc.expected, tc.words...)
	}
	assertTopScore(t, "_lines", 1, "_lineStarts", "_lines")
	assertTopScore(t, "_lines", 1, "_lineS", "_lines")
	assertTopScore(t, "_lineS", 0, "_lineS", "_lines")
}

func TestFuzzyScoreFilterFullMatchBoost(t *testing.T) {
	prefix := "create"
	a := "createModelServices"
	b := "create"
	aBoost := fuzzyScore(prefix, prefix, 0, a, a, 0, filterScoreOptions{BoostFullMatch: true, FirstMatchCanBeWeak: true})
	bBoost := fuzzyScore(prefix, prefix, 0, b, b, 0, filterScoreOptions{BoostFullMatch: true, FirstMatchCanBeWeak: true})
	if aBoost == nil || bBoost == nil || aBoost.Score >= bBoost.Score {
		t.Fatalf("boost = %v, %v", aBoost, bBoost)
	}

	const wordPrefix = "$(symbol-function) "
	boostingA := wordPrefix + a
	boostingB := wordPrefix + b
	aBoost = fuzzyScore(prefix, prefix, 0, boostingA, boostingA, len(wordPrefix), filterScoreOptions{BoostFullMatch: true, FirstMatchCanBeWeak: true})
	bBoost = fuzzyScore(prefix, prefix, 0, boostingB, boostingB, len(wordPrefix), filterScoreOptions{BoostFullMatch: true, FirstMatchCanBeWeak: true})
	if aBoost == nil || bBoost == nil || aBoost.Score >= bBoost.Score {
		t.Fatalf("boost with offset = %v, %v", aBoost, bBoost)
	}

	aScore := fuzzyScore(prefix, prefix, 0, a, a, 0, filterScoreOptions{FirstMatchCanBeWeak: true})
	bScore := fuzzyScore(prefix, prefix, 0, b, b, 0, filterScoreOptions{FirstMatchCanBeWeak: true})
	if aScore == nil || bScore == nil || aScore.Score != bScore.Score {
		t.Fatalf("without boost = %v, %v", aScore, bScore)
	}
}

func TestFuzzyScoreFilterLongHighlights(t *testing.T) {
	opts := filterScoreOptions{BoostFullMatch: true}
	assertMatches(t, "foo", "barbarbarbarbarbarbarbarbarbarbarbarbarbarbarbar_foo",
		"barbarbarbarbarbarbarbarbarbarbarbarbarbarbarbar_^f^o^o", opts)
	assertMatches(t, "foo",
		"ffffffffffffffffffffffffffffbarbarbarbarbarbarbarbarbarbarbarbarbarbarbarbarbarbarbarbarbarbarbarbarbarbarbarbarbarbarbarbar_foo",
		"ffffffffffffffffffffffffffffbarbarbarbarbarbarbarbarbarbarbarbarbarbarbarbarbarbarbarbarbarbarbarbarbarbarbarbarbarbarbarbar_^f^o^o", opts)
	assertMatches(t, "Aoo",
		"Affffffffffffffffffffffffffffbarbarbarbarbarbarbarbarbarbarbarbarbarbarbarbarbarbarbarbarbarbarbarbarbarbarbarbarbarbar_foo",
		"^Affffffffffffffffffffffffffffbarbarbarbarbarbarbarbarbarbarbarbarbarbarbarbarbarbarbarbarbarbarbarbarbarbarbarbarbarbar_f^o^o", opts)
	assertMatches(t, "foo",
		"Gffffffffffffffffffffffffffffbarbarbarbarbarbarbarbarbarbarbarbarbarbarbarbarbarbarbarbarbarbarbarbarbarbarbarbarbarbarbarbar_foo",
		"", opts)
}
