package app

import (
	"slices"
	"sync"
	"weak"

	"github.com/egoist/mygo/i18n"
	"github.com/egoist/mygo/ui/editor"
)

// unicodeCachedDiagnostics keeps a scan of an editor's text so the build pass
// can add the same warnings every frame without rescanning a large body. The
// key uses a weak pointer, so a closed editor's entry can be collected with
// the editor. The buffer version and language make an edit or a locale change
// invalidate the entry.
type unicodeCachedDiagnostics struct {
	version     uint64
	locale      string
	diagnostics []editor.Diagnostic
}

var unicodeDiagnosticsByEditor sync.Map // weak.Pointer[editor.State] -> unicodeCachedDiagnostics

func (a *app) cachedUnicodeDiagnostics(state *editor.State, text string) []editor.Diagnostic {
	if state == nil {
		return a.unicodeDiagnostics(text)
	}
	version := state.Buffer.Version()
	locale := a.language
	if locale == "" {
		locale = i18n.Locale()
	}
	key := weak.Make(state)
	if cached, ok := unicodeDiagnosticsByEditor.Load(key); ok {
		entry := cached.(unicodeCachedDiagnostics)
		if entry.version == version && entry.locale == locale {
			return slices.Clone(entry.diagnostics)
		}
	}
	diagnostics := a.unicodeDiagnostics(text)
	unicodeDiagnosticsByEditor.Store(key, unicodeCachedDiagnostics{
		version: version, locale: locale, diagnostics: diagnostics,
	})
	return slices.Clone(diagnostics)
}
