package markers_test

import (
	"cmp"
	"slices"
	"testing"

	"github.com/baba983/vscroxy/internal/lifecycle"
	"github.com/baba983/vscroxy/internal/markers"
)

func TestChangeOneReplacesOnlyItsOwner(t *testing.T) {
	s := markers.New()
	t.Cleanup(s.Dispose)
	const resource = "vscroxy:request/one"
	s.ChangeOne("assertions", resource, []markers.Marker{
		{Severity: markers.Error, Message: "status 200 failed", Code: "tests"},
		{Severity: markers.Warning, Message: "slow", Code: "url"},
	})
	s.ChangeOne("variables", resource, []markers.Marker{
		{Severity: markers.Warning, Message: "unresolved {{token}}", Code: "headers"},
	})
	s.ChangeOne("assertions", resource, []markers.Marker{
		{Severity: markers.Error, Message: "body shape failed", Code: "tests"},
	})
	s.ChangeOne("assertions", "vscroxy:request/two", []markers.Marker{
		{Severity: markers.Info, Message: "other request", Code: "url"},
	})

	got := s.Read(markers.Filter{Resource: resource})
	// Read returns owners in map order; the panel sorts before showing them.
	slices.SortFunc(got, func(a, b markers.Marker) int { return cmp.Compare(a.Message, b.Message) })
	want := []string{"body shape failed:assertions", "unresolved {{token}}:variables"}
	if len(got) != len(want) {
		t.Fatalf("read(%s) = %d markers, want %d: %+v", resource, len(got), len(want), got)
	}
	for i, marker := range got {
		if marker.Message+":"+marker.Owner != want[i] {
			t.Errorf("marker %d = %q/%q, want %q", i, marker.Message, marker.Owner, want[i])
		}
		if marker.Resource != resource {
			t.Errorf("marker %d resource = %q", i, marker.Resource)
		}
	}
	if n := len(s.Read(markers.Filter{Owner: "assertions"})); n != 2 {
		t.Errorf("assertions have %d markers, want 2", n)
	}
}

func TestChangeOneEventHasExactlyChangedResources(t *testing.T) {
	s := markers.New()
	t.Cleanup(s.Dispose)
	var resources [][]string
	subscription := s.OnMarkerChanged(func(changed []string) {
		resources = append(resources, slices.Clone(changed))
	})
	store := lifecycle.NewDisposableStore()
	store.Add(subscription)
	s.ChangeOne("variables", "a", []markers.Marker{{Severity: markers.Warning, Message: "one"}})
	s.ChangeOne("variables", "a", []markers.Marker{{Severity: markers.Warning, Message: "two"}})
	s.ChangeOne("assertions", "a", []markers.Marker{{Severity: markers.Error, Message: "three"}})
	s.ChangeOne("variables", "a", nil) // other owner still has a marker
	s.ChangeOne("variables", "a", nil) // removing nothing is silent
	s.ChangeOne("assertions", "b", []markers.Marker{{Severity: markers.Hint, Message: "four"}})
	want := [][]string{{"a"}, {"a"}, {"a"}, {"a"}, {"b"}}
	if !slices.EqualFunc(resources, want, slices.Equal) {
		t.Fatalf("changed resources = %v, want %v", resources, want)
	}
	got := s.Read(markers.Filter{})
	if len(got) != 2 {
		t.Fatalf("final markers = %+v", got)
	}
	messages := []string{got[0].Message, got[1].Message}
	slices.Sort(messages)
	if !slices.Equal(messages, []string{"four", "three"}) {
		t.Fatalf("final markers = %+v", got)
	}
	store.Dispose()
	s.ChangeOne("assertions", "c", []markers.Marker{{Severity: markers.Error, Message: "not seen"}})
	if len(resources) != len(want) {
		t.Fatal("disposed listener still received an event")
	}
}

func TestReadFilterAndRangeNormalization(t *testing.T) {
	s := markers.New()
	t.Cleanup(s.Dispose)
	s.ChangeOne("request.scripts", "r", []markers.Marker{
		{Message: "", Severity: markers.Error}, // empty messages are dropped
		{Message: "hint", Severity: markers.Hint, Range: &markers.Range{}},
		{Message: "error", Severity: markers.Error, Range: &markers.Range{StartLineNumber: 3, StartColumn: 7, EndLineNumber: 2, EndColumn: 0}},
		{Message: "warning", Severity: markers.Warning},
	})
	all := s.Read(markers.Filter{Resource: "r"})
	if len(all) != 3 {
		t.Fatalf("read = %+v, want 3 non-empty markers", all)
	}
	if r := all[0].Range; r == nil || r.StartLineNumber != 1 || r.StartColumn != 1 || r.EndLineNumber != 1 || r.EndColumn != 1 {
		t.Errorf("empty range was not normalized: %+v", r)
	}
	if r := all[1].Range; r == nil || r.StartLineNumber != 3 || r.StartColumn != 7 || r.EndLineNumber != 3 || r.EndColumn != 7 {
		t.Errorf("range was not normalized: %+v", r)
	}
	severe := s.Read(markers.Filter{Severities: markers.Error | markers.Warning})
	if len(severe) != 2 {
		t.Errorf("severity mask = %+v, want error and warning", severe)
	}
	if got := s.Read(markers.Filter{Take: 1}); len(got) != 1 {
		t.Errorf("take = %d markers, want 1", len(got))
	}
	if got := markers.Signature(all); got == "" {
		t.Fatal("signature is empty")
	}
}
