package app

// The last session is a VS Code-style restart: the live capture is saved in
// the sessions folder with the ordinary session format, and settings keep
// only the small state that says which session to open and where the person
// left the workbench. The capture file is read on a worker after startup, so
// a long capture does not hold the first frame while its bodies are decoded.

import (
	"encoding/json/v2"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"time"

	"github.com/baba983/vscroxy/internal/store"
	"github.com/baba983/vscroxy/locales"
	"github.com/egoist/mygo/ui"
)

const (
	lastSessionFile = "last-session.json"
	lastPageLive    = "live"
	lastPageSession = "session"
)

// lastSessionState is the UI state around the capture snapshot. Filters and
// the split's layout also live in the regular settings fields; these are the
// parts that follow a particular session: its rows' identity, its open
// session tabs and the inspector's window.
type lastSessionState struct {
	Capture           string            `json:"capture,omitempty"`
	Captures          []string          `json:"captures,omitempty"`
	Active            string            `json:"active,omitempty"`
	Page              string            `json:"page,omitempty"`
	Views             []lastSessionView `json:"views,omitempty"`
	Focus             int               `json:"focus,omitzero"`
	InspectorDetached bool              `json:"inspectorDetached,omitzero"`
	InspectorFlowID   int               `json:"inspectorFlowID,omitzero"`
}

type lastSessionView struct {
	ID         int                `json:"id,omitzero"`
	Name       string             `json:"name,omitempty"`
	Preset     string             `json:"preset,omitempty"`
	Conditions savedCaptureFilter `json:"conditions"`
	PickedID   int                `json:"pickedID,omitzero"`
	Chosen     []int              `json:"chosen,omitempty"`
}

// lastSessionReader is the worker's read. Tests replace it to hold the read
// open and show that the table starts before the capture bodies are parsed.
var lastSessionReader = readSessionFile

// lastSessionRestore is one pending read. The worker writes file and err,
// then closes done; checkpoint runs on the UI thread and applies them once.
type lastSessionRestore struct {
	state   lastSessionState
	done    chan struct{}
	file    sessionFile
	err     error
	applied bool
}

func sameRestoreChoice(x, y *bool) bool {
	if x == nil || y == nil {
		return x == nil && y == nil
	}
	return *x == *y
}

func sameLastSession(x, y lastSessionState) bool {
	if x.Capture != y.Capture || x.Active != y.Active || x.Page != y.Page ||
		x.Focus != y.Focus || x.InspectorDetached != y.InspectorDetached ||
		x.InspectorFlowID != y.InspectorFlowID || !slices.Equal(x.Captures, y.Captures) ||
		len(x.Views) != len(y.Views) {
		return false
	}
	for i := range x.Views {
		v, w := x.Views[i], y.Views[i]
		if v.ID != w.ID || v.Name != w.Name || v.Preset != w.Preset ||
			v.PickedID != w.PickedID || v.Conditions != w.Conditions || !slices.Equal(v.Chosen, w.Chosen) {
			return false
		}
	}
	return true
}

func (a *app) lastSessionPath() string {
	if a.sessions.dir == "" {
		return ""
	}
	return filepath.Join(a.sessions.dir, lastSessionFile)
}

// lastSessionState reads the small part of the state from the app itself.
// currentSettings calls it on every layout write, so the snapshot's name
// stays the one the last save chose rather than changing underneath it.
func (a *app) lastSessionState() lastSessionState {
	state := lastSessionState{
		Capture:           a.saved.LastSession.Capture,
		Active:            a.sessions.active,
		Focus:             a.views.focus,
		InspectorDetached: a.inspectorDetached,
		Page:              lastPageLive,
	}
	if a.page == pageSession {
		state.Page = lastPageSession
	}
	for _, item := range a.sessions.items {
		if item.open {
			state.Captures = append(state.Captures, item.key)
		}
	}
	for _, view := range a.views.groupViews() {
		if view == nil {
			continue
		}
		conditions := view.savedFilter()
		var chosen []int
		for id := range view.chosen.All() {
			chosen = append(chosen, id)
		}
		state.Views = append(state.Views, lastSessionView{
			ID: view.id, Name: view.name, Preset: view.preset,
			Conditions: conditions, PickedID: view.pickedID, Chosen: chosen,
		})
	}
	if f, ok := a.current(); ok {
		state.InspectorFlowID = f.id
	}
	return state
}

// setRestoreLastSession is the one place the switch changes the setting.
// Opting out discards the snapshot at once, so the captured traffic does not
// stay on disk until the next launch.
func (a *app) setRestoreLastSession(on bool) {
	a.restoreLastSession = on
	if !on {
		a.clearLastSession()
	}
}

// clearLastSession removes the snapshot after an opt-out and clears the small
// state that pointed at it. A file this version refuses to read is left for
// its owner: neither corruption nor a newer version makes it disposable.
func (a *app) clearLastSession() {
	path := a.lastSessionPath()
	a.saved.LastSession = lastSessionState{}
	if path == "" {
		return
	}
	if _, err := store.ReadFile(path, store.Session); err != nil {
		return
	}
	if err := os.Remove(path); err != nil && !errors.Is(err, fs.ErrNotExist) {
		a.fail(locales.LastSessionNotSaved(), err)
	}
}

// saveLastSession writes the live capture through sessionFrom, the same file
// format and folder as an explicitly saved session. A file that could not be
// read as this version is never replaced: store refuses to write it, and the
// settings stop pointing at it so a broken file cannot become current. The
// snapshot is regenerated on every quit, so keeping backups would keep up to
// ten copies of bodies, headers and cookies on disk: pass no backup dir.
func (a *app) saveLastSession() {
	if !a.restoreLastSession {
		a.clearLastSession()
		return
	}
	if a.lastSessionPath() == "" {
		return
	}
	state := a.lastSessionState()
	flows, frames := a.snapshot()
	file := sessionFrom(flows, frames)
	file.Title = locales.RestoreLastSession()
	file.Created = time.Now()
	data, err := json.Marshal(file)
	if err == nil {
		path := a.lastSessionPath()
		err = store.WriteFileVersioned(path, store.Session, append(data, '\n'), "")
	}
	if err != nil {
		a.fail(locales.LastSessionNotSaved(), err)
		state.Capture = ""
		a.saved.LastSession = state
		return
	}
	state.Capture = lastSessionFile
	a.saved.LastSession = state
}

// prepareLastSessionRestore starts the capture read after the session folder
// has been indexed. Nothing here touches the table: the worker keeps the
// decoded file until the UI applies it.
func (a *app) prepareLastSessionRestore() {
	state := a.saved.LastSession
	if !a.restoreLastSession || state.Capture == "" {
		return
	}
	r := &lastSessionRestore{state: state, done: make(chan struct{})}
	a.lastSessionRestore = r
	if state.Capture != lastSessionFile {
		r.err = fmt.Errorf("unknown last session file %q", state.Capture)
		close(r.done)
		return
	}
	go guarded(func() {
		r.file, r.err = lastSessionReader(a.lastSessionPath())
		close(r.done)
		if a.win != nil {
			a.win.Update(func() { a.checkpointLastSession() })
		} else {
			a.invalidate()
		}
	})
}

// checkpointLastSession applies a finished read on the UI thread, once. A
// refused file is reported and leaves the app empty; no write repairs it.
func (a *app) checkpointLastSession() {
	r := a.lastSessionRestore
	if r == nil || r.applied {
		return
	}
	select {
	case <-r.done:
	default:
		return
	}
	r.applied = true
	if r.err != nil {
		a.fail(locales.LastSessionNotRestored(), r.err)
		return
	}
	a.applyLastSession(r.state, r.file)
}

func (a *app) applyLastSession(state lastSessionState, file sessionFile) {
	flows, frames := file.tables()
	a.replaceSession(flows, frames)
	a.restoreSessionTabs(state)
	a.restoreLastSessionViews(state)
	// Reopening a workspace replaces the empty first-run guide with the
	// table, even when the saved capture had no rows.
	a.guide = false
	if state.Page == lastPageSession && a.sessions.active != "" && a.sessions.current() != nil {
		a.openPageTab(pageSession, tabOpen{})
		*a.regionOpen("sessions") = true
	} else if k := a.tabsGroup().active().kind; k != pageRequest && k != pageAPI {
		// A request or contract the open tabs brought back stays in front;
		// the capture loads behind it.
		a.openPageTab(pageLive, tabOpen{})
	}
	a.restoreInspector(state)
	a.invalidate()
}

// restoreSessionTabs reopens the saved captures that were tabs at quit. The
// files keep loading through the normal session worker, so the live table
// never waits for another session's payload.
func (a *app) restoreSessionTabs(state lastSessionState) {
	s := &a.sessions
	for _, key := range state.Captures {
		i := sliceIndexSession(s.items, key)
		if i < 0 {
			continue
		}
		item := s.items[i]
		item.open = true
		a.loadSessionTab(item)
	}
	if i := sliceIndexSession(s.items, state.Active); i >= 0 && state.Active != "" {
		item := s.items[i]
		item.open, s.active = true, state.Active
		a.loadSessionTab(item)
	}
}

func sliceIndexSession(items []*savedSession, key string) int {
	for i, item := range items {
		if item.key == key {
			return i
		}
	}
	return -1
}

// restoreLastSessionViews applies the conditions, rows and picked exchange
// saved for each open group. A saved group's id may differ from the id the new
// session generated, so state.Views is matched by group order, not by label.
func (a *app) restoreLastSessionViews(state lastSessionState) {
	if len(state.Views) == 0 {
		return
	}
	filters := make([]savedCaptureFilter, 0, len(state.Views))
	for _, saved := range state.Views {
		filter := saved.Conditions
		filter.Name = fmt.Sprint(saved.ID)
		filters = append(filters, filter)
	}
	// Settings already restored the same split on a normal launch. Reuse
	// those groups instead of making another view for the second slot.
	if len(a.views.groupViews()) != len(filters) {
		a.views.restoreWorkbench(a, filters, state.Focus)
	} else {
		for i, view := range a.views.groupViews() {
			view.captureConditions = conditionsFromFilter(filters[i], view.captureConditions.sort)
			view.body, view.dirty = filters[i].Body, true
		}
		focus := min(max(state.Focus, 0), len(filters)-1)
		a.views.focus = focus
		a.views.active = a.views.index(a.views.viewByID(a.views.groups[focus]))
		a.views.bindGroupFields(a, a.views.activeView())
	}
	views := a.views.groupViews()
	for i, saved := range state.Views {
		if i >= len(views) || views[i] == nil {
			continue
		}
		view := views[i]
		// Rebuild this group's rows from its own conditions before mapping
		// the saved id to a row: the app mirrors one group at a time. Give
		// each group its own array so a rebuild cannot write through a
		// slice the other group still holds.
		view.visible = slices.Clone(view.visible)
		a.views.rebuildOne(a, view)
		if a.views.activeView() == view {
			// The focused group owns the app's mirrored fields after the
			// rebuild; write them back to the group before saving its pick.
			a.views.captureGroupFields(a, view)
		}
		view.picked, view.pickedID = -1, saved.PickedID
		for row, at := range view.visible {
			if a.flows[at].id == saved.PickedID {
				view.picked = row
				break
			}
		}
		if view.picked < 0 {
			view.pickedID = 0
		}
		var chosen ui.Selection[int]
		for _, id := range saved.Chosen {
			chosen.Add(id)
		}
		if view.pickedID != 0 && chosen.Len() == 0 {
			chosen.Add(view.pickedID)
		}
		view.chosen = chosen
	}
	focus := min(max(state.Focus, 0), len(a.views.groups)-1)
	if view := a.views.viewByID(a.views.groups[focus]); view != nil {
		a.views.bindGroupFields(a, view)
		a.visible, a.chosen = slices.Clone(view.visible), cloneSelection(view.chosen)
		a.picked, a.pickedID = view.picked, view.pickedID
	}
}

// restoreInspector opens a detached inspector only when the exchange it was
// showing is still here. Saved captures can still be loading, so that part
// is decided once they have landed (see checkpointInspectorRestore).
func (a *app) restoreInspector(state lastSessionState) {
	if !state.InspectorDetached {
		return
	}
	if a.exchangeRestored(state.InspectorFlowID) {
		a.openInspectorWindow()
		return
	}
	if a.savedSessionLoading() {
		a.pendingInspector = &state
		return
	}
	a.warn(locales.InspectorNotRestored())
}

func (a *app) exchangeRestored(id int) bool {
	if id == 0 {
		return false
	}
	if slices.ContainsFunc(a.flows, func(f flow) bool { return f.id == id }) {
		return true
	}
	for _, item := range a.sessions.items {
		if !item.open || !item.loaded {
			continue
		}
		if slices.ContainsFunc(item.data.Flows, func(f sessionFlow) bool { return f.ID == id }) {
			return true
		}
	}
	return false
}

func (a *app) savedSessionLoading() bool {
	for _, item := range a.sessions.items {
		if item.open && item.path != "" && !item.loaded && item.saveErr == "" {
			return true
		}
	}
	return false
}

// checkpointInspectorRestore finishes the part of the restore that depends
// on background session loads. It runs after checkpointSessions in each
// frame, when loaded flags are current.
func (a *app) checkpointInspectorRestore() {
	state := a.pendingInspector
	if state == nil || a.savedSessionLoading() {
		return
	}
	a.pendingInspector = nil
	if a.exchangeRestored(state.InspectorFlowID) {
		a.openInspectorWindow()
		return
	}
	a.warn(locales.InspectorNotRestored())
}
