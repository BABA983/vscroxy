package app

import (
	"bytes"
	"encoding/json/v2"
	"errors"
	"fmt"
	"maps"
	"os"
	"path/filepath"
	"reflect"
	"slices"

	"github.com/baba983/vscroxy/internal/capture"
	"github.com/baba983/vscroxy/internal/hosts"
	"github.com/baba983/vscroxy/internal/request"
	"github.com/baba983/vscroxy/internal/store"
)

// undoState is the state an undo element restores. Each snapshot keeps the
// data the app needs to write through its ordinary paths and adopt only
// after the write succeeds. Everything here is in memory; a restart starts
// with empty stacks.
type undoState struct {
	data                 variableStateCopy
	folderOrder          map[string][]string
	library, vars, rules bool
	ruleList             []capture.Rule
	hosts                *hostsUndoState
	capture              *captureUndoState
	drafts               *draftUndoState
}

// hostsUndoState keeps the whole profile index and the bytes of each profile
// file, so undoing a rename or delete also restores the files themselves.
type hostsUndoState struct {
	set   hosts.Set
	files map[string][]byte
}

// captureUndoState keeps the captured rows and the selection with them. Rows
// are values the capture owns; like the old deletion stack, a snapshot holds
// their current shape, not copies of their bodies.
type captureUndoState struct {
	flows     []flow
	pickedID  int
	chosen    []int
	detailFor int
}

// draftUndoState keeps the open tabs of an unsaved duplicate.
type draftUndoState struct {
	documents []*requestDocument
	active    *requestDocument
}

func (a *app) snapshotUndoState() (undoState, error) {
	data, err := copyVariableState(a)
	if err != nil {
		return undoState{}, err
	}
	return undoState{data: data, folderOrder: cloneFolderOrder(a.library.folderOrder)}, nil
}

// cloneVariableState copies every map and library value the bulk edits may
// change, so the two sides of an undo element never alias each other.
func cloneVariableState(s variableStateCopy) (variableStateCopy, error) {
	requests, err := cloneLibraryValue(s.requests)
	if err != nil {
		return variableStateCopy{}, err
	}
	folders, err := cloneLibraryValue(s.folders)
	if err != nil {
		return variableStateCopy{}, err
	}
	out := variableStateCopy{
		requests: requests, folders: folders,
		envs: slices.Clone(s.envs), globals: maps.Clone(s.globals),
		globalsCurrent:    maps.Clone(s.globalsCurrent),
		folderCurrent:     map[string]map[string]string{},
		folderCredentials: map[string]map[string]bool{},
	}
	for i := range out.envs {
		out.envs[i].Vars = maps.Clone(out.envs[i].Vars)
		out.envs[i].Current = maps.Clone(out.envs[i].Current)
		out.envs[i].Credentials = maps.Clone(out.envs[i].Credentials)
	}
	for path, values := range s.folderCurrent {
		out.folderCurrent[path] = maps.Clone(values)
	}
	for path, marks := range s.folderCredentials {
		out.folderCredentials[path] = maps.Clone(marks)
	}
	return out, nil
}

// snapshotUndoLibrary is the before/after snapshot of a library operation.
// Folder-local variables belong to the same operation, so their values go
// with the library files.
func (a *app) snapshotUndoLibrary() (undoState, error) {
	s, err := a.snapshotUndoState()
	if err != nil {
		return undoState{}, err
	}
	s.library, s.vars = true, true
	return s, nil
}

func (a *app) snapshotHostsUndo() (hostsUndoState, error) {
	set, err := cloneLibraryValue(a.profiles.set)
	if err != nil {
		return hostsUndoState{}, err
	}
	s := hostsUndoState{set: set, files: map[string][]byte{}}
	for _, p := range set.Profiles {
		path := a.profiles.path(p.Name)
		data, err := os.ReadFile(path)
		if err != nil && !errors.Is(err, os.ErrNotExist) {
			return hostsUndoState{}, err
		}
		s.files[path] = data
		if data == nil {
			s.files[path] = []byte(p.Text)
		}
	}
	return s, nil
}

func (a *app) snapshotCaptureUndo() undoState {
	chosen := make([]int, 0, a.chosen.Len())
	for id := range a.chosen.All() {
		chosen = append(chosen, id)
	}
	return undoState{capture: &captureUndoState{
		flows: slices.Clone(a.flows), pickedID: a.pickedID,
		chosen: chosen, detailFor: a.detailFor,
	}}
}

func (a *app) snapshotDraftsUndo() undoState {
	return undoState{drafts: &draftUndoState{
		documents: slices.Clone(a.documents), active: a.requestDocument,
	}}
}

func varsEnvFile(active string, vars variableStateCopy) envFile {
	return envFile{Active: active, Envs: vars.envs, Globals: vars.globals,
		GlobalsCurrent: vars.globalsCurrent, FolderCurrent: vars.folderCurrent,
		FolderCredentials: vars.folderCredentials}
}

func (s undoState) varsEnvFile(active string) envFile {
	return varsEnvFile(active, s.data)
}

// apply writes every part of the snapshot through the path that owns it and
// adopts each part in memory only after that write succeeded. Only the assets
// that differ from the counterpart are overlaid: an unrelated edit saved
// after the operation survives its undo.
func (s undoState) apply(a *app, counterpart *undoState) error {
	other := undoState{}
	if counterpart != nil {
		other = *counterpart
	}
	if s.hosts != nil {
		if err := s.hosts.apply(a); err != nil {
			return err
		}
	}
	if s.library || s.vars {
		if s.library {
			requests := overlayRequests(a.requests, s.data.requests, other.data.requests)
			folders := overlayFolders(a.folders, s.data.folders, other.data.folders)
			if err := a.writeLibrary(requests, folders); err != nil {
				return err
			}
			order := cloneFolderOrder(a.library.folderOrder)
			for parent, names := range s.folderOrder {
				if !reflect.DeepEqual(names, other.folderOrder[parent]) {
					order[parent] = slices.Clone(names)
				}
			}
			a.library.folderOrder = order
			a.refreshDocuments()
		}
		if s.vars {
			vars := s.data
			vars.requests, vars.folders = a.requests, a.folders
			vars.envs = overlayEnvs(a.envs, s.data.envs, other.data.envs)
			vars.globals = overlayValues(a.globals, s.data.globals, other.data.globals)
			vars.globalsCurrent = overlayValues(a.globalsCurrent, s.data.globalsCurrent, other.data.globalsCurrent)
			vars.folderCurrent = overlayMaps(a.folderCurrent, s.data.folderCurrent, other.data.folderCurrent)
			vars.folderCredentials = overlayMarks(a.folderCredentials, s.data.folderCredentials, other.data.folderCredentials)
			env := varsEnvFile(a.activeEnv, vars)
			if err := a.saveEnvFile(env); err != nil {
				return err
			}
			a.envs, a.globals, a.globalsCurrent = vars.envs, vars.globals, vars.globalsCurrent
			a.folderCurrent, a.folderCredentials = vars.folderCurrent, vars.folderCredentials
		}
	}
	if s.rules {
		var ruleErr error
		a.withoutUndo(func() { ruleErr = a.writeRules(s.ruleList) })
		if ruleErr != nil {
			return ruleErr
		}
		a.rules = slices.Clone(s.ruleList)
		if a.srv != nil {
			_ = a.srv.SetRules(a.rules)
		}
	}
	if s.capture != nil {
		c := s.capture
		a.flows = slices.Clone(c.flows)
		a.recount()
		a.chosen.Clear()
		for _, id := range c.chosen {
			if id >= 0 {
				a.chosen.Add(id)
			}
		}
		a.pickedID, a.detailFor = c.pickedID, c.detailFor
		if a.pickedID > 0 {
			a.picked = 0
		}
		a.applyFilter()
		a.choosePicked()
	}
	if s.drafts != nil {
		a.documents = slices.Clone(s.drafts.documents)
		if active := s.drafts.active; active != nil {
			a.openRequestTab(active, tabOpen{})
		}
	}
	a.invalidate()
	return nil
}

func (s hostsUndoState) apply(a *app) error {
	h := &a.profiles
	index := filepath.Join(h.dir, hosts.IndexFile)
	data, err := s.set.MarshalIndex()
	if err != nil {
		return err
	}
	if err := a.file(index).Write(data); err != nil {
		return err
	}
	for path, text := range s.files {
		if path == index {
			continue
		}
		if err := a.file(path).Write(text); err != nil {
			return err
		}
	}
	keep := make(map[string]bool, len(s.set.Profiles))
	for _, p := range s.set.Profiles {
		keep[h.path(p.Name)] = true
	}
	for _, p := range h.set.Profiles {
		path := h.path(p.Name)
		if keep[path] || path == index {
			continue
		}
		if err := os.Remove(path); err != nil && !errors.Is(err, os.ErrNotExist) {
			return err
		}
		delete(a.files, path)
	}
	set, err := cloneLibraryValue(s.set)
	if err != nil {
		return err
	}
	h.set, h.rev = set, h.rev+1
	for name := range h.editors {
		if !slices.ContainsFunc(set.Profiles, func(p hosts.Profile) bool { return p.Name == name }) {
			delete(h.editors, name)
		}
	}
	h.choice = min(h.choice, max(len(set.Profiles)-1, 0))
	a.applyHosts()
	return nil
}

// diffUndoResources lists the files whose bytes differ between the two
// snapshots. One operation touching several files pushes one element per
// file, in one group, so all of them are checked and undone together.
func (a *app) diffUndoResources(before, after undoState) []undoResource {
	var out []undoResource
	add := func(path string) {
		if path != "" {
			out = append(out, a.undoFileResource(path))
		}
	}
	if before.library && after.library {
		old, err := undoLibraryFiles(before.data, before.folderOrder)
		if err != nil {
			return nil
		}
		next, err := undoLibraryFiles(after.data, after.folderOrder)
		if err != nil {
			return nil
		}
		for name, data := range next {
			if !bytes.Equal(old[name], data) {
				add(filepath.Join(a.library.root, filepath.FromSlash(name)))
			}
		}
		for name := range old {
			if _, ok := next[name]; !ok {
				add(filepath.Join(a.library.root, filepath.FromSlash(name)))
			}
		}
	}
	if before.vars && after.vars {
		old, oldErr := json.Marshal(before.varsEnvFile(a.activeEnv))
		next, nextErr := json.Marshal(after.varsEnvFile(a.activeEnv))
		if oldErr == nil && nextErr == nil && !bytes.Equal(old, next) {
			add(a.envsPath)
			add(a.projectFile("environments.json"))
		}
	}
	if before.rules && after.rules && !reflect.DeepEqual(before.ruleList, after.ruleList) {
		add(a.rulesPath)
	}
	if before.hosts != nil && after.hosts != nil {
		add(filepath.Join(a.profiles.dir, hosts.IndexFile))
		for path := range before.hosts.files {
			add(path)
		}
		for path := range after.hosts.files {
			add(path)
		}
	}
	if before.capture != nil || after.capture != nil {
		out = append(out, undoResource{id: undoCaptureResource})
	}
	if before.drafts != nil || after.drafts != nil {
		out = append(out, undoResource{id: undoDraftsResource})
	}
	return dedupUndoResources(out)
}

func undoLibraryFiles(s variableStateCopy, order map[string][]string) (map[string][]byte, error) {
	return libraryFiles(s.requests, s.folders, order)
}

func (a *app) undoFileResource(path string) undoResource {
	return undoResource{id: "file:" + filepath.Clean(path), path: filepath.Clean(path)}
}

func dedupUndoResources(in []undoResource) []undoResource {
	seen := map[string]bool{}
	out := make([]undoResource, 0, len(in))
	for _, r := range in {
		if !seen[r.id] {
			seen[r.id] = true
			out = append(out, r)
		}
	}
	return out
}

// undoFileError reports a file that no longer holds the bytes the element
// expects: it was edited elsewhere, replaced with another format version, or
// deleted. The service checks every file in a group before undoing any of it.
func undoFileError(path string, err error) error {
	if err == nil {
		err = errors.New("missing on disk")
	}
	return fmt.Errorf("%w: %s: %v", store.ErrChanged, path, err)
}

func (a *app) undoResourceBytes(r undoResource) ([]byte, error) {
	if r.path == "" {
		return nil, nil
	}
	return os.ReadFile(r.path)
}

func cloneFolderOrder(order map[string][]string) map[string][]string {
	if order == nil {
		return nil
	}
	out := make(map[string][]string, len(order))
	for parent, names := range order {
		out[parent] = slices.Clone(names)
	}
	return out
}

// overlayRequests restores only the requests whose snapshot differs from the
// other side of the operation. Requests saved after it keep their new values.
func overlayRequests(current, wanted, other []savedRequest) []savedRequest {
	want := make(map[string]savedRequest, len(wanted))
	had := make(map[string]savedRequest, len(other))
	for _, r := range wanted {
		want[r.key()] = r
	}
	for _, r := range other {
		had[r.key()] = r
	}
	out := make([]savedRequest, 0, len(current)+len(want))
	for _, r := range current {
		key := r.key()
		if w, ok := want[key]; ok {
			if prev, exists := had[key]; exists && reflect.DeepEqual(prev, w) {
				out = append(out, r)
			} else {
				out = append(out, w)
			}
			continue
		}
		if _, removed := had[key]; removed {
			continue
		}
		out = append(out, r)
	}
	for key, r := range want {
		if !slices.ContainsFunc(out, func(x savedRequest) bool { return x.key() == key }) {
			out = append(out, r)
		}
	}
	return out
}

func overlayFolders(current, wanted, other []request.Folder) []request.Folder {
	want := make(map[string]request.Folder, len(wanted))
	had := make(map[string]request.Folder, len(other))
	for _, f := range wanted {
		want[f.Path] = f
	}
	for _, f := range other {
		had[f.Path] = f
	}
	out := make([]request.Folder, 0, len(current)+len(want))
	for _, f := range current {
		if w, ok := want[f.Path]; ok {
			if prev, exists := had[f.Path]; exists && reflect.DeepEqual(prev, w) {
				out = append(out, f)
			} else {
				out = append(out, w)
			}
			continue
		}
		if _, removed := had[f.Path]; removed {
			continue
		}
		out = append(out, f)
	}
	for path, f := range want {
		if !slices.ContainsFunc(out, func(x request.Folder) bool { return x.Path == path }) {
			out = append(out, f)
		}
	}
	return out
}

func overlayEnvs(current, wanted, other []environment) []environment {
	want := make(map[string]environment, len(wanted))
	had := make(map[string]environment, len(other))
	for _, e := range wanted {
		want[e.Name] = e
	}
	for _, e := range other {
		had[e.Name] = e
	}
	out := make([]environment, 0, len(current)+len(want))
	for _, e := range current {
		if w, ok := want[e.Name]; ok {
			if prev, exists := had[e.Name]; exists && reflect.DeepEqual(prev, w) {
				out = append(out, e)
			} else {
				out = append(out, w)
			}
			continue
		}
		if _, removed := had[e.Name]; removed {
			continue
		}
		out = append(out, e)
	}
	for name, e := range want {
		if !slices.ContainsFunc(out, func(x environment) bool { return x.Name == name }) {
			out = append(out, e)
		}
	}
	return out
}

func overlayValues(current, wanted, other map[string]string) map[string]string {
	out := maps.Clone(current)
	if out == nil {
		out = map[string]string{}
	}
	for name, value := range wanted {
		if prev, ok := other[name]; !ok || prev != value {
			out[name] = value
		}
	}
	for name := range out {
		if _, ok := wanted[name]; ok {
			continue
		}
		if _, removed := other[name]; removed {
			delete(out, name)
		}
	}
	return out
}

func overlayMaps(current, wanted, other map[string]map[string]string) map[string]map[string]string {
	out := make(map[string]map[string]string, len(current)+len(wanted))
	for path, values := range current {
		out[path] = maps.Clone(values)
	}
	for path, values := range wanted {
		if prev, ok := other[path]; ok && reflect.DeepEqual(prev, values) {
			continue
		}
		out[path] = maps.Clone(values)
	}
	for path := range out {
		if _, ok := wanted[path]; ok {
			continue
		}
		if _, removed := other[path]; removed {
			delete(out, path)
		}
	}
	return out
}

func overlayMarks(current, wanted, other map[string]map[string]bool) map[string]map[string]bool {
	out := make(map[string]map[string]bool, len(current)+len(wanted))
	for path, marks := range current {
		out[path] = maps.Clone(marks)
	}
	for path, marks := range wanted {
		if prev, ok := other[path]; ok && maps.Equal(prev, marks) {
			continue
		}
		out[path] = maps.Clone(marks)
	}
	for path := range out {
		if _, ok := wanted[path]; ok {
			continue
		}
		if _, removed := other[path]; removed {
			delete(out, path)
		}
	}
	return out
}
