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
// precondition are.
func runFindToggle(t *testing.T, a *app, tt *ui.Tester, c *ui.Context, id string) {
	t.Helper()
	action := pick(a.actions(c), id)
	if action.id == "" || !action.enabled {
		t.Fatalf("find command %s is unavailable while the bar is focused", id)
	}
	if action.shortcut.key == 0 {
		t.Fatalf("find command %s has no shortcut owner", id)
	}
	action.run()
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
