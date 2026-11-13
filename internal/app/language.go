package app

// The language of the app. English is the source language and the default;
// Simplified Chinese is the other one the catalogs carry. The setting says
// which language the app shows: "" follows the computer's languages, as
// MyGo's App.Locale resolves them, and a tag chooses one.
//
// MyGo keeps the choice too, so that its own controls and menus, the text
// engine and the windows all show the same language; the settings file is
// this app's record, which the Language menu reads and which is applied
// before the first window opens.

import (
	"slices"
	"time"

	"github.com/baba983/vscroxy/internal/uikit"
	"github.com/baba983/vscroxy/locales"
	"github.com/egoist/mygo"
	"github.com/egoist/mygo/i18n"
	"github.com/egoist/mygo/ui"
)

// languages are the choices of Settings → General → Language, in order:
// following the computer, then the languages the catalogs carry.
var languages = []string{"", "en", "zh-Hans"}

// languageName is a language in itself, as a menu of languages names them
// ("English", "简体中文"), and System in the app's language.
func languageName(tag string) string {
	if tag == "" {
		return locales.System()
	}
	return i18n.Autonym(tag)
}

// languageNames are languageName for every choice.
func languageNames() []string {
	names := make([]string, len(languages))
	for i, tag := range languages {
		names[i] = languageName(tag)
	}
	return names
}

// languageSettings is Settings → General → Language.
func (a *app) languageSettings(c *ui.Context) {
	uikit.SettingsRow(c, locales.Language(), func() ui.Element {
		names := languageNames()
		choice := max(slices.Index(languages, a.language), 0)
		sel := uikit.Select(c, &names[choice], names).Label(locales.Language()).Width(160)
		if sel.Changed() {
			if i := slices.Index(names, names[choice]); i >= 0 && languages[i] != a.language {
				a.setLanguage(languages[i])
			}
		}
		return sel
	}, locales.LanguageDescription())
}

// setLanguage shows tag at once and keeps it for the next launches.
func (a *app) setLanguage(tag string) {
	a.language = tag
	a.lastSave = time.Time{} // an explicit choice is not debounced
	if err := a.persistSettings(); err != nil {
		a.fail(locales.LanguageNotSaved(), err)
	}
	mygo.App.SetLocale(tag)
}

// stateLabel names a protocol session's state in the app's language. The
// state itself stays as the session records it, so comparisons keep working.
func stateLabel(state string) string {
	switch state {
	case "Not connected":
		return locales.NotConnected()
	case "Connected":
		return locales.Connected()
	case "Connecting":
		return locales.Connecting()
	case "Disconnecting":
		return locales.Disconnecting()
	case "Disconnected":
		return locales.Disconnected()
	case "Reconnecting":
		return locales.Reconnecting()
	case "Failed":
		return locales.Failed()
	case "Closed":
		return locales.Closed()
	case "Stopped":
		return locales.Stopped()
	case "Starting":
		return locales.Starting()
	case "Paused":
		return locales.PausedState()
	case "Active":
		return locales.Active()
	}
	return state
}

// watchLocale redraws what a language change does not redraw by itself: the
// menu's labels, which the app sets once, and the titles of its windows.
func (a *app) watchLocale() {
	mygo.App.OnLocaleChanged(func(string) {
		mygo.App.SetMenu(a.menu())
		if a.settingsWin != nil {
			a.settingsWin.SetTitle(locales.Settings())
			a.settingsWin.Invalidate()
		}
		if a.toolsWin != nil {
			a.toolsWin.SetTitle(locales.Tools())
			a.toolsWin.Invalidate()
		}
		if a.win != nil {
			a.win.Invalidate()
		}
	})
}
