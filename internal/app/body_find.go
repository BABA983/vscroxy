package app

import (
	"runtime"

	"github.com/baba983/vscroxy/internal/uikit"
	"github.com/baba983/vscroxy/locales"
	"github.com/egoist/mygo/ui"
	"github.com/egoist/mygo/ui/editor"
)

// findTarget is one set of find options: the three toggles the body's find
// bar and the Search view share. The body's values live in editor.FindBar;
// the Search view owns its own. Toggling, the Alt+C/W/R keys and the compact
// controls are implemented once here, so a key has one owner and one meaning.
type findTarget interface {
	caseOption() *bool
	wordOption() *bool
	regexpOption() *bool
}

func toggleFindCase(target findTarget) {
	if target == nil {
		return
	}
	*target.caseOption() = !*target.caseOption()
}

func toggleFindWord(target findTarget) {
	if target == nil {
		return
	}
	*target.wordOption() = !*target.wordOption()
}

func toggleFindRegexp(target findTarget) {
	if target == nil {
		return
	}
	*target.regexpOption() = !*target.regexpOption()
}

// bindFindOptionKeys lets Alt+C/W/R reach the options while typing in a find
// field. On macOS an Option+letter arrives as text, so the keyup does it.
func bindFindOptionKeys(query ui.Element, target findTarget) {
	keyKind := ui.InputKeyDown
	if runtime.GOOS == "darwin" {
		keyKind = ui.InputKeyUp
	}
	query.HandleInput(func(ev ui.InputEvent) bool {
		if ev.Kind != keyKind || ev.Mods != ui.Alt {
			return false
		}
		switch {
		case ev.Key == ui.KeyC || ev.Code == ui.CodeKeyC:
			toggleFindCase(target)
		case ev.Key == ui.KeyW || ev.Code == ui.CodeKeyW:
			toggleFindWord(target)
		case ev.Key == ui.KeyR || ev.Code == ui.CodeKeyR:
			toggleFindRegexp(target)
		default:
			return false
		}
		return true
	})
}

// findOptionToggles draws the bar's three compact switches from the app's
// catalog, with the same registry commands behind their shortcuts.
func findOptionToggles(c *ui.Context, target findTarget) {
	caseToggle := ui.Toggle(c, target.caseOption(), locales.FindCaseMark()).KeepFocus().KeyShortcuts(ui.Alt, ui.KeyC)
	uikit.Tip(c, caseToggle.Label(locales.FindMatchCase()), locales.FindMatchCaseTip())
	wordToggle := ui.Toggle(c, target.wordOption(), locales.FindWordMark()).KeepFocus().KeyShortcuts(ui.Alt, ui.KeyW)
	uikit.Tip(c, wordToggle.Label(locales.FindMatchWholeWord()), locales.FindMatchWholeWordTip())
	regexToggle := ui.Toggle(c, target.regexpOption(), locales.FindRegexpMark()).KeepFocus().KeyShortcuts(ui.Alt, ui.KeyR)
	uikit.Tip(c, regexToggle.Label(locales.FindUseRegexp()), locales.FindUseRegexpTip())
}

// bodyFindState is the find bar of one body pane, kept across builds so the
// command registry can tell whether a find shortcut belongs to the focused
// bar. The bar binds its handle on every pass it is shown.
type bodyFindState struct {
	handle ui.Handle
	find   *editor.FindBar
}

func (s *bodyFindState) caseOption() *bool   { return s.find.CaseSensitive() }
func (s *bodyFindState) wordOption() *bool   { return s.find.WholeWord() }
func (s *bodyFindState) regexpOption() *bool { return s.find.Regexp() }
