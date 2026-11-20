package undo

import (
	"errors"
	"reflect"
	"slices"
	"testing"
)

// fakeElement records calls in the order the service makes them.
type fakeElement struct {
	name      string
	resources []string
	events    *[]string
	verifyErr error
	undoErr   error
	redoErr   error
}

func (e *fakeElement) Type() ElementType {
	if len(e.resources) > 1 {
		return Workspace
	}
	return Resource
}

func (e *fakeElement) Label() string       { return e.name }
func (e *fakeElement) Code() string        { return "test." + e.name }
func (e *fakeElement) Resources() []string { return slices.Clone(e.resources) }

func (e *fakeElement) Verify() error {
	*e.events = append(*e.events, "verify:"+e.name)
	return e.verifyErr
}

func (e *fakeElement) Undo() error {
	*e.events = append(*e.events, "undo:"+e.name)
	return e.undoErr
}

func (e *fakeElement) Redo() error {
	*e.events = append(*e.events, "redo:"+e.name)
	return e.redoErr
}

func TestGroupUndoIsAtomicWithSameResourceMembers(t *testing.T) {
	s, events := NewService(), &[]string{}
	g := NewGroup()
	first := &fakeElement{name: "one", resources: []string{"a"}, events: events}
	second := &fakeElement{name: "two", resources: []string{"a"}, events: events}
	s.Push(first, g)
	s.Push(second, g)

	if err := s.Undo(); err != nil {
		t.Fatal(err)
	}
	if want := []string{"verify:one", "verify:two", "undo:two", "undo:one"}; !reflect.DeepEqual(*events, want) {
		t.Fatalf("group undo = %v, want %v", *events, want)
	}
	if s.CanUndo() {
		t.Fatal("one member of the group was left on the undo stack")
	}
	if !s.CanRedo() {
		t.Fatal("the group has no redo")
	}
	*events = nil
	if err := s.Redo(); err != nil {
		t.Fatal(err)
	}
	if want := []string{"verify:one", "verify:two", "redo:one", "redo:two"}; !reflect.DeepEqual(*events, want) {
		t.Fatalf("group redo = %v, want %v", *events, want)
	}
}

func TestUndoIsNewestFirstAcrossResources(t *testing.T) {
	s, events := NewService(), &[]string{}
	a := &fakeElement{name: "a", resources: []string{"a"}, events: events}
	b := &fakeElement{name: "b", resources: []string{"b"}, events: events}
	c := &fakeElement{name: "c", resources: []string{"a"}, events: events}
	s.Push(a, nil)
	s.Push(b, nil)
	s.Push(c, nil)
	for _, name := range []string{"c", "b", "a"} {
		*events = nil
		if err := s.Undo(); err != nil {
			t.Fatal(err)
		}
		want := []string{"verify:" + name, "undo:" + name}
		if !reflect.DeepEqual(*events, want) {
			t.Fatalf("undo = %v, want %v", *events, want)
		}
	}
}

func TestUndoHandleRefusesStaleEntry(t *testing.T) {
	s, events := NewService(), &[]string{}
	old := &fakeElement{name: "old", resources: []string{"a"}, events: events}
	newer := &fakeElement{name: "new", resources: []string{"a"}, events: events}
	oldHandle := s.Push(old, nil)
	s.Push(newer, nil)

	if s.HasPast(oldHandle) {
		t.Fatal("a toast stayed valid after a newer change on the same resource")
	}
	if err := s.UndoHandle(oldHandle); err != nil {
		t.Fatal(err)
	}
	if len(*events) != 0 {
		t.Fatalf("stale toast ran an undo: %v", *events)
	}
	newHandle := s.Push(&fakeElement{name: "third", resources: []string{"a"}, events: events}, nil)
	if err := s.UndoHandle(newHandle); err != nil {
		t.Fatal(err)
	}
	if !slices.Contains(*events, "undo:third") {
		t.Fatalf("fresh toast did not undo: %v", *events)
	}
}

func TestInterleavedGroupIsRefused(t *testing.T) {
	s, events := NewService(), &[]string{}
	g := NewGroup()
	first := &fakeElement{name: "g1", resources: []string{"a"}, events: events}
	between := &fakeElement{name: "x", resources: []string{"a"}, events: events}
	last := &fakeElement{name: "g2", resources: []string{"a"}, events: events}
	h1 := s.Push(first, g)
	s.Push(between, nil)
	h2 := s.Push(last, g)

	if s.HasPast(h1) || s.HasPast(h2) {
		t.Fatal("a group with an interleaved change stayed ready for its toasts")
	}
	if err := s.Undo(); err == nil {
		t.Fatal("an interleaved group was undone as one step")
	} else {
		var notReady *NotReadyError
		if !errors.As(err, &notReady) {
			t.Fatalf("refusal = %T, want *NotReadyError", err)
		}
	}
	if len(*events) != 0 {
		t.Fatalf("refused group ran a write: %v", *events)
	}
	if !s.CanUndo() {
		t.Fatal("refused group changed the stacks")
	}
}

func TestVerifyRefusesWholeGroupBeforeAnyWrite(t *testing.T) {
	s, events := NewService(), &[]string{}
	g := NewGroup()
	clean := &fakeElement{name: "clean", resources: []string{"a"}, events: events}
	stale := &fakeElement{name: "stale", resources: []string{"b"}, events: events,
		verifyErr: errors.New("changed on disk")}
	s.Push(clean, g)
	s.Push(stale, g)

	if err := s.Undo(); err == nil {
		t.Fatal("a stale group member did not refuse the group")
	} else {
		var refused *VerificationError
		if !errors.As(err, &refused) {
			t.Fatalf("refusal = %T, want *VerificationError", err)
		}
	}
	if slices.ContainsFunc(*events, func(event string) bool { return len(event) > 5 && event[:5] == "undo:" }) {
		t.Fatalf("refused group still ran a write: %v", *events)
	}
	if !s.CanUndo() || s.CanRedo() {
		t.Fatal("refused group changed the stacks")
	}
}

func TestPushClearsRedo(t *testing.T) {
	s, events := NewService(), &[]string{}
	s.Push(&fakeElement{name: "old", resources: []string{"a"}, events: events}, nil)
	if err := s.Undo(); err != nil {
		t.Fatal(err)
	}
	if !s.CanRedo() {
		t.Fatal("undo left no redo")
	}
	s.Push(&fakeElement{name: "new", resources: []string{"a"}, events: events}, nil)
	if s.CanRedo() {
		t.Fatal("a new edit kept the old redo")
	}
}

func TestFailedGroupMemberRollsBackEarlierWrites(t *testing.T) {
	s, events := NewService(), &[]string{}
	g := NewGroup()
	// Group undo runs newest first: "two" succeeds, then "one" fails.
	first := &fakeElement{name: "one", resources: []string{"a"}, events: events,
		undoErr: errors.New("disk full")}
	second := &fakeElement{name: "two", resources: []string{"b"}, events: events}
	s.Push(first, g)
	s.Push(second, g)

	if err := s.Undo(); err == nil {
		t.Fatal("the failing write was not reported")
	}
	want := []string{
		"verify:one", "verify:two",
		"undo:two", "undo:one", "redo:two",
	}
	if !reflect.DeepEqual(*events, want) {
		t.Fatalf("failed group = %v, want %v", *events, want)
	}
	if !s.CanUndo() || s.CanRedo() {
		t.Fatal("a failed group changed the stacks")
	}
}
