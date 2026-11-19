package app

import (
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
