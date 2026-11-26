package app

import (
	"cmp"
	"slices"

	"github.com/baba983/vscroxy/internal/uikit"
	"github.com/baba983/vscroxy/locales"
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
		a.liveProblem = locales.NotEditableWith(err.Error())
		return nil, a.liveProblem
	}
	d := a.newDocument(r, "")
	d.setDebugSource(newDebugSource(f))
	d.baseline = d.value()
	// An unchanged capture is the document: its tabs open on the part a
	// request of this shape is usually edited in, and the capture itself is
	// only somewhere else to look once the draft differs from it.
	d.composeTab = composeTabFor(r)
	a.live = d
	return d, ""
}

// keepLiveDraft makes the unchanged capture's document its draft once it is
// edited or sent: copy on write. The draft also gets a tab, in the background,
// so the strip is the one place that says what is open.
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
	if !slices.Contains(a.documents, d) {
		a.documents = append(a.documents, d)
	}
	g := a.tabsGroup()
	id := requestTabID(d)
	a.tabDocuments[id] = d
	if g.indexOf(id) < 0 {
		g.add(requestTabDescriptor(d))
	}
}

// edited reports whether the capture f has a draft that differs from it or
// was sent.
func (a *app) edited(id int) bool {
	d := a.flowDrafts[id]
	return d != nil && (d.dirty() || d.sentSet)
}

// syncDocument points the app's document at what the content area shows: the
// inspected capture's document in LIVE, the active request tab's document in
// a request tab, none elsewhere. The tab model already chose the tab; this
// only keeps the inspector's document and the flow-draft registry in step.
func (a *app) syncDocument() {
	a.keepLiveDraft()
	for id := range a.flowDrafts {
		if !slices.ContainsFunc(a.flows, func(f flow) bool { return f.id == id }) {
			delete(a.flowDrafts, id) // The capture is gone, and its draft with it.
		}
	}
	switch a.page {
	case pageLive:
		a.requestDocument = nil
		if f, ok := a.current(); ok && a.chosen.Len() <= 1 {
			a.requestDocument, _ = a.liveDocument(f)
		}
	case pageRequest:
		// syncTabs set this from the active tab. A page restored without one
		// falls back to the last open document, as it did before tabs.
		if a.requestDocument == nil && len(a.documents) > 0 {
			a.requestDocument = a.documents[len(a.documents)-1]
		}
	default:
		a.requestDocument = nil
	}
}

// editCapture gives the capture f a draft tab and shows it in the inspector,
// where editing already happens in place. The tab is the open document; LIVE
// stays the page, so the traffic list and the captured exchange stay beside
// the draft.
func (a *app) editCapture(f flow) *requestDocument {
	if !slices.ContainsFunc(a.flows, func(g flow) bool { return g.id == f.id }) {
		a.warn(locales.CaptureIsNoLongerIn())
		return nil
	}
	a.liveLoad = true
	if a.liveFor == f.id && a.live == nil {
		a.liveFor = 0 // Try again, now reading a spilled body.
	}
	d, problem := a.liveDocument(f)
	if d == nil {
		a.warn(cmp.Or(problem, locales.ThisCaptureIsReadOnly()))
		return nil
	}
	if a.flowDrafts == nil {
		a.flowDrafts = map[int]*requestDocument{}
	}
	a.flowDrafts[f.id] = d
	if a.live == d {
		a.live = nil
	}
	a.liveFor = f.id
	a.openRequestTab(d, tabOpen{})
	a.inspectCapture(f.id)
	a.workbench.focused = false
	a.requestDocument = d
	d.urlInput.Focus()
	return d
}

// capturedHeader names where the document came from and what the capture
// produced, with the actions that read or copy it.
func (a *app) capturedHeader(c *ui.Context, t *ui.Theme, f flow, d *requestDocument) {
	compact := a.inspectorWidth(c) < 600
	ui.Row(c).Height(uikit.HeightControl).Padding(0, t.Space(uikit.SpaceL)).Gap(t.Space(uikit.SpaceM)).AlignItems(ui.Center).Children(func() {
		source := locales.CapturedAt(f.at.Local().Format("15:04:05"))
		if f.at.IsZero() {
			source = locales.CapturedNumber(f.id)
		}
		if f.process != "" {
			source += " · " + f.process
		}
		// A saved request that matches is the header's title, and the capture
		// it came from is kept in that title's card: two runs in one row were
		// cut to ellipses before either said what it held.
		if !a.capturedRequestMatch(c, t, f, source) {
			// No saved request matches, so the capture's own line is the
			// title. Its card carries the whole exchange: the table's columns
			// cut a long URL, and this one does not.
			title := f.method + " " + cmp.Or(f.url, f.scheme+"://"+f.host+f.path)
			// The card repeats the source in full at the top of its facts.
			card := captureCard(f)
			if card.Meta != "" {
				card.Meta = source + " · " + card.Meta
			} else {
				card.Meta = source
			}
			uikit.CardTip(c, uikit.Meta(c, source).Label(title).FontFeatures("tnum").SingleLine().MinWidth(0), card)
		}
		if d == nil {
			// Streams and tunnels read as they were; the path names them.
			uikit.Tip(c, uikit.Mono(c, f.method+" "+cmp.Or(f.path, "/")).SingleLine().MinWidth(48), f.url)
		}
		if a.edited(f.id) {
			// The source names give way first; the mark and the status stay.
			uikit.Caption(c, locales.Edited()).TextColor(t.Accent).SingleLine().Shrink(0)
		}
		if f.dns != "" {
			// Which Host profile sent it elsewhere, as the Remote IP says.
			uikit.Tip(c, uikit.Caption(c, mappedToLabel(f)).TextColor(uikit.Colors(t).Primary).
				SingleLine().MinWidth(0), mappedBy(f))
		}
		ui.Spacer(c)
		// The original response names its result in the pane. Keep the
		// capture's summary when reading another view or a later send.
		if a.inspect.pane != inspectorSplit || d != nil && (d.sentSet || d.composeWaiting || d.response != nil) {
			a.exchangeResult(c, t, f, compact)
		}
		if f.conn.error != "" {
			if uikit.Tip(c, uikit.SecondaryButton(c, locales.Explain()), locales.DiagnoseThisConnection()).Clicked() {
				a.openDiagnostics(&f, diagnoseMac)
			}
		}
		var copy ui.Element
		uikit.MenuFace(c, func() {
			copy = uikit.ToolbarButton(c, locales.CopyAs(), locales.CopyTheOriginalCapturedRequest(), false).PopoverMenuButton(func(m *ui.Menu) {
				actionMenu(m, copyAsMenu(append(a.requestCopyActions(c, f, nil), a.captureCopyActions(c, []flow{f})...)).items)
			}, uikit.MenuButtonOptions(c))
		})
		copy.Children(func() {
			ui.Icon(c, copyIcon).Size(uikit.IconInline, uikit.IconInline)
			if !compact {
				uikit.Body(c, locales.CopyAs())
			}
		})
		copy.Shrink(0)
		info := uikit.IconButton(c, locales.ConnectionDetails(), locales.ConnectionAddressesDnsAndTls(), infoIcon)
		info.OnClick(func() { a.connOpen = !a.connOpen })
		ui.Popover(c, info, &a.connOpen, func() { a.connPanel(c, t, f) })
		if !a.inspectorDetached {
			if uikit.Tip(c, uikit.IconButton(c, locales.DetachInspector(), locales.DetachInspectorHint(), detachIcon), locales.DetachInspectorHint()).Clicked() {
				a.openInspectorWindow()
			}
		}
		a.inspectorMenu(c, t, f)
	})
}

// capturedDocument is a captured HTTP request as an editable document: the
// request bar, the request's parts with the capture's own reader beside
// them, and the captured response until the document is sent.
func (a *app) capturedDocument(c *ui.Context, t *ui.Theme, f flow, d *requestDocument) {
	a.protoOwner = a.protobufOwnerForDocument(f, d)
	a.requestBar(c, t, d, func() {
		// A narrow inspector keeps its width for the URL.
		var save ui.Element
		if a.inspectorWidth(c) < 600 {
			save = uikit.IconButton(c, locales.SaveToCollection(), locales.SaveThisRequestToA(), saveIcon)
		} else {
			save = uikit.SecondaryButton(c, locales.SaveToCollection())
			uikit.Tip(c, save, locales.SaveThisRequestToA())
		}
		save.Shrink(0)
		if save.Clicked() {
			// A capture has no name or collection yet: ask for both.
			d.composeName = cmp.Or(d.composeName, d.composeMethod+" "+f.path)
			a.renameOpen, a.saveAfterRename = true, true
		}
	}, false)
	if d.comparing {
		// A comparison takes the whole document, as in a request tab.
		ui.Column(c).Grow(1).MinHeight(0).Children(func() { a.requestResponse(c, t, d) })
		return
	}
	if a.inspect.percent == 0 {
		a.inspect.percent = splitShare
	}
	// The capture's panes keep their own tab: moving to another request and
	// back should land where the reader was.
	request := func() { a.requestEditor(c, t, d, f, &a.inspect.tab[0]) }
	sent := d.sentSet || d.composeWaiting || d.response != nil
	response := func() {
		// Sent, the document's response is what it got back; until then it
		// is what the capture got.
		if sent {
			a.requestResponse(c, t, d)
			return
		}
		a.responsePane(c, f)
	}
	percent, reset := &a.inspect.percent, float32(splitShare)
	if a.bodyMode[0] == 5 && a.inspect.tab[0] != composeTabBody {
		// The request's headers need one table, not half the inspector; the
		// decoded protobuf message needs the room.
		if a.inspect.protobufPercent == 0 {
			a.inspect.protobufPercent = 25
		}
		percent, reset = &a.inspect.protobufPercent, 25
	}
	if a.sidesStacked(c) {
		resp := &a.inspectorRespPane
		if sent {
			resp = &a.editorRespPane
		}
		a.stackedSplit(c, percent, reset, &a.editorReqPane, resp, request, response)
		return
	}
	uikit.SplitPercent(c.Key("inspector-sides"+a.detailKeySuffix), percent, request, response).SplitReset(reset).Grow(1).MinHeight(0)
}

// capturedReadOnly says why a captured HTTP request shows only its readers,
// with the way to edit it when there is one.
func (a *app) capturedReadOnly(c *ui.Context, t *ui.Theme, f flow, problem string) {
	if problem == "" {
		return
	}
	ui.Row(c).Height(uikit.HeightControl).Padding(0, t.Space(uikit.SpaceL)).Gap(t.Space(uikit.SpaceM)).AlignItems(ui.Center).Children(func() {
		if problem == "spilled" {
			uikit.Hint(c, locales.LargeRequestBodyKeptOn()).Grow(1)
			if uikit.Tip(c, uikit.SecondaryButton(c, locales.EditRequest()), locales.ReadTheBodyFromDisk()).Clicked() {
				a.editCapture(f)
			}
			return
		}
		uikit.Tip(c, uikit.Hint(c, problem).SingleLine().Grow(1), problem)
	})
}
