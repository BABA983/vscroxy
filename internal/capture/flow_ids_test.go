package capture

import (
	"testing"

	"github.com/google/uuid"
	"github.com/lqqyt2423/go-mitmproxy/proxy"
)

// A stream that keeps producing frames stays addressable while many newer
// flows arrive. Clearing the whole cache at the cap would lose its ID.
func TestFlowIDCacheKeepsActiveStream(t *testing.T) {
	s, err := New("127.0.0.1:0", t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = s.Close() })
	active := uuid.New()
	s.flowIDs.Set(active, 7)
	f := &proxy.Flow{Id: active}
	in := frameInput{Direction: "in", Kind: "text", Data: []byte("ping")}
	drain := func() {
		for {
			select {
			case <-s.frames:
			default:
				return
			}
		}
	}

	for i := range flowIDsLimit + 1 {
		s.flowIDs.Set(uuid.New(), 100+i)
		if i%97 == 0 {
			s.emitFrame(f, in)
			drain()
		}
	}

	s.emitFrame(f, in)
	select {
	case event := <-s.frames:
		if event.FlowID != 7 {
			t.Fatalf("frame attached to flow %d, want 7", event.FlowID)
		}
		if event.Frame.Seq == 0 {
			t.Error("frame was published without a sequence number")
		}
	default:
		t.Fatal("the active stream's frame was not attached")
	}
	if got := s.flowIDs.Size(); got != flowIDsLimit {
		t.Fatalf("cache size = %d, want %d", got, flowIDsLimit)
	}
	if got, ok := s.flowIDs.Get(active); !ok || got != 7 {
		t.Errorf("active flow = %d, %v; want 7, true", got, ok)
	}
}
