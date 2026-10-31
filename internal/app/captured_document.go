package app

import (
	"cmp"
	"fmt"
	"slices"

	"github.com/baba983/vscroxy/internal/uikit"
	"github.com/egoist/mygo/ui"
)

// A captured HTTP request opens as a request document in place: its method,
// URL and parts can be edited and sent from the inspector. Until something
// changes, the document is the capture as it was (live) and nothing holds
// it; the first edit or send makes it the capture's draft (flowDrafts),
// which stays with the row while the capture does.

// liveDocument is the document the inspector edits for f: its draft, or the
// unchanged capture. It is nil, with the reason, for a capture the editor
// cannot represent; and nil, without one, for streams and tunnels, which
// are read only.
func (a *app) liveDocument(f flow) (*requestDocument, string) {
	if d := a.flowDrafts[f.id]; d != nil {
		return d, ""
	}
	if a.liveFor == f.id && (a.live != nil || a.liveProblem != "") {
		return a.live, a.liveProblem
	}
	a.live, a.liveFor, a.liveProblem = nil, f.id, ""
	if f.kind != "" {
		return nil, ""
	}
	// A body spilled to disk is read only when asked for, never as the
	// choice moves over its row.
	if f.reqFile != "" && !a.liveLoad {
		a.liveProblem = "spilled"
		return nil, a.liveProblem
	}
	a.liveLoad = false
	r, err := f.requestDraft()
	if err != nil {
		a.liveProblem = "Not editable: " + err.Error()
		return nil, a.liveProblem
	}
	d := a.newDocument(r, "")
	d.setDebugSource(newDebugSource(f))
	d.baseline = d.value()
	// Read first: an unchanged capture opens in its own readers, with the
	// editor's tabs beside them.
	d.composeTab = composeTabOriginal
	a.live = d
	return d, ""
}

// keepLiveDraft makes the unchanged capture's document its draft once it is
// edited or sent: copy on write.
func (a *app) keepLiveDraft() {
	d := a.live
	if d == nil || !d.dirty() && !d.sentSet && !d.composeWaiting && d.response == nil {
		return
	}
	if a.flowDrafts == nil {
		a.flowDrafts = map[int]*requestDocument{}
	}
	a.flowDrafts[a.liveFor] = d
	a.live = nil
}

// edited reports whether the capture f has a draft that differs from it or
// was sent.
func (a *app) edited(id int) bool {
	d := a.flowDrafts[id]
	return d != nil && (d.dirty() || d.sentSet)
}

// syncDocument points the app's document at what the content area shows:
// the inspected capture's in LIVE, a tab's in COLLECTIONS, none elsewhere.
func (a *app) syncDocument() {
	a.keepLiveDraft()
	for id := range a.flowDrafts {
		if !slices.ContainsFunc(a.flows, func(f flow) bool { return f.id == id }) {
			delete(a.flowDrafts, id) // The capture is gone, and its draft with it.
		}
	}
	if d := a.requestDocument; d != nil && slices.Contains(a.documents, d) {
		a.libraryDocument = d
	}
	// One request shows at a time. A saved one left unchanged and unsent
	// reopens from its collection exactly as it was, so it closes when it
	// is no longer on screen; the others stay open in the sidebar.
	a.documents = slices.DeleteFunc(a.documents, func(d *requestDocument) bool {
		return d != a.requestDocument && d != a.libraryDocument && d.savedKey != "" &&
			!d.dirty() && !d.sentSet && !d.composeWaiting && d.response == nil
	})
	switch {
	case a.page == pageLive:
		a.requestDocument = nil
		if f, ok := a.current(); ok && a.chosen.Len() <= 1 {
			a.requestDocument, _ = a.liveDocument(f)
		}
	case a.page.capture():
		a.requestDocument = nil
	case !slices.Contains(a.documents, a.requestDocument):
		a.requestDocument = nil
		if slices.Contains(a.documents, a.libraryDocument) {
			a.requestDocument = a.libraryDocument
		} else if n := len(a.documents); n > 0 {
			a.requestDocument = a.documents[n-1]
		}
	}
}

// editCapture shows the capture f in LIVE as its draft, ready to change.
func (a *app) editCapture(f flow) *requestDocument {
	if !slices.ContainsFunc(a.flows, func(g flow) bool { return g.id == f.id }) {
		a.setMessage("Capture is no longer in this session")
		return nil
	}
	a.inspectCapture(f.id)
	a.page, a.workbench.focused = pageLive, false
	a.liveLoad = true
	if a.liveFor == f.id && a.live == nil {
		a.liveFor = 0 // Try again, now reading a spilled body.
	}
	d, problem := a.liveDocument(f)
	if d == nil {
		a.setMessage(cmp.Or(problem, "This capture is read only"))
		return nil
	}
	a.requestDocument = d
	d.urlInput.Focus()
	return d
}

// capturedHeader names where the document came from and what the capture
// produced, with the actions that read or copy it.
func (a *app) capturedHeader(c *ui.Context, t *ui.Theme, f flow, d *requestDocument) {
	compact := a.inspectorWidth(c) < 600
	ui.Row(c).Height(uikit.HeightLarge).Padding(0, t.Space(3)).Gap(t.Space(2)).AlignItems(ui.Center).Children(func() {
		source := "Captured · " + f.at.Local().Format("15:04:05")
		if f.at.IsZero() {
			source = fmt.Sprintf("Captured #%d", f.id)
		}
		if f.process != "" {
			source += " · " + f.process
		}
		// Assistive technology hears the request itself, as the bar below
		// shows it.
		title := f.method + " " + cmp.Or(f.url, f.scheme+"://"+f.host+f.path)
		uikit.Meta(c, source).Label(title).FontFeatures("tnum").SingleLine().MinWidth(0).Tooltip(title)
		if d == nil {
			// Streams and tunnels read as they were; the path names them.
			uikit.Mono(c, f.method+" "+cmp.Or(f.path, "/")).SingleLine().MinWidth(48).Tooltip(f.url)
		}
		if a.edited(f.id) {
			// The source names give way first; the mark and the status stay.
			ui.Text(c, "Edited").FontSize(uikit.TextMeta).TextColor(t.Accent).SingleLine().Shrink(0)
		}
		ui.Spacer(c)
		a.exchangeResult(c, t, f, compact)
		copy := uikit.ToolbarButton(c, "Copy as cURL", "Copy the captured request as cURL", false).Padding(0, t.Space(1))
		if copy.Clicked() {
			a.copyCurl(c, f)
		}
		copy.Children(func() {
			ui.Icon(c, copyIcon).Size(uikit.IconInline, uikit.IconInline)
			if !compact {
				uikit.Body(c, "Copy as cURL")
			}
		})
		info := uikit.IconButton(c, "Connection details", "Connection: addresses, DNS and TLS", infoIcon)
		info.OnClick(func() { a.connOpen = !a.connOpen })
		ui.Popover(c, info, &a.connOpen, func() { a.connPanel(c, t, f) })
		a.inspectorMenu(c, t, f)
	})
	ui.Divider(c)
}

// capturedDocument is a captured HTTP request as an editable document: the
// request bar, the request's parts with the capture's own reader beside
// them, and the captured response until the document is sent.
func (a *app) capturedDocument(c *ui.Context, t *ui.Theme, f flow, d *requestDocument) {
	a.requestBar(c, t, d, func() {
		// A narrow inspector keeps its width for the URL.
		var save ui.Element
		if a.inspectorWidth(c) < 600 {
			save = uikit.IconButton(c, "Save to collection", "Save this request to a collection · Ctrl/Cmd+S", saveIcon)
		} else {
			save = uikit.SecondaryButton(c, "Save to collection").Height(uikit.HeightLarge).
				Tooltip("Save this request to a collection · Ctrl/Cmd+S")
		}
		if save.Clicked() {
			// A capture has no name or collection yet: ask for both.
			d.composeName = cmp.Or(d.composeName, d.composeMethod+" "+f.path)
			a.renameOpen, a.saveAfterRename = true, true
		}
	})
	if d.comparing {
		// A comparison takes the whole document, as in a request tab.
		ui.Column(c).Grow(1).MinHeight(0).Children(func() { a.requestResponse(c, t, d) })
		return
	}
	if a.inspect.percent == 0 {
		a.inspect.percent = 50
	}
	request := func() {
		a.requestEditor(c, t, d, func() { a.requestPane(c, f) })
	}
	response := func() {
		// Sent, the document's response is what it got back; until then it
		// is what the capture got.
		if d.sentSet || d.composeWaiting || d.response != nil {
			a.requestResponse(c, t, d)
			return
		}
		a.responsePane(c, f)
	}
	if a.sidesStacked() {
		ui.SplitVerticalPercent(c.Key("inspector-sides"), &a.inspect.percent, request, response).Grow(1)
		return
	}
	ui.SplitPercent(c.Key("inspector-sides"), &a.inspect.percent, request, response).Grow(1)
}

// capturedReadOnly says why a captured HTTP request shows only its readers,
// with the way to edit it when there is one.
func (a *app) capturedReadOnly(c *ui.Context, t *ui.Theme, f flow, problem string) {
	if problem == "" {
		return
	}
	ui.Row(c).Height(uikit.HeightLarge).Padding(0, t.Space(3)).Gap(t.Space(2)).AlignItems(ui.Center).Children(func() {
		if problem == "spilled" {
			uikit.Hint(c, "Large request body kept on disk").Grow(1)
			if uikit.SecondaryButton(c, "Edit request").Tooltip("Read the body from disk and edit this request").Clicked() {
				a.editCapture(f)
			}
			return
		}
		uikit.Hint(c, problem).SingleLine().Grow(1).Tooltip(problem)
	})
	ui.Divider(c)
}
