package app

import (
	"github.com/egoist/mygo/ui"
	"github.com/egoist/mygo/ui/editor"
)

// bodyFindState is the find bar of one body pane, kept across builds so the
// command registry can tell whether a find shortcut belongs to the focused
// bar. The bar binds its handle on every pass it is shown.
type bodyFindState struct {
	handle ui.Handle
	find   *editor.FindBar
}

func (s *bodyFindState) toggleCase() {
	if s != nil && s.find != nil {
		*s.find.CaseSensitive() = !*s.find.CaseSensitive()
	}
}

func (s *bodyFindState) toggleWord() {
	if s != nil && s.find != nil {
		*s.find.WholeWord() = !*s.find.WholeWord()
	}
}

func (s *bodyFindState) toggleRegexp() {
	if s != nil && s.find != nil {
		*s.find.Regexp() = !*s.find.Regexp()
	}
}
