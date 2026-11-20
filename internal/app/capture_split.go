package app

// Two capture views can share the window the way VS Code shares an editor
// group: each group keeps its own conditions, rows, selection, scroll and
// inspector, while both read the same flow store. Views are the tabs the
// groups show; a group is a window slot holding one of them.

import (
	"slices"
	"strconv"

	"github.com/baba983/vscroxy/internal/uikit"
	"github.com/baba983/vscroxy/locales"
	"github.com/egoist/mygo/ui"
	"github.com/egoist/mygo/ui/editor"
	"github.com/egoist/mygo/ui/richtext"
)

// captureInspectorState is the reader's state that belongs to one capture
// group. The detail panes build it from the group's selected flow; keeping a
// copy per group lets both inspectors stay where their reader left them.
type captureInspectorState struct {
	inspect          inspectorState
	detailFor        int
	body, reqBody    string
	bodyNotice       [2]string
	diff             *editor.DiffState
	diffBaseline     bool
	tab              int
	connOpen         bool
	bodyMode         [2]int
	bodyQuery        [2]string
	headerScroll     [2]ui.ScrollState
	bodyText         [2]*editor.State
	bodyDisplayCache [2]bodyDisplayCache
	bodyPrettyFor    [2]string
	bodyReader       [2]ui.Handle
	bodyFind         [2]bodyFindState
	jsonCrumbs       [2]jsonBreadcrumbs
	activeJSONPath   activeJSONPath
	responseCrumbs   jsonBreadcrumbs
	outline          outlineUI
	jsonScroll       [2]ui.ListState
	hexScroll        [2]ui.ListState
	rawText          [2]string
	rawEditor        [2]*editor.State
	jsonTrees        [2]jsonTreeResult
	jsonExpanded     [2]map[nodeID]bool
	jsonReader       [2]jsonReader
	protoFor         [2]string
	protoOK          [2]bool
	protoOwner       int
	protobuf         protoInspector
	structured       [2]structuredInspector
	htmlDoc          [2]*richtext.Document
	htmlFor          [2]string
	imageBmp         [2]*ui.Bitmap
	imageFor         [2]string
	imageErr         [2]string
	formattedCode    [2]*editor.State
	codeFor          [2]string
	markdownDoc      [2]*richtext.Document
	markdownFor      [2]string
	mergedText       [2]*editor.State
	mergedFor        [2]string
	live             *requestDocument
	liveFor          int
	liveProblem      string
	liveLoad         bool
	autoSelect       bool
	autoSelecting    bool
	lastPicked       int
}

func (s *captureInspectorState) take(a *app) {
	*s = captureInspectorState{
		inspect: a.inspect, detailFor: a.detailFor,
		body: a.body, reqBody: a.reqBody, bodyNotice: a.bodyNotice,
		diff: a.diff, diffBaseline: a.diffBaseline, tab: a.tab,
		connOpen: a.connOpen, bodyMode: a.bodyMode, bodyQuery: a.bodyQuery,
		headerScroll: a.headerScroll, bodyText: a.bodyText,
		bodyDisplayCache: a.bodyDisplayCache, bodyPrettyFor: a.bodyPrettyFor,
		bodyReader: a.bodyReader, bodyFind: a.bodyFind,
		jsonCrumbs: a.jsonCrumbs, activeJSONPath: a.activeJSONPath,
		responseCrumbs: a.responseCrumbs, outline: a.outline,
		jsonScroll: a.jsonScroll, hexScroll: a.hexScroll,
		rawText: a.rawText, rawEditor: a.rawEditor,
		jsonTrees: a.jsonTrees, jsonExpanded: a.jsonExpanded,
		jsonReader: a.jsonReader, protoFor: a.protoFor, protoOK: a.protoOK,
		protoOwner: a.protoOwner, protobuf: a.protobuf, structured: a.structured,
		htmlDoc: a.htmlDoc, htmlFor: a.htmlFor,
		imageBmp: a.imageBmp, imageFor: a.imageFor, imageErr: a.imageErr,
		formattedCode: a.formattedCode, codeFor: a.codeFor,
		markdownDoc: a.markdownDoc, markdownFor: a.markdownFor,
		mergedText: a.mergedText, mergedFor: a.mergedFor,
		live: a.live, liveFor: a.liveFor, liveProblem: a.liveProblem, liveLoad: a.liveLoad,
		autoSelect:    a.autoSelect,
		autoSelecting: a.autoSelecting, lastPicked: a.lastPicked,
	}
}

func (s captureInspectorState) put(a *app) {
	a.inspect, a.detailFor = s.inspect, s.detailFor
	a.body, a.reqBody, a.bodyNotice = s.body, s.reqBody, s.bodyNotice
	a.diff, a.diffBaseline, a.tab = s.diff, s.diffBaseline, s.tab
	a.connOpen, a.bodyMode, a.bodyQuery = s.connOpen, s.bodyMode, s.bodyQuery
	a.headerScroll, a.bodyText = s.headerScroll, s.bodyText
	a.bodyDisplayCache, a.bodyPrettyFor = s.bodyDisplayCache, s.bodyPrettyFor
	a.bodyReader, a.bodyFind = s.bodyReader, s.bodyFind
	a.jsonCrumbs, a.activeJSONPath = s.jsonCrumbs, s.activeJSONPath
	a.responseCrumbs, a.outline = s.responseCrumbs, s.outline
	a.jsonScroll, a.hexScroll = s.jsonScroll, s.hexScroll
	a.rawText, a.rawEditor = s.rawText, s.rawEditor
	a.jsonTrees, a.jsonExpanded, a.jsonReader = s.jsonTrees, s.jsonExpanded, s.jsonReader
	a.protoFor, a.protoOK, a.protoOwner = s.protoFor, s.protoOK, s.protoOwner
	a.protobuf, a.structured = s.protobuf, s.structured
	a.htmlDoc, a.htmlFor = s.htmlDoc, s.htmlFor
	a.imageBmp, a.imageFor, a.imageErr = s.imageBmp, s.imageFor, s.imageErr
	a.formattedCode, a.codeFor = s.formattedCode, s.codeFor
	a.markdownDoc, a.markdownFor = s.markdownDoc, s.markdownFor
	a.mergedText, a.mergedFor = s.mergedText, s.mergedFor
	a.live, a.liveFor = s.live, s.liveFor
	a.liveProblem, a.liveLoad = s.liveProblem, s.liveLoad
	a.autoSelect, a.autoSelecting, a.lastPicked = s.autoSelect, s.autoSelecting, s.lastPicked
}

// captureGroupFields is the rest of the app state each view owns: the rows
// the filters produced, the table's choice and the search field's draft. The
// app's own fields mirror one group at a time, so every existing table,
// toolbar and inspector widget can be reused unchanged.
func (v *captureViews) captureGroupFields(a *app, view *captureView) {
	if view == nil {
		return
	}
	capture := v.wrote
	picked, chosen, sort := view.picked, view.chosen, view.sort
	v.capture(a)
	if v.drawing {
		view.picked, view.chosen, view.sort = picked, chosen, sort
	}
	view.queryProblem, view.searchProblem = a.queryProblem, a.searchProblem
	view.queryCaret, view.queryOpen = a.queryCaret, a.queryOpen
	view.searchFocused = a.searchFocused
	view.search, view.tablePercent, view.protoPercent = a.search, a.tablePercent, a.protobufTablePercent
	view.sidePercent = a.captureSidePercent
	view.inspector, view.inspectorReq, view.inspectorResp = a.workbench.inspector, a.inspectorReqPane, a.inspectorRespPane
	view.editorReq, view.editorResp = a.editorReqPane, a.editorRespPane
	view.visible = a.visible
	view.chosen = cloneSelection(a.chosen)
	view.sortApplied = a.sortApplied
	view.detail.take(a)
	if view.captureConditions != capture {
		view.dirty = true
		v.persistWorkbench(a)
	}
}

// cloneSelection keeps one group's choice from changing another's when the
// table later edits the app's own selection set.
func cloneSelection(from ui.Selection[int]) ui.Selection[int] {
	var to ui.Selection[int]
	for id := range from.All() {
		to.Add(id)
	}
	return to
}

func (v *captureViews) bindGroupFields(a *app, view *captureView) {
	if view == nil {
		return
	}
	v.bind(a)
	a.queryProblem, a.searchProblem = view.queryProblem, view.searchProblem
	a.queryCaret, a.queryOpen = view.queryCaret, view.queryOpen
	a.searchFocused = view.searchFocused
	a.search, a.tablePercent, a.protobufTablePercent = view.search, view.tablePercent, view.protoPercent
	a.captureSidePercent = view.sidePercent
	a.workbench.inspector, a.inspectorReqPane, a.inspectorRespPane = view.inspector, view.inspectorReq, view.inspectorResp
	a.editorReqPane, a.editorRespPane = view.editorReq, view.editorResp
	a.visible, a.chosen = view.visible, cloneSelection(view.chosen)
	a.picked, a.pickedID = view.picked, view.pickedID
	a.sort, a.sortApplied = view.sort, view.sortApplied
	view.detail.put(a)
}

// groupViews returns the views currently on screen, in group order. Without a
// split both slots point at the focused view.
func (v *captureViews) groupViews() []*captureView {
	if !v.split {
		view := v.activeView()
		if view == nil {
			return nil
		}
		return []*captureView{view}
	}
	first, second := v.viewByID(v.groups[0]), v.viewByID(v.groups[1])
	if first == nil {
		first, second = second, nil
	}
	if second == nil {
		return []*captureView{first}
	}
	return []*captureView{first, second}
}

// viewByID finds a saved view by its stable id: closing another view or
// renaming it never changes which one a group holds.
func (v *captureViews) viewByID(id int) *captureView {
	if id == 0 {
		return nil
	}
	for _, view := range v.views {
		if view.id == id {
			return view
		}
	}
	return nil
}

// focusedGroup returns 0 or 1, the slot whose fields the app mirrors.
func (v *captureViews) focusedGroup() int {
	if !v.split {
		return 0
	}
	return v.focus
}

// splitRight opens the second group with a copy of the focused view's
// conditions. The copy starts with its own rows, choice and inspector.
func (v *captureViews) splitRight(a *app) {
	if v.split {
		return
	}
	current := v.activeView()
	if current == nil {
		return
	}
	v.captureGroupFields(a, current)
	saved := current.savedFilter()
	view := v.addView(a, saved, current.body)
	view.name = current.name
	v.groups[0], v.groups[1] = current.id, view.id
	v.split, v.focus = true, 1
	v.active = len(v.views) - 1
	v.bindGroupFields(a, view)
	v.applyFilter(a)
	v.persistWorkbench(a)
}

// closeGroup removes the second group, keeping its view in the saved-view
// list. Closing a group is a layout command, not a delete of the view.
func (v *captureViews) closeGroup(a *app) {
	if !v.split {
		return
	}
	current := v.activeView()
	if current != nil {
		v.captureGroupFields(a, current)
	}
	// Closing the group leaves the first one, whichever of the two had the
	// keyboard: closing a group is not a request to switch to its neighbour.
	remaining := v.viewByID(v.groups[0])
	v.groups = [2]int{}
	v.split, v.focus = false, 0
	if remaining == nil {
		remaining = current
	}
	if remaining == nil && len(v.views) > 0 {
		remaining = v.views[0]
	}
	v.active = v.index(remaining)
	v.bindGroupFields(a, remaining)
	if remaining != nil && remaining.dirty {
		v.applyFilter(a)
	}
	v.persistWorkbench(a)
}

// focusCaptureGroup moves the app's mirrored state to another group. No rows
// are recomputed here: both groups keep their own result from the last filter
// pass, so switching focus is a bind.
func (v *captureViews) focusCaptureGroup(a *app, group int) {
	if group < 0 || group > 1 || !v.split || group == v.focus {
		return
	}
	current := v.activeView()
	if current != nil {
		v.captureGroupFields(a, current)
	}
	view := v.viewByID(v.groups[group])
	if view == nil {
		return
	}
	v.focus = group
	v.active = v.index(view)
	v.bindGroupFields(a, view)
	if view.dirty {
		v.applyFilter(a)
	}
	v.persistWorkbench(a)
}

func (v *captureViews) index(view *captureView) int {
	for i, candidate := range v.views {
		if candidate == view {
			return i
		}
	}
	return 0
}

// groupHeader draws the group's name, its focused state and its close action.
// Buttons name the group they act on, never a localized label.
func (v *captureViews) groupHeader(c *ui.Context, t *ui.Theme, a *app, group int, view *captureView) {
	name := v.label(v.index(view))
	focused := group == v.focusedGroup()
	row := ui.Row(c).Height(uikit.HeightControl).Gap(t.Space(uikit.SpaceS)).Padding(0, t.Space(uikit.SpaceM)).
		AlignItems(ui.Center).Border(0, t.Border).BorderWidth(0, 0, 1, 0)
	if focused {
		row.Background(t.SurfaceHover)
	}
	row.Children(func() {
		uikit.Caption(c, locales.CaptureGroup(group+1)).FontWeight(500)
		if name != "" {
			uikit.Meta(c, name).Grow(1).SingleLine()
		} else {
			ui.Box(c).Grow(1)
		}
		if !focused && uikit.LinkButton(c, locales.FocusCaptureGroup(group+1)).Clicked() {
			v.focusCaptureGroup(a, group)
		}
		if focused && v.split && uikit.RowIconButton(c, locales.CloseCaptureGroup(), locales.CloseSecondCaptureView(), closeIcon).Clicked() {
			v.closeGroup(a)
		}
	})
}

// restoreFocusedGroup puts the app's fields back on the focused group after a
// frame has built the other groups in turn.
func (v *captureViews) restoreFocusedGroup(a *app) {
	current := v.activeView()
	if current == nil {
		return
	}
	view := v.viewByID(v.groups[v.focusedGroup()])
	if view == nil {
		view = current
	}
	v.active = v.index(view)
	v.bindGroupFields(a, view)
}

func (v *captureViews) groupsInUse() []*captureView {
	seen := make(map[int]bool)
	var views []*captureView
	for _, view := range v.groupViews() {
		if view != nil && !seen[view.id] {
			seen[view.id] = true
			views = append(views, view)
		}
	}
	return views
}

// withView lends the app's mirrored fields to one group for the duration of
// run, then puts the focus group's fields back. This is how one table, toolbar
// and inspector implementation serves both groups.
func (v *captureViews) withView(a *app, view *captureView, run func()) {
	if view == nil {
		run()
		return
	}
	current := v.activeView()
	if current == view {
		run()
		return
	}
	v.captureGroupFields(a, current)
	v.active = v.index(view)
	v.bindGroupFields(a, view)
	run()
	v.captureGroupFields(a, view)
	v.active = v.index(current)
	v.bindGroupFields(a, current)
}

// rebuildOne refreshes one group's rows using its own conditions. It Borrows
// the app fields only for that pass and does not ask every group to refresh
// again, so a body-search answer cannot loop back into another search.
func (v *captureViews) rebuildOne(a *app, view *captureView) {
	if view == nil {
		return
	}
	v.refreshing = true
	defer func() { v.refreshing = false }()
	v.withView(a, view, func() { a.applyFilterRows(); v.narrowBorrowed(a) })
	view.dirty = false
}

// narrowBorrowed applies a body condition when the views being exercised are
// not the app's own (the isolated capture-views tests wire them that way).
// In the app, applyFilterRows already narrows the borrowed view.
func (v *captureViews) narrowBorrowed(a *app) {
	if v != &a.views {
		v.narrow(a)
		a.restoreSelection()
	}
}

// refreshGroups rebuilds the groups that are not in the app's fields. A
// filter pass, new traffic or a body answer changes the shared stores all
// groups read, so each group's own rows are recomputed once, not per frame.
func (v *captureViews) refreshGroups(a *app) {
	if v.refreshing || len(v.views) == 0 {
		return
	}
	v.refreshing = true
	defer func() { v.refreshing = false }()
	focused := v.activeView()
	for _, view := range v.groupsInUse() {
		if view == focused {
			continue
		}
		v.withView(a, view, func() { a.applyFilterRows(); v.narrowBorrowed(a) })
		view.dirty = false
	}
}

// mergeArrivals offers new rows to every open group. A group keeps the rows
// its own filters already show and appends only the new ones that match.
func (v *captureViews) mergeArrivals(a *app, rows []int) {
	if v.refreshing || len(v.views) == 0 {
		return
	}
	v.refreshing = true
	defer func() { v.refreshing = false }()
	focused := v.activeView()
	for _, view := range v.groupsInUse() {
		if view == focused {
			continue
		}
		v.withView(a, view, func() { a.showArrivalRows(slices.Clone(rows)) })
	}
}

// workbenchGroups is the saved form of the open groups, in group order.
func (v *captureViews) workbenchGroups() []savedCaptureFilter {
	views := v.groupViews()
	list := make([]savedCaptureFilter, 0, len(views))
	for _, view := range views {
		if view != nil {
			list = append(list, view.savedFilter())
		}
	}
	return list
}

// persistWorkbench copies the open groups into the workbench state and saves
// it. persistSettings collapses repeated calls while a filter is being typed,
// so this is safe to call whenever a group's conditions change.
func (v *captureViews) persistWorkbench(a *app) {
	w := &a.workbench
	groups := v.workbenchGroups()
	if w.captureSplit == v.split && w.captureFocus == v.focus && slices.Equal(w.captureGroups, groups) {
		return
	}
	w.captureSplit, w.captureFocus, w.captureGroups = v.split, v.focus, groups
	a.persistSettings()
}

// restoreWorkbench rebuilds the saved groups on launch. Each saved filter is
// the conditions of one view, in group order; the second view is new to this
// session and holds the conditions the second group had.
func (v *captureViews) restoreWorkbench(a *app, state []savedCaptureFilter, focus int) {
	if len(state) == 0 {
		return
	}
	first := v.activeView()
	if first == nil {
		return
	}
	first.captureConditions = conditionsFromFilter(state[0], first.captureConditions.sort)
	first.body = state[0].Body
	v.groups[0] = first.id
	if len(state) > 1 {
		second := v.addView(a, state[1], state[1].Body)
		second.name = first.name
		v.groups[1] = second.id
		v.split = true
		v.focus = min(max(focus, 0), 1)
		v.active = v.index(second)
	}
	var chosen *captureView
	if v.split {
		chosen = v.viewByID(v.groups[v.focus])
	} else {
		chosen = first
	}
	if chosen == nil {
		chosen = first
	}
	v.active = v.index(chosen)
	v.bindGroupFields(a, chosen)
	v.applyFilter(a)
}

// conditionsFromFilter turns the workbench's saved conditions into a view's.
func conditionsFromFilter(f savedCaptureFilter, sort ui.SortOrder) captureConditions {
	return captureConditions{
		query: f.Query, filter: f.Filter, chip: f.Chip,
		status: f.Status, header: f.Header, triage: f.Triage,
		any: f.Any, sort: sort,
	}
}

// captureTableRegion is the same bulk-action bar, activity strip and table
// the single view draws. It reads the app's mirrored fields, which belong to
// the group being built at that moment.
func (a *app) captureTableRegion(c *ui.Context) {
	ui.Column(c).Fill().Children(func() {
		if a.hadFlows {
			a.activityStrip(c)
		}
		if a.chosen.Len() > 1 {
			ui.Row(c).Height(uikit.HeightControl).Padding(0, c.Theme().Space(uikit.SpaceM)).Gap(c.Theme().Space(uikit.SpaceM)).AlignItems(ui.Center).Children(func() {
				uikit.Caption(c, locales.SelectedCount(a.chosen.Len())).FontWeight(500).Grow(1)
				if a.chosen.Len() == 2 {
					compare := pick(a.actions(c), "compare.two")
					if uikit.SecondaryButton(c, locales.Compare()).Disabled(!compare.enabled).Clicked() {
						compare.run()
					}
				}
				replay := pick(a.actions(c), "flow.replay")
				if uikit.PrimaryButton(c, locales.ReplaySelected()).Disabled(!replay.enabled).Clicked() {
					replay.run()
				}
				uikit.MoreMenu(c, locales.Export(), func(m *ui.Menu) {
					actionMenu(m, []action{
						pick(a.actions(c), "capture.export-chosen"),
						pick(a.actions(c), "capture.export-chosen-csv"),
					})
				})
				if uikit.LinkButton(c, locales.ClearSelection()).Clicked() {
					a.chosen.Clear()
				}
			})
		}
		a.table(c)
	})
}

// captureSplitView builds both groups in one pass. One group at a time owns
// the app's mirrored fields, so every toolbar, table and inspector widget is
// the same one the single view uses. Their widget ids carry the view id.
func (a *app) captureSplitView(c *ui.Context, available float32) {
	v := &a.views
	views := v.groupViews()
	if len(views) < 2 {
		v.restoreFocusedGroup(a)
		return
	}
	if a.workbench.captureGroupPercent == 0 {
		a.workbench.captureGroupPercent = 50
	}
	wide := splitFits(available)
	draw := func(group int, view *captureView) {
		v.useView(a, view)
		v.drawing = true
		defer func() {
			v.drawing = false
			v.releaseView(a, view)
		}()
		key := "-" + strconv.Itoa(view.id)
		a.captureSearchKey, a.detailKeySuffix = key, key
		defer func() { a.captureSearchKey, a.detailKeySuffix = "", "" }()
		ui.Column(c).Grow(1).MinWidth(0).MinHeight(0).Children(func() {
			v.groupHeader(c, c.Theme(), a, group, view)
			a.captureToolbar(c, c.Theme())
			a.certificateBanner(c, c.Theme())
			table := func() { a.captureTableRegion(c) }
			switch {
			case len(a.visible) == 0 && !a.activity.active():
				a.syncDetail()
				table()
			case a.inspectorDetached:
				table()
			case !wide:
				// In a narrow window the group keeps the table readable and
				// its inspector collapses to the exchange it holds.
				ui.Column(c).Fill().MinHeight(0).Children(func() {
					table()
					a.compactCaptureDetail(c)
				})
			case available/2 >= 800:
				percent := a.captureSidePercent
				if percent == 0 {
					percent = 58
				}
				ui.SplitPercent(c.Key("capture-group-side"+key), &percent, table, func() { a.detail(c) }).
					SplitReset(58).Grow(1).FillWidth().MinHeight(0)
			default:
				percent, reset := &a.tablePercent, float32(44)
				if a.bodyMode[0] == 5 {
					if a.protobufTablePercent == 0 {
						a.protobufTablePercent = 34
					}
					percent, reset = &a.protobufTablePercent, 34
				}
				ui.SplitVerticalPercent(c.Key("capture-group-stack"+key), percent, table, func() { a.detail(c) }).
					SplitReset(reset).Grow(1).FillWidth().MinHeight(0)
			}
		})
	}
	ui.SplitPercent(c.Key("capture-groups"), &a.workbench.captureGroupPercent,
		func() { draw(0, views[0]) }, func() { draw(1, views[1]) }).
		SplitReset(50).Grow(1).FillWidth().MinHeight(0)
	v.restoreFocusedGroup(a)
}

// compactCaptureDetail names the flow a narrow group inspects. It is a status
// line, not a second inspector: the full panes come back with the room.
func (a *app) compactCaptureDetail(c *ui.Context) {
	ui.Row(c).Height(uikit.HeightControl).Padding(0, c.Theme().Space(uikit.SpaceM)).AlignItems(ui.Center).Children(func() {
		if f, ok := a.current(); ok {
			uikit.Meta(c, locales.ReadExchange(f.id)+" · "+f.method+" "+f.host+f.path).SingleLine()
		} else {
			uikit.Meta(c, locales.NoRequestChosen()).SingleLine()
		}
	})
}

// useView and releaseView bracket the drawing of one group. They are the
// drawing counterpart of withView, which lends the fields for a row pass.
func (v *captureViews) useView(a *app, view *captureView) {
	current := v.activeView()
	if current == view {
		return
	}
	v.captureGroupFields(a, current)
	v.active = v.index(view)
	v.bindGroupFields(a, view)
}

func (v *captureViews) releaseView(a *app, view *captureView) {
	v.captureGroupFields(a, view)
	v.persistWorkbench(a)
}

// narrowWindow reports whether two columns would leave each group too little
// room for a list and its fields. The groups stack with a horizontal divider
// until there is room again, keeping both views on screen.
const splitMinWidth = 1080

func splitFits(width float32) bool { return width >= splitMinWidth }
