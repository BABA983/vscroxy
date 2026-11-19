package markers_test

import (
	"testing"

	"github.com/baba983/vscroxy/internal/markers"
)

func TestMultiLineRangeKeepsItsEndColumn(t *testing.T) {
	s := markers.New()
	t.Cleanup(s.Dispose)
	s.ChangeOne("scripts", "r", []markers.Marker{{
		Severity: markers.Error, Message: "multi-line",
		Range: &markers.Range{StartLineNumber: 2, StartColumn: 10, EndLineNumber: 3, EndColumn: 2},
	}})
	got := s.Read(markers.Filter{Resource: "r"})
	if len(got) != 1 {
		t.Fatalf("read = %+v", got)
	}
	r := got[0].Range
	if r == nil || r.StartLineNumber != 2 || r.StartColumn != 10 || r.EndLineNumber != 3 || r.EndColumn != 2 {
		t.Fatalf("multi-line range was rewritten: %+v", r)
	}
}
