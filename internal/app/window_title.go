package app

import (
	"runtime"

	"github.com/baba983/vscroxy/locales"
)

// titleWindow is the part of the main window the title keeps in sync. A
// Tester has no native window, so tests supply a counter through this seam.
type titleWindow interface {
	SetTitle(string)
	SetDocumentEdited(bool)
}

// windowName is the part of the title that says what is on screen. Request
// tabs contribute only their own name and collection path: never a URL,
// header, body or secret.
func windowName(a *app) string {
	switch a.page {
	case pageRequest:
		d := a.requestDocument
		if d == nil {
			return locales.Request()
		}
		if d.composeCollection == "" {
			return d.title()
		}
		return d.title() + " — " + d.composeCollection
	case pageLive:
		return locales.Live()
	case pageSession:
		return locales.Sessions()
	case pageAPI:
		return locales.ApiContracts()
	case pageRuns:
		return locales.RunsPage()
	case pageDiagnostics:
		return locales.Diagnostics()
	case pageOverview:
		return locales.Overview()
	default:
		return "vscroxy"
	}
}

// windowTitleText marks an unsaved document in the title only where there is
// no native edited marker: on macOS SetDocumentEdited shows a dot in the
// close button, so repeating it in the text would be noise.
func windowTitleText(name string, edited, nativeMarker bool) string {
	if edited && !nativeMarker {
		name = "● " + name
	}
	return name + " — vscroxy"
}

// anyDocumentEdited covers every open request, not only the active tab: a
// window with unsaved work stays marked while the reader looks elsewhere.
func anyDocumentEdited(a *app) bool {
	for _, d := range a.documents {
		if d != nil && d.dirty() {
			return true
		}
	}
	for _, d := range []*requestDocument{a.requestDocument} {
		if d != nil && d.dirty() {
			return true
		}
	}
	return false
}

// syncTitle writes only what changed. MyGo's SetDocumentEdited already
// ignores an unchanged flag, but keeping the last values here also avoids
// a SetTitle round trip on every frame.
func (a *app) syncTitle() {
	a.syncTitleFor(runtime.GOOS == "darwin")
}

func (a *app) syncTitleFor(nativeMarker bool) {
	if a.titleWindow == nil {
		return
	}
	edited := anyDocumentEdited(a)
	title := windowTitleText(windowName(a), edited, nativeMarker)
	if !a.titleSynced || title != a.windowTitle {
		a.titleWindow.SetTitle(title)
		a.windowTitle = title
	}
	// Off macOS MyGo marks an edited document with "*" in the native title,
	// so the dot above is the marker there and the native flag stays off.
	edited = edited && nativeMarker
	if !a.titleSynced || edited != a.windowEdited {
		a.titleWindow.SetDocumentEdited(edited)
		a.windowEdited = edited
	}
	a.titleSynced = true
}
