package capture

import (
	"testing"

	"github.com/google/uuid"
)

// The flow lookup clears once it grows past its cap; only the flow added in
// that insert remains addressable for later frames.
func TestFlowIDCacheClearsPastLimit(t *testing.T) {
	s, err := New("127.0.0.1:0", t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	var first uuid.UUID
	for i := 1; i <= flowIDsLimit+1; i++ {
		id := uuid.New()
		if i == 1 {
			first = id
		}
		s.cacheFlowID(id, i)
	}
	if got := s.flowIDs.Size(); got != flowIDsLimit+1 {
		t.Fatalf("cache size = %d, want %d", got, flowIDsLimit+1)
	}

	latest := uuid.New()
	s.cacheFlowID(latest, 9001)
	if got := s.flowIDs.Size(); got != 1 {
		t.Fatalf("cache size after the cap = %d, want 1", got)
	}
	if _, ok := s.flowIDs.Get(first); ok {
		t.Error("a flow from the full cache survived")
	}
	if got, ok := s.flowIDs.Get(latest); !ok || got != 9001 {
		t.Errorf("new flow = %d, %v; want 9001, true", got, ok)
	}
}
