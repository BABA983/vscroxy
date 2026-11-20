// Package undo is the in-memory undo/redo service. Its model follows VS
// Code's undoRedoService: an element names the resource (or several, for a
// workspace element) it changes, carries a presentable label and a stable
// code, and knows how to undo and redo itself. Elements pushed with one
// group are undone and redone together. Nothing here is persisted.
package undo

import (
	"cmp"
	"errors"
	"fmt"
	"maps"
	"slices"
	"sync/atomic"
)

// ElementType says whether one element changes one resource or several.
type ElementType uint8

const (
	Resource ElementType = iota
	Workspace
)

// Element is one reversible change. Verify runs before either direction and
// refuses the change when its resources no longer hold what the element
// expects, so a group can be checked before any of its members run.
type Element interface {
	Type() ElementType
	Label() string
	Code() string
	Resources() []string
	Verify() error
	Undo() error
	Redo() error
}

// Change is the ordinary element: closures own the state and the write
// paths, while the service owns only the stacks and the grouping.
type Change struct {
	kind      ElementType
	label     string
	code      string
	resources []string
	verify    func() error
	undo      func() error
	redo      func() error
}

func (c *Change) Type() ElementType   { return c.kind }
func (c *Change) Label() string       { return c.label }
func (c *Change) Code() string        { return c.code }
func (c *Change) Resources() []string { return slices.Clone(c.resources) }

func (c *Change) Verify() error {
	if c.verify == nil {
		return nil
	}
	return c.verify()
}

func (c *Change) Undo() error { return c.undo() }
func (c *Change) Redo() error { return c.redo() }

// NewResource makes an element for one resource.
func NewResource(resource, label, code string, verify, undo, redo func() error) *Change {
	return &Change{kind: Resource, label: label, code: code, resources: []string{resource},
		verify: verify, undo: undo, redo: redo}
}

// NewWorkspace makes an element for several resources. Repeated resources
// are dropped: one resource has one stack, so it must hold one element.
func NewWorkspace(resources []string, label, code string, verify, undo, redo func() error) *Change {
	return &Change{kind: Workspace, label: label, code: code,
		resources: slices.Compact(slices.Clone(resources)), verify: verify, undo: undo, redo: redo}
}

var groupIDs atomic.Uint64

// Group collects the elements of one operation. Its members are undone in
// reverse push order and redone in push order, never one at a time.
type Group struct {
	id    uint64
	order uint64
}

func NewGroup() *Group { return &Group{id: groupIDs.Add(1)} }

type entry struct {
	id      uint64
	element Element
	group   uint64
	order   uint64
}

// Handle identifies a pushed element in the undo stack, for a toast that
// undoes that particular operation rather than whatever is on top now.
type Handle struct {
	id uint64
}

type stack struct {
	past   []*entry
	future []*entry
}

// Service owns the undo and redo stacks of every resource.
type Service struct {
	stacks map[string]*stack
	nextID uint64
}

func NewService() *Service { return &Service{stacks: make(map[string]*stack)} }

// Push adds an element to the undo stack and drops the redo stack of every
// resource it touches: a new change makes the old future unreachable.
func (s *Service) Push(element Element, group *Group) *Handle {
	s.nextID++
	e := &entry{id: s.nextID, element: element}
	if group != nil {
		if group.id == 0 {
			group.id = groupIDs.Add(1)
		}
		group.order++
		e.group, e.order = group.id, group.order
	}
	for _, r := range dedup(element.Resources()) {
		st := s.stack(r)
		st.future = nil
		st.past = append(st.past, e)
	}
	return &Handle{id: e.id}
}

func dedup(resources []string) []string {
	out := make([]string, 0, len(resources))
	seen := make(map[string]bool, len(resources))
	for _, r := range resources {
		if !seen[r] {
			seen[r] = true
			out = append(out, r)
		}
	}
	return out
}

func (s *Service) stack(resource string) *stack {
	st := s.stacks[resource]
	if st == nil {
		st = &stack{}
		s.stacks[resource] = st
	}
	return st
}

func (s *Service) CanUndo() bool { return s.latest(true) != nil }
func (s *Service) CanRedo() bool { return s.latest(false) != nil }

// latest finds the newest element ready in the given direction. Entries can
// appear in several stacks; the id makes each operation count once.
func (s *Service) latest(past bool) *entry {
	var best *entry
	for _, st := range s.stacks {
		var candidates []*entry
		if past {
			candidates = st.past
		} else {
			candidates = st.future
		}
		if len(candidates) == 0 {
			continue
		}
		e := candidates[len(candidates)-1]
		if best == nil || e.id > best.id {
			best = e
		}
	}
	return best
}

// NotReadyError says a group cannot run as one step because a newer change
// now sits above one of its members in a stack it touches.
type NotReadyError struct {
	Label string
}

func (e *NotReadyError) Error() string {
	return fmt.Sprintf("cannot undo %q: a newer change touched the same resource", e.Label)
}

// find locates an entry in the active side of the stacks.
func (s *Service) find(id uint64, past bool) *entry {
	for _, st := range s.stacks {
		list := st.past
		if !past {
			list = st.future
		}
		if at := slices.IndexFunc(list, func(x *entry) bool { return x.id == id }); at >= 0 {
			return list[at]
		}
	}
	return nil
}

// topRunCoversMembers says every member of this group in a stack is part of
// the group's topmost contiguous run. A member underneath an unrelated entry
// means the group can no longer be undone as one step.
func topRunCoversMembers(list []*entry, group uint64) bool {
	total, run := 0, 0
	for _, e := range list {
		if e.group == group {
			total++
		}
	}
	for i := len(list) - 1; i >= 0 && list[i].group == group; i-- {
		run++
	}
	return total > 0 && run == total
}

// isNewest says an entry is still the newest ready operation on every stack
// it belongs to. A group counts as one operation, and all its members in a
// stack must sit in one run at the top.
func (s *Service) isNewest(e *entry, past bool) bool {
	for _, r := range dedup(e.element.Resources()) {
		st := s.stacks[r]
		if st == nil {
			continue
		}
		list := st.past
		if !past {
			list = st.future
		}
		if len(list) == 0 {
			return false
		}
		top := list[len(list)-1]
		if e.group == 0 {
			if top != e {
				return false
			}
		} else if !topRunCoversMembers(list, e.group) {
			return false
		}
	}
	return true
}

// HasPast says whether the toast's operation is still at the front of every
// stack it touches. A newer change on the same resource makes the old toast
// stale, as does undoing the operation itself.
func (s *Service) HasPast(h *Handle) bool {
	if h == nil {
		return false
	}
	e := s.find(h.id, true)
	return e != nil && s.isNewest(e, true)
}

// Undo undoes the newest operation.
func (s *Service) Undo() error {
	e := s.latest(true)
	if e == nil {
		return nil
	}
	return s.run(e, true)
}

// UndoHandle undoes the operation a toast points at, with its whole group.
// A stale toast does nothing.
func (s *Service) UndoHandle(h *Handle) error {
	if h == nil {
		return nil
	}
	if !s.HasPast(h) {
		return nil
	}
	return s.run(s.find(h.id, true), true)
}

// Redo redoes the newest undone operation.
func (s *Service) Redo() error {
	e := s.latest(false)
	if e == nil {
		return nil
	}
	return s.run(e, false)
}

// VerificationError says that an element's resources changed on disk since
// it was pushed. The app turns it into a notice; no member of the group ran.
type VerificationError struct {
	Label string
	Err   error
}

func (e *VerificationError) Error() string {
	return fmt.Sprintf("cannot undo or redo %q across all files: %v", e.Label, e.Err)
}

func (e *VerificationError) Unwrap() error { return e.Err }

func (s *Service) run(top *entry, past bool) error {
	members, err := s.group(top, past)
	if err != nil {
		return err
	}
	// Verify every resource in the group before a single write. One changed
	// file refuses the whole operation, leaving all files untouched.
	for _, e := range members {
		if err := e.element.Verify(); err != nil {
			return &VerificationError{Label: e.element.Label(), Err: err}
		}
	}
	// Undo newest first, redo oldest first: same-resource members of one
	// group must unwind in the opposite order from their push.
	if past {
		slices.Reverse(members)
	}
	var done []*entry
	for _, e := range members {
		var err error
		if past {
			err = e.element.Undo()
		} else {
			err = e.element.Redo()
		}
		if err != nil {
			verb := "redo"
			if past {
				verb = "undo"
			}
			failure := fmt.Errorf("could not %s %q: %w", verb, e.element.Label(), err)
			if rollbackErr := s.rollback(done, past); rollbackErr != nil {
				return errors.Join(failure, rollbackErr)
			}
			return failure
		}
		done = append(done, e)
	}
	for _, e := range members {
		for _, r := range dedup(e.element.Resources()) {
			st := s.stacks[r]
			if past {
				st.past = remove(st.past, e)
				st.future = append(st.future, e)
			} else {
				st.future = remove(st.future, e)
				st.past = append(st.past, e)
			}
		}
	}
	return nil
}

func remove(entries []*entry, want *entry) []*entry {
	return slices.DeleteFunc(entries, func(e *entry) bool { return e == want })
}

// rollback puts back the members that already ran before a later write
// failed. The stacks were not moved, so only the elements need to be wound
// back; their Verify closures see the restored state afterwards.
func (s *Service) rollback(done []*entry, past bool) error {
	var errs []error
	for i := len(done) - 1; i >= 0; i-- {
		var err error
		if past {
			err = done[i].element.Redo()
		} else {
			err = done[i].element.Undo()
		}
		if err != nil {
			errs = append(errs, fmt.Errorf("could not roll back %q: %w", done[i].element.Label(), err))
		}
	}
	return errors.Join(errs...)
}

// group returns every member of an operation ready in the given direction.
// All members are collected across all stacks, including two members of one
// group on the same resource. If a newer, non-member entry now tops any stack
// the group touches, the whole group is refused instead of half-undoing it.
func (s *Service) group(top *entry, past bool) ([]*entry, error) {
	if top.group == 0 {
		return []*entry{top}, nil
	}
	seen := map[uint64]*entry{}
	for _, st := range s.stacks {
		list := st.past
		if !past {
			list = st.future
		}
		var members []*entry
		for _, e := range list {
			if e.group == top.group {
				members = append(members, e)
				seen[e.id] = e
			}
		}
		if len(members) == 0 {
			continue
		}
		if !topRunCoversMembers(list, top.group) {
			return nil, &NotReadyError{Label: top.element.Label()}
		}
	}
	if len(seen) == 0 {
		seen[top.id] = top
	}
	out := slices.Collect(maps.Values(seen))
	slices.SortFunc(out, func(x, y *entry) int {
		if x.order != y.order {
			return cmp.Compare(x.order, y.order)
		}
		return cmp.Compare(x.id, y.id)
	})
	return out, nil
}

// Clear drops every undo and redo element of the named resources. Replacing a
// capture session, for example, leaves old capture Undos with no target.
func (s *Service) Clear(resources ...string) {
	if len(resources) == 0 {
		s.stacks = make(map[string]*stack)
		return
	}
	for _, r := range resources {
		delete(s.stacks, r)
	}
}

// Past and Future expose the service's elements for menus and tests.
func (s *Service) Past() []Element   { return s.elements(true) }
func (s *Service) Future() []Element { return s.elements(false) }

func (s *Service) elements(past bool) []Element {
	seen := map[uint64]bool{}
	var out []Element
	for _, st := range s.stacks {
		entries := st.past
		if !past {
			entries = st.future
		}
		for _, e := range entries {
			if !seen[e.id] {
				seen[e.id] = true
				out = append(out, e.element)
			}
		}
	}
	return out
}

// Resources lists the resource keys currently holding elements.
func (s *Service) Resources() []string {
	return slices.Sorted(maps.Keys(s.stacks))
}
