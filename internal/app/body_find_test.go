package app

import (
	"strings"
	"testing"

	"github.com/egoist/mygo/ui"
	"github.com/egoist/mygo/ui/editor"
)

// settleBodyFind draws until the bar owns the focus and the commands read
// that binding. A first pass can hand the focus to the workbench instead.
func settleBodyFind(t *testing.T, a *app, tt *ui.Tester, c *ui.Context) *editor.FindBar {
	t.Helper()
	for range 6 {
		tt.Frame()
		state := &a.bodyFind[0]
		if state.find != nil && state.handle.FocusWithin(c) {
			return state.find
		}
		a.bodyText[0].Run(editor.Find)
	}
	t.Fatalf("find bar never took focus: %v", tt.Texts())
	return nil
}

// runFindToggle uses the registry's command for the focused bar. MyGo turns
// Option+letter into text on macOS before a shortcut sees it, so pressing a
// key in a tester is not the contract; the registry's owner and its focus
// precondition are. Focus can move for a frame while a tester builds, so let
// the bar settle before asking the registry as the tests do when opening it.
func runFindToggle(t *testing.T, a *app, tt *ui.Tester, c *ui.Context, id string) {
	t.Helper()
	var found action
	for range 6 {
		found = pick(a.actions(c), id)
		if found.id != "" && found.enabled {
			break
		}
		if a.bodyText[0] != nil {
			a.bodyText[0].Run(editor.Find)
		}
		tt.Frame()
	}
	if found.id == "" || !found.enabled {
		t.Fatalf("find command %s is unavailable while the bar is focused", id)
	}
	if found.shortcut.key == 0 {
		t.Fatalf("find command %s has no shortcut owner", id)
	}
	found.run()
	tt.Frame()
}

// openBodyFind opens the response body's find bar in the real window, so the
// shortcuts come from the command registry and the bar has the focus.
func openBodyFind(t *testing.T, body string) (*app, *ui.Tester, *ui.Context, *editor.FindBar) {
	t.Helper()
	a := snapshotApp()
	a.flows[0].resBody = body
	a.bodyModes(a.flows[0], 0)
	a.bodyMode[0] = 1
	var c *ui.Context
	tt := ui.NewTester(func(ctx *ui.Context) { c = ctx; a.view(ctx) }, 1440, 900)
	tt.Frame()
	if a.bodyText[0] == nil {
		t.Fatal("the response body has no reader")
	}
	a.bodyText[0].Run(editor.Find)
	find := settleBodyFind(t, a, tt, c)
	return a, tt, c, find
}

// TestBodyFindOptionKeysPressOnce sends the real Option+letter keys with the
// find bar focused. A key that reaches both the query and the command registry
// toggles twice; one that reaches text input inserts a letter into the query.
func TestBodyFindOptionKeysPressOnce(t *testing.T) {
	tests := []struct {
		name  string
		key   ui.Key
		value func(*editor.FindBar) *bool
	}{
		{"match-case", ui.KeyC, func(f *editor.FindBar) *bool { return f.CaseSensitive() }},
		{"whole-word", ui.KeyW, func(f *editor.FindBar) *bool { return f.WholeWord() }},
		{"regexp", ui.KeyR, func(f *editor.FindBar) *bool { return f.Regexp() }},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			a, tester, c, _ := openBodyFind(t, "alpha beta")
			// The bar's state is rebound on some frames; always read the one
			// the view is using now, not the pointer from the first frame.
			current := func() *editor.FindBar { return a.bodyFind[0].find }
			if !a.bodyFind[0].handle.FocusWithin(c) {
				t.Fatal("the find bar is not focused")
			}
			*current().Query() = "alpha"
			tester.Frame()
			wantCase, wantWord, wantRegexp := *current().CaseSensitive(), *current().WholeWord(), *current().Regexp()
			for press := range 2 {
				if !a.bodyFind[0].handle.FocusWithin(c) {
					settleBodyFind(t, a, tester, c)
				}
				tester.Key(ui.Alt, tt.key)
				tester.Frame()
				find := current()
				if find == nil {
					t.Fatalf("press %d left no find bar", press+1)
				}
				var want bool
				switch tt.name {
				case "match-case":
					wantCase, want = !wantCase, !wantCase
				case "whole-word":
					wantWord, want = !wantWord, !wantWord
				case "regexp":
					wantRegexp, want = !wantRegexp, !wantRegexp
				}
				if got := *tt.value(find); got != want {
					t.Fatalf("press %d did not flip %s exactly once: got %v, want %v", press+1, tt.name, got, want)
				}
				if *find.Query() != "alpha" {
					t.Errorf("press %d changed the query to %q", press+1, *find.Query())
				}
				if *find.CaseSensitive() != wantCase || *find.WholeWord() != wantWord || *find.Regexp() != wantRegexp {
					t.Errorf("press %d changed another option: case %v, word %v, regexp %v",
						press+1, *find.CaseSensitive(), *find.WholeWord(), *find.Regexp())
				}
			}
		})
	}
}

func TestBodyFindCaseSensitiveNeedsFindFocus(t *testing.T) {
	a, tt, c, find := openBodyFind(t, "error Error error")
	find.Close()
	tt.Frame()
	if x := pick(a.actions(c), "find.match-case"); x.enabled {
		t.Fatal("Alt+C owns a find toggle while no bar is focused")
	}
	a.bodyText[0].Run(editor.Find)
	find = settleBodyFind(t, a, tt, c)
	*find.Query() = "error"
	tt.Frame()
	loose, _ := find.Matches()
	runFindToggle(t, a, tt, c, "find.match-case")
	if !*find.CaseSensitive() {
		t.Fatal("the focused case command did not toggle case sensitivity")
	}
	strict, _ := find.Matches()
	if loose != 3 || strict != 2 {
		t.Errorf("matches = %d insensitive, %d sensitive; want 3 then 2", loose, strict)
	}
}

// A fault-tolerant answer that reformats the body must change the existing
// reader, not close its find bar: query, options and focus survive, and the
// next build searches the formatted text.
func TestBodyFindSurvivesReformattedAnswer(t *testing.T) {
	body := `{"a":1,// note
"b":2,}`
	result := computeFaultTolerantBody(body)
	formatted, isJSON := prettyBodyText(body, &jsonReader{ready: true, result: result})
	query := `"a": 1` // only the formatted text has the space
	if !result.prettyOK || !isJSON || formatted == body || !strings.Contains(formatted, query) {
		t.Fatalf("the fixture answer did not format the query into the body: %q", formatted)
	}

	a := snapshotApp()
	a.flows[0].resBody, a.body, a.detailFor = body, body, a.flows[0].id
	a.bodyModes(a.flows[0], 0)
	a.bodyMode[0] = 1
	// Hold the worker's answer so it lands after the query and the option are
	// set, without depending on when the parsing goroutine finishes.
	a.jsonReader[0] = jsonReader{key: "1:0", body: body, contentType: "application/json", pending: true}
	var c *ui.Context
	tt := ui.NewTester(func(ctx *ui.Context) { c = ctx; a.view(ctx) }, 1440, 900)
	tt.Frame()
	if a.bodyText[0] == nil {
		t.Fatal("the response body has no reader")
	}
	a.bodyText[0].Run(editor.Find)
	settleBodyFind(t, a, tt, c)
	tt.Type(query)
	tt.Frame()
	find := a.bodyFind[0].find
	if got := *find.Query(); got != query {
		t.Fatalf("typed query = %q, want %q", got, query)
	}
	if n, _ := find.Matches(); n != 0 {
		t.Fatalf("%d matches before the answer, want 0", n)
	}
	runFindToggle(t, a, tt, c, "find.match-case")
	if !*a.bodyFind[0].find.CaseSensitive() {
		t.Fatal("the option was not set before the answer")
	}

	reader := a.bodyText[0]
	a.finishJSONRead(0, "1:0", body, "application/json", result)
	tt.Frame()
	if a.bodyText[0] != reader {
		t.Fatal("the answer replaced the reader and its find bar")
	}
	if got := a.bodyText[0].Buffer.String(); got != formatted {
		t.Fatalf("reader text = %q, want %q", got, formatted)
	}
	if !a.bodyFind[0].handle.FocusWithin(c) {
		t.Fatal("the answer took focus from the find bar")
	}
	find = a.bodyFind[0].find
	if find == nil {
		t.Fatal("the answer left no find bar")
	}
	if got := *find.Query(); got != query || !*find.CaseSensitive() {
		t.Fatalf("after the answer: query %q, case %v; want %q, true", got, *find.CaseSensitive(), query)
	}
	if n, current := find.Matches(); n != 1 || current != 0 {
		t.Fatalf("formatted body search = %d matches, current %d; want 1, 0", n, current)
	}
}

func TestBodyFindWholeWordExcludesSubstrings(t *testing.T) {
	a, tt, c, find := openBodyFind(t, "error errors error")
	*find.Query() = "error"
	tt.Frame()
	before, _ := find.Matches()
	runFindToggle(t, a, tt, c, "find.match-whole-word")
	if !*find.WholeWord() {
		t.Fatal("the focused whole-word command did not toggle whole word")
	}
	after, _ := find.Matches()
	if before != 3 || after != 2 {
		t.Errorf("matches = %d all words, %d whole words; want 3 then 2", before, after)
	}
}

func TestBodyFindBadRegexShowsTheErrorAndNoMatches(t *testing.T) {
	a, tt, c, find := openBodyFind(t, "error Error")
	*find.Query() = "error"
	tt.Frame()
	if count, _ := find.Matches(); count != 2 {
		t.Fatalf("%d matches before regexp, want 2", count)
	}
	runFindToggle(t, a, tt, c, "find.use-regexp")
	if !*find.Regexp() {
		t.Fatal("the focused regexp command did not toggle regular expressions")
	}
	*find.Query() = "["
	tt.Frame()
	if err := find.Err(); err == nil {
		t.Fatal("a broken regular expression has no error")
	} else if count, _ := find.Matches(); count != 0 {
		t.Errorf("%d matches for a broken regular expression, want none", count)
	} else if !tt.HasText(err.Error()) {
		t.Errorf("invalid regex error is not inline: %v", tt.Texts())
	}
}
